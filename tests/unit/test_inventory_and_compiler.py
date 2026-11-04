"""Unit tests for native Ansible INI/YAML parser, TODO-016 dedup, 3-level resolver, and Goss smart-merge."""

from __future__ import annotations

import pytest

from readyspec.compiler.goss_compiler import (
    CompilationConflictError,
    compile_host_artifacts,
)
from readyspec.compiler.resolver import (
    ParameterResolutionError,
    build_readonly_shell_env_prefix,
    resolve_instance_params_for_host,
)
from readyspec.constants import PlanState
from readyspec.domain.models import (
    CheckInstance,
    ParameterOverrideRule,
    PlanDocument,
    RoleSpec,
    RunBinding,
)
from readyspec.inventory.parser import (
    InventoryParseError,
    parse_ansible_ini,
    parse_ansible_yaml,
    parse_quick_ip_list,
)
from readyspec.registry.builtin_catalog import get_builtin_templates


def test_inventory_parsers_and_todo016_duplicate_endpoint_rejection() -> None:
    ini_text = """
[all:vars]
ansible_user=deploy

[web]
node-a ansible_host=10.0.1.10 ansible_port=2222
node-b ansible_host=10.0.1.11

[db]
node-a ansible_host=10.0.1.10 ansible_port=2222

[cluster:children]
web
db
"""
    snap = parse_ansible_ini(ini_text)
    assert len(snap.hosts) == 2
    node_a = next(h for h in snap.hosts if h.name == "node-a")
    assert node_a.port == 2222
    assert node_a.ssh_user == "deploy"
    assert set(node_a.groups) >= {"web", "db", "cluster"}

    # Different hostnames pointing to same (host, port, jump) MUST fail (TODO-016)
    dup_ini = """
[web]
web-alias ansible_host=10.0.1.10
[db]
db-alias ansible_host=10.0.1.10
"""
    with pytest.raises(InventoryParseError) as exc_info:
        parse_ansible_ini(dup_ini)
    assert exc_info.value.code == "INVENTORY_DUPLICATE_ENDPOINT"

    yaml_inv = """
all:
  vars:
    ansible_user: ops
  children:
    web:
      hosts:
        w1:
          ansible_host: 10.1.0.1
    db:
      hosts:
        d1:
          ansible_host: 10.1.0.2
"""
    ysnap = parse_ansible_yaml(yaml_inv)
    assert [h.name for h in ysnap.hosts] == ["w1", "d1"]

    qsnap = parse_quick_ip_list("10.2.0.1, root@10.2.0.2:2200")
    assert len(qsnap.hosts) == 2
    assert qsnap.hosts[1].ssh_user == "root"
    assert qsnap.hosts[1].port == 2200


def test_three_level_parameter_resolution_and_conflicts() -> None:
    builtins = get_builtin_templates()
    s04 = builtins["S04"]
    inst = CheckInstance(
        instance_id="db_space",
        template_id="S04",
        params={"min_free_gib": 100.0},
        overridable_params=["path", "min_free_gib"],
    )
    host = parse_ansible_ini("[g1]\nh1 ansible_host=10.0.0.1\n[g2]\nh1 ansible_host=10.0.0.1\n").hosts[0]

    # Single host override wins over group override, which wins over instance & template default
    resolved = resolve_instance_params_for_host(
        s04,
        inst,
        "db",
        host,
        [
            ParameterOverrideRule(
                scope_type="group",
                scope_target="g1",
                role_id="db",
                instance_id="db_space",
                param_name="path",
                value="/mnt/vol1",
            ),
            ParameterOverrideRule(
                scope_type="host",
                scope_target="h1",
                role_id="db",
                instance_id="db_space",
                param_name="min_free_gib",
                value=200.0,
            ),
        ],
    )
    assert resolved["path"].value == "/mnt/vol1"
    assert resolved["path"].source == "RUNTIME_GROUP_OVERRIDE:g1"
    assert resolved["min_free_gib"].value == 200.0
    assert resolved["min_free_gib"].source == "RUNTIME_HOST_OVERRIDE:h1"

    # Safe POSIX readonly env encoding prevents shell injection
    env_prefix = build_readonly_shell_env_prefix(resolved)
    assert "RS_PARAM_PATH=/mnt/vol1; readonly RS_PARAM_PATH; export RS_PARAM_PATH" in env_prefix
    injected = dict(resolved)
    injected["path"] = resolved["path"].model_copy(update={"value": "/mnt/vol1; rm -rf /"})
    inj_prefix = build_readonly_shell_env_prefix(injected)
    assert "RS_PARAM_PATH='/mnt/vol1; rm -rf /'; readonly RS_PARAM_PATH" in inj_prefix

    # Ambiguous multi-group override MUST raise OVERRIDE_CONFLICT (REQ-TPL-12)
    with pytest.raises(ParameterResolutionError) as conflict_exc:
        resolve_instance_params_for_host(
            s04,
            inst,
            "db",
            host,
            [
                ParameterOverrideRule(
                    scope_type="group",
                    scope_target="g1",
                    role_id="db",
                    instance_id="db_space",
                    param_name="path",
                    value="/mnt/a",
                ),
                ParameterOverrideRule(
                    scope_type="group",
                    scope_target="g2",
                    role_id="db",
                    instance_id="db_space",
                    param_name="path",
                    value="/mnt/b",
                ),
            ],
        )
    assert conflict_exc.value.code == "OVERRIDE_CONFLICT"

    # Overriding non-whitelisted param MUST raise OVERRIDE_NOT_ALLOWED (REQ-TPL-13)
    locked_inst = inst.model_copy(update={"overridable_params": []})
    with pytest.raises(ParameterResolutionError) as deny_exc:
        resolve_instance_params_for_host(
            s04,
            locked_inst,
            "db",
            host,
            [
                ParameterOverrideRule(
                    scope_type="host",
                    scope_target="h1",
                    role_id="db",
                    instance_id="db_space",
                    param_name="path",
                    value="/mnt/hack",
                )
            ],
        )
    assert deny_exc.value.code == "OVERRIDE_NOT_ALLOWED"


def test_goss_smart_merge_and_contradictory_resource_conflict() -> None:
    templates = get_builtin_templates()
    inv = parse_quick_ip_list("node1=10.0.0.5")
    host = inv.hosts[0]

    # S01 (exists, filetype) + S02 (exists, owner, group, mode) on `/data` + A03 (privileged) + D04 (no ldd)
    plan = PlanDocument(
        plan_id="merge-test",
        name="Smart Merge Plan",
        state=PlanState.RELEASED,
        roles=[
            RoleSpec(
                role_id="app",
                name="App",
                instances=[
                    CheckInstance(
                        instance_id="i_s01",
                        template_id="S01",
                        params={"path": "/data", "exists": True, "filetype": "directory"},
                    ),
                    CheckInstance(
                        instance_id="i_s02",
                        template_id="S02",
                        params={"path": "/data", "owner": "root", "group": "root", "mode": "0755"},
                    ),
                    CheckInstance(
                        instance_id="i_d04",
                        template_id="D04",
                        params={"elf_path": "/usr/bin/openssl", "required_soname": "libc.so.6"},
                    ),
                    CheckInstance(
                        instance_id="i_a03",
                        template_id="A03",
                        params={"option_key": "maxauthtries", "expected_value": "4"},
                    ),
                ],
            )
        ],
    ).with_updated_digest()

    binding = RunBinding(
        plan_id=plan.plan_id,
        plan_version=plan.version,
        plan_state=plan.state,
        plan_digest=plan.digest or "",
        inventory=inv,
        role_to_hosts={"app": ["node1"]},
    )

    std_art, priv_art = compile_host_artifacts(plan, binding, host, ["app"], templates)
    assert std_art is not None
    assert priv_art is not None
    # S01 & S02 smart-merged into a single `file:/data` object without YAML key overwrite!
    assert std_art.goss_rules["file"]["/data"] == {
        "exists": True,
        "filetype": "directory",
        "owner": "root",
        "group": "root",
        "mode": "0755",
    }
    # TODO-008: D04 strictly forbids `ldd`
    d04_exec = std_art.goss_rules["command"]["rs_i_d04"]["exec"]
    assert "ldd" not in d04_exec
    assert "readelf" in d04_exec
    # A03 isolated in PRIVILEGED artifact
    assert "rs_i_a03" in priv_art.goss_rules["command"]

    # Co-located contradictory assertion on `port:tcp:8080` (`listening: false` vs `true`) -> RESOURCE_CONFLICT
    conflict_plan = PlanDocument(
        plan_id="conflict-plan",
        name="Conflict",
        state=PlanState.RELEASED,
        roles=[
            RoleSpec(
                role_id="r1",
                name="R1",
                instances=[
                    CheckInstance(
                        instance_id="p_free",
                        template_id="N02",
                        params={"protocol": "tcp", "port": 8080, "should_listen": False},
                    )
                ],
            ),
            RoleSpec(
                role_id="r2",
                name="R2",
                instances=[
                    CheckInstance(
                        instance_id="p_listen",
                        template_id="N02",
                        params={"protocol": "tcp", "port": 8080, "should_listen": True},
                    )
                ],
            ),
        ],
    )
    with pytest.raises(CompilationConflictError) as c_err:
        compile_host_artifacts(conflict_plan, binding, host, ["r1", "r2"], templates)
    assert c_err.value.code == "RESOURCE_CONFLICT"

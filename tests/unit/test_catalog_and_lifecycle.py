"""Unit tests for 54-group catalog, 40 P0 templates, safe YAML, RFC 8785 digests, and Plan lifecycle."""

from __future__ import annotations

from pathlib import Path

import pytest

from readyspec.constants import PlanState, PrivilegeGroup, TemplateMaturity, TemplateSource
from readyspec.domain.models import (
    CheckInstance,
    CustomShellSpec,
    PlanDocument,
    RoleSpec,
    TemplateKind,
    TemplateSpec,
)
from readyspec.domain.safe_yaml import DuplicateYamlKeyError, SafeYamlError, load_safe_yaml
from readyspec.registry.builtin_catalog import DOMAINS, get_builtin_templates, get_catalog_groups
from readyspec.registry.workspace_store import PlanLifecycleError, WorkspaceStore


def test_catalog_has_exact_9_domains_54_groups_and_40_p0_templates() -> None:
    assert len(DOMAINS) == 9
    groups = get_catalog_groups()
    assert len(groups) == 54

    by_domain: dict[str, int] = {}
    for g in groups:
        by_domain[g.domain_code] = by_domain.get(g.domain_code, 0) + 1
    assert by_domain == {
        "H": 4,
        "O": 4,
        "S": 5,
        "N": 7,
        "R": 6,
        "A": 7,
        "D": 7,
        "L": 7,
        "P": 7,
    }

    builtins = get_builtin_templates()
    assert len(builtins) == 40
    assert all(t.maturity == TemplateMaturity.INTEGRATED for t in builtins.values())
    assert builtins["A03"].privilege_group == PrivilegeGroup.PRIVILEGED


def test_safe_yaml_rejects_duplicate_keys_and_oversize() -> None:
    dup_yaml = """
plan_id: demo
name: First
name: OverwrittenSilently
"""
    with pytest.raises(DuplicateYamlKeyError):
        load_safe_yaml(dup_yaml)

    with pytest.raises(SafeYamlError) as exc_info:
        load_safe_yaml("a: 1\n", max_bytes=2)
    assert exc_info.value.code == "YAML_SIZE_EXCEEDED"


def test_release_portable_export_import_and_digest_conflict(tmp_path: Path) -> None:
    ws_dev = WorkspaceStore(tmp_path / "ws_dev")
    ws_ops = WorkspaceStore(tmp_path / "ws_ops")

    custom = TemplateSpec(
        template_id="local.echo_ok",
        name_zh="自定义检查",
        name_en="Custom Echo",
        domain_code="D",
        group_code="CUSTOM",
        source=TemplateSource.PLAN_LOCAL,
        kind=TemplateKind.CUSTOM_SHELL,
        shell_spec=CustomShellSpec(script="echo ok"),
    ).with_digest()

    plan = PlanDocument(
        plan_id="svc-alpha",
        name="Alpha 部署方案",
        version="1.0.0",
        roles=[
            RoleSpec(
                role_id="app",
                name="App 节点",
                instances=[
                    CheckInstance(instance_id="app_h02", template_id="H02", params={"min_cores": 4}),
                    CheckInstance(instance_id="app_custom", template_id="local.echo_ok"),
                ],
            )
        ],
        embedded_templates=[custom],
    )

    # Promote custom template without mutating original plan (REQ-TPL-15/16)
    pub = ws_dev.promote_custom_template(plan, "local.echo_ok")
    assert pub.source == TemplateSource.USER_PUBLIC
    assert pub.privilege_group == PrivilegeGroup.STANDARD
    assert plan.roles[0].instances[1].template_id == "local.echo_ok"

    released = ws_dev.release_plan(plan, version="1.0.0")
    assert released.state == PlanState.RELEASED
    yaml_text = ws_dev.export_plan_yaml(released)

    # Clean workspace imports single self-contained file with matching semantic digest (F08)
    imported = ws_ops.import_plan_yaml(yaml_text)
    assert imported.digest == released.digest
    assert imported.state == PlanState.RELEASED

    # Mutating same version must trigger PLAN_DIGEST_CONFLICT (REQ-PLAN-05/06)
    mutated = plan.model_copy(deep=True)
    mutated.roles[0].instances[0].params["min_cores"] = 16
    with pytest.raises(PlanLifecycleError) as conflict_err:
        ws_dev.release_plan(mutated, version="1.0.0")
    assert conflict_err.value.code == "PLAN_DIGEST_CONFLICT"


def test_forged_privileged_template_rejected_on_import(tmp_path: Path) -> None:
    ws = WorkspaceStore(tmp_path / "ws")
    forged_tpl = TemplateSpec(
        template_id="local.evil_root",
        name_zh="伪造特权模板",
        name_en="Evil Root",
        domain_code="A",
        group_code="EVIL",
        source=TemplateSource.PLAN_LOCAL,
        privilege_group=PrivilegeGroup.PRIVILEGED,
        kind=TemplateKind.CUSTOM_SHELL,
        shell_spec=CustomShellSpec(script="id"),
    ).with_digest()

    bad_plan = PlanDocument(
        plan_id="bad-plan",
        name="伪造方案",
        version="1.0.0",
        roles=[
            RoleSpec(
                role_id="app",
                name="App",
                instances=[CheckInstance(instance_id="i1", template_id="local.evil_root")],
            )
        ],
        embedded_templates=[forged_tpl],
    ).with_updated_digest()

    diags = ws.validate_plan(bad_plan)
    codes = {d.code for d in diags}
    assert "USER_TEMPLATE_PRIVILEGED_FORBIDDEN" in codes
    assert "UNTRUSTED_PRIVILEGED_TEMPLATE" in codes


def test_clone_instance_across_roles_creates_independent_id() -> None:
    plan = PlanDocument(
        plan_id="clone-demo",
        name="Clone Demo",
        roles=[
            RoleSpec(
                role_id="web",
                name="Web",
                instances=[
                    CheckInstance(
                        instance_id="web_disk",
                        template_id="S04",
                        params={"path": "/data", "min_free_gib": 50.0},
                    )
                ],
            ),
            RoleSpec(role_id="db", name="DB", instances=[]),
        ],
    )
    cloned = plan.clone_instance_to_role("web", "web_disk", "db")
    assert cloned.instance_id != "web_disk"
    cloned.params["min_free_gib"] = 200.0
    assert plan.roles[0].instances[0].params["min_free_gib"] == 50.0

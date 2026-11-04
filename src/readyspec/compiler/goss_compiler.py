"""Deterministic Goss v0.4.9 compiler with native smart-merging & conflict detection (TODO-002)."""

from __future__ import annotations

import shlex
from typing import Any

from readyspec.compiler.resolver import (
    ParameterResolutionError,
    build_readonly_shell_env_prefix,
    resolve_instance_params_for_host,
)
from readyspec.constants import (
    COMPILER_VERSION,
    DEFAULT_SHELL_TIMEOUT_SEC,
    PrivilegeGroup,
    RunScope,
    TemplateSource,
)
from readyspec.domain.digest import compute_canonical_digest
from readyspec.domain.models import (
    CapabilityRecord,
    CheckInstance,
    ExecutionArtifact,
    InventoryHost,
    PlanDocument,
    PropertyOrigin,
    ResolvedParamValue,
    RunBinding,
    StaticRunPreview,
    TemplateKind,
    TemplateSpec,
    ValidationDiagnostic,
)
from readyspec.domain.safe_yaml import dump_safe_yaml


class CompilationConflictError(ValueError):
    """Raised when co-located instances assert contradictory Goss properties or invalid security rules."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def build_static_preview(
    plan: PlanDocument,
    binding: RunBinding,
    templates: dict[str, TemplateSpec],
) -> StaticRunPreview:
    """Compute pre-SSH static preview of candidate checks, privileges, root SSH, and probes (Section 5.4)."""
    diagnostics: list[ValidationDiagnostic] = []
    hosts_by_name = {h.name: h for h in binding.inventory.hosts}
    active_filter = set(binding.selected_hosts_filter) if binding.run_scope == RunScope.PARTIAL_SCOPE else None

    role_bindings: dict[str, list[str]] = {}
    referenced_hosts: set[str] = set()
    total_checks = 0
    priv_templates: set[str] = set()
    custom_shells: list[str] = []
    active_probes: list[str] = []

    for role in plan.roles:
        bound_names = [
            h_name
            for h_name in binding.role_to_hosts.get(role.role_id, [])
            if h_name in hosts_by_name and (active_filter is None or h_name in active_filter)
        ]
        role_bindings[role.role_id] = bound_names
        if binding.run_scope == RunScope.FULL and len(bound_names) < role.min_nodes:
            diagnostics.append(
                ValidationDiagnostic(
                    severity="WARNING",
                    code="TOPOLOGY_BELOW_MIN_NODES",
                    message=f"角色 '{role.name}' 当前绑定 {len(bound_names)} 台主机，少于方案要求的最小节点数 {role.min_nodes}（P02 将判定 FAIL）。",
                    role_id=role.role_id,
                )
            )
        if role.max_nodes is not None and len(bound_names) > role.max_nodes:
            diagnostics.append(
                ValidationDiagnostic(
                    severity="WARNING",
                    code="TOPOLOGY_ABOVE_MAX_NODES",
                    message=f"角色 '{role.name}' 当前绑定 {len(bound_names)} 台主机，超过最大节点数 {role.max_nodes}。",
                    role_id=role.role_id,
                )
            )

        for h_name in bound_names:
            referenced_hosts.add(h_name)
            host_obj = hosts_by_name[h_name]
            for inst in role.instances:
                if not inst.enabled:
                    continue
                tpl = templates.get(inst.template_id)
                if tpl is None or tpl.kind == TemplateKind.CONTROL:
                    continue
                total_checks += 1
                if tpl.privilege_group == PrivilegeGroup.PRIVILEGED:
                    priv_templates.add(tpl.template_id)
                if tpl.kind == TemplateKind.CUSTOM_SHELL and inst.instance_id not in custom_shells:
                    custom_shells.append(inst.instance_id)
                if tpl.active_network_probe and f"{inst.instance_id}({tpl.template_id})" not in active_probes:
                    active_probes.append(f"{inst.instance_id}({tpl.template_id})")
                try:
                    resolve_instance_params_for_host(
                        tpl, inst, role.role_id, host_obj, binding.runtime_overrides
                    )
                except ParameterResolutionError as exc:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code=exc.code,
                            message=exc.message,
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )

    # Check co-location disallowance
    host_roles_map: dict[str, list[str]] = {}
    for role in plan.roles:
        for h_name in role_bindings.get(role.role_id, []):
            host_roles_map.setdefault(h_name, []).append(role.role_id)

    for role in plan.roles:
        if not role.allow_colocation:
            for h_name in role_bindings.get(role.role_id, []):
                if len(host_roles_map.get(h_name, [])) > 1:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="WARNING",
                            code="COLOCATION_POLICY_VIOLATION",
                            message=f"主机 '{h_name}' 同时承担多个角色 {host_roles_map[h_name]}，但角色 '{role.name}' 声明禁止共址（P02 将判定 FAIL）。",
                            role_id=role.role_id,
                        )
                    )

    root_hosts = sorted(
        h_name for h_name in referenced_hosts if hosts_by_name[h_name].ssh_user == "root"
    )
    if root_hosts and not binding.approve_root_ssh:
        diagnostics.append(
            ValidationDiagnostic(
                severity="WARNING",
                code="ROOT_SSH_REQUIRES_APPROVAL",
                message=f"主机 {', '.join(root_hosts)} 使用 root SSH 连接，必须勾选/传入显式 root SSH 审批方可放行 (CHG-001)。",
            )
        )

    unapproved_priv = sorted(priv_templates - set(binding.approved_privileged_templates))
    if unapproved_priv:
        diagnostics.append(
            ValidationDiagnostic(
                severity="WARNING",
                code="PRIVILEGED_REQUIRES_APPROVAL",
                message=f"方案包含内置特权模板 {', '.join(unapproved_priv)}，未批准时对应特权项将报未授权 ERROR。",
            )
        )

    return StaticRunPreview(
        plan_id=plan.plan_id,
        plan_name=plan.name,
        plan_version=plan.version,
        plan_state=plan.state,
        plan_digest=plan.digest or plan.compute_semantic_digest(),
        host_count=len(referenced_hosts),
        role_bindings=role_bindings,
        total_candidate_checks=total_checks,
        privileged_templates_needed=sorted(priv_templates),
        root_ssh_hosts=root_hosts,
        custom_shell_instances=custom_shells,
        active_network_probes=active_probes,
        diagnostics=diagnostics,
    )


def _compile_single_instance_rules(
    tpl: TemplateSpec,
    inst: CheckInstance,
    resolved: dict[str, ResolvedParamValue],
    capability: CapabilityRecord | None,
) -> tuple[str, str, dict[str, Any], dict[str, str]]:
    """Return `(goss_resource_type, goss_resource_id, goss_props, expected_summaries_by_prop)`."""
    tid = tpl.template_id
    vals = {k: v.value for k, v in resolved.items()}

    # Native Goss v0.4.9 resources
    if tid == "O03":
        key = str(vals["key"])
        exp = str(vals["expected_value"])
        return "kernel-param", key, {"value": exp}, {"value": f"{key} == {exp}"}

    if tid == "S01":
        path = str(vals["path"])
        exists = bool(vals["exists"])
        ftype = str(vals["filetype"])
        props: dict[str, Any] = {"exists": exists}
        exp_map = {"exists": f"exists == {exists}"}
        if exists:
            props["filetype"] = ftype
            exp_map["filetype"] = f"filetype == {ftype}"
        return "file", path, props, exp_map

    if tid == "S02":
        path = str(vals["path"])
        owner = str(vals["owner"])
        group = str(vals["group"])
        mode = str(vals["mode"])
        return (
            "file",
            path,
            {"exists": True, "owner": owner, "group": group, "mode": mode},
            {
                "exists": "exists == True",
                "owner": f"owner == {owner}",
                "group": f"group == {group}",
                "mode": f"mode == {mode}",
            },
        )

    if tid == "S03":
        mp = str(vals["mount_point"])
        fs = str(vals["filesystem"])
        opts = [str(o) for o in (vals.get("required_opts") or [])]
        return (
            "mount",
            mp,
            {"exists": True, "filesystem": fs, "opts": opts},
            {
                "exists": "mounted == True",
                "filesystem": f"filesystem == {fs}",
                "opts": f"opts contains {opts}",
            },
        )

    if tid == "N01":
        nic = str(vals["interface_name"])
        exists = bool(vals["exists"])
        return "interface", nic, {"exists": exists}, {"exists": f"interface {nic} exists == {exists}"}

    if tid == "N02":
        proto = str(vals["protocol"])
        port = int(vals["port"])
        should_listen = bool(vals["should_listen"])
        rid = f"{proto}:{port}"
        return (
            "port",
            rid,
            {"listening": should_listen},
            {"listening": f"{rid} listening == {should_listen}"},
        )

    if tid == "N03":
        hp = str(vals["target_host_port"])
        reachable = bool(vals["reachable"])
        timeout_ms = int(vals.get("timeout_ms", 3000))
        rid = hp if "://" in hp else f"tcp://{hp}"
        return (
            "addr",
            rid,
            {"reachable": reachable, "timeout": timeout_ms},
            {"reachable": f"{rid} reachable == {reachable}"},
        )

    if tid == "N04":
        fqdn = str(vals["fqdn"])
        resolvable = bool(vals["resolvable"])
        return "dns", fqdn, {"resolvable": resolvable}, {"resolvable": f"{fqdn} resolvable == {resolvable}"}

    if tid == "N05":
        url = str(vals["url"])
        method = str(vals.get("method", "GET")).upper()
        if method not in ("GET", "HEAD"):
            raise CompilationConflictError(
                "HTTP_METHOD_FORBIDDEN",
                f"N05 仅允许 GET/HEAD 低影响探测，已拒绝方法 '{method}' (TODO-015)。",
            )
        status = int(vals["expected_status"])
        insecure = bool(vals.get("allow_insecure_tls", False))
        follow = bool(vals.get("follow_redirects", False))
        return (
            "http",
            url,
            {
                "status": status,
                "method": method,
                "allow-insecure": insecure,
                "no-follow-redirects": not follow,
                "timeout": 5000,
            },
            {"status": f"HTTP {method} {url} -> {status}"},
        )

    if tid == "R01":
        svc = str(vals["service_name"])
        running = bool(vals["running"])
        enabled = bool(vals["enabled"])
        return (
            "service",
            svc,
            {"enabled": enabled, "running": running},
            {"enabled": f"{svc} enabled == {enabled}", "running": f"{svc} running == {running}"},
        )

    if tid == "R02":
        comm = str(vals["comm_name"])
        running = bool(vals["running"])
        return "process", comm, {"running": running}, {"running": f"process {comm} running == {running}"}

    if tid == "A01":
        user = str(vals["username"])
        exists = bool(vals["exists"])
        return "user", user, {"exists": exists}, {"exists": f"user {user} exists == {exists}"}

    if tid == "A02":
        grp = str(vals["groupname"])
        exists = bool(vals["exists"])
        return "group", grp, {"exists": exists}, {"exists": f"group {grp} exists == {exists}"}

    if tid == "D01":
        pkg = str(vals["package_name"])
        installed = bool(vals["installed"])
        return "package", pkg, {"installed": installed}, {"installed": f"package {pkg} installed == {installed}"}

    # Custom Shell template (Section 7 / CHG-003)
    if tpl.kind == TemplateKind.CUSTOM_SHELL and tpl.shell_spec is not None:
        rule_id = f"rs_{inst.instance_id}"
        env_prefix = build_readonly_shell_env_prefix(resolved)
        exec_cmd = f"{tpl.shell_spec.interpreter} -c {shlex.quote(env_prefix + tpl.shell_spec.script)}"
        timeout_ms = tpl.shell_spec.timeout_sec * 1000
        return (
            "command",
            rule_id,
            {
                "exec": exec_cmd,
                "exit-status": tpl.shell_spec.assertions.expected_exit_code,
                "timeout": timeout_ms,
                "meta": {
                    "readyspec_template": tpl.template_id,
                    "readyspec_instance": inst.instance_id,
                    "readyspec_kind": "CUSTOM_SHELL",
                },
            },
            {"exit-status": f"custom_shell ({tpl.shell_spec.assertions.mode.value})"},
        )

    # Built-in POSIX / Static Inspection Command templates (keyed by unique `rs_<instance_id>`, TODO-002)
    rule_id = f"rs_{inst.instance_id}"
    exec_cmd, exp_desc = _build_builtin_shell_command(tid, vals, capability)
    return (
        "command",
        rule_id,
        {
            "exec": exec_cmd,
            "exit-status": 0,
            "timeout": DEFAULT_SHELL_TIMEOUT_SEC * 1000,
            "meta": {
                "readyspec_template": tid,
                "readyspec_instance": inst.instance_id,
                "readyspec_params": vals,
            },
        },
        {"stdout": exp_desc},
    )


def _build_builtin_shell_command(
    tid: str, vals: dict[str, Any], capability: CapabilityRecord | None
) -> tuple[str, str]:
    """Generate safe, read-only POSIX `/bin/sh` commands for built-in shell templates (no target Python)."""
    if tid == "H01":
        allowed = ",".join(str(x) for x in (vals.get("allowed_arch") or []))
        return "uname -m", f"arch in [{allowed}]"
    if tid == "H02":
        return "getconf _NPROCESSORS_ONLN || grep -c '^processor' /proc/cpuinfo", f"online_cpus >= {vals['min_cores']}"
    if tid == "H03":
        return "awk '/^MemTotal:/ {print $2}' /proc/meminfo", f"total_mem >= {vals['min_gib']} GiB"
    if tid == "H04":
        return "awk '/^SwapTotal:/ {print $2}' /proc/meminfo", f"swap policy == {vals['policy']}"
    if tid == "O01":
        return ". /etc/os-release && printf '%s:%s\\n' \"$ID\" \"$VERSION_ID\"", f"os in {vals['allowed_distros']}"
    if tid == "O02":
        return "uname -r", f"kernel >= {vals['min_kernel']}"
    if tid == "O04":
        mod = shlex.quote(str(vals["module_name"]))
        return f"awk -v m={mod} '$1==m {{found=1}} END {{print found+0}}' /proc/modules", f"module {vals['module_name']} loaded == {vals['should_be_loaded']}"
    if tid == "S04":
        p = shlex.quote(str(vals["path"]))
        return f"df -Pk {p} | tail -n 1", f"avail_space({vals['path']}) >= {vals['min_free_gib']} GiB"
    if tid == "S05":
        p = shlex.quote(str(vals["path"]))
        return f"df -Pi {p} | tail -n 1", f"free_inodes_pct({vals['path']}) >= {vals['min_free_pct']}%"
    if tid == "N06":
        ip = shlex.quote(str(vals["destination_ip"]))
        return f"ip -o route get {ip}", f"route to {vals['destination_ip']} dev={vals.get('expected_dev') or '*'}"
    if tid == "R03":
        return "cat /proc/loadavg", f"loadavg({vals['window_minutes']}m) <= {vals['max_load']}"
    if tid == "R04":
        unit = shlex.quote(str(vals["unit_name"]))
        prop = shlex.quote(str(vals["property_name"]))
        return f"systemctl show -p {prop} --value {unit}", f"{vals['unit_name']} {vals['property_name']} >= {vals['min_limit']}"
    if tid == "R05":
        return "timedatectl show -p NTPSynchronized -p NTP", f"NTPSynchronized == {vals['require_synchronized']}"
    if tid == "A03":
        opt = shlex.quote(str(vals["option_key"]).lower())
        return f"/usr/sbin/sshd -T | awk -v k={opt} '$1==k {{print $2}}'", f"sshd -T {vals['option_key']} == {vals['expected_value']}"
    if tid == "A04":
        return "if command -v getenforce >/dev/null 2>&1; then getenforce; elif command -v aa-status >/dev/null 2>&1; then echo apparmor; else echo none; fi", f"mac_policy == {vals['expected_policy']}"
    if tid == "D02":
        cmd = shlex.quote(str(vals["command_or_path"]))
        return f"command -v {cmd}", f"executable '{vals['command_or_path']}' exists"
    if tid == "D03":
        rt = str(vals["runtime"])
        ver_cmd = "openssl version" if rt == "openssl" else ("java -version 2>&1" if rt == "java" else "python3 --version")
        return ver_cmd, f"{rt} version >= {vals['min_version']}"
    if tid == "D04":
        # TODO-008: Strictly forbid `ldd`! Use readelf -> eu-readelf -> objdump + ldconfig -p
        elf = shlex.quote(str(vals["elf_path"]))
        tool = "readelf"
        if capability is not None:
            for candidate in ("readelf", "eu-readelf", "objdump"):
                if candidate in capability.available_tools:
                    tool = candidate
                    break
        if tool == "objdump":
            cmd = f"objdump -f -p {elf} && ldconfig -p"
        else:
            cmd = f"{tool} -h -d {elf} && ldconfig -p"
        return cmd, f"ELF {vals['elf_path']} static NEEDED includes {vals['required_soname']} (no ldd)"
    if tid == "D05":
        cfg = shlex.quote(str(vals["config_path"]))
        return f"cat {cfg}", f"{vals['config_path']} matches /{vals['expected_pattern']}/"
    if tid == "L01":
        return "awk '{print $1}' /proc/uptime", f"uptime >= {vals['min_uptime_minutes']} min"
    if tid == "L02":
        hrs = int(vals["window_hours"])
        return (
            f"journalctl -k --since '-{hrs}h' --output=short-iso --no-pager",
            f"OOM events in {hrs}h <= {vals['max_events']} (with window coverage proof)",
        )
    if tid == "L03":
        svc = shlex.quote(str(vals["service_name"]))
        hrs = int(vals["window_hours"])
        return (
            f"journalctl -u {svc} --since '-{hrs}h' --output=short-iso --no-pager",
            f"{vals['service_name']} failures in {hrs}h <= {vals['max_failures']}",
        )
    return "true", tid


def compile_host_artifacts(
    plan: PlanDocument,
    binding: RunBinding,
    host: InventoryHost,
    assigned_roles: list[str],
    templates: dict[str, TemplateSpec],
    capability: CapabilityRecord | None = None,
) -> tuple[ExecutionArtifact | None, ExecutionArtifact | None]:
    """Compile a host's bound role instances into isolated STANDARD & PRIVILEGED Goss v0.4.9 YAML artifacts."""
    grouped_rules: dict[PrivilegeGroup, dict[str, dict[str, dict[str, Any]]]] = {
        PrivilegeGroup.STANDARD: {},
        PrivilegeGroup.PRIVILEGED: {},
    }
    grouped_origins: dict[PrivilegeGroup, list[PropertyOrigin]] = {
        PrivilegeGroup.STANDARD: [],
        PrivilegeGroup.PRIVILEGED: [],
    }
    grouped_resolved: dict[PrivilegeGroup, dict[str, dict[str, ResolvedParamValue]]] = {
        PrivilegeGroup.STANDARD: {},
        PrivilegeGroup.PRIVILEGED: {},
    }

    for role in plan.roles:
        if role.role_id not in assigned_roles:
            continue
        for inst in role.instances:
            if not inst.enabled:
                continue
            tpl = templates.get(inst.template_id)
            if tpl is None or tpl.kind == TemplateKind.CONTROL:
                continue

            # Security red-line: User/custom templates can NEVER enter PRIVILEGED (Section 5.5 / REQ-SEC-01/03)
            if tpl.privilege_group == PrivilegeGroup.PRIVILEGED and tpl.source != TemplateSource.BUILTIN:
                raise CompilationConflictError(
                    "PRIVILEGED_TRUST_VIOLATION",
                    f"模板 '{tpl.template_id}' 非本地内置可信模板，严禁编译入 PRIVILEGED 工件。",
                )

            resolved = resolve_instance_params_for_host(
                tpl, inst, role.role_id, host, binding.runtime_overrides
            )
            res_type, res_id, props, exp_summaries = _compile_single_instance_rules(
                tpl, inst, resolved, capability
            )

            priv = tpl.privilege_group
            type_bucket = grouped_rules[priv].setdefault(res_type, {})
            if res_id not in type_bucket:
                type_bucket[res_id] = dict(props)
            else:
                # Smart-merge non-conflicting properties on identical Goss native key; reject conflicts (TODO-002)
                existing_props = type_bucket[res_id]
                for prop_k, prop_v in props.items():
                    if prop_k in existing_props and existing_props[prop_k] != prop_v:
                        raise CompilationConflictError(
                            "RESOURCE_CONFLICT",
                            f"主机 '{host.name}' 上底层 Goss 资源 '{res_type}:{res_id}' 属性 '{prop_k}' 发生矛盾断言 "
                            f"({existing_props[prop_k]!r} vs {prop_v!r}，来自实例 '{inst.instance_id}')，已拒绝静默覆盖 (TODO-002 / REQ-COMPILER-06)。",
                        )
                    existing_props[prop_k] = prop_v

            grouped_resolved[priv][inst.instance_id] = resolved
            for prop_k, summary_text in exp_summaries.items():
                grouped_origins[priv].append(
                    PropertyOrigin(
                        role_id=role.role_id,
                        instance_id=inst.instance_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        template_digest=tpl.digest or "",
                        importance=inst.importance,
                        privilege_group=priv,
                        evidence_depth=tpl.evidence_depth,
                        goss_resource_type=res_type,
                        goss_resource_id=res_id,
                        property_key=prop_k,
                        expected_summary=summary_text,
                    )
                )

    artifacts: dict[PrivilegeGroup, ExecutionArtifact | None] = {
        PrivilegeGroup.STANDARD: None,
        PrivilegeGroup.PRIVILEGED: None,
    }
    for priv in (PrivilegeGroup.STANDARD, PrivilegeGroup.PRIVILEGED):
        rules = grouped_rules[priv]
        if not rules:
            continue
        # Canonical deterministic ordering of resource types & IDs (REQ-COMPILER-08)
        ordered_rules: dict[str, dict[str, dict[str, Any]]] = {}
        for r_type in sorted(rules):
            ordered_rules[r_type] = {r_id: rules[r_type][r_id] for r_id in sorted(rules[r_type])}
        yaml_str = dump_safe_yaml(ordered_rules)
        digest = compute_canonical_digest(ordered_rules)
        artifacts[priv] = ExecutionArtifact(
            host_name=host.name,
            privilege_group=priv,
            goss_rules=ordered_rules,
            goss_yaml=yaml_str,
            digest=digest,
            origins=grouped_origins[priv],
            resolved_params_by_instance=grouped_resolved[priv],
            compiler_version=COMPILER_VERSION,
        )

    return artifacts[PrivilegeGroup.STANDARD], artifacts[PrivilegeGroup.PRIVILEGED]

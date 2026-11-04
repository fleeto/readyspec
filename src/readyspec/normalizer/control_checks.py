"""Control-plane checks P01-P04, Readiness aggregation, and CLI ExitCode derivation."""

from __future__ import annotations

from readyspec.constants import (
    CheckStatus,
    CleanupStatus,
    EvidenceDepth,
    ExitCode,
    Importance,
    PlanState,
    PrivilegeGroup,
    ReadinessStatus,
    RunScope,
    RunStatus,
)
from readyspec.domain.models import (
    CheckEvidence,
    CheckInstanceResult,
    CleanupRecord,
    HostRunResult,
    PlanDocument,
    RunBinding,
)


def evaluate_control_plane_and_readiness(
    *,
    run_id: str,
    plan: PlanDocument,
    binding: RunBinding,
    run_status: RunStatus,
    host_results: list[HostRunResult],
    cleanup_records: list[CleanupRecord],
) -> tuple[list[CheckInstanceResult], ReadinessStatus, str, bool]:
    """Compute P01-P04 control checks and derive `(control_checks, readiness, reason, gate_eligible)`."""
    hosts_by_name = {h.host_name: h for h in host_results}

    # ------------------------------------------------------------------
    # P01: Software Platform Compatibility (Section 6.9)
    # ------------------------------------------------------------------
    allowed_os = [x.lower() for x in plan.compatibility.allowed_os]
    allowed_arch = [x.lower() for x in plan.compatibility.allowed_arch]
    p01_mismatches: list[str] = []
    p01_unknowns: list[str] = []
    p01_observed_parts: list[str] = []

    for h in host_results:
        if h.capability is None:
            p01_unknowns.append(f"{h.host_name}(未采集到平台信息)")
            continue
        os_tag = f"{h.capability.os_id}:{h.capability.os_version}".lower()
        arch_tag = h.capability.arch.lower()
        p01_observed_parts.append(f"{h.host_name}={os_tag}/{arch_tag}")
        if h.capability.os_id == "unknown":
            p01_unknowns.append(f"{h.host_name}(未知发行版)")
            continue
        os_ok = not allowed_os or os_tag in allowed_os or os_tag.split(".")[0] in allowed_os
        arch_ok = not allowed_arch or arch_tag in allowed_arch
        if not (os_ok and arch_ok):
            p01_mismatches.append(f"{h.host_name}({os_tag}/{arch_tag})")

    if p01_mismatches:
        p01_status = CheckStatus.FAIL
        p01_summary = f"目标机平台不符合方案软件兼容矩阵: {', '.join(p01_mismatches)}"
    elif p01_unknowns or not host_results:
        p01_status = CheckStatus.ERROR
        p01_summary = f"存在未能确认平台兼容性的主机: {', '.join(p01_unknowns) or '无主机结果'}"
    else:
        p01_status = CheckStatus.PASS
        p01_summary = f"全部 {len(host_results)} 台目标机平台符合软件支持矩阵 ({', '.join(p01_observed_parts)})"

    p01_check = CheckInstanceResult(
        instance_id="ctrl_p01_platform",
        title="P01 业务软件目标平台兼容矩阵匹配",
        role_id="_control",
        template_id="P01",
        template_version="1.0.0",
        importance=Importance.REQUIRED,
        privilege_group=PrivilegeGroup.STANDARD,
        status=p01_status,
        summary=p01_summary,
        evidences=[
            CheckEvidence(
                run_id=run_id,
                host_name="_control",
                role_id="_control",
                instance_id="ctrl_p01_platform",
                template_id="P01",
                template_version="1.0.0",
                template_digest="builtin:P01",
                goss_subrule_id="control:P01",
                execution_identity="control_plane",
                privilege_group=PrivilegeGroup.STANDARD,
                expected=f"os in {allowed_os}, arch in {allowed_arch}",
                observed="; ".join(p01_observed_parts) or "none",
                evidence_depth=EvidenceDepth.CONTROL_AGGREGATE,
                source="capability_discovery",
            )
        ],
    )

    # ------------------------------------------------------------------
    # P02: Role Node Count, Colocation & Dynamic Identity Dedup (TODO-016)
    # ------------------------------------------------------------------
    p02_failures: list[str] = []
    p02_errors: list[str] = []
    seen_dynamic_fp: dict[tuple[str, str, str], str] = {}

    for h in host_results:
        if h.capability is not None:
            fp = (
                h.capability.machine_id,
                h.capability.boot_id,
                h.capability.ssh_host_key_fp,
            )
            prev_host = seen_dynamic_fp.get(fp)
            if prev_host is not None and prev_host != h.host_name:
                p02_errors.append(
                    f"动态实机指纹冲突 (TODO-016)：主机名 '{prev_host}' 与 '{h.host_name}' 实测为同一物理/虚拟机 (machine-id={fp[0]})"
                )
            seen_dynamic_fp[fp] = h.host_name

    host_role_membership: dict[str, list[str]] = {}
    for role in plan.roles:
        bound_hosts = binding.role_to_hosts.get(role.role_id, [])
        for h_name in bound_hosts:
            host_role_membership.setdefault(h_name, []).append(role.role_id)

        if binding.run_scope == RunScope.FULL:
            if len(bound_hosts) < role.min_nodes:
                p02_failures.append(
                    f"角色 '{role.name}' 仅绑定 {len(bound_hosts)} 台主机，少于最少要求 {role.min_nodes} 台"
                )
        if role.max_nodes is not None and len(bound_hosts) > role.max_nodes:
            p02_failures.append(
                f"角色 '{role.name}' 绑定 {len(bound_hosts)} 台主机，超过最大上限 {role.max_nodes} 台"
            )
        if not role.allow_colocation:
            for h_name in bound_hosts:
                if len(host_role_membership.get(h_name, [])) > 1:
                    p02_failures.append(
                        f"主机 '{h_name}' 同时承担 {host_role_membership[h_name]}，违反角色 '{role.name}' 禁止共址策略"
                    )

    if binding.run_scope == RunScope.PARTIAL_SCOPE:
        p02_errors.append(
            f"本次运行为 PARTIAL_SCOPE 局部排障快测（仅跑 {binding.selected_hosts_filter}），未验证完整角色拓扑"
        )

    if p02_failures:
        p02_status = CheckStatus.FAIL
        p02_summary = "；".join(p02_failures)
    elif p02_errors:
        p02_status = CheckStatus.ERROR
        p02_summary = "；".join(p02_errors)
    else:
        p02_status = CheckStatus.PASS
        p02_summary = "全部业务角色节点数、共址约束与实机身份唯一性校验通过"

    p02_check = CheckInstanceResult(
        instance_id="ctrl_p02_topology",
        title="P02 业务角色节点数量与共址拓扑",
        role_id="_control",
        template_id="P02",
        template_version="1.0.0",
        importance=Importance.REQUIRED,
        privilege_group=PrivilegeGroup.STANDARD,
        status=p02_status,
        summary=p02_summary,
        evidences=[
            CheckEvidence(
                run_id=run_id,
                host_name="_control",
                role_id="_control",
                instance_id="ctrl_p02_topology",
                template_id="P02",
                template_version="1.0.0",
                template_digest="builtin:P02",
                goss_subrule_id="control:P02",
                execution_identity="control_plane",
                privilege_group=PrivilegeGroup.STANDARD,
                expected="min/max nodes & colocation & unique machine_id",
                observed=str(binding.role_to_hosts),
                evidence_depth=EvidenceDepth.CONTROL_AGGREGATE,
                source="topology_resolver",
            )
        ],
    )

    # ------------------------------------------------------------------
    # P03: Required Obligation Coverage (Section 6.9)
    # ------------------------------------------------------------------
    expected_required_obligations = 0
    covered_definitive_obligations = 0
    missing_or_inconclusive: list[str] = []
    explicit_required_failures: list[str] = []

    for role in plan.roles:
        bound_hosts = binding.role_to_hosts.get(role.role_id, [])
        for inst in role.instances:
            if not inst.enabled or inst.importance != Importance.REQUIRED:
                continue
            for h_name in bound_hosts:
                expected_required_obligations += 1
                h_res = hosts_by_name.get(h_name)
                if h_res is None:
                    missing_or_inconclusive.append(f"{h_name}:{inst.instance_id}(未在本次范围执行)")
                    continue
                if h_res.host_error:
                    missing_or_inconclusive.append(f"{h_name}:{inst.instance_id}({h_res.host_error})")
                    continue
                c_res = next(
                    (c for c in h_res.check_results if c.instance_id == inst.instance_id),
                    None,
                )
                if c_res is None:
                    missing_or_inconclusive.append(f"{h_name}:{inst.instance_id}(无结果)")
                elif c_res.status == CheckStatus.PASS:
                    covered_definitive_obligations += 1
                elif c_res.status == CheckStatus.FAIL:
                    covered_definitive_obligations += 1
                    explicit_required_failures.append(f"{h_name}/{inst.instance_id}: {c_res.summary}")
                else:
                    missing_or_inconclusive.append(
                        f"{h_name}/{inst.instance_id}({c_res.status.value})"
                    )

    if expected_required_obligations == 0:
        p03_status = CheckStatus.ERROR
        p03_summary = "有效必需检查义务集合为空（0 项），严禁空方案放行 (REQ-RESULT-05)"
    elif missing_or_inconclusive:
        p03_status = CheckStatus.ERROR
        p03_summary = (
            f"必需检查未完整获得确定结论 ({covered_definitive_obligations}/{expected_required_obligations})："
            + ", ".join(missing_or_inconclusive[:4])
        )
    else:
        p03_status = CheckStatus.PASS
        p03_summary = f"全部 {expected_required_obligations} 项主机级必需检查义务均已完整采集并获得确定结论"

    p03_check = CheckInstanceResult(
        instance_id="ctrl_p03_coverage",
        title="P03 必需检查项执行覆盖完整性",
        role_id="_control",
        template_id="P03",
        template_version="1.0.0",
        importance=Importance.REQUIRED,
        privilege_group=PrivilegeGroup.STANDARD,
        status=p03_status,
        summary=p03_summary,
        evidences=[
            CheckEvidence(
                run_id=run_id,
                host_name="_control",
                role_id="_control",
                instance_id="ctrl_p03_coverage",
                template_id="P03",
                template_version="1.0.0",
                template_digest="builtin:P03",
                goss_subrule_id="control:P03",
                execution_identity="control_plane",
                privilege_group=PrivilegeGroup.STANDARD,
                expected=f"{expected_required_obligations} definitive required checks",
                observed=f"{covered_definitive_obligations}/{expected_required_obligations}",
                evidence_depth=EvidenceDepth.CONTROL_AGGREGATE,
                source="obligation_tracker",
            )
        ],
    )

    # ------------------------------------------------------------------
    # P04: Final Readiness Aggregation & Gate Eligibility (Section 10.4 / 11.1)
    # ------------------------------------------------------------------
    blocking_fails: list[str] = []
    if p01_status == CheckStatus.FAIL:
        blocking_fails.append(f"P01: {p01_summary}")
    if p02_status == CheckStatus.FAIL:
        blocking_fails.append(f"P02: {p02_summary}")
    blocking_fails.extend(explicit_required_failures)

    unresolved_cleanups = [
        c for c in cleanup_records if c.status not in (CleanupStatus.CLEAN,)
    ]

    # REQ-RESULT-03: Explicit REQUIRED FAIL always wins -> NOT_READY
    if blocking_fails:
        readiness = ReadinessStatus.NOT_READY
        reason = f"存在 {len(blocking_fails)} 项必需部署条件明确 FAIL（首项：{blocking_fails[0]}）"
        if missing_or_inconclusive:
            reason += f"；另有 {len(missing_or_inconclusive)} 项未知/异常待核查"
        p04_status = CheckStatus.FAIL
        gate_eligible = False
    elif binding.run_scope == RunScope.PARTIAL_SCOPE:
        # CHG-003: Partial fault-triage re-run NEVER outputs global READY
        readiness = ReadinessStatus.UNKNOWN
        reason = "PARTIAL_SCOPE 局部排障快测：仅验证了部分主机，严禁输出全局 READY 通行结论；请执行全量正式复测确认"
        p04_status = CheckStatus.ERROR
        gate_eligible = False
    elif (
        run_status != RunStatus.COMPLETED
        or p01_status != CheckStatus.PASS
        or p02_status != CheckStatus.PASS
        or p03_status != CheckStatus.PASS
    ):
        readiness = ReadinessStatus.UNKNOWN
        reasons_unk: list[str] = []
        if run_status != RunStatus.COMPLETED:
            reasons_unk.append(f"任务状态为 {run_status.value}")
        if p01_status != CheckStatus.PASS:
            reasons_unk.append(p01_summary)
        if p02_status != CheckStatus.PASS:
            reasons_unk.append(p02_summary)
        if p03_status != CheckStatus.PASS:
            reasons_unk.append(p03_summary)
        reason = "未发现明确 FAIL，但关键前提尚未充分验证：" + "；".join(reasons_unk)
        p04_status = CheckStatus.ERROR
        gate_eligible = False
    elif unresolved_cleanups:
        readiness = ReadinessStatus.UNKNOWN
        reason = f"存在 {len(unresolved_cleanups)} 条未结案的远端清理/执行完整性不确定记录，按门禁红线暂不自动放行 (Section 10.4)"
        p04_status = CheckStatus.ERROR
        gate_eligible = False
    elif plan.state != PlanState.RELEASED:
        # CHG-002 / Section 10.4: TRIAL_SNAPSHOT cannot pass formal deployment gate!
        readiness = ReadinessStatus.READY
        reason = "全部必需条件实测满足，但当前方案为 TRIAL_SNAPSHOT 草稿试跑快照（gate_eligible=False，严禁用于正式门禁放行）"
        p04_status = CheckStatus.PASS
        gate_eligible = False
    else:
        readiness = ReadinessStatus.READY
        reason = f"正式发布方案 {plan.plan_id}@{plan.version} 全部 {expected_required_obligations} 项必需条件及平台拓扑已充分证明满足"
        p04_status = CheckStatus.PASS
        gate_eligible = True

    p04_check = CheckInstanceResult(
        instance_id="ctrl_p04_readiness",
        title="P04 部署放行就绪度总聚合 (Readiness)",
        role_id="_control",
        template_id="P04",
        template_version="1.0.0",
        importance=Importance.REQUIRED,
        privilege_group=PrivilegeGroup.STANDARD,
        status=p04_status,
        summary=f"Readiness={readiness.value} (gate_eligible={gate_eligible}) — {reason}",
        evidences=[
            CheckEvidence(
                run_id=run_id,
                host_name="_control",
                role_id="_control",
                instance_id="ctrl_p04_readiness",
                template_id="P04",
                template_version="1.0.0",
                template_digest="builtin:P04",
                goss_subrule_id="control:P04",
                execution_identity="control_plane",
                privilege_group=PrivilegeGroup.STANDARD,
                expected="RELEASED & FULL scope & all REQUIRED PASS & clean",
                observed=f"readiness={readiness.value}, gate_eligible={gate_eligible}",
                evidence_depth=EvidenceDepth.CONTROL_AGGREGATE,
                source="readiness_gate",
            )
        ],
    )

    return [p01_check, p02_check, p03_check, p04_check], readiness, reason, gate_eligible


def derive_cli_exit_code(
    run_status: RunStatus, readiness: ReadinessStatus, gate_eligible: bool
) -> ExitCode:
    """Derive locked ReadySpec CLI exit code (Section 10.4 / REQ-CLI-04/05)."""
    if run_status == RunStatus.CANCELLED:
        return ExitCode.CANCELLED
    if run_status == RunStatus.FAILED:
        return ExitCode.INFRA_ERROR
    if readiness == ReadinessStatus.NOT_READY:
        return ExitCode.NOT_READY
    if readiness == ReadinessStatus.READY and gate_eligible and run_status == RunStatus.COMPLETED:
        return ExitCode.READY
    return ExitCode.UNKNOWN

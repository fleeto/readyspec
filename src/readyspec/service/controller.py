"""Single-controller Run Manager orchestrating preview, execution, vault, and recovery."""

from __future__ import annotations

import asyncio
import uuid

from readyspec.compiler.goss_compiler import (
    CompilationConflictError,
    build_static_preview,
    compile_host_artifacts,
)
from readyspec.compiler.resolver import ParameterResolutionError
from readyspec.constants import (
    DEFAULT_HOST_CONCURRENCY,
    MAX_QUEUE_DEPTH,
    HostPhase,
    PlanState,
    RunScope,
    RunStatus,
)
from readyspec.domain.digest import compute_canonical_digest
from readyspec.domain.models import (
    CleanupRecord,
    HostRunResult,
    InventoryHost,
    PlanDocument,
    RunAuditRecord,
    RunBinding,
    RunReport,
    StaticRunPreview,
    TemplateSpec,
    ValidationDiagnostic,
    utc_now_iso,
)
from readyspec.execution.mock_executor import (
    MockRemoteExecutor,
    RemoteExecutionInterruptedError,
)
from readyspec.execution.protocol import PhaseCallback, TargetExecutorProtocol
from readyspec.normalizer.control_checks import evaluate_control_plane_and_readiness
from readyspec.normalizer.goss_normalizer import normalize_host_goss_results
from readyspec.registry.workspace_store import WorkspaceStore


class ControllerSecurityError(PermissionError):
    """Raised on digest mismatch, unapproved root SSH, or idempotency payload conflict."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


class EphemeralCredentialVault:
    """Pure in-memory short-lived password store; wiped immediately on run completion (CHG-002)."""

    def __init__(self) -> None:
        self._secrets: dict[str, str] = {}

    def put_secret(self, secret: str) -> str:
        token = f"cred-{uuid.uuid4().hex}"
        self._secrets[token] = secret
        return token

    def has_secret(self, token: str | None) -> bool:
        return bool(token and token in self._secrets)

    def burn_secret(self, token: str | None) -> None:
        if token and token in self._secrets:
            self._secrets[token] = ""
            del self._secrets[token]

    @property
    def active_count(self) -> int:
        return len(self._secrets)


class RunController:
    """Unified Run Manager shared by CLI & TUI (REQ-RUN-01/03/04/05)."""

    def __init__(
        self,
        store: WorkspaceStore,
        executor: TargetExecutorProtocol | None = None,
        *,
        host_concurrency: int = DEFAULT_HOST_CONCURRENCY,
    ) -> None:
        self.store = store
        self.executor: TargetExecutorProtocol = executor or MockRemoteExecutor()
        self.vault = EphemeralCredentialVault()
        self._host_concurrency = host_concurrency
        self._run_fifo_sem = asyncio.Semaphore(1)
        self._queued_count = 0
        self._idempotency_index: dict[str, tuple[str, str]] = {}
        self._cancel_flags: dict[str, bool] = {}
        self.active_run_id: str | None = None

    def preview_run(self, plan: PlanDocument, binding: RunBinding) -> StaticRunPreview:
        templates = self.store.get_all_available_templates(plan)
        return build_static_preview(plan, binding, templates)

    def request_cancel(self, run_id: str) -> bool:
        if run_id in self._cancel_flags:
            self._cancel_flags[run_id] = True
            return True
        return False

    async def execute_run(
        self,
        plan: PlanDocument,
        binding: RunBinding,
        *,
        expected_plan_digest: str | None = None,
        on_phase: PhaseCallback | None = None,
    ) -> RunReport:
        """Freeze Run Intent, enforce security gates, execute across hosts, normalize, and archive."""
        # Always burn ephemeral credentials on exit regardless of success/exception (CHG-002)
        try:
            return await self._execute_run_inner(
                plan,
                binding,
                expected_plan_digest=expected_plan_digest,
                on_phase=on_phase,
            )
        finally:
            self.vault.burn_secret(binding.credential_token)

    async def _execute_run_inner(
        self,
        plan: PlanDocument,
        binding: RunBinding,
        *,
        expected_plan_digest: str | None,
        on_phase: PhaseCallback | None,
    ) -> RunReport:
        if plan.state not in (PlanState.RELEASED, PlanState.TRIAL_SNAPSHOT):
            raise ControllerSecurityError(
                "UNRUNNABLE_PLAN_STATE",
                f"方案状态为 {plan.state.value}；未校验草稿禁止运行，请先发布为 RELEASED 或生成 TRIAL_SNAPSHOT (REQ-PLAN-03)。",
            )

        actual_digest = plan.digest or plan.compute_semantic_digest()
        if expected_plan_digest and expected_plan_digest != actual_digest:
            raise ControllerSecurityError(
                "PLAN_DIGEST_MISMATCH",
                f"方案摘要不匹配 (--expected-plan-digest={expected_plan_digest} != 实际 {actual_digest})，拒绝执行 (TODO-018)。",
            )

        # Idempotency check (REQ-RUN-04)
        binding_payload_digest = compute_canonical_digest(
            binding.model_copy(update={"credential_token": None}).model_dump(mode="json")
        )
        if binding.idempotency_key in self._idempotency_index:
            prev_payload_digest, prev_run_id = self._idempotency_index[binding.idempotency_key]
            if prev_payload_digest != binding_payload_digest:
                raise ControllerSecurityError(
                    "IDEMPOTENCY_CONFLICT",
                    f"幂等请求键 '{binding.idempotency_key}' 已被用于不同运行负载，已拒绝重复冲突提交 (REQ-RUN-04)。",
                )
            existing_report = self.store.get_run_report(prev_run_id)
            if existing_report is not None:
                return existing_report

        if self._queued_count >= MAX_QUEUE_DEPTH:
            raise ControllerSecurityError(
                "QUEUE_FULL",
                f"待执行 FIFO 队列已满 ({MAX_QUEUE_DEPTH})，请稍后重试 (REQ-RUN-03)。",
            )

        # Freeze immutable copies of Plan & Binding before queueing (G03 / REQ-RUN-05/07)
        frozen_plan = plan.model_copy(deep=True)
        frozen_binding = binding.model_copy(deep=True)
        templates = self.store.get_all_available_templates(frozen_plan)

        # Pre-check root SSH approval gate (CHG-001 / REQ-SEC-02)
        hosts_by_name = {h.name: h for h in frozen_binding.inventory.hosts}
        active_filter = (
            set(frozen_binding.selected_hosts_filter)
            if frozen_binding.run_scope == RunScope.PARTIAL_SCOPE
            else None
        )

        host_roles_map: dict[str, list[str]] = {}
        for role in frozen_plan.roles:
            for h_name in frozen_binding.role_to_hosts.get(role.role_id, []):
                if h_name in hosts_by_name and (active_filter is None or h_name in active_filter):
                    host_roles_map.setdefault(h_name, []).append(role.role_id)

        root_hosts = sorted(
            h_name for h_name in host_roles_map if hosts_by_name[h_name].ssh_user == "root"
        )
        if root_hosts and not frozen_binding.approve_root_ssh:
            raise ControllerSecurityError(
                "ROOT_SSH_UNAPPROVED",
                f"目标主机 {', '.join(root_hosts)} 使用 root SSH 连接身份，未经显式审批严禁执行 (CHG-001 / REQ-SEC-02)。",
            )

        # Pre-compile static verification so CompilationConflictError / ParameterResolutionError surfaces cleanly
        for h_name, r_ids in host_roles_map.items():
            compile_host_artifacts(
                frozen_plan,
                frozen_binding,
                hosts_by_name[h_name],
                r_ids,
                templates,
                capability=None,
            )

        run_id = f"run-{uuid.uuid4().hex[:10]}"
        self._idempotency_index[frozen_binding.idempotency_key] = (
            binding_payload_digest,
            run_id,
        )
        self._cancel_flags[run_id] = False
        self._queued_count += 1

        try:
            async with self._run_fifo_sem:
                self._queued_count -= 1
                self.active_run_id = run_id
                return await self._run_active_hosts(
                    run_id=run_id,
                    plan=frozen_plan,
                    binding=frozen_binding,
                    templates=templates,
                    hosts_by_name=hosts_by_name,
                    host_roles_map=host_roles_map,
                    root_hosts=root_hosts,
                    actual_digest=actual_digest,
                    on_phase=on_phase,
                )
        finally:
            self.active_run_id = None
            self._cancel_flags.pop(run_id, None)

    async def _run_active_hosts(
        self,
        *,
        run_id: str,
        plan: PlanDocument,
        binding: RunBinding,
        templates: dict[str, TemplateSpec],
        hosts_by_name: dict[str, InventoryHost],
        host_roles_map: dict[str, list[str]],
        root_hosts: list[str],
        actual_digest: str,
        on_phase: PhaseCallback | None,
    ) -> RunReport:
        created_at = utc_now_iso()
        host_sem = asyncio.Semaphore(self._host_concurrency)
        target_names = list(host_roles_map.keys())
        fault_host_name = target_names[-1] if target_names else ""

        cleanup_records: list[CleanupRecord] = []
        diagnostics: list[ValidationDiagnostic] = []
        approved_priv = set(binding.approved_privileged_templates)

        def is_cancelled() -> bool:
            return bool(self._cancel_flags.get(run_id, False))

        async def run_one_host(h_name: str) -> HostRunResult:
            host = hosts_by_name[h_name]
            roles = host_roles_map[h_name]
            if on_phase:
                on_phase(h_name, HostPhase.QUEUED, "等待主机并发槽位")

            async with host_sem:
                if is_cancelled():
                    return HostRunResult(
                        host_name=h_name,
                        address=host.address,
                        ssh_user=host.ssh_user,
                        roles=roles,
                        phase=HostPhase.QUEUED,
                        host_error="CANCELLED_BEFORE_START",
                    )

                try:
                    cap = await self.executor.discover_capabilities(
                        host,
                        simulation_profile=binding.simulation_profile,
                        on_phase=on_phase,
                    )
                    std_art, priv_art = compile_host_artifacts(
                        plan,
                        binding,
                        host,
                        roles,
                        templates,  # type: ignore[arg-type]
                        capability=cap,
                    )

                    std_json = None
                    priv_json = None
                    unapproved_priv_ids: set[str] = set()

                    # Serial per-host order: STANDARD first, approved PRIVILEGED second (REQ-EXEC-09 / Section 9.3)
                    if std_art is not None:
                        std_json, cln_std = await self.executor.execute_goss_artifact(
                            run_id,
                            host,
                            cap,
                            std_art,
                            simulation_profile=binding.simulation_profile,
                            is_fault_target=(h_name == fault_host_name),
                            cancel_requested=is_cancelled,
                            on_phase=on_phase,
                        )
                        cleanup_records.append(cln_std)

                    if priv_art is not None and not is_cancelled():
                        needed_ids = {o.template_id for o in priv_art.origins}
                        unapproved_priv_ids = needed_ids - approved_priv
                        if not unapproved_priv_ids:
                            priv_json, cln_priv = await self.executor.execute_goss_artifact(
                                run_id,
                                host,
                                cap,
                                priv_art,
                                simulation_profile=binding.simulation_profile,
                                is_fault_target=(h_name == fault_host_name),
                                cancel_requested=is_cancelled,
                                on_phase=on_phase,
                            )
                            cleanup_records.append(cln_priv)

                    check_results = normalize_host_goss_results(
                        run_id=run_id,
                        plan=plan,
                        host_name=h_name,
                        ssh_user=host.ssh_user,
                        assigned_roles=roles,
                        templates=templates,  # type: ignore[arg-type]
                        capability=cap,
                        standard_artifact=std_art,
                        standard_goss_json=std_json,
                        privileged_artifact=priv_art,
                        privileged_goss_json=priv_json,
                        privileged_unapproved_ids=unapproved_priv_ids,
                    )
                    if on_phase:
                        on_phase(h_name, HostPhase.FINISHED, "单机检查与证据归一化完成")

                    return HostRunResult(
                        host_name=h_name,
                        address=host.address,
                        ssh_user=host.ssh_user,
                        roles=roles,
                        phase=HostPhase.FINISHED,
                        capability=cap,
                        standard_artifact_yaml=std_art.goss_yaml if std_art else None,
                        privileged_artifact_yaml=priv_art.goss_yaml if priv_art else None,
                        standard_artifact_digest=std_art.digest if std_art else None,
                        privileged_artifact_digest=priv_art.digest if priv_art else None,
                        execution_may_have_started=True,
                        check_results=check_results,
                    )
                except RemoteExecutionInterruptedError as exc:
                    cleanup_records.append(exc.cleanup_record)
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="HOST_EXECUTION_INTERRUPTED",
                            message=f"{h_name}: {exc}",
                        )
                    )
                    return HostRunResult(
                        host_name=h_name,
                        address=host.address,
                        ssh_user=host.ssh_user,
                        roles=roles,
                        phase=HostPhase.RUNNING_STANDARD,
                        execution_may_have_started=True,
                        host_error=str(exc),
                    )
                except (CompilationConflictError, ParameterResolutionError) as exc:
                    raise exc
                except Exception as exc:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="HOST_INFRA_ERROR",
                            message=f"{h_name}: {exc}",
                        )
                    )
                    return HostRunResult(
                        host_name=h_name,
                        address=host.address,
                        ssh_user=host.ssh_user,
                        roles=roles,
                        phase=HostPhase.CONNECTING,
                        host_error=str(exc),
                    )

        host_results = list(await asyncio.gather(*(run_one_host(h) for h in target_names)))

        if is_cancelled():
            run_status = RunStatus.CANCELLED
        elif any(h.host_error for h in host_results):
            run_status = (
                RunStatus.PARTIAL
                if any(not h.host_error for h in host_results)
                else RunStatus.FAILED
            )
        else:
            run_status = RunStatus.COMPLETED

        ctrl_checks, readiness, reason, gate_eligible = evaluate_control_plane_and_readiness(
            run_id=run_id,
            plan=plan,
            binding=binding,
            run_status=run_status,
            host_results=host_results,
            cleanup_records=cleanup_records,
        )

        report = RunReport(
            run_id=run_id,
            execution_backend=self.executor.backend_name,
            platform_verified_claim=False,
            plan_id=plan.plan_id,
            plan_name=plan.name,
            plan_version=plan.version,
            plan_state=plan.state,
            plan_digest=actual_digest,
            run_scope=binding.run_scope,
            run_status=run_status,
            readiness=readiness,
            readiness_reason=reason,
            gate_eligible=gate_eligible,
            simulation_profile=binding.simulation_profile,
            created_at=created_at,
            finished_at=utc_now_iso(),
            hosts=host_results,
            control_checks=ctrl_checks,
            cleanup_records=cleanup_records,
            diagnostics=diagnostics,
            audit=RunAuditRecord(
                idempotency_key=binding.idempotency_key,
                approved_privileged_templates=sorted(approved_priv),
                approve_root_ssh=binding.approve_root_ssh,
                high_risk_root_hosts=root_hosts,
            ),
        )
        self.store.save_run_report(report)
        return report

    async def resolve_cleanup_item(
        self,
        cleanup_id: str,
        *,
        confirm_kill_live_process: bool = False,
        waive_reason: str | None = None,
    ) -> CleanupRecord:
        records = {r.cleanup_id: r for r in self.store.list_cleanup_records()}
        target = records.get(cleanup_id)
        if target is None:
            raise KeyError(f"找不到清理待办项: {cleanup_id}")
        updated = await self.executor.resolve_cleanup(
            target,
            confirm_kill_live_process=confirm_kill_live_process,
            waive_reason=waive_reason,
        )
        self.store.save_cleanup_record(updated)
        return updated

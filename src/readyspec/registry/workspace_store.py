"""Atomic workspace persistence, Plan validation/release/import/export, and recovery registry."""

from __future__ import annotations

import fcntl
import json
import os
import re
import tempfile
from datetime import UTC, datetime, timedelta
from pathlib import Path

from pydantic import ValidationError

from readyspec.constants import (
    HISTORY_MAX_COMPLETED_RUNS,
    HISTORY_RETENTION_DAYS,
    MAX_PLAN_BYTES,
    MAX_REPORT_BYTES,
    CheckStatus,
    CleanupStatus,
    EvidenceDepth,
    HostPhase,
    Importance,
    PlanState,
    PrivilegeGroup,
    ReadinessStatus,
    RunScope,
    RunStatus,
    TemplateMaturity,
    TemplateSource,
)
from readyspec.domain.digest import compute_template_semantic_digest
from readyspec.domain.models import (
    CapabilityRecord,
    CheckEvidence,
    CheckInstance,
    CheckInstanceResult,
    CleanupRecord,
    CustomShellSpec,
    HostRunResult,
    ParameterSpec,
    ParamType,
    PlanDocument,
    ReleaseManifest,
    RoleSpec,
    RunAuditRecord,
    RunReport,
    ShellAssertionMode,
    ShellAssertionSpec,
    TemplateKind,
    TemplateSpec,
    ValidationDiagnostic,
    utc_now_iso,
)
from readyspec.domain.safe_yaml import SafeYamlError, dump_safe_yaml, load_safe_yaml
from readyspec.domain.schema_validator import validate_against_schema
from readyspec.registry.builtin_catalog import get_builtin_templates


class PlanLifecycleError(ValueError):
    """Raised when a Plan fails validation, conflict checks, or trust rules."""

    def __init__(
        self,
        code: str,
        message: str,
        diagnostics: list[ValidationDiagnostic] | None = None,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.diagnostics = diagnostics or []


def default_workspace_dir() -> Path:
    env_dir = os.environ.get("READYSPEC_WORKSPACE")
    if env_dir:
        return Path(env_dir).expanduser().resolve()
    return (Path.home() / ".local" / "share" / "readyspec" / "workspace").resolve()


def _atomic_write_bytes(target: Path, data: bytes, mode: int = 0o600) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    try:
        os.chmod(target.parent, 0o700)
    except OSError:
        pass
    fd, tmp_path_str = tempfile.mkstemp(
        prefix=f".{target.name}.", suffix=".tmp", dir=str(target.parent)
    )
    tmp_path = Path(tmp_path_str)
    try:
        os.chmod(fd, mode)
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, target)
    except Exception:
        tmp_path.unlink(missing_ok=True)
        raise


class WorkspaceLock:
    """Exclusive flock workspace lock ensuring a single active Run Manager (TODO-017)."""

    def __init__(self, lock_file: Path) -> None:
        self._lock_file = lock_file
        self._fd: int | None = None

    def acquire(self) -> None:
        self._lock_file.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(str(self._lock_file), os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            os.close(fd)
            raise RuntimeError(
                f"工作区 {self._lock_file.parent} 已被另一个活动控制器锁定 (.lock)。"
            ) from exc
        self._fd = fd

    def release(self) -> None:
        if self._fd is not None:
            try:
                fcntl.flock(self._fd, fcntl.LOCK_UN)
            finally:
                os.close(self._fd)
                self._fd = None


class WorkspaceStore:
    """Manages plans, templates, runs, exports, and crash-recovery records in a workspace."""

    def __init__(self, root: Path | None = None, *, seed_demo: bool = False) -> None:
        self.root = (root or default_workspace_dir()).resolve()
        self.drafts_dir = self.root / "plans" / "drafts"
        self.released_dir = self.root / "plans" / "released"
        self.templates_dir = self.root / "templates"
        self.runs_dir = self.root / "runs"
        self.recovery_dir = self.root / "recovery"
        self.exports_dir = self.root / "exports"
        self._init_dirs()
        if seed_demo:
            self.ensure_demo_seed()

    def _init_dirs(self) -> None:
        self.root.mkdir(parents=True, exist_ok=True)
        try:
            os.chmod(self.root, 0o700)
        except OSError:
            pass
        for sub in (
            self.drafts_dir,
            self.released_dir,
            self.templates_dir,
            self.runs_dir,
            self.recovery_dir,
            self.exports_dir,
        ):
            sub.mkdir(parents=True, exist_ok=True)
            try:
                os.chmod(sub, 0o700)
            except OSError:
                pass

    def create_lock(self) -> WorkspaceLock:
        return WorkspaceLock(self.root / ".lock")

    # ------------------------------------------------------------------
    # Template Registry
    # ------------------------------------------------------------------
    def list_user_templates(self) -> list[TemplateSpec]:
        templates: list[TemplateSpec] = []
        for path in sorted(self.templates_dir.glob("*.yaml")):
            raw = load_safe_yaml(path.read_bytes())
            templates.append(TemplateSpec.model_validate(raw))
        return templates

    def get_all_available_templates(
        self, plan: PlanDocument | None = None
    ) -> dict[str, TemplateSpec]:
        merged: dict[str, TemplateSpec] = dict(get_builtin_templates())
        for user_tpl in self.list_user_templates():
            merged[user_tpl.template_id] = user_tpl
        if plan is not None:
            for local_tpl in plan.embedded_templates:
                if local_tpl.source != TemplateSource.BUILTIN:
                    merged[local_tpl.template_id] = local_tpl
                elif local_tpl.template_id not in merged:
                    merged[local_tpl.template_id] = local_tpl
        return merged

    def promote_custom_template(
        self, plan: PlanDocument, local_template_id: str, public_template_id: str | None = None
    ) -> TemplateSpec:
        """Promote a plan-local custom Shell template to an immutable USER_PUBLIC template (REQ-TPL-15/16)."""
        target = next(
            (t for t in plan.embedded_templates if t.template_id == local_template_id), None
        )
        if target is None:
            raise PlanLifecycleError("TEMPLATE_NOT_FOUND", f"方案内找不到自定义模板: {local_template_id}")
        if target.privilege_group != PrivilegeGroup.STANDARD:
            raise PlanLifecycleError(
                "PRIVILEGE_ESCALATION_DENIED",
                "安全拒绝：用户模板永远只能为 STANDARD 权限，禁止通过提升公共模板获得 PRIVILEGED。",
            )
        new_id = public_template_id or (
            local_template_id
            if local_template_id.startswith("user.")
            else f"user.{local_template_id.removeprefix('local.')}"
        )
        promoted = target.model_copy(
            update={
                "template_id": new_id,
                "source": TemplateSource.USER_PUBLIC,
                "maturity": TemplateMaturity.INTEGRATED,
                "privilege_group": PrivilegeGroup.STANDARD,
            }
        ).with_digest()
        dest = self.templates_dir / f"{new_id}__{promoted.version}.yaml"
        if dest.exists():
            existing = TemplateSpec.model_validate(load_safe_yaml(dest.read_bytes()))
            if existing.digest != promoted.digest:
                raise PlanLifecycleError(
                    "TEMPLATE_DIGEST_CONFLICT",
                    f"用户公共模板 {new_id}@{promoted.version} 已存在且摘要不同，请升级模板版本号。",
                )
            return existing
        _atomic_write_bytes(dest, dump_safe_yaml(promoted.model_dump(mode="json")).encode("utf-8"))
        return promoted

    # ------------------------------------------------------------------
    # Static Validation & Lifecycle
    # ------------------------------------------------------------------
    def validate_plan(self, plan: PlanDocument) -> list[ValidationDiagnostic]:
        """Static validation of Plan Schema, templates, parameters, and trust rules (no SSH)."""
        diagnostics: list[ValidationDiagnostic] = []
        raw_plan = plan.model_dump(mode="json")
        for issue in validate_against_schema(raw_plan, "plan.v1.json"):
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="SCHEMA_INVALID",
                    message=f"Schema 校验错误 ({issue.path}): {issue.message}",
                )
            )

        builtin = get_builtin_templates()
        available = self.get_all_available_templates(plan)

        # Verify embedded templates trust & digest
        for emb in plan.embedded_templates:
            expected_digest = compute_template_semantic_digest(emb.model_dump(mode="json"))
            if emb.digest and emb.digest != expected_digest:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="EMBEDDED_TEMPLATE_TAMPERED",
                        message=f"内嵌模板 {emb.template_id} 摘要与实际内容不一致。",
                    )
                )
            if emb.source != TemplateSource.BUILTIN and emb.privilege_group == PrivilegeGroup.PRIVILEGED:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="USER_TEMPLATE_PRIVILEGED_FORBIDDEN",
                        message=f"用户/方案模板 {emb.template_id} 严禁声明 PRIVILEGED 特权组 (REQ-SEC-03)。",
                    )
                )
            if emb.privilege_group == PrivilegeGroup.PRIVILEGED:
                trusted = builtin.get(emb.template_id)
                if (
                    trusted is None
                    or trusted.version != emb.version
                    or trusted.digest != expected_digest
                ):
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="UNTRUSTED_PRIVILEGED_TEMPLATE",
                            message=f"内嵌特权模板 {emb.template_id}@{emb.version} 与本地受信内置注册表不匹配 (G02)。",
                        )
                    )

        if not plan.roles:
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="EMPTY_ROLES",
                    message="方案至少需要定义一个业务角色。",
                )
            )

        seen_role_ids: set[str] = set()
        seen_instance_ids: set[str] = set()
        enabled_required_count = 0

        for role in plan.roles:
            if role.role_id in seen_role_ids:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="DUPLICATE_ROLE_ID",
                        message=f"重复的业务角色标识: {role.role_id}",
                        role_id=role.role_id,
                    )
                )
            seen_role_ids.add(role.role_id)

            if role.max_nodes is not None and role.max_nodes < role.min_nodes:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="INVALID_NODE_COUNT_RANGE",
                        message=f"角色 {role.name} 的最大节点数 ({role.max_nodes}) 小于最小节点数 ({role.min_nodes})。",
                        role_id=role.role_id,
                    )
                )

            for inst in role.instances:
                if inst.instance_id in seen_instance_ids:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="DUPLICATE_INSTANCE_ID",
                            message=f"检查实例 ID '{inst.instance_id}' 重复（每个实例必须拥有独立稳定 ID）。",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )
                seen_instance_ids.add(inst.instance_id)

                tpl = available.get(inst.template_id)
                if tpl is None:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="UNKNOWN_TEMPLATE",
                            message=f"实例引用了未知模板 '{inst.template_id}'。",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )
                    continue

                if tpl.source == TemplateSource.BUILTIN and tpl.maturity != TemplateMaturity.INTEGRATED:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="ERROR",
                            code="TEMPLATE_NOT_INTEGRATED",
                            message=f"内置模板 {tpl.template_id} 当前状态为 {tpl.maturity}，不可加入正式方案 (REQ-TPL-04/05)。",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )

                if tpl.kind == TemplateKind.CONTROL:
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="WARNING",
                            code="CONTROL_TEMPLATE_AUTO_MANAGED",
                            message=f"{tpl.template_id} 由控制面自动聚合执行，无需作为角色实例手工挂载。",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )

                if (
                    tpl.kind == TemplateKind.CUSTOM_SHELL
                    and tpl.shell_spec is not None
                    and tpl.shell_spec.assertions.mode == ShellAssertionMode.CUSTOM_RESULT_V1
                ):
                    diagnostics.append(
                        ValidationDiagnostic(
                            severity="WARNING",
                            code="CONDITIONAL_PROTOCOL_M0",
                            message=f"实例 {inst.instance_id} 使用高级结构化结果协议 v1（待 M0 实证后方可正式发布，G06）。",
                            role_id=role.role_id,
                            instance_id=inst.instance_id,
                        )
                    )

                if inst.enabled and inst.importance == Importance.REQUIRED:
                    enabled_required_count += 1

                param_map = {p.name: p for p in tpl.parameters}
                for unknown_key in inst.params:
                    if unknown_key not in param_map:
                        diagnostics.append(
                            ValidationDiagnostic(
                                severity="ERROR",
                                code="UNKNOWN_PARAMETER",
                                message=f"未知参数 '{unknown_key}'（模板 {tpl.template_id} 未声明该参数）。",
                                role_id=role.role_id,
                                instance_id=inst.instance_id,
                                field_name=unknown_key,
                            )
                        )

                for ov_key in inst.overridable_params:
                    if ov_key not in param_map:
                        diagnostics.append(
                            ValidationDiagnostic(
                                severity="ERROR",
                                code="UNKNOWN_OVERRIDABLE_PARAM",
                                message=f"现场可覆盖列表包含未声明参数 '{ov_key}'。",
                                role_id=role.role_id,
                                instance_id=inst.instance_id,
                                field_name=ov_key,
                            )
                        )

                for p_spec in tpl.parameters:
                    self._validate_single_param(
                        p_spec, inst, role.role_id, diagnostics
                    )

        if enabled_required_count == 0:
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="NO_REQUIRED_CHECKS",
                    message="方案中没有任何已启用的『必需 (REQUIRED)』检查实例，严禁发布或输出 READY (REQ-RESULT-05)。",
                )
            )

        return diagnostics

    def _validate_single_param(
        self,
        p_spec: ParameterSpec,
        inst: CheckInstance,
        role_id: str,
        diagnostics: list[ValidationDiagnostic],
    ) -> None:
        is_explicit_null = p_spec.name in inst.explicit_null_params
        if is_explicit_null:
            if not p_spec.allow_null:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_NULL_FORBIDDEN",
                        message=f"参数 '{p_spec.label_zh}' ({p_spec.name}) 不允许显式设为空值。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )
            return

        val = inst.params.get(p_spec.name, p_spec.default)
        if val is None or (isinstance(val, str) and val == "" and p_spec.required):
            if p_spec.required:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_REQUIRED_MISSING",
                        message=f"缺少必填参数 '{p_spec.label_zh}' ({p_spec.name})。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )
            return

        if p_spec.param_type in (ParamType.INTEGER, ParamType.PORT):
            if isinstance(val, bool) or not isinstance(val, int):
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_TYPE_MISMATCH",
                        message=f"参数 '{p_spec.name}' 必须为整数。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )
                return
            num_val = float(val)
        elif p_spec.param_type == ParamType.NUMBER:
            if isinstance(val, bool) or not isinstance(val, (int, float)):
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_TYPE_MISMATCH",
                        message=f"参数 '{p_spec.name}' 必须为数值。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )
                return
            num_val = float(val)
        else:
            num_val = None

        if num_val is not None:
            if p_spec.min_value is not None and num_val < p_spec.min_value:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_BELOW_MIN",
                        message=f"参数 '{p_spec.name}' 值 {val} 小于允许下限 {p_spec.min_value}。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )
            if p_spec.max_value is not None and num_val > p_spec.max_value:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_ABOVE_MAX",
                        message=f"参数 '{p_spec.name}' 值 {val} 超过允许上限 {p_spec.max_value}。",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )

        if p_spec.param_type == ParamType.BOOLEAN and not isinstance(val, bool):
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="PARAM_TYPE_MISMATCH",
                    message=f"参数 '{p_spec.name}' 必须为布尔值。",
                    role_id=role_id,
                    instance_id=inst.instance_id,
                    field_name=p_spec.name,
                )
            )
        if p_spec.param_type == ParamType.PATH and (
            not isinstance(val, str) or not val.startswith("/") or ".." in val.split("/")
        ):
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="PARAM_INVALID_PATH",
                    message=f"参数 '{p_spec.name}' 必须为不含 '..' 穿越的规范绝对路径。",
                    role_id=role_id,
                    instance_id=inst.instance_id,
                    field_name=p_spec.name,
                )
            )
        if p_spec.enum_values and str(val) not in p_spec.enum_values:
            diagnostics.append(
                ValidationDiagnostic(
                    severity="ERROR",
                    code="PARAM_INVALID_ENUM",
                    message=f"参数 '{p_spec.name}' 值 '{val}' 不在允许枚举 {p_spec.enum_values} 内。",
                    role_id=role_id,
                    instance_id=inst.instance_id,
                    field_name=p_spec.name,
                )
            )
        if p_spec.param_type == ParamType.REGEX and isinstance(val, str):
            try:
                re.compile(val)
            except re.error as exc:
                diagnostics.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="PARAM_INVALID_REGEX",
                        message=f"参数 '{p_spec.name}' 正则表达式无效: {exc}",
                        role_id=role_id,
                        instance_id=inst.instance_id,
                        field_name=p_spec.name,
                    )
                )

    def _lock_plan_dependencies(self, plan: PlanDocument, target_state: PlanState) -> PlanDocument:
        available = self.get_all_available_templates(plan)
        used_ids: set[str] = set()
        updated_roles: list[RoleSpec] = []
        for role in plan.roles:
            new_instances: list[CheckInstance] = []
            for inst in role.instances:
                tpl = available[inst.template_id].with_digest()
                used_ids.add(tpl.template_id)
                new_instances.append(
                    inst.model_copy(
                        update={
                            "template_version": tpl.version,
                            "template_digest": tpl.digest,
                        }
                    )
                )
            updated_roles.append(role.model_copy(update={"instances": new_instances}))

        embedded: list[TemplateSpec] = []
        lock_map: dict[str, str] = {}
        for tid in sorted(used_ids):
            tpl = available[tid].with_digest()
            embedded.append(tpl)
            assert tpl.digest is not None
            lock_map[f"{tpl.template_id}@{tpl.version}"] = tpl.digest

        staged = plan.model_copy(
            update={
                "state": target_state,
                "roles": updated_roles,
                "embedded_templates": embedded,
                "template_lock": lock_map,
                "updated_at": utc_now_iso(),
            }
        )
        semantic_digest = staged.compute_semantic_digest()
        manifest = ReleaseManifest(
            plan_digest=semantic_digest,
            template_digests=lock_map,
            released_at=utc_now_iso(),
        )
        return staged.model_copy(
            update={"digest": semantic_digest, "release_manifest": manifest}
        )

    def save_draft(self, plan: PlanDocument) -> PlanDocument:
        """Save an editable draft (allows incomplete drafts per G01 / REQ-PLAN-03)."""
        draft = plan.model_copy(
            update={
                "state": PlanState.DRAFT,
                "release_manifest": None,
                "updated_at": utc_now_iso(),
            }
        ).with_updated_digest()
        path = self.drafts_dir / f"{draft.plan_id}.yaml"
        _atomic_write_bytes(path, dump_safe_yaml(draft.model_dump(mode="json")).encode("utf-8"))
        return draft

    def create_trial_snapshot(self, plan: PlanDocument) -> PlanDocument:
        """Validate a draft and freeze an in-memory/ephemeral TRIAL_SNAPSHOT for dev trial runs (CHG-002)."""
        diags = self.validate_plan(plan)
        errors = [d for d in diags if d.severity == "ERROR"]
        if errors:
            raise PlanLifecycleError(
                "DRAFT_INVALID_FOR_TRIAL",
                f"草稿存在 {len(errors)} 处校验错误，未通过静态校验前禁止试跑。",
                errors,
            )
        return self._lock_plan_dependencies(plan, PlanState.TRIAL_SNAPSHOT)

    def release_plan(
        self, plan: PlanDocument, *, version: str | None = None
    ) -> PlanDocument:
        """Publish an immutable self-contained RELEASED plan (REQ-PLAN-04/05/06/07)."""
        candidate = plan.model_copy(update={"version": version or plan.version})
        diags = self.validate_plan(candidate)
        # G06: Conditional M0 Custom Result Protocol v1 blocks formal RELEASED until M0 signoff
        for d in diags:
            if d.code == "CONDITIONAL_PROTOCOL_M0":
                diags.append(
                    ValidationDiagnostic(
                        severity="ERROR",
                        code="M0_PROTOCOL_UNVERIFIED",
                        message="依赖未完成 M0 实测的结构化结果协议 v1，暂不允许发布为正式 RELEASED (G06)。",
                        role_id=d.role_id,
                        instance_id=d.instance_id,
                    )
                )
                break
        errors = [d for d in diags if d.severity == "ERROR"]
        if errors:
            raise PlanLifecycleError(
                "RELEASE_VALIDATION_FAILED",
                f"正式发布静态校验未通过（共 {len(errors)} 项错误）。",
                errors,
            )

        released = self._lock_plan_dependencies(candidate, PlanState.RELEASED)
        dest = self.released_dir / f"{released.plan_id}__{released.version}.yaml"
        if dest.exists():
            existing = PlanDocument.model_validate(load_safe_yaml(dest.read_bytes()))
            if existing.digest != released.digest:
                raise PlanLifecycleError(
                    "PLAN_DIGEST_CONFLICT",
                    f"已存在相同版本号 {released.plan_id}@{released.version} 但摘要不同 ({existing.digest} != {released.digest})；已发布版本不可原地修改，请升级版本号 (REQ-PLAN-05/06)。",
                )
            return existing

        _atomic_write_bytes(dest, dump_safe_yaml(released.model_dump(mode="json")).encode("utf-8"))
        return released

    def export_plan_yaml(self, plan: PlanDocument, dest_path: Path | None = None) -> str:
        """Export a self-contained YAML document (REQ-PLAN-02/07/08)."""
        if plan.state == PlanState.RELEASED and plan.embedded_templates:
            export_doc = plan
        else:
            export_doc = self._lock_plan_dependencies(plan, plan.state)
        yaml_text = dump_safe_yaml(export_doc.model_dump(mode="json"))
        if dest_path is not None:
            _atomic_write_bytes(dest_path, yaml_text.encode("utf-8"), mode=0o644)
        return yaml_text

    def import_plan_yaml(
        self, yaml_content: str | bytes, *, save_to_workspace: bool = True
    ) -> PlanDocument:
        """Safely parse, schema-validate, verify digests & privileged trust, and import a Plan (Section 12.2)."""
        try:
            raw_obj = load_safe_yaml(yaml_content, max_bytes=MAX_PLAN_BYTES)
        except SafeYamlError as exc:
            raise PlanLifecycleError(exc.code, exc.message) from exc

        if not isinstance(raw_obj, dict):
            raise PlanLifecycleError("PLAN_NOT_OBJECT", "导入内容不是合法的方案对象。")

        schema_issues = validate_against_schema(raw_obj, "plan.v1.json")
        if schema_issues:
            diags = [
                ValidationDiagnostic(
                    severity="ERROR",
                    code="SCHEMA_INVALID",
                    message=f"{iss.path}: {iss.message}",
                )
                for iss in schema_issues
            ]
            raise PlanLifecycleError(
                "IMPORT_SCHEMA_INVALID",
                f"导入方案不符合 Plan Schema v1: {schema_issues[0].path}: {schema_issues[0].message}",
                diags,
            )

        try:
            imported = PlanDocument.model_validate(raw_obj)
        except ValidationError as exc:
            raise PlanLifecycleError("IMPORT_MODEL_INVALID", f"方案模型校验失败: {exc}") from exc

        computed_digest = imported.compute_semantic_digest()
        if imported.digest and imported.digest != computed_digest:
            raise PlanLifecycleError(
                "IMPORT_DIGEST_MISMATCH",
                f"方案文件声明摘要 ({imported.digest}) 与规范化计算摘要 ({computed_digest}) 不一致，文件可能被篡改。",
            )

        diags = self.validate_plan(imported)
        errors = [d for d in diags if d.severity == "ERROR"]
        if errors:
            raise PlanLifecycleError(
                errors[0].code,
                f"导入审查拒绝: {errors[0].message}",
                errors,
            )

        imported = imported.model_copy(update={"digest": computed_digest})

        if save_to_workspace:
            if imported.state == PlanState.RELEASED:
                dest = self.released_dir / f"{imported.plan_id}__{imported.version}.yaml"
                if dest.exists():
                    existing = PlanDocument.model_validate(load_safe_yaml(dest.read_bytes()))
                    if existing.digest != imported.digest:
                        raise PlanLifecycleError(
                            "PLAN_DIGEST_CONFLICT",
                            f"工作区已存在 {imported.plan_id}@{imported.version} 且摘要不同，已拒绝静默覆盖 (REQ-PLAN-05)。",
                        )
                    return existing
                _atomic_write_bytes(
                    dest, dump_safe_yaml(imported.model_dump(mode="json")).encode("utf-8")
                )
            else:
                draft_dest = self.drafts_dir / f"{imported.plan_id}.yaml"
                if draft_dest.exists():
                    existing_draft = PlanDocument.model_validate(
                        load_safe_yaml(draft_dest.read_bytes())
                    )
                    if existing_draft.digest != imported.digest:
                        # Save under versioned draft ID or raise explicit conflict if same version
                        if existing_draft.version == imported.version:
                            raise PlanLifecycleError(
                                "PLAN_DIGEST_CONFLICT",
                                f"本地草稿 {imported.plan_id}@{imported.version} 已存在且内容不同，请先重命名或另存新版本。",
                            )
                self.save_draft(imported)

        return imported

    def list_plans(self) -> list[PlanDocument]:
        plans: list[PlanDocument] = []
        for path in sorted(self.released_dir.glob("*.yaml")):
            plans.append(PlanDocument.model_validate(load_safe_yaml(path.read_bytes())))
        for path in sorted(self.drafts_dir.glob("*.yaml")):
            plans.append(PlanDocument.model_validate(load_safe_yaml(path.read_bytes())))
        return plans

    def get_plan(
        self, plan_id: str, version: str | None = None, *, prefer_released: bool = True
    ) -> PlanDocument | None:
        if version is not None:
            rel_path = self.released_dir / f"{plan_id}__{version}.yaml"
            if rel_path.exists():
                return PlanDocument.model_validate(load_safe_yaml(rel_path.read_bytes()))
            draft_path = self.drafts_dir / f"{plan_id}.yaml"
            if draft_path.exists():
                draft = PlanDocument.model_validate(load_safe_yaml(draft_path.read_bytes()))
                if draft.version == version:
                    return draft
            return None

        if prefer_released:
            matches = sorted(self.released_dir.glob(f"{plan_id}__*.yaml"))
            if matches:
                return PlanDocument.model_validate(load_safe_yaml(matches[-1].read_bytes()))

        draft_path = self.drafts_dir / f"{plan_id}.yaml"
        if draft_path.exists():
            return PlanDocument.model_validate(load_safe_yaml(draft_path.read_bytes()))

        matches = sorted(self.released_dir.glob(f"{plan_id}__*.yaml"))
        if matches:
            return PlanDocument.model_validate(load_safe_yaml(matches[-1].read_bytes()))
        return None

    # ------------------------------------------------------------------
    # Run Reports & Cleanup Records
    # ------------------------------------------------------------------
    def save_run_report(self, report: RunReport) -> Path:
        payload = json.dumps(report.model_dump(mode="json"), ensure_ascii=False, indent=2).encode(
            "utf-8"
        )
        if len(payload) > MAX_REPORT_BYTES:
            raise ValueError(f"运行报告超过大小上限 ({MAX_REPORT_BYTES} 字节)")
        run_folder = self.runs_dir / report.run_id
        run_folder.mkdir(parents=True, exist_ok=True)
        report_path = run_folder / "report.json"
        _atomic_write_bytes(report_path, payload, mode=0o600)
        for cln in report.cleanup_records:
            if cln.status != CleanupStatus.CLEAN:
                self.save_cleanup_record(cln)
        self.prune_run_history()
        return report_path

    def get_run_report(self, run_id: str) -> RunReport | None:
        report_path = self.runs_dir / run_id / "report.json"
        if not report_path.exists():
            return None
        return RunReport.model_validate_json(report_path.read_bytes())

    def list_run_reports(self) -> list[RunReport]:
        reports: list[RunReport] = []
        for report_file in self.runs_dir.glob("*/report.json"):
            try:
                reports.append(RunReport.model_validate_json(report_file.read_bytes()))
            except Exception:
                continue
        reports.sort(key=lambda r: (r.created_at, r.run_id), reverse=True)
        return reports

    def save_cleanup_record(self, record: CleanupRecord) -> None:
        dest = self.recovery_dir / f"{record.cleanup_id}.json"
        data = json.dumps(record.model_dump(mode="json"), ensure_ascii=False, indent=2).encode(
            "utf-8"
        )
        _atomic_write_bytes(dest, data, mode=0o600)

    def list_cleanup_records(self) -> list[CleanupRecord]:
        items: list[CleanupRecord] = []
        for path in sorted(self.recovery_dir.glob("*.json")):
            try:
                items.append(CleanupRecord.model_validate_json(path.read_bytes()))
            except Exception:
                continue
        return items

    def pending_cleanup_records(self) -> list[CleanupRecord]:
        return [
            r
            for r in self.list_cleanup_records()
            if r.status in (CleanupStatus.PENDING, CleanupStatus.UNKNOWN, CleanupStatus.FAILED)
        ]

    def prune_run_history(self) -> None:
        """Retain max 30 days & 100 ordinary completed runs; protect unresolved cleanups (REQ-RUN-12 / G05)."""
        protected_run_ids = {r.run_id for r in self.pending_cleanup_records()}
        reports = self.list_run_reports()
        cutoff = datetime.now(UTC) - timedelta(days=HISTORY_RETENTION_DAYS)

        ordinary_completed: list[RunReport] = []
        for rep in reports:
            if rep.run_id in protected_run_ids:
                continue
            if rep.run_status != RunStatus.COMPLETED:
                continue
            ordinary_completed.append(rep)

        for idx, rep in enumerate(ordinary_completed):
            should_prune = idx >= HISTORY_MAX_COMPLETED_RUNS
            if not should_prune and rep.finished_at:
                try:
                    ts = datetime.fromisoformat(rep.finished_at.replace("Z", "+00:00"))
                    if ts < cutoff:
                        should_prune = True
                except ValueError:
                    pass
            if should_prune:
                run_folder = self.runs_dir / rep.run_id
                for child in run_folder.glob("*"):
                    child.unlink(missing_ok=True)
                try:
                    run_folder.rmdir()
                except OSError:
                    pass

    # ------------------------------------------------------------------
    # Initial Demo Seed (Out-of-the-box interactive prototype experience)
    # ------------------------------------------------------------------
    def ensure_demo_seed(self) -> None:
        if any(self.released_dir.glob("*.yaml")) or any(self.drafts_dir.glob("*.yaml")):
            return

        custom_tpl = TemplateSpec(
            template_id="local.ulimit_app_conf",
            version="1.0.0",
            name_zh="应用自定义目录可写前检脚本",
            name_en="Custom App Directory Pre-check",
            domain_code="D",
            group_code="CUSTOM",
            description="使用只读环境变量 $RS_PARAM_DATA_DIR 检查目录状态及句柄软限制配置。",
            source=TemplateSource.PLAN_LOCAL,
            maturity=TemplateMaturity.INTEGRATED,
            privilege_group=PrivilegeGroup.STANDARD,
            evidence_depth=EvidenceDepth.CONFIGURATION,
            kind=TemplateKind.CUSTOM_SHELL,
            keywords=["custom", "shell", "目录"],
            parameters=[
                ParameterSpec(
                    name="data_dir",
                    label_zh="业务数据根目录",
                    param_type=ParamType.PATH,
                    default="/data/order",
                    overridable_by_default=True,
                )
            ],
            shell_spec=CustomShellSpec(
                interpreter="/bin/sh",
                script='echo "data_dir=${RS_PARAM_DATA_DIR} status=ok"',
                timeout_sec=15,
                assertions=ShellAssertionSpec(
                    mode=ShellAssertionMode.BASIC,
                    expected_exit_code=0,
                    stdout_contains="status=ok",
                ),
            ),
        ).with_digest()

        order_plan = PlanDocument(
            plan_id="order-platform",
            name="订单核心系统部署前环境检查",
            software_name="OrderCore Enterprise",
            software_version="3.4.0",
            version="1.0.0",
            description="包含 Web 接入层与 DB 存储层的生产环境前置检查（含特权 sshd -T 基线与现场可调数据盘路径）。",
            roles=[
                RoleSpec(
                    role_id="web",
                    name="Web 接入节点",
                    min_nodes=2,
                    instances=[
                        CheckInstance(
                            instance_id="web_cpu_arch",
                            title="CPU 架构要求 x86_64",
                            template_id="H01",
                            params={"allowed_arch": ["x86_64"]},
                        ),
                        CheckInstance(
                            instance_id="web_mem_8g",
                            title="Web 节点最低 8 GiB 内存",
                            template_id="H03",
                            params={"min_gib": 8.0},
                        ),
                        CheckInstance(
                            instance_id="web_port_8080_free",
                            title="业务监听端口 8080 空闲未占用",
                            template_id="N02",
                            params={"protocol": "tcp", "port": 8080, "should_listen": False},
                            overridable_params=["port"],
                        ),
                        CheckInstance(
                            instance_id="web_ntp_sync",
                            title="系统时钟已完成 NTP/Chrony 同步",
                            template_id="R05",
                            params={"require_synchronized": True},
                        ),
                        CheckInstance(
                            instance_id="web_sshd_baseline",
                            title="SSH 生效安全基线 MaxAuthTries",
                            template_id="A03",
                            importance=Importance.WARNING,
                            params={"option_key": "maxauthtries", "expected_value": "4"},
                        ),
                    ],
                ),
                RoleSpec(
                    role_id="db",
                    name="DB 数据库节点",
                    min_nodes=1,
                    allow_colocation=False,
                    instances=[
                        CheckInstance(
                            instance_id="db_mem_16g",
                            title="DB 节点最低 16 GiB 物理内存",
                            template_id="H03",
                            params={"min_gib": 16.0},
                        ),
                        CheckInstance(
                            instance_id="db_swap_off",
                            title="数据库节点禁用 Swap",
                            template_id="H04",
                            params={"policy": "disabled", "min_mib": 0},
                        ),
                        CheckInstance(
                            instance_id="db_data_space",
                            title="数据盘剩余空间 >= 100 GiB",
                            template_id="S04",
                            params={"path": "/data", "min_free_gib": 100.0},
                            overridable_params=["path", "min_free_gib"],
                        ),
                        CheckInstance(
                            instance_id="db_custom_dir_check",
                            title="自定义数据目录前检脚本",
                            template_id="local.ulimit_app_conf",
                            params={"data_dir": "/data/order"},
                            overridable_params=["data_dir"],
                        ),
                    ],
                ),
            ],
            embedded_templates=[custom_tpl],
        )
        released_order = self.release_plan(order_plan, version="1.0.0")

        draft_plan = PlanDocument(
            plan_id="payment-gateway",
            name="支付网关前置检查方案（草稿）",
            software_name="PayGateway",
            software_version="2.1.0",
            version="0.2.0",
            state=PlanState.DRAFT,
            description="正在编写中的网关部署检查草稿，可在方案工作台中增删角色与实例并直接试跑。",
            roles=[
                RoleSpec(
                    role_id="gateway",
                    name="API 网关节点",
                    min_nodes=1,
                    instances=[
                        CheckInstance(
                            instance_id="gw_cores_4",
                            title="至少 4 核逻辑 CPU",
                            template_id="H02",
                            params={"min_cores": 4},
                        ),
                        CheckInstance(
                            instance_id="gw_port_8443",
                            title="HTTPS 8443 端口空闲",
                            template_id="N02",
                            params={"protocol": "tcp", "port": 8443, "should_listen": False},
                            overridable_params=["port"],
                        ),
                        CheckInstance(
                            instance_id="gw_elf_openssl",
                            title="静态 ELF 校验 OpenSSL 动态库",
                            template_id="D04",
                            params={"elf_path": "/usr/bin/openssl", "required_soname": "libc.so.6"},
                            overridable_params=["elf_path"],
                        ),
                    ],
                )
            ],
        )
        self.save_draft(draft_plan)

        # Seed one historical run report + one pending cleanup record with live PID fingerprint
        pending_cleanup = CleanupRecord(
            cleanup_id="cln-demo-001",
            run_id="run-demo-001",
            host_name="db-node-01",
            remote_dir="/tmp/readyspec_run_demo_001_db01",
            privilege_group=PrivilegeGroup.STANDARD,
            owner_token="tok_9f81c3a40e7142b8a91c5d0f2238ab12",
            boot_id="7a4e1c9d-41b2-4e8a-9c12-001122334455",
            process_pid=28491,
            process_starttime_ticks=9182736,
            live_process_detected=True,
            live_process_cmdline="/tmp/readyspec_run_demo_001_db01/goss -g goss_standard.yaml validate --format json",
            status=CleanupStatus.PENDING,
        )
        self.save_cleanup_record(pending_cleanup)

        assert released_order.digest is not None
        sample_report = RunReport(
            run_id="run-demo-001",
            execution_backend="mock_simulator",
            platform_verified_claim=False,
            plan_id=released_order.plan_id,
            plan_name=released_order.name,
            plan_version=released_order.version,
            plan_state=PlanState.RELEASED,
            plan_digest=released_order.digest,
            run_scope=RunScope.FULL,
            run_status=RunStatus.COMPLETED,
            readiness=ReadinessStatus.NOT_READY,
            readiness_reason="存在 1 项必需检查明确 FAIL（db-node-01 数据盘剩余空间仅 42.5 GiB < 100.0 GiB）。",
            gate_eligible=False,
            simulation_profile="mixed_realistic",
            finished_at=utc_now_iso(),
            hosts=[
                HostRunResult(
                    host_name="web-node-01",
                    address="10.20.1.11",
                    ssh_user="ops",
                    roles=["web"],
                    phase=HostPhase.FINISHED,
                    capability=CapabilityRecord(
                        host_name="web-node-01",
                        os_id="ubuntu",
                        os_version="24.04",
                        arch="x86_64",
                        kernel_release="6.8.0-45-generic",
                        ssh_user="ops",
                        available_tools=["uname", "df", "ip", "systemctl", "timedatectl", "journalctl", "readelf", "ldconfig", "sha256sum"],
                        machine_id="m-web-01",
                        boot_id="b-web-01",
                        ssh_host_key_fp="SHA256:demoWeb01Key",
                    ),
                    check_results=[
                        CheckInstanceResult(
                            instance_id="web_port_8080_free",
                            title="业务监听端口 8080 空闲未占用",
                            role_id="web",
                            template_id="N02",
                            template_version="1.0.0",
                            importance=Importance.REQUIRED,
                            privilege_group=PrivilegeGroup.STANDARD,
                            status=CheckStatus.PASS,
                            summary="tcp:8080 当前未监听（空闲可用）",
                            evidences=[
                                CheckEvidence(
                                    run_id="run-demo-001",
                                    host_name="web-node-01",
                                    role_id="web",
                                    instance_id="web_port_8080_free",
                                    template_id="N02",
                                    template_version="1.0.0",
                                    template_digest="sha256:demo",
                                    goss_subrule_id="port:tcp:8080:listening",
                                    execution_identity="ops",
                                    privilege_group=PrivilegeGroup.STANDARD,
                                    expected="listening == false",
                                    observed="false",
                                    evidence_depth=EvidenceDepth.EFFECTIVE_STATE,
                                    source="goss.port",
                                )
                            ],
                        )
                    ],
                ),
                HostRunResult(
                    host_name="db-node-01",
                    address="10.20.2.21",
                    ssh_user="ops",
                    roles=["db"],
                    phase=HostPhase.FINISHED,
                    capability=CapabilityRecord(
                        host_name="db-node-01",
                        os_id="rocky",
                        os_version="9",
                        arch="x86_64",
                        kernel_release="5.14.0-427.el9.x86_64",
                        ssh_user="ops",
                        available_tools=["uname", "df", "ip", "systemctl", "timedatectl", "journalctl", "readelf", "ldconfig", "sha256sum"],
                        machine_id="m-db-01",
                        boot_id="7a4e1c9d-41b2-4e8a-9c12-001122334455",
                        ssh_host_key_fp="SHA256:demoDb01Key",
                    ),
                    check_results=[
                        CheckInstanceResult(
                            instance_id="db_data_space",
                            title="数据盘剩余空间 >= 100 GiB",
                            role_id="db",
                            template_id="S04",
                            template_version="1.0.0",
                            importance=Importance.REQUIRED,
                            privilege_group=PrivilegeGroup.STANDARD,
                            status=CheckStatus.FAIL,
                            summary="/data 可用空间不足：实测 42.5 GiB，低于必需阈值 100.0 GiB",
                            remediation_hint="请扩容 /data 挂载卷或通过现场参数将目录调整至大容量挂载点后复测。",
                            evidences=[
                                CheckEvidence(
                                    run_id="run-demo-001",
                                    host_name="db-node-01",
                                    role_id="db",
                                    instance_id="db_data_space",
                                    template_id="S04",
                                    template_version="1.0.0",
                                    template_digest="sha256:demo",
                                    goss_subrule_id="command:rs_db_data_space:stdout",
                                    execution_identity="ops",
                                    privilege_group=PrivilegeGroup.STANDARD,
                                    expected="avail_gib >= 100.0 GiB",
                                    observed="42.5 GiB (/dev/nvme1n1 on /data)",
                                    evidence_depth=EvidenceDepth.METADATA,
                                    source="df -Pk /data",
                                )
                            ],
                        )
                    ],
                ),
            ],
            cleanup_records=[pending_cleanup],
            audit=RunAuditRecord(
                idempotency_key="req-demo-seed-001",
                approved_privileged_templates=["A03"],
                approve_root_ssh=False,
            ),
        )
        self.save_run_report(sample_report)

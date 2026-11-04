"""Pydantic v2 domain models for ReadySpec V1."""

from __future__ import annotations

import copy
import re
import uuid
from datetime import UTC, datetime
from enum import StrEnum
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from readyspec.constants import (
    COMPILER_VERSION,
    DEFAULT_SHELL_TIMEOUT_SEC,
    PLAN_SCHEMA_VERSION,
    PRODUCT_VERSION,
    REPORT_SCHEMA_VERSION,
    TEMPLATE_SCHEMA_VERSION,
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
from readyspec.domain.digest import (
    compute_plan_semantic_digest,
    compute_template_semantic_digest,
)

SAFE_ID_RE = re.compile(r"^[a-zA-Z0-9._-]+$")


def utc_now_iso() -> str:
    return datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


class ParamType(StrEnum):
    STRING = "string"
    BOOLEAN = "boolean"
    INTEGER = "integer"
    NUMBER = "number"
    CAPACITY = "capacity"
    DURATION = "duration"
    PATH = "path"
    VERSION_CONSTRAINT = "version_constraint"
    IP_OR_CIDR = "ip_or_cidr"
    PORT = "port"
    ENUM = "enum"
    LIST = "list"
    MAP = "map"
    REGEX = "regex"
    SECRET_REF = "secret_ref"


class TemplateKind(StrEnum):
    NATIVE = "NATIVE"
    SHELL = "SHELL"
    FLAT = "FLAT"
    CONTROL = "CONTROL"
    CUSTOM_SHELL = "CUSTOM_SHELL"


class NumericOperator(StrEnum):
    EQ = "=="
    NE = "!="
    GE = ">="
    GT = ">"
    LE = "<="
    LT = "<"
    RANGE = "range"


class ShellAssertionMode(StrEnum):
    BASIC = "basic"
    CUSTOM_RESULT_V1 = "custom_result_v1"


class ParameterSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str
    label_zh: str
    param_type: ParamType = ParamType.STRING
    required: bool = True
    default: Any = None
    unit: str = ""
    min_value: float | None = None
    max_value: float | None = None
    enum_values: list[str] = Field(default_factory=list)
    regex_pattern: str | None = None
    allow_null: bool = False
    sensitive: bool = False
    overridable_by_default: bool = False
    description: str = ""


class ShellAssertionSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    mode: ShellAssertionMode = ShellAssertionMode.BASIC
    expected_exit_code: int = 0
    stdout_contains: str | None = None
    stdout_not_contains: str | None = None
    stdout_exact: str | None = None
    stdout_regex: str | None = None
    stderr_not_contains: str | None = None
    numeric_op: NumericOperator | None = None
    numeric_threshold: float | None = None
    numeric_max: float | None = None
    unit: str = ""


class CustomShellSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    interpreter: str = "/bin/sh"
    script: str
    timeout_sec: int = Field(default=DEFAULT_SHELL_TIMEOUT_SEC, ge=1, le=120)
    assertions: ShellAssertionSpec = Field(default_factory=ShellAssertionSpec)
    side_effect_note: str = "自定义命令由非特权连接身份执行（无强制只读沙箱）"


class TemplateSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: str = TEMPLATE_SCHEMA_VERSION
    template_id: str
    version: str = "1.0.0"
    name_zh: str
    name_en: str
    domain_code: str
    group_code: str
    description: str = ""
    source: TemplateSource = TemplateSource.BUILTIN
    maturity: TemplateMaturity = TemplateMaturity.INTEGRATED
    privilege_group: PrivilegeGroup = PrivilegeGroup.STANDARD
    evidence_depth: EvidenceDepth = EvidenceDepth.METADATA
    kind: TemplateKind = TemplateKind.NATIVE
    keywords: list[str] = Field(default_factory=list)
    dependencies: list[str] = Field(default_factory=list)
    conditional_notes: str = ""
    active_network_probe: bool = False
    parameters: list[ParameterSpec] = Field(default_factory=list)
    shell_spec: CustomShellSpec | None = None
    digest: str | None = None

    def with_digest(self) -> TemplateSpec:
        raw = self.model_dump(mode="json")
        digest = compute_template_semantic_digest(raw)
        return self.model_copy(update={"digest": digest})


class CatalogGroup(BaseModel):
    """Represents one of the 54 check groups across the 9 domains (Section 2.3)."""

    model_config = ConfigDict(extra="forbid")

    domain_code: str
    domain_name_zh: str
    group_code: str
    group_name_zh: str
    group_name_en: str
    maturity: TemplateMaturity
    template_id: str | None = None
    summary: str = ""


class CheckInstance(BaseModel):
    model_config = ConfigDict(extra="forbid")

    instance_id: str
    title: str = ""
    template_id: str
    template_version: str = "1.0.0"
    template_digest: str | None = None
    importance: Importance = Importance.REQUIRED
    enabled: bool = True
    params: dict[str, Any] = Field(default_factory=dict)
    explicit_null_params: list[str] = Field(default_factory=list)
    overridable_params: list[str] = Field(default_factory=list)


class RoleSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    role_id: str
    name: str
    min_nodes: int = Field(default=1, ge=0)
    max_nodes: int | None = None
    allow_colocation: bool = True
    instances: list[CheckInstance] = Field(default_factory=list)


class CompatibilitySpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    allowed_os: list[str] = Field(
        default_factory=lambda: ["ubuntu:24.04", "debian:13", "rocky:9"]
    )
    allowed_arch: list[str] = Field(default_factory=lambda: ["x86_64", "aarch64"])


class ReleaseManifest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    plan_digest: str
    template_digests: dict[str, str] = Field(default_factory=dict)
    released_at: str
    compiler_version: str = COMPILER_VERSION
    product_version: str = PRODUCT_VERSION
    notes: str = ""


class PlanDocument(BaseModel):
    """Flattened V1 Plan model (`Plan -> Role -> Independent CheckInstance`, CHG-002)."""

    model_config = ConfigDict(extra="forbid")

    schema_version: str = PLAN_SCHEMA_VERSION
    plan_id: str
    name: str
    software_name: str = ""
    software_version: str = ""
    version: str = "0.1.0"
    state: PlanState = PlanState.DRAFT
    description: str = ""
    compatibility: CompatibilitySpec = Field(default_factory=CompatibilitySpec)
    roles: list[RoleSpec] = Field(default_factory=list)
    embedded_templates: list[TemplateSpec] = Field(default_factory=list)
    template_lock: dict[str, str] = Field(default_factory=dict)
    release_manifest: ReleaseManifest | None = None
    digest: str | None = None
    updated_at: str | None = None

    def compute_semantic_digest(self) -> str:
        return compute_plan_semantic_digest(self.model_dump(mode="json"))

    def with_updated_digest(self) -> PlanDocument:
        digest = self.compute_semantic_digest()
        return self.model_copy(update={"digest": digest, "updated_at": utc_now_iso()})

    def get_role(self, role_id: str) -> RoleSpec | None:
        for role in self.roles:
            if role.role_id == role_id:
                return role
        return None

    def clone_instance_to_role(
        self, source_role_id: str, instance_id: str, target_role_id: str
    ) -> CheckInstance:
        """Copy an instance into the same or another role with a brand-new independent ID (REQ-TPL-08)."""
        src_role = self.get_role(source_role_id)
        dst_role = self.get_role(target_role_id)
        if src_role is None or dst_role is None:
            raise KeyError("源角色或目标角色不存在")
        src_inst = next((i for i in src_role.instances if i.instance_id == instance_id), None)
        if src_inst is None:
            raise KeyError(f"找不到检查实例: {instance_id}")
        short_suffix = uuid.uuid4().hex[:6]
        new_id = f"{target_role_id}_{src_inst.template_id.lower().replace('.', '_')}_{short_suffix}"
        cloned = CheckInstance(
            instance_id=new_id,
            title=src_inst.title,
            template_id=src_inst.template_id,
            template_version=src_inst.template_version,
            template_digest=src_inst.template_digest,
            importance=src_inst.importance,
            enabled=src_inst.enabled,
            params=copy.deepcopy(src_inst.params),
            explicit_null_params=list(src_inst.explicit_null_params),
            overridable_params=list(src_inst.overridable_params),
        )
        dst_role.instances.append(cloned)
        return cloned


class ValidationDiagnostic(BaseModel):
    model_config = ConfigDict(extra="forbid")

    severity: str = "ERROR"  # ERROR | WARNING | INFO
    code: str
    message: str
    role_id: str | None = None
    instance_id: str | None = None
    field_name: str | None = None


class InventoryHost(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str
    address: str
    port: int = Field(default=22, ge=1, le=65535)
    ssh_user: str = "ops"
    proxy_jump: str = ""
    groups: list[str] = Field(default_factory=list)
    host_vars: dict[str, Any] = Field(default_factory=dict)


class InventorySnapshot(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_mode: str = "quick_ip"  # quick_ip | ansible_ini | ansible_yaml
    hosts: list[InventoryHost] = Field(default_factory=list)
    groups: dict[str, list[str]] = Field(default_factory=dict)


class ParameterOverrideRule(BaseModel):
    """Controlled deployment-time runtime override (Group-level or Host-level, Section 5.2)."""

    model_config = ConfigDict(extra="forbid")

    scope_type: str  # "group" | "host"
    scope_target: str  # group name or host name
    role_id: str
    instance_id: str
    param_name: str
    value: Any


class RunBinding(BaseModel):
    model_config = ConfigDict(extra="forbid")

    plan_id: str
    plan_version: str
    plan_state: PlanState
    plan_digest: str
    inventory: InventorySnapshot
    role_to_hosts: dict[str, list[str]] = Field(default_factory=dict)
    runtime_overrides: list[ParameterOverrideRule] = Field(default_factory=list)
    approved_privileged_templates: list[str] = Field(default_factory=list)
    approve_root_ssh: bool = False
    run_scope: RunScope = RunScope.FULL
    selected_hosts_filter: list[str] = Field(default_factory=list)
    simulation_profile: str = "mixed_realistic"
    credential_token: str | None = None
    idempotency_key: str = Field(default_factory=lambda: f"req-{uuid.uuid4().hex[:12]}")


class ResolvedParamValue(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str
    value: Any
    source: str  # TEMPLATE_DEFAULT | INSTANCE_EXPLICIT | INSTANCE_EXPLICIT_NULL | RUNTIME_GROUP_OVERRIDE | RUNTIME_HOST_OVERRIDE
    sensitive: bool = False


class StaticRunPreview(BaseModel):
    """Static pre-SSH preview (Section 5.4 / 10.1 Act 3 Step 4)."""

    model_config = ConfigDict(extra="forbid")

    plan_id: str
    plan_name: str
    plan_version: str
    plan_state: PlanState
    plan_digest: str
    host_count: int
    role_bindings: dict[str, list[str]]
    total_candidate_checks: int
    privileged_templates_needed: list[str]
    root_ssh_hosts: list[str]
    custom_shell_instances: list[str]
    active_network_probes: list[str]
    diagnostics: list[ValidationDiagnostic] = Field(default_factory=list)


class CapabilityRecord(BaseModel):
    model_config = ConfigDict(extra="forbid")

    host_name: str
    os_id: str
    os_version: str
    arch: str
    kernel_release: str
    ssh_user: str
    has_sudo: bool = True
    sudo_mode: str = "passwordless"  # passwordless | password | restricted_whitelist | none
    available_tools: list[str] = Field(default_factory=list)
    exec_tmpdir_standard: str = "/tmp"
    exec_tmpdir_privileged: str = "/run"
    machine_id: str
    boot_id: str
    ssh_host_key_fp: str
    collected_at: str = Field(default_factory=utc_now_iso)


class PropertyOrigin(BaseModel):
    """Maps a compiled Goss resource property back to its originating Role & Instance (TODO-002)."""

    model_config = ConfigDict(extra="forbid")

    role_id: str
    instance_id: str
    template_id: str
    template_version: str
    template_digest: str
    importance: Importance
    privilege_group: PrivilegeGroup
    evidence_depth: EvidenceDepth
    goss_resource_type: str
    goss_resource_id: str
    property_key: str
    expected_summary: str


class ExecutionArtifact(BaseModel):
    model_config = ConfigDict(extra="forbid")

    host_name: str
    privilege_group: PrivilegeGroup
    goss_rules: dict[str, dict[str, dict[str, Any]]]
    goss_yaml: str
    digest: str
    origins: list[PropertyOrigin] = Field(default_factory=list)
    resolved_params_by_instance: dict[str, dict[str, ResolvedParamValue]] = Field(
        default_factory=dict
    )
    compiler_version: str = COMPILER_VERSION


class CheckEvidence(BaseModel):
    model_config = ConfigDict(extra="forbid")

    run_id: str
    host_name: str
    role_id: str
    instance_id: str
    template_id: str
    template_version: str
    template_digest: str
    goss_subrule_id: str
    execution_identity: str
    privilege_group: PrivilegeGroup
    expected: str
    observed: str
    evidence_depth: EvidenceDepth
    source: str
    window_coverage: str | None = None
    collected_at: str = Field(default_factory=utc_now_iso)
    high_risk_root_audit: bool = False
    goss_version: str = "v0.4.9"
    compiler_version: str = COMPILER_VERSION


class CheckInstanceResult(BaseModel):
    model_config = ConfigDict(extra="forbid")

    instance_id: str
    title: str
    role_id: str
    template_id: str
    template_version: str
    importance: Importance
    privilege_group: PrivilegeGroup
    status: CheckStatus
    summary: str
    remediation_hint: str = ""
    resolved_params: dict[str, ResolvedParamValue] = Field(default_factory=dict)
    evidences: list[CheckEvidence] = Field(default_factory=list)


class HostRunResult(BaseModel):
    model_config = ConfigDict(extra="forbid")

    host_name: str
    address: str
    ssh_user: str
    roles: list[str]
    phase: HostPhase = HostPhase.FINISHED
    capability: CapabilityRecord | None = None
    standard_artifact_yaml: str | None = None
    privileged_artifact_yaml: str | None = None
    standard_artifact_digest: str | None = None
    privileged_artifact_digest: str | None = None
    execution_may_have_started: bool = False
    check_results: list[CheckInstanceResult] = Field(default_factory=list)
    host_error: str | None = None


class CleanupRecord(BaseModel):
    """Triple-fingerprint remote artifact cleanup & crash recovery record (TODO-007 / REQ-RUN-12/13)."""

    model_config = ConfigDict(extra="forbid")

    cleanup_id: str
    run_id: str
    host_name: str
    remote_dir: str
    privilege_group: PrivilegeGroup = PrivilegeGroup.STANDARD
    owner_token: str
    boot_id: str
    process_pid: int | None = None
    process_starttime_ticks: int | None = None
    live_process_detected: bool = False
    live_process_cmdline: str = ""
    status: CleanupStatus = CleanupStatus.CLEAN
    waived_reason: str | None = None
    updated_at: str = Field(default_factory=utc_now_iso)


class RunAuditRecord(BaseModel):
    model_config = ConfigDict(extra="forbid")

    idempotency_key: str
    approved_privileged_templates: list[str] = Field(default_factory=list)
    approve_root_ssh: bool = False
    high_risk_root_hosts: list[str] = Field(default_factory=list)
    credential_redacted: bool = True
    compiler_version: str = COMPILER_VERSION
    locked_goss_version: str = "v0.4.9"


class RunReport(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: str = REPORT_SCHEMA_VERSION
    run_id: str
    execution_backend: str = "mock_simulator"  # mock_simulator | openssh_goss
    platform_verified_claim: bool = False
    plan_id: str
    plan_name: str = ""
    plan_version: str
    plan_state: PlanState
    plan_digest: str
    run_scope: RunScope = RunScope.FULL
    run_status: RunStatus
    readiness: ReadinessStatus
    readiness_reason: str = ""
    gate_eligible: bool = False
    simulation_profile: str = "mixed_realistic"
    created_at: str = Field(default_factory=utc_now_iso)
    finished_at: str | None = None
    hosts: list[HostRunResult] = Field(default_factory=list)
    control_checks: list[CheckInstanceResult] = Field(default_factory=list)
    cleanup_records: list[CleanupRecord] = Field(default_factory=list)
    diagnostics: list[ValidationDiagnostic] = Field(default_factory=list)
    audit: RunAuditRecord

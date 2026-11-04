"""Global constants, exit codes, resource caps, and locked tool versions for ReadySpec V1."""

from __future__ import annotations

from enum import IntEnum, StrEnum

PRODUCT_NAME = "ReadySpec"
PRODUCT_VERSION = "0.1.0"
COMPILER_VERSION = "readyspec-compiler-0.1.0"

# Locked Official Goss Binary Specification (TODO-001 / TODO-002 / CHG-004)
LOCKED_GOSS_VERSION = "v0.4.9"
LOCKED_GOSS_LICENSE = "Apache-2.0"
LOCKED_GOSS_CLI_ARGS = (
    "validate",
    "--format",
    "json",
    "--retry-timeout",
    "0s",
    "--no-color",
)
DEFAULT_GOSS_MAX_CONCURRENT = 10

# Schema Versions (TODO-011 / REQ-PLAN-13)
PLAN_SCHEMA_VERSION = "readyspec.plan/v1"
TEMPLATE_SCHEMA_VERSION = "readyspec.template/v1"
CUSTOM_RESULT_PROTOCOL_VERSION = "readyspec.custom_result/v1"
REPORT_SCHEMA_VERSION = "readyspec.report/v1"

# Resource & Timeout Caps (TODO-014 / TODO-003 / TODO-015)
MAX_PLAN_BYTES = 10 * 1024 * 1024  # 10 MiB
MAX_EVIDENCE_BYTES = 64 * 1024  # 64 KiB
MAX_CUSTOM_JSON_BYTES = 16 * 1024  # 16 KiB (TODO-003)
MAX_HOST_JSON_BYTES = 16 * 1024 * 1024  # 16 MiB
MAX_REPORT_BYTES = 64 * 1024 * 1024  # 64 MiB
MAX_HTTP_RESPONSE_BYTES = 1 * 1024 * 1024  # 1 MiB (TODO-015)
MAX_HTTP_REDIRECTS = 3

DEFAULT_SSH_CONNECT_TIMEOUT_SEC = 10
DEFAULT_DISCOVERY_TIMEOUT_SEC = 30
DEFAULT_TRANSFER_TIMEOUT_SEC = 60
DEFAULT_PROBE_TIMEOUT_SEC = 5
MAX_PROBE_TIMEOUT_SEC = 30
DEFAULT_SHELL_TIMEOUT_SEC = 15
MAX_SHELL_TIMEOUT_SEC = 120
DEFAULT_HOST_GOSS_TIMEOUT_SEC = 300  # 5m
DEFAULT_HOST_TOTAL_TIMEOUT_SEC = 600  # 10m
DEFAULT_QUEUE_TIMEOUT_SEC = 1800  # 30m
DEFAULT_RUN_TIMEOUT_SEC = 3600  # 60m

MAX_QUEUE_DEPTH = 10
DEFAULT_HOST_CONCURRENCY = 5
HISTORY_RETENTION_DAYS = 30
HISTORY_MAX_COMPLETED_RUNS = 100


class ExitCode(IntEnum):
    """Locked ReadySpec V1 CLI exit codes (Section 10.4 / TODO-011)."""

    READY = 0
    NOT_READY = 10
    UNKNOWN = 11
    PLAN_ERROR = 12
    INFRA_ERROR = 13
    CANCELLED = 14
    AUTH_ERROR = 15


class PlanState(StrEnum):
    DRAFT = "DRAFT"
    VALIDATED = "VALIDATED"
    TRIAL_SNAPSHOT = "TRIAL_SNAPSHOT"
    RELEASED = "RELEASED"
    RETIRED = "RETIRED"


class TemplateMaturity(StrEnum):
    PLANNED = "PLANNED"
    DEFINED = "DEFINED"
    IMPLEMENTED = "IMPLEMENTED"
    INTEGRATED = "INTEGRATED"


class TemplateSource(StrEnum):
    BUILTIN = "BUILTIN"
    USER_PUBLIC = "USER_PUBLIC"
    PLAN_LOCAL = "PLAN_LOCAL"


class PrivilegeGroup(StrEnum):
    STANDARD = "STANDARD"
    PRIVILEGED = "PRIVILEGED"


class Importance(StrEnum):
    REQUIRED = "REQUIRED"
    WARNING = "WARNING"
    INFO = "INFO"


class CheckStatus(StrEnum):
    PASS = "PASS"
    FAIL = "FAIL"
    ERROR = "ERROR"
    UNSUPPORTED = "UNSUPPORTED"
    SKIP = "SKIP"


class ReadinessStatus(StrEnum):
    READY = "READY"
    NOT_READY = "NOT_READY"
    UNKNOWN = "UNKNOWN"


class RunStatus(StrEnum):
    PENDING = "PENDING"
    RUNNING = "RUNNING"
    CANCELLING = "CANCELLING"
    COMPLETED = "COMPLETED"
    PARTIAL = "PARTIAL"
    FAILED = "FAILED"
    CANCELLED = "CANCELLED"


class RunScope(StrEnum):
    FULL = "FULL"
    PARTIAL_SCOPE = "PARTIAL_SCOPE"


class HostPhase(StrEnum):
    QUEUED = "QUEUED"
    CONNECTING = "CONNECTING"
    DISCOVERING = "DISCOVERING"
    PREPARING = "PREPARING"
    RUNNING_STANDARD = "RUNNING_STANDARD"
    RUNNING_PRIVILEGED = "RUNNING_PRIVILEGED"
    COLLECTING = "COLLECTING"
    CLEANING = "CLEANING"
    FINISHED = "FINISHED"


class CleanupStatus(StrEnum):
    CLEAN = "CLEAN"
    PENDING = "PENDING"
    FAILED = "FAILED"
    UNKNOWN = "UNKNOWN"
    WAIVED = "WAIVED"


class EvidenceDepth(StrEnum):
    EXISTENCE = "EXISTENCE"
    METADATA = "METADATA"
    CONFIGURATION = "CONFIGURATION"
    EFFECTIVE_STATE = "EFFECTIVE_STATE"
    STATIC_COMPATIBILITY = "STATIC_COMPATIBILITY"
    ACTIVE_PROBE = "ACTIVE_PROBE"
    HISTORICAL_EVIDENCE = "HISTORICAL_EVIDENCE"
    CONTROL_AGGREGATE = "CONTROL_AGGREGATE"


CERTIFIED_TARGETS: tuple[tuple[str, str, str], ...] = (
    ("ubuntu", "24.04", "x86_64"),
    ("ubuntu", "24.04", "aarch64"),
    ("debian", "13", "x86_64"),
    ("debian", "13", "aarch64"),
    ("rocky", "9", "x86_64"),
    ("rocky", "9", "aarch64"),
)

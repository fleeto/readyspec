"""TargetExecutorProtocol: strict boundary between real Control Plane and remote SSH/Goss transport."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any, Protocol

from readyspec.constants import HostPhase
from readyspec.domain.models import (
    CapabilityRecord,
    CleanupRecord,
    ExecutionArtifact,
    InventoryHost,
)

PhaseCallback = Callable[[str, HostPhase, str], None]


class TargetExecutorProtocol(Protocol):
    """Pluggable remote executor contract (implemented by MockRemoteExecutor & future OpenSSHExecutor)."""

    @property
    def backend_name(self) -> str: ...

    async def discover_capabilities(
        self,
        host: InventoryHost,
        *,
        simulation_profile: str,
        on_phase: PhaseCallback | None = None,
    ) -> CapabilityRecord: ...

    async def execute_goss_artifact(
        self,
        run_id: str,
        host: InventoryHost,
        capability: CapabilityRecord,
        artifact: ExecutionArtifact,
        *,
        simulation_profile: str,
        is_fault_target: bool,
        cancel_requested: Callable[[], bool],
        on_phase: PhaseCallback | None = None,
    ) -> tuple[dict[str, Any], CleanupRecord]: ...

    async def resolve_cleanup(
        self,
        record: CleanupRecord,
        *,
        confirm_kill_live_process: bool = False,
        waive_reason: str | None = None,
    ) -> CleanupRecord: ...

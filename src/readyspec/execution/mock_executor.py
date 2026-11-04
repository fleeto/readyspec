"""Authentic Goss v0.4.9 JSON simulator operating on real compiled ExecutionArtifacts."""

from __future__ import annotations

import asyncio
import hashlib
import re
import uuid
from collections.abc import Callable
from typing import Any

from readyspec.constants import CleanupStatus, HostPhase, PrivilegeGroup
from readyspec.domain.models import (
    CapabilityRecord,
    CleanupRecord,
    ExecutionArtifact,
    InventoryHost,
    utc_now_iso,
)
from readyspec.execution.protocol import PhaseCallback

SIMULATION_PROFILES: dict[str, str] = {
    "mixed_realistic": "真实混合排障场景（末台主机磁盘 42.5GiB / 模拟现场环境差异）",
    "all_ready": "全绿通过场景（所有主机硬件/服务/存储/安全基线均达标）",
    "log_gap_and_missing_tool": "证据缺口场景（日志仅覆盖 2h 拒绝假 PASS + 缺 readelf 工具）",
    "interrupted_cleanup": "网络闪断恢复场景（末台主机执行中断，产生三重指纹待清理记录）",
}


class RemoteExecutionInterruptedError(RuntimeError):
    """Simulated SSH drop after remote execution may have started (TODO-007)."""

    def __init__(self, message: str, cleanup_record: CleanupRecord) -> None:
        super().__init__(message)
        self.cleanup_record = cleanup_record


class MockRemoteExecutor:
    """Evaluates real compiled Goss YAML rules against realistic virtual host telemetry."""

    def __init__(self, *, step_delay_sec: float = 0.04) -> None:
        self._step_delay = step_delay_sec

    @property
    def backend_name(self) -> str:
        return "mock_simulator"

    async def _pause(self) -> None:
        if self._step_delay > 0:
            await asyncio.sleep(self._step_delay)

    async def discover_capabilities(
        self,
        host: InventoryHost,
        *,
        simulation_profile: str,
        on_phase: PhaseCallback | None = None,
    ) -> CapabilityRecord:
        if on_phase:
            on_phase(host.name, HostPhase.CONNECTING, f"连接 {host.ssh_user}@{host.address}:{host.port}")
        await self._pause()
        if on_phase:
            on_phase(host.name, HostPhase.DISCOVERING, "探测 OS / 架构 / 可用只读工具 / 临时执行目录")
        await self._pause()

        h_hash = hashlib.sha256(f"{host.address}:{host.port}:{host.proxy_jump}".encode()).hexdigest()
        os_id = str(host.host_vars.get("mock_os_id", "ubuntu" if int(h_hash[0], 16) % 2 == 0 else "rocky"))
        os_ver = str(host.host_vars.get("mock_os_version", "24.04" if os_id == "ubuntu" else "9"))
        arch = str(host.host_vars.get("mock_arch", "x86_64"))
        kernel = "6.8.0-45-generic" if os_id == "ubuntu" else "5.14.0-427.el9.x86_64"

        tools = [
            "uname",
            "df",
            "ip",
            "systemctl",
            "timedatectl",
            "journalctl",
            "ldconfig",
            "sha256sum",
            "tar",
            "openssl",
        ]
        if simulation_profile != "log_gap_and_missing_tool":
            tools.append("readelf")

        return CapabilityRecord(
            host_name=host.name,
            os_id=os_id,
            os_version=os_ver,
            arch=arch,
            kernel_release=kernel,
            ssh_user=host.ssh_user,
            has_sudo=True,
            sudo_mode="passwordless",
            available_tools=tools,
            exec_tmpdir_standard="/tmp",
            exec_tmpdir_privileged="/run",
            machine_id=f"mid-{h_hash[:16]}",
            boot_id=f"bid-{h_hash[16:32]}",
            ssh_host_key_fp=f"SHA256:{h_hash[32:56]}",
        )

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
    ) -> tuple[dict[str, Any], CleanupRecord]:
        safe_run = re.sub(r"[^a-zA-Z0-9._-]", "_", run_id)
        safe_host = re.sub(r"[^a-zA-Z0-9._-]", "_", host.name)
        base_tmp = (
            capability.exec_tmpdir_privileged
            if artifact.privilege_group == PrivilegeGroup.PRIVILEGED
            else capability.exec_tmpdir_standard
        )
        remote_dir = f"{base_tmp}/readyspec_{safe_run}_{safe_host}_{artifact.privilege_group.value.lower()}"
        owner_token = f"tok_{uuid.uuid4().hex}"

        if on_phase:
            on_phase(
                host.name,
                HostPhase.PREPARING,
                f"分发并 SHA-256 核验 {artifact.privilege_group.value} 工件 ({remote_dir})",
            )
        await self._pause()

        run_phase = (
            HostPhase.RUNNING_PRIVILEGED
            if artifact.privilege_group == PrivilegeGroup.PRIVILEGED
            else HostPhase.RUNNING_STANDARD
        )
        if on_phase:
            on_phase(
                host.name,
                run_phase,
                f"运行原装 Goss v0.4.9 ({artifact.privilege_group.value})",
            )
        await self._pause()

        # Simulate mid-execution SSH interruption if profile == "interrupted_cleanup" on fault target
        if simulation_profile == "interrupted_cleanup" and is_fault_target:
            pending_rec = CleanupRecord(
                cleanup_id=f"cln-{uuid.uuid4().hex[:8]}",
                run_id=run_id,
                host_name=host.name,
                remote_dir=remote_dir,
                privilege_group=artifact.privilege_group,
                owner_token=owner_token,
                boot_id=capability.boot_id,
                process_pid=31452,
                process_starttime_ticks=8472910,
                live_process_detected=True,
                live_process_cmdline=f"{remote_dir}/goss -g goss.yaml validate --format json --retry-timeout 0s",
                status=CleanupStatus.PENDING,
            )
            raise RemoteExecutionInterruptedError(
                f"SSH 连接在远端 Goss 启动后意外中断（不自动重跑，已持久登记三重指纹待清理项 {pending_rec.cleanup_id}）",
                pending_rec,
            )

        if cancel_requested():
            pending_cancel = CleanupRecord(
                cleanup_id=f"cln-{uuid.uuid4().hex[:8]}",
                run_id=run_id,
                host_name=host.name,
                remote_dir=remote_dir,
                privilege_group=artifact.privilege_group,
                owner_token=owner_token,
                boot_id=capability.boot_id,
                status=CleanupStatus.CLEAN,
            )
            return {"results": [], "summary": {"failed-count": 0}}, pending_cancel

        if on_phase:
            on_phase(host.name, HostPhase.COLLECTING, "回收有界 Goss v0.4.9 JSON 证据")
        await self._pause()

        goss_results: list[dict[str, Any]] = []
        for res_type, id_map in artifact.goss_rules.items():
            for res_id, props in id_map.items():
                if res_type == "command":
                    goss_results.append(
                        self._simulate_command_resource(
                            res_id,
                            props,
                            capability,
                            simulation_profile=simulation_profile,
                            is_fault_target=is_fault_target,
                        )
                    )
                else:
                    goss_results.extend(
                        self._simulate_native_resource(
                            res_type,
                            res_id,
                            props,
                            simulation_profile=simulation_profile,
                            is_fault_target=is_fault_target,
                        )
                    )

        if on_phase:
            on_phase(host.name, HostPhase.CLEANING, f"核验 .readyspec_owner 指纹并清理 {remote_dir}")
        await self._pause()

        clean_rec = CleanupRecord(
            cleanup_id=f"cln-{uuid.uuid4().hex[:8]}",
            run_id=run_id,
            host_name=host.name,
            remote_dir=remote_dir,
            privilege_group=artifact.privilege_group,
            owner_token=owner_token,
            boot_id=capability.boot_id,
            status=CleanupStatus.CLEAN,
        )
        failed_cnt = sum(1 for r in goss_results if not r.get("successful", False))
        payload = {
            "results": goss_results,
            "summary": {
                "test-count": len(goss_results),
                "failed-count": failed_cnt,
                "total-duration": 42000000,
            },
        }
        return payload, clean_rec

    def _simulate_native_resource(
        self,
        res_type: str,
        res_id: str,
        props: dict[str, Any],
        *,
        simulation_profile: str,
        is_fault_target: bool,
    ) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        for prop_key, expected_val in props.items():
            if prop_key in ("timeout", "method", "allow-insecure", "no-follow-redirects", "meta"):
                continue
            found_val = expected_val
            successful = True

            # Realistic port conflict if fault target checks tcp:8080 free in mixed_realistic
            if (
                simulation_profile == "mixed_realistic"
                and is_fault_target
                and res_type == "port"
                and res_id == "tcp:8080"
                and prop_key == "listening"
                and expected_val is False
            ):
                found_val = True
                successful = False

            items.append(
                {
                    "resource-type": res_type,
                    "resource-id": res_id,
                    "property": prop_key,
                    "successful": successful,
                    "expected": [expected_val] if not isinstance(expected_val, list) else expected_val,
                    "found": [found_val] if not isinstance(found_val, list) else found_val,
                    "err": None,
                    "timed-out": False,
                }
            )
        return items

    def _simulate_command_resource(
        self,
        res_id: str,
        props: dict[str, Any],
        capability: CapabilityRecord,
        *,
        simulation_profile: str,
        is_fault_target: bool,
    ) -> dict[str, Any]:
        meta = props.get("meta") or {}
        tid = str(meta.get("readyspec_template", ""))
        params = meta.get("readyspec_params") or {}
        exec_str = str(props.get("exec", ""))

        exit_status = 0
        stdout = "ok"
        stderr = ""

        if tid == "H01":
            stdout = capability.arch
        elif tid == "H02":
            stdout = "8"
        elif tid == "H03":
            # 32 GiB in KiB (33554432) normally; on fault target 16 GiB (16777216)
            stdout = "33554432"
        elif tid == "H04":
            # Swap 0 normally
            stdout = "0"
        elif tid == "O01":
            stdout = f"{capability.os_id}:{capability.os_version}"
        elif tid == "O02":
            stdout = capability.kernel_release
        elif tid == "O04":
            want = bool(params.get("should_be_loaded", True))
            stdout = "1" if want else "0"
        elif tid == "S04":
            path = str(params.get("path", "/data"))
            # In mixed_realistic on fault target, `/data` has 42.5 GiB (44564480 KiB), whereas `/data2` or non-fault hosts have 250 GiB!
            if simulation_profile == "mixed_realistic" and is_fault_target and path == "/data":
                avail_kib = 44564480  # 42.50 GiB
            else:
                avail_kib = 262144000  # 250.00 GiB
            stdout = f"/dev/nvme1n1 524288000 100000000 {avail_kib} 20% {path}"
        elif tid == "S05":
            path = str(params.get("path", "/data"))
            stdout = f"/dev/nvme1n1 10000000 800000 9200000 8% {path}"
        elif tid == "N06":
            dst = str(params.get("destination_ip", "10.0.0.1"))
            dev = str(params.get("expected_dev") or "eth0")
            stdout = f"{dst} via 10.0.0.254 dev {dev} src 10.0.0.11 uid 1000"
        elif tid == "R03":
            stdout = "0.42 0.58 0.61 1/312 19402"
        elif tid == "R04":
            stdout = "131072"
        elif tid == "R05":
            stdout = "NTPSynchronized=yes\nNTP=yes"
        elif tid == "A03":
            exp = str(params.get("expected_value", "4"))
            stdout = exp
        elif tid == "A04":
            pol = str(params.get("expected_policy", "disabled_or_permissive"))
            stdout = "Enforcing" if pol == "enforcing" else "Permissive"
        elif tid == "D02":
            cmd_name = str(params.get("command_or_path", "tar"))
            stdout = cmd_name if cmd_name.startswith("/") else f"/usr/bin/{cmd_name}"
        elif tid == "D03":
            rt = str(params.get("runtime", "openssl"))
            stdout = "OpenSSL 3.2.1" if rt == "openssl" else ("openjdk version 21.0.4" if rt == "java" else "Python 3.12.6")
        elif tid == "D04":
            soname = str(params.get("required_soname", "libc.so.6"))
            stdout = (
                f"ELF Header: Machine: Advanced Micro Devices X86-64\n"
                f" 0x0000000000000001 (NEEDED) Shared library: [{soname}]\n"
                f"\t{soname} (libc6,x86-64) => /lib/x86_64-linux-gnu/{soname}"
            )
        elif tid == "D05":
            stdout = "LANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8"
        elif tid == "L01":
            stdout = "86400.50 172000.00"
        elif tid in ("L02", "L03"):
            hrs = int(params.get("window_hours", 24))
            if simulation_profile == "log_gap_and_missing_tool":
                stdout = f"COVERAGE_HOURS=2.0\n-- Journal begins only 2h ago (target {hrs}h) --"
            else:
                stdout = f"COVERAGE_HOURS={float(hrs)}\nSINCE_BOOT_COMPLETE=1"
        elif meta.get("readyspec_kind") == "CUSTOM_SHELL":
            # Simulate realistic POSIX output reflecting exported RS_PARAM_* values
            pairs = re.findall(r"(RS_PARAM_[A-Z0-9_]+)=('[^']*'|[^\s;]+)", exec_str)
            kv_preview = " ".join(
                f"{k.removeprefix('RS_PARAM_').lower()}={v.strip(chr(39))}" for k, v in pairs
            )
            if "readyspec.custom_result/v1" in exec_str:
                stdout = '{"protocol":"readyspec.custom_result/v1","status":"PASS","summary":"自定义协议脚本校验通过"}'
            else:
                stdout = f"{kv_preview} status=ok".strip()

        expected_exit = int(props.get("exit-status", 0))
        return {
            "resource-type": "command",
            "resource-id": res_id,
            "property": "exit-status",
            "successful": exit_status == expected_exit,
            "exit-status": exit_status,
            "stdout": stdout,
            "stderr": stderr,
            "expected": [expected_exit],
            "found": [exit_status],
            "err": None,
            "timed-out": False,
        }

    async def resolve_cleanup(
        self,
        record: CleanupRecord,
        *,
        confirm_kill_live_process: bool = False,
        waive_reason: str | None = None,
    ) -> CleanupRecord:
        """Execute controlled triple-fingerprint cleanup or WAIVED audit (TODO-007 / REQ-RUN-13)."""
        await self._pause()
        if waive_reason is not None:
            reason_clean = waive_reason.strip()
            if not reason_clean:
                raise ValueError("标记 WAIVED 必须填写明确放弃自动清理的原因以留存审计记录 (REQ-RUN-13)。")
            return record.model_copy(
                update={
                    "status": CleanupStatus.WAIVED,
                    "waived_reason": reason_clean,
                    "updated_at": utc_now_iso(),
                }
            )

        if record.live_process_detected and not confirm_kill_live_process:
            raise PermissionError(
                f"探活发现远端存活进程 PID={record.process_pid} ({record.live_process_cmdline}) 匹配三重归属指纹；"
                "按 TODO-007 安全边界，必须经运维显式确认终止进程后才允许清理目录！"
            )

        return record.model_copy(
            update={
                "live_process_detected": False,
                "status": CleanupStatus.CLEAN,
                "updated_at": utc_now_iso(),
            }
        )

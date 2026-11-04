"""Normalizes Goss v0.4.9 JSON outputs into CheckInstanceResult & Evidence Contracts."""

from __future__ import annotations

import re
from typing import Any

from readyspec.constants import CheckStatus, PrivilegeGroup
from readyspec.domain.models import (
    CapabilityRecord,
    CheckEvidence,
    CheckInstanceResult,
    ExecutionArtifact,
    PlanDocument,
    PropertyOrigin,
    ResolvedParamValue,
    TemplateKind,
    TemplateSpec,
)
from readyspec.normalizer.shell_evaluator import (
    evaluate_custom_shell_output,
    truncate_evidence_text,
)


def _parse_version_tuple(raw: str) -> tuple[int, ...]:
    nums = re.findall(r"\d+", raw.split("-", 1)[0])
    return tuple(int(x) for x in nums[:3]) if nums else (0,)


def _normalize_arch(raw: str) -> str:
    s = raw.strip().lower()
    if s in ("x86_64", "amd64"):
        return "x86_64"
    if s in ("aarch64", "arm64"):
        return "aarch64"
    return s


def _evaluate_builtin_command_contract(
    origin: PropertyOrigin,
    params: dict[str, ResolvedParamValue],
    goss_item: dict[str, Any],
    capability: CapabilityRecord | None,
) -> tuple[CheckStatus, str, str, str | None, str]:
    """Return `(status, summary, observed, window_coverage, remediation)` for built-in command checks."""
    tid = origin.template_id
    vals = {k: v.value for k, v in params.items()}
    exit_code = int(goss_item.get("exit-status", 0))
    stdout = str(goss_item.get("stdout", "") or "").strip()
    stderr = str(goss_item.get("stderr", "") or "").strip()
    timed_out = bool(goss_item.get("timed-out", False))
    err_str = str(goss_item.get("err", "") or "").strip()

    if timed_out:
        return CheckStatus.ERROR, f"{tid} 执行超时", "TIMED_OUT", None, "检查主机负载或调高命令超时预算。"
    if err_str:
        return CheckStatus.ERROR, f"{tid} 探测异常: {err_str}", err_str, None, ""
    if exit_code in (126, 127) and tid != "D02":
        return (
            CheckStatus.ERROR,
            f"{tid} 缺少目标系统探测工具 (exit={exit_code})",
            stderr or stdout or f"exit={exit_code}",
            None,
            "确认目标机已安装对应基础只读工具。",
        )

    if tid == "H01":
        actual = _normalize_arch(stdout)
        allowed = [_normalize_arch(str(x)) for x in (vals.get("allowed_arch") or [])]
        if not actual:
            return CheckStatus.ERROR, "无法解析 uname -m 输出", stdout, None, ""
        ok = actual in allowed
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"CPU 架构 {actual} {'符合' if ok else '不在允许范围'} {allowed}",
            actual,
            None,
            "" if ok else f"请使用架构属于 {allowed} 的目标主机。",
        )

    if tid == "H02":
        try:
            cores = int(stdout.splitlines()[-1].strip())
        except (ValueError, IndexError):
            return CheckStatus.ERROR, f"无法解析在线逻辑核数: {stdout!r}", stdout, None, ""
        req = int(vals["min_cores"])
        ok = cores >= req
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"在线逻辑 CPU {cores} 核 ({'满足' if ok else '低于'} >= {req} 核)",
            f"{cores} cores",
            None,
            "" if ok else f"请扩容目标主机至至少 {req} vCPU。",
        )

    if tid == "H03":
        try:
            kib = int(stdout.split()[0])
        except (ValueError, IndexError):
            return CheckStatus.ERROR, f"无法解析 /proc/meminfo MemTotal: {stdout!r}", stdout, None, ""
        gib = round(kib / (1024 * 1024), 2)
        req_gib = float(vals["min_gib"])
        ok = gib >= req_gib
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"物理总内存 {gib} GiB ({'满足' if ok else '低于'} >= {req_gib} GiB)",
            f"{gib} GiB ({kib} kB)",
            None,
            "" if ok else f"请将主机物理内存扩容至 >= {req_gib} GiB。",
        )

    if tid == "H04":
        try:
            swap_kib = int(stdout.split()[0])
        except (ValueError, IndexError):
            return CheckStatus.ERROR, f"无法解析 SwapTotal: {stdout!r}", stdout, None, ""
        swap_mib = round(swap_kib / 1024, 1)
        policy = str(vals["policy"])
        if policy == "disabled":
            ok = swap_kib == 0
            return (
                CheckStatus.PASS if ok else CheckStatus.FAIL,
                "Swap 已按要求关闭 (0 MiB)" if ok else f"检测到开启了 {swap_mib} MiB Swap，违反禁用要求",
                f"{swap_mib} MiB",
                None,
                "" if ok else "执行 swapoff -a 并注释 /etc/fstab 中的 swap 挂载项。",
            )
        if policy == "enabled":
            ok = swap_kib > 0
            return (
                CheckStatus.PASS if ok else CheckStatus.FAIL,
                f"Swap 已开启 ({swap_mib} MiB)" if ok else "当前未启用任何 Swap",
                f"{swap_mib} MiB",
                None,
                "" if ok else "请按系统要求配置 Swap 分区或文件。",
            )
        min_mib = int(vals.get("min_mib", 0))
        ok = swap_mib >= min_mib
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"Swap 总量 {swap_mib} MiB ({'满足' if ok else '低于'} >= {min_mib} MiB)",
            f"{swap_mib} MiB",
            None,
            "" if ok else f"请将 Swap 调整至不少于 {min_mib} MiB。",
        )

    if tid == "O01":
        if ":" not in stdout:
            return CheckStatus.ERROR, f"/etc/os-release 字段缺失: {stdout!r}", stdout, None, ""
        allowed_os = [str(x).lower() for x in (vals.get("allowed_distros") or [])]
        actual_os = stdout.lower()
        ok = actual_os in allowed_os or actual_os.split(".")[0] in allowed_os
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"操作系统 {stdout} {'属于' if ok else '不在'}支持列表 {allowed_os}",
            stdout,
            None,
            "" if ok else f"请更换为受支持的发行版: {allowed_os}。",
        )

    if tid == "O02":
        if not stdout:
            return CheckStatus.ERROR, "无法获取 uname -r", stdout, None, ""
        actual_t = _parse_version_tuple(stdout)
        min_t = _parse_version_tuple(str(vals["min_kernel"]))
        ok = actual_t >= min_t
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"内核版本 {stdout} ({'满足' if ok else '低于'} >= {vals['min_kernel']})",
            stdout,
            None,
            "" if ok else f"请升级内核至 {vals['min_kernel']} 及以上版本。",
        )

    if tid == "O04":
        if stdout not in ("0", "1"):
            return CheckStatus.ERROR, f"读取 /proc/modules 异常: {stdout!r}", stdout, None, ""
        is_loaded = stdout == "1"
        want_loaded = bool(vals["should_be_loaded"])
        ok = is_loaded == want_loaded
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"内核模块 {vals['module_name']} 当前{'已加载' if is_loaded else '未出现在 /proc/modules'}",
            f"loaded={is_loaded}",
            None,
            "" if ok else f"检查模块 {vals['module_name']} 加载配置（modprobe / modules-load.d）。",
        )

    if tid == "S04":
        if exit_code != 0 or not stdout:
            return CheckStatus.ERROR, f"无法统计路径 {vals['path']} 空间: {stderr or stdout}", stderr or stdout, None, "确认父目录存在且可访问。"
        cols = stdout.split()
        if len(cols) < 6:
            return CheckStatus.ERROR, f"df -Pk 输出格式异常: {stdout!r}", stdout, None, ""
        try:
            avail_kib = int(cols[3])
        except ValueError:
            return CheckStatus.ERROR, f"df 可用块字段解析失败: {cols[3]!r}", stdout, None, ""
        avail_gib = round(avail_kib / (1024 * 1024), 2)
        min_gib = float(vals["min_free_gib"])
        ok = avail_gib >= min_gib
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"{vals['path']} 挂载点 ({cols[5]}) 可用空间 {avail_gib} GiB ({'满足' if ok else '低于'} >= {min_gib} GiB)",
            f"{avail_gib} GiB on {cols[0]} ({cols[5]})",
            None,
            "" if ok else f"请清理或扩容 {cols[5]} 至可用空间 >= {min_gib} GiB，或在部署向导中覆盖路径参数。",
        )

    if tid == "S05":
        if exit_code != 0 or not stdout:
            return CheckStatus.ERROR, f"无法统计路径 {vals['path']} inode: {stderr or stdout}", stderr or stdout, None, ""
        cols = stdout.split()
        if len(cols) < 6:
            return CheckStatus.ERROR, f"df -Pi 输出异常: {stdout!r}", stdout, None, ""
        try:
            total_inodes = int(cols[1])
            free_inodes = int(cols[3])
        except ValueError:
            return CheckStatus.UNSUPPORTED, f"文件系统 ({cols[0]}) 不提供传统 inode 计数", stdout, None, ""
        if total_inodes <= 0:
            return CheckStatus.UNSUPPORTED, f"文件系统 ({cols[0]}) 总 inode 为 0（动态/非 inode 文件系统，不误判为耗尽）", stdout, None, ""
        free_pct = round((free_inodes / total_inodes) * 100.0, 2)
        min_pct = float(vals["min_free_pct"])
        ok = free_pct >= min_pct
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"{vals['path']} 可用 inode 比例 {free_pct}% ({free_inodes}/{total_inodes})",
            f"{free_pct}% free ({free_inodes}/{total_inodes})",
            None,
            "" if ok else f"请清理大量小文件释放 inode（要求空闲 >= {min_pct}%）。",
        )

    if tid == "N06":
        if exit_code != 0:
            return CheckStatus.FAIL, f"目标 IP {vals['destination_ip']} 不可路由: {stderr or stdout}", stderr or stdout, None, "检查主机路由表配置。"
        m = re.search(r"\bdev\s+(\S+)", stdout)
        actual_dev = m.group(1) if m else ""
        exp_dev = str(vals.get("expected_dev") or "").strip()
        ok = bool(actual_dev) and (not exp_dev or actual_dev == exp_dev)
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"目标 {vals['destination_ip']} 路由出口网卡为 {actual_dev or '未知'}" + (f" (期望 {exp_dev})" if exp_dev else ""),
            stdout,
            None,
            "" if ok else f"核对去往 {vals['destination_ip']} 的静态路由或策略路由。",
        )

    if tid == "R03":
        cols = stdout.split()
        win_idx = {"1": 0, "5": 1, "15": 2}.get(str(vals["window_minutes"]), 1)
        try:
            load_val = float(cols[win_idx])
        except (ValueError, IndexError):
            return CheckStatus.ERROR, f"无法解析 /proc/loadavg: {stdout!r}", stdout, None, ""
        max_load = float(vals["max_load"])
        ok = load_val <= max_load
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"{vals['window_minutes']} 分钟平均负载 {load_val} ({'<= ' if ok else '> '}{max_load})",
            f"{load_val} (raw: {' '.join(cols[:3])})",
            None,
            "" if ok else "当前主机负载过高，建议排查繁忙进程后再部署。",
        )

    if tid == "R04":
        if exit_code != 0 or not stdout:
            return CheckStatus.ERROR, f"无法查询 {vals['unit_name']} 的 {vals['property_name']}（绝不用当前 SSH ulimit 冒充）", stderr or stdout, None, ""
        min_lim = int(vals["min_limit"])
        if stdout.lower() == "infinity":
            return CheckStatus.PASS, f"{vals['unit_name']} {vals['property_name']}=infinity (满足 >= {min_lim})", "infinity", None, ""
        try:
            actual_lim = int(stdout.splitlines()[0].strip())
        except ValueError:
            return CheckStatus.ERROR, f"资源限制值解析失败: {stdout!r}", stdout, None, ""
        ok = actual_lim >= min_lim
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"{vals['unit_name']} {vals['property_name']}={actual_lim} ({'满足' if ok else '低于'} >= {min_lim})",
            str(actual_lim),
            None,
            "" if ok else f"在 systemd drop-in 中设置 {vals['property_name']}={min_lim} 并执行 daemon-reload。",
        )

    if tid == "R05":
        kv = dict(
            line.split("=", 1) for line in stdout.splitlines() if "=" in line
        )
        if "NTPSynchronized" not in kv:
            return CheckStatus.ERROR, f"无法从 timedatectl 获取时钟同步标志: {stdout!r}", stdout, None, ""
        synced = kv["NTPSynchronized"].strip().lower() == "yes"
        req_sync = bool(vals["require_synchronized"])
        ok = synced if req_sync else True
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"系统时钟同步状态 NTPSynchronized={kv['NTPSynchronized']} (NTP={kv.get('NTP', 'unknown')})",
            stdout.replace("\n", ", "),
            None,
            "" if ok else "请检查 chronyd / systemd-timesyncd 上游 NTP 服务器可达性。",
        )

    if tid == "A03":
        if exit_code != 0 or not stdout:
            return (
                CheckStatus.ERROR,
                f"sshd -T 查询失败或无权限（绝不降级 grep /etc/ssh/sshd_config，TODO-010）: {stderr or stdout}",
                stderr or stdout,
                None,
                "确保为本次运行批准 A03 特权检查且主机存在 /usr/sbin/sshd。",
            )
        actual_val = stdout.splitlines()[0].strip()
        exp_val = str(vals["expected_value"]).strip()
        ok = actual_val.lower() == exp_val.lower()
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"sshd -T 生效项 {vals['option_key']}={actual_val} ({'符合' if ok else '不等于'} {exp_val})",
            actual_val,
            None,
            "" if ok else f"调整 /etc/ssh/sshd_config 中 {vals['option_key']} 为 {exp_val}。",
        )

    if tid == "A04":
        mode_raw = stdout.lower()
        policy = str(vals["expected_policy"])
        if policy == "any_recorded":
            return CheckStatus.PASS, f"安全访问控制状态已记录: {stdout}", stdout, None, ""
        if policy == "enforcing":
            ok = mode_raw in ("enforcing", "apparmor")
        else:
            ok = mode_raw in ("disabled", "permissive", "none", "apparmor")
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"MAC 安全模式实测为 '{stdout}' (策略要求: {policy})",
            stdout,
            None,
            "" if ok else f"请按部署规范调整 SELinux/AppArmor 模式 ({policy})。",
        )

    if tid == "D02":
        ok = exit_code == 0 and bool(stdout)
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"命令 '{vals['command_or_path']}' {'已找到: ' + stdout if ok else '不存在或不可执行'}",
            stdout or "NOT_FOUND",
            None,
            "" if ok else f"请在目标主机安装并提供可执行命令 '{vals['command_or_path']}'。",
        )

    if tid == "D03":
        if exit_code != 0 or not stdout:
            return CheckStatus.FAIL, f"运行时 {vals['runtime']} 未安装或不可用", stderr or stdout or "missing", None, f"请安装 {vals['runtime']} >= {vals['min_version']}。"
        m = re.search(r"(\d+\.\d+(?:\.\d+)?)", stdout)
        if not m:
            return CheckStatus.ERROR, f"无法提取运行时版本号: {stdout!r}", stdout, None, ""
        ver_str = m.group(1)
        ok = _parse_version_tuple(ver_str) >= _parse_version_tuple(str(vals["min_version"]))
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"{vals['runtime']} 版本 {ver_str} ({'满足' if ok else '低于'} >= {vals['min_version']})",
            ver_str,
            None,
            "" if ok else f"请升级 {vals['runtime']} 至 >= {vals['min_version']}。",
        )

    if tid == "D04":
        # TODO-008: Static ELF analysis only (never ldd). Missing readelf/objdump -> ERROR!
        if capability is not None and not any(
            t in capability.available_tools for t in ("readelf", "eu-readelf", "objdump")
        ):
            return (
                CheckStatus.ERROR,
                "目标机缺少静态 ELF 解析工具 (readelf/eu-readelf/objdump)，严禁降级调用 ldd 或仅靠文件存在推断兼容 (TODO-008)",
                "MISSING_STATIC_ELF_TOOL",
                None,
                "在目标机安装 binutils (readelf) 或 elfutils 后复测。",
            )
        if exit_code != 0:
            return CheckStatus.ERROR, f"静态 ELF 解析命令失败: {stderr or stdout}", stderr or stdout, None, ""
        soname = str(vals["required_soname"])
        needed_present = f"[{soname}]" in stdout or f"NEEDED               {soname}" in stdout or soname in stdout
        ldcache_resolved = f"{soname} (" in stdout or "=> /" in stdout
        ok = needed_present and ldcache_resolved
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"静态 ELF 核验 {vals['elf_path']} 依赖 {soname}: {'NEEDED 声明且系统库缓存已匹配' if ok else '缺失所需动态库映射'}",
            truncate_evidence_text(stdout, 512),
            None,
            "" if ok else f"安装提供 {soname} 的运行库包并刷新 ldconfig。",
        )

    if tid == "D05":
        if exit_code != 0:
            return CheckStatus.FAIL, f"配置文件 {vals['config_path']} 不存在或无法读取", stderr or "unreadable", None, ""
        pat = str(vals["expected_pattern"])
        ok = re.search(pat, stdout) is not None
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"配置文件 {vals['config_path']} {'匹配' if ok else '未匹配'}期望模式 /{pat}/",
            f"matched={ok} (已脱敏，共 {len(stdout.splitlines())} 行)",
            None,
            "" if ok else f"核对 {vals['config_path']} 中的配置键值。",
        )

    if tid == "L01":
        try:
            up_sec = float(stdout.split()[0])
        except (ValueError, IndexError):
            return CheckStatus.ERROR, f"无法解析 /proc/uptime: {stdout!r}", stdout, None, ""
        up_min = round(up_sec / 60.0, 1)
        req_min = int(vals["min_uptime_minutes"])
        ok = up_min >= req_min
        return (
            CheckStatus.PASS if ok else CheckStatus.FAIL,
            f"系统已连续运行 {up_min} 分钟 ({'满足' if ok else '低于'} >= {req_min} 分钟)",
            f"{up_min} min",
            None,
            "",
        )

    if tid in ("L02", "L03"):
        # TODO-009 / G07: Historical log check MUST prove window coverage before returning PASS;
        # however if violations already exceed threshold, immediately FAIL and note coverage!
        hrs = int(vals["window_hours"])
        allow_boot = bool(vals.get("allow_since_boot", False))
        if exit_code != 0 or "No journal files were opened due to insufficient permissions" in stderr:
            return (
                CheckStatus.ERROR,
                f"无权限读取系统 journal 日志，无法证明 {hrs}h 历史窗口覆盖 (TODO-009)",
                stderr or "PERMISSION_DENIED",
                f"0h / required {hrs}h (unverified)",
                "授予 systemd-journal 组读权限或启用受控特权日志变体。",
            )

        if tid == "L02":
            events = re.findall(r"Killed process \d+", stdout)
            count = len(set(events))
            max_allowed = int(vals["max_events"])
        else:
            events = [
                ln
                for ln in stdout.splitlines()
                if "Entered failed state" in ln or "Failed with result" in ln or "Main process exited, code=killed" in ln
            ]
            count = len(events)
            max_allowed = int(vals["max_failures"])

        coverage_tag_match = re.search(r"COVERAGE_HOURS=([0-9.]+)", stdout)
        covered_hours = float(coverage_tag_match.group(1)) if coverage_tag_match else (float(hrs) if "-- Logs begin" not in stdout and stdout else 0.0)
        boot_complete = "SINCE_BOOT_COMPLETE=1" in stdout
        coverage_desc = f"covered={covered_hours}h / target={hrs}h" + (" (since_boot)" if boot_complete else "")

        if count > max_allowed:
            return (
                CheckStatus.FAIL,
                f"历史窗口内检出 {count} 次异常事件，超过上限 {max_allowed} ({coverage_desc})",
                f"events={count} (max={max_allowed})",
                coverage_desc,
                "排查近期 OOM / 服务异常退出根因后再执行部署。",
            )

        coverage_sufficient = covered_hours >= hrs or (allow_boot and boot_complete)
        if not coverage_sufficient:
            return (
                CheckStatus.ERROR,
                f"虽未发现超限事件，但可读日志仅覆盖 {covered_hours}h < 要求窗口 {hrs}h（严禁将日志覆盖不足判为虚假 PASS，G07 / TODO-009）",
                f"events={count}, coverage={covered_hours}h<{hrs}h",
                coverage_desc,
                "确认持久化 journald 存储覆盖窗口，或对新装主机显式开启 allow_since_boot。",
            )

        return (
            CheckStatus.PASS,
            f"过去 {hrs}h 窗口覆盖完整 ({coverage_desc})，异常事件 {count} <= {max_allowed}",
            f"events={count}",
            coverage_desc,
            "",
        )

    return CheckStatus.PASS, f"{tid} 检查完成", stdout or "ok", None, ""


def normalize_host_goss_results(
    *,
    run_id: str,
    plan: PlanDocument,
    host_name: str,
    ssh_user: str,
    assigned_roles: list[str],
    templates: dict[str, TemplateSpec],
    capability: CapabilityRecord | None,
    standard_artifact: ExecutionArtifact | None,
    standard_goss_json: dict[str, Any] | None,
    privileged_artifact: ExecutionArtifact | None,
    privileged_goss_json: dict[str, Any] | None,
    privileged_unapproved_ids: set[str],
) -> list[CheckInstanceResult]:
    """Normalize Goss v0.4.9 JSON outputs back into ordered CheckInstanceResult items."""
    high_risk_root = ssh_user == "root"

    # Index Goss JSON result items by `(privilege_group, resource-type, resource-id, property)`
    indexed_goss: dict[tuple[PrivilegeGroup, str, str, str], dict[str, Any]] = {}
    for priv, payload in (
        (PrivilegeGroup.STANDARD, standard_goss_json),
        (PrivilegeGroup.PRIVILEGED, privileged_goss_json),
    ):
        if not payload or not isinstance(payload.get("results"), list):
            continue
        for item in payload["results"]:
            r_type = str(item.get("resource-type", ""))
            r_id = str(item.get("resource-id", ""))
            prop = str(item.get("property", ""))
            indexed_goss[(priv, r_type, r_id, prop)] = item

    origins_by_instance: dict[str, list[PropertyOrigin]] = {}
    resolved_by_instance: dict[str, dict[str, ResolvedParamValue]] = {}
    for art in (standard_artifact, privileged_artifact):
        if art is None:
            continue
        resolved_by_instance.update(art.resolved_params_by_instance)
        for orig in art.origins:
            origins_by_instance.setdefault(orig.instance_id, []).append(orig)

    results: list[CheckInstanceResult] = []
    for role in plan.roles:
        if role.role_id not in assigned_roles:
            continue
        for inst in role.instances:
            tpl = templates.get(inst.template_id)
            if tpl is None or tpl.kind == TemplateKind.CONTROL:
                continue

            if not inst.enabled:
                results.append(
                    CheckInstanceResult(
                        instance_id=inst.instance_id,
                        title=inst.title or tpl.name_zh,
                        role_id=role.role_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        importance=inst.importance,
                        privilege_group=tpl.privilege_group,
                        status=CheckStatus.SKIP,
                        summary="已在方案中显式停用 (SKIP ≠ PASS)",
                    )
                )
                continue

            # Unapproved PRIVILEGED check -> explicit ERROR (Section 9.3 / TODO-010)
            if (
                tpl.privilege_group == PrivilegeGroup.PRIVILEGED
                and tpl.template_id in privileged_unapproved_ids
            ):
                results.append(
                    CheckInstanceResult(
                        instance_id=inst.instance_id,
                        title=inst.title or tpl.name_zh,
                        role_id=role.role_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        importance=inst.importance,
                        privilege_group=tpl.privilege_group,
                        status=CheckStatus.ERROR,
                        summary=f"特权模板 {tpl.template_id} 未获得本次 Run 显式特权预授权，已安全拒绝执行",
                        remediation_hint=f"在执行确认单中勾选批准 {tpl.template_id} 或在 CLI 传入 --approve-privileged {tpl.template_id}。",
                        resolved_params=resolved_by_instance.get(inst.instance_id, {}),
                    )
                )
                continue

            inst_origins = origins_by_instance.get(inst.instance_id, [])
            inst_params = resolved_by_instance.get(inst.instance_id, {})
            if not inst_origins:
                results.append(
                    CheckInstanceResult(
                        instance_id=inst.instance_id,
                        title=inst.title or tpl.name_zh,
                        role_id=role.role_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        importance=inst.importance,
                        privilege_group=tpl.privilege_group,
                        status=CheckStatus.ERROR,
                        summary="缺少编译后的 Goss 规则映射记录",
                    )
                )
                continue

            # Custom Shell evaluation
            if tpl.kind == TemplateKind.CUSTOM_SHELL and tpl.shell_spec is not None:
                orig = inst_origins[0]
                goss_cmd = indexed_goss.get(
                    (orig.privilege_group, "command", orig.goss_resource_id, "exit-status")
                )
                if goss_cmd is None:
                    results.append(
                        CheckInstanceResult(
                            instance_id=inst.instance_id,
                            title=inst.title or tpl.name_zh,
                            role_id=role.role_id,
                            template_id=tpl.template_id,
                            template_version=tpl.version,
                            importance=inst.importance,
                            privilege_group=tpl.privilege_group,
                            status=CheckStatus.ERROR,
                            summary="未回收到 Goss command 执行结果",
                            resolved_params=inst_params,
                        )
                    )
                    continue

                outcome = evaluate_custom_shell_output(
                    tpl.shell_spec.assertions,
                    exit_code=int(goss_cmd.get("exit-status", 0)),
                    stdout=str(goss_cmd.get("stdout", "") or ""),
                    stderr=str(goss_cmd.get("stderr", "") or ""),
                    timed_out=bool(goss_cmd.get("timed-out", False)),
                    exec_error=str(goss_cmd.get("err", "") or "") or None,
                )
                ev = CheckEvidence(
                    run_id=run_id,
                    host_name=host_name,
                    role_id=role.role_id,
                    instance_id=inst.instance_id,
                    template_id=tpl.template_id,
                    template_version=tpl.version,
                    template_digest=tpl.digest or "",
                    goss_subrule_id=f"command:{orig.goss_resource_id}",
                    execution_identity=ssh_user,
                    privilege_group=orig.privilege_group,
                    expected=outcome.expected,
                    observed=outcome.observed,
                    evidence_depth=tpl.evidence_depth,
                    source="goss.command(custom_shell)",
                    high_risk_root_audit=high_risk_root,
                )
                results.append(
                    CheckInstanceResult(
                        instance_id=inst.instance_id,
                        title=inst.title or tpl.name_zh,
                        role_id=role.role_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        importance=inst.importance,
                        privilege_group=tpl.privilege_group,
                        status=outcome.status,
                        summary=outcome.summary,
                        resolved_params=inst_params,
                        evidences=[ev],
                    )
                )
                continue

            # Built-in Shell Command templates
            if inst_origins[0].goss_resource_type == "command":
                orig = inst_origins[0]
                goss_cmd = indexed_goss.get(
                    (orig.privilege_group, "command", orig.goss_resource_id, "exit-status")
                ) or indexed_goss.get(
                    (orig.privilege_group, "command", orig.goss_resource_id, "stdout")
                )
                if goss_cmd is None:
                    results.append(
                        CheckInstanceResult(
                            instance_id=inst.instance_id,
                            title=inst.title or tpl.name_zh,
                            role_id=role.role_id,
                            template_id=tpl.template_id,
                            template_version=tpl.version,
                            importance=inst.importance,
                            privilege_group=tpl.privilege_group,
                            status=CheckStatus.ERROR,
                            summary="未回收到 Goss 内置命令检查结果",
                            resolved_params=inst_params,
                        )
                    )
                    continue

                st, summary, obs, win_cov, hint = _evaluate_builtin_command_contract(
                    orig, inst_params, goss_cmd, capability
                )
                exec_id = (
                    "root(sudo)"
                    if orig.privilege_group == PrivilegeGroup.PRIVILEGED and ssh_user != "root"
                    else ssh_user
                )
                ev = CheckEvidence(
                    run_id=run_id,
                    host_name=host_name,
                    role_id=role.role_id,
                    instance_id=inst.instance_id,
                    template_id=tpl.template_id,
                    template_version=tpl.version,
                    template_digest=tpl.digest or "",
                    goss_subrule_id=f"command:{orig.goss_resource_id}",
                    execution_identity=exec_id,
                    privilege_group=orig.privilege_group,
                    expected=orig.expected_summary,
                    observed=obs,
                    evidence_depth=tpl.evidence_depth,
                    source=f"goss.command({tpl.template_id})",
                    window_coverage=win_cov,
                    high_risk_root_audit=high_risk_root,
                )
                results.append(
                    CheckInstanceResult(
                        instance_id=inst.instance_id,
                        title=inst.title or tpl.name_zh,
                        role_id=role.role_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        importance=inst.importance,
                        privilege_group=tpl.privilege_group,
                        status=st,
                        summary=summary,
                        remediation_hint=hint,
                        resolved_params=inst_params,
                        evidences=[ev],
                    )
                )
                continue

            # Native Goss Resource properties (supports smart-merged multi-property split!)
            evidences: list[CheckEvidence] = []
            prop_statuses: list[CheckStatus] = []
            prop_messages: list[str] = []

            for orig in inst_origins:
                goss_prop = indexed_goss.get(
                    (
                        orig.privilege_group,
                        orig.goss_resource_type,
                        orig.goss_resource_id,
                        orig.property_key,
                    )
                )
                if goss_prop is None:
                    prop_statuses.append(CheckStatus.ERROR)
                    prop_messages.append(
                        f"缺失子规则结果 {orig.goss_resource_type}:{orig.goss_resource_id}:{orig.property_key}"
                    )
                    continue

                err_msg = str(goss_prop.get("err", "") or "").strip()
                if err_msg or bool(goss_prop.get("timed-out", False)):
                    st_prop = CheckStatus.ERROR
                    obs_str = err_msg or "TIMED_OUT"
                    prop_messages.append(f"{orig.property_key} 探测异常: {obs_str}")
                elif bool(goss_prop.get("successful", False)):
                    st_prop = CheckStatus.PASS
                    obs_str = str(goss_prop.get("found", ""))
                    prop_messages.append(f"{orig.property_key}={obs_str}")
                else:
                    st_prop = CheckStatus.FAIL
                    obs_str = str(goss_prop.get("found", ""))
                    exp_str = str(goss_prop.get("expected", orig.expected_summary))
                    prop_messages.append(
                        f"{orig.goss_resource_id}.{orig.property_key} 实测 {obs_str} != 期望 {exp_str}"
                    )

                prop_statuses.append(st_prop)
                evidences.append(
                    CheckEvidence(
                        run_id=run_id,
                        host_name=host_name,
                        role_id=role.role_id,
                        instance_id=inst.instance_id,
                        template_id=tpl.template_id,
                        template_version=tpl.version,
                        template_digest=tpl.digest or "",
                        goss_subrule_id=f"{orig.goss_resource_type}:{orig.goss_resource_id}:{orig.property_key}",
                        execution_identity=ssh_user,
                        privilege_group=orig.privilege_group,
                        expected=orig.expected_summary,
                        observed=obs_str,
                        evidence_depth=tpl.evidence_depth,
                        source=f"goss.{orig.goss_resource_type}",
                        high_risk_root_audit=high_risk_root,
                    )
                )

            if CheckStatus.FAIL in prop_statuses:
                final_st = CheckStatus.FAIL
            elif CheckStatus.ERROR in prop_statuses:
                final_st = CheckStatus.ERROR
            else:
                final_st = CheckStatus.PASS

            results.append(
                CheckInstanceResult(
                    instance_id=inst.instance_id,
                    title=inst.title or tpl.name_zh,
                    role_id=role.role_id,
                    template_id=tpl.template_id,
                    template_version=tpl.version,
                    importance=inst.importance,
                    privilege_group=tpl.privilege_group,
                    status=final_st,
                    summary="；".join(prop_messages),
                    resolved_params=inst_params,
                    evidences=evidences,
                )
            )

    return results

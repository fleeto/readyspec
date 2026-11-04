"""Custom Shell output evaluator supporting Basic Assertions & Custom Result Protocol v1."""

from __future__ import annotations

import json
import math
import re
from dataclasses import dataclass

from readyspec.constants import (
    CUSTOM_RESULT_PROTOCOL_VERSION,
    MAX_CUSTOM_JSON_BYTES,
    MAX_EVIDENCE_BYTES,
    CheckStatus,
)
from readyspec.domain.models import (
    NumericOperator,
    ShellAssertionMode,
    ShellAssertionSpec,
)
from readyspec.domain.schema_validator import validate_against_schema


@dataclass(frozen=True)
class ShellEvaluationOutcome:
    status: CheckStatus
    summary: str
    expected: str
    observed: str


def truncate_evidence_text(text: str, max_bytes: int = MAX_EVIDENCE_BYTES) -> str:
    raw = text.encode("utf-8", errors="replace")
    if len(raw) <= max_bytes:
        return text
    clipped = raw[:max_bytes].decode("utf-8", errors="ignore")
    return f"{clipped}... [已按 {max_bytes} 字节上限截断]"


def evaluate_custom_shell_output(
    assertions: ShellAssertionSpec,
    *,
    exit_code: int,
    stdout: str,
    stderr: str = "",
    timed_out: bool = False,
    exec_error: str | None = None,
) -> ShellEvaluationOutcome:
    """Evaluate Shell execution or offline sandbox sample without running any shell commands."""
    if timed_out:
        return ShellEvaluationOutcome(
            status=CheckStatus.ERROR,
            summary="命令执行超时（不能将执行超时误判为业务断言 FAIL）",
            expected=f"exit == {assertions.expected_exit_code} within timeout",
            observed="TIMED_OUT",
        )
    if exec_error:
        return ShellEvaluationOutcome(
            status=CheckStatus.ERROR,
            summary=f"命令执行环境异常: {exec_error}",
            expected="command executable",
            observed=truncate_evidence_text(exec_error, 512),
        )
    if exit_code in (126, 127):
        reason = "命令不存在 (exit 127)" if exit_code == 127 else "无可执行权限 (exit 126)"
        return ShellEvaluationOutcome(
            status=CheckStatus.ERROR,
            summary=f"目标环境缺失解释器或命令: {reason}",
            expected="command available & executable",
            observed=truncate_evidence_text(stderr or stdout or f"exit={exit_code}", 512),
        )

    # Mode 2: Conditional ReadySpec Custom Result Protocol v1 (Section 7.3 / TODO-003)
    if assertions.mode == ShellAssertionMode.CUSTOM_RESULT_V1:
        raw_bytes = stdout.encode("utf-8", errors="replace")
        if len(raw_bytes) > MAX_CUSTOM_JSON_BYTES:
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"结构化结果超过 {MAX_CUSTOM_JSON_BYTES} 字节协议上限",
                expected=f"single-line JSON <= {MAX_CUSTOM_JSON_BYTES}B",
                observed=f"{len(raw_bytes)} bytes",
            )
        non_empty_lines = [ln for ln in stdout.splitlines() if ln.strip()]
        if len(non_empty_lines) != 1:
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"协议错误：stdout 必须仅包含单行 JSON 记录（实际 {len(non_empty_lines)} 行）",
                expected="1 JSON line",
                observed=truncate_evidence_text(stdout, 512),
            )
        try:
            parsed = json.loads(non_empty_lines[0])
        except json.JSONDecodeError as exc:
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"协议 JSON 解析失败: {exc}",
                expected=CUSTOM_RESULT_PROTOCOL_VERSION,
                observed=truncate_evidence_text(non_empty_lines[0], 512),
            )
        issues = validate_against_schema(parsed, "custom_result.v1.json")
        if issues:
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"不合法的结构化协议字段 ({issues[0].path}: {issues[0].message})；用户脚本严禁伪造 UNSUPPORTED/SKIP/READY",
                expected=CUSTOM_RESULT_PROTOCOL_VERSION,
                observed=truncate_evidence_text(non_empty_lines[0], 512),
            )
        proto_status = str(parsed["status"])
        if exit_code != assertions.expected_exit_code and proto_status == "PASS":
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"协议不一致：脚本退出码 ({exit_code}) 异常但输出声明 PASS，按安全规则拒绝采信 (Section 7.3)",
                expected=f"exit == {assertions.expected_exit_code}",
                observed=f"exit={exit_code}, status=PASS",
            )
        mapped = (
            CheckStatus.PASS
            if proto_status == "PASS"
            else (CheckStatus.FAIL if proto_status == "FAIL" else CheckStatus.ERROR)
        )
        return ShellEvaluationOutcome(
            status=mapped,
            summary=str(parsed["summary"]),
            expected=str(parsed.get("expected", "protocol PASS")),
            observed=str(parsed.get("observed", proto_status)),
        )

    # Mode 1: Basic Assertions (Section 7.2)
    failures: list[str] = []
    expected_parts: list[str] = [f"exit=={assertions.expected_exit_code}"]

    if exit_code != assertions.expected_exit_code:
        failures.append(f"退出码不匹配 (期望 {assertions.expected_exit_code}, 实际 {exit_code})")

    if assertions.stdout_exact is not None:
        expected_parts.append(f"stdout=={assertions.stdout_exact!r}")
        if stdout.strip() != assertions.stdout_exact.strip():
            failures.append(f"stdout 不等于期望文本 {assertions.stdout_exact!r}")

    if assertions.stdout_contains:
        expected_parts.append(f"stdout contains {assertions.stdout_contains!r}")
        if assertions.stdout_contains not in stdout:
            failures.append(f"stdout 未包含 {assertions.stdout_contains!r}")

    if assertions.stdout_not_contains:
        expected_parts.append(f"stdout !contains {assertions.stdout_not_contains!r}")
        if assertions.stdout_not_contains in stdout:
            failures.append(f"stdout 出现了禁止文本 {assertions.stdout_not_contains!r}")

    if assertions.stdout_regex:
        expected_parts.append(f"stdout ~ /{assertions.stdout_regex}/")
        if re.search(assertions.stdout_regex, stdout) is None:
            failures.append(f"stdout 不匹配正则 /{assertions.stdout_regex}/")

    if assertions.stderr_not_contains:
        expected_parts.append(f"stderr !contains {assertions.stderr_not_contains!r}")
        if assertions.stderr_not_contains in stderr:
            failures.append(f"stderr 出现了禁止文本 {assertions.stderr_not_contains!r}")

    if assertions.numeric_op is not None and assertions.numeric_threshold is not None:
        token = stdout.strip()
        if assertions.unit and token.endswith(assertions.unit):
            token = token[: -len(assertions.unit)].strip()
        try:
            num = float(token)
            if math.isnan(num) or math.isinf(num):
                raise ValueError("NaN/Infinity forbidden")
        except ValueError:
            return ShellEvaluationOutcome(
                status=CheckStatus.ERROR,
                summary=f"单数值断言解析失败：无法将 stdout {token!r} 解析为有限实数 (Section 7.2)",
                expected=f"numeric {assertions.numeric_op.value} {assertions.numeric_threshold}",
                observed=truncate_evidence_text(stdout.strip(), 256),
            )
        op = assertions.numeric_op
        thr = assertions.numeric_threshold
        expected_parts.append(f"value {op.value} {thr}{assertions.unit}")
        ok = True
        if op == NumericOperator.EQ:
            ok = num == thr
        elif op == NumericOperator.NE:
            ok = num != thr
        elif op == NumericOperator.GE:
            ok = num >= thr
        elif op == NumericOperator.GT:
            ok = num > thr
        elif op == NumericOperator.LE:
            ok = num <= thr
        elif op == NumericOperator.LT:
            ok = num < thr
        elif op == NumericOperator.RANGE:
            upper = assertions.numeric_max if assertions.numeric_max is not None else thr
            expected_parts[-1] = f"{thr} <= value <= {upper}{assertions.unit}"
            ok = thr <= num <= upper
        if not ok:
            failures.append(f"数值 {num}{assertions.unit} 不满足 {expected_parts[-1]}")

    obs_preview = truncate_evidence_text(
        f"exit={exit_code}; stdout={stdout.strip()!r}"
        + (f"; stderr={stderr.strip()!r}" if stderr.strip() else ""),
        1024,
    )
    if failures:
        return ShellEvaluationOutcome(
            status=CheckStatus.FAIL,
            summary="；".join(failures),
            expected=", ".join(expected_parts),
            observed=obs_preview,
        )
    return ShellEvaluationOutcome(
        status=CheckStatus.PASS,
        summary="自定义 Shell 断言全部满足",
        expected=", ".join(expected_parts),
        observed=obs_preview,
    )

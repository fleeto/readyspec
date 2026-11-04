"""Deterministic 3-level linear parameter resolution & safe POSIX environment encoding."""

from __future__ import annotations

import re
import shlex
from typing import Any

from readyspec.domain.models import (
    CheckInstance,
    InventoryHost,
    ParameterOverrideRule,
    ResolvedParamValue,
    TemplateSpec,
)

ENV_NAME_CLEAN_RE = re.compile(r"[^A-Z0-9_]")


class ParameterResolutionError(ValueError):
    """Raised on unauthorized override or ambiguous group-level override conflict (REQ-TPL-12/13)."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def resolve_instance_params_for_host(
    template: TemplateSpec,
    instance: CheckInstance,
    role_id: str,
    host: InventoryHost,
    runtime_overrides: list[ParameterOverrideRule],
) -> dict[str, ResolvedParamValue]:
    """Resolve parameters via: Template Default -> Instance Explicit -> Runtime Override (Group -> Host)."""
    param_specs = {p.name: p for p in template.parameters}
    resolved: dict[str, ResolvedParamValue] = {}

    # 1. Template defaults & 2. Role Instance explicit settings
    for name, p_spec in param_specs.items():
        if name in instance.explicit_null_params:
            resolved[name] = ResolvedParamValue(
                name=name,
                value=None,
                source="INSTANCE_EXPLICIT_NULL",
                sensitive=p_spec.sensitive,
            )
        elif name in instance.params:
            resolved[name] = ResolvedParamValue(
                name=name,
                value=instance.params[name],
                source="INSTANCE_EXPLICIT",
                sensitive=p_spec.sensitive,
            )
        else:
            resolved[name] = ResolvedParamValue(
                name=name,
                value=p_spec.default,
                source="TEMPLATE_DEFAULT",
                sensitive=p_spec.sensitive,
            )

    # 3. Runtime Overrides (Group-level -> Single-host-level)
    matching_rules = [
        rule
        for rule in runtime_overrides
        if rule.role_id == role_id and rule.instance_id == instance.instance_id
    ]
    if not matching_rules:
        return resolved

    allowed_overridable = set(instance.overridable_params)

    # Validate permission & group-level conflicts independent of list order
    group_hits: dict[str, list[tuple[str, Any]]] = {}
    host_hits: dict[str, list[Any]] = {}

    host_group_set = set(host.groups) | {"all"}
    for rule in matching_rules:
        if rule.param_name not in param_specs:
            raise ParameterResolutionError(
                "OVERRIDE_UNKNOWN_PARAM",
                f"运行时覆盖引用了未知参数 '{rule.param_name}' (实例 {instance.instance_id})。",
            )
        if rule.param_name not in allowed_overridable:
            raise ParameterResolutionError(
                "OVERRIDE_NOT_ALLOWED",
                f"参数 '{rule.param_name}' 未在方案实例 '{instance.instance_id}' 中勾选『允许部署时现场覆盖』，已拒绝越权修改 (REQ-TPL-13)。",
            )
        if rule.scope_type == "group" and rule.scope_target in host_group_set:
            group_hits.setdefault(rule.param_name, []).append((rule.scope_target, rule.value))
        elif rule.scope_type == "host" and rule.scope_target == host.name:
            host_hits.setdefault(rule.param_name, []).append(rule.value)

    for p_name, grp_vals in group_hits.items():
        first_val = grp_vals[0][1]
        for _grp_name, val in grp_vals[1:]:
            if val != first_val:
                groups_desc = ", ".join(f"{g}={v!r}" for g, v in grp_vals)
                raise ParameterResolutionError(
                    "OVERRIDE_CONFLICT",
                    f"主机 '{host.name}' 同时命中多个主机组对参数 '{p_name}' 的不同覆盖值 ({groups_desc})，已按确定性原则拒绝歧义 (REQ-TPL-12)。",
                )
        resolved[p_name] = ResolvedParamValue(
            name=p_name,
            value=first_val,
            source=f"RUNTIME_GROUP_OVERRIDE:{grp_vals[0][0]}",
            sensitive=param_specs[p_name].sensitive,
        )

    for p_name, h_vals in host_hits.items():
        first_h = h_vals[0]
        for val in h_vals[1:]:
            if val != first_h:
                raise ParameterResolutionError(
                    "OVERRIDE_CONFLICT",
                    f"主机 '{host.name}' 对同一参数 '{p_name}' 提供了多条冲突单机覆盖。",
                )
        resolved[p_name] = ResolvedParamValue(
            name=p_name,
            value=first_h,
            source=f"RUNTIME_HOST_OVERRIDE:{host.name}",
            sensitive=param_specs[p_name].sensitive,
        )

    return resolved


def build_readonly_shell_env_prefix(resolved: dict[str, ResolvedParamValue]) -> str:
    """Encode resolved parameters into safe POSIX readonly RS_PARAM_<KEY> exports (REQ-SEC-10 / CHG-003)."""
    if not resolved:
        return ""
    statements: list[str] = []
    for key in sorted(resolved):
        env_key = "RS_PARAM_" + ENV_NAME_CLEAN_RE.sub("_", key.upper())
        raw_val = resolved[key].value
        if raw_val is None:
            str_val = ""
        elif isinstance(raw_val, bool):
            str_val = "true" if raw_val else "false"
        elif isinstance(raw_val, list):
            str_val = ",".join(str(x) for x in raw_val)
        else:
            str_val = str(raw_val)
        quoted = shlex.quote(str_val)
        statements.append(f"{env_key}={quoted}; readonly {env_key}; export {env_key}")
    return "; ".join(statements) + "; "

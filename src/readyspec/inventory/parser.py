"""Native parser for Quick-IP, Ansible INI, and Ansible YAML inventories + TODO-016 deduplication."""

from __future__ import annotations

import re
import shlex
from typing import Any

from readyspec.domain.models import InventoryHost, InventorySnapshot, PlanDocument
from readyspec.domain.safe_yaml import SafeYamlError, load_safe_yaml


class InventoryParseError(ValueError):
    """Raised on malformed inventory syntax or duplicate endpoint conflict (TODO-016)."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def _extract_proxy_jump(vars_dict: dict[str, Any]) -> str:
    if "proxy_jump" in vars_dict and vars_dict["proxy_jump"]:
        return str(vars_dict["proxy_jump"]).strip()
    common_args = str(vars_dict.get("ansible_ssh_common_args", "") or "")
    if "ProxyJump=" in common_args:
        m = re.search(r"ProxyJump=([^\s'\"]+)", common_args)
        if m:
            return m.group(1)
    if "-J" in common_args:
        parts = shlex.split(common_args)
        for idx, tok in enumerate(parts):
            if tok == "-J" and idx + 1 < len(parts):
                return parts[idx + 1]
    return ""


def _parse_scalar(val: str) -> Any:
    if (val.startswith('"') and val.endswith('"')) or (
        val.startswith("'") and val.endswith("'")
    ):
        val = val[1:-1]
    low = val.lower()
    if low == "true":
        return True
    if low == "false":
        return False
    if re.fullmatch(r"-?\d+", val):
        return int(val)
    if re.fullmatch(r"-?\d+\.\d+", val):
        return float(val)
    return val


def _validate_endpoint_dedup(hosts: list[InventoryHost]) -> None:
    """Enforce TODO-016 'One-Host-One-Name' static endpoint uniqueness `(address, port, proxy_jump)`."""
    if not hosts:
        raise InventoryParseError("INVENTORY_EMPTY", "主机清单为空，至少需要提供 1 台目标主机。")

    seen_endpoints: dict[tuple[str, int, str], str] = {}
    for host in hosts:
        key = (host.address.strip().lower(), host.port, host.proxy_jump.strip().lower())
        existing_name = seen_endpoints.get(key)
        if existing_name is not None and existing_name != host.name:
            raise InventoryParseError(
                "INVENTORY_DUPLICATE_ENDPOINT",
                f"违反『一机一名』规则 (TODO-016)：主机名 '{existing_name}' 与 '{host.name}' 指向同一连接端点 "
                f"{host.address}:{host.port}"
                + (f" (jump={host.proxy_jump})" if host.proxy_jump else "")
                + "。如需同机承担多业务角色，请将同一主机名同时放入多个组。",
            )
        seen_endpoints[key] = host.name


def parse_quick_ip_list(
    text: str,
    *,
    default_user: str = "ops",
    default_port: int = 22,
    default_group: str = "all",
) -> InventorySnapshot:
    """Parse Quick-IP input (`10.0.1.11`, `ops@10.0.1.12:2222`, `web01=root@10.0.1.13`)."""
    tokens: list[str] = []
    for raw_line in text.splitlines():
        line = raw_line.split("#", 1)[0].strip()
        if not line:
            continue
        for part in re.split(r"[,;\s]+", line):
            if part.strip():
                tokens.append(part.strip())

    hosts_by_name: dict[str, InventoryHost] = {}
    for tok in tokens:
        alias: str | None = None
        rest = tok
        if "=" in tok:
            alias, rest = tok.split("=", 1)
            alias = alias.strip()
            rest = rest.strip()

        user = default_user
        if "@" in rest:
            user, rest = rest.split("@", 1)
            user = user.strip() or default_user

        port = default_port
        address = rest
        if ":" in rest and not rest.startswith("["):
            addr_part, port_part = rest.rsplit(":", 1)
            if port_part.isdigit():
                address = addr_part
                port = int(port_part)
            else:
                raise InventoryParseError("INVALID_PORT", f"无效端口格式: '{tok}'")

        if not address:
            raise InventoryParseError("INVALID_HOST_ENTRY", f"无效主机条目: '{tok}'")

        name = alias or address
        if name in hosts_by_name:
            existing = hosts_by_name[name]
            if (existing.address, existing.port, existing.ssh_user) != (address, port, user):
                raise InventoryParseError(
                    "HOST_REDEFINITION_CONFLICT",
                    f"主机名 '{name}' 被重复定义为不同连接参数。",
                )
            continue

        hosts_by_name[name] = InventoryHost(
            name=name,
            address=address,
            port=port,
            ssh_user=user,
            groups=[default_group],
        )

    host_list = list(hosts_by_name.values())
    _validate_endpoint_dedup(host_list)
    groups = {default_group: [h.name for h in host_list]}
    return InventorySnapshot(source_mode="quick_ip", hosts=host_list, groups=groups)


def parse_ansible_ini(text: str, *, default_user: str = "ops") -> InventorySnapshot:
    """Clean-room parser for Ansible INI inventory syntax (`[group]`, `[group:vars]`, `[group:children]`)."""
    current_group = "ungrouped"
    section_kind = "hosts"  # hosts | vars | children

    raw_host_defs: dict[str, dict[str, Any]] = {}
    group_direct_hosts: dict[str, list[str]] = {"ungrouped": []}
    group_children: dict[str, list[str]] = {}
    group_vars: dict[str, dict[str, Any]] = {}

    for line_no, raw_line in enumerate(text.splitlines(), start=1):
        stripped = raw_line.strip()
        if not stripped or stripped.startswith("#") or stripped.startswith(";"):
            continue
        # Strip inline comment when preceded by whitespace
        stripped = re.sub(r"\s+[#;].*$", "", stripped).strip()
        if not stripped:
            continue

        if stripped.startswith("[") and stripped.endswith("]"):
            header = stripped[1:-1].strip()
            if ":" in header:
                grp, modifier = header.split(":", 1)
                grp = grp.strip()
                modifier = modifier.strip().lower()
                if modifier not in ("vars", "children"):
                    raise InventoryParseError(
                        "INI_UNKNOWN_MODIFIER",
                        f"第 {line_no} 行不支持的段落修饰符 ':{modifier}'（支持 :vars 与 :children）。",
                    )
                current_group = grp
                section_kind = modifier
            else:
                current_group = header
                section_kind = "hosts"
            group_direct_hosts.setdefault(current_group, [])
            continue

        if section_kind == "vars":
            if "=" not in stripped:
                raise InventoryParseError(
                    "INI_INVALID_VAR",
                    f"第 {line_no} 行 [{current_group}:vars] 变量定义缺少 '=': {stripped}",
                )
            k, v = stripped.split("=", 1)
            group_vars.setdefault(current_group, {})[k.strip()] = _parse_scalar(v.strip())
            continue

        if section_kind == "children":
            child_grp = stripped.split()[0]
            group_children.setdefault(current_group, []).append(child_grp)
            group_direct_hosts.setdefault(child_grp, [])
            continue

        # Host line: `web-01 ansible_host=10.0.1.11 ansible_port=2222 custom_var=foo`
        try:
            parts = shlex.split(stripped)
        except ValueError as exc:
            raise InventoryParseError(
                "INI_SYNTAX_ERROR", f"第 {line_no} 行引号解析失败: {exc}"
            ) from exc
        if not parts:
            continue

        host_token = parts[0]
        inline_vars: dict[str, Any] = {}
        if ":" in host_token and not host_token.startswith("["):
            h_part, p_part = host_token.rsplit(":", 1)
            if p_part.isdigit():
                host_token = h_part
                inline_vars["ansible_port"] = int(p_part)

        for kv in parts[1:]:
            if "=" not in kv:
                raise InventoryParseError(
                    "INI_INVALID_HOST_VAR",
                    f"第 {line_no} 行主机变量格式错误 '{kv}'，应为 key=value。",
                )
            k, v = kv.split("=", 1)
            inline_vars[k.strip()] = _parse_scalar(v.strip())

        entry = raw_host_defs.setdefault(host_token, {"groups": [], "inline_vars": {}})
        if current_group not in entry["groups"]:
            entry["groups"].append(current_group)
        entry["inline_vars"].update(inline_vars)
        if host_token not in group_direct_hosts.setdefault(current_group, []):
            group_direct_hosts[current_group].append(host_token)

    # Expand `:children` hierarchy with cycle protection
    def resolve_group_hosts(grp: str, visited: set[str]) -> list[str]:
        if grp in visited:
            return []
        visited = visited | {grp}
        members = list(group_direct_hosts.get(grp, []))
        for child in group_children.get(grp, []):
            for h in resolve_group_hosts(child, visited):
                if h not in members:
                    members.append(h)
        return members

    expanded_groups: dict[str, list[str]] = {}
    all_group_names = set(group_direct_hosts) | set(group_children) | set(group_vars)
    for grp in sorted(all_group_names):
        if grp in ("all", "ungrouped") and not group_direct_hosts.get(grp):
            continue
        members = resolve_group_hosts(grp, set())
        if members:
            expanded_groups[grp] = members
            for h in members:
                if grp not in raw_host_defs[h]["groups"]:
                    raw_host_defs[h]["groups"].append(grp)

    hosts: list[InventoryHost] = []
    global_vars = dict(group_vars.get("all", {}))
    for host_name, info in raw_host_defs.items():
        merged_vars: dict[str, Any] = dict(global_vars)
        for grp in info["groups"]:
            merged_vars.update(group_vars.get(grp, {}))
        merged_vars.update(info["inline_vars"])

        address = str(merged_vars.get("ansible_host", host_name))
        port = int(merged_vars.get("ansible_port", 22))
        ssh_user = str(merged_vars.get("ansible_user", default_user))
        proxy_jump = _extract_proxy_jump(merged_vars)
        clean_groups = [g for g in info["groups"] if g != "ungrouped"]
        hosts.append(
            InventoryHost(
                name=host_name,
                address=address,
                port=port,
                ssh_user=ssh_user,
                proxy_jump=proxy_jump,
                groups=clean_groups,
                host_vars=merged_vars,
            )
        )

    _validate_endpoint_dedup(hosts)
    expanded_groups["all"] = [h.name for h in hosts]
    return InventorySnapshot(source_mode="ansible_ini", hosts=hosts, groups=expanded_groups)


def parse_ansible_yaml(text: str, *, default_user: str = "ops") -> InventorySnapshot:
    """Clean-room parser for Ansible YAML inventory syntax (`all.hosts` & `all.children`)."""
    try:
        data = load_safe_yaml(text)
    except SafeYamlError as exc:
        raise InventoryParseError(exc.code, exc.message) from exc

    if not isinstance(data, dict):
        raise InventoryParseError("YAML_INVENTORY_INVALID", "YAML Inventory 顶层必须为字典。")

    host_vars_acc: dict[str, dict[str, Any]] = {}
    host_groups_acc: dict[str, list[str]] = {}
    groups_map: dict[str, list[str]] = {}

    def walk_group(group_name: str, node: Any, inherited_vars: dict[str, Any]) -> list[str]:
        if not isinstance(node, dict):
            return []
        local_vars = dict(inherited_vars)
        raw_vars = node.get("vars")
        if isinstance(raw_vars, dict):
            local_vars.update(raw_vars)

        collected: list[str] = []
        raw_hosts = node.get("hosts")
        if isinstance(raw_hosts, dict):
            for h_name, h_cfg in raw_hosts.items():
                h_str = str(h_name)
                merged = dict(local_vars)
                if isinstance(h_cfg, dict):
                    merged.update(h_cfg)
                host_vars_acc.setdefault(h_str, {}).update(merged)
                grp_list = host_groups_acc.setdefault(h_str, [])
                if group_name != "all" and group_name not in grp_list:
                    grp_list.append(group_name)
                if h_str not in collected:
                    collected.append(h_str)

        raw_children = node.get("children")
        if isinstance(raw_children, dict):
            for child_name, child_node in raw_children.items():
                child_hosts = walk_group(str(child_name), child_node, local_vars)
                for h_str in child_hosts:
                    grp_list = host_groups_acc.setdefault(h_str, [])
                    if group_name != "all" and group_name not in grp_list:
                        grp_list.append(group_name)
                    if h_str not in collected:
                        collected.append(h_str)

        if group_name != "all" and collected:
            groups_map[group_name] = collected
        return collected

    if "all" in data and isinstance(data["all"], dict):
        walk_group("all", data["all"], {})
    else:
        for grp_key, grp_val in data.items():
            walk_group(str(grp_key), grp_val, {})

    hosts: list[InventoryHost] = []
    for h_name, vars_dict in host_vars_acc.items():
        address = str(vars_dict.get("ansible_host", h_name))
        port = int(vars_dict.get("ansible_port", 22))
        ssh_user = str(vars_dict.get("ansible_user", default_user))
        proxy_jump = _extract_proxy_jump(vars_dict)
        hosts.append(
            InventoryHost(
                name=h_name,
                address=address,
                port=port,
                ssh_user=ssh_user,
                proxy_jump=proxy_jump,
                groups=host_groups_acc.get(h_name, []),
                host_vars=vars_dict,
            )
        )

    _validate_endpoint_dedup(hosts)
    groups_map["all"] = [h.name for h in hosts]
    return InventorySnapshot(source_mode="ansible_yaml", hosts=hosts, groups=groups_map)


def parse_inventory_auto(
    text: str, *, mode: str = "auto", default_user: str = "ops", default_port: int = 22
) -> InventorySnapshot:
    """Parse inventory according to explicit mode (`quick_ip`, `ansible_ini`, `ansible_yaml`, or `auto`)."""
    if mode == "quick_ip":
        return parse_quick_ip_list(text, default_user=default_user, default_port=default_port)
    if mode == "ansible_ini":
        return parse_ansible_ini(text, default_user=default_user)
    if mode == "ansible_yaml":
        return parse_ansible_yaml(text, default_user=default_user)

    stripped = text.lstrip()
    if stripped.startswith("["):
        return parse_ansible_ini(text, default_user=default_user)
    if ("all:" in stripped or "hosts:" in stripped or "children:" in stripped) and ":" in stripped:
        try:
            return parse_ansible_yaml(text, default_user=default_user)
        except InventoryParseError as exc:
            if exc.code == "INVENTORY_DUPLICATE_ENDPOINT":
                raise
    if "ansible_host=" in stripped or "ansible_user=" in stripped:
        return parse_ansible_ini(text, default_user=default_user)
    return parse_quick_ip_list(text, default_user=default_user, default_port=default_port)


def auto_suggest_role_mapping(
    plan: PlanDocument, inventory: InventorySnapshot
) -> dict[str, list[str]]:
    """Auto-bind single-role plans or match role IDs/names to inventory group names (Act 3 Step 3)."""
    mapping: dict[str, list[str]] = {r.role_id: [] for r in plan.roles}
    all_hosts = [h.name for h in inventory.hosts]
    if len(plan.roles) == 1:
        mapping[plan.roles[0].role_id] = list(all_hosts)
        return mapping

    lower_groups = {g.lower(): members for g, members in inventory.groups.items()}
    for role in plan.roles:
        rid_low = role.role_id.lower()
        if rid_low in lower_groups:
            mapping[role.role_id] = list(lower_groups[rid_low])
            continue
        for grp_low, members in lower_groups.items():
            if grp_low != "all" and (rid_low in grp_low or grp_low in rid_low):
                mapping[role.role_id] = list(members)
                break
    return mapping

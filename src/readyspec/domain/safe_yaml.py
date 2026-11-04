"""Safe YAML parser enforcing size limits, duplicate-key rejection, and bounded aliases."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

from readyspec.constants import MAX_PLAN_BYTES

MAX_YAML_ALIASES = 100


class SafeYamlError(ValueError):
    """Raised when YAML input violates ReadySpec safety or structural rules."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


class DuplicateYamlKeyError(SafeYamlError):
    """Raised when duplicate mapping keys appear in YAML input (REQ-PLAN-10)."""

    def __init__(self, key: Any, mark: Any = None) -> None:
        loc = f" at line {mark.line + 1}, column {mark.column + 1}" if mark is not None else ""
        super().__init__(
            "YAML_DUPLICATE_KEY",
            f"YAML 存在重复键 '{key}'{loc}，已拒绝静默覆盖。",
        )


class _SafeUniqueKeyLoader(yaml.SafeLoader):
    """SafeLoader subclass rejecting duplicate keys and unbounded alias expansion."""

    def __init__(self, stream: Any) -> None:
        super().__init__(stream)
        self._alias_expansions = 0

    def compose_node(self, parent: Any, index: Any) -> Any:
        if self.check_event(yaml.AliasEvent):
            self._alias_expansions += 1
            if self._alias_expansions > MAX_YAML_ALIASES:
                event = self.peek_event()
                raise SafeYamlError(
                    "YAML_ALIAS_LIMIT",
                    f"YAML 别名展开次数超过上限 ({MAX_YAML_ALIASES})，疑似递归/膨胀载荷 (line {event.start_mark.line + 1})。",
                )
        return super().compose_node(parent, index)

    def construct_mapping(self, node: yaml.MappingNode, deep: bool = False) -> dict[Any, Any]:
        self.flatten_mapping(node)
        mapping: dict[Any, Any] = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            try:
                hash(key)
            except TypeError as exc:
                raise SafeYamlError(
                    "YAML_UNHASHABLE_KEY",
                    f"YAML 映射键不可哈希: {key!r}",
                ) from exc
            if key in mapping:
                raise DuplicateYamlKeyError(key, key_node.start_mark)
            mapping[key] = self.construct_object(value_node, deep=deep)
        return mapping


def load_safe_yaml(content: str | bytes, *, max_bytes: int = MAX_PLAN_BYTES) -> Any:
    """Safely parse YAML text/bytes with size cap, duplicate-key check, and SafeLoader."""
    raw_bytes = content.encode("utf-8") if isinstance(content, str) else content
    if len(raw_bytes) > max_bytes:
        raise SafeYamlError(
            "YAML_SIZE_EXCEEDED",
            f"输入大小 ({len(raw_bytes)} 字节) 超过安全上限 ({max_bytes} 字节)。",
        )
    try:
        text = raw_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise SafeYamlError("YAML_INVALID_UTF8", f"YAML 不是合法 UTF-8 编码: {exc}") from exc

    try:
        return yaml.load(text, Loader=_SafeUniqueKeyLoader)
    except SafeYamlError:
        raise
    except yaml.YAMLError as exc:
        raise SafeYamlError("YAML_SYNTAX_ERROR", f"YAML 语法解析失败: {exc}") from exc


def load_safe_yaml_file(path: Path, *, max_bytes: int = MAX_PLAN_BYTES) -> Any:
    """Safely read and parse a YAML file with pre-read stat size check."""
    if not path.exists() or not path.is_file():
        raise SafeYamlError("YAML_FILE_NOT_FOUND", f"找不到文件: {path}")
    size = path.stat().st_size
    if size > max_bytes:
        raise SafeYamlError(
            "YAML_SIZE_EXCEEDED",
            f"文件 {path.name} 大小 ({size} 字节) 超过安全上限 ({max_bytes} 字节)。",
        )
    return load_safe_yaml(path.read_bytes(), max_bytes=max_bytes)


def dump_safe_yaml(data: Any) -> str:
    """Serialize plain Python structures into clean UTF-8 YAML."""
    return yaml.safe_dump(
        data,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
        width=100,
    )

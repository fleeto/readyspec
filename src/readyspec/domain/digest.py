"""RFC 8785 (JCS) canonical JSON SHA-256 digest computation (TODO-011 / REQ-PLAN-14)."""

from __future__ import annotations

import copy
import hashlib
from typing import Any

import rfc8785

NON_SEMANTIC_PLAN_FIELDS = frozenset({"digest", "updated_at", "release_manifest"})
NON_SEMANTIC_TEMPLATE_FIELDS = frozenset({"digest"})


def canonical_json_bytes(payload: Any) -> bytes:
    """Serialize JSON-compatible Python data into RFC 8785 canonical UTF-8 bytes."""
    return rfc8785.dumps(payload)


def sha256_prefixed(data: bytes) -> str:
    """Compute sha256:<64-hex> string for raw bytes."""
    return f"sha256:{hashlib.sha256(data).hexdigest()}"


def compute_canonical_digest(payload: Any) -> str:
    """Compute RFC 8785 canonical SHA-256 digest (`sha256:<hex>`)."""
    return sha256_prefixed(canonical_json_bytes(payload))


def compute_template_semantic_digest(template_dict: dict[str, Any]) -> str:
    """Compute deterministic digest for a TemplateSpec excluding self-referential digest."""
    cleaned = {
        k: copy.deepcopy(v)
        for k, v in template_dict.items()
        if k not in NON_SEMANTIC_TEMPLATE_FIELDS
    }
    return compute_canonical_digest(cleaned)


def _embedded_sort_key(item: dict[str, Any]) -> str:
    return f"{item.get('template_id', '')}@{item.get('version', '')}"


def compute_plan_semantic_digest(plan_dict: dict[str, Any]) -> str:
    """Compute deterministic digest for a PlanDocument excluding non-semantic metadata."""
    cleaned = {
        k: copy.deepcopy(v)
        for k, v in plan_dict.items()
        if k not in NON_SEMANTIC_PLAN_FIELDS
    }
    if "embedded_templates" in cleaned and isinstance(cleaned["embedded_templates"], list):
        cleaned["embedded_templates"] = sorted(
            cleaned["embedded_templates"],
            key=_embedded_sort_key,
        )
    return compute_canonical_digest(cleaned)

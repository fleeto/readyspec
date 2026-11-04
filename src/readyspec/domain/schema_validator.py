"""JSON Schema (Draft 2020-12) validation utilities for Plan, Template, Result, and Report."""

from __future__ import annotations

import json
from dataclasses import dataclass
from functools import lru_cache
from importlib import resources
from typing import Any

from jsonschema import Draft202012Validator


@dataclass(frozen=True)
class SchemaIssue:
    path: str
    message: str


@lru_cache(maxsize=8)
def _load_validator(schema_filename: str) -> Draft202012Validator:
    schema_bytes = (
        resources.files("readyspec.schemas").joinpath(schema_filename).read_bytes()
    )
    schema_obj = json.loads(schema_bytes.decode("utf-8"))
    Draft202012Validator.check_schema(schema_obj)
    return Draft202012Validator(schema_obj)


def validate_against_schema(payload: Any, schema_filename: str) -> list[SchemaIssue]:
    """Validate payload against one of the bundled Draft 2020-12 JSON schemas."""
    validator = _load_validator(schema_filename)
    issues: list[SchemaIssue] = []
    for err in sorted(validator.iter_errors(payload), key=lambda e: list(e.absolute_path)):
        json_path = ".".join(str(part) for part in err.absolute_path) or "<root>"
        issues.append(SchemaIssue(path=json_path, message=err.message))
    return issues

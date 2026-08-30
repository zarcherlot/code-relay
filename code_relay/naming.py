"""Canonical Code Relay naming constants."""
from __future__ import annotations

import os
from pathlib import Path

PRODUCT_NAME = "Code Relay"
META_DIR = ".code-relay"
URI_PREFIX = "code-relay://join/"
SECRET_ENV = "CODE_RELAY_INVITE_SECRET"


def meta_dir(root: Path, *, create: bool = False) -> Path:
    path = root.resolve() / META_DIR
    if create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def secret_from_env() -> str | None:
    return os.environ.get(SECRET_ENV)


def is_join_uri(value: str) -> bool:
    return isinstance(value, str) and value.startswith(URI_PREFIX)


def canonical_join_uri(token: str) -> str:
    return URI_PREFIX + token

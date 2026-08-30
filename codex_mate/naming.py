"""Naming and compatibility helpers for the Code Relay migration.

New installations use the ``code-relay`` names.  Existing checkouts created
by the MVP continue to work through the legacy aliases and directory layout.
"""
from __future__ import annotations

import os
from pathlib import Path

PRODUCT_NAME = "Code Relay"
NEW_META_DIR = ".code-relay"
LEGACY_META_DIR = ".codex-relay"
NEW_URI_PREFIX = "code-relay://join/"
LEGACY_URI_PREFIX = "codex-relay://join/"
NEW_SECRET_ENV = "CODE_RELAY_INVITE_SECRET"
LEGACY_SECRET_ENV = "CODEX_RELAY_INVITE_SECRET"


def meta_dir(root: Path, *, create: bool = False) -> Path:
    """Return the canonical or legacy metadata directory for a project."""
    root = root.resolve()
    current = root / NEW_META_DIR
    legacy = root / LEGACY_META_DIR
    if current.exists() or not legacy.exists():
        if create:
            current.mkdir(parents=True, exist_ok=True)
        return current
    return legacy


def legacy_meta_dir(root: Path, *, create: bool = False) -> Path:
    path = root.resolve() / LEGACY_META_DIR
    if create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def secret_from_env() -> str | None:
    """Read the new secret variable first, then the legacy variable."""
    return os.environ.get(NEW_SECRET_ENV) or os.environ.get(LEGACY_SECRET_ENV)


def is_join_uri(value: str) -> bool:
    return isinstance(value, str) and value.startswith((NEW_URI_PREFIX, LEGACY_URI_PREFIX))


def canonical_join_uri(token: str) -> str:
    return NEW_URI_PREFIX + token

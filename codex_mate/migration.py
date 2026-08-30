"""Project metadata migration helpers for the Code Relay rename."""
from __future__ import annotations

import json
import shutil
from pathlib import Path
from typing import Any

from .naming import LEGACY_META_DIR, NEW_META_DIR
from .protocol import ProtocolError


def migrate_project(root: Path) -> dict[str, Any]:
    root = root.resolve()
    legacy, current = root / LEGACY_META_DIR, root / NEW_META_DIR
    if not legacy.exists():
        return {"status": "not_needed", "root": str(root), "metadata": str(current)}
    if legacy.is_symlink() or (current.exists() and current.is_symlink()):
        raise ProtocolError("拒绝迁移符号链接元数据目录")
    current.mkdir(parents=True, exist_ok=True)
    copied: list[str] = []
    for source in legacy.rglob("*"):
        if not source.is_file():
            continue
        relative = source.relative_to(legacy)
        destination = current / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        if not destination.exists():
            shutil.copy2(source, destination)
            copied.append(str(relative))
    marker = current / "migration.json"
    marker.write_text(json.dumps({"from": LEGACY_META_DIR, "to": NEW_META_DIR, "copied": copied}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return {"status": "migrated", "root": str(root), "metadata": str(current), "copied": copied}

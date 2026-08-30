"""Validation helpers for the small on-disk Code Relay configuration contract."""
from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

from .protocol import ProtocolError

REF_RE = re.compile(r"^refs/heads/[A-Za-z0-9._/-]+$")


def load_binding(path: Path) -> dict[str, Any]:
    try:
        if path.is_symlink():
            raise ProtocolError(f"拒绝读取符号链接绑定配置: {path}")
        value = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ProtocolError(f"找不到绑定配置: {path}") from exc
    except (OSError, json.JSONDecodeError) as exc:
        raise ProtocolError(f"绑定配置无法读取: {path}") from exc
    if not isinstance(value, dict) or value.get("schema_version") != 1:
        raise ProtocolError("绑定配置 schema_version 必须为 1")
    repository = value.get("repository")
    if not isinstance(repository, str) or not repository or len(repository) > 2048 or any(ord(char) < 32 or char.isspace() for char in repository):
        raise ProtocolError("绑定配置 repository 非法")
    if repository.lower().startswith(("ext::", "file://", "git+file://", "fd::")):
        raise ProtocolError("绑定配置禁止使用本地协议或 ext transport")
    ref = value.get("ref")
    if not isinstance(ref, str) or not REF_RE.fullmatch(ref) or ".." in ref or "//" in ref or ref.endswith("/"):
        raise ProtocolError("绑定配置 ref 非法")
    runtime = value.get("runtime")
    if runtime is not None and runtime not in {"go", "python"}:
        raise ProtocolError("绑定配置 runtime 必须是 go 或 python")
    return value

"""Minimal secret-free JSON diagnostics shared by long-running Python components."""
from __future__ import annotations

import json
import sys
from typing import Any

from .protocol import utc_now


def emit(event: str, **fields: Any) -> None:
    record = {"timestamp": utc_now(), "event": event}
    for key, value in fields.items():
        if key.lower() in {"token", "secret", "password", "authorization"}:
            continue
        record[key] = value
    print(json.dumps(record, ensure_ascii=False, separators=(",", ":")), file=sys.stderr, flush=True)

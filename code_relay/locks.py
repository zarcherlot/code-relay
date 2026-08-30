"""Small cross-platform, crash-tolerant project lock."""
from __future__ import annotations

import json
import os
import time
from pathlib import Path

from .protocol import ProtocolError


class ProjectLock:
    def __init__(self, root: Path, name: str, timeout: float = 10.0, stale_after: float = 3600.0):
        safe_name = "".join(char if char.isalnum() or char in "._-" else "_" for char in name)
        self.path = root.resolve() / ".code-relay" / "locks" / f"{safe_name}.lock"
        self.timeout = max(0.1, timeout)
        self.stale_after = max(60.0, stale_after)
        self.acquired = False

    def __enter__(self) -> "ProjectLock":
        self.path.parent.mkdir(parents=True, exist_ok=True)
        deadline = time.monotonic() + self.timeout
        while True:
            try:
                fd = os.open(self.path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                with os.fdopen(fd, "w", encoding="utf-8") as handle:
                    json.dump({"pid": os.getpid(), "created_at": time.time()}, handle)
                    handle.flush()
                    os.fsync(handle.fileno())
                self.acquired = True
                return self
            except FileExistsError:
                try:
                    age = time.time() - self.path.stat().st_mtime
                except FileNotFoundError:
                    continue
                if age > self.stale_after:
                    try:
                        self.path.unlink()
                    except FileNotFoundError:
                        continue
                    continue
                if time.monotonic() >= deadline:
                    raise ProtocolError(f"任务正在执行或锁未释放: {self.path.name}")
                time.sleep(0.05)

    def __exit__(self, *_args: object) -> None:
        if self.acquired:
            try:
                self.path.unlink()
            except FileNotFoundError:
                pass
            try:
                self.path.parent.rmdir()
            except OSError:
                pass
            self.acquired = False

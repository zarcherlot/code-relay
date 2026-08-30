"""Optional Go Code Relay agent discovery and invocation."""
from __future__ import annotations

import json
import os
import shutil
import subprocess
from pathlib import Path
from typing import Any


def find_agent() -> str | None:
    configured = os.environ.get("CODE_RELAY_AGENT")
    candidates = [configured] if configured else []
    candidates.extend([
        str(Path(__file__).resolve().parents[1] / "bin" / ("code-relay-agent.exe" if os.name == "nt" else "code-relay-agent")),
        str(Path(__file__).resolve().parents[1] / "dist" / ("code-relay-agent-windows-amd64.exe" if os.name == "nt" else "code-relay-agent-linux-amd64")),
        shutil.which("code-relay-agent"),
    ])
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return str(Path(candidate).resolve())
    return None


def run_agent(args: list[str], root: Path, *, timeout: float = 30) -> Any:
    executable = find_agent()
    if not executable:
        raise FileNotFoundError("未找到 code-relay-agent")
    result = subprocess.run([executable, *args, "--root", str(root)], cwd=root, text=True, capture_output=True, timeout=timeout, check=False)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip() or f"code-relay-agent 退出码 {result.returncode}")
    if not result.stdout.strip():
        return {"status": "ok"}
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        return {"output": result.stdout.strip()}

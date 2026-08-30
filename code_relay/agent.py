"""Optional Go Code Relay agent discovery and invocation."""
from __future__ import annotations

import json
import os
import platform
import shutil
import subprocess
from pathlib import Path
from typing import Any


def platform_target(system: str | None = None, machine: str | None = None) -> tuple[str, str] | None:
    """Return the Go release target for the current host (GOOS, GOARCH)."""
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    goos = {"linux": "linux", "windows": "windows", "darwin": "darwin"}.get(system)
    goarch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine)
    return (goos, goarch) if goos and goarch else None


def find_agent() -> str | None:
    target = platform_target()
    suffix = ".exe" if target and target[0] == "windows" else ""
    configured = os.environ.get("CODE_RELAY_AGENT")
    candidates = [configured] if configured else []
    candidates.extend([
        str(Path(__file__).resolve().parents[1] / "bin" / ("code-relay-agent.exe" if os.name == "nt" else "code-relay-agent")),
        shutil.which("code-relay-agent"),
    ])
    if target:
        goos, goarch = target
        candidates.insert(2, str(Path(__file__).resolve().parents[1] / "dist" / f"code-relay-agent-{goos}-{goarch}{suffix}"))
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

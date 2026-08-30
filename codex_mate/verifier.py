from __future__ import annotations

import os
import platform
import re
import shlex
import subprocess
import time
from pathlib import Path
from typing import Any

from .protocol import Task, utc_now

ALLOWED_COMMANDS = {"python", "python3", "pytest", "py", "node", "npm", "go", "cargo", "dotnet", "bash", "sh", "pwsh", "powershell", "cmd", "echo"}
DENY_TOKENS = ("rm -rf", "rmdir /s", "del /s", "format ", "shutdown", "git push", "git reset --hard", "curl | sh", "wget | sh")


def _commands(task: Task) -> list[str]:
    commands: list[str] = []
    # Fenced commands are unambiguous and can contain shell arguments.
    for match in re.finditer(r"```(?:shell|bash|sh|powershell|pwsh|cmd)?\s*\n(.*?)```", task.raw_markdown, re.I | re.S):
        commands.extend(line.strip() for line in match.group(1).splitlines() if line.strip() and not line.lstrip().startswith("#"))
    if commands:
        return commands
    for item in task.validation_plan:
        value = re.sub(r"^`|`$", "", item.strip())
        value = re.sub(r"^命令\s*[:：]\s*", "", value, flags=re.I)
        # Treat common executable-looking plan items as commands.
        first = value.split(maxsplit=1)[0].lower() if value else ""
        if first in ALLOWED_COMMANDS or first.endswith(".exe"):
            commands.append(value)
    return commands


def _safe(command: str) -> bool:
    lowered = command.lower()
    return not any(token in lowered for token in DENY_TOKENS)


def run_task(task: Task, root: Path, timeout: int = 600, worktree: str | None = None) -> dict[str, Any]:
    cwd = Path(worktree).resolve() if worktree else root.resolve()
    commands = _commands(task)
    checks: list[dict[str, Any]] = []
    risks: list[str] = []
    next_actions: list[str] = []
    if not commands:
        return {
            "task_id": task.task_id,
            "source_commit": task.source_commit,
            "status": "blocked",
            "checks": [{"name": "validation plan", "expected": "至少一条可执行命令", "actual": "未识别到安全可执行命令", "status": "blocked"}],
            "risks": ["Validation Plan 未包含受支持的命令"],
            "next_actions": ["补充明确的验证命令后重新发布任务"],
            "verified_at": utc_now(),
            "environment": {"platform": platform.platform(), "python": platform.python_version()},
        }
    overall = "passed"
    for index, command in enumerate(commands, 1):
        name = f"验证命令 {index}: {command}"
        if not _safe(command):
            checks.append({"name": name, "expected": "命令通过安全策略", "actual": "命令被安全策略拦截", "status": "blocked"})
            overall = "blocked"
            risks.append(f"命令被拦截: {command}")
            continue
        try:
            first = shlex.split(command, posix=os.name != "nt")[0].lower()
        except (ValueError, IndexError):
            first = ""
        if first not in ALLOWED_COMMANDS and not first.endswith(".exe"):
            checks.append({"name": name, "expected": "使用允许的验证命令", "actual": f"不允许的可执行文件: {first or 'unknown'}", "status": "blocked"})
            overall = "blocked"
            continue
        started = time.monotonic()
        try:
            proc = subprocess.run(command, cwd=cwd, shell=True, text=True, capture_output=True, timeout=timeout)
            elapsed = round(time.monotonic() - started, 3)
            output = ((proc.stdout or "") + (proc.stderr or "")).strip()
            if len(output) > 4000:
                output = output[-4000:]
            passed = proc.returncode == 0
            checks.append({"name": name, "expected": "退出码为 0", "actual": f"退出码 {proc.returncode}; {output or '无输出'}", "status": "passed" if passed else "failed", "duration_seconds": elapsed})
            if not passed:
                overall = "failed"
                next_actions.append(f"修复并重试: {command}")
        except subprocess.TimeoutExpired:
            checks.append({"name": name, "expected": f"在 {timeout}s 内完成", "actual": "命令超时", "status": "failed"})
            overall = "failed"
            next_actions.append(f"检查超时命令: {command}")
    if overall == "failed" and not next_actions:
        next_actions.append("查看验证日志并决定是否启动下一轮")
    return {"task_id": task.task_id, "source_commit": task.source_commit, "status": overall, "checks": checks, "risks": risks, "next_actions": next_actions, "verified_at": utc_now(), "environment": {"platform": platform.platform(), "python": platform.python_version(), "cwd": str(cwd)}}


from __future__ import annotations

import os
import platform
import re
import signal
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any

from .protocol import ProtocolError, Task, utc_now
from .locks import ProjectLock
from .policy import ALLOWED_COMMANDS, MAX_OUTPUT_LENGTH, MAX_TIMEOUT_SECONDS, SENSITIVE_ENV_KEYS, parse_command

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


def _parse_command(command: str) -> tuple[list[str] | None, str | None]:
    return parse_command(command)


def _run_command(argv: list[str], cwd: Path, timeout: int) -> tuple[int | None, str, bool]:
    env = os.environ.copy()
    for key in SENSITIVE_ENV_KEYS:
        env.pop(key, None)
    kwargs: dict[str, Any] = {
        "cwd": str(cwd),
        "stdout": None,
        "stderr": subprocess.STDOUT,
        "env": env,
    }
    if os.name != "nt":
        kwargs["start_new_session"] = True
    with tempfile.TemporaryFile(mode="w+b") as output:
        kwargs["stdout"] = output
        process = subprocess.Popen(argv, **kwargs)
        timed_out = False
        try:
            returncode = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            if os.name == "nt":
                subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True, check=False)
            else:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            try:
                returncode = process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                returncode = process.wait(timeout=5)
        output.seek(0, os.SEEK_END)
        size = output.tell()
        output.seek(max(0, size - MAX_OUTPUT_LENGTH))
        text = output.read().decode("utf-8", errors="replace").strip()
    return returncode, text, timed_out


def run_task(task: Task, root: Path, timeout: int = 600, worktree: str | None = None) -> dict[str, Any]:
    try:
        task.validate()
    except ProtocolError as exc:
        return {
            "task_id": task.task_id,
            "source_commit": task.source_commit,
            "status": "blocked",
            "checks": [{"name": "任务协议", "expected": "task.md 通过协议校验", "actual": str(exc), "status": "blocked"}],
            "risks": ["任务字段不符合 Code Relay 协议"],
            "next_actions": ["修正 task.md 后重新发布"],
            "verified_at": utc_now(),
            "environment": {"platform": platform.platform(), "python": platform.python_version()},
        }
    root = root.resolve()
    cwd = Path(worktree).resolve() if worktree else root
    try:
        inside_root = os.path.commonpath((str(root), str(cwd))) == str(root)
    except ValueError:
        inside_root = False
    if not cwd.is_dir() or not inside_root:
        return {
            "task_id": task.task_id,
            "source_commit": task.source_commit,
            "status": "blocked",
            "checks": [{"name": "验证工作目录", "expected": "位于仓库目录内且存在", "actual": f"拒绝工作目录: {cwd}", "status": "blocked"}],
            "risks": ["验证工作目录必须位于绑定工程内"],
            "next_actions": ["选择工程内的隔离 worktree 后重新执行"],
            "verified_at": utc_now(),
            "environment": {"platform": platform.platform(), "python": platform.python_version()},
        }
    timeout = min(max(int(timeout), 1), MAX_TIMEOUT_SECONDS)
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
    with ProjectLock(root, f"task-{task.task_id}"):
        for index, command in enumerate(commands, 1):
            name = f"验证命令 {index}: {command}"
            argv, reason = _parse_command(command)
            if argv is None:
                checks.append({"name": name, "expected": "命令通过安全策略", "actual": reason or "命令被安全策略拦截", "status": "blocked"})
                overall = "blocked"
                risks.append(f"命令被拦截: {command}")
                continue
            started = time.monotonic()
            try:
                returncode, output, timed_out = _run_command(argv, cwd, timeout)
                elapsed = round(time.monotonic() - started, 3)
                if timed_out:
                    checks.append({"name": name, "expected": f"在 {timeout}s 内完成", "actual": f"命令超时; {output or '无输出'}", "status": "failed", "duration_seconds": elapsed})
                    overall = "failed"
                    next_actions.append(f"检查超时命令: {command}")
                    continue
                passed = returncode == 0
                checks.append({"name": name, "expected": "退出码为 0", "actual": f"退出码 {returncode}; {output or '无输出'}", "status": "passed" if passed else "failed", "duration_seconds": elapsed})
                if not passed:
                    overall = "failed"
                    next_actions.append(f"修复并重试: {command}")
            except (OSError, subprocess.SubprocessError) as exc:
                checks.append({"name": name, "expected": "命令可以启动并完成", "actual": f"执行异常: {exc}", "status": "failed"})
                overall = "failed"
                next_actions.append(f"检查执行环境: {command}")
    if overall == "failed" and not next_actions:
        next_actions.append("查看验证日志并决定是否启动下一轮")
    return {"task_id": task.task_id, "source_commit": task.source_commit, "status": overall, "checks": checks, "risks": risks, "next_actions": next_actions, "verified_at": utc_now(), "environment": {"platform": platform.platform(), "python": platform.python_version(), "cwd": str(cwd)}}

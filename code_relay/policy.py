"""Shared runtime safety policy for the Python and Go agents."""
from __future__ import annotations

import shlex
from pathlib import Path

ALLOWED_COMMANDS = frozenset({
    "python", "python3", "pytest", "py", "node", "npm", "go", "cargo",
    "dotnet", "bash", "sh", "pwsh", "powershell", "cmd", "echo",
})
DENY_TOKENS = (
    "rm -rf", "rmdir /s", "del /s", "format ", "shutdown", "git push",
    "git reset --hard", "curl | sh", "wget | sh", "invoke-webrequest",
    "pip install", "npm install",
)
SHELL_OPERATORS = frozenset({"&&", "||", "|", ";", "&", ">", "<"})
SENSITIVE_ENV_KEYS = frozenset({
    "GITHUB_TOKEN", "GH_TOKEN", "CODE_RELAY_INVITE_SECRET",
    "CODE_RELAY_WEBHOOK_SECRET", "CODEX_API_KEY", "OPENAI_API_KEY",
})
MAX_COMMAND_LENGTH = 4096
MAX_OUTPUT_LENGTH = 4000
MAX_TIMEOUT_SECONDS = 3600
GIT_TIMEOUT_SECONDS = 30


def parse_command(command: str) -> tuple[list[str] | None, str | None]:
    """Parse and validate one task command without invoking a shell."""
    if not isinstance(command, str) or len(command) > MAX_COMMAND_LENGTH:
        return None, "命令超过 4096 字符限制"
    try:
        argv = shlex.split(command, posix=True)
    except ValueError as exc:
        return None, f"命令解析失败: {exc}"
    return validate_argv(argv)


def validate_argv(argv: list[str]) -> tuple[list[str] | None, str | None]:
    if not argv:
        return None, "命令为空"
    executable = argv[0].lower()
    if any(separator in executable for separator in ("/", "\\")):
        return None, "不允许使用带路径的可执行文件"
    executable = executable.removesuffix(".exe").removesuffix(".cmd").removesuffix(".bat")
    if executable not in ALLOWED_COMMANDS:
        return None, f"不允许的可执行文件: {argv[0]}"
    if any(token in SHELL_OPERATORS for token in argv):
        return None, "不允许使用 shell 管道、重定向或串联操作符"
    lowered = " ".join(argv).lower()
    if any(token in lowered for token in DENY_TOKENS):
        return None, "命令被安全策略拦截"
    return argv, None


def policy_document() -> dict[str, object]:
    return {
        "allowed_commands": sorted(ALLOWED_COMMANDS),
        "deny_tokens": list(DENY_TOKENS),
        "shell_operators": sorted(SHELL_OPERATORS),
        "sensitive_env_keys": sorted(SENSITIVE_ENV_KEYS),
        "max_command_length": MAX_COMMAND_LENGTH,
        "max_output_length": MAX_OUTPUT_LENGTH,
        "max_timeout_seconds": MAX_TIMEOUT_SECONDS,
        "git_timeout_seconds": GIT_TIMEOUT_SECONDS,
    }


def repository_policy_path() -> Path:
    return Path(__file__).resolve().parents[1] / "schemas" / "runtime-policy.json"

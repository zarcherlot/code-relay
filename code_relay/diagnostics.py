"""Local, non-mutating health checks for Code Relay hosts."""
from __future__ import annotations

import os
import subprocess
from pathlib import Path
from typing import Any

from .agent import find_agent
from .naming import meta_dir
from .policy import GIT_TIMEOUT_SECONDS


def _check(name: str, status: str, detail: str) -> dict[str, str]:
    return {"name": name, "status": status, "detail": detail}


def doctor(root: Path) -> dict[str, Any]:
    root = root.resolve()
    checks: list[dict[str, str]] = []
    if root.is_dir():
        checks.append(_check("root", "ok", str(root)))
    else:
        checks.append(_check("root", "error", "工程根目录不存在"))
        return {"status": "error", "root": str(root), "checks": checks}

    try:
        git = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=root, text=True, capture_output=True, check=False, timeout=GIT_TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired:
        git = subprocess.CompletedProcess(["git"], 124, "", "git 操作超时")
    if git.returncode == 0 and Path(git.stdout.strip()).resolve() == root:
        checks.append(_check("git", "ok", "当前目录是 Git 工程根目录"))
    else:
        checks.append(_check("git", "error", git.stderr.strip() or "无法识别 Git 工程根目录"))

    metadata = meta_dir(root)
    if metadata.is_dir():
        checks.append(_check("metadata", "ok", str(metadata)))
        if os.access(metadata, os.W_OK):
            checks.append(_check("metadata-write", "ok", "元数据目录可写"))
        else:
            checks.append(_check("metadata-write", "error", "元数据目录不可写"))
    else:
        checks.append(_check("metadata", "warning", "尚未初始化 .code-relay/，可运行 init 或 project-bind"))

    verifier = metadata / "verifier.json"
    project = metadata / "project.json"
    if verifier.exists() or project.exists():
        checks.append(_check("binding", "ok", "已发现项目绑定配置"))
    else:
        checks.append(_check("binding", "warning", "尚未发现 project.json 或 verifier.json"))

    agent = find_agent()
    checks.append(_check("go-agent", "ok", agent) if agent else _check("go-agent", "warning", "未找到 code-relay-agent；生产 verifier 需要安装 Go runtime，开发调试可显式设置 runtime=python"))
    try:
        remote = subprocess.run(["git", "config", "--get", "remote.origin.url"], cwd=root, text=True, capture_output=True, check=False, timeout=GIT_TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired:
        remote = subprocess.CompletedProcess(["git"], 124, "", "git 操作超时")
    checks.append(_check("remote", "ok", remote.stdout.strip()) if remote.returncode == 0 and remote.stdout.strip() else _check("remote", "warning", "未配置 origin remote"))
    status = "error" if any(item["status"] == "error" for item in checks) else ("warning" if any(item["status"] == "warning" for item in checks) else "ok")
    return {"status": status, "root": str(root), "checks": checks}

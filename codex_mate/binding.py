from __future__ import annotations

import base64
import json
import os
import secrets
import shutil
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from .protocol import ProtocolError

_WATCHER_PROCS: dict[str, subprocess.Popen[Any]] = {}


def _git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=root, text=True, capture_output=True, check=False)
    if result.returncode:
        raise ProtocolError(result.stderr.strip() or f"git {' '.join(args)} 执行失败")
    return result.stdout.strip()


def canonical_repo(root: Path) -> str:
    value = _git(root, "config", "--get", "remote.origin.url")
    if value.endswith(".git"):
        value = value[:-4]
    if value.startswith("git@github.com:"):
        value = "https://github.com/" + value.removeprefix("git@github.com:")
    return value


def current_ref(root: Path) -> str:
    branch = _git(root, "symbolic-ref", "--short", "HEAD")
    return "refs/heads/" + branch


def bind_project(root: Path, role: str, ref: str | None = None) -> dict[str, Any]:
    if role not in {"orchestrator", "verifier"}:
        raise ProtocolError("role 必须是 orchestrator 或 verifier")
    root = root.resolve()
    repository, remote_ref = canonical_repo(root), ref or current_ref(root)
    config = {
        "schema_version": 1,
        "repository": repository,
        "ref": remote_ref,
        "task_path": "tasks/**",
        "role": role,
        "created_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    }
    target = root / ".codex-relay" / ("project.json" if role == "orchestrator" else "verifier.json")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (root / "tasks").mkdir(exist_ok=True)
    (root / "receipts").mkdir(exist_ok=True)
    return config


def provision_project(root: Path, role: str, ref: str | None = None) -> dict[str, Any]:
    """Bind a repo/ref and copy the project integration files from the plugin."""
    root = root.resolve()
    config = bind_project(root, role, ref)
    plugin_root = Path(__file__).resolve().parents[1]
    copy_pairs = [
        (plugin_root / "schemas", root / "schemas"),
        (plugin_root / "templates", root / "templates"),
        (plugin_root / "skills", root / ".codex" / "skills"),
        (plugin_root / ".github" / "workflows" / "verify-on-b.yml", root / ".github" / "workflows" / "verify-on-b.yml"),
    ]
    for source, destination in copy_pairs:
        if source.is_dir():
            destination.mkdir(parents=True, exist_ok=True)
            for child in source.iterdir():
                target = destination / child.name
                if child.is_dir():
                    shutil.copytree(child, target, dirs_exist_ok=True)
                elif not target.exists():
                    shutil.copy2(child, target)
        elif source.is_file() and not destination.exists():
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, destination)
    return config


def create_invite(root: Path, expires_minutes: int = 30) -> dict[str, Any]:
    root = root.resolve()
    project = json.loads((root / ".codex-relay" / "project.json").read_text(encoding="utf-8"))
    nonce = secrets.token_urlsafe(18)
    expires_at = datetime.now(timezone.utc) + timedelta(minutes=expires_minutes)
    payload = {"v": 1, "repository": project["repository"], "ref": project["ref"], "task_path": project.get("task_path", "tasks/**"), "mode": "codex", "nonce": nonce, "expires_at": expires_at.replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    encoded = base64.urlsafe_b64encode(json.dumps(payload, separators=(",", ":")).encode()).decode().rstrip("=")
    url = "codex-relay://join/" + encoded
    record = root / ".codex-relay" / "invitations" / f"{nonce}.json"
    record.parent.mkdir(parents=True, exist_ok=True)
    record.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return {"url": url, **payload}


def decode_invite(value: str) -> dict[str, Any]:
    token = value.rstrip("/").rsplit("/", 1)[-1]
    try:
        padded = token + "=" * (-len(token) % 4)
        payload = json.loads(base64.urlsafe_b64decode(padded).decode("utf-8"))
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ProtocolError("无效的 Codex Relay 加入链接") from exc
    if payload.get("v") != 1 or not payload.get("repository") or not payload.get("ref") or not payload.get("nonce"):
        raise ProtocolError("加入链接缺少必要绑定信息")
    expires = payload.get("expires_at")
    if expires and datetime.fromisoformat(expires.replace("Z", "+00:00")) < datetime.now(timezone.utc):
        raise ProtocolError("加入链接已过期")
    return payload


def join_verifier(root: Path, invite: str) -> dict[str, Any]:
    root = root.resolve()
    payload = decode_invite(invite)
    actual = canonical_repo(root)
    if actual != payload["repository"]:
        raise ProtocolError(f"当前工程 remote 不匹配：{actual} != {payload['repository']}")
    actual_ref = current_ref(root)
    if actual_ref != payload["ref"]:
        raise ProtocolError(f"当前工程分支不匹配：{actual_ref} != {payload['ref']}（请切换分支或选择克隆目标目录）")
    config = {"schema_version": 1, "verifier_id": "b-" + secrets.token_hex(4), **{key: payload[key] for key in ("repository", "ref", "task_path", "mode")}, "joined_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    target = root / ".codex-relay" / "verifier.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (root / "tasks").mkdir(exist_ok=True)
    (root / "receipts").mkdir(exist_ok=True)
    return config


def _pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except (OSError, ProcessLookupError):
        return False
    return True


def watcher_status(root: Path) -> dict[str, Any]:
    state_path = root.resolve() / ".codex-relay" / "watcher.json"
    if not state_path.exists():
        return {"status": "stopped"}
    state = json.loads(state_path.read_text(encoding="utf-8"))
    if state.get("status") == "stopped":
        return state
    pid = state.get("pid")
    if isinstance(pid, int) and _pid_alive(pid):
        state["status"] = "running"
        return state
    state["status"] = "stopped"
    return state


def start_watcher(root: Path, interval: float = 5.0) -> dict[str, Any]:
    root = root.resolve()
    current = watcher_status(root)
    if current.get("status") == "running":
        return current
    meta = root / ".codex-relay"
    meta.mkdir(parents=True, exist_ok=True)
    log = (meta / "watcher.log").open("a", encoding="utf-8")
    launcher = Path(__file__).resolve().parents[1] / "daemon" / "relay-daemon"
    command = [sys.executable, str(launcher), "--root", str(root), "--role", "verifier", "--poll-interval", str(interval)] if launcher.exists() else [sys.executable, "-m", "codex_relay.daemon", "--root", str(root), "--role", "verifier", "--poll-interval", str(interval)]
    config_path = meta / "verifier.json"
    if config_path.exists():
        try:
            agent_command = json.loads(config_path.read_text(encoding="utf-8")).get("agent_command")
            if agent_command:
                command.extend(["--on-event", str(agent_command)])
        except (OSError, json.JSONDecodeError):
            pass
    kwargs: dict[str, Any] = {"cwd": str(root), "stdin": subprocess.DEVNULL, "stdout": log, "stderr": subprocess.STDOUT}
    if os.name == "nt":
        kwargs["creationflags"] = getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) | getattr(subprocess, "DETACHED_PROCESS", 0)
    else:
        kwargs["start_new_session"] = True
    process = subprocess.Popen(command, **kwargs)
    log.close()
    _WATCHER_PROCS[str(root)] = process
    state = {"status": "running", "pid": process.pid, "command": command, "started_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    (meta / "watcher.json").write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return state


def stop_watcher(root: Path) -> dict[str, Any]:
    root = root.resolve()
    state = watcher_status(root)
    pid = state.get("pid")
    process = _WATCHER_PROCS.pop(str(root), None)
    # Prefer the tracked handle so the parent can wait for the detached
    # watcher to release watcher.log before a temporary workspace is removed.
    if process is not None and process.poll() is None:
        try:
            process.terminate()
        except OSError:
            pass
    if isinstance(pid, int) and _pid_alive(pid):
        try:
            if os.name == "nt":
                subprocess.run(["taskkill", "/PID", str(pid), "/T", "/F"], capture_output=True, check=False)
            else:
                os.kill(pid, 15)
        except OSError:
            pass
    if process is not None:
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                pass
    state["status"] = "stopped"
    (root / ".codex-relay" / "watcher.json").write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return state


def _is_empty(path: Path) -> bool:
    return not path.exists() or not any(path.iterdir())


def clone_and_join(root: Path, invite: str, destination: str | None = None) -> dict[str, Any]:
    """Clone the invited branch when the current Codex window has no repository."""
    payload = decode_invite(invite)
    target = Path(destination).expanduser().resolve() if destination else root.resolve()
    if not _is_empty(target):
        raise ProtocolError(f"目标目录非空，拒绝自动克隆: {target}")
    target.parent.mkdir(parents=True, exist_ok=True)
    branch = payload["ref"].removeprefix("refs/heads/")
    result = subprocess.run(["git", "clone", "--branch", branch, "--single-branch", payload["repository"], str(target)], text=True, capture_output=True, check=False)
    if result.returncode:
        raise ProtocolError(result.stderr.strip() or "无法克隆邀请中的工程")
    # The verifier plugin is globally installed, but the project still needs
    # the branch-scoped integration assets (schemas, templates and workflow).
    # Copy only missing files before writing the final verifier binding.
    provision_project(target, "verifier", payload["ref"])
    config = join_verifier(target, invite)
    config["workspace"] = str(target)
    (target / ".codex-relay" / "verifier.json").write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return config

from __future__ import annotations

import base64
import binascii
import hashlib
import hmac
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from .protocol import ProtocolError

_WATCHER_PROCS: dict[str, subprocess.Popen[Any]] = {}
REF_RE = re.compile(r"^refs/heads/[A-Za-z0-9._/-]+$")
NONCE_RE = re.compile(r"^[A-Za-z0-9_-]{16,128}$")
MAX_INVITE_LENGTH = 16 * 1024


def _git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=root, text=True, capture_output=True, check=False)
    if result.returncode:
        raise ProtocolError(result.stderr.strip() or f"git {' '.join(args)} 执行失败")
    return result.stdout.strip()


def canonical_repo(root: Path) -> str:
    value = _git(root, "config", "--get", "remote.origin.url").strip()
    if value.endswith(".git"):
        value = value[:-4]
    if value.startswith("git@github.com:"):
        value = "https://github.com/" + value.removeprefix("git@github.com:")
    if not value or any(ord(char) < 32 or char.isspace() for char in value) or len(value) > 2048:
        raise ProtocolError("origin URL 非法或过长")
    if value.lower().startswith(("ext::", "file://", "git+file://", "fd::")):
        raise ProtocolError("不允许使用本地协议或 ext transport 作为远端仓库")
    return value


def current_ref(root: Path) -> str:
    branch = _git(root, "symbolic-ref", "--short", "HEAD")
    ref = "refs/heads/" + branch
    _validate_ref(ref)
    return ref


def _validate_ref(ref: str) -> None:
    if not isinstance(ref, str) or not REF_RE.fullmatch(ref) or ".." in ref or "//" in ref or ref.endswith("/"):
        raise ProtocolError("只允许绑定合法的 refs/heads/<branch>")


def _write_json_private(path: Path, data: dict[str, Any]) -> None:
    if path.parent.exists() and path.parent.is_symlink():
        raise ProtocolError("拒绝写入符号链接配置目录")
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        path.parent.chmod(0o700)
    except OSError:
        pass
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    try:
        temporary.chmod(0o600)
    except OSError:
        pass
    os.replace(temporary, path)


def _copy_missing(source: Path, destination: Path) -> None:
    """Copy plugin assets without overwriting project-owned files."""
    if source.is_dir():
        if destination.exists() and destination.is_symlink():
            raise ProtocolError(f"拒绝写入符号链接目录: {destination}")
        destination.mkdir(parents=True, exist_ok=True)
        for child in source.iterdir():
            _copy_missing(child, destination / child.name)
    elif source.is_file() and not destination.exists():
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination)


def _invite_signature(payload: dict[str, Any], secret: str) -> str:
    canonical = json.dumps(payload, ensure_ascii=False, separators=(",", ":"), sort_keys=True).encode("utf-8")
    return hmac.new(secret.encode("utf-8"), canonical, hashlib.sha256).hexdigest()


def bind_project(root: Path, role: str, ref: str | None = None) -> dict[str, Any]:
    if role not in {"orchestrator", "verifier"}:
        raise ProtocolError("role 必须是 orchestrator 或 verifier")
    root = root.resolve()
    repository, remote_ref = canonical_repo(root), ref or current_ref(root)
    _validate_ref(remote_ref)
    config = {
        "schema_version": 1,
        "repository": repository,
        "ref": remote_ref,
        "task_path": "tasks/**",
        "role": role,
        "created_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    }
    target = root / ".codex-relay" / ("project.json" if role == "orchestrator" else "verifier.json")
    _write_json_private(target, config)
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
        _copy_missing(source, destination)
    return config


def create_invite(root: Path, expires_minutes: int = 30) -> dict[str, Any]:
    root = root.resolve()
    if not 5 <= int(expires_minutes) <= 1440:
        raise ProtocolError("邀请有效期必须在 5 到 1440 分钟之间")
    project = json.loads((root / ".codex-relay" / "project.json").read_text(encoding="utf-8"))
    if project.get("role") != "orchestrator" or not project.get("repository"):
        raise ProtocolError("当前工程不是有效的 Codex Relay orchestrator 绑定")
    _validate_ref(project.get("ref"))
    nonce = secrets.token_urlsafe(18)
    expires_at = datetime.now(timezone.utc) + timedelta(minutes=expires_minutes)
    payload = {"v": 1, "repository": project["repository"], "ref": project["ref"], "task_path": project.get("task_path", "tasks/**"), "mode": "codex", "nonce": nonce, "expires_at": expires_at.replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    secret = os.environ.get("CODEX_RELAY_INVITE_SECRET")
    if secret:
        if len(secret) < 32:
            raise ProtocolError("CODEX_RELAY_INVITE_SECRET 至少需要 32 个字符")
        payload["signature"] = _invite_signature(payload, secret)
    encoded = base64.urlsafe_b64encode(json.dumps(payload, separators=(",", ":")).encode()).decode().rstrip("=")
    url = "codex-relay://join/" + encoded
    record = root / ".codex-relay" / "invitations" / f"{nonce}.json"
    _write_json_private(record, payload)
    return {"url": url, **payload}


def decode_invite(value: str) -> dict[str, Any]:
    if not isinstance(value, str) or len(value) > MAX_INVITE_LENGTH or not value.startswith("codex-relay://join/"):
        raise ProtocolError("无效的 Codex Relay 加入链接")
    token = value.rstrip("/").rsplit("/", 1)[-1]
    try:
        padded = token + "=" * (-len(token) % 4)
        payload = json.loads(base64.urlsafe_b64decode(padded).decode("utf-8"))
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError, binascii.Error, TypeError) as exc:
        raise ProtocolError("无效的 Codex Relay 加入链接") from exc
    if not isinstance(payload, dict) or payload.get("v") != 1 or payload.get("mode") != "codex" or not isinstance(payload.get("repository"), str) or not isinstance(payload.get("ref"), str) or not isinstance(payload.get("nonce"), str):
        raise ProtocolError("加入链接缺少必要绑定信息")
    _validate_ref(payload["ref"])
    if not NONCE_RE.fullmatch(payload["nonce"]) or payload.get("task_path", "tasks/**") != "tasks/**":
        raise ProtocolError("加入链接包含不支持的绑定范围")
    if any(ord(char) < 32 or char.isspace() for char in payload["repository"]):
        raise ProtocolError("加入链接包含非法仓库地址")
    secret = os.environ.get("CODEX_RELAY_INVITE_SECRET")
    signature = payload.get("signature")
    if signature:
        if not secret or len(secret) < 32 or not isinstance(signature, str):
            raise ProtocolError("该邀请需要配置 CODEX_RELAY_INVITE_SECRET 才能验证")
        unsigned = {key: value for key, value in payload.items() if key != "signature"}
        if not hmac.compare_digest(signature, _invite_signature(unsigned, secret)):
            raise ProtocolError("加入链接签名校验失败")
    expires = payload.get("expires_at")
    if expires:
        try:
            expires_at = datetime.fromisoformat(str(expires).replace("Z", "+00:00"))
        except ValueError as exc:
            raise ProtocolError("加入链接有效期格式非法") from exc
        if expires_at.tzinfo is None or expires_at < datetime.now(timezone.utc):
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
    try:
        state = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {"status": "stopped", "error": "watcher state 无法读取"}
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
    if not 1 <= float(interval) <= 3600:
        raise ProtocolError("watcher 轮询间隔必须在 1 到 3600 秒之间")
    meta = root / ".codex-relay"
    meta.mkdir(parents=True, exist_ok=True)
    log = (meta / "watcher.log").open("a", encoding="utf-8")
    launcher = Path(__file__).resolve().parents[1] / "daemon" / "relay-daemon"
    command = [sys.executable, str(launcher), "--root", str(root), "--role", "verifier", "--poll-interval", str(interval)] if launcher.exists() else [sys.executable, "-m", "codex_relay.daemon", "--root", str(root), "--role", "verifier", "--poll-interval", str(interval)]
    config_path = meta / "verifier.json"
    if not config_path.exists():
        log.close()
        raise ProtocolError("当前工程尚未加入 Codex Relay verifier")
    if config_path.exists():
        try:
            agent_command = json.loads(config_path.read_text(encoding="utf-8")).get("agent_command")
            if agent_command:
                command.extend(["--on-event", str(agent_command)])
        except (OSError, json.JSONDecodeError):
            pass
    kwargs: dict[str, Any] = {"cwd": str(root), "stdin": subprocess.DEVNULL, "stdout": log, "stderr": subprocess.STDOUT, "close_fds": True}
    if os.name == "nt":
        kwargs["creationflags"] = getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) | getattr(subprocess, "DETACHED_PROCESS", 0)
    else:
        kwargs["start_new_session"] = True
    process = subprocess.Popen(command, **kwargs)
    log.close()
    _WATCHER_PROCS[str(root)] = process
    state = {"status": "running", "pid": process.pid, "command": command, "started_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    _write_json_private(meta / "watcher.json", state)
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
    _write_json_private(root / ".codex-relay" / "watcher.json", state)
    return state


def _is_empty(path: Path) -> bool:
    return not path.exists() or not any(path.iterdir())


def clone_and_join(root: Path, invite: str, destination: str | None = None) -> dict[str, Any]:
    """Clone the invited branch when the current Codex window has no repository."""
    payload = decode_invite(invite)
    target = Path(destination).expanduser().resolve() if destination else root.resolve()
    if target.is_symlink():
        raise ProtocolError("拒绝在符号链接目录中自动克隆")
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

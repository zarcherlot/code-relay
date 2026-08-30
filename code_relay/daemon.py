from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import os
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

from .protocol import MAX_TASK_BYTES, TASK_ID_RE, ProtocolError, atomic_write_bytes, dump_json
from .policy import GIT_TIMEOUT_SECONDS
from .config import load_binding
from .observability import emit

MAX_EVENT_BYTES = 1024 * 1024
MAX_QUEUE_BYTES = 50 * 1024 * 1024
MAX_DELIVERIES = 10000
MAX_WEBHOOKS_PER_MINUTE = 60


def _event_argv(value: str | list[str] | None) -> list[str] | None:
    if value is None:
        return None
    parsed: object = value
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except json.JSONDecodeError as exc:
            raise ValueError("--on-event 必须是 JSON argv 数组") from exc
    if not isinstance(parsed, list) or not parsed or len(parsed) > 32 or any(not isinstance(item, str) or not item or len(item) > 4096 for item in parsed):
        raise ValueError("事件回调必须是非空的 argv 数组")
    if any(any(token in item for token in ("&&", "||", "|", ";", ">", "<")) for item in parsed):
        raise ValueError("事件回调不得包含 shell 操作符")
    return list(parsed)


class _RateLimiter:
    def __init__(self, max_events: int = MAX_WEBHOOKS_PER_MINUTE):
        self.max_events = max_events
        self._events: dict[str, list[float]] = {}
        self._lock = threading.Lock()

    def allow(self, key: str) -> bool:
        now = time.monotonic()
        with self._lock:
            recent = [stamp for stamp in self._events.get(key, []) if now - stamp < 60]
            if len(recent) >= self.max_events:
                self._events[key] = recent
                return False
            recent.append(now)
            self._events[key] = recent
            return True


class RelayDaemon:
    def __init__(self, root: Path, role: str, interval: float = 5.0, on_event: str | list[str] | None = None):
        self.root, self.role, self.interval, self.on_event = root.resolve(), role, interval, _event_argv(on_event)
        self.meta = self.root / ".code-relay"
        self.state_path, self.queue_path = self.meta / "state.json", self.meta / "events.jsonl"
        self.meta.mkdir(parents=True, exist_ok=True)
        self.state: dict[str, str] = {}
        if self.state_path.exists():
            try:
                loaded = json.loads(self.state_path.read_text(encoding="utf-8"))
                if isinstance(loaded, dict):
                    self.state = {str(key): str(value) for key, value in loaded.items() if len(str(key)) <= 1024 and len(str(value)) <= 128}
            except (OSError, json.JSONDecodeError):
                self.state = {}
        self.deliveries_path = self.meta / "deliveries.json"
        self.deliveries: set[str] = set()
        if self.deliveries_path.exists():
            try:
                loaded = json.loads(self.deliveries_path.read_text(encoding="utf-8"))
                if isinstance(loaded, list):
                    self.deliveries = {str(item) for item in loaded if len(str(item)) <= 256}
            except (OSError, json.JSONDecodeError):
                self.deliveries = set()
        self.verifier_config = self.meta / "verifier.json"
        self._queue_lock = threading.Lock()
        self.rate_limiter = _RateLimiter()

    def enqueue(self, event: dict[str, Any], delivery: str | None = None) -> bool:
        """Append an event once; GitHub delivery IDs make webhook retries idempotent."""
        if not isinstance(event, dict) or len(json.dumps(event, ensure_ascii=False)) > MAX_EVENT_BYTES:
            return False
        if delivery and len(delivery) > 256:
            return False
        with self._queue_lock:
            self.queue_path.parent.mkdir(parents=True, exist_ok=True)
            if self.queue_path.exists() and self.queue_path.stat().st_size >= MAX_QUEUE_BYTES:
                return False
            if delivery and delivery in self.deliveries:
                return False
            if delivery:
                self.deliveries.add(delivery)
                self.deliveries = set(sorted(self.deliveries)[-MAX_DELIVERIES:])
                dump_json(sorted(self.deliveries), self.deliveries_path)
            with self.queue_path.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(event, ensure_ascii=False) + "\n")
                handle.flush()
                os.fsync(handle.fileno())
        return True

    def _targets(self) -> list[Path]:
        if self.role == "orchestrator":
            base = self.root / "receipts"
            return [p for p in base.glob("*/receipt.json") if p.is_file()] if base.exists() else []
        base = self.meta / "inbox" / "tasks" if self.verifier_config.exists() else self.root / "tasks"
        return [p for p in base.glob("*/task.md") if p.is_file()] if base.exists() else []

    def _sync_remote_tasks(self) -> None:
        """Fetch the subscribed ref and materialize task files in a private inbox.

        The user's active worktree is never fast-forwarded or overwritten.
        """
        if self.role != "verifier" or not self.verifier_config.exists():
            return
        try:
            config = load_binding(self.verifier_config)
            ref = config["ref"]
            fetched = subprocess.run(["git", "fetch", "--quiet", "origin", ref], cwd=self.root, text=True, capture_output=True, check=False, timeout=GIT_TIMEOUT_SECONDS)
            if fetched.returncode:
                return
            tree = subprocess.run(["git", "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "tasks"], cwd=self.root, text=True, capture_output=True, check=False, timeout=GIT_TIMEOUT_SECONDS)
            if tree.returncode:
                return
            for relative in (line.strip() for line in tree.stdout.splitlines()):
                parts = relative.split("/")
                if len(parts) != 3 or parts[0] != "tasks" or parts[2] != "task.md" or not TASK_ID_RE.fullmatch(parts[1]):
                    continue
                shown = subprocess.run(["git", "show", f"FETCH_HEAD:{relative}"], cwd=self.root, capture_output=True, check=False, timeout=GIT_TIMEOUT_SECONDS)
                if shown.returncode or len(shown.stdout) > MAX_TASK_BYTES:
                    continue
                content = shown.stdout
                destination = self.meta / "inbox" / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                existing = destination.read_bytes() if destination.exists() else None
                if existing != content:
                    atomic_write_bytes(destination, content)
        except (OSError, KeyError, json.JSONDecodeError, subprocess.TimeoutExpired, ProtocolError):
            emit("sync_error", role=self.role)
            return

    def scan(self) -> list[dict[str, Any]]:
        self._sync_remote_tasks()
        events = []
        for path in self._targets():
            key = str(path.relative_to(self.root)).replace("\\", "/")
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            if self.state.get(key) == digest:
                continue
            self.state[key] = digest
            public_key = key.removeprefix(".code-relay/inbox/")
            event = {"type": "receipt_published" if self.role == "orchestrator" else "task_published", "path": public_key, "timestamp": time.time()}
            events.append(event)
            self.enqueue(event)
            if self.on_event:
                kwargs: dict[str, Any] = {"cwd": str(self.root), "stdin": subprocess.DEVNULL, "stdout": subprocess.DEVNULL, "stderr": subprocess.DEVNULL, "close_fds": True}
                if hasattr(os, "setsid"):
                    kwargs["start_new_session"] = True
                try:
                    subprocess.Popen(self.on_event, shell=False, **kwargs)
                except OSError as exc:
                    emit("event_callback_error", error=str(exc))
        dump_json(self.state, self.state_path)
        return events

    def run(self, once: bool = False) -> None:
        while True:
            for event in self.scan():
                print(json.dumps(event, ensure_ascii=False), flush=True)
                emit("watcher_event", role=self.role, event_type=event["type"], path=event["path"])
            if once:
                return
            time.sleep(self.interval)


def webhook_server(root: Path, secret: str | None, port: int, daemon: RelayDaemon) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802
            if self.path != "/":
                self.send_response(404); self.end_headers(); return
            client = self.client_address[0] if self.client_address else "unknown"
            if not daemon.rate_limiter.allow(client):
                self.send_response(429); self.end_headers(); return
            content_type = self.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
            if content_type != "application/json":
                self.send_response(415); self.end_headers(); return
            try:
                length = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                self.send_response(400); self.end_headers(); return
            if length < 0 or length > MAX_EVENT_BYTES:
                self.send_response(413); self.end_headers(); return
            body = self.rfile.read(length)
            try:
                json.loads(body.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                self.send_response(400); self.end_headers(); return
            signature = self.headers.get("X-Hub-Signature-256", "")
            if secret:
                expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
                if not hmac.compare_digest(signature, expected):
                    self.send_response(401); self.end_headers(); return
            delivery = self.headers.get("X-GitHub-Delivery", "")
            event = {"type": "github_event", "delivery": delivery, "github_event": self.headers.get("X-GitHub-Event", "unknown"), "timestamp": time.time()}
            if not daemon.enqueue(event, delivery):
                self.send_response(202); self.end_headers(); return
            self.send_response(202); self.end_headers(); self.wfile.write(b"queued\n")
        def log_message(self, *_args):
            return
    return ThreadingHTTPServer(("127.0.0.1", port), Handler)


def main() -> int:
    parser = argparse.ArgumentParser(prog="code-relay-daemon")
    parser.add_argument("--root", default=".")
    parser.add_argument("--role", choices=("orchestrator", "verifier"), required=True)
    parser.add_argument("--poll-interval", type=float, default=5.0)
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--webhook-port", type=int)
    parser.add_argument("--webhook-secret")
    parser.add_argument("--on-event", help="事件入队后执行的 JSON argv 数组，例如 [\"code-relay\",\"status\"]")
    args = parser.parse_args()
    daemon = RelayDaemon(Path(args.root), args.role, args.poll_interval, args.on_event)
    if args.webhook_port:
        server = webhook_server(Path(args.root), args.webhook_secret or os.environ.get("CODE_RELAY_WEBHOOK_SECRET"), args.webhook_port, daemon)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        print(f"Webhook listening on 127.0.0.1:{args.webhook_port}", flush=True)
    daemon.run(args.once)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

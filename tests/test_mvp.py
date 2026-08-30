import io
import json
import hashlib
import hmac
import base64
import http.client
import os
import threading
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

from codex_mate.daemon import RelayDaemon, webhook_server
from codex_mate.binding import bind_project, create_invite, decode_invite, join_verifier, provision_project, start_watcher, stop_watcher, watcher_status
from codex_mate.mcp_server import handle
from codex_mate.protocol import ProtocolError, Task
from codex_mate.relay import main
from codex_mate.verifier import run_task


TASK = """# Task
- task_id: task-test
- source_commit: abc1234
- target: B
- objective: smoke

## Validation Plan
1. python -c \"print('ok')\"

## Expected Results
- exits successfully
"""


class MvpTests(unittest.TestCase):
    def test_task_and_receipt_round_trip(self):
        task = Task.from_markdown(TASK)
        receipt = run_task(task, Path.cwd())
        self.assertEqual(receipt["status"], "passed")
        self.assertEqual(receipt["task_id"], "task-test")

    def test_invalid_task_protocol_produces_blocked_receipt(self):
        task = Task.from_markdown(TASK.replace("abc1234", "not-a-sha"))
        receipt = run_task(task, Path.cwd())
        self.assertEqual(receipt["status"], "blocked")
        self.assertEqual(receipt["checks"][0]["status"], "blocked")

    def test_publish_run_fetch_analyze_and_daemon(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            source = root / "task.md"
            source.write_text(TASK, encoding="utf-8")
            self.assertEqual(main(["--root", str(root), "publish", "--file", str(source), "--no-git"]), 0)
            # Publishing the same bytes is idempotent.
            self.assertEqual(main(["--root", str(root), "publish", "--file", str(source), "--no-git"]), 0)
            self.assertEqual(main(["--root", str(root), "run-task", "task-test"]), 0)
            output = io.StringIO()
            with redirect_stdout(output):
                self.assertEqual(main(["--root", str(root), "status", "--json"]), 0)
            self.assertEqual(json.loads(output.getvalue())[0]["status"], "passed")
            output = io.StringIO()
            with redirect_stdout(output):
                self.assertEqual(main(["--root", str(root), "analyze", "task-test"]), 0)
            self.assertEqual(json.loads(output.getvalue())["conclusion"], "done")
            daemon = RelayDaemon(root, "orchestrator")
            self.assertEqual(len(daemon.scan()), 1)
            self.assertEqual(len(daemon.scan()), 0)
            self.assertTrue((root / ".codex-relay/events.jsonl").exists())

    def test_receipt_binding_is_strict(self):
        task = Task.from_markdown(TASK)
        with self.assertRaises(ProtocolError):
            from codex_mate.protocol import validate_receipt
            validate_receipt({"task_id": "task-test", "source_commit": "wrong", "status": "passed", "checks": []}, task)

    def test_unsafe_command_is_blocked(self):
        task = Task.from_markdown(TASK.replace("python -c", "rm -rf / && python -c"))
        receipt = run_task(task, Path.cwd())
        self.assertEqual(receipt["status"], "blocked")

    def test_command_execution_is_shell_free_and_worktree_scoped(self):
        task = Task.from_markdown(TASK.replace("python -c", "python -c \"print('one')\" && echo two"))
        receipt = run_task(task, Path.cwd())
        self.assertEqual(receipt["status"], "blocked")
        outside = Path(tempfile.gettempdir()).resolve()
        receipt = run_task(Task.from_markdown(TASK), Path.cwd(), worktree=str(outside))
        self.assertEqual(receipt["status"], "blocked")

    def test_signed_invitation_rejects_tampering(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            subprocess.run(["git", "init", "-q", "-b", "main"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
            subprocess.run(["git", "remote", "add", "origin", "https://github.com/acme/demo.git"], cwd=root, check=True)
            (root / "README.md").write_text("demo", encoding="utf-8")
            with patch.dict(os.environ, {"CODEX_RELAY_INVITE_SECRET": "s" * 32}):
                provision_project(root, "orchestrator")
                invite = create_invite(root)
                self.assertEqual(decode_invite(invite["url"])["ref"], "refs/heads/main")
                token = invite["url"].rsplit("/", 1)[-1]
                payload = json.loads(base64.urlsafe_b64decode(token + "=" * (-len(token) % 4)).decode())
                payload["ref"] = "refs/heads/other"
                forged = "codex-relay://join/" + base64.urlsafe_b64encode(json.dumps(payload, separators=(",", ":")).encode()).decode().rstrip("=")
                with self.assertRaises(ProtocolError):
                    decode_invite(forged)

    def test_watcher_requires_verifier_binding(self):
        with tempfile.TemporaryDirectory() as folder:
            with self.assertRaises(ProtocolError):
                start_watcher(Path(folder))

    def test_webhook_signature_and_delivery_deduplication(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            daemon = RelayDaemon(root, "orchestrator")
            server = webhook_server(root, "secret", 0, daemon)
            thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
            port = server.server_address[1]
            body = b'{"action":"completed"}'
            signature = "sha256=" + hmac.new(b"secret", body, hashlib.sha256).hexdigest()
            for _ in range(2):
                connection = http.client.HTTPConnection("127.0.0.1", port)
                connection.request("POST", "/", body=body, headers={"Content-Type": "application/json", "X-Hub-Signature-256": signature, "X-GitHub-Delivery": "delivery-1", "X-GitHub-Event": "check_run"})
                self.assertEqual(connection.getresponse().status, 202)
                connection.close()
            server.shutdown(); server.server_close()
            self.assertEqual(len((root / ".codex-relay/events.jsonl").read_text(encoding="utf-8").splitlines()), 1)

    def test_branch_scoped_binding_and_join_link(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
            subprocess.run(["git", "remote", "add", "origin", "https://github.com/acme/demo.git"], cwd=root, check=True)
            (root / "README.md").write_text("demo", encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "init"], cwd=root, check=True)
            project = provision_project(root, "orchestrator")
            self.assertEqual(project["repository"], "https://github.com/acme/demo")
            self.assertTrue((root / ".github/workflows/verify-on-b.yml").exists())
            invite = create_invite(root, 30)
            joined = join_verifier(root, invite["url"])
            self.assertEqual(joined["repository"], project["repository"])
            running = handle({"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": "join_verifier", "arguments": {"root": str(root), "url": invite["url"]}}})
            self.assertIn('"status": "running"', running["result"]["content"][0]["text"])
            stop_watcher(root)
            self.assertEqual(watcher_status(root)["status"], "stopped")
            response = handle({"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}})
            self.assertTrue(any(tool["name"] == "join_verifier" for tool in response["result"]["tools"]))
            bound = handle({"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "bind_project", "arguments": {"root": str(root), "role": "orchestrator"}}})
            self.assertIn("invite", bound["result"]["structuredContent"])

    def test_verifier_watcher_fetches_bound_remote_ref_without_touching_worktree(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            bare, source, verifier = root / "remote.git", root / "source", root / "verifier"
            subprocess.run(["git", "init", "--bare", "-q", str(bare)], check=True)
            source.mkdir(); subprocess.run(["git", "init", "-q", "-b", "main"], cwd=source, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=source, check=True)
            subprocess.run(["git", "config", "user.name", "Test"], cwd=source, check=True)
            subprocess.run(["git", "remote", "add", "origin", str(bare)], cwd=source, check=True)
            (source / "README.md").write_text("source", encoding="utf-8")
            (source / "tasks").mkdir(); (source / "tasks/task-remote").mkdir()
            (source / "tasks/task-remote/task.md").write_text(TASK.replace("task-test", "task-remote"), encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=source, check=True); subprocess.run(["git", "commit", "-qm", "task"], cwd=source, check=True); subprocess.run(["git", "push", "-q", "-u", "origin", "main"], cwd=source, check=True)
            subprocess.run(["git", "clone", "-q", "--branch", "main", str(bare), str(verifier)], check=True)
            subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=verifier, check=True); subprocess.run(["git", "config", "user.name", "Test"], cwd=verifier, check=True)
            bind_project(verifier, "verifier")
            daemon = RelayDaemon(verifier, "verifier")
            events = daemon.scan()
            self.assertEqual(events[0]["path"], "tasks/task-remote/task.md")
            self.assertTrue((verifier / ".codex-relay/inbox/tasks/task-remote/task.md").exists())

    def test_join_from_empty_codex_workspace_clones_and_bootstraps(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            bare, source, empty = root / "remote.git", root / "source", root / "empty-workspace"
            subprocess.run(["git", "init", "--bare", "-q", str(bare)], check=True)
            source.mkdir(); subprocess.run(["git", "init", "-q", "-b", "main"], cwd=source, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=source, check=True)
            subprocess.run(["git", "config", "user.name", "Test"], cwd=source, check=True)
            subprocess.run(["git", "remote", "add", "origin", str(bare)], cwd=source, check=True)
            (source / "README.md").write_text("source", encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=source, check=True)
            subprocess.run(["git", "commit", "-qm", "init"], cwd=source, check=True)
            subprocess.run(["git", "push", "-q", "-u", "origin", "main"], cwd=source, check=True)
            provision_project(source, "orchestrator")
            subprocess.run(["git", "add", "."], cwd=source, check=True)
            subprocess.run(["git", "commit", "-qm", "relay"], cwd=source, check=True)
            subprocess.run(["git", "push", "-q", "origin", "main"], cwd=source, check=True)
            invite = create_invite(source, 30)
            response = handle({"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "join_verifier", "arguments": {"root": str(empty), "url": invite["url"], "poll_interval": 1}}})
            self.assertNotIn("error", response)
            self.assertEqual(response["result"]["structuredContent"]["repository"], str(bare)[:-4])
            self.assertTrue((empty / ".codex-relay/verifier.json").exists())
            self.assertTrue((empty / ".codex/skills/codex-relay-verifier/SKILL.md").exists())
            stop_watcher(empty)


if __name__ == "__main__":
    unittest.main()

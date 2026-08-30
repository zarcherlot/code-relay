from __future__ import annotations

import hashlib
import json
import os
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

TASK_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
COMMIT_RE = re.compile(r"^[0-9a-fA-F]{7,64}$")
STATUSES = {"passed", "failed", "blocked"}
MAX_TASK_BYTES = 1024 * 1024
MAX_RECEIPT_BYTES = 1024 * 1024
MAX_FIELD_LENGTH = 8192


class ProtocolError(ValueError):
    """Raised when a task or receipt violates the wire protocol."""


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _metadata(markdown: str) -> dict[str, str]:
    result: dict[str, str] = {}
    for line in markdown.splitlines():
        match = re.match(r"^\s*-\s*([A-Za-z][A-Za-z0-9_-]*)\s*:\s*(.*?)\s*$", line)
        if match:
            if match.group(1) in result:
                raise ProtocolError(f"task.md 元数据重复: {match.group(1)}")
            result[match.group(1)] = match.group(2).strip()
    return result


@dataclass(frozen=True)
class Task:
    task_id: str
    source_commit: str
    target: str
    objective: str
    validation_plan: list[str]
    expected_results: list[str]
    raw_markdown: str = ""

    @classmethod
    def from_markdown(cls, markdown: str) -> "Task":
        if len(markdown.encode("utf-8")) > MAX_TASK_BYTES:
            raise ProtocolError("task.md 超过 1 MiB 大小限制")
        meta = _metadata(markdown)
        missing = [key for key in ("task_id", "source_commit", "target", "objective") if not meta.get(key)]
        if missing:
            raise ProtocolError("task.md 缺少必填字段: " + ", ".join(missing))
        task_id = meta["task_id"]
        if not TASK_ID_RE.match(task_id):
            raise ProtocolError(f"非法 task_id: {task_id!r}")
        validation_plan = _section_items(markdown, "Validation Plan")
        expected = _section_items(markdown, "Expected Results")
        if not validation_plan:
            raise ProtocolError("Validation Plan 至少需要一项")
        if not expected:
            raise ProtocolError("Expected Results 至少需要一项")
        return cls(task_id, meta["source_commit"], meta["target"], meta["objective"], validation_plan, expected, markdown)

    @classmethod
    def from_file(cls, path: Path) -> "Task":
        return cls.from_markdown(path.read_text(encoding="utf-8"))

    def validate(self) -> None:
        if not COMMIT_RE.fullmatch(self.source_commit):
            raise ProtocolError("source_commit 必须是 7-64 位十六进制 commit SHA")
        if not self.target or len(self.target) > MAX_FIELD_LENGTH:
            raise ProtocolError("target 不能为空")
        if not self.objective or len(self.objective) > MAX_FIELD_LENGTH:
            raise ProtocolError("objective 不能为空或过长")
        if any(len(item) > MAX_FIELD_LENGTH for item in self.validation_plan + self.expected_results):
            raise ProtocolError("Validation Plan 或 Expected Results 项过长")


def _section_items(markdown: str, heading: str) -> list[str]:
    pattern = re.compile(rf"^##\s+{re.escape(heading)}\s*$", re.I | re.M)
    found = pattern.search(markdown)
    if not found:
        return []
    tail = markdown[found.end() :]
    next_heading = re.search(r"^##\s+", tail, re.M)
    section = tail[: next_heading.start()] if next_heading else tail
    items: list[str] = []
    for line in section.splitlines():
        match = re.match(r"^\s*(?:\d+[.)]|[-*])\s+(.*\S)\s*$", line)
        if match:
            items.append(match.group(1).strip())
    return items


def validate_receipt(data: Any, task: Task | None = None) -> dict[str, Any]:
    if not isinstance(data, dict):
        raise ProtocolError("receipt 必须是 JSON 对象")
    required = ("task_id", "source_commit", "status", "checks")
    missing = [key for key in required if key not in data]
    if missing:
        raise ProtocolError("receipt 缺少必填字段: " + ", ".join(missing))
    if not isinstance(data["task_id"], str) or not TASK_ID_RE.fullmatch(data["task_id"]):
        raise ProtocolError("receipt.task_id 非法")
    if not isinstance(data["source_commit"], str) or not COMMIT_RE.fullmatch(data["source_commit"]):
        raise ProtocolError("receipt.source_commit 必须是 7-64 位十六进制 commit SHA")
    if data["status"] not in STATUSES:
        raise ProtocolError("receipt.status 必须是 passed、failed 或 blocked")
    if not isinstance(data["checks"], list):
        raise ProtocolError("receipt.checks 必须是数组")
    for i, check in enumerate(data["checks"]):
        if not isinstance(check, dict) or not all(key in check for key in ("name", "expected", "actual", "status")):
            raise ProtocolError(f"checks[{i}] 缺少 name/expected/actual/status")
        if check["status"] not in STATUSES:
            raise ProtocolError(f"checks[{i}].status 非法")
        if any(not isinstance(check[key], str) or len(check[key]) > MAX_FIELD_LENGTH for key in ("name", "expected", "actual")):
            raise ProtocolError(f"checks[{i}] 文本字段非法或过长")
    for key in ("risks", "next_actions"):
        if key in data and not isinstance(data[key], list):
            raise ProtocolError(f"receipt.{key} 必须是数组")
    if task:
        if data["task_id"] != task.task_id:
            raise ProtocolError("receipt.task_id 与 task.md 不一致")
        if data["source_commit"] != task.source_commit:
            raise ProtocolError("receipt.source_commit 与 task.md 不一致")
        if data.get("task_sha256"):
            expected_hash = hashlib.sha256(task.raw_markdown.encode("utf-8")).hexdigest()
            if data["task_sha256"] != expected_hash:
                raise ProtocolError("receipt.task_sha256 与 task.md 不一致")
    return data


def load_receipt(path: Path, task: Task | None = None) -> dict[str, Any]:
    try:
        info = path.lstat()
        if path.is_symlink() or info.st_size > MAX_RECEIPT_BYTES:
            if path.is_symlink():
                raise ProtocolError("拒绝读取符号链接 receipt")
            raise ProtocolError("receipt.json 超过 1 MiB 大小限制")
    except FileNotFoundError as exc:
        raise ProtocolError(f"找不到 receipt JSON: {path}") from exc
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ProtocolError(f"无法解析 receipt JSON: {exc}") from exc
    return validate_receipt(data, task)


def render_receipt_markdown(receipt: dict[str, Any]) -> str:
    lines = [
        "# Verification Receipt",
        f"- task_id: {receipt['task_id']}",
        f"- source_commit: {receipt['source_commit']}",
        f"- status: {receipt['status']}",
        f"- verified_at: {receipt.get('verified_at', utc_now())}",
        "",
        "## Checks",
    ]
    for check in receipt.get("checks", []):
        lines.append(f"- **{check['name']}** — `{check['status']}`")
        lines.append(f"  - Expected: {check['expected']}")
        lines.append(f"  - Actual: {check['actual']}")
        if check.get("evidence"):
            lines.append(f"  - Evidence: {check['evidence']}")
    lines += ["", "## Risks"]
    lines.extend(f"- {risk}" for risk in receipt.get("risks", []))
    lines += ["", "## Next Actions"]
    lines.extend(f"- {action}" for action in receipt.get("next_actions", []))
    return "\n".join(lines).rstrip() + "\n"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def dump_json(data: Any, path: Path) -> None:
    atomic_write_text(path, json.dumps(data, ensure_ascii=False, indent=2) + "\n")


def atomic_write_text(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and path.is_symlink():
        raise ProtocolError(f"拒绝覆盖符号链接: {path}")
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        with temporary.open("w", encoding="utf-8", newline="") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        try:
            temporary.chmod(0o600)
        except OSError:
            pass
        os.replace(temporary, path)
        try:
            directory_fd = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        except OSError:
            pass
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


def atomic_write_bytes(path: Path, content: bytes) -> None:
    """Write binary protocol artifacts atomically and refuse symlink targets."""
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and path.is_symlink():
        raise ProtocolError(f"拒绝覆盖符号链接: {path}")
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        with temporary.open("wb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        try:
            temporary.chmod(0o600)
        except OSError:
            pass
        os.replace(temporary, path)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass

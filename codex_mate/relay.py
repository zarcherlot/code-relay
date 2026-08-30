from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Sequence

from .protocol import ProtocolError, Task, atomic_write_text, dump_json, load_receipt, render_receipt_markdown, utc_now


def _git(root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=root, text=True, capture_output=True, check=False)


def _repo_root(value: str | None) -> Path:
    root = Path(value or os.environ.get("CODEX_RELAY_ROOT") or os.environ.get("CODEX_MATE_ROOT", ".")).resolve()
    root.mkdir(parents=True, exist_ok=True)
    return root


def _task_markdown(args: argparse.Namespace) -> str:
    if args.file:
        return Path(args.file).read_text(encoding="utf-8")
    required = (args.task_id, args.source_commit, args.target, args.objective)
    if not all(required):
        raise ProtocolError("publish 需要 --file，或同时提供 --task-id/--source-commit/--target/--objective")
    plan = args.validation or ["python -m pytest -q"]
    expected = args.expected or ["验证命令全部成功"]
    lines = [
        "# Task",
        f"- task_id: {args.task_id}",
        f"- source_commit: {args.source_commit}",
        f"- target: {args.target}",
        f"- objective: {args.objective}",
        "",
        "## Validation Plan",
    ]
    lines.extend(f"{i}. {item}" for i, item in enumerate(plan, 1))
    lines += ["", "## Expected Results"]
    lines.extend(f"- {item}" for item in expected)
    lines += ["", "## Receipt Contract", "- 执行状态、实际命令和环境、每项验证的 expected / actual / status。"]
    return "\n".join(lines) + "\n"


def cmd_publish(args: argparse.Namespace) -> int:
    root = _repo_root(args.root)
    markdown = _task_markdown(args)
    task = Task.from_markdown(markdown)
    task.validate()
    destination = root / "tasks" / task.task_id / "task.md"
    if destination.exists() and not args.force:
        existing = Task.from_file(destination)
        if existing.source_commit != task.source_commit or existing.raw_markdown != markdown:
            raise ProtocolError(f"任务已存在且内容不同: {task.task_id}（使用 --force 明确覆盖）")
        print(f"已存在任务 {task.task_id}，保持幂等")
        return 0
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(markdown, encoding="utf-8")
    print(f"已发布任务 {task.task_id}: {destination}")
    if not args.no_git:
        result = _git(root, "add", str(destination.relative_to(root)))
        if result.returncode == 0:
            message = f"codex-relay: publish {task.task_id}"
            commit = _git(root, "commit", "-m", message)
            if commit.returncode == 0:
                push = _git(root, "push")
                if push.returncode != 0:
                    print("警告：任务已本地提交，但 git push 失败：" + push.stderr.strip(), file=sys.stderr)
            elif "nothing to commit" not in (commit.stdout + commit.stderr).lower():
                print("警告：无法提交任务：" + commit.stderr.strip(), file=sys.stderr)
    return 0


def _task_dirs(root: Path) -> list[Path]:
    base = root / "tasks"
    return sorted((p for p in base.iterdir() if p.is_dir() and (p / "task.md").exists()), key=lambda p: p.name) if base.exists() else []


def cmd_status(args: argparse.Namespace) -> int:
    root = _repo_root(args.root)
    rows = []
    for path in _task_dirs(root):
        try:
            task = Task.from_file(path / "task.md")
            receipt_path = root / "receipts" / task.task_id / "receipt.json"
            status = "task_published"
            source = task.source_commit
            if receipt_path.exists():
                receipt = load_receipt(receipt_path, task)
                status = receipt["status"]
            rows.append({"task_id": task.task_id, "source_commit": source, "target": task.target, "status": status})
        except ProtocolError as exc:
            rows.append({"task_id": path.name, "status": "invalid", "error": str(exc)})
    payload = {"tasks": rows}
    try:
        from .github import remote_status
        remote = remote_status(root)
        if remote:
            payload["github"] = remote
    except Exception as exc:  # Remote enrichment must never break local status.
        payload["github"] = {"error": str(exc)}
    if args.json:
        print(json.dumps(payload if payload.get("github") else rows, ensure_ascii=False, indent=2))
    else:
        if not rows:
            print("没有已发布任务")
        for row in rows:
            print(f"{row['task_id']}: {row['status']} (commit {row.get('source_commit', '-')})")
    return 0


def _find_task(root: Path, task_id: str) -> Task:
    path = root / "tasks" / task_id / "task.md"
    if not path.exists():
        raise ProtocolError(f"找不到任务: {task_id}")
    return Task.from_file(path)


def cmd_fetch_receipt(args: argparse.Namespace) -> int:
    root = _repo_root(args.root)
    task = _find_task(root, args.task_id)
    path = root / "receipts" / task.task_id / "receipt.json"
    if not path.exists():
        raise ProtocolError(f"回执尚未生成: {task.task_id}")
    receipt = load_receipt(path, task)
    if args.output:
        Path(args.output).write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if args.markdown:
        md = path.with_name("receipt.md")
        print(md.read_text(encoding="utf-8") if md.exists() else render_receipt_markdown(receipt), end="")
    else:
        print(json.dumps(receipt, ensure_ascii=False, indent=2))
    return 0


def cmd_analyze(args: argparse.Namespace) -> int:
    root = _repo_root(args.root)
    task = _find_task(root, args.task_id)
    receipt = load_receipt(root / "receipts" / task.task_id / "receipt.json", task)
    failed = [check for check in receipt["checks"] if check["status"] != "passed"]
    if receipt["status"] == "passed" and not failed:
        result = {"task_id": task.task_id, "conclusion": "done", "summary": "B 验证全部通过，任务完成。", "next_actions": receipt.get("next_actions", [])}
    elif receipt["status"] == "blocked":
        result = {"task_id": task.task_id, "conclusion": "blocked", "summary": "B 验证被阻塞，需要用户决策。", "next_actions": receipt.get("next_actions", [])}
    else:
        result = {"task_id": task.task_id, "conclusion": "iterate", "summary": f"B 验证失败 {len(failed)} 项，建议根据失败项启动下一轮任务。", "failed_checks": failed, "next_actions": receipt.get("next_actions", [])}
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0 if result["conclusion"] == "done" else 2


def cmd_init(args: argparse.Namespace) -> int:
    root = _repo_root(args.root)
    for path in (root / "tasks", root / "receipts"):
        path.mkdir(parents=True, exist_ok=True)
    print(f"已初始化 Codex Relay 目录: {root}")
    return 0


def cmd_project_bind(args: argparse.Namespace) -> int:
    from .binding import create_invite, provision_project

    root = _repo_root(args.root)
    config = provision_project(root, args.role, args.ref)
    print(json.dumps(config, ensure_ascii=False, indent=2))
    if args.role == "orchestrator" and not args.no_invite:
        invite = create_invite(root, args.expires)
        print("B verifier join URL:")
        print(invite["url"])
    return 0


def cmd_invite(args: argparse.Namespace) -> int:
    from .binding import create_invite

    invite = create_invite(_repo_root(args.root), args.expires)
    print(invite["url"])
    return 0


def cmd_join(args: argparse.Namespace) -> int:
    from .binding import join_verifier

    config = join_verifier(_repo_root(args.root), args.url)
    print(json.dumps(config, ensure_ascii=False, indent=2))
    return 0


def cmd_run_task(args: argparse.Namespace) -> int:
    from .verifier import run_task

    root = _repo_root(args.root)
    task = _find_task(root, args.task_id)
    receipt = run_task(task, root, timeout=args.timeout, worktree=args.worktree)
    # Hash the decoded task content so LF/CRLF checkout differences do not
    # invalidate an otherwise identical task across A and B hosts.
    receipt["task_sha256"] = hashlib.sha256(task.raw_markdown.encode("utf-8")).hexdigest()
    output = root / "receipts" / task.task_id
    output.mkdir(parents=True, exist_ok=True)
    dump_json(receipt, output / "receipt.json")
    atomic_write_text(output / "receipt.md", render_receipt_markdown(receipt))
    print(f"已生成回执 {output / 'receipt.json'}（{receipt['status']}）")
    return 0 if receipt["status"] == "passed" else 2


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="codex-relay", description="Codex Relay Git-backed relay")
    parser.add_argument("--root", help="仓库根目录，默认当前目录")
    sub = parser.add_subparsers(dest="command", required=True)
    init = sub.add_parser("init", help="创建 tasks/receipts 目录")
    init.set_defaults(func=cmd_init)
    bind = sub.add_parser("project-bind", help="插件内部：绑定当前工程与远端分支")
    bind.add_argument("--role", choices=("orchestrator", "verifier"), required=True)
    bind.add_argument("--ref")
    bind.add_argument("--expires", type=int, default=30)
    bind.add_argument("--no-invite", action="store_true")
    bind.set_defaults(func=cmd_project_bind)
    invite = sub.add_parser("invite", help="插件内部：生成 B 验证端加入链接")
    invite.add_argument("--expires", type=int, default=30)
    invite.set_defaults(func=cmd_invite)
    join = sub.add_parser("join", help="插件内部：使用邀请链接加入验证端")
    join.add_argument("url")
    join.set_defaults(func=cmd_join)
    pub = sub.add_parser("publish", help="校验并发布任务")
    pub.add_argument("--file")
    pub.add_argument("--task-id")
    pub.add_argument("--source-commit")
    pub.add_argument("--target", default="B")
    pub.add_argument("--objective")
    pub.add_argument("--validation", action="append")
    pub.add_argument("--expected", action="append")
    pub.add_argument("--force", action="store_true")
    pub.add_argument("--no-git", action="store_true", help="不执行 git add/commit/push")
    pub.set_defaults(func=cmd_publish)
    status = sub.add_parser("status", help="显示任务与回执状态")
    status.add_argument("--json", action="store_true")
    status.set_defaults(func=cmd_status)
    fetch = sub.add_parser("fetch-receipt", help="拉取并校验回执")
    fetch.add_argument("task_id")
    fetch.add_argument("--output")
    fetch.add_argument("--markdown", action="store_true")
    fetch.set_defaults(func=cmd_fetch_receipt)
    analyze = sub.add_parser("analyze", help="分析回执并决定下一步")
    analyze.add_argument("task_id")
    analyze.set_defaults(func=cmd_analyze)
    run = sub.add_parser("run-task", help="在 B 上执行验证计划并生成回执")
    run.add_argument("task_id")
    run.add_argument("--timeout", type=int, default=600)
    run.add_argument("--worktree", help="验证命令执行目录，默认仓库根目录")
    run.set_defaults(func=cmd_run_task)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except (ProtocolError, OSError) as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

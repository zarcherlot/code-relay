"""Minimal stdio MCP bridge used by the Codex Relay plugin.

The bridge intentionally exposes bounded project operations; the long-running
watcher remains a host service managed by the plugin/runtime.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

from .binding import clone_and_join, create_invite, join_verifier, provision_project, start_watcher, stop_watcher, watcher_status

TOOLS = [
    {"name": "bind_project", "description": "Bind the current project and branch to Codex Relay and create the verifier invite for an orchestrator.", "inputSchema": {"type": "object", "properties": {"root": {"type": "string"}, "role": {"enum": ["orchestrator", "verifier"]}, "ref": {"type": "string"}, "expires": {"type": "integer", "minimum": 5, "maximum": 1440}}, "required": ["role"]}},
    {"name": "create_verifier_invite", "description": "Create a short-lived verifier join link for the bound project.", "inputSchema": {"type": "object", "properties": {"root": {"type": "string"}, "expires": {"type": "integer", "minimum": 5, "maximum": 1440}}, "required": []}},
    {"name": "join_verifier", "description": "Join a branch-scoped Codex Relay verifier subscription using a join link and start its watcher.", "inputSchema": {"type": "object", "properties": {"root": {"type": "string"}, "url": {"type": "string"}, "destination": {"type": "string"}, "poll_interval": {"type": "number", "minimum": 1}}, "required": ["url"]}},
    {"name": "watcher_status", "description": "Show the Codex Relay verifier watcher status.", "inputSchema": {"type": "object", "properties": {"root": {"type": "string"}}, "required": []}},
    {"name": "stop_watcher", "description": "Stop the local Codex Relay verifier watcher after user confirmation.", "inputSchema": {"type": "object", "properties": {"root": {"type": "string"}}, "required": []}},
]


def _result(value: Any) -> dict[str, Any]:
    return {"content": [{"type": "text", "text": json.dumps(value, ensure_ascii=False, indent=2)}], "structuredContent": value}


def handle(request: dict[str, Any]) -> dict[str, Any] | None:
    method, params, request_id = request.get("method"), request.get("params", {}), request.get("id")
    if method == "notifications/initialized":
        return None
    if method == "initialize":
        return {"jsonrpc": "2.0", "id": request_id, "result": {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}}, "serverInfo": {"name": "codex-relay", "version": "0.3.0"}}}
    if method == "ping":
        return {"jsonrpc": "2.0", "id": request_id, "result": {}}
    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": request_id, "result": {"tools": TOOLS}}
    if method == "tools/call":
        name, args = params.get("name"), params.get("arguments", {})
        root = Path(args.get("root") or ".")
        try:
            if name == "bind_project":
                value = provision_project(root, args["role"], args.get("ref"))
                if args["role"] == "orchestrator":
                    value["invite"] = create_invite(root, int(args.get("expires", 30)))
            elif name == "create_verifier_invite":
                value = create_invite(root, int(args.get("expires", 30)))
            elif name == "join_verifier":
                # A Codex window may be opened without a repository. In that
                # case the plugin treats the current empty workspace as the
                # proposed clone target (or uses the explicit destination).
                # The skill asks for confirmation before invoking this action.
                if args.get("destination") or not (root / ".git").exists():
                    value = clone_and_join(root, args["url"], args.get("destination"))
                else:
                    value = join_verifier(root, args["url"])
                value["watcher"] = start_watcher(Path(value.get("workspace", root)), float(args.get("poll_interval", 5)))
            elif name == "watcher_status":
                value = watcher_status(root)
            elif name == "stop_watcher":
                value = stop_watcher(root)
            else:
                raise ValueError(f"unknown tool: {name}")
            return {"jsonrpc": "2.0", "id": request_id, "result": _result(value)}
        except Exception as exc:
            return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32000, "message": str(exc)}}
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": f"method not found: {method}"}}


def main() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            request = json.loads(line)
            response = handle(request)
            if response is not None:
                sys.stdout.write(json.dumps(response, ensure_ascii=False, separators=(",", ":")) + "\n")
                sys.stdout.flush()
        except json.JSONDecodeError as exc:
            sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": str(exc)}}) + "\n"); sys.stdout.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

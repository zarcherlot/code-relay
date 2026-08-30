"""Small, optional GitHub REST adapter used by `relay status`.

The CLI remains fully usable offline; remote enrichment is attempted only when a
repository and token are available in the environment.
"""
from __future__ import annotations

import json
import os
import re
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


def repository_slug(root: Path) -> str | None:
    explicit = os.environ.get("GITHUB_REPOSITORY")
    if explicit and "/" in explicit:
        return explicit.strip()
    try:
        import subprocess
        remote = subprocess.run(["git", "config", "--get", "remote.origin.url"], cwd=root, text=True, capture_output=True, check=False).stdout.strip()
    except OSError:
        return None
    match = re.search(r"github\.com[:/]([^/]+/[^/]+?)(?:\.git)?$", remote)
    return match.group(1) if match else None


def remote_status(root: Path) -> dict[str, Any] | None:
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    slug = repository_slug(root)
    if not token or not slug:
        return None

    def get(path: str) -> Any:
        request = urllib.request.Request("https://api.github.com" + path, headers={"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json", "User-Agent": "codex-relay"})
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.load(response)

    result: dict[str, Any] = {"repository": slug}
    try:
        prs = get(f"/repos/{slug}/pulls?state=open&per_page=20")
        result["open_prs"] = [{"number": p["number"], "title": p["title"], "head": p["head"]["sha"], "base": p["base"]["ref"]} for p in prs]
        runs = get(f"/repos/{slug}/actions/runs?per_page=10")
        result["workflow_runs"] = [{"name": r["name"], "status": r["status"], "conclusion": r["conclusion"], "head_sha": r["head_sha"]} for r in runs.get("workflow_runs", [])]
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, KeyError, json.JSONDecodeError) as exc:
        result["error"] = str(exc)
    return result

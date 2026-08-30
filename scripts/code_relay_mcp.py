#!/usr/bin/env python3
"""Code Relay MCP launcher (legacy codex_relay_mcp.py remains supported)."""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from code_relay.mcp_server import main

if __name__ == "__main__":
    raise SystemExit(main())

import base64
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from codex_mate.binding import bind_project, create_invite, decode_invite
from codex_mate.migration import migrate_project


class CodeRelayNamingTests(unittest.TestCase):
    def _repo(self, root: Path) -> None:
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
        subprocess.run(["git", "remote", "add", "origin", "https://github.com/acme/demo.git"], cwd=root, check=True)
        (root / "README.md").write_text("demo", encoding="utf-8")

    def test_new_names_and_legacy_invite_are_supported(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            self._repo(root)
            bind_project(root, "orchestrator")
            self.assertTrue((root / ".code-relay/project.json").exists())
            self.assertTrue((root / ".codex-relay/project.json").exists())
            with patch.dict(os.environ, {"CODE_RELAY_INVITE_SECRET": "s" * 32}, clear=False):
                invite = create_invite(root)
                self.assertTrue(invite["url"].startswith("code-relay://join/"))
                token = invite["url"].rsplit("/", 1)[-1]
                legacy = "codex-relay://join/" + token
                self.assertEqual(decode_invite(legacy)["ref"], "refs/heads/main")

    def test_migrate_copies_without_deleting_legacy(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            legacy = root / ".codex-relay"
            legacy.mkdir()
            (legacy / "verifier.json").write_text(json.dumps({"runtime": "python"}), encoding="utf-8")
            result = migrate_project(root)
            self.assertEqual(result["status"], "migrated")
            self.assertTrue((root / ".code-relay/verifier.json").exists())
            self.assertTrue((root / ".codex-relay/verifier.json").exists())


if __name__ == "__main__":
    unittest.main()

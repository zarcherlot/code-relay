import base64
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from code_relay.binding import bind_project, create_invite, decode_invite


class CodeRelayNamingTests(unittest.TestCase):
    def _repo(self, root: Path) -> None:
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
        subprocess.run(["git", "remote", "add", "origin", "https://github.com/acme/demo.git"], cwd=root, check=True)
        (root / "README.md").write_text("demo", encoding="utf-8")

    def test_new_names_and_invite_are_supported(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            self._repo(root)
            bind_project(root, "orchestrator")
            self.assertTrue((root / ".code-relay/project.json").exists())
            with patch.dict(os.environ, {"CODE_RELAY_INVITE_SECRET": "s" * 32}, clear=True):
                invite = create_invite(root)
                self.assertTrue(invite["url"].startswith("code-relay://join/"))
                self.assertEqual(decode_invite(invite["url"])["ref"], "refs/heads/main")

    def test_malformed_invite_is_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            self._repo(root)
            bind_project(root, "orchestrator")
            with patch.dict(os.environ, {}, clear=True):
                invite = create_invite(root)
            with self.assertRaises(ValueError):
                decode_invite("not-a-code-relay-invite")


if __name__ == "__main__":
    unittest.main()

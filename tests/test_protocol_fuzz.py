import random
import string
import unittest

from code_relay.binding import decode_invite
from code_relay.protocol import ProtocolError, Task


class ProtocolFuzzTests(unittest.TestCase):
    def test_random_invites_never_escape_protocol_error(self):
        rng = random.Random(20260830)
        alphabet = string.ascii_letters + string.digits + "-_/:{}[]\""
        for _ in range(500):
            value = "code-relay://join/" + "".join(rng.choice(alphabet) for _ in range(rng.randrange(0, 256)))
            try:
                decode_invite(value)
            except ProtocolError:
                pass

    def test_random_tasks_never_raise_unexpected_exception(self):
        rng = random.Random(20260831)
        alphabet = string.ascii_letters + string.digits + " -_:#\n"
        for _ in range(200):
            raw = "# Task\n" + "".join(rng.choice(alphabet) for _ in range(rng.randrange(0, 800)))
            try:
                Task.from_markdown(raw)
            except (ValueError, UnicodeError):
                pass


if __name__ == "__main__":
    unittest.main()

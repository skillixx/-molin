#!/usr/bin/env python3
"""验证 011 人工交互安装命令只携带冻结脚本且不处理密码。"""

import base64
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-011-command.py")
INSTALLER_PATH = Path(__file__).with_name("g8-test-readonly-access-install-011.sh")
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"


class TestPrepareG8ReadonlyAccess011Command(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), *arguments],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )

    def test_generates_new_absolute_output_only(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            result = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines()[0], "G8_TEST_READONLY_ACCESS_011_COMMAND=PASS")
            self.assertTrue(output.is_file())
            replay = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(replay.returncode, 2)

    def test_command_has_one_interactive_authentication_and_only_noninteractive_followups(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        self.assertEqual(command.count("sudo -k -v"), 1)
        self.assertEqual(command.count("sudo -n /bin/bash -ceu"), 1)
        self.assertEqual(command.count("sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test"), 1)
        self.assertIn("G8_011_INSTALL_B64", command)
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("IdentitiesOnly=yes", command)
        self.assertIn("UserKnownHostsFile=", command)
        self.assertIn("pc@8.130.9.163", command)
        self.assertIn("-p 10003", command)

    def test_embedded_installer_is_exact_and_secrets_are_absent(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        encoded = command.split("G8_011_INSTALL_B64'\n", 1)[1].split("\nG8_011_INSTALL_B64", 1)[0]
        self.assertEqual(base64.b64decode(encoded), INSTALLER_PATH.read_bytes())
        for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
            self.assertNotIn(forbidden, command)

    def test_invalid_request_fails_closed(self) -> None:
        for arguments in ((), ("--change-id=bad", "--output-file=relative")):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(
                result.stdout.strip(),
                "G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=invalid_request",
            )


if __name__ == "__main__":
    unittest.main()

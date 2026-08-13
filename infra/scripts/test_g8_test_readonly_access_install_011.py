#!/usr/bin/env python3
"""验证 011 root 安装器的固定输入、no-clobber 与回滚边界。"""

import os
import subprocess
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("g8-test-readonly-access-install-011.sh")


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestG8ReadonlyAccessInstall011(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT_PATH.read_text(encoding="utf-8")

    def test_bash_syntax_and_fixed_identity(self) -> None:
        result = subprocess.run(
            [bash_executable(), "-n", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn("/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn("/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn('if [ "$#" -ne 0 ] || [ "$(/usr/bin/id -u)" -ne 0 ]; then', self.source)

    def test_candidate_contract_is_frozen(self) -> None:
        for value in (
            "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
            "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
            "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
            "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
            "13066129",
            "DROP_SSH_INTERACTIVE_SUDO",
        ):
            self.assertIn(value, self.source)
        self.assertIn("EXPECTED_FILES='SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers'", self.source)
        self.assertIn("-mindepth 1 -maxdepth 1 -printf '%f\\n'", self.source)
        self.assertIn("check_candidate \"$STAGING\" 0 'pc:pc'", self.source)
        self.assertIn("check_candidate \"$ROOT_COPY\" 1 'root:root'", self.source)
        self.assertIn("SHA256SUMS:600", self.source)
        self.assertIn("ai-gateway-reconcile:700", self.source)

    def test_root_only_visudo_and_live_gates_are_exact(self) -> None:
        self.assertIn('/usr/sbin/visudo -cf "$ROOT_COPY/molin-g8-test-readonly-audit.sudoers"', self.source)
        self.assertIn("/usr/sbin/visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit", self.source)
        self.assertIn("/usr/bin/sudo -n -l -U pc", self.source)
        self.assertIn("/usr/bin/id -nG pc", self.source)
        self.assertIn('grep -Eq "(^|[[:space:]])docker([[:space:]]|$)"', self.source)

    def test_live_creation_uses_same_descriptor_and_registered_rollback(self) -> None:
        self.assertGreaterEqual(self.source.count("set -o noclobber"), 3)
        self.assertGreaterEqual(self.source.count('exec 3> "$target"'), 3)
        self.assertGreaterEqual(self.source.count('/usr/bin/cat "$source" >&3'), 3)
        self.assertIn("created_auditor=1", self.source)
        self.assertIn("created_reconcile=1", self.source)
        self.assertIn("created_sudoers=1", self.source)
        self.assertIn("rollback()", self.source)
        self.assertIn('if [ "$created_sudoers" -eq 1 ]; then', self.source)

    def test_script_has_no_remote_or_business_access_capability(self) -> None:
        for forbidden in (
            "curl ", "wget ", "ssh ", "sftp ", "scp ", "mysql ", "redis-cli",
            "rabbitmq", "docker ", "systemctl ", "sudo -S", "SUDO_ASKPASS",
        ):
            self.assertNotIn(forbidden, self.source)


if __name__ == "__main__":
    unittest.main()

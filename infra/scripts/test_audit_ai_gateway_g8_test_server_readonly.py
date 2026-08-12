#!/usr/bin/env python3
"""验证 G8 测试服务器基线脚本始终保持只读、低敏和可解析。"""

import os
import re
import subprocess
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("audit-ai-gateway-g8-test-server-readonly.sh")


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestServerReadonlyAuditTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = SCRIPT_PATH.read_text(encoding="utf-8")

    def test_bash_syntax_and_self_test_pass(self) -> None:
        syntax = subprocess.run(
            [bash_executable(), "-n", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(syntax.returncode, 0, syntax.stderr)
        self_test = subprocess.run(
            [bash_executable(), str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(self_test.returncode, 0, self_test.stderr)
        self.assertEqual(self_test.stdout.strip(), "G8_TEST_READONLY_AUDIT_SELF_TEST=PASS")

    def test_script_has_no_mutating_command(self) -> None:
        forbidden = re.compile(
            r"\b(?:rm|mv|cp|install|tee|truncate|chmod|chown|kill|systemctl|service)\b"
            r"|\bdocker\s+(?:restart|stop|rm)\b"
            r"|\b(?:INSERT|UPDATE|DELETE|ALTER|DROP|TRUNCATE|CREATE)\b"
        )
        self.assertIsNone(forbidden.search(self.source))

    def test_environment_file_values_are_not_printed(self) -> None:
        self.assertIn("env_keys=", self.source)
        self.assertNotRegex(self.source, r"\b(?:cat|sed)\s+[^\n]*\$ENV_FILE")
        self.assertNotIn("printenv", self.source)
        self.assertNotRegex(self.source, r"\bsource\s+[\"']?\$ENV_FILE")
        self.assertNotRegex(self.source, r"printf[^\n]*\$\{(?:MYSQL_PASSWORD|REDIS_PASSWORD|MINIO_SECRET_KEY)")

    def test_backup_output_excludes_path_and_reconciliation_is_read_only(self) -> None:
        self.assertNotIn("backup_latest_path=", self.source)
        self.assertIn("AI_GATEWAY_RECONCILE_READ_ONLY=YES", self.source)
        self.assertIn("--format summary", self.source)

    def test_docker_fallback_is_non_interactive(self) -> None:
        self.assertIn("sudo -n docker info", self.source)
        self.assertNotRegex(self.source, r"sudo\s+(?!-n\b)")

    def test_git_inspection_disables_optional_locks(self) -> None:
        git_commands = [line for line in self.source.splitlines() if "git -C" in line]
        self.assertTrue(git_commands)
        for command in git_commands:
            self.assertIn("GIT_OPTIONAL_LOCKS=0", command)

    def test_credential_parser_is_allowlisted_and_fail_closed(self) -> None:
        self.assertIn('allowed_keys = {"MYSQL_PASSWORD", "MINIO_SECRET_KEY", "RABBITMQ_URL", "REDIS_PASSWORD"}', self.source)
        self.assertIn("if key in allowed_keys:", self.source)
        self.assertIn("if not parse_ok:", self.source)
        self.assertIn("urllib.parse.unquote", self.source)
        self.assertIn('if "REDIS_PASSWORD" not in values:', self.source)


if __name__ == "__main__":
    unittest.main()

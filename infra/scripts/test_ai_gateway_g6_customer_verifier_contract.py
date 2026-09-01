import re
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("verify-ai-gateway-g6-customer.sh")


class AIGatewayG6CustomerVerifierContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.script = SCRIPT_PATH.read_text(encoding="utf-8")

    def test_baseline_migrations_stop_at_g5(self):
        """G6 专项验证必须先停在 000064，再单独执行 65 和 66。"""

        self.assertIn("migration_version > 64", self.script)
        self.assertRegex(
            self.script,
            re.compile(
                r"if \(\( migration_version > 64 \)\); then\s+break\s+fi\s+"
                r"docker exec",
                re.MULTILINE,
            ),
        )
        self.assertNotIn(
            '[[ "${migration}" == "${up65}" || "${migration}" == "${up66}" ]] && continue',
            self.script,
        )

    def test_unexpected_failure_emits_low_sensitive_diagnostic(self):
        """非预期退出只暴露行号和退出码，不回显包含临时凭据的命令。"""

        self.assertIn("reason=unexpected_error", self.script)
        self.assertIn("line=${line_no} exit_code=${exit_code}", self.script)
        self.assertNotIn("BASH_COMMAND", self.script)

    def test_go_test_failure_redacts_temporary_database_password(self):
        """真实 MySQL 测试失败可以诊断，但不得把一次性口令写入 CI 日志。"""

        self.assertIn('sed "s/${password}/[REDACTED]/g"', self.script)
        self.assertIn("reason=g6_mysql_go_test_failed", self.script)
        self.assertRegex(
            self.script,
            re.compile(r"go test .*?>\"\$\{test_output\}\" 2>&1", re.DOTALL),
        )

    def test_usage_fixture_time_is_inside_aggregate_window(self):
        """账单汇总夹具必须固定在测试窗口内，不能依赖执行当天。"""

        self.assertIn(
            "quoted_amount,settled_amount,created_at,completed_at",
            self.script,
        )
        self.assertEqual(self.script.count("'2026-08-08 00:00:00','2026-08-08 00:00:01'"), 2)


if __name__ == "__main__":
    unittest.main()

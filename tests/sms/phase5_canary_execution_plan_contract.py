import pathlib
import re
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "verify-sms-phase5-canary-execution-plan.ps1"


class Phase5CanaryExecutionPlanContract(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT.read_text(encoding="utf-8")

    def test_script_is_offline_and_has_no_send_path(self) -> None:
        self.assertNotRegex(self.source, re.compile(r"\b(?:curl|Invoke-WebRequest|ssh|scp)\b", re.I))
        self.assertNotIn("SMS_ENABLED=true", self.source)
        self.assertIn('Write-Output "network_connections=0"', self.source)
        self.assertIn('Write-Output "real_sms_sent=0"', self.source)

    def test_single_target_conflict_and_budget_are_fail_closed(self) -> None:
        self.assertIn("单号码同时承担注册与已注册场景的反例未被阻断", self.source)
        self.assertIn("requested_sends -ne 5", self.source)
        self.assertIn("max_sends -gt 10", self.source)
        self.assertIn("no_retries -ne $true", self.source)

    def test_sensitive_values_must_not_be_persisted(self) -> None:
        self.assertIn("目标只能使用 target- 前缀的低敏别名", self.source)
        self.assertIn("Canary 计划包含未定义字段", self.source)
        self.assertIn("场景计划字段必须严格限定", self.source)
        self.assertIn("sensitive_values_persisted=0", self.source)
        self.assertRegex(self.source, r"1\[3-9\].*\\d\{9\}")


if __name__ == "__main__":
    unittest.main()

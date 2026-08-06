import pathlib
import subprocess
import sys
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "verify-sms-phase5-observation-evidence.py"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class Phase5ObservationEvidenceContract(unittest.TestCase):
    def setUp(self) -> None:
        self.ci = CI.read_text(encoding="utf-8")

    def test_offline_self_test_passes(self) -> None:
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--self-test"],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("phase5_observation_evidence_self_test=passed", result.stdout)
        self.assertIn("closed_state_send_growth_rejected=true", result.stdout)
        self.assertIn("process_metric_reset_supported=true", result.stdout)
        self.assertIn("closed_process_provider_growth_rejected=true", result.stdout)
        self.assertIn("latency_stop_line_rejected=true", result.stdout)
        self.assertIn("counter_rollback_rejected=true", result.stdout)
        self.assertIn("sensitive_value_rejected=true", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_source_has_all_windows_and_stop_lines(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")
        for marker in ('"5m"', '"15m"', '"30m"', '"2h"', '"24h"'):
            self.assertIn(marker, source)
        self.assertIn("Provider 非受理比例越过自动停止线", source)
        self.assertIn("Provider 平均耗时越过 2 秒停止线", source)
        self.assertIn("观察窗口存在活动告警或通知失败", source)
        self.assertIn("关闭态恢复后当前进程 Provider 计数发生增长", source)

    def test_source_has_no_network_or_send_implementation(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")
        for forbidden in ("requests", "urllib", "http.client", "subprocess", "socket"):
            self.assertNotIn(forbidden, source)
        self.assertNotIn("SMS_ENABLED=true", source)
        self.assertIn("real_sms_sent_by_verifier=0", source)

    def test_contract_is_explicitly_wired_into_ci(self) -> None:
        self.assertIn("python tests/sms/phase5_observation_evidence_contract.py", self.ci)


if __name__ == "__main__":
    unittest.main()

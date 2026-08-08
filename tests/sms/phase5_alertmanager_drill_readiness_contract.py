#!/usr/bin/env python3
"""阶段 5 Alertmanager 通知演练只读预检的静态安全契约。"""

from __future__ import annotations

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "verify-sms-phase5-alertmanager-drill-readiness.ps1"
SSH_HELPER = ROOT / "scripts" / "sms-phase5-test-server-ssh.ps1"
PAYLOAD = ROOT / "scripts" / "verify-sms-phase5-alertmanager-drill-readiness.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-alertmanager-change-runbook.md"


class AlertmanagerDrillReadinessContractTest(unittest.TestCase):
    """保证演练前置检查只读、失败关闭，并保留独立人工授权门禁。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig") + SSH_HELPER.read_text(
            encoding="utf-8-sig"
        )
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.runbook = RUNBOOK.read_text(encoding="utf-8")

    def test_wrapper_locks_fixed_test_server_and_deployment_identity(self) -> None:
        self.assertIn("[switch]$SelfTest", self.ps)
        self.assertIn('$ServerHost -cne "8.130.9.163"', self.ps)
        self.assertIn('$SSHUser -cne "pc"', self.ps)
        self.assertIn("$SSHPort -ne 10003", self.ps)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", self.ps)
        self.assertIn("UserKnownHostsFile=", self.ps)
        self.assertIn("20260805T084215Z", self.ps)
        self.assertIn(
            "sha256:82c38dcc97cd0fbf5d5e31ddfb304dbb3a6e411194477de5de82ec71b328bb40",
            self.ps,
        )

    def test_payload_proves_closed_state_without_exposing_receiver_values(self) -> None:
        for marker in (
            "notification_drill_preflight=passed",
            "closed_route_discard_only",
            "receiver_configuration_loaded",
            "inline_secret_count",
            "secret_file_ref_count",
            "smtp_secret_file_secure=true",
            "notification_baseline_total",
            "receiver_delivery_unverified=true",
            "notification_drill_execution_authorization_required=true",
            "business_configuration_mutations=0",
            "service_restarts=0",
            "notification_attempts=0",
            "notifications_sent=0",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, self.sh)
        self.assertIn("SMS_ENABLED", self.sh)
        self.assertIn("SMS_TEST_MODE", self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)
        self.assertNotRegex(self.sh, r"(?i)to:\s*[^\n]+@")

    def test_payload_contains_no_mutating_or_notification_submission_commands(self) -> None:
        forbidden = (
            r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b",
            r"\bsed\s+-i\b",
            r"\bdocker\s+(?:run|create|restart|stop|kill|rm|exec|pull)\b",
            r"\bsystemctl\s+(?:restart|stop|start|enable|disable|reload)\b",
            r"\bcurl\b[^\n]*(?:--request|-X|--data|-d|--form|-F)\b",
        )
        for pattern in forbidden:
            self.assertIsNone(re.search(pattern, self.sh), pattern)
        # 先移除内嵌 Python，避免类型标注和正则表达式中的大于号被误判为 Shell 写重定向。
        shell_only = re.sub(r"<<'PY'\n.*?\nPY\n", "", self.sh, flags=re.DOTALL)
        self.assertNotRegex(shell_only, r">{1,2}\s*(?!/dev/null\b)[^\s;&|]+")

    def test_assets_are_in_readiness_ci_and_runbook(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn(Path(__file__).name, self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)
        self.assertIn(f"scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn(POWERSHELL.name, self.runbook)
        self.assertIn("notification_drill_execution_authorization_required=true", self.runbook)


if __name__ == "__main__":
    unittest.main()

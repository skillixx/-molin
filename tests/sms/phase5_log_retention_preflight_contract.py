#!/usr/bin/env python3
"""阶段 5 测试服日志留存只读审计的静态安全契约。"""

from __future__ import annotations

import re
import subprocess
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "verify-sms-phase5-test-server-log-retention.ps1"
PAYLOAD = ROOT / "scripts" / "verify-sms-phase5-test-server-log-retention.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-log-retention-runbook.md"


class LogRetentionPreflightContractTest(unittest.TestCase):
    """保证日志留存审计保持只读，并清楚区分运行状态与策略证据。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig")
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.runbook = RUNBOOK.read_text(encoding="utf-8")

    def test_wrapper_locks_the_only_approved_test_server_identity(self) -> None:
        self.assertIn("[switch]$SelfTest", self.ps)
        self.assertIn('$ServerHost -cne "8.130.9.163"', self.ps)
        self.assertIn('$SSHUser -cne "pc"', self.ps)
        self.assertIn("$SSHPort -ne 10003", self.ps)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", self.ps)
        self.assertIn("UserKnownHostsFile=", self.ps)
        self.assertIn("SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I", self.ps)
        self.assertIn("remote_connections=0", self.ps)

    def test_payload_reports_policy_evidence_without_exposing_values(self) -> None:
        for marker in (
            "journald_active",
            "journald_persistent_storage_present",
            "journald_storage_mode_persistent",
            "journald_capacity_limit_configured",
            "journald_keep_free_configured",
            "journald_retention_limit_configured",
            "journald_rotation_limit_configured",
            "log_retention_configuration_complete",
            "log_retention_runtime_reload_verified=false",
            "log_retention_policy_verified",
            "log_retention_change_authorization_required",
            "business_configuration_mutations=0",
            "access_audit_logs_may_increase=true",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, self.sh)
        for setting in (
            "Storage",
            "SystemMaxUse",
            "SystemKeepFree",
            "MaxRetentionSec",
            "MaxFileSec",
        ):
            self.assertIn(setting, self.sh)
        self.assertIn("systemd-analyze cat-config systemd/journald.conf", self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)
        self.assertNotIn("log_retention_policy_verified=true", self.sh)
        self.assertNotIn("log_retention_change_authorization_required=false", self.sh)

    def test_parser_rejects_invalid_zero_and_volatile_settings(self) -> None:
        start_marker = "python3 -c '\n"
        end_marker = "\n')\" || fail policy_parse"
        start = self.sh.index(start_marker) + len(start_marker)
        end = self.sh.index(end_marker, start)
        parser = self.sh[start:end]

        invalid = subprocess.run(
            [sys.executable, "-c", parser],
            input="""[Journal]
Storage=volatile
SystemMaxUse=garbage
SystemKeepFree=0B
MaxRetentionSec=0min
MaxFileSec=infinity
""",
            capture_output=True,
            text=True,
            check=True,
        )
        self.assertEqual(
            invalid.stdout.splitlines(),
            [
                "storage_mode_persistent=false",
                "capacity=false",
                "keep_free=false",
                "retention=false",
                "rotation=false",
            ],
        )

        configured = subprocess.run(
            [sys.executable, "-c", parser],
            input="""[Journal]
Storage=persistent
SystemMaxUse=3G
SystemKeepFree=1G
MaxRetentionSec=14day
MaxFileSec=1day
""",
            capture_output=True,
            text=True,
            check=True,
        )
        self.assertEqual(
            configured.stdout.splitlines(),
            [
                "storage_mode_persistent=true",
                "capacity=true",
                "keep_free=true",
                "retention=true",
                "rotation=true",
            ],
        )

        for unsafe_capacity in ("1iB", "18446744073709551616"):
            overflow = subprocess.run(
                [sys.executable, "-c", parser],
                input=f"""[Journal]
Storage=persistent
SystemMaxUse={unsafe_capacity}
SystemKeepFree=1G
MaxRetentionSec=14day
MaxFileSec=1day
""",
                capture_output=True,
                text=True,
                check=True,
            )
            self.assertIn("capacity=false", overflow.stdout.splitlines())

    def test_payload_contains_no_mutating_commands(self) -> None:
        forbidden = (
            r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b",
            r"\bsed\s+-i\b",
            r"\bsystemctl\s+(?:restart|stop|start|enable|disable|reload)\b",
            r"\bjournalctl\s+(?:--vacuum|--rotate|--flush)\b",
            r"\bdocker\s+(?:run|create|restart|stop|kill|rm|exec)\b",
        )
        for pattern in forbidden:
            self.assertIsNone(re.search(pattern, self.sh), pattern)
        self.assertNotRegex(self.sh, r">{1,2}\s*(?!/dev/null\b)[^\s;&|]+")

    def test_assets_are_in_readiness_ci_and_documented_as_a_gate(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn(Path(__file__).name, self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn("未配置不等于默认值已获批准", self.runbook)
        self.assertIn("日志配置变更必须单独授权", self.runbook)
        self.assertIn("真实短信发送数为 0", self.runbook)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""阶段 5 测试服关闭态只读审计的静态安全契约。"""

from __future__ import annotations

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "verify-sms-phase5-test-server-readonly.ps1"
PAYLOAD = ROOT / "scripts" / "verify-sms-phase5-test-server-readonly.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class TestServerReadonlyContractTest(unittest.TestCase):
    """锁定关闭态审计的目标身份、只读边界和证据口径。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig")
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")

    def test_wrapper_requires_fixed_ssh_and_supports_offline_self_test(self) -> None:
        for marker in (
            "[switch]$SelfTest",
            '$ServerHost -cne "8.130.9.163"',
            '$SSHUser -cne "pc"',
            "$SSHPort -ne 10003",
            "BatchMode=yes",
            "StrictHostKeyChecking=yes",
            "HostKeyAlgorithms=ssh-ed25519",
            "UserKnownHostsFile=",
            "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
            "remote_connections=0",
        ):
            self.assertIn(marker, self.ps)

    def test_payload_verifies_whitelist_without_disclosing_it(self) -> None:
        self.assertIn("SMS_TEST_PHONE_WHITELIST", self.sh)
        self.assertIn("expected_whitelist_count", self.sh)
        self.assertIn("sms_test_whitelist_count", self.sh)
        self.assertIn("sms_test_whitelist_count_matches_expected", self.sh)
        self.assertNotIn("printf '%s\\n' \"$sms_test_whitelist\"", self.sh)

    def test_payload_uses_honest_zero_activity_evidence(self) -> None:
        self.assertIn("observation_send_delta_zero", self.sh)
        self.assertIn("observation_provider_delta_zero", self.sh)
        self.assertIn("real_sms_delivery_not_verified=true", self.sh)
        self.assertIn("business_configuration_mutations=0", self.sh)
        self.assertIn("access_audit_logs_may_increase=true", self.sh)
        self.assertNotIn("remote_mutations=0", self.sh)
        self.assertNotIn("real_sms_sent=0", self.sh)

    def test_payload_contains_no_mutating_shell_commands(self) -> None:
        forbidden = (
            r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b",
            r"\bsed\s+-i\b",
            r"\bdocker\s+(?:run|create|restart|stop|kill|rm)\b",
            r"\bsystemctl\s+(?:restart|stop|start|enable|disable|reload)\b",
        )
        for pattern in forbidden:
            self.assertIsNone(re.search(pattern, self.sh), pattern)

    def test_offline_contract_is_part_of_readiness_and_ci(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn("phase5_test_server_readonly_contract.py", self.ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)


if __name__ == "__main__":
    unittest.main()

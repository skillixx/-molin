#!/usr/bin/env python3
"""阶段 5 固定测试服 SSH 身份校验的共享契约。"""

from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = ROOT / "scripts"
HELPER = SCRIPTS / "sms-phase5-test-server-ssh.ps1"
READINESS = SCRIPTS / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
WRAPPERS = (
    "apply-sms-phase5-test-server-log-retention.ps1",
    "prepare-sms-phase5-test-server-rollback-candidate.ps1",
    "verify-sms-phase5-test-server-log-retention.ps1",
    "verify-sms-phase5-test-server-readonly.ps1",
    "verify-sms-phase5-test-server-recovery-readiness.ps1",
)


class Phase5SSHIdentityContractTest(unittest.TestCase):
    """保证所有阶段 5 远端包装器复用同一套固定身份校验。"""

    def setUp(self) -> None:
        self.helper = HELPER.read_text(encoding="utf-8-sig")
        self.wrappers = {
            name: (SCRIPTS / name).read_text(encoding="utf-8-sig")
            for name in WRAPPERS
        }
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")

    def test_helper_owns_the_complete_fixed_identity_contract(self) -> None:
        for marker in (
            "function Assert-SmsPhase5FixedTestServerIdentity",
            '$ServerHost -cne "8.130.9.163"',
            '$SSHUser -cne "pc"',
            "$SSHPort -ne 10003",
            'Join-Path $env:USERPROFILE ".ssh\\known_hosts"',
            "ReparsePoint",
            'ssh-keygen -F "[8.130.9.163]:10003"',
            'ssh-ed25519',
            "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
        ):
            self.assertIn(marker, self.helper)

    def test_every_wrapper_delegates_identity_validation_only_once(self) -> None:
        for name, source in self.wrappers.items():
            with self.subTest(wrapper=name):
                self.assertIn(
                    '. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")',
                    source,
                )
                self.assertEqual(
                    source.count("Assert-SmsPhase5FixedTestServerIdentity"), 1
                )
                self.assertEqual(
                    source.count("Assert-SmsPhase5FixedTestServerTarget"), 1
                )
                self.assertNotIn('$ServerHost -cne "8.130.9.163"', source)
                self.assertNotIn("ssh-keygen -F", source)
                self.assertNotIn(
                    "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
                    source,
                )
                self.assertIn('$destination = "${SSHUser}@${ServerHost}"', source)
                self.assertIn('"-p", $SSHPort.ToString()', source)

    def test_shared_contract_is_mandatory_in_readiness_and_ci(self) -> None:
        self.assertIn(HELPER.name, self.readiness)
        self.assertIn(Path(__file__).name, self.ci)


if __name__ == "__main__":
    unittest.main()

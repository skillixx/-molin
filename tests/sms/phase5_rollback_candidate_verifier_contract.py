#!/usr/bin/env python3
"""阶段 5 测试服回滚候选只读验证器契约。"""

from __future__ import annotations

import re
import shutil
import subprocess
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "verify-sms-phase5-test-server-rollback-candidate.ps1"
PAYLOAD = ROOT / "scripts" / "verify-sms-phase5-test-server-rollback-candidate.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-rollback-runbook.md"


class RollbackCandidateVerifierContractTest(unittest.TestCase):
    """候选验证必须只读、固定目标并以非敏感摘要报告。"""

    def test_public_cli_assets_are_wired_into_readiness_ci_and_runbook(self) -> None:
        self.assertTrue(POWERSHELL.is_file())
        self.assertTrue(PAYLOAD.is_file())
        ps = POWERSHELL.read_text(encoding="utf-8-sig")
        sh = PAYLOAD.read_text(encoding="utf-8")
        readiness = READINESS.read_text(encoding="utf-8-sig")
        ci = CI.read_text(encoding="utf-8")
        runbook = RUNBOOK.read_text(encoding="utf-8")

        for marker in (
            "[string]$ChangeId",
            "[switch]$SelfTest",
            "必须提供 UTC ChangeId",
            "BatchMode=yes",
            "StrictHostKeyChecking=yes",
            "HostKeyAlgorithms=ssh-ed25519",
            "remote_connections=0",
        ):
            self.assertIn(marker, ps)
        self.assertIn("__CANDIDATE_PATH__", sh)
        self.assertIn("--self-test", sh)
        self.assertIn(POWERSHELL.name, readiness)
        self.assertIn(PAYLOAD.name, readiness)
        self.assertIn(Path(__file__).name, ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", ci)
        self.assertIn(f"bash scripts/{PAYLOAD.name} --self-test", ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", ci)
        self.assertIn(POWERSHELL.name, runbook)

        self.assertNotIn("SMS_ENABLED=true", sh)
        self.assertIsNone(
            re.search(r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|touch|truncate)\b", sh)
        )

    def test_payload_self_test_covers_candidate_acceptance_and_rejection(self) -> None:
        sh = PAYLOAD.read_text(encoding="utf-8")
        for marker in (
            "candidate_verification=passed",
            "candidate_sha256=",
            "candidate_owner_mode=pc:600",
            "candidate_root_owner_mode=pc:700",
            "candidate_sms_enabled=false",
            "candidate_sms_test_mode=true",
            "candidate_fixed_proxy_preserved=true",
            "candidate_legacy_template_keys=0",
            "candidate_duplicate_keys=0",
            "candidate_sensitive_values_printed=0",
            "business_configuration_mutations=0",
            "access_audit_logs_may_increase=true",
            "real_sms_delivery_not_verified=true",
        ):
            self.assertIn(marker, sh)
        self.assertNotIn("cat ", sh)
        self.assertNotIn("printenv", sh)
        self.assertNotIn("env |", sh)
        for marker in (
            "quoted_export_candidate=passed",
            "quoted_short_hmac_candidate=passed",
            "concurrent_replacement_candidate=passed",
        ):
            self.assertIn(marker, sh)

        # 验证器依赖 Linux 的 O_NOFOLLOW、目录文件描述符和 uid/mode 语义，Windows Git Bash 不等价。
        if not sys.platform.startswith("linux"):
            self.skipTest("当前平台不是原生 Linux，Linux CI 负责行为自测")
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("当前平台没有 Bash，Linux CI 负责执行行为自测")
        completed = subprocess.run(
            [bash, str(PAYLOAD), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        for marker in (
            "valid_candidate=passed",
            "quoted_export_candidate=passed",
            "quoted_short_hmac_candidate=passed",
            "missing_candidate=passed",
            "symlink_candidate=passed",
            "wrong_mode_candidate=passed",
            "sms_enabled_candidate=passed",
            "proxy_drift_candidate=passed",
            "legacy_key_candidate=passed",
            "duplicate_key_candidate=passed",
            "concurrent_replacement_candidate=passed",
            "payload_self_test=passed",
            "business_configuration_mutations=0",
            "service_restarts=0",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, completed.stdout)


if __name__ == "__main__":
    unittest.main()

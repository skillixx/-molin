#!/usr/bin/env python3
"""阶段 5 测试服回滚候选暂存与执行后独立验收契约。"""

from __future__ import annotations

import re
import shutil
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
STAGE_PS = ROOT / "scripts" / "stage-sms-phase5-test-server-rollback-drill.ps1"
STAGE_SH = ROOT / "scripts" / "stage-sms-phase5-test-server-rollback-drill.sh"
VERIFY_PS = ROOT / "scripts" / "verify-sms-phase5-test-server-rollback-drill.ps1"
VERIFY_SH = ROOT / "scripts" / "verify-sms-phase5-test-server-rollback-drill.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-rollback-runbook.md"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell")


class RollbackRuntimeEvidenceContractTest(unittest.TestCase):
    """确保暂存不扩大执行授权，独立验收只读且不信任执行器单方结论。"""

    def setUp(self) -> None:
        self.stage_ps = STAGE_PS.read_text(encoding="utf-8-sig")
        self.stage_sh = STAGE_SH.read_text(encoding="utf-8")
        self.verify_ps = VERIFY_PS.read_text(encoding="utf-8-sig")
        self.verify_sh = VERIFY_SH.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.runbook = RUNBOOK.read_text(encoding="utf-8")

    def require_powershell(self) -> str:
        """包装器动态契约依赖 PowerShell，缺失时交由 CI 对应步骤验证。"""
        if POWERSHELL is None:
            self.skipTest("当前环境没有 PowerShell")
        return POWERSHELL

    def test_stage_wrapper_defaults_offline_and_requires_exact_authorization(self) -> None:
        powershell = self.require_powershell()
        base = [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(STAGE_PS),
        ]
        checked = subprocess.run(
            base + ["-SelfTest"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("rollback_runtime_staging_self_test=passed", checked.stdout)
        self.assertIn("remote_connections=0", checked.stdout)
        planned = subprocess.run(
            base,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("rollback_runtime_staging_authorized=false", planned.stdout)
        self.assertIn("remote_files_written=0", planned.stdout)
        for marker in (
            "APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_STAGE_20260805T115540Z",
            "StageAndPreflight",
            "StrictHostKeyChecking=yes",
            "HostKeyAlgorithms=ssh-ed25519",
            "Get-FileHash",
        ):
            self.assertIn(marker, self.stage_ps)

    def test_stage_payload_only_prepares_uploads_and_reads(self) -> None:
        for marker in (
            "rollback_runtime_staging_prepared=true",
            "rollback_runtime_staging_validation=passed",
            'bash "$runner" --self-test',
            'bash "$runner" --preflight',
            "remote_files_written=1",
            "service_restarts=0",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, self.stage_sh)
        self.assertNotIn("--execute", self.stage_sh)
        self.assertNotIn("SMS_ENABLED=true", self.stage_sh)
        self.assertNotRegex(self.stage_sh, r"\brm\s+(?:-[A-Za-z]*r[A-Za-z]*\b|--recursive\b)")
        self.assertNotRegex(self.stage_sh, r"\b(?:kill|systemctl|pkill)\b")
        self.assertNotRegex(
            self.stage_sh,
            r"\bcurl\b[^\n]*(?:--request|-X|--data|-d|--form|-F)\b",
        )

    def test_independent_verifier_requires_evidence_and_live_state(self) -> None:
        for marker in (
            "expected_files=",
            "drill-result.txt",
            "exit-evidence.txt",
            "old-runtime.txt",
            "sensitive_environment_retained",
            "candidate_snapshot_retained",
            "old_binary_runtime_verified=true",
            "current_binary_restored=true",
            "current_environment_file_replaced=false",
            "sms_send_log_delta_zero=true",
            "notification_delta_zero=true",
            "current_environment_hash",
            "candidate_matches_running_environment=true",
            "runtime_secret_in_log",
            "send_summary_drift",
            "provider_delta",
            "notification_snapshot=3:0:3:0",
            "rollback_restore_runtime_verified=true",
        ):
            self.assertIn(marker, self.verify_sh)
        self.assertNotIn("SMS_ENABLED=true", self.verify_sh)
        self.assertNotRegex(
            self.verify_sh,
            r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b",
        )
        self.assertNotRegex(self.verify_sh, r"\b(?:kill|pkill|systemctl)\b")
        self.assertNotRegex(
            self.verify_sh,
            r"\bcurl\b[^\n]*(?:--request|-X|--data|-d|--form|-F)\b",
        )

    def test_verifier_self_test_is_offline(self) -> None:
        powershell = self.require_powershell()
        completed = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(VERIFY_PS),
                "-SelfTest",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("rollback_runtime_evidence_self_test=passed", completed.stdout)
        self.assertIn("remote_connections=0", completed.stdout)
        self.assertIn("remote_files_written=0", completed.stdout)

    def test_assets_are_wired_into_readiness_ci_and_runbook(self) -> None:
        for path in (STAGE_PS, STAGE_SH, VERIFY_PS, VERIFY_SH):
            self.assertIn(path.name, self.readiness)
            self.assertIn(path.name, self.runbook)
        self.assertIn("phase5_rollback_runtime_evidence_contract.py", self.ci)
        self.assertIn(f"bash -n scripts/{STAGE_SH.name}", self.ci)
        self.assertIn(f"bash -n scripts/{VERIFY_SH.name}", self.ci)
        self.assertIn(f"./scripts/{STAGE_PS.name} -SelfTest", self.ci)
        self.assertIn(f"./scripts/{VERIFY_PS.name} -SelfTest", self.ci)

    def test_bash_assets_are_syntax_valid_when_bash_is_available(self) -> None:
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("当前环境没有 Bash，Linux CI 和固定测试服负责语法验证")
        for path in (STAGE_SH, VERIFY_SH):
            subprocess.run([bash, "-n", str(path)], check=True)


if __name__ == "__main__":
    unittest.main()

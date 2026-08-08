#!/usr/bin/env python3
"""阶段 5 测试服旧二进制回滚与当前版本恢复演练契约。"""

from __future__ import annotations

import hashlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WRAPPER = ROOT / "scripts" / "prepare-sms-phase5-test-server-rollback-drill.ps1"
PAYLOAD = ROOT / "scripts" / "run-sms-phase5-test-server-rollback-drill.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-rollback-runbook.md"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell")


class RollbackRuntimeDrillContractTest(unittest.TestCase):
    """锁定只读预检、双版本恢复、自动回滚和零外发边界。"""

    def setUp(self) -> None:
        self.ps = WRAPPER.read_text(encoding="utf-8-sig")
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.runbook = RUNBOOK.read_text(encoding="utf-8")

    def require_powershell(self) -> str:
        """动态导出测试需要 PowerShell；缺失时由 CI 的 PowerShell 步骤覆盖。"""
        if POWERSHELL is None:
            self.skipTest("当前环境没有 PowerShell")
        return POWERSHELL

    def test_payload_binds_exact_test_server_assets_and_closed_state(self) -> None:
        for marker in (
            "/home/pc/molin/molin-api",
            "/home/pc/molin/backups/sms-phase5-20260804T120056Z",
            "candidate-${candidate_change_id}.env",
            "molin-alertmanager-phase5-closed",
            "alertmanager_notification_baseline=%s",
            "[ \"$notification_before\" = '3:0:3:0' ]",
            "SMS_ENABLED",
            "SMS_TEST_MODE",
            "rollback_runtime_preflight=passed",
            "old_binary_runtime_verified=true",
            "current_binary_restored=true",
            "current_environment_file_replaced=false",
            "current_environment_sha256=%s",
            "candidate_matches_running_environment=true",
            "notification_delta_zero=true",
            "sms_send_log_delta_zero=true",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)
        self.assertNotIn("/api/auth/verification-codes/phone", self.sh)
        self.assertNotRegex(self.sh, r"/api/admin/sms/.+test-send")
        self.assertNotRegex(
            self.sh,
            r"\bcurl\b[^\n]*(?:--request|-X|--data|-d|--form|-F)\b",
        )
        self.assertNotRegex(
            self.sh,
            r"\brm\s+(?:-[A-Za-z]*r[A-Za-z]*\b|--recursive\b)",
        )

    def test_process_replacement_is_exact_and_recovery_is_armed_first(self) -> None:
        self.assertEqual(len(re.findall(r"(?m)^\s*kill -TERM ", self.sh)), 1)
        self.assertEqual(len(re.findall(r"(?m)^\s*kill -KILL ", self.sh)), 1)
        self.assertEqual(len(re.findall(r"(?m)^\s*nohup python3 ", self.sh)), 1)
        self.assertIn('pgrep -f "^${api_path}$"', self.sh)
        self.assertIn("rollback_armed=true", self.sh)
        self.assertIn("trap 'handle_exit $?' EXIT", self.sh)
        self.assertIn("restore_current || fail current_recovery", self.sh)
        self.assertIn("install_binary_atomically", self.sh)
        self.assertIn("os.execve(binary_path, [binary_path], environment)", self.sh)
        self.assertNotRegex(self.sh, r"(?m)^\s*(?:source|\.)\s+.*candidate")

    def test_wrapper_self_test_and_export_are_offline_and_non_overwriting(self) -> None:
        powershell = self.require_powershell()
        change_id = "20990101T000000Z"
        base = [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(WRAPPER),
            "-ChangeId",
            change_id,
        ]
        checked = subprocess.run(
            base + ["-SelfTest"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("rollback_runtime_candidate_self_test=passed", checked.stdout)
        self.assertIn("remote_connections=0", checked.stdout)
        self.assertIn("service_restarts=0", checked.stdout)
        self.assertIn("real_sms_sent=0", checked.stdout)

        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "rollback-runtime-drill.sh"
            command = base + ["-ExportOperatorPayload", str(output)]
            exported = subprocess.run(
                command,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=True,
            )
            content = output.read_bytes()
            digest = re.search(
                r"^operator_payload_sha256=([0-9a-f]{64})$",
                exported.stdout,
                re.MULTILINE,
            )
            self.assertIsNotNone(digest)
            assert digest is not None
            self.assertEqual(digest.group(1), hashlib.sha256(content).hexdigest())
            self.assertTrue(content.startswith(b"#!/usr/bin/env bash\n"))
            self.assertNotIn(b"\r", content)
            self.assertNotIn(b"\xef\xbb\xbf", content)
            self.assertNotRegex(content.decode("utf-8"), r"__[A-Z0-9_]+__")
            self.assertIn(change_id.encode(), content)
            self.assertIn(
                f"APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_{change_id}".encode(),
                content,
            )

            repeated = subprocess.run(
                command,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(repeated.returncode, 0)
            self.assertEqual(content, output.read_bytes())

            mixed = subprocess.run(
                command + ["-SelfTest"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(mixed.returncode, 0)

            for unsafe_path in (r"\\server\share\runner.sh", r"C:runner.sh", r"\runner.sh"):
                unsafe = subprocess.run(
                    base + ["-ExportOperatorPayload", unsafe_path],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertNotEqual(unsafe.returncode, 0)

            # Windows 的 System32 bash.exe 可能只是 WSL 转发器，优先使用可独立运行的 Git Bash。
            git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
            bash = str(git_bash) if sys.platform == "win32" and git_bash.is_file() else shutil.which("bash")
            if bash is not None:
                subprocess.run([bash, "-n", str(output)], check=True)
                runner = subprocess.run(
                    [bash, str(output), "--self-test"],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    check=True,
                )
                self.assertIn("rollback_runtime_runner_self_test=passed", runner.stdout)

    def test_assets_are_wired_into_readiness_ci_and_runbook(self) -> None:
        self.assertIn(WRAPPER.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn("phase5_rollback_runtime_drill_contract.py", self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)
        self.assertIn(f"./scripts/{WRAPPER.name}", self.ci)
        self.assertIn(WRAPPER.name, self.runbook)
        self.assertIn("APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_", self.runbook)


if __name__ == "__main__":
    unittest.main()

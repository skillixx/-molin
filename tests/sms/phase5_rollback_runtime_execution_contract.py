import pathlib
import re
import shutil
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "execute-sms-phase5-test-server-rollback-drill.ps1"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class Phase5RollbackRuntimeExecutionContract(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT.read_text(encoding="utf-8-sig")

    def test_wrapper_is_frozen_and_default_closed(self) -> None:
        self.assertIn('ChangeId = "20260805T115540Z"', self.source)
        self.assertIn("2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46", self.source)
        self.assertIn("rollback_runtime_execution_authorized=false", self.source)
        self.assertIn("APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_20260805T115540Z", self.source)

    def test_remote_payload_calls_execute_exactly_once_and_no_retry(self) -> None:
        self.assertEqual(len(re.findall(r'bash "\$runner" --execute', self.source)), 1)
        self.assertIn("execution_attempts=1", self.source)
        self.assertIn("execution_retries=0", self.source)
        self.assertIn("禁止自动重试", self.source)

    def test_success_path_runs_independent_verifier(self) -> None:
        self.assertIn("verify-sms-phase5-test-server-rollback-drill.ps1", self.source)
        self.assertIn("independent_verification=passed", self.source)
        self.assertIn("不信任 runner 单一成功标记", self.source)

    def test_self_test_is_offline(self) -> None:
        powershell = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
        if powershell is None:
            self.skipTest("当前环境没有 PowerShell")
        result = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(SCRIPT),
                "-SelfTest",
            ],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("rollback_runtime_execution_wrapper_self_test=passed", result.stdout)
        self.assertIn("remote_connections=0", result.stdout)
        self.assertIn("execution_attempts=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_assets_are_wired_to_readiness_and_ci(self) -> None:
        readiness = READINESS.read_text(encoding="utf-8-sig")
        ci = CI.read_text(encoding="utf-8")
        self.assertIn("execute-sms-phase5-test-server-rollback-drill.ps1", readiness)
        self.assertIn("phase5_rollback_runtime_execution_contract.py", ci)


if __name__ == "__main__":
    unittest.main()

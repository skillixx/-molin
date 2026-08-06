import base64
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-canary-target-state-readonly.ps1"


class Phase5CanaryTargetStateReadonlyCandidateContract(unittest.TestCase):
    """验证固定测试服双号码状态只读预检候选的安全边界。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
        if cls.powershell is None:
            raise unittest.SkipTest("当前环境没有 PowerShell")

    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                self.powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(SCRIPT),
                *arguments,
            ],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )

    @staticmethod
    def write_plan(path: pathlib.Path, change_id: str) -> str:
        plan = {
            "change_id": change_id,
            "environment": "test",
            "sms_test_mode": True,
            "restore_sms_enabled": "false",
            "no_retries": True,
            "requested_sends": 5,
            "max_sends": 5,
            "same_target_min_interval_seconds": 65,
            "scheduled_waits": 2,
            "acceptance_scope": "receipt_only",
            "business_state_changes": False,
            "business_state_rollback_approved": False,
            "disposable_accounts": False,
            "scenes": [
                {"scene": "register", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "login", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "reset_password", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "bind_phone", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "admin_verify", "target_alias": "target-admin", "target_state": "registered_admin"},
            ],
        }
        path.write_text(json.dumps(plan, ensure_ascii=False), encoding="utf-8")
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def test_default_mode_is_closed_without_prompt_or_network(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("target_state_readonly_candidate_authorized=false", result.stdout)
        self.assertIn("interactive_prompts=0", result.stdout)
        self.assertIn("candidate_files_written=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("uploads=0", result.stdout)
        self.assertIn("business_posts=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_self_test_is_offline_and_uses_only_synthetic_values(self) -> None:
        result = self.run_script("-SelfTest")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("target_state_readonly_candidate_self_test=passed", result.stdout)
        self.assertIn("fixed_ssh_identity_contract_frozen=true", result.stdout)
        self.assertIn("readonly_state_fixture_verified=true", result.stdout)
        self.assertIn("interactive_prompts=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("business_posts=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)
        self.assertNotRegex(result.stdout, r"(?<!\d)1[3-9]\d{9}(?!\d)")

    def test_export_generates_fixed_identity_readonly_runner(self) -> None:
        change_id = "20990104T010203Z"
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            plan_path = temporary_path / "plan.json"
            plan_sha = self.write_plan(plan_path, change_id)
            output_path = temporary_path / "candidate"

            result = self.run_script(
                "-ExportCandidate",
                "-ChangeId",
                change_id,
                "-PlanFile",
                str(plan_path),
                "-ExpectedPlanSHA256",
                plan_sha,
                "-OutputDirectory",
                str(output_path),
            )

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("target_state_readonly_candidate=passed", result.stdout)
            self.assertIn(f"change_id={change_id}", result.stdout)
            self.assertIn(f"plan_sha256={plan_sha}", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("interactive_prompts=0", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("uploads=0", result.stdout)
            self.assertIn("business_posts=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            candidates = list(output_path.iterdir())
            self.assertEqual(len(candidates), 1)
            runner = candidates[0]
            runner_text = runner.read_text(encoding="utf-8-sig")
            payload_match = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', runner_text)
            self.assertIsNotNone(payload_match)
            payload_text = base64.b64decode(payload_match.group(1)).decode("utf-8")

            self.assertIn("Read-Host -Prompt $Prompt -AsSecureString", runner_text)
            self.assertIn("8.130.9.163", runner_text)
            self.assertIn("10003", runner_text)
            self.assertIn("pc", runner_text)
            self.assertIn("SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I", runner_text)
            self.assertIn("StrictHostKeyChecking=yes", runner_text)
            self.assertIn("HostKeyAlgorithms=ssh-ed25519", runner_text)
            self.assertIn("SELECT", payload_text)
            self.assertIn("SMS_TEST_PHONE_WHITELIST", payload_text)
            self.assertIn("direct_admin_role_verified", payload_text)
            self.assertIn("phone_verified", payload_text)
            self.assertIn("user:manage", payload_text)
            self.assertIn('whitelist_read_verified=true', payload_text)
            self.assertIn('whitelist_targets_ready=true', payload_text)
            self.assertIn('[ "$whitelist_verified" = true ]', payload_text)
            self.assertNotRegex(payload_text, r"(?im)^\s*(?:insert|update|delete|replace|alter|drop|truncate|create)\b")
            self.assertNotRegex(runner_text, r"(?i)\b(?:scp|sftp|curl|wget)\b")
            self.assertNotRegex(runner_text, r"(?i)invoke-(?:webrequest|restmethod)")
            self.assertNotRegex(runner_text, r"(?i)-method\s+post")
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            # 固定 SSH 只允许通过 LF/无 BOM 的 stdin 把完整脚本交给 bash -s，禁止再次使用会丢换行的 eval 参数链。
            self.assertIn("Invoke-FixedSshReadonlyScript", runner_text)
            self.assertIn("RedirectStandardInput = $true", runner_text)
            self.assertIn('New-Object System.Text.UTF8Encoding($false)', runner_text)
            self.assertIn('.Replace("`r`n", "`n").Replace("`r", "`n")', runner_text)
            self.assertIn("StandardInput.BaseStream.Write", runner_text)
            self.assertIn("[Array]::Clear($inputBytes", runner_text)
            self.assertNotIn("$startInfo.StandardInputEncoding", runner_text)
            self.assertIn("bash -s", runner_text)
            self.assertNotIn('eval "$(printf', runner_text)
            self.assertNotIn("$remoteCommand =", runner_text)
            self.assertNotIn("$sshArgs =", runner_text)
            self.assertIn("remote_stderr_present=", runner_text)

            # 远端以非零码失败关闭时，也必须先输出已经取得的低敏布尔结果和精确退出码。
            remote_call_index = runner_text.index("$remoteOutput = @(")
            exit_capture_index = runner_text.index("$readonlyExitCode = $remoteResult.ExitCode")
            remote_output_index = runner_text.index("$remoteOutput | Write-Output")
            exit_output_index = runner_text.index('Write-Output "readonly_exit_code=$readonlyExitCode"')
            failure_index = runner_text.index('if ($readonlyExitCode -ne 0)')
            self.assertLess(remote_call_index, exit_capture_index)
            self.assertLess(exit_capture_index, remote_output_index)
            self.assertLess(remote_output_index, exit_output_index)
            self.assertLess(exit_output_index, failure_index)

            closed = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner)],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
            self.assertIn("readonly_execution_authorized=false", closed.stdout)
            self.assertIn("interactive_prompts=0", closed.stdout)
            self.assertIn("network_connections=0", closed.stdout)
            self.assertIn("business_posts=0", closed.stdout)
            self.assertIn("real_sms_sent=0", closed.stdout)

            self_test = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-SelfTest"],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
            self.assertIn("target_state_readonly_runner_self_test=passed", self_test.stdout)
            self.assertIn("network_connections=0", self_test.stdout)
            self.assertNotRegex(self_test.stdout, r"(?<!\d)1[3-9]\d{9}(?!\d)")

    def test_export_rejects_unc_before_file_system_access(self) -> None:
        result = self.run_script(
            "-ExportCandidate",
            "-ChangeId",
            "20990104T010203Z",
            "-PlanFile",
            r"\\phase5-invalid.example.invalid\plan.json",
            "-ExpectedPlanSHA256",
            "0" * 64,
            "-OutputDirectory",
            r"\\phase5-invalid.example.invalid\candidate",
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("候选输出目录必须是本地文件系统绝对路径", result.stdout + result.stderr)

    def test_contract_is_wired_into_readiness_ci_and_documentation(self) -> None:
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        design = (ROOT / "docs" / "sms-phase5-canary-execution-design.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-canary-target-state-readonly.ps1", readiness)
        self.assertIn("phase5_canary_target_state_readonly_candidate_contract.py", ci)
        self.assertIn("prepare-sms-phase5-canary-target-state-readonly.ps1", design)
        self.assertIn("prepare-sms-phase5-canary-target-state-readonly.ps1", tools)


if __name__ == "__main__":
    unittest.main()

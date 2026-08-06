import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-canary-target-preflight.ps1"


class Phase5CanaryTargetIntakeCandidateContract(unittest.TestCase):
    """通过公共 CLI 验证双号码隐藏输入预检候选的生成边界。"""

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

    def test_default_mode_is_closed_without_prompt_or_side_effects(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("target_preflight_candidate_authorized=false", result.stdout)
        self.assertIn("interactive_prompts=0", result.stdout)
        self.assertIn("candidate_files_written=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("uploads=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_self_test_uses_only_synthetic_in_memory_values(self) -> None:
        result = self.run_script("-SelfTest")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("target_preflight_candidate_self_test=passed", result.stdout)
        self.assertIn("valid_distinct_pair_accepted=true", result.stdout)
        self.assertIn("duplicate_pair_rejected=true", result.stdout)
        self.assertIn("invalid_format_rejected=true", result.stdout)
        self.assertIn("interactive_prompts=0", result.stdout)
        self.assertIn("sensitive_values_persisted=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)
        self.assertNotRegex(result.stdout, r"(?<!\d)1[3-9]\d{9}(?!\d)")

    def test_export_binds_plan_and_generates_hidden_input_runner(self) -> None:
        change_id = "20990104T010203Z"
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
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            plan_path = temporary_path / "plan.json"
            plan_path.write_text(json.dumps(plan, ensure_ascii=False), encoding="utf-8")
            plan_sha = hashlib.sha256(plan_path.read_bytes()).hexdigest()
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
            self.assertIn("target_preflight_candidate=passed", result.stdout)
            self.assertIn(f"change_id={change_id}", result.stdout)
            self.assertIn(f"plan_sha256={plan_sha}", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("interactive_prompts=0", result.stdout)
            self.assertIn("sensitive_values_persisted=0", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            candidates = list(output_path.iterdir())
            self.assertEqual(len(candidates), 1)
            runner = candidates[0]
            runner_text = runner.read_text(encoding="utf-8-sig")
            self.assertIn("Read-Host -Prompt $Prompt -AsSecureString", runner_text)
            self.assertIn("SecureStringToBSTR", runner_text)
            self.assertIn("ZeroFreeBSTR", runner_text)
            self.assertIn("target-new", runner_text)
            self.assertIn("target-admin", runner_text)
            self.assertIn(change_id, runner_text)
            self.assertIn(plan_sha, runner_text)
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(runner_text, r"(?i)\b(?:ssh|scp|curl|wget)\b")
            self.assertNotIn("Invoke-WebRequest", runner_text)
            self.assertNotIn("Invoke-RestMethod", runner_text)
            self.assertNotIn("Set-Content", runner_text)
            self.assertNotIn("WriteAllText", runner_text)

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
            self.assertIn("interactive_authorized=false", closed.stdout)
            self.assertIn("interactive_prompts=0", closed.stdout)
            self.assertIn("sensitive_values_persisted=0", closed.stdout)
            self.assertIn("network_connections=0", closed.stdout)
            self.assertIn("real_sms_sent=0", closed.stdout)

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

    def test_export_rejects_unc_plan_with_local_output_before_file_system_access(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            output_path = pathlib.Path(temporary) / "candidate"
            result = self.run_script(
                "-ExportCandidate",
                "-ChangeId",
                "20990104T010203Z",
                "-PlanFile",
                r"\\phase5-invalid.example.invalid\plan.json",
                "-ExpectedPlanSHA256",
                "0" * 64,
                "-OutputDirectory",
                str(output_path),
            )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Canary 计划文件必须是本地文件系统绝对路径", result.stdout + result.stderr)

    def test_contract_is_wired_into_readiness_ci_and_documentation(self) -> None:
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        design = (ROOT / "docs" / "sms-phase5-canary-execution-design.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-canary-target-preflight.ps1", readiness)
        self.assertIn("phase5_canary_target_intake_candidate_contract.py", ci)
        self.assertIn("prepare-sms-phase5-canary-target-preflight.ps1", design)
        self.assertIn("prepare-sms-phase5-canary-target-preflight.ps1", tools)


if __name__ == "__main__":
    unittest.main()

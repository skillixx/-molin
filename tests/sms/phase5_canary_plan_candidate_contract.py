import json
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-canary-execution-plan.ps1"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
DESIGN = ROOT / "docs" / "sms-phase5-canary-execution-design.md"


class Phase5CanaryPlanCandidateContract(unittest.TestCase):
    """通过公共 PowerShell CLI 验证脱敏 Canary 计划候选生成行为。"""

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

    def test_default_mode_is_closed_and_offline(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("canary_plan_candidate_authorized=false", result.stdout)
        self.assertIn("candidate_files_written=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("uploads=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_generate_creates_one_valid_receipt_only_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            output_directory = pathlib.Path(temporary_directory) / "candidate"
            result = self.run_script(
                "-Generate",
                "-ChangeId",
                "20990102T010203Z",
                "-AcceptanceScope",
                "receipt_only",
                "-OutputDirectory",
                str(output_directory),
            )

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("canary_plan_candidate=passed", result.stdout)
            self.assertIn("canary_execution_plan=passed", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("target_alias_count=2", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("uploads=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            files = list(output_directory.iterdir())
            self.assertEqual(["sms-phase5-canary-plan-20990102T010203Z.json"], [item.name for item in files])
            raw = files[0].read_text(encoding="utf-8-sig")
            plan = json.loads(raw)
            self.assertEqual("receipt_only", plan["acceptance_scope"])
            self.assertFalse(plan["business_state_changes"])
            self.assertEqual(5, plan["requested_sends"])
            self.assertEqual(5, plan["max_sends"])
            by_scene = {entry["scene"]: entry for entry in plan["scenes"]}
            self.assertEqual(
                ("target-new", "unregistered"),
                (by_scene["register"]["target_alias"], by_scene["register"]["target_state"]),
            )
            self.assertEqual(
                ("target-new", "unregistered"),
                (by_scene["bind_phone"]["target_alias"], by_scene["bind_phone"]["target_state"]),
            )
            self.assertEqual("target-admin", by_scene["admin_verify"]["target_alias"])
            self.assertIsNone(re.search(r"(?<!\d)1[3-9]\d{9}(?!\d)", raw))
            self.assertNotRegex(raw, r'(?i)"(?:phone|token|password|otp|secret)"\s*:')

    def test_generate_requires_explicit_acceptance_scope(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            output_directory = pathlib.Path(temporary_directory) / "candidate"
            result = self.run_script(
                "-Generate",
                "-ChangeId",
                "20990102T010204Z",
                "-OutputDirectory",
                str(output_directory),
            )

            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("必须显式指定 AcceptanceScope=receipt_only", result.stdout + result.stderr)
            self.assertFalse(output_directory.exists())

    def test_self_test_is_offline_and_leaves_no_candidate(self) -> None:
        result = self.run_script("-SelfTest")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("canary_plan_candidate_self_test=passed", result.stdout)
        self.assertIn("unc_output_path_rejected=true", result.stdout)
        self.assertIn("candidate_files_remaining=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("uploads=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_generator_is_wired_into_readiness_ci_and_design(self) -> None:
        readiness = READINESS.read_text(encoding="utf-8-sig")
        ci = CI.read_text(encoding="utf-8")
        design = DESIGN.read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-canary-execution-plan.ps1", readiness)
        self.assertIn("phase5_canary_plan_candidate_contract.py", ci)
        self.assertIn("prepare-sms-phase5-canary-execution-plan.ps1", design)


if __name__ == "__main__":
    unittest.main()

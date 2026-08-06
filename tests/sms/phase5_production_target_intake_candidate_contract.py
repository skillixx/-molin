import base64
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-production-target-intake.ps1"


class Phase5ProductionTargetIntakeCandidateContract(unittest.TestCase):
    """通过公共 CLI 验证生产目标元数据候选始终离线且默认关闭。"""

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

    def export_arguments(self, output_path: pathlib.Path) -> list[str]:
        return [
            "-ExportCandidate",
            "-ChangeId",
            "20990105T010203Z",
            "-TargetAlias",
            "prod-primary",
            "-ServerHost",
            "prod.example.invalid",
            "-SSHPort",
            "2222",
            "-SSHUser",
            "deploy",
            "-ExpectedEd25519Fingerprint",
            "SHA256:"
            + base64.b64encode(hashlib.sha256(b"synthetic-production-host-key").digest()).decode().rstrip("="),
            "-ProjectRoot",
            "/srv/molin",
            "-EnvironmentFile",
            "/srv/molin/.env.prod",
            "-ServiceKind",
            "systemd",
            "-RollbackOperatorAlias",
            "operator-a",
            "-ObserverAlias",
            "observer-a",
            "-OutputDirectory",
            str(output_path),
        ]

    def test_default_mode_is_closed_without_side_effects(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("production_target_intake_authorized=false", result.stdout)
        self.assertIn("candidate_files_written=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("configuration_mutations=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_self_test_rejects_unsafe_target_and_path(self) -> None:
        result = self.run_script("-SelfTest")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("production_target_intake_self_test=passed", result.stdout)
        self.assertIn("fixed_identity_required=true", result.stdout)
        self.assertIn("loopback_target_rejected=true", result.stdout)
        self.assertIn("path_escape_rejected=true", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_export_writes_only_low_sensitivity_closed_candidate(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            output_path = pathlib.Path(temporary) / "candidate"
            result = self.run_script(*self.export_arguments(output_path))

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("production_target_intake_candidate=passed", result.stdout)
            self.assertIn("readonly_baseline_authorized=false", result.stdout)
            self.assertIn("deployment_authorized=false", result.stdout)
            self.assertIn("canary_authorized=false", result.stdout)
            self.assertIn("production_enable_authorized=false", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            candidates = list(output_path.iterdir())
            self.assertEqual(len(candidates), 1)
            candidate_bytes = candidates[0].read_bytes()
            candidate = json.loads(candidate_bytes.decode("utf-8"))
            self.assertEqual(candidate["environment"], "production")
            self.assertFalse(candidate["expected_sms_enabled"])
            self.assertTrue(candidate["expected_sms_test_mode"])
            self.assertTrue(candidate["readonly_baseline_requires_separate_approval"])
            self.assertEqual(candidate["automatic_retries"], 0)
            self.assertEqual(candidate["real_sms_sent"], 0)
            self.assertNotRegex(candidate_bytes.decode("utf-8"), r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotIn("password", candidate_bytes.decode("utf-8").lower())
            self.assertIn(hashlib.sha256(candidate_bytes).hexdigest(), result.stdout)

    def test_export_rejects_loopback_and_invalid_fingerprint_without_writing(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            output_path = pathlib.Path(temporary) / "candidate"
            arguments = self.export_arguments(output_path)
            arguments[arguments.index("prod.example.invalid")] = "127.0.0.1"
            fingerprint = "SHA256:" + base64.b64encode(
                hashlib.sha256(b"synthetic-production-host-key").digest()
            ).decode().rstrip("=")
            arguments[arguments.index(fingerprint)] = "SHA256:short"
            result = self.run_script(*arguments)

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output_path.exists())

    def test_contract_is_wired_into_readiness_ci_and_documentation(self) -> None:
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        deployment = (ROOT / "docs" / "sms-phase5-deployment-plan.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-production-target-intake.ps1", readiness)
        self.assertIn("phase5_production_target_intake_candidate_contract.py", ci)
        self.assertIn("prepare-sms-phase5-production-target-intake.ps1", deployment)
        self.assertIn("prepare-sms-phase5-production-target-intake.ps1", tools)


if __name__ == "__main__":
    unittest.main()

import base64
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-production-readonly-baseline.ps1"


class Phase5ProductionReadonlyBaselineCandidateContract(unittest.TestCase):
    """验证生产关闭态只读基线候选的摘要绑定、单连接和零副作用边界。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
        if cls.powershell is None:
            raise unittest.SkipTest("当前环境没有 PowerShell")

    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SCRIPT), *arguments],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )

    @staticmethod
    def target_candidate() -> dict[str, object]:
        fingerprint = "SHA256:" + base64.b64encode(
            hashlib.sha256(b"synthetic-production-host-key").digest()
        ).decode().rstrip("=")
        return {
            "schema_version": 1,
            "change_id": "20990105T010203Z",
            "environment": "production",
            "target_alias": "prod-primary",
            "server_host": "prod.example.invalid",
            "ssh_port": 2222,
            "ssh_user": "deploy",
            "expected_ed25519_fingerprint": fingerprint,
            "project_root": "/srv/molin",
            "environment_file": "/srv/molin/.env.prod",
            "service_kind": "systemd",
            "api_service_identifier": "molin-api.service",
            "api_local_port": 8080,
            "prometheus_local_port": 19090,
            "alertmanager_local_port": 19093,
            "rollback_operator_alias": "operator-a",
            "observer_alias": "observer-a",
            "expected_sms_enabled": False,
            "expected_sms_test_mode": True,
            "readonly_baseline_requires_separate_approval": True,
            "deployment_requires_separate_approval": True,
            "canary_requires_separate_approval": True,
            "production_enable_requires_separate_approval": True,
            "automatic_retries": 0,
            "business_posts": 0,
            "real_sms_sent": 0,
        }

    def test_default_and_self_test_are_offline(self) -> None:
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("production_readonly_candidate_authorized=false", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        self.assertIn("real_sms_sent=0", closed.stdout)

        self_test = self.run_script("-SelfTest")
        self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
        self.assertIn("production_readonly_candidate_self_test=passed", self_test.stdout)
        self.assertIn("single_ssh_connection_contract=true", self_test.stdout)
        self.assertIn("readonly_payload_verified=true", self_test.stdout)
        self.assertIn("network_connections=0", self_test.stdout)

    def test_export_binds_target_and_builds_default_closed_runner(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path = temporary_path / "target.json"
            target_path.write_text(json.dumps(self.target_candidate(), ensure_ascii=False) + "\n", encoding="utf-8")
            target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
            output_path = temporary_path / "candidate"
            result = self.run_script(
                "-ExportCandidate",
                "-ChangeId",
                "20990105T020304Z",
                "-TargetCandidateFile",
                str(target_path),
                "-ExpectedTargetCandidateSHA256",
                target_sha,
                "-OutputDirectory",
                str(output_path),
            )

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("production_readonly_candidate=passed", result.stdout)
            self.assertIn(f"target_candidate_sha256={target_sha}", result.stdout)
            self.assertIn("execute_readonly_authorized=false", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            runners = list(output_path.iterdir())
            self.assertEqual(len(runners), 1)
            runner_text = runners[0].read_text(encoding="utf-8-sig")
            self.assertIn("StrictHostKeyChecking=yes", runner_text)
            self.assertIn("HostKeyAlgorithms=ssh-ed25519", runner_text)
            self.assertIn("production_readonly_authorized=false", runner_text)
            self.assertIn("backup_capability_verified", runner_text)
            self.assertIn("duplicate_sms_config_absent", runner_text)
            self.assertIn('"schema_version"', runner_text)
            self.assertIn('"binding_distinct_templates"', runner_text)
            self.assertIn("[IO.FileMode]::CreateNew", runner_text)
            self.assertIn("结果文件不得使用网络映射盘", runner_text)
            self.assertIn("[IO.FileAttributes]::ReparsePoint", runner_text)
            self.assertIn("runner_sha256 = $runnerSHA256", runner_text)
            self.assertIn("sensitive_values_persisted = 0", runner_text)
            self.assertEqual(runner_text.count("& $sshPath @sshArguments"), 1)
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")

            closed = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runners[0])],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
            self.assertIn("production_readonly_authorized=false", closed.stdout)
            self.assertIn("network_connections=0", closed.stdout)
            self.assertIn("low_sensitivity_result_persisted=false", closed.stdout)

            runner_self_test = subprocess.run(
                [
                    self.powershell,
                    "-NoProfile",
                    "-ExecutionPolicy",
                    "Bypass",
                    "-File",
                    str(runners[0]),
                    "-SelfTest",
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(runner_self_test.returncode, 0, runner_self_test.stdout + runner_self_test.stderr)
            self.assertIn("production_readonly_runner_self_test=passed", runner_self_test.stdout)
            self.assertIn("network_connections=0", runner_self_test.stdout)

            # Windows 的 System32 bash.exe 可能只是 WSL 转发器，优先使用可独立运行的 Git Bash。
            git_bash = pathlib.Path(r"C:\Program Files\Git\bin\bash.exe")
            bash = str(git_bash) if sys.platform == "win32" and git_bash.is_file() else shutil.which("bash")
            if bash is not None:
                import re

                encoded = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', runner_text)
                self.assertIsNotNone(encoded)
                payload = base64.b64decode(encoded.group(1)).decode("utf-8")
                syntax = subprocess.run(
                    [bash, "-n"],
                    input=payload,
                    text=True,
                    capture_output=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertEqual(syntax.returncode, 0, syntax.stdout + syntax.stderr)

    def test_export_rejects_target_digest_mismatch_before_writing(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path = temporary_path / "target.json"
            target_path.write_text(json.dumps(self.target_candidate()) + "\n", encoding="utf-8")
            output_path = temporary_path / "candidate"
            result = self.run_script(
                "-ExportCandidate",
                "-ChangeId",
                "20990105T020304Z",
                "-TargetCandidateFile",
                str(target_path),
                "-ExpectedTargetCandidateSHA256",
                "0" * 64,
                "-OutputDirectory",
                str(output_path),
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("生产目标候选摘要不匹配", result.stdout + result.stderr)
            self.assertFalse(output_path.exists())

    def test_remote_payload_contains_only_read_operations(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8-sig")
        forbidden = (
            "curl -X POST",
            "docker restart",
            "systemctl restart",
            "sed -i",
            "INSERT INTO",
            "UPDATE sms_",
            "DELETE FROM",
        )
        for marker in forbidden:
            self.assertNotIn(marker, source)
        self.assertIn("SELECT CONCAT(version,':',dirty)", source)
        self.assertIn("SELECT CONCAT(COUNT(*),':',SUM(submit_status='accepted')", source)
        self.assertIn("configuration_mutations=0", source)
        self.assertIn("business_posts=0", source)
        self.assertIn("real_sms_sent=0", source)

    def test_contract_is_wired_into_readiness_ci_and_documentation(self) -> None:
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        deployment = (ROOT / "docs" / "sms-phase5-deployment-plan.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-production-readonly-baseline.ps1", readiness)
        self.assertIn("phase5_production_readonly_baseline_candidate_contract.py", ci)
        self.assertIn("prepare-sms-phase5-production-readonly-baseline.ps1", deployment)
        self.assertIn("prepare-sms-phase5-production-readonly-baseline.ps1", tools)


if __name__ == "__main__":
    unittest.main()

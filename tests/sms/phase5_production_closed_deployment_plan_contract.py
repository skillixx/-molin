import base64
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-production-closed-deployment-plan.ps1"
BASELINE_SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-production-readonly-baseline.ps1"


class Phase5ProductionClosedDeploymentPlanContract(unittest.TestCase):
    """验证生产关闭态部署计划只绑定低敏证据且不继承任何执行授权。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
        if cls.powershell is None:
            raise unittest.SkipTest("当前环境没有 PowerShell")
        cls.runner_temporary = tempfile.TemporaryDirectory(dir=ROOT)
        temporary_path = pathlib.Path(cls.runner_temporary.name)
        target_path = temporary_path / "target.json"
        target_path.write_text(json.dumps(cls.target_candidate()) + "\n", encoding="utf-8")
        target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
        output_path = temporary_path / "runner"
        generated = subprocess.run(
            [
                cls.powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(BASELINE_SCRIPT),
                "-ExportCandidate",
                "-ChangeId",
                "20990105T020304Z",
                "-TargetCandidateFile",
                str(target_path),
                "-ExpectedTargetCandidateSHA256",
                target_sha,
                "-OutputDirectory",
                str(output_path),
            ],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )
        if generated.returncode != 0:
            cls.runner_temporary.cleanup()
            raise RuntimeError(generated.stdout + generated.stderr)
        runners = list(output_path.glob("*.ps1"))
        if len(runners) != 1:
            cls.runner_temporary.cleanup()
            raise RuntimeError("生产只读基线生成器没有生成唯一 runner")
        cls.runner_bytes = runners[0].read_bytes()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.runner_temporary.cleanup()

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

    @staticmethod
    def readonly_result(target_sha: str, runner_sha: str) -> dict[str, object]:
        observed = {
            "production_readonly_baseline": "passed",
            "app_env_production": "true",
            "sms_enabled_false": "true",
            "sms_test_mode_true": "true",
            "provider_aliyun": "true",
            "endpoint_official": "true",
            "required_sms_config_present": "true",
            "legacy_sms_keys_absent": "true",
            "template_env_overrides_absent": "true",
            "duplicate_sms_config_absent": "true",
            "environment_file_secure": "true",
            "service_running": "true",
            "process_environment_readable": "true",
            "file_process_sms_config_match": "true",
            "health_ready": "true",
            "schema_ready": "true",
            "schema_version": "64",
            "schema_dirty": "0",
            "template_bindings_ready": "true",
            "template_total": "5",
            "template_approved": "5",
            "template_enabled": "5",
            "binding_total": "5",
            "binding_enabled": "5",
            "binding_distinct_templates": "5",
            "send_log_readable": "true",
            "send_total": "0",
            "send_accepted": "0",
            "send_failed": "0",
            "metrics_ready": "true",
            "sms_metric_shape_ready": "true",
            "prometheus_ready": "true",
            "sms_alert_rules_loaded": "true",
            "prometheus_target_up": "true",
            "active_sms_alerts": "0",
            "notification_failures_total": "0",
            "alertmanager_ready": "true",
            "rollback_operator_declared": "true",
            "observer_declared": "true",
            "backup_capability_verified": "false",
            "configuration_mutations": "0",
            "service_operations": "0",
            "business_posts": "0",
            "uploads": "0",
            "emails_sent": "0",
            "real_sms_sent": "0",
        }
        return {
            "schema_version": 1,
            "change_id": "20990105T020304Z",
            "target_change_id": "20990105T010203Z",
            "target_candidate_sha256": target_sha,
            "runner_sha256": runner_sha,
            "observed": observed,
            "network_connections": 1,
            "remote_stderr_present": False,
            "readonly_exit_code": 0,
            "uploads": 0,
            "configuration_mutations": 0,
            "service_operations": 0,
            "business_posts": 0,
            "emails_sent": 0,
            "real_sms_sent": 0,
            "sensitive_values_persisted": 0,
        }

    def fixture_files(
        self, directory: pathlib.Path
    ) -> tuple[pathlib.Path, str, pathlib.Path, str, pathlib.Path, str]:
        target_path = directory / "target.json"
        target_path.write_text(json.dumps(self.target_candidate()) + "\n", encoding="utf-8")
        target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
        runner_path = directory / "readonly-runner.ps1"
        runner_path.write_bytes(self.runner_bytes)
        runner_sha = hashlib.sha256(runner_path.read_bytes()).hexdigest()
        readonly_path = directory / "readonly.json"
        readonly_path.write_text(json.dumps(self.readonly_result(target_sha, runner_sha)) + "\n", encoding="utf-8")
        readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
        return target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha

    def export_arguments(
        self,
        target_path: pathlib.Path,
        target_sha: str,
        readonly_path: pathlib.Path,
        readonly_sha: str,
        runner_path: pathlib.Path,
        runner_sha: str,
        output_path: pathlib.Path,
    ) -> list[str]:
        return [
            "-ExportPlan",
            "-ChangeId",
            "20990105T030405Z",
            "-TargetCandidateFile",
            str(target_path),
            "-ExpectedTargetCandidateSHA256",
            target_sha,
            "-ReadonlyResultFile",
            str(readonly_path),
            "-ExpectedReadonlyResultSHA256",
            readonly_sha,
            "-ReadonlyRunnerFile",
            str(runner_path),
            "-ExpectedReadonlyRunnerSHA256",
            runner_sha,
            "-ReleaseCommitSHA",
            "f" * 40,
            "-ApiArtifactSHA256",
            "c" * 64,
            "-AdminImageDigest",
            "sha256:" + "a" * 64,
            "-UserImageDigest",
            "sha256:" + "b" * 64,
            "-MigrationAction",
            "verify-only",
            "-BackupEvidenceSHA256",
            "d" * 64,
            "-RollbackEvidenceSHA256",
            "e" * 64,
            "-OutputDirectory",
            str(output_path),
        ]

    def test_default_and_self_test_are_offline(self) -> None:
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("production_closed_deployment_plan_authorized=false", closed.stdout)
        self.assertIn("plan_files_written=0", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)

        self_test = self.run_script("-SelfTest")
        self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
        self.assertIn("production_closed_deployment_plan_self_test=passed", self_test.stdout)
        self.assertIn("verify_only_requires_schema_59=true", self_test.stdout)
        self.assertIn("migration_requires_schema_58=true", self_test.stdout)
        self.assertIn("unsupported_migration_start_rejected=true", self_test.stdout)
        self.assertIn("deployment_authorized=false", self_test.stdout)

    def test_export_binds_artifacts_and_keeps_all_execution_gates_closed(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha = self.fixture_files(temporary_path)
            output_path = temporary_path / "plan"
            result = self.run_script(
                *self.export_arguments(
                    target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
                )
            )

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("production_closed_deployment_plan=passed", result.stdout)
            self.assertIn("deployment_authorized=false", result.stdout)
            self.assertIn("migration_authorized=false", result.stdout)
            self.assertIn("canary_authorized=false", result.stdout)
            self.assertIn("production_enable_authorized=false", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            plans = list(output_path.iterdir())
            self.assertEqual(len(plans), 1)
            plan = json.loads(plans[0].read_text(encoding="utf-8"))
            self.assertEqual(plan["acceptance_scope"], "closed_state_deployment")
            self.assertEqual(plan["migration_action"], "verify-only")
            self.assertFalse(plan["expected_sms_enabled"])
            self.assertTrue(plan["expected_sms_test_mode"])
            self.assertTrue(plan["automatic_rollback_required"])
            self.assertFalse(plan["backup_recovery_verified"])
            self.assertEqual(plan["readonly_runner_sha256"], runner_sha)
            self.assertEqual(
                plan["readonly_runner_generator_sha256"],
                hashlib.sha256(BASELINE_SCRIPT.read_bytes()).hexdigest(),
            )
            self.assertTrue(plan["deployment_requires_separate_approval"])
            self.assertEqual(plan["automatic_retries"], 0)
            self.assertEqual(plan["real_sms_sent"], 0)

    def test_export_rejects_migration_decision_inconsistent_with_schema(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha = self.fixture_files(temporary_path)
            output_path = temporary_path / "plan"
            arguments = self.export_arguments(
                target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
            )
            arguments[arguments.index("verify-only")] = "apply-up-to-59"
            result = self.run_script(*arguments)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("migration 决策不一致", result.stdout + result.stderr)
            self.assertFalse(output_path.exists())

    def test_export_rejects_tampered_readonly_runner(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha = self.fixture_files(temporary_path)
            # 重新计算伪造 runner 与结果摘要，证明即使调用方认可伪造摘要，也无法绕过权威生成器逐文件复核。
            runner_path.write_bytes(
                self.runner_bytes + b"\n# forged dead code and extra network operation\nssh attacker.invalid\n"
            )
            runner_sha = hashlib.sha256(runner_path.read_bytes()).hexdigest()
            readonly_path.write_text(
                json.dumps(self.readonly_result(target_sha, runner_sha)) + "\n", encoding="utf-8"
            )
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            result = self.run_script(
                *self.export_arguments(
                    target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
                )
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("权威生成器", result.stdout + result.stderr)
            self.assertFalse(output_path.exists())

    def test_export_rejects_readonly_result_with_side_effect(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path = temporary_path / "target.json"
            target_path.write_text(json.dumps(self.target_candidate()) + "\n", encoding="utf-8")
            target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
            runner_path = temporary_path / "readonly-runner.ps1"
            runner_path.write_bytes(self.runner_bytes)
            runner_sha = hashlib.sha256(runner_path.read_bytes()).hexdigest()
            readonly = self.readonly_result(target_sha, runner_sha)
            readonly["configuration_mutations"] = 1
            readonly_path = temporary_path / "readonly.json"
            readonly_path.write_text(json.dumps(readonly) + "\n", encoding="utf-8")
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            result = self.run_script(
                *self.export_arguments(
                    target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
                )
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("未批准副作用", result.stdout + result.stderr)
            self.assertFalse(output_path.exists())

    def test_export_rejects_inconsistent_send_summary(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path, target_sha, readonly_path, _, runner_path, runner_sha = self.fixture_files(temporary_path)
            readonly = json.loads(readonly_path.read_text(encoding="utf-8"))
            readonly["observed"]["send_total"] = "2"
            readonly_path.write_text(json.dumps(readonly) + "\n", encoding="utf-8")
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            result = self.run_script(
                *self.export_arguments(
                    target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
                )
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output_path.exists())

    def test_export_rejects_persisted_blocked_readonly_result(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path = temporary_path / "target.json"
            target_path.write_text(json.dumps(self.target_candidate()) + "\n", encoding="utf-8")
            target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
            runner_path = temporary_path / "readonly-runner.ps1"
            runner_path.write_bytes(self.runner_bytes)
            runner_sha = hashlib.sha256(runner_path.read_bytes()).hexdigest()
            readonly = self.readonly_result(target_sha, runner_sha)
            readonly["readonly_exit_code"] = 3
            readonly["observed"]["production_readonly_baseline"] = "blocked"
            readonly_path = temporary_path / "readonly.json"
            readonly_path.write_text(json.dumps(readonly) + "\n", encoding="utf-8")
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            result = self.run_script(
                *self.export_arguments(
                    target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
                )
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output_path.exists())

    def test_export_accepts_schema_only_block_for_migration_plan(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path = temporary_path / "target.json"
            target_path.write_text(json.dumps(self.target_candidate()) + "\n", encoding="utf-8")
            target_sha = hashlib.sha256(target_path.read_bytes()).hexdigest()
            runner_path = temporary_path / "readonly-runner.ps1"
            runner_path.write_bytes(self.runner_bytes)
            runner_sha = hashlib.sha256(runner_path.read_bytes()).hexdigest()
            readonly = self.readonly_result(target_sha, runner_sha)
            readonly["readonly_exit_code"] = 3
            readonly["observed"]["production_readonly_baseline"] = "blocked"
            readonly["observed"]["schema_ready"] = "false"
            readonly["observed"]["schema_version"] = "58"
            readonly_path = temporary_path / "readonly.json"
            readonly_path.write_text(json.dumps(readonly) + "\n", encoding="utf-8")
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            arguments = self.export_arguments(
                target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
            )
            arguments[arguments.index("verify-only")] = "apply-up-to-59"
            result = self.run_script(*arguments)

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            plan = json.loads(next(output_path.iterdir()).read_text(encoding="utf-8"))
            self.assertEqual(plan["migration_action"], "apply-up-to-59")
            self.assertTrue(plan["migration_requires_separate_approval"])

    def test_export_rejects_migration_plan_below_schema_58(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            temporary_path = pathlib.Path(temporary)
            target_path, target_sha, readonly_path, _, runner_path, runner_sha = self.fixture_files(temporary_path)
            readonly = json.loads(readonly_path.read_text(encoding="utf-8"))
            readonly["readonly_exit_code"] = 3
            readonly["observed"]["production_readonly_baseline"] = "blocked"
            readonly["observed"]["schema_ready"] = "false"
            readonly["observed"]["schema_version"] = "57"
            readonly_path.write_text(json.dumps(readonly) + "\n", encoding="utf-8")
            readonly_sha = hashlib.sha256(readonly_path.read_bytes()).hexdigest()
            output_path = temporary_path / "plan"
            arguments = self.export_arguments(
                target_path, target_sha, readonly_path, readonly_sha, runner_path, runner_sha, output_path
            )
            arguments[arguments.index("verify-only")] = "apply-up-to-59"
            result = self.run_script(*arguments)

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output_path.exists())

    def test_contract_is_wired_into_readiness_ci_and_documentation(self) -> None:
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        deployment = (ROOT / "docs" / "sms-phase5-deployment-plan.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")

        self.assertIn("prepare-sms-phase5-production-closed-deployment-plan.ps1", readiness)
        self.assertIn("phase5_production_closed_deployment_plan_contract.py", ci)
        self.assertIn("prepare-sms-phase5-production-closed-deployment-plan.ps1", deployment)
        self.assertIn("prepare-sms-phase5-production-closed-deployment-plan.ps1", tools)
        script = SCRIPT.read_text(encoding="utf-8-sig")
        self.assertIn("[IO.FileAttributes]::ReparsePoint", script)
        self.assertIn("[IO.FileMode]::CreateNew", script)


if __name__ == "__main__":
    unittest.main()

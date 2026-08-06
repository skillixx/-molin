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
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-canary-postcheck-readonly.ps1"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class CanaryPostcheckReadonlyCandidateContract(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SCRIPT), *arguments],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            timeout=60,
        )

    def make_source_files(self, directory: pathlib.Path, source_id: str):
        plan = directory / "plan.json"
        plan.write_text(
            json.dumps(
                {
                    "change_id": source_id,
                    "requested_sends": 5,
                    "max_sends": 5,
                    "same_target_min_interval_seconds": 65,
                    "scheduled_waits": 2,
                    "no_retries": True,
                    "acceptance_scope": "receipt_only",
                }
            ),
            encoding="utf-8",
        )
        runner = directory / "source-runner.ps1"
        runner.write_text("param()\n# 测试夹具不执行。\n", encoding="utf-8-sig")
        result = directory / "source-result.txt"
        result.write_text(
            "\n".join(
                [
                    "canary_send=awaiting_manual_receipt_confirmation",
                    "scene_register_submitted=true",
                    "scene_login_submitted=true",
                    "scene_reset_password_submitted=true",
                    "scene_bind_phone_submitted=true",
                    "scene_admin_verify_submitted=true",
                    "requested_sends=5",
                    "completed_scenes=5",
                    "sms_enabled=false",
                    "sms_test_mode=true",
                    "same_target_min_interval_seconds=65",
                    "scheduled_waits=2",
                    "completed_pacing_waits=2",
                    "same_target_min_interval_seconds=65",
                    "scheduled_waits=2",
                    "completed_pacing_waits=2",
                    "baseline_send_log_id=13",
                    "baseline_verification_code_id=21",
                    "baseline_send_total=13",
                    "baseline_send_accepted=13",
                    "baseline_send_failed=0",
                    "baseline_provider_calls_total=8",
                    "baseline_provider_nonaccepted_total=0",
                    "canary_completed_at=2099-01-01T00:00:00Z",
                    "sensitive_values_persisted=0",
                    "real_sms_receipt_confirmed=false",
                    "service_stops=2",
                    "service_starts=2",
                    "sms_submission_requests=5",
                    "automatic_retries=0",
                    "remote_stderr_present=false",
                    "canary_send_exit_code=0",
                    "",
                ]
            ),
            encoding="utf-8",
        )
        return plan, runner, result

    def test_default_closed_and_self_test(self):
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("canary_postcheck_readonly_candidate_authorized=false", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        self.assertIn("real_sms_sent=0", closed.stdout)

        checked = self.run_script("-SelfTest")
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertIn("source_result_binding_required=true", checked.stdout)
        self.assertIn("database_cursor_binding_required=true", checked.stdout)

    def test_export_binds_source_result_and_builds_readonly_runner(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source_id = "20990101T010203Z"
            change_id = "20990101T020304Z"
            plan, source_runner, source_result = self.make_source_files(root, source_id)
            output = root / "candidate"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", change_id,
                "-SourceCanaryChangeId", source_id,
                "-PlanFile", str(plan),
                "-ExpectedPlanSHA256", sha256(plan),
                "-CanaryRunnerFile", str(source_runner),
                "-ExpectedCanaryRunnerSHA256", sha256(source_runner),
                "-CanaryResultFile", str(source_result),
                "-ExpectedCanaryResultSHA256", sha256(source_result),
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("canary_postcheck_readonly_candidate=passed", completed.stdout)
            self.assertIn("candidate_files_written=1", completed.stdout)
            self.assertIn("network_connections=0", completed.stdout)
            self.assertIn("real_sms_sent=0", completed.stdout)

            generated = output / f"run-sms-phase5-canary-postcheck-readonly-{change_id}.ps1"
            text = generated.read_text(encoding="utf-8-sig")
            encoded = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', text).group(1)
            payload = base64.b64decode(encoded).decode("utf-8")
            for marker in (
                "baseline_send_log_id=13",
                "baseline_verification_code_id=21",
                "START TRANSACTION READ ONLY",
                "provider_request_id IS NOT NULL",
                "used_at IS NULL",
                "INTERNAL_API_TOKEN",
                "current_process_provider_metric_total",
                "五次受理由数据库持久证据证明",
                "alertmanager.closed.yml",
                "otp_unconsumed_verified=true",
                "configuration_mutations=0",
                "real_sms_sent=0",
            ):
                self.assertIn(marker, payload)
            for forbidden in ("curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "SMS_ENABLED=true", "kill "):
                self.assertNotIn(forbidden, payload)
            self.assertNotRegex(text, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertIn("ExpectedCanaryResultSHA256", text)
            self.assertIn("ExpectedCanaryRunnerSHA256", text)

            bash = shutil.which("bash")
            if bash:
                syntax = subprocess.run(
                    [bash, "-n"], input=payload, text=True, capture_output=True, encoding="utf-8", errors="replace", timeout=30
                )
                self.assertEqual(syntax.returncode, 0, syntax.stdout + syntax.stderr)

            runner_self_test = subprocess.run(
                [POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(generated), "-SelfTest"],
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                timeout=30,
            )
            self.assertEqual(runner_self_test.returncode, 0, runner_self_test.stdout + runner_self_test.stderr)
            self.assertIn("canary_postcheck_readonly_runner_self_test=passed", runner_self_test.stdout)

    def test_rejects_tampered_or_failed_source_result(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source_id = "20990101T030405Z"
            plan, source_runner, source_result = self.make_source_files(root, source_id)
            expected_result_sha = sha256(source_result)
            source_result.write_text(source_result.read_text(encoding="utf-8").replace("completed_scenes=5", "completed_scenes=4"), encoding="utf-8")
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", "20990101T040506Z",
                "-SourceCanaryChangeId", source_id,
                "-PlanFile", str(plan),
                "-ExpectedPlanSHA256", sha256(plan),
                "-CanaryRunnerFile", str(source_runner),
                "-ExpectedCanaryRunnerSHA256", sha256(source_runner),
                "-CanaryResultFile", str(source_result),
                "-ExpectedCanaryResultSHA256", expected_result_sha,
                "-OutputDirectory", str(root / "candidate"),
            )
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("Canary 结果摘要不匹配", completed.stdout + completed.stderr)


if __name__ == "__main__":
    unittest.main()

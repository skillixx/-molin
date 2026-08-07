import base64
import hashlib
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-canary-failure-readonly-diagnostic.ps1"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class CanaryFailureReadonlyDiagnosticContract(unittest.TestCase):
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

    @staticmethod
    def make_source_result(directory: pathlib.Path) -> pathlib.Path:
        result = directory / "source-result.txt"
        result.write_text(
            "\n".join(
                [
                    "scene_register_submitted=true",
                    "scene_login_submitted=true",
                    "canary_send=blocked",
                    "failure_gate=scene_reset_password",
                    "automatic_closed_state_restore=true",
                    "service_stops=2",
                    "service_starts=2",
                    "sms_submission_requests=3",
                    "automatic_retries=0",
                    "remote_stderr_present=false",
                    "canary_send_exit_code=2",
                    "",
                ]
            ),
            encoding="utf-8",
        )
        return result

    def test_default_closed_and_self_test(self):
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("canary_failure_readonly_candidate_authorized=false", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        self.assertIn("real_sms_sent=0", closed.stdout)

        checked = self.run_script("-SelfTest")
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertIn("partial_failure_binding_required=true", checked.stdout)
        self.assertIn("baseline_send_total_required=true", checked.stdout)

    def test_export_binds_partial_failure_and_builds_readonly_runner(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source_id = "20990101T010203Z"
            change_id = "20990101T020304Z"
            source_result = self.make_source_result(root)
            output = root / "candidate"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", change_id,
                "-SourceCanaryChangeId", source_id,
                "-CanaryResultFile", str(source_result),
                "-ExpectedCanaryResultSHA256", sha256(source_result),
                "-ExpectedBaselineSendTotal", "13",
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("canary_failure_readonly_candidate=passed", completed.stdout)
            self.assertIn("baseline_send_total=13", completed.stdout)
            self.assertIn("network_connections=0", completed.stdout)
            self.assertIn("real_sms_sent=0", completed.stdout)

            generated = output / f"run-sms-phase5-canary-failure-readonly-diagnostic-{change_id}.ps1"
            text = generated.read_text(encoding="utf-8-sig")
            encoded = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', text).group(1)
            payload = base64.b64decode(encoded).decode("utf-8")
            for marker in (
                f"source_change_id='{source_id}'",
                "baseline_send_total=13",
                "START TRANSACTION READ ONLY",
                "scene='reset_password'",
                "submit_status='failed'",
                "短信供应商触发频率限制",
                "used_at IS NULL",
                "alertmanager.closed.yml",
                "recovery_lock_clear=true",
                "configuration_mutations=0",
                "real_sms_sent=0",
            ):
                self.assertIn(marker, payload)
            for forbidden in ("curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "SMS_ENABLED=true", "kill ", "sleep "):
                self.assertNotIn(forbidden, payload)
            self.assertNotRegex(text, r"(?<!\d)1[3-9]\d{9}(?!\d)")

            # Windows 的 System32 bash.exe 可能只是 WSL 转发器，优先使用可独立运行的 Git Bash。
            git_bash = pathlib.Path(r"C:\Program Files\Git\bin\bash.exe")
            bash = str(git_bash) if sys.platform == "win32" and git_bash.is_file() else shutil.which("bash")
            if bash:
                syntax = subprocess.run(
                    [bash, "-n"], input=payload, text=True, capture_output=True,
                    encoding="utf-8", errors="replace", timeout=30,
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
            self.assertIn("canary_failure_readonly_runner_self_test=passed", runner_self_test.stdout)

    def test_rejects_tampered_source_result(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source_result = self.make_source_result(root)
            expected_sha = sha256(source_result)
            source_result.write_text(
                source_result.read_text(encoding="utf-8").replace("sms_submission_requests=3", "sms_submission_requests=4"),
                encoding="utf-8",
            )
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", "20990101T040506Z",
                "-SourceCanaryChangeId", "20990101T030405Z",
                "-CanaryResultFile", str(source_result),
                "-ExpectedCanaryResultSHA256", expected_sha,
                "-ExpectedBaselineSendTotal", "13",
                "-OutputDirectory", str(root / "candidate"),
            )
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("源结果摘要不匹配", completed.stdout + completed.stderr)


if __name__ == "__main__":
    unittest.main()

import base64
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-observation-snapshot-readonly.ps1"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
WINDOWS = {"5m": 300, "15m": 900, "30m": 1800, "2h": 7200, "24h": 86400}


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class ObservationSnapshotCandidateContract(unittest.TestCase):
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

    def make_result(self, path: pathlib.Path) -> None:
        path.write_text(
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
            newline="\n",
        )

    def test_default_closed_and_self_test(self):
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("observation_snapshot_candidate_authorized=false", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        checked = self.run_script("-SelfTest")
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertIn("no_internal_sleep=true", checked.stdout)
        self.assertIn("one_snapshot_per_window=true", checked.stdout)

    def test_export_builds_one_hash_frozen_five_window_runner(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            result_path = root / "canary-result.txt"
            self.make_result(result_path)
            output = root / "candidate"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", "20990102T000000Z",
                "-SourceCanaryChangeId", "20990101T000000Z",
                "-CanaryResultFile", str(result_path),
                "-ExpectedCanaryResultSHA256", sha256(result_path),
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("observation_snapshot_candidate=passed", completed.stdout)
            self.assertIn("observation_windows=5m,15m,30m,2h,24h", completed.stdout)
            self.assertIn("network_connections=0", completed.stdout)
            runner = output / "run-sms-phase5-observation-snapshot-20990102T000000Z.ps1"
            text = runner.read_text(encoding="utf-8-sig")
            payload_json = re.search(r"\$Payloads = ConvertFrom-Json '([^']+)'", text).group(1).replace("''", "'")
            payloads = json.loads(payload_json)
            self.assertEqual(set(payloads), set(WINDOWS))
            # Windows 自带的 bash.exe 可能只是未安装 WSL 发行版的转发器；仅在原生 Linux CI 执行 Bash 语法检查。
            bash = shutil.which("bash") if sys.platform.startswith("linux") else None
            for window, minimum in WINDOWS.items():
                raw_payload = base64.b64decode(payloads[window])
                self.assertTrue(raw_payload.startswith(b"set"))
                self.assertFalse(raw_payload.startswith(b"\xef\xbb\xbf"))
                payload = raw_payload.decode("utf-8")
                for marker in (
                    f"window='{window}'",
                    f"minimum_elapsed={minimum}",
                    "START TRANSACTION READ ONLY",
                    "sms_provider_calls_total",
                    "sms_provider_request_duration_seconds_sum",
                    "notification_failed_delta=0",
                    "configuration_mutations=0",
                    "real_sms_sent=0",
                ):
                    self.assertIn(marker, payload)
                for forbidden in ("sleep ", "kill ", "curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "SMS_ENABLED=true"):
                    self.assertNotIn(forbidden, payload)
                if bash:
                    syntax = subprocess.run([bash, "-n"], input=payload, text=True, capture_output=True, timeout=30)
                    self.assertEqual(syntax.returncode, 0, syntax.stdout + syntax.stderr)
            self.assertIn('ValidateSet("5m", "15m", "30m", "2h", "24h")', text)
            self.assertIn("该观察窗口快照已存在，禁止重复执行", text)
            self.assertIn("观察窗口尚未到达，禁止提前连接测试服", text)
            self.assertIn('"-T", "-p", "10003"', text)
            self.assertIn('"LANG=C", "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-s", "--"', text)
            self.assertIn("Microsoft.PowerShell.Management\\Start-Process", text)
            self.assertIn("-RedirectStandardInput $stdinPath", text)
            self.assertIn('Write-Output "stdin_transport=file_redirect_no_bom"', text)
            self.assertIn("$payloadBytes[0] -ne 0x73", text)
            self.assertNotIn("StandardInput.BaseStream", text)
            self.assertNotIn("RedirectStandardInput = $true", text)
            self.assertIn("$safeLines | Write-Output", text)
            self.assertIn('Write-Output "remote_stderr_present=$($stderrPresent.ToString().ToLowerInvariant())"', text)
            self.assertIn('Write-Output "remote_stderr_classification=$($stderrMetadata.Classification)"', text)
            self.assertIn('Write-Output "remote_stderr_sha256=$($stderrMetadata.SHA256)"', text)
            self.assertNotIn("Write-Output $stderr", text)
            runner_test = subprocess.run(
                [POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-SelfTest"],
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                timeout=30,
            )
            self.assertEqual(runner_test.returncode, 0, runner_test.stdout + runner_test.stderr)
            self.assertIn("observation_snapshot_runner_self_test=passed", runner_test.stdout)
            self.assertIn("stderr_metadata_self_test=passed", runner_test.stdout)
            self.assertIn("stdin_transport=file_redirect_no_bom", runner_test.stdout)

    def test_rejects_source_result_hash_mismatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            result_path = root / "canary-result.txt"
            self.make_result(result_path)
            expected = sha256(result_path)
            result_path.write_text(
                result_path.read_text(encoding="utf-8").replace("completed_scenes=5", "completed_scenes=4"),
                encoding="utf-8",
            )
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", "20990102T010000Z",
                "-SourceCanaryChangeId", "20990101T000000Z",
                "-CanaryResultFile", str(result_path),
                "-ExpectedCanaryResultSHA256", expected,
                "-OutputDirectory", str(root / "candidate"),
            )
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("Canary 结果摘要不匹配", completed.stdout + completed.stderr)


if __name__ == "__main__":
    unittest.main()

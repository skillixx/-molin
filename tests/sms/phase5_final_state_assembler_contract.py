import datetime as dt
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "assemble-sms-phase5-final-state.py"


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_text(path: pathlib.Path, values: dict[str, str], newline: str = "\n") -> None:
    path.write_text("".join(f"{key}={value}\n" for key, value in values.items()), encoding="utf-8", newline=newline)


def write_json(path: pathlib.Path, value: object) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


class FinalStateAssemblerContract(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments], cwd=ROOT, text=True,
            capture_output=True, encoding="utf-8", errors="replace", timeout=30,
        )

    def make_fixture(self, root: pathlib.Path) -> tuple[list[str], pathlib.Path, pathlib.Path]:
        source_id = "20990101T000000Z"
        canary = root / "canary.txt"
        canary_values = {
            "scene_register_submitted": "true", "scene_login_submitted": "true",
            "scene_reset_password_submitted": "true", "scene_bind_phone_submitted": "true",
            "scene_admin_verify_submitted": "true", "requested_sends": "5", "completed_scenes": "5",
            "sms_enabled": "false", "sms_test_mode": "true", "automatic_retries": "0",
            "canary_send_exit_code": "0", "baseline_send_total": "16",
            "baseline_send_accepted": "15", "baseline_send_failed": "1",
        }
        write_text(canary, canary_values)
        postcheck = root / "postcheck.txt"
        postcheck_values = {
            "canary_postcheck_readonly": "passed", "sms_enabled": "false", "sms_test_mode": "true",
            "health_ready_verified": "true", "post_baseline_send_logs": "5", "accepted_send_logs": "5",
            "distinct_scenes": "5", "provider_acceptance_fields_complete": "true",
            "post_baseline_verification_codes": "5", "otp_unconsumed_verified": "true",
            "log_verification_join_verified": "true", "alertmanager_discard_verified": "true",
            "active_alertmanager_alerts": "0", "active_sms_alerts": "0", "notification_failures": "0",
            "recovery_lock_clear": "true", "recovery_materials_clear": "true",
            "configuration_mutations": "0", "service_signals": "0", "service_restarts": "0",
            "business_posts": "0", "emails_sent": "0", "sms_submission_requests": "0", "real_sms_sent": "0",
            "remote_stderr_present": "false", "readonly_exit_code": "0",
        }
        # 真实事后核验结果由 Windows PowerShell 生成，因此契约夹具必须覆盖规范 CRLF。
        write_text(postcheck, postcheck_values, newline="\r\n")
        snapshot = root / "snapshot-24h.json"
        write_json(snapshot, {
            "schema_version": 1, "source_canary_change_id": source_id,
            "snapshot": {
                "window": "24h", "observed_at": dt.datetime(2099, 1, 2, tzinfo=dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "elapsed_seconds": 86400, "api_health_http": 200, "api_ready_http": 200,
                "send_total": 21, "send_accepted": 20, "send_failed": 1,
                "provider_calls_total": 0, "provider_nonaccepted_total": 0,
                "avg_provider_duration_seconds": 0, "active_sms_alerts": 0,
                "active_alertmanager_alerts": 0, "notification_failed_delta": 0,
            },
        })
        output = root / "final-state.json"
        arguments = [
            "--source-canary-change-id", source_id,
            "--canary-result", str(canary), "--expected-canary-result-sha256", sha256(canary),
            "--postcheck-result", str(postcheck), "--expected-postcheck-result-sha256", sha256(postcheck),
            "--snapshot-24h", str(snapshot), "--expected-snapshot-24h-sha256", sha256(snapshot),
            "--output", str(output),
        ]
        return arguments, output, snapshot

    def test_self_test_is_offline(self) -> None:
        result = self.run_script("--self-test")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_assembles_strict_final_state(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, _ = self.make_fixture(pathlib.Path(temp))
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            value = json.loads(output.read_text(encoding="utf-8"))
            self.assertFalse(value["final_state"]["sms_enabled"])
            self.assertTrue(value["final_state"]["sms_test_mode"])
            self.assertEqual(value["final_state"]["unexpected_business_mutations"], 0)

    def test_rejects_closed_state_send_growth(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, snapshot = self.make_fixture(pathlib.Path(temp))
            value = json.loads(snapshot.read_text(encoding="utf-8"))
            value["snapshot"]["send_total"] = 22
            write_json(snapshot, value)
            hash_index = arguments.index("--expected-snapshot-24h-sha256") + 1
            arguments[hash_index] = sha256(snapshot)
            result = self.run_script(*arguments)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())

    def test_rejects_mixed_postcheck_line_endings(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, _ = self.make_fixture(pathlib.Path(temp))
            postcheck = pathlib.Path(arguments[arguments.index("--postcheck-result") + 1])
            # 仅把第一处 CRLF 改为 LF，验证混合换行不能绕过低敏文本边界。
            postcheck.write_bytes(postcheck.read_bytes().replace(b"\r\n", b"\n", 1))
            hash_index = arguments.index("--expected-postcheck-result-sha256") + 1
            arguments[hash_index] = sha256(postcheck)
            result = self.run_script(*arguments)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())

    def test_rejects_unapproved_sensitive_result_key(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, _ = self.make_fixture(pathlib.Path(temp))
            canary = pathlib.Path(arguments[arguments.index("--canary-result") + 1])
            # 即使摘要同步更新，完整手机号类额外字段也不能进入低敏证据输入。
            with canary.open("a", encoding="utf-8", newline="\n") as stream:
                stream.write("phone=13800138000\n")
            hash_index = arguments.index("--expected-canary-result-sha256") + 1
            arguments[hash_index] = sha256(canary)
            result = self.run_script(*arguments)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()

import datetime as dt
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "verify-sms-phase5-observation-progress.py"
WINDOWS = {"5m": 300, "15m": 900, "30m": 1800}


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class ObservationProgressContract(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments], cwd=ROOT, text=True,
            capture_output=True, encoding="utf-8", errors="replace", timeout=30,
        )

    def make_fixture(self, root: pathlib.Path) -> tuple[list[str], pathlib.Path]:
        source_id = "20990101T000000Z"
        started = dt.datetime(2099, 1, 1, tzinfo=dt.timezone.utc)
        canary = root / "canary.txt"
        canary.write_text("\n".join([
            "canary_send=awaiting_manual_receipt_confirmation",
            "scene_register_submitted=true", "scene_login_submitted=true",
            "scene_reset_password_submitted=true", "scene_bind_phone_submitted=true",
            "scene_admin_verify_submitted=true", "requested_sends=5", "completed_scenes=5",
            "sms_enabled=false", "sms_test_mode=true", "same_target_min_interval_seconds=65",
            "scheduled_waits=2", "completed_pacing_waits=2", "baseline_send_log_id=16",
            "baseline_verification_code_id=1751", "baseline_send_total=16",
            "baseline_send_accepted=15", "baseline_send_failed=1",
            "baseline_provider_calls_total=0", "baseline_provider_nonaccepted_total=0",
            "canary_completed_at=2099-01-01T00:00:00Z", "sensitive_values_persisted=0",
            "real_sms_receipt_confirmed=false", "service_stops=2", "service_starts=2",
            "sms_submission_requests=5", "automatic_retries=0", "remote_stderr_present=false",
            "canary_send_exit_code=0", "",
        ]), encoding="utf-8", newline="\n")
        snapshots = root / "snapshots"
        snapshots.mkdir()
        arguments = [
            "--source-canary-change-id", source_id,
            "--canary-result", str(canary),
            "--expected-canary-result-sha256", sha256(canary),
            "--snapshot-directory", str(snapshots), "--through", "30m",
        ]
        for window, elapsed in WINDOWS.items():
            path = snapshots / f"snapshot-{window}.json"
            value = {
                "schema_version": 1, "source_canary_change_id": source_id,
                "snapshot": {
                    "window": window,
                    "observed_at": (started + dt.timedelta(seconds=elapsed)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                    "elapsed_seconds": elapsed, "api_health_http": 200, "api_ready_http": 200,
                    "send_total": 21, "send_accepted": 20, "send_failed": 1,
                    "provider_calls_total": 0, "provider_nonaccepted_total": 0,
                    "avg_provider_duration_seconds": 0, "active_sms_alerts": 0,
                    "active_alertmanager_alerts": 0, "notification_failed_delta": 0,
                },
            }
            path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
            arguments.extend([f"--expected-snapshot-{window}-sha256", sha256(path)])
        return arguments, snapshots

    def test_self_test_is_offline(self) -> None:
        result = self.run_script("--self-test")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("continuous_prefix_required=true", result.stdout)
        self.assertIn("network_connections=0", result.stdout)

    def test_validates_three_window_prefix(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, _ = self.make_fixture(pathlib.Path(temp))
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("through_window=30m", result.stdout)
            self.assertIn("snapshots_verified=3", result.stdout)

    def test_rejects_provider_growth_even_with_matching_hash(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            arguments, snapshots = self.make_fixture(pathlib.Path(temp))
            path = snapshots / "snapshot-15m.json"
            value = json.loads(path.read_text(encoding="utf-8"))
            value["snapshot"]["provider_calls_total"] = 1
            path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
            index = arguments.index("--expected-snapshot-15m-sha256") + 1
            arguments[index] = sha256(path)
            result = self.run_script(*arguments)
            self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()

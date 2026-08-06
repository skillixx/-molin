import datetime as dt
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "assemble-sms-phase5-observation-evidence.py"
WINDOWS = {"5m": 300, "15m": 900, "30m": 1800, "2h": 7200, "24h": 86400}


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: pathlib.Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


class Phase5ObservationAssemblerContract(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment["PYTHONIOENCODING"] = "utf-8"
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            env=environment,
            timeout=30,
        )

    def make_fixture(self, root: pathlib.Path):
        source_id = "20260101T000000Z"
        evidence_id = "20260102T000000Z"
        started = dt.datetime.now(dt.timezone.utc).replace(microsecond=0) - dt.timedelta(hours=25)
        started_text = started.strftime("%Y-%m-%dT%H:%M:%SZ")
        result = root / "canary-result.txt"
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
                    "baseline_provider_calls_total=0",
                    "baseline_provider_nonaccepted_total=0",
                    f"canary_completed_at={started_text}",
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
        receipt = root / "receipt.json"
        write_json(
            receipt,
            {
                "schema_version": 1,
                "source_canary_change_id": source_id,
                "confirmed_at": (started + dt.timedelta(minutes=4)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "scene_receipts": {
                    "register": True,
                    "login": True,
                    "reset_password": True,
                    "bind_phone": True,
                    "admin_verify": True,
                },
            },
        )
        snapshots = root / "snapshots"
        snapshots.mkdir()
        snapshot_hashes = {}
        for window, elapsed in WINDOWS.items():
            path = snapshots / f"snapshot-{window}.json"
            write_json(
                path,
                {
                    "schema_version": 1,
                    "source_canary_change_id": source_id,
                    "snapshot": {
                        "window": window,
                        "observed_at": (started + dt.timedelta(seconds=elapsed)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                        "elapsed_seconds": elapsed,
                        "api_health_http": 200,
                        "api_ready_http": 200,
                        "send_total": 18,
                        "send_accepted": 18,
                        "send_failed": 0,
                        "provider_calls_total": 0,
                        "provider_nonaccepted_total": 0,
                        "avg_provider_duration_seconds": 0.4,
                        "active_sms_alerts": 0,
                        "active_alertmanager_alerts": 0,
                        "notification_failed_delta": 0,
                    },
                },
            )
            snapshot_hashes[window] = sha256(path)
        final = root / "final.json"
        write_json(
            final,
            {
                "schema_version": 1,
                "source_canary_change_id": source_id,
                "final_state": {
                    "sms_enabled": False,
                    "sms_test_mode": True,
                    "alertmanager_route": "discard",
                    "active_sms_alerts": 0,
                    "active_alertmanager_alerts": 0,
                    "notification_failed_delta": 0,
                    "unexpected_business_mutations": 0,
                },
            },
        )
        output = root / "evidence.json"
        arguments = [
            "--change-id", evidence_id,
            "--source-canary-change-id", source_id,
            "--canary-result", str(result),
            "--expected-canary-result-sha256", sha256(result),
            "--receipt-attestation", str(receipt),
            "--expected-receipt-sha256", sha256(receipt),
            "--snapshot-directory", str(snapshots),
        ]
        for window in WINDOWS:
            arguments.extend([f"--expected-snapshot-{window}-sha256", snapshot_hashes[window]])
        arguments.extend(
            [
                "--final-state", str(final),
                "--expected-final-state-sha256", sha256(final),
                "--output", str(output),
            ]
        )
        return arguments, output, receipt

    def test_self_test_is_offline(self):
        result = self.run_script("--self-test")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("five_snapshot_hashes_required=true", result.stdout)
        self.assertIn("manual_receipt_attestation_required=true", result.stdout)
        self.assertIn("network_connections=0", result.stdout)

    def test_assembles_and_revalidates_five_windows(self):
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, _ = self.make_fixture(pathlib.Path(temp))
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("phase5_observation_assembler=passed", result.stdout)
            self.assertIn("observation_windows=5m,15m,30m,2h,24h", result.stdout)
            self.assertTrue(output.is_file())
            evidence = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(len(evidence["snapshots"]), 5)
            self.assertEqual(evidence["canary_result"]["receipts_confirmed"], 5)
            self.assertNotRegex(output.read_text(encoding="utf-8"), r"(?<!\d)1[3-9]\d{9}(?!\d)")

    def test_rejects_tampered_receipt_attestation(self):
        with tempfile.TemporaryDirectory() as temp:
            arguments, output, receipt = self.make_fixture(pathlib.Path(temp))
            receipt.write_text(receipt.read_text(encoding="utf-8").replace('"login": true', '"login": false'), encoding="utf-8")
            result = self.run_script(*arguments)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("人工收件确认摘要不匹配", result.stdout)
            self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()

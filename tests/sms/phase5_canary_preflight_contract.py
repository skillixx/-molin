#!/usr/bin/env python3
"""阶段 5 测试服 Canary 只读聚合预检契约。"""

from __future__ import annotations

import hashlib
import json
import re
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
PREFLIGHT = ROOT / "scripts" / "verify-sms-phase5-test-server-canary-preflight.ps1"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
REPORT = ROOT / "docs" / "sms-phase5-canary-test-report.md"
TOOLS = ROOT / "docs" / "tools.md"


class CanaryPreflightContractTest(unittest.TestCase):
    """聚合入口必须只读、失败关闭，并且不能成为真实发送入口。"""

    def test_public_cli_is_read_only_and_wired_into_release_assets(self) -> None:
        self.assertTrue(PREFLIGHT.is_file())
        source = PREFLIGHT.read_text(encoding="utf-8-sig")
        readiness = READINESS.read_text(encoding="utf-8-sig")
        ci = CI.read_text(encoding="utf-8")
        report = REPORT.read_text(encoding="utf-8")
        tools = TOOLS.read_text(encoding="utf-8")

        for marker in (
            "[switch]$SelfTest",
            "我已确认阶段5测试服告警通知演练成功",
            "[string]$NotificationDrillChangeId",
            "[string]$NotificationDrillEvidencePath",
            "[string]$NotificationDrillEvidenceSHA256",
            "[switch]$ValidateNotificationEvidenceOnly",
            "canary_preflight=",
            "canary_preflight_ready=",
            "closed_state_ready",
            "rollback_candidate_ready",
            "notification_drill_ready",
            "log_retention_policy_verified",
            "real_sms_sent=0",
            "business_configuration_mutations=0",
        ):
            self.assertIn(marker, source)

        for forbidden in (
            "SMS_ENABLED=true",
            'Test-SafeValue $ClosedState "sms_provider_metric_total_before" "0"',
            'Test-SafeValue $ClosedState "sms_provider_metric_total_after" "0"',
            "/verification-codes/phone",
            "/sms/test-send",
            "Invoke-RestMethod",
            "Invoke-WebRequest",
            "ssh ",
        ):
            self.assertNotIn(forbidden, source)
        self.assertIsNone(
            re.search(r"(?im)^\s*(?:set-content|add-content|remove-item|move-item|copy-item)\b", source)
        )

        self.assertIn(PREFLIGHT.name, readiness)
        self.assertIn(Path(__file__).name, ci)
        self.assertIn(f"./scripts/{PREFLIGHT.name} -SelfTest", ci)
        self.assertIn(PREFLIGHT.name, report)
        self.assertIn(PREFLIGHT.name, tools)

    def test_self_test_proves_ready_and_fail_closed_cases(self) -> None:
        powershell = shutil.which("powershell") or shutil.which("pwsh")
        if powershell is None:
            self.skipTest("当前平台没有 PowerShell，Windows CI 负责执行行为自测")
        completed = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(PREFLIGHT),
                "-SelfTest",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        for marker in (
            "ready_case=passed",
            "closed_state_blocker_case=passed",
            "rollback_candidate_blocker_case=passed",
            "notification_blocker_case=passed",
            "log_retention_blocker_case=passed",
            "malformed_output_blocker_case=passed",
            "legacy_datetime_normalization_case=passed",
            "self_test=passed",
            "remote_connections=0",
            "business_configuration_mutations=0",
            "service_restarts=0",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, completed.stdout)

    def test_notification_attestation_requires_exact_phrase_and_sha256(self) -> None:
        powershell = shutil.which("powershell") or shutil.which("pwsh")
        if powershell is None:
            self.skipTest("当前平台没有 PowerShell，Windows CI 负责执行行为自测")
        change_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        evidence = {
            "schema": "molin.sms.phase5.notification-drill.v1",
            "environment": "test",
            "change_id": change_id,
            "created_at_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "result": "passed",
            "sms_enabled": False,
            "synthetic_alert_firing_count": 1,
            "synthetic_alert_resolved_count": 1,
            "alertmanager_received": True,
            "route_matched": True,
            "notification_attempted": True,
            "receiver_delivered": True,
            "on_call_acknowledged": True,
            "notification_queue_empty": True,
            "provider_call_delta": 0,
            "real_sms_sent": 0,
            "contains_sensitive_values": False,
        }

        def invoke(path: Path, digest: str, phrase: str) -> subprocess.CompletedProcess[str]:
            return subprocess.run(
                [
                    powershell,
                    "-NoProfile",
                    "-ExecutionPolicy",
                    "Bypass",
                    "-File",
                    str(PREFLIGHT),
                    "-ValidateNotificationEvidenceOnly",
                    "-NotificationDrillConfirmation",
                    phrase,
                    "-NotificationDrillChangeId",
                    change_id,
                    "-NotificationDrillEvidencePath",
                    str(path),
                    "-NotificationDrillEvidenceSHA256",
                    digest,
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )

        with tempfile.TemporaryDirectory(prefix="molin-phase5-notification-") as temp_dir:
            layer_fields = (
                ("alertmanager_evidence_path", "alertmanager_evidence_sha256"),
                ("route_evidence_path", "route_evidence_sha256"),
                ("notification_attempt_evidence_path", "notification_attempt_evidence_sha256"),
                ("receiver_delivery_evidence_path", "receiver_delivery_evidence_sha256"),
                ("on_call_ack_evidence_path", "on_call_ack_evidence_sha256"),
            )
            layer_paths: list[Path] = []
            for index, (path_key, digest_key) in enumerate(layer_fields, start=1):
                layer_path = Path(temp_dir) / f"layer-{index}.txt"
                layer_payload = f"phase5-notification-layer-{index}\n".encode()
                layer_path.write_bytes(layer_payload)
                layer_paths.append(layer_path)
                evidence[path_key] = str(layer_path)
                evidence[digest_key] = hashlib.sha256(layer_payload).hexdigest()

            path = Path(temp_dir) / "notification-drill.json"
            payload = json.dumps(evidence, separators=(",", ":"), ensure_ascii=False).encode()
            path.write_bytes(payload)
            digest = hashlib.sha256(payload).hexdigest()

            valid = invoke(path, digest, "我已确认阶段5测试服告警通知演练成功")
            self.assertEqual(valid.returncode, 0, valid.stderr)
            self.assertIn("notification_evidence_validation=passed", valid.stdout)
            self.assertIn(f"notification_drill_evidence_sha256={digest}", valid.stdout)
            self.assertIn("remote_connections=0", valid.stdout)
            self.assertIn("real_sms_sent=0", valid.stdout)

            valid_created_at = evidence["created_at_utc"]
            escaped_duplicate_payload = payload.replace(
                f'"created_at_utc":"{valid_created_at}"'.encode(),
                (
                    '"created_at\\u005futc":"2000-01-01T00:00:00Z",'
                    f'"created_at_utc":"{valid_created_at}"'
                ).encode(),
            )
            escaped_duplicate_path = Path(temp_dir) / "notification-drill-duplicate.json"
            escaped_duplicate_path.write_bytes(escaped_duplicate_payload)
            escaped_duplicate = invoke(
                escaped_duplicate_path,
                hashlib.sha256(escaped_duplicate_payload).hexdigest(),
                "我已确认阶段5测试服告警通知演练成功",
            )
            self.assertNotEqual(escaped_duplicate.returncode, 0)

            for suffix, created_at in (
                ("offset", datetime.now(timezone.utc).isoformat()),
                ("expired", (datetime.now(timezone.utc) - timedelta(hours=25)).strftime("%Y-%m-%dT%H:%M:%SZ")),
                ("future", (datetime.now(timezone.utc) + timedelta(minutes=6)).strftime("%Y-%m-%dT%H:%M:%SZ")),
            ):
                time_evidence = dict(evidence)
                time_evidence["created_at_utc"] = created_at
                time_payload = json.dumps(time_evidence, separators=(",", ":"), ensure_ascii=False).encode()
                time_path = Path(temp_dir) / f"notification-drill-{suffix}.json"
                time_path.write_bytes(time_payload)
                invalid_time = invoke(
                    time_path,
                    hashlib.sha256(time_payload).hexdigest(),
                    "我已确认阶段5测试服告警通知演练成功",
                )
                self.assertNotEqual(invalid_time.returncode, 0)

            wrong_phrase = invoke(path, digest, "错误确认短语")
            self.assertNotEqual(wrong_phrase.returncode, 0)
            wrong_digest = invoke(path, "a" * 64, "我已确认阶段5测试服告警通知演练成功")
            self.assertNotEqual(wrong_digest.returncode, 0)

            evidence["receiver_delivered"] = False
            invalid_payload = json.dumps(evidence, separators=(",", ":"), ensure_ascii=False).encode()
            path.write_bytes(invalid_payload)
            invalid = invoke(
                path,
                hashlib.sha256(invalid_payload).hexdigest(),
                "我已确认阶段5测试服告警通知演练成功",
            )
            self.assertNotEqual(invalid.returncode, 0)

            evidence["receiver_delivered"] = True
            missing_layer_payload = json.dumps(
                evidence, separators=(",", ":"), ensure_ascii=False
            ).encode()
            path.write_bytes(missing_layer_payload)
            layer_paths[-1].unlink()
            missing_layer = invoke(
                path,
                hashlib.sha256(missing_layer_payload).hexdigest(),
                "我已确认阶段5测试服告警通知演练成功",
            )
            self.assertNotEqual(missing_layer.returncode, 0)


if __name__ == "__main__":
    unittest.main()

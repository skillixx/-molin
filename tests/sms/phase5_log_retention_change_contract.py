#!/usr/bin/env python3
"""阶段 5 测试服 journald 留存策略变更资产契约。"""

from __future__ import annotations

import hashlib
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "apply-sms-phase5-test-server-log-retention.ps1"
SSH_HELPER = ROOT / "scripts" / "sms-phase5-test-server-ssh.ps1"
PAYLOAD = ROOT / "scripts" / "apply-sms-phase5-test-server-log-retention.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
RUNBOOK = ROOT / "docs" / "sms-phase5-log-retention-runbook.md"
POWERSHELL_EXE = shutil.which("pwsh") or shutil.which("powershell")


class LogRetentionChangeContractTest(unittest.TestCase):
    """锁定默认无连接、显式授权、关闭态部署和失败回滚行为。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig") + SSH_HELPER.read_text(
            encoding="utf-8-sig"
        )
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.runbook = RUNBOOK.read_text(encoding="utf-8")

    def require_powershell(self) -> str:
        """动态契约优先使用跨平台 PowerShell，缺失时由其他静态契约继续检查。"""
        if POWERSHELL_EXE is None:
            self.skipTest("当前平台没有 pwsh/powershell，CI PowerShell 步骤负责执行")
        return POWERSHELL_EXE

    def test_default_mode_and_self_test_never_connect(self) -> None:
        powershell = self.require_powershell()
        completed = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(POWERSHELL),
                "-SelfTest",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("self_test=passed", completed.stdout)
        self.assertIn("remote_connections=0", completed.stdout)
        self.assertIn("configuration_writes=0", completed.stdout)
        self.assertIn("service_restarts=0", completed.stdout)

        planned = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(POWERSHELL),
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        self.assertIn("apply_authorized=false", planned.stdout)
        self.assertIn("remote_connections=0", planned.stdout)

    def test_wrapper_locks_target_values_and_double_authorization(self) -> None:
        for marker in (
            '$ServerHost -cne "8.130.9.163"',
            '$SSHUser -cne "pc"',
            "$SSHPort -ne 10003",
            "[switch]$Apply",
            '"APPROVE_TEST_JOURNALD_RETENTION"',
            'SystemMaxUse = "8G"',
            'SystemKeepFree = "50G"',
            'MaxRetentionSec = "14day"',
            'MaxFileSec = "1day"',
            'SystemMaxUse -cne "8G"',
            'SystemKeepFree -cne "50G"',
            "HostKeyAlgorithms=ssh-ed25519",
            "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
        ):
            self.assertIn(marker, self.ps)

    def test_authorized_operator_payload_export_is_offline_and_never_overwrites(self) -> None:
        powershell = self.require_powershell()
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = Path(temp_dir) / "apply-journald-retention.sh"
            command = [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(POWERSHELL),
                "-ExportOperatorPayload",
                str(output_path),
            ]

            unauthorized = subprocess.run(
                command,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(unauthorized.returncode, 0)
            self.assertFalse(output_path.exists())

            exported = subprocess.run(
                command + ["-Authorization", "APPROVE_TEST_JOURNALD_RETENTION"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=True,
                env={**os.environ, "USERPROFILE": str(Path(temp_dir) / "missing-profile")},
            )
            self.assertIn("operator_payload_exported=true", exported.stdout)
            self.assertIn("remote_connections=0", exported.stdout)
            self.assertIn("configuration_writes=0", exported.stdout)
            self.assertIn("local_operator_payload_writes=1", exported.stdout)

            payload = output_path.read_bytes()
            hash_match = re.search(
                r"^operator_payload_sha256=([0-9a-f]{64})$",
                exported.stdout,
                re.MULTILINE,
            )
            self.assertIsNotNone(hash_match)
            self.assertEqual(hash_match.group(1), hashlib.sha256(payload).hexdigest())
            self.assertTrue(payload.startswith(b"#!/usr/bin/env bash\n"))
            self.assertNotIn(b"\r", payload)
            self.assertNotIn(b"\xef\xbb\xbf", payload)
            for expected in (
                b"SystemMaxUse=8G",
                b"SystemKeepFree=50G",
                b"MaxRetentionSec=14day",
                b"MaxFileSec=1day",
                b"b60555f0a2defd1c02b752b215989686592244e810e3d22c884ab5d5e8d578d4",
                b"machine_identity_verified=true",
            ):
                self.assertIn(expected, payload)
            self.assertNotIn(b"__SYSTEM_MAX_USE__", payload)
            self.assertNotIn(b"__EXPECTED_MACHINE_ID_SHA256__", payload)
            self.assertNotIn(b"SMS_ENABLED=true", payload)

            original = payload
            repeated = subprocess.run(
                command + ["-Authorization", "APPROVE_TEST_JOURNALD_RETENTION"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(repeated.returncode, 0)
            self.assertEqual(output_path.read_bytes(), original)

            combined_path = Path(temp_dir) / "combined-mode.sh"
            combined = subprocess.run(
                [
                    *command[:-1],
                    str(combined_path),
                    "-Apply",
                    "-Authorization",
                    "APPROVE_TEST_JOURNALD_RETENTION",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(combined.returncode, 0)
            self.assertFalse(combined_path.exists())

            for mixed_arguments in (
                ["-SelfTest", "-Apply"],
                ["-SelfTest", "-ExportOperatorPayload", str(combined_path)],
            ):
                mixed = subprocess.run(
                    [
                        powershell,
                        "-NoProfile",
                        "-ExecutionPolicy",
                        "Bypass",
                        "-File",
                        str(POWERSHELL),
                        *mixed_arguments,
                    ],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertNotEqual(mixed.returncode, 0)
            self.assertFalse(combined_path.exists())

            unc = subprocess.run(
                command[:-1]
                + [
                    r"\\server\share\apply-journald-retention.sh",
                    "-Authorization",
                    "APPROVE_TEST_JOURNALD_RETENTION",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertNotEqual(unc.returncode, 0)

            for ambiguous_path in (r"C:payload.sh", r"\root-relative-payload.sh"):
                ambiguous = subprocess.run(
                    command[:-1]
                    + [
                        ambiguous_path,
                        "-Authorization",
                        "APPROVE_TEST_JOURNALD_RETENTION",
                    ],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertNotEqual(ambiguous.returncode, 0)

            real_parent = Path(temp_dir) / "real-parent"
            linked_parent = Path(temp_dir) / "linked-parent"
            real_parent.mkdir()
            try:
                os.symlink(real_parent, linked_parent, target_is_directory=True)
            except OSError:
                self.assertIn("ReparsePoint", self.ps)
                self.assertIn(".Parent", self.ps)
            else:
                linked_export = subprocess.run(
                    command[:-1]
                    + [
                        str(linked_parent / "payload.sh"),
                        "-Authorization",
                        "APPROVE_TEST_JOURNALD_RETENTION",
                    ],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertNotEqual(linked_export.returncode, 0)
                self.assertFalse((real_parent / "payload.sh").exists())

    def test_payload_preserves_sms_closed_state_and_rolls_back_failures(self) -> None:
        for marker in (
            "SMS_ENABLED",
            "__EXPECTED_MACHINE_ID_SHA256__",
            "machine_identity_verified=true",
            "sms_closed_before=true",
            "sms_closed_after=true",
            "90-molin-sms-phase5-retention.conf",
            "SystemMaxUse=__SYSTEM_MAX_USE__",
            "SystemKeepFree=__SYSTEM_KEEP_FREE__",
            "MaxRetentionSec=__MAX_RETENTION_SEC__",
            "MaxFileSec=__MAX_FILE_SEC__",
            "rollback_journald_configuration",
            "journald_configuration_rollback_verified",
            "unsafe_config_directory",
            "unsafe_config_target",
            "unsafe_backup_root",
            "config_directory_was_present",
            "merged-config.sha256",
            "api-health.txt",
            "monitoring-health.txt",
            "provider_metric_delta_zero=true",
            "prometheus_provider_metric_delta_zero=true",
            "prometheus_postcheck=true",
            "systemctl restart systemd-journald",
            "log_retention_change_applied=true",
            "real_sms_delivery_not_verified=true",
        ):
            self.assertIn(marker, self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)
        self.assertIn('test -L "$config_dir"', self.sh)
        self.assertIn('test -L "$target"', self.sh)
        self.assertIn("trap 'on_signal HUP 129' HUP", self.sh)
        self.assertIn("trap 'on_signal INT 130' INT", self.sh)
        self.assertIn("trap 'on_signal TERM 143' TERM", self.sh)
        self.assertIn('verify_api_closed "$api_pid" || rollback_failed api_not_closed', self.sh)
        self.assertIn('mv -f -- "$staged_target" "$target"', self.sh)
        self.assertNotRegex(self.sh, r"\bjournalctl\s+(?:--vacuum|--rotate|--flush)\b")

    def test_assets_are_wired_into_readiness_ci_and_runbook(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn("phase5_log_retention_change_contract.py", self.ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)
        self.assertIn(POWERSHELL.name, self.runbook)
        self.assertIn("APPROVE_TEST_JOURNALD_RETENTION", self.runbook)
        self.assertIn("ExportOperatorPayload", self.runbook)
        self.assertIn("machine-id", self.runbook)
        self.assertIn("默认模式不连接测试服", self.runbook)

    def test_payload_does_not_embed_secrets_or_sms_operations(self) -> None:
        for pattern in (
            r"(?i)(access[_-]?key|secret|token)\s*[:=]\s*[\"']?(?!\$)[A-Za-z0-9+/=_-]{12,}",
            r"/api/auth/verification-codes/phone",
            r"/api/admin/sms/.+test-send",
        ):
            self.assertIsNone(re.search(pattern, self.sh), pattern)

    def test_linux_payload_self_test_covers_rollback_paths(self) -> None:
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("当前平台没有 Bash，Linux CI 和固定测试服语法门禁负责执行")
        completed = subprocess.run(
            [bash, str(PAYLOAD), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=True,
        )
        for marker in (
            "existing_config_rollback=passed",
            "absent_config_rollback=passed",
            "error_path_rollback=passed",
            "signal_HUP_rollback=passed",
            "signal_INT_rollback=passed",
            "signal_TERM_rollback=passed",
            "rollback_failure_exit_90=passed",
            "install_failure_rollback=passed",
            "restart_failure_rollback=passed",
            "payload_self_test=passed",
            "system_paths_written=0",
            "service_restarts=0",
        ):
            self.assertIn(marker, completed.stdout)


if __name__ == "__main__":
    unittest.main()

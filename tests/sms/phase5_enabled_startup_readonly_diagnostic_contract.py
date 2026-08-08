import base64
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
GENERATOR = ROOT / "scripts" / "prepare-sms-phase5-enabled-startup-readonly-diagnostic.ps1"


class Phase5EnabledStartupReadonlyDiagnosticContract(unittest.TestCase):
    """验证启用态启动诊断候选在取得独立授权前保持默认关闭和零副作用。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("powershell") or shutil.which("pwsh")
        if cls.powershell is None:
            raise unittest.SkipTest("缺少 PowerShell")

    def run_generator(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(GENERATOR), *args],
            cwd=ROOT,
            text=True,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )

    def test_default_and_self_test_are_offline(self) -> None:
        closed = self.run_generator()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("enabled_startup_readonly_candidate_authorized=false", closed.stdout)
        for marker in (
            "network_connections=0",
            "configuration_mutations=0",
            "service_signals=0",
            "service_restarts=0",
            "business_posts=0",
            "emails_sent=0",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, closed.stdout)

        self_test = self.run_generator("-SelfTest")
        self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
        self.assertIn("enabled_startup_readonly_candidate_self_test=passed", self_test.stdout)
        self.assertIn("boolean_only_result_contract=true", self_test.stdout)
        self.assertIn("network_connections=0", self_test.stdout)

    def test_export_freezes_readonly_boolean_only_payload(self) -> None:
        change_id = "20990107T010203Z"
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            output = pathlib.Path(temporary) / "candidate"
            result = self.run_generator(
                "-ExportCandidate",
                "-ChangeId",
                change_id,
                "-OutputDirectory",
                str(output),
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("enabled_startup_readonly_candidate=passed", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            runner = next(output.iterdir())
            runner_text = runner.read_text(encoding="utf-8-sig")
            payload_match = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', runner_text)
            self.assertIsNotNone(payload_match)
            payload = base64.b64decode(payload_match.group(1), validate=True).decode("utf-8")

            for marker in (
                "SMS_PROVIDER",
                "SMS_ALIYUN_ACCESS_KEY_ID",
                "SMS_ALIYUN_ACCESS_KEY_SECRET",
                "SMS_ALIYUN_SIGN_NAME",
                "SMS_ALIYUN_ENDPOINT",
                "SMS_PHONE_HMAC_SECRET",
                "SMS_TEST_PHONE_WHITELIST",
                "legacy_sms_keys_absent",
                "file_process_sms_config_parity",
                "enabled_startup_config_ready",
                "current_closed_api_ready",
            ):
                self.assertIn(marker, payload)
            for marker in (
                "ExpectedRunnerSHA256",
                "ExpectedSSHHelperSHA256",
                "Assert-SmsPhase5FixedTestServerIdentity",
                "StrictHostKeyChecking=yes",
                "HostKeyAlgorithms=ssh-ed25519",
                "[IO.FileMode]::CreateNew",
                "$safeKeys = @(",
                "remote_stderr_present=",
            ):
                self.assertIn(marker, runner_text)

            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(payload, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(payload, r"(?im)^\s*(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|DROP|TRUNCATE|CREATE)\b")
            self.assertNotRegex(payload, r"(?i)\b(?:kill|systemctl|scp|sftp|wget)\b")
            self.assertNotIn("SMS_ENABLED=true", payload)
            self.assertNotIn("curl -X POST", payload)
            self.assertNotIn("Read-Host", runner_text)
            self.assertNotIn("Bearer", runner_text)

    def test_export_rejects_existing_directory(self) -> None:
        change_id = "20990107T010204Z"
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            result = self.run_generator(
                "-ExportCandidate",
                "-ChangeId",
                change_id,
                "-OutputDirectory",
                temporary,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("禁止覆盖", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

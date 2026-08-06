import base64
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
GENERATOR = ROOT / "scripts" / "prepare-sms-phase5-legacy-config-cleanup.ps1"


class Phase5LegacyConfigCleanupCandidateContract(unittest.TestCase):
    """验证旧短信键清理候选只删除精确旧键，并在执行授权前保持完全关闭。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("powershell") or shutil.which("pwsh")
        if cls.powershell is None:
            raise unittest.SkipTest("缺少 PowerShell")
        cls.bash = shutil.which("bash")
        if cls.bash is None:
            git_bash = pathlib.Path(r"C:\Program Files\Git\bin\bash.exe")
            cls.bash = str(git_bash) if git_bash.is_file() else None

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
        source = GENERATOR.read_text(encoding="utf-8-sig")
        self.assertIn("Get-Command bash -CommandType Application -All", source)
        self.assertIn("$isWindowsPlatform", source)
        self.assertIn("Select-Object -First 1", source)
        closed = self.run_generator()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("legacy_cleanup_candidate_authorized=false", closed.stdout)
        for marker in (
            "network_connections=0",
            "uploads=0",
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
        self.assertIn("legacy_cleanup_candidate_self_test=passed", self_test.stdout)
        self.assertIn("exact_legacy_key_set_frozen=true", self_test.stdout)
        self.assertIn("automatic_rollback_contract=true", self_test.stdout)

    def test_export_freezes_exact_cleanup_and_rollback(self) -> None:
        change_id = "20990108T010203Z"
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
            self.assertIn("legacy_cleanup_candidate=passed", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("configuration_mutations=0", result.stdout)
            runner = next(output.iterdir())
            runner_text = runner.read_text(encoding="utf-8-sig")
            payload_match = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', runner_text)
            self.assertIsNotNone(payload_match)
            payload = base64.b64decode(payload_match.group(1), validate=True).decode("utf-8")

            if self.bash is not None:
                syntax = subprocess.run([self.bash, "-n"], input=payload, text=True, capture_output=True, check=False)
                self.assertEqual(syntax.returncode, 0, syntax.stdout + syntax.stderr)

            exact_set = re.search(
                r'legacy = \{b"SMS_ACCESS_KEY", b"SMS_ACCESS_SECRET", b"SMS_SIGN_NAME"\}',
                payload,
            )
            self.assertIsNotNone(exact_set)
            self.assertIn("if match and match.group(1) in legacy", payload)
            self.assertIn("if key in legacy", payload)
            self.assertIn("removed_file != removed_process", payload)
            self.assertIn("SMS_ALIYUN_ACCESS_KEY_ID", payload)
            self.assertIn("SMS_ALIYUN_ACCESS_KEY_SECRET", payload)
            self.assertIn("SMS_ALIYUN_SIGN_NAME", payload)
            self.assertIn("verify_sms_environment \"$new_pid\" absent", payload)
            self.assertIn("verify_sms_environment \"$restored_pid\" present", payload)
            self.assertIn("sleep 10", payload)
            self.assertIn("closed_state_stability_verified=true", payload)
            self.assertIn("rollback_armed=true", payload)
            self.assertIn("restore_original", payload)
            self.assertIn("trap 'handle_exit $?' EXIT", payload)
            self.assertIn("trap 'exit 130' INT TERM HUP", payload)
            self.assertIn("kill -TERM", payload)
            self.assertIn("kill -KILL", payload)
            self.assertIn("sha256sum \"/proc/${pid}/exe\"", payload)
            self.assertIn("stat -c '%U:%a' \"$env_file\"", payload)
            self.assertIn("verify_alertmanager_discard", payload)
            self.assertIn("SMS_ENABLED=false", payload)
            self.assertIn("SMS_TEST_MODE=true", payload)
            self.assertNotIn("SMS_ENABLED=true", payload)
            self.assertNotIn("send_scene", payload)
            self.assertNotRegex(payload, r"(?im)^\s*(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|DROP|TRUNCATE|CREATE)\b")
            self.assertNotRegex(payload, r"(?<!\d)1[3-9]\d{9}(?!\d)")

            for marker in (
                "ExpectedRunnerSHA256",
                "ExpectedSSHHelperSHA256",
                "Assert-SmsPhase5FixedTestServerIdentity",
                "StrictHostKeyChecking=yes",
                "HostKeyAlgorithms=ssh-ed25519",
                "[IO.FileMode]::CreateNew",
                "$safeKeys = @(",
                "low_sensitivity_result_persisted=true",
            ):
                self.assertIn(marker, runner_text)
            self.assertNotIn("Read-Host", runner_text)
            self.assertNotIn("Bearer", runner_text)
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")

    def test_export_rejects_existing_directory(self) -> None:
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            result = self.run_generator(
                "-ExportCandidate",
                "-ChangeId",
                "20990108T010204Z",
                "-OutputDirectory",
                temporary,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("禁止覆盖", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

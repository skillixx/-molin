import base64
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
GENERATOR = ROOT / "scripts" / "prepare-sms-phase5-canary-whitelist-change.ps1"


class Phase5CanaryWhitelistChangeCandidateContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.powershell = shutil.which("powershell") or shutil.which("pwsh")
        if cls.powershell is None:
            raise unittest.SkipTest("缺少 PowerShell")
        git_bash = pathlib.Path(r"C:\Program Files\Git\bin\bash.exe")
        cls.bash = str(git_bash) if sys.platform == "win32" and git_bash.is_file() else shutil.which("bash")

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

    def test_default_is_closed(self) -> None:
        result = self.run_generator()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("whitelist_change_candidate_authorized=false", result.stdout)
        self.assertIn("candidate_files_written=0", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("configuration_mutations=0", result.stdout)
        self.assertIn("service_restarts=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_generator_self_test_is_offline(self) -> None:
        result = self.run_generator("-SelfTest")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("whitelist_change_candidate_self_test=passed", result.stdout)
        self.assertIn("fixed_ssh_identity_contract_frozen=true", result.stdout)
        self.assertIn("network_connections=0", result.stdout)
        self.assertIn("configuration_mutations=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_export_freezes_exact_change_and_rollback_contract(self) -> None:
        change_id = "20990105T010203Z"
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
            self.assertIn("whitelist_change_candidate=passed", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
            self.assertIn("interactive_prompts=0", result.stdout)
            self.assertIn("network_connections=0", result.stdout)
            self.assertIn("configuration_mutations=0", result.stdout)
            self.assertIn("service_restarts=0", result.stdout)
            self.assertIn("real_sms_sent=0", result.stdout)

            files = list(output.iterdir())
            self.assertEqual(len(files), 1)
            runner = files[0]
            runner_text = runner.read_text(encoding="utf-8-sig")
            payload_match = re.search(r'\$RemotePayloadBase64 = "([A-Za-z0-9+/=]+)"', runner_text)
            self.assertIsNotNone(payload_match)
            payload = base64.b64decode(payload_match.group(1), validate=True).decode("utf-8")

            for marker in (
                "Read-Host -Prompt $Prompt -AsSecureString",
                "8.130.9.163",
                "10003",
                "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
                "StrictHostKeyChecking=yes",
                "StandardInput.BaseStream.Write",
                "[Array]::Clear($inputBytes",
                "bash -s",
            ):
                self.assertIn(marker, runner_text)
            self.assertNotIn("$startInfo.StandardInputEncoding", runner_text)
            self.assertNotIn("$remoteCommand =", runner_text)
            self.assertNotIn('eval "$(printf', runner_text)
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")

            for marker in (
                "env_file='/home/pc/molin/infra/.env.test'",
                "rollback_armed=true",
                "restore_original",
                "previous.env",
                "original.environ",
                "rollback_parent",
                "lock_directory_identity",
                "pc:600",
                "SMS_TEST_PHONE_WHITELIST",
                "sms_enabled=false",
                "sms_test_mode=true",
                "target_admin_added=true",
                "send_log_delta_zero=true",
                "provider_call_delta_zero=true",
                "notification_delta_zero=true",
                "whitelist_change_payload_self_test=passed",
                "automatic_file_rollback_test=passed",
                "service_stops=1",
                "service_starts=1",
                "sms_submission_requests=0",
                "real_sms_sent=0",
            ):
                self.assertIn(marker, payload)
            self.assertNotIn("SMS_ENABLED=true", payload)
            self.assertNotRegex(payload, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(payload, r"(?im)^\s*(?:insert|update|delete|replace|alter|drop|truncate|create)\b")
            self.assertNotRegex(runner_text, r"(?i)\b(?:scp|sftp|wget)\b")
            self.assertNotRegex(runner_text, r"(?i)invoke-(?:webrequest|restmethod)")
            self.assertNotRegex(runner_text, r"(?i)-method\s+post")
            self.assertNotIn(' -e "$statement"', payload)
            self.assertNotIn('"$statement" 2>/dev/null', payload)
            self.assertIn("SQL 只经标准输入交给固定参数的客户端", payload)
            self.assertIn('if ! mkdir -- "$lock_dir"; then', payload)
            self.assertIn('rmdir -- "$lock_dir" || fail lock_release', payload)
            self.assertIn("change_dir_verified=false", payload)
            self.assertIn('if [ "$change_dir_verified" = true ]', payload)
            self.assertIn('rmdir -- "$change_dir" 2>/dev/null || true', payload)
            lock_index = payload.index('if ! mkdir -- "$lock_dir"; then')
            self.assertLess(payload.rindex("trap 'handle_exit $?' EXIT", 0, lock_index), lock_index)
            self.assertLess(payload.rindex("trap '' INT TERM HUP", 0, lock_index), lock_index)
            change_dir_index = payload.index('if ! mkdir -- "$change_dir"; then')
            self.assertLess(payload.rindex("trap '' INT TERM HUP", 0, change_dir_index), change_dir_index)
            release_index = payload.index('rmdir -- "$lock_dir" || fail lock_release')
            self.assertLess(payload.rindex("trap '' INT TERM HUP", 0, release_index), release_index)
            self.assertLess(release_index, payload.index("rollback_armed=false", release_index))
            stage_index = payload.index('stage="${env_file}.stage-${change_id}"')
            rollback_index = payload.index("rollback_armed=true", stage_index)
            replace_index = payload.index('mv -fT -- "$stage" "$env_file"', stage_index)
            self.assertLess(stage_index, rollback_index)
            self.assertLess(rollback_index, replace_index)
            self.assertIn('if [ -n "$stage" ]; then rm -f -- "$stage"; fi', payload)

            if self.bash is not None:
                payload_self_test = subprocess.run(
                    [self.bash, "-s", "--", "--self-test"],
                    input=payload,
                    text=True,
                    capture_output=True,
                    encoding="utf-8",
                    errors="replace",
                    check=False,
                )
                self.assertEqual(payload_self_test.returncode, 0, payload_self_test.stdout + payload_self_test.stderr)
                self.assertIn("whitelist_change_payload_self_test=passed", payload_self_test.stdout)
                self.assertIn("candidate_add_only_test=passed", payload_self_test.stdout)
                self.assertIn("automatic_file_rollback_test=passed", payload_self_test.stdout)
                self.assertIn("network_connections=0", payload_self_test.stdout)
                self.assertIn("configuration_mutations=0", payload_self_test.stdout)
                self.assertIn("service_restarts=0", payload_self_test.stdout)
                self.assertIn("real_sms_sent=0", payload_self_test.stdout)

            closed = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner)],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
            self.assertIn("whitelist_change_execution_authorized=false", closed.stdout)
            self.assertIn("interactive_prompts=0", closed.stdout)
            self.assertIn("network_connections=0", closed.stdout)
            self.assertIn("configuration_mutations=0", closed.stdout)
            self.assertIn("real_sms_sent=0", closed.stdout)

            self_test = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-SelfTest"],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )
            self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
            self.assertIn("whitelist_change_runner_self_test=passed", self_test.stdout)
            self.assertIn("automatic_rollback_contract_verified=true", self_test.stdout)
            self.assertIn("network_connections=0", self_test.stdout)
            self.assertIn("configuration_mutations=0", self_test.stdout)
            self.assertIn("real_sms_sent=0", self_test.stdout)

    def test_invalid_or_existing_output_is_rejected(self) -> None:
        invalid = self.run_generator(
            "-ExportCandidate", "-ChangeId", "bad", "-OutputDirectory", str(ROOT / "never-created")
        )
        self.assertNotEqual(invalid.returncode, 0)
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            existing = pathlib.Path(temporary) / "existing"
            existing.mkdir()
            result = self.run_generator(
                "-ExportCandidate",
                "-ChangeId",
                "20990105T010203Z",
                "-OutputDirectory",
                str(existing),
            )
            self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()

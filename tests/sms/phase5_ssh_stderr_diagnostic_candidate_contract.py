import hashlib
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "prepare-sms-phase5-ssh-stderr-diagnostic.ps1"
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")


class SSHStderrDiagnosticCandidateContract(unittest.TestCase):
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

    def test_default_closed_and_self_test(self):
        closed = self.run_script()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("ssh_stderr_diagnostic_candidate_authorized=false", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        self.assertIn("real_sms_sent=0", closed.stdout)

        checked = self.run_script("-SelfTest")
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertIn("ssh_stderr_diagnostic_candidate_self_test=passed", checked.stdout)
        self.assertIn("diagnostic_modes=base-transport,isolated-bash,isolated-bash-stdin", checked.stdout)
        self.assertIn("single_execution_lock_required=true", checked.stdout)

    def test_export_builds_hash_bound_single_execution_runner(self):
        with tempfile.TemporaryDirectory() as temp:
            output = pathlib.Path(temp) / "candidate"
            change_id = "20990101T010203Z"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", change_id,
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("ssh_stderr_diagnostic_candidate=passed", completed.stdout)
            self.assertIn("diagnostic_mode=base-transport", completed.stdout)
            self.assertIn("network_connections=0", completed.stdout)
            runner = output / f"run-sms-phase5-ssh-stderr-diagnostic-{change_id}.ps1"
            text = runner.read_text(encoding="utf-8-sig")

            for marker in (
                "/usr/bin/true",
                "[IO.FileMode]::CreateNew",
                "(Test-Path -LiteralPath $ExecutionLockPath) -or (Test-Path -LiteralPath $ResultPath)",
                "本 ChangeId 已执行或已有结果，禁止重试",
                "remote_stderr_redacted=",
                "raw_stderr_persisted=false",
                "business_reads=0",
                "configuration_mutations=0",
                "service_operations=0",
                "business_posts=0",
                "emails_sent=0",
                "real_sms_sent=0",
            ):
                self.assertIn(marker, text)
            for forbidden in (
                "curl ", "Invoke-WebRequest", "Invoke-RestMethod", "scp ", "sftp ",
                "SMS_ENABLED=true", "systemctl", "docker restart",
            ):
                self.assertNotIn(forbidden, text)
            self.assertNotIn("Write-Output $stderr", text)
            self.assertNotIn("WriteAllText($ResultPath, $stderr", text)

            digest = hashlib.sha256(runner.read_bytes()).hexdigest()
            self.assertIn(f"runner_sha256={digest}", completed.stdout)
            self_test = subprocess.run(
                [POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-SelfTest"],
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                timeout=30,
            )
            self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
            self.assertIn("ssh_stderr_diagnostic_self_test=passed", self_test.stdout)
            self.assertFalse((output / f"execution-{change_id}.lock").exists())
            self.assertFalse((output / f"result-{change_id}.txt").exists())

            # 预置执行锁后必须在任何 SSH 身份读取和网络动作前失败，证明一次性门禁可真实触发。
            (output / f"execution-{change_id}.lock").write_bytes(b"")
            replay = subprocess.run(
                [
                    POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner),
                    "-ExecuteDiagnostic", "-ExpectedRunnerSHA256", digest,
                ],
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                timeout=30,
            )
            self.assertNotEqual(replay.returncode, 0)
            self.assertIn("禁止重试", replay.stdout + replay.stderr)
            self.assertFalse((output / f"result-{change_id}.txt").exists())

    def test_export_isolated_bash_minimal_difference(self):
        with tempfile.TemporaryDirectory() as temp:
            output = pathlib.Path(temp) / "candidate"
            change_id = "20990101T040506Z"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", change_id,
                "-DiagnosticMode", "isolated-bash",
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("diagnostic_mode=isolated-bash", completed.stdout)
            runner = output / f"run-sms-phase5-ssh-stderr-diagnostic-{change_id}.ps1"
            text = runner.read_text(encoding="utf-8-sig")
            self.assertIn('$DiagnosticMode = "isolated-bash"', text)
            self.assertIn(
                "/usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc "
                "LOGNAME=pc LANG=C LC_ALL=C /bin/bash --noprofile --norc -c /usr/bin/true",
                text,
            )
            self.assertNotIn("START TRANSACTION", text)
            self.assertNotIn("/api/", text)
            self.assertNotIn("SMS_ENABLED", text)

    def test_export_isolated_bash_stdin_uses_only_true_line(self):
        with tempfile.TemporaryDirectory() as temp:
            output = pathlib.Path(temp) / "candidate"
            change_id = "20990101T050607Z"
            completed = self.run_script(
                "-ExportCandidate",
                "-ChangeId", change_id,
                "-DiagnosticMode", "isolated-bash-stdin",
                "-OutputDirectory", str(output),
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertIn("diagnostic_mode=isolated-bash-stdin", completed.stdout)
            runner = output / f"run-sms-phase5-ssh-stderr-diagnostic-{change_id}.ps1"
            text = runner.read_text(encoding="utf-8-sig")
            self.assertIn('$DiagnosticMode = "isolated-bash-stdin"', text)
            self.assertIn("/bin/bash --noprofile --norc -s --", text)
            self.assertIn('$RemoteInputBase64 = "dHJ1ZQo="', text)
            self.assertIn("StandardInput.BaseStream.Write", text)
            self.assertNotIn("START TRANSACTION", text)
            self.assertNotIn("/api/", text)
            self.assertNotIn("SMS_ENABLED", text)

    def test_rejects_existing_or_nonlocal_output(self):
        with tempfile.TemporaryDirectory() as temp:
            existing = pathlib.Path(temp) / "existing"
            existing.mkdir()
            duplicate = self.run_script(
                "-ExportCandidate", "-ChangeId", "20990101T020304Z", "-OutputDirectory", str(existing)
            )
            self.assertNotEqual(duplicate.returncode, 0)
            self.assertIn("候选输出目录已存在", duplicate.stdout + duplicate.stderr)

        unc = self.run_script(
            "-ExportCandidate", "-ChangeId", "20990101T030405Z", "-OutputDirectory", "\\\\server\\share\\candidate"
        )
        self.assertNotEqual(unc.returncode, 0)
        self.assertIn("本地文件系统绝对路径", unc.stdout + unc.stderr)


if __name__ == "__main__":
    unittest.main()

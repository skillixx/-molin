import base64
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
GENERATOR = ROOT / "scripts" / "prepare-sms-phase5-canary-send-candidate.ps1"


class Phase5CanarySendCandidateContract(unittest.TestCase):
    """验证五场景真实收件候选在未获执行授权时保持完全关闭。"""

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

    @staticmethod
    def write_plan(path: pathlib.Path, change_id: str) -> str:
        plan = {
            "change_id": change_id,
            "environment": "test",
            "sms_test_mode": True,
            "restore_sms_enabled": "false",
            "no_retries": True,
            "requested_sends": 5,
            "max_sends": 5,
            "same_target_min_interval_seconds": 65,
            "scheduled_waits": 2,
            "acceptance_scope": "receipt_only",
            "business_state_changes": False,
            "business_state_rollback_approved": False,
            "disposable_accounts": False,
            "scenes": [
                {"scene": "register", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "login", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "reset_password", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "bind_phone", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "admin_verify", "target_alias": "target-admin", "target_state": "registered_admin"},
            ],
        }
        path.write_text(json.dumps(plan, ensure_ascii=False), encoding="utf-8")
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def test_default_and_self_test_are_offline(self) -> None:
        closed = self.run_generator()
        self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
        self.assertIn("canary_send_candidate_authorized=false", closed.stdout)
        self.assertIn("interactive_prompts=0", closed.stdout)
        self.assertIn("network_connections=0", closed.stdout)
        self.assertIn("configuration_mutations=0", closed.stdout)
        self.assertIn("service_restarts=0", closed.stdout)
        self.assertIn("real_sms_sent=0", closed.stdout)

        self_test = self.run_generator("-SelfTest")
        self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
        self.assertIn("canary_send_candidate_self_test=passed", self_test.stdout)
        self.assertIn("five_scene_contract_verified=true", self_test.stdout)
        self.assertIn("network_connections=0", self_test.stdout)
        self.assertIn("real_sms_sent=0", self_test.stdout)

    def test_export_freezes_five_sends_and_automatic_closed_state_restore(self) -> None:
        change_id = "20990106T010203Z"
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            root = pathlib.Path(temporary)
            plan = root / "plan.json"
            plan_sha = self.write_plan(plan, change_id)
            output = root / "candidate"
            result = self.run_generator(
                "-ExportCandidate", "-ChangeId", change_id,
                "-PlanFile", str(plan), "-ExpectedPlanSHA256", plan_sha,
                "-OutputDirectory", str(output),
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("canary_send_candidate=passed", result.stdout)
            self.assertIn("candidate_files_written=1", result.stdout)
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
                "请输入管理员 Bearer Token（隐藏输入）",
                "StrictHostKeyChecking=yes",
                "HostKeyAlgorithms=ssh-ed25519",
                "StandardInput.BaseStream.Write",
                "[Array]::Clear($inputBytes",
                "$ExpectedSSHHelperSHA256",
                "固定 SSH 身份辅助脚本摘要不匹配",
                "rollback_armed=true",
                "restore_closed_state",
                "requested_sends=5",
                "baseline_send_log_id",
                "baseline_verification_code_id",
                "baseline_send_total",
                "baseline_provider_calls_total",
                "canary_completed_at",
                "automatic_retries=0",
                "same_target_min_interval_seconds=65",
                "scheduled_waits=2",
                "wait_same_target_interval",
            ):
                self.assertIn(marker, runner_text + payload)
            self.assertIn("replace_sms_enabled \"$env_file\" \"$enabled_env\" '" + "tr" + "ue'", payload)
            for scene in ("register", "login", "reset_password", "bind_phone", "admin_verify"):
                self.assertEqual(payload.count(f"send_scene {scene} "), 1)
            self.assertEqual(payload.count("wait_same_target_interval || fail"), 2)
            self.assertIn("for index in $(seq 1 65)", payload)
            self.assertIn("completed_pacing_waits", runner_text)
            self.assertIn("Authorization: Bearer", payload)
            self.assertIn("--data-binary @-", payload)
            self.assertNotIn('--data-binary "$body"', payload)
            self.assertIn("/api/me/verification-codes/phone", payload)
            self.assertIn("/api/admin/auth/verification-codes/phone", payload)
            self.assertIn("verify_target_and_token_state", payload)
            self.assertIn("http://127.0.0.1:8080/api/me", payload)
            self.assertIn("u.id=${admin_id}", payload)
            self.assertIn("u.phone=CONVERT(0x${admin_hex} USING utf8mb4)", payload)
            self.assertIn("r.code='admin'", payload)
            self.assertIn("p.code='user:manage'", payload)
            self.assertLess(payload.index("verify_target_and_token_state || fail"), payload.index("rollback_armed=true", payload.index("replace_process_sms_enabled")))
            self.assertLess(payload.index("verify_target_and_token_state || fail"), payload.index("send_scene register"))
            self.assertIn("trap 'handle_exit $?' EXIT", payload)
            self.assertIn("verify_alertmanager_discard", payload)
            self.assertIn(
                "alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'",
                payload,
            )
            self.assertNotIn("/home/pc/molin/infra/alertmanager/alertmanager.yml", payload)
            self.assertIn("alertmanager_container='molin-alertmanager-phase5-closed'", payload)
            self.assertIn("http://127.0.0.1:${alertmanager_port}/-/ready", payload)
            self.assertIn("docker inspect \"$alertmanager_container\"", payload)
            self.assertLess(
                payload.index("verify_alertmanager_discard || fail alertmanager_discard"),
                payload.index("verify_target_and_token_state || fail"),
            )
            self.assertIn("lock_acquired=false", payload)
            self.assertIn("lock_acquired=true", payload)
            self.assertIn('if [ "$lock_acquired" = true ]; then rmdir -- "$lock_dir"', payload)
            self.assertIn("recovery_materials_retained=true", payload)
            self.assertIn("lock_retained=true", payload)
            self.assertIn('if [ "$recovery_failed" = false ]; then', payload)
            self.assertIn('replace_process_sms_enabled "$original_env_snapshot"', payload)
            self.assertIn('sha256sum "/proc/${pid}/exe"', payload)
            self.assertIn('stat -c \'%U:%a\' "$env_file"', payload)
            self.assertIn('Write-Output "remote_stderr_present=$stderrPresent"', runner_text)
            self.assertIn('$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath)', runner_text)
            self.assertIn('if (Test-Path -LiteralPath $ResultPath)', runner_text)
            self.assertIn("[IO.FileMode]::CreateNew", runner_text)
            self.assertIn("low_sensitivity_result_persisted=true", runner_text)
            self.assertIn("result_sha256=", runner_text)
            self.assertIn('"baseline_send_log_id", "baseline_verification_code_id"', runner_text)
            self.assertIn('"baseline_provider_calls_total", "baseline_provider_nonaccepted_total", "canary_completed_at"', runner_text)
            self.assertIn("$safeKeys = @(", runner_text)
            self.assertIn("$safeKeys -ccontains $Matches['key']", runner_text)
            self.assertIn("^(?<key>[a-z][a-z0-9_]*)=[A-Za-z0-9_.:,-]+$", runner_text)
            self.assertIn('"scene_admin_verify_submitted"', runner_text)
            self.assertNotIn('"phone"', runner_text)
            self.assertNotIn('"token"', runner_text)
            self.assertNotIn('"otp"', runner_text)
            self.assertNotIn('$safeLines += $stderr', runner_text)
            self.assertNotRegex(runner_text, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(payload, r"(?<!\d)1[3-9]\d{9}(?!\d)")
            self.assertNotRegex(runner_text, r"(?i)\b(?:scp|sftp|wget)\b")

            if self.bash is not None:
                syntax = subprocess.run(
                    [self.bash, "-n"], input=payload, text=True, capture_output=True,
                    encoding="utf-8", errors="replace", check=False,
                )
                self.assertEqual(syntax.returncode, 0, syntax.stdout + syntax.stderr)
                payload_self_test = subprocess.run(
                    [self.bash, "-s", "--", "--self-test"], input=payload, text=True,
                    capture_output=True, encoding="utf-8", errors="replace", check=False,
                )
                self.assertEqual(payload_self_test.returncode, 0, payload_self_test.stdout + payload_self_test.stderr)
                self.assertIn("canary_send_payload_self_test=passed", payload_self_test.stdout)
                self.assertIn("network_connections=0", payload_self_test.stdout)
                self.assertIn("real_sms_sent=0", payload_self_test.stdout)

            closed = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner)],
                cwd=ROOT, text=True, capture_output=True, encoding="utf-8", errors="replace", check=False,
            )
            self.assertEqual(closed.returncode, 0, closed.stdout + closed.stderr)
            self.assertIn("canary_send_execution_authorized=false", closed.stdout)
            self.assertIn("interactive_prompts=0", closed.stdout)
            self.assertIn("network_connections=0", closed.stdout)
            self.assertIn("real_sms_sent=0", closed.stdout)

            self_test = subprocess.run(
                [self.powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-SelfTest"],
                cwd=ROOT, text=True, capture_output=True, encoding="utf-8", errors="replace", check=False,
            )
            self.assertEqual(self_test.returncode, 0, self_test.stdout + self_test.stderr)
            self.assertIn("canary_send_runner_self_test=passed", self_test.stdout)
            self.assertIn("network_connections=0", self_test.stdout)
            self.assertIn("real_sms_sent=0", self_test.stdout)
            self.assertEqual(len(list(output.iterdir())), 1, "默认关闭与 SelfTest 不得生成结果文件")

    def test_candidate_is_wired_into_release_gates_and_docs(self) -> None:
        source = GENERATOR.read_text(encoding="utf-8-sig")
        readiness = (ROOT / "scripts" / "verify-sms-phase5-readiness.ps1").read_text(encoding="utf-8-sig")
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
        design = (ROOT / "docs" / "sms-phase5-canary-execution-design.md").read_text(encoding="utf-8")
        tools = (ROOT / "docs" / "tools.md").read_text(encoding="utf-8")
        self.assertNotIn("$isWindows =", source)
        self.assertIn("$isWindowsPlatform", source)
        for text in (readiness, design, tools):
            self.assertIn("prepare-sms-phase5-canary-send-candidate.ps1", text)
        self.assertIn("phase5_canary_send_candidate_contract.py", ci)


if __name__ == "__main__":
    unittest.main()

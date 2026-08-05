#!/usr/bin/env python3
"""阶段 5 敏感信息与短信关闭态发布门禁契约。"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
GATE = ROOT / "scripts" / "verify-sms-phase5-sensitive-data.py"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class Phase5SensitiveDataGateContractTest(unittest.TestCase):
    """通过公开命令行验证门禁结果，避免绑定脚本内部实现。"""

    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repo = Path(self.temporary_directory.name)
        self.run_git("init", "--quiet")
        self.run_git("config", "user.name", "阶段5契约测试")
        self.run_git("config", "user.email", "phase5@example.invalid")
        (self.repo / "README.md").write_text("阶段 5 基线\n", encoding="utf-8")
        self.run_git("add", "README.md")
        self.run_git("commit", "--quiet", "-m", "建立测试基线")
        self.run_git("tag", "phase5-base")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def run_git(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["git", *arguments],
            cwd=self.repo,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

    def commit_file(self, relative_path: str, content: str) -> None:
        target = self.repo / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        self.run_git("add", relative_path)
        self.run_git("commit", "--quiet", "-m", "更新测试文件")

    def run_gate(self, *extra_arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(GATE),
                "--repo-root",
                str(self.repo),
                "--base-ref",
                "phase5-base",
                *extra_arguments,
            ],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

    def test_safe_closed_state_change_passes(self) -> None:
        self.commit_file(
            "infra/.env.example",
            "SMS_ENABLED=false\nSMS_TEST_MODE=true\nSMS_ALIYUN_ACCESS_KEY_ID=replace_me\n",
        )

        result = self.run_gate()

        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn("phase5_sensitive_scan=passed", result.stdout)
        self.assertIn("sms_enable_literals=0", result.stdout)
        self.assertIn("real_sms_sent=0", result.stdout)

    def test_enabled_sms_change_is_rejected_without_echoing_source(self) -> None:
        source = "SMS_ENABLED=true"
        self.commit_file("infra/release.env.example", source + "\nSMS_TEST_MODE=true\n")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("phase5_sensitive_scan=failed", result.stdout)
        self.assertIn("category=sms_enabled_true", result.stdout)
        self.assertNotIn(source, result.stdout)

    def test_forbidden_pattern_literal_does_not_count_as_enabling_sms(self) -> None:
        self.commit_file(
            "scripts/read-only-check.ps1",
            "$forbiddenPatterns = @(\n    'SMS_ENABLED=true'\n)\n",
        )

        result = self.run_gate()

        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn("sms_enable_literals=0", result.stdout)

    def test_uncommitted_worktree_change_is_also_rejected(self) -> None:
        target = self.repo / "infra" / "pending.env.example"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("SMS_TEST_MODE=false\n", encoding="utf-8")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=sms_test_mode_false", result.stdout)

    def test_secret_added_then_deleted_is_still_rejected_from_branch_history(self) -> None:
        secret = "LTAI" + "9" * 16
        self.commit_file("temporary-secret.log", f"access_key_id={secret}\n")
        (self.repo / "temporary-secret.log").unlink()
        self.run_git("add", "-u")
        self.run_git("commit", "--quiet", "-m", "删除临时文件")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=access_key_or_secret_value", result.stdout)
        self.assertIn("source_ref=blob:", result.stdout)
        self.assertNotIn(secret, result.stdout)

    def test_deleted_protected_env_is_rejected_even_when_blob_already_existed(self) -> None:
        # 内容与基线 README 完全相同，验证门禁依赖历史路径而非“新 blob”巧合。
        self.commit_file("infra/.env", "阶段 5 基线\n")
        (self.repo / "infra" / ".env").unlink()
        self.run_git("add", "-u")
        self.run_git("commit", "--quiet", "-m", "删除受保护环境文件")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=historical_protected_env", result.stdout)
        self.assertIn("source_ref=commit:", result.stdout)

    def test_non_placeholder_phone_and_otp_in_document_are_rejected(self) -> None:
        phone = "137" + "1357" + "2468"
        otp = "135" + "790"
        self.commit_file("docs/evidence.md", f"phone={phone}\ncode={otp}\n")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=static_phone_literal", result.stdout)
        self.assertIn("category=synthetic_test_otp", result.stdout)
        self.assertNotIn(phone, result.stdout)
        self.assertNotIn(otp, result.stdout)

    def test_common_configuration_forms_cannot_bypass_closed_state(self) -> None:
        self.commit_file(
            "infra/runtime-config.txt",
            "{\"SMS_ENABLED\": true},\n"
            "environment: { SMS_ENABLED: true }\n"
            "$env:SMS_ENABLED = $true\n"
            "SMS_ENABLED=true command\n"
            "{\"SMS_TEST_MODE\": false}\n",
        )

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=sms_enabled_true", result.stdout)
        self.assertIn("category=sms_test_mode_false", result.stdout)
        self.assertIn("sms_enable_literals=4", result.stdout)

    def test_required_dist_artifacts_fail_closed_when_missing(self) -> None:
        self.commit_file("infra/.env.example", "SMS_ENABLED=false\nSMS_TEST_MODE=true\n")

        result = self.run_gate("--require-dist")

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=missing_required_dist", result.stdout)

    def test_tracked_protected_environment_file_is_rejected_without_reading_value(self) -> None:
        secret = "never-print-this-environment-value"
        self.commit_file("infra/.env", f"SMS_ALIYUN_ACCESS_KEY_SECRET={secret}\n")

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=tracked_protected_env", result.stdout)
        self.assertNotIn(secret, result.stdout)

    def test_sensitive_artifact_is_rejected_without_echoing_values(self) -> None:
        access_key = "LTAI" + "7" * 16
        phone = "137" + "2468" + "1357"
        otp = "246" + "810"
        self.commit_file(
            "artifacts/canary.log",
            f"access_key_id={access_key}\nphone={phone}\ncode={otp}\n",
        )

        result = self.run_gate()

        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("category=access_key_or_secret_value", result.stdout)
        self.assertIn("category=unmasked_phone_artifact", result.stdout)
        self.assertIn("category=otp_value", result.stdout)
        self.assertNotIn(access_key, result.stdout)
        self.assertNotIn(phone, result.stdout)
        self.assertNotIn(otp, result.stdout)

    def test_gate_is_wired_into_readiness_and_ci(self) -> None:
        readiness = READINESS.read_text(encoding="utf-8")
        ci = CI.read_text(encoding="utf-8")
        phase5_job = ci.split("phase5-release-safety:", 1)[1].split("\n  gateway-g3:", 1)[0]

        self.assertIn("verify-sms-phase5-sensitive-data.py", readiness)
        self.assertIn("phase5_sensitive_scan=passed", readiness)
        self.assertIn("fetch-depth: 0", phase5_job)
        self.assertIn("actions/setup-node@v4", phase5_job)
        self.assertIn("npm ci", phase5_job)
        self.assertGreaterEqual(phase5_job.count("npm run build"), 2)
        self.assertIn("phase5_sensitive_data_gate_contract.py", phase5_job)
        self.assertIn("-RunSensitiveScan", phase5_job)
        self.assertIn("--require-dist", readiness)


if __name__ == "__main__":
    unittest.main(verbosity=2)

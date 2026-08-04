#!/usr/bin/env python3
"""阶段 5 测试服回滚候选配置生成器的静态安全契约。"""

from __future__ import annotations

import re
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "prepare-sms-phase5-test-server-rollback-candidate.ps1"
PAYLOAD = ROOT / "scripts" / "prepare-sms-phase5-test-server-rollback-candidate.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class RollbackCandidateContractTest(unittest.TestCase):
    """确保默认执行不会连接远端，真实写入必须经过双重人工门禁。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig")
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")

    def test_wrapper_defaults_to_offline_and_requires_exact_execution_approval(self) -> None:
        self.assertIn("[switch]$SelfTest", self.ps)
        self.assertIn("[switch]$Execute", self.ps)
        self.assertIn("我已批准生成阶段5测试服回滚候选配置", self.ps)
        self.assertIn("必须显式使用 -SelfTest 或 -Execute", self.ps)
        self.assertIn("BatchMode=yes", self.ps)
        self.assertIn("StrictHostKeyChecking=yes", self.ps)
        self.assertIn('$ServerHost -cne "8.130.9.163"', self.ps)
        self.assertIn('$SSHUser -cne "pc"', self.ps)
        self.assertIn("$SSHPort -ne 10003", self.ps)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", self.ps)
        self.assertIn("UserKnownHostsFile=", self.ps)
        self.assertIn("SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I", self.ps)
        self.assertIn("remote_connections=0", self.ps)
        self.assertIn("remote_files_written=0", self.ps)

    def test_payload_only_builds_a_closed_candidate_at_the_fixed_target(self) -> None:
        for marker in (
            "__CURRENT_ENVIRONMENT_PATH__",
            "__CANDIDATE_PATH__",
            "__CANDIDATE_ROOT__",
            "SMS_ENABLED=false",
            "SMS_TEST_MODE=true",
            "TRUSTED_PROXY_IPS",
            "SMS_TEMPLATE_CODE_",
            "candidate_environment_created=true",
            "candidate_mode=600",
            "real_sms_sent=0",
        ):
            self.assertIn(marker, self.sh)
        self.assertIn("umask 077", self.sh)
        self.assertIn("os.open", self.sh)
        self.assertIn("O_EXCL", self.sh)
        self.assertIn("O_DIRECTORY", self.sh)
        self.assertIn("dir_fd", self.sh)
        self.assertIn("follow_symlinks=False", self.sh)
        self.assertIn('trusted_items == {"172.20.250.0/28"}', self.sh)
        self.assertNotIn('{"172.20.250.2", "172.20.250.3"}.issubset(trusted_items)', self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)
        self.assertNotRegex(self.sh, r"(?m)^\s*(?:source|\.)\s+")
        self.assertNotRegex(self.sh, r"(?m)^\s*(?:systemctl|docker|curl|wget|mysql|redis-cli)\b")

    def test_payload_never_prints_environment_values(self) -> None:
        self.assertNotIn("cat ", self.sh)
        self.assertNotIn("env |", self.sh)
        self.assertNotIn("printenv", self.sh)
        self.assertNotRegex(self.sh, r"printf[^\n]*(?:MYSQL_PASSWORD|JWT_SECRET|ACCESS_KEY|TOKEN)")
        self.assertIn("candidate_sensitive_values_printed=0", self.sh)

    def test_embedded_builder_creates_a_closed_exclusive_candidate(self) -> None:
        match = re.search(r"<<'PY'[^\n]*\n(.*?)\nPY\n", self.sh, flags=re.DOTALL)
        self.assertIsNotNone(match)
        assert match is not None
        with tempfile.TemporaryDirectory() as temporary:
            temporary_path = Path(temporary)
            root = temporary_path / "rollback"
            source = temporary_path / ".env.test"
            candidate = root / "candidate-20260804T150000Z.env"
            source.write_text(
                "\n".join(
                    (
                        "APP_ENV=test",
                        "API_HOST=127.0.0.1",
                        "API_PORT=8080",
                        "MYSQL_HOST=127.0.0.1",
                        "MYSQL_PORT=3306",
                        "MYSQL_DATABASE=molin",
                        "MYSQL_USER=molin",
                        "MYSQL_PASSWORD=<测试占位值>",
                        "REDIS_ADDR=127.0.0.1:6379",
                        "JWT_SECRET=<测试占位值>",
                        "REFRESH_TOKEN_SECRET=<测试占位值>",
                        "TRUSTED_PROXY_IPS=172.20.250.0/28",
                        "SMS_ENABLED=true",
                        "SMS_TEST_MODE=false",
                        "SMS_TEMPLATE_CODE_REGISTER=<废弃占位值>",
                        "",
                    )
                ),
                encoding="utf-8",
            )
            root_text = root.as_posix()
            code = match.group(1).replace("__CANDIDATE_ROOT__", root_text)
            first = subprocess.run(
                [sys.executable, "-c", code, source.as_posix(), root.as_posix(), candidate.as_posix()],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(first.returncode, 0, first.stderr)
            generated = candidate.read_text(encoding="utf-8")
            self.assertIn("SMS_ENABLED=false", generated)
            self.assertIn("SMS_TEST_MODE=true", generated)
            self.assertIn("TRUSTED_PROXY_IPS=172.20.250.0/28", generated)
            self.assertNotIn("SMS_TEMPLATE_CODE_", generated)
            self.assertNotIn("<测试占位值>", first.stdout + first.stderr)
            if sys.platform != "win32":
                self.assertEqual(stat.S_IMODE(candidate.stat().st_mode), 0o600)

            second = subprocess.run(
                [sys.executable, "-c", code, source.as_posix(), root.as_posix(), candidate.as_posix()],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(second.returncode, 0)

            invalid_source = temporary_path / ".env.invalid"
            invalid_source.write_text(
                generated.replace("TRUSTED_PROXY_IPS=172.20.250.0/28", "TRUSTED_PROXY_IPS=0.0.0.0/0"),
                encoding="utf-8",
            )
            invalid_candidate = root / "candidate-20260804T150001Z.env"
            invalid = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    code,
                    invalid_source.as_posix(),
                    root.as_posix(),
                    invalid_candidate.as_posix(),
                ],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(invalid.returncode, 0)
            self.assertFalse(invalid_candidate.exists())

            mixed_source = temporary_path / ".env.mixed"
            mixed_source.write_text(
                generated.replace(
                    "TRUSTED_PROXY_IPS=172.20.250.0/28",
                    "TRUSTED_PROXY_IPS=172.20.250.0/28,0.0.0.0/0",
                ),
                encoding="utf-8",
            )
            mixed_candidate = root / "candidate-20260804T150002Z.env"
            mixed = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    code,
                    mixed_source.as_posix(),
                    root.as_posix(),
                    mixed_candidate.as_posix(),
                ],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(mixed.returncode, 0)
            self.assertFalse(mixed_candidate.exists())

    def test_assets_are_part_of_readiness_and_ci(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn("phase5_rollback_candidate_contract.py", self.ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)


if __name__ == "__main__":
    unittest.main()

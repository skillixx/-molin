#!/usr/bin/env python3
"""验证 011 人工交互安装命令只携带冻结脚本且不处理密码。"""

import base64
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-011-command.py")
INSTALLER_PATH = Path(__file__).with_name("g8-test-readonly-access-install-011.sh")
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"


class TestPrepareG8ReadonlyAccess011Command(unittest.TestCase):
    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), *arguments],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )

    def test_generates_new_absolute_output_only(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            result = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines()[0], "G8_TEST_READONLY_ACCESS_011_COMMAND=PASS")
            self.assertTrue(output.is_file())
            replay = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(replay.returncode, 2)

    def test_command_has_one_interactive_authentication_and_only_noninteractive_followups(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        self.assertEqual(command.count("sudo -k -v"), 1)
        self.assertEqual(command.count("sudo -n /bin/bash -ceu"), 1)
        self.assertEqual(command.count("sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test"), 1)
        self.assertIn("G8_011_INSTALL_B64", command)
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("IdentitiesOnly=yes", command)
        self.assertIn("UserKnownHostsFile=", command)
        self.assertIn("pc@8.130.9.163", command)
        self.assertIn("-p 10003", command)

    def test_password_input_path_uses_only_frozen_executables_and_identity_materials(self) -> None:
        """sudo 密码输入前不得信任可变环境路径、主机密钥策略或远端 PATH。"""
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        self.assertIn(r'& "C:\Windows\System32\OpenSSH\ssh.exe"', command)
        self.assertIn(r'UserKnownHostsFile="C:\Users\skillixx\.ssh\known_hosts"', command)
        self.assertIn(r'-i "C:\Users\skillixx\.ssh\id_ed25519"', command)
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn(r'C:\Windows\System32\OpenSSH\ssh-keygen.exe', command)
        self.assertIn("SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I", command)
        self.assertIn("identity_pair_mismatch", command)
        self.assertNotIn("$env:SystemRoot", command)
        self.assertNotIn("$env:USERPROFILE", command)
        self.assertNotIn("\nsudo -k -v", command)
        self.assertNotIn("\nsudo -n ", command)
        self.assertEqual(command.count("/usr/bin/sudo -k -v"), 1)
        self.assertEqual(command.count("/usr/bin/sudo -n /bin/bash -ceu"), 1)

    def test_installer_bytes_are_stable_and_frozen_before_embedding(self) -> None:
        """命令生成器不得把未冻结或读取期间漂移的安装器嵌入命令。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn("EXPECTED_INSTALLER_SHA256", source)
        self.assertIn("EXPECTED_INSTALLER_SIZE", source)
        self.assertIn("stat.S_ISREG", source)
        self.assertIn("installer_before.st_ino", source)
        self.assertIn("sha256_bytes(installer) != EXPECTED_INSTALLER_SHA256", source)

    def test_nonprivileged_preflight_freezes_staging_files_and_parent_chain_before_sudo(self) -> None:
        """进入密码提示前必须完成暂存、五文件、摘要和父链的完整只读门禁。"""
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        preflight = command[: command.index("/usr/bin/sudo -k -v")]
        for frozen in (
            "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
            "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
            "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
            "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
            "13066129",
            "SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers",
            "/usr/bin/sha256sum -c SHA256SUMS",
            "/usr /usr/local /usr/local/libexec /etc /etc/sudoers.d",
            "PREFLIGHT_011=PASS",
        ):
            self.assertIn(frozen, preflight)

    def test_consumed_gate_precedes_installer_read(self) -> None:
        """未来消费011后，命令生成入口必须在读取安装器前固定拒绝。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn("CHANGE_ID_CONSUMED = False", source)
        self.assertLess(source.index("if CHANGE_ID_CONSUMED:"), source.index("installer_path.read_bytes()"))
        with tempfile.TemporaryDirectory(prefix="g8-011-consumed-command-") as temporary:
            script = Path(temporary) / SCRIPT_PATH.name
            script.write_text(source.replace("CHANGE_ID_CONSUMED = False", "CHANGE_ID_CONSUMED = True"), encoding="utf-8")
            output = Path(temporary) / "command.txt"
            result = subprocess.run(
                ["python", "-I", str(script), f"--change-id={CHANGE_ID}", f"--output-file={output}"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
        self.assertEqual(result.returncode, 2)
        self.assertEqual(
            result.stdout.strip(),
            "G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=change_id_consumed",
        )
        self.assertFalse(output.exists())

    def test_embedded_installer_is_exact_and_secrets_are_absent(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-011-command-") as temporary:
            output = Path(temporary) / "command.txt"
            self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            command = output.read_text(encoding="utf-8")
        encoded = command.split("G8_011_INSTALL_B64'\n", 1)[1].split("\nG8_011_INSTALL_B64", 1)[0]
        self.assertEqual(base64.b64decode(encoded), INSTALLER_PATH.read_bytes())
        for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
            self.assertNotIn(forbidden, command)

    def test_invalid_request_fails_closed(self) -> None:
        for arguments in ((), ("--change-id=bad", "--output-file=relative")):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(
                result.stdout.strip(),
                "G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=invalid_request",
            )


if __name__ == "__main__":
    unittest.main()

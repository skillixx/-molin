#!/usr/bin/env python3
"""验证 016 命令生成器只生成冻结人工步骤，不读取密码或连接网络。"""

import ast
import base64
import importlib.util
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-016-command.py")
INSTALLER_PATH = Path(__file__).with_name("g8-test-readonly-access-install-016.sh")
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016"
SOURCE_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"


def bash_executable() -> str:
    """Windows 使用 Git Bash；Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


def load_module():
    """从固定路径加载生成器，仅调用离线纯函数。"""
    specification = importlib.util.spec_from_file_location("g8_command_016", SCRIPT_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestPrepareG8ReadonlyAccess016Command(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module()
        self.installer = INSTALLER_PATH.read_bytes()
        self.command = self.module.build_command(self.installer)

    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), *arguments],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )

    def test_self_test_is_local_and_exact(self) -> None:
        result = self.run_script("--self-test")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_016_COMMAND_SELF_TEST=PASS")
        tree = ast.parse(SCRIPT_PATH.read_text(encoding="utf-8"))
        imports = {
            alias.name
            for node in ast.walk(tree)
            if isinstance(node, ast.Import)
            for alias in node.names
        }
        self.assertNotIn("subprocess", imports)
        self.assertNotIn("socket", imports)

    def test_formal_generation_creates_one_new_low_sensitive_file(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-016-command-") as temporary:
            output = Path(temporary).resolve() / "install-command.txt"
            result = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(output.is_file())
            generated = output.read_text(encoding="utf-8")
            self.assertEqual(generated, self.command)
            self.assertIn("G8_TEST_READONLY_ACCESS_016_COMMAND=PASS", result.stdout)
            self.assertIn("root_installer_sha256=", result.stdout)
            self.assertIn("command_sha256=", result.stdout)
            duplicate = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(duplicate.returncode, 2)
            self.assertEqual(duplicate.stdout.strip(), "G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=invalid_request")

    def test_command_separates_authorization_from_frozen_011_source(self) -> None:
        self.assertIn(CHANGE_ID, self.command)
        self.assertIn(SOURCE_CHANGE_ID, self.command)
        self.assertIn("auth_change_id=CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016", self.command)
        self.assertIn("source_change_id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.command)
        self.assertIn("REMOTE_AUTHORIZED", self.command)
        self.assertIn(".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.command)

    def test_windows_paths_come_from_trusted_api_and_ssh_is_single_attempt(self) -> None:
        command = self.command
        self.assertIn("GetFolderPath([Environment+SpecialFolder]::Windows)", command)
        self.assertIn("GetFolderPath([Environment+SpecialFolder]::CommonApplicationData)", command)
        self.assertIn("GetFolderPath([Environment+SpecialFolder]::UserProfile)", command)
        self.assertLess(command.index("$ErrorActionPreference = 'Stop'"), command.index("$windowsRoot ="))
        self.assertIn("$ErrorActionPreference = $g8PreviousErrorActionPreference", command)
        self.assertIn("[IO.Path]::GetFullPath($systemPath)", command)
        self.assertIn("[IO.FileAttributes]::ReparsePoint", command)
        self.assertIn("$env:SystemRoot = $windowsRoot", command)
        self.assertIn("$env:ProgramData = $programData", command)
        self.assertIn("BatchMode=yes", command)
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("NumberOfPasswordPrompts=0", command)
        self.assertIn("PasswordAuthentication=no", command)
        self.assertIn("KbdInteractiveAuthentication=no", command)
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("ClearAllForwardings=yes", command)
        self.assertEqual(command.count("pc@8.130.9.163"), 1)

    @unittest.skipUnless(os.name == "nt", "Windows API 环境漂移测试只在原生 Windows 门禁执行。")
    def test_trusted_windows_path_prefix_ignores_forged_environment(self) -> None:
        """执行生成命令的真实路径前缀，证明伪造环境不会改变三个系统 API 结果。"""
        prefix = self.command.split("$ssh =", 1)[0]
        probe = (
            "$env:SystemRoot='C:\\forged-root';"
            "$env:ProgramData='\\\\forged-host\\share';"
            "$env:USERPROFILE='C:\\forged-profile';\n"
            + prefix
            + "\nWrite-Output ('WINDOWS=' + $windowsRoot);"
            + "Write-Output ('DATA=' + $programData);"
            + "Write-Output ('PROFILE=' + $profileRoot);"
            + "} finally { $ErrorActionPreference = $g8PreviousErrorActionPreference }"
        )
        result = subprocess.run(
            [r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe", "-NoProfile", "-NonInteractive", "-Command", probe],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertNotIn("forged", result.stdout.lower())
        lines = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
        self.assertRegex(lines["WINDOWS"], r"^[A-Za-z]:\\Windows$")
        self.assertRegex(lines["DATA"], r"^[A-Za-z]:\\ProgramData$")
        self.assertRegex(lines["PROFILE"], r"^[A-Za-z]:\\")

    @unittest.skipUnless(os.name == "nt", "交互式 PowerShell 回归只在原生 Windows 门禁执行。")
    def test_trusted_windows_path_prefix_runs_from_standard_input(self) -> None:
        """按人工粘贴等价的标准输入方式执行前缀，防止命令行转义掩盖运行时错误。"""
        prefix = self.command.split("$ssh =", 1)[0]
        probe = (
            prefix
            + "\nWrite-Output 'G8_016_WINDOWS_PATH_GUARD=PASS'\n"
            + "} finally { $ErrorActionPreference = $g8PreviousErrorActionPreference }\n"
        )
        result = subprocess.run(
            [
                r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source))",
            ],
            input=probe,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertIn("G8_016_WINDOWS_PATH_GUARD=PASS", result.stdout)

    @unittest.skipUnless(os.name == "nt", "PowerShell 失败关闭回归只在原生 Windows 门禁执行。")
    def test_path_guard_error_cannot_reach_identity_or_ssh_stage(self) -> None:
        """破坏真实正则后必须在路径门禁终止，后续身份与 SSH 阶段哨兵不可到达。"""
        prefix = self.command.split("$ssh =", 1)[0]
        corrupted = prefix.replace("-notmatch '^[A-Za-z]:\\\\'", "-notmatch '^[A-Za-z]:\\'")
        self.assertNotEqual(corrupted, prefix)
        probe = (
            corrupted
            + "\nWrite-Output 'G8_016_IDENTITY_OR_SSH_STAGE_REACHED'\n"
            + "} finally { $ErrorActionPreference = $g8PreviousErrorActionPreference }\n"
        )
        result = subprocess.run(
            [
                r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source))",
            ],
            input=probe,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertNotEqual(result.stderr, "")
        self.assertNotIn("G8_016_IDENTITY_OR_SSH_STAGE_REACHED", result.stdout)

    def test_remote_block_has_one_manual_sudo_and_only_noninteractive_followups(self) -> None:
        command = self.command
        self.assertEqual(command.count("/usr/bin/sudo -k -v"), 1)
        self.assertEqual(command.count("/usr/bin/sudo -n /bin/bash -ceu"), 1)
        # 审计器入口验证已经移入安装器回滚事务，远端包装层不得在事务提交后再次执行。
        self.assertEqual(command.count("/usr/bin/sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test"), 0)
        self.assertIn("G8_TEST_READONLY_ACCESS_PREFLIGHT_016=PASS", command)
        self.assertIn("G8_TEST_READONLY_ACCESS_POSTCHECK_016=PASS", command)
        self.assertIn("G8_016_INSTALL_B64", command)
        self.assertEqual(command.count("G8_016_REMOTE"), 2)
        self.assertIn("/bin/bash -s <<'G8_016_REMOTE'", command)
        self.assertIn('exit "$g8_remote_status"', command)
        self.assertEqual(command.count("$'\\r' \"$staging/manifest.env\""), 3)

    def test_remote_here_doc_has_valid_bash_syntax(self) -> None:
        """对真正交给非交互 Bash 的正文做语法解析，不执行任何命令。"""
        remote = self.command.split("/bin/bash -s <<'G8_016_REMOTE'\n", 1)[1].split("\nG8_016_REMOTE\n", 1)[0]
        result = subprocess.run(
            [bash_executable(), "-n"],
            input=remote,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    @unittest.skipUnless(os.name == "nt", "PowerShell 语法解析只在原生 Windows 门禁执行。")
    def test_local_connection_block_has_valid_powershell_syntax(self) -> None:
        """只解析本地连接段为 ScriptBlock，不读取身份材料或启动 SSH。"""
        local = self.command.split("# 第二步：", 1)[0]
        parser = "$source=[Console]::In.ReadToEnd();[scriptblock]::Create($source)|Out-Null"
        result = subprocess.run(
            [r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe", "-NoProfile", "-NonInteractive", "-Command", parser],
            input=local,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_embedded_installer_is_exact_and_secrets_are_absent(self) -> None:
        encoded = self.command.split("G8_016_INSTALL_B64'\n", 1)[1].split("\nG8_016_INSTALL_B64", 1)[0]
        self.assertEqual(base64.b64decode(encoded), self.installer)
        for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
            self.assertNotIn(forbidden, self.command)

    def test_invalid_arguments_fail_closed_without_echo(self) -> None:
        for arguments in (
            (),
            ("--help",),
            ("--self-test", "--change-id=DO_NOT_ECHO_SECRET_SENTINEL"),
            ("--change-id=bad", "--output-file=relative"),
            ("--unknown=DO_NOT_ECHO_SECRET_SENTINEL",),
        ):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=invalid_request")
            self.assertEqual(result.stderr, "")
            self.assertNotIn("DO_NOT_ECHO_SECRET_SENTINEL", result.stdout + result.stderr)

    def test_future_consumed_gate_precedes_installer_read_and_argument_parse(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertLess(source.index("if CHANGE_ID_CONSUMED:"), source.index("parser = SafeArgumentParser"))
        self.assertLess(source.index("if CHANGE_ID_CONSUMED:"), source.index("installer = read_frozen_installer()"))
        with tempfile.TemporaryDirectory(prefix="g8-016-consumed-") as temporary:
            script = Path(temporary) / SCRIPT_PATH.name
            script.write_text(source.replace("CHANGE_ID_CONSUMED = False", "CHANGE_ID_CONSUMED = True"), encoding="utf-8")
            result = subprocess.run(
                ["python", "-I", str(script), "--unknown=DO_NOT_ECHO_SECRET_SENTINEL"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=change_id_consumed")
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()

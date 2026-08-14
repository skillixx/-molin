#!/usr/bin/env python3
"""验证 019 命令生成器只生成冻结人工步骤，不读取密码或连接网络。"""

import ast
import base64
import importlib.util
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-019-command.py")
INSTALLER_PATH = Path(__file__).with_name("g8-test-readonly-access-install-019.sh")
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019"
SOURCE_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"


def bash_executable() -> str:
    """Windows 使用 Git Bash；Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


def load_module():
    """从固定路径加载生成器，仅调用离线纯函数。"""
    specification = importlib.util.spec_from_file_location("g8_command_019", SCRIPT_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


def decode_remote_script(command: str) -> str:
    """从 PowerShell 固定变量中解码真正交给远端 Bash 的脚本。"""
    payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
    return base64.b64decode(payload, validate=True).decode("utf-8")


class TestPrepareG8ReadonlyAccess019Command(unittest.TestCase):
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
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_019_COMMAND_SELF_TEST=PASS")
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
        with tempfile.TemporaryDirectory(prefix="g8-019-command-") as temporary:
            output = Path(temporary).resolve() / "install-command.txt"
            result = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(output.is_file())
            generated = output.read_text(encoding="utf-8")
            self.assertEqual(generated, self.command)
            self.assertIn("G8_TEST_READONLY_ACCESS_019_COMMAND=PASS", result.stdout)
            self.assertIn("root_installer_sha256=", result.stdout)
            self.assertIn("command_sha256=", result.stdout)
            duplicate = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={output}")
            self.assertEqual(duplicate.returncode, 2)
            self.assertEqual(duplicate.stdout.strip(), "G8_TEST_READONLY_ACCESS_019_COMMAND=FAILED reason=invalid_request")

    def test_command_separates_authorization_from_frozen_011_source(self) -> None:
        remote = decode_remote_script(self.command)
        self.assertIn(CHANGE_ID, remote)
        self.assertIn(SOURCE_CHANGE_ID, remote)
        self.assertIn("auth_change_id=CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019", remote)
        self.assertIn("source_change_id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", remote)
        self.assertIn("REMOTE_AUTHORIZED", self.command)
        self.assertIn(".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", remote)

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
        self.assertIn("LogLevel=QUIET", command)
        self.assertNotIn("LogLevel=ERROR", command)
        self.assertEqual(command.count("pc@8.130.9.163"), 1)
        self.assertEqual(command.count("  -tt `"), 1)
        self.assertEqual(command.count("& $ssh `"), 1)
        self.assertNotIn("# 第二步", command)
        payload = self.command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
        # Windows CreateProcess 的命令行上限为 32767 字符，保留选项和目标参数的安全余量。
        self.assertLess(len(payload), 30000)

    def test_host_shell_never_exits_and_always_reports_fixed_result(self) -> None:
        """本地失败不得关闭父 PowerShell，必须留下固定结果与可读取退出码。"""
        self.assertNotIn("exit 2", self.command)
        self.assertIn("G8_TEST_READONLY_ACCESS_019_HOST_RESULT=PASS exit_code=0", self.command)
        self.assertIn("G8_TEST_READONLY_ACCESS_019_HOST_RESULT=FAILED exit_code=2", self.command)
        self.assertIn("$global:LASTEXITCODE = $g8HostExitCode", self.command)

    @unittest.skipUnless(os.name == "nt", "父 PowerShell 保活回归只在原生 Windows 门禁执行。")
    def test_host_result_tail_keeps_parent_process_alive(self) -> None:
        """成功或失败尾段都不得结束父进程，并须留下固定结果和退出码。"""
        tail = self.command.split("if ($g8LocalGatePassed) {", 1)[1]
        tail = "if ($g8LocalGatePassed) {" + tail
        for passed, marker, code in (
            ("$true", "G8_TEST_READONLY_ACCESS_019_HOST_RESULT=PASS exit_code=0", "0"),
            ("$false", "G8_TEST_READONLY_ACCESS_019_HOST_RESULT=FAILED exit_code=2", "2"),
        ):
            with self.subTest(passed=passed):
                probe = f"$g8LocalGatePassed={passed};" + tail + ";Write-Output ('OBSERVED=' + $LASTEXITCODE)"
                result = subprocess.run(
                    [
                        r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                        "-NoProfile",
                        "-NonInteractive",
                        "-Command",
                        probe,
                    ],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), [marker, f"OBSERVED={code}"])

    def test_low_sensitive_stage_reasons_and_ssh_attempt_boundary_are_fixed(self) -> None:
        """六类失败必须可区分，且只有真正进入唯一 SSH 调用时才记录尝试。"""
        command = self.command
        stage_contract = (
            ("trusted_windows_path_failed", "$windowsRoot ="),
            ("material_evidence_failed", "$materialEvidence = @{}"),
            ("known_hosts_failed", "$foundHostMatches ="),
            ("identity_pair_failed", "$derivedPublic ="),
            ("material_drift_failed", "foreach ($path in $materialEvidence.Keys)"),
            ("ssh_session_failed", "[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_019_PRE_SSH_GATE=PASS')"),
        )
        for reason, guarded_operation in stage_contract:
            assignment = "$g8FailureReason = '" + reason + "'"
            expected_count = 2 if reason == "known_hosts_failed" else 1
            self.assertEqual(command.count(assignment), expected_count)
            self.assertLess(command.index(assignment), command.index(guarded_operation))

        pre_ssh = "[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_019_PRE_SSH_GATE=PASS')"
        attempted = "[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_019_SSH_ATTEMPTED=YES')"
        invocation = "    & $ssh `"
        self.assertEqual(command.count(pre_ssh), 1)
        self.assertEqual(command.count(attempted), 1)
        self.assertEqual(command.count(invocation), 1)
        self.assertLess(command.index("$g8FailureReason = 'ssh_session_failed'"), command.index(pre_ssh))
        self.assertLess(command.index(pre_ssh), command.index(attempted))
        self.assertEqual(command.index(invocation), command.index(attempted) + len(attempted) + 1)

        catch_block = command.split("} catch {", 1)[1].split("} finally {", 1)[0]
        self.assertIn("reason=' + $g8FailureReason", catch_block)
        self.assertNotIn("local_gate_failed", catch_block)
        self.assertNotIn("$_", catch_block)

    def test_frozen_known_hosts_is_locked_under_trusted_profile_until_ssh_returns(self) -> None:
        """冻结 known_hosts 禁止依赖 TEMP，并须在 SSH 读取期间阻止替换或删除。"""
        command = self.command
        self.assertNotIn("GetTempFileName", command)
        self.assertNotIn("$env:TEMP", command)
        self.assertNotIn("$env:TMP", command)
        self.assertIn("Join-Path $profileRoot ('.g8-known-hosts-'", command)
        self.assertIn("[IO.FileMode]::CreateNew", command)
        self.assertIn("[IO.FileAccess]::ReadWrite", command)
        self.assertIn("[IO.FileShare]::Read", command)
        self.assertIn("$knownHostsStream.Flush($true)", command)
        self.assertIn("$knownHostsStream.Dispose()", command)
        create_index = command.index("$frozenKnownHosts = Join-Path")
        flush_index = command.index("$knownHostsStream.Flush($true)")
        ssh_reason_index = command.index("$g8FailureReason = 'ssh_session_failed'")
        ssh_index = command.index("    & $ssh `")
        dispose_index = command.index("$knownHostsStream.Dispose()")
        self.assertLess(command.rindex("$g8FailureReason = 'known_hosts_failed'", 0, create_index), create_index)
        self.assertLess(create_index, flush_index)
        self.assertLess(flush_index, ssh_reason_index)
        self.assertLess(ssh_reason_index, ssh_index)
        self.assertLess(ssh_index, dispose_index)

    @unittest.skipUnless(os.name == "nt", "文件共享锁与伪造 TEMP 回归只在原生 Windows 门禁执行。")
    def test_frozen_known_hosts_ignores_forged_temp_and_blocks_replacement(self) -> None:
        """执行生产创建段，证明伪造 TEMP 不生效且持锁期间不可写入或删除。"""
        creation = self.command.rsplit("  $g8FailureReason = 'known_hosts_failed'\n", 1)[1].split(
            "    $g8FailureReason = 'ssh_session_failed'\n",
            1,
        )[0]
        with tempfile.TemporaryDirectory(prefix="g8-019-known-hosts-lock-") as temporary:
            trusted_profile = str(Path(temporary).resolve()).replace("'", "''")
            probe = (
                "$ErrorActionPreference='Stop';"
                "$env:TEMP='\\\\forged-host\\share';$env:TMP='\\\\forged-host\\share';"
                "$profileRoot='"
                + trusted_profile
                + "';$targetParts=@('endpoint','ssh-ed25519','AAAA');"
                + creation
                + "    Write-Output ('UNDER_PROFILE=' + $frozenKnownHosts.StartsWith($profileRoot + [IO.Path]::DirectorySeparatorChar));\n"
                + "    try { $writer=[IO.File]::Open($frozenKnownHosts,[IO.FileMode]::Open,[IO.FileAccess]::Write,[IO.FileShare]::ReadWrite); $writer.Dispose(); Write-Output 'WRITE=ALLOWED' } catch { Write-Output 'WRITE=BLOCKED' };\n"
                + "    try { Remove-Item -LiteralPath $frozenKnownHosts -Force -ErrorAction Stop; Write-Output 'DELETE=ALLOWED' } catch { Write-Output 'DELETE=BLOCKED' };\n"
                + "  } finally { if ($null -ne $knownHostsStream) { $knownHostsStream.Dispose() }; Remove-Item -LiteralPath $frozenKnownHosts -Force -ErrorAction SilentlyContinue }"
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
        self.assertEqual(result.stdout.splitlines(), ["UNDER_PROFILE=True", "WRITE=BLOCKED", "DELETE=BLOCKED"])
        self.assertNotIn("forged-host", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "known_hosts 预占回归只在原生 Windows 门禁执行。")
    def test_preoccupied_frozen_known_hosts_is_not_removed_or_marked_as_ssh_attempt(self) -> None:
        """CreateNew 失败时不得删除非本事务文件，也不得越过 known_hosts 阶段。"""
        local_gate = self.command
        suffix = local_gate.rsplit("  $g8FailureReason = 'known_hosts_failed'\n", 1)[1]
        suffix = suffix.replace("[Guid]::NewGuid().ToString('N')", "'fixed-preoccupied'", 1)
        with tempfile.TemporaryDirectory(prefix="g8-019-known-hosts-preoccupied-") as temporary:
            profile = Path(temporary).resolve()
            preoccupied = profile / ".g8-known-hosts-fixed-preoccupied"
            original = b"PREOCCUPIED_DO_NOT_DELETE\n"
            preoccupied.write_bytes(original)
            escaped_profile = str(profile).replace("'", "''")
            probe = (
                "$g8PreviousErrorActionPreference=$ErrorActionPreference;$ErrorActionPreference='Stop';"
                "$g8LocalGatePassed=$false;$g8FailureReason='known_hosts_failed';try {"
                "$previousSystemRoot=$env:SystemRoot;$previousProgramData=$env:ProgramData;"
                "$profileRoot='"
                + escaped_profile
                + "';$targetParts=@('endpoint','ssh-ed25519','AAAA');try {\n"
                + "  $g8FailureReason = 'known_hosts_failed'\n"
                + suffix
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
            self.assertEqual(preoccupied.read_bytes(), original)
        self.assertEqual(result.stderr, "")
        self.assertEqual(
            result.stdout.splitlines(),
            [
                "G8_TEST_READONLY_ACCESS_019_LOCAL_GATE=FAILED reason=known_hosts_failed",
                "G8_TEST_READONLY_ACCESS_019_HOST_RESULT=FAILED exit_code=2",
            ],
        )
        self.assertNotIn("PRE_SSH_GATE", result.stdout)
        self.assertNotIn("SSH_ATTEMPTED", result.stdout)

    @unittest.skipUnless(os.name == "nt", "低敏异常注入只在原生 Windows PowerShell 5.1 门禁执行。")
    def test_each_stage_exception_is_reduced_to_fixed_low_sensitive_output(self) -> None:
        """逐阶段注入含敏感哨兵的异常，证明捕获层不会回显异常正文或误记 SSH 尝试。"""
        catch_block = self.command.split("} catch {", 1)[1].split("} finally {", 1)[0]
        reasons = (
            "trusted_windows_path_failed",
            "material_evidence_failed",
            "known_hosts_failed",
            "identity_pair_failed",
            "material_drift_failed",
            "ssh_session_failed",
        )
        for reason in reasons:
            with self.subTest(reason=reason):
                probe = (
                    "$g8FailureReason='"
                    + reason
                    + "'; try { throw 'DO_NOT_ECHO_PATH_FINGERPRINT_OR_SECRET' } catch {"
                    + catch_block
                    + "}"
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
                self.assertEqual(
                    result.stdout.strip(),
                    "G8_TEST_READONLY_ACCESS_019_LOCAL_GATE=FAILED reason=" + reason,
                )
                self.assertNotIn("DO_NOT_ECHO", result.stdout + result.stderr)
                self.assertNotIn("SSH_ATTEMPTED", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "加密私钥无提示拒绝只在原生 Windows 门禁执行。")
    def test_encrypted_identity_is_rejected_without_extra_passphrase_prompt(self) -> None:
        """私钥配对检查必须显式使用空口令，拒绝在 sudo 前出现第二类凭据提示。"""
        invocation = "$derivedPublic = (& $sshKeygen -y -P '' -f $identity 2>$null)"
        self.assertIn(invocation, self.command)
        ssh_keygen = Path(r"C:\Windows\System32\OpenSSH\ssh-keygen.exe")
        self.assertTrue(ssh_keygen.is_file())
        with tempfile.TemporaryDirectory(prefix="g8-019-encrypted-identity-") as temporary:
            identity = Path(temporary) / "id_ed25519"
            generated = subprocess.run(
                [
                    str(ssh_keygen),
                    "-q",
                    "-t",
                    "ed25519",
                    "-N",
                    "temporary-test-passphrase",
                    "-f",
                    str(identity),
                ],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                check=False,
            )
            self.assertEqual(generated.returncode, 0, generated.stderr)
            rejected = subprocess.run(
                [str(ssh_keygen), "-y", "-P", "", "-f", str(identity)],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                timeout=5,
                check=False,
            )
            self.assertNotEqual(rejected.returncode, 0)

    @unittest.skipUnless(os.name == "nt", "低敏失败输出回归只在原生 Windows 门禁执行。")
    def test_local_material_failure_does_not_disclose_absolute_path(self) -> None:
        """本地身份材料门禁失败时只输出固定原因，不回显绝对路径或异常正文。"""
        local_gate = self.command
        fake_identity = str(Path(tempfile.gettempdir()).resolve() / "g8-019-sensitive-identity" / "id_ed25519")
        probe = local_gate.replace(
            "$identity = Join-Path $profileRoot '.ssh\\id_ed25519'",
            "$identity = '" + fake_identity.replace("'", "''") + "'",
            1,
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
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stderr, "")
        self.assertEqual(
            result.stdout.splitlines(),
            [
                "G8_TEST_READONLY_ACCESS_019_LOCAL_GATE=FAILED reason=material_evidence_failed",
                "G8_TEST_READONLY_ACCESS_019_HOST_RESULT=FAILED exit_code=2",
            ],
        )
        self.assertNotIn(fake_identity, result.stdout + result.stderr)

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
            + "\nWrite-Output 'G8_019_WINDOWS_PATH_GUARD=PASS'\n"
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
        self.assertIn("G8_019_WINDOWS_PATH_GUARD=PASS", result.stdout)

    @unittest.skipUnless(os.name == "nt", "PowerShell 模块隔离回归只在原生 Windows 门禁执行。")
    def test_material_hash_does_not_depend_on_module_autoload(self) -> None:
        """交互宿主无法加载外部模块时，冻结材料摘要仍必须由纯 .NET 路径计算。"""
        function = self.command.split("function Get-FrozenMaterialEvidence", 1)[1].split(
            "$previousSystemRoot", 1
        )[0]
        probe = (
            "$PSModuleAutoLoadingPreference='None'\n"
            "Remove-Module Microsoft.PowerShell.Utility -Force -ErrorAction SilentlyContinue\n"
            "function Get-FrozenMaterialEvidence"
            + function
            + "$temporary=[IO.Path]::GetTempFileName()\n"
            "try {\n"
            "  [IO.File]::WriteAllBytes($temporary,[Text.Encoding]::ASCII.GetBytes('abc'))\n"
            "  $evidence=Get-FrozenMaterialEvidence $temporary\n"
            "  if (-not $evidence.EndsWith(':BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD')) { throw 'hash_mismatch' }\n"
            "  [Console]::Out.WriteLine('G8_019_MODULE_ISOLATED_HASH=PASS')\n"
            "} finally { [IO.File]::Delete($temporary) }\n"
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
        self.assertIn("G8_019_MODULE_ISOLATED_HASH=PASS", result.stdout)

    @unittest.skipUnless(os.name == "nt", "PowerShell 资源释放回归只在原生 Windows 门禁执行。")
    def test_material_hash_releases_stream_when_hash_lifecycle_fails(self) -> None:
        """哈希对象创建或释放失败时，外层 finally 仍必须关闭冻结材料文件流。"""
        function = self.command.split("function Get-FrozenMaterialEvidence", 1)[1].split(
            "$previousSystemRoot", 1
        )[0]
        scenarios = (
            (
                "$sha = [Security.Cryptography.SHA256]::Create()",
                "throw 'sha_create_failed'",
                "CREATE",
            ),
            ("$sha.Dispose()", "throw 'sha_dispose_failed'", "DISPOSE"),
        )
        for original, replacement, label in scenarios:
            with self.subTest(label=label):
                injected = function.replace(original, replacement, 1)
                self.assertNotEqual(injected, function)
                probe = (
                    "function Get-FrozenMaterialEvidence"
                    + injected
                    + "$temporary=[IO.Path]::GetTempFileName()\n"
                    "try {\n"
                    "  [IO.File]::WriteAllBytes($temporary,[Text.Encoding]::ASCII.GetBytes('abc'))\n"
                    "  $failed=$false\n"
                    "  try { Get-FrozenMaterialEvidence $temporary | Out-Null } catch { $failed=$true }\n"
                    "  if (-not $failed) { throw 'injected_failure_not_observed' }\n"
                    "  [IO.File]::Delete($temporary)\n"
                    "  if ([IO.File]::Exists($temporary)) { throw 'stream_not_released' }\n"
                    f"  [Console]::Out.WriteLine('G8_019_HASH_{label}_RELEASE=PASS')\n"
                    "} finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }\n"
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
                self.assertIn(f"G8_019_HASH_{label}_RELEASE=PASS", result.stdout)

    @unittest.skipUnless(os.name == "nt", "PowerShell 失败关闭回归只在原生 Windows 门禁执行。")
    def test_path_guard_error_cannot_reach_identity_or_ssh_stage(self) -> None:
        """破坏真实正则后必须在路径门禁终止，后续身份与 SSH 阶段哨兵不可到达。"""
        prefix = self.command.split("$ssh =", 1)[0]
        corrupted = prefix.replace("-notmatch '^[A-Za-z]:\\\\'", "-notmatch '^[A-Za-z]:\\'")
        self.assertNotEqual(corrupted, prefix)
        probe = (
            corrupted
            + "\nWrite-Output 'G8_019_IDENTITY_OR_SSH_STAGE_REACHED'\n"
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
        self.assertNotIn("G8_019_IDENTITY_OR_SSH_STAGE_REACHED", result.stdout)

    def test_remote_block_has_one_manual_sudo_and_only_noninteractive_followups(self) -> None:
        remote = decode_remote_script(self.command)
        self.assertEqual(remote.count("/usr/bin/sudo -k -v"), 1)
        self.assertEqual(remote.count("/usr/bin/sudo -n /bin/bash -ceu"), 1)
        # 审计器入口验证已经移入安装器回滚事务，远端包装层不得在事务提交后再次执行。
        self.assertEqual(remote.count("/usr/bin/sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test"), 0)
        self.assertIn("G8_TEST_READONLY_ACCESS_PREFLIGHT_019=PASS", remote)
        self.assertIn("G8_TEST_READONLY_ACCESS_POSTCHECK_019=PASS", remote)
        self.assertIn("G8_019_INSTALL_B64", remote)
        self.assertNotIn("G8_019_REMOTE", self.command)
        self.assertEqual(remote.count("$'\\r' \"$staging/manifest.env\""), 3)

    def test_remote_here_doc_has_valid_bash_syntax(self) -> None:
        """对真正交给非交互 Bash 的正文做语法解析，不执行任何命令。"""
        remote = decode_remote_script(self.command)
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
        local = self.command
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
        remote = decode_remote_script(self.command)
        encoded = remote.split("G8_019_INSTALL_B64'\n", 1)[1].split("\nG8_019_INSTALL_B64", 1)[0]
        self.assertEqual(base64.b64decode(encoded), self.installer)
        for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
            self.assertNotIn(forbidden, self.command)
            self.assertNotIn(forbidden, remote)

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
            self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_019_COMMAND=FAILED reason=invalid_request")
            self.assertEqual(result.stderr, "")
            self.assertNotIn("DO_NOT_ECHO_SECRET_SENTINEL", result.stdout + result.stderr)

    def test_unc_and_device_output_paths_are_rejected_before_filesystem_probe(self) -> None:
        """UNC 与设备路径必须在 exists/open 前纯字符串拒绝，禁止生成器触发 SMB 或设备访问。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertLess(
            source.index("if not is_safe_local_output_path(output_value):"),
            source.index("output.exists()"),
        )
        for unsafe in (
            r"\\forged-host\share\g8-019.txt",
            r"\\?\UNC\forged-host\share\g8-019.txt",
            r"\\.\C:\g8-019.txt",
            "//forged-host/share/g8-019.txt",
            r"C:\g8\NUL",
            r"C:\g8\CON.txt",
            r"C:\g8\AUX. ",
            r"C:\g8\COM1.log",
            r"C:\g8\LPT9",
            r"C:\g8\CONIN$",
            r"C:\g8\CONOUT$.txt",
            r"C:\g8\CLOCK$",
            r"C:\g8\report.txt:hidden",
        ):
            self.assertFalse(self.module.is_safe_local_output_path(unsafe))
            result = self.run_script(f"--change-id={CHANGE_ID}", f"--output-file={unsafe}")
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_019_COMMAND=FAILED reason=invalid_request")
            self.assertEqual(result.stderr, "")
            self.assertNotIn("forged-host", result.stdout + result.stderr)

    def test_future_consumed_gate_precedes_installer_read_and_argument_parse(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertLess(source.index("if CHANGE_ID_CONSUMED:"), source.index("parser = SafeArgumentParser"))
        self.assertLess(source.index("if CHANGE_ID_CONSUMED:"), source.index("installer = read_frozen_installer()"))
        with tempfile.TemporaryDirectory(prefix="g8-019-consumed-") as temporary:
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
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_019_COMMAND=FAILED reason=change_id_consumed")
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()

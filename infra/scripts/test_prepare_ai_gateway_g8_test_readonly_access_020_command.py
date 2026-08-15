#!/usr/bin/env python3
"""验证 020 冻结命令的耐久低敏回执与 PowerShell 5.1 状态恢复。"""

import importlib.util
import base64
import ast
import hashlib
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-020-command.py")
INSTALLER_PATH = Path(__file__).with_name("g8-test-readonly-access-install-020.sh")


def load_module():
    """从固定路径加载生成器，只调用不联网的构造接口。"""
    specification = importlib.util.spec_from_file_location("g8_command_020", SCRIPT_PATH)
    if specification is None or specification.loader is None:
        raise RuntimeError("020 生成器无法加载")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def decode_remote_script(command: str) -> str:
    """解码唯一 SSH 会话携带的固定远端脚本。"""
    payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
    return base64.b64decode(payload, validate=True).decode("utf-8")


class TestPrepareG8ReadonlyAccess020Command(unittest.TestCase):
    @unittest.skipUnless(os.name == "nt", "完整 PowerShell 5.1 回归只在原生 Windows 门禁执行。")
    def test_null_preference_cannot_mask_primary_result_and_receipt_is_durable(self) -> None:
        """Null 状态恢复不得抛错，固定失败结果必须同时出现在控制台和落盘回执。"""
        module = load_module()
        installer = INSTALLER_PATH.read_bytes()
        with tempfile.TemporaryDirectory(prefix="g8-020-receipt-") as temporary:
            receipt = Path(temporary).resolve() / "execution.receipt.txt"
            command = module.build_command(
                installer,
                receipt_path=str(receipt),
                test_scenario="fail_before_ssh",
            )
            probe = "$ErrorActionPreference=$null\n" + command
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
            self.assertIn(
                "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=test_failure exit_code=2",
                result.stdout,
            )
            self.assertTrue(receipt.is_file())
            lines = receipt.read_text(encoding="utf-8").splitlines()
            self.assertEqual(lines[0], "G8_TEST_READONLY_ACCESS_020_RECEIPT=STARTED")
            self.assertEqual(
                lines[-1],
                "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=test_failure exit_code=2",
            )
            self.assertNotIn(str(receipt), result.stdout + result.stderr + "\n".join(lines))

    @unittest.skipUnless(os.name == "nt", "回执刷盘故障注入只在原生 Windows 门禁执行。")
    def test_receipt_flush_failures_stop_before_ssh_without_leaking_exception(self) -> None:
        """普通或耐久刷盘失败都必须在 SSH 前低敏停止，并保留父 PowerShell。"""
        module = load_module()
        for expression in (
            "$g8ReceiptWriter.WriteLine($line)",
            "$g8ReceiptWriter.Flush()",
            "$g8ReceiptStream.Flush($true)",
        ):
            with self.subTest(expression=expression), tempfile.TemporaryDirectory(
                prefix="g8-020-flush-failure-"
            ) as temporary:
                receipt = Path(temporary).resolve() / "execution.receipt.txt"
                command = module.build_command(
                    INSTALLER_PATH.read_bytes(),
                    receipt_path=str(receipt),
                    test_scenario="fail_before_ssh",
                )
                injected = command.replace(expression, "throw 'DO_NOT_ECHO_FLUSH_FAILURE'", 1)
                result = subprocess.run(
                    [
                        r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                        "-NoProfile",
                        "-NonInteractive",
                        "-Command",
                        "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE); Write-Output ('PREFERENCE=' + $ErrorActionPreference)",
                    ],
                    input=injected,
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
                        "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2",
                        "OBSERVED=2",
                        "PREFERENCE=Continue",
                    ],
                )
                self.assertNotIn("DO_NOT_ECHO_FLUSH_FAILURE", result.stdout + result.stderr)
                self.assertNotIn("SSH_ATTEMPTED", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "失效回执状态回归只在原生 Windows 门禁执行。")
    def test_failed_receipt_writer_is_not_used_again_for_final_result(self) -> None:
        """失败回执写入一旦失败，最终结果不得再次调用同一失效 writer。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-020-writer-state-") as temporary:
            receipt = Path(temporary).resolve() / "execution.receipt.txt"
            command = module.build_command(
                INSTALLER_PATH.read_bytes(),
                receipt_path=str(receipt),
                test_scenario="fail_before_ssh",
            )
            injected = command.replace(
                "$g8ReceiptWriter.WriteLine($line)",
                "if ($line -like '*LOCAL_GATE=FAILED*') { throw 'DO_NOT_ECHO_WRITE_FAILURE' }; $g8ReceiptWriter.WriteLine($line)",
                1,
            )
            result = subprocess.run(
                [
                    r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-Command",
                    "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE)",
                ],
                input=injected,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, "")
            self.assertIn("G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=test_failure exit_code=2", result.stdout)
            self.assertIn("OBSERVED=2", result.stdout)
            self.assertNotIn("DO_NOT_ECHO_WRITE_FAILURE", result.stdout + result.stderr)
            self.assertEqual(
                receipt.read_text(encoding="utf-8").splitlines(),
                ["G8_TEST_READONLY_ACCESS_020_RECEIPT=STARTED"],
            )

    @unittest.skipUnless(os.name == "nt", "假 SSH 进程回归只在原生 Windows 门禁执行。")
    def test_fake_ssh_result_is_persisted_at_the_exact_attempt_boundary(self) -> None:
        """本地假 SSH 必须实际执行一次，且回执按 PRE_SSH、ATTEMPTED、最终结果排序。"""
        module = load_module()
        installer = INSTALLER_PATH.read_bytes()
        for fake_exit, expected_result in (
            (0, "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=PASS exit_code=0"),
            (23, "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=ssh_session_failed exit_code=2"),
        ):
            with self.subTest(fake_exit=fake_exit), tempfile.TemporaryDirectory(prefix="g8-020-fake-ssh-") as temporary:
                root = Path(temporary).resolve()
                receipt = root / "execution.receipt.txt"
                fake_ssh = root / "ssh.cmd"
                fake_ssh.write_text(f"@exit /b {fake_exit}\n", encoding="ascii")
                command = module.build_command(
                    installer,
                    receipt_path=str(receipt),
                    test_scenario="fake_ssh",
                    test_ssh_path=str(fake_ssh),
                )
                result = subprocess.run(
                    [
                        r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                        "-NoProfile",
                        "-NonInteractive",
                        "-Command",
                        "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source))",
                    ],
                    input=command,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stderr, "")
                lines = receipt.read_text(encoding="utf-8").splitlines()
                self.assertEqual(lines.count("G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES"), 1)
                self.assertLess(
                    lines.index("G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS"),
                    lines.index("G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES"),
                )
                self.assertEqual(lines[-1], expected_result)
                self.assertIn(expected_result, result.stdout)
                self.assertNotIn(str(fake_ssh), result.stdout + result.stderr + "\n".join(lines))

    def test_remote_live_state_is_classified_before_any_interactive_sudo(self) -> None:
        """精确已安装只做 post-check，完全缺失才安装，部分或漂移必须在 sudo 前停止。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path=r"C:\g8-020.receipt.txt")
        remote = decode_remote_script(command)
        self.assertIn("classify_live_state()", remote)
        self.assertIn("G8_TEST_READONLY_ACCESS_LIVE_STATE_020=EXACT", remote)
        self.assertIn("G8_TEST_READONLY_ACCESS_LIVE_STATE_020=ABSENT", remote)
        self.assertIn("G8_TEST_READONLY_ACCESS_LIVE_STATE_020=DRIFT", remote)
        classify_index = remote.index("live_state=$(classify_live_state)")
        sudo_index = remote.index("/usr/bin/sudo -k -v")
        self.assertLess(classify_index, sudo_index)
        exact_block = remote.split('if [ "$live_state" = exact ]; then', 1)[1].split("fi", 1)[0]
        self.assertIn("G8_TEST_READONLY_ACCESS_POSTCHECK_020=PASS", exact_block)
        self.assertNotIn("sudo -k -v", exact_block)
        drift_block = remote.split('if [ "$live_state" = drift ]; then', 1)[1].split("fi", 1)[0]
        self.assertIn("G8_TEST_READONLY_ACCESS_LIVE_STATE_020=DRIFT", drift_block)
        self.assertNotIn("sudo -k -v", drift_block)

    def test_production_receipt_marks_the_unique_ssh_boundary(self) -> None:
        """正式命令必须把 PRE_SSH 与 ATTEMPTED 同步刷盘，且 ATTEMPTED 紧邻唯一 SSH 调用。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path=r"C:\g8-020.receipt.txt")
        pre_console = "[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')"
        pre_receipt = "if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')) { $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }"
        attempted_console = "[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')"
        attempted_receipt = "if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')) { $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }"
        invocation = "    & $ssh `"
        for marker in (pre_console, pre_receipt, attempted_console, attempted_receipt):
            self.assertEqual(command.count(marker), 1)
        self.assertEqual(command.count(invocation), 1)
        self.assertLess(command.index(pre_console), command.index(pre_receipt))
        self.assertLess(command.index(pre_receipt), command.index(attempted_console))
        self.assertLess(command.index(attempted_console), command.index(attempted_receipt))
        self.assertLess(command.index(attempted_receipt), command.index(invocation))
        between_attempt_and_ssh = command[
            command.index(attempted_receipt) + len(attempted_receipt) : command.index(invocation)
        ]
        self.assertEqual(between_attempt_and_ssh.strip(), "")

    def test_formal_generation_uses_one_fixed_trusted_profile_receipt(self) -> None:
        """正式 CLI 不接受可变回执路径，命令必须从 Windows API 派生固定可信回执。"""
        change_id = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020"
        with tempfile.TemporaryDirectory(prefix="g8-020-formal-") as temporary:
            root = Path(temporary).resolve()
            output = root / "authorized-command.ps1"
            result = subprocess.run(
                [
                    "python",
                    "-I",
                    str(SCRIPT_PATH),
                    f"--change-id={change_id}",
                    f"--output-file={output}",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(output.is_file())
            command = output.read_text(encoding="utf-8")
            self.assertIn("GetFolderPath([Environment+SpecialFolder]::UserProfile)", command)
            self.assertIn(".g8-020-execution-receipt.txt", command)
            self.assertIn("[IO.FileMode]::CreateNew", command)
            self.assertNotIn(str(output), result.stdout + result.stderr)
            rejected_output = root / "rejected-command.ps1"
            rejected = subprocess.run(
                [
                    "python",
                    "-I",
                    str(SCRIPT_PATH),
                    f"--change-id={change_id}",
                    f"--output-file={rejected_output}",
                    f"--receipt-file={root / 'forbidden.receipt.txt'}",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(rejected.returncode, 2)
            self.assertFalse(rejected_output.exists())

    @unittest.skipUnless(os.name == "nt", "回执预占回归只在原生 Windows 门禁执行。")
    def test_preoccupied_receipt_fails_closed_without_path_or_ssh(self) -> None:
        """CreateNew 失败必须固定低敏停止，不能覆盖既有回执或误记 SSH 尝试。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-020-preoccupied-receipt-") as temporary:
            receipt = Path(temporary).resolve() / "execution.receipt.txt"
            receipt.write_text("PREOCCUPIED_DO_NOT_OVERWRITE\n", encoding="ascii")
            command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path=str(receipt))
            result = subprocess.run(
                [
                    r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-Command",
                        "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE); Write-Output ('PREFERENCE=' + $ErrorActionPreference)",
                ],
                input=command,
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
                    "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2",
                    "OBSERVED=2",
                    "PREFERENCE=Continue",
                ],
            )
            self.assertEqual(receipt.read_text(encoding="ascii"), "PREOCCUPIED_DO_NOT_OVERWRITE\n")
            self.assertNotIn(str(receipt), result.stdout + result.stderr)
            self.assertNotIn("SSH_ATTEMPTED", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "回执清理故障注入只在原生 Windows 门禁执行。")
    def test_receipt_dispose_failure_cannot_mask_primary_result(self) -> None:
        """最终 Dispose 异常不得替换固定主结果、泄露异常或阻止 LASTEXITCODE 落定。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-020-dispose-failure-") as temporary:
            receipt = Path(temporary).resolve() / "execution.receipt.txt"
            command = module.build_command(
                INSTALLER_PATH.read_bytes(),
                receipt_path=str(receipt),
                test_scenario="fail_before_ssh",
            )
            before, separator, after = command.rpartition("$g8ReceiptWriter.Dispose()")
            self.assertNotEqual(separator, "")
            injected = before + "throw 'DO_NOT_ECHO_DISPOSE_FAILURE'" + after
            result = subprocess.run(
                [
                    r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-Command",
                    "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE)",
                ],
                input=injected,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, "")
            self.assertIn(
                "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=test_failure exit_code=2",
                result.stdout,
            )
            self.assertIn("OBSERVED=2", result.stdout)
            self.assertNotIn("DO_NOT_ECHO", result.stdout + result.stderr)
            self.assertEqual(
                receipt.read_text(encoding="utf-8").splitlines()[-1],
                "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=test_failure exit_code=2",
            )

    def test_self_test_and_source_are_offline(self) -> None:
        """生成器自检只解析冻结字节，不得导入联网或子进程模块。"""
        result = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_020_COMMAND_SELF_TEST=PASS")
        tree = ast.parse(SCRIPT_PATH.read_text(encoding="utf-8"))
        imports = {
            alias.name
            for node in ast.walk(tree)
            if isinstance(node, ast.Import)
            for alias in node.names
        }
        self.assertNotIn("subprocess", imports)
        self.assertNotIn("socket", imports)

    def test_production_command_preserves_single_session_security_controls(self) -> None:
        """020 不得降低 019 的单会话、固定材料、无口令提示和最小 sudo 控制。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path=r"C:\g8-020.receipt.txt")
        remote = decode_remote_script(command)
        for required in (
            "BatchMode=yes",
            "ConnectionAttempts=1",
            "NumberOfPasswordPrompts=0",
            "PasswordAuthentication=no",
            "KbdInteractiveAuthentication=no",
            "StrictHostKeyChecking=yes",
            "HostKeyAlgorithms=ssh-ed25519",
            "ClearAllForwardings=yes",
            "RequestTTY=force",
            "LogLevel=QUIET",
            "$sshKeygen -y -P '' -f $identity",
        ):
            self.assertIn(required, command)
        self.assertEqual(command.count("pc@8.130.9.163"), 1)
        self.assertEqual(command.count("    & $ssh `"), 1)
        self.assertEqual(remote.count("/usr/bin/sudo -k -v"), 1)
        self.assertEqual(remote.count("/usr/bin/sudo -n /bin/bash -ceu"), 1)
        payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
        self.assertLess(len(payload), 30000)
        for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
            self.assertNotIn(forbidden, command)
            self.assertNotIn(forbidden, remote)

    def test_remote_script_and_embedded_installer_are_exact(self) -> None:
        """远端 Bash 必须可解析，且 heredoc 中安装器字节与仓库冻结文件完全相同。"""
        module = load_module()
        installer = INSTALLER_PATH.read_bytes()
        command = module.build_command(installer, receipt_path=r"C:\g8-020.receipt.txt")
        remote = decode_remote_script(command)
        bash = Path(r"C:\Program Files\Git\bin\bash.exe") if os.name == "nt" else Path("/bin/bash")
        parsed = subprocess.run(
            [str(bash), "-n"],
            input=remote,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(parsed.returncode, 0, parsed.stderr)
        encoded = remote.split("G8_020_INSTALL_B64'\n", 1)[1].split("\nG8_020_INSTALL_B64", 1)[0]
        self.assertEqual(base64.b64decode(encoded), installer)

    @unittest.skipUnless(os.name == "nt", "PowerShell 语法解析只在原生 Windows 门禁执行。")
    def test_production_command_has_valid_powershell_51_syntax(self) -> None:
        """正式生成物整体必须由 Windows PowerShell 5.1 成功解析。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path=r"C:\g8-020.receipt.txt")
        result = subprocess.run(
            [
                r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$source=[Console]::In.ReadToEnd();[scriptblock]::Create($source)|Out-Null",
            ],
            input=command,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")

    @unittest.skipUnless(os.name != "nt", "三态动态回归在 Linux 断网门禁执行。")
    def test_absent_and_partial_live_states_are_behaviorally_distinct(self) -> None:
        """真实执行分类函数，证明全缺失为 absent，任何部分存在均为 drift。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path="/tmp/g8-020.receipt.txt")
        remote = decode_remote_script(command)
        function = "classify_live_state() {" + remote.split("classify_live_state() {", 1)[1].split(
            "\n/usr/bin/test \"$(/usr/bin/id -un)\"", 1
        )[0]
        with tempfile.TemporaryDirectory(prefix="g8-020-live-state-") as temporary:
            root = Path(temporary)
            auditor = root / "auditor"
            reconcile = root / "reconcile"
            sudoers = root / "sudoers"
            harness = root / "classify.sh"
            harness.write_text(
                "#!/bin/bash\nset -euo pipefail\n"
                + f"auditor_target='{auditor}'\nreconcile_target='{reconcile}'\nsudoers_target='{sudoers}'\n"
                + function
                + "\nclassify_live_state\n",
                encoding="utf-8",
            )
            absent = subprocess.run(["/bin/bash", str(harness)], capture_output=True, text=True, check=False)
            self.assertEqual(absent.returncode, 0, absent.stderr)
            self.assertEqual(absent.stdout.strip(), "absent")
            auditor.write_text("partial", encoding="ascii")
            partial = subprocess.run(["/bin/bash", str(harness)], capture_output=True, text=True, check=False)
            self.assertEqual(partial.returncode, 0, partial.stderr)
            self.assertEqual(partial.stdout.strip(), "drift")

    @unittest.skipUnless(os.name != "nt", "EXACT 与 sudoers 摘要漂移回归在 Linux 断网门禁执行。")
    def test_exact_live_state_rejects_sudoers_digest_drift(self) -> None:
        """三份 live 文件完全匹配才可判定 exact，sudoers 等价改写也必须判定 drift。"""
        module = load_module()
        command = module.build_command(INSTALLER_PATH.read_bytes(), receipt_path="/tmp/g8-020.receipt.txt")
        remote = decode_remote_script(command)
        function = "classify_live_state() {" + remote.split("classify_live_state() {", 1)[1].split(
            "\n/usr/bin/test \"$(/usr/bin/id -un)\"", 1
        )[0]
        with tempfile.TemporaryDirectory(prefix="g8-020-live-exact-") as temporary:
            root = Path(temporary)
            fixtures = {
                "auditor": b"#!/bin/sh\nexit 0\n",
                "reconcile": b"frozen-reconcile-fixture\n",
                "sudoers": b"pc ALL=(root) NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit\n",
            }
            paths = {name: root / name for name in fixtures}
            for name, content in fixtures.items():
                paths[name].write_bytes(content)
            paths["auditor"].chmod(0o755)
            paths["reconcile"].chmod(0o755)
            paths["sudoers"].chmod(0o440)
            function = function.replace(
                "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
                hashlib.sha256(fixtures["auditor"]).hexdigest(),
            ).replace(
                "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
                hashlib.sha256(fixtures["reconcile"]).hexdigest(),
            ).replace(
                "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
                hashlib.sha256(fixtures["sudoers"]).hexdigest(),
            ).replace("13066129", str(len(fixtures["reconcile"])))
            function = function.replace(
                "LC_ALL=C /usr/bin/sudo -n -l 2>/dev/null",
                "/usr/bin/printf '(root) NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit\\n'",
            ).replace(
                "/usr/bin/id -nG pc",
                "/usr/bin/printf '%s\\n' pc",
            ).replace(
                "/usr/bin/sudo -n \"$auditor_target\" --self-test >/dev/null 2>&1",
                "/usr/bin/test -x \"$auditor_target\"",
            ).replace(
                "$(/usr/bin/stat -c '%U:%G:%a' -- \"$auditor_target\")",
                "root:root:755",
            ).replace(
                "$(/usr/bin/stat -c '%U:%G:%a' -- \"$reconcile_target\")",
                "root:root:755",
            ).replace(
                "$(/usr/bin/stat -c '%U:%G:%a' -- \"$sudoers_target\")",
                "root:root:440",
            )
            harness = root / "classify-exact.sh"
            harness.write_text(
                "#!/bin/bash\nset -euo pipefail\n"
                + f"auditor_target='{paths['auditor']}'\nreconcile_target='{paths['reconcile']}'\nsudoers_target='{paths['sudoers']}'\n"
                + f"ah='{hashlib.sha256(fixtures['auditor']).hexdigest()}'\nrh='{hashlib.sha256(fixtures['reconcile']).hexdigest()}'\nsh='{hashlib.sha256(fixtures['sudoers']).hexdigest()}'\nrs='{len(fixtures['reconcile'])}'\n"
                + function
                + "\nclassify_live_state\n",
                encoding="utf-8",
            )
            exact = subprocess.run(["/bin/bash", str(harness)], capture_output=True, text=True, check=False)
            self.assertEqual(exact.returncode, 0, exact.stderr)
            self.assertEqual(exact.stdout.strip(), "exact")
            paths["sudoers"].chmod(0o640)
            paths["sudoers"].write_bytes(fixtures["sudoers"] + b"# equivalent drift\n")
            paths["sudoers"].chmod(0o440)
            drift = subprocess.run(["/bin/bash", str(harness)], capture_output=True, text=True, check=False)
            self.assertEqual(drift.returncode, 0, drift.stderr)
            self.assertEqual(drift.stdout.strip(), "drift")


if __name__ == "__main__":
    unittest.main()

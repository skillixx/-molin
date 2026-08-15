#!/usr/bin/env python3
"""验证 022 无安装、无 sudo 的单次 Docker 只读审计命令。"""

import base64
import hashlib
import importlib.util
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py")
AUDITOR_PATH = Path(__file__).with_name("audit-ai-gateway-g8-test-server-readonly.sh")
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022"


def load_module():
    """从固定文件加载离线生成器。"""
    specification = importlib.util.spec_from_file_location("g8_runtime_audit_022", SCRIPT_PATH)
    if specification is None or specification.loader is None:
        raise RuntimeError("022 运行态审计生成器无法加载")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def decode_remote(command: str) -> str:
    """解码唯一 SSH 命令携带的无秘密审计脚本。"""
    payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
    return base64.b64decode(payload, validate=True).decode("utf-8")


class TestPrepareG8ReadonlyRuntimeAudit022Command(unittest.TestCase):
    def test_source_auditor_is_frozen_and_019_remains_untouched(self) -> None:
        """022 只转换既有只读审计源，不能修改 019 墓碑或引入新安装资产。"""
        module = load_module()
        source = AUDITOR_PATH.read_bytes()
        self.assertEqual(len(source), module.EXPECTED_AUDITOR_SIZE)
        self.assertEqual(hashlib.sha256(source).hexdigest(), module.EXPECTED_AUDITOR_SHA256)
        self.assertEqual(module.CHANGE_ID, CHANGE_ID)
        self.assertFalse(module.CHANGE_ID_CONSUMED)
        self.assertFalse(module.REMOTE_EXECUTION_AUTHORIZED)

    def test_remote_audit_uses_direct_docker_without_install_or_sudo(self) -> None:
        """远端能力只能是 pc 直接执行固定只读 Docker/宿主查询。"""
        module = load_module()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=r"C:\g8-022.receipt.txt")
        remote = decode_remote(command)
        for required in (
            CHANGE_ID,
            "G8_TEST_READONLY_RUNTIME_AUDIT_022=PREFLIGHT_PASS",
            "docker_access=direct",
            "docker version --format",
            "docker ps --format",
            "docker inspect molin-mysql",
            "docker exec molin-mysql",
            "docker exec molin-redis",
            "docker exec molin-rabbitmq",
            "G8_TEST_READONLY_RUNTIME_AUDIT_022=COLLECTION_PASS",
            "reason=audit_evidence_failed",
            "AUDIT_COMPLETE=true",
        ):
            self.assertIn(required, remote)
        for forbidden in (
            "sudo",
            "/usr/local/libexec",
            "docker run",
            "docker create",
            "docker start",
            "docker stop",
            "docker restart",
            "docker rm",
            "docker cp",
            "docker compose",
            "install -",
            "visudo",
            "chmod ",
            "chown ",
            "mkdir ",
            "rm -",
            "INSERT ",
            "UPDATE ",
            "DELETE ",
            "CREATE ",
            "ALTER ",
            "DROP ",
            "hostname=",
            "machine_id_sha256=",
            "passwd_status=",
            "ssh_password_state=",
            "pc_docker_group_member=",
        ):
            self.assertNotIn(forbidden, remote)
        self.assertIn("audit_output=\"$(run_frozen_audit)\"", remote)
        self.assertIn("=(UNAVAILABLE|MISSING|INVALID|000)$", remote)

    def test_remote_script_is_valid_bash_and_payload_stays_bounded(self) -> None:
        """嵌入脚本必须可解析，且保持单个 SSH 参数的保守长度。"""
        module = load_module()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=r"C:\g8-022.receipt.txt")
        remote = decode_remote(command)
        bash = Path(r"C:\Program Files\Git\bin\bash.exe") if os.name == "nt" else Path("/bin/bash")
        result = subprocess.run(
            [str(bash), "-n"],
            input=remote,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
        self.assertLess(len(payload), 30000)

    def test_collection_footer_fails_closed_without_network(self) -> None:
        """必需探针不可用时必须由同一内存收集器非零停止，不能输出采集成功。"""
        module = load_module()
        remote = module.build_remote_audit(AUDITOR_PATH.read_bytes())
        start = remote.index("run_frozen_audit() {\n")
        footer = remote.index("# 全部低敏证据只保存在本会话内存中", start)
        bash = Path(r"C:\Program Files\Git\bin\bash.exe") if os.name == "nt" else Path("/bin/bash")
        complete = "".join(f"{key}=READY\\n" for key in module.REQUIRED_COLLECTION_KEYS)
        missing = "".join(f"{key}=READY\\n" for key in module.REQUIRED_COLLECTION_KEYS[1:])
        scenarios = (
            ("printf 'probe=UNAVAILABLE\\nAUDIT_COMPLETE=true\\n'", 42, "audit_evidence_failed"),
            ("printf 'probe=\\nAUDIT_COMPLETE=true\\n'", 42, "audit_evidence_failed"),
            (f"printf '{missing}AUDIT_COMPLETE=true\\n'", 42, "audit_evidence_failed"),
            (f"printf '{complete}AUDIT_COMPLETE=true\\n'", 0, "COLLECTION_PASS"),
        )
        for fake_body, expected_exit, expected_marker in scenarios:
            synthetic = remote[:start] + f"run_frozen_audit() {{\n  {fake_body}\n}}\n\n" + remote[footer:]
            result = subprocess.run(
                [str(bash)],
                input=synthetic,
                text=True,
                capture_output=True,
                encoding="utf-8",
                check=False,
                timeout=15,
            )
            self.assertEqual(result.returncode, expected_exit, result.stderr)
            self.assertIn(expected_marker, result.stdout)
            if expected_exit:
                self.assertNotIn("COLLECTION_PASS", result.stdout)

    def test_single_noninteractive_ssh_has_fixed_security_controls(self) -> None:
        """无 sudo 后不得申请 TTY；唯一 SSH 仍必须固定身份与零重试。"""
        module = load_module()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=r"C:\g8-022.receipt.txt")
        self.assertEqual(command.count("    & $ssh `"), 1)
        self.assertEqual(command.count("pc@8.130.9.163"), 1)
        for required in (
            "-T `",
            "RequestTTY=no",
            "BatchMode=yes",
            "ConnectionAttempts=1",
            "NumberOfPasswordPrompts=0",
            "StrictHostKeyChecking=yes",
            "LogLevel=QUIET",
            "G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS",
            "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES",
        ):
            self.assertIn(required, command)
        for forbidden in (" -tt `", "RequestTTY=force", "PasswordAuthentication=yes", "exit 2"):
            self.assertNotIn(forbidden, command)

    def test_trusted_receipt_uses_unique_create_new_name_and_fixed_failure_stages(self) -> None:
        """正式回执必须使用可信本地目录内的唯一名称，并区分低敏失败阶段。"""
        module = load_module()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=module.TRUSTED_LOCAL_APPDATA_RECEIPT)
        self.assertIn("[Environment+SpecialFolder]::LocalApplicationData", command)
        self.assertIn("while ($null -ne $directoryCursor)", command)
        self.assertIn("[Guid]::NewGuid().ToString('N')", command)
        self.assertIn(".g8-022-runtime-audit-", command)
        self.assertNotIn(".g8-022-runtime-audit-receipt.txt", command)
        for reason in (
            "receipt_directory_unavailable",
            "receipt_preoccupied",
            "receipt_write_failed",
            "receipt_flush_failed",
        ):
            self.assertIn(reason, command)
        pre_write = command.index("Write-G8Receipt 'G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS'")
        pre_output = command.index("[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS')")
        attempted_write = command.index("Write-G8Receipt 'G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES'")
        attempted_output = command.index("[Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES')")
        ssh_call = command.index("    & $ssh `")
        self.assertLess(pre_write, pre_output)
        self.assertLess(pre_output, attempted_write)
        self.assertLess(attempted_write, attempted_output)
        self.assertLess(attempted_output, ssh_call)
        self.assertLess(command.index("$g8FailureReason = 'known_hosts_failed'", command.index("$frozenKnownHosts")), ssh_call)

    @unittest.skipUnless(os.name == "nt", "PowerShell 5.1 动态回归只在原生 Windows 门禁执行。")
    def test_preoccupied_receipt_is_preserved_and_fails_before_ssh(self) -> None:
        """显式测试回执被预占时不得覆盖或删除，且不得进入假 SSH。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-022-runtime-preoccupied-") as temporary:
            root = Path(temporary).resolve()
            receipt = root / "execution.receipt.txt"
            receipt.write_bytes(b"KEEP")
            command = module.build_command(
                AUDITOR_PATH.read_bytes(),
                receipt_path=str(receipt),
                test_scenario="fake_ssh",
                test_ssh_path=str(root / "never-called.cmd"),
            )
            result = subprocess.run(
                [
                    r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-Command",
                    "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE)",
                ],
                input=command,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
                timeout=30,
            )
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, "")
            self.assertEqual(receipt.read_bytes(), b"KEEP")
            self.assertEqual(
                result.stdout.splitlines(),
                [
                    "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason=receipt_preoccupied exit_code=2",
                    "OBSERVED=2",
                ],
            )
            self.assertNotIn("SSH_ATTEMPTED", result.stdout)

    @unittest.skipUnless(os.name == "nt", "可信本地回执目录动态回归只在原生 Windows 门禁执行。")
    def test_trusted_local_appdata_receipt_is_durable_without_network(self) -> None:
        """真实可信目录必须能耐久写入唯一回执，测试只调用本地假 SSH。"""
        module = load_module()
        local_app_data = Path(os.environ["LOCALAPPDATA"]).resolve()
        before = set(local_app_data.glob(".g8-022-runtime-audit-*.receipt"))
        created: set[Path] = set()
        try:
            with tempfile.TemporaryDirectory(prefix="g8-022-runtime-trusted-") as temporary:
                fake_ssh = Path(temporary).resolve() / "ssh.cmd"
                fake_ssh.write_text("@exit /b 0\n", encoding="ascii")
                command = module.build_command(
                    AUDITOR_PATH.read_bytes(),
                    receipt_path=module.TRUSTED_LOCAL_APPDATA_RECEIPT,
                    test_scenario="fake_ssh",
                    test_ssh_path=str(fake_ssh),
                )
                environment = os.environ.copy()
                environment["LOCALAPPDATA"] = r"\\forged.invalid\share"
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
                    timeout=30,
                    env=environment,
                )
                created = set(local_app_data.glob(".g8-022-runtime-audit-*.receipt")) - before
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stderr, "")
                self.assertEqual(len(created), 1)
                lines = next(iter(created)).read_text(encoding="utf-8").splitlines()
                self.assertEqual(lines[0], "G8_TEST_READONLY_ACCESS_022_RECEIPT=STARTED")
                self.assertEqual(lines.count("G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES"), 1)
                self.assertEqual(lines[-1], "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=PASS exit_code=0")
        finally:
            for receipt in created:
                receipt.unlink(missing_ok=True)

    @unittest.skipUnless(os.name == "nt", "回执权限故障动态回归只在原生 Windows 门禁执行。")
    def test_receipt_create_permission_failure_is_low_sensitive_and_stops_before_ssh(self) -> None:
        """CreateNew 权限故障必须收敛为目录不可用，且不得泄露异常或尝试 SSH。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-022-runtime-permission-") as temporary:
            receipt = Path(temporary).resolve() / "execution.receipt.txt"
            command = module.build_command(
                AUDITOR_PATH.read_bytes(),
                receipt_path=str(receipt),
                test_scenario="fail_before_ssh",
            )
            injected = command.replace(
                "[IO.File]::Open($g8ReceiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)",
                "$(throw 'DO_NOT_ECHO_ACCESS_DENIED')",
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
                timeout=30,
            )
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, "")
            self.assertEqual(
                result.stdout.splitlines(),
                [
                    "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason=receipt_directory_unavailable exit_code=2",
                    "OBSERVED=2",
                ],
            )
            self.assertNotIn("DO_NOT_ECHO_ACCESS_DENIED", result.stdout + result.stderr)
            self.assertFalse(receipt.exists())
            self.assertNotIn("SSH_ATTEMPTED", result.stdout)

    @unittest.skipUnless(os.name == "nt", "回执目录与 Writer 构造故障动态回归只在原生 Windows 门禁执行。")
    def test_receipt_directory_and_writer_construction_fail_closed(self) -> None:
        """可信目录解析和 Writer 构造异常必须在 SSH 前固定低敏失败。"""
        module = load_module()
        cases = (
            (
                "$g8ReceiptDirectory = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)",
                "throw 'DO_NOT_ECHO_SPECIAL_FOLDER_FAILURE'",
                "receipt_directory_unavailable",
            ),
            (
                "$directoryItem = Get-Item -LiteralPath $g8ReceiptDirectory -Force -ErrorAction Stop",
                "throw 'DO_NOT_ECHO_DIRECTORY_FAILURE'",
                "receipt_directory_unavailable",
            ),
            (
                "$g8ReceiptWriter = New-Object IO.StreamWriter($g8ReceiptStream, (New-Object Text.UTF8Encoding($false)))",
                "throw 'DO_NOT_ECHO_WRITER_FAILURE'",
                "receipt_write_failed",
            ),
        )
        for original, replacement, expected_reason in cases:
            with self.subTest(original=original):
                command = module.build_command(
                    AUDITOR_PATH.read_bytes(),
                    receipt_path=module.TRUSTED_LOCAL_APPDATA_RECEIPT,
                    test_scenario="fail_before_ssh",
                )
                injected = command.replace(original, replacement, 1)
                self.assertNotEqual(injected, command)
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
                    timeout=30,
                )
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stderr, "")
                self.assertEqual(
                    result.stdout.splitlines(),
                    [
                        f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={expected_reason} exit_code=2",
                        "OBSERVED=2",
                    ],
                )
                self.assertNotIn("DO_NOT_ECHO", result.stdout + result.stderr)
                self.assertNotIn("SSH_ATTEMPTED", result.stdout)

    @unittest.skipUnless(os.name == "nt", "阶段回执故障动态回归只在原生 Windows 门禁执行。")
    def test_stage_receipt_failure_never_claims_or_calls_ssh(self) -> None:
        """阶段写入或刷盘失败时不得先输出 SSH 尝试标志，也不得调用假 SSH。"""
        module = load_module()
        stages = (
            "G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS",
            "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES",
        )
        expressions = (
            ("$g8ReceiptWriter.WriteLine($line)", "receipt_write_failed"),
            ("$g8ReceiptWriter.Flush()", "receipt_flush_failed"),
            ("$g8ReceiptStream.Flush($true)", "receipt_flush_failed"),
        )
        for stage in stages:
            for expression, expected_reason in expressions:
                with self.subTest(stage=stage, expression=expression), tempfile.TemporaryDirectory(
                    prefix="g8-022-runtime-stage-receipt-"
                ) as temporary:
                    root = Path(temporary).resolve()
                    receipt = root / "execution.receipt.txt"
                    attempts = root / "ssh-attempts.txt"
                    fake_ssh = root / "ssh.cmd"
                    fake_ssh.write_text(f'@echo attempted>>"{attempts}"\n@exit /b 0\n', encoding="ascii")
                    command = module.build_command(
                        AUDITOR_PATH.read_bytes(),
                        receipt_path=str(receipt),
                        test_scenario="fake_ssh",
                        test_ssh_path=str(fake_ssh),
                    )
                    injected = command.replace(
                        expression,
                        f"if ($line -eq '{stage}') {{ throw 'DO_NOT_ECHO_STAGE_RECEIPT_FAILURE' }}; {expression}",
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
                        timeout=30,
                    )
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(result.stderr, "")
                    self.assertFalse(attempts.exists())
                    self.assertNotIn("G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES", result.stdout)
                    if receipt.exists():
                        self.assertNotIn(
                            "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES",
                            receipt.read_text(encoding="utf-8"),
                        )
                    if stage.endswith("PRE_SSH_GATE=PASS"):
                        self.assertNotIn("G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS", result.stdout)
                    else:
                        self.assertIn("G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS", result.stdout)
                    self.assertIn(
                        f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={expected_reason} exit_code=2",
                        result.stdout,
                    )
                    self.assertIn("OBSERVED=2", result.stdout)
                    self.assertNotIn("DO_NOT_ECHO_STAGE_RECEIPT_FAILURE", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "PowerShell 5.1 动态回归只在原生 Windows 门禁执行。")
    def test_receipt_and_parent_state_survive_null_and_write_failures(self) -> None:
        """Null 偏好及回执写/刷盘故障都必须固定低敏停止并保留父窗口。"""
        module = load_module()
        for expression, expected_reason in (
            ("$g8ReceiptWriter.WriteLine($line)", "receipt_write_failed"),
            ("$g8ReceiptWriter.Flush()", "receipt_flush_failed"),
            ("$g8ReceiptStream.Flush($true)", "receipt_flush_failed"),
        ):
            with self.subTest(expression=expression), tempfile.TemporaryDirectory(
                prefix="g8-022-runtime-receipt-"
            ) as temporary:
                receipt = Path(temporary).resolve() / "execution.receipt.txt"
                command = module.build_command(
                    AUDITOR_PATH.read_bytes(),
                    receipt_path=str(receipt),
                    test_scenario="fail_before_ssh",
                )
                injected = command.replace(expression, "throw 'DO_NOT_ECHO_RECEIPT_FAILURE'", 1)
                probe = "$ErrorActionPreference=$null\n" + injected
                result = subprocess.run(
                    [
                        r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                        "-NoProfile",
                        "-NonInteractive",
                        "-Command",
                        "$source=[Console]::In.ReadToEnd(); & ([scriptblock]::Create($source)); Write-Output ('OBSERVED=' + $LASTEXITCODE); Write-Output ('PREFERENCE=' + $ErrorActionPreference)",
                    ],
                    input=probe,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    check=False,
                    timeout=30,
                )
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stderr, "")
                self.assertEqual(
                    result.stdout.splitlines(),
                    [
                        f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={expected_reason} exit_code=2",
                        "OBSERVED=2",
                        "PREFERENCE=Continue",
                    ],
                )
                self.assertNotIn("DO_NOT_ECHO_RECEIPT_FAILURE", result.stdout + result.stderr)
                self.assertNotIn("SSH_ATTEMPTED", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "假 SSH 动态回归只在原生 Windows 门禁执行。")
    def test_fake_ssh_records_exact_attempt_boundary_without_network(self) -> None:
        """本地假 SSH 只执行一次，并把尝试边界与最终结果耐久写入回执。"""
        module = load_module()
        for fake_exit, expected in (
            (0, "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=PASS exit_code=0"),
            (23, "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason=ssh_session_failed exit_code=2"),
        ):
            with self.subTest(fake_exit=fake_exit), tempfile.TemporaryDirectory(prefix="g8-022-runtime-fake-") as temporary:
                root = Path(temporary).resolve()
                receipt = root / "execution.receipt.txt"
                fake_ssh = root / "ssh.cmd"
                attempts = root / "ssh-attempts.txt"
                fake_ssh.write_text(
                    f'@echo attempted>>"{attempts}"\n@exit /b {fake_exit}\n',
                    encoding="ascii",
                )
                command = module.build_command(
                    AUDITOR_PATH.read_bytes(),
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
                    timeout=30,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stderr, "")
                lines = receipt.read_text(encoding="utf-8").splitlines()
                self.assertEqual(attempts.read_text(encoding="ascii").splitlines(), ["attempted"])
                self.assertEqual(lines.count("G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES"), 1)
                self.assertEqual(lines[-1], expected)
                self.assertIn(expected, result.stdout)

    @unittest.skipUnless(os.name == "nt", "最终回执故障动态回归只在原生 Windows 门禁执行。")
    def test_final_receipt_failure_overrides_fake_ssh_success_without_retry(self) -> None:
        """SSH 后最终结果无法耐久写入时必须返回失败，且不得再次调用假 SSH。"""
        module = load_module()
        for expression, expected_reason in (
            ("$g8ReceiptWriter.WriteLine($line)", "receipt_write_failed"),
            ("$g8ReceiptWriter.Flush()", "receipt_flush_failed"),
            ("$g8ReceiptStream.Flush($true)", "receipt_flush_failed"),
        ):
            with self.subTest(expression=expression), tempfile.TemporaryDirectory(
                prefix="g8-022-runtime-final-receipt-"
            ) as temporary:
                root = Path(temporary).resolve()
                receipt = root / "execution.receipt.txt"
                attempts = root / "ssh-attempts.txt"
                fake_ssh = root / "ssh.cmd"
                fake_ssh.write_text(
                    f'@echo attempted>>"{attempts}"\n@exit /b 0\n',
                    encoding="ascii",
                )
                command = module.build_command(
                    AUDITOR_PATH.read_bytes(),
                    receipt_path=str(receipt),
                    test_scenario="fake_ssh",
                    test_ssh_path=str(fake_ssh),
                )
                injected = command.replace(
                    expression,
                    f"if ($line -like '*HOST_RESULT=PASS*') {{ throw 'DO_NOT_ECHO_FINAL_RECEIPT_FAILURE' }}; {expression}",
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
                    timeout=30,
                )
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stderr, "")
                self.assertEqual(attempts.read_text(encoding="ascii").splitlines(), ["attempted"])
                self.assertEqual(
                    result.stdout.splitlines(),
                    [
                        "G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS",
                        "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES",
                        f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={expected_reason} exit_code=2",
                        "OBSERVED=2",
                    ],
                )
                self.assertNotIn("DO_NOT_ECHO_FINAL_RECEIPT_FAILURE", result.stdout + result.stderr)

    @unittest.skipUnless(os.name == "nt", "PowerShell 语法回归只在原生 Windows 门禁执行。")
    def test_formal_command_has_valid_powershell_51_syntax(self) -> None:
        """正式生成物必须由 Windows PowerShell 5.1 完整解析。"""
        module = load_module()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=module.TRUSTED_LOCAL_APPDATA_RECEIPT)
        result = subprocess.run(
            [
                r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$source=[Console]::In.ReadToEnd(); [scriptblock]::Create($source) | Out-Null",
            ],
            input=command,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")

    def test_self_test_is_offline_and_cli_has_no_receipt_override(self) -> None:
        """自检不得联网，正式 CLI 必须固定可信用户目录回执。"""
        result = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_RUNTIME_AUDIT_022_COMMAND_SELF_TEST=PASS")
        with tempfile.TemporaryDirectory(prefix="g8-022-runtime-formal-") as temporary:
            output = Path(temporary).resolve() / "authorized-command.ps1"
            generated = subprocess.run(
                ["python", "-I", str(SCRIPT_PATH), f"--change-id={CHANGE_ID}", f"--output-file={output}"],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
                timeout=30,
            )
            self.assertEqual(generated.returncode, 0, generated.stderr)
            command = output.read_text(encoding="utf-8")
            self.assertIn(".g8-022-runtime-audit-", command)
            self.assertIn("[Guid]::NewGuid().ToString('N')", command)
            self.assertIn("[IO.FileMode]::CreateNew", command)
            rejected = subprocess.run(
                [
                    "python",
                    "-I",
                    str(SCRIPT_PATH),
                    f"--change-id={CHANGE_ID}",
                    f"--output-file={output}.second",
                    "--receipt-file=forbidden",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
                timeout=30,
            )
            self.assertEqual(rejected.returncode, 2)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 012 Drop 暂存只读取证的低敏输出契约。"""

from __future__ import annotations

import importlib.util
import base64
import hashlib
import io
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name(
    "run-ai-gateway-g8-test-drop-staging-evidence-012.py"
)
REPO_ROOT = Path(__file__).resolve().parents[2]


def load_module():
    """从固定同目录路径加载生产脚本，缺失时形成清晰的 RED 断言。"""

    if not SCRIPT_PATH.is_file():
        raise AssertionError(f"生产脚本尚未创建：{SCRIPT_PATH.name}")
    spec = importlib.util.spec_from_file_location("g8_drop_staging_evidence_012", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise AssertionError("无法构造 012 生产脚本加载器")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class TestDropStagingEvidence012Contract(unittest.TestCase):
    """锁定调用方可见的九键三态契约。"""

    def test_parser_accepts_exact_absent_state(self):
        """错误删除 ABSENT 合法分支时，本测试必须失败。"""

        module = load_module()
        stdout = "\n".join(
            (
                f"EVIDENCE_CHANGE_ID={module.CHANGE_ID}",
                f"TARGET_CHANGE_ID={module.TARGET_CHANGE_ID}",
                "LOGIN_USER=pc",
                "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
                "DEPLOYMENT_ROOT_CHECK=PASS",
                "STAGING_STATE=ABSENT",
                "STAGING_INTEGRITY=NOT_APPLICABLE",
                "STAGING_MISMATCH_REASON=NONE",
                "EVIDENCE_RESULT=PASS",
            )
        )

        values = module.parse_remote_output(stdout)

        self.assertEqual(values["STAGING_STATE"], "ABSENT")
        self.assertEqual(values["STAGING_INTEGRITY"], "NOT_APPLICABLE")
        self.assertEqual(values["STAGING_MISMATCH_REASON"], "NONE")

    def test_parser_accepts_present_pass_and_each_fixed_mismatch(self):
        """误删 PASS 或任一固定 MISMATCH 分类时，本测试必须失败。"""

        module = load_module()
        states = (
            ("PRESENT", "PASS", "NONE"),
            *(("PRESENT", "MISMATCH", reason) for reason in (
                "PATH",
                "FILE_SET",
                "FILE_METADATA",
                "FILE_CONTENT",
                "MANIFEST",
                "RECEIPT",
                "READ_ERROR",
            )),
        )
        for state, integrity, reason in states:
            with self.subTest(state=state, integrity=integrity, reason=reason):
                stdout = "\n".join(
                    (
                        f"EVIDENCE_CHANGE_ID={module.CHANGE_ID}",
                        f"TARGET_CHANGE_ID={module.TARGET_CHANGE_ID}",
                        "LOGIN_USER=pc",
                        "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
                        "DEPLOYMENT_ROOT_CHECK=PASS",
                        f"STAGING_STATE={state}",
                        f"STAGING_INTEGRITY={integrity}",
                        f"STAGING_MISMATCH_REASON={reason}",
                        "EVIDENCE_RESULT=PASS",
                    )
                )

                try:
                    values = module.parse_remote_output(stdout)
                except module.EvidenceError as error:
                    self.fail(f"合法三态被拒绝：{error}")

                self.assertEqual(
                    (
                        values["STAGING_STATE"],
                        values["STAGING_INTEGRITY"],
                        values["STAGING_MISMATCH_REASON"],
                    ),
                    (state, integrity, reason),
                )

    def test_remote_program_omits_physical_host_identity(self):
        """误恢复物理主机身份读取或遗漏固定路径时，本测试必须失败。"""

        module = load_module()

        try:
            program = module.build_remote_program()
        except AttributeError as error:
            self.fail(f"尚未提供远端程序接口：{error}")

        self.assertNotIn("/etc/machine-id", program)
        self.assertNotIn("HOSTNAME=", program)
        self.assertNotIn("os.uname", program)
        self.assertNotIn("instance-id", program)
        self.assertIn("/home/pc/molin", program)
        self.assertIn(module.CHANGE_ID, program)
        self.assertIn(module.TARGET_CHANGE_ID, program)

    def test_parser_rejects_malformed_or_misattributed_evidence(self):
        """放宽键集、字符集、证据归属或状态组合时，本测试必须失败。"""

        module = load_module()
        valid = "\n".join(
            (
                f"EVIDENCE_CHANGE_ID={module.CHANGE_ID}",
                f"TARGET_CHANGE_ID={module.TARGET_CHANGE_ID}",
                "LOGIN_USER=pc",
                "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
                "DEPLOYMENT_ROOT_CHECK=PASS",
                "STAGING_STATE=ABSENT",
                "STAGING_INTEGRITY=NOT_APPLICABLE",
                "STAGING_MISMATCH_REASON=NONE",
                "EVIDENCE_RESULT=PASS",
            )
        )
        malformed = (
            valid.replace("LOGIN_USER=pc\n", ""),
            valid + "\nHOSTNAME=untrusted",
            valid + "\nLOGIN_USER=pc",
            valid.replace("LOGIN_USER=pc", "LOGIN_USER=电脑"),
            valid.replace(module.CHANGE_ID, "CHG-WRONG", 1),
            valid.replace(module.TARGET_CHANGE_ID, "CHG-WRONG", 1),
            valid.replace("STAGING_INTEGRITY=NOT_APPLICABLE", "STAGING_INTEGRITY=PASS"),
            valid.replace("STAGING_MISMATCH_REASON=NONE", "STAGING_MISMATCH_REASON=PATH"),
        )

        for stdout in malformed:
            with self.subTest(stdout=stdout[-96:]):
                with self.assertRaises(module.EvidenceError):
                    module.parse_remote_output(stdout)

    def test_stream_capture_is_bounded_but_counts_the_complete_stream(self):
        """错误地把输出全部保存在内存或只统计截断片段时，本测试必须失败。"""

        module = load_module()
        payload = (b"first\n" * 40_000) + b"tail"

        try:
            capture = module.collect_stream(io.BytesIO(payload), 64 * 1024)
        except AttributeError as error:
            self.fail(f"尚未实现有界流采集接口：{error}")

        self.assertEqual(len(capture.data), 64 * 1024 + 1)
        self.assertEqual(capture.byte_count, len(payload))
        self.assertEqual(capture.line_count, payload.count(b"\n") + 1)
        self.assertEqual(capture.sha256, hashlib.sha256(payload).hexdigest())
        self.assertTrue(capture.exceeded)
        self.assertFalse(capture.error)

    def test_stream_capture_converts_read_errors_to_internal_flag(self):
        """读取异常若向 stderr 泄漏 traceback 或继续当作成功，本测试必须失败。"""

        module = load_module()

        class BrokenStream:
            def read(self, _size):
                raise OSError("DO_NOT_ECHO_LOCAL_PATH")

        capture = module.collect_stream(BrokenStream(), 64 * 1024)

        self.assertTrue(capture.error)
        self.assertEqual(capture.data, b"")

    def test_file_evidence_detects_local_material_drift(self):
        """身份材料冻结后被替换却仍放行时，本测试必须失败。"""

        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-012-evidence-") as directory:
            path = Path(directory) / "known_hosts"
            path.write_bytes(b"approved\n")
            evidence = module.freeze_file(path)
            path.write_bytes(b"replaced\n")

            with self.assertRaises(module.EvidenceError):
                module.assert_file_unchanged(evidence)

    def test_freeze_file_rejects_relative_paths(self):
        """相对身份材料路径被静默解析时，本测试必须失败。"""

        module = load_module()

        with self.assertRaises(module.EvidenceError):
            module.freeze_file(Path("relative-known-hosts"))

    def test_freeze_file_rejects_lstat_open_replacement_race(self):
        """lstat 与 open 之间替换同名文件仍通过冻结时，本测试必须失败。"""

        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-012-open-race-") as directory:
            path = Path(directory) / "identity"
            old = Path(directory) / "identity.old"
            path.write_bytes(b"approved\n")
            real_open = module.os.open
            replaced = False

            def replace_then_open(target, flags, *args, **kwargs):
                nonlocal replaced
                if not replaced and Path(target) == path:
                    replaced = True
                    path.replace(old)
                    path.write_bytes(b"malicious\n")
                return real_open(target, flags, *args, **kwargs)

            with mock.patch.object(module.os, "open", side_effect=replace_then_open):
                with self.assertRaises(module.EvidenceError):
                    module.freeze_file(path)

    def test_known_hosts_requires_one_approved_endpoint_key(self):
        """固定端点出现额外明文或哈希密钥仍通过时，本测试必须失败。"""

        module = load_module()
        approved = "[8.130.9.163]:10003 ssh-ed25519 AAAAAPPROVED"
        malicious = "|1|hash|value ssh-ed25519 AAAAMALICIOUS"

        def fake_runner(command, **_kwargs):
            if "-F" in command:
                return subprocess.CompletedProcess(command, 0, approved + "\n" + malicious + "\n", "")
            raise AssertionError(f"未预期的工具调用：{command}")

        with self.assertRaises(module.EvidenceError):
            module.validate_known_hosts(
                Path("known_hosts"),
                Path("ssh-keygen"),
                expected_fingerprint="SHA256:approved",
                tool_runner=fake_runner,
                fingerprint_reader=lambda _line: "SHA256:approved",
            )

    def test_local_check_never_starts_ssh(self):
        """离线检查触发 SSH 或输出不稳定时，本测试必须失败。"""

        module = load_module()
        arguments = [
            "--local-check",
            "--known-hosts", "known_hosts",
            "--identity-file", "id_ed25519",
            "--identity-public-file", "id_ed25519.pub",
        ]
        with (
            mock.patch.object(module, "freeze_local_inputs", return_value=object()),
            mock.patch.object(module, "run_once") as run_once,
            mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]),
            mock.patch("sys.stdout", new_callable=io.StringIO) as stdout,
            mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
        ):
            code = module.main()

        self.assertEqual(code, 0)
        self.assertEqual(
            stdout.getvalue(),
            "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_LOCAL_CHECK=PASS\n",
        )
        self.assertEqual(stderr.getvalue(), "")
        run_once.assert_not_called()

    def make_local_inputs(self, module, directory: Path):
        """建立只供假进程测试使用的本地冻结材料。"""

        paths = {}
        for name in ("ssh", "ssh-keygen", "known_hosts", "id_ed25519", "id_ed25519.pub"):
            path = directory / name
            path.write_bytes((name + "\n").encode("ascii"))
            paths[name] = module.freeze_file(path)
        return module.LocalInputs(
            ssh=paths["ssh"],
            ssh_keygen=paths["ssh-keygen"],
            known_hosts=paths["known_hosts"],
            identity_file=paths["id_ed25519"],
            identity_public_file=paths["id_ed25519.pub"],
            approved_known_hosts_line=(
                "[8.130.9.163]:10003 ssh-ed25519 "
                "AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
            ),
        )

    @staticmethod
    def valid_remote_stdout(module) -> bytes:
        """生成严格九键 ABSENT 成功输出。"""

        return ("\n".join((
            f"EVIDENCE_CHANGE_ID={module.CHANGE_ID}",
            f"TARGET_CHANGE_ID={module.TARGET_CHANGE_ID}",
            "LOGIN_USER=pc",
            "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
            "DEPLOYMENT_ROOT_CHECK=PASS",
            "STAGING_STATE=ABSENT",
            "STAGING_INTEGRITY=NOT_APPLICABLE",
            "STAGING_MISMATCH_REASON=NONE",
            "EVIDENCE_RESULT=PASS",
        )) + "\n").encode("ascii")

    def fake_process(self, stdout: bytes, stderr: bytes = b"", returncode: int = 0):
        """构造支持 stdin、双输出流和 wait 的无网络假进程。"""

        class RecordingInput(io.BytesIO):
            def close(self):
                self.was_closed = True

        class FakeProcess:
            def __init__(self):
                self.stdin = RecordingInput()
                self.stdout = io.BytesIO(stdout)
                self.stderr = io.BytesIO(stderr)
                self.returncode = returncode
                self.killed = False

            def wait(self, timeout=None):
                self.timeout = timeout
                return self.returncode

            def kill(self):
                self.killed = True

        return FakeProcess()

    def test_run_once_uses_one_fixed_ssh_and_parses_evidence(self):
        """增加第二进程、放宽SSH参数或不解析输出时，本测试必须失败。"""

        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-012-run-") as directory:
            inputs = self.make_local_inputs(module, Path(directory))
            process = self.fake_process(self.valid_remote_stdout(module))
            with mock.patch.object(module.subprocess, "Popen", return_value=process) as popen:
                values = module.run_once(inputs)

        popen.assert_called_once()
        command = popen.call_args.args[0]
        self.assertEqual(command[0], str(inputs.ssh.path))
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", command)
        self.assertEqual(
            command[-1],
            "/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -",
        )
        self.assertEqual(values["STAGING_STATE"], "ABSENT")
        self.assertTrue(process.stdin.was_closed)

    def test_run_once_rejects_post_ssh_material_drift(self):
        """SSH结束后材料被替换却仍形成证据时，本测试必须失败。"""

        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-012-drift-") as directory:
            inputs = self.make_local_inputs(module, Path(directory))

            def mutate_then_start(*_args, **_kwargs):
                inputs.known_hosts.path.write_bytes(b"replaced\n")
                return self.fake_process(self.valid_remote_stdout(module))

            with mock.patch.object(module.subprocess, "Popen", side_effect=mutate_then_start):
                with self.assertRaises(module.EvidenceError):
                    module.run_once(inputs)

    def test_run_once_rejects_stderr_and_nonzero_without_echo(self):
        """远端异常正文、非零状态或超限输出被接受时，本测试必须失败。"""

        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-012-failure-") as directory:
            inputs = self.make_local_inputs(module, Path(directory))
            process = self.fake_process(b"", b"DO_NOT_ECHO_REMOTE_SECRET\n", 23)
            with mock.patch.object(module.subprocess, "Popen", return_value=process):
                with self.assertRaises(module.EvidenceError) as raised:
                    module.run_once(inputs)

        self.assertNotIn("DO_NOT_ECHO", str(raised.exception))

    def test_freeze_local_inputs_validates_key_pair_and_rejects_mismatch(self):
        """客户端公私钥不一致仍能通过本地门禁时，本测试必须失败。"""

        module = load_module()
        key_blob = base64.b64encode(b"approved-client-key").decode("ascii")
        public_line = f"ssh-ed25519 {key_blob} local-test"
        endpoint_line = f"{module.TARGET_HOST_ALIAS} ssh-ed25519 {key_blob}"
        expected_fingerprint = module.ssh_fingerprint(public_line)
        with tempfile.TemporaryDirectory(prefix="g8-012-identity-") as directory:
            root = Path(directory)
            paths = {}
            for name, content in {
                "ssh": b"ssh\n",
                "ssh-keygen": b"ssh-keygen\n",
                "known_hosts": (endpoint_line + "\n").encode("ascii"),
                "id_ed25519": b"private\n",
                "id_ed25519.pub": (public_line + "\n").encode("ascii"),
            }.items():
                path = root / name
                path.write_bytes(content)
                paths[name] = path

            tool_environments = []

            def runner(command, **kwargs):
                tool_environments.append(kwargs.get("env"))
                if "-F" in command:
                    return subprocess.CompletedProcess(command, 0, endpoint_line + "\n", "")
                if "-y" in command:
                    return subprocess.CompletedProcess(command, 0, " ".join(public_line.split()[:2]) + "\n", "")
                raise AssertionError(command)

            inputs = module.freeze_local_inputs(
                paths["known_hosts"], paths["id_ed25519"], paths["id_ed25519.pub"],
                ssh_path=paths["ssh"], ssh_keygen_path=paths["ssh-keygen"],
                tool_runner=runner,
                expected_host_fingerprint=expected_fingerprint,
                expected_identity_fingerprint=expected_fingerprint,
            )
            self.assertEqual(inputs.approved_known_hosts_line, endpoint_line)
            self.assertTrue(tool_environments)
            self.assertTrue(all(environment is not None for environment in tool_environments))
            self.assertTrue(all("SSH_AUTH_SOCK" not in environment for environment in tool_environments))
            self.assertTrue(all("SSH_ASKPASS" not in environment for environment in tool_environments))

            def mismatch_runner(command, **kwargs):
                completed = runner(command, **kwargs)
                if "-y" in command:
                    completed.stdout = "ssh-ed25519 AAAAWRONG\n"
                return completed

            with self.assertRaises(module.EvidenceError):
                module.freeze_local_inputs(
                    paths["known_hosts"], paths["id_ed25519"], paths["id_ed25519.pub"],
                    ssh_path=paths["ssh"], ssh_keygen_path=paths["ssh-keygen"],
                    tool_runner=mismatch_runner,
                    expected_host_fingerprint=expected_fingerprint,
                    expected_identity_fingerprint=expected_fingerprint,
                )

    def test_consumed_gate_rejects_every_cli_before_material_or_network(self):
        """消费态任一 argv 到达解析器、材料或网络时，本测试必须失败。"""

        module = load_module()
        invocations = (
            [],
            ["--help"],
            ["--unknown", "DO_NOT_ECHO_SECRET_SENTINEL"],
            ["--known-hosts"],
            ["--self-test"],
            ["--local-check"],
            ["--change-id", module.CHANGE_ID],
        )
        for arguments in invocations:
            with self.subTest(arguments=arguments):
                with (
                    mock.patch.object(module, "CHANGE_ID_CONSUMED", True),
                    mock.patch.object(module, "build_argument_parser") as parser,
                    mock.patch.object(module, "freeze_local_inputs") as freeze,
                    mock.patch.object(module, "run_once") as run_once,
                    mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]),
                    mock.patch("sys.stdout", new_callable=io.StringIO) as stdout,
                    mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
                ):
                    code = module.main()
            self.assertEqual(code, 2)
            self.assertEqual(
                stdout.getvalue(),
                "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=change_id_consumed\n",
            )
            self.assertEqual(stderr.getvalue(), "")
            self.assertNotIn("DO_NOT_ECHO", stdout.getvalue())
            parser.assert_not_called()
            freeze.assert_not_called()
            run_once.assert_not_called()

    def test_main_reports_exact_pass_mismatch_and_self_test(self):
        """主入口状态码或首行契约漂移时，本测试必须失败。"""

        module = load_module()
        base_arguments = ["--change-id", module.CHANGE_ID, "--known-hosts", "C:/kh", "--identity-file", "C:/id", "--identity-public-file", "C:/id.pub"]
        cases = (
            (["--self-test"], None, 0, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_SELF_TEST=PASS\n"),
            (base_arguments, ("ABSENT", "NOT_APPLICABLE", "NONE"), 0, "PASS"),
            (base_arguments, ("PRESENT", "PASS", "NONE"), 0, "PASS"),
            (base_arguments, ("PRESENT", "MISMATCH", "FILE_CONTENT"), 3, "MISMATCH"),
        )
        for arguments, state, expected_code, headline in cases:
            values = None
            expected_stdout = "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_SELF_TEST=PASS\n"
            if state is not None:
                staging_state, integrity, reason = state
                values = {
                    "STAGING_STATE": staging_state,
                    "STAGING_INTEGRITY": integrity,
                    "STAGING_MISMATCH_REASON": reason,
                }
                expected_stdout = "\n".join((
                    f"G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012={headline}",
                    f"change_id={module.CHANGE_ID}",
                    f"target_change_id={module.TARGET_CHANGE_ID}",
                    f"staging_state={staging_state}",
                    f"staging_integrity={integrity}",
                    f"staging_mismatch_reason={reason}",
                    "",
                ))
            with self.subTest(state=state):
                with (
                    mock.patch.object(module, "freeze_local_inputs", return_value=object()),
                    mock.patch.object(module, "run_once", return_value=values),
                    mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]),
                    mock.patch("sys.stdout", new_callable=io.StringIO) as stdout,
                    mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
                ):
                    code = module.main()
            self.assertEqual(code, expected_code)
            self.assertEqual(stdout.getvalue(), expected_stdout)
            self.assertEqual(stderr.getvalue(), "")

    def test_main_collapses_internal_errors_to_evidence_unavailable(self):
        """正式失败回显内部分类或本地信息时，本测试必须失败。"""

        module = load_module()
        arguments = [
            "--change-id", module.CHANGE_ID,
            "--known-hosts", "C:/known_hosts",
            "--identity-file", "C:/id_ed25519",
            "--identity-public-file", "C:/id_ed25519.pub",
        ]
        with (
            mock.patch.object(module, "freeze_local_inputs", return_value=object()),
            mock.patch.object(module, "run_once", side_effect=module.EvidenceError("DO_NOT_ECHO_INTERNAL")),
            mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]),
            mock.patch("sys.stdout", new_callable=io.StringIO) as stdout,
            mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
        ):
            code = module.main()

        self.assertEqual(code, 2)
        self.assertEqual(
            stdout.getvalue(),
            "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=evidence_unavailable\n",
        )
        self.assertEqual(stderr.getvalue(), "")

    def test_ci_runs_012_on_windows_and_linux_without_network(self):
        """CI 遗漏 012、断网 Linux 动态测试或离线自检时，本测试必须失败。"""

        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")

        self.assertIn(
            "test_run_ai_gateway_g8_test_drop_staging_evidence_012.py",
            workflow,
        )
        self.assertIn(
            "run-ai-gateway-g8-test-drop-staging-evidence-012.py --self-test",
            workflow,
        )
        self.assertIn("python:3.13-alpine", workflow)
        self.assertIn("--network none", workflow)


@unittest.skipUnless(os.name == "posix", "目录描述符动态取证只在 Linux CI 执行")
class TestDropStagingEvidence012Posix(unittest.TestCase):
    """在真实临时文件系统中验证 012 远端程序。"""

    def setUp(self) -> None:
        self.module = load_module()
        self.temporary = tempfile.TemporaryDirectory(prefix="g8-drop-012-")
        self.root = Path(self.temporary.name) / "molin"
        self.stage = self.root / ".stage-011"
        self.root.mkdir(mode=0o700)
        self.manifest_values = {
            "CHANGE_ID": self.module.TARGET_CHANGE_ID,
            "TARGET_DEPLOYMENT_ROOT": str(self.root),
        }
        self.contents = {
            "ai-gateway-reconcile": b"reconcile\n",
            "g8-test-readonly-audit": b"audit\n",
            "manifest.env": (
                f"CHANGE_ID={self.module.TARGET_CHANGE_ID}\n"
                f"TARGET_DEPLOYMENT_ROOT={self.root}\n"
            ).encode("ascii"),
            "molin-g8-test-readonly-audit.sudoers": b"sudoers\n",
        }
        checksum_names = (
            "ai-gateway-reconcile",
            "g8-test-readonly-audit",
            "manifest.env",
            "molin-g8-test-readonly-audit.sudoers",
        )
        self.checksums = {
            name: hashlib.sha256(self.contents[name]).hexdigest()
            for name in checksum_names
        }
        self.contents["SHA256SUMS"] = "".join(
            f"{self.checksums[name]}  {name}\n" for name in checksum_names
        ).encode("ascii")
        modes = {
            "SHA256SUMS": 0o600,
            "ai-gateway-reconcile": 0o700,
            "g8-test-readonly-audit": 0o700,
            "manifest.env": 0o600,
            "molin-g8-test-readonly-audit.sudoers": 0o600,
        }
        self.expected = {
            name: (hashlib.sha256(content).hexdigest(), len(content), modes[name])
            for name, content in self.contents.items()
        }

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def create_valid_stage(self) -> None:
        """创建与测试冻结值完全一致的五文件暂存。"""

        self.stage.mkdir(mode=0o700)
        for name, content in self.contents.items():
            path = self.stage / name
            path.write_bytes(content)
            path.chmod(self.expected[name][2])

    def reset_valid_stage(self) -> None:
        """只清理测试临时目录并重新建立合法暂存。"""

        if self.stage.is_symlink():
            self.stage.unlink()
        elif self.stage.exists():
            shutil.rmtree(self.stage)
        self.create_valid_stage()

    def run_remote(self) -> dict[str, str]:
        """执行真实远端程序并解析严格九键输出。"""

        try:
            program = self.module.build_remote_program(
                deployment_root=str(self.root),
                staging_path=str(self.stage),
                expected_files=self.expected,
                expected_manifest=self.manifest_values,
                expected_checksums=self.checksums,
                _test_uid=os.getuid(),
                _test_gid=os.getgid(),
            )
        except TypeError as error:
            self.fail(f"远端程序尚未支持可测试的冻结输入：{error}")
        completed = subprocess.run(
            [sys.executable, "-I", "-c", program],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=10,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(completed.stderr, "")
        return self.module.parse_remote_output(
            completed.stdout,
            expected_deployment_root=str(self.root),
        )

    def build_remote(self) -> str:
        """生成绑定当前测试临时目录的远端程序。"""

        return self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            expected_manifest=self.manifest_values,
            expected_checksums=self.checksums,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
        )

    def execute_program(self, program: str) -> subprocess.CompletedProcess[str]:
        """执行注入竞态后的测试程序，不隐藏退出码或输出。"""

        return subprocess.run(
            [sys.executable, "-I", "-c", program],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=10,
            check=False,
        )

    def test_remote_program_reports_absent_and_present_pass(self):
        """删除目录读取或完整文件通过分支时，本测试必须失败。"""

        absent = self.run_remote()
        self.assertEqual(
            (
                absent["STAGING_STATE"],
                absent["STAGING_INTEGRITY"],
                absent["STAGING_MISMATCH_REASON"],
            ),
            ("ABSENT", "NOT_APPLICABLE", "NONE"),
        )

        self.create_valid_stage()
        present = self.run_remote()
        self.assertEqual(
            (
                present["STAGING_STATE"],
                present["STAGING_INTEGRITY"],
                present["STAGING_MISMATCH_REASON"],
            ),
            ("PRESENT", "PASS", "NONE"),
        )

    def test_remote_program_classifies_each_fixed_mismatch(self):
        """弱化任一五文件失败分类时，本测试必须失败。"""

        def change_artifact() -> None:
            path = self.stage / "g8-test-readonly-audit"
            path.write_bytes(b"budit\n")
            path.chmod(0o700)

        def change_manifest() -> None:
            path = self.stage / "manifest.env"
            content = path.read_bytes()
            path.write_bytes(content[:-2] + b"x\n")
            path.chmod(0o600)

        def change_receipt() -> None:
            path = self.stage / "SHA256SUMS"
            content = bytearray(path.read_bytes())
            content[0] = ord("0") if content[0] != ord("0") else ord("1")
            path.write_bytes(content)
            path.chmod(0o600)

        def replace_manifest_with_symlink() -> None:
            path = self.stage / "manifest.env"
            path.unlink()
            path.symlink_to(self.stage / "SHA256SUMS")

        mutations = (
            ("file_set", "FILE_SET", lambda: (self.stage / "extra").write_bytes(b"extra")),
            ("file_mode", "FILE_METADATA", lambda: (self.stage / "manifest.env").chmod(0o622)),
            ("file_content", "FILE_CONTENT", change_artifact),
            ("manifest", "MANIFEST", change_manifest),
            ("receipt", "RECEIPT", change_receipt),
            ("file_symlink", "FILE_METADATA", replace_manifest_with_symlink),
        )
        for case_name, expected_reason, mutate in mutations:
            with self.subTest(case_name=case_name, expected_reason=expected_reason):
                self.reset_valid_stage()
                mutate()
                values = self.run_remote()
                self.assertEqual(values["STAGING_INTEGRITY"], "MISMATCH")
                self.assertEqual(values["STAGING_MISMATCH_REASON"], expected_reason)

    def test_remote_program_classifies_stage_symlink_as_path(self):
        """跟随暂存目录链接并误报完整时，本测试必须失败。"""

        alternate = self.root / ".alternate"
        alternate.mkdir(mode=0o700)
        self.stage.symlink_to(alternate, target_is_directory=True)

        values = self.run_remote()

        self.assertEqual(values["STAGING_INTEGRITY"], "MISMATCH")
        self.assertEqual(values["STAGING_MISMATCH_REASON"], "PATH")

    def test_remote_program_detects_stage_file_and_root_drift(self):
        """删除任一最终元数据复核时，本测试至少有一个子例失败。"""

        injections = {
            "stage_mode": (
                "                stage_fd = os.open(\n",
                "                os.chmod(staging_path, 0o777)\n"
                "                stage_fd = os.open(\n",
                0,
                "PATH",
            ),
            "file_entry": (
                "                            final_names = os.listdir(stage_fd)\n",
                "                            old_name = 'manifest.env.old'\n"
                "                            os.rename('manifest.env', old_name, src_dir_fd=stage_fd, dst_dir_fd=stage_fd)\n"
                "                            replacement_fd = os.open('manifest.env', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600, dir_fd=stage_fd)\n"
                "                            os.close(replacement_fd)\n"
                "                            final_names = os.listdir(stage_fd)\n",
                0,
                "PATH",
            ),
            "root_mode": (
                "    final_root = os.fstat(root_fd)\n",
                "    os.chmod(deployment_root, 0o777)\n"
                "    final_root = os.fstat(root_fd)\n",
                41,
                None,
            ),
        }
        for name, (needle, replacement, expected_code, expected_reason) in injections.items():
            with self.subTest(name=name):
                self.reset_valid_stage()
                program = self.build_remote()
                self.assertIn(needle, program)
                program = program.replace(needle, replacement, 1)
                try:
                    completed = self.execute_program(program)
                finally:
                    self.root.chmod(0o700)
                    if self.stage.exists() and not self.stage.is_symlink():
                        self.stage.chmod(0o700)
                self.assertEqual(completed.returncode, expected_code, completed.stderr)
                self.assertEqual(completed.stderr, "")
                if expected_reason is None:
                    self.assertEqual(completed.stdout, "")
                else:
                    values = self.module.parse_remote_output(
                        completed.stdout,
                        expected_deployment_root=str(self.root),
                    )
                    self.assertEqual(values["STAGING_INTEGRITY"], "MISMATCH")
                    self.assertEqual(values["STAGING_MISMATCH_REASON"], expected_reason)

    def test_remote_program_classifies_system_read_failure(self):
        """真实系统读取异常未归入 READ_ERROR 时，本测试必须失败。"""

        self.create_valid_stage()
        program = self.build_remote()
        needle = "                                file_fd = os.open(\n"
        self.assertIn(needle, program)
        program = program.replace(
            needle,
            "                                raise OSError('injected read failure')\n"
            "                                file_fd = os.open(\n",
            1,
        )

        completed = self.execute_program(program)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(completed.stderr, "")
        values = self.module.parse_remote_output(
            completed.stdout,
            expected_deployment_root=str(self.root),
        )
        self.assertEqual(values["STAGING_INTEGRITY"], "MISMATCH")
        self.assertEqual(values["STAGING_MISMATCH_REASON"], "READ_ERROR")


if __name__ == "__main__":
    unittest.main()

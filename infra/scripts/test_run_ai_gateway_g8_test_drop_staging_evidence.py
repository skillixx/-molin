#!/usr/bin/env python3
"""验证 G8 Drop 映射场景的暂存只读取证契约。"""

import importlib.util
import hashlib
import os
import subprocess
import sys
import tempfile
import time
import types
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name(
    "run-ai-gateway-g8-test-drop-staging-evidence.py"
)
REPO_ROOT = Path(__file__).resolve().parents[2]


def load_module():
    """从精确脚本路径加载模块，避免从 PATH 或其他目录寻找替代实现。"""
    if not SCRIPT_PATH.is_file():
        raise AssertionError("008 Drop 暂存取证脚本尚未实现")
    spec = importlib.util.spec_from_file_location("g8_drop_staging_evidence", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise AssertionError("008 Drop 暂存取证脚本无法加载")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class TestDropStagingEvidenceContract(unittest.TestCase):
    def test_ci_runs_windows_and_linux_no_network_gates(self) -> None:
        """分级 CI 必须同时运行本机门禁和 Linux 无网络动态取证。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(
            encoding="utf-8"
        )
        self.assertIn(
            "test_run_ai_gateway_g8_test_drop_staging_evidence.py", workflow
        )
        self.assertIn("python:3.13-alpine", workflow)
        self.assertIn("--network none", workflow)
        self.assertIn(
            "run-ai-gateway-g8-test-drop-staging-evidence.py --self-test",
            workflow,
        )

    @staticmethod
    def valid_absent_output(module) -> str:
        """生成严格九键的暂存不存在结果，供负例逐项变异。"""
        return "\n".join(
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

    def test_remote_program_omits_physical_host_identity(self) -> None:
        """Drop 入口只验证传输端点与目录，不得恢复物理主机身份门禁。"""
        module = load_module()
        program = module.build_remote_program()
        for forbidden in (
            "/etc/machine-id",
            "HOSTNAME=",
            "os.uname",
            "instance-id",
        ):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, program)
        self.assertIn("deployment_root = '/home/pc/molin'", program)

    def test_parser_accepts_absent_state(self) -> None:
        """九键契约必须能严格接受暂存目录不存在的低敏结果。"""
        module = load_module()
        values = module.parse_remote_output(self.valid_absent_output(module))
        self.assertEqual(values["STAGING_STATE"], "ABSENT")

    def test_parser_rejects_identity_keys_and_invalid_combinations(self) -> None:
        """额外身份键、错误 ChangeId 和不一致三态必须全部失败关闭。"""
        module = load_module()
        valid = self.valid_absent_output(module)
        invalid_outputs = (
            valid + "\nHOSTNAME=backend",
            valid + "\nMACHINE_ID_SHA256=" + "a" * 64,
            valid.replace(
                "STAGING_INTEGRITY=NOT_APPLICABLE", "STAGING_INTEGRITY=PASS"
            ),
            valid.replace(
                f"EVIDENCE_CHANGE_ID={module.CHANGE_ID}",
                "EVIDENCE_CHANGE_ID=wrong",
            ),
            valid.replace("LOGIN_USER=pc", "LOGIN_USER=其他"),
        )
        for invalid in invalid_outputs:
            with self.subTest(tail=invalid[-80:]):
                with self.assertRaises(module.EvidenceError):
                    module.parse_remote_output(invalid)

    def test_local_check_validates_inputs_without_starting_ssh(self) -> None:
        """本地检查只验证冻结输入，不得启动 SSH。"""
        module = load_module()
        helper = types.SimpleNamespace(
            validate_known_hosts=mock.Mock(),
            validate_identity_file=mock.Mock(),
            validate_identity_pair=mock.Mock(),
        )
        arguments = [
            str(SCRIPT_PATH),
            "--local-check",
            "--change-id",
            module.CHANGE_ID,
            "--known-hosts",
            "known_hosts",
            "--identity-file",
            "id_ed25519",
            "--identity-public-file",
            "id_ed25519.pub",
        ]
        with (
            mock.patch.object(module, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(module, "load_frozen_helper", return_value=helper),
            mock.patch.object(module, "run_once") as run_once,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(module.main(), 0)
        run_once.assert_not_called()
        output.assert_called_once_with(
            "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_LOCAL_CHECK=PASS"
        )

    def test_run_once_uses_one_locked_down_ssh_process(self) -> None:
        """正式包装器只能创建一次固定参数的 OpenSSH 子进程。"""
        module = load_module()

        class FakeProcess:
            def __init__(self) -> None:
                import io

                self.stdin = io.BytesIO()
                self.stdout = io.BytesIO()
                self.stderr = io.BytesIO()
                self.returncode = 0

            def wait(self, timeout=None):
                return self.returncode

            def kill(self):
                self.returncode = -9

        fake_process = FakeProcess()
        fake_process.valid_output = self.valid_absent_output(module)
        fake_process.stdout = __import__("io").BytesIO(
            (fake_process.valid_output + "\n").encode("ascii")
        )
        helper = types.SimpleNamespace(
            TARGET_PORT="10003",
            TARGET="pc@8.130.9.163",
            fixed_ssh_executable=lambda: Path("/usr/bin/ssh"),
            fixed_ssh_environment=lambda: {"PATH": "/usr/bin:/bin"},
        )
        with mock.patch.object(
            module.subprocess, "Popen", return_value=fake_process
        ) as popen:
            result = module.run_once(
                helper,
                Path("known_hosts"),
                Path("id_ed25519"),
            )
        self.assertEqual(popen.call_count, 1)
        command = popen.call_args.args[0]
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("PasswordAuthentication=no", command)
        self.assertIn("ClearAllForwardings=yes", command)
        self.assertEqual(result["STAGING_STATE"], "ABSENT")

    def test_helper_type_digest_and_contract_drift_fail_closed(self) -> None:
        """冻结 helper 的类型、摘要和传输端点契约任一漂移都必须拒绝。"""
        module = load_module()
        helper = SCRIPT_PATH.with_name("run-ai-gateway-g8-test-staging-evidence.py")
        with tempfile.TemporaryDirectory(prefix="g8-drop-helper-") as temporary:
            root = Path(temporary)
            symlink = root / "helper-link.py"
            symlink.symlink_to(helper)
            with self.assertRaises(module.EvidenceError):
                module.load_frozen_helper(symlink)

            changed = root / "helper-changed.py"
            changed.write_bytes(helper.read_bytes() + b"\n")
            with self.assertRaises(module.EvidenceError):
                module.load_frozen_helper(changed)

            contract = root / "helper-contract.py"
            source = helper.read_bytes().replace(b'TARGET_PORT = "10003"', b'TARGET_PORT = "22"   ')
            contract.write_bytes(source)
            with mock.patch.object(
                module,
                "FROZEN_HELPER_SHA256",
                hashlib.sha256(source).hexdigest(),
            ):
                with self.assertRaises(module.EvidenceError):
                    module.load_frozen_helper(contract)

    @unittest.skipUnless(os.name == "posix", "helper 目录项竞态只在 Linux CI 执行")
    def test_helper_path_replacement_after_open_fails_closed(self) -> None:
        """读取中的 helper 即使摘要相同，目录项 inode 漂移也必须拒绝。"""
        module = load_module()
        helper = SCRIPT_PATH.with_name("run-ai-gateway-g8-test-staging-evidence.py")
        with tempfile.TemporaryDirectory(prefix="g8-drop-helper-race-") as temporary:
            candidate = Path(temporary) / "helper.py"
            source = helper.read_bytes()
            candidate.write_bytes(source)
            original_open = module.os.open

            def replace_after_open(path, flags, *args, **kwargs):
                descriptor = original_open(path, flags, *args, **kwargs)
                if Path(path) == candidate:
                    old = candidate.with_suffix(".old")
                    candidate.rename(old)
                    candidate.write_bytes(source)
                return descriptor

            with (
                mock.patch.object(module.os, "open", side_effect=replace_after_open),
                mock.patch.object(
                    module,
                    "FROZEN_HELPER_SHA256",
                    hashlib.sha256(source).hexdigest(),
                ),
                self.assertRaises(module.EvidenceError),
            ):
                module.load_frozen_helper(candidate)

    def test_stream_capture_is_bounded_and_low_sensitive_on_read_error(self) -> None:
        """管道采集必须有界，并把读取异常收敛为内部标志。"""
        module = load_module()
        import io

        exact = module.collect_stream(io.BytesIO(b"a" * module.MAX_CAPTURE_BYTES))
        exceeded = module.collect_stream(
            io.BytesIO(b"b" * (module.MAX_CAPTURE_BYTES + 1))
        )
        self.assertFalse(exact.exceeded)
        self.assertTrue(exceeded.exceeded)
        self.assertEqual(len(exceeded.data), module.MAX_CAPTURE_BYTES + 1)

        class BrokenStream:
            def read(self, size):
                raise OSError("不得回显的本地路径")

        failed = module.collect_stream(BrokenStream())
        self.assertTrue(failed.error)
        self.assertEqual(failed.data, b"")

    def test_consumed_change_rejects_before_helper_identity_or_network(self) -> None:
        """已消费 ChangeId 必须在读取 helper、身份材料或联网前拒绝。"""
        module = load_module()
        with (
            mock.patch.object(module, "load_frozen_helper") as helper,
            mock.patch.object(module, "run_once") as run_once,
            mock.patch.object(sys, "argv", [str(SCRIPT_PATH)]),
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(module.main(), 2)
        helper.assert_not_called()
        run_once.assert_not_called()
        output.assert_called_once_with(
            "G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=change_id_consumed"
        )

    def test_main_returns_three_for_present_mismatch(self) -> None:
        """完整但不匹配的暂存证据必须用专用退出码 3 阻断后续动作。"""
        module = load_module()
        helper = types.SimpleNamespace(
            validate_known_hosts=mock.Mock(),
            validate_identity_file=mock.Mock(),
            validate_identity_pair=mock.Mock(),
        )
        arguments = [
            str(SCRIPT_PATH),
            "--change-id",
            module.CHANGE_ID,
            "--known-hosts",
            "known_hosts",
            "--identity-file",
            "id_ed25519",
            "--identity-public-file",
            "id_ed25519.pub",
        ]
        mismatch = {
            "STAGING_STATE": "PRESENT",
            "STAGING_INTEGRITY": "MISMATCH",
            "STAGING_MISMATCH_REASON": "FILE_CONTENT",
        }
        with (
            mock.patch.object(module, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(module, "load_frozen_helper", return_value=helper),
            mock.patch.object(module, "run_once", return_value=mismatch),
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(module.main(), 3)
        self.assertEqual(
            output.call_args_list[0],
            mock.call("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=MISMATCH"),
        )


@unittest.skipUnless(os.name == "posix", "目录描述符动态取证只在 Linux CI 执行")
class TestDropStagingEvidencePosix(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module()
        self.temporary = tempfile.TemporaryDirectory(prefix="g8-drop-008-")
        self.root = Path(self.temporary.name) / "molin"
        self.stage = self.root / ".stage-003"
        self.root.mkdir(mode=0o700)
        self.contents = {
            "SHA256SUMS": b"sum\n",
            "ai-gateway-reconcile": b"reconcile\n",
            "g8-test-readonly-audit": b"audit\n",
            "manifest.env": b"manifest\n",
            "molin-g8-test-readonly-audit.sudoers": b"sudoers\n",
        }
        self.expected = {
            name: (hashlib.sha256(content).hexdigest(), len(content))
            for name, content in self.contents.items()
        }

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def create_valid_stage(self) -> None:
        self.stage.mkdir(mode=0o700)
        for name, content in self.contents.items():
            path = self.stage / name
            path.write_bytes(content)
            path.chmod(0o600)

    def run_remote(self) -> dict[str, str]:
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
        )
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

    def test_remote_program_reports_absent_and_present_pass(self) -> None:
        absent = self.run_remote()
        self.assertEqual(absent["STAGING_STATE"], "ABSENT")
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

    def test_deployment_root_accepts_read_only_group_and_other_bits(self) -> None:
        """部署根可保留 0755 的读取/遍历位，但绝不能允许组或其他写入。"""
        self.root.chmod(0o755)
        result = self.run_remote()
        self.assertEqual(result["STAGING_STATE"], "ABSENT")

    def test_remote_program_classifies_static_mismatches(self) -> None:
        mutations = {
            "FILE_SET": lambda: (self.stage / "extra").write_bytes(b"extra"),
            "FILE_METADATA": lambda: (self.stage / "manifest.env").chmod(0o622),
            "FILE_CONTENT": lambda: (self.stage / "manifest.env").write_bytes(b"changed!\n"),
        }
        for expected_reason, mutate in mutations.items():
            with self.subTest(expected_reason=expected_reason):
                if self.stage.exists():
                    for path in self.stage.iterdir():
                        path.unlink()
                    self.stage.rmdir()
                self.create_valid_stage()
                mutate()
                result = self.run_remote()
                self.assertEqual(result["STAGING_INTEGRITY"], "MISMATCH")
                self.assertEqual(result["STAGING_MISMATCH_REASON"], expected_reason)

    def test_remote_program_classifies_path_and_read_error(self) -> None:
        alternate_stage = self.root / ".alternate-stage"
        alternate_stage.mkdir(mode=0o700)
        self.stage.symlink_to(alternate_stage, target_is_directory=True)
        path_result = self.run_remote()
        self.assertEqual(path_result["STAGING_MISMATCH_REASON"], "PATH")

        self.stage.unlink()
        alternate_stage.rmdir()
        self.create_valid_stage()
        manifest = self.stage / "manifest.env"
        manifest.unlink()
        manifest.symlink_to(self.stage / "SHA256SUMS")
        read_result = self.run_remote()
        self.assertEqual(read_result["STAGING_MISMATCH_REASON"], "READ_ERROR")

    def test_remote_program_detects_file_entry_replacement_race(self) -> None:
        """哈希后的同名目录项替换必须归类为 PATH，不能沿旧文件描述符误报 PASS。"""
        self.create_valid_stage()
        large_content = b"m" * (64 * 1024 * 1024)
        manifest = self.stage / "manifest.env"
        manifest.write_bytes(large_content)
        manifest.chmod(0o600)
        os.utime(manifest, (1, 1))
        self.expected["manifest.env"] = (
            hashlib.sha256(large_content).hexdigest(),
            len(large_content),
        )
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
        )
        process = subprocess.Popen(
            [sys.executable, "-I", "-c", program],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        try:
            deadline = time.monotonic() + 5
            while manifest.stat().st_atime_ns == 1_000_000_000:
                if time.monotonic() >= deadline:
                    self.fail("远端程序未在时限内开始读取 manifest")
                time.sleep(0.005)
            old_manifest = self.stage / "manifest.env.old"
            manifest.rename(old_manifest)
            manifest.write_bytes(b"x" * len(self.contents["manifest.env"]))
            manifest.chmod(0o600)
            stdout, stderr = process.communicate(timeout=10)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
        self.assertEqual(process.returncode, 0, stderr)
        self.assertEqual(stderr, "")
        result = self.module.parse_remote_output(
            stdout,
            expected_deployment_root=str(self.root),
        )
        self.assertEqual(result["STAGING_INTEGRITY"], "MISMATCH")
        self.assertEqual(result["STAGING_MISMATCH_REASON"], "PATH")

    def test_remote_program_detects_stage_metadata_drift(self) -> None:
        """暂存目录在检查与打开之间发生权限漂移时必须阻断，不能沿用旧元数据误报通过。"""
        self.create_valid_stage()
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
        )
        open_stage = """                stage_fd = os.open(
                    stage_name,"""
        self.assertIn(open_stage, program)
        program = program.replace(
            open_stage,
            """                os.chmod(staging_path, 0o777)
                stage_fd = os.open(
                    stage_name,""",
            1,
        )
        completed = subprocess.run(
            [sys.executable, "-I", "-c", program],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=10,
            check=False,
        )
        try:
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertEqual(completed.stderr, "")
            result = self.module.parse_remote_output(
                completed.stdout,
                expected_deployment_root=str(self.root),
            )
        finally:
            self.stage.chmod(0o700)
        self.assertEqual(result["STAGING_INTEGRITY"], "MISMATCH")
        self.assertEqual(result["STAGING_MISMATCH_REASON"], "PATH")

    def test_remote_program_rejects_deployment_root_metadata_drift(self) -> None:
        """部署根在哈希期间发生权限漂移时必须无证据退出，不能继续输出根检查通过。"""
        self.create_valid_stage()
        large_content = b"m" * (64 * 1024 * 1024)
        manifest = self.stage / "manifest.env"
        manifest.write_bytes(large_content)
        manifest.chmod(0o600)
        os.utime(manifest, (1, 1))
        self.expected["manifest.env"] = (
            hashlib.sha256(large_content).hexdigest(),
            len(large_content),
        )
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
        )
        process = subprocess.Popen(
            [sys.executable, "-I", "-c", program],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        try:
            deadline = time.monotonic() + 5
            while manifest.stat().st_atime_ns == 1_000_000_000:
                if time.monotonic() >= deadline:
                    self.fail("远端程序未在时限内开始读取 manifest")
                time.sleep(0.005)
            self.root.chmod(0o777)
            stdout, stderr = process.communicate(timeout=10)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
            self.root.chmod(0o700)
        self.assertEqual(process.returncode, 41, stderr)
        self.assertEqual(stdout, "")
        self.assertEqual(stderr, "")


if __name__ == "__main__":
    unittest.main()

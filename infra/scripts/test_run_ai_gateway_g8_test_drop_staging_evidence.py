#!/usr/bin/env python3
"""验证 G8 Drop 映射场景的暂存只读取证契约。"""

import importlib.util
import hashlib
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name(
    "run-ai-gateway-g8-test-drop-staging-evidence.py"
)


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

    def run_remote(self, test_hook: str = "") -> dict[str, str]:
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
            _test_hook=test_hook,
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
        read_result = self.run_remote("remove_manifest_before_open")
        self.assertEqual(read_result["STAGING_MISMATCH_REASON"], "READ_ERROR")

    def test_remote_program_detects_file_entry_replacement_race(self) -> None:
        """哈希后的同名目录项替换必须归类为 PATH，不能沿旧文件描述符误报 PASS。"""
        self.create_valid_stage()
        program = self.module.build_remote_program(
            deployment_root=str(self.root),
            staging_path=str(self.stage),
            expected_files=self.expected,
            _test_uid=os.getuid(),
            _test_gid=os.getgid(),
            _test_hook="pause_after_manifest_hash",
        )
        process = subprocess.Popen(
            [sys.executable, "-I", "-c", program],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        try:
            time.sleep(0.2)
            manifest = self.stage / "manifest.env"
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


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 012 Drop 暂存只读取证的低敏输出契约。"""

from __future__ import annotations

import importlib.util
import hashlib
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name(
    "run-ai-gateway-g8-test-drop-staging-evidence-012.py"
)


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

        mutations = {
            "FILE_SET": lambda: (self.stage / "extra").write_bytes(b"extra"),
            "FILE_METADATA": lambda: (self.stage / "manifest.env").chmod(0o622),
            "FILE_CONTENT": change_artifact,
            "MANIFEST": change_manifest,
            "RECEIPT": change_receipt,
            "READ_ERROR": replace_manifest_with_symlink,
        }
        for expected_reason, mutate in mutations.items():
            with self.subTest(expected_reason=expected_reason):
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


if __name__ == "__main__":
    unittest.main()

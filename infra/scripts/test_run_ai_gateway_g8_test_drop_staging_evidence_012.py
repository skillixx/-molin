#!/usr/bin/env python3
"""验证 012 Drop 暂存只读取证的低敏输出契约。"""

from __future__ import annotations

import importlib.util
import sys
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


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 G8 Drop 映射场景的暂存只读取证契约。"""

import importlib.util
import sys
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


if __name__ == "__main__":
    unittest.main()

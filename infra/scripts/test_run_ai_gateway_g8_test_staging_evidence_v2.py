#!/usr/bin/env python3
"""验证 G8 暂存取证 006 的门禁分类、单次 SSH 与失败关闭。"""

import importlib.util
import subprocess
import sys
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence-v2.py")


def load_module():
    """从精确路径加载脚本，避免 PATH 搜索替换实现。"""
    spec = importlib.util.spec_from_file_location("g8_staging_evidence_v2", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_load_failed")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


class TestStagingEvidenceV2(unittest.TestCase):
    def test_consumed_change_rejects_before_identity_or_network(self) -> None:
        """006 已消费后必须在读取身份材料或联网前失败关闭。"""
        with (
            mock.patch.object(sys, "argv", [str(SCRIPT_PATH)]),
            mock.patch.object(MODULE, "load_frozen_helper") as helper,
            mock.patch.object(MODULE, "run_once") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 2)
        helper.assert_not_called()
        remote.assert_not_called()
        output.assert_called_once_with(
            "G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=change_id_consumed"
        )

    def test_interpreter_and_self_test_are_fail_closed(self) -> None:
        """普通解释器必须拒绝，隔离解释器的派生程序必须通过自检。"""
        ordinary = subprocess.run(["python", str(SCRIPT_PATH), "--self-test"], capture_output=True, text=True, check=False)
        self.assertEqual(ordinary.returncode, 2)
        self.assertEqual(ordinary.stdout.strip(), "G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=isolated_python_required")
        isolated = subprocess.run(["python", "-I", str(SCRIPT_PATH), "--self-test"], capture_output=True, text=True, check=False)
        self.assertEqual(isolated.returncode, 0, isolated.stderr)
        self.assertEqual(isolated.stdout.strip(), "G8_TEST_READONLY_STAGING_EVIDENCE_V2_SELF_TEST=PASS")

    def test_helper_digest_is_frozen(self) -> None:
        """004 helper 漂移时必须在执行其代码前失败关闭。"""
        helper = MODULE.load_frozen_helper()
        self.assertTrue(helper.CHANGE_ID_CONSUMED)
        with mock.patch.object(MODULE, "HELPER_SHA256", "0" * 64):
            with self.assertRaises(MODULE.EvidenceError):
                MODULE.load_frozen_helper()

    def test_remote_program_has_six_fixed_gate_reasons(self) -> None:
        """远端程序仅增加六类固定门禁枚举，保留原暂存读取算法。"""
        helper = MODULE.load_frozen_helper()
        program = MODULE.build_remote_program(helper)
        self.assertNotIn("reject()", program)
        for reason in MODULE.GATE_REASONS:
            self.assertIn(f"reject('{reason}')", program)
        for forbidden in ("import subprocess", "os.remove(", "os.unlink(", "os.rmdir(", "import shutil"):
            self.assertNotIn(forbidden, program)

        self.assertIn("except BaseException:\n    reject('IDENTITY')", program)
        self.assertIn("except BaseException:\n    reject('MACHINE_ID')", program)
        self.assertIn("except BaseException:\n        reject('DEPLOYMENT_ROOT_METADATA')", program)
        self.assertIn("except BaseException:\n        reject('DEPLOYMENT_ROOT_DRIFT')", program)

    @unittest.skipIf(sys.platform == "win32", "远端 Linux 标准库故障注入仅在 Linux 执行")
    def test_remote_identity_and_machine_errors_return_fixed_gate(self) -> None:
        """NSS 与 machine-id 系统调用异常必须返回固定门禁，不得打印 traceback。"""
        helper = MODULE.load_frozen_helper()
        program = MODULE.build_remote_program(helper)
        identity_failure = program.replace(
            "try:\n    account = pwd.getpwnam('pc')",
            "pwd.getpwnam = lambda name: (_ for _ in ()).throw(OSError('secret'))\n"
            "try:\n    account = pwd.getpwnam('pc')",
            1,
        )
        result = subprocess.run([sys.executable, "-I", "-c", identity_failure], capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stderr, "")
        self.assertIn("GATE_REASON=IDENTITY", result.stdout)

        machine_failure = program.replace(
            "try:\n    account = pwd.getpwnam('pc')",
            "class Account:\n    pw_uid = os.getuid()\n"
            "class Group:\n    gr_gid = os.getgid()\n"
            "class Uname:\n    nodename = target_hostname\n"
            "pwd.getpwnam = lambda name: Account()\n"
            "grp.getgrnam = lambda name: Group()\n"
            "os.uname = lambda: Uname()\n"
            "try:\n    account = pwd.getpwnam('pc')",
            1,
        ).replace(
            "try:\n    current_machine_id_sha256 = digest('/etc/machine-id')",
            "digest = lambda path: (_ for _ in ()).throw(OSError('secret'))\n"
            "try:\n    current_machine_id_sha256 = digest('/etc/machine-id')",
            1,
        )
        result = subprocess.run([sys.executable, "-I", "-c", machine_failure], capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stderr, "")
        self.assertIn("GATE_REASON=MACHINE_ID", result.stdout)

    def test_parse_accepts_blocked_and_absent_evidence(self) -> None:
        """解析器只接受固定门禁阻断或原 004 已验证的证据键集合。"""
        helper = MODULE.load_frozen_helper()
        blocked = (
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}\n"
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "GATE_RESULT=BLOCKED\nGATE_REASON=DEPLOYMENT_ROOT_METADATA\n"
        ).encode()
        result_type, values = MODULE.parse_output(helper, blocked)
        self.assertEqual(result_type, "BLOCKED")
        self.assertEqual(values["GATE_REASON"], "DEPLOYMENT_ROOT_METADATA")

        absent = (
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}\n"
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "LOGIN_USER=pc\nHOSTNAME=pc-Z790-UD-AX\n"
            "MACHINE_ID_SHA256=b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4\n"
            "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin\nDEPLOYMENT_ROOT_CHECK=PASS\n"
            "STAGING_STATE=ABSENT\nSTAGING_INTEGRITY=NOT_APPLICABLE\n"
            "STAGING_MISMATCH_REASON=NONE\nEVIDENCE_RESULT=PASS\n"
        ).encode()
        result_type, values = MODULE.parse_output(helper, absent)
        self.assertEqual(result_type, "EVIDENCE")
        self.assertEqual(values["STAGING_STATE"], "ABSENT")

    def test_parse_rejects_unknown_gate_or_extra_key(self) -> None:
        """未知门禁原因或额外字段必须失败关闭。"""
        helper = MODULE.load_frozen_helper()
        for payload in (
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}\nTARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\nGATE_RESULT=BLOCKED\nGATE_REASON=SECRET\n",
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}\nTARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\nGATE_RESULT=BLOCKED\nGATE_REASON=IDENTITY\nEXTRA=1\n",
        ):
            with self.subTest(payload=payload):
                with self.assertRaises(MODULE.EvidenceError):
                    MODULE.parse_output(helper, payload.encode())

    def test_evidence_rejects_missing_or_wrong_change_id_for_all_states(self) -> None:
        """三类证据都必须原样绑定 006 ChangeId，禁止解析器替调用方修正。"""
        helper = MODULE.load_frozen_helper()
        states = (
            ("ABSENT", "NOT_APPLICABLE", "NONE"),
            ("PRESENT", "PASS", "NONE"),
            ("PRESENT", "MISMATCH", "FILE_CONTENT"),
        )
        for state, integrity, reason in states:
            base_lines = [
                f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}",
                f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}",
                "LOGIN_USER=pc",
                "HOSTNAME=pc-Z790-UD-AX",
                "MACHINE_ID_SHA256=b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4",
                "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
                "DEPLOYMENT_ROOT_CHECK=PASS",
                f"STAGING_STATE={state}",
                f"STAGING_INTEGRITY={integrity}",
                f"STAGING_MISMATCH_REASON={reason}",
                "EVIDENCE_RESULT=PASS",
            ]
            payloads = (
                "\n".join(base_lines[1:]) + "\n",
                "\n".join(["EVIDENCE_CHANGE_ID=WRONG", *base_lines[1:]]) + "\n",
                "\n".join([base_lines[0], "TARGET_CHANGE_ID=WRONG", *base_lines[2:]]) + "\n",
            )
            for payload in payloads:
                with self.subTest(state=state, payload=payload):
                    with self.assertRaises(MODULE.EvidenceError):
                        MODULE.parse_output(helper, payload.encode())

    def test_invalid_change_rejects_before_identity_or_network(self) -> None:
        """未知 ChangeId 必须在身份读取和联网前拒绝。"""
        arguments = [str(SCRIPT_PATH), "--change-id", "INVALID", "--known-hosts", "missing", "--identity-file", "missing", "--identity-public-file", "missing"]
        helper = SimpleNamespace()
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "load_frozen_helper", return_value=helper),
            mock.patch.object(MODULE, "build_remote_program", return_value="pass"),
            mock.patch.object(MODULE, "run_once") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 2)
        remote.assert_not_called()
        output.assert_called_once_with("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=invalid_request")

    def test_cli_local_check_and_blocked_output(self) -> None:
        """本地检查不联网，正式阻断只输出固定门禁原因。"""
        arguments = [str(SCRIPT_PATH), "--change-id", MODULE.CHANGE_ID, "--known-hosts", "/fixed/known_hosts", "--identity-file", "/fixed/key", "--identity-public-file", "/fixed/key.pub"]
        helper = SimpleNamespace(
            validate_known_hosts=mock.Mock(),
            validate_identity_file=mock.Mock(),
            validate_identity_pair=mock.Mock(),
        )
        with (
            mock.patch.object(sys, "argv", arguments + ["--local-check"]),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "load_frozen_helper", return_value=helper),
            mock.patch.object(MODULE, "build_remote_program", return_value="pass"),
            mock.patch.object(MODULE, "run_once") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 0)
        remote.assert_not_called()
        output.assert_called_once_with("G8_TEST_READONLY_STAGING_EVIDENCE_V2_LOCAL_CHECK=PASS")

        stdout = (
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}\nTARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "GATE_RESULT=BLOCKED\nGATE_REASON=IDENTITY\n"
        ).encode()
        stream = {"captured": stdout, "bytes": len(stdout), "sha256": "x", "exceeded": False, "error": False}
        empty = {"captured": b"", "bytes": 0, "sha256": "NONE", "exceeded": False, "error": False}
        real_helper = MODULE.load_frozen_helper()
        real_helper.validate_known_hosts = mock.Mock()
        real_helper.validate_identity_file = mock.Mock()
        real_helper.validate_identity_pair = mock.Mock()
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "load_frozen_helper", return_value=real_helper),
            mock.patch.object(MODULE, "run_once", return_value=(0, stream, empty)) as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 3)
        remote.assert_called_once()
        output.assert_any_call("G8_TEST_READONLY_STAGING_EVIDENCE_V2=BLOCKED")
        output.assert_any_call("gate_reason=IDENTITY")


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 011 暂存包装器只保留离线检查和单次 SFTP 能力。"""

import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-access-stage-drop-interactive.py")
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"


def load_module():
    """从固定路径加载待测包装器，避免修改模块搜索路径。"""
    specification = importlib.util.spec_from_file_location("g8_stage_011", SCRIPT_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyAccessStageDropInteractive(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.module = load_module()

    def test_consumed_change_rejects_every_entry_before_material_or_network_access(self) -> None:
        """消费后 self-test、本地检查和正式入口都必须先固定拒绝。"""
        requests = (
            ("--self-test",),
            (
                "--local-check", f"--change-id={CHANGE_ID}",
                "--candidate-dir=C:\\missing-candidate", "--known-hosts=C:\\missing-known-hosts",
                "--identity-file=C:\\missing-identity", "--identity-public-file=C:\\missing-public",
            ),
            (
                f"--change-id={CHANGE_ID}",
                "--candidate-dir=C:\\missing-candidate", "--known-hosts=C:\\missing-known-hosts",
                "--identity-file=C:\\missing-identity", "--identity-public-file=C:\\missing-public",
            ),
        )
        for arguments in requests:
            with self.subTest(arguments=arguments):
                result = subprocess.run(
                    ["python", "-I", str(SCRIPT_PATH), *arguments],
                    capture_output=True, text=True, encoding="utf-8", check=False,
                )
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertEqual(
                    result.stdout.strip(),
                    "G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=FAILED reason=change_id_consumed",
                )

    def test_sftp_rejects_any_stdout_even_when_bounded(self) -> None:
        """批处理 SFTP 的任何 stdout 都属于输出契约漂移。"""
        direct = mock.Mock()
        direct.EXPECTED_FILES = {
            "SHA256SUMS", "ai-gateway-reconcile", "g8-test-readonly-audit",
            "manifest.env", "molin-g8-test-readonly-audit.sudoers",
        }
        direct.TARGET_PORT = "10003"
        direct.TARGET = "pc@8.130.9.163"
        helper = mock.Mock()
        helper.fixed_tool.return_value = Path("/usr/bin/sftp")
        helper.ssh_options.return_value = []
        helper.fixed_ssh_environment.return_value = {}
        helper.run_bounded_process.return_value = (
            0, {"bytes": 1, "exceeded": False}, {"bytes": 0, "exceeded": False},
        )
        with tempfile.TemporaryDirectory(prefix="g8-011-sftp-") as temporary:
            with self.assertRaises(self.module.StageError):
                self.module.run_single_sftp(
                    direct, helper, Path(temporary) / "known_hosts",
                    Path(temporary) / "id_ed25519", Path(temporary),
                )
        self.assertIn(CHANGE_ID, self.source)
        self.assertIn("DROP_SSH_INTERACTIVE_SUDO", self.source)
        self.assertIn("15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f", self.source)

    def test_production_path_has_one_sftp_and_no_ssh_or_sudo_call(self) -> None:
        self.assertEqual(self.source.count("helper.run_bounded_process("), 1)
        self.assertEqual(self.source.count('fixed_tool("sftp")'), 1)
        self.assertNotIn('fixed_tool("ssh")', self.source)
        self.assertNotIn("run_remote_preflight(", self.source)
        self.assertNotIn("sudo ", self.source)

    def test_010_staging_is_not_referenced(self) -> None:
        self.assertNotIn(".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010", self.source)
        self.assertIn(".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        for command in (
            "chmod 600 {STAGING_PATH}/SHA256SUMS",
            "chmod 700 {STAGING_PATH}/ai-gateway-reconcile",
            "chmod 700 {STAGING_PATH}/g8-test-readonly-audit",
            "chmod 600 {STAGING_PATH}/manifest.env",
            "chmod 600 {STAGING_PATH}/molin-g8-test-readonly-audit.sudoers",
        ):
            self.assertIn(command, self.source)

    def test_formal_mode_revalidates_inputs_around_single_sftp(self) -> None:
        module = load_module()
        fake_direct = mock.Mock(unsafe=True)
        fake_direct.validate_local_inputs.return_value = {"fixed": "evidence"}
        with tempfile.TemporaryDirectory(prefix="g8-stage-011-") as temporary:
            root = Path(temporary)
            candidate = root / "candidate"
            candidate.mkdir()
            known = root / "known_hosts"
            identity = root / "id_ed25519"
            public = root / "id_ed25519.pub"
            for path in (known, identity, public):
                path.write_text("fixture", encoding="ascii")
            with (
                mock.patch.object(module, "load_frozen_direct", return_value=fake_direct),
                mock.patch.object(module, "create_candidate_snapshot", return_value=candidate),
                mock.patch.object(module, "run_single_sftp") as sftp,
            ):
                result = module.execute(candidate, known, identity, public, local_check=False)
        self.assertEqual(result, "G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=PASS")
        self.assertEqual(fake_direct.assert_local_inputs_unchanged.call_count, 2)
        sftp.assert_called_once()

    def test_consumed_gate_precedes_material_or_network_access(self) -> None:
        module = load_module()
        with (
            mock.patch.object(module, "CHANGE_ID_CONSUMED", True),
            mock.patch.object(module, "load_frozen_direct") as loader,
        ):
            self.assertEqual(module.main(["--self-test"]), 2)
        loader.assert_not_called()


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 G8 测试服 003 暂存状态取证入口严格只读、单次且失败关闭。"""

import base64
import hashlib
import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence.py")


def load_module():
    """从精确脚本路径加载模块，避免从 PATH 或其他目录寻找替代实现。"""
    spec = importlib.util.spec_from_file_location("g8_staging_evidence", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_load_failed")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


def remote_stdout(state: str, integrity: str, mismatch_reason: str = "NONE") -> str:
    """生成只包含固定枚举的低敏远端输出。"""
    return "\n".join(
        (
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}",
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}",
            "LOGIN_USER=pc",
            f"HOSTNAME={MODULE.TARGET_HOSTNAME}",
            f"MACHINE_ID_SHA256={MODULE.TARGET_MACHINE_ID_SHA256}",
            f"DEPLOYMENT_ROOT_REALPATH={MODULE.TARGET_DEPLOYMENT_ROOT}",
            "DEPLOYMENT_ROOT_CHECK=PASS",
            f"STAGING_STATE={state}",
            f"STAGING_INTEGRITY={integrity}",
            f"STAGING_MISMATCH_REASON={mismatch_reason}",
            "EVIDENCE_RESULT=PASS",
        )
    ) + "\n"


class TestStagingEvidence(unittest.TestCase):
    def test_self_test_requires_isolated_python(self) -> None:
        """入口必须拒绝可能受脚本目录或 PYTHONPATH 劫持的普通解释器。"""
        isolated = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(isolated.returncode, 0, isolated.stderr)
        self.assertEqual(
            isolated.stdout.strip(),
            "G8_TEST_READONLY_STAGING_EVIDENCE_SELF_TEST=PASS",
        )
        ordinary = subprocess.run(
            ["python", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(ordinary.returncode, 2)
        self.assertEqual(
            ordinary.stdout.strip(),
            "G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=isolated_python_required",
        )

    def test_remote_output_accepts_only_absent_or_exact_present(self) -> None:
        """远端证据只能关闭为不存在或完整匹配，不能接受部分状态。"""
        absent = MODULE.parse_remote_output(remote_stdout("ABSENT", "NOT_APPLICABLE"))
        present = MODULE.parse_remote_output(remote_stdout("PRESENT", "PASS"))
        mismatch = MODULE.parse_remote_output(
            remote_stdout("PRESENT", "MISMATCH", "FILE_CONTENT")
        )
        self.assertEqual(absent["STAGING_STATE"], "ABSENT")
        self.assertEqual(present["STAGING_STATE"], "PRESENT")
        self.assertEqual(mismatch["STAGING_MISMATCH_REASON"], "FILE_CONTENT")
        invalid_outputs = (
            remote_stdout("UNKNOWN", "NOT_APPLICABLE"),
            remote_stdout("PRESENT", "FAILED", "FILE_CONTENT"),
            remote_stdout("ABSENT", "PASS"),
            remote_stdout("PRESENT", "MISMATCH", "SECRET_DYNAMIC_REASON"),
            remote_stdout("PRESENT", "MISMATCH", "NONE"),
            remote_stdout("ABSENT", "NOT_APPLICABLE") + "EXTRA=value\n",
        )
        for output in invalid_outputs:
            with self.subTest(output=output), self.assertRaises(MODULE.EvidenceError):
                MODULE.parse_remote_output(output)

    def test_remote_program_is_fixed_read_only_and_checks_exact_bundle(self) -> None:
        """远端程序只读固定路径，并绑定五文件、摘要、属主和权限。"""
        program = MODULE.REMOTE_PROGRAM
        self.assertIn(MODULE.STAGING_PATH, program)
        self.assertIn(MODULE.FROZEN_BUNDLE_RECEIPT_SHA256, program)
        self.assertIn(MODULE.FROZEN_RECONCILE_SHA256, program)
        self.assertIn("os.listdir(stage_descriptor)", program)
        self.assertIn("os.O_NOFOLLOW", program)
        self.assertIn("dir_fd=root_descriptor", program)
        self.assertIn("dir_fd=stage_descriptor", program)
        self.assertIn("pinned_root = os.fstat(root_descriptor)", program)
        self.assertIn("current_root = os.lstat(deployment_root)", program)
        self.assertIn("os.lstat", program)
        self.assertIn("hashlib.sha256", program)
        self.assertIn("open(path, 'rb')", program)
        for forbidden in (
            "import subprocess",
            "os.remove(",
            "os.unlink(",
            "os.rmdir(",
            "import shutil",
            "open(path, 'w')",
        ):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, program)

    @unittest.skipUnless(os.name == "posix", "远端隔离程序只在 Linux CI 动态执行")
    def test_remote_program_distinguishes_absent_present_and_content_mismatch(self) -> None:
        """Linux CI 必须真实执行远端程序并覆盖三种低敏证据状态。"""
        import grp
        import pwd

        def values(stdout: str) -> dict[str, str]:
            return dict(line.split("=", 1) for line in stdout.splitlines())

        with tempfile.TemporaryDirectory(prefix="g8-staging-remote-") as temporary:
            root = Path(temporary) / "molin"
            stage = root / ".stage-003"
            machine_id = Path(temporary) / "machine-id"
            root.mkdir(mode=0o700)
            root.chmod(0o700)
            machine_id.write_bytes(b"fixed-machine-id\n")
            fixtures = {
                name: (f"fixture-{index}-{name}").encode("ascii")
                for index, name in enumerate(sorted(MODULE.FROZEN_FILES), start=1)
            }
            program = MODULE.REMOTE_PROGRAM
            program = program.replace(repr(MODULE.STAGING_PATH), repr(str(stage)))
            program = program.replace(repr(MODULE.TARGET_DEPLOYMENT_ROOT), repr(str(root)))
            program = program.replace(
                repr(MODULE.TARGET_HOSTNAME), repr(os.uname().nodename)
            )
            program = program.replace(
                repr(MODULE.TARGET_MACHINE_ID_SHA256),
                repr(hashlib.sha256(machine_id.read_bytes()).hexdigest()),
            )
            program = program.replace("'/etc/machine-id'", repr(str(machine_id)))
            program = program.replace(
                "pwd.getpwnam('pc')", repr(pwd.getpwuid(os.getuid()).pw_name).join(("pwd.getpwnam(", ")"))
            )
            program = program.replace(
                "grp.getgrnam('pc')", repr(grp.getgrgid(os.getgid()).gr_name).join(("grp.getgrnam(", ")"))
            )
            for name, frozen in MODULE.FROZEN_FILES.items():
                replacement = (hashlib.sha256(fixtures[name]).hexdigest(), len(fixtures[name]))
                program = program.replace(repr(frozen), repr(replacement))

            absent = subprocess.run(
                [sys.executable, "-I", "-"],
                input=program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(absent.returncode, 0, absent.stderr)
            self.assertEqual(values(absent.stdout)["STAGING_STATE"], "ABSENT")

            stage.mkdir(mode=0o700)
            stage.chmod(0o700)
            for name, content in fixtures.items():
                path = stage / name
                path.write_bytes(content)
                path.chmod(0o600)
            present = subprocess.run(
                [sys.executable, "-I", "-"],
                input=program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(present.returncode, 0, present.stderr)
            self.assertEqual(values(present.stdout)["STAGING_INTEGRITY"], "PASS")

            metadata_path = stage / "manifest.env"
            metadata_path.chmod(0o622)
            metadata_mismatch = subprocess.run(
                [sys.executable, "-I", "-"],
                input=program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(metadata_mismatch.returncode, 0, metadata_mismatch.stderr)
            metadata_result = values(metadata_mismatch.stdout)
            self.assertEqual(metadata_result["STAGING_INTEGRITY"], "MISMATCH")
            self.assertEqual(metadata_result["STAGING_MISMATCH_REASON"], "FILE_METADATA")
            metadata_path.chmod(0o600)

            # 哈希完成后替换同名目录项并保留旧文件，最终证据必须拒绝把旧 fd 内容当作当前文件。
            raced_entry_program = program.replace(
                "actual_digest = digest_handle(handle)",
                "actual_digest = digest_handle(handle)\n"
                "                                        if name == 'manifest.env':\n"
                "                                            os.rename(os.path.join(staging_path, name), os.path.join(staging_path, name + '.old'))\n"
                "                                            with open(os.path.join(staging_path, name), 'wb') as replacement:\n"
                "                                                replacement.write(b'X' * expected_size)",
                1,
            )
            raced_entry = subprocess.run(
                [sys.executable, "-I", "-"],
                input=raced_entry_program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(raced_entry.returncode, 0, raced_entry.stderr)
            raced_entry_result = values(raced_entry.stdout)
            self.assertEqual(raced_entry_result["STAGING_INTEGRITY"], "MISMATCH")
            self.assertEqual(raced_entry_result["STAGING_MISMATCH_REASON"], "PATH")

            (stage / "manifest.env").unlink()
            (stage / "manifest.env.old").rename(stage / "manifest.env")

            changed = bytearray(fixtures["manifest.env"])
            changed[0] ^= 1
            (stage / "manifest.env").write_bytes(changed)
            mismatch = subprocess.run(
                [sys.executable, "-I", "-"],
                input=program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(mismatch.returncode, 0, mismatch.stderr)
            result = values(mismatch.stdout)
            self.assertEqual(result["STAGING_INTEGRITY"], "MISMATCH")
            self.assertEqual(result["STAGING_MISMATCH_REASON"], "FILE_CONTENT")

            # 在部署根完成校验后替换其路径，必须由结束前的 inode 复核失败关闭。
            replaced_root = Path(temporary) / "molin-replaced"
            raced_program = program.replace(
                "staging_state = 'ABSENT'",
                "os.rename(deployment_root, "
                + repr(str(replaced_root))
                + ")\n    os.mkdir(deployment_root, 0o700)\n    staging_state = 'ABSENT'",
                1,
            )
            raced = subprocess.run(
                [sys.executable, "-I", "-"],
                input=raced_program,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )
            self.assertEqual(raced.returncode, 41)
            self.assertEqual(raced.stdout, "")

    def test_remote_evidence_invokes_fixed_ssh_exactly_once(self) -> None:
        """正式入口只能通过固定参数发起一次 SSH，且远端程序只经 stdin 传递。"""
        completed = subprocess.CompletedProcess(
            [], 0, remote_stdout("ABSENT", "NOT_APPLICABLE"), ""
        )
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            values = MODULE.run_remote_evidence(
                Path("/usr/bin/ssh"), Path("/tmp/known_hosts"), Path("/tmp/id_ed25519")
            )
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        self.assertNotIn(MODULE.REMOTE_PROGRAM, command)
        self.assertEqual(
            command[-6:],
            ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/usr/bin/python3", "-I", "-"],
        )
        self.assertEqual(run.call_args.kwargs["input"], MODULE.REMOTE_PROGRAM)
        for option in (
            "NumberOfPasswordPrompts=0",
            "ConnectionAttempts=1",
            "StrictHostKeyChecking=yes",
            "IdentitiesOnly=yes",
            "ClearAllForwardings=yes",
            "RequestTTY=no",
        ):
            self.assertIn(option, command)
        environment = run.call_args.kwargs["env"]
        self.assertNotIn("SSH_AUTH_SOCK", environment)
        self.assertNotIn("SSH_ASKPASS", environment)
        self.assertEqual(values["STAGING_STATE"], "ABSENT")

    def test_remote_failure_is_not_retried_or_leaked(self) -> None:
        """任意非零或 stderr 必须低敏失败，禁止重试和回显。"""
        completed = subprocess.CompletedProcess([], 41, "SECRET_MARKER", "remote detail")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            with self.assertRaises(MODULE.EvidenceError):
                MODULE.run_remote_evidence(
                    Path("/usr/bin/ssh"), Path("/tmp/known_hosts"), Path("/tmp/id_ed25519")
                )
        self.assertEqual(run.call_count, 1)

    def test_known_host_and_public_identity_are_bound_without_path_lookup(self) -> None:
        """known_hosts 与公钥必须绑定冻结 ED25519 指纹和同一受控目录。"""
        host_blob = b"fixed-host-key"
        identity_blob = b"fixed-identity-key"
        host_fingerprint = "SHA256:" + base64.b64encode(
            hashlib.sha256(host_blob).digest()
        ).decode("ascii").rstrip("=")
        identity_fingerprint = "SHA256:" + base64.b64encode(
            hashlib.sha256(identity_blob).digest()
        ).decode("ascii").rstrip("=")
        with tempfile.TemporaryDirectory(prefix="g8-staging-identity-") as temporary:
            ssh_dir = Path(temporary) / ".ssh"
            ssh_dir.mkdir()
            known_hosts = ssh_dir / "known_hosts"
            identity = ssh_dir / "id_ed25519"
            public = ssh_dir / "id_ed25519.pub"
            known_hosts.write_text(
                f"[{MODULE.TARGET_HOST}]:{MODULE.TARGET_PORT} ssh-ed25519 "
                + base64.b64encode(host_blob).decode("ascii")
                + "\n",
                encoding="ascii",
            )
            identity.write_text("placeholder", encoding="ascii")
            public.write_text(
                "ssh-ed25519 " + base64.b64encode(identity_blob).decode("ascii") + " test\n",
                encoding="ascii",
            )
            with (
                mock.patch.object(MODULE, "TARGET_SSH_ED25519_FINGERPRINT", host_fingerprint),
                mock.patch.object(MODULE, "LOCAL_IDENTITY_ED25519_FINGERPRINT", identity_fingerprint),
            ):
                MODULE.validate_known_hosts(known_hosts)
                MODULE.validate_identity_file(identity, public, known_hosts)

    def test_cli_validates_local_identity_before_optional_single_remote_call(self) -> None:
        """local-check 不联网；正式模式只在全部本地事实通过后调用一次远端取证。"""
        common = [
            str(SCRIPT_PATH),
            "--change-id",
            MODULE.CHANGE_ID,
            "--known-hosts",
            "/fixed/known_hosts",
            "--identity-file",
            "/fixed/id_ed25519",
            "--identity-public-file",
            "/fixed/id_ed25519.pub",
        ]
        with (
            mock.patch.object(sys, "argv", common + ["--local-check"]),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "validate_known_hosts") as known_hosts,
            mock.patch.object(MODULE, "validate_identity_file") as identity,
            mock.patch.object(MODULE, "validate_identity_pair") as pair,
            mock.patch.object(MODULE, "run_remote_evidence") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 0)
        known_hosts.assert_called_once()
        identity.assert_called_once()
        pair.assert_called_once()
        remote.assert_not_called()
        output.assert_called_once_with("G8_TEST_READONLY_STAGING_EVIDENCE_LOCAL_CHECK=PASS")

        with (
            mock.patch.object(sys, "argv", common),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "validate_known_hosts"),
            mock.patch.object(MODULE, "validate_identity_file"),
            mock.patch.object(MODULE, "validate_identity_pair"),
            mock.patch.object(MODULE, "fixed_ssh_executable", return_value=Path("/usr/bin/ssh")),
            mock.patch.object(
                MODULE,
                "run_remote_evidence",
                return_value=MODULE.parse_remote_output(remote_stdout("PRESENT", "PASS")),
            ) as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 0)
        remote.assert_called_once()
        output.assert_any_call("G8_TEST_READONLY_STAGING_EVIDENCE=PASS")
        output.assert_any_call("staging_state=PRESENT")
        output.assert_any_call("staging_integrity=PASS")
        output.assert_any_call("staging_mismatch_reason=NONE")

    def test_cli_rejects_unapproved_change_before_reading_files_or_network(self) -> None:
        """未知 ChangeId 必须在读取本地身份文件或联网前失败。"""
        arguments = [
            str(SCRIPT_PATH),
            "--change-id",
            "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-999",
            "--known-hosts",
            "/does/not/exist",
            "--identity-file",
            "/does/not/exist",
            "--identity-public-file",
            "/does/not/exist",
        ]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "validate_known_hosts") as local_read,
            mock.patch.object(MODULE, "run_remote_evidence") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 2)
        local_read.assert_not_called()
        remote.assert_not_called()
        output.assert_called_once_with(
            "G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=invalid_request"
        )

    def test_cli_rejects_consumed_change_before_reading_files_or_network(self) -> None:
        """004 已消费后，本地检查和正式模式都必须在读取文件或联网前失败关闭。"""
        common = [
            str(SCRIPT_PATH),
            "--change-id",
            MODULE.CHANGE_ID,
            "--known-hosts",
            "/does/not/exist",
            "--identity-file",
            "/does/not/exist",
            "--identity-public-file",
            "/does/not/exist",
        ]
        for arguments in (common, common + ["--local-check"]):
            with (
                self.subTest(arguments=arguments),
                mock.patch.object(sys, "argv", arguments),
                mock.patch.object(MODULE, "validate_known_hosts") as local_read,
                mock.patch.object(MODULE, "run_remote_evidence") as remote,
                mock.patch("builtins.print") as output,
            ):
                self.assertEqual(MODULE.main(), 2)
            local_read.assert_not_called()
            remote.assert_not_called()
            output.assert_called_once_with(
                "G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=change_id_consumed"
            )

    def test_cli_returns_distinct_nonzero_for_observed_mismatch(self) -> None:
        """已观察到的暂存不一致必须输出固定证据并以非零状态阻断后续动作。"""
        arguments = [
            str(SCRIPT_PATH),
            "--change-id",
            MODULE.CHANGE_ID,
            "--known-hosts",
            "/fixed/known_hosts",
            "--identity-file",
            "/fixed/id_ed25519",
            "--identity-public-file",
            "/fixed/id_ed25519.pub",
        ]
        mismatch = MODULE.parse_remote_output(
            remote_stdout("PRESENT", "MISMATCH", "FILE_SET")
        )
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "validate_known_hosts"),
            mock.patch.object(MODULE, "validate_identity_file"),
            mock.patch.object(MODULE, "validate_identity_pair"),
            mock.patch.object(MODULE, "fixed_ssh_executable", return_value=Path("/usr/bin/ssh")),
            mock.patch.object(MODULE, "run_remote_evidence", return_value=mismatch),
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 3)
        output.assert_any_call("G8_TEST_READONLY_STAGING_EVIDENCE=MISMATCH")
        output.assert_any_call("staging_state=PRESENT")
        output.assert_any_call("staging_mismatch_reason=FILE_SET")


if __name__ == "__main__":
    unittest.main()

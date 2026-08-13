#!/usr/bin/env python3
"""验证 010 Drop 直连候选的本地门禁、原始身份路径和单次远端调用契约。"""

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-access-stage-drop-direct.py")
AUTHORIZATION_PATH = SCRIPT_PATH.parents[2] / "docs" / "ai-gateway-g8-test-readonly-access-install-authorization-20260813-010.md"


def load_module():
    """按真实路径加载 010 包装器，避免依赖调用方模块搜索路径。"""
    spec = importlib.util.spec_from_file_location("g8_access_stage_drop_direct", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_unavailable")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class TestReadonlyAccessStageDropDirect(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module()

    def test_candidate_is_bound_to_010_direct_drop_contract(self) -> None:
        """010 必须使用新身份，并明确不把物理主机身份作为门禁。"""
        self.assertEqual(
            self.module.CHANGE_ID,
            "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010",
        )
        self.assertTrue(self.module.CHANGE_ID_CONSUMED)
        self.assertEqual(self.module.SOURCE_COMMIT, "75b1fc4ddb7138495547cec03fa948648de337d7")
        self.assertEqual(self.module.SOURCE_TREE, "53ba990318bc1a036b442d88ff8133d776a453dc")
        self.assertEqual(self.module.TARGET_TRANSPORT, "DROP_SSH_DIRECT")
        self.assertEqual(self.module.PHYSICAL_HOST_IDENTITY, "NOT_APPLICABLE")

    def test_helper_is_frozen_before_loading(self) -> None:
        """复用的历史纯函数必须先按普通文件、inode 和摘要冻结。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn(
            'FROZEN_HELPER_SHA256 = "4be88638f2a4a271ebbf23751bd3f7238ea5f78f1f18fcb6889c9e071b953f30"',
            source,
        )
        self.assertIn("helper_path.lstat()", source)
        self.assertIn("helper_path.open(\"rb\")", source)
        self.assertIn("helper_path.stat()", source)
        self.assertIn("helper_sha256 != FROZEN_HELPER_SHA256", source)

    def test_direct_mode_never_copies_or_changes_identity_acl(self) -> None:
        """010 只能直接引用原始身份路径，不得复制或修改私钥权限。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertNotIn("create_frozen_local_snapshot", source)
        self.assertNotIn("icacls", source.lower())
        self.assertNotIn("os.chmod", source)
        self.assertIn("create_frozen_candidate_snapshot", source)

    def test_local_check_never_calls_ssh_or_sftp(self) -> None:
        arguments = [
            str(SCRIPT_PATH),
            "--local-check",
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=C:/candidate",
            "--known-hosts=C:/Users/test/.ssh/known_hosts",
            "--identity-file=C:/Users/test/.ssh/id_ed25519",
            "--identity-public-file=C:/Users/test/.ssh/id_ed25519.pub",
        ]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(self.module, "load_frozen_helper") as load_helper,
            mock.patch.object(self.module, "validate_local_inputs"),
            mock.patch.object(self.module, "run_remote_preflight") as remote,
            mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
            mock.patch("builtins.print"),
        ):
            load_helper.return_value = mock.Mock()
            self.assertEqual(self.module.main(), 0)
        remote.assert_not_called()
        sftp.assert_not_called()

    def test_wrong_change_id_fails_before_helper_or_local_reads(self) -> None:
        arguments = [str(SCRIPT_PATH), "--change-id=wrong"]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "load_frozen_helper") as helper,
            mock.patch.object(self.module, "validate_local_inputs") as validate,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 2)
        helper.assert_not_called()
        validate.assert_not_called()

    def test_remote_preflight_invokes_ssh_exactly_once_with_direct_identity(self) -> None:
        """一次预检必须把原始身份路径传入固定 SSH 选项，且不得出现物理身份读取。"""
        stdout = "\n".join(
            (
                f"PREFLIGHT_CHANGE_ID={self.module.CHANGE_ID}",
                "LOGIN_USER=pc",
                "LOGIN_GROUP=pc",
                f"DEPLOYMENT_ROOT_REALPATH={self.module.TARGET_DEPLOYMENT_ROOT}",
                "DEPLOYMENT_ROOT_META=pc:pc:700:directory",
                "STAGING_ABSENT=true",
                "INSTALL_TARGETS_ABSENT=true",
                "PREFLIGHT_RESULT=PASS",
            )
        ).encode("ascii") + b"\n"
        helper = mock.Mock()
        helper.ssh_options.return_value = ["-F", "none", "-o", "ConnectionAttempts=1"]
        helper.fixed_ssh_environment.return_value = {"PATH": "/usr/bin:/bin"}
        helper.run_bounded_process.return_value = (
            0,
            {"captured": stdout, "bytes": len(stdout), "exceeded": False},
            {"captured": b"", "bytes": 0, "exceeded": False},
        )
        values = self.module.run_remote_preflight(
            helper,
            Path("/usr/bin/ssh"),
            Path("/tmp/known_hosts"),
            Path("/tmp/id_ed25519"),
        )
        self.assertEqual(values["PREFLIGHT_RESULT"], "PASS")
        self.assertEqual(helper.run_bounded_process.call_count, 1)
        helper.ssh_options.assert_called_once_with(Path("/tmp/known_hosts"), Path("/tmp/id_ed25519"))
        self.assertNotIn("hostname", self.module.REMOTE_SCRIPT)
        self.assertNotIn("machine-id", self.module.REMOTE_SCRIPT)

    def test_remote_stderr_fails_without_retry_or_leak(self) -> None:
        """任意 SSH stderr 都必须令唯一调用失败，不能重试或回显正文。"""
        helper = mock.Mock()
        helper.ssh_options.return_value = []
        helper.fixed_ssh_environment.return_value = {"PATH": "/usr/bin:/bin"}
        helper.run_bounded_process.return_value = (
            0,
            {"captured": b"ignored", "bytes": 7, "exceeded": False},
            {"captured": b"sensitive", "bytes": 9, "exceeded": False},
        )
        with self.assertRaises(self.module.DirectStageError):
            self.module.run_remote_preflight(
                helper,
                Path("/usr/bin/ssh"),
                Path("/tmp/known_hosts"),
                Path("/tmp/id_ed25519"),
            )
        self.assertEqual(helper.run_bounded_process.call_count, 1)

    def test_sftp_uses_one_exclusive_batch_and_direct_identity(self) -> None:
        """SFTP 只能启动一次，第一条命令必须独占新建 010 暂存目录。"""
        helper = mock.Mock()
        helper.ssh_options.return_value = ["-F", "none", "-o", "ConnectionAttempts=1"]
        helper.fixed_ssh_environment.return_value = {"PATH": "/usr/bin:/bin"}
        helper.run_bounded_process.return_value = (
            0,
            {"captured": b"", "bytes": 0, "exceeded": False},
            {"captured": b"", "bytes": 0, "exceeded": False},
        )
        self.module.run_atomic_sftp_upload(
            helper,
            Path("/usr/bin/sftp"),
            Path("/tmp/known_hosts"),
            Path("/tmp/id_ed25519"),
            Path("/tmp/candidate"),
        )
        self.assertEqual(helper.run_bounded_process.call_count, 1)
        helper.ssh_options.assert_called_once_with(Path("/tmp/known_hosts"), Path("/tmp/id_ed25519"))
        batch = helper.run_bounded_process.call_args.kwargs["input_data"].decode("ascii")
        self.assertTrue(batch.startswith(f"mkdir {self.module.STAGING_PATH}\n"))
        self.assertEqual(batch.count("mkdir "), 1)
        self.assertEqual(batch.count("put "), 5)

    def test_helper_digest_drift_fails_before_import(self) -> None:
        """helper 摘要漂移必须在 import 前失败关闭。"""
        with mock.patch.object(self.module, "FROZEN_HELPER_SHA256", "0" * 64):
            with self.assertRaises(RuntimeError):
                self.module.load_frozen_helper()

    def test_formal_path_uses_original_identity_paths_and_candidate_snapshot(self) -> None:
        """SSH/SFTP 必须直接使用调用方原始密钥路径，SFTP 只读取候选快照。"""
        arguments = [
            str(SCRIPT_PATH),
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=C:/candidate",
            "--known-hosts=C:/Users/test/.ssh/known_hosts",
            "--identity-file=C:/Users/test/.ssh/id_ed25519",
            "--identity-public-file=C:/Users/test/.ssh/id_ed25519.pub",
        ]
        helper = mock.Mock()
        trust_evidence = mock.Mock()
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(self.module, "load_frozen_helper", return_value=helper),
            mock.patch.object(self.module, "validate_local_inputs", return_value=trust_evidence),
            mock.patch.object(self.module, "validate_candidate"),
            mock.patch.object(self.module, "assert_local_inputs_unchanged"),
            mock.patch.object(
                self.module,
                "create_frozen_candidate_snapshot",
                return_value=Path("C:/snapshot/candidate"),
            ),
            mock.patch.object(self.module, "run_remote_preflight", return_value={"DEPLOYMENT_ROOT_META": "pc:pc:700:directory"}) as remote,
            mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 0)
        self.assertEqual(remote.call_args.args[2], Path("C:/Users/test/.ssh/known_hosts"))
        self.assertEqual(remote.call_args.args[3], Path("C:/Users/test/.ssh/id_ed25519"))
        self.assertEqual(sftp.call_args.args[2], Path("C:/Users/test/.ssh/known_hosts"))
        self.assertEqual(sftp.call_args.args[3], Path("C:/Users/test/.ssh/id_ed25519"))
        self.assertEqual(sftp.call_args.args[4], Path("C:/snapshot/candidate"))

    def test_formal_path_revalidates_original_materials_around_remote_calls(self) -> None:
        """原始候选和身份材料必须在 SSH/SFTP 边界反复核对，持久漂移立即停止。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertGreaterEqual(source.count("assert_local_inputs_unchanged("), 3)
        self.assertIn("run_remote_preflight(", source)
        self.assertIn("run_atomic_sftp_upload(", source)

    def test_known_hosts_drift_is_rejected_by_public_evidence_gate(self) -> None:
        """known_hosts 在初始取证后发生变化时，公开复核入口必须动态拒绝。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / "candidate"
            candidate.mkdir()
            for name in self.module.EXPECTED_FILES:
                (candidate / name).write_bytes(name.encode("ascii"))
            known_hosts = root / "known_hosts"
            identity = root / "id_ed25519"
            identity_public = root / "id_ed25519.pub"
            known_hosts.write_text("frozen-host-entry\n", encoding="ascii")
            identity.write_text("private-test-material\n", encoding="ascii")
            identity_public.write_text("public-test-material\n", encoding="ascii")
            helper = mock.Mock()
            with mock.patch.object(self.module, "validate_candidate"):
                evidence = self.module.validate_local_inputs(
                    candidate, known_hosts, identity, identity_public, helper
                )
                known_hosts.write_text("drifted-host-entry\n", encoding="ascii")
                with self.assertRaisesRegex(RuntimeError, "local_input_drift"):
                    self.module.assert_local_inputs_unchanged(
                        candidate,
                        known_hosts,
                        identity,
                        identity_public,
                        helper,
                        evidence,
                    )

    def test_identity_pair_mismatch_stops_before_remote_calls(self) -> None:
        """现有私钥与公钥不匹配时，主入口必须在 SSH/SFTP 前失败关闭。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / "candidate"
            candidate.mkdir()
            known_hosts = root / "known_hosts"
            identity = root / "id_ed25519"
            identity_public = root / "id_ed25519.pub"
            known_hosts.write_text("known-host\n", encoding="ascii")
            identity.write_text("private-a\n", encoding="ascii")
            identity_public.write_text("public-b\n", encoding="ascii")
            helper = mock.Mock()

            def reject_mismatched_pair(private_path, public_path, _known_hosts):
                if private_path.read_text(encoding="ascii").strip().removeprefix("private-") != public_path.read_text(
                    encoding="ascii"
                ).strip().removeprefix("public-"):
                    raise RuntimeError("identity_pair_mismatch")

            helper.validate_identity_files.side_effect = reject_mismatched_pair
            arguments = [
                str(SCRIPT_PATH),
                f"--change-id={self.module.CHANGE_ID}",
                f"--candidate-dir={candidate}",
                f"--known-hosts={known_hosts}",
                f"--identity-file={identity}",
                f"--identity-public-file={identity_public}",
            ]
            with (
                mock.patch.object(sys, "argv", arguments),
                mock.patch.object(self.module, "CHANGE_ID_CONSUMED", False),
                mock.patch.object(self.module, "load_frozen_helper", return_value=helper),
                mock.patch.object(self.module, "validate_candidate"),
                mock.patch.object(self.module, "run_remote_preflight") as remote,
                mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
                mock.patch("builtins.print"),
            ):
                self.assertEqual(self.module.main(), 2)
            remote.assert_not_called()
            sftp.assert_not_called()

    def test_post_ssh_material_drift_blocks_sftp(self) -> None:
        """SSH 返回后原始材料持续漂移时，必须在唯一一次 SFTP 启动前停止。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / "candidate"
            candidate.mkdir()
            for name in self.module.EXPECTED_FILES:
                (candidate / name).write_bytes(name.encode("ascii"))
            known_hosts = root / "known_hosts"
            identity = root / "id_ed25519"
            identity_public = root / "id_ed25519.pub"
            known_hosts.write_text("frozen-host-entry\n", encoding="ascii")
            identity.write_text("private-test-material\n", encoding="ascii")
            identity_public.write_text("public-test-material\n", encoding="ascii")
            helper = mock.Mock()

            def drift_after_ssh(*_arguments, **_keywords):
                known_hosts.write_text("drifted-host-entry\n", encoding="ascii")
                return {"DEPLOYMENT_ROOT_META": "pc:pc:700:directory"}

            arguments = [
                str(SCRIPT_PATH),
                f"--change-id={self.module.CHANGE_ID}",
                f"--candidate-dir={candidate}",
                f"--known-hosts={known_hosts}",
                f"--identity-file={identity}",
                f"--identity-public-file={identity_public}",
            ]
            with (
                mock.patch.object(sys, "argv", arguments),
                mock.patch.object(self.module, "CHANGE_ID_CONSUMED", False),
                mock.patch.object(self.module, "load_frozen_helper", return_value=helper),
                mock.patch.object(self.module, "validate_candidate"),
                mock.patch.object(
                    self.module,
                    "create_frozen_candidate_snapshot",
                    return_value=candidate,
                ),
                mock.patch.object(
                    self.module,
                    "run_remote_preflight",
                    side_effect=drift_after_ssh,
                ) as remote,
                mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
                mock.patch("builtins.print"),
            ):
                self.assertEqual(self.module.main(), 2)
            remote.assert_called_once()
            sftp.assert_not_called()

    def test_consumed_gate_covers_all_entry_modes(self) -> None:
        """010 消费后，普通、本地检查和自检都必须在读取材料前拒绝。"""
        arguments_by_mode = ((), ("--local-check",), ("--self-test",))
        for extra in arguments_by_mode:
            with (
                self.subTest(extra=extra),
                mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *extra]),
                mock.patch.object(self.module, "load_frozen_helper") as helper,
                mock.patch("builtins.print") as output,
            ):
                self.assertEqual(self.module.main(), 2)
            helper.assert_not_called()
            output.assert_called_once_with(
                "G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=change_id_consumed"
            )

    def test_candidate_snapshot_contains_only_the_five_frozen_files(self) -> None:
        """本地临时快照只复制五文件候选，不能夹带 SSH 身份材料。"""
        expected = {
            "SHA256SUMS",
            "ai-gateway-reconcile",
            "g8-test-readonly-audit",
            "manifest.env",
            "molin-g8-test-readonly-audit.sudoers",
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            target = root / "target"
            source.mkdir()
            for name in expected:
                (source / name).write_bytes(name.encode("ascii"))
            with mock.patch.object(self.module, "validate_candidate") as validate:
                result = self.module.create_frozen_candidate_snapshot(source, target)
            self.assertEqual({path.name for path in result.iterdir()}, expected)
            validate.assert_called_once_with(result)

    def test_authorization_freezes_direct_wrapper_and_all_execution_boundaries(self) -> None:
        """授权清单必须冻结执行时包装器并明确 010 已消费。"""
        content = AUTHORIZATION_PATH.read_text(encoding="utf-8")
        self.assertIn("`CONSUMED_STAGED_ROOT_NOT_RUN`", content)
        self.assertIn(
            "`185c0ccda420d3bbe97e95c3218a03642372e05525d2663258287ebd981360b8`",
            content,
        )
        self.assertIn("不复制、不 chmod、不修改私钥", content)
        self.assertIn("本地检查 1 次、SSH 1 次、SFTP 1 次、root 安装 1 次、sudo self-test 1 次", content)

    def test_authorization_binds_three_live_targets_to_noclobber_cleanup(self) -> None:
        """三个 live 文件必须使用同一独占描述符，并只回滚本次新建目标。"""
        content = AUTHORIZATION_PATH.read_text(encoding="utf-8")
        self.assertEqual(content.count("created=0; cleanup()"), 3)
        self.assertEqual(content.count("trap cleanup EXIT; set -o noclobber; exec 3> \"$target\""), 3)
        self.assertEqual(content.count("exec 3>&-; /usr/bin/chown root:root \"$target\""), 3)
        self.assertEqual(content.count("trap - EXIT'"), 3)

    @unittest.skipIf(os.name == "nt", "真实 noclobber 语义由 Linux 断网门禁执行。")
    def test_root_noclobber_command_preserves_existing_target(self) -> None:
        """预存目标必须使固定写入模式非零退出且内容保持不变。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            target = root / "target"
            source.write_text("new-content", encoding="ascii")
            target.write_text("existing-content", encoding="ascii")
            shell = "/bin/bash" if Path("/bin/bash").is_file() else "/bin/sh"
            result = subprocess.run(
                [
                    shell,
                    "-ceu" if shell.endswith("bash") else "-ce",
                    'target=$1; source=$2; created=0; cleanup() { rc=$?; '
                    'if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; '
                    'exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; '
                    '/usr/bin/cat "$source" >&3; exec 3>&-; trap - EXIT',
                    "g8-010-noclobber-test",
                    str(target),
                    str(source),
                ],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(encoding="ascii"), "existing-content")


if __name__ == "__main__":
    unittest.main()

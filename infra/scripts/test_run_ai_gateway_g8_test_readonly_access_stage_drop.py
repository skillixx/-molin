#!/usr/bin/env python3
"""验证 009 Drop 只读入口候选的本地门禁、单次预检与原子暂存契约。"""

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-access-stage-drop.py")
AUTHORIZATION_PATH = SCRIPT_PATH.parents[2] / "docs" / "ai-gateway-g8-test-readonly-access-install-authorization-20260813-009.md"


def load_module():
    """按真实文件路径加载被测模块，避免依赖调用方模块搜索路径。"""
    spec = importlib.util.spec_from_file_location("g8_access_stage_drop", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_unavailable")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class TestReadonlyAccessStageDrop(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module()

    def test_candidate_is_bound_to_drop_without_physical_identity(self) -> None:
        self.assertEqual(
            self.module.CHANGE_ID,
            "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009",
        )
        self.assertEqual(self.module.TARGET_TRANSPORT, "DROP_SSH")
        self.assertEqual(self.module.PHYSICAL_HOST_IDENTITY, "NOT_APPLICABLE")
        self.assertNotIn("HOSTNAME", self.module.EXPECTED_REMOTE_KEYS)
        self.assertNotIn("MACHINE_ID_SHA256", self.module.EXPECTED_REMOTE_KEYS)

    def test_manifest_requires_drop_contract_and_frozen_receipt(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn('"TARGET_TRANSPORT": TARGET_TRANSPORT', source)
        self.assertIn('"PHYSICAL_HOST_IDENTITY": PHYSICAL_HOST_IDENTITY', source)
        self.assertIn("if set(values) != EXPECTED_MANIFEST_KEYS", source)
        self.assertIn(
            'EXPECTED_BUNDLE_RECEIPT_SHA256 = "840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd"',
            source,
        )

    def test_remote_preflight_invokes_ssh_exactly_once(self) -> None:
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
        ) + "\n"
        stdout_result = self.module.captured_result(stdout.encode("ascii"))
        stderr_result = self.module.captured_result(b"")
        with mock.patch.object(
            self.module,
            "run_bounded_process",
            return_value=(0, stdout_result, stderr_result),
        ) as run:
            values = self.module.run_remote_preflight(Path("/usr/bin/ssh"), Path("/tmp/known"), Path("/tmp/id"))
        self.assertEqual(run.call_count, 1)
        self.assertEqual(values["PREFLIGHT_RESULT"], "PASS")
        command = run.call_args.args[0]
        self.assertIn("ConnectionAttempts=1", command)
        self.assertNotIn("hostname", self.module.REMOTE_SCRIPT)
        self.assertNotIn("machine-id", self.module.REMOTE_SCRIPT)

    def test_remote_stderr_fails_without_retry_or_leak(self) -> None:
        stdout_result = self.module.captured_result(b"ignored-sensitive-output")
        stderr_result = self.module.captured_result(b"warning-sensitive-output")
        with mock.patch.object(
            self.module,
            "run_bounded_process",
            return_value=(0, stdout_result, stderr_result),
        ) as run:
            with self.assertRaises(self.module.RemoteStageError):
                self.module.run_remote_preflight(Path("/usr/bin/ssh"), Path("/tmp/known"), Path("/tmp/id"))
        self.assertEqual(run.call_count, 1)

    def test_sftp_uses_one_exclusive_batch(self) -> None:
        empty = self.module.captured_result(b"")
        with tempfile.TemporaryDirectory() as temporary:
            candidate = Path(temporary)
            with mock.patch.object(
                self.module,
                "run_bounded_process",
                return_value=(0, empty, empty),
            ) as run:
                self.module.run_atomic_sftp_upload(Path("/usr/bin/sftp"), Path("/tmp/known"), Path("/tmp/id"), candidate)
        self.assertEqual(run.call_count, 1)
        batch = run.call_args.kwargs["input_data"].decode("ascii")
        self.assertIn("-q", run.call_args.args[0])
        self.assertTrue(batch.startswith(f"mkdir {self.module.STAGING_PATH}\n"))
        self.assertEqual(batch.count("mkdir "), 1)
        self.assertEqual(batch.count("put "), 5)

    def test_local_check_never_calls_remote_or_sftp(self) -> None:
        arguments = [
            str(SCRIPT_PATH),
            "--local-check",
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=/tmp/candidate",
            "--known-hosts=/tmp/known_hosts",
            "--identity-file=/tmp/id_ed25519",
            "--identity-public-file=/tmp/id_ed25519.pub",
        ]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "validate_local_inputs"),
            mock.patch.object(self.module, "run_remote_preflight") as remote,
            mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 0)
        remote.assert_not_called()
        sftp.assert_not_called()

    def test_frozen_local_snapshot_copies_all_ssh_trust_materials(self) -> None:
        """冻结目录必须包含候选快照和三份 SSH 信任材料，并在返回前整体复核。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / "candidate-source"
            candidate.mkdir()
            trust = root / "trust-source"
            trust.mkdir()
            known_hosts = trust / "known_hosts"
            identity = trust / "id_ed25519"
            identity_public = trust / "id_ed25519.pub"
            known_hosts.write_bytes(b"known-hosts-content")
            identity.write_bytes(b"private-key-content")
            identity_public.write_bytes(b"public-key-content")
            snapshot_root = root / "snapshot"
            snapshot_root.mkdir()
            with (
                mock.patch.object(self.module, "create_frozen_candidate_snapshot") as candidate_snapshot,
                mock.patch.object(self.module, "validate_local_inputs") as validate,
            ):
                result = self.module.create_frozen_local_snapshot(
                    candidate,
                    known_hosts,
                    identity,
                    identity_public,
                    snapshot_root,
                )
            self.assertEqual(result[1].read_bytes(), b"known-hosts-content")
            self.assertEqual(result[2].read_bytes(), b"private-key-content")
            self.assertEqual(result[3].read_bytes(), b"public-key-content")
            candidate_snapshot.assert_called_once_with(candidate, result[0])
            validate.assert_called_once_with(*result)

    def test_unexpected_change_id_fails_before_local_or_network_reads(self) -> None:
        arguments = [str(SCRIPT_PATH), "--change-id=wrong"]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "validate_local_inputs") as validate,
            mock.patch.object(self.module, "run_remote_preflight") as remote,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 2)
        validate.assert_not_called()
        remote.assert_not_called()

    def test_formal_path_revalidates_local_inputs_around_sftp(self) -> None:
        """正式路径须在 SSH 前、SSH 后/SFTP 前以及 SFTP 后共核对三次本地输入。"""
        arguments = [
            str(SCRIPT_PATH),
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=/tmp/candidate",
            "--known-hosts=/tmp/known_hosts",
            "--identity-file=/tmp/id_ed25519",
            "--identity-public-file=/tmp/id_ed25519.pub",
        ]
        values = {"DEPLOYMENT_ROOT_META": "pc:pc:700:directory"}
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "validate_local_inputs") as validate,
            mock.patch.object(
                self.module,
                "create_frozen_local_snapshot",
                return_value=(
                    Path("/tmp/snapshot-candidate"),
                    Path("/tmp/snapshot-known-hosts"),
                    Path("/tmp/snapshot-id"),
                    Path("/tmp/snapshot-id.pub"),
                ),
            ),
            mock.patch.object(self.module, "fixed_tool", return_value=Path("/usr/bin/tool")),
            mock.patch.object(self.module, "run_remote_preflight", return_value=values) as remote,
            mock.patch.object(self.module, "run_atomic_sftp_upload"),
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 0)
        self.assertEqual(validate.call_count, 5)

    def test_formal_path_uploads_only_frozen_snapshot(self) -> None:
        """正式 SFTP 必须读取本次临时快照，而不是可被并发替换的原候选目录。"""
        arguments = [
            str(SCRIPT_PATH),
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=/tmp/original-candidate",
            "--known-hosts=/tmp/known_hosts",
            "--identity-file=/tmp/id_ed25519",
            "--identity-public-file=/tmp/id_ed25519.pub",
        ]
        values = {"DEPLOYMENT_ROOT_META": "pc:pc:700:directory"}
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(self.module, "validate_local_inputs"),
            mock.patch.object(
                self.module,
                "create_frozen_local_snapshot",
                return_value=(
                    Path("/tmp/snapshot-candidate"),
                    Path("/tmp/snapshot-known-hosts"),
                    Path("/tmp/snapshot-id"),
                    Path("/tmp/snapshot-id.pub"),
                ),
            ) as create_snapshot,
            mock.patch.object(self.module, "fixed_tool", return_value=Path("/usr/bin/tool")),
            mock.patch.object(self.module, "run_remote_preflight", return_value=values) as remote,
            mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 0)
        snapshot = create_snapshot.return_value
        self.assertNotEqual(snapshot[0], Path("/tmp/original-candidate"))
        self.assertEqual(remote.call_args.args[1:3], snapshot[1:3])
        self.assertEqual(sftp.call_args.args[1:4], snapshot[1:3] + snapshot[0:1])

    def test_local_replacement_after_ssh_blocks_sftp(self) -> None:
        """SSH 后复核发现候选或身份材料漂移时，必须在 SFTP 前停止。"""
        arguments = [
            str(SCRIPT_PATH),
            f"--change-id={self.module.CHANGE_ID}",
            "--candidate-dir=/tmp/candidate",
            "--known-hosts=/tmp/known_hosts",
            "--identity-file=/tmp/id_ed25519",
            "--identity-public-file=/tmp/id_ed25519.pub",
        ]
        values = {"DEPLOYMENT_ROOT_META": "pc:pc:700:directory"}
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(
                self.module,
                "validate_local_inputs",
                side_effect=(None, RuntimeError("local_input_replaced")),
            ),
            mock.patch.object(
                self.module,
                "create_frozen_local_snapshot",
                return_value=(
                    Path("/tmp/snapshot-candidate"),
                    Path("/tmp/snapshot-known-hosts"),
                    Path("/tmp/snapshot-id"),
                    Path("/tmp/snapshot-id.pub"),
                ),
            ),
            mock.patch.object(self.module, "fixed_tool", return_value=Path("/usr/bin/tool")),
            mock.patch.object(self.module, "run_remote_preflight", return_value=values),
            mock.patch.object(self.module, "run_atomic_sftp_upload") as sftp,
            mock.patch("builtins.print"),
        ):
            self.assertEqual(self.module.main(), 2)
        sftp.assert_not_called()

    @unittest.skipIf(os.name == "nt", "真实 noclobber 语义由 Linux 断网门禁执行。")
    def test_root_noclobber_pattern_preserves_existing_target(self) -> None:
        """预存目标必须令固定文件描述符写入模式非零退出且内容保持不变。"""
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
                    'if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /bin/rm -f -- "$target"; fi; '
                    'exit "$rc"; }; trap cleanup EXIT; set -C; exec 3> "$target"; created=1; '
                    '/bin/cat "$source" >&3; exec 3>&-; trap - EXIT',
                    "g8-noclobber-test",
                    str(target),
                    str(source),
                ],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(encoding="ascii"), "existing-content")

    def test_authorization_binds_all_live_installs_to_owned_cleanup_trap(self) -> None:
        """三个 live 安装命令都必须使用同一独占描述符和本次创建标志。"""
        content = AUTHORIZATION_PATH.read_text(encoding="utf-8")
        self.assertEqual(content.count("created=0; cleanup()"), 3)
        self.assertEqual(content.count("trap cleanup EXIT; set -o noclobber; exec 3> \"$target\""), 3)
        self.assertEqual(content.count("exec 3>&-; /usr/bin/chown root:root \"$target\""), 3)
        self.assertEqual(content.count("trap - EXIT'"), 3)

    @unittest.skipIf(os.name == "nt", "真实失败回滚语义由 Linux 断网门禁执行。")
    def test_root_noclobber_pattern_removes_only_partial_new_target(self) -> None:
        """独占创建成功后若复制失败，只能删除本次刚创建的空目标。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            missing_source = root / "missing-source"
            target = root / "target"
            shell = "/bin/bash" if Path("/bin/bash").is_file() else "/bin/sh"
            result = subprocess.run(
                [
                    shell,
                    "-ceu" if shell.endswith("bash") else "-ce",
                    'target=$1; source=$2; created=0; cleanup() { rc=$?; '
                    'if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /bin/rm -f -- "$target"; fi; '
                    'exit "$rc"; }; trap cleanup EXIT; set -C; exec 3> "$target"; created=1; '
                    '/bin/cat "$source" >&3; exec 3>&-; trap - EXIT',
                    "g8-noclobber-test",
                    str(target),
                    str(missing_source),
                ],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(target.exists())


if __name__ == "__main__":
    unittest.main()

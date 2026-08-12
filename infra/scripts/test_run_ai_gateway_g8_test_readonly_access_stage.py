#!/usr/bin/env python3
"""验证 G8 测试服只读预检与暂存上传严格排序并失败关闭。"""

import base64
import hashlib
import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-access-stage.py")


def load_module():
    """从精确脚本路径加载模块，测试不会从 PATH 寻找替代实现。"""
    spec = importlib.util.spec_from_file_location("g8_readonly_preflight", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_load_failed")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


def remote_stdout(meta: str = "pc:pc:755") -> str:
    """生成唯一允许的低敏远端输出。"""
    return "\n".join(
        (
            f"PREFLIGHT_CHANGE_ID={MODULE.CHANGE_ID}",
            "LOGIN_USER=pc",
            f"HOSTNAME={MODULE.TARGET_HOSTNAME}",
            f"MACHINE_ID_SHA256={MODULE.TARGET_MACHINE_ID_SHA256}",
            f"DEPLOYMENT_ROOT_REALPATH={MODULE.TARGET_DEPLOYMENT_ROOT}",
            f"DEPLOYMENT_ROOT_META={meta}",
            "STAGING_ABSENT=true",
            "INSTALL_TARGETS_ABSENT=true",
            "PREFLIGHT_RESULT=PASS",
        )
    ) + "\n"


class TestReadonlyAccessPreflight(unittest.TestCase):
    def test_remote_script_avoids_cross_shell_cut_quoting(self) -> None:
        self.assertNotIn("cut", MODULE.REMOTE_SCRIPT)
        self.assertNotIn("awk", MODULE.REMOTE_SCRIPT)
        self.assertIn("machine_id_sha256=${machine_id_line%% *}", MODULE.REMOTE_SCRIPT)
        self.assertIn(MODULE.STAGING_PATH, MODULE.REMOTE_SCRIPT)

    def test_self_test_requires_isolated_python(self) -> None:
        isolated = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(isolated.returncode, 0, isolated.stderr)
        self.assertEqual(isolated.stdout.strip(), "G8_TEST_READONLY_ACCESS_STAGE_SELF_TEST=PASS")
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
            "G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=isolated_python_required",
        )

    def test_remote_preflight_invokes_ssh_exactly_once(self) -> None:
        completed = subprocess.CompletedProcess([], 0, remote_stdout(), "")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            values = MODULE.run_remote_preflight(
                Path("/usr/bin/ssh"), Path("/tmp/known_hosts"), Path("/tmp/id_ed25519")
            )
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        self.assertNotIn(MODULE.REMOTE_SCRIPT, command)
        self.assertEqual(command[-5:], ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/bin/sh", "-s"])
        self.assertEqual(run.call_args.kwargs["input"], MODULE.REMOTE_SCRIPT)
        self.assertIn("NumberOfPasswordPrompts=0", command)
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("IdentitiesOnly=yes", command)
        self.assertIn(f"IdentityFile={Path('/tmp/id_ed25519')}", command)
        self.assertIn("ClearAllForwardings=yes", command)
        environment = run.call_args.kwargs["env"]
        self.assertNotIn("SSH_AUTH_SOCK", environment)
        self.assertNotIn("SSH_ASKPASS", environment)
        self.assertIn(environment["PATH"], (r"C:\Windows\System32\OpenSSH;C:\Windows\System32", "/usr/bin:/bin"))
        self.assertEqual(values["PREFLIGHT_RESULT"], "PASS")

    def test_remote_failure_never_retries_or_leaks_output(self) -> None:
        completed = subprocess.CompletedProcess([], 1, "TOKEN_SHOULD_NOT_LEAK", "remote error")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            with self.assertRaises(MODULE.RemotePreflightError):
                MODULE.run_remote_preflight(
                    Path("/usr/bin/ssh"), Path("/tmp/known_hosts"), Path("/tmp/id_ed25519")
                )
        self.assertEqual(run.call_count, 1)

    def test_sftp_atomically_creates_new_stage_and_uploads_five_files_once(self) -> None:
        completed = subprocess.CompletedProcess([], 0, "", "")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            MODULE.run_atomic_sftp_upload(
                Path("/usr/bin/sftp"),
                Path("/tmp/known_hosts"),
                Path("/tmp/id_ed25519"),
                Path("/tmp/candidate"),
            )
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        batch = run.call_args.kwargs["input"]
        self.assertEqual(batch.splitlines()[0], f"mkdir {MODULE.STAGING_PATH}")
        self.assertNotIn("-mkdir", batch)
        self.assertEqual(sum(line.startswith("put ") for line in batch.splitlines()), 5)
        self.assertIn("IdentitiesOnly=yes", command)
        self.assertIn("ConnectionAttempts=1", command)

    def test_sftp_failure_is_not_retried(self) -> None:
        completed = subprocess.CompletedProcess([], 1, "partial", "failure")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            with self.assertRaises(MODULE.RemotePreflightError):
                MODULE.run_atomic_sftp_upload(
                    Path("/usr/bin/sftp"),
                    Path("/tmp/known_hosts"),
                    Path("/tmp/id_ed25519"),
                    Path("/tmp/candidate"),
                )
        self.assertEqual(run.call_count, 1)

    def test_remote_output_and_deployment_permissions_are_exact(self) -> None:
        self.assertEqual(MODULE.parse_remote_output(remote_stdout("pc:pc:755"))["PREFLIGHT_RESULT"], "PASS")
        for output in (
            remote_stdout("root:root:755"),
            remote_stdout("pc:pc:775"),
            remote_stdout() + "EXTRA=value\n",
            remote_stdout().replace("HOSTNAME=", "HOSTNAME=wrong\nDUPLICATE="),
        ):
            with self.subTest(output=output), self.assertRaises(MODULE.RemotePreflightError):
                MODULE.parse_remote_output(output)

    def test_known_host_fingerprint_is_computed_without_external_tools(self) -> None:
        key_blob = b"fixed-test-ed25519-key-blob"
        encoded = base64.b64encode(key_blob).decode("ascii")
        fingerprint = "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")
        with tempfile.TemporaryDirectory(prefix="g8-known-host-") as temporary:
            path = Path(temporary) / "known_hosts"
            path.write_text(f"[{MODULE.TARGET_HOST}]:{MODULE.TARGET_PORT} ssh-ed25519 {encoded}\n", encoding="ascii")
            with mock.patch.object(MODULE, "TARGET_SSH_ED25519_FINGERPRINT", fingerprint):
                MODULE.validate_known_hosts(path)
            path.write_text(path.read_text(encoding="ascii") * 2, encoding="ascii")
            with mock.patch.object(MODULE, "TARGET_SSH_ED25519_FINGERPRINT", fingerprint):
                with self.assertRaises(RuntimeError):
                    MODULE.validate_known_hosts(path)

    def test_identity_file_must_share_fixed_ssh_directory(self) -> None:
        key_blob = b"fixed-local-identity-key"
        encoded = base64.b64encode(key_blob).decode("ascii")
        fingerprint = "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")
        with tempfile.TemporaryDirectory(prefix="g8-identity-") as temporary:
            ssh_dir = Path(temporary) / ".ssh"
            ssh_dir.mkdir()
            known_hosts = ssh_dir / "known_hosts"
            identity = ssh_dir / "id_ed25519"
            public = ssh_dir / "id_ed25519.pub"
            known_hosts.write_text("placeholder", encoding="ascii")
            identity.write_text("placeholder", encoding="ascii")
            public.write_text(f"ssh-ed25519 {encoded} test\n", encoding="ascii")
            with mock.patch.object(MODULE, "LOCAL_IDENTITY_ED25519_FINGERPRINT", fingerprint):
                MODULE.validate_identity_file(identity, public, known_hosts)
            wrong = Path(temporary) / "id_ed25519"
            wrong.write_text("placeholder", encoding="ascii")
            with mock.patch.object(MODULE, "LOCAL_IDENTITY_ED25519_FINGERPRINT", fingerprint):
                with self.assertRaises(RuntimeError):
                    MODULE.validate_identity_file(wrong, public, known_hosts)

    def test_identity_pair_uses_fixed_ssh_keygen_once(self) -> None:
        completed = subprocess.CompletedProcess([], 0, "ssh-ed25519 AAAA\n", "")
        with tempfile.TemporaryDirectory(prefix="g8-keypair-") as temporary:
            identity = Path(temporary) / "id_ed25519"
            public = Path(temporary) / "id_ed25519.pub"
            identity.write_text("private", encoding="ascii")
            public.write_text("ssh-ed25519 AAAA test\n", encoding="ascii")
            with mock.patch.object(MODULE.Path, "is_file", return_value=True), mock.patch.object(
                MODULE.subprocess, "run", return_value=completed
            ) as run:
                MODULE.validate_identity_pair(identity, public)
        self.assertEqual(run.call_count, 1)
        self.assertEqual(run.call_args.args[0][1:3], ["-y", "-f"])
        self.assertIs(run.call_args.kwargs["stdin"], subprocess.DEVNULL)

    def test_candidate_validator_checks_all_files_and_receipt(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-candidate-") as temporary:
            candidate = Path(temporary) / "candidate"
            candidate.mkdir()
            contents = {
                "ai-gateway-reconcile": b"reconcile",
                "g8-test-readonly-audit": b"auditor",
                "molin-g8-test-readonly-audit.sudoers": b"sudoers",
            }
            for name, value in contents.items():
                (candidate / name).write_bytes(value)
            hashes = {name: hashlib.sha256(value).hexdigest() for name, value in contents.items()}
            manifest = "\n".join(
                (
                    f"CHANGE_ID={MODULE.CHANGE_ID}",
                    f"SOURCE_COMMIT={MODULE.SOURCE_COMMIT}",
                    f"SOURCE_TREE={MODULE.SOURCE_TREE}",
                    f"TARGET_DEPLOYMENT_ROOT={MODULE.TARGET_DEPLOYMENT_ROOT}",
                    f"AUDITOR_SHA256={hashes['g8-test-readonly-audit']}",
                    f"SUDOERS_SHA256={hashes['molin-g8-test-readonly-audit.sudoers']}",
                    f"RECONCILE_SHA256={hashes['ai-gateway-reconcile']}",
                    "RECONCILE_SIZE=9",
                )
            ) + "\n"
            (candidate / "manifest.env").write_text(manifest, encoding="ascii")
            checksums = "".join(
                f"{hashlib.sha256((candidate / name).read_bytes()).hexdigest()}  {name}\n"
                for name in (
                    "ai-gateway-reconcile",
                    "g8-test-readonly-audit",
                    "manifest.env",
                    "molin-g8-test-readonly-audit.sudoers",
                )
            )
            (candidate / "SHA256SUMS").write_text(checksums, encoding="ascii")
            with (
                mock.patch.object(MODULE, "EXPECTED_BUNDLE_RECEIPT_SHA256", MODULE.sha256(candidate / "SHA256SUMS")),
                mock.patch.object(MODULE, "FROZEN_AUDITOR_SHA256", hashes["g8-test-readonly-audit"]),
                mock.patch.object(MODULE, "FROZEN_SUDOERS_SHA256", hashes["molin-g8-test-readonly-audit.sudoers"]),
                mock.patch.object(MODULE, "FROZEN_RECONCILE_SHA256", hashes["ai-gateway-reconcile"]),
                mock.patch.object(MODULE, "FROZEN_RECONCILE_SIZE", 9),
            ):
                MODULE.validate_candidate(candidate)
                (candidate / "unexpected").write_text("x", encoding="ascii")
                with self.assertRaises(RuntimeError):
                    MODULE.validate_candidate(candidate)

    def test_only_003_is_accepted_by_cli_contract(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn('CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"', source)
        self.assertNotIn('CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-002"', source)
        self.assertIn("validate_candidate(candidate_dir)", source)
        self.assertIn("validate_known_hosts(known_hosts)", source)
        self.assertIn("validate_identity_file(identity_file, identity_public_file, known_hosts)", source)
        self.assertIn("validate_identity_pair(identity_file, identity_public_file)", source)
        self.assertIn("run_remote_preflight(fixed_ssh_executable(), known_hosts, identity_file)", source)
        self.assertIn("run_atomic_sftp_upload(fixed_sftp_executable(), known_hosts, identity_file, candidate_dir)", source)


if __name__ == "__main__":
    unittest.main()

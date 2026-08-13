import contextlib
import importlib.util
import io
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("diagnose-ai-gateway-g8-local-ssh-materials.py")


def load_module():
    spec = importlib.util.spec_from_file_location("g8_local_ssh_materials", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class LocalMaterialsDiagnosticTests(unittest.TestCase):
    def setUp(self):
        self.module = load_module()

    def call_main(self, *arguments):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]):
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                code = self.module.main()
        return code, stdout.getvalue(), stderr.getvalue()

    def test_self_test_is_offline_and_passes(self):
        with mock.patch.object(self.module.subprocess, "run") as run:
            code, stdout, stderr = self.call_main("--self-test")
        self.assertEqual((code, stdout, stderr), (0, "G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC_SELF_TEST=PASS\n", ""))
        run.assert_not_called()

    def test_source_has_no_remote_transport_capability(self):
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        forbidden = ("subprocess.Popen", "socket.", "sftp", "scp ", "pc@", "python3 -I -")
        for token in forbidden:
            with self.subTest(token=token):
                self.assertNotIn(token, source)

    def test_invalid_request_is_low_sensitive(self):
        sentinel = "DO_NOT_ECHO_SECRET_SENTINEL"
        code, stdout, stderr = self.call_main("--unknown", sentinel)
        self.assertEqual(code, 2)
        self.assertEqual(stdout, "G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=invalid_request\n")
        self.assertEqual(stderr, "")
        self.assertNotIn(sentinel, stdout + stderr)

    def test_relative_paths_are_rejected(self):
        code, stdout, stderr = self.call_main(
            "--known-hosts", "relative-known-hosts",
            "--identity-file", "relative-key",
            "--identity-public-key", "relative-key.pub",
        )
        self.assertEqual(code, 2)
        self.assertEqual(stdout, "G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=invalid_request\n")
        self.assertEqual(stderr, "")

    def test_freeze_file_rejects_symlink(self):
        if os.name == "nt":
            self.skipTest("Windows 创建符号链接通常需要额外权限。")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / "target"
            link = root / "link"
            target.write_bytes(b"approved")
            link.symlink_to(target)
            with self.assertRaises(self.module.DiagnosticError):
                self.module.freeze_file(link)

    def test_freeze_file_reads_same_descriptor_and_detects_entry_replacement(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "material"
            path.write_bytes(b"approved")
            original_fstat = self.module.os.fstat
            calls = 0

            def replace_after_read(fd):
                nonlocal calls
                result = original_fstat(fd)
                calls += 1
                if calls == 2:
                    replacement = path.with_suffix(".replacement")
                    replacement.write_bytes(b"replaced")
                    os.replace(replacement, path)
                return result

            with mock.patch.object(self.module.os, "fstat", side_effect=replace_after_read):
                with self.assertRaises(self.module.DiagnosticError):
                    self.module.freeze_file(path)

    def test_known_hosts_allows_other_algorithms_but_returns_only_approved_ed25519(self):
        approved = "[8.130.9.163]:10003 ssh-ed25519 AAAAapproved"
        lookup = "\n".join((approved, "[8.130.9.163]:10003 ssh-rsa AAAArsa")) + "\n"
        responses = [
            self.module.CommandResult(0, lookup.encode("ascii"), b""),
            self.module.CommandResult(0, b"256 SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I host (ED25519)\n", b""),
        ]
        with mock.patch.object(self.module, "run_ssh_keygen", side_effect=responses):
            line = self.module.find_approved_host_key(Path("C:/fixed/known_hosts"), Path("C:/fixed/ssh-keygen.exe"))
        self.assertEqual(line, approved)

    def test_known_hosts_rejects_duplicate_ed25519(self):
        lookup = "\n".join((
            "[8.130.9.163]:10003 ssh-ed25519 AAAAapproved",
            "|1|hash|value ssh-ed25519 AAAAevil",
        )) + "\n"
        with mock.patch.object(
            self.module,
            "run_ssh_keygen",
            return_value=self.module.CommandResult(0, lookup.encode("ascii"), b""),
        ):
            with self.assertRaises(self.module.DiagnosticError):
                self.module.find_approved_host_key(Path("C:/fixed/known_hosts"), Path("C:/fixed/ssh-keygen.exe"))

    def test_identity_pair_mismatch_is_rejected(self):
        responses = [
            self.module.CommandResult(0, b"ssh-ed25519 AAAAderived\n", b""),
        ]
        with mock.patch.object(self.module, "run_ssh_keygen", side_effect=responses):
            with self.assertRaises(self.module.DiagnosticError):
                self.module.validate_identity_pair(
                    Path("C:/fixed/key"),
                    b"ssh-ed25519 AAAAdeclared comment\n",
                    Path("C:/fixed/ssh-keygen.exe"),
                )


if __name__ == "__main__":
    unittest.main()

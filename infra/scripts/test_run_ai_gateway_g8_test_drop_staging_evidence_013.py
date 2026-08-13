import contextlib
import hashlib
import importlib.util
import io
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-drop-staging-evidence-013.py")


def load_module():
    spec = importlib.util.spec_from_file_location("g8_staging_evidence_013", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class StagingEvidence013Tests(unittest.TestCase):
    def setUp(self):
        self.module = load_module()

    def call_main(self, *arguments):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *arguments]):
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                code = self.module.main()
        return code, stdout.getvalue(), stderr.getvalue()

    def remote_output(self, state="ABSENT", integrity="NOT_APPLICABLE", reason="NONE"):
        values = {
            "EVIDENCE_CHANGE_ID": self.module.CHANGE_ID,
            "TARGET_CHANGE_ID": self.module.TARGET_CHANGE_ID,
            "LOGIN_USER": "pc",
            "DEPLOYMENT_ROOT_REALPATH": "/home/pc/molin",
            "DEPLOYMENT_ROOT_CHECK": "PASS",
            "STAGING_STATE": state,
            "STAGING_INTEGRITY": integrity,
            "STAGING_MISMATCH_REASON": reason,
            "EVIDENCE_RESULT": "PASS",
        }
        return "".join(f"{key}={value}\n" for key, value in values.items()).encode("ascii")

    def test_self_test_is_offline(self):
        with mock.patch.object(self.module, "run_once") as run_once:
            code, stdout, stderr = self.call_main("--self-test")
        self.assertEqual((code, stdout, stderr), (0, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013_SELF_TEST=PASS\n", ""))
        run_once.assert_not_called()

    def test_local_check_is_not_an_entry(self):
        code, stdout, stderr = self.call_main("--local-check")
        self.assertEqual((code, stderr), (2, ""))
        self.assertEqual(stdout, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=FAILED reason=evidence_unavailable\n")

    def test_source_does_not_import_consumed_012(self):
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertNotIn("staging-evidence-012", source)
        self.assertNotIn("import_012", source)

    def test_parser_accepts_three_legal_states(self):
        absent = self.module.parse_remote_output(self.remote_output())
        present = self.module.parse_remote_output(self.remote_output("PRESENT", "PASS", "NONE"))
        mismatch = self.module.parse_remote_output(self.remote_output("PRESENT", "MISMATCH", "FILE_SET"))
        self.assertEqual((absent.state, present.integrity, mismatch.reason), ("ABSENT", "PASS", "FILE_SET"))

    def test_parser_rejects_extra_duplicate_non_ascii_and_wrong_change_id(self):
        cases = (
            self.remote_output() + b"EXTRA=value\n",
            self.remote_output() + b"STAGING_STATE=ABSENT\n",
            self.remote_output() + b"\xff",
            self.remote_output().replace(self.module.CHANGE_ID.encode(), b"WRONG"),
        )
        for payload in cases:
            with self.subTest(payload=payload[-30:]):
                with self.assertRaises(self.module.EvidenceError):
                    self.module.parse_remote_output(payload)

    def test_render_result_is_six_lines_and_exit_codes_are_stable(self):
        absent = self.module.parse_remote_output(self.remote_output())
        mismatch = self.module.parse_remote_output(self.remote_output("PRESENT", "MISMATCH", "MANIFEST"))
        absent_code, absent_text = self.module.render_result(absent)
        mismatch_code, mismatch_text = self.module.render_result(mismatch)
        self.assertEqual(absent_code, 0)
        self.assertEqual(mismatch_code, 3)
        self.assertEqual(len(absent_text.splitlines()), 6)
        self.assertTrue(absent_text.startswith("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=PASS\n"))
        self.assertTrue(mismatch_text.startswith("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=MISMATCH\n"))

    def test_stream_capture_is_bounded_but_hashes_full_stream(self):
        payload = b"x" * (192 * 1024)
        capture = self.module.collect_stream(io.BytesIO(payload), 64 * 1024)
        self.assertEqual(len(capture.data), 64 * 1024 + 1)
        self.assertEqual(capture.byte_count, len(payload))
        self.assertEqual(capture.sha256, hashlib.sha256(payload).hexdigest())
        self.assertTrue(capture.exceeded)

    def test_run_once_uses_one_process_and_fixed_options(self):
        process = mock.Mock()
        process.stdin = io.BytesIO()
        process.stdout = io.BytesIO(self.remote_output())
        process.stderr = io.BytesIO(b"")
        process.wait.return_value = 0
        materials = mock.Mock()
        materials.approved_host_line = "[8.130.9.163]:10003 ssh-ed25519 AAAAapproved"
        materials.identity_file.path = Path("C:/fixed/id_ed25519")
        helper = mock.Mock(unsafe=True)
        helper.assert_materials_unchanged.return_value = None
        with mock.patch.object(self.module.subprocess, "Popen", return_value=process) as popen:
            result = self.module.run_once(helper, materials, Path("C:/fixed/ssh.exe"))
        self.assertEqual(result.state, "ABSENT")
        self.assertEqual(popen.call_count, 1)
        command = popen.call_args.args[0]
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", command)
        self.assertNotIn("-R", command)

    def test_any_stderr_fails_closed(self):
        process = mock.Mock()
        process.stdin = io.BytesIO()
        process.stdout = io.BytesIO(self.remote_output())
        process.stderr = io.BytesIO(b"low-level detail")
        process.wait.return_value = 0
        materials = mock.Mock()
        materials.approved_host_line = "[8.130.9.163]:10003 ssh-ed25519 AAAAapproved"
        materials.identity_file.path = Path("C:/fixed/id_ed25519")
        helper = mock.Mock(unsafe=True)
        with mock.patch.object(self.module.subprocess, "Popen", return_value=process):
            with self.assertRaises(self.module.EvidenceError):
                self.module.run_once(helper, materials, Path("C:/fixed/ssh.exe"))

    def test_consumed_gate_precedes_parser_helper_and_network(self):
        self.module.CHANGE_ID_CONSUMED = True
        sentinel = "DO_NOT_ECHO_SECRET_SENTINEL"
        with mock.patch.object(self.module, "load_local_diagnostic") as load_helper:
            with mock.patch.object(self.module, "run_once") as run_once:
                code, stdout, stderr = self.call_main("--unknown", sentinel)
        self.assertEqual((code, stderr), (2, ""))
        self.assertEqual(stdout, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=FAILED reason=change_id_consumed\n")
        self.assertNotIn(sentinel, stdout + stderr)
        load_helper.assert_not_called()
        run_once.assert_not_called()

    def test_remote_program_has_no_write_or_privilege_capability(self):
        remote = self.module.build_remote_program()
        for token in ("O_WRONLY", "O_RDWR", "O_CREAT", "subprocess", "socket", "/usr/bin/sudo", "unlink(", "rename(", "mkdir("):
            with self.subTest(token=token):
                self.assertNotIn(token, remote)
        compile(remote, "<g8-013-remote>", "exec")

    def build_posix_fixture(self, root: Path):
        stage = root / self.module.TARGET_STAGE_NAME
        stage.mkdir(mode=0o700)
        manifest = {"CHANGE_ID": self.module.TARGET_CHANGE_ID, "TEST_ONLY": "1"}
        payloads = {
            "ai-gateway-reconcile": b"reconcile",
            "g8-test-readonly-audit": b"audit",
            "manifest.env": "".join(f"{key}={value}\n" for key, value in manifest.items()).encode("ascii"),
            "molin-g8-test-readonly-audit.sudoers": b"rule",
        }
        modes = {
            "ai-gateway-reconcile": 0o700,
            "g8-test-readonly-audit": 0o700,
            "manifest.env": 0o600,
            "molin-g8-test-readonly-audit.sudoers": 0o600,
        }
        for name, data in payloads.items():
            path = stage / name
            path.write_bytes(data)
            path.chmod(modes[name])
        receipt = "".join(
            f"{hashlib.sha256(payloads[name]).hexdigest()}  {name}\n"
            for name in sorted(payloads)
        ).encode("ascii")
        (stage / "SHA256SUMS").write_bytes(receipt)
        (stage / "SHA256SUMS").chmod(0o600)
        expected = {}
        for name in ("SHA256SUMS", *payloads):
            path = stage / name
            expected[name] = (hashlib.sha256(path.read_bytes()).hexdigest(), path.stat().st_size, stat.S_IMODE(path.stat().st_mode))
        return stage, expected, manifest

    @unittest.skipIf(os.name == "nt", "POSIX fd/uid/gid 动态语义由 Linux 断网门禁执行。")
    def test_remote_program_reports_present_pass_for_complete_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            root.chmod(0o700)
            _, expected, manifest = self.build_posix_fixture(root)
            program = self.module.build_remote_program(
                _test_root=str(root), _test_files=expected, _test_manifest=manifest,
                _test_uid=os.getuid(), _test_gid=os.getgid(),
            )
            completed = subprocess.run([sys.executable, "-I", "-c", program], capture_output=True, check=False)
        self.assertEqual(completed.returncode, 0)
        self.assertEqual(completed.stderr, b"")
        self.assertIn(b"STAGING_STATE=PRESENT\n", completed.stdout)
        self.assertIn(b"STAGING_INTEGRITY=PASS\n", completed.stdout)

    @unittest.skipIf(os.name == "nt", "POSIX fd/uid/gid 动态语义由 Linux 断网门禁执行。")
    def test_remote_program_reports_file_set_mismatch(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            root.chmod(0o700)
            stage, expected, manifest = self.build_posix_fixture(root)
            (stage / "manifest.env").unlink()
            program = self.module.build_remote_program(
                _test_root=str(root), _test_files=expected, _test_manifest=manifest,
                _test_uid=os.getuid(), _test_gid=os.getgid(),
            )
            completed = subprocess.run([sys.executable, "-I", "-c", program], capture_output=True, check=False)
        self.assertEqual(completed.returncode, 0)
        self.assertEqual(completed.stderr, b"")
        self.assertIn(b"STAGING_INTEGRITY=MISMATCH\n", completed.stdout)
        self.assertIn(b"STAGING_MISMATCH_REASON=FILE_SET\n", completed.stdout)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 G8 测试服只读入口候选包生成器的输入、制品和安全边界。"""

import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-bundle.py")


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestReadonlyAccessBundle(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = SCRIPT_PATH.read_text(encoding="utf-8")

    def run_script(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), *arguments],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )

    def test_syntax_and_self_test(self) -> None:
        syntax = subprocess.run(
            ["python", "-m", "py_compile", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(syntax.returncode, 0, syntax.stderr)
        result = self.run_script("--self-test")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "G8_TEST_READONLY_ACCESS_BUNDLE_SELF_TEST=PASS")

        non_isolated = subprocess.run(
            ["python", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(non_isolated.returncode, 2)
        self.assertEqual(
            non_isolated.stdout.strip(),
            "G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=isolated_python_required",
        )

    def test_invalid_inputs_fail_before_output(self) -> None:
        for arguments in (
            (),
            ("--change-id=bad", "--source-commit=" + "0" * 40, "--output-dir=/tmp/x"),
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-002", "--source-commit=" + "0" * 40, "--output-dir=/tmp/x"),
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001", "--source-commit=short", "--output-dir=/tmp/x"),
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001", "--source-commit=" + "0" * 40, "--output-dir=relative"),
        ):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)

    def test_consumed_change_id_cannot_create_persistent_bundle(self) -> None:
        with tempfile.TemporaryDirectory(prefix="g8-consumed-cli-") as temporary:
            output = Path(temporary) / "bundle"
            result = self.run_script(
                "--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001",
                "--source-commit=c50f092339fcad79ca1262925480219db1755318",
                f"--output-dir={output}",
            )
            self.assertEqual(result.returncode, 2)
            self.assertFalse(output.exists())
            self.assertEqual(
                result.stdout.strip(),
                "G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=invalid_request",
            )

    def test_consumed_candidate_verification_is_ephemeral(self) -> None:
        self.assertIn('TemporaryDirectory(prefix="molin-g8-consumed-verify-")', self.source)
        self.assertIn('print("G8_TEST_READONLY_ACCESS_BUNDLE_VERIFY=PASS")', self.source)

    def test_source_comes_from_exact_git_archive_and_build_is_repeated(self) -> None:
        self.assertIn('[git, "archive", source_commit]', self.source)
        self.assertEqual(self.source.count('[go, "build", "-trimpath"'), 1)
        self.assertIn('"REPRODUCIBLE_BUILD_COUNT": "2"', self.source)
        self.assertIn("APPROVED_SOURCE_COMMIT", self.source)
        self.assertIn("APPROVED_SOURCE_TREE", self.source)
        self.assertIn("APPROVED_CHANGE_ID", self.source)

    def test_bundle_contains_only_fixed_low_sensitive_assets(self) -> None:
        for name in (
            "g8-test-readonly-audit",
            "molin-g8-test-readonly-audit.sudoers",
            "ai-gateway-reconcile",
            "manifest.env",
            "SHA256SUMS",
        ):
            self.assertIn(name, self.source)
        self.assertNotRegex(self.source, r"\b(?:scp|sudo|install|systemctl|docker)\b")
        self.assertNotIn(".env.test", self.source)
        self.assertNotIn("MYSQL_PASSWORD", self.source)
        self.assertIn('"TARGET_MACHINE_ID_SHA256":', self.source)
        self.assertIn('"TARGET_SSH_ED25519_FINGERPRINT":', self.source)

    def test_output_must_be_absolute_and_new(self) -> None:
        self.assertIn("not output_dir.is_absolute() or output_dir.exists()", self.source)
        self.assertIn("output_dir.mkdir(mode=0o700)", self.source)
        self.assertIn("if output_created and output_dir.is_dir()", self.source)

    def test_temporary_cleanup_is_scoped_to_mktemp_directory(self) -> None:
        self.assertIn('TemporaryDirectory(prefix="molin-g8-access-")', self.source)
        self.assertIn("shutil.rmtree(output_dir)", self.source)

    def test_build_environment_is_fixed(self) -> None:
        for name in ("GOENV", "GOWORK", "GOTOOLCHAIN", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"):
            self.assertIn(f'"{name}"', self.source)
        self.assertIn('"GOCACHE": str(temporary_root / "go-build-cache")', self.source)
        self.assertIn('"GOMODCACHE": str(temporary_root / "go-module-cache")', self.source)
        self.assertIn('[go, "mod", "download"]', self.source)
        self.assertIn('if go_version != "go1.26.5"', self.source)
        self.assertIn('"/opt/hostedtoolcache/go/1.26.5/x64/bin"', self.source)
        for name in ("GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_COUNT", "core.autocrlf", "core.eol"):
            self.assertIn(f'"{name}"', self.source)

    def test_caller_environment_is_removed_before_tools_run(self) -> None:
        self.assertIn("env=environment", self.source)
        self.assertIn("if change_id != APPROVED_CHANGE_ID", self.source)
        self.assertIn("script_path = Path(__file__).resolve(strict=True)", self.source)
        self.assertIn("safe_extract", self.source)

        result = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            env={**os.environ, "TAR_OPTIONS": "--checkpoint=1 --checkpoint-action=exec=echo BAD"},
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("BAD", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 G8 测试服只读入口候选包生成器的输入、制品和安全边界。"""

import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-bundle.py")
CI_PATH = SCRIPT_PATH.parents[2] / ".github" / "workflows" / "ci.yml"
CONSUMED_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
CONSUMED_SOURCE_COMMIT = "8ec878572f62ef2584c38aaadc1bca1cb802b13f"
CONSUMED_SOURCE_TREE = "988bdcdc8017322264733ebe68876e4811b01412"
CONSUMED_DROP_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009"
CONSUMED_DROP_SOURCE_COMMIT = "7f3325e2d6801567fea34a2049a2f3ada114e348"
CONSUMED_DROP_SOURCE_TREE = "4563feb59850dca87789adfb5eea820f78b1a209"
CONSUMED_DIRECT_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010"
CONSUMED_DIRECT_SOURCE_COMMIT = "75b1fc4ddb7138495547cec03fa948648de337d7"
CONSUMED_DIRECT_SOURCE_TREE = "53ba990318bc1a036b442d88ff8133d776a453dc"


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
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-003", "--source-commit=" + "0" * 40, "--output-dir=/tmp/x"),
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001", "--source-commit=short", "--output-dir=/tmp/x"),
            ("--change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001", "--source-commit=" + "0" * 40, "--output-dir=relative"),
        ):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)

    def test_consumed_change_id_cannot_create_persistent_bundle(self) -> None:
        for change_id, source_commit in (
            ("CHG-G8-TEST-READONLY-ACCESS-20260812-001", "c50f092339fcad79ca1262925480219db1755318"),
            ("CHG-G8-TEST-READONLY-ACCESS-20260812-002", "50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44"),
            (CONSUMED_CHANGE_ID, CONSUMED_SOURCE_COMMIT),
            (CONSUMED_DROP_CHANGE_ID, CONSUMED_DROP_SOURCE_COMMIT),
            (CONSUMED_DIRECT_CHANGE_ID, CONSUMED_DIRECT_SOURCE_COMMIT),
        ):
            with self.subTest(change_id=change_id), tempfile.TemporaryDirectory(prefix="g8-consumed-cli-") as temporary:
                output = Path(temporary) / "bundle"
                result = self.run_script(
                    f"--change-id={change_id}",
                    f"--source-commit={source_commit}",
                    f"--output-dir={output}",
                )
                self.assertEqual(result.returncode, 2)
                self.assertFalse(output.exists())
                self.assertEqual(
                    result.stdout.strip(),
                    "G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=invalid_request",
                )

    def test_009_and_010_are_consumed_without_active_candidate(self) -> None:
        """009 与 010 都只能临时复现，生成器不得保留活动候选。"""
        self.assertIn("ACTIVE_CANDIDATE = None", self.source)
        self.assertIn(f'"{CONSUMED_DIRECT_CHANGE_ID}": FrozenCandidate(', self.source)
        self.assertIn(f'        "{CONSUMED_DIRECT_SOURCE_COMMIT}",', self.source)
        self.assertIn(f'        "{CONSUMED_DIRECT_SOURCE_TREE}",', self.source)
        self.assertIn('        "DROP_SSH_DIRECT",', self.source)
        self.assertIn(f'"{CONSUMED_DROP_CHANGE_ID}": FrozenCandidate(', self.source)
        self.assertIn(f'        "{CONSUMED_DROP_SOURCE_COMMIT}",', self.source)
        self.assertIn(f'        "{CONSUMED_DROP_SOURCE_TREE}",', self.source)
        self.assertIn('        "DROP_SSH",', self.source)
        self.assertIn(f'"{CONSUMED_CHANGE_ID}": FrozenCandidate(', self.source)
        self.assertIn(f'        "{CONSUMED_SOURCE_COMMIT}",', self.source)
        self.assertIn(f'        "{CONSUMED_SOURCE_TREE}",', self.source)

    def test_010_manifest_uses_drop_direct_contract(self) -> None:
        """010 清单必须显式区分直连传输，且不得恢复物理主机身份门禁。"""
        self.assertIn('candidate.target_transport in {"DROP_SSH", "DROP_SSH_DIRECT"}', self.source)
        self.assertIn('values["PHYSICAL_HOST_IDENTITY"] = "NOT_APPLICABLE"', self.source)

    def test_ci_rejects_010_replay_and_verifies_it_ephemerally(self) -> None:
        """CI 必须拒绝 010 持久重放，仅在临时目录复现冻结回执。"""
        workflow = CI_PATH.read_text(encoding="utf-8")
        self.assertIn("--change-id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010", workflow)
        self.assertIn("--source-commit=75b1fc4ddb7138495547cec03fa948648de337d7", workflow)
        self.assertIn(
            "bundle_receipt_sha256=b3fac1a1530124da9dc604c32d11bd665de3daa5d6799aebb33c38a3d2f174f4",
            workflow,
        )
        self.assertIn("target_transport=DROP_SSH_DIRECT", workflow)
        self.assertIn("--consumed-change-id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010", workflow)
        self.assertIn("g8_010_replay_status=$?", workflow)
        self.assertNotIn("run-ai-gateway-g8-test-readonly-access-stage-drop-direct.py --change-id", workflow)
        self.assertIn('"CHG-G8-TEST-READONLY-ACCESS-20260812-001": FrozenCandidate(', self.source)
        self.assertIn('"CHG-G8-TEST-READONLY-ACCESS-20260812-002": FrozenCandidate(', self.source)
        self.assertIn("if candidate.target_deployment_root", self.source)

    def test_ci_verifies_009_only_as_consumed_candidate(self) -> None:
        """CI 必须拒绝009持久生成，并只在临时目录复现冻结回执。"""
        workflow = CI_PATH.read_text(encoding="utf-8")
        self.assertIn("g8_stage_drop_status=$?", workflow)
        self.assertIn('test "$g8_stage_drop_status" -eq 2', workflow)
        self.assertIn(
            "G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=change_id_consumed",
            workflow,
        )
        self.assertIn(
            "--consumed-change-id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009",
            workflow,
        )
        self.assertIn(
            "bundle_receipt_sha256=5bb49ad531410c8a719008bcd860d143eb9c51d23858a14737d678d5a60fc893",
            workflow,
        )
        self.assertNotIn("G8_TEST_READONLY_ACCESS_BUNDLE=PASS' /tmp/g8-test-readonly-access-009.log", workflow)

    def test_no_active_candidate_can_create_persistent_output(self) -> None:
        """即使输入 009 或 010 的精确历史来源，也不得创建持久候选目录。"""
        for change_id, source_commit in (
            (CONSUMED_DROP_CHANGE_ID, CONSUMED_DROP_SOURCE_COMMIT),
            (CONSUMED_DIRECT_CHANGE_ID, CONSUMED_DIRECT_SOURCE_COMMIT),
        ):
            with self.subTest(change_id=change_id), tempfile.TemporaryDirectory(prefix="g8-active-cli-") as temporary:
                output = Path(temporary) / "bundle"
                result = self.run_script(
                    f"--change-id={change_id}",
                    f"--source-commit={source_commit}",
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
        self.assertIn('marker = "G8_TEST_READONLY_ACCESS_BUNDLE_VERIFY=PASS"', self.source)
        self.assertIn("candidate = CONSUMED_CANDIDATES.get(arguments.consumed_change_id)", self.source)
        self.assertIn("values = prepare(candidate", self.source)
        for receipt in (
            "2fb6a964cf017997fa07d1df557cc41979b873597445f2543b497744b4fa70c9",
            "d6d07f7b4959e48f5ffe0e92ee4116cef55fe56f5318df6ae3f0d9c5350ee567",
            "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d",
            "14b7d8cd832f0b719031fcc93adbbb2208afe76d34383e63d51c44b044772b5a",
            "7ae580cc06fb101fe44c9e3a4d7581116fd258ef1e2d09d99bba0bda50151a1f",
            "7f4633357bf6883d166b0ee7d9750d7e745cf0a15d23163a547d6519e217efc1",
            "840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd",
            "5bb49ad531410c8a719008bcd860d143eb9c51d23858a14737d678d5a60fc893",
            "3ff8cf3ad7237f866f83305d00ab73f766381b7f3247abee915efee629e41fb0",
            "b3fac1a1530124da9dc604c32d11bd665de3daa5d6799aebb33c38a3d2f174f4",
        ):
            self.assertIn(receipt, self.source)
        self.assertIn('raise RuntimeError("consumed_receipt_mismatch")', self.source)

        for arguments in (
            ("--verify-consumed-candidate",),
            ("--verify-consumed-candidate", "--consumed-change-id=unknown"),
            (
                "--verify-consumed-candidate",
                "--consumed-change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001",
                "--output-dir=/tmp/forbidden",
            ),
        ):
            result = self.run_script(*arguments)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(
                result.stdout.strip(),
                "G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=invalid_request",
            )

    def test_source_comes_from_exact_git_archive_and_build_is_repeated(self) -> None:
        self.assertIn('[git, "archive", candidate.source_commit]', self.source)
        self.assertEqual(self.source.count('[go, "build", "-trimpath"'), 1)
        self.assertIn('"REPRODUCIBLE_BUILD_COUNT": "2"', self.source)
        self.assertIn("CONSUMED_CANDIDATES", self.source)

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
        self.assertIn('values["TARGET_MACHINE_ID_SHA256"]', self.source)
        self.assertIn('values["TARGET_SSH_ED25519_FINGERPRINT"]', self.source)

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
        self.assertIn("arguments.change_id != ACTIVE_CANDIDATE.change_id", self.source)
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

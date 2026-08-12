#!/usr/bin/env python3
"""验证 G8 测试服务器基线脚本始终保持只读、低敏和可解析。"""

import os
import re
import subprocess
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("audit-ai-gateway-g8-test-server-readonly.sh")
SUDOERS_PATH = SCRIPT_PATH.parent.parent / "sudoers" / "molin-g8-test-readonly-audit"


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestServerReadonlyAuditTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = SCRIPT_PATH.read_text(encoding="utf-8")
        cls.sudoers = SUDOERS_PATH.read_text(encoding="utf-8")

    def test_bash_syntax_and_self_test_pass(self) -> None:
        syntax = subprocess.run(
            [bash_executable(), "-n", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(syntax.returncode, 0, syntax.stderr)
        self_test = subprocess.run(
            [bash_executable(), str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(self_test.returncode, 0, self_test.stderr)
        self.assertEqual(self_test.stdout.strip(), "G8_TEST_READONLY_AUDIT_SELF_TEST=PASS")

    def test_script_has_no_mutating_command(self) -> None:
        forbidden = re.compile(
            r"\b(?:rm|mv|cp|install|tee|truncate|chmod|chown|kill|systemctl|service)\b"
            r"|\bdocker\s+(?:restart|stop|rm)\b"
            r"|\b(?:INSERT|UPDATE|DELETE|ALTER|DROP|TRUNCATE|CREATE)\b"
        )
        self.assertIsNone(forbidden.search(self.source))

    def test_environment_file_values_are_not_printed(self) -> None:
        self.assertIn("env_keys=", self.source)
        self.assertNotRegex(self.source, r"\b(?:cat|sed)\s+[^\n]*\$ENV_FILE")
        self.assertNotIn("printenv", self.source)
        self.assertNotRegex(self.source, r"\bsource\s+[\"']?\$ENV_FILE")
        self.assertNotRegex(self.source, r"printf[^\n]*\$\{(?:MYSQL_PASSWORD|REDIS_PASSWORD|MINIO_SECRET_KEY)")

    def test_backup_output_excludes_path_and_reconciliation_is_read_only(self) -> None:
        self.assertNotIn("backup_latest_path=", self.source)
        self.assertIn('"AI_GATEWAY_RECONCILE_READ_ONLY": "YES"', self.source)
        self.assertIn('[sys.argv[1], "--format", "summary", "--timeout", "30s"]', self.source)

    def test_docker_fallback_is_non_interactive(self) -> None:
        self.assertIn("sudo -n docker info", self.source)
        executable_lines = "\n".join(
            line for line in self.source.splitlines() if not line.lstrip().startswith("#")
        )
        self.assertNotRegex(executable_lines, r"sudo\s+(?!-n\b)")

    def test_git_inspection_disables_optional_locks(self) -> None:
        git_commands = [line for line in self.source.splitlines() if "git -C" in line]
        self.assertTrue(git_commands)
        for command in git_commands:
            self.assertIn("GIT_OPTIONAL_LOCKS=0", command)
            self.assertIn("GIT_CONFIG_NOSYSTEM=1", command)
            self.assertIn("GIT_CONFIG_GLOBAL=/dev/null", command)
        self.assertNotIn("status --porcelain", self.source)
        self.assertIn("git_dirty_count_read_only_policy", self.source)

    def test_credential_parser_is_allowlisted_and_fail_closed(self) -> None:
        self.assertIn('allowed_keys = {"MYSQL_PASSWORD", "MINIO_SECRET_KEY", "RABBITMQ_URL", "REDIS_PASSWORD"}', self.source)
        self.assertIn("if key in allowed_keys:", self.source)
        self.assertIn("if not parse_ok:", self.source)
        self.assertIn("urllib.parse.unquote", self.source)
        self.assertIn('if "REDIS_PASSWORD" not in values:', self.source)

    def test_privileged_entry_uses_fixed_root_owned_installation(self) -> None:
        self.assertIn('readonly PRIVILEGED_INSTALL_PATH="/usr/local/libexec/molin/g8-test-readonly-audit"', self.source)
        self.assertIn('readonly FIXED_ROOT="/home/pc/molin"', self.source)
        self.assertIn('readonly FIXED_ENV_FILE="/home/pc/molin/infra/.env.test"', self.source)
        self.assertIn('if ((EUID == 0)); then', self.source)
        self.assertIn('root:root:755', self.source)
        self.assertIn('privileged_installation=INVALID', self.source)
        self.assertIn('privileged_installation=VERIFIED', self.source)

    def test_runtime_accepts_only_a_valid_change_id_and_uses_trusted_path(self) -> None:
        self.assertIn('if (($# != 1)) || [[ "$1" != --change-id=* ]]; then', self.source)
        self.assertIn('CHG-G8-TEST-READONLY-', self.source)
        self.assertIn('invalid_arguments=true', self.source)
        self.assertIn('export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"', self.source)
        self.assertIn('unset BASH_ENV ENV CDPATH GLOBIGNORE', self.source)
        self.assertIn("PYTHONPATH PYTHONHOME", self.source)
        self.assertIn("cd / || exit 41", self.source)
        self.assertNotRegex(self.source, r"(?<!/usr/bin/)python3\s+-")
        self.assertEqual(self.source.count("/usr/bin/python3 -I -"), 4)

    def test_sudoers_allows_only_fixed_auditor(self) -> None:
        command = "/usr/local/libexec/molin/g8-test-readonly-audit"
        self.assertIn(f"Defaults!{command} env_reset", self.sudoers)
        self.assertIn(f"Defaults!{command} secure_path=", self.sudoers)
        self.assertIn(f"pc ALL=(root) NOPASSWD: {command}", self.sudoers)
        self.assertNotIn("SETENV", self.sudoers)
        self.assertNotRegex(self.sudoers, r"\b(?:docker|bash|sh)\b")
        self.assertNotIn("*", self.sudoers)

    def test_root_reconciliation_never_executes_user_owned_binary(self) -> None:
        self.assertIn('readonly PRIVILEGED_RECONCILE_PATH="/usr/local/libexec/molin/ai-gateway-reconcile"', self.source)
        self.assertIn('reconcile_meta="$(stat -Lc', self.source)
        self.assertIn('root:root:755', self.source)
        self.assertIn('child_env = {', self.source)
        self.assertIn('capture_output=True', self.source)
        self.assertIn('"MYSQL_HOST": "127.0.0.1"', self.source)
        self.assertIn('"MYSQL_PORT": "13306"', self.source)
        self.assertIn('values["MYSQL_USER"] != "molin"', self.source)
        self.assertIn('values["MYSQL_DATABASE"] != "molin"', self.source)
        self.assertNotIn('allowed_keys = {"MYSQL_HOST", "MYSQL_PORT"', self.source)

    def test_mysql_metadata_covers_text_gateway_core_tables(self) -> None:
        for table in (
            "token_models",
            "token_channels",
            "api_keys",
            "api_key_model_scopes",
            "ai_requests",
            "ai_usage_items",
            "ai_price_versions",
            "ai_model_routes",
            "ai_safety_policy_versions",
            "ai_compensation_tasks",
            "ai_billing_disputes",
            "wallet_holds",
            "wallet_transactions",
        ):
            self.assertIn(f'\\"{table}\\"', self.source)

    def test_audit_reports_docker_group_and_bifrost_env_scope_without_values(self) -> None:
        self.assertIn("pc_docker_group_member=true", self.source)
        self.assertIn("pc_docker_group_member=false", self.source)
        self.assertIn("bifrost_env_keys=", self.source)
        self.assertIn("bifrost_env_scope=", self.source)
        self.assertIn("image inspect", self.source)
        self.assertIn("comm -23", self.source)
        self.assertIn('container_env="$', self.source)
        self.assertIn('image_env="$', self.source)
        self.assertIn("awk -F= '{print $1}'", self.source)
        self.assertNotIn("container_keys=", self.source)
        self.assertNotIn("image_keys=", self.source)
        self.assertIn("BAILIAN_API_KEY,BIFROST_ENCRYPTION_KEY,OPENROUTER_API_KEY", self.source)
        self.assertIn("BIFROST_INTERNAL_TOKEN", self.source)
        self.assertNotIn("bifrost_env_values=", self.source)

    def test_bifrost_env_diff_treats_same_key_override_as_runtime_injection(self) -> None:
        image_env = {"PATH=/usr/bin", "BASE=image"}
        container_env = {"PATH=/tmp/unapproved", "BASE=image", "BIFROST_INTERNAL_TOKEN=secret"}
        difference = container_env - image_env
        keys = {entry.split("=", 1)[0] for entry in difference}
        self.assertEqual(keys, {"PATH", "BIFROST_INTERNAL_TOKEN"})


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""验证 011 root 安装器的固定输入、no-clobber 与回滚边界。"""

import base64
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("g8-test-readonly-access-install-011.sh")


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestG8ReadonlyAccessInstall011(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT_PATH.read_text(encoding="utf-8")

    def test_bash_syntax_and_fixed_identity(self) -> None:
        result = subprocess.run(
            [bash_executable(), "-n", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn("/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn("/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn('if [ "$#" -ne 0 ] || [ "$(/usr/bin/id -u)" -ne 0 ]; then', self.source)

    def test_candidate_contract_is_frozen(self) -> None:
        for value in (
            "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
            "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
            "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
            "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
            "13066129",
            "DROP_SSH_INTERACTIVE_SUDO",
        ):
            self.assertIn(value, self.source)
        self.assertIn("EXPECTED_FILES='SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers'", self.source)
        self.assertIn("-mindepth 1 -maxdepth 1 -printf '%f\\n'", self.source)
        self.assertIn("check_candidate \"$STAGING\" 0 'pc:pc'", self.source)
        self.assertIn("check_candidate \"$ROOT_COPY\" 1 'root:root'", self.source)
        self.assertIn("SHA256SUMS:600", self.source)
        self.assertIn("ai-gateway-reconcile:700", self.source)

    def test_root_only_visudo_and_live_gates_are_exact(self) -> None:
        self.assertIn('/usr/sbin/visudo -cf "$ROOT_COPY/molin-g8-test-readonly-audit.sudoers"', self.source)
        self.assertIn("/usr/sbin/visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit", self.source)
        self.assertIn("/usr/bin/sudo -n -l -U pc", self.source)
        self.assertIn("/usr/bin/id -nG pc", self.source)
        self.assertIn('grep -Eq "(^|[[:space:]])docker([[:space:]]|$)"', self.source)
        self.assertIn("validate_sudo_scope", self.source)
        self.assertIn("NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit", self.source)
        self.assertNotIn("/usr/bin/sudo -n -l -U pc >/dev/null", self.source)

    @unittest.skipIf(os.name == "nt", "真实 no-clobber 语义由 Linux 断网门禁执行。")
    def test_live_transaction_preserves_existing_target_and_rolls_back_partial_creation(self) -> None:
        """执行生产事务函数，证明预存保护和部分失败回滚。"""
        with tempfile.TemporaryDirectory(prefix="g8-011-install-") as temporary:
            root = Path(temporary)
            source = root / "source"
            target = root / "target"
            source.write_text("new", encoding="ascii")
            target.write_text("old", encoding="ascii")
            harness = root / "harness.sh"
            functions = self.source.split('main "$@"', 1)[0]
            harness.write_text(
                functions + "\nsource_file=$1\ntarget_file=$2\n"
                + 'install_live_file "$source_file" "$target_file" 0600 created_test\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(source), str(target)], capture_output=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(encoding="ascii"), "old")
            target.unlink()
            source.unlink()
            result = subprocess.run(
                ["/bin/bash", str(harness), str(source), str(target)], capture_output=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(target.exists())

    @unittest.skipIf(os.name == "nt", "sudoers 优先回滚语义由 Linux 断网门禁执行。")
    def test_rollback_orders_sudoers_before_tools_and_revalidates_visudo(self) -> None:
        """回滚必须先撤销本次 sudoers，再校验主配置，最后处理工具。"""
        rollback = self.source.split("rollback() {", 1)[1].split("}\ntrap rollback EXIT", 1)[0]
        sudoers = rollback.index("created_sudoers")
        visudo = rollback.index("visudo -cf /etc/sudoers")
        reconcile = rollback.index("created_reconcile")
        auditor = rollback.index("created_auditor")
        self.assertLess(sudoers, visudo)
        self.assertLess(visudo, reconcile)
        self.assertLess(reconcile, auditor)

    @unittest.skipIf(os.name == "nt", "sudo 精确范围解析由 Linux 断网门禁执行。")
    def test_sudo_scope_accepts_only_one_frozen_nopasswd_command(self) -> None:
        """真实执行生产解析函数，额外 NOPASSWD、SETENV、通配符或 Shell 必须被拒绝。"""
        function = self.source.split("validate_sudo_scope() {", 1)[1].split("}\n\nmain()", 1)[0]
        with tempfile.TemporaryDirectory(prefix="g8-011-sudo-scope-") as temporary:
            root = Path(temporary)
            fake_sudo = root / "sudo"
            harness = root / "harness.sh"
            harness.write_text(
                "#!/bin/bash\nset -euo pipefail\nvalidate_sudo_scope() {"
                + function.replace("/usr/bin/sudo", str(fake_sudo))
                + "}\nvalidate_sudo_scope\n",
                encoding="utf-8",
            )
            allowed = "User pc may run the following commands:\n    (root) NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit\n"
            rejected = (
                allowed
                + "    (root) NOPASSWD: /bin/bash\n"
            )
            for output, expected in ((allowed, 0), (rejected, 1), (allowed + "    SETENV: ALL\n", 1)):
                fake_sudo.write_text(
                    "#!/bin/sh\n/usr/bin/printf '%s' '"
                    + base64.b64encode(output.encode("ascii")).decode("ascii")
                    + "' | /usr/bin/base64 -d\n",
                    encoding="utf-8",
                )
                fake_sudo.chmod(0o700)
                result = subprocess.run(["/bin/bash", str(harness)], capture_output=True, check=False)
                self.assertEqual(result.returncode == 0, expected == 0, output)

    def test_live_creation_uses_same_descriptor_and_registered_rollback(self) -> None:
        self.assertGreaterEqual(self.source.count("set -o noclobber"), 2)
        self.assertGreaterEqual(self.source.count('exec 3> "$target"'), 2)
        self.assertEqual(self.source.count("install_live_file \"$ROOT_COPY/"), 3)
        self.assertIn("0755 created_auditor", self.source)
        self.assertIn("0755 created_reconcile", self.source)
        self.assertIn("0440 created_sudoers", self.source)
        self.assertIn("rollback()", self.source)
        self.assertIn('if [ "$created_sudoers" -eq 1 ]; then', self.source)

    def test_script_has_no_remote_or_business_access_capability(self) -> None:
        for forbidden in (
            "curl ", "wget ", "ssh ", "sftp ", "scp ", "mysql ", "redis-cli",
            "rabbitmq", "docker ", "systemctl ", "sudo -S", "SUDO_ASKPASS",
        ):
            self.assertNotIn(forbidden, self.source)


if __name__ == "__main__":
    unittest.main()

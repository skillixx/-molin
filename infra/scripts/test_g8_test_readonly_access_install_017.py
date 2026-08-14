#!/usr/bin/env python3
"""验证 017 安装器的冻结来源、no-clobber、最小 sudo 与回滚边界。"""

import base64
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("g8-test-readonly-access-install-017.sh")


def bash_executable() -> str:
    """Windows 使用 Git Bash；Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class TestG8ReadonlyAccessInstall017(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT_PATH.read_text(encoding="utf-8")

    def test_bash_syntax_and_two_change_ids_are_frozen(self) -> None:
        result = subprocess.run(
            [bash_executable(), "-n", str(SCRIPT_PATH)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("AUTH_CHANGE_ID='CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017'", self.source)
        self.assertIn("SOURCE_CHANGE_ID='CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011'", self.source)
        self.assertIn(".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", self.source)
        self.assertIn("molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017", self.source)
        self.assertIn('if [ "$#" -ne 0 ] || [ "$(/usr/bin/id -u)" -ne 0 ]; then', self.source)

    def test_candidate_and_live_contracts_are_exact(self) -> None:
        for value in (
            "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
            "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
            "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
            "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
            "13066129",
            "DROP_SSH_INTERACTIVE_SUDO",
            "PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE",
        ):
            self.assertIn(value, self.source)
        self.assertIn('check_candidate "$STAGING" 0 \'pc:pc\'', self.source)
        self.assertIn('check_candidate "$ROOT_COPY" 1 \'root:root\'', self.source)
        self.assertEqual(self.source.count('install_live_file "$ROOT_COPY/'), 3)
        self.assertIn('"$AUDITOR_TARGET" 0755 created_auditor', self.source)
        self.assertIn('"$RECONCILE_TARGET" 0755 created_reconcile', self.source)
        self.assertIn('"$SUDOERS_TARGET" 0440 created_sudoers', self.source)

    @unittest.skipIf(os.name == "nt", "冻结 manifest 的 CRLF 字节契约由 Linux 断网门禁执行。")
    def test_manifest_gate_accepts_only_frozen_crlf_lines(self) -> None:
        """真实执行 manifest 行门禁，证明 CRLF 受支持且未冻结的 LF 版本被拒绝。"""
        function = self.source.split("check_manifest_line() {", 1)[1].split("}\n\ncheck_candidate()", 1)[0]
        with tempfile.TemporaryDirectory(prefix="g8-017-manifest-") as temporary:
            root = Path(temporary)
            harness = root / "harness.sh"
            harness.write_text(
                "#!/bin/bash\nset -euo pipefail\ncheck_manifest_line() {"
                + function
                + '}\ncheck_manifest_line "CHANGE_ID=$1" "$2"\n'
                + "check_manifest_line 'TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO' \"$2\"\n"
                + "check_manifest_line 'PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE' \"$2\"\n",
                encoding="utf-8",
            )
            content = (
                "CHANGE_ID=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011\n"
                "TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO\n"
                "PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE\n"
            )
            manifest = root / "manifest.env"
            manifest.write_bytes(content.replace("\n", "\r\n").encode("ascii"))
            accepted = subprocess.run(
                ["/bin/bash", str(harness), "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", str(manifest)],
                capture_output=True,
                check=False,
            )
            self.assertEqual(accepted.returncode, 0, accepted.stderr)
            manifest.write_bytes(content.encode("ascii"))
            rejected = subprocess.run(
                ["/bin/bash", str(harness), "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011", str(manifest)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(rejected.returncode, 0)

    def test_sudo_scope_and_rollback_are_fail_closed(self) -> None:
        self.assertIn("/usr/bin/sudo -n -l -U pc", self.source)
        self.assertIn("NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit", self.source)
        self.assertIn("SETENV", self.source)
        self.assertIn("/bin/(ba)?sh|docker", self.source)
        self.assertIn("$VISUDO_BIN\" -cf /etc/sudoers", self.source)
        self.assertLess(self.source.index('rm -f -- "$SUDOERS_TARGET"'), self.source.index('rm -f -- "$RECONCILE_TARGET"'))
        self.assertLess(self.source.index('rm -f -- "$RECONCILE_TARGET"'), self.source.index('rm -f -- "$AUDITOR_TARGET"'))
        self.assertNotIn("usermod", self.source)
        self.assertNotIn("groupadd", self.source)
        self.assertNotIn("systemctl", self.source)
        self.assertNotIn("docker ", self.source)
        self.assertLess(
            self.source.index("created_parent=1", self.source.index("/usr/bin/mkdir -m 0755")),
            self.source.index('/usr/bin/chown root:root "$TOOLS_PARENT"'),
        )
        self.assertLess(self.source.index("validate_auditor_entry\n"), self.source.index("install_complete=1"))

    @unittest.skipIf(os.name == "nt", "异步终止回滚由 Linux 断网门禁执行。")
    def test_async_termination_after_target_creation_removes_live_file(self) -> None:
        """目标独占创建后立即终止时，EXIT trap 仍必须识别所有权并删除半成品。"""
        with tempfile.TemporaryDirectory(prefix="g8-017-signal-rollback-") as temporary:
            root = Path(temporary)
            parent = root / "molin"
            parent.mkdir()
            source = root / "source"
            source.write_text("auditor", encoding="ascii")
            target = parent / "auditor"
            functions = self.source.split("main() {", 1)[0]
            # 在独占创建完成、复制尚未开始的精确窗口注入 TERM，复现 SSH 断开场景。
            injected = functions.replace(
                "    set +o noclobber\n    if ! /usr/bin/cat",
                "    set +o noclobber\n    kill -TERM $$\n    if ! /usr/bin/cat",
                1,
            )
            self.assertNotEqual(injected, functions)
            harness = root / "harness.sh"
            harness.write_text(
                injected
                + '\nAUDITOR_TARGET="$1"\nTOOLS_PARENT="$2"\n'
                + 'install_live_file "$3" "$AUDITOR_TARGET" 0600 created_auditor\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(target), str(parent), str(source)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(target.exists())

    @unittest.skipIf(os.name == "nt", "真实 no-clobber 语义由 Linux 断网门禁执行。")
    def test_copy_no_clobber_preserves_existing_target(self) -> None:
        """执行生产复制函数，证明既有目标不会被覆盖。"""
        with tempfile.TemporaryDirectory(prefix="g8-017-copy-") as temporary:
            root = Path(temporary)
            source = root / "source"
            target = root / "target"
            source.write_text("new", encoding="ascii")
            target.write_text("old", encoding="ascii")
            functions = self.source.split("main() {", 1)[0]
            harness = root / "harness.sh"
            harness.write_text(
                functions + '\ncopy_no_clobber "$1" "$2" 0600\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(source), str(target)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(encoding="ascii"), "old")

    @unittest.skipIf(os.name == "nt", "部分安装回滚由 Linux 断网门禁执行。")
    def test_partial_live_install_is_removed_by_exit_trap(self) -> None:
        """第一个 live 文件成功、第二个失败时必须撤销第一个文件。"""
        with tempfile.TemporaryDirectory(prefix="g8-017-rollback-") as temporary:
            root = Path(temporary)
            parent = root / "molin"
            parent.mkdir()
            source = root / "source"
            source.write_text("auditor", encoding="ascii")
            auditor = parent / "auditor"
            reconcile = parent / "reconcile"
            missing = root / "missing"
            functions = self.source.split("main() {", 1)[0]
            harness = root / "harness.sh"
            harness.write_text(
                functions
                + '\nAUDITOR_TARGET="$1"\nRECONCILE_TARGET="$2"\nTOOLS_PARENT="$3"\n'
                + 'install_live_file "$4" "$AUDITOR_TARGET" 0600 created_auditor\n'
                + 'install_live_file "$5" "$RECONCILE_TARGET" 0600 created_reconcile\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(auditor), str(reconcile), str(parent), str(source), str(missing)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(auditor.exists())
            self.assertFalse(reconcile.exists())
            self.assertTrue(parent.is_dir())

    @unittest.skipIf(os.name == "nt", "跨目标回滚顺序由 Linux 断网门禁执行。")
    def test_global_rollback_removes_sudoers_first_and_revalidates(self) -> None:
        """动态证明失败回滚先撤 sudoers，再撤工具并清理本次空父目录。"""
        with tempfile.TemporaryDirectory(prefix="g8-017-global-rollback-") as temporary:
            root = Path(temporary)
            parent = root / "molin"
            parent.mkdir()
            auditor = parent / "auditor"
            reconcile = parent / "reconcile"
            sudoers = root / "sudoers"
            for target in (auditor, reconcile, sudoers):
                target.write_text("created", encoding="ascii")
            visudo_log = root / "visudo.log"
            fake_visudo = root / "visudo"
            fake_visudo.write_text(
                "#!/bin/sh\n/usr/bin/printf '%s\\n' visudo > \"$VISUDO_LOG\"\n",
                encoding="utf-8",
            )
            fake_visudo.chmod(0o700)
            functions = self.source.split("main() {", 1)[0]
            harness = root / "harness.sh"
            harness.write_text(
                functions
                + "\ninstall_complete=0\ncreated_auditor=1\ncreated_reconcile=1\ncreated_sudoers=1\ncreated_parent=1\n"
                + 'AUDITOR_TARGET="$1"\nRECONCILE_TARGET="$2"\nSUDOERS_TARGET="$3"\nTOOLS_PARENT="$4"\n'
                + 'VISUDO_BIN="$5"\nexport VISUDO_LOG="$6"\nfalse\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(auditor), str(reconcile), str(sudoers), str(parent),
                 str(fake_visudo), str(visudo_log)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(auditor.exists())
            self.assertFalse(reconcile.exists())
            self.assertFalse(sudoers.exists())
            self.assertFalse(parent.exists())
            self.assertEqual(visudo_log.read_text(encoding="ascii").strip(), "visudo")

    @unittest.skipIf(os.name == "nt", "审计器入口失败回滚由 Linux 断网门禁执行。")
    def test_auditor_entry_failure_rolls_back_all_live_files(self) -> None:
        """审计器 NOPASSWD 实际调用失败时，事务不得保留任何 live 文件。"""
        with tempfile.TemporaryDirectory(prefix="g8-017-entry-rollback-") as temporary:
            root = Path(temporary)
            parent = root / "molin"
            parent.mkdir()
            auditor = parent / "auditor"
            reconcile = parent / "reconcile"
            sudoers = root / "sudoers"
            for target in (auditor, reconcile, sudoers):
                target.write_text("created", encoding="ascii")
            fake_sudo = root / "sudo"
            fake_sudo.write_text("#!/bin/sh\nexit 19\n", encoding="ascii")
            fake_sudo.chmod(0o700)
            fake_visudo = root / "visudo"
            fake_visudo.write_text("#!/bin/sh\nexit 0\n", encoding="ascii")
            fake_visudo.chmod(0o700)
            functions = self.source.split("main() {", 1)[0].replace("/usr/bin/sudo", str(fake_sudo))
            harness = root / "harness.sh"
            harness.write_text(
                functions
                + "\ninstall_complete=0\ncreated_auditor=1\ncreated_reconcile=1\ncreated_sudoers=1\ncreated_parent=1\n"
                + 'AUDITOR_TARGET="$1"\nRECONCILE_TARGET="$2"\nSUDOERS_TARGET="$3"\nTOOLS_PARENT="$4"\n'
                + 'VISUDO_BIN="$5"\nvalidate_auditor_entry\ninstall_complete=1\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                ["/bin/bash", str(harness), str(auditor), str(reconcile), str(sudoers), str(parent), str(fake_visudo)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(auditor.exists())
            self.assertFalse(reconcile.exists())
            self.assertFalse(sudoers.exists())
            self.assertFalse(parent.exists())

    @unittest.skipIf(os.name == "nt", "sudo 精确范围解析由 Linux 断网门禁执行。")
    def test_sudo_scope_accepts_only_one_frozen_nopasswd_command(self) -> None:
        """真实执行解析函数，拒绝额外 NOPASSWD、SETENV、通配符、Shell 或 Docker。"""
        function = self.source.split("validate_sudo_scope() {", 1)[1].split("}\n\nmain()", 1)[0]
        with tempfile.TemporaryDirectory(prefix="g8-017-sudo-scope-") as temporary:
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
                allowed + "    (root) NOPASSWD: /bin/bash\n",
                allowed + "    SETENV: ALL\n",
                allowed.replace("g8-test-readonly-audit", "g8-test-readonly-audit *"),
                allowed.replace("g8-test-readonly-audit", "docker"),
            )
            for output, expected_ok in ((allowed, True), *((value, False) for value in rejected)):
                encoded = base64.b64encode(output.encode("ascii")).decode("ascii")
                fake_sudo.write_text(
                    "#!/bin/sh\n/usr/bin/printf '%s' '" + encoded + "' | /usr/bin/base64 -d\n",
                    encoding="utf-8",
                )
                fake_sudo.chmod(0o700)
                result = subprocess.run(["/bin/bash", str(harness)], capture_output=True, check=False)
                self.assertEqual(result.returncode == 0, expected_ok, output)


if __name__ == "__main__":
    unittest.main()

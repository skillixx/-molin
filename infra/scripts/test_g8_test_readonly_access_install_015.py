#!/usr/bin/env python3
"""验证已消费的 015 安装器入口固定拒绝且不执行历史安装逻辑。"""

import os
from pathlib import Path
import subprocess
import unittest


SCRIPT_PATH = Path(__file__).with_name("g8-test-readonly-access-install-015.sh")


def bash_executable() -> str:
    """Windows 使用 Git Bash，Linux CI 使用系统 Bash。"""
    git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    return str(git_bash) if os.name == "nt" and git_bash.exists() else "bash"


class ConsumedReadonlyAccess015InstallerTests(unittest.TestCase):
    def test_installer_always_returns_consumed(self) -> None:
        """安装器不得因环境或参数变化重新进入 root/live 文件逻辑。"""
        result = subprocess.run(
            [bash_executable(), str(SCRIPT_PATH), "historical"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(result.returncode, 2)
        self.assertEqual(
            result.stdout,
            "G8_TEST_READONLY_ACCESS_INSTALL_015=FAILED reason=change_id_consumed\n",
        )
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()

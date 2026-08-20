#!/usr/bin/env python3
"""验证已消费的 016 生成入口不会解析历史参数或触达外部进程。"""

import contextlib
import importlib.util
import io
from pathlib import Path
import sys
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-access-016-command.py")


class ConsumedReadonlyAccess016CommandTests(unittest.TestCase):
    def test_every_invocation_fails_before_parser_materials_and_network(self) -> None:
        """历史参数不得让已消费入口重新读取材料、生成命令或启动进程。"""
        spec = importlib.util.spec_from_file_location("g8_readonly_access_016_consumed", SCRIPT_PATH)
        module = importlib.util.module_from_spec(spec)
        assert spec and spec.loader
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        stdout = io.StringIO()
        with mock.patch("subprocess.Popen") as popen:
            with mock.patch.object(sys, "argv", [str(SCRIPT_PATH), "--change-id=historical", "--output-file=secret"]):
                with contextlib.redirect_stdout(stdout):
                    code = module.main()
        self.assertEqual(code, 2)
        self.assertEqual(
            stdout.getvalue(),
            "G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=change_id_consumed\n",
        )
        popen.assert_not_called()


if __name__ == "__main__":
    unittest.main()

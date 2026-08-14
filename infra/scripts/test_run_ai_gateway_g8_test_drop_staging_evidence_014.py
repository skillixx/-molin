import contextlib
import importlib.util
import io
from pathlib import Path
import sys
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-drop-staging-evidence-014.py")


class ConsumedStagingEvidence014Tests(unittest.TestCase):
    def test_every_invocation_fails_before_parser_materials_and_network(self):
        """已消费入口不得因历史参数重新触达材料检查或网络进程。"""
        spec = importlib.util.spec_from_file_location("g8_staging_evidence_014_consumed", SCRIPT_PATH)
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        stdout = io.StringIO()
        with mock.patch("subprocess.Popen") as popen:
            with mock.patch.object(sys, "argv", [str(SCRIPT_PATH), "--change-id", "historical"]):
                with contextlib.redirect_stdout(stdout):
                    code = module.main()
        self.assertEqual(code, 2)
        self.assertEqual(
            stdout.getvalue(),
            "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed\n",
        )
        popen.assert_not_called()


if __name__ == "__main__":
    unittest.main()

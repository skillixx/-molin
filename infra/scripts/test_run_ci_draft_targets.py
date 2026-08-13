import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ci-draft-targets.py")


def load_runner():
    """从固定路径加载执行器，验证其只使用参数数组启动本地测试进程。"""

    spec = importlib.util.spec_from_file_location("run_ci_draft_targets", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载 Draft 定向目标执行器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class RunCIDraftTargetsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.runner = load_runner()

    def test_parse_targets_rejects_empty_invalid_and_unsafe_values(self):
        for value in ("", "{}", "[]", '["../secret.py"]', '[1]'):
            with self.subTest(value=value):
                with self.assertRaises(self.runner.DraftTargetError):
                    self.runner.parse_targets_json(value)

    def test_python_tests_use_argument_lists_without_shell(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            target = root / "infra/scripts/test_alpha.py"
            target.parent.mkdir(parents=True)
            target.write_text("", encoding="utf-8")
            with mock.patch.object(self.runner.subprocess, "run") as run:
                self.runner.run_python_tests(
                    ["infra/scripts/test_alpha.py"], root
                )

            run.assert_called_once()
            args, kwargs = run.call_args
            self.assertEqual("-I", args[0][1])
            self.assertIn("infra/scripts/test_alpha.py", args[0])
            self.assertTrue(kwargs["check"])
            self.assertNotIn("shell", kwargs)

    def test_python_compile_runs_all_selected_files_in_one_command(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            targets = ["infra/scripts/a.py", "infra/scripts/b.py"]
            for relative_path in targets:
                target = root / relative_path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text("", encoding="utf-8")
            with mock.patch.object(self.runner.subprocess, "run") as run:
                self.runner.run_python_compile(targets, root)

            command = run.call_args.args[0]
            self.assertEqual("py_compile", command[2])
            self.assertEqual(targets, command[3:])

    def test_go_runs_only_selected_test_and_vet_packages(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            (root / "server/internal/modules/content").mkdir(parents=True)
            with mock.patch.object(self.runner.subprocess, "run") as run:
                self.runner.run_go(
                    ["./internal/modules/content"], root
                )

            self.assertEqual(2, run.call_count)
            self.assertEqual(
                ["go", "test", "-count=1", "./internal/modules/content"],
                run.call_args_list[0].args[0],
            )
            self.assertEqual(
                ["go", "vet", "./internal/modules/content"],
                run.call_args_list[1].args[0],
            )
            self.assertEqual(root / "server", run.call_args_list[0].kwargs["cwd"])
            self.assertNotIn("shell", run.call_args_list[0].kwargs)

    def test_subprocess_failure_is_not_swallowed(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            target = root / "infra/scripts/test_alpha.py"
            target.parent.mkdir(parents=True)
            target.write_text("", encoding="utf-8")
            with mock.patch.object(
                self.runner.subprocess,
                "run",
                side_effect=self.runner.subprocess.CalledProcessError(1, ["python"]),
            ):
                with self.assertRaises(self.runner.subprocess.CalledProcessError):
                    self.runner.run_python_tests(
                        ["infra/scripts/test_alpha.py"], root
                    )


if __name__ == "__main__":
    unittest.main()

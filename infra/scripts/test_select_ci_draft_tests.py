import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("select-ci-draft-tests.py")


def load_selector():
    """从固定路径加载选择器，直接验证其公开的路径到目标契约。"""

    spec = importlib.util.spec_from_file_location("select_ci_draft_tests", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载 Draft 定向目标选择器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SelectCIDraftTestsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.selector = load_selector()

    def make_file(self, root: Path, relative_path: str, content: str = "") -> None:
        target = root / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")

    def test_python_production_script_selects_matching_test_and_compile(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            self.make_file(root, "infra/scripts/run-check.py")
            self.make_file(root, "infra/scripts/test_run_check.py")

            tests, compile_targets = self.selector.select_python_targets(
                ["infra/scripts/run-check.py"], root
            )

            self.assertEqual(["infra/scripts/test_run_check.py"], tests)
            self.assertEqual(["infra/scripts/run-check.py"], compile_targets)

    def test_missing_matching_test_falls_back_to_all_infra_tests(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            self.make_file(root, "infra/scripts/new-check.py")
            self.make_file(root, "infra/scripts/test_alpha.py")
            self.make_file(root, "infra/scripts/test_beta.py")

            tests, _ = self.selector.select_python_targets(
                ["infra/scripts/new-check.py"], root
            )

            self.assertEqual(
                ["infra/scripts/test_alpha.py", "infra/scripts/test_beta.py"],
                tests,
            )

    def test_test_file_selects_itself(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            self.make_file(root, "infra/scripts/test_exact.py")

            tests, compile_targets = self.selector.select_python_targets(
                ["infra/scripts/test_exact.py"], root
            )

            self.assertEqual(["infra/scripts/test_exact.py"], tests)
            self.assertEqual(["infra/scripts/test_exact.py"], compile_targets)

    def test_workflow_change_selects_all_ci_contract_tests(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            expected = [
                "infra/scripts/test_ci_draft_ready_workflow_contract.py",
                "infra/scripts/test_classify_ci_change_scope.py",
                "infra/scripts/test_run_ci_draft_targets.py",
                "infra/scripts/test_select_ci_draft_tests.py",
            ]
            for relative_path in expected:
                self.make_file(root, relative_path)

            tests, _ = self.selector.select_python_targets(
                [".github/workflows/ci.yml"], root
            )

            self.assertEqual(expected, tests)

    def test_tests_directory_change_never_returns_empty_target_set(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            self.make_file(root, "tests/helper.py")
            self.make_file(root, "tests/test_contract.py")

            tests, _ = self.selector.select_python_targets(["tests/helper.py"], root)

            self.assertEqual(["tests/test_contract.py"], tests)

    def test_go_regular_file_maps_to_parent_package(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            (root / "server/internal/modules/content/service").mkdir(parents=True)

            packages = self.selector.select_go_packages(
                ["server/internal/modules/content/service/article.go"], root
            )

            self.assertEqual(["./internal/modules/content/service"], packages)

    def test_go_shared_and_runtime_paths_fail_closed_to_all_packages(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            for path in (
                "server/go.mod",
                "server/go.sum",
                "server/pkg/db/db.go",
                "server/internal/bootstrap/app.go",
                "server/internal/config/config.go",
                "server/migrations/000001.up.sql",
            ):
                with self.subTest(path=path):
                    self.assertEqual(
                        ["./..."],
                        self.selector.select_go_packages([path], root),
                    )

    def test_go_list_validates_selected_package(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            (root / "server").mkdir()
            completed = self.selector.subprocess.CompletedProcess(
                ["go", "list"], 0, "example/content\n", ""
            )
            with mock.patch.object(
                self.selector.subprocess, "run", return_value=completed
            ) as run:
                packages = self.selector.validate_go_packages_with_go_list(
                    ["./internal/modules/content"], root
                )

            self.assertEqual(["./internal/modules/content"], packages)
            self.assertEqual(
                ["go", "list", "./internal/modules/content"],
                run.call_args.args[0],
            )
            self.assertEqual(root / "server", run.call_args.kwargs["cwd"])

    def test_go_list_failure_falls_back_to_all_packages(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            (root / "server").mkdir()
            completed = self.selector.subprocess.CompletedProcess(
                ["go", "list"], 1, "", "not-found"
            )
            with mock.patch.object(
                self.selector.subprocess, "run", return_value=completed
            ):
                self.assertEqual(
                    ["./..."],
                    self.selector.validate_go_packages_with_go_list(
                        ["./internal/modules/deleted"], root
                    ),
                )

    def test_targets_are_sorted_and_deduplicated(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            self.make_file(root, "infra/scripts/test_zeta.py")
            tests, compile_targets = self.selector.select_python_targets(
                [
                    "infra/scripts/test_zeta.py",
                    "infra/scripts/test_zeta.py",
                ],
                root,
            )
            self.assertEqual(["infra/scripts/test_zeta.py"], tests)
            self.assertEqual(["infra/scripts/test_zeta.py"], compile_targets)

    def test_unsafe_repository_paths_are_rejected(self):
        for value in (
            "",
            "../secret.py",
            "/absolute.py",
            "C:/absolute.py",
            "infra\\scripts\\test.py",
            "infra/scripts/test.py;echo-bad",
            "infra/scripts/test.py\nnext",
        ):
            with self.subTest(value=value):
                with self.assertRaises(self.selector.SelectionError):
                    self.selector.validate_repository_path(value)

    def test_json_outputs_are_single_line_string_arrays(self):
        value = self.selector.encode_targets_json(
            ["infra/scripts/test_alpha.py", "infra/scripts/test_beta.py"]
        )
        self.assertNotIn("\n", value)
        self.assertEqual(
            ["infra/scripts/test_alpha.py", "infra/scripts/test_beta.py"],
            json.loads(value),
        )


if __name__ == "__main__":
    unittest.main()

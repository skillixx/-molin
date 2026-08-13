#!/usr/bin/env python3
"""根据精确 Git 差异选择 Draft PR 的本地测试与 Go package。"""

from __future__ import annotations

import argparse
import importlib.util
import json
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath
from typing import Iterable


CLASSIFIER_PATH = Path(__file__).with_name("classify-ci-change-scope.py")
UNSAFE_PATH_PATTERN = re.compile(r"[\x00-\x1f\x7f:;&|`$<>]")
CI_CONTRACT_TESTS = (
    "infra/scripts/test_ci_draft_ready_workflow_contract.py",
    "infra/scripts/test_classify_ci_change_scope.py",
    "infra/scripts/test_run_ci_draft_targets.py",
    "infra/scripts/test_select_ci_draft_tests.py",
)
CI_COMPILE_TARGETS = (
    "infra/scripts/classify-ci-change-scope.py",
    "infra/scripts/run-ci-draft-targets.py",
    "infra/scripts/select-ci-draft-tests.py",
)
GO_FULL_PREFIXES = (
    "server/pkg/",
    "server/internal/bootstrap/",
    "server/internal/config/",
    "server/migrations/",
    "server/cmd/api/",
)


class SelectionError(ValueError):
    """表示变更路径无法安全映射到非空定向目标，调用方必须停止。"""


def validate_repository_path(value: str) -> str:
    """只接受无 shell 片段的 Git 风格仓库相对路径。"""

    if not value or value != value.strip():
        raise SelectionError("仓库路径不能为空或包含首尾空白")
    if "\\" in value or value.startswith(("/", "-")):
        raise SelectionError("仓库路径必须使用正斜杠相对格式")
    if UNSAFE_PATH_PATTERN.search(value):
        raise SelectionError("仓库路径包含控制字符或 shell 元字符")
    parts = PurePosixPath(value).parts
    if not parts or any(part in {"", ".", ".."} for part in parts):
        raise SelectionError("仓库路径包含不安全片段")
    return value


def normalize_paths(paths: Iterable[str]) -> list[str]:
    """验证、去重并排序变更路径，确保目标选择结果可复现。"""

    normalized = sorted({validate_repository_path(value) for value in paths})
    if not normalized:
        raise SelectionError("变更路径集合不能为空")
    return normalized


def existing_files(repo_root: Path, paths: Iterable[str]) -> list[str]:
    """返回当前精确 HEAD 中真实存在的普通文件，不跟随已删除目标。"""

    result: list[str] = []
    for relative_path in paths:
        validate_repository_path(relative_path)
        target = repo_root / relative_path
        if target.is_file():
            result.append(relative_path)
    return sorted(set(result))


def find_tests(repo_root: Path, directory: str) -> list[str]:
    """递归列出固定目录内真实存在的 Python 测试。"""

    validate_repository_path(directory)
    root = repo_root / directory
    tests = [
        path.relative_to(repo_root).as_posix()
        for path in root.rglob("test_*.py")
        if path.is_file()
    ] if root.is_dir() else []
    return sorted(set(tests))


def discover_tests(repo_root: Path, directory: str) -> list[str]:
    """查找指定范围测试，空集合视为无法安全选择。"""

    tests = find_tests(repo_root, directory)
    if not tests:
        raise SelectionError(f"{directory} 没有可执行的 Python 测试")
    return tests


def discover_all_tests(repo_root: Path) -> list[str]:
    """汇总全部固定 Python 测试目录，仅在总集合为空时失败关闭。"""

    tests: set[str] = set()
    for directory in ("infra/scripts", "scripts", "tests"):
        tests.update(find_tests(repo_root, directory))
    if not tests:
        raise SelectionError("仓库没有可执行的 Python 测试")
    return sorted(tests)


def select_python_targets(
    paths: Iterable[str], repo_root: Path
) -> tuple[list[str], list[str]]:
    """选择非空 Python 测试集和需要语法编译的变更文件。"""

    normalized = normalize_paths(paths)
    classifier = load_classifier()
    tests: set[str] = set()
    compile_targets: set[str] = set()
    needs_infra_fallback = False
    needs_scripts_fallback = False
    needs_tests_fallback = False
    needs_all_python_fallback = False

    for relative_path in normalized:
        path = PurePosixPath(relative_path)
        if relative_path.startswith(".github/"):
            tests.update(existing_files(repo_root, CI_CONTRACT_TESTS))
            compile_targets.update(existing_files(repo_root, CI_COMPILE_TARGETS))
            continue
        if relative_path.startswith("infra/scripts/"):
            if path.suffix == ".py":
                if (repo_root / relative_path).is_file():
                    compile_targets.add(relative_path)
                if path.name.startswith("test_"):
                    if (repo_root / relative_path).is_file():
                        tests.add(relative_path)
                    else:
                        needs_infra_fallback = True
                    continue
                expected_test = (
                    path.parent
                    / f"test_{path.stem.replace('-', '_')}.py"
                ).as_posix()
                if (repo_root / expected_test).is_file():
                    tests.add(expected_test)
                else:
                    needs_infra_fallback = True
            else:
                # 非 Python 运维资产仍可能改变 Python 包装器契约，回退到完整 infra 测试。
                needs_infra_fallback = True
            continue
        if relative_path.startswith("scripts/"):
            if path.suffix == ".py" and (repo_root / relative_path).is_file():
                compile_targets.add(relative_path)
            if path.name.startswith("test_") and path.suffix == ".py" and (
                repo_root / relative_path
            ).is_file():
                tests.add(relative_path)
            else:
                needs_scripts_fallback = True
            continue
        if relative_path.startswith("tests/"):
            if path.suffix == ".py" and (repo_root / relative_path).is_file():
                compile_targets.add(relative_path)
            if path.name.startswith("test_") and path.suffix == ".py" and (
                repo_root / relative_path
            ).is_file():
                tests.add(relative_path)
            else:
                needs_tests_fallback = True
            continue
        individual_scope = classifier.classify_draft_paths([relative_path])
        if individual_scope["draft_python"]:
            # 未知根路径由分类器选中全部 Draft 门禁，Python 侧必须执行全部测试。
            needs_all_python_fallback = True

    if needs_all_python_fallback:
        fallback_tests = discover_all_tests(repo_root)
        tests.update(fallback_tests)
        compile_targets.update(fallback_tests)
    else:
        if needs_infra_fallback:
            fallback_tests = discover_tests(repo_root, "infra/scripts")
            tests.update(fallback_tests)
            compile_targets.update(fallback_tests)
        if needs_scripts_fallback:
            fallback_tests = discover_tests(repo_root, "scripts")
            tests.update(fallback_tests)
            compile_targets.update(fallback_tests)
        if needs_tests_fallback:
            fallback_tests = discover_tests(repo_root, "tests")
            tests.update(fallback_tests)
            compile_targets.update(fallback_tests)
    if not tests:
        raise SelectionError("Draft Python 变更没有映射到任何测试")
    return sorted(tests), sorted(compile_targets)


def select_go_packages(paths: Iterable[str], repo_root: Path) -> list[str]:
    """把 server 变更映射到最小 Go package；共享运行时变更回退到全量。"""

    normalized = normalize_paths(paths)
    classifier = load_classifier()
    packages: set[str] = set()
    for relative_path in normalized:
        if not relative_path.startswith("server/"):
            if classifier.classify_draft_paths([relative_path])["draft_backend"]:
                # 未知根路径由分类器选中后端门禁，Go 侧必须回退到全量 package。
                return ["./..."]
            continue
        if relative_path in {"server/go.mod", "server/go.sum"} or relative_path.startswith(
            GO_FULL_PREFIXES
        ):
            return ["./..."]
        parent = PurePosixPath(relative_path).parent
        if parent == PurePosixPath("server"):
            return ["./..."]
        if not (repo_root / parent.as_posix()).is_dir():
            return ["./..."]
        packages.add("./" + parent.relative_to("server").as_posix())
    if not packages:
        raise SelectionError("Draft backend 变更没有映射到任何 Go package")
    return sorted(packages)


def validate_go_packages_with_go_list(
    packages: Iterable[str], repo_root: Path
) -> list[str]:
    """用 go list 验证 package 映射；无法确认时回退到全量 package。"""

    normalized = sorted(set(packages))
    if not normalized:
        raise SelectionError("Go package 集合不能为空")
    if normalized == ["./..."]:
        return normalized
    server_root = repo_root / "server"
    if not server_root.is_dir():
        raise SelectionError("server 目录不存在")
    for package in normalized:
        completed = subprocess.run(
            ["go", "list", package],
            cwd=server_root,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        if completed.returncode != 0 or completed.stderr or not completed.stdout.strip():
            return ["./..."]
    return normalized


def encode_targets_json(values: Iterable[str]) -> str:
    """把已验证目标编码为单行 JSON，避免 GitHub output 注入。"""

    normalized = [validate_repository_path(value) for value in values]
    return json.dumps(sorted(set(normalized)), ensure_ascii=True, separators=(",", ":"))


def load_classifier():
    """加载现有差异读取器，复用其 SHA 和 NUL 分隔解析门禁。"""

    spec = importlib.util.spec_from_file_location("classify_ci_change_scope", CLASSIFIER_PATH)
    if spec is None or spec.loader is None:
        raise SelectionError("无法加载 CI 变更范围分类器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write_outputs(output_path: Path, paths: list[str], repo_root: Path) -> None:
    """根据已分类范围输出 Draft 执行器消费的低敏 JSON 目标。"""

    normalized = normalize_paths(paths)
    classifier = load_classifier()
    draft_scope = classifier.classify_draft_paths(normalized)
    python_tests: list[str] = []
    python_compile: list[str] = []
    go_packages: list[str] = []
    if draft_scope["draft_python"]:
        python_tests, python_compile = select_python_targets(normalized, repo_root)
    if draft_scope["draft_backend"]:
        go_packages = validate_go_packages_with_go_list(
            select_go_packages(normalized, repo_root),
            repo_root,
        )
    with output_path.open("a", encoding="utf-8", newline="\n") as output_file:
        output_file.write(f"python_tests_json={encode_targets_json(python_tests)}\n")
        output_file.write(f"python_compile_json={encode_targets_json(python_compile)}\n")
        output_file.write(
            "go_packages_json="
            + json.dumps(go_packages, ensure_ascii=True, separators=(",", ":"))
            + "\n"
        )


def main() -> int:
    parser = argparse.ArgumentParser(description="选择 Draft PR 定向测试目标")
    parser.add_argument("--base-sha", required=True)
    parser.add_argument("--head-sha", required=True)
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    parser.add_argument("--github-output", type=Path, required=True)
    args = parser.parse_args()
    try:
        repo_root = args.repo_root.resolve()
        classifier = load_classifier()
        paths = classifier.changed_paths(repo_root, args.base_sha, args.head_sha)
        write_outputs(args.github_output, paths, repo_root)
    except (SelectionError, OSError, ValueError) as error:
        print(f"CI_DRAFT_TARGET_SELECTION=FAILED reason={type(error).__name__}", file=sys.stderr)
        return 2
    print("CI_DRAFT_TARGET_SELECTION=PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

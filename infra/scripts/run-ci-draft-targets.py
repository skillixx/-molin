#!/usr/bin/env python3
"""以参数数组执行选择后的 Draft 本地测试，禁止 shell 拼接和远端能力。"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath
from typing import Iterable


UNSAFE_PATH_PATTERN = re.compile(r"[\x00-\x1f\x7f:;&|`$<>]")
GO_PACKAGE_PATTERN = re.compile(r"\./(?:\.\.\.|[A-Za-z0-9_./-]+)")


class DraftTargetError(ValueError):
    """表示执行目标为空、越界或不是允许的本地仓库目标。"""


def validate_repository_path(value: str) -> str:
    """再次验证仓库相对路径，防止工作流输出被篡改后进入子进程。"""

    if not value or value != value.strip():
        raise DraftTargetError("目标路径不能为空或包含首尾空白")
    if "\\" in value or value.startswith(("/", "-")):
        raise DraftTargetError("目标路径必须是仓库相对路径")
    if UNSAFE_PATH_PATTERN.search(value):
        raise DraftTargetError("目标路径包含控制字符或 shell 元字符")
    parts = PurePosixPath(value).parts
    if not parts or any(part in {"", ".", ".."} for part in parts):
        raise DraftTargetError("目标路径包含不安全片段")
    return value


def parse_targets_json(value: str) -> list[str]:
    """解析非空字符串数组，并对每个仓库路径执行二次校验。"""

    try:
        decoded = json.loads(value)
    except (json.JSONDecodeError, TypeError) as error:
        raise DraftTargetError("目标 JSON 无法解析") from error
    if not isinstance(decoded, list) or not decoded:
        raise DraftTargetError("目标 JSON 必须是非空数组")
    if any(not isinstance(item, str) for item in decoded):
        raise DraftTargetError("目标 JSON 只能包含字符串")
    return sorted({validate_repository_path(item) for item in decoded})


def ensure_files_exist(targets: Iterable[str], repo_root: Path) -> list[str]:
    """只允许运行当前精确 HEAD 中存在的普通文件。"""

    normalized: list[str] = []
    for target in targets:
        relative_path = validate_repository_path(target)
        if not (repo_root / relative_path).is_file():
            raise DraftTargetError(f"目标文件不存在：{relative_path}")
        normalized.append(relative_path)
    if not normalized:
        raise DraftTargetError("执行目标不能为空")
    return sorted(set(normalized))


def run_python_tests(targets: Iterable[str], repo_root: Path) -> None:
    """逐项运行定向 Python 测试，单项失败立即停止。"""

    for target in ensure_files_exist(targets, repo_root):
        subprocess.run(
            [
                sys.executable,
                "-I",
                "-W",
                "error::ResourceWarning",
                target,
                "-v",
            ],
            cwd=repo_root,
            check=True,
        )


def run_python_compile(targets: Iterable[str], repo_root: Path) -> None:
    """一次编译全部选中 Python 文件，验证最小语法契约。"""

    normalized = ensure_files_exist(targets, repo_root)
    subprocess.run(
        [sys.executable, "-m", "py_compile", *normalized],
        cwd=repo_root,
        check=True,
    )


def validate_go_packages(packages: Iterable[str]) -> list[str]:
    """只接受 Go 相对 package，不允许 flags、绝对路径或 shell 片段。"""

    normalized = sorted(set(packages))
    if not normalized or any(
        not isinstance(package, str) or not GO_PACKAGE_PATTERN.fullmatch(package)
        for package in normalized
    ):
        raise DraftTargetError("Go package 集合无效")
    return normalized


def run_go(packages: Iterable[str], repo_root: Path) -> None:
    """只对选中 package 运行 test 与 vet，不启动 race、容器或服务。"""

    normalized = validate_go_packages(packages)
    server_root = repo_root / "server"
    if not server_root.is_dir():
        raise DraftTargetError("server 目录不存在")
    subprocess.run(
        ["go", "test", "-count=1", *normalized],
        cwd=server_root,
        check=True,
    )
    subprocess.run(
        ["go", "vet", *normalized],
        cwd=server_root,
        check=True,
    )


def parse_go_packages_json(value: str) -> list[str]:
    """解析 Go package JSON，并应用专用白名单格式。"""

    try:
        decoded = json.loads(value)
    except (json.JSONDecodeError, TypeError) as error:
        raise DraftTargetError("Go package JSON 无法解析") from error
    if not isinstance(decoded, list):
        raise DraftTargetError("Go package JSON 必须是数组")
    return validate_go_packages(decoded)


def main() -> int:
    parser = argparse.ArgumentParser(description="执行 Draft PR 定向本地测试")
    parser.add_argument(
        "--kind",
        choices=("python-tests", "python-compile", "go"),
        required=True,
    )
    parser.add_argument("--targets-json", required=True)
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    try:
        repo_root = args.repo_root.resolve()
        if args.kind == "go":
            run_go(parse_go_packages_json(args.targets_json), repo_root)
        elif args.kind == "python-tests":
            run_python_tests(parse_targets_json(args.targets_json), repo_root)
        else:
            run_python_compile(parse_targets_json(args.targets_json), repo_root)
    except (DraftTargetError, OSError, subprocess.CalledProcessError) as error:
        print(f"CI_DRAFT_TARGET_RUN=FAILED reason={type(error).__name__}", file=sys.stderr)
        return 2
    print("CI_DRAFT_TARGET_RUN=PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

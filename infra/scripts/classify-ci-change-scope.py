#!/usr/bin/env python3
"""根据 PR 变更路径选择 CI 门禁，无法识别时统一失败关闭为完整 CI。"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath
from typing import Iterable


OUTPUT_NAMES = (
    "docs_lightweight",
    "backend",
    "phase5",
    "gateway_g3",
    "gateway_g4",
    "gateway_g7",
    "gateway_g8",
    "gateway_g8_real_e2e",
    "frontend_admin",
    "frontend_user",
    "full",
)

PR_MODES = ("draft", "ready")

DRAFT_OUTPUT_NAMES = (
    "draft_docs",
    "draft_python",
    "draft_backend",
    "draft_frontend_admin",
    "draft_frontend_user",
)

GATEWAY_OUTPUTS = (
    "gateway_g3",
    "gateway_g4",
    "gateway_g7",
    "gateway_g8",
    "gateway_g8_real_e2e",
)

SHA_PATTERN = re.compile(r"[0-9a-fA-F]{40,64}")


class ClassificationError(ValueError):
    """表示输入无法被安全分类，调用方必须停止而不是猜测。"""


def empty_result() -> dict[str, bool]:
    """创建所有门禁默认关闭的结果，后续只按明确规则开启。"""

    return {name: False for name in OUTPUT_NAMES}


def empty_draft_result() -> dict[str, bool]:
    """创建 Draft 定向门禁的默认关闭结果，避免新增路径被隐式放行。"""

    return {name: False for name in DRAFT_OUTPUT_NAMES}


def enable_all_draft(result: dict[str, bool]) -> dict[str, bool]:
    """未知或跨域路径启用全部 Draft 定向门禁，以失败关闭代替猜测。"""

    for name in DRAFT_OUTPUT_NAMES:
        result[name] = True
    return result


def enable_full(result: dict[str, bool]) -> dict[str, bool]:
    """未知或高风险变更开启全部门禁，确保分类器失败时不漏测。"""

    for name in OUTPUT_NAMES:
        result[name] = True
    return result


def enable_gateway(result: dict[str, bool]) -> None:
    """开启文字网关历阶段及真实后端浏览器回归。"""

    result["backend"] = True
    for name in GATEWAY_OUTPUTS:
        result[name] = True


def normalize_paths(paths: Iterable[str]) -> list[str]:
    """只接受 Git 风格仓库相对路径，拒绝可疑绝对路径和路径穿越。"""

    normalized: list[str] = []
    for raw_path in paths:
        path = raw_path.strip()
        if not path:
            continue
        if "\\" in path or path.startswith("/"):
            raise ClassificationError("变更路径必须使用 Git 风格仓库相对路径")
        parts = PurePosixPath(path).parts
        if not parts or any(part in {"", ".", ".."} for part in parts):
            raise ClassificationError("变更路径包含不安全片段")
        normalized.append(path)
    if not normalized:
        raise ClassificationError("变更路径集合不能为空")
    return sorted(set(normalized))


def is_pure_documentation(paths: list[str]) -> bool:
    """仅 README 和 docs 下 Markdown 可进入纯文档轻量门禁。"""

    return all(
        path == "README.md" or (path.startswith("docs/") and path.endswith(".md"))
        for path in paths
    )


def is_gateway_frontend_path(path: str) -> bool:
    """识别会影响文字网关真实浏览器旅程的前端目录。"""

    gateway_prefixes = (
        "web/admin-console/src/views/token/",
        "web/admin-console/src/api/token",
        "web/admin-console/src/types/token",
        "web/user-console/src/views/ai/",
        "web/user-console/src/views/agent/",
        "web/user-console/src/views/token/",
        "web/user-console/src/components/ai/",
        "web/user-console/src/api/aiGateway",
        "web/user-console/src/api/conversation",
        "web/user-console/src/api/token",
        "web/user-console/src/types/aiGateway",
        "web/user-console/src/types/conversation",
        "web/user-console/src/types/token",
        "web/admin-console/tests/g8-",
        "web/user-console/tests/g8-",
        "web/user-console/tests/g6-",
        "web/admin-console/playwright.g8-",
        "web/user-console/playwright.g8-",
    )
    return path.startswith(gateway_prefixes)


def is_sms_frontend_path(path: str) -> bool:
    """识别短信管理、验证码与认证页面的前端变更。"""

    sms_prefixes = (
        "web/admin-console/src/views/auth/",
        "web/admin-console/src/views/sms/",
        "web/admin-console/src/components/sms/",
        "web/admin-console/src/api/auth",
        "web/admin-console/src/api/sms",
        "web/admin-console/src/stores/auth",
        "web/admin-console/src/stores/token-storage",
        "web/admin-console/src/types/sms",
        "web/admin-console/tests/sms-",
        "web/admin-console/tests/admin-verification-",
        "web/user-console/src/views/auth/",
        "web/user-console/src/api/auth",
        "web/user-console/src/types/auth",
        "web/user-console/src/utils/sms",
        "web/user-console/tests/sms",
    )
    return path.startswith(sms_prefixes)


def is_cross_domain_frontend_path(path: str) -> bool:
    """识别会同时影响认证和文字网关旅程的前端公共入口。"""

    cross_domain_paths = {
        "web/admin-console/src/App.vue",
        "web/admin-console/src/main.ts",
        "web/admin-console/src/api/http.ts",
        "web/admin-console/src/stores/auth.ts",
        "web/admin-console/src/stores/token-storage.ts",
        "web/admin-console/package.json",
        "web/admin-console/package-lock.json",
        "web/admin-console/vite.config.ts",
        "web/user-console/src/App.vue",
        "web/user-console/src/main.ts",
        "web/user-console/src/api/http.ts",
        "web/user-console/src/stores/auth.ts",
        "web/user-console/package.json",
        "web/user-console/package-lock.json",
        "web/user-console/vite.config.ts",
    }
    return path in cross_domain_paths or path.startswith(
        (
            "web/admin-console/src/router/",
            "web/user-console/src/router/",
        )
    )


def classify_paths(paths: Iterable[str]) -> dict[str, bool]:
    """把变更路径映射为需要执行的门禁集合。"""

    normalized = normalize_paths(paths)
    result = empty_result()
    if is_pure_documentation(normalized):
        result["docs_lightweight"] = True
        return result

    for path in normalized:
        if path == "README.md" or (path.startswith("docs/") and path.endswith(".md")):
            continue

        # CI、基础设施、根级构建资产和无法识别的路径都可能跨模块影响发布，必须完整回归。
        if path.startswith((".github/", "infra/", "scripts/", "tests/")):
            return enable_full(result)

        if path.startswith("server/"):
            result["backend"] = True
            if path.startswith(
                (
                    "server/cmd/ai-gateway-reconcile/",
                    "server/cmd/ai-price-publish/",
                )
            ):
                enable_gateway(result)
                continue
            if path.startswith(
                (
                    "server/internal/modules/auth/",
                    "server/internal/modules/audit/",
                    "server/internal/modules/billing/",
                    "server/internal/modules/finance_consumer/",
                    "server/internal/modules/iam/",
                    "server/internal/modules/identity/",
                    "server/internal/modules/order/",
                    "server/internal/modules/product/",
                    "server/internal/bootstrap/",
                    "server/internal/config/",
                    "server/internal/httpserver/",
                    "server/internal/middleware/",
                    "server/internal/router/",
                    "server/migrations/",
                    "server/cmd/seed-admin/",
                    "server/pkg/",
                )
            ) or "/security/" in path or "security" in PurePosixPath(path).name.lower() or path in {
                "server/go.mod",
                "server/go.sum",
                "server/cmd/api/main.go",
            }:
                return enable_full(result)
            if path.startswith(
                (
                    "server/internal/modules/token_gateway/",
                    "server/internal/modules/asset/",
                    "server/internal/modules/conversation/",
                    "server/internal/modules/provision/",
                    "server/internal/modules/workbench/",
                )
            ):
                enable_gateway(result)
            elif path.startswith("server/internal/modules/sms/"):
                result["phase5"] = True
            continue

        if path.startswith("web/shared/"):
            result["frontend_admin"] = True
            result["frontend_user"] = True
            result["phase5"] = True
            result["gateway_g8_real_e2e"] = True
            continue

        if path.startswith("web/admin-console/"):
            result["frontend_admin"] = True
            if is_sms_frontend_path(path) or is_cross_domain_frontend_path(path):
                result["phase5"] = True
            if is_gateway_frontend_path(path) or is_cross_domain_frontend_path(path):
                result["gateway_g8_real_e2e"] = True
            continue

        if path.startswith("web/user-console/"):
            result["frontend_user"] = True
            if is_sms_frontend_path(path) or is_cross_domain_frontend_path(path):
                result["phase5"] = True
            if is_gateway_frontend_path(path) or is_cross_domain_frontend_path(path):
                result["gateway_g8_real_e2e"] = True
            continue

        return enable_full(result)

    if not any(result.values()):
        return enable_full(result)
    return result


def classify_draft_paths(paths: Iterable[str]) -> dict[str, bool]:
    """按精确变更路径选择 Draft 快速门禁，Ready 分类仍由 classify_paths 独立负责。"""

    normalized = normalize_paths(paths)
    result = empty_draft_result()
    if is_pure_documentation(normalized):
        result["draft_docs"] = True
        return result

    for path in normalized:
        if path == "README.md" or (path.startswith("docs/") and path.endswith(".md")):
            continue
        if path.startswith((".github/", "infra/scripts/", "scripts/", "tests/")):
            result["draft_python"] = True
            continue
        if path.startswith("server/"):
            result["draft_backend"] = True
            continue
        if path.startswith("web/shared/"):
            result["draft_frontend_admin"] = True
            result["draft_frontend_user"] = True
            continue
        if path.startswith("web/admin-console/"):
            result["draft_frontend_admin"] = True
            continue
        if path.startswith("web/user-console/"):
            result["draft_frontend_user"] = True
            continue
        return enable_all_draft(result)

    if not any(result.values()):
        return enable_all_draft(result)
    return result


def validate_sha(value: str, label: str) -> str:
    """只允许完整 Git 对象摘要进入固定参数的 diff 调用。"""

    if not SHA_PATTERN.fullmatch(value):
        raise ClassificationError(f"{label} 不是合法 Git 摘要")
    return value


def parse_name_status(output: str) -> list[str]:
    """解析 Git NUL 分隔状态；重命名和复制必须同时返回源、目标路径。"""

    tokens = output.split("\0")
    if tokens and tokens[-1] == "":
        tokens.pop()
    paths: list[str] = []
    index = 0
    while index < len(tokens):
        status = tokens[index]
        index += 1
        if not status or status[0] not in "ACDMRT":
            raise ClassificationError("Git 返回未知变更状态")
        path_count = 2 if status[0] in "CR" else 1
        if index + path_count > len(tokens):
            raise ClassificationError("Git 变更状态缺少路径")
        paths.extend(tokens[index : index + path_count])
        index += path_count
    return normalize_paths(paths)


def changed_paths(repo_root: Path, base_sha: str, head_sha: str) -> list[str]:
    """从精确 PR 基线和头提交读取新增、修改、重命名及类型变化路径。"""

    base = validate_sha(base_sha, "base_sha")
    head = validate_sha(head_sha, "head_sha")
    environment = os.environ.copy()
    environment["GIT_OPTIONAL_LOCKS"] = "0"
    completed = subprocess.run(
        [
            "git",
            "-C",
            str(repo_root),
            "diff",
            "--name-status",
            "-z",
            "--find-copies-harder",
            "--diff-filter=ACDMRT",
            f"{base}...{head}",
        ],
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        env=environment,
    )
    if completed.returncode != 0 or completed.stderr:
        raise ClassificationError("无法读取 PR 变更路径")
    return parse_name_status(completed.stdout)


def write_github_outputs(
    output_path: Path,
    result: dict[str, bool],
    pr_mode: str,
    draft_result: dict[str, bool],
) -> None:
    """以 GitHub Actions 规定格式输出低敏布尔门禁，不输出仓库内容。"""

    if pr_mode not in PR_MODES:
        raise ClassificationError("PR 模式只能是 draft 或 ready")
    with output_path.open("a", encoding="utf-8", newline="\n") as output_file:
        output_file.write(f"pr_mode={pr_mode}\n")
        for name in DRAFT_OUTPUT_NAMES:
            output_file.write(
                f"{name}={'true' if draft_result[name] else 'false'}\n"
            )
        for name in OUTPUT_NAMES:
            output_file.write(f"{name}={'true' if result[name] else 'false'}\n")


def main() -> int:
    parser = argparse.ArgumentParser(description="按 PR 变更路径选择 CI 门禁")
    parser.add_argument("--base-sha", required=True)
    parser.add_argument("--head-sha", required=True)
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    parser.add_argument("--github-output", type=Path, required=True)
    parser.add_argument("--pr-mode", choices=PR_MODES, required=True)
    args = parser.parse_args()
    try:
        paths = changed_paths(args.repo_root.resolve(), args.base_sha, args.head_sha)
        result = classify_paths(paths)
        draft_result = classify_draft_paths(paths)
        write_github_outputs(
            args.github_output,
            result,
            args.pr_mode,
            draft_result,
        )
    except (ClassificationError, OSError) as error:
        print(f"CI_CHANGE_SCOPE=FAILED reason={type(error).__name__}", file=sys.stderr)
        return 2
    print("CI_CHANGE_SCOPE=PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

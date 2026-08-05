#!/usr/bin/env python3
"""只读检查阶段 5 分支差异和前端构建产物中的敏感信息。"""

from __future__ import annotations

import argparse
import importlib.util
import pathlib
import re
import subprocess
import sys
from dataclasses import dataclass


PROTECTED_ENV_NAMES = {".env", ".env.local", ".env.test", ".env.production"}
SMS_STATE_PATTERNS = {
    "sms_enabled_true": re.compile(
        r"(?im)^\s*(?:-\s*)?(?:export\s+)?(?:[\"']?SMS_ENABLED[\"']?|\[[\"']SMS_ENABLED[\"']\])"
        r"\s*[:=]\s*[\"']?true[\"']?\s*(?:#.*)?$"
    ),
    "sms_test_mode_false": re.compile(
        r"(?im)^\s*(?:-\s*)?(?:export\s+)?(?:[\"']?SMS_TEST_MODE[\"']?|\[[\"']SMS_TEST_MODE[\"']\])"
        r"\s*[:=]\s*[\"']?false[\"']?\s*(?:#.*)?$"
    ),
}


@dataclass(frozen=True)
class GateFinding:
    """仅保存分类、路径和行号，禁止把命中的敏感正文带入输出。"""

    category: str
    path: pathlib.Path
    lines: tuple[int, ...]


def run_git(repo: pathlib.Path, *arguments: str) -> bytes:
    """以字节形式读取 Git 结果，避免文件名经过终端转义后再解析。"""

    completed = subprocess.run(
        ["git", "-C", str(repo), *arguments],
        check=False,
        capture_output=True,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"Git 命令失败，退出码={completed.returncode}")
    return completed.stdout


def decode_null_paths(raw: bytes) -> list[pathlib.Path]:
    """解析 Git 的 NUL 分隔路径，完整保留空格和非 ASCII 文件名。"""

    return [pathlib.Path(item.decode("utf-8", errors="strict")) for item in raw.split(b"\0") if item]


def load_sensitive_scanner(repo: pathlib.Path):
    """复用仓库已有脱敏扫描规则，避免阶段 5 形成第二套规则真相源。"""

    scanner_path = repo / "tests" / "email" / "sensitive_scan.py"
    if not scanner_path.is_file():
        # 契约测试使用临时仓库，因此从本脚本所属仓库加载规则实现。
        scanner_path = pathlib.Path(__file__).resolve().parents[1] / "tests" / "email" / "sensitive_scan.py"
    spec = importlib.util.spec_from_file_location("molin_sensitive_scan", scanner_path)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载敏感信息扫描规则")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def relative_path(path: pathlib.Path, repo: pathlib.Path) -> str:
    """输出仓库相对路径；构建目录也必须保持可定位但不得泄漏文件内容。"""

    try:
        return path.resolve().relative_to(repo.resolve()).as_posix()
    except ValueError:
        return path.name


def line_number(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def is_forbidden_pattern_literal(text: str, match: re.Match[str]) -> bool:
    """允许只读包装器把危险赋值写进禁止模式数组，但不放行普通字符串赋值。"""

    matched_line = line_number(text, match.start())
    lines = text.splitlines()
    current = lines[matched_line - 1].strip().rstrip(",")
    if not re.fullmatch(r"[\"']SMS_(?:ENABLED=true|TEST_MODE=false)[\"']", current):
        return False
    context_start = max(0, matched_line - 9)
    context_end = min(len(lines), matched_line + 2)
    nearby = "\n".join(lines[context_start:context_end])
    return bool(re.search(r"(?i)(forbidden|pattern)", nearby))


def scan_sms_state(path: pathlib.Path, text: str) -> list[GateFinding]:
    """只把可执行配置赋值判为违规，不误报文档和契约测试中的禁止示例。"""

    if "docs" in path.parts or "tests" in path.parts:
        return []
    findings: list[GateFinding] = []
    for category, pattern in SMS_STATE_PATTERNS.items():
        lines = tuple(
            sorted(
                {
                    line_number(text, match.start())
                    for match in pattern.finditer(text)
                    if not is_forbidden_pattern_literal(text, match)
                }
            )
        )
        if lines:
            findings.append(GateFinding(category, path, lines))
    return findings


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="阶段 5 敏感信息与短信关闭态只读门禁")
    parser.add_argument("--repo-root", default=".", help="待检查仓库根目录")
    parser.add_argument("--base-ref", default="origin/main", help="阶段分支比较基线")
    args = parser.parse_args(argv)

    repo = pathlib.Path(args.repo_root).resolve()
    try:
        run_git(repo, "rev-parse", "--verify", f"{args.base_ref}^{{commit}}")
        # 同时覆盖已提交阶段差异、暂存区、未暂存修改和未忽略的新文件，避免本地提交前形成扫描盲区。
        changed = set(
            decode_null_paths(
                run_git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z", f"{args.base_ref}...HEAD")
            )
        )
        changed.update(
            decode_null_paths(run_git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z"))
        )
        changed.update(
            decode_null_paths(run_git(repo, "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"))
        )
        changed.update(decode_null_paths(run_git(repo, "ls-files", "--others", "--exclude-standard", "-z")))
        tracked = decode_null_paths(run_git(repo, "ls-files", "-z"))
    except (OSError, RuntimeError, UnicodeDecodeError) as error:
        print(f"phase5_sensitive_scan=error")
        print(f"error_category=git_or_path_failure")
        print(f"error_type={type(error).__name__}")
        return 2

    findings: list[GateFinding] = []
    for path in tracked:
        if path.name in PROTECTED_ENV_NAMES:
            findings.append(GateFinding("tracked_protected_env", repo / path, (0,)))

    scan_targets = [str(repo / path) for path in sorted(changed) if (repo / path).is_file()]
    for relative in ("web/admin-console/dist", "web/user-console/dist"):
        target = repo / relative
        if target.is_dir():
            scan_targets.append(str(target))

    scanner = load_sensitive_scanner(repo)
    files, skipped = scanner.iter_files(scan_targets)
    for path in skipped:
        findings.append(GateFinding("protected_env_in_scan_scope", path, (0,)))

    read_errors = 0
    for path in files:
        try:
            data = path.read_bytes()
            if b"\x00" in data:
                continue
            text = data.decode("utf-8", errors="replace")
        except OSError:
            findings.append(GateFinding("read_failure", path, (0,)))
            read_errors += 1
            continue
        for finding in scanner.scan_text(path, text, set()):
            if finding.level in {"FAIL", "REVIEW"}:
                findings.append(GateFinding(finding.category, path, finding.lines))
        findings.extend(scan_sms_state(path, text))

    for finding in sorted(findings, key=lambda item: (item.category, str(item.path), item.lines)):
        lines = ",".join(str(line) for line in finding.lines)
        print(
            f"[FAIL] category={finding.category} "
            f"file={relative_path(finding.path, repo)} lines={lines}"
        )

    sms_enable_count = sum(finding.category == "sms_enabled_true" for finding in findings)
    status = "passed" if not findings and read_errors == 0 else "failed"
    print(f"phase5_sensitive_scan={status}")
    print(f"files_scanned={len(files)}")
    print(f"findings={len(findings)}")
    print(f"sms_enable_literals={sms_enable_count}")
    print("external_actions=0")
    print("real_sms_sent=0")
    return 0 if status == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())

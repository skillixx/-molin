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
        r"(?m)(?<![A-Za-z0-9_])(?:(?i:export)\s+)?"
        r"(?:[\"']?SMS_ENABLED[\"']?|\[\s*[\"']SMS_ENABLED[\"']\s*\]|(?i:\$env:SMS_ENABLED)|"
        r"\$[A-Za-z_]\w*\[\s*[\"']SMS_ENABLED[\"']\s*\])"
        r"\s*[:=]\s*(?:[\"']?(?i:true)[\"']?|(?i:\$true))(?=\s|[,;#}]|$)"
    ),
    "sms_test_mode_false": re.compile(
        r"(?m)(?<![A-Za-z0-9_])(?:(?i:export)\s+)?"
        r"(?:[\"']?SMS_TEST_MODE[\"']?|\[\s*[\"']SMS_TEST_MODE[\"']\s*\]|(?i:\$env:SMS_TEST_MODE)|"
        r"\$[A-Za-z_]\w*\[\s*[\"']SMS_TEST_MODE[\"']\s*\])"
        r"\s*[:=]\s*(?:[\"']?(?i:false)[\"']?|(?i:\$false))(?=\s|[,;#}]|$)"
    ),
}
REJECT_INFO_CATEGORIES = {"static_phone_literal", "synthetic_test_otp"}


@dataclass(frozen=True)
class GateFinding:
    """仅保存分类、路径和行号，禁止把命中的敏感正文带入输出。"""

    category: str
    path: pathlib.Path
    lines: tuple[int, ...]
    source_ref: str = ""


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


def list_branch_objects(repo: pathlib.Path, base_ref: str) -> list[tuple[str, pathlib.Path]]:
    """列出阶段分支引入的历史对象及其最后可识别路径。"""

    raw = run_git(repo, "rev-list", "--objects", f"{base_ref}..HEAD")
    objects: list[tuple[str, pathlib.Path]] = []
    seen: set[str] = set()
    for raw_line in raw.splitlines():
        object_id_raw, separator, path_raw = raw_line.partition(b" ")
        object_id = object_id_raw.decode("ascii", errors="strict")
        if not re.fullmatch(r"[0-9a-f]{40,64}", object_id) or object_id in seen:
            continue
        seen.add(object_id)
        # 无路径对象仍使用固定安全名称参与内容扫描；输出只展示对象摘要。
        relative = pathlib.Path(path_raw.decode("utf-8", errors="strict")) if separator else pathlib.Path("git-object.txt")
        objects.append((object_id, relative))
    return objects


def list_branch_commit_paths(repo: pathlib.Path, base_ref: str) -> list[tuple[str, pathlib.Path]]:
    """逐提交读取变更路径，确保复用既有 blob 的受保护文件也不能从历史中消失。"""

    commits = run_git(repo, "rev-list", "--reverse", f"{base_ref}..HEAD").splitlines()
    paths: list[tuple[str, pathlib.Path]] = []
    for raw_commit in commits:
        commit = raw_commit.decode("ascii", errors="strict")
        changed = decode_null_paths(
            run_git(
                repo,
                "diff-tree",
                "--root",
                "--first-parent",
                "--no-commit-id",
                "--name-only",
                "--diff-filter=ACMR",
                "-r",
                "-z",
                commit,
            )
        )
        paths.extend((commit, path) for path in changed)
    return paths


def read_branch_blobs(
    repo: pathlib.Path, objects: list[tuple[str, pathlib.Path]]
) -> list[tuple[str, pathlib.Path, bytes]]:
    """通过单个 cat-file 批处理读取历史 blob，避免为每个对象启动一个 Git 进程。"""

    object_paths = {object_id: path for object_id, path in objects}
    request = b"".join(object_id.encode("ascii") + b"\n" for object_id, _ in objects)
    completed = subprocess.run(
        ["git", "-C", str(repo), "cat-file", "--batch"],
        input=request,
        check=False,
        capture_output=True,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"Git 对象批量读取失败，退出码={completed.returncode}")

    blobs: list[tuple[str, pathlib.Path, bytes]] = []
    output = completed.stdout
    offset = 0
    for _ in objects:
        header_end = output.find(b"\n", offset)
        if header_end < 0:
            raise RuntimeError("Git 对象批量输出缺少头部终止符")
        header = output[offset:header_end].split(b" ")
        if len(header) != 3:
            raise RuntimeError("Git 对象批量输出头部格式异常")
        object_id = header[0].decode("ascii", errors="strict")
        object_type = header[1]
        size = int(header[2])
        data_start = header_end + 1
        data_end = data_start + size
        if data_end >= len(output) or output[data_end:data_end + 1] != b"\n":
            raise RuntimeError("Git 对象批量输出长度异常")
        if object_type == b"blob":
            blobs.append((object_id, object_paths[object_id], output[data_start:data_end]))
        offset = data_end + 1
    return blobs


def scanner_findings(scanner, path: pathlib.Path, text: str, source_ref: str = "") -> list[GateFinding]:
    """把通用扫描器结果收敛为阶段 5 的 fail-closed 判定。"""

    findings: list[GateFinding] = []
    for finding in scanner.scan_text(path, text, set()):
        if finding.level in {"FAIL", "REVIEW"} or finding.category in REJECT_INFO_CATEGORIES:
            findings.append(GateFinding(finding.category, path, finding.lines, source_ref))
    for finding in scan_sms_state(path, text):
        findings.append(GateFinding(finding.category, finding.path, finding.lines, source_ref))
    return findings


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
    parser.add_argument("--require-dist", action="store_true", help="要求两套前端构建产物存在且结构完整")
    args = parser.parse_args(argv)

    repo = pathlib.Path(args.repo_root).resolve()
    try:
        run_git(repo, "rev-parse", "--verify", f"{args.base_ref}^{{commit}}")
        # 已提交内容由历史 blob 扫描覆盖；当前文件扫描只处理尚未提交的工作树差异，避免同一 HEAD blob 重复计数。
        committed_changed = set(
            decode_null_paths(
                run_git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z", f"{args.base_ref}...HEAD")
            )
        )
        working_changed = set(
            decode_null_paths(run_git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z"))
        )
        working_changed.update(
            decode_null_paths(run_git(repo, "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"))
        )
        working_changed.update(
            decode_null_paths(run_git(repo, "ls-files", "--others", "--exclude-standard", "-z"))
        )
        tracked = decode_null_paths(run_git(repo, "ls-files", "-z"))
        branch_blobs = read_branch_blobs(repo, list_branch_objects(repo, args.base_ref))
        branch_commit_paths = list_branch_commit_paths(repo, args.base_ref)
        commit_message_parts = run_git(
            repo, "log", "-z", "--format=%H%x00%B", f"{args.base_ref}..HEAD"
        ).split(b"\0")
    except (OSError, RuntimeError, UnicodeDecodeError) as error:
        print(f"phase5_sensitive_scan=error")
        print(f"error_category=git_or_path_failure")
        print(f"error_type={type(error).__name__}")
        return 2

    findings: list[GateFinding] = []
    for path in tracked:
        if path.name in PROTECTED_ENV_NAMES:
            findings.append(GateFinding("tracked_protected_env", repo / path, (0,)))
    for commit, path in branch_commit_paths:
        if path.name in PROTECTED_ENV_NAMES:
            findings.append(
                GateFinding("historical_protected_env", repo / path, (0,), f"commit:{commit[:12]}")
            )

    scan_targets = [str(repo / path) for path in sorted(working_changed) if (repo / path).is_file()]
    dist_artifacts_verified = 0
    for relative in ("web/admin-console/dist", "web/user-console/dist"):
        target = repo / relative
        dist_ready = (target / "index.html").is_file() and (target / "assets").is_dir()
        if dist_ready:
            scan_targets.append(str(target))
            dist_artifacts_verified += 1
        elif args.require_dist:
            findings.append(GateFinding("missing_required_dist", target, (0,)))

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
        findings.extend(scanner_findings(scanner, path, text))

    historical_blobs_scanned = 0
    for object_id, historical_path, data in branch_blobs:
        if b"\x00" in data:
            continue
        historical_blobs_scanned += 1
        text = data.decode("utf-8", errors="replace")
        findings.extend(
            scanner_findings(scanner, repo / historical_path, text, f"blob:{object_id[:12]}")
        )

    # 提交说明同样属于 Git 历史；按 NUL 分隔读取，任何命中只输出提交摘要而不输出正文。
    commit_messages_scanned = 0
    for index in range(0, len(commit_message_parts) - 1, 2):
        revision = commit_message_parts[index].decode("ascii", errors="strict")[:12]
        message = commit_message_parts[index + 1].decode("utf-8", errors="replace")
        if not revision:
            continue
        commit_messages_scanned += 1
        findings.extend(
            scanner_findings(scanner, repo / "COMMIT_MESSAGE.txt", message, f"commit:{revision}")
        )

    for finding in sorted(
        findings, key=lambda item: (item.category, str(item.path), item.source_ref, item.lines)
    ):
        lines = ",".join(str(line) for line in finding.lines)
        source_ref = f" source_ref={finding.source_ref}" if finding.source_ref else ""
        print(f"[FAIL] category={finding.category} file={relative_path(finding.path, repo)} lines={lines}{source_ref}")

    sms_enable_count = sum(
        len(finding.lines) for finding in findings if finding.category == "sms_enabled_true"
    )
    status = "passed" if not findings and read_errors == 0 else "failed"
    print(f"phase5_sensitive_scan={status}")
    print(f"files_scanned={len(files)}")
    print(f"committed_paths_considered={len(committed_changed)}")
    print(f"historical_path_events_checked={len(branch_commit_paths)}")
    print(f"historical_blobs_scanned={historical_blobs_scanned}")
    print(f"commit_messages_scanned={commit_messages_scanned}")
    print(f"dist_artifacts_verified={dist_artifacts_verified}")
    print(f"findings={len(findings)}")
    print(f"sms_enable_literals={sms_enable_count}")
    print("external_actions=0")
    print("real_sms_sent=0")
    return 0 if status == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())

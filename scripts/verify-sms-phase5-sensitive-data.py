#!/usr/bin/env python3
"""只读检查阶段 5 分支差异和前端构建产物中的敏感信息。"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import pathlib
import re
import subprocess
import sys
import unicodedata
from dataclasses import dataclass


PROTECTED_ENV_NAMES = {".env", ".env.local", ".env.test", ".env.production"}
TRUE_VALUES = {"1", "t", "true", "y", "yes", "on"}


def compile_sms_assignment_pattern(key: str) -> re.Pattern[str]:
    """为环境键生成赋值匹配器；布尔判定由运行时真值表统一完成。"""

    escaped_key = re.escape(key)
    return re.compile(
        rf"(?m)(?<![A-Za-z0-9_])(?:(?i:export)\s+)?"
        rf"(?:[\"']?{escaped_key}[\"']?|\[\s*[\"']{escaped_key}[\"']\s*\]|(?i:\$env:{escaped_key})|"
        rf"\$[A-Za-z_]\w*\[\s*[\"']{escaped_key}[\"']\s*\])"
        rf"\s*[:=]\s*(?P<quote>[\"']?)(?P<value>\$?[A-Za-z0-9_-]+)(?P=quote)(?=\s|[,;#}}]|$)"
    )


SMS_ASSIGNMENT_PATTERNS = {
    "sms_enabled_true": compile_sms_assignment_pattern("SMS_ENABLED"),
    "sms_test_mode_false": compile_sms_assignment_pattern("SMS_TEST_MODE"),
}
REJECT_INFO_CATEGORIES = {"static_phone_literal", "synthetic_test_otp"}
ALIYUN_ACCESS_KEY_ID_RE = re.compile(r"(?<![A-Za-z0-9])LTAI[A-Za-z0-9]{12,32}(?![A-Za-z0-9])")
BEARER_TOKEN_RE = re.compile(
    r"(?i)\bBearer[ \t]+(?P<value>[A-Za-z0-9._~+/-]{16,}=*)(?![A-Za-z0-9._~+/-])"
)
PATH_PHONE_RE = re.compile(r"(?<!\d)1[3-9]\d{9}(?!\d)")
PATH_OTP_RE = re.compile(r"(?<!\d)\d{6}(?!\d)")
PATH_JWT_RE = re.compile(
    r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(?![A-Za-z0-9_-])"
)
PATH_EMAIL_RE = re.compile(r"(?i)[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")


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


def parse_raw_diff(raw: bytes, source: str) -> list[tuple[str, pathlib.Path, str]]:
    """解析 Git `--raw -z`，保留每次变更的新路径与新 blob，不受文件名空格影响。"""

    tokens = raw.split(b"\0")
    events: list[tuple[str, pathlib.Path, str]] = []
    index = 0
    while index < len(tokens) and tokens[index]:
        header = tokens[index].split(b" ")
        index += 1
        if len(header) != 5 or not header[0].startswith(b":"):
            raise RuntimeError("Git raw diff 头部格式异常")
        object_id = header[3].decode("ascii", errors="strict")
        status = header[4].decode("ascii", errors="strict")
        if not re.fullmatch(r"[0-9a-f]{40,64}", object_id) or not status:
            raise RuntimeError("Git raw diff 对象摘要或状态异常")
        if status[0] in {"R", "C"}:
            if index + 1 >= len(tokens):
                raise RuntimeError("Git raw diff 重命名路径缺失")
            index += 1  # 跳过旧路径，只扫描提交后的新路径和新 blob。
            path_raw = tokens[index]
            index += 1
        else:
            if index >= len(tokens):
                raise RuntimeError("Git raw diff 路径缺失")
            path_raw = tokens[index]
            index += 1
        path = pathlib.Path(path_raw.decode("utf-8", errors="strict"))
        events.append((source, path, object_id))
    return events


def list_branch_blob_events(repo: pathlib.Path, base_ref: str) -> list[tuple[str, pathlib.Path, str]]:
    """逐提交列出新 blob，确保删除、覆盖、重命名和复用基线 blob 都按实际路径扫描。"""

    commits = run_git(repo, "rev-list", "--reverse", f"{base_ref}..HEAD").splitlines()
    events: list[tuple[str, pathlib.Path, str]] = []
    for raw_commit in commits:
        commit = raw_commit.decode("ascii", errors="strict")
        ancestry = run_git(repo, "rev-list", "--parents", "-n", "1", commit).split()
        if len(ancestry) > 1:
            # 对合并提交显式比较第一父提交，确保冲突解决中新写入的 blob 不被 Git 默认合并展示规则隐藏。
            raw = run_git(
                repo,
                "diff",
                "--raw",
                "--no-abbrev",
                "--diff-filter=ACMR",
                "-r",
                "-z",
                ancestry[1].decode("ascii", errors="strict"),
                commit,
            )
        else:
            raw = run_git(
                repo,
                "diff-tree",
                "--root",
                "--no-commit-id",
                "--raw",
                "--no-abbrev",
                "--diff-filter=ACMR",
                "-r",
                "-z",
                commit,
            )
        events.extend(parse_raw_diff(raw, f"commit:{commit[:12]}"))
    return events


def read_git_objects(repo: pathlib.Path, object_ids: set[str]) -> dict[str, tuple[bytes, bytes]]:
    """通过单个 cat-file 批处理读取对象，供历史提交和暂存区共享同一解析逻辑。"""

    ordered_ids = sorted(object_ids)
    request = b"".join(object_id.encode("ascii") + b"\n" for object_id in ordered_ids)
    completed = subprocess.run(
        ["git", "-C", str(repo), "cat-file", "--batch"],
        input=request,
        check=False,
        capture_output=True,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"Git 对象批量读取失败，退出码={completed.returncode}")

    objects: dict[str, tuple[bytes, bytes]] = {}
    output = completed.stdout
    offset = 0
    for _ in ordered_ids:
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
        objects[object_id] = (object_type, output[data_start:data_end])
        offset = data_end + 1
    return objects


def scanner_findings(scanner, path: pathlib.Path, text: str, source_ref: str = "") -> list[GateFinding]:
    """把通用扫描器结果收敛为阶段 5 的 fail-closed 判定。"""

    findings: list[GateFinding] = []
    for finding in scanner.scan_text(path, text, set()):
        if finding.level in {"FAIL", "REVIEW"} or finding.category in REJECT_INFO_CATEGORIES:
            findings.append(GateFinding(finding.category, path, finding.lines, source_ref))
    for finding in scan_sms_state(path, text):
        findings.append(GateFinding(finding.category, finding.path, finding.lines, source_ref))
    for match in ALIYUN_ACCESS_KEY_ID_RE.finditer(text):
        # 裸凭据没有可靠的占位上下文；只要形态命中就拒绝，避免随机值包含 test/example 子串而被误放。
        findings.append(
            GateFinding("aliyun_access_key_id", path, (line_number(text, match.start()),), source_ref)
        )
    for match in BEARER_TOKEN_RE.finditer(text):
        findings.append(
            GateFinding("bearer_token", path, (line_number(text, match.start()),), source_ref)
        )
    return findings


def is_text_path(scanner, path: pathlib.Path) -> bool:
    """判断 NUL 是否出现在应为文本的文件中；二进制产物仍按二进制安全跳过。"""

    suffix = ".env.example" if path.name.endswith(".env.example") else path.suffix.lower()
    return suffix in scanner.TEXT_SUFFIXES or path.name in PROTECTED_ENV_NAMES


def relative_path(path: pathlib.Path, repo: pathlib.Path) -> str:
    """输出仓库相对路径；构建目录也必须保持可定位但不得泄漏文件内容。"""

    try:
        return path.resolve().relative_to(repo.resolve()).as_posix()
    except ValueError:
        return path.name


def safe_path_metadata(path: pathlib.Path, repo: pathlib.Path) -> tuple[str, str]:
    """仅在路径不含敏感形态或控制字符时展示原文，并始终提供可复核摘要。"""

    raw = relative_path(path, repo)
    digest = hashlib.sha256(raw.encode("utf-8", errors="surrogatepass")).hexdigest()
    has_control = any(
        unicodedata.category(character).startswith("C")
        or unicodedata.category(character) in {"Zl", "Zp"}
        for character in raw
    )
    has_sensitive_shape = any(
        pattern.search(raw)
        for pattern in (
            PATH_PHONE_RE,
            PATH_OTP_RE,
            ALIYUN_ACCESS_KEY_ID_RE,
            PATH_JWT_RE,
            PATH_EMAIL_RE,
            BEARER_TOKEN_RE,
        )
    )
    label = "[redacted-sensitive-path]" if has_control or has_sensitive_shape else raw
    return label, digest


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
    """按 Go 运行时真值表判断危险赋值，不误报文档和契约测试中的禁止示例。"""

    if "docs" in path.parts or "tests" in path.parts:
        return []
    findings: list[GateFinding] = []
    for category, pattern in SMS_ASSIGNMENT_PATTERNS.items():
        lines = tuple(
            sorted(
                {
                    line_number(text, match.start())
                    for match in pattern.finditer(text)
                    if not is_forbidden_pattern_literal(text, match)
                    and (
                        (
                            category == "sms_enabled_true"
                            and match.group("value").lstrip("$").lower() in TRUE_VALUES
                        )
                        or (
                            category == "sms_test_mode_false"
                            and match.group("value").lstrip("$").lower() not in TRUE_VALUES
                        )
                    )
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
        branch_blob_events = list_branch_blob_events(repo, args.base_ref)
        index_blob_events = parse_raw_diff(
            run_git(
                repo,
                "diff",
                "--cached",
                "--raw",
                "--no-abbrev",
                "--diff-filter=ACMR",
                "-z",
            ),
            "index",
        )
        object_data = read_git_objects(
            repo,
            {object_id for _, _, object_id in branch_blob_events + index_blob_events},
        )
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
                findings.append(GateFinding("nul_byte_in_text", path, (0,)))
                continue
            text = data.decode("utf-8", errors="replace")
        except OSError:
            findings.append(GateFinding("read_failure", path, (0,)))
            read_errors += 1
            continue
        findings.extend(scanner_findings(scanner, path, text))

    historical_blob_versions_scanned = 0
    for source_ref, historical_path, object_id in branch_blob_events:
        object_type, data = object_data[object_id]
        if object_type != b"blob":
            continue
        if historical_path.name in PROTECTED_ENV_NAMES:
            findings.append(
                GateFinding("historical_protected_env", repo / historical_path, (0,), source_ref)
            )
        if b"\x00" in data:
            if is_text_path(scanner, historical_path):
                findings.append(
                    GateFinding("nul_byte_in_text", repo / historical_path, (0,), source_ref)
                )
            continue
        historical_blob_versions_scanned += 1
        text = data.decode("utf-8", errors="replace")
        findings.extend(
            scanner_findings(scanner, repo / historical_path, text, source_ref)
        )

    index_blobs_scanned = 0
    for _, index_path, object_id in index_blob_events:
        object_type, data = object_data[object_id]
        if object_type != b"blob":
            continue
        source_ref = f"index:{object_id[:12]}"
        if b"\x00" in data:
            if is_text_path(scanner, index_path):
                findings.append(GateFinding("nul_byte_in_text", repo / index_path, (0,), source_ref))
            continue
        index_blobs_scanned += 1
        findings.extend(
            scanner_findings(
                scanner,
                repo / index_path,
                data.decode("utf-8", errors="replace"),
                source_ref,
            )
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
        safe_path, path_digest = safe_path_metadata(finding.path, repo)
        print(
            f"[FAIL] category={finding.category} file={safe_path} "
            f"path_sha256={path_digest} lines={lines}{source_ref}"
        )

    sms_enable_count = sum(
        len(finding.lines) for finding in findings if finding.category == "sms_enabled_true"
    )
    status = "passed" if not findings and read_errors == 0 else "failed"
    print(f"phase5_sensitive_scan={status}")
    print(f"files_scanned={len(files)}")
    print(f"committed_paths_considered={len(committed_changed)}")
    print(f"historical_path_events_checked={len(branch_blob_events)}")
    print(f"historical_blob_versions_scanned={historical_blob_versions_scanned}")
    print(f"index_blobs_scanned={index_blobs_scanned}")
    print(f"commit_messages_scanned={commit_messages_scanned}")
    print(f"dist_artifacts_verified={dist_artifacts_verified}")
    print(f"findings={len(findings)}")
    print(f"sms_enable_literals={sms_enable_count}")
    print("external_actions=0")
    print("real_sms_sent=0")
    return 0 if status == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())

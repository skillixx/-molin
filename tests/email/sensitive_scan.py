#!/usr/bin/env python3
"""离线扫描邮件相关源码和构建产物，只输出分类、相对路径与行号。"""

from __future__ import annotations

import argparse
import collections
import pathlib
import re
import sys
from dataclasses import dataclass


TEXT_SUFFIXES = {
    ".bash", ".cjs", ".conf", ".css", ".env.example", ".go", ".html",
    ".http", ".js", ".json", ".jsonl", ".jsx", ".log", ".map", ".md", ".mjs",
    ".ps1", ".py", ".sh", ".sql", ".toml", ".ts", ".tsx", ".txt",
    ".vue", ".yaml", ".yml",
}
SKIP_PARTS = {".git", "node_modules", "vendor", "__pycache__"}
PROHIBITED_ENV_NAMES = {".env", ".env.local", ".env.test", ".env.production"}
PLACEHOLDER_DOMAINS = {"example.com", "example.net", "example.org", "example.invalid", "localhost"}
PLACEHOLDER_PHONES = {"13800138000", "13900000000", "18888888888"}
SAFE_VALUE_WORDS = {
    "changeme", "example", "fake", "masked", "placeholder", "redacted", "replace_me",
    "replace-with", "test", "your_access_key", "your_secret",
}


EMAIL_RE = re.compile(r"(?i)(?<![\w.*])(?:[a-z0-9.!#$%&'+/=?^_`{|}~-]+)@(?:[a-z0-9-]+\.)+[a-z]{2,}")
PHONE_RE = re.compile(r"(?<!\d)1[3-9]\d{9}(?!\d)")
PRIVATE_KEY_RE = re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----")
JWT_RE = re.compile(r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(?![A-Za-z0-9_-])")
CREDENTIAL_RE = re.compile(
    r"(?i)\b(?:access[_-]?key[_-]?(?:id|secret)|accesskey(?:id|secret)|secret[_-]?access[_-]?key|client[_-]?secret|api[_-]?secret)\b"
    r"\s*[=:]\s*(?P<quote>[\"']?)(?P<value>[^\s,;\"'}]{6,})"
)
REFRESH_RE = re.compile(
    r"(?i)\brefresh[_-]?token\b\s*[=:]\s*(?P<quote>[\"']?)(?P<value>[^\s,;\"'}]{12,})"
)
OTP_VALUE_RE = re.compile(r"(?i)[\"']?(?:code|otp|verification_code|debug_code)[\"']?\s*[=:]\s*[\"']?(\d{6})(?!\d)")
DEBUG_RESPONSE_RE = re.compile(
    r"(?i)(?:[\"'](?:code|otp|verification_code|debug_code)[\"']\s*:\s*[A-Za-z_$][\w.$]*|"
    r"\b(?:Code|OTP|VerificationCode|DebugCode)\s*:\s*[A-Za-z_][\w.]*)"
)
PROVIDER_OUTPUT_FIELD_RE = re.compile(
    r"(?i)(?:json:\"(?:provider_raw|provider_message|provider_response_body|raw_response|raw_body|html_body)\"|"
    r"[\"'](?:provider_raw|provider_message|provider_response_body|raw_response|raw_body|html_body)[\"']\s*:)"
)
OUTPUT_CALL_RE = re.compile(r"(?i)(?:log\.(?:Printf|Println|Errorf)|fmt\.Printf|console\.(?:log|error)|logger\.[A-Za-z]+|print\s*\()")
RAW_NAME_RE = re.compile(r"(?i)\b(?:provider_raw|provider_message|provider_response_body|raw_response|raw_body|response_body|html_body|template_data)\b")
DOCUMENT_LITERAL_RE = re.compile(r"(?i)\b(?:TemplateData|template_data|AccessKey|refresh token|JWT|debug code)\b")


@dataclass(frozen=True)
class Finding:
    level: str
    category: str
    path: pathlib.Path
    lines: tuple[int, ...]


def line_number(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def is_placeholder_domain(domain: str, allowed_domains: set[str]) -> bool:
    lowered = domain.lower().rstrip(".")
    return (
        lowered in PLACEHOLDER_DOMAINS
        or lowered in allowed_domains
        or lowered.endswith((".invalid", ".test", ".example"))
    )


def is_safe_config_value(value: str) -> bool:
    lowered = value.lower().strip("\"'<>[]{}()")
    return (
        any(word in lowered for word in SAFE_VALUE_WORDS)
        or "$" in value
        or "{{" in value
        or "}}" in value
        or lowered.startswith(("env(", "getenv("))
        or lowered.startswith(("read-host", "process.env", "os.getenv", "config."))
    )


def looks_like_secret_literal(value: str) -> bool:
    """仅把具有令牌形态的硬编码字面量判为秘密，变量和函数调用不误报。"""
    stripped = value.strip("\"'<>[]{}()")
    if is_safe_config_value(value) or len(stripped) < 16:
        return False
    if any(character in stripped for character in "()[]{}$ "):
        return False
    if re.fullmatch(r"[A-Za-z_][A-Za-z_]*", stripped):
        return False
    return bool(re.search(r"[A-Za-z]", stripped) and re.search(r"\d", stripped))


def add_match(bucket: dict[tuple[str, str], set[int]], level: str, category: str, text: str, offset: int) -> None:
    bucket.setdefault((level, category), set()).add(line_number(text, offset))


def scan_text(path: pathlib.Path, text: str, allowed_domains: set[str]) -> list[Finding]:
    """按上下文区分真实值、占位值和仅用于说明的术语。"""
    bucket: dict[tuple[str, str], set[int]] = {}
    source_like_test = (
        "tests" in path.parts
        or "apitest" in path.parts
        or path.name.endswith("_test.go")
        or path.name.endswith(".http")
    )
    documentation = path.suffix.lower() == ".md"
    runtime_artifact = path.suffix.lower() in {".jsonl", ".log"}
    scanner_fixture = path.name == "sensitive_scan_selftest.py"

    for match in EMAIL_RE.finditer(text):
        domain = match.group(0).rsplit("@", 1)[1]
        if is_placeholder_domain(domain, allowed_domains):
            level, category = "INFO", "placeholder_email"
        elif runtime_artifact:
            level, category = "FAIL", "unmasked_email_artifact"
        elif source_like_test or documentation:
            level, category = "INFO", "static_email_literal"
        else:
            level, category = "REVIEW", "complete_email_literal"
        add_match(bucket, level, category, text, match.start())

    for match in PHONE_RE.finditer(text):
        phone = match.group(0)
        if phone in PLACEHOLDER_PHONES or len(set(phone[3:])) <= 2:
            level, category = "INFO", "placeholder_phone"
        elif runtime_artifact:
            level, category = "FAIL", "unmasked_phone_artifact"
        elif source_like_test or documentation:
            level, category = "INFO", "static_phone_literal"
        else:
            level, category = "REVIEW", "complete_phone_literal"
        add_match(bucket, level, category, text, match.start())

    for match in PRIVATE_KEY_RE.finditer(text):
        add_match(bucket, "FAIL", "private_key", text, match.start())
    for match in JWT_RE.finditer(text):
        add_match(bucket, "FAIL", "jwt_value", text, match.start())
    for match in CREDENTIAL_RE.finditer(text):
        if looks_like_secret_literal(match.group("value")):
            add_match(bucket, "FAIL", "access_key_or_secret_value", text, match.start())
    for match in REFRESH_RE.finditer(text):
        if looks_like_secret_literal(match.group("value")):
            add_match(bucket, "FAIL", "refresh_token_value", text, match.start())
    for match in OTP_VALUE_RE.finditer(text):
        safe_literal = source_like_test or documentation
        level = "INFO" if safe_literal else "FAIL"
        category = "synthetic_test_otp" if safe_literal else "otp_value"
        add_match(bucket, level, category, text, match.start())
    for match in DEBUG_RESPONSE_RE.finditer(text):
        nearby = text[max(0, match.start() - 800): min(len(text), match.end() + 800)]
        if re.search(r"(?i)(otp|verification|verify|debug|验证码|验码)", nearby):
            if scanner_fixture or source_like_test or documentation:
                level, category = "INFO", "static_debug_code_surface"
            elif "web" in path.parts:
                level, category = "INFO", "debug_code_consumer_surface"
            elif re.search(r"(?i)(EMAIL_DEBUG_RETURN_CODE|debug.{0,40}(?:enabled|return|allow)|allow.{0,40}debug)", nearby):
                level, category = "INFO", "gated_debug_code_response_surface"
            else:
                level, category = "REVIEW", "debug_code_response_surface"
            add_match(bucket, level, category, text, match.start())
    for match in PROVIDER_OUTPUT_FIELD_RE.finditer(text):
        level = "FAIL" if runtime_artifact else ("INFO" if scanner_fixture else "REVIEW")
        add_match(bucket, level, "provider_raw_or_body_output", text, match.start())

    # 只有输出调用与高风险变量名出现在同一行时，才判定可能回显供应商正文。
    offset = 0
    for line in text.splitlines(keepends=True):
        if OUTPUT_CALL_RE.search(line) and RAW_NAME_RE.search(line):
            level = "FAIL" if runtime_artifact else ("INFO" if scanner_fixture else "REVIEW")
            add_match(bucket, level, "provider_raw_or_body_log", text, offset)
        offset += len(line)

    # Markdown 中单独出现风险术语只作为字面量说明，不再冒充真实泄漏。
    if documentation:
        for match in DOCUMENT_LITERAL_RE.finditer(text):
            add_match(bucket, "INFO", "document_literal", text, match.start())

    return [
        Finding(level, category, path, tuple(sorted(lines)))
        for (level, category), lines in sorted(bucket.items())
    ]


def iter_files(raw_paths: list[str]) -> tuple[list[pathlib.Path], list[pathlib.Path]]:
    files: set[pathlib.Path] = set()
    skipped: list[pathlib.Path] = []
    for raw in raw_paths:
        path = pathlib.Path(raw)
        candidates = path.rglob("*") if path.is_dir() else (path,)
        for candidate in candidates:
            if not candidate.is_file() or any(part in SKIP_PARTS for part in candidate.parts):
                continue
            if candidate.name in PROHIBITED_ENV_NAMES:
                skipped.append(candidate)
                continue
            suffix = ".env.example" if candidate.name.endswith(".env.example") else candidate.suffix.lower()
            if suffix in TEXT_SUFFIXES:
                files.add(candidate)
    return sorted(files), sorted(set(skipped))


def relative_path(path: pathlib.Path, root: pathlib.Path) -> str:
    try:
        return path.resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        return path.name


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="离线扫描邮件相关敏感信息")
    parser.add_argument("paths", nargs="+", help="待扫描文件或目录")
    parser.add_argument("--repo-root", default=".", help="用于输出相对路径的仓库根目录")
    parser.add_argument("--allow-domain", action="append", default=[], help="允许的占位域名，可重复")
    parser.add_argument("--show-level", action="append", choices=("FAIL", "REVIEW", "INFO"), help="只显示指定级别，可重复；汇总仍统计全部级别")
    parser.add_argument("--show-counts", action="store_true", help="输出各级别分类的文件命中数和行命中数")
    args = parser.parse_args(argv)

    root = pathlib.Path(args.repo_root)
    allowed_domains = {domain.lower() for domain in args.allow_domain}
    files, skipped = iter_files(args.paths)
    fail_count = 0
    review_count = 0
    info_count = 0
    read_errors = 0
    finding_counts: collections.Counter[tuple[str, str]] = collections.Counter()
    line_counts: collections.Counter[tuple[str, str]] = collections.Counter()
    for path in files:
        try:
            data = path.read_bytes()
            if b"\x00" in data:
                continue
            text = data.decode("utf-8", errors="replace")
        except OSError:
            print(f"[ERROR] category=read_failure file={relative_path(path, root)} lines=0")
            read_errors += 1
            continue
        for finding in scan_text(path, text, allowed_domains):
            finding_counts[(finding.level, finding.category)] += 1
            line_counts[(finding.level, finding.category)] += len(finding.lines)
            lines = ",".join(str(line) for line in finding.lines[:50])
            if not args.show_level or finding.level in args.show_level:
                print(f"[{finding.level}] category={finding.category} file={relative_path(path, root)} lines={lines}")
            if finding.level == "FAIL":
                fail_count += 1
            elif finding.level == "REVIEW":
                review_count += 1
            else:
                info_count += 1

    for path in skipped:
        print(f"[SKIP] category=protected_env file={relative_path(path, root)} lines=0")
    result = "PASS" if fail_count == 0 and read_errors == 0 else "FAIL"
    print(
        f"[SUMMARY] result={result} files={len(files)} fail_categories={fail_count} "
        f"review_categories={review_count} info_categories={info_count} "
        f"skipped_protected_env={len(skipped)} read_errors={read_errors}"
    )
    if args.show_counts:
        for (level, category), count in sorted(finding_counts.items()):
            print(f"[COUNT] level={level} category={category} findings={count} lines={line_counts[(level, category)]}")
    if read_errors:
        return 2
    return 1 if fail_count else 0


if __name__ == "__main__":
    sys.exit(main())

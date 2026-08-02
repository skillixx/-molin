#!/usr/bin/env python3
"""Phase 4 同一时间窗运行时敏感信息扫描器。

扫描器只读取已经由受控采集器生成的证据包，不访问网络、数据库或 Redis，
也不执行部署、重启和邮件发送。任何异常均压缩为固定分类，避免把原始证据、
文件路径或异常正文带入标准输出。
"""

from __future__ import annotations

import contextlib
import datetime as dt
import hashlib
import io
import json
import os
import pathlib
import re
import stat
import sys
from dataclasses import dataclass
from typing import Any


SCHEMA = "molin.phase4.runtime-sensitive-scan/v1"
SURFACES = (
    "http_responses",
    "application_logs",
    "audit_projection",
    "database_projection",
    "telemetry",
    "frontend_artifacts",
)
FILE_SURFACES = SURFACES[:-1]
HTTP_ROLES = frozenset({"public_get", "admin_get"})
MAX_MANIFEST_BYTES = 256 * 1024
MAX_FILE_BYTES = 16 * 1024 * 1024
MAX_SURFACE_BYTES = 32 * 1024 * 1024
MAX_FRONTEND_BYTES = 64 * 1024 * 1024
MAX_BUNDLE_BYTES = 96 * 1024 * 1024
MAX_FRONTEND_FILES = 20_000
MAX_WINDOW_SECONDS = 30 * 60
COLLECTOR_MODE = "trusted_local_closed_v1"
SHA256_RE = re.compile(r"\A[0-9a-f]{64}\Z")
DEPLOYMENT_SHA_RE = re.compile(r"\A(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
SAFE_ROLE_RE = re.compile(r"\A[a-z][a-z0-9_]{1,63}\Z")
UTC_RE = re.compile(r"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")
TEXT_FRONTEND_SUFFIXES = frozenset({".css", ".html", ".js", ".json", ".map", ".mjs", ".txt"})
WINDOWS_RESERVED_NAMES = frozenset(
    {"con", "prn", "aux", "nul"}
    | {f"com{index}" for index in range(1, 10)}
    | {f"lpt{index}" for index in range(1, 10)}
)
WINDOWS_REPARSE_ATTRIBUTE = 0x400
WINDOWS_READONLY_ATTRIBUTE = 0x1

# 这些表达式只用于判定是否存在风险，不会回显命中的正文。
EMAIL_VALUE_RE = re.compile(r"(?i)(?<![\w.*])(?:[a-z0-9.!#$%&'+/=?^_`{|}~-]+)@(?:[a-z0-9-]+\.)+[a-z]{2,}")
FRONTEND_PLACEHOLDER_DOMAINS = frozenset({"example.com", "example.net", "example.org", "example.invalid"})
TOKEN_ASSIGN_RE = re.compile(
    r"(?i)\b(?:access|refresh|bootstrap|session|id)[_-]?token\b(?![_-]?(?:hash|present|count))"
    r"\s*[\"']?\s*[:=]\s*[\"']?(?P<value>[^\s,;\"'}]{8,})"
)
SECRET_ASSIGN_RE = re.compile(
    r"(?i)\b(?:access[_-]?key[_-]?(?:id|secret)|secret[_-]?access[_-]?key|client[_-]?secret|api[_-]?secret|password)\b"
    r"(?![_-]?(?:hash|present|count))\s*[\"']?\s*[:=]\s*[\"']?(?P<value>[^\s,;\"'}]{6,})"
)
VALUE_SENSITIVE_PATTERNS = (
    re.compile(r"(?<!\d)1[3-9]\d{9}(?!\d)"),
    re.compile(r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(?![A-Za-z0-9_-])"),
    re.compile(r"(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    re.compile(r"(?i)(?<![A-Za-z0-9])(?:LTAI|AKID)[A-Za-z0-9]{12,}(?![A-Za-z0-9])"),
    re.compile(r"(?i)\bauthorization\s*[:=]\s*[\"']?bearer\s+[A-Za-z0-9._~+/=-]{8,}"),
    re.compile(r"(?i)\b(?:otp|verification[_-]?code|debug[_-]?code|email[_-]?code|phone[_-]?code|验证码)\b\D{0,20}\d{6}(?!\d)"),
    re.compile(r"(?i)(?<![A-Za-z0-9_])code\s*[\"']?\s*[:=]\s*[\"']?\d{6}(?!\d)"),
    re.compile(r"(?i)\brequest[_-]?id\b(?![_-]?(?:hash|present|count))\s*[\"']?\s*[:=]\s*[\"']?[A-Za-z0-9._:/+=-]{6,}"),
    re.compile(r"(?i)\bprovider[_-]?request[_-]?id\b\s*[\"']?\s*[:=]\s*[\"']?[A-Za-z0-9._:/+=-]{6,}"),
    re.compile(r"(?i)\b(?:provider[_-]?(?:raw|message|response|body)|raw[_-]?(?:response|body)|html[_-]?body|template[_-]?data)\b"),
)
HIGH_CARDINAL_FIELD_RE = re.compile(
    r"(?i)\b(?:user|admin|business|provider|template|recipient|client[_-]?ip)(?:[_-]?(?:id|key|value|hash))?\b\s*[\"']?\s*[:=]"
)

PROJECTION_FIELDS = {
    "http_responses": frozenset({
        "records", "route_class", "http_status", "app_code", "message_classification",
        "result", "status", "outcome", "record_count", "email_masked", "phone_masked",
        "request_id_hash", "sensitive_field_count",
    }),
    "audit_projection": frozenset({
        "records", "action_class", "actor_type", "result", "status", "outcome",
        "success", "record_count", "failure_count", "failure_classification",
        "sensitive_field_count",
    }),
    "database_projection": frozenset({
        "records", "table_class", "result", "status", "row_count", "status_count",
        "failure_count", "accepted_count", "non_null_count", "sensitive_field_count",
        "all_masked", "all_hashed", "all_safe",
    }),
}
TELEMETRY_OPERATIONS = {
    "query_templates": frozenset({"template_sync"}),
    "describe_template": frozenset({"template_sync"}),
    "send_mail": frozenset({"register", "login", "reset_password", "bind_email", "admin_verify"}),
}
TELEMETRY_RESULTS = frozenset({"accepted", "failed", "timeout"})
TELEMETRY_SERIES_RE = re.compile(
    r'\Aemail_adapter_calls_total\{operation="([a-z_]+)",scene="([a-z_]+)",result="([a-z_]+)"\} ([0-9]+)\Z'
)


class ScanFailure(Exception):
    """仅携带固定分类，禁止包含原始数据或路径。"""

    def __init__(self, classification: str) -> None:
        super().__init__(classification)
        self.classification = classification


@dataclass(frozen=True)
class ScanResult:
    status: str
    classification: str
    surfaces_passed: int
    files_scanned: int
    bytes_scanned: int
    findings: int
    window_bound: bool
    deployment_bound: bool

    def line(self) -> str:
        """生成不含路径、值或动态异常正文的唯一固定摘要。"""
        return (
            f"status={self.status} classification={self.classification} "
            f"surfaces={len(SURFACES)} surfaces_passed={self.surfaces_passed} "
            f"files_scanned={self.files_scanned} bytes_scanned={self.bytes_scanned} "
            f"findings={self.findings} window_bound={str(self.window_bound).lower()} "
            f"deployment_bound={str(self.deployment_bound).lower()} "
            "writes=false restart=false deploy=false mail_sent=false"
        )


@dataclass(frozen=True)
class CliContract:
    manifest_relative: str
    manifest_sha256: str
    deployment_sha: str
    bundle_id: str


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ScanFailure(classification)


def strict_keys(value: dict[str, Any], expected: set[str], classification: str) -> None:
    require(set(value) == expected, classification)


def parse_utc(value: Any) -> dt.datetime:
    require(isinstance(value, str) and UTC_RE.fullmatch(value) is not None, "time_contract")
    try:
        parsed = dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError as error:
        raise ScanFailure("time_contract") from error
    return parsed.replace(tzinfo=dt.timezone.utc)


def parse_json_bytes(data: bytes, classification: str) -> Any:
    try:
        text = data.decode("utf-8", errors="strict")
        return json.loads(text)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ScanFailure(classification) from error


def parse_safe_relative(raw: Any) -> tuple[str, ...]:
    """在任何文件系统调用前同时按 Windows 与 POSIX 语义拒绝危险路径。"""
    require(isinstance(raw, str) and raw != "" and "\x00" not in raw, "path_contract")
    require("\\" not in raw and ":" not in raw and not raw.startswith(("/", "\\")), "path_contract")
    windows = pathlib.PureWindowsPath(raw)
    posix = pathlib.PurePosixPath(raw)
    require(not windows.is_absolute() and windows.drive == "" and windows.root == "", "path_contract")
    require(not posix.is_absolute(), "path_contract")
    parts = posix.parts
    require(parts != () and all(part not in {"", ".", ".."} for part in parts), "path_contract")
    for part in parts:
        stem = part.rstrip(" .").split(".", 1)[0].lower()
        require(part == part.rstrip(" .") and stem not in WINDOWS_RESERVED_NAMES, "path_contract")
    return parts


def is_reparse(metadata: os.stat_result) -> bool:
    return stat.S_ISLNK(metadata.st_mode) or bool(
        getattr(metadata, "st_file_attributes", 0) & WINDOWS_REPARSE_ATTRIBUTE
    )


def is_closed_metadata(metadata: os.stat_result) -> bool:
    """正式路径仅按 POSIX 权限位验证 collector 已封闭节点。"""
    return os.name == "posix" and metadata.st_mode & 0o222 == 0


def platform_supports_secure_openat() -> bool:
    """正式运行只支持具备 dir_fd/openat 语义的 POSIX 平台。"""
    return (
        os.name == "posix"
        and os.open in os.supports_dir_fd
        and os.stat in os.supports_dir_fd
        and os.scandir in os.supports_fd
    )


def identity_tuple(metadata: os.stat_result) -> tuple[int, int, int, int, int]:
    return (
        metadata.st_dev,
        metadata.st_ino,
        metadata.st_mode,
        metadata.st_size,
        metadata.st_mtime_ns,
    )


def bundle_identity(root: pathlib.Path) -> str:
    try:
        metadata = root.lstat()
    except OSError as error:
        raise ScanFailure("bundle_identity") from error
    require(stat.S_ISDIR(metadata.st_mode) and not is_reparse(metadata), "bundle_identity")
    encoded = f"{metadata.st_dev}:{metadata.st_ino}".encode("ascii")
    return hashlib.sha256(encoded).hexdigest()


def open_regular_descriptor(root: pathlib.Path, parts: tuple[str, ...]) -> tuple[int, list[int], os.stat_result]:
    """正式路径只使用 POSIX openat 逐级锁定目录描述符。"""
    require(platform_supports_secure_openat(), "platform_not_supported")
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0)
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    opened_directories: list[int] = []
    try:
        directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
        directory = os.open(root, directory_flags)
        opened_directories.append(directory)
        root_metadata = os.fstat(directory)
        require(
            stat.S_ISDIR(root_metadata.st_mode)
            and not is_reparse(root_metadata)
            and is_closed_metadata(root_metadata),
            "bundle_not_closed",
        )
        for part in parts[:-1]:
            directory = os.open(part, directory_flags, dir_fd=directory)
            opened_directories.append(directory)
            metadata = os.fstat(directory)
            require(
                stat.S_ISDIR(metadata.st_mode)
                and not is_reparse(metadata)
                and is_closed_metadata(metadata),
                "path_contract",
            )
        descriptor = os.open(parts[-1], flags, dir_fd=directory)
        metadata = os.fstat(descriptor)
        require(
            stat.S_ISREG(metadata.st_mode)
            and not is_reparse(metadata)
            and is_closed_metadata(metadata),
            "file_contract",
        )
        return descriptor, opened_directories, metadata
    except ScanFailure:
        for opened in reversed(opened_directories):
            os.close(opened)
        raise
    except OSError as error:
        for opened in reversed(opened_directories):
            os.close(opened)
        raise ScanFailure("file_contract") from error


def read_regular_file(
    root: pathlib.Path,
    parts: tuple[str, ...],
    expected_sha: str | None,
    maximum_bytes: int = MAX_FILE_BYTES,
) -> bytes:
    if expected_sha is not None:
        require(SHA256_RE.fullmatch(expected_sha) is not None, "hash_contract")
    root_before = bundle_identity(root)
    descriptor, opened_directories, before = open_regular_descriptor(root, parts)
    try:
        require(before.st_size <= maximum_bytes, "size_limit")
        chunks: list[bytes] = []
        digest = hashlib.sha256()
        remaining = before.st_size
        while remaining:
            chunk = os.read(descriptor, min(1024 * 1024, remaining))
            require(chunk != b"", "file_contract")
            chunks.append(chunk)
            digest.update(chunk)
            remaining -= len(chunk)
        after = os.fstat(descriptor)
        require(identity_tuple(before) == identity_tuple(after), "file_identity")
    finally:
        os.close(descriptor)
        for opened in reversed(opened_directories):
            os.close(opened)
    require(bundle_identity(root) == root_before, "bundle_identity")
    data = b"".join(chunks)
    if expected_sha is not None:
        require(digest.hexdigest() == expected_sha, "hash_contract")
    return data


def contains_sensitive(data: bytes, surface: str) -> bool:
    try:
        text = data.decode("utf-8", errors="strict")
    except UnicodeDecodeError:
        return True
    email_matches = list(EMAIL_VALUE_RE.finditer(text))
    if email_matches and (
        surface != "frontend_artifacts"
        or any(match.group(0).rsplit("@", 1)[1].lower() not in FRONTEND_PLACEHOLDER_DOMAINS for match in email_matches)
    ):
        return True
    if any(pattern.search(text) is not None for pattern in VALUE_SENSITIVE_PATTERNS):
        return True
    token_match = TOKEN_ASSIGN_RE.search(text)
    if token_match is not None:
        token_value = token_match.group("value")
        if len(token_value) >= 16 and not re.search(r"[.(){}\[\]]", token_value):
            return True
    secret_match = SECRET_ASSIGN_RE.search(text)
    if secret_match is not None:
        secret_value = secret_match.group("value")
        if (
            len(secret_value) >= 12
            and re.search(r"[A-Za-z]", secret_value)
            and re.search(r"\d", secret_value)
            and not re.search(r"[.(){}\[\]]", secret_value)
        ):
            return True
    return surface in {"application_logs", "telemetry"} and HIGH_CARDINAL_FIELD_RE.search(text) is not None


def validate_projection_fields(surface: str, value: Any) -> None:
    """安全投影只接受每个证据面冻结的低基数字段白名单。"""
    allowed = PROJECTION_FIELDS[surface]
    if isinstance(value, dict):
        for key, nested in value.items():
            require(isinstance(key, str) and key in allowed, "projection_contract")
            validate_projection_fields(surface, nested)
    elif isinstance(value, list):
        for nested in value:
            validate_projection_fields(surface, nested)
    else:
        require(value is None or isinstance(value, (str, int, float, bool)), "projection_contract")


def validate_telemetry(data: bytes) -> None:
    """只接受后端已冻结的唯一指标族、21 个固定标签组合及整数计数。"""
    try:
        text = data.decode("utf-8", errors="strict")
    except UnicodeDecodeError as error:
        raise ScanFailure("telemetry_contract") from error
    require("\x00" not in text and text.endswith("\n"), "telemetry_contract")
    lines = text.splitlines()
    require(len(lines) == 23, "telemetry_contract")
    require(lines[0].startswith("# HELP email_adapter_calls_total "), "telemetry_contract")
    require(lines[1] == "# TYPE email_adapter_calls_total counter", "telemetry_contract")
    actual: set[tuple[str, str, str]] = set()
    for line in lines[2:]:
        match = TELEMETRY_SERIES_RE.fullmatch(line)
        require(match is not None, "telemetry_contract")
        operation, scene, result, _value = match.groups()
        require(operation in TELEMETRY_OPERATIONS, "telemetry_contract")
        require(scene in TELEMETRY_OPERATIONS[operation] and result in TELEMETRY_RESULTS, "telemetry_contract")
        actual.add((operation, scene, result))
    expected = {
        (operation, scene, result)
        for operation, scenes in TELEMETRY_OPERATIONS.items()
        for scene in scenes
        for result in TELEMETRY_RESULTS
    }
    require(actual == expected and len(actual) == 21, "telemetry_contract")


def validate_structured_surface(surface: str, data: bytes) -> None:
    if surface == "telemetry":
        validate_telemetry(data)
        return
    if surface == "application_logs":
        try:
            data.decode("utf-8", errors="strict")
        except UnicodeDecodeError as error:
            raise ScanFailure("log_contract") from error
        return
    parsed = parse_json_bytes(data, "projection_contract")
    validate_projection_fields(surface, parsed)


def validate_entry(
    root: pathlib.Path,
    surface: str,
    raw: Any,
    start: dt.datetime,
    end: dt.datetime,
    deployment_sha: str,
) -> tuple[str, bytes]:
    require(isinstance(raw, dict), "manifest_contract")
    strict_keys(
        raw,
        {"role", "path", "sha256", "captured_at_utc", "deployment_sha", "content_type"},
        "manifest_contract",
    )
    role = raw["role"]
    require(isinstance(role, str) and SAFE_ROLE_RE.fullmatch(role) is not None, "manifest_contract")
    captured = parse_utc(raw["captured_at_utc"])
    require(start <= captured <= end, "window_binding")
    require(raw["deployment_sha"] == deployment_sha, "deployment_binding")
    require(isinstance(raw["content_type"], str) and 1 <= len(raw["content_type"]) <= 80, "manifest_contract")
    parts = parse_safe_relative(raw["path"])
    data = read_regular_file(root, parts, raw["sha256"])
    require(data != b"", "surface_contract")
    validate_structured_surface(surface, data)
    return role, data


def update_frontend_tree_digest(digest: Any, relative: str, data: bytes) -> None:
    encoded = relative.encode("utf-8")
    digest.update(len(encoded).to_bytes(4, "big"))
    digest.update(encoded)
    digest.update(hashlib.sha256(data).digest())


def open_closed_directory_at(parent: int, name: str) -> tuple[int, os.stat_result]:
    """相对已打开父目录使用 openat 打开并锁定只读普通子目录。"""
    parts = parse_safe_relative(name)
    require(len(parts) == 1, "path_contract")
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        before = os.stat(name, dir_fd=parent, follow_symlinks=False)
        descriptor = os.open(name, flags, dir_fd=parent)
        opened = os.fstat(descriptor)
    except OSError as error:
        raise ScanFailure("frontend_contract") from error
    require(
        identity_tuple(before) == identity_tuple(opened)
        and stat.S_ISDIR(opened.st_mode)
        and not is_reparse(opened)
        and is_closed_metadata(opened),
        "frontend_contract",
    )
    return descriptor, opened


def read_frontend_file_at(parent: int, name: str, before: os.stat_result) -> bytes:
    """文件只相对已持有的目录描述符打开，并在关闭前后复核目录项身份。"""
    parts = parse_safe_relative(name)
    require(len(parts) == 1, "path_contract")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(name, flags, dir_fd=parent)
        opened = os.fstat(descriptor)
        require(
            identity_tuple(before) == identity_tuple(opened)
            and stat.S_ISREG(opened.st_mode)
            and not is_reparse(opened)
            and is_closed_metadata(opened)
            and opened.st_size <= MAX_FILE_BYTES,
            "frontend_contract",
        )
        chunks: list[bytes] = []
        remaining = opened.st_size
        while remaining:
            chunk = os.read(descriptor, min(1024 * 1024, remaining))
            require(chunk != b"", "frontend_contract")
            chunks.append(chunk)
            remaining -= len(chunk)
        require(identity_tuple(opened) == identity_tuple(os.fstat(descriptor)), "file_identity")
        current = os.stat(name, dir_fd=parent, follow_symlinks=False)
        require(identity_tuple(opened) == identity_tuple(current), "file_identity")
    except ScanFailure:
        raise
    except OSError as error:
        raise ScanFailure("frontend_contract") from error
    finally:
        if "descriptor" in locals():
            os.close(descriptor)
    return b"".join(chunks)


def scan_frontend_tree(
    root: pathlib.Path,
    raw: Any,
    start: dt.datetime,
    end: dt.datetime,
    deployment_sha: str,
) -> tuple[int, int, int]:
    require(isinstance(raw, dict), "manifest_contract")
    strict_keys(
        raw,
        {
            "role", "path", "tree_sha256", "file_count", "text_file_count",
            "captured_at_utc", "deployment_sha",
        },
        "manifest_contract",
    )
    require(raw["role"] == "deployed_frontend_root", "manifest_contract")
    captured = parse_utc(raw["captured_at_utc"])
    require(start <= captured <= end, "window_binding")
    require(raw["deployment_sha"] == deployment_sha, "deployment_binding")
    require(SHA256_RE.fullmatch(raw["tree_sha256"] or "") is not None, "hash_contract")
    require(type(raw["file_count"]) is int and 1 <= raw["file_count"] <= MAX_FRONTEND_FILES, "manifest_contract")
    require(type(raw["text_file_count"]) is int and 1 <= raw["text_file_count"] <= raw["file_count"], "manifest_contract")
    tree_parts = parse_safe_relative(raw["path"])
    require(platform_supports_secure_openat(), "platform_not_supported")
    root_identity = bundle_identity(root)
    text_count = 0
    findings = 0
    bytes_scanned = 0
    file_count = 0
    digest = hashlib.sha256()
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    opened_chain: list[tuple[int, str, int, os.stat_result]] = []
    try:
        root_descriptor = os.open(root, directory_flags)
        root_metadata = os.fstat(root_descriptor)
        require(
            stat.S_ISDIR(root_metadata.st_mode)
            and is_closed_metadata(root_metadata)
            and not is_reparse(root_metadata),
            "bundle_not_closed",
        )
        current_descriptor = root_descriptor
        for part in tree_parts:
            child_descriptor, child_metadata = open_closed_directory_at(current_descriptor, part)
            opened_chain.append((current_descriptor, part, child_descriptor, child_metadata))
            current_descriptor = child_descriptor

        def visit(directory: int, relative_directory: tuple[str, ...]) -> None:
            nonlocal text_count, findings, bytes_scanned, file_count
            try:
                with os.scandir(directory) as iterator:
                    entries = sorted(iterator, key=lambda item: item.name)
            except OSError as error:
                raise ScanFailure("frontend_contract") from error
            for entry in entries:
                parts = parse_safe_relative(entry.name)
                require(len(parts) == 1, "frontend_contract")
                try:
                    metadata = entry.stat(follow_symlinks=False)
                except OSError as error:
                    raise ScanFailure("frontend_contract") from error
                require(not is_reparse(metadata) and is_closed_metadata(metadata), "frontend_contract")
                relative_parts = relative_directory + (entry.name,)
                if stat.S_ISDIR(metadata.st_mode):
                    child, opened = open_closed_directory_at(directory, entry.name)
                    try:
                        visit(child, relative_parts)
                        current = os.stat(entry.name, dir_fd=directory, follow_symlinks=False)
                        require(identity_tuple(opened) == identity_tuple(current), "file_identity")
                    finally:
                        os.close(child)
                    continue
                require(stat.S_ISREG(metadata.st_mode), "frontend_contract")
                data = read_frontend_file_at(directory, entry.name, metadata)
                file_count += 1
                require(file_count <= MAX_FRONTEND_FILES, "size_limit")
                bytes_scanned += len(data)
                require(bytes_scanned <= MAX_FRONTEND_BYTES, "size_limit")
                relative = pathlib.PurePosixPath(*relative_parts).as_posix()
                update_frontend_tree_digest(digest, relative, data)
                if pathlib.PurePosixPath(relative).suffix.lower() in TEXT_FRONTEND_SUFFIXES:
                    text_count += 1
                    if contains_sensitive(data, "frontend_artifacts"):
                        findings += 1

        visit(current_descriptor, ())
        for parent, name, _child, opened in opened_chain:
            current = os.stat(name, dir_fd=parent, follow_symlinks=False)
            require(identity_tuple(opened) == identity_tuple(current), "file_identity")
    except ScanFailure:
        raise
    except OSError as error:
        raise ScanFailure("frontend_contract") from error
    finally:
        for _parent, _name, child, _metadata in reversed(opened_chain):
            os.close(child)
        if "root_descriptor" in locals():
            os.close(root_descriptor)
    require(file_count == raw["file_count"], "frontend_contract")
    require(text_count == raw["text_file_count"], "frontend_contract")
    require(digest.hexdigest() == raw["tree_sha256"], "hash_contract")
    require(bundle_identity(root) == root_identity, "bundle_identity")
    return file_count, bytes_scanned, findings


def scan_bundle(contract: CliContract) -> ScanResult:
    require(platform_supports_secure_openat(), "platform_not_supported")
    manifest_parts = parse_safe_relative(contract.manifest_relative)
    require(len(manifest_parts) >= 2, "path_contract")
    root = pathlib.Path.cwd().joinpath(*manifest_parts[:-1])
    require(bundle_identity(root) == contract.bundle_id, "bundle_identity")
    manifest_data = read_regular_file(
        root, (manifest_parts[-1],), contract.manifest_sha256, MAX_MANIFEST_BYTES
    )
    manifest = parse_json_bytes(manifest_data, "manifest_contract")
    require(isinstance(manifest, dict), "manifest_contract")
    strict_keys(
        manifest,
        {"schema", "collector", "window", "deployment_sha", "surfaces"},
        "manifest_contract",
    )
    require(manifest["schema"] == SCHEMA, "manifest_contract")
    collector = manifest["collector"]
    require(isinstance(collector, dict), "collector_contract")
    strict_keys(
        collector,
        {"mode", "bundle_id", "bundle_closed", "stdout_lines", "stderr_bytes"},
        "collector_contract",
    )
    require(
        collector["mode"] == COLLECTOR_MODE
        and collector["bundle_id"] == contract.bundle_id
        and collector["bundle_closed"] is True
        and collector["stdout_lines"] == 1
        and collector["stderr_bytes"] == 0,
        "collector_contract",
    )
    deployment_sha = manifest["deployment_sha"]
    require(isinstance(deployment_sha, str) and DEPLOYMENT_SHA_RE.fullmatch(deployment_sha) is not None, "deployment_binding")
    require(deployment_sha == contract.deployment_sha, "deployment_binding")
    window = manifest["window"]
    require(isinstance(window, dict), "manifest_contract")
    strict_keys(window, {"start_utc", "end_utc"}, "manifest_contract")
    start = parse_utc(window["start_utc"])
    end = parse_utc(window["end_utc"])
    require(start <= end and (end - start).total_seconds() <= MAX_WINDOW_SECONDS, "time_contract")
    surfaces = manifest["surfaces"]
    require(isinstance(surfaces, dict) and set(surfaces) == set(SURFACES), "surface_contract")

    files_scanned = 0
    bytes_scanned = 0
    findings = 0
    surfaces_passed = 0
    for surface in FILE_SURFACES:
        entries = surfaces[surface]
        require(isinstance(entries, list) and 1 <= len(entries) <= 256, "surface_contract")
        roles: list[str] = []
        surface_bytes = 0
        for entry in entries:
            role, data = validate_entry(root, surface, entry, start, end, deployment_sha)
            roles.append(role)
            files_scanned += 1
            bytes_scanned += len(data)
            surface_bytes += len(data)
            require(surface_bytes <= MAX_SURFACE_BYTES and bytes_scanned <= MAX_BUNDLE_BYTES, "size_limit")
            if contains_sensitive(data, surface):
                findings += 1
        require(len(set(roles)) == len(roles), "surface_contract")
        if surface == "http_responses":
            require(set(roles) == HTTP_ROLES, "surface_contract")
        surfaces_passed += 1

    frontend = surfaces["frontend_artifacts"]
    frontend_files, frontend_bytes, frontend_findings = scan_frontend_tree(
        root, frontend, start, end, deployment_sha
    )
    files_scanned += frontend_files
    bytes_scanned += frontend_bytes
    require(bytes_scanned <= MAX_BUNDLE_BYTES, "size_limit")
    findings += frontend_findings
    surfaces_passed += 1
    if findings:
        require(bundle_identity(root) == contract.bundle_id, "bundle_identity")
        return ScanResult(
            "failed", "sensitive_finding", surfaces_passed, files_scanned,
            bytes_scanned, findings, True, True,
        )
    require(bundle_identity(root) == contract.bundle_id, "bundle_identity")
    return ScanResult("pass", "complete", surfaces_passed, files_scanned, bytes_scanned, 0, True, True)


def parse_args(argv: list[str] | None) -> CliContract:
    """使用固定参数契约，并在任何文件系统调用前完成词法和摘要门禁。"""
    values = list(sys.argv[1:] if argv is None else argv)
    require(
        len(values) == 10
        and values[0] == "--manifest"
        and values[2] == "--manifest-sha256"
        and values[4] == "--deployment-sha"
        and values[6] == "--bundle-id"
        and values[8] == "--collector-mode",
        "argument_contract",
    )
    parse_safe_relative(values[1])
    require(SHA256_RE.fullmatch(values[3]) is not None, "argument_contract")
    require(DEPLOYMENT_SHA_RE.fullmatch(values[5]) is not None, "argument_contract")
    require(SHA256_RE.fullmatch(values[7]) is not None, "argument_contract")
    require(values[9] == COLLECTOR_MODE, "argument_contract")
    return CliContract(values[1], values[3], values[5], values[7])


def execute(argv: list[str] | None = None) -> tuple[int, str]:
    """捕获内部所有输出；出现额外 stdout/stderr 时按失败关闭处理。"""
    captured_stdout = io.StringIO()
    captured_stderr = io.StringIO()
    try:
        with contextlib.redirect_stdout(captured_stdout), contextlib.redirect_stderr(captured_stderr):
            contract = parse_args(argv)
            result = scan_bundle(contract)
        require(captured_stdout.getvalue() == "" and captured_stderr.getvalue() == "", "unexpected_output")
        return (0 if result.status == "pass" else 2), result.line()
    except ScanFailure as error:
        result = ScanResult("failed", error.classification, 0, 0, 0, 0, False, False)
        return 2, result.line()
    except BaseException:
        result = ScanResult("failed", "internal_error", 0, 0, 0, 0, False, False)
        return 2, result.line()


def main(argv: list[str] | None = None) -> int:
    code, line = execute(argv)
    sys.stdout.write(line + "\n")
    return code


if __name__ == "__main__":
    raise SystemExit(main())

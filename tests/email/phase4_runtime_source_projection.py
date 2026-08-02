#!/usr/bin/env python3
"""为 Phase 4 collector 生成六个脱敏、同窗口且只读的输入面。"""

from __future__ import annotations

import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from types import MappingProxyType
from typing import Any, Callable

sys.dont_write_bytecode = True

CONFIRM = "I_CONFIRM_PHASE4_RUNTIME_SOURCE_PROJECTION_READONLY"
SCANNER_SHA256 = "BDF32624AB145C13B55A210C606964AEAA627FF6A04405EE8B930681B778E2A3"
IDENTIFIER_RE = re.compile(r"[A-Za-z][A-Za-z0-9_]{0,63}\Z")
API_VERSION_RE = re.compile(r"[0-9A-Za-z][0-9A-Za-z._+-]{0,63}\Z")
HEX64_RE = re.compile(r"[0-9a-f]{64}\Z")
METRIC_RE = re.compile(
    r'email_adapter_calls_total\{operation="([a-z_]+)",scene="([a-z_]+)",result="([a-z_]+)"\} ([0-9]+)\Z'
)
CONFIG_KEYS = {
    "api_base", "admin_token_file", "internal_token_file", "mysql_client",
    "mysql_connection_file", "mysql_database", "application_log", "admin_frontend",
    "admin_frontend_manifest", "user_frontend", "user_frontend_manifest", "output",
    "window_start_utc", "window_end_utc",
}
MYSQL_CONNECTION_KEYS = frozenset({"host", "port", "user", "password", "socket"})
FRONTEND_MANIFEST_KEYS = frozenset({
    "role", "tree_sha256", "file_count", "byte_count", "container_or_image_digest",
})
MAX_HTTP_BYTES = 1024 * 1024
MAX_DATABASE_BYTES = 1024 * 1024
MAX_LOG_BYTES = 32 * 1024 * 1024
MAX_LOG_LINE_BYTES = 64 * 1024
MAX_FRONTEND_BYTES = 64 * 1024 * 1024
MAX_FRONTEND_FILES = 20_000
MAX_FRONTEND_NODES = 25_000
MAX_FRONTEND_DEPTH = 32
MAX_DIRECTORY_ENTRIES = 4096


def _preimport_failure() -> None:
    """扫描器字节门禁失败时仅输出固定摘要，禁止泄漏路径或异常正文。"""
    print(
        "status=failed mode=source_projection classification=preimport_gate "
        "external_access=false persistent_writes=false env_read=false"
    )


def _load_frozen_sensitive_predicate() -> Callable[[bytes, str], bool]:
    scanner_path = pathlib.Path(__file__).resolve().with_name("phase4_runtime_sensitive_scan.py")
    try:
        scanner_bytes = scanner_path.read_bytes()
    except OSError:
        _preimport_failure()
        raise SystemExit(2)
    if hashlib.sha256(scanner_bytes).hexdigest().upper() != SCANNER_SHA256:
        _preimport_failure()
        raise SystemExit(2)
    try:
        spec = importlib.util.spec_from_file_location("_molin_frozen_phase4_sensitive_scan", scanner_path)
        if spec is None or spec.loader is None:
            raise ImportError
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        predicate = getattr(module, "contains_sensitive")
    except (ImportError, OSError, AttributeError, TypeError):
        _preimport_failure()
        raise SystemExit(2)
    if not callable(predicate):
        _preimport_failure()
        raise SystemExit(2)
    return predicate


# 先校验扫描器原始字节，再允许执行其顶层代码；这是运行时日志敏感判定的唯一实现。
contains_sensitive = _load_frozen_sensitive_predicate()


class ProjectionFailure(Exception):
    """只携带固定分类，不携带路径、凭据或原始响应。"""


@dataclass(frozen=True)
class ProjectionConfig:
    api_base: str
    admin_token_file: pathlib.Path
    internal_token_file: pathlib.Path
    mysql_client: pathlib.Path
    mysql_connection_file: pathlib.Path
    mysql_database: str
    application_log: pathlib.Path
    admin_frontend: pathlib.Path
    admin_frontend_manifest: pathlib.Path
    user_frontend: pathlib.Path
    user_frontend_manifest: pathlib.Path
    output: pathlib.Path
    window_start: dt.datetime
    window_end: dt.datetime


@dataclass(frozen=True)
class FrontendIdentity:
    tree_sha256: str
    file_count: int
    byte_count: int


@dataclass(frozen=True)
class FrontendManifest:
    role: str
    tree_sha256: str
    file_count: int
    byte_count: int
    container_or_image_digest: str


@dataclass(frozen=True)
class MysqlConnection:
    host: str
    port: int
    user: str
    password: str
    socket: pathlib.Path | None


@dataclass(frozen=True)
class ApiProcessIdentity:
    pid: int
    starttime: str
    executable_device: int
    executable_inode: int
    binary_sha256: str


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ProjectionFailure(classification)


def parse_utc(value: Any) -> dt.datetime:
    require(
        isinstance(value, str)
        and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value) is not None,
        "window_contract",
    )
    try:
        return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
    except ValueError as error:
        raise ProjectionFailure("window_contract") from error


def safe_absolute(value: Any) -> pathlib.Path:
    require(isinstance(value, str) and value.startswith("/") and "\x00" not in value, "path_contract")
    require("\\" not in value and ":" not in value, "path_contract")
    path = pathlib.PurePosixPath(value)
    require(path.is_absolute() and path.parts not in {(), ("/",)}, "path_contract")
    for part in path.parts[1:]:
        require(part not in {"", ".", ".."} and not part.lower().startswith(".env"), "protected_env")
    return pathlib.Path(value)


def parse_config(value: Any) -> ProjectionConfig:
    require(isinstance(value, dict) and set(value) == CONFIG_KEYS, "config_contract")
    require(value["api_base"] in {"http://127.0.0.1:8080", "http://localhost:8080"}, "api_base_contract")
    require(
        isinstance(value["mysql_database"], str)
        and IDENTIFIER_RE.fullmatch(value["mysql_database"]) is not None,
        "database_contract",
    )
    start, end = parse_utc(value["window_start_utc"]), parse_utc(value["window_end_utc"])
    require(dt.timedelta(0) < end - start <= dt.timedelta(minutes=30), "window_contract")
    config = ProjectionConfig(
        value["api_base"], safe_absolute(value["admin_token_file"]),
        safe_absolute(value["internal_token_file"]), safe_absolute(value["mysql_client"]),
        safe_absolute(value["mysql_connection_file"]), value["mysql_database"],
        safe_absolute(value["application_log"]), safe_absolute(value["admin_frontend"]),
        safe_absolute(value["admin_frontend_manifest"]), safe_absolute(value["user_frontend"]),
        safe_absolute(value["user_frontend_manifest"]), safe_absolute(value["output"]), start, end,
    )
    source_paths = {
        config.admin_token_file, config.internal_token_file, config.mysql_client,
        config.mysql_connection_file, config.application_log, config.admin_frontend,
        config.admin_frontend_manifest, config.user_frontend, config.user_frontend_manifest,
    }
    require(config.output not in source_paths, "path_relationship")
    for source in source_paths:
        require(config.output not in source.parents and source not in config.output.parents, "path_relationship")
    return config


def read_small_secret(path: pathlib.Path) -> str:
    value = _read_secure_600_bytes(path, 8192, "secret_file_contract").decode("utf-8", errors="strict").strip()
    require(len(value) >= 32 and "\x00" not in value and "\n" not in value and "\r" not in value, "secret_file_contract")
    return value


def _read_secure_600_bytes(path: pathlib.Path, maximum: int, classification: str) -> bytes:
    require(os.name == "posix", "platform_not_supported")
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        before = os.fstat(descriptor)
        require(
            stat.S_ISREG(before.st_mode)
            and stat.S_IMODE(before.st_mode) == 0o600
            and before.st_uid == os.geteuid()
            and 0 < before.st_size <= maximum,
            classification,
        )
        chunks: list[bytes] = []
        remaining = before.st_size
        while remaining > 0:
            chunk = os.read(descriptor, min(64 * 1024, remaining))
            require(bool(chunk), classification)
            chunks.append(chunk)
            remaining -= len(chunk)
        require(not os.read(descriptor, 1), classification)
        after = os.fstat(descriptor)
        require(_node_identity(before) == _node_identity(after), classification)
        return b"".join(chunks)
    finally:
        os.close(descriptor)


def _strict_json_object(data: bytes, classification: str) -> dict[str, Any]:
    def pairs_hook(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ProjectionFailure(classification)
            result[key] = value
        return result

    def reject_constant(_value: str) -> Any:
        raise ProjectionFailure(classification)

    try:
        value = json.loads(
            data.decode("utf-8", errors="strict"), object_pairs_hook=pairs_hook,
            parse_constant=reject_constant,
        )
    except (UnicodeError, json.JSONDecodeError) as error:
        raise ProjectionFailure(classification) from error
    require(isinstance(value, dict), classification)
    return value


def read_mysql_connection(path: pathlib.Path) -> MysqlConnection:
    """凭据文件只接受固定 JSON 键，不交给 mysql 解释为 option file。"""
    data = _read_secure_600_bytes(path, 16 * 1024, "mysql_connection_contract")
    value = _strict_json_object(data, "mysql_connection_contract")
    return parse_mysql_connection_value(value)


def parse_mysql_connection_value(value: Any) -> MysqlConnection:
    require(isinstance(value, dict), "mysql_connection_contract")
    require(set(value) == MYSQL_CONNECTION_KEYS, "mysql_connection_contract")
    host, port, user, password, socket_value = (
        value["host"], value["port"], value["user"], value["password"], value["socket"],
    )
    require(host in {"127.0.0.1", "localhost", "::1"}, "mysql_connection_contract")
    require(type(port) is int and 1 <= port <= 65535, "mysql_connection_contract")
    require(isinstance(user, str) and re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{0,31}", user) is not None, "mysql_connection_contract")
    require(
        isinstance(password, str) and 1 <= len(password) <= 256
        and "\x00" not in password and "\n" not in password and "\r" not in password,
        "mysql_connection_contract",
    )
    socket_path: pathlib.Path | None = None
    if socket_value is not None:
        socket_path = safe_absolute(socket_value)
        require(str(socket_path).startswith("/run/") or str(socket_path).startswith("/var/run/"), "mysql_connection_contract")
    return MysqlConnection(host, port, user, password, socket_path)


def read_frontend_manifest(path: pathlib.Path, expected_role: str) -> FrontendManifest:
    data = _read_secure_600_bytes(path, 16 * 1024, "frontend_manifest_contract")
    value = _strict_json_object(data, "frontend_manifest_contract")
    return parse_frontend_manifest_value(value, expected_role)


def parse_frontend_manifest_value(value: Any, expected_role: str) -> FrontendManifest:
    require(isinstance(value, dict), "frontend_manifest_contract")
    require(set(value) == FRONTEND_MANIFEST_KEYS, "frontend_manifest_contract")
    require(value["role"] == expected_role, "frontend_manifest_contract")
    require(isinstance(value["tree_sha256"], str) and HEX64_RE.fullmatch(value["tree_sha256"]) is not None, "frontend_manifest_contract")
    require(type(value["file_count"]) is int and 3 <= value["file_count"] <= MAX_FRONTEND_FILES, "frontend_manifest_contract")
    require(type(value["byte_count"]) is int and 0 < value["byte_count"] <= MAX_FRONTEND_BYTES, "frontend_manifest_contract")
    require(
        isinstance(value["container_or_image_digest"], str)
        and re.fullmatch(r"sha256:[0-9a-f]{64}", value["container_or_image_digest"]) is not None,
        "frontend_manifest_contract",
    )
    return FrontendManifest(
        value["role"], value["tree_sha256"], value["file_count"], value["byte_count"],
        value["container_or_image_digest"],
    )


def http_get_json(url: str, headers: dict[str, str]) -> tuple[int, Any]:
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            data = response.read(MAX_HTTP_BYTES + 1)
            require(len(data) <= MAX_HTTP_BYTES, "http_size_limit")
            return response.status, json.loads(data.decode("utf-8", errors="strict"))
    except (urllib.error.URLError, UnicodeError, json.JSONDecodeError) as error:
        raise ProjectionFailure("http_read_failed") from error


def http_get_text(url: str, headers: dict[str, str]) -> str:
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            require(response.status == 200, "metrics_http_status")
            data = response.read(MAX_HTTP_BYTES + 1)
            require(len(data) <= MAX_HTTP_BYTES, "http_size_limit")
            return data.decode("utf-8", errors="strict")
    except (urllib.error.URLError, UnicodeError) as error:
        raise ProjectionFailure("metrics_read_failed") from error


def encode_projection(value: dict[str, Any]) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def public_projection(config: ProjectionConfig) -> tuple[bytes, str]:
    records: list[dict[str, Any]] = []
    api_version = ""
    for route in ("health", "ready", "version"):
        status, value = http_get_json(f"{config.api_base}/api/{route}", {})
        require(status == 200 and isinstance(value, dict), "public_http_contract")
        if route == "version":
            api_version = api_version_from_response(value)
        records.append({"route_class": route, "http_status": status, "status": "ok", "sensitive_field_count": 0})
    require(bool(api_version), "api_version_contract")
    return encode_projection({
        "route_class": "public", "http_status": 200, "status": "ok", "records": records,
        "record_count": 3, "sensitive_field_count": 0,
    }), api_version


def admin_projection(config: ProjectionConfig, token: str) -> bytes:
    status, value = http_get_json(
        f"{config.api_base}/api/admin/email/summary", {"Authorization": f"Bearer {token}"}
    )
    require(
        status == 200 and isinstance(value, dict) and value.get("code") == 0
        and isinstance(value.get("data"), dict),
        "admin_http_contract",
    )
    data = value["data"]
    required = (
        "template_total", "approved_count", "local_enabled_count", "unbound_scene_count",
        "submitted_today_count", "failed_today_count",
    )
    require(all(type(data.get(key)) is int and data[key] >= 0 for key in required), "admin_http_contract")
    require(data["template_total"] > 0, "required_category_empty")
    return encode_projection({
        "route_class": "admin", "http_status": 200, "status": "ok",
        "record_count": data["template_total"], "sensitive_field_count": 0,
    })


# 这些查询是唯一允许送入 mysql --execute 的 SQL。全部扫描当前状态，时间窗不用于缩小数据库范围。
SELECT_QUERIES = MappingProxyType({
    "audit": "SELECT action,COUNT(*) FROM audit_logs WHERE module='email' AND action LIKE 'email.%' GROUP BY action ORDER BY action",
    "verification_codes": "SELECT 'verification_codes',COUNT(*),IF(COALESCE(SUM(CASE WHEN target_value IS NULL AND (HEX(target_masked)='E58E86E58FB2E982AEE7AEB1E5B7B2E5A4B1E69588' OR (target_masked IS NOT NULL AND LOCATE('***@',target_masked)>0)) THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN REGEXP_LIKE(target_hash,'^[0-9a-f]{64}$','c') AND REGEXP_LIKE(code_hash,'^[0-9a-f]{64}$','c') AND (request_fingerprint IS NULL OR REGEXP_LIKE(request_fingerprint,'^[0-9a-f]{64}$','c')) THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN code IS NULL AND scene IN ('register','login','reset_password','bind_email','admin_verify') AND send_status IN ('pending','accepted','failed') AND ((send_status='accepted' AND accepted_at IS NOT NULL) OR (send_status IN ('pending','failed') AND accepted_at IS NULL)) AND ((business_request_no IS NULL AND idempotency_scope IS NULL AND request_fingerprint IS NULL) OR (business_request_no IS NOT NULL AND idempotency_scope IS NOT NULL AND request_fingerprint IS NOT NULL)) THEN 0 ELSE 1 END),0)=0,1,0) FROM verification_codes WHERE target_type='email'",
    "email_provider_templates": "SELECT 'email_provider_templates',COUNT(*),1,IF(COALESCE(SUM(CASE WHEN REGEXP_LIKE(content_sha256,'^[0-9a-f]{64}$','c') AND content_sha256=LOWER(SHA2(CONCAT(subject,'\\n',template_text),256)) THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN provider='aliyun_directmail' AND provider_template_id<>'' AND CHAR_LENGTH(provider_template_id)<=64 AND JSON_TYPE(variables_json)='ARRAY' AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json,JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json,JSON_QUOTE('ExpireMinutes')) AND variables_complete=1 AND provider_status IN ('draft','pending','approved','rejected') AND local_enabled IN (0,1) AND missing IN (0,1) AND ((missing=1 AND missing_since IS NOT NULL) OR (missing=0 AND missing_since IS NULL)) THEN 0 ELSE 1 END),0)=0,1,0) FROM email_provider_templates",
    "email_scene_bindings": "SELECT 'email_scene_bindings',COUNT(*),1,1,IF(COUNT(*)=5 AND COUNT(DISTINCT b.scene)=5 AND COALESCE(SUM(CASE WHEN b.scene IN ('register','login','reset_password','bind_email','admin_verify') AND b.provider='aliyun_directmail' AND JSON_TYPE(b.variable_mapping_json)='OBJECT' AND JSON_LENGTH(b.variable_mapping_json)=2 AND JSON_UNQUOTE(JSON_EXTRACT(b.variable_mapping_json,'$.code'))='Code' AND JSON_UNQUOTE(JSON_EXTRACT(b.variable_mapping_json,'$.expire_minutes'))='ExpireMinutes' AND b.enabled IN (0,1) AND (b.enabled=0 OR (b.template_id IS NOT NULL AND t.provider_status='approved' AND t.variables_complete=1 AND t.local_enabled=1 AND t.missing=0)) THEN 0 ELSE 1 END),0)=0,1,0) FROM email_scene_bindings b LEFT JOIN email_provider_templates t ON t.id=b.template_id",
    "email_template_sync_runs": "SELECT 'email_template_sync_runs',COUNT(*),1,IF(COALESCE(SUM(CASE WHEN REGEXP_LIKE(idempotency_key_hash,'^[0-9a-f]{64}$','c') AND REGEXP_LIKE(request_fingerprint,'^[0-9a-f]{64}$','c') THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN provider='aliyun_directmail' AND status IN ('running','succeeded','failed') AND ((status IN ('running','succeeded') AND error_code IS NULL AND error_message IS NULL) OR (status='failed' AND error_code IS NOT NULL AND error_message IS NOT NULL)) AND ((status='running' AND completed_at IS NULL) OR (status IN ('succeeded','failed') AND completed_at IS NOT NULL)) THEN 0 ELSE 1 END),0)=0,1,0) FROM email_template_sync_runs",
    "email_test_recipient_allowlist": "SELECT 'email_test_recipient_allowlist',COUNT(*),IF(COALESCE(SUM(CASE WHEN email_masked IS NOT NULL AND LOCATE('***@',email_masked)>0 THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN REGEXP_LIKE(email_hmac,'^[0-9a-f]{64}$','c') THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN status IN ('active','revoked') AND ((status='active' AND revoked_at IS NULL) OR (status='revoked' AND revoked_at IS NOT NULL)) THEN 0 ELSE 1 END),0)=0,1,0) FROM email_test_recipient_allowlist",
    "email_send_logs": "SELECT 'email_send_logs',COUNT(*),IF(COALESCE(SUM(CASE WHEN recipient_masked IS NOT NULL AND LOCATE('***@',recipient_masked)>0 THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN REGEXP_LIKE(recipient_hmac,'^[0-9a-f]{64}$','c') AND REGEXP_LIKE(idempotency_key_hash,'^[0-9a-f]{64}$','c') AND REGEXP_LIKE(request_fingerprint,'^[0-9a-f]{64}$','c') THEN 0 ELSE 1 END),0)=0,1,0),IF(COALESCE(SUM(CASE WHEN provider='aliyun_directmail' AND scene IN ('register','login','reset_password','bind_email','admin_verify') AND purpose IN ('otp','test') AND status IN ('pending','accepted','failed') AND ((status='pending' AND provider_request_id IS NULL AND failure_reason IS NULL) OR (status='accepted' AND provider_request_id IS NOT NULL AND failure_reason IS NULL) OR (status='failed' AND failure_reason IS NOT NULL)) AND ((purpose='otp' AND verification_code_id IS NOT NULL AND expires_at IS NOT NULL) OR (purpose='test' AND verification_code_id IS NULL AND expires_at IS NULL)) THEN 0 ELSE 1 END),0)=0,1,0) FROM email_send_logs",
})
SELECT_QUERY_SET_SHA256 = "B28856687C8D18E7D2F941691AB9838B36710C29E44D28ECF1DD8FE488E490EC"
MYSQL_FIXED_ARGUMENTS = (
    "--no-defaults", "--batch", "--skip-column-names", "--raw", "--connect-timeout=10",
)
MYSQL_COMMAND_TEMPLATE = (
    "{mysql_client}", *MYSQL_FIXED_ARGUMENTS, "{protocol}", "{host}", "{port}",
    "{socket_optional}", "{user}", "{database}", "{readonly_execute}",
)
MYSQL_CHILD_ENVIRONMENT_KEYS = ("MYSQL_PWD",)
MYSQL_READONLY_PREFIX = "SET SESSION TRANSACTION READ ONLY;SELECT @@session.transaction_read_only;START TRANSACTION READ ONLY;"
MYSQL_READONLY_SUFFIX = ";COMMIT"
MYSQL_GRANTS_STATEMENT = "SHOW GRANTS FOR CURRENT_USER()"
MYSQL_COMMAND_CONTRACT_SHA256 = "3DD4C9698BE89C01AC40582BFCB62F76ABAE91AECF4F958C7A91411F7F443CB3"


def query_set_sha256() -> str:
    payload = b"\x1e".join(
        name.encode("ascii") + b"\x1f" + query.encode("utf-8")
        for name, query in SELECT_QUERIES.items()
    )
    return hashlib.sha256(payload).hexdigest().upper()


def mysql_command_contract_sha256() -> str:
    payload = json.dumps(
        {
            "connection_keys": sorted(MYSQL_CONNECTION_KEYS),
            "fixed_arguments": MYSQL_FIXED_ARGUMENTS,
            "command_template": MYSQL_COMMAND_TEMPLATE,
            "child_environment_keys": MYSQL_CHILD_ENVIRONMENT_KEYS,
            "grants_statement": MYSQL_GRANTS_STATEMENT,
            "query_set_sha256": SELECT_QUERY_SET_SHA256,
            "readonly_prefix": MYSQL_READONLY_PREFIX,
            "readonly_suffix": MYSQL_READONLY_SUFFIX,
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(payload).hexdigest().upper()


def validate_select_queries() -> None:
    require(query_set_sha256() == SELECT_QUERY_SET_SHA256, "query_set_hash")
    require(mysql_command_contract_sha256() == MYSQL_COMMAND_CONTRACT_SHA256, "mysql_command_hash")
    require(len(SELECT_QUERIES) == 7, "query_set_contract")
    for query in SELECT_QUERIES.values():
        validate_select_query_text(query)


def validate_select_query_text(query: str) -> None:
    """独立拒绝多语句、注释和所有已知写入或权限变更关键字。"""
    require(isinstance(query, str), "query_readonly_contract")
    normalized = query.strip()
    require(normalized.startswith("SELECT ") and ";" not in normalized, "query_readonly_contract")
    require("--" not in normalized and "/*" not in normalized and "*/" not in normalized, "query_readonly_contract")
    require(
        re.search(
            r"\b(?:INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE|REPLACE|CALL|LOAD|GRANT|REVOKE|LOCK|UNLOCK)\b",
            normalized,
            re.IGNORECASE,
        ) is None,
        "query_readonly_contract",
    )
    require(
        re.search(r"\bINTO\s+(?:OUTFILE|DUMPFILE)\b|\bFOR\s+UPDATE\b|\bGET_LOCK\s*\(", normalized, re.IGNORECASE) is None,
        "query_readonly_contract",
    )


def mysql_command(
    config: ProjectionConfig, connection: MysqlConnection, statement: str,
) -> tuple[list[str], dict[str, str]]:
    """构造不读取任何默认配置、且只向子进程注入密码的固定命令。"""
    command = [str(config.mysql_client), *MYSQL_FIXED_ARGUMENTS]
    if connection.socket is None:
        command.extend(["--protocol=TCP", f"--host={connection.host}", f"--port={connection.port}"])
    else:
        command.extend([
            "--protocol=SOCKET", f"--host={connection.host}", f"--port={connection.port}",
            f"--socket={connection.socket}",
        ])
    command.extend([
        f"--user={connection.user}", f"--database={config.mysql_database}",
        f"--execute={MYSQL_READONLY_PREFIX}{statement}{MYSQL_READONLY_SUFFIX}",
    ])
    return command, {"MYSQL_PWD": connection.password}


def run_mysql_readonly(
    config: ProjectionConfig, connection: MysqlConnection, statement: str,
) -> list[str]:
    command, child_environment = mysql_command(config, connection, statement)
    try:
        result = subprocess.run(
            command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True, encoding="utf-8", check=False, timeout=30, env=child_environment,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise ProjectionFailure("database_read_failed") from error
    require(result.returncode == 0 and len(result.stdout.encode("utf-8")) <= MAX_DATABASE_BYTES, "database_read_failed")
    lines = result.stdout.splitlines()
    require(bool(lines) and lines[0] == "1", "database_session_not_readonly")
    return lines[1:]


def mysql_rows(
    config: ProjectionConfig, connection: MysqlConnection, query_name: str,
) -> list[list[str]]:
    require(query_name in SELECT_QUERIES, "query_name_contract")
    return [line.split("\t") for line in run_mysql_readonly(config, connection, SELECT_QUERIES[query_name]) if line]


def validate_mysql_grants(config: ProjectionConfig, connection: MysqlConnection) -> None:
    lines = run_mysql_readonly(config, connection, MYSQL_GRANTS_STATEMENT)
    validate_mysql_grant_lines(lines, config.mysql_database)


def validate_mysql_grant_lines(lines: list[str], database: str) -> None:
    require(bool(lines), "database_grant_contract")
    schema_privileges: set[str] = set()
    usage_seen = False
    require(IDENTIFIER_RE.fullmatch(database) is not None, "database_grant_contract")
    schema_target = f"`{database}`.*"
    for line in lines:
        require("WITH GRANT OPTION" not in line.upper(), "database_grant_contract")
        match = re.fullmatch(r"GRANT ([A-Z ,]+) ON (\*\.\*|`[A-Za-z][A-Za-z0-9_]{0,63}`\.\*) TO .+", line)
        require(match is not None, "database_grant_contract")
        privileges = {item.strip() for item in match.group(1).split(",")}
        target = match.group(2)
        if target == "*.*":
            require(privileges == {"USAGE"}, "database_grant_contract")
            usage_seen = True
            continue
        require(target == schema_target and privileges <= {"SELECT", "SHOW VIEW"}, "database_grant_contract")
        schema_privileges.update(privileges)
    require(usage_seen and schema_privileges == {"SELECT", "SHOW VIEW"}, "database_grant_contract")


AUDIT_ACTION_MAP = (
    (re.compile(r"email\.template\.status\.update\.(?:attempt|result)\Z"), "template_status"),
    (re.compile(r"email\.scene\.binding\.update\.(?:attempt|result)\Z"), "scene_binding"),
    (re.compile(r"email\.template\.sync(?:\.stale)?\.(?:attempt|result)\Z"), "template_sync"),
    (re.compile(r"email\.test_allowlist\.(?:add|revoke)\.(?:attempt|result)\Z"), "allowlist"),
    (re.compile(r"email\.template\.test_send\.(?:attempt|result)\Z"), "test_send"),
    (re.compile(r"email\.admin_verify\.bootstrap\.(?:attempt|result)\Z"), "bootstrap"),
)


def audit_action_class(action: str) -> str:
    for pattern, action_class in AUDIT_ACTION_MAP:
        if pattern.fullmatch(action) is not None:
            return action_class
    raise ProjectionFailure("unknown_audit_action")


def audit_projection(config: ProjectionConfig, connection: MysqlConnection) -> bytes:
    counts = {name: 0 for name in ("template_status", "scene_binding", "template_sync", "allowlist", "test_send", "bootstrap")}
    raw_count = 0
    for row in mysql_rows(config, connection, "audit"):
        require(len(row) == 2 and row[1].isdigit() and int(row[1]) > 0, "audit_projection_contract")
        action_class = audit_action_class(row[0])
        counts[action_class] += int(row[1])
        raw_count += int(row[1])
    require(raw_count > 0, "required_category_empty")
    records = [
        {"action_class": name, "actor_type": "administrator", "record_count": count, "sensitive_field_count": 0}
        for name, count in counts.items() if count > 0
    ]
    return encode_projection({
        "action_class": "email_current_state", "actor_type": "administrator", "records": records,
        "record_count": raw_count, "sensitive_field_count": 0,
    })


def database_projection(config: ProjectionConfig, connection: MysqlConnection) -> bytes:
    records = []
    required_tables = tuple(name for name in SELECT_QUERIES if name != "audit")
    for table_class in required_tables:
        rows = mysql_rows(config, connection, table_class)
        require(len(rows) == 1 and len(rows[0]) == 5, "database_projection_contract")
        row = rows[0]
        require(row[0] == table_class and row[1].isdigit(), "database_projection_contract")
        row_count = int(row[1])
        require(row_count > 0, "required_category_empty")
        require(row[2:] == ["1", "1", "1"], "database_safety_contract")
        records.append({
            "table_class": table_class, "row_count": row_count, "all_masked": True,
            "all_hashed": True, "all_safe": True, "sensitive_field_count": 0,
        })
    return encode_projection({
        "table_class": "email_current_state", "records": records,
        "row_count": sum(item["row_count"] for item in records),
        "all_masked": True, "all_hashed": True, "all_safe": True, "sensitive_field_count": 0,
    })


RELEVANT_LOG_RE = re.compile(
    r"(?:/api/(?:admin/email(?:/|\b)|internal/metrics\b|(?:auth|me)/[^\s]*email[^\s]*)|\bemail_adapter\b|\bdirectmail\b|\bemail_[a-z_]+\b)",
    re.IGNORECASE,
)

GO_STANDARD_LOG_RE = re.compile(
    r"(?P<stamp>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) (?P<message>[^\x00-\x1f\x7f]+)\Z"
)
GO_STARTUP_LOG_RE = re.compile(
    r"API server 启动，监听 (?:0\.0\.0\.0|127\.0\.0\.1|localhost|\[::\]|):8080\Z"
)
GO_REQUEST_LOG_RE = re.compile(
    r"(?:GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD) /[^\s]* "
    r"(?:(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?s|\d+(?:\.\d+)?(?:ms|µs|us|ns))\Z"
)
GORM_SLOW_HEADER_RE = re.compile(
    r"(?P<stamp>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) "
    r"\x1b\[32m(?P<source>(?:/home/pc/)?molin/server/[A-Za-z0-9_./-]+\.go:\d+) "
    r"\x1b\[33mSLOW SQL >= (?P<threshold>\d+(?:\.\d+)?(?:ms|s))\Z"
)
GORM_SLOW_QUERY_RE = re.compile(
    r"\x1b\[0m\x1b\[31;1m\[(?P<elapsed>\d+\.\d{3})ms\] "
    r"\x1b\[33m\[rows:(?P<rows>-|\d+)\]\x1b\[35m (?P<sql>[^\x00-\x1f\x7f;]+)\x1b\[0m\Z"
)
GORM_UNSAFE_SELECT_MARKERS = (
    "--", "/*", "*/", "#", " INTO OUTFILE", " INTO DUMPFILE", " FOR UPDATE",
    " LOCK IN SHARE MODE", " GET_LOCK(", " RELEASE_LOCK(", " LOAD_FILE(", " SLEEP(",
    " BENCHMARK(",
)


def _local_utc_offset(instant: dt.datetime) -> dt.timedelta:
    """读取执行主机在指定 UTC 时刻的本地偏移，用于绑定 Go 无时区标准日志。"""
    require(instant.tzinfo is not None, "log_contract")
    epoch = instant.timestamp()
    local_value = time.localtime(epoch)
    offset_seconds = getattr(local_value, "tm_gmtoff", None)
    if offset_seconds is None:
        # Windows 的 struct_time 可能没有 tm_gmtoff；差值仍由系统时区规则计算。
        local_naive = dt.datetime.fromtimestamp(epoch)
        utc_naive = dt.datetime.fromtimestamp(epoch, tz=dt.timezone.utc).replace(tzinfo=None)
        offset_seconds = int((local_naive - utc_naive).total_seconds())
    require(
        isinstance(offset_seconds, int)
        and -24 * 60 * 60 < offset_seconds < 24 * 60 * 60,
        "log_contract",
    )
    return dt.timedelta(seconds=offset_seconds)


def _log_window_timezone(config: ProjectionConfig) -> dt.timezone:
    """窗口不得跨本地 UTC 偏移变化点，避免夏令时重复时间产生歧义。"""
    start_offset = _local_utc_offset(config.window_start)
    end_offset = _local_utc_offset(config.window_end)
    require(start_offset == end_offset, "log_contract")
    return dt.timezone(start_offset)


def _parse_go_standard_log_line(raw_line: bytes, local_timezone: dt.timezone) -> tuple[dt.datetime, str]:
    """严格解析 Go 默认 logger 的“本地日期 时间 消息”单行格式。"""
    require(0 < len(raw_line) <= MAX_LOG_LINE_BYTES, "log_contract")
    require(raw_line.endswith(b"\n") and not raw_line.endswith(b"\r\n"), "log_contract")
    try:
        text = raw_line[:-1].decode("utf-8", errors="strict")
    except UnicodeError as error:
        raise ProjectionFailure("log_contract") from error
    match = GO_STANDARD_LOG_RE.fullmatch(text)
    require(match is not None, "log_contract")
    try:
        local_stamp = dt.datetime.strptime(match.group("stamp"), "%Y/%m/%d %H:%M:%S").replace(
            tzinfo=local_timezone,
        )
    except ValueError as error:
        raise ProjectionFailure("log_contract") from error
    return local_stamp.astimezone(dt.timezone.utc), match.group("message")


def _parse_gorm_slow_header(raw_line: bytes, local_timezone: dt.timezone) -> dt.datetime:
    """只接受部署根或 Go 模块根与 GORM 1.31 默认彩色 Warn logger 的慢查询首行。"""
    require(0 < len(raw_line) <= MAX_LOG_LINE_BYTES, "log_contract")
    require(raw_line.endswith(b"\n") and not raw_line.endswith(b"\r\n"), "log_contract")
    try:
        text = raw_line[:-1].decode("utf-8", errors="strict")
    except UnicodeError as error:
        raise ProjectionFailure("log_contract") from error
    match = GORM_SLOW_HEADER_RE.fullmatch(text)
    require(match is not None, "log_contract")
    try:
        local_stamp = dt.datetime.strptime(match.group("stamp"), "%Y/%m/%d %H:%M:%S").replace(
            tzinfo=local_timezone,
        )
    except ValueError as error:
        raise ProjectionFailure("log_contract") from error
    return local_stamp.astimezone(dt.timezone.utc)


def _parse_gorm_slow_query(raw_line: bytes) -> None:
    """慢查询只允许单条无锁 SELECT，避免用伪造日志把写操作包装成诊断噪声。"""
    require(0 < len(raw_line) <= MAX_LOG_LINE_BYTES, "log_contract")
    require(raw_line.endswith(b"\n") and not raw_line.endswith(b"\r\n"), "log_contract")
    try:
        text = raw_line[:-1].decode("utf-8", errors="strict")
    except UnicodeError as error:
        raise ProjectionFailure("log_contract") from error
    match = GORM_SLOW_QUERY_RE.fullmatch(text)
    require(match is not None, "log_contract")
    normalized_sql = " ".join(match.group("sql").upper().split())
    require(normalized_sql.startswith("SELECT "), "log_contract")
    require(not any(marker in normalized_sql for marker in GORM_UNSAFE_SELECT_MARKERS), "log_contract")


def application_log_projection(config: ProjectionConfig) -> bytes:
    metadata = config.application_log.stat(follow_symlinks=False)
    require(
        stat.S_ISREG(metadata.st_mode) and 0 < metadata.st_size <= MAX_LOG_BYTES
        and metadata.st_mode & 0o222 == 0,
        "log_contract",
    )
    # 捕获器独占新建空文件，恢复时 chmod 封闭；mtime 与 ctime 都必须落在显式 UTC 捕获窗口内。
    modified_at = dt.datetime.fromtimestamp(metadata.st_mtime, tz=dt.timezone.utc)
    changed_at = dt.datetime.fromtimestamp(metadata.st_ctime, tz=dt.timezone.utc)
    require(
        config.window_start <= modified_at <= config.window_end
        and config.window_start <= changed_at <= config.window_end,
        "log_contract",
    )
    counts = {"public": 0, "admin": 0, "internal": 0, "other": 0}
    relevant_count = 0
    startup_seen = False
    local_timezone = _log_window_timezone(config)
    with config.application_log.open("rb") as handle:
        raw_lines = handle.readlines()
    # 先扫描整份原始日志，再做任何结构解析，避免畸形块借失败顺序绕过敏感门禁。
    for raw_line in raw_lines:
        require(not contains_sensitive(raw_line, "application_logs"), "sensitive_log_detected")
    line_index = 0
    while line_index < len(raw_lines):
            raw_line = raw_lines[line_index]
            if raw_line == b"\r\n":
                # GORM 默认 logger 固定使用 CRLF 前缀，随后输出彩色慢查询首行和 SQL 续行。
                require(line_index + 2 < len(raw_lines), "log_contract")
                stamp = _parse_gorm_slow_header(raw_lines[line_index + 1], local_timezone)
                _parse_gorm_slow_query(raw_lines[line_index + 2])
                require(config.window_start <= stamp <= config.window_end, "log_contract")
                line_index += 3
                continue
            if raw_line.endswith(b"\n") and not raw_line.endswith(b"\r\n"):
                try:
                    startup_text = raw_line[:-1].decode("utf-8", errors="strict")
                except UnicodeError:
                    startup_text = ""
                if GO_STARTUP_LOG_RE.fullmatch(startup_text) is not None:
                    # NewApp 可能先输出带时间的启动告警；固定无时间启动行必须唯一且早于所有请求日志。
                    require(not startup_seen, "log_contract")
                    startup_seen = True
                    line_index += 1
                    continue
            stamp, message = _parse_go_standard_log_line(raw_line, local_timezone)
            require(config.window_start <= stamp <= config.window_end, "log_contract")
            require(startup_seen or GO_REQUEST_LOG_RE.fullmatch(message) is None, "log_contract")
            if not startup_seen:
                line_index += 1
                continue
            if RELEVANT_LOG_RE.search(message) is None:
                line_index += 1
                continue
            relevant_count += 1
            route_class = "other"
            if "/api/internal/" in message:
                route_class = "internal"
            elif "/api/admin/" in message:
                route_class = "admin"
            elif "/api/" in message:
                route_class = "public"
            counts[route_class] += 1
            line_index += 1
    require(startup_seen, "log_contract")
    require(relevant_count > 0, "required_category_empty")
    lines = [
        f"level=info route_class={key} outcome=observed count={counts[key]}"
        for key in sorted(counts) if counts[key] > 0
    ]
    return ("\n".join(lines) + "\n").encode("utf-8")


def telemetry_projection(text: str) -> bytes:
    lines = text.splitlines()
    require(
        len(lines) == 23 and lines[0].startswith("# HELP email_adapter_calls_total ")
        and lines[1] == "# TYPE email_adapter_calls_total counter",
        "telemetry_contract",
    )
    seen = set()
    for line in lines[2:]:
        match = METRIC_RE.fullmatch(line)
        require(match is not None, "telemetry_contract")
        operation, scene, result, _count = match.groups()
        require(
            operation in {"query_templates", "describe_template", "send_mail"}
            and result in {"accepted", "failed", "timeout"},
            "telemetry_contract",
        )
        seen.add((operation, scene, result))
    expected = {
        (operation, scene, result)
        for operation, scenes in (
            ("query_templates", ("template_sync",)),
            ("describe_template", ("template_sync",)),
            ("send_mail", ("register", "login", "reset_password", "bind_email", "admin_verify")),
        )
        for scene in scenes for result in ("accepted", "failed", "timeout")
    }
    require(seen == expected, "telemetry_contract")
    return ("\n".join(lines) + "\n").encode("utf-8")


def _closed_metadata(metadata: os.stat_result, node_type: str) -> None:
    if node_type == "directory":
        require(stat.S_ISDIR(metadata.st_mode), "frontend_contract")
    else:
        require(stat.S_ISREG(metadata.st_mode), "frontend_contract")
    require(metadata.st_mode & 0o222 == 0, "frontend_contract")


def _node_identity(metadata: os.stat_result) -> tuple[int, int, int, int, int]:
    return (
        metadata.st_dev, metadata.st_ino, metadata.st_mode,
        metadata.st_size, metadata.st_mtime_ns,
    )


def _open_absolute_directory(path: pathlib.Path) -> int:
    """从根目录逐段以 O_NOFOLLOW 打开，拒绝任一祖先段符号链接。"""
    require(os.name == "posix" and path.is_absolute(), "platform_not_supported")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    descriptor = os.open("/", flags)
    try:
        for part in path.parts[1:]:
            require(part not in {"", ".", ".."} and not part.lower().startswith(".env"), "frontend_contract")
            child = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        return descriptor
    except (OSError, ProjectionFailure):
        os.close(descriptor)
        raise


def _open_relative_directory(root_descriptor: int, parts: tuple[str, ...]) -> int:
    descriptor = os.dup(root_descriptor)
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    try:
        for part in parts:
            child = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        return descriptor
    except OSError:
        os.close(descriptor)
        raise


def _bounded_directory_entries(descriptor: int) -> list[os.DirEntry[str]]:
    """最多保留冻结上限个目录项，读取到第上限加一项时立即失败。"""
    entries: list[os.DirEntry[str]] = []
    with os.scandir(descriptor) as iterator:
        for entry in iterator:
            require(len(entries) < MAX_DIRECTORY_ENTRIES, "frontend_limit")
            entries.append(entry)
    return entries


def frontend_identity(root: pathlib.Path) -> FrontendIdentity:
    require(os.name == "posix", "platform_not_supported")
    root_descriptor = _open_absolute_directory(root)
    root_metadata = os.fstat(root_descriptor)
    _closed_metadata(root_metadata, "directory")
    root_identity = _node_identity(root_metadata)
    digest = hashlib.sha256()
    file_count = 0
    node_count = 1
    byte_count = 0
    has_index = False
    has_asset_script = False
    has_asset_style = False
    stack: list[tuple[tuple[str, ...], int, tuple[int, int, int, int, int]]] = [
        ((), 0, root_identity),
    ]
    try:
        while stack:
            parts, depth, expected_directory_identity = stack.pop()
            require(depth <= MAX_FRONTEND_DEPTH, "frontend_limit")
            descriptor = _open_relative_directory(root_descriptor, parts)
            try:
                before_directory = os.fstat(descriptor)
                _closed_metadata(before_directory, "directory")
                require(_node_identity(before_directory) == expected_directory_identity, "frontend_identity_changed")
                entries = _bounded_directory_entries(descriptor)
                for entry in sorted(entries, key=lambda item: item.name.encode("utf-8")):
                    require(
                        entry.name not in {"", ".", ".."}
                        and not entry.name.lower().startswith(".env")
                        and "/" not in entry.name and "\\" not in entry.name,
                        "frontend_contract",
                    )
                    metadata = entry.stat(follow_symlinks=False)
                    require(not stat.S_ISLNK(metadata.st_mode), "frontend_contract")
                    node_count += 1
                    require(node_count <= MAX_FRONTEND_NODES, "frontend_limit")
                    child_parts = (*parts, entry.name)
                    child_name = "/".join(child_parts)
                    if stat.S_ISDIR(metadata.st_mode):
                        _closed_metadata(metadata, "directory")
                        child_descriptor = os.open(
                            entry.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                            dir_fd=descriptor,
                        )
                        try:
                            opened_metadata = os.fstat(child_descriptor)
                            require(_node_identity(opened_metadata) == _node_identity(metadata), "frontend_identity_changed")
                        finally:
                            os.close(child_descriptor)
                        digest.update(b"D\x00" + child_name.encode("utf-8") + b"\x00")
                        stack.append((child_parts, depth + 1, _node_identity(metadata)))
                        continue
                    _closed_metadata(metadata, "file")
                    require(metadata.st_size <= MAX_FRONTEND_BYTES, "frontend_limit")
                    file_descriptor = os.open(entry.name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=descriptor)
                    try:
                        before_file = os.fstat(file_descriptor)
                        require(_node_identity(before_file) == _node_identity(metadata), "frontend_identity_changed")
                        file_digest = hashlib.sha256()
                        remaining = metadata.st_size
                        while remaining > 0:
                            chunk = os.read(file_descriptor, min(1024 * 1024, remaining))
                            require(bool(chunk), "frontend_identity_changed")
                            file_digest.update(chunk)
                            remaining -= len(chunk)
                        require(not os.read(file_descriptor, 1), "frontend_identity_changed")
                        after_file = os.fstat(file_descriptor)
                        require(_node_identity(after_file) == _node_identity(before_file), "frontend_identity_changed")
                    finally:
                        os.close(file_descriptor)
                    file_count += 1
                    byte_count += metadata.st_size
                    require(file_count <= MAX_FRONTEND_FILES and byte_count <= MAX_FRONTEND_BYTES, "frontend_limit")
                    digest.update(
                        b"F\x00" + child_name.encode("utf-8") + b"\x00"
                        + str(metadata.st_size).encode("ascii") + b"\x00" + file_digest.digest()
                    )
                    has_index = has_index or child_name == "index.html"
                    has_asset_script = has_asset_script or (
                        child_name.startswith("assets/") and pathlib.PurePosixPath(child_name).suffix in {".js", ".mjs"}
                    )
                    has_asset_style = has_asset_style or (
                        child_name.startswith("assets/") and pathlib.PurePosixPath(child_name).suffix == ".css"
                    )
                require(_node_identity(os.fstat(descriptor)) == expected_directory_identity, "frontend_identity_changed")
            finally:
                os.close(descriptor)
        require(_node_identity(os.fstat(root_descriptor)) == root_identity, "frontend_identity_changed")
        rechecked_root = _open_absolute_directory(root)
        try:
            require(_node_identity(os.fstat(rechecked_root)) == root_identity, "frontend_identity_changed")
        finally:
            os.close(rechecked_root)
    finally:
        os.close(root_descriptor)
    require(
        has_index and has_asset_script and has_asset_style and file_count >= 3 and byte_count > 0,
        "frontend_incomplete",
    )
    return FrontendIdentity(digest.hexdigest(), file_count, byte_count)


def verify_frontend_manifest(identity: FrontendIdentity, manifest: FrontendManifest, expected_role: str) -> None:
    require(
        identity.file_count >= 3 and identity.byte_count > 0
        and manifest.role == expected_role
        and manifest.tree_sha256 == identity.tree_sha256
        and manifest.file_count == identity.file_count
        and manifest.byte_count == identity.byte_count,
        "frontend_manifest_mismatch",
    )


def parse_listening_inodes(text: str, port: int) -> set[str]:
    """从 Linux proc tcp 表中提取固定端口 LISTEN socket inode。"""
    require(1 <= port <= 65535, "api_process_contract")
    result: set[str] = set()
    expected_port = f"{port:04X}"
    for line in text.splitlines()[1:]:
        fields = line.split()
        if len(fields) < 10 or ":" not in fields[1]:
            continue
        _address, local_port = fields[1].rsplit(":", 1)
        if local_port.upper() == expected_port and fields[3] == "0A" and fields[9].isdigit():
            result.add(fields[9])
    return result


def _process_starttime(pid: int) -> str:
    data = pathlib.Path(f"/proc/{pid}/stat").read_text(encoding="ascii", errors="strict")
    closing = data.rfind(")")
    require(closing > 0, "api_process_contract")
    fields = data[closing + 2:].split()
    require(len(fields) > 19 and fields[19].isdigit(), "api_process_contract")
    return fields[19]


def _listener_owner(inodes: set[str]) -> int:
    require(bool(inodes), "api_process_contract")
    owners: set[int] = set()
    for proc_entry in os.scandir("/proc"):
        if not proc_entry.name.isdigit() or not proc_entry.is_dir(follow_symlinks=False):
            continue
        try:
            fd_entries = os.scandir(f"/proc/{proc_entry.name}/fd")
        except OSError:
            continue
        with fd_entries:
            for fd_entry in fd_entries:
                try:
                    target = os.readlink(fd_entry.path)
                except OSError:
                    continue
                if target.startswith("socket:[") and target.endswith("]") and target[8:-1] in inodes:
                    owners.add(int(proc_entry.name))
    require(len(owners) == 1, "api_process_contract")
    return next(iter(owners))


def api_process_identity(port: int = 8080) -> ApiProcessIdentity:
    """把部署身份绑定到实际监听进程和其已打开可执行文件。"""
    require(os.name == "posix", "platform_not_supported")
    tcp = pathlib.Path("/proc/net/tcp").read_text(encoding="ascii", errors="strict")
    tcp6 = pathlib.Path("/proc/net/tcp6").read_text(encoding="ascii", errors="strict")
    inodes = parse_listening_inodes(tcp, port) | parse_listening_inodes(tcp6, port)
    pid = _listener_owner(inodes)
    proc_metadata = pathlib.Path(f"/proc/{pid}").stat(follow_symlinks=False)
    require(proc_metadata.st_uid == os.geteuid(), "api_process_contract")
    starttime = _process_starttime(pid)
    executable_link = pathlib.Path(f"/proc/{pid}/exe")
    executable_target = os.readlink(executable_link)
    require(executable_target.startswith("/") and not executable_target.endswith(" (deleted)"), "api_process_contract")
    proc_executable_metadata = executable_link.stat()
    descriptor = os.open(executable_target, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        before = os.fstat(descriptor)
        require(
            stat.S_ISREG(before.st_mode)
            and (before.st_dev, before.st_ino) == (proc_executable_metadata.st_dev, proc_executable_metadata.st_ino)
            and 0 < before.st_size <= 512 * 1024 * 1024,
            "api_process_contract",
        )
        digest = hashlib.sha256()
        remaining = before.st_size
        while remaining > 0:
            chunk = os.read(descriptor, min(1024 * 1024, remaining))
            require(bool(chunk), "api_process_contract")
            digest.update(chunk)
            remaining -= len(chunk)
        after = os.fstat(descriptor)
        require(_node_identity(after) == _node_identity(before), "api_process_contract")
    finally:
        os.close(descriptor)
    require(_process_starttime(pid) == starttime, "api_process_contract")
    require((executable_link.stat().st_dev, executable_link.stat().st_ino) == (before.st_dev, before.st_ino), "api_process_contract")
    require(_listener_owner(inodes) == pid, "api_process_contract")
    return ApiProcessIdentity(pid, starttime, before.st_dev, before.st_ino, digest.hexdigest())


def deployment_identity(
    api_version: str, api_process: ApiProcessIdentity,
    admin: FrontendIdentity, admin_manifest: FrontendManifest,
    user: FrontendIdentity, user_manifest: FrontendManifest,
) -> str:
    require(API_VERSION_RE.fullmatch(api_version) is not None, "api_version_contract")
    payload = json.dumps(
        {
            "admin_container_or_image_digest": admin_manifest.container_or_image_digest,
            "admin_file_count": admin.file_count,
            "admin_tree_sha256": admin.tree_sha256,
            "api_binary_sha256": api_process.binary_sha256,
            "api_executable_device": api_process.executable_device,
            "api_executable_inode": api_process.executable_inode,
            "api_pid": api_process.pid,
            "api_starttime": api_process.starttime,
            "api_version": api_version,
            "user_container_or_image_digest": user_manifest.container_or_image_digest,
            "user_file_count": user.file_count,
            "user_tree_sha256": user.tree_sha256,
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def api_version_from_response(value: Any) -> str:
    """只提取稳定版本字节，不允许把任意响应正文纳入部署身份。"""
    require(isinstance(value, dict), "api_version_contract")
    candidate: Any = value.get("version")
    if not isinstance(candidate, str) and isinstance(value.get("data"), dict):
        candidate = value["data"].get("version")
    require(isinstance(candidate, str) and API_VERSION_RE.fullmatch(candidate) is not None, "api_version_contract")
    return candidate


def read_api_version(config: ProjectionConfig) -> str:
    status, value = http_get_json(f"{config.api_base}/api/version", {})
    require(status == 200, "api_version_contract")
    return api_version_from_response(value)


def write_output(config: ProjectionConfig, values: dict[str, bytes]) -> None:
    # 目标由调用方指定；失败时保留已写入证据，不做自动删除或替换。
    require(not config.output.exists(), "target_exists")
    config.output.mkdir(mode=0o700)
    for name, data in values.items():
        target = config.output / name
        with target.open("xb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        target.chmod(0o444)
    config.output.chmod(0o555)


def execute(config: ProjectionConfig) -> str:
    require(os.name == "posix", "platform_not_supported")
    require(config.window_start <= dt.datetime.now(dt.timezone.utc) <= config.window_end, "window_not_active")
    validate_select_queries()
    for path in (
        config.admin_token_file, config.internal_token_file, config.mysql_client,
        config.mysql_connection_file, config.application_log, config.admin_frontend,
        config.admin_frontend_manifest, config.user_frontend, config.user_frontend_manifest,
    ):
        require(path.exists() and not path.is_symlink(), "source_contract")
    mysql_metadata = config.mysql_client.stat(follow_symlinks=False)
    require(stat.S_ISREG(mysql_metadata.st_mode) and mysql_metadata.st_mode & 0o111 != 0, "mysql_client_contract")
    connection = read_mysql_connection(config.mysql_connection_file)
    validate_mysql_grants(config, connection)
    admin_manifest = read_frontend_manifest(config.admin_frontend_manifest, "admin_frontend")
    user_manifest = read_frontend_manifest(config.user_frontend_manifest, "user_frontend")
    api_process = api_process_identity(8080)
    admin_identity = frontend_identity(config.admin_frontend)
    user_identity = frontend_identity(config.user_frontend)
    verify_frontend_manifest(admin_identity, admin_manifest, "admin_frontend")
    verify_frontend_manifest(user_identity, user_manifest, "user_frontend")
    admin_token = read_small_secret(config.admin_token_file)
    internal_token = read_small_secret(config.internal_token_file)
    public_bytes, api_version = public_projection(config)
    derived_deployment_sha = deployment_identity(
        api_version, api_process, admin_identity, admin_manifest, user_identity, user_manifest,
    )
    values = {
        "public-get.safe.json": public_bytes,
        "admin-get.safe.json": admin_projection(config, admin_token),
        "application.safe.log": application_log_projection(config),
        "audit.safe.json": audit_projection(config, connection),
        "database.safe.json": database_projection(config, connection),
        "email-metrics.prom": telemetry_projection(
            http_get_text(f"{config.api_base}/api/internal/metrics", {"X-Internal-Token": internal_token})
        ),
    }
    # 落盘前再次读取版本并完整遍历两个前端树，任何并发替换都使部署身份复核失败。
    rechecked_api_version = read_api_version(config)
    rechecked_api_process = api_process_identity(8080)
    rechecked_connection = read_mysql_connection(config.mysql_connection_file)
    rechecked_admin_manifest = read_frontend_manifest(config.admin_frontend_manifest, "admin_frontend")
    rechecked_user_manifest = read_frontend_manifest(config.user_frontend_manifest, "user_frontend")
    rechecked_admin_identity = frontend_identity(config.admin_frontend)
    rechecked_user_identity = frontend_identity(config.user_frontend)
    require(
        rechecked_api_version == api_version
        and rechecked_api_process == api_process
        and rechecked_connection == connection
        and rechecked_admin_manifest == admin_manifest
        and rechecked_user_manifest == user_manifest
        and rechecked_admin_identity == admin_identity
        and rechecked_user_identity == user_identity
        and deployment_identity(
            rechecked_api_version, rechecked_api_process,
            rechecked_admin_identity, rechecked_admin_manifest,
            rechecked_user_identity, rechecked_user_manifest,
        ) == derived_deployment_sha,
        "deployment_identity_changed",
    )
    require(config.window_start <= dt.datetime.now(dt.timezone.utc) <= config.window_end, "window_expired")
    write_output(config, values)
    return derived_deployment_sha


def metric_fixture() -> str:
    metric_lines = [
        f'email_adapter_calls_total{{operation="{operation}",scene="{scene}",result="{result}"}} 0'
        for operation, scenes in (
            ("query_templates", ("template_sync",)),
            ("describe_template", ("template_sync",)),
            ("send_mail", ("register", "login", "reset_password", "bind_email", "admin_verify")),
        )
        for scene in scenes for result in ("accepted", "failed", "timeout")
    ]
    return "# HELP email_adapter_calls_total safe\n# TYPE email_adapter_calls_total counter\n" + "\n".join(metric_lines) + "\n"


def self_test() -> int:
    sample = {
        "api_base": "http://127.0.0.1:8080", "admin_token_file": "/tmp/admin.token",
        "internal_token_file": "/tmp/internal.token", "mysql_client": "/usr/bin/mysql",
        "mysql_connection_file": "/tmp/mysql-connection.json", "mysql_database": "molin",
        "application_log": "/tmp/application.jsonl", "admin_frontend": "/tmp/admin-dist",
        "admin_frontend_manifest": "/tmp/admin-frontend-manifest.json",
        "user_frontend": "/tmp/user-dist", "user_frontend_manifest": "/tmp/user-frontend-manifest.json",
        "output": "/tmp/phase4-source",
        "window_start_utc": "2026-07-31T00:00:00Z", "window_end_utc": "2026-07-31T00:30:00Z",
    }
    config = parse_config(sample)
    validate_select_queries()
    require(len(telemetry_projection(metric_fixture())) > 0, "self_test")
    require(audit_action_class("email.scene.binding.update.result") == "scene_binding", "self_test")
    process = ApiProcessIdentity(7, "11", 1, 2, "c" * 64)
    admin = FrontendIdentity("a" * 64, 3, 3)
    user = FrontendIdentity("b" * 64, 3, 3)
    admin_manifest = FrontendManifest("admin_frontend", admin.tree_sha256, 3, 3, "sha256:" + "d" * 64)
    user_manifest = FrontendManifest("user_frontend", user.tree_sha256, 3, 3, "sha256:" + "e" * 64)
    require(HEX64_RE.fullmatch(deployment_identity("0.1.0", process, admin, admin_manifest, user, user_manifest)) is not None, "self_test")
    command, child_env = mysql_command(config, MysqlConnection("127.0.0.1", 3306, "molin", "secret", None), "SELECT 1")
    require(command[1] == "--no-defaults" and child_env == {"MYSQL_PWD": "secret"}, "self_test")
    tcp_fixture = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 12345"
    require(parse_listening_inodes(tcp_fixture, 8080) == {"12345"}, "self_test")
    return 7


def main(argv: list[str]) -> int:
    try:
        if argv == ["--self-test"]:
            cases = self_test()
            print(f"status=pass mode=source_projection_selftest cases={cases} external_access=false persistent_writes=false env_read=false")
            return 0
        if not argv:
            print("status=disabled mode=source_projection external_access=false persistent_writes=false env_read=false")
            return 0
        require(
            len(argv) == 5 and argv[:2] == ["--execute", "--confirm"]
            and argv[2] == CONFIRM and argv[3] == "--config",
            "argument_contract",
        )
        config_path = safe_absolute(argv[4])
        config_metadata = config_path.stat(follow_symlinks=False)
        require(
            stat.S_ISREG(config_metadata.st_mode) and 0 < config_metadata.st_size <= 64 * 1024
            and stat.S_IMODE(config_metadata.st_mode) == 0o600
            and config_metadata.st_uid == os.geteuid(),
            "config_file_contract",
        )
        config = parse_config(json.loads(config_path.read_text(encoding="utf-8", errors="strict")))
        derived_sha = execute(config)
        print(
            "status=pass mode=source_projection surfaces=6 window_bound=true deployment_bound=true "
            f"deployment_sha={derived_sha} collector_started=false scanner_started=false "
            "external_access=readonly persistent_writes=true env_read=false"
        )
        return 0
    except (ProjectionFailure, OSError, UnicodeError, json.JSONDecodeError, subprocess.SubprocessError) as error:
        classification = error.args[0] if isinstance(error, ProjectionFailure) and error.args else "source_projection_failed"
        if re.fullmatch(r"[a-z_]+", str(classification)) is None:
            classification = "source_projection_failed"
        print(
            f"status=failed mode=source_projection classification={classification} "
            "external_access=readonly persistent_writes=unknown env_read=false"
        )
        return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

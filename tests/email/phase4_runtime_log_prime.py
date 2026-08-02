#!/usr/bin/env python3
"""在受控捕获期内依次触发五个只读 GET，为 Phase 4 应用日志生成可验证记录。"""

from __future__ import annotations

import contextlib
import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import signal
import stat
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any, Iterator

sys.dont_write_bytecode = True

CONFIRM = "I_CONFIRM_PHASE4_RUNTIME_LOG_PRIME_READONLY_GETS"
SOURCE_SHA256 = "2BC04F38C2E5073B5FE390C83394F16ACC46B0C6B353834A848EEC5487F606AB"
CONFIG_KEYS = frozenset({
    "api_base", "admin_token_file", "internal_token_file", "application_log",
    "window_start_utc", "window_end_utc",
})
MAX_CONFIG_BYTES = 64 * 1024
MAX_RESPONSE_BYTES = 1024 * 1024
HTTP_TIMEOUT_SECONDS = 10.0


def _preimport_failure() -> None:
    """源码冻结失败只输出固定摘要，不泄漏路径或异常正文。"""
    print(
        "status=failed mode=runtime_log_prime classification=preimport_gate "
        "external_access=false requests=0 writes=none env_read=false"
    )


def _load_source_projection() -> Any:
    source_path = pathlib.Path(__file__).resolve().with_name("phase4_runtime_source_projection.py")
    try:
        source_bytes = source_path.read_bytes()
    except OSError:
        _preimport_failure()
        raise SystemExit(2)
    if hashlib.sha256(source_bytes).hexdigest().upper() != SOURCE_SHA256:
        _preimport_failure()
        raise SystemExit(2)
    try:
        spec = importlib.util.spec_from_file_location("_molin_frozen_phase4_source_projection", source_path)
        if spec is None or spec.loader is None:
            raise ImportError
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        return module
    except (ImportError, OSError, TypeError):
        _preimport_failure()
        raise SystemExit(2)


# 只有完整 source projection 字节门禁通过后，才复用其进程身份与 telemetry 契约。
source_projection = _load_source_projection()


class PrimeFailure(Exception):
    """失败只携带固定分类，不携带 Token、响应正文或系统异常。"""


@dataclass(frozen=True)
class PrimeConfig:
    api_base: str
    admin_token_file: pathlib.Path
    internal_token_file: pathlib.Path
    application_log: pathlib.Path
    window_start: dt.datetime
    window_end: dt.datetime


@dataclass(frozen=True)
class LogIdentity:
    device: int
    inode: int
    owner: int
    mode: int
    size: int


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise PrimeFailure(classification)


def _source_call(callable_value: Any, classification: str) -> Any:
    """把冻结 source projection 的内部分类折叠为 priming 固定失败面。"""
    try:
        return callable_value()
    except (source_projection.ProjectionFailure, OSError, ValueError) as error:
        raise PrimeFailure(classification) from error


def strict_json(data: bytes, classification: str) -> dict[str, Any]:
    """拒绝重复键、非有限常量、非 UTF-8 和非对象 JSON。"""
    def pairs_hook(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise PrimeFailure(classification)
            result[key] = value
        return result

    def reject_constant(_value: str) -> Any:
        raise PrimeFailure(classification)

    try:
        value = json.loads(
            data.decode("utf-8", errors="strict"), object_pairs_hook=pairs_hook,
            parse_constant=reject_constant,
        )
    except (UnicodeError, json.JSONDecodeError) as error:
        raise PrimeFailure(classification) from error
    require(isinstance(value, dict), classification)
    return value


def parse_utc(value: Any) -> dt.datetime:
    require(isinstance(value, str) and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value) is not None, "window_contract")
    try:
        return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
    except ValueError as error:
        raise PrimeFailure("window_contract") from error


def safe_absolute(value: Any) -> pathlib.Path:
    require(isinstance(value, str) and value.startswith("/") and "\x00" not in value, "path_contract")
    require("\\" not in value and ":" not in value, "path_contract")
    path = pathlib.PurePosixPath(value)
    require(path.is_absolute() and path.parts not in {(), ("/",)}, "path_contract")
    require(all(part not in {"", ".", ".."} and not part.lower().startswith(".env") for part in path.parts[1:]), "path_contract")
    return pathlib.Path(value)


def parse_config(value: Any) -> PrimeConfig:
    require(isinstance(value, dict) and set(value) == CONFIG_KEYS, "config_contract")
    require(value["api_base"] in {"http://127.0.0.1:8080", "http://localhost:8080"}, "api_base_contract")
    start, end = parse_utc(value["window_start_utc"]), parse_utc(value["window_end_utc"])
    require(dt.timedelta(0) < end - start <= dt.timedelta(minutes=30), "window_contract")
    config = PrimeConfig(
        value["api_base"], safe_absolute(value["admin_token_file"]),
        safe_absolute(value["internal_token_file"]), safe_absolute(value["application_log"]),
        start, end,
    )
    require(len({config.admin_token_file, config.internal_token_file, config.application_log}) == 3, "path_relationship")
    return config


def _read_secure_file(path: pathlib.Path, maximum: int, classification: str) -> bytes:
    require(os.name == "posix", "platform_not_supported")
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError as error:
        raise PrimeFailure(classification) from error
    try:
        before = os.fstat(descriptor)
        require(
            stat.S_ISREG(before.st_mode) and stat.S_IMODE(before.st_mode) == 0o600
            and before.st_uid == os.geteuid() and 0 < before.st_size <= maximum,
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
        data = b"".join(chunks)
        after = os.fstat(descriptor)
        require(
            (before.st_dev, before.st_ino, before.st_mode, before.st_uid, before.st_size, before.st_mtime_ns)
            == (after.st_dev, after.st_ino, after.st_mode, after.st_uid, after.st_size, after.st_mtime_ns),
            classification,
        )
        return data
    finally:
        os.close(descriptor)


def read_token(path: pathlib.Path) -> str:
    try:
        value = _read_secure_file(path, 8192, "token_file_contract").decode("utf-8", errors="strict").strip()
    except UnicodeError as error:
        raise PrimeFailure("token_file_contract") from error
    require(len(value) >= 32 and "\x00" not in value and "\n" not in value and "\r" not in value, "token_file_contract")
    return value


def log_identity(path: pathlib.Path) -> LogIdentity:
    metadata = path.stat(follow_symlinks=False)
    require(
        stat.S_ISREG(metadata.st_mode) and stat.S_IMODE(metadata.st_mode) == 0o600
        and metadata.st_uid == os.geteuid() and metadata.st_size > 0,
        "log_contract",
    )
    return LogIdentity(metadata.st_dev, metadata.st_ino, metadata.st_uid, stat.S_IMODE(metadata.st_mode), metadata.st_size)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """任何 3xx 都作为 HTTP 失败处理，绝不跟随重定向。"""
    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> None:
        return None


@contextlib.contextmanager
def _hard_deadline(seconds: float) -> Iterator[None]:
    """真实 POSIX 主线程用单调时钟信号封闭整个连接与正文读取期限。"""
    if os.name != "posix" or not hasattr(signal, "setitimer"):
        yield
        return
    previous_handler = signal.getsignal(signal.SIGALRM)

    def timeout_handler(_signum: int, _frame: Any) -> None:
        raise TimeoutError

    signal.signal(signal.SIGALRM, timeout_handler)
    previous_timer = signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, *previous_timer)
        signal.signal(signal.SIGALRM, previous_handler)


def _request_once(url: str, headers: dict[str, str]) -> tuple[int, bytes]:
    request = urllib.request.Request(url, headers=headers, method="GET")
    # 显式空代理表避免 urllib 从环境变量读取代理配置，回环请求不会被环境重定向。
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())
    started = time.monotonic()
    try:
        with _hard_deadline(HTTP_TIMEOUT_SECONDS):
            with opener.open(request, timeout=HTTP_TIMEOUT_SECONDS) as response:
                body = response.read(MAX_RESPONSE_BYTES + 1)
                status = response.status
        require(time.monotonic() - started <= HTTP_TIMEOUT_SECONDS, "http_timeout")
        require(len(body) <= MAX_RESPONSE_BYTES, "http_size_limit")
        return status, body
    except PrimeFailure:
        raise
    except (OSError, TimeoutError, urllib.error.URLError, ValueError) as error:
        raise PrimeFailure("http_read_failed") from error


def _validate_envelope(value: dict[str, Any], data_keys: set[str], classification: str) -> dict[str, Any]:
    require(set(value) == {"code", "message", "data"}, classification)
    require(value["code"] == 0 and value["message"] == "ok" and isinstance(value["data"], dict), classification)
    require(set(value["data"]) == data_keys, classification)
    return value["data"]


def validate_json_route(route: str, status: int, body: bytes) -> None:
    require(status == 200, "http_status")
    value = strict_json(body, "http_response_contract")
    if route == "health":
        data = _validate_envelope(value, {"status"}, "public_response_contract")
        require(data["status"] == "ok", "public_response_contract")
    elif route == "ready":
        data = _validate_envelope(value, {"status"}, "public_response_contract")
        require(data["status"] == "ready", "public_response_contract")
    elif route == "version":
        _validate_envelope(value, {"version"}, "public_response_contract")
        _source_call(lambda: source_projection.api_version_from_response(value), "public_response_contract")
    else:
        data = _validate_envelope(value, {
            "template_total", "approved_count", "local_enabled_count", "unbound_scene_count",
            "submitted_today_count", "failed_today_count", "last_synced_at",
        }, "admin_response_contract")
        counters = (
            "template_total", "approved_count", "local_enabled_count", "unbound_scene_count",
            "submitted_today_count", "failed_today_count",
        )
        require(all(type(data[key]) is int and data[key] >= 0 for key in counters), "admin_response_contract")
        last_synced_at = data["last_synced_at"]
        if last_synced_at is not None:
            require(isinstance(last_synced_at, str) and "T" in last_synced_at, "admin_response_contract")
            try:
                parsed_last_synced_at = dt.datetime.fromisoformat(last_synced_at.replace("Z", "+00:00"))
            except ValueError as error:
                raise PrimeFailure("admin_response_contract") from error
            require(parsed_last_synced_at.tzinfo is not None, "admin_response_contract")
        require(data["template_total"] > 0, "admin_response_contract")


def execute(config: PrimeConfig) -> None:
    require(config.window_start <= dt.datetime.now(dt.timezone.utc) <= config.window_end, "window_expired")
    before_process = _source_call(lambda: source_projection.api_process_identity(8080), "api_process_contract")
    before_log = log_identity(config.application_log)
    admin_token = read_token(config.admin_token_file)
    internal_token = read_token(config.internal_token_file)
    requests = (
        ("health", "/api/health", {}),
        ("ready", "/api/ready", {}),
        ("version", "/api/version", {}),
        ("admin", "/api/admin/email/summary", {"Authorization": f"Bearer {admin_token}"}),
        ("metrics", "/api/internal/metrics", {"X-Internal-Token": internal_token}),
    )
    for route, path, headers in requests:
        status, body = _request_once(config.api_base + path, headers)
        if route == "metrics":
            require(status == 200, "http_status")
            try:
                text = body.decode("utf-8", errors="strict")
            except UnicodeError as error:
                raise PrimeFailure("metrics_response_contract") from error
            _source_call(lambda: source_projection.telemetry_projection(text), "metrics_response_contract")
        else:
            validate_json_route(route, status, body)
    after_process = _source_call(lambda: source_projection.api_process_identity(8080), "api_process_contract")
    after_log = log_identity(config.application_log)
    require(before_process == after_process, "api_identity_changed")
    require(
        before_log.device == after_log.device and before_log.inode == after_log.inode
        and before_log.owner == after_log.owner and before_log.mode == after_log.mode
        and after_log.size > before_log.size,
        "log_identity_changed",
    )
    require(config.window_start <= dt.datetime.now(dt.timezone.utc) <= config.window_end, "window_expired")


def self_test() -> int:
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    value = {
        "api_base": "http://127.0.0.1:8080", "admin_token_file": "/tmp/admin.token",
        "internal_token_file": "/tmp/internal.token", "application_log": "/tmp/application.safe-source.log",
        "window_start_utc": (now - dt.timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "window_end_utc": (now + dt.timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    config = parse_config(value)
    validate_json_route("health", 200, b'{"code":0,"message":"ok","data":{"status":"ok"}}')
    validate_json_route("ready", 200, b'{"code":0,"message":"ok","data":{"status":"ready"}}')
    validate_json_route("version", 200, b'{"code":0,"message":"ok","data":{"version":"0.1.0"}}')
    require(config.api_base == "http://127.0.0.1:8080", "self_test")
    require(len(source_projection.telemetry_projection(source_projection.metric_fixture())) > 0, "self_test")
    return 5


def main(argv: list[str]) -> int:
    try:
        if not argv:
            print("status=disabled mode=runtime_log_prime external_access=false requests=0 writes=none env_read=false")
            return 0
        if argv == ["--self-test"]:
            cases = self_test()
            print(f"status=pass mode=runtime_log_prime_selftest cases={cases} external_access=false requests=0 writes=none env_read=false")
            return 0
        require(
            len(argv) == 5 and argv[:2] == ["--execute", "--confirm"]
            and argv[2] == CONFIRM and argv[3] == "--config",
            "argument_contract",
        )
        config_path = safe_absolute(argv[4])
        config_value = strict_json(_read_secure_file(config_path, MAX_CONFIG_BYTES, "config_file_contract"), "config_contract")
        execute(parse_config(config_value))
        print(
            "status=pass mode=runtime_log_prime public=pass admin=pass internal=pass "
            "requests=5 writes=application_log_only env_read=false"
        )
        return 0
    except (PrimeFailure, OSError, ValueError):
        print(
            "status=failed mode=runtime_log_prime classification=closed "
            "requests=not_completed writes=application_log_possible env_read=false"
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

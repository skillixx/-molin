#!/usr/bin/env python3
"""离线验证 Phase 4 日志 priming 的关闭模式、五请求顺序与失败关闭边界。"""

from __future__ import annotations

import ast
import datetime as dt
import hashlib
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
from typing import Any

sys.dont_write_bytecode = True

HERE = pathlib.Path(__file__).resolve().parent
SCRIPT = HERE / "phase4_runtime_log_prime.py"
SOURCE = HERE / "phase4_runtime_source_projection.py"
EXPECTED_PRIME_SHA256 = "2D20F228FB771E1C264E909F4C4857FD5EF0D29995B3BC1E804C11BC6DE13ECE"
PREIMPORT_FAILURE = (
    "status=failed mode=runtime_log_prime_contract classification=preimport_gate "
    "external_access=false requests=0 writes=none env_read=false\n"
)


def _preimport_prime() -> None:
    try:
        source_bytes = SCRIPT.read_bytes()
    except OSError:
        print(PREIMPORT_FAILURE, end="")
        raise SystemExit(1)
    if hashlib.sha256(source_bytes).hexdigest().upper() != EXPECTED_PRIME_SHA256:
        print(PREIMPORT_FAILURE, end="")
        raise SystemExit(1)


_preimport_prime()

# 候选脚本完整字节通过后才允许执行顶层 source projection 绑定逻辑。
import phase4_runtime_log_prime as prime


class ContractFailure(Exception):
    """契约失败只携带固定分类。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def expect_failure(callable_value: Any, classification: str) -> None:
    try:
        callable_value()
    except prime.PrimeFailure as error:
        require(error.args == (classification,), "failure_classification")
        return
    raise ContractFailure("unsafe_case_accepted")


def run_cli(arguments: list[str], optimize: bool = False, script: pathlib.Path = SCRIPT) -> tuple[int, str, str]:
    command = [sys.executable]
    if optimize:
        command.append("-O")
    command.extend(["-B", str(script), *arguments])
    result = subprocess.run(
        command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, check=False, timeout=30,
    )
    return result.returncode, result.stdout, result.stderr


def mode_contract() -> int:
    cases = 0
    for optimize in (False, True):
        code, stdout, stderr = run_cli([], optimize)
        require(code == 0 and stderr == "", "default_mode")
        require(stdout == "status=disabled mode=runtime_log_prime external_access=false requests=0 writes=none env_read=false\n", "default_summary")
        cases += 1
        code, stdout, stderr = run_cli(["--self-test"], optimize)
        require(code == 0 and stderr == "", "selftest_mode")
        require(stdout == "status=pass mode=runtime_log_prime_selftest cases=5 external_access=false requests=0 writes=none env_read=false\n", "selftest_summary")
        cases += 1
    return cases


def config_value() -> dict[str, Any]:
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    return {
        "api_base": "http://127.0.0.1:8080", "admin_token_file": "/tmp/admin.token",
        "internal_token_file": "/tmp/internal.token", "application_log": "/tmp/application.safe-source.log",
        "window_start_utc": (now - dt.timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "window_end_utc": (now + dt.timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }


def config_contract() -> int:
    cases = 0
    value = config_value()
    prime.parse_config(value)
    cases += 1
    attacks: list[dict[str, Any]] = []
    for base in ("http://8.130.9.163:8080", "https://127.0.0.1:8080", "http://127.0.0.1:8081"):
        attack = dict(value); attack["api_base"] = base; attacks.append(attack)
    attack = dict(value); attack["unknown"] = "x"; attacks.append(attack)
    attack = dict(value); attack["admin_token_file"] = value["internal_token_file"]; attacks.append(attack)
    attack = dict(value); attack["application_log"] = "/tmp/../unsafe.log"; attacks.append(attack)
    attack = dict(value); attack["admin_token_file"] = "/tmp/.env.token"; attacks.append(attack)
    attack = dict(value); attack["window_end_utc"] = value["window_start_utc"]; attacks.append(attack)
    for attack in attacks:
        expect_failure(lambda attack=attack: prime.parse_config(attack), "api_base_contract" if attack.get("api_base") != value["api_base"] else ("path_relationship" if attack.get("admin_token_file") == value["internal_token_file"] else ("config_contract" if "unknown" in attack else ("window_contract" if attack.get("window_end_utc") == value["window_start_utc"] else "path_contract"))))
        cases += 1
    expect_failure(
        lambda: prime.strict_json(b'{"api_base":"a","api_base":"b"}', "config_contract"),
        "config_contract",
    )
    return cases + 1


def safe_bodies() -> dict[str, bytes]:
    return {
        "health": b'{"code":0,"message":"ok","data":{"status":"ok"}}',
        "ready": b'{"code":0,"message":"ok","data":{"status":"ready"}}',
        "version": b'{"code":0,"message":"ok","data":{"version":"0.1.0"}}',
        "admin": b'{"code":0,"message":"ok","data":{"template_total":1,"approved_count":1,"local_enabled_count":1,"unbound_scene_count":0,"submitted_today_count":0,"failed_today_count":0,"last_synced_at":"2026-07-31T14:00:00+08:00"}}',
        "metrics": prime.source_projection.metric_fixture().encode("utf-8"),
    }


def execute_fixture(
    *, process_after: Any | None = None, log_after: prime.LogIdentity | None = None,
    response_override: dict[str, tuple[int, bytes] | Exception] | None = None,
) -> tuple[list[tuple[str, dict[str, str]]], Any]:
    config = prime.parse_config(config_value())
    process_before = prime.source_projection.ApiProcessIdentity(7, "11", 1, 2, "a" * 64)
    process_values = iter((process_before, process_before if process_after is None else process_after))
    log_before = prime.LogIdentity(1, 2, 1000, 0o600, 100)
    log_values = iter((log_before, prime.LogIdentity(1, 2, 1000, 0o600, 200) if log_after is None else log_after))
    bodies = safe_bodies()
    seen: list[tuple[str, dict[str, str]]] = []
    originals = (
        prime.source_projection.api_process_identity, prime.log_identity,
        prime.read_token, prime._request_once,
    )

    def request(url: str, headers: dict[str, str]) -> tuple[int, bytes]:
        route = url.rsplit("/", 1)[-1]
        route = "admin" if route == "summary" else route
        seen.append((url, dict(headers)))
        override = (response_override or {}).get(route)
        if isinstance(override, Exception):
            raise override
        if override is not None:
            return override
        return 200, bodies[route]

    try:
        prime.source_projection.api_process_identity = lambda _port=8080: next(process_values)
        prime.log_identity = lambda _path: next(log_values)
        prime.read_token = lambda path: "A" * 40 if "admin" in str(path) else "I" * 40
        prime._request_once = request
        prime.execute(config)
        return seen, None
    except Exception as error:
        return seen, error
    finally:
        (
            prime.source_projection.api_process_identity, prime.log_identity,
            prime.read_token, prime._request_once,
        ) = originals


def request_contract() -> int:
    seen, error = execute_fixture()
    require(error is None and [url.rsplit("/", 1)[-1] for url, _headers in seen] == ["health", "ready", "version", "summary", "metrics"], "request_order")
    require(seen[0][1] == seen[1][1] == seen[2][1] == {}, "public_headers")
    require(seen[3][1] == {"Authorization": "Bearer " + "A" * 40}, "admin_header")
    require(seen[4][1] == {"X-Internal-Token": "I" * 40}, "internal_header")
    return 4


def failure_contract() -> int:
    cases = 0
    failures = (
        ({"health": (500, b"{}")}, "http_status"),
        ({"health": (302, b"{}")}, "http_status"),
        ({"health": (200, b'{"code":0,"code":0,"message":"ok","data":{"status":"ok"}}')}, "http_response_contract"),
        ({"admin": (200, b'{"code":0,"message":"ok","data":{"template_total":0}}')}, "admin_response_contract"),
        ({"admin": (200, b'{"code":0,"message":"ok","data":{"template_total":1,"approved_count":1,"local_enabled_count":1,"unbound_scene_count":0,"submitted_today_count":0,"failed_today_count":0,"last_synced_at":"invalid"}}')}, "admin_response_contract"),
        ({"admin": (200, b'{"code":0,"message":"ok","data":{"template_total":1,"approved_count":1,"local_enabled_count":1,"unbound_scene_count":0,"submitted_today_count":0,"failed_today_count":0,"last_synced_at":"2026-07-31T14:00:00"}}')}, "admin_response_contract"),
        ({"metrics": (200, b"bad")}, "metrics_response_contract"),
        ({"ready": prime.PrimeFailure("http_timeout")}, "http_timeout"),
        ({"version": prime.PrimeFailure("http_size_limit")}, "http_size_limit"),
    )
    for override, classification in failures:
        _seen, error = execute_fixture(response_override=override)
        require(isinstance(error, prime.PrimeFailure) and error.args == (classification,), "response_failure")
        cases += 1
    changed_process = prime.source_projection.ApiProcessIdentity(8, "12", 1, 2, "a" * 64)
    _seen, error = execute_fixture(process_after=changed_process)
    require(isinstance(error, prime.PrimeFailure) and error.args == ("api_identity_changed",), "process_drift")
    cases += 1
    for changed_log in (
        prime.LogIdentity(1, 2, 1000, 0o600, 100),
        prime.LogIdentity(1, 3, 1000, 0o600, 200),
        prime.LogIdentity(1, 2, 1000, 0o400, 200),
    ):
        _seen, error = execute_fixture(log_after=changed_log)
        require(isinstance(error, prime.PrimeFailure) and error.args == ("log_identity_changed",), "log_drift")
        cases += 1
    return cases


def transport_contract() -> int:
    require(prime._NoRedirect().redirect_request(None, None, 302, "", None, "http://evil") is None, "redirect_gate")
    source = SCRIPT.read_text(encoding="utf-8")
    require("response.read(MAX_RESPONSE_BYTES + 1)" in source and "time.monotonic() - started <= HTTP_TIMEOUT_SECONDS" in source, "bounded_http")
    require("_hard_deadline(HTTP_TIMEOUT_SECONDS)" in source and "signal.setitimer" in source, "slow_response_gate")
    require("urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())" in source, "redirect_handler")
    require("subprocess" not in source and "os.environ" not in source, "token_process_boundary")
    require("Authorization" in source and "X-Internal-Token" in source, "auth_headers")
    tree = ast.parse(source)
    print_nodes = [node for node in ast.walk(tree) if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "print"]
    require(all("admin_token" not in ast.dump(node) and "internal_token" not in ast.dump(node) for node in print_nodes), "token_output_gate")
    return 7


def file_contract() -> int:
    source = SCRIPT.read_text(encoding="utf-8")
    require("os.O_NOFOLLOW" in source and "stat.S_IMODE(before.st_mode) == 0o600" in source, "nofollow_mode_gate")
    require("before.st_uid == os.geteuid()" in source and "before.st_ino" in source, "owner_inode_gate")
    require("after_log.size > before_log.size" in source, "log_growth_gate")
    require("before_process == after_process" in source, "process_recheck_gate")
    require("SOURCE_SHA256 = \"2BC04F38C2E5073B5FE390C83394F16ACC46B0C6B353834A848EEC5487F606AB\"" in source, "source_sha_gate")
    require(not any(isinstance(node, ast.Assert) for node in ast.walk(ast.parse(source))), "assert_dependent")
    cases = 6
    if os.name == "posix":
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            token = root / "token"
            token.write_text("T" * 40, encoding="utf-8")
            token.chmod(0o600)
            require(prime.read_token(token) == "T" * 40, "secure_token")
            cases += 1
            token.chmod(0o644)
            expect_failure(lambda: prime.read_token(token), "token_file_contract")
            cases += 1
            token.chmod(0o600)
            token_link = root / "token-link"
            token_link.symlink_to(token)
            expect_failure(lambda: prime.read_token(token_link), "token_file_contract")
            cases += 1
            log_link = root / "log-link"
            log_link.symlink_to(token)
            expect_failure(lambda: prime.log_identity(log_link), "log_contract")
            cases += 1
    return cases


def preimport_contract() -> int:
    cases = 0
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary)
        for name in (SCRIPT.name, SOURCE.name, "phase4_runtime_sensitive_scan.py", pathlib.Path(__file__).name):
            shutil.copy2(HERE / name, root / name)
        candidate = root / SCRIPT.name
        candidate.write_bytes(candidate.read_bytes() + b"\n")
        contract_candidate = root / pathlib.Path(__file__).name
        for optimize in (False, True):
            code, stdout, stderr = run_cli([], optimize, contract_candidate)
            require(code == 1 and stderr == "", "prime_preimport_exit")
            require(stdout == PREIMPORT_FAILURE, "prime_preimport_summary")
            cases += 1
        shutil.copy2(SCRIPT, candidate)
        (root / SOURCE.name).write_bytes((root / SOURCE.name).read_bytes() + b"\n")
        for optimize in (False, True):
            code, stdout, stderr = run_cli([], optimize, candidate)
            require(code == 2 and stderr == "", "source_preimport_exit")
            require(stdout.startswith("status=failed mode=runtime_log_prime classification=preimport_gate "), "source_preimport_summary")
            cases += 1
    return cases


def main() -> int:
    try:
        cases = (
            mode_contract() + config_contract() + request_contract() + failure_contract()
            + transport_contract() + file_contract() + preimport_contract()
        )
        print(f"status=pass mode=runtime_log_prime_contract cases={cases} external_access=false requests=0 writes=none env_read=false")
        return 0
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=runtime_log_prime_contract classification=offline_contract external_access=false requests=0 writes=none env_read=false")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

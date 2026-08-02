#!/usr/bin/env python3
"""离线验证 Phase 4 运行时敏感扫描器的契约和攻击模型。"""

from __future__ import annotations

import ast
import contextlib
import copy
import hashlib
import io
import json
import os
import pathlib
import re
import stat
import subprocess
import sys
import tempfile
import types
from typing import Any, Callable

import phase4_runtime_sensitive_scan as scanner


HERE = pathlib.Path(__file__).resolve().parent
SCANNER = HERE / "phase4_runtime_sensitive_scan.py"
OPTIMIZED_ENV = "MOLIN_PHASE4_RUNTIME_SCAN_OPTIMIZED"
PASS_RE = re.compile(
    r"\Astatus=pass classification=complete surfaces=6 surfaces_passed=6 "
    r"files_scanned=8 bytes_scanned=\d+ findings=0 window_bound=true "
    r"deployment_bound=true writes=false restart=false deploy=false mail_sent=false\n?\Z"
)
FAIL_RE = re.compile(
    r"\Astatus=failed classification=[a-z_]+ surfaces=6 surfaces_passed=0 "
    r"files_scanned=0 bytes_scanned=0 findings=0 window_bound=false "
    r"deployment_bound=false writes=false restart=false deploy=false mail_sent=false\n?\Z"
)
SENSITIVE_FAIL_RE = re.compile(
    r"\Astatus=failed classification=sensitive_finding surfaces=6 surfaces_passed=6 "
    r"files_scanned=8 bytes_scanned=\d+ findings=[1-9]\d* window_bound=true "
    r"deployment_bound=true writes=false restart=false deploy=false mail_sent=false\n?\Z"
)


class ContractFailure(Exception):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractFailure(message)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def write_bytes(root: pathlib.Path, relative: str, data: bytes) -> dict[str, Any]:
    target = root.joinpath(*pathlib.PurePosixPath(relative).parts)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)
    return {
        "path": relative,
        "sha256": sha256(data),
        "captured_at_utc": "2026-07-31T01:05:00Z",
        "deployment_sha": "a" * 40,
    }


def tree_digest(root: pathlib.Path) -> tuple[str, int, int]:
    digest = hashlib.sha256()
    count = 0
    text_count = 0
    for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
        data = path.read_bytes()
        scanner.update_frontend_tree_digest(digest, path.relative_to(root).as_posix(), data)
        count += 1
        if path.suffix.lower() in scanner.TEXT_FRONTEND_SUFFIXES:
            text_count += 1
    return digest.hexdigest(), count, text_count


def telemetry_fixture() -> bytes:
    lines = [
        "# HELP email_adapter_calls_total 邮件供应商 Adapter 调用总数。",
        "# TYPE email_adapter_calls_total counter",
    ]
    for operation, scenes in scanner.TELEMETRY_OPERATIONS.items():
        for scene in sorted(scenes):
            for result in sorted(scanner.TELEMETRY_RESULTS):
                lines.append(
                    f'email_adapter_calls_total{{operation="{operation}",scene="{scene}",result="{result}"}} 1'
                )
    return ("\n".join(lines) + "\n").encode("utf-8")


def unseal_bundle(root: pathlib.Path) -> None:
    for path in [root, *sorted((item for item in root.rglob("*") if item.is_dir()), key=lambda item: len(item.parts))]:
        os.chmod(path, 0o755)
    for path in (item for item in root.rglob("*") if item.is_file()):
        os.chmod(path, 0o644)


def seal_bundle(root: pathlib.Path) -> None:
    for path in (item for item in root.rglob("*") if item.is_file()):
        os.chmod(path, 0o444)
    directories = sorted((item for item in root.rglob("*") if item.is_dir()), key=lambda item: len(item.parts), reverse=True)
    for path in directories:
        os.chmod(path, 0o555)
    os.chmod(root, 0o555)


def fixture_closed_metadata(metadata: os.stat_result) -> bool:
    """Windows 仅用于离线 fixture，不能作为正式 ACL 或运行时通过证据。"""
    if os.name == "nt":
        return bool(getattr(metadata, "st_file_attributes", 0) & scanner.WINDOWS_READONLY_ATTRIBUTE)
    return metadata.st_mode & 0o222 == 0


def fixture_audit_closed_chain(root: pathlib.Path, parts: tuple[str, ...], expect_dir: bool) -> pathlib.Path:
    current = root
    for index, part in enumerate(parts):
        current = current / part
        metadata = current.lstat()
        require(not scanner.is_reparse(metadata) and fixture_closed_metadata(metadata), "fixture_path_contract")
        leaf = index == len(parts) - 1
        require(
            stat.S_ISDIR(metadata.st_mode) if not leaf or expect_dir else stat.S_ISREG(metadata.st_mode),
            "fixture_path_type",
        )
    return current


def fixture_open_regular_descriptor(
    root: pathlib.Path, parts: tuple[str, ...]
) -> tuple[int, list[int], os.stat_result]:
    path = fixture_audit_closed_chain(root, parts, expect_dir=False)
    before = path.lstat()
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_BINARY", 0))
    opened = os.fstat(descriptor)
    require(scanner.identity_tuple(before) == scanner.identity_tuple(opened), "fixture_file_identity")
    return descriptor, [], opened


def fixture_scan_frontend_tree(
    root: pathlib.Path,
    raw: Any,
    start: Any,
    end: Any,
    deployment_sha: str,
) -> tuple[int, int, int]:
    captured = scanner.parse_utc(raw["captured_at_utc"])
    require(start <= captured <= end and raw["deployment_sha"] == deployment_sha, "fixture_frontend_binding")
    tree_parts = scanner.parse_safe_relative(raw["path"])
    tree = fixture_audit_closed_chain(root, tree_parts, expect_dir=True)
    digest = hashlib.sha256()
    file_count = 0
    text_count = 0
    findings = 0
    bytes_scanned = 0
    for path in sorted(candidate for candidate in tree.rglob("*") if candidate.is_file()):
        require(not path.is_symlink() and fixture_closed_metadata(path.lstat()), "fixture_frontend_path")
        data = path.read_bytes()
        relative = path.relative_to(tree).as_posix()
        scanner.update_frontend_tree_digest(digest, relative, data)
        file_count += 1
        scanner.require(file_count <= scanner.MAX_FRONTEND_FILES, "size_limit")
        bytes_scanned += len(data)
        scanner.require(bytes_scanned <= scanner.MAX_FRONTEND_BYTES, "size_limit")
        if path.suffix.lower() in scanner.TEXT_FRONTEND_SUFFIXES:
            text_count += 1
            findings += int(scanner.contains_sensitive(data, "frontend_artifacts"))
    require(file_count == raw["file_count"] and text_count == raw["text_file_count"], "fixture_frontend_count")
    require(digest.hexdigest() == raw["tree_sha256"], "fixture_frontend_hash")
    return file_count, bytes_scanned, findings


@contextlib.contextmanager
def controlled_posix_fixture() -> Any:
    """只让 Windows 离线契约执行逻辑分支，不改变正式平台门禁。"""
    if os.name == "posix":
        yield
        return
    original_platform = scanner.platform_supports_secure_openat
    original_open = scanner.open_regular_descriptor
    original_closed = scanner.is_closed_metadata
    original_frontend = scanner.scan_frontend_tree
    try:
        scanner.platform_supports_secure_openat = lambda: True
        scanner.open_regular_descriptor = fixture_open_regular_descriptor
        scanner.is_closed_metadata = fixture_closed_metadata
        scanner.scan_frontend_tree = fixture_scan_frontend_tree
        yield
    finally:
        scanner.platform_supports_secure_openat = original_platform
        scanner.open_regular_descriptor = original_open
        scanner.is_closed_metadata = original_closed
        scanner.scan_frontend_tree = original_frontend


def build_bundle(base: pathlib.Path) -> pathlib.Path:
    root = base / "bundle"
    root.mkdir()
    public = write_bytes(root, "http/public.json", b'{"route_class":"public","http_status":200,"email_masked":"u***@example.invalid"}')
    admin = write_bytes(root, "http/admin.json", b'{"route_class":"admin","http_status":200,"request_id_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}')
    log = write_bytes(root, "runtime/application.log", b"level=info scene=register outcome=accepted target_class=masked\n")
    audit = write_bytes(root, "runtime/audit.json", b'{"action_class":"template_sync","result":"accepted","actor_type":"administrator","sensitive_field_count":0}')
    database = write_bytes(root, "runtime/database.json", b'{"table_class":"email_send_logs","row_count":3,"sensitive_field_count":0,"all_safe":true}')
    telemetry = write_bytes(root, "runtime/telemetry.prom", telemetry_fixture())
    frontend_root = root / "frontend"
    (frontend_root / "assets").mkdir(parents=True)
    (frontend_root / "index.html").write_text("<!doctype html><title>墨灵</title>", encoding="utf-8")
    (frontend_root / "assets" / "app.js").write_text('const status="accepted";', encoding="utf-8")
    frontend_sha, frontend_count, frontend_text_count = tree_digest(frontend_root)

    def entry(base: dict[str, Any], role: str, content_type: str) -> dict[str, Any]:
        return {"role": role, **base, "content_type": content_type}

    manifest = {
        "schema": scanner.SCHEMA,
        "collector": {
            "mode": scanner.COLLECTOR_MODE,
            "bundle_id": scanner.bundle_identity(root),
            "bundle_closed": True,
            "stdout_lines": 1,
            "stderr_bytes": 0,
        },
        "window": {"start_utc": "2026-07-31T01:00:00Z", "end_utc": "2026-07-31T01:10:00Z"},
        "deployment_sha": "a" * 40,
        "surfaces": {
            "http_responses": [
                entry(public, "public_get", "application/json"),
                entry(admin, "admin_get", "application/json"),
            ],
            "application_logs": [entry(log, "application_log", "text/plain")],
            "audit_projection": [entry(audit, "audit_safe_projection", "application/json")],
            "database_projection": [entry(database, "database_safe_projection", "application/json")],
            "telemetry": [entry(telemetry, "prometheus_metrics", "text/plain")],
            "frontend_artifacts": {
                "role": "deployed_frontend_root",
                "path": "frontend",
                "tree_sha256": frontend_sha,
                "file_count": frontend_count,
                "text_file_count": frontend_text_count,
                "captured_at_utc": "2026-07-31T01:06:00Z",
                "deployment_sha": "a" * 40,
            },
        },
    }
    manifest_path = root / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
    seal_bundle(root)
    return manifest_path


def load_manifest(path: pathlib.Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def save_manifest(path: pathlib.Path, manifest: dict[str, Any]) -> None:
    path.write_text(json.dumps(manifest, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")


def refresh_file_entry(root: pathlib.Path, entry: dict[str, Any], data: bytes) -> None:
    path = root.joinpath(*pathlib.PurePosixPath(entry["path"]).parts)
    path.write_bytes(data)
    entry["sha256"] = sha256(data)


def refresh_frontend(root: pathlib.Path, manifest: dict[str, Any]) -> None:
    digest, count, text_count = tree_digest(root / "frontend")
    frontend = manifest["surfaces"]["frontend_artifacts"]
    frontend["tree_sha256"] = digest
    frontend["file_count"] = count
    frontend["text_file_count"] = text_count


def run_scanner(
    manifest: pathlib.Path,
    optimized: bool = False,
    manifest_sha256: str | None = None,
    deployment_sha: str = "a" * 40,
    bundle_id: str | None = None,
) -> subprocess.CompletedProcess[str]:
    command = [sys.executable]
    if optimized:
        command.append("-O")
    expected_manifest_sha = manifest_sha256 or sha256(manifest.read_bytes())
    expected_bundle_id = bundle_id or scanner.bundle_identity(manifest.parent)
    values = [
        "--manifest", f"{manifest.parent.name}/{manifest.name}",
        "--manifest-sha256", expected_manifest_sha,
        "--deployment-sha", deployment_sha,
        "--bundle-id", expected_bundle_id,
        "--collector-mode", scanner.COLLECTOR_MODE,
    ]
    command.extend(["-B", str(SCANNER), *values])
    if os.name != "posix":
        old_cwd = pathlib.Path.cwd()
        os.chdir(manifest.parent.parent)
        try:
            with controlled_posix_fixture():
                code, line = scanner.execute(values)
        finally:
            os.chdir(old_cwd)
        return subprocess.CompletedProcess(command, code, line + "\n", "")
    environment = os.environ.copy()
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    return subprocess.run(
        command,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
        timeout=20,
        env=environment,
        cwd=manifest.parent.parent,
    )


def run_raw_scanner(cwd: pathlib.Path, values: list[str]) -> subprocess.CompletedProcess[str]:
    environment = os.environ.copy()
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    return subprocess.run(
        [sys.executable, "-B", str(SCANNER), *values],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
        timeout=20,
        env=environment,
        cwd=cwd,
    )


def scanner_args(manifest: pathlib.Path) -> list[str]:
    return [
        "--manifest", f"{manifest.parent.name}/{manifest.name}",
        "--manifest-sha256", sha256(manifest.read_bytes()),
        "--deployment-sha", "a" * 40,
        "--bundle-id", scanner.bundle_identity(manifest.parent),
        "--collector-mode", scanner.COLLECTOR_MODE,
    ]


def execute_scanner_local(manifest: pathlib.Path) -> tuple[int, str]:
    old_cwd = pathlib.Path.cwd()
    os.chdir(manifest.parent.parent)
    try:
        with controlled_posix_fixture():
            return scanner.execute(scanner_args(manifest))
    finally:
        os.chdir(old_cwd)


def require_fixed_result(result: subprocess.CompletedProcess[str], expect_pass: bool) -> None:
    require(result.stderr == "", "scanner_stderr_not_empty")
    require(result.stdout.count("\n") == 1, "scanner_stdout_not_single_line")
    if expect_pass:
        require(result.returncode == 0 and PASS_RE.fullmatch(result.stdout) is not None, "scanner_pass_summary_invalid")
    else:
        require(
            result.returncode == 2
            and (FAIL_RE.fullmatch(result.stdout) is not None or SENSITIVE_FAIL_RE.fullmatch(result.stdout) is not None),
            "scanner_fail_summary_invalid",
        )


def mutate_manifest(
    manifest_path: pathlib.Path,
    mutator: Callable[[dict[str, Any]], None],
) -> None:
    unseal_bundle(manifest_path.parent)
    manifest = load_manifest(manifest_path)
    mutator(manifest)
    save_manifest(manifest_path, manifest)
    seal_bundle(manifest_path.parent)


def run_attack_cases() -> int:
    cases = 0
    secret_values = [
        "private" + "@" + "corp.invalid.cn",
        "182" + "12345678",
        "eyJ" + "a" * 12 + "." + "b" * 12 + "." + "c" * 12,
        "-----BEGIN " + "PRIVATE KEY-----",
        "LTAI" + "7" * 16,
        "Authorization: Bearer " + "T" * 24,
        "refresh_token=" + "R" * 24,
        "client_secret=" + "S3cretValue987654",
        "verification_code=654321",
        "code=654321",
        "request_id=provider-request-12345",
        "provider_request_id=provider-request-12345",
        "provider_raw=hidden-provider-payload",
    ]
    for value in secret_values:
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            manifest_path = build_bundle(root)
            bundle = manifest_path.parent
            manifest = load_manifest(manifest_path)
            entry = manifest["surfaces"]["application_logs"][0]
            unseal_bundle(bundle)
            refresh_file_entry(bundle, entry, value.encode("utf-8"))
            save_manifest(manifest_path, manifest)
            seal_bundle(bundle)
            result = run_scanner(manifest_path)
            require_fixed_result(result, False)
            require(value not in result.stdout and value not in result.stderr, "secret_echoed")
            cases += 1

    projection_attacks = (
        {"email": "masked-value"},
        {"token": "present"},
        {"request_id": "masked-value"},
        {"audit": {("provider_" + "message"): "masked-value"}},
        {"user": "masked-value"},
        {"admin": "masked-value"},
        {"business": "masked-value"},
        {"provider": "masked-value"},
        {"template": "masked-value"},
        {"recipient": "masked-value"},
        {"client_ip": "masked-value"},
        {"provider_request_id": "masked-value"},
    )
    for payload in projection_attacks:
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            manifest_path = build_bundle(root)
            bundle = manifest_path.parent
            manifest = load_manifest(manifest_path)
            entry = manifest["surfaces"]["audit_projection"][0]
            unseal_bundle(bundle)
            refresh_file_entry(bundle, entry, json.dumps(payload).encode("utf-8"))
            save_manifest(manifest_path, manifest)
            seal_bundle(bundle)
            require_fixed_result(run_scanner(manifest_path), False)
            cases += 1

    def spoof_deployment(value: dict[str, Any]) -> None:
        forged = "b" * 40
        value["deployment_sha"] = forged
        for surface in scanner.FILE_SURFACES:
            for entry in value["surfaces"][surface]:
                entry["deployment_sha"] = forged
        value["surfaces"]["frontend_artifacts"]["deployment_sha"] = forged

    manifest_mutations: tuple[Callable[[dict[str, Any]], None], ...] = (
        lambda value: value.update({"unexpected": True}),
        lambda value: value.update({"schema": "wrong"}),
        lambda value: value["collector"].update({"mode": "untrusted"}),
        lambda value: value["collector"].update({"stdout_lines": 2}),
        lambda value: value["collector"].update({"stderr_bytes": 1}),
        lambda value: value["collector"].update({"bundle_id": "0" * 64}),
        lambda value: value["window"].update({"start_utc": "2026-07-31T01:00:00+00:00"}),
        lambda value: value["window"].update({"end_utc": "2026-07-31T01:31:00Z"}),
        lambda value: value["surfaces"]["application_logs"][0].update({"captured_at_utc": "2026-07-31T01:11:00Z"}),
        lambda value: value["surfaces"]["telemetry"][0].update({"deployment_sha": "b" * 40}),
        lambda value: value["surfaces"]["database_projection"][0].update({"sha256": "0" * 64}),
        lambda value: value["surfaces"].pop("audit_projection"),
        lambda value: value["surfaces"]["http_responses"].pop(),
        lambda value: value["surfaces"]["http_responses"].append(copy.deepcopy(value["surfaces"]["http_responses"][0])),
        lambda value: value["surfaces"]["application_logs"][0].update({"path": "../escape.log"}),
        lambda value: value["surfaces"]["frontend_artifacts"].update({"tree_sha256": "0" * 64}),
        lambda value: value["surfaces"]["frontend_artifacts"].update({"file_count": 99}),
        lambda value: value["surfaces"]["frontend_artifacts"].update({"text_file_count": 99}),
        spoof_deployment,
    )
    for mutation in manifest_mutations:
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            manifest_path = build_bundle(root)
            mutate_manifest(manifest_path, mutation)
            require_fixed_result(run_scanner(manifest_path), False)
            cases += 1

    frontend_values = ("admin@corp.invalid.cn", "request_id=frontend-raw-123")
    for value in frontend_values:
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            manifest_path = build_bundle(root)
            bundle = manifest_path.parent
            manifest = load_manifest(manifest_path)
            unseal_bundle(bundle)
            (bundle / "frontend" / "assets" / "app.js").write_text(value, encoding="utf-8")
            refresh_frontend(bundle, manifest)
            save_manifest(manifest_path, manifest)
            seal_bundle(bundle)
            result = run_scanner(manifest_path)
            require_fixed_result(result, False)
            require(value not in result.stdout, "frontend_secret_echoed")
            cases += 1

    unsafe_cli_paths = (
        "C:/bundle/manifest.json",
        "C:\\bundle\\manifest.json",
        "//server/share/manifest.json",
        "\\\\server\\share\\manifest.json",
        "/rooted/manifest.json",
        "\\rooted\\manifest.json",
        "bundle/manifest.json:stream",
        "\\\\?\\C:\\bundle\\manifest.json",
        "bundle/CON.json",
    )
    with tempfile.TemporaryDirectory() as raw:
        cwd = pathlib.Path(raw)
        for unsafe in unsafe_cli_paths:
            values = [
                "--manifest", unsafe,
                "--manifest-sha256", "0" * 64,
                "--deployment-sha", "a" * 40,
                "--bundle-id", "0" * 64,
                "--collector-mode", scanner.COLLECTOR_MODE,
            ]
            require_fixed_result(run_raw_scanner(cwd, values), False)
            cases += 1

    unsafe_entry_paths = (
        "C:/runtime/application.log",
        "C:\\runtime\\application.log",
        "//server/share/application.log",
        "/runtime/application.log",
        "runtime/application.log:stream",
        "runtime\\application.log",
        "runtime/NUL.log",
    )
    for unsafe in unsafe_entry_paths:
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = build_bundle(pathlib.Path(raw))
            mutate_manifest(
                manifest_path,
                lambda value, path=unsafe: value["surfaces"]["application_logs"][0].update({"path": path}),
            )
            require_fixed_result(run_scanner(manifest_path), False)
            cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        manifest_path = build_bundle(base)
        original_sha = sha256(manifest_path.read_bytes())
        mutate_manifest(manifest_path, lambda value: value["window"].update({"end_utc": "2026-07-31T01:09:59Z"}))
        require_fixed_result(run_scanner(manifest_path, manifest_sha256=original_sha), False)
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        manifest_path = build_bundle(base)
        original_bundle_id = scanner.bundle_identity(manifest_path.parent)
        old_bundle = base / "bundle_original"
        manifest_path.parent.rename(old_bundle)
        replacement = build_bundle(base)
        require_fixed_result(run_scanner(replacement, bundle_id=original_bundle_id), False)
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        manifest_path = build_bundle(base)
        bundle = manifest_path.parent
        unseal_bundle(bundle)
        original_log = bundle / "runtime" / "application.original"
        (bundle / "runtime" / "application.log").rename(original_log)
        os.symlink(original_log, bundle / "runtime" / "application.log")
        seal_bundle(bundle)
        require_fixed_result(run_scanner(manifest_path), False)
        cases += 1

    fake_symlink = types.SimpleNamespace(st_mode=stat.S_IFLNK, st_file_attributes=0)
    fake_junction = types.SimpleNamespace(st_mode=stat.S_IFDIR, st_file_attributes=scanner.WINDOWS_REPARSE_ATTRIBUTE)
    require(scanner.is_reparse(fake_symlink), "symlink_model_not_rejected")
    require(scanner.is_reparse(fake_junction), "junction_model_not_rejected")
    cases += 2

    telemetry_attacks = (
        lambda data: data.replace(b'scene="register"', b'scene="user_12345"', 1),
        lambda data: data.replace(b'result="accepted"', b'result="accepted",recipient="masked"', 1),
        lambda data: data.replace(b"email_adapter_calls_total", b"email_user_identifier_total", 1),
    )
    for mutate_telemetry in telemetry_attacks:
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = build_bundle(pathlib.Path(raw))
            bundle = manifest_path.parent
            manifest = load_manifest(manifest_path)
            entry = manifest["surfaces"]["telemetry"][0]
            original = bundle.joinpath(*pathlib.PurePosixPath(entry["path"]).parts).read_bytes()
            unseal_bundle(bundle)
            refresh_file_entry(bundle, entry, mutate_telemetry(original))
            save_manifest(manifest_path, manifest)
            seal_bundle(bundle)
            require_fixed_result(run_scanner(manifest_path), False)
            cases += 1

    with tempfile.TemporaryDirectory() as raw:
        manifest_path = build_bundle(pathlib.Path(raw))
        require_fixed_result(run_scanner(manifest_path, bundle_id="0" * 64), False)
        cases += 1

    original_surface_limit = scanner.MAX_SURFACE_BYTES
    original_frontend_limit = scanner.MAX_FRONTEND_BYTES
    original_bundle_limit = scanner.MAX_BUNDLE_BYTES
    try:
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = build_bundle(pathlib.Path(raw))
            scanner.MAX_SURFACE_BYTES = 1
            code, output = execute_scanner_local(manifest_path)
            require(code == 2 and "classification=size_limit" in output, "surface_size_limit_missing")
            cases += 1
        scanner.MAX_SURFACE_BYTES = original_surface_limit
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = build_bundle(pathlib.Path(raw))
            scanner.MAX_FRONTEND_BYTES = 1
            code, output = execute_scanner_local(manifest_path)
            require(code == 2 and "classification=size_limit" in output, "frontend_size_limit_missing")
            cases += 1
        scanner.MAX_FRONTEND_BYTES = original_frontend_limit
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = build_bundle(pathlib.Path(raw))
            scanner.MAX_BUNDLE_BYTES = 1
            code, output = execute_scanner_local(manifest_path)
            require(code == 2 and "classification=size_limit" in output, "bundle_size_limit_missing")
            cases += 1
    finally:
        scanner.MAX_SURFACE_BYTES = original_surface_limit
        scanner.MAX_FRONTEND_BYTES = original_frontend_limit
        scanner.MAX_BUNDLE_BYTES = original_bundle_limit
    return cases


def test_internal_output_gate() -> int:
    cases = 0
    original = scanner.scan_bundle
    secret = "internal" + "@" + "corp.invalid.cn"
    try:
        setattr(scanner, "scan_bundle", lambda _path: (print(secret), original(_path))[1])
        with tempfile.TemporaryDirectory() as raw:
            manifest = build_bundle(pathlib.Path(raw))
            code, output = execute_scanner_local(manifest)
            require(code == 2 and "classification=unexpected_output" in output, "stdout_gate_missing")
            require(secret not in output, "internal_stdout_echoed")
            cases += 1
    finally:
        scanner.scan_bundle = original

    try:
        def emit_stderr(_path: pathlib.Path) -> scanner.ScanResult:
            print(secret, file=sys.stderr)
            return original(_path)

        setattr(scanner, "scan_bundle", emit_stderr)
        with tempfile.TemporaryDirectory() as raw:
            manifest = build_bundle(pathlib.Path(raw))
            code, output = execute_scanner_local(manifest)
            require(code == 2 and "classification=unexpected_output" in output, "stderr_gate_missing")
            require(secret not in output, "internal_stderr_echoed")
            cases += 1
    finally:
        scanner.scan_bundle = original

    try:
        setattr(scanner, "scan_bundle", lambda _path: (_ for _ in ()).throw(RuntimeError(secret)))
        code, output = scanner.execute([
            "--manifest", "bundle/manifest.json",
            "--manifest-sha256", "0" * 64,
            "--deployment-sha", "a" * 40,
            "--bundle-id", "0" * 64,
            "--collector-mode", scanner.COLLECTOR_MODE,
        ])
        require(code == 2 and "classification=internal_error" in output, "exception_gate_missing")
        require(secret not in output, "exception_text_echoed")
        cases += 1
    finally:
        scanner.scan_bundle = original
    return cases


def test_frontend_dist_compatibility() -> int:
    """直接只读当前两端 dist，不把构建产物复制进攻击 fixture。"""
    roots = (
        HERE.parents[1] / "web" / "admin-console" / "dist",
        HERE.parents[1] / "web" / "user-console" / "dist",
    )
    scanned = 0
    for root in roots:
        require(root.is_dir(), "frontend_dist_missing")
        for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
            if path.suffix.lower() not in scanner.TEXT_FRONTEND_SUFFIXES:
                continue
            data = path.read_bytes()
            require(not scanner.contains_sensitive(data, "frontend_artifacts"), "frontend_dist_false_positive")
            scanned += 1
    require(scanned > 0, "frontend_dist_text_missing")

    ordinary = b"const user={};const template={};const provider={};const admin={};"
    require(not scanner.contains_sensitive(ordinary, "frontend_artifacts"), "frontend_business_key_false_positive")
    secrets = (
        ("owner" + "@" + "corp.invalid.cn").encode(),
        ("182" + "12345678").encode(),
        ("eyJ" + "a" * 12 + "." + "b" * 12 + "." + "c" * 12).encode(),
        ("LTAI" + "7" * 16).encode(),
        ("-----BEGIN " + "PRIVATE KEY-----").encode(),
        b"code=654321",
        ("client_" + "secret=" + "S3cretValue987654").encode(),
    )
    require(all(scanner.contains_sensitive(value, "frontend_artifacts") for value in secrets), "frontend_secret_variant_missed")
    return 2


def test_platform_root_exchange() -> None:
    """验证 bundle 根目录在首次打开后被交换时，扫描器按身份变化拒绝。"""
    require(os.name == "posix", "platform_not_supported")
    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        manifest = build_bundle(base)
        values = scanner_args(manifest)
        original_open = os.open
        original_platform = scanner.platform_supports_secure_openat
        exchanged = False

        def exchange_after_root_open(path: Any, flags: int, *args: Any, **kwargs: Any) -> int:
            nonlocal exchanged
            descriptor = original_open(path, flags, *args, **kwargs)
            if not exchanged and pathlib.Path(path) == manifest.parent:
                exchanged = True
                manifest.parent.rename(base / "bundle_original")
                build_bundle(base)
            return descriptor

        old_cwd = pathlib.Path.cwd()
        os.chdir(base)
        try:
            scanner.os.open = exchange_after_root_open
            scanner.platform_supports_secure_openat = lambda: True
            code, output = scanner.execute(values)
        finally:
            scanner.os.open = original_open
            scanner.platform_supports_secure_openat = original_platform
            os.chdir(old_cwd)
        require(exchanged, "posix_exchange_not_triggered")
        require(code == 2 and "classification=bundle_identity" in output, "posix_exchange_not_rejected")


def test_platform_intermediate_exchange(target: str, timing: str, replacement: str) -> None:
    """验证前端中间目录交换不会越界枚举替换目录。"""
    require(os.name == "posix", "platform_not_supported")
    require(
        (target, timing, replacement) in {
            ("frontend", "before", "symlink"),
            ("frontend", "after", "alternative"),
            ("assets", "before", "symlink"),
            ("assets", "after", "alternative"),
        },
        "intermediate_exchange_case_not_frozen",
    )
    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        manifest = build_bundle(base)
        bundle = manifest.parent
        target_path = bundle / "frontend" if target == "frontend" else bundle / "frontend" / "assets"
        parent_path = target_path.parent
        outside = base / f"outside_{target}_{timing}"
        outside.mkdir()
        (outside / "outside.js").write_text("const outside=true;", encoding="utf-8")
        seal_bundle(outside)
        values = scanner_args(manifest)
        original_open = os.open
        original_scandir = os.scandir
        original_platform = scanner.platform_supports_secure_openat
        exchanged = False
        outside_enumerated = False
        replacement_readonly = False
        outside_identity = (outside.stat().st_dev, outside.stat().st_ino)

        def exchange() -> None:
            nonlocal exchanged, replacement_readonly
            os.chmod(parent_path, 0o755)
            completed = False
            try:
                target_path.rename(parent_path / f"{target_path.name}.original")
                if replacement == "symlink":
                    os.symlink(outside, target_path, target_is_directory=True)
                else:
                    os.chmod(outside, 0o755)
                    outside.rename(target_path)
                    os.chmod(target_path, 0o555)
                    replacement_metadata = target_path.stat(follow_symlinks=False)
                    replacement_readonly = (
                        stat.S_ISDIR(replacement_metadata.st_mode)
                        and replacement_metadata.st_mode & 0o222 == 0
                    )
                exchanged = True
                completed = True
            except BaseException:
                # 异常路径只尽力恢复临时目录的可清理权限，不能覆盖原始失败。
                for candidate in (outside, target_path, parent_path / f"{target_path.name}.original"):
                    try:
                        if candidate.exists() and not candidate.is_symlink():
                            os.chmod(candidate, 0o755)
                    except OSError:
                        pass
                try:
                    os.chmod(parent_path, 0o755)
                except OSError:
                    pass
                raise
            finally:
                if completed:
                    os.chmod(parent_path, 0o555)

        def exchange_open(path: Any, flags: int, *args: Any, **kwargs: Any) -> int:
            is_target = path == target_path.name and kwargs.get("dir_fd") is not None and not exchanged
            if is_target and timing == "before":
                exchange()
            descriptor = original_open(path, flags, *args, **kwargs)
            if is_target and timing == "after":
                exchange()
            return descriptor

        def guarded_scandir(path: Any) -> Any:
            nonlocal outside_enumerated
            if isinstance(path, int):
                metadata = os.fstat(path)
                if (metadata.st_dev, metadata.st_ino) == outside_identity:
                    outside_enumerated = True
            return original_scandir(path)

        old_cwd = pathlib.Path.cwd()
        os.chdir(base)
        try:
            scanner.os.open = exchange_open
            scanner.os.scandir = guarded_scandir
            scanner.platform_supports_secure_openat = lambda: True
            code, output = scanner.execute(values)
        finally:
            scanner.os.open = original_open
            scanner.os.scandir = original_scandir
            scanner.platform_supports_secure_openat = original_platform
            os.chdir(old_cwd)
        require("classification=platform_not_supported" not in output, "intermediate_exchange_platform_gate_bypassed")
        require(exchanged and code == 2, "intermediate_exchange_not_rejected")
        require("classification=sensitive_finding" not in output, "outside_content_scanned")
        if replacement == "alternative":
            require(replacement_readonly, "alternative_exchange_not_readonly")
        if replacement == "symlink" or timing == "after":
            require(not outside_enumerated, "outside_directory_enumerated")


def test_platform_boundary() -> tuple[int, str]:
    """非 POSIX 只验证正式早停；父目录并发交换仅在真实 POSIX 执行。"""
    if os.name != "posix":
        with tempfile.TemporaryDirectory() as raw:
            values = [
                "--manifest", "bundle/manifest.json",
                "--manifest-sha256", "0" * 64,
                "--deployment-sha", "a" * 40,
                "--bundle-id", "0" * 64,
                "--collector-mode", scanner.COLLECTOR_MODE,
            ]
            result = run_raw_scanner(pathlib.Path(raw), values)
            require_fixed_result(result, False)
            require("classification=platform_not_supported" in result.stdout, "nonposix_not_failed_before_access")
        return 1, "skipped_nonposix"

    test_platform_root_exchange()
    test_platform_intermediate_exchange("frontend", "before", "symlink")
    test_platform_intermediate_exchange("frontend", "after", "alternative")
    test_platform_intermediate_exchange("assets", "before", "symlink")
    test_platform_intermediate_exchange("assets", "after", "alternative")
    return 1, "pass"


def static_contract() -> int:
    source = SCANNER.read_text(encoding="utf-8")
    tree = ast.parse(source, str(SCANNER))
    compile(tree, str(SCANNER), "exec")
    allowed_imports = {
        "contextlib", "datetime", "hashlib", "io", "json", "os", "pathlib",
        "re", "stat", "sys", "dataclasses", "typing", "__future__",
    }
    forbidden_roots = {
        "ctypes", "subprocess", "socket", "requests", "urllib", "http", "ftplib",
        "pymysql", "mysql", "redis", "paramiko", "asyncssh", "shutil",
    }
    forbidden_calls = {
        "__import__", "eval", "exec", "compile", "open", "input", "setattr",
        "remove", "unlink", "rmdir", "removedirs", "rename", "replace", "renames",
        "write_text", "write_bytes", "writelines", "truncate", "touch",
        "mkdir", "makedirs", "chmod", "chown", "system", "popen", "spawnl", "spawnv",
        "startfile", "connect", "send", "sendall", "request", "urlopen",
    }
    allowed_getattr_names = {
        "O_BINARY", "O_NOFOLLOW", "O_DIRECTORY", "st_file_attributes",
    }
    forbidden_attributes = forbidden_calls - {"compile", "open", "replace"}

    def attribute_root(node: ast.AST) -> str | None:
        current = node
        while isinstance(current, ast.Attribute):
            current = current.value
        return current.id if isinstance(current, ast.Name) else None

    stdout_writes = 0
    readonly_opens = 0
    flag_assignments = {
        target.id: ast.unparse(node.value)
        for node in ast.walk(tree)
        if isinstance(node, ast.Assign)
        for target in node.targets
        if isinstance(target, ast.Name) and target.id in {"flags", "directory_flags"}
    }
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            require(all(alias.name.split(".", 1)[0] in allowed_imports for alias in node.names), "import_not_whitelisted")
        elif isinstance(node, ast.ImportFrom):
            require((node.module or "").split(".", 1)[0] in allowed_imports, "import_not_whitelisted")
        elif isinstance(node, ast.Call):
            if isinstance(node.func, ast.Name):
                require(node.func.id not in forbidden_calls, "dynamic_or_write_call")
                if node.func.id == "getattr":
                    require(
                        len(node.args) >= 2
                        and isinstance(node.args[1], ast.Constant)
                        and node.args[1].value in allowed_getattr_names,
                        "dynamic_attribute_not_whitelisted",
                    )
            elif isinstance(node.func, ast.Attribute):
                root = attribute_root(node.func)
                require(root not in forbidden_roots, "external_module_call")
                require(node.func.attr not in forbidden_attributes, "write_or_external_call")
                require(node.func.attr != "__dict__", "dynamic_attribute_not_whitelisted")
                if node.func.attr == "compile":
                    require(root == "re", "dynamic_or_write_call")
                if node.func.attr == "replace":
                    require(ast.unparse(node.func) == "parsed.replace", "write_or_external_call")
                if node.func.attr == "open":
                    require(root == "os", "write_or_external_call")
                if node.func.attr == "write":
                    require(
                        root == "sys"
                        and isinstance(node.func.value, ast.Attribute)
                        and node.func.value.attr == "stdout",
                        "write_or_external_call",
                    )
                    stdout_writes += 1
                if root == "os" and node.func.attr == "open":
                    require(len(node.args) >= 2, "os_open_flags_missing")
                    flags = ast.unparse(node.args[1])
                    flags = flag_assignments.get(flags, flags)
                    require(
                        "O_RDONLY" in flags
                        and not any(name in flags for name in ("O_WRONLY", "O_RDWR", "O_CREAT", "O_TRUNC", "O_APPEND")),
                        "os_open_not_readonly",
                    )
                    readonly_opens += 1
    require(readonly_opens >= 2 and stdout_writes == 1, "read_boundary_not_frozen")
    require("writes=false restart=false deploy=false mail_sent=false" in source, "side_effect_summary_missing")
    require("redirect_stdout" in source and "redirect_stderr" in source, "unexpected_output_gate_missing")
    require("MAX_SURFACE_BYTES" in source and "MAX_FRONTEND_BYTES" in source and "MAX_BUNDLE_BYTES" in source, "size_limits_missing")
    require("os.scandir" in source and ".rglob(" not in source, "frontend_streaming_contract_missing")
    require("COLLECTOR_MODE" in source and "manifest_sha256" in source and "bundle_id" in source, "collector_identity_gate_missing")
    require('os.name == "posix"' in source and "os.scandir in os.supports_fd" in source, "posix_only_gate_missing")
    require("dir_fd=directory" in source and "if os.name != \"nt\"" not in source, "formal_openat_chain_missing")
    require(source.count(".lstat()") == 1 and "root.lstat()" in source, "subpath_lstat_forbidden")
    scandir_calls = [
        node for node in ast.walk(tree)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Attribute)
        and attribute_root(node.func) == "os"
        and node.func.attr == "scandir"
    ]
    require(
        len(scandir_calls) == 1
        and len(scandir_calls[0].args) == 1
        and isinstance(scandir_calls[0].args[0], ast.Name)
        and scandir_calls[0].args[0].id == "directory",
        "path_scandir_forbidden",
    )
    scan_function = next(
        node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "scan_bundle"
    )
    require(
        isinstance(scan_function.body[0], ast.Expr)
        and "platform_supports_secure_openat" in ast.unparse(scan_function.body[0]),
        "platform_gate_not_first",
    )
    frontend_function = next(
        node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "scan_frontend_tree"
    )
    frontend_platform_calls = [
        node for node in ast.walk(frontend_function)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name)
        and node.func.id == "platform_supports_secure_openat"
    ]
    frontend_supports_fd_references = [
        node for node in ast.walk(frontend_function)
        if isinstance(node, ast.Attribute) and node.attr == "supports_fd"
    ]
    require(
        len(frontend_platform_calls) == 1 and not frontend_supports_fd_references,
        "frontend_platform_gate_not_centralized",
    )
    contract_tree = ast.parse(pathlib.Path(__file__).read_text(encoding="utf-8"), __file__)
    platform_function = next(
        node for node in contract_tree.body
        if isinstance(node, ast.FunctionDef) and node.name == "test_platform_boundary"
    )
    platform_calls = [
        ast.unparse(node.value)
        for node in platform_function.body
        if isinstance(node, ast.Expr) and isinstance(node.value, ast.Call)
    ]
    require(
        platform_calls == [
            "test_platform_root_exchange()",
            "test_platform_intermediate_exchange('frontend', 'before', 'symlink')",
            "test_platform_intermediate_exchange('frontend', 'after', 'alternative')",
            "test_platform_intermediate_exchange('assets', 'before', 'symlink')",
            "test_platform_intermediate_exchange('assets', 'after', 'alternative')",
        ],
        "platform_exchange_sequence_not_frozen",
    )
    platform_return = platform_function.body[-1]
    require(
        isinstance(platform_return, ast.Return)
        and ast.unparse(platform_return.value) == "(1, 'pass')",
        "platform_case_aggregation_not_frozen",
    )
    require(tuple(scanner.SURFACES) == (
        "http_responses", "application_logs", "audit_projection",
        "database_projection", "telemetry", "frontend_artifacts",
    ), "six_surfaces_not_frozen")
    return 1


def run_contract() -> tuple[int, str]:
    cases = static_contract()
    with tempfile.TemporaryDirectory() as raw:
        manifest = build_bundle(pathlib.Path(raw))
        require_fixed_result(run_scanner(manifest), True)
        cases += 1
        require_fixed_result(run_scanner(manifest, optimized=True), True)
        cases += 1
    cases += run_attack_cases()
    cases += test_internal_output_gate()
    cases += test_frontend_dist_compatibility()
    platform_cases, exchange_status = test_platform_boundary()
    return cases + platform_cases, exchange_status


def main() -> int:
    try:
        cases, exchange_status = run_contract()
        if os.environ.get(OPTIMIZED_ENV) != "1":
            environment = os.environ.copy()
            environment[OPTIMIZED_ENV] = "1"
            environment["PYTHONDONTWRITEBYTECODE"] = "1"
            optimized = subprocess.run(
                [sys.executable, "-O", "-B", str(pathlib.Path(__file__).resolve())],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                check=False,
                timeout=120,
                env=environment,
            )
            require(optimized.returncode == 0 and optimized.stderr == "", "optimized_contract_failed")
            require(
                re.fullmatch(r"status=pass mode=contract cases=\d+ optimized=true posix_exchange=(?:pass|skipped_nonposix) external_access=false writes=false\n?", optimized.stdout) is not None,
                "optimized_summary_invalid",
            )
        optimized_flag = str(sys.flags.optimize > 0).lower()
        print(
            f"status=pass mode=contract cases={cases} optimized={optimized_flag} posix_exchange={exchange_status} "
            "external_access=false writes=false"
        )
        return 0
    except (ContractFailure, OSError, subprocess.SubprocessError):
        print("status=failed mode=contract classification=offline_contract external_access=false writes=false")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

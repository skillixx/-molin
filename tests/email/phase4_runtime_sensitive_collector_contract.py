#!/usr/bin/env python3
"""验证 Phase 4 受控采集器的公开 CLI、封闭输出和失败清理。"""

from __future__ import annotations

import ast
import contextlib
import hashlib
import importlib
import json
import os
import pathlib
import re
import stat
import subprocess
import sys
import tempfile
from typing import Any, Callable

sys.dont_write_bytecode = True

HERE = pathlib.Path(__file__).resolve().parent
COLLECTOR = HERE / "phase4_runtime_sensitive_collector.py"
SCANNER = HERE / "phase4_runtime_sensitive_scan.py"
FROZEN_COLLECTOR_SHA256 = "6e65a3f80fa61d1fda046576aaa1a15ab1715e21c8e7acb1ed1e058c3fde6f68"
FROZEN_SCANNER_SHA256 = "bdf32624ab145c13b55a210c606964aeaa627ff6a04405ee8b930681b778e2a3"


def bootstrap_fail(reason: str) -> None:
    """在业务模块导入前以固定摘要失败，禁止 traceback 泄露本地路径。"""
    if reason not in {"collector_sha_mismatch", "scanner_sha_mismatch"}:
        reason = "collector_sha_mismatch"
    sys.stdout.write(
        "status=failed mode=collector_contract classification=offline_contract "
        f"failure_stage=preimport_gate failure_reason={reason} "
        "external_access=false persistent_writes=false\n"
    )
    raise SystemExit(1)


# 必须在任何业务模块加载动作前完成两个原始字节门禁；失败直接终止导入。
try:
    collector_bootstrap_sha = hashlib.sha256(COLLECTOR.read_bytes()).hexdigest()
except OSError:
    bootstrap_fail("collector_sha_mismatch")
if collector_bootstrap_sha != FROZEN_COLLECTOR_SHA256:
    bootstrap_fail("collector_sha_mismatch")
try:
    scanner_bootstrap_sha = hashlib.sha256(SCANNER.read_bytes()).hexdigest()
except OSError:
    bootstrap_fail("scanner_sha_mismatch")
if scanner_bootstrap_sha != FROZEN_SCANNER_SHA256:
    bootstrap_fail("scanner_sha_mismatch")

collector = importlib.import_module("phase4_runtime_sensitive_collector")
scanner = importlib.import_module("phase4_runtime_sensitive_scan")

OPTIMIZED_ENV = "MOLIN_PHASE4_COLLECTOR_CONTRACT_OPTIMIZED"
SAFE_LABEL_RE = re.compile(r"\A[a-z][a-z0-9_]{0,63}\Z")
ACTIVE_STAGE = "startup"
PASS_RE = re.compile(
    r"\Astatus=pass mode=collector surfaces=6 files=\d+ bytes=\d+ "
    r"manifest_sha256=([0-9a-f]{64}) bundle_id=([0-9a-f]{64}) "
    r"external_access=false database_access=false redis_access=false env_read=false "
    r"partial_retained=false\Z"
)
FAIL_RE = re.compile(
    r"\Astatus=failed mode=collector classification=([a-z_]+) "
    r"external_access=false database_access=false redis_access=false env_read=false "
    r"partial_retained=(?:false|true)\Z"
)


class ContractFailure(Exception):
    """仅携带固定诊断标签，不携带路径、输入值或系统异常正文。"""

    def __init__(self, classification: str) -> None:
        safe = classification if SAFE_LABEL_RE.fullmatch(classification) is not None else "internal_contract"
        super().__init__(safe)
        self.classification = safe


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def mark_stage(stage: str) -> None:
    """冻结当前离线用例阶段，失败摘要只能返回该安全标签。"""
    global ACTIVE_STAGE
    require(SAFE_LABEL_RE.fullmatch(stage) is not None, "diagnostic_stage_contract")
    ACTIVE_STAGE = stage


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


def write_source(root: pathlib.Path, relative: str, data: bytes) -> pathlib.Path:
    path = root.joinpath(*pathlib.PurePosixPath(relative).parts)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return path


def seal(root: pathlib.Path) -> None:
    for path in (item for item in root.rglob("*") if item.is_file()):
        os.chmod(path, 0o444)
    directories = sorted(
        (item for item in root.rglob("*") if item.is_dir()),
        key=lambda item: len(item.parts),
        reverse=True,
    )
    for path in directories:
        os.chmod(path, 0o555)
    os.chmod(root, 0o555)


def unseal(root: pathlib.Path) -> None:
    os.chmod(root, 0o755)
    for path in (item for item in root.rglob("*") if item.is_dir()):
        os.chmod(path, 0o755)
    for path in (item for item in root.rglob("*") if item.is_file()):
        os.chmod(path, 0o644)


def build_inputs(base: pathlib.Path) -> tuple[pathlib.Path, list[str]]:
    source = base / "source"
    source.mkdir()
    public = write_source(
        source,
        "public.json",
        b'{"route_class":"public","http_status":200,"status":"ok","email_masked":"u***@example.invalid"}',
    )
    admin = write_source(
        source,
        "admin.json",
        b'{"route_class":"admin","http_status":200,"status":"ok","request_id_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}',
    )
    application_log = write_source(
        source,
        "application.log",
        b"level=info scene=register outcome=accepted target_class=masked\n",
    )
    audit = write_source(
        source,
        "audit.json",
        b'{"action_class":"template_sync","actor_type":"administrator","result":"accepted","sensitive_field_count":0}',
    )
    database = write_source(
        source,
        "database.json",
        b'{"table_class":"email_send_logs","row_count":2,"all_masked":true,"all_safe":true,"sensitive_field_count":0}',
    )
    telemetry = write_source(source, "telemetry.prom", telemetry_fixture())
    admin_frontend = source / "admin-frontend"
    user_frontend = source / "user-frontend"
    write_source(admin_frontend, "index.html", b"<html><body>admin</body></html>\n")
    write_source(admin_frontend, "assets/app.js", b"const template={status:'ready'};\n")
    write_source(user_frontend, "index.html", b"<html><body>user</body></html>\n")
    write_source(user_frontend, "assets/app.js", b"const provider={status:'ready'};\n")
    seal(source)
    output = base / "bundle"
    arguments = [
        "--public-get", str(public),
        "--admin-get", str(admin),
        "--application-log", str(application_log),
        "--audit-projection", str(audit),
        "--database-projection", str(database),
        "--telemetry", str(telemetry),
        "--admin-frontend", str(admin_frontend),
        "--user-frontend", str(user_frontend),
        "--output", str(output),
        "--deployment-sha", "a" * 40,
    ]
    return output, arguments


def replace_argument(arguments: list[str], flag: str, value: str) -> list[str]:
    changed = list(arguments)
    changed[changed.index(flag) + 1] = value
    return changed


def execute(arguments: list[str]) -> tuple[int, str]:
    return collector.execute(arguments)


def require_failure(
    code: int,
    line: str,
    output: pathlib.Path,
    classifications: set[str] | None = None,
    expected_retained: bool | None = None,
    stage: str = "failure_case",
) -> str:
    mark_stage(stage)
    match = FAIL_RE.fullmatch(line)
    require(code == 2 and match is not None, "failure_summary_contract")
    classification = match.group(1)
    if classifications is not None:
        require(classification in classifications, "failure_classification_contract")
    require(expected_retained is not None, "retention_expectation_missing")
    require(
        line.endswith(f"partial_retained={str(expected_retained).lower()}"),
        "retention_expectation_mismatch",
    )
    require(not output.exists(), "partial_output_retained")
    return classification


def require_retained_failure(
    code: int,
    line: str,
    output: pathlib.Path,
    classifications: set[str],
    stage: str = "retained_failure_case",
) -> str:
    mark_stage(stage)
    match = FAIL_RE.fullmatch(line)
    require(
        code == 2
        and match is not None
        and match.group(1) in classifications
        and line.endswith("partial_retained=true")
        and output.is_dir(),
        "retention_summary_contract",
    )
    return match.group(1)


def verify_success(base: pathlib.Path, output: pathlib.Path, arguments: list[str]) -> None:
    code, line = execute(arguments)
    match = PASS_RE.fullmatch(line)
    require(code == 0 and match is not None, "collector_success_missing")
    manifest_sha, frozen_bundle_id = match.groups()
    manifest = output / "manifest.json"
    require(hashlib.sha256(manifest.read_bytes()).hexdigest() == manifest_sha, "manifest_sha_mismatch")
    value = json.loads(manifest.read_text(encoding="utf-8"))
    require(set(value["surfaces"]) == set(scanner.SURFACES), "surface_missing")
    require(
        value["surfaces"]["frontend_artifacts"]["file_count"] == 4
        and value["surfaces"]["frontend_artifacts"]["text_file_count"] == 4,
        "frontend_incomplete",
    )
    require(
        value["collector"]["bundle_id"] == frozen_bundle_id
        and value["collector"]["bundle_closed"] is True,
        "collector_identity_mismatch",
    )
    for path in [output, *output.rglob("*")]:
        require(not path.is_symlink() and path.stat().st_mode & 0o222 == 0, "bundle_not_closed")
    old_cwd = pathlib.Path.cwd()
    os.chdir(base)
    try:
        scan_args = [
            "--manifest", "bundle/manifest.json",
            "--manifest-sha256", manifest_sha,
            "--deployment-sha", "a" * 40,
            "--bundle-id", frozen_bundle_id,
            "--collector-mode", scanner.COLLECTOR_MODE,
        ]
        scan_code, scan_line = scanner.execute(scan_args)
    finally:
        os.chdir(old_cwd)
    require(scan_code == 0 and "classification=complete" in scan_line, "scanner_rejected_bundle")


def require_no_delete_capability(source: str) -> None:
    """按调用语义拒绝删除、覆盖路径及可构造此类调用的反射能力。"""
    tree = ast.parse(source)
    dangerous = {"unlink", "rmdir", "remove", "removedirs", "rename"}
    reflection = {"__import__", "eval", "exec"}
    getattr_aliases = {"getattr"}
    direct_dangerous: set[str] = set()
    direct_reflection = set(reflection)
    os_aliases = {"os"}
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name == "os":
                    os_aliases.add(alias.asname or alias.name)
        elif isinstance(node, ast.ImportFrom):
            for alias in node.names:
                local = alias.asname or alias.name
                if node.module == "os" and alias.name in dangerous | {"replace"}:
                    direct_dangerous.add(local)
                if node.module == "builtins" and alias.name in reflection:
                    direct_reflection.add(local)
                if node.module == "builtins" and alias.name == "getattr":
                    getattr_aliases.add(local)
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if isinstance(node.func, ast.Name):
            require(node.func.id not in direct_dangerous, "automatic_cleanup_forbidden")
            require(node.func.id not in direct_reflection, "reflection_forbidden")
            if node.func.id in getattr_aliases and len(node.args) >= 2:
                dangerous_getattr = (
                    isinstance(node.args[0], ast.Name)
                    and node.args[0].id in os_aliases
                    and isinstance(node.args[1], ast.Constant)
                    and node.args[1].value in dangerous | {"replace"}
                )
                require(not dangerous_getattr, "automatic_cleanup_forbidden")
        elif isinstance(node.func, ast.Attribute):
            require(node.func.attr not in dangerous, "automatic_cleanup_forbidden")
            require(
                not (
                    node.func.attr == "replace"
                    and isinstance(node.func.value, ast.Name)
                    and node.func.value.id in os_aliases
                ),
                "automatic_cleanup_forbidden",
            )
            require(node.func.attr not in reflection, "reflection_forbidden")


def require_frozen_collector(data: bytes) -> None:
    """以人工审查后的原始字节摘要作为删除能力边界。"""
    require(hashlib.sha256(data).hexdigest() == FROZEN_COLLECTOR_SHA256, "collector_sha_mismatch")


def static_contract() -> int:
    raw_source = COLLECTOR.read_bytes()
    require_frozen_collector(raw_source)
    source = raw_source.decode("utf-8", errors="strict")
    tree = ast.parse(source, str(COLLECTOR))
    require_no_delete_capability(source)
    forbidden_imports = {
        "socket", "requests", "urllib", "http", "ftplib", "pymysql", "mysql",
        "redis", "paramiko", "asyncssh", "subprocess", "shutil",
    }
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            require(all(alias.name.split(".", 1)[0] not in forbidden_imports for alias in node.names), "external_import")
        elif isinstance(node, ast.ImportFrom):
            require((node.module or "").split(".", 1)[0] not in forbidden_imports, "external_import")
        elif isinstance(node, ast.Attribute):
            require(not (isinstance(node.value, ast.Name) and node.value.id == "os" and node.attr in {"environ", "getenv"}), "env_access")
    require("O_NOFOLLOW" in source and "supports_dir_fd" in source and "os.scandir" in source, "openat_contract_missing")
    require(
        "target_exists" in source
        and "CreatedNode" in source
        and "parent: int" in source
        and "device: int" in source
        and "inode: int" in source
        and "purge_directory" not in source,
        "creation_registry_missing",
    )
    require(
        "os.unlink" not in source
        and "os.rmdir" not in source
        and "partial_retained = True" in source,
        "automatic_cleanup_forbidden",
    )
    require(
        "MAX_SOURCE_NODES" in source
        and "MAX_DIRECTORY_ENTRIES" in source
        and "MAX_FRONTEND_DEPTH" in source
        and "sorted(iterator" not in source,
        "bounded_traversal_missing",
    )
    require(
        "manifest_sha256" in source
        and "bundle_id" in source
        and "partial_retained" in source
        and "error.partial_retained" in source,
        "summary_contract_missing",
    )
    require(
        "ENV_PREFIX" in source
        and "protected_env" in source
        and "for part in parts:" in source
        and "reject_env_name(part)" in source,
        "env_gate_missing",
    )
    require(
        "output_within_source" in source
        and "source_within_output" in source
        and "contract.public_get" in source
        and "contract.user_frontend" in source,
        "input_output_relation_missing",
    )
    require(
        "close_failed" in source and "close_registry" in source and "close_chain(parent)" in source,
        "close_contract_missing",
    )
    return 1


def lexical_contract() -> int:
    allowed = collector.parse_absolute("/tmp/molin/source.json")
    require(allowed == ("tmp", "molin", "source.json"), "absolute_path_rejected")
    rejected = (
        "relative", "../escape", "/tmp/../escape", "/tmp\\escape", "C:/tmp/file", "/",
        "/.env", "/.env/source.json", "/tmp/.env.test/source.json", "/tmp/.env.production/source.json",
    )
    for value in rejected:
        try:
            collector.parse_absolute(value)
        except collector.CollectFailure:
            continue
        raise ContractFailure("unsafe_path_accepted")
    return len(rejected) + 1


def preimport_gate_contract() -> int:
    """隔离证明字节不匹配时，候选模块顶层代码不会获得执行机会。"""
    with tempfile.TemporaryDirectory() as raw:
        root = pathlib.Path(raw)
        collector_candidate = root / "collector_candidate.py"
        scanner_candidate = root / "scanner_candidate.py"
        malicious = "import pathlib,sys\npathlib.Path(sys.argv[1]).write_text('executed')\n"
        safe = "VALUE = 1\n"
        gate = (
            "import hashlib,importlib.util,pathlib,sys\n"
            "def fail(r):\n print('status=failed mode=collector_contract classification=offline_contract failure_stage=preimport_gate failure_reason='+r+' external_access=false persistent_writes=false');raise SystemExit(1)\n"
            "c=pathlib.Path(sys.argv[1]);s=pathlib.Path(sys.argv[2])\n"
            "try: ch=hashlib.sha256(c.read_bytes()).hexdigest()\nexcept OSError: fail('collector_sha_mismatch')\n"
            "if ch!=sys.argv[4]: fail('collector_sha_mismatch')\n"
            "try: sh=hashlib.sha256(s.read_bytes()).hexdigest()\nexcept OSError: fail('scanner_sha_mismatch')\n"
            "if sh!=sys.argv[5]: fail('scanner_sha_mismatch')\n"
            "sys.argv=[str(c),sys.argv[3]]\n"
            "for n,p in [('collector_candidate',c),('scanner_candidate',s)]:\n x=importlib.util.spec_from_file_location(n,p);m=importlib.util.module_from_spec(x);x.loader.exec_module(m)\n"
        )
        cases = 0
        for target in ("collector", "scanner"):
            sentinel = root / f"sentinel-{target}"
            collector_candidate.write_text(malicious if target == "collector" else safe, encoding="utf-8")
            scanner_candidate.write_text(malicious if target == "scanner" else safe, encoding="utf-8")
            collector_expected = "0" * 64 if target == "collector" else hashlib.sha256(collector_candidate.read_bytes()).hexdigest()
            scanner_expected = "0" * 64 if target == "scanner" else hashlib.sha256(scanner_candidate.read_bytes()).hexdigest()
            reason = f"{target}_sha_mismatch"
            expected = (
                "status=failed mode=collector_contract classification=offline_contract "
                f"failure_stage=preimport_gate failure_reason={reason} "
                "external_access=false persistent_writes=false\n"
            )
            result = subprocess.run(
                [sys.executable, "-I", "-B", "-c", gate, str(collector_candidate), str(scanner_candidate), str(sentinel), collector_expected, scanner_expected],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, check=False, timeout=30,
            )
            require(result.returncode == 1, "preimport_gate_exit")
            require(result.stdout == expected and result.stderr == "", "preimport_gate_output")
            require(not sentinel.exists(), "preimport_sentinel_executed")
            cases += 1
    return cases


def retention_contract() -> int:
    """离线证明失败保留策略不存在自动删除能力，且旧算法必定变红。"""
    frozen = COLLECTOR.read_bytes()
    require_frozen_collector(frozen)
    source = frozen.decode("utf-8", errors="strict")
    attacks = (
        "import os\nos.unlink('x')\n",
        "from os import unlink as erase\nerase('x')\n",
        "import os as operating_system\noperating_system.rmdir('x')\n",
        "import os\ngetattr(os, 'unlink')('x')\n",
        "import pathlib\npathlib.Path('x').unlink()\n",
        "import os\nos.remove('x')\n",
        "import os\nos.rename('x', 'y')\n",
        "import os\nos.replace('x', 'y')\n",
        "__import__('os').unlink('x')\n",
        "eval('1')\n",
        "exec('pass')\n",
        "import os\nerase = os.unlink\nerase('x')\n",
        "import os\nvars(os)['unlink']('x')\n",
        "import os\nos.__dict__['unlink']('x')\n",
        "import os\nname = 'unlink'\ngetattr(os, name)('x')\n",
        "from pathlib import Path\nPath('x').replace('y')\n",
        "import os\ng = getattr\ng(os, 'unlink')('x')\n",
        "import os, operator\noperator.attrgetter('unlink')(os)('x')\n",
        "import os\nos.renames('x', 'y')\n",
    )
    for attack in attacks:
        try:
            require_frozen_collector(frozen + attack.encode("utf-8"))
        except ContractFailure as error:
            require(error.classification == "collector_sha_mismatch", "legacy_expected_red_missing")
        else:
            raise ContractFailure("legacy_expected_red_missing")
    require_no_delete_capability(source)
    return len(attacks) + 1


def run_posix_contract() -> int:
    cases = 0
    with tempfile.TemporaryDirectory() as raw:
        mark_stage("successful_collection")
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        verify_success(base, output, arguments)
        cases += 1

    malformed: tuple[tuple[str, bytes, str, str], ...] = (
        ("public.json", b'{"route_class":"public","http_status":200,"details":{"raw":"x"}}', "projection_contract", "malformed_public_projection"),
        (
            "admin.json",
            b'{"route_class":"admin","http_status":200,"raw_' + b'response":"x"}',
            "projection_contract",
            "malformed_admin_projection",
        ),
        ("audit.json", b'{"action_class":"sync","details":"raw"}', "projection_contract", "malformed_audit_projection"),
        ("database.json", b'{"table_class":"logs","recipient_hash":"x"}', "projection_contract", "malformed_database_projection"),
        ("application.log", b"level=info user_id=42\n", "sensitive_source", "sensitive_log_identifier"),
        ("application.log", b"recipient=owner@corp.invalid.cn\n", "sensitive_source", "sensitive_log_recipient"),
    )
    for relative, data, expected, stage in malformed:
        with tempfile.TemporaryDirectory() as raw:
            base = pathlib.Path(raw)
            output, arguments = build_inputs(base)
            source = base / "source"
            unseal(source)
            (source / relative).write_bytes(data)
            seal(source)
            require_failure(*execute(arguments), output, {expected}, expected_retained=False, stage=stage)
            cases += 1

    with tempfile.TemporaryDirectory() as raw:
        mark_stage("preexisting_target")
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        telemetry = (source / "telemetry.prom").read_text(encoding="utf-8")
        telemetry = telemetry.replace('result="accepted"}', 'result="accepted",recipient="masked"}', 1)
        (source / "telemetry.prom").write_text(telemetry, encoding="utf-8")
        seal(source)
        require_failure(*execute(arguments), output, {"telemetry_contract"}, expected_retained=False, stage="telemetry_high_cardinality")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        require_failure(*execute(arguments[:-2]), output, {"argument_contract"}, expected_retained=False, stage="missing_cli_argument")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        unsafe = replace_argument(arguments, "--application-log", "../escape.log")
        require_failure(*execute(unsafe), output, {"path_contract"}, expected_retained=False, stage="relative_input_path")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        protected = source / ".env.test"
        protected.write_text("SECRET=hidden", encoding="utf-8")
        os.chmod(protected, 0o444)
        os.chmod(source, 0o555)
        unsafe = replace_argument(arguments, "--application-log", str(protected))
        require_failure(*execute(unsafe), output, {"protected_env"}, expected_retained=False, stage="protected_env_input")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        output.mkdir()
        sentinel = output / "sentinel"
        sentinel.write_text("owned-by-caller", encoding="utf-8")
        code, line = execute(arguments)
        require(code == 2 and "classification=target_exists" in line, "preexisting_target_not_rejected")
        require(line.endswith("partial_retained=false"), "preexisting_target_retention_mismatch")
        require(sentinel.read_text(encoding="utf-8") == "owned-by-caller", "preexisting_target_modified")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        os.chmod(source / "public.json", 0o644)
        os.chmod(source, 0o555)
        require_failure(*execute(arguments), output, {"source_not_readonly"}, expected_retained=False, stage="writable_source_file")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        (source / "admin-frontend" / ".env.production").write_text("SECRET=hidden", encoding="utf-8")
        seal(source)
        require_retained_failure(
            *execute(arguments), output, {"protected_env"}, stage="protected_env_frontend"
        )
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        target = source / "admin-frontend" / "assets" / "app.js"
        target.unlink()
        os.symlink(source / "user-frontend" / "assets" / "app.js", target)
        seal(source)
        require_retained_failure(
            *execute(arguments),
            output,
            {"source_not_readonly", "source_contract"},
            stage="frontend_symlink",
        )
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        empty_frontend = base / "empty-frontend"
        empty_frontend.mkdir()
        os.chmod(empty_frontend, 0o555)
        unsafe = replace_argument(arguments, "--user-frontend", str(empty_frontend))
        require_retained_failure(
            *execute(unsafe),
            output,
            {"frontend_contract"},
            stage="empty_frontend_after_sealed_tree",
        )
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        nested_output = base / "source" / "admin-frontend" / "bundle"
        unsafe = replace_argument(arguments, "--output", str(nested_output))
        require_failure(*execute(unsafe), nested_output, {"path_contract"}, expected_retained=False, stage="nested_output_path")
        cases += 1

    relation_flags = (
        "--public-get", "--admin-get", "--application-log", "--audit-projection",
        "--database-projection", "--telemetry", "--admin-frontend", "--user-frontend",
    )
    for flag in relation_flags:
        mark_stage(f"relation_{flag[2:].replace('-', '_')}")
        with tempfile.TemporaryDirectory() as raw:
            base = pathlib.Path(raw)
            _output, arguments = build_inputs(base)
            source_value = pathlib.Path(arguments[arguments.index(flag) + 1])
            unsafe_output = source_value / "nested-output"
            unsafe = replace_argument(arguments, "--output", str(unsafe_output))
            code, line = execute(unsafe)
            require(
                code == 2
                and "classification=path_contract" in line
                and source_value.exists(),
                "input_output_relation_not_frozen",
            )
            cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        source = base / "source"
        unseal(source)
        write_source(source / "admin-frontend", "extra-a.js", b"const a=true;\n")
        write_source(source / "admin-frontend", "extra-b.js", b"const b=true;\n")
        seal(source)
        original_limit = collector.MAX_DIRECTORY_ENTRIES
        collector.MAX_DIRECTORY_ENTRIES = 2
        try:
            require_retained_failure(
                *execute(arguments), output, {"size_limit"}, stage="directory_entry_limit"
            )
        finally:
            collector.MAX_DIRECTORY_ENTRIES = original_limit
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_depth = collector.MAX_FRONTEND_DEPTH
        collector.MAX_FRONTEND_DEPTH = 1
        try:
            require_retained_failure(
                *execute(arguments), output, {"frontend_contract"}, stage="frontend_depth_limit"
            )
        finally:
            collector.MAX_FRONTEND_DEPTH = original_depth
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_write = collector.write_file
        original_unlink = collector.os.unlink
        original_rmdir = collector.os.rmdir
        delete_calls: list[str] = []
        calls = 0

        def fail_after_two(
            parent: int,
            name: str,
            data: bytes,
            registry: collector.CreationRegistry,
        ) -> os.stat_result:
            nonlocal calls
            calls += 1
            if calls == 3:
                raise collector.CollectFailure("fault_injected")
            return original_write(parent, name, data, registry)

        collector.write_file = fail_after_two
        collector.os.unlink = lambda *_args, **_kwargs: delete_calls.append("unlink")
        collector.os.rmdir = lambda *_args, **_kwargs: delete_calls.append("rmdir")
        try:
            require_retained_failure(
                *execute(arguments), output, {"fault_injected"}, stage="owned_partial_retained"
            )
        finally:
            collector.write_file = original_write
            collector.os.unlink = original_unlink
            collector.os.rmdir = original_rmdir
        require(delete_calls == [], "automatic_cleanup_called")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_fchmod = collector.os.fchmod
        fchmod_failed = False

        def fail_first_output_fchmod(descriptor: int, mode: int) -> None:
            nonlocal fchmod_failed
            if output.exists() and not fchmod_failed:
                fchmod_failed = True
                raise OSError("fixed-fchmod-fault")
            original_fchmod(descriptor, mode)

        collector.os.fchmod = fail_first_output_fchmod
        try:
            require_retained_failure(
                *execute(arguments),
                output,
                {"output_contract"},
                stage="fchmod_partial_retained",
            )
        finally:
            collector.os.fchmod = original_fchmod
        require(fchmod_failed, "fchmod_fault_not_triggered")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_write = collector.write_file
        calls = 0

        def inject_unknown_then_fail(
            parent: int,
            name: str,
            data: bytes,
            registry: collector.CreationRegistry,
        ) -> os.stat_result:
            nonlocal calls
            metadata = original_write(parent, name, data, registry)
            calls += 1
            if calls == 2:
                intruder = output / "intruder"
                intruder.write_text("not-owned", encoding="utf-8")
                os.chmod(intruder, 0o444)
                raise collector.CollectFailure("fault_injected")
            return metadata

        collector.write_file = inject_unknown_then_fail
        try:
            require_retained_failure(
                *execute(arguments), output, {"fault_injected"}, stage="unknown_entry_retained"
            )
        finally:
            collector.write_file = original_write
        require((output / "intruder").read_text(encoding="utf-8") == "not-owned", "unknown_entry_modified")
        require((output / "http" / "public_get.json").is_file(), "owned_entry_deleted_before_preflight")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_write = collector.write_file
        exchanged = False

        def exchange_created_entry_then_fail(
            parent: int,
            name: str,
            data: bytes,
            registry: collector.CreationRegistry,
        ) -> os.stat_result:
            nonlocal exchanged
            metadata = original_write(parent, name, data, registry)
            if not exchanged:
                created = output / "http" / name
                original = output / "http" / f"{name}.original"
                os.chmod(output / "http", 0o755)
                created.rename(original)
                created.write_bytes(b"replacement")
                os.chmod(created, 0o444)
                os.chmod(output / "http", 0o700)
                exchanged = True
                raise collector.CollectFailure("fault_injected")
            return metadata

        collector.write_file = exchange_created_entry_then_fail
        try:
            require_retained_failure(
                *execute(arguments), output, {"fault_injected"}, stage="replaced_entry_retained"
            )
        finally:
            collector.write_file = original_write
        require(exchanged and (output / "http" / "public_get.json").read_bytes() == b"replacement", "replaced_entry_modified")
        cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        original_close = os.close
        close_failed = False

        def fail_one_close(descriptor: int) -> None:
            nonlocal close_failed
            if output.exists() and not close_failed:
                close_failed = True
                raise OSError("fixed-close-fault")
            original_close(descriptor)

        collector.os.close = fail_one_close
        try:
            require_retained_failure(
                *execute(arguments),
                output,
                {"close_failed"},
                stage="descriptor_close_failure",
            )
        finally:
            collector.os.close = original_close
        require(close_failed, "close_fault_not_triggered")
        cases += 1

    for timing in ("before", "after"):
        with tempfile.TemporaryDirectory() as raw:
            base = pathlib.Path(raw)
            output, arguments = build_inputs(base)
            source = base / "source"
            target = source / "public.json"
            replacement = base / "replacement.json"
            replacement.write_bytes(b'{"route_class":"public","http_status":200,"status":"ok"}')
            os.chmod(replacement, 0o444)
            original_open = os.open
            original_platform = collector.platform_supported
            exchanged = False

            def exchange_open(path: Any, flags: int, *args: Any, **kwargs: Any) -> int:
                nonlocal exchanged
                is_target = path == "public.json" and kwargs.get("dir_fd") is not None and not exchanged
                if is_target and timing == "before":
                    os.chmod(source, 0o755)
                    target.rename(source / "public.original")
                    os.symlink(replacement, target)
                    os.chmod(source, 0o555)
                    exchanged = True
                descriptor = original_open(path, flags, *args, **kwargs)
                if is_target and timing == "after":
                    os.chmod(source, 0o755)
                    target.rename(source / "public.original")
                    replacement.rename(target)
                    os.chmod(source, 0o555)
                    exchanged = True
                return descriptor

            try:
                collector.os.open = exchange_open
                collector.platform_supported = lambda: True
                require_failure(
                    *execute(arguments),
                    output,
                    {"source_contract", "source_identity"},
                    expected_retained=False,
                    stage=f"source_exchange_{timing}",
                )
            finally:
                collector.os.open = original_open
                collector.platform_supported = original_platform
            require(exchanged, "source_exchange_not_triggered")
            cases += 1

    for timing, replacement_kind in (("before", "symlink"), ("after", "alternative")):
        with tempfile.TemporaryDirectory() as raw:
            base = pathlib.Path(raw)
            output, arguments = build_inputs(base)
            target = base / "source" / "admin-frontend" / "assets"
            parent_path = target.parent
            outside = base / "outside-assets"
            write_source(outside, "outside.js", b"const outside=true;\n")
            seal(outside)
            original_open = os.open
            original_platform = collector.platform_supported
            exchanged = False

            def exchange() -> None:
                nonlocal exchanged
                os.chmod(parent_path, 0o755)
                target.rename(parent_path / "assets.original")
                if replacement_kind == "symlink":
                    os.symlink(outside, target, target_is_directory=True)
                else:
                    os.chmod(outside, 0o755)
                    outside.rename(target)
                    os.chmod(target, 0o555)
                os.chmod(parent_path, 0o555)
                exchanged = True

            def exchange_frontend_open(path: Any, flags: int, *args: Any, **kwargs: Any) -> int:
                is_target = path == "assets" and kwargs.get("dir_fd") is not None and not exchanged
                if is_target and timing == "before":
                    exchange()
                descriptor = original_open(path, flags, *args, **kwargs)
                if is_target and timing == "after":
                    exchange()
                return descriptor

            try:
                collector.os.open = exchange_frontend_open
                collector.platform_supported = lambda: True
                require_retained_failure(
                    *execute(arguments),
                    output,
                    {"source_contract", "source_identity", "output_contract"},
                    stage=f"frontend_exchange_{timing}_{replacement_kind}",
                )
            finally:
                collector.os.open = original_open
                collector.platform_supported = original_platform
            require(exchanged, "frontend_exchange_not_triggered")
            cases += 1

    with tempfile.TemporaryDirectory() as raw:
        base = pathlib.Path(raw)
        output, arguments = build_inputs(base)
        frontend = base / "source" / "admin-frontend"
        frontend_identity = (frontend.stat().st_dev, frontend.stat().st_ino)
        original_scandir = os.scandir
        original_platform = collector.platform_supported
        injected = False

        class InjectingIterator:
            def __init__(self, path: Any) -> None:
                self.path = path
                self.inner = original_scandir(path)
                self.is_target = isinstance(path, int) and (
                    os.fstat(path).st_dev,
                    os.fstat(path).st_ino,
                ) == frontend_identity

            def __enter__(self) -> "InjectingIterator":
                self.inner.__enter__()
                return self

            def __exit__(self, *values: Any) -> Any:
                return self.inner.__exit__(*values)

            def __iter__(self) -> "InjectingIterator":
                return self

            def __next__(self) -> os.DirEntry[str]:
                nonlocal injected
                try:
                    return next(self.inner)
                except StopIteration:
                    if self.is_target and not injected:
                        os.chmod(frontend, 0o755)
                        added = frontend / "injected.js"
                        added.write_text("const injected=true;\n", encoding="utf-8")
                        os.chmod(added, 0o444)
                        os.chmod(frontend, 0o555)
                        injected = True
                    raise

        try:
            collector.os.scandir = lambda path: InjectingIterator(path)
            collector.platform_supported = lambda: True
            require_retained_failure(
                *execute(arguments),
                output,
                {"source_identity"},
                stage="frontend_entry_injection",
            )
        finally:
            collector.os.scandir = original_scandir
            collector.platform_supported = original_platform
        require(injected, "frontend_entry_injection_not_triggered")
        cases += 1
    return cases


def run_contract() -> tuple[int, str]:
    mark_stage("static_contract")
    cases = static_contract() + lexical_contract()
    mark_stage("preimport_gate_contract")
    cases += preimport_gate_contract()
    mark_stage("retention_contract")
    cases += retention_contract()
    if os.name != "posix":
        code, line = collector.execute([])
        require(code == 2 and "classification=argument_contract" in line, "nonposix_fixed_failure_missing")
        return cases + 1, "skipped_nonposix"
    return cases + run_posix_contract(), "pass"


def main() -> int:
    try:
        mark_stage("contract_start")
        cases, posix_status = run_contract()
        if os.environ.get(OPTIMIZED_ENV) != "1":
            mark_stage("optimized_subprocess")
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
                timeout=180,
                env=environment,
            )
            require(optimized.returncode == 0 and optimized.stderr == "", "optimized_contract_failed")
            require("status=pass mode=collector_contract" in optimized.stdout, "optimized_summary_invalid")
        print(
            f"status=pass mode=collector_contract cases={cases} optimized={str(sys.flags.optimize > 0).lower()} "
            f"posix_io={posix_status} external_access=false persistent_writes=false"
        )
        return 0
    except (ContractFailure, OSError, subprocess.SubprocessError) as error:
        if isinstance(error, ContractFailure):
            reason = error.classification
        elif isinstance(error, subprocess.SubprocessError):
            reason = "subprocess_contract"
        else:
            reason = "os_contract"
        print(
            "status=failed mode=collector_contract classification=offline_contract "
            f"failure_stage={ACTIVE_STAGE} failure_reason={reason} "
            "external_access=false persistent_writes=false"
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

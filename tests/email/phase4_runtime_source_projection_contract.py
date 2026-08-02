#!/usr/bin/env python3
"""离线验证 Phase 4 源投影的日志、审计与预导入字节门禁。"""

from __future__ import annotations

import datetime as dt
import ast
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
from types import MappingProxyType

sys.dont_write_bytecode = True

HERE = pathlib.Path(__file__).resolve().parent
SCRIPT = HERE / "phase4_runtime_source_projection.py"
SCANNER = HERE / "phase4_runtime_sensitive_scan.py"
EXPECTED_PROJECTION_SHA256 = "2BC04F38C2E5073B5FE390C83394F16ACC46B0C6B353834A848EEC5487F606AB"
PREIMPORT_FAILURE = (
    "status=failed mode=source_projection_contract classification=preimport_gate "
    "external_access=false persistent_writes=false env_read=false\n"
)
SCANNER_PREIMPORT_FAILURE = (
    "status=failed mode=source_projection classification=preimport_gate "
    "external_access=false persistent_writes=false env_read=false\n"
)


def _preimport_projection() -> None:
    """任何字节漂移都在候选模块顶层代码执行前固定失败。"""
    try:
        source_bytes = SCRIPT.read_bytes()
    except OSError:
        print(PREIMPORT_FAILURE, end="")
        raise SystemExit(1)
    if hashlib.sha256(source_bytes).hexdigest().upper() != EXPECTED_PROJECTION_SHA256:
        print(PREIMPORT_FAILURE, end="")
        raise SystemExit(1)


_preimport_projection()

# 只有完整字节 SHA 门禁通过后，才允许执行候选 projection 的顶层代码。
import phase4_runtime_source_projection as projection


class ContractFailure(Exception):
    """测试失败只携带固定分类。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def run_cli(arguments: list[str], optimize: bool = False) -> tuple[int, str, str]:
    command = [sys.executable]
    if optimize:
        command.append("-O")
    command.extend(["-B", str(SCRIPT), *arguments])
    result = subprocess.run(
        command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, check=False, timeout=60,
    )
    return result.returncode, result.stdout, result.stderr


def mode_contract() -> int:
    cases = 0
    for optimize in (False, True):
        code, stdout, stderr = run_cli([], optimize)
        require(code == 0 and stderr == "", "default_mode_failed")
        require(
            stdout == "status=disabled mode=source_projection external_access=false persistent_writes=false env_read=false\n",
            "default_summary",
        )
        cases += 1
        code, stdout, stderr = run_cli(["--self-test"], optimize)
        require(code == 0 and stderr == "", "selftest_failed")
        require(
            stdout == "status=pass mode=source_projection_selftest cases=7 external_access=false persistent_writes=false env_read=false\n",
            "selftest_summary",
        )
        cases += 1
    return cases


def config_for_log(path: pathlib.Path, start: dt.datetime, end: dt.datetime) -> projection.ProjectionConfig:
    return projection.ProjectionConfig(
        "http://127.0.0.1:8080", pathlib.Path("/tmp/admin.token"),
        pathlib.Path("/tmp/internal.token"), pathlib.Path("/usr/bin/mysql"),
        pathlib.Path("/tmp/mysql-connection.json"), "molin", path,
        pathlib.Path("/tmp/admin-dist"), pathlib.Path("/tmp/admin-manifest.json"),
        pathlib.Path("/tmp/user-dist"), pathlib.Path("/tmp/user-manifest.json"),
        pathlib.Path("/tmp/phase4-source"), start, end,
    )


def write_readonly_go_log(path: pathlib.Path, records: list[tuple[dt.datetime, str]]) -> None:
    """按执行主机本地时区生成 Go 默认 logger 的真实单行格式。"""
    require(len(records) > 0, "fixture_contract")
    startup_stamp = dt.datetime.fromtimestamp(records[-1][0].timestamp())
    lines = [
        f"{startup_stamp:%Y/%m/%d %H:%M:%S} [security] 启动配置检查完成",
        "API server 启动，监听 0.0.0.0:8080",
    ]
    for stamp, message in records:
        local_stamp = dt.datetime.fromtimestamp(stamp.timestamp())
        lines.append(f"{local_stamp:%Y/%m/%d %H:%M:%S} {message}")
    path.write_bytes(("\n".join(lines) + "\n").encode("utf-8"))
    path.chmod(0o444)


def write_readonly_bytes(path: pathlib.Path, value: bytes) -> None:
    path.write_bytes(value)
    path.chmod(0o444)


def gorm_slow_block(stamp: dt.datetime, sql: str, source: str = "molin/server/internal/modules/auth/repository.go:321") -> bytes:
    """生成与 GORM 1.31 默认彩色 Warn logger 完全一致的三行慢查询块。"""
    local_stamp = dt.datetime.fromtimestamp(stamp.timestamp())
    return (
        "\r\n"
        f"{local_stamp:%Y/%m/%d %H:%M:%S} \x1b[32m{source} \x1b[33mSLOW SQL >= 200ms\n"
        f"\x1b[0m\x1b[31;1m[201.125ms] \x1b[33m[rows:1]\x1b[35m {sql}\x1b[0m\n"
    ).encode("utf-8")


def expect_projection_failure(callable_value: object, classification: str) -> None:
    try:
        callable_value()  # type: ignore[operator]
    except projection.ProjectionFailure as error:
        require(error.args == (classification,), "failure_classification")
        return
    raise ContractFailure("unsafe_case_accepted")


def application_log_contract() -> int:
    cases = 0
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    start = now - dt.timedelta(minutes=5)
    end = now + dt.timedelta(minutes=5)
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary)
        valid = root / "valid.log"
        write_readonly_go_log(valid, [(start + dt.timedelta(minutes=1), "GET /api/admin/email/templates 4ms")])
        output = projection.application_log_projection(config_for_log(valid, start, end)).decode("utf-8")
        require(output == "level=info route_class=admin outcome=observed count=1\n", "log_projection")
        cases += 1

        valid_gorm = root / "valid-gorm.log"
        valid_gorm_value = valid.read_bytes() + gorm_slow_block(
            start + dt.timedelta(minutes=2),
            "SELECT count(*) FROM `email_provider_templates` WHERE `status` = 'approved'",
        ) + gorm_slow_block(
            start + dt.timedelta(minutes=2, seconds=1),
            "SELECT count(*) FROM `email_scene_bindings` WHERE `enabled` = 1",
            "/home/pc/molin/server/internal/modules/auth/repository.go:654",
        )
        write_readonly_bytes(valid_gorm, valid_gorm_value)
        output = projection.application_log_projection(config_for_log(valid_gorm, start, end)).decode("utf-8")
        require(output == "level=info route_class=admin outcome=observed count=1\n", "gorm_log_projection")
        cases += 1

        gorm_attacks = (
            gorm_slow_block(start + dt.timedelta(minutes=2), "UPDATE users SET status = 'disabled'"),
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT 1; DROP TABLE users"),
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT * FROM users FOR UPDATE"),
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT * FROM users -- hidden write"),
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT 1", "/tmp/forged.go:1"),
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT 1")[2:],
            gorm_slow_block(start + dt.timedelta(minutes=2), "SELECT 1").splitlines(keepends=True)[0],
        )
        for index, attack in enumerate(gorm_attacks):
            gorm_attack = root / f"gorm-attack-{index}.log"
            write_readonly_bytes(gorm_attack, valid.read_bytes() + attack)
            expect_projection_failure(
                lambda gorm_attack=gorm_attack: projection.application_log_projection(
                    config_for_log(gorm_attack, start, end),
                ),
                "log_contract",
            )
            cases += 1

        sensitive = root / "sensitive.log"
        write_readonly_go_log(
            sensitive,
            [(start + dt.timedelta(minutes=2), "POST /api/admin/email/templates code=123456")],
        )
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(sensitive, start, end)),
            "sensitive_log_detected",
        )
        cases += 1

        unrelated_sensitive = root / "unrelated-sensitive.log"
        write_readonly_go_log(
            unrelated_sensitive,
            [(start + dt.timedelta(minutes=2), "background recipient secret@example.net")],
        )
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(unrelated_sensitive, start, end)),
            "sensitive_log_detected",
        )
        cases += 1

        malformed_sensitive = root / "malformed-sensitive.log"
        write_readonly_bytes(malformed_sensitive, b"missing timestamp recipient=secret@example.net\n")
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(malformed_sensitive, start, end)),
            "sensitive_log_detected",
        )
        cases += 1

        outside_sensitive = root / "outside-sensitive.log"
        write_readonly_go_log(
            outside_sensitive,
            [
                (start - dt.timedelta(seconds=1), "background recipient secret@example.net"),
                (start + dt.timedelta(minutes=3), "GET /api/admin/email/templates 2ms"),
            ],
        )
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(outside_sensitive, start, end)),
            "sensitive_log_detected",
        )
        cases += 1

        outside_safe = root / "outside-safe.log"
        write_readonly_go_log(
            outside_safe,
            [
                (start - dt.timedelta(seconds=1), "GET /api/admin/email/templates 1ms"),
                (start + dt.timedelta(minutes=3), "GET /api/admin/email/templates 2ms"),
            ],
        )
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(outside_safe, start, end)),
            "log_contract",
        )
        cases += 1

        unrelated = root / "unrelated.log"
        write_readonly_go_log(unrelated, [(start + dt.timedelta(minutes=3), "GET /api/health 1ms")])
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(unrelated, start, end)),
            "required_category_empty",
        )
        cases += 1

        malformed_values = (
            (b"GET /api/admin/email/templates 1ms\n", "log_contract"),
            (b"2026/07/31 99:00:00 GET /api/admin/email/templates 1ms\n", "log_contract"),
            (b"2026/07/31 00:03:00 GET /api/admin/email/templates 1ms", "log_contract"),
            (b"2026/07/31 00:03:00 GET /api/admin/email/templates 1ms\r\n", "log_contract"),
            (b"2026/07/31 00:03:00 GET /api/admin/email/templates\x00 1ms\n", "log_contract"),
            # 冻结扫描器把非 UTF-8 原始字节先判为敏感，证明解码失败不能抢先绕过扫描门禁。
            (b"2026/07/31 00:03:00 GET /api/admin/email/templates \xff\n", "sensitive_log_detected"),
            (b"{" + b"x" * (projection.MAX_LOG_LINE_BYTES + 1) + b"}\n", "log_contract"),
        )
        for index, (malformed_value, expected_classification) in enumerate(malformed_values):
            malformed = root / f"malformed-{index}.log"
            write_readonly_bytes(malformed, malformed_value)
            expect_projection_failure(
                lambda malformed=malformed: projection.application_log_projection(config_for_log(malformed, start, end)),
                expected_classification,
            )
            cases += 1

        # 旧 journald JSON 即使字段齐全也不是捕获器产生的 Go 标准日志，必须关闭失败。
        journald = root / "journald.jsonl"
        write_readonly_bytes(
            journald,
            json.dumps({"__REALTIME_TIMESTAMP": "1785456180000000", "MESSAGE": "GET /api/admin/email/templates 1ms"}).encode("utf-8") + b"\n",
        )
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(journald, start, end)),
            "log_contract",
        )
        cases += 1

        startup_attacks = (
            b"API server \xe5\x90\xaf\xe5\x8a\xa8\xef\xbc\x8c\xe7\x9b\x91\xe5\x90\xac 0.0.0.0:8081\n",
            b"API server \xe5\x90\xaf\xe5\x8a\xa8\xef\xbc\x8c\xe7\x9b\x91\xe5\x90\xac evil.example:8080\n",
            (
                "API server 启动，监听 0.0.0.0:8080\n"
                f"{dt.datetime.fromtimestamp(now.timestamp()):%Y/%m/%d %H:%M:%S} GET /api/admin/email/templates 1ms\n"
                "API server 启动，监听 0.0.0.0:8080\n"
            ).encode("utf-8"),
            (
                f"{dt.datetime.fromtimestamp(now.timestamp()):%Y/%m/%d %H:%M:%S} GET /api/admin/email/templates 1ms\n"
                "API server 启动，监听 0.0.0.0:8080\n"
            ).encode("utf-8"),
            (
                "API server 启动，监听 0.0.0.0:8080\n"
                "API server 已就绪但缺少时间\n"
            ).encode("utf-8"),
        )
        for index, attack in enumerate(startup_attacks):
            startup_attack = root / f"startup-attack-{index}.log"
            write_readonly_bytes(startup_attack, attack)
            expect_projection_failure(
                lambda startup_attack=startup_attack: projection.application_log_projection(
                    config_for_log(startup_attack, start, end),
                ),
                "log_contract",
            )
            cases += 1

        stale = root / "stale-capture.log"
        write_readonly_go_log(stale, [(now, "GET /api/admin/email/templates 1ms")])
        stale_epoch = (start - dt.timedelta(minutes=1)).timestamp()
        os.utime(stale, (stale_epoch, stale_epoch))
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(stale, start, end)),
            "log_contract",
        )
        cases += 1

    if os.name == "posix":
        expect_projection_failure(
            lambda: projection.application_log_projection(config_for_log(pathlib.Path("/dev/null"), start, end)),
            "log_contract",
        )
        cases += 1
    else:
        # Windows 没有 /dev/null；静态检查仍确保非普通文件被拒绝，真实 POSIX 契约补充动态用例。
        source = SCRIPT.read_text(encoding="utf-8")
        require("stat.S_ISREG(metadata.st_mode)" in source, "dev_null_gate_missing")
        cases += 1
    return cases


def audit_mapping_contract() -> int:
    expected = {
        "email.template.status.update.attempt": "template_status",
        "email.scene.binding.update.result": "scene_binding",
        "email.template.sync.result": "template_sync",
        "email.template.sync.stale.attempt": "template_sync",
        "email.test_allowlist.add.result": "allowlist",
        "email.test_allowlist.revoke.attempt": "allowlist",
        "email.template.test_send.result": "test_send",
        "email.admin_verify.bootstrap.attempt": "bootstrap",
    }
    for action, action_class in expected.items():
        require(projection.audit_action_class(action) == action_class, "audit_mapping")
    expect_projection_failure(lambda: projection.audit_action_class("email.unknown.result"), "unknown_audit_action")
    return len(expected) + 1


def database_projection_contract() -> int:
    cases = 0
    expected_tables = (
        "verification_codes", "email_provider_templates", "email_scene_bindings",
        "email_template_sync_runs", "email_test_recipient_allowlist", "email_send_logs",
    )
    original_mysql_rows = projection.mysql_rows

    connection = projection.MysqlConnection("127.0.0.1", 3306, "molin", "secret", None)

    def safe_rows(_config: projection.ProjectionConfig, _connection: projection.MysqlConnection, query_name: str) -> list[list[str]]:
        count = "5" if query_name == "email_scene_bindings" else "1"
        return [[query_name, count, "1", "1", "1"]]

    try:
        projection.mysql_rows = safe_rows
        value = json.loads(projection.database_projection(
            config_for_log(pathlib.Path("/tmp/not-used"), dt.datetime.now(dt.timezone.utc), dt.datetime.now(dt.timezone.utc)),
            connection,
        ))
        require(
            set(value) == {
                "table_class", "records", "row_count", "all_masked", "all_hashed",
                "all_safe", "sensitive_field_count",
            },
            "database_projection_fields",
        )
        require(len(value["records"]) == 6, "database_projection_count")
        for record, table_class in zip(value["records"], expected_tables):
            require(
                set(record) == {
                    "table_class", "row_count", "all_masked", "all_hashed",
                    "all_safe", "sensitive_field_count",
                }
                and record["table_class"] == table_class
                and record["row_count"] > 0
                and record["all_masked"] is True
                and record["all_hashed"] is True
                and record["all_safe"] is True,
                "database_projection_record",
            )
        encoded = json.dumps(value, ensure_ascii=False, sort_keys=True)
        require(
            all(token not in encoded for token in (
                "recipient_masked", "email_masked", "target_value", "target_hash",
                "provider_request_id", "template_text", "code_hash",
            )),
            "database_value_exposed",
        )
        cases += 1

        def zero_rows(_config: projection.ProjectionConfig, _connection: projection.MysqlConnection, query_name: str) -> list[list[str]]:
            count = "0" if query_name == "email_test_recipient_allowlist" else "1"
            return [[query_name, count, "1", "1", "1"]]

        projection.mysql_rows = zero_rows
        expect_projection_failure(
            lambda: projection.database_projection(
                config_for_log(pathlib.Path("/tmp/not-used"), dt.datetime.now(dt.timezone.utc), dt.datetime.now(dt.timezone.utc)),
                connection,
            ),
            "required_category_empty",
        )
        cases += 1

        def unsafe_rows(_config: projection.ProjectionConfig, _connection: projection.MysqlConnection, query_name: str) -> list[list[str]]:
            flags = ["1", "0", "1"] if query_name == "verification_codes" else ["1", "1", "1"]
            return [[query_name, "1", *flags]]

        projection.mysql_rows = unsafe_rows
        expect_projection_failure(
            lambda: projection.database_projection(
                config_for_log(pathlib.Path("/tmp/not-used"), dt.datetime.now(dt.timezone.utc), dt.datetime.now(dt.timezone.utc)),
                connection,
            ),
            "database_safety_contract",
        )
        cases += 1
    finally:
        projection.mysql_rows = original_mysql_rows
    return cases


def select_only_contract() -> int:
    cases = 0
    require(
        tuple(projection.SELECT_QUERIES) == (
            "audit", "verification_codes", "email_provider_templates", "email_scene_bindings",
            "email_template_sync_runs", "email_test_recipient_allowlist", "email_send_logs",
        ),
        "query_names",
    )
    require(
        projection.query_set_sha256() == "B28856687C8D18E7D2F941691AB9838B36710C29E44D28ECF1DD8FE488E490EC",
        "query_hash",
    )
    projection.validate_select_queries()
    require(
        all("window_start" not in query.lower() and "window_end" not in query.lower() for query in projection.SELECT_QUERIES.values()),
        "database_window_filter",
    )
    require(
        all("REGEXP BINARY" not in query and "REGEXP_LIKE" in query for name, query in projection.SELECT_QUERIES.items() if name not in {"audit", "email_scene_bindings"}),
        "mysql8_regexp_contract",
    )
    cases += 4

    required_fragments = {
        "verification_codes": ("target_value IS NULL", "HEX(target_masked)='E58E86E58FB2E982AEE7AEB1E5B7B2E5A4B1E69588'", "REGEXP_LIKE(target_hash,'^[0-9a-f]{64}$','c')", "code IS NULL", "REGEXP_LIKE(code_hash,'^[0-9a-f]{64}$','c')"),
        "email_provider_templates": ("content_sha256=LOWER(SHA2", "JSON_LENGTH(variables_json)=2", "JSON_QUOTE('Code')", "JSON_QUOTE('ExpireMinutes')"),
        "email_scene_bindings": ("COUNT(*)=5", "JSON_EXTRACT(b.variable_mapping_json,'$.code')", "t.provider_status='approved'", "t.missing=0"),
        "email_template_sync_runs": ("REGEXP_LIKE(idempotency_key_hash,'^[0-9a-f]{64}$','c')", "REGEXP_LIKE(request_fingerprint,'^[0-9a-f]{64}$','c')", "status IN ('running','succeeded','failed')"),
        "email_test_recipient_allowlist": ("REGEXP_LIKE(email_hmac,'^[0-9a-f]{64}$','c')", "LOCATE('***@',email_masked)>0"),
        "email_send_logs": ("REGEXP_LIKE(recipient_hmac,'^[0-9a-f]{64}$','c')", "LOCATE('***@',recipient_masked)>0", "purpose IN ('otp','test')"),
    }
    for query_name, fragments in required_fragments.items():
        require(all(fragment in projection.SELECT_QUERIES[query_name] for fragment in fragments), "query_coverage")
        cases += 1

    attacks = (
        "SELECT 1; DELETE FROM email_send_logs",
        "SELECT 1 UNION SELECT 2 INTO OUTFILE '/tmp/x'",
        "SELECT 1 INTO DUMPFILE '/tmp/x'",
        "SELECT * FROM email_send_logs FOR UPDATE",
        "SELECT GET_LOCK('x',1)",
        "SELECT 1 /* hidden */",
        "UPDATE email_send_logs SET status='failed'",
        "CALL unsafe_proc()",
    )
    for query in attacks:
        expect_projection_failure(lambda query=query: projection.validate_select_query_text(query), "query_readonly_contract")
        cases += 1

    original_queries = projection.SELECT_QUERIES
    try:
        changed = dict(original_queries)
        changed["audit"] = "SELECT 1"
        projection.SELECT_QUERIES = MappingProxyType(changed)
        expect_projection_failure(projection.validate_select_queries, "query_set_hash")
        cases += 1
    finally:
        projection.SELECT_QUERIES = original_queries
    return cases


def mysql_security_contract() -> int:
    cases = 0
    valid_value = {
        "host": "127.0.0.1", "port": 3306, "user": "molin",
        "password": "secret-value", "socket": None,
    }
    connection = projection.parse_mysql_connection_value(valid_value)
    require(connection == projection.MysqlConnection("127.0.0.1", 3306, "molin", "secret-value", None), "mysql_connection_parse")
    cases += 1

    attacks = []
    for key in ("init-command", "local-infile", "defaults-extra-file", "plugin-dir"):
        value = dict(valid_value)
        value[key] = "unsafe"
        attacks.append(value)
    value = dict(valid_value); value["host"] = "--host=evil"; attacks.append(value)
    value = dict(valid_value); value["port"] = "3306"; attacks.append(value)
    value = dict(valid_value); value["user"] = "--execute=DROP"; attacks.append(value)
    value = dict(valid_value); value["socket"] = "/tmp/unsafe.sock"; attacks.append(value)
    for attack in attacks:
        expect_projection_failure(lambda attack=attack: projection.parse_mysql_connection_value(attack), "mysql_connection_contract")
        cases += 1
    expect_projection_failure(
        lambda: projection._strict_json_object(
            b'{"host":"127.0.0.1","host":"localhost","port":3306,"user":"molin","password":"x","socket":null}',
            "mysql_connection_contract",
        ),
        "mysql_connection_contract",
    )
    cases += 1

    config = config_for_log(
        pathlib.Path("/tmp/not-used"), dt.datetime.now(dt.timezone.utc), dt.datetime.now(dt.timezone.utc),
    )
    command, child_environment = projection.mysql_command(config, connection, projection.SELECT_QUERIES["audit"])
    require(command[1] == "--no-defaults", "mysql_no_defaults")
    require(not any("defaults" in argument for argument in command[2:]), "mysql_defaults_injection")
    require(
        f"--execute={projection.MYSQL_READONLY_PREFIX}{projection.SELECT_QUERIES['audit']}{projection.MYSQL_READONLY_SUFFIX}" in command,
        "mysql_readonly_wrapper",
    )
    require(child_environment == {"MYSQL_PWD": "secret-value"}, "mysql_child_environment")
    require(
        projection.mysql_command_contract_sha256()
        == "3DD4C9698BE89C01AC40582BFCB62F76ABAE91AECF4F958C7A91411F7F443CB3",
        "mysql_command_hash",
    )
    cases += 5

    captured: dict[str, object] = {}
    original_run = projection.subprocess.run

    class FakeResult:
        returncode = 0
        stdout = "1\nresult\n"

    def fake_run(command_value: list[str], **kwargs: object) -> FakeResult:
        captured["command"] = command_value
        captured["env"] = kwargs.get("env")
        return FakeResult()

    try:
        projection.subprocess.run = fake_run
        require(projection.run_mysql_readonly(config, connection, "SELECT 1") == ["result"], "mysql_runner_result")
        require(captured["env"] == {"MYSQL_PWD": "secret-value"}, "mysql_environment_inherited")
        cases += 2
    finally:
        projection.subprocess.run = original_run

    valid_grants = [
        "GRANT USAGE ON *.* TO `molin`@`%`",
        "GRANT SELECT, SHOW VIEW ON `molin`.* TO `molin`@`%`",
    ]
    projection.validate_mysql_grant_lines(valid_grants, "molin")
    cases += 1
    grant_attacks = (
        ["GRANT ALL PRIVILEGES ON *.* TO `molin`@`%`"],
        [*valid_grants, "GRANT INSERT ON `molin`.* TO `molin`@`%`"],
        ["GRANT USAGE ON *.* TO `molin`@`%`", "GRANT SELECT ON `other`.* TO `molin`@`%`"],
        ["GRANT USAGE ON *.* TO `molin`@`%` WITH GRANT OPTION", valid_grants[1]],
        ["GRANT USAGE ON *.* TO `molin`@`%`"],
    )
    for grants in grant_attacks:
        expect_projection_failure(lambda grants=grants: projection.validate_mysql_grant_lines(grants, "molin"), "database_grant_contract")
        cases += 1
    return cases


def close_tree(root: pathlib.Path) -> None:
    for path in sorted(root.rglob("*"), key=lambda item: len(item.parts), reverse=True):
        path.chmod(0o555 if path.is_dir() else 0o444)
    root.chmod(0o555)


def deployment_identity_contract() -> int:
    cases = 0
    process = projection.ApiProcessIdentity(7, "11", 1, 2, "c" * 64)
    admin = projection.FrontendIdentity("a" * 64, 3, 3)
    user = projection.FrontendIdentity("b" * 64, 4, 5)
    admin_manifest = projection.FrontendManifest("admin_frontend", "a" * 64, 3, 3, "sha256:" + "d" * 64)
    user_manifest = projection.FrontendManifest("user_frontend", "b" * 64, 4, 5, "sha256:" + "e" * 64)
    known = projection.deployment_identity(
        "0.1.0", process, admin, admin_manifest, user, user_manifest,
    )
    require(known == "edebd679327e26a3c69ee9efc739cb1cf6ef60840edae396add8da9644223ca9", "deployment_known_answer")
    require(projection.api_version_from_response({"code": 0, "data": {"version": "0.1.0"}}) == "0.1.0", "api_version_extract")
    projection.verify_frontend_manifest(admin, admin_manifest, "admin_frontend")
    require(
        projection.parse_listening_inodes(
            "sl local_address rem_address st tx rx tr tm retr uid timeout inode\n0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 76543",
            8080,
        ) == {"76543"},
        "listener_parse",
    )
    cases += 4
    try:
        value = base_config()
        value["deployment_sha"] = "a" * 64
        projection.parse_config(value)
    except projection.ProjectionFailure as error:
        require(error.args == ("config_contract",), "deployment_input_classification")
        cases += 1
    else:
        raise ContractFailure("arbitrary_deployment_accepted")

    two_file = projection.FrontendIdentity("f" * 64, 2, 100)
    two_file_manifest = projection.FrontendManifest("admin_frontend", "f" * 64, 2, 100, "sha256:" + "1" * 64)
    expect_projection_failure(
        lambda: projection.verify_frontend_manifest(two_file, two_file_manifest, "admin_frontend"),
        "frontend_manifest_mismatch",
    )
    expect_projection_failure(
        lambda: projection.verify_frontend_manifest(
            admin,
            projection.FrontendManifest("admin_frontend", "9" * 64, 3, 3, "sha256:" + "d" * 64),
            "admin_frontend",
        ),
        "frontend_manifest_mismatch",
    )
    cases += 2

    valid_manifest_value = {
        "role": "admin_frontend", "tree_sha256": "a" * 64,
        "file_count": 3, "byte_count": 3,
        "container_or_image_digest": "sha256:" + "d" * 64,
    }
    require(
        projection.parse_frontend_manifest_value(valid_manifest_value, "admin_frontend") == admin_manifest,
        "frontend_manifest_parse",
    )
    cases += 1
    manifest_attacks = []
    value = dict(valid_manifest_value); value["unknown"] = "x"; manifest_attacks.append(value)
    value = dict(valid_manifest_value); value["file_count"] = 2; manifest_attacks.append(value)
    value = dict(valid_manifest_value); value["container_or_image_digest"] = "latest"; manifest_attacks.append(value)
    value = dict(valid_manifest_value); value["role"] = "user_frontend"; manifest_attacks.append(value)
    for attack in manifest_attacks:
        expect_projection_failure(
            lambda attack=attack: projection.parse_frontend_manifest_value(attack, "admin_frontend"),
            "frontend_manifest_contract",
        )
        cases += 1
    expect_projection_failure(
        lambda: projection._strict_json_object(
            ('{"role":"admin_frontend","role":"user_frontend","tree_sha256":"' + "a" * 64
             + '","file_count":3,"byte_count":3,"container_or_image_digest":"sha256:' + "d" * 64 + '"}').encode("utf-8"),
            "frontend_manifest_contract",
        ),
        "frontend_manifest_contract",
    )
    cases += 1

    if os.name == "posix":
        temporary_context = tempfile.TemporaryDirectory()
    else:
        temporary_context = None
    if temporary_context is not None:
        temporary = temporary_context.name
        root = pathlib.Path(temporary)
        valid = root / "valid"
        (valid / "assets").mkdir(parents=True)
        (valid / "index.html").write_bytes(b"<html></html>")
        (valid / "assets" / "app.js").write_bytes(b"console.log('safe')")
        (valid / "assets" / "app.css").write_bytes(b"body{}")
        close_tree(valid)
        first = projection.frontend_identity(valid)
        second = projection.frontend_identity(valid)
        require(first == second and first.file_count == 3 and first.byte_count > 0, "frontend_identity")
        cases += 1

        empty = root / "empty"
        empty.mkdir()
        empty.chmod(0o555)
        expect_projection_failure(lambda: projection.frontend_identity(empty), "frontend_incomplete")
        cases += 1

        incomplete = root / "incomplete"
        incomplete.mkdir()
        (incomplete / "index.html").write_bytes(b"x")
        close_tree(incomplete)
        expect_projection_failure(lambda: projection.frontend_identity(incomplete), "frontend_incomplete")
        cases += 1

        symlink_tree = root / "symlink-tree"
        (symlink_tree / "assets").mkdir(parents=True)
        (symlink_tree / "index.html").write_bytes(b"x")
        (symlink_tree / "assets" / "app.css").write_bytes(b"x")
        outside_script = root / "outside.js"
        outside_script.write_bytes(b"x")
        (symlink_tree / "assets" / "app.js").symlink_to(outside_script)
        (symlink_tree / "index.html").chmod(0o444)
        (symlink_tree / "assets" / "app.css").chmod(0o444)
        (symlink_tree / "assets").chmod(0o555)
        symlink_tree.chmod(0o555)
        expect_projection_failure(lambda: projection.frontend_identity(symlink_tree), "frontend_contract")
        cases += 1

        exchange_tree = root / "exchange-tree"
        (exchange_tree / "assets").mkdir(parents=True)
        (exchange_tree / "index.html").write_bytes(b"x")
        (exchange_tree / "assets" / "app.js").write_bytes(b"old")
        (exchange_tree / "assets" / "app.css").write_bytes(b"x")
        replacement = root / "replacement.js"
        replacement.write_bytes(b"new")
        close_tree(exchange_tree)
        original_scandir = projection.os.scandir
        exchanged = {"done": False}
        proxy_state = {"closed": False}

        class EntryProxy:
            def __init__(self, entry: os.DirEntry[str], directory_path: pathlib.Path) -> None:
                self._entry = entry
                self._directory_path = directory_path
                self.name = entry.name

            def stat(self, follow_symlinks: bool = False) -> os.stat_result:
                metadata = self._entry.stat(follow_symlinks=follow_symlinks)
                if self.name == "app.js" and not exchanged["done"]:
                    os.replace(replacement, self._directory_path / self.name)
                    exchanged["done"] = True
                return metadata

        class ScandirProxy:
            """模拟真实 scandir 迭代器的关闭和上下文管理语义。"""

            def __init__(self, entries: list[EntryProxy]) -> None:
                self._iterator = iter(entries)
                self._closed = False

            def __iter__(self) -> "ScandirProxy":
                return self

            def __next__(self) -> EntryProxy:
                if self._closed:
                    raise StopIteration
                return next(self._iterator)

            def close(self) -> None:
                self._closed = True
                proxy_state["closed"] = True

            def __enter__(self) -> "ScandirProxy":
                return self

            def __exit__(self, exc_type: object, exc_value: object, traceback: object) -> None:
                self.close()

        def swapping_scandir(target: object) -> object:
            if isinstance(target, int):
                directory_path = pathlib.Path(os.readlink(f"/proc/self/fd/{target}"))
                if directory_path.name == "assets" and directory_path.parent.name == "exchange-tree":
                    with original_scandir(target) as original_iterator:
                        entries = list(original_iterator)
                    directory_path.chmod(0o755)
                    return ScandirProxy([EntryProxy(entry, directory_path) for entry in entries])
            return original_scandir(target)

        try:
            projection.os.scandir = swapping_scandir
            expect_projection_failure(lambda: projection.frontend_identity(exchange_tree), "frontend_identity_changed")
            require(exchanged["done"], "frontend_exchange_not_triggered")
            require(proxy_state["closed"], "frontend_scandir_proxy_not_closed")
            cases += 1
        finally:
            projection.os.scandir = original_scandir
        temporary_context.cleanup()
    else:
        source = SCRIPT.read_text(encoding="utf-8")
        require(
            "dir_fd=descriptor" in source and "os.O_NOFOLLOW" in source
            and "os.scandir(descriptor)" in source and "frontend_identity_changed" in source,
            "frontend_openat_contract",
        )
        cases += 5
    return cases


def base_config() -> dict[str, str]:
    return {
        "api_base": "http://127.0.0.1:8080",
        "admin_token_file": "/tmp/admin.token",
        "internal_token_file": "/tmp/internal.token",
        "mysql_client": "/usr/bin/mysql",
        "mysql_connection_file": "/tmp/mysql-connection.json",
        "mysql_database": "molin",
        "application_log": "/tmp/application.jsonl",
        "admin_frontend": "/tmp/admin-dist",
        "admin_frontend_manifest": "/tmp/admin-manifest.json",
        "user_frontend": "/tmp/user-dist",
        "user_frontend_manifest": "/tmp/user-manifest.json",
        "output": "/tmp/phase4-source",
        "window_start_utc": "2026-07-31T00:00:00Z",
        "window_end_utc": "2026-07-31T00:30:00Z",
    }


def retention_and_credentials_contract() -> int:
    cases = 0
    source = SCRIPT.read_text(encoding="utf-8")
    tree = ast.parse(source)
    forbidden_calls = {"unlink", "rmdir", "remove", "removedirs", "rmtree"}
    require(
        not any(
            isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
            and node.func.attr in forbidden_calls
            for node in ast.walk(tree)
        ),
        "automatic_cleanup_present",
    )
    require("stat.S_IMODE(before.st_mode) == 0o600" in source, "credential_mode_gate")
    require("_read_secure_600_bytes(path" in source, "connection_mode_gate")
    cases += 3

    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary)
        output = root / "caller-output"
        output.mkdir()
        marker = output / "keep"
        marker.write_bytes(b"caller-owned")
        config = config_for_log(pathlib.Path("/tmp/not-used"), dt.datetime.now(dt.timezone.utc), dt.datetime.now(dt.timezone.utc))
        config = projection.ProjectionConfig(
            config.api_base, config.admin_token_file, config.internal_token_file,
            config.mysql_client, config.mysql_connection_file, config.mysql_database,
            config.application_log, config.admin_frontend, config.admin_frontend_manifest,
            config.user_frontend, config.user_frontend_manifest,
            output, config.window_start, config.window_end,
        )
        expect_projection_failure(lambda: projection.write_output(config, {"x": b"x"}), "target_exists")
        require(marker.read_bytes() == b"caller-owned", "caller_target_modified")
        cases += 1

        partial_output = root / "partial-output"
        partial_config = projection.ProjectionConfig(
            config.api_base, config.admin_token_file, config.internal_token_file,
            config.mysql_client, config.mysql_connection_file, config.mysql_database,
            config.application_log, config.admin_frontend, config.admin_frontend_manifest,
            config.user_frontend, config.user_frontend_manifest,
            partial_output, config.window_start, config.window_end,
        )
        try:
            projection.write_output(partial_config, {"first.safe": b"retained", "second.safe": object()})  # type: ignore[dict-item]
        except TypeError:
            require(partial_output.is_dir() and (partial_output / "first.safe").is_file(), "partial_not_retained")
            cases += 1
        else:
            raise ContractFailure("partial_failure_not_triggered")
        finally:
            # 这里只恢复隔离临时 fixture 的权限，准备器自身从不清理调用方目标。
            if partial_output.exists():
                for child in partial_output.iterdir():
                    child.chmod(0o666)
                partial_output.chmod(0o777)
    return cases


def preimport_gate_contract() -> int:
    cases = 0
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary)
        shutil.copyfile(pathlib.Path(__file__), root / pathlib.Path(__file__).name)
        shutil.copyfile(SCANNER, root / SCANNER.name)
        projection_sentinel = root / "projection-sentinel"
        malicious_projection = (
            "import pathlib\n"
            f"pathlib.Path({str(projection_sentinel)!r}).write_text('executed')\n"
        ).encode("utf-8")
        (root / SCRIPT.name).write_bytes(malicious_projection)
        copied_contract = root / pathlib.Path(__file__).name
        for optimize in (False, True):
            command = [sys.executable]
            if optimize:
                command.append("-O")
            command.extend(["-B", str(copied_contract)])
            result = subprocess.run(
                command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, text=True, check=False, timeout=60,
            )
            require(result.returncode == 1, "preimport_exit")
            require(result.stdout == PREIMPORT_FAILURE and result.stderr == "", "preimport_output")
            require(not projection_sentinel.exists(), "projection_top_level_executed")
            cases += 1

        for mode in ("mismatch", "read_error"):
            scanner_root = root / mode
            scanner_root.mkdir()
            shutil.copyfile(SCRIPT, scanner_root / SCRIPT.name)
            scanner_sentinel = scanner_root / "scanner-sentinel"
            if mode == "mismatch":
                (scanner_root / SCANNER.name).write_bytes(
                    ("import pathlib\n" f"pathlib.Path({str(scanner_sentinel)!r}).write_text('executed')\n").encode("utf-8")
                )
            else:
                (scanner_root / SCANNER.name).mkdir()
            for optimize in (False, True):
                command = [sys.executable]
                if optimize:
                    command.append("-O")
                command.extend(["-B", str(scanner_root / SCRIPT.name)])
                result = subprocess.run(
                    command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE, text=True, check=False, timeout=60,
                )
                require(result.returncode == 2, "scanner_preimport_exit")
                require(
                    result.stdout == SCANNER_PREIMPORT_FAILURE and result.stderr == "",
                    "scanner_preimport_output",
                )
                require(not scanner_sentinel.exists(), "scanner_top_level_executed")
                cases += 1
    return cases


def static_contract() -> int:
    contract_source = pathlib.Path(__file__).read_text(encoding="utf-8")
    projection_source = SCRIPT.read_text(encoding="utf-8")
    require(not any(isinstance(node, ast.Assert) for node in ast.walk(ast.parse(contract_source))), "assert_dependent")
    require("contains_sensitive(raw_line, \"application_logs\")" in projection_source, "scanner_call_missing")
    require("relevant_count > 0" in projection_source, "relevant_count_missing")
    require("unknown_audit_action" in projection_source, "unknown_audit_gate_missing")
    require("EXPECTED_PROJECTION_SHA256" in contract_source and "_preimport_projection()" in contract_source, "preimport_gate_missing")
    require("--no-defaults" in projection_source and 'return command, {"MYSQL_PWD": connection.password}' in projection_source, "mysql_process_contract_missing")
    require("@@session.transaction_read_only" in projection_source and "SHOW GRANTS FOR CURRENT_USER()" in projection_source, "mysql_readonly_gate_missing")
    require("api_process_identity(8080)" in projection_source and "/proc/net/tcp" in projection_source, "api_process_gate_missing")
    require("os.O_NOFOLLOW" in projection_source and "dir_fd=descriptor" in projection_source, "frontend_openat_gate_missing")
    require(
        "list(os.scandir(descriptor))" not in projection_source
        and "_bounded_directory_entries(descriptor)" in projection_source
        and "len(entries) < MAX_DIRECTORY_ENTRIES" in projection_source,
        "frontend_bounded_iteration_missing",
    )
    return 9


def main() -> int:
    try:
        cases = (
            static_contract() + mode_contract() + application_log_contract()
            + audit_mapping_contract() + database_projection_contract()
            + select_only_contract() + mysql_security_contract() + deployment_identity_contract()
            + retention_and_credentials_contract() + preimport_gate_contract()
        )
        print(
            f"status=pass mode=source_projection_contract cases={cases} "
            "external_access=false persistent_writes=false env_read=false"
        )
        return 0
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print(
            "status=failed mode=source_projection_contract classification=offline_contract "
            "external_access=false persistent_writes=false env_read=false"
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

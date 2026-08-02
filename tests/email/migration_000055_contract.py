#!/usr/bin/env python3
"""离线校验 000055 migration 的冻结结构与安全回滚契约。"""

from __future__ import annotations

import hashlib
import re
import sys
from pathlib import Path
from typing import Callable


ROOT = Path(__file__).resolve().parents[2]
UP_PATH = ROOT / "server/migrations/000055_add_directmail_email_management.up.sql"
DOWN_PATH = ROOT / "server/migrations/000055_add_directmail_email_management.down.sql"
UP_SHA256 = "7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D"
DOWN_SHA256 = "217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE"


class ContractError(RuntimeError):
    """表示冻结 migration 与静态安全契约不一致。"""


CHECK_COUNT = 0


def check(condition: bool, message: str) -> None:
    """使用显式异常而不是 assert，确保 Python 优化模式不跳过校验。"""
    global CHECK_COUNT
    CHECK_COUNT += 1
    if not condition:
        raise ContractError(message)


def active_sql(text: str) -> str:
    """移除 MySQL 注释并压缩空白，避免注释伪造契约片段。"""
    # MySQL 的 /*!...*/ 会执行其中语句，/*+...*/ 会改变优化器行为；二者都不是可安全忽略的普通注释。
    check(re.search(r"/\*[!+]", text) is None, "禁止 MySQL 可执行注释或优化器提示")
    text = re.sub(r"/\*.*?\*/", " ", text, flags=re.DOTALL)
    text = re.sub(r"(?m)#.*$", " ", text)
    text = re.sub(r"(?m)--[\t ].*$", " ", text)
    return re.sub(r"\s+", " ", text).strip()


def require_fragment(sql: str, fragment: str, label: str) -> None:
    check(active_sql(fragment) in sql, f"缺少契约片段：{label}")


def require_regex(sql: str, pattern: str, label: str) -> None:
    check(re.search(pattern, sql, flags=re.IGNORECASE | re.DOTALL) is not None, f"缺少 SQL 结构：{label}")


def forbid_regex(sql: str, pattern: str, label: str) -> None:
    check(re.search(pattern, sql, flags=re.IGNORECASE | re.DOTALL) is None, f"出现禁止 SQL：{label}")


def require_order(sql: str, nodes: list[tuple[str, str]]) -> None:
    cursor = -1
    for fragment, label in nodes:
        position = sql.find(active_sql(fragment))
        check(position >= 0, f"缺少顺序节点：{label}")
        check(position > cursor, f"顺序错误：{label}")
        cursor = position


def table_block(sql: str, table: str) -> str:
    match = re.search(
        rf"CREATE TABLE {re.escape(table)} \((.*?)\) ENGINE=InnoDB",
        sql,
        flags=re.IGNORECASE | re.DOTALL,
    )
    check(match is not None, f"缺少表定义：{table}")
    return match.group(1) if match is not None else ""


def require_exact_set(actual: set[str], expected: set[str], label: str) -> None:
    check(actual == expected, f"{label}集合不一致")


def validate_hashes(up_bytes: bytes, down_bytes: bytes) -> None:
    check(hashlib.sha256(up_bytes).hexdigest().upper() == UP_SHA256, "000055 Up SHA-256 不一致")
    check(hashlib.sha256(down_bytes).hexdigest().upper() == DOWN_SHA256, "000055 Down SHA-256 不一致")


def validate_forbidden_sql(up: str, down: str) -> None:
    combined = f"{up} {down}"
    for pattern, label in (
        (r"\bDROP\s+(?:DATABASE|SCHEMA)\b", "删除 schema"),
        (r"\bTRUNCATE\b", "TRUNCATE"),
        (r"\b(?:GRANT|REVOKE|CREATE\s+USER|DROP\s+USER|ALTER\s+USER)\b", "账号或授权变更"),
        (r"\b(?:PREPARE|EXECUTE|LOAD\s+DATA|INTO\s+(?:OUTFILE|DUMPFILE))\b", "动态 SQL 或文件导入导出"),
        (r"\b(?:SET\s+GLOBAL|RENAME\s+TABLE)\b", "全局配置或表改名"),
        (r"(?<![0-9])0000(?:0[1-9]|[1-4][0-9]|5[0-4])(?![0-9])", "引用 000001-000054 历史 migration"),
        (r"\bDROP\s+TABLE\s+(?:verification_codes|users|roles|permissions|role_permissions|audit_logs)\b", "删除基础业务表"),
        (r"\bDELETE\s+FROM\s+verification_codes\b", "删除验证码历史行"),
    ):
        forbid_regex(combined, pattern, label)

    # 000055 只允许五场景 seed 使用一次 INSERT IGNORE；权限与 ownership 不得忽略冲突。
    check(len(re.findall(r"\bINSERT\s+IGNORE\b", up, flags=re.IGNORECASE)) == 1, "INSERT IGNORE 数量必须恰好为 1")
    require_regex(up, r"INSERT IGNORE INTO email_scene_bindings\b", "INSERT IGNORE 只能用于五场景 seed")
    forbid_regex(up, r"INSERT\s+IGNORE\s+INTO\s+(?:permissions|role_permissions|migration_000055_permission_ownership)", "权限写入忽略冲突")


def validate_up(up: str) -> None:
    expected_tables = {
        "email_provider_templates",
        "email_scene_bindings",
        "email_template_sync_runs",
        "email_test_recipient_allowlist",
        "email_send_logs",
        "migration_000055_permission_ownership",
    }
    created_tables = set(re.findall(r"\bCREATE TABLE ([a-z0-9_]+) \(", up, flags=re.IGNORECASE))
    require_exact_set(created_tables, expected_tables, "000055 持久表")

    require_order(
        up,
        [
            ("CREATE TEMPORARY TABLE migration_000055_assertions", "创建前置断言表"),
            ("ALTER TABLE verification_codes MODIFY COLUMN code VARCHAR(64) NULL", "扩容旧 code"),
            ("ADD COLUMN code_hash CHAR(64)", "新增 code_hash"),
            ("UPDATE verification_codes SET code_hash", "失效历史验证码"),
            ("UPDATE verification_codes SET target_hash", "清理历史邮箱"),
            ("MODIFY COLUMN code_hash CHAR(64)", "收紧 code_hash"),
            ("CREATE TABLE email_provider_templates", "创建模板镜像表"),
            ("CREATE TABLE email_scene_bindings", "创建场景绑定表"),
            ("CREATE TABLE email_template_sync_runs", "创建同步记录表"),
            ("CREATE TABLE email_test_recipient_allowlist", "创建测试白名单表"),
            ("CREATE TABLE email_send_logs", "创建发送日志表"),
            ("INSERT IGNORE INTO email_scene_bindings", "写入五场景"),
            ("CREATE TABLE migration_000055_permission_ownership", "创建 ownership 表"),
            ("INSERT INTO migration_000055_permission_ownership", "写入迁移前归属"),
            ("INSERT INTO permissions", "补齐四权限"),
            ("INSERT INTO role_permissions", "补齐管理员绑定"),
            ("DROP TEMPORARY TABLE migration_000055_assertions", "成功后删除断言表"),
        ],
    )

    for fragment, label in (
        ("MODIFY COLUMN code VARCHAR(64) NULL", "旧 code 保留并扩容"),
        ("ADD COLUMN code_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER code", "code_hash 二进制排序规则"),
        ("ADD COLUMN send_status VARCHAR(16) NOT NULL DEFAULT 'accepted'", "发送状态列"),
        ("ADD COLUMN business_request_no VARCHAR(64) NULL", "业务请求号"),
        ("ADD COLUMN idempotency_scope VARCHAR(191) NULL", "幂等作用域"),
        ("ADD COLUMN request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL", "请求指纹"),
        ("ADD COLUMN accepted_at DATETIME NULL", "供应商受理时间"),
        ("ADD COLUMN target_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL", "目标摘要"),
        ("ADD COLUMN target_masked VARCHAR(191) NULL", "脱敏目标"),
        ("code_hash = LOWER(SHA2(CONCAT('verification-retired-v2:', id, ':', UUID(), ':', UUID()), 256))", "历史验证码不可关联占位"),
        ("target_hash = LOWER(SHA2(CONCAT('email-target-retired-v2:', id, ':', UUID(), ':', UUID()), 256))", "历史邮箱不可关联占位"),
        ("target_masked = '历史邮箱已失效'", "历史邮箱统一占位"),
        ("expires_at = LEAST(expires_at, DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 1 SECOND))", "历史验证码立即过期"),
        ("used_at = COALESCE(used_at, CURRENT_TIMESTAMP)", "历史验证码标记已使用"),
        ("WHERE target_type = 'phone'", "手机兼容分支"),
        ("MODIFY COLUMN code_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL", "code_hash 最终非空"),
        ("ADD KEY idx_verification_email_target (target_type, target_hash, scene)", "邮件目标索引"),
        ("ADD UNIQUE KEY uk_verification_business_request (business_request_no)", "业务请求唯一索引"),
        ("ADD KEY idx_verification_email_idempotency (idempotency_scope, created_at)", "邮件幂等索引"),
    ):
        require_fragment(up, fragment, label)
    require_regex(
        up,
        r"UPDATE verification_codes SET code_hash = LOWER\(SHA2\(CONCAT\('verification-retired-v2:'.*?send_status = 'failed'.*?accepted_at = NULL.*?expires_at = LEAST\(expires_at, DATE_SUB\(CURRENT_TIMESTAMP, INTERVAL 1 SECOND\)\).*?used_at = COALESCE\(used_at, CURRENT_TIMESTAMP\).*?request_fingerprint = NULL;",
        "历史验证码必须在同一更新中失效并清空幂等字段",
    )

    expected_checks = {
        "chk_verification_code_hash", "chk_verification_send_status", "chk_verification_target_type",
        "chk_verification_target_shape", "chk_verification_email_acceptance",
        "chk_verification_email_idempotency", "chk_verification_request_fingerprint",
        "chk_verification_target_hash", "chk_email_templates_provider", "chk_email_templates_status",
        "chk_email_templates_variables_complete", "chk_email_templates_local_enabled",
        "chk_email_templates_missing", "chk_email_templates_missing_since",
        "chk_email_templates_content_sha256", "chk_email_scene_name", "chk_email_scene_provider",
        "chk_email_scene_enabled", "chk_email_sync_provider", "chk_email_sync_status",
        "chk_email_sync_hashes", "chk_email_sync_error", "chk_email_sync_completed_at",
        "chk_email_allowlist_hmac", "chk_email_allowlist_status", "chk_email_allowlist_revoked_at",
        "chk_email_send_scene", "chk_email_send_provider", "chk_email_send_purpose",
        "chk_email_send_status", "chk_email_send_hashes", "chk_email_send_result",
        "chk_email_send_purpose_shape", "chk_migration_000055_permission_created",
        "chk_migration_000055_binding_created",
    }
    actual_checks = set(re.findall(r"\bCONSTRAINT (chk_[a-z0-9_]+) CHECK\b", up, flags=re.IGNORECASE))
    actual_checks.discard("chk_migration_000055_assertion")
    require_exact_set(actual_checks, expected_checks, "CHECK")

    expected_fks = {
        "fk_email_scene_template", "fk_email_scene_updated_by", "fk_email_sync_created_by",
        "fk_email_allowlist_created_by", "fk_email_allowlist_updated_by",
        "fk_email_send_verification", "fk_email_send_template",
    }
    actual_fks = set(re.findall(r"\bCONSTRAINT (fk_[a-z0-9_]+) FOREIGN KEY\b", up, flags=re.IGNORECASE))
    require_exact_set(actual_fks, expected_fks, "外键")
    check(len(re.findall(r"\bFOREIGN KEY\b", up, flags=re.IGNORECASE)) == 7, "外键数量必须为 7")

    expected_indexes = {
        "idx_verification_email_target", "uk_verification_business_request",
        "idx_verification_email_idempotency", "uk_email_templates_provider_id",
        "idx_email_templates_status", "idx_email_templates_missing_cleanup",
        "uk_email_scene_bindings_scene", "idx_email_scene_bindings_template",
        "idx_email_scene_bindings_updated_by", "uk_email_sync_idem", "idx_email_sync_status",
        "idx_email_sync_completed", "idx_email_sync_created_by", "uk_email_test_allowlist_hmac",
        "idx_email_test_allowlist_cleanup", "idx_email_test_allowlist_created_by",
        "idx_email_test_allowlist_updated_by", "uk_email_send_logs_business_request",
        "uk_email_send_logs_verification", "uk_email_send_logs_idem", "idx_email_send_logs_scene",
        "idx_email_send_logs_status", "idx_email_send_logs_submitted_at", "idx_email_send_logs_template",
        "uk_migration_000055_permission_id", "uk_migration_000055_role_permission_id",
    }
    actual_indexes = set(re.findall(r"\b(?:UNIQUE )?KEY ([a-z][a-z0-9_]+) \(", up, flags=re.IGNORECASE))
    require_exact_set(actual_indexes, expected_indexes, "显式索引")

    table_requirements = {
        "email_provider_templates": [
            "provider_template_id VARCHAR(64) NOT NULL", "subject VARCHAR(256) NOT NULL",
            "template_text MEDIUMTEXT NOT NULL", "variables_json JSON NOT NULL",
            "content_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
            "provider_status VARCHAR(16) NOT NULL", "version BIGINT UNSIGNED NOT NULL DEFAULT 1",
        ],
        "email_scene_bindings": [
            "scene VARCHAR(32) NOT NULL", "template_id BIGINT UNSIGNED NULL",
            "variable_mapping_json JSON NOT NULL", "version BIGINT UNSIGNED NOT NULL DEFAULT 1",
            "FOREIGN KEY (template_id) REFERENCES email_provider_templates (id) ON DELETE RESTRICT",
        ],
        "email_template_sync_runs": [
            "idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
            "request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
            "status VARCHAR(16) NOT NULL", "created_by BIGINT UNSIGNED NOT NULL",
        ],
        "email_test_recipient_allowlist": [
            "email_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
            "email_masked VARCHAR(191) NOT NULL", "status VARCHAR(16) NOT NULL",
            "version BIGINT UNSIGNED NOT NULL DEFAULT 1",
        ],
        "email_send_logs": [
            "business_request_no VARCHAR(64) NOT NULL", "verification_code_id BIGINT UNSIGNED NULL",
            "provider_template_id VARCHAR(64) NOT NULL", "recipient_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
            "provider_request_id VARCHAR(128) NULL", "failure_reason VARCHAR(64) NULL",
            "expires_at DATETIME NULL", "submitted_at DATETIME NOT NULL",
        ],
        "migration_000055_permission_ownership": [
            "permission_code VARCHAR(191) NOT NULL", "permission_id BIGINT UNSIGNED NULL",
            "permission_created TINYINT(1) NOT NULL", "admin_role_permission_id BIGINT UNSIGNED NULL",
            "admin_binding_created TINYINT(1) NOT NULL", "PRIMARY KEY (permission_code)",
        ],
    }
    for table, fragments in table_requirements.items():
        block = table_block(up, table)
        for fragment in fragments:
            require_fragment(block, fragment, f"{table}.{fragment.split()[0]}")

    scenes = ("register", "login", "reset_password", "bind_email", "admin_verify")
    for scene in scenes:
        require_regex(
            up,
            rf"\('{scene}', 'aliyun_directmail', NULL, 0, JSON_OBJECT\('code', 'Code', 'expire_minutes', 'ExpireMinutes'\), 1, NULL\)",
            f"场景 {scene} 固定 seed",
        )
    check(len(re.findall(r"JSON_OBJECT\('code', 'Code', 'expire_minutes', 'ExpireMinutes'\)", up)) == 5, "变量映射必须恰好五份")

    permissions = {
        "email:template:view": ("查看邮件模板与发送记录", "view"),
        "email:template:manage": ("管理邮件模板与场景配置", "manage"),
        "email:template:sync": ("同步邮件模板", "sync"),
        "email:template:test": ("测试发送邮件模板", "test"),
    }
    permission_insert_match = re.search(
        r"INSERT INTO permissions \(code, name, resource, action\).*?;",
        up,
        flags=re.IGNORECASE | re.DOTALL,
    )
    check(permission_insert_match is not None, "缺少四权限 seed 语句")
    permission_insert = permission_insert_match.group(0) if permission_insert_match is not None else ""
    for code, (name, action) in permissions.items():
        require_regex(permission_insert, rf"'{re.escape(code)}'.*?'{re.escape(name)}'.*?'email_template'.*?'{action}'", f"权限 {code} 元数据")
    require_fragment(up, "IF(p.id IS NULL, 1, 0)", "权限预存标志")
    require_fragment(up, "IF(rp.id IS NULL, 1, 0)", "管理员绑定预存标志")
    require_fragment(up, "WHERE ownership.permission_id IS NULL", "ownership 写后强断言")
    require_fragment(up, "IF(COUNT(*) = 4, 1, 0) FROM migration_000055_permission_ownership", "ownership 精确四行")


def validate_down(down: str) -> None:
    require_order(
        down,
        [
            ("CREATE TEMPORARY TABLE migration_000055_down_assertions", "创建 down 断言表"),
            ("五张邮件表必须完整存在", "确认五业务表"),
            ("权限 ownership 标记表必须存在", "确认 ownership"),
            ("本 migration 新增权限不得存在未知角色、用户或分组引用", "未知引用门禁"),
            ("UPDATE verification_codes SET send_status = 'failed'", "先失效验证码"),
            ("DELETE rp FROM role_permissions", "精确删除管理员绑定"),
            ("DELETE p FROM permissions", "精确删除权限"),
            ("本 migration 新增的权限和 admin 关联必须已删除", "删除后断言"),
            ("DROP TABLE migration_000055_permission_ownership", "删除 ownership"),
            ("DROP TEMPORARY TABLE migration_000055_down_assertions", "删除 down 断言表"),
            ("DROP TABLE email_send_logs", "删除发送日志"),
            ("DROP TABLE email_test_recipient_allowlist", "删除白名单"),
            ("DROP TABLE email_template_sync_runs", "删除同步记录"),
            ("DROP TABLE email_scene_bindings", "删除场景绑定"),
            ("DROP TABLE email_provider_templates", "删除模板镜像"),
            ("DROP CHECK chk_verification_target_hash", "删除验证码 CHECK"),
            ("DROP INDEX idx_verification_email_idempotency", "删除验证码索引"),
            ("DROP COLUMN target_masked", "删除新增列"),
            ("MODIFY COLUMN code VARCHAR(64) NULL", "最终保留旧 code"),
        ],
    )

    for fragment, label in (
        ("FROM user_permission_overrides upo", "用户覆盖引用门禁"),
        ("FROM group_permissions gp", "分组权限引用门禁"),
        ("rp.id <> ownership.admin_role_permission_id", "未知角色引用门禁"),
        ("WHERE ownership.admin_binding_created = 1", "仅删除本迁移创建的管理员绑定"),
        ("WHERE ownership.permission_created = 1", "仅删除本迁移创建的权限"),
        ("expires_at = LEAST(expires_at, DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 1 SECOND))", "回滚前验证码过期"),
        ("used_at = COALESCE(used_at, CURRENT_TIMESTAMP)", "回滚前验证码标记已用"),
        ("MODIFY COLUMN code VARCHAR(64) NULL", "旧 code 保持 64 位可空"),
    ):
        require_fragment(down, fragment, label)

    require_regex(
        down,
        r"DELETE rp FROM role_permissions rp .*?WHERE ownership\.admin_binding_created = 1;",
        "管理员绑定删除语句必须携带 ownership 条件",
    )
    require_regex(
        down,
        r"DELETE p FROM permissions p .*?WHERE ownership\.permission_created = 1;",
        "权限删除语句必须携带 ownership 条件",
    )

    expected_drop_checks = {
        "chk_verification_target_hash", "chk_verification_request_fingerprint",
        "chk_verification_email_idempotency", "chk_verification_email_acceptance",
        "chk_verification_target_shape", "chk_verification_target_type",
        "chk_verification_send_status", "chk_verification_code_hash",
    }
    require_exact_set(set(re.findall(r"\bDROP CHECK (chk_[a-z0-9_]+)", down, flags=re.IGNORECASE)), expected_drop_checks, "down CHECK")

    expected_drop_indexes = {
        "idx_verification_email_idempotency", "uk_verification_business_request", "idx_verification_email_target"
    }
    require_exact_set(set(re.findall(r"\bDROP INDEX ([a-z0-9_]+)", down, flags=re.IGNORECASE)), expected_drop_indexes, "down 索引")

    expected_drop_columns = {
        "target_masked", "target_hash", "accepted_at", "request_fingerprint",
        "idempotency_scope", "business_request_no", "send_status", "code_hash",
    }
    require_exact_set(set(re.findall(r"\bDROP COLUMN ([a-z0-9_]+)", down, flags=re.IGNORECASE)), expected_drop_columns, "down 新增列")
    forbid_regex(down, r"\bDROP COLUMN (?:code|target_value)\b", "删除旧兼容列")


def validate_pair(up_text: str, down_text: str, *, enforce_hash: bool) -> None:
    if enforce_hash:
        validate_hashes(up_text.encode("utf-8"), down_text.encode("utf-8"))
    up = active_sql(up_text)
    down = active_sql(down_text)
    validate_forbidden_sql(up, down)
    validate_up(up)
    validate_down(down)


def replace_once(text: str, old: str, new: str, label: str) -> str:
    check(text.count(old) == 1, f"故障注入基线不唯一：{label}")
    return text.replace(old, new, 1)


def expect_contract_failure(name: str, mutate: Callable[[str, str], tuple[str, str]], up: str, down: str) -> None:
    changed_up, changed_down = mutate(up, down)
    check((changed_up, changed_down) != (up, down), f"故障注入未改变输入：{name}")
    try:
        validate_pair(changed_up, changed_down, enforce_hash=False)
    except ContractError:
        return
    raise ContractError(f"故障注入未被拦截：{name}")


def run_mutation_suite(up: str, down: str) -> int:
    mutations: list[tuple[str, Callable[[str, str], tuple[str, str]]]] = [
        ("删除模板表", lambda u, d: (re.sub(r"CREATE TABLE email_provider_templates \(.*?\) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;", "", u, count=1, flags=re.DOTALL), d)),
        ("缩短code_hash", lambda u, d: (u.replace("ADD COLUMN code_hash CHAR(64)", "ADD COLUMN code_hash CHAR(32)", 1), d)),
        ("破坏邮件目标索引", lambda u, d: (u.replace("(target_type, target_hash, scene)", "(target_type, scene)", 1), d)),
        ("删除验收CHECK", lambda u, d: (u.replace("chk_verification_email_acceptance", "chk_verification_email_acceptance_removed", 1), d)),
        ("放宽模板外键删除", lambda u, d: (u.replace("REFERENCES email_provider_templates (id) ON DELETE RESTRICT", "REFERENCES email_provider_templates (id) ON DELETE CASCADE", 1), d)),
        ("删除admin_verify场景", lambda u, d: (u.replace("  ('admin_verify', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL);", "  ('bind_email_duplicate', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL);", 1), d)),
        ("篡改变量映射", lambda u, d: (u.replace("JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes')", "JSON_OBJECT('code', 'code', 'expire_minutes', 'ExpireMinutes')", 1), d)),
        ("篡改权限元数据", lambda u, d: (u.replace("'同步邮件模板', 'email_template', 'sync'", "'管理全部邮件', 'email_template', 'sync'", 1), d)),
        ("ownership晚于权限写入", lambda u, d: (u.replace("CREATE TABLE migration_000055_permission_ownership", "CREATE TABLE migration_000055_permission_ownership_delayed", 1), d)),
        ("移除权限删除归属条件", lambda u, d: (u, d.replace("WHERE ownership.permission_created = 1;", ";", 1))),
        ("打乱业务表删除顺序", lambda u, d: (u, d.replace("DROP TABLE email_send_logs;\nDROP TABLE email_test_recipient_allowlist;", "DROP TABLE email_test_recipient_allowlist;\nDROP TABLE email_send_logs;", 1))),
        ("注入DROP DATABASE", lambda u, d: (u + "\nDROP DATABASE molin;\n", d)),
        ("可执行注释注入", lambda u, d: (u + "\n/*!50000 DROP DATABASE molin */;\n", d)),
        ("引用旧migration", lambda u, d: (u + "\nSELECT * FROM migration_000054_legacy;\n", d)),
        ("移除历史验证码失效", lambda u, d: (u.replace("send_status = 'failed',", "send_status = 'accepted',", 1), d)),
        ("删除回滚兼容列", lambda u, d: (u, d + "\nALTER TABLE verification_codes DROP COLUMN code;\n")),
    ]
    for name, mutate in mutations:
        expect_contract_failure(name, mutate, up, down)
    return len(mutations)


def main() -> int:
    up_bytes = UP_PATH.read_bytes()
    down_bytes = DOWN_PATH.read_bytes()
    try:
        up = up_bytes.decode("utf-8")
        down = down_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ContractError("000055 SQL 必须是无损 UTF-8") from exc

    # 先对原始字节冻结摘要，再执行全部语义检查；故障注入刻意关闭摘要门禁，证明语义断言自身有效。
    validate_hashes(up_bytes, down_bytes)
    validate_pair(up, down, enforce_hash=False)
    mutation_count = run_mutation_suite(up, down)
    print(
        "PASS migration=000055 mode=static "
        f"optimized={str(sys.flags.optimize > 0).lower()} checks={CHECK_COUNT} "
        f"mutations={mutation_count} remote_access=false db_scenarios=not_run"
    )
    print(f"up_sha256={UP_SHA256}")
    print(f"down_sha256={DOWN_SHA256}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ContractError as exc:
        print(f"FAIL migration=000055 mode=static reason={exc}", file=sys.stderr)
        raise SystemExit(1)

#!/usr/bin/env python3
"""离线校验 000056 migration 的关键安全契约，不连接数据库或外部服务。"""

from __future__ import annotations

import hashlib
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
UP_PATH = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.up.sql"
DOWN_PATH = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.down.sql"
CHECKS = 0


def require(text: str, fragment: str, label: str) -> None:
    global CHECKS
    if fragment not in active_sql(text):
        raise AssertionError(f"缺少契约片段：{label}")
    CHECKS += 1


def require_order(text: str, labels: list[tuple[str, str]]) -> None:
    global CHECKS
    text = active_sql(text)
    cursor = -1
    for fragment, label in labels:
        position = text.find(fragment)
        if position < 0:
            raise AssertionError(f"缺少顺序节点：{label}")
        if position <= cursor:
            raise AssertionError(f"顺序错误：{label}")
        cursor = position
        CHECKS += 1


def reject_pattern(text: str, pattern: str, label: str) -> None:
    global CHECKS
    if re.search(pattern, active_sql(text), flags=re.IGNORECASE | re.MULTILINE):
        raise AssertionError(f"出现禁止写法：{label}")
    CHECKS += 1


def active_sql(text: str) -> str:
    """移除三种 MySQL 注释并规范化空白，避免注释中的示例让契约检查假通过。"""
    without_comments = re.sub(r"/\*.*?\*/", " ", text, flags=re.DOTALL)
    without_comments = re.sub(r"(?m)#.*$", " ", without_comments)
    without_comments = re.sub(r"(?m)--[\t ].*$", " ", without_comments)
    return re.sub(r"\s+", " ", without_comments).strip()


def require_sql(text: str, pattern: str, label: str) -> None:
    """对实际 SQL（不含注释）做结构正则检查；本函数不宣称替代真实 MySQL。"""
    global CHECKS
    if not re.search(pattern, active_sql(text), flags=re.IGNORECASE):
        raise AssertionError(f"缺少有效 SQL 结构：{label}")
    CHECKS += 1


def main() -> int:
    up = UP_PATH.read_text(encoding="utf-8")
    down = DOWN_PATH.read_text(encoding="utf-8")

    comment_probe = "/* DELETE FROM permissions; */\n# DROP TABLE roles;\n-- INSERT INTO permissions;\nSELECT 1; -- DROP TABLE users;"
    if active_sql(comment_probe) != "SELECT 1;":
        raise AssertionError("MySQL 注释剥离自测失败")
    global CHECKS
    CHECKS += 1

    for text, direction in ((up, "up"), (down, "down")):
        require(text, "CREATE TABLE migration_000056_assertions", f"{direction} 持久断言表")
        require(text, "CHECK (passed = 1)", f"{direction} 断言失败关闭")
        reject_pattern(text, r"\bIF\s+(?:NOT\s+)?EXISTS\b", f"{direction} 模糊对象存在判断")
        reject_pattern(text, r"\bINSERT\s+IGNORE\b", f"{direction} 忽略冲突写入")
        reject_pattern(text, r"\bON\s+DUPLICATE\s+KEY\b", f"{direction} 隐式覆盖写入")

    for fragment, label in (
        ("CREATE TABLE email_admin_verify_bootstrap_receipts", "成功凭据表"),
        ("scope VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL", "scope 二进制存储"),
        ("provider VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL", "provider 二进制存储"),
        ("provider_template_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL", "模板标识 64 字节精确存储"),
        ("BINARY scope = BINARY 'admin_verify' AND OCTET_LENGTH(scope) = 12", "scope 字节精确约束"),
        ("BINARY provider = BINARY 'aliyun_directmail' AND OCTET_LENGTH(provider) = 17", "provider 字节精确约束"),
        ("provider_template_id REGEXP '^[0-9]{1,64}$'", "模板标识数字格式"),
        ("provider_template_id REGEXP '[1-9]'", "模板标识拒绝全零"),
        ("idempotency_key_hash REGEXP '^[0-9a-f]{64}$'", "幂等摘要格式"),
        ("request_fingerprint REGEXP '^[0-9a-f]{64}$'", "请求指纹格式"),
        ("FOREIGN KEY (template_id) REFERENCES email_provider_templates(id)", "模板外键"),
        ("FOREIGN KEY (completed_by) REFERENCES users(id)", "操作者外键"),
        ("ON DELETE RESTRICT ON UPDATE RESTRICT", "外键限制删除更新"),
        ("CREATE TABLE migration_000056_permission_ownership", "独立 ownership 表"),
        ("permission_code = 'email:template:bootstrap'", "ownership 权限封闭约束"),
        ("NOT (permission_created = 1 AND admin_binding_created = 0)", "ownership 状态形状"),
        ("'首次配置管理员邮箱认证模板'", "权限名称"),
        ("'email_template'", "权限资源"),
        ("'bootstrap'", "权限动作"),
        ("verification_codes 必须处于 000055 安全结构", "验证码 000055 基线"),
        ("verification_codes 必须保留 000055 三个精确索引", "验证码 000055 索引"),
        ("verification_codes 必须保留 000055 八个安全 CHECK", "验证码 000055 CHECK"),
        ("000055 ownership 必须恰好包含四个冻结权限码", "000055 ownership 行集"),
        ("000055 ownership 必须完整匹配四权限与 admin 绑定", "000055 ownership 关联"),
        ("000055 四个邮件权限元数据必须完整一致", "000055 权限元数据"),
        ("admin 必须完整关联 000055 四个邮件权限", "000055 admin 绑定"),
        ("五个邮件场景必须保持 000055 安全初始态", "000055 五场景初始态"),
        ("五场景 scene 唯一索引必须完整存在", "五场景唯一索引"),
    ):
        require(up, fragment, label)

    require_order(
        up,
        [
            ("CREATE TABLE migration_000056_assertions", "创建断言表"),
            ("000056 持久对象必须尚未创建", "检查目标对象不存在"),
            ("admin_verify 场景必须处于首次引导初始态", "检查绑定初始态"),
            ("CREATE TABLE email_admin_verify_bootstrap_receipts", "创建凭据表"),
            ("CREATE TABLE migration_000056_permission_ownership", "创建 ownership"),
            ("INSERT INTO migration_000056_permission_ownership", "记录预存状态"),
            ("INSERT INTO permissions", "补权限"),
            ("INSERT INTO role_permissions", "补 admin 绑定"),
            ("000056 ownership 必须完整匹配权限与 admin 绑定", "写后断言"),
            ("DROP TABLE migration_000056_assertions", "成功后删除断点证据"),
        ],
    )

    # 以下检查匹配去除注释后的实际 SQL，并固定关键断言的真值条件，避免仅凭关键词假通过。
    require_sql(
        up,
        r"SELECT '000055 ownership 必须恰好包含四个冻结权限码'.*?COUNT\(\*\) = 4.*?FROM migration_000055_permission_ownership;",
        "000055 ownership 精确四行断言",
    )
    require_sql(
        up,
        r"SELECT 'verification_codes 必须处于 000055 安全结构'.*?COUNT\(\*\) = 10.*?column_name = 'code_hash'.*?column_default IS NULL.*?collation_name = 'ascii_bin'.*?column_name = 'request_fingerprint'.*?column_default IS NULL.*?collation_name = 'ascii_bin'.*?column_name = 'target_hash'.*?column_default IS NULL.*?collation_name = 'ascii_bin'",
        "verification_codes 十列精确属性断言",
    )
    require_sql(
        up,
        r"SELECT 'verification_codes 必须保留 000055 三个精确索引'.*?COUNT\(\*\) = 3.*?sub_part IS NULL.*?prefix_parts = 0.*?target_type,target_hash,scene.*?business_request_no.*?idempotency_scope,created_at",
        "verification_codes 三索引完整列且无前缀断言",
    )
    require_sql(
        up,
        r"SELECT 'verification_codes 必须保留 000055 八个安全 CHECK'.*?COUNT\(\*\) = 8.*?information_schema.check_constraints.*?tc.enforced = 'YES'",
        "verification_codes 八个 enforced CHECK 查询基线",
    )
    require_sql(
        up,
        r"BINARY clause_raw = BINARY CONCAT\(.*?CHAR\(92\).*?CHAR\(39\)",
        "CHECK 原始文本按字节精确比较并显式保留引号转义",
    )
    for constraint_name, clause_fragment in (
        ("chk_verification_code_hash", "BINARY clause_raw = BINARY CONCAT('regexp_like(`code_hash`,_utf8mb4'"),
        ("chk_verification_send_status", "BINARY clause_raw = BINARY CONCAT('(`send_status` in (_utf8mb4'"),
        ("chk_verification_target_type", "BINARY clause_raw = BINARY CONCAT('(`target_type` in (_utf8mb4'"),
        ("chk_verification_target_shape", "BINARY clause_raw = BINARY CONCAT('(((`target_type` = _utf8mb4'"),
        ("chk_verification_email_acceptance", "BINARY clause_raw = BINARY CONCAT('((`target_type` <> _utf8mb4'"),
        ("chk_verification_email_idempotency", "`business_request_no` is null"),
        ("chk_verification_request_fingerprint", "regexp_like(`request_fingerprint`,_utf8mb4"),
        ("chk_verification_target_hash", "regexp_like(`target_hash`,_utf8mb4"),
    ):
        require(up, f"constraint_name = '{constraint_name}'", f"{constraint_name} 名称")
        require(up, clause_fragment, f"{constraint_name} 精确表达式")
    require_sql(
        up,
        r"SELECT '五个邮件场景必须保持 000055 安全初始态'.*?COUNT\(\*\) = 5.*?COUNT\(DISTINCT scene\) = 5.*?FROM email_scene_bindings.*?template_id IS NULL.*?enabled = 0.*?version = 1",
        "五场景完整初始态断言",
    )
    require_sql(
        up,
        r"SELECT '五场景 scene 唯一索引必须完整存在'.*?COUNT\(\*\) = 1.*?sub_part IS NULL.*?non_unique = 0.*?prefix_parts = 0.*?indexed_columns = 'scene'",
        "五场景唯一索引无前缀断言",
    )

    for fragment, label in (
        ("成功 bootstrap receipt 必须为空", "receipt 第一业务门禁"),
        ("本 migration 新增权限不得存在未知角色用户或分组引用", "未知引用门禁"),
        ("user_permission_overrides", "用户权限覆盖引用"),
        ("group_permissions", "分组权限引用"),
        ("ownership.admin_binding_created = 1", "精确删除 admin 绑定"),
        ("ownership.permission_created = 1", "精确删除权限"),
    ):
        require(down, fragment, label)

    require_order(
        down,
        [
            ("CREATE TABLE migration_000056_assertions", "创建 down 断言表"),
            ("成功 bootstrap receipt 必须为空", "先检查 receipt"),
            ("本 migration 新增权限不得存在未知角色用户或分组引用", "再检查未知引用"),
            ("DELETE rp FROM role_permissions", "删除 owned admin 绑定"),
            ("DELETE p FROM permissions", "删除 owned 权限"),
            ("本 migration 新增的权限必须已删除", "写后删除断言"),
            ("DROP TABLE email_admin_verify_bootstrap_receipts", "删除空凭据表"),
            ("DROP TABLE migration_000056_permission_ownership", "删除 ownership"),
            ("DROP TABLE migration_000056_assertions", "删除断点证据"),
        ],
    )

    require_sql(
        down,
        r"SELECT '成功 bootstrap receipt 必须为空', IF\(COUNT\(\*\) = 0, 1, 0\) FROM email_admin_verify_bootstrap_receipts;",
        "receipt 必须为空的真实断言",
    )
    require_sql(down, r"DELETE rp FROM role_permissions rp .*?admin_binding_created = 1;", "精确删除 owned admin 绑定")
    require_sql(down, r"DELETE p FROM permissions p .*?permission_created = 1;", "精确删除 owned 权限")
    require_sql(down, r"DROP TABLE email_admin_verify_bootstrap_receipts;", "主动删除空 receipt 表")
    require_sql(down, r"DROP TABLE migration_000056_permission_ownership;", "主动删除 ownership 表")

    # 只输出文件摘要与断言数量，不输出数据库配置、账号或任何运行时敏感值。
    up_hash = hashlib.sha256(up.encode("utf-8")).hexdigest().upper()
    down_hash = hashlib.sha256(down.encode("utf-8")).hexdigest().upper()
    print(f"PASS migration=000056 mode=static checks={CHECKS} remote_access=false db_scenarios=not_run")
    print(f"up_sha256={up_hash}")
    print(f"down_sha256={down_hash}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

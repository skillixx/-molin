#!/usr/bin/env python3
"""
PR#42（feature/backend-a-audit-log-request-summary, A-14）
GET /api/admin/audit-logs 响应新增 request_summary 字段 — 接口验收脚本

验收范围：
  1. 调用 GET /api/admin/audit-logs（管理员权限），确认每条记录包含 request_summary 字段
  2. 已有记录中 request_summary 非 null 时，响应中应为反序列化后的 JSON 对象/数组
     （不是被转义的字符串，例如不应是 "{\"user_id\":1}" 这种形式）
  3. request_summary 为 NULL 的记录，响应中应为 null（DB 中目前无此类记录，
     本脚本临时插入一条 NULL 记录用于验证，结束后清理）
  4. 结合 A-13（PR#38/#39）覆盖的 4 个操作（assign_role/revoke_role/
     set_permission_override/delete_permission_override）触发新的审计记录，
     验证新记录的 request_summary 内容与写入 DB 的内容一致
  5. 回归检查：其余字段（id/operator_id/module/action/target_type/target_id/ip/created_at）
     及分页结构（items + pagination）未被破坏
  6. 权限边界：非管理员调用 /api/admin/audit-logs 应返回 403（简要确认，不重复造数据）

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 ~/molin/test_pr42_audit_log_summary.py
"""

import json
import os
import subprocess
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed = 0
failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    msg = f"  {GREEN}PASS{RESET} {label}"
    if detail:
        msg += f"\n       {YELLOW}{detail}{RESET}"
    print(msg)
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}FAIL{RESET} {label}"
    if detail:
        msg += f"\n       {RED}{detail}{RESET}"
    print(msg)
    results.append(("FAIL", label, detail))


# ── HTTP 工具 ────────────────────────────────────────────────

def http(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL 查询工具 ────────────────────────────────────────────

def db_query(sql):
    cmd = [
        "mysql",
        "-h", MYSQL_HOST,
        f"-P{MYSQL_PORT}",
        f"-u{MYSQL_USER}",
        f"-p{MYSQL_PASS}",
        MYSQL_DB,
        "-N", "-e", sql,
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return None, result.stderr.strip()
        rows = []
        for line in result.stdout.strip().split("\n"):
            if line:
                rows.append(line.split("\t"))
        return rows, None
    except Exception as ex:
        return None, str(ex)


def db_exec(sql):
    """执行非查询 SQL（INSERT/DELETE），返回 (success, err)。"""
    cmd = [
        "mysql",
        "-h", MYSQL_HOST,
        f"-P{MYSQL_PORT}",
        f"-u{MYSQL_USER}",
        f"-p{MYSQL_PASS}",
        MYSQL_DB,
        "-e", sql,
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return False, result.stderr.strip()
        return True, None
    except Exception as ex:
        return False, str(ex)


# ── 测试数据 ────────────────────────────────────────────────

ADMIN_EMAIL = "aisiqin@163.com"
ADMIN_PASSWORD = "123456"

# 目标用户：PR#38/PR#31 验收时注册的普通测试账号，当前无任何角色/权限覆盖
TARGET_USER_ID = 147

# qa_buyer 角色（id=3）：无绑定权限的非关键测试角色，用于 assign/revoke 验收
TEST_ROLE_ID = 3
TEST_ROLE_CODE = "qa_buyer"

# report:export 权限（id=64）：用于 set/delete permission-override 验收
TEST_PERM_ID = 64
TEST_PERM_CODE = "report:export"

print(f"\n{BOLD}{CYAN}PR#42 audit-logs.request_summary 接口验收 — 开始{RESET}")
print(f"  API_BASE  : {API_BASE}")
print(f"  MYSQL     : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  目标用户  : user_id={TARGET_USER_ID}")
print(f"  测试角色  : role_id={TEST_ROLE_ID} ({TEST_ROLE_CODE})")
print(f"  测试权限  : permission_id={TEST_PERM_ID} ({TEST_PERM_CODE})")
print()


# ═══════════════════════════════════════════════════════════════
# 准备：管理员登录
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}准备：管理员登录{RESET}")
status, resp = http("POST", "/api/auth/login/email", {
    "email": ADMIN_EMAIL, "password": ADMIN_PASSWORD
})
print(f"       响应 HTTP {status}: code={resp.get('code')}")
if status != 200 or resp.get("code") != 0:
    fail("管理员登录失败，后续测试无法进行", json.dumps(resp, ensure_ascii=False))
    admin_token = None
    admin_user_id = None
else:
    admin_token = resp["data"]["access_token"]
    ok("管理员登录成功")
    rows, err = db_query(f"SELECT id FROM users WHERE email='{ADMIN_EMAIL}'")
    admin_user_id = int(rows[0][0]) if rows else None
    print(f"       admin_user_id={admin_user_id}")


# ═══════════════════════════════════════════════════════════════
# 前置检查：目标用户当前应无角色 / 无权限覆盖（避免污染）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置检查：目标用户初始状态{RESET}")
rows, err = db_query(f"SELECT role_id FROM user_roles WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_roles 失败", err)
elif rows:
    fail(f"目标用户已有角色，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无角色")

rows, err = db_query(f"SELECT id, permission_id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_permission_overrides 失败", err)
elif rows:
    fail(f"目标用户已有权限覆盖，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无权限覆盖")


# ═══════════════════════════════════════════════════════════════
# 1. 基本字段 + 分页结构回归检查
#    GET /api/admin/audit-logs?page=1&page_size=5
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}1. 基本响应结构回归检查（items + pagination + 各字段）{RESET}")

EXPECTED_FIELDS = {
    "id", "operator_id", "module", "action", "target_type",
    "target_id", "ip", "created_at", "request_summary",
}

if admin_token:
    status, resp = http("GET", "/api/admin/audit-logs?page=1&page_size=5", token=admin_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    data = resp.get("data", {})
    items = data.get("items")
    pagination = data.get("pagination")

    if status == 200 and resp.get("code") == 0 and isinstance(items, list) and isinstance(pagination, dict):
        ok("1.1 响应契约：HTTP 200, code=0, data.items 为数组, data.pagination 为对象")
    else:
        fail("1.1 响应契约不符合预期", f"resp={json.dumps(resp, ensure_ascii=False)[:500]}")

    if isinstance(pagination, dict) and {"page", "page_size", "total"} <= set(pagination.keys()):
        ok("1.2 分页结构 pagination 包含 page/page_size/total", f"pagination={pagination}")
    else:
        fail("1.2 分页结构不符合预期", f"pagination={pagination}")

    if items:
        sample = items[0]
        actual_fields = set(sample.keys())
        if EXPECTED_FIELDS <= actual_fields:
            ok("1.3 记录包含全部预期字段（含新增 request_summary）", f"fields={sorted(actual_fields)}")
        else:
            fail("1.3 记录缺少预期字段", f"缺少={EXPECTED_FIELDS - actual_fields}, 实际={sorted(actual_fields)}")

        # 回归字段类型检查（id/operator_id 为数字或 null，module/action/created_at 为字符串）
        regress_ok = (
            isinstance(sample.get("id"), int)
            and isinstance(sample.get("module"), str)
            and isinstance(sample.get("action"), str)
            and isinstance(sample.get("created_at"), str)
        )
        if regress_ok:
            ok("1.4 既有字段类型未被破坏（id/module/action/created_at）", f"sample={sample}")
        else:
            fail("1.4 既有字段类型异常", f"sample={sample}")
    else:
        fail("1.3/1.4 audit_logs 列表为空，无法检查字段", f"data={data}")
else:
    fail("1.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 2. 已有记录 request_summary 非 null -> 应为反序列化后的 JSON 对象/数组
#    （不应是被转义的字符串）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}2. 已有记录 request_summary 非 null 时应为 JSON 对象/数组（非字符串）{RESET}")

if admin_token:
    # 拉取较多记录，找一条 DB 中 request_summary 非 NULL 的记录做比对
    status, resp = http("GET", "/api/admin/audit-logs?page=1&page_size=20", token=admin_token)
    items = resp.get("data", {}).get("items", []) if status == 200 else []

    non_null_checked = False
    for item in items:
        log_id = item.get("id")
        rows, err = db_query(f"SELECT request_summary FROM audit_logs WHERE id={log_id}")
        if err or not rows:
            continue
        db_raw = rows[0][0]
        if db_raw is None or db_raw == "NULL":
            continue
        # DB 中非 NULL，校验响应中是对象/数组且内容与 DB 一致
        try:
            db_parsed = json.loads(db_raw)
        except Exception:
            db_parsed = None

        resp_summary = item.get("request_summary")
        is_object_or_array = isinstance(resp_summary, (dict, list))
        if is_object_or_array:
            ok(f"2.1 记录 id={log_id} 的 request_summary 是 JSON 对象/数组（非字符串）",
               f"type={type(resp_summary).__name__}, value={resp_summary}")
        else:
            fail(f"2.1 记录 id={log_id} 的 request_summary 不是 JSON 对象/数组",
                 f"实际类型={type(resp_summary).__name__}, value={resp_summary!r}")

        if db_parsed is not None and resp_summary == db_parsed:
            ok(f"2.2 记录 id={log_id} 的 request_summary 内容与 DB 反序列化结果一致",
               f"resp={resp_summary}")
        else:
            fail(f"2.2 记录 id={log_id} 的 request_summary 内容与 DB 不一致",
                 f"resp={resp_summary}, db_parsed={db_parsed}, db_raw={db_raw}")

        non_null_checked = True
        break

    if not non_null_checked:
        fail("2.x 未在前 20 条记录中找到 request_summary 非 NULL 的记录，无法验证", f"items_count={len(items)}")
else:
    fail("2.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 3. request_summary 为 NULL 的记录 -> 响应应为 null
#    DB 中目前无此类记录，临时插入一条用于验证，结束后清理
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}3. request_summary 为 NULL 的记录，响应应为 null{RESET}")

null_log_id = None
if admin_token:
    # 先确认 DB 中是否已存在 NULL 记录
    rows, err = db_query("SELECT id FROM audit_logs WHERE request_summary IS NULL ORDER BY id DESC LIMIT 1")
    if err:
        fail("3.x 查询 audit_logs 失败", err)
    elif rows:
        null_log_id = int(rows[0][0])
        print(f"  发现已存在 request_summary 为 NULL 的记录 id={null_log_id}")
    else:
        # 临时插入一条 NULL 记录用于验证（module 使用专属标识，结束后清理）
        ok_ins, err = db_exec(
            "INSERT INTO audit_logs (operator_id, module, action, target_type, target_id, ip, "
            "request_summary, created_at) VALUES "
            f"({admin_user_id}, 'qa_pr42', 'null_summary_probe', 'user', '{TARGET_USER_ID}', "
            "'127.0.0.1', NULL, NOW())"
        )
        if not ok_ins:
            fail("3.x 插入 NULL request_summary 测试记录失败", err)
        else:
            rows, err = db_query(
                "SELECT id FROM audit_logs WHERE module='qa_pr42' AND action='null_summary_probe' "
                "ORDER BY id DESC LIMIT 1"
            )
            if rows and not err:
                null_log_id = int(rows[0][0])
                print(f"  已插入临时 NULL 记录 id={null_log_id}（测试结束后清理）")
            else:
                fail("3.x 未能查到刚插入的临时 NULL 记录", f"err={err}")

    if null_log_id is not None:
        # 通过 module=qa_pr42（如为临时记录）或直接翻页查找该 id
        status, resp = http("GET", f"/api/admin/audit-logs?module=qa_pr42&page=1&page_size=5", token=admin_token)
        target_item = None
        if status == 200:
            for item in resp.get("data", {}).get("items", []):
                if item.get("id") == null_log_id:
                    target_item = item
                    break
        if target_item is None:
            # 不是临时记录（已存在的 NULL 记录），按 id 翻页查找
            page = 1
            while target_item is None:
                status, resp = http("GET", f"/api/admin/audit-logs?page={page}&page_size=50", token=admin_token)
                if status != 200:
                    break
                items = resp.get("data", {}).get("items", [])
                if not items:
                    break
                for item in items:
                    if item.get("id") == null_log_id:
                        target_item = item
                        break
                pagination = resp.get("data", {}).get("pagination", {})
                total = pagination.get("total", 0)
                if page * 50 >= total:
                    break
                page += 1

        if target_item is not None:
            if "request_summary" in target_item and target_item["request_summary"] is None:
                ok(f"3.1 记录 id={null_log_id} 的 request_summary 在响应中为 null",
                   f"item={target_item}")
            else:
                fail(f"3.1 记录 id={null_log_id} 的 request_summary 不为 null",
                     f"item={target_item}")
        else:
            fail(f"3.1 未在响应分页中找到记录 id={null_log_id}")
    else:
        fail("3.1 跳过（未获取到 NULL request_summary 记录）")
else:
    fail("3.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 工具：取审计日志中最新一条记录（按 module+action 过滤）
# ═══════════════════════════════════════════════════════════════
def latest_audit_log(module, action, token):
    status, resp = http("GET", f"/api/admin/audit-logs?module={module}&action={action}&page=1&page_size=1", token=token)
    if status != 200 or resp.get("code") != 0:
        return status, resp, None
    items = resp.get("data", {}).get("items", [])
    return status, resp, (items[0] if items else None)


def db_audit_payload(log_id):
    rows, err = db_query(f"SELECT request_summary FROM audit_logs WHERE id={log_id}")
    if err or not rows:
        return None
    raw = rows[0][0]
    try:
        return json.loads(raw)
    except Exception:
        return raw


# ═══════════════════════════════════════════════════════════════
# 4. A-13 四个操作 -> 触发新审计记录，验证 request_summary 与写入 DB 内容一致
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}4. A-13 四个操作触发新审计记录，验证 request_summary 内容{RESET}")

override_id = None

# 4.1 AssignRole -> assign_role
print(f"\n  {BOLD}4.1 POST /api/admin/users/{{id}}/roles (AssignRole) -> assign_role{RESET}")
if admin_token:
    status, resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/roles",
                         {"role_id": TEST_ROLE_ID, "reason": "PR42验收-分配角色"},
                         token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.1.0 AssignRole 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("4.1.0 AssignRole 响应契约不符合预期", f"实际 HTTP={status}, resp={resp}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "assign_role", admin_token)
    print(f"    审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        print(f"         request_summary(DB)   = {db_payload}")
        print(f"         request_summary(resp) = {resp_summary}")
        if isinstance(resp_summary, dict) and resp_summary.get("user_id") == TARGET_USER_ID and resp_summary.get("role_id") == TEST_ROLE_ID:
            ok("4.1.1 响应 request_summary 为对象，包含正确的 user_id/role_id", f"resp_summary={resp_summary}")
        else:
            fail("4.1.1 响应 request_summary 内容不符合预期", f"resp_summary={resp_summary}")

        if resp_summary == db_payload:
            ok("4.1.2 响应 request_summary 与 DB 反序列化结果一致")
        else:
            fail("4.1.2 响应 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.1.1/4.1.2 未查询到 assign_role 审计记录", f"HTTP={status}, resp={resp}")
else:
    fail("4.1.x 跳过（管理员登录失败）")


# 4.2 SetPermissionOverride -> set_permission_override
print(f"\n  {BOLD}4.2 POST /api/admin/users/{{id}}/permission-overrides (SetPermissionOverride) -> set_permission_override{RESET}")
if admin_token:
    status, resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/permission-overrides",
                         {"permission_id": TEST_PERM_ID, "effect": "allow", "reason": "PR42验收-设置权限覆盖"},
                         token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.2.0 SetPermissionOverride 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("4.2.0 SetPermissionOverride 响应契约不符合预期", f"实际 HTTP={status}, resp={resp}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "set_permission_override", admin_token)
    print(f"    审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        print(f"         request_summary(DB)   = {db_payload}")
        print(f"         request_summary(resp) = {resp_summary}")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("permission_id") == TEST_PERM_ID
                and resp_summary.get("permission_code") == TEST_PERM_CODE
                and resp_summary.get("effect") == "allow"):
            ok("4.2.1 响应 request_summary 为对象，包含正确的 user_id/permission_id/permission_code/effect", f"resp_summary={resp_summary}")
        else:
            fail("4.2.1 响应 request_summary 内容不符合预期", f"resp_summary={resp_summary}")

        if resp_summary == db_payload:
            ok("4.2.2 响应 request_summary 与 DB 反序列化结果一致")
        else:
            fail("4.2.2 响应 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.2.1/4.2.2 未查询到 set_permission_override 审计记录", f"HTTP={status}, resp={resp}")

    # 查询刚创建的 override id，供下一步使用
    rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID} AND permission_id={TEST_PERM_ID}")
    if rows and not err:
        override_id = int(rows[0][0])
        print(f"         override_id={override_id}")
    else:
        fail("4.2.x 未能查到新建的 permission override 记录", f"err={err}")
else:
    fail("4.2.x 跳过（管理员登录失败）")


# 4.3 DeletePermissionOverride -> delete_permission_override
print(f"\n  {BOLD}4.3 DELETE /api/admin/users/{{id}}/permission-overrides/{{override_id}} (DeletePermissionOverride) -> delete_permission_override{RESET}")
if admin_token and override_id:
    status, resp = http("DELETE", f"/api/admin/users/{TARGET_USER_ID}/permission-overrides/{override_id}", token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.3.0 DeletePermissionOverride 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("4.3.0 DeletePermissionOverride 响应契约不符合预期", f"实际 HTTP={status}, resp={resp}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "delete_permission_override", admin_token)
    print(f"    审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        print(f"         request_summary(DB)   = {db_payload}")
        print(f"         request_summary(resp) = {resp_summary}")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("override_id") == override_id):
            ok("4.3.1 响应 request_summary 为对象，包含正确的 user_id/override_id", f"resp_summary={resp_summary}")
        else:
            fail("4.3.1 响应 request_summary 内容不符合预期", f"resp_summary={resp_summary}")

        if resp_summary == db_payload:
            ok("4.3.2 响应 request_summary 与 DB 反序列化结果一致")
        else:
            fail("4.3.2 响应 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.3.1/4.3.2 未查询到 delete_permission_override 审计记录", f"HTTP={status}, resp={resp}")

    rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE id={override_id}")
    if not rows and not err:
        ok("4.3.3 user_permission_overrides 记录已删除")
    else:
        fail("4.3.3 user_permission_overrides 记录未删除", f"rows={rows}")
else:
    fail("4.3.x 跳过（前置条件不满足：override_id 未获取到）")


# 4.4 RevokeRole -> revoke_role
print(f"\n  {BOLD}4.4 DELETE /api/admin/users/{{id}}/roles/{{role_id}} (RevokeRole) -> revoke_role{RESET}")
if admin_token:
    status, resp = http("DELETE", f"/api/admin/users/{TARGET_USER_ID}/roles/{TEST_ROLE_ID}", token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.4.0 RevokeRole 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("4.4.0 RevokeRole 响应契约不符合预期", f"实际 HTTP={status}, resp={resp}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "revoke_role", admin_token)
    print(f"    审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        print(f"         request_summary(DB)   = {db_payload}")
        print(f"         request_summary(resp) = {resp_summary}")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("role_id") == TEST_ROLE_ID):
            ok("4.4.1 响应 request_summary 为对象，包含正确的 user_id/role_id", f"resp_summary={resp_summary}")
        else:
            fail("4.4.1 响应 request_summary 内容不符合预期", f"resp_summary={resp_summary}")

        if resp_summary == db_payload:
            ok("4.4.2 响应 request_summary 与 DB 反序列化结果一致")
        else:
            fail("4.4.2 响应 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.4.1/4.4.2 未查询到 revoke_role 审计记录", f"HTTP={status}, resp={resp}")

    rows, err = db_query(f"SELECT id FROM user_roles WHERE user_id={TARGET_USER_ID} AND role_id={TEST_ROLE_ID}")
    if not rows and not err:
        ok("4.4.3 user_roles 记录已删除（恢复初始状态）")
    else:
        fail("4.4.3 user_roles 记录未删除", f"rows={rows}")
else:
    fail("4.4.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 5. 权限校验：普通用户调用 /api/admin/audit-logs -> 403
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}5. 权限校验：非管理员调用 /api/admin/audit-logs{RESET}")

status, resp = http("POST", "/api/auth/login/email", {
    "email": "pr31norm81328894@testmail.io", "password": "Test@Pr31x"
})
print(f"  普通用户登录  HTTP {status}: code={resp.get('code')}")
normal_token = resp.get("data", {}).get("access_token") if status == 200 else None

if normal_token:
    status, resp = http("GET", "/api/admin/audit-logs", token=normal_token)
    print(f"  调用 audit-logs HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 403:
        ok("5.1 普通用户调用 /api/admin/audit-logs 返回 403", f"code={resp.get('code')}")
    else:
        fail("5.1 普通用户调用 /api/admin/audit-logs 应返回 403",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("5.1 跳过（普通用户登录失败，无法验证权限边界）", json.dumps(resp, ensure_ascii=False))


# ═══════════════════════════════════════════════════════════════
# 清理：删除本脚本临时插入的 NULL request_summary 记录
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除临时插入的测试数据{RESET}")
ok_del, err = db_exec("DELETE FROM audit_logs WHERE module='qa_pr42' AND action='null_summary_probe'")
if ok_del:
    print(f"  已清理 module='qa_pr42' 的临时审计记录")
else:
    print(f"  {YELLOW}清理临时记录时出现问题: {err}{RESET}")


# ═══════════════════════════════════════════════════════════════
# 汇总
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}{CYAN}═══ 测试结果汇总 ═══{RESET}")
total = passed + failed
print(f"  总计: {total}  {GREEN}通过: {passed}{RESET}  {RED}失败: {failed}{RESET}")
print()
for status_label, label, detail in results:
    color = GREEN if status_label == "PASS" else RED
    print(f"  {color}[{status_label}]{RESET} {label}")

print()
if failed == 0:
    print(f"{BOLD}{GREEN}结论：PR#42 全部测试用例通过，建议可以合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#42 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

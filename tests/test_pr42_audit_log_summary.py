#!/usr/bin/env python3
"""
PR#42（feature/backend-a-audit-log-request-summary, A-14）
GET /api/admin/audit-logs 响应新增 request_summary 字段 — 接口验收脚本

修复记录：
  - 管理员账号改为动态创建（原硬编码 aisiqin@example.com）
  - TARGET_USER_ID 改为动态创建（原硬编码 id=147）
  - TEST_ROLE_ID/TEST_PERM_ID 改为从 DB 按 code 查询（原硬编码 id）
  - 普通用户权限边界测试改为使用 target 用户（原硬编码 pr31norm）
  - 新增 import hashlib

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 tests/test_pr42_audit_log_summary.py
"""

import hashlib
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


# ── MySQL 工具 ────────────────────────────────────────────────

def db_query(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}",
           f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
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
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}",
           f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return False, result.stderr.strip()
        return True, None
    except Exception as ex:
        return False, str(ex)


# ── 注册工具：DB 插入验证码 + API 注册 ────────────────────────

def register_user_via_api(email, phone, password, username=None):
    """通过 DB 插入验证码后调用注册接口，返回 (user_id, access_token)。"""
    otp_code     = "888888"
    otp_code_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql   = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone', '{phone}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email', '{email}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    body = {"email": email, "phone": phone, "password": password,
            "phone_code": otp_code, "email_code": otp_code}
    if username:
        body["username"] = username
    s, r = http("POST", "/api/auth/register", body)
    if s in (200, 201) and r.get("code") == 0:
        token = r.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        uid = int(rows[0][0]) if rows else None
        return uid, token
    print(f"    {RED}注册失败: HTTP={s}, {json.dumps(r, ensure_ascii=False)[:200]}{RESET}")
    return None, None


# ── 时间戳 + 动态账号 ───────────────────────────────────────

TS = int(time.time())

ADMIN_EMAIL    = f"pr42adm{TS}@testmail.io"
ADMIN_PHONE    = f"170{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr42Admin"

TARGET_EMAIL   = f"pr42tgt{TS}@testmail.io"
TARGET_PHONE   = f"171{TS % 100000000:08d}"
TARGET_PASSWORD = "Test@Pr42Target"

print(f"\n{BOLD}{CYAN}PR#42 audit-logs.request_summary 接口验收 — 开始{RESET}")
print(f"  API_BASE  : {API_BASE}")
print(f"  MYSQL     : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  时间戳    : {TS}")
print()


# ═══════════════════════════════════════════════════════════════
# 前置准备：创建测试账号
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}前置准备：创建测试账号{RESET}")

rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = int(rows[0][0]) if rows else None
if not admin_role_id:
    db_exec("INSERT IGNORE INTO roles (code, name) VALUES ('admin', '管理员')")
    rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
    admin_role_id = int(rows[0][0]) if rows else None

print(f"\n  创建管理员账号 {ADMIN_EMAIL}...")
admin_user_id, _ = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"pr42adm{TS}")
if not admin_user_id:
    print(f"  {RED}管理员注册失败，中止{RESET}")
    raise SystemExit(1)
if admin_role_id:
    db_exec(f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
            f"SELECT {admin_role_id}, p.id FROM permissions p")
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({admin_user_id}, {admin_role_id})")
db_exec(f"UPDATE users SET admin_phone_verified_at=NOW(), admin_email_verified_at=NOW() WHERE id={admin_user_id}")
print(f"  管理员注册成功: id={admin_user_id}")

print(f"\n  创建目标普通用户 {TARGET_EMAIL}...")
target_user_id, _ = register_user_via_api(TARGET_EMAIL, TARGET_PHONE, TARGET_PASSWORD, f"pr42tgt{TS}")
if not target_user_id:
    print(f"  {RED}目标用户注册失败，中止{RESET}")
    raise SystemExit(1)
print(f"  目标用户注册成功: id={target_user_id}")

TARGET_USER_ID = target_user_id

# 查找测试用角色（优先任意非 admin 角色，不存在则临时创建）
rows, _ = db_query("SELECT id, code FROM roles WHERE code != 'admin' LIMIT 1")
if rows:
    TEST_ROLE_ID   = int(rows[0][0])
    TEST_ROLE_CODE = rows[0][1]
else:
    tmp_code = f"qa_tmp{TS}"
    db_exec(f"INSERT INTO roles (code, name) VALUES ('{tmp_code}', 'PR42临时角色')")
    rows, _ = db_query(f"SELECT id FROM roles WHERE code='{tmp_code}'")
    TEST_ROLE_ID   = int(rows[0][0]) if rows else None
    TEST_ROLE_CODE = tmp_code

# 查找测试用权限（取第一个存在的权限码）
rows, _ = db_query("SELECT id, code FROM permissions LIMIT 1")
TEST_PERM_ID   = int(rows[0][0]) if rows else None
TEST_PERM_CODE = rows[0][1] if rows else None

print(f"\n  测试角色 : id={TEST_ROLE_ID} ({TEST_ROLE_CODE})")
print(f"  测试权限 : id={TEST_PERM_ID} ({TEST_PERM_CODE})")


# ═══════════════════════════════════════════════════════════════
# 准备：管理员登录
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}准备：管理员登录{RESET}")
status, resp = http("POST", "/api/auth/login/email",
                    {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
print(f"       响应 HTTP {status}: code={resp.get('code')}")
if status != 200 or resp.get("code") != 0:
    fail("管理员登录失败，后续测试无法进行", json.dumps(resp, ensure_ascii=False))
    admin_token = None
else:
    admin_token = resp["data"]["access_token"]
    ok("管理员登录成功")


# ═══════════════════════════════════════════════════════════════
# 前置检查：目标用户当前应无角色 / 无权限覆盖
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置检查：目标用户初始状态{RESET}")
rows, err = db_query(f"SELECT role_id FROM user_roles WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_roles 失败", err)
elif rows:
    fail("目标用户已有角色，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无角色")

rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_permission_overrides 失败", err)
elif rows:
    fail("目标用户已有权限覆盖，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无权限覆盖")


# ═══════════════════════════════════════════════════════════════
# 1. 基本字段 + 分页结构回归检查（D-95 扁平分页）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}1. 基本响应结构回归检查（items + 扁平分页字段 + 各字段）{RESET}")

EXPECTED_FIELDS = {
    "id", "operator_id", "module", "action", "target_type",
    "target_id", "ip", "created_at", "request_summary",
}

if admin_token:
    status, resp = http("GET", "/api/admin/audit-logs?page=1&page_size=5", token=admin_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    data = resp.get("data", {})
    items = data.get("items")

    if status == 200 and resp.get("code") == 0 and isinstance(items, list):
        ok("1.1 响应契约：HTTP 200, code=0, data.items 为数组")
    else:
        fail("1.1 响应契约不符合预期", f"resp={json.dumps(resp, ensure_ascii=False)[:500]}")

    if "pagination" not in data and {"page", "page_size", "total"} <= set(data.keys()):
        ok("1.2 分页结构已扁平化（D-95）",
           f"page={data.get('page')}, page_size={data.get('page_size')}, total={data.get('total')}")
    else:
        fail("1.2 分页结构不符合预期（D-95 扁平结构）", f"data keys={sorted(data.keys())}")

    if items:
        sample = items[0]
        actual_fields = set(sample.keys())
        if EXPECTED_FIELDS <= actual_fields:
            ok("1.3 记录包含全部预期字段（含新增 request_summary）", f"fields={sorted(actual_fields)}")
        else:
            fail("1.3 记录缺少预期字段", f"缺少={EXPECTED_FIELDS - actual_fields}")

        regress_ok = (
            isinstance(sample.get("id"), int)
            and isinstance(sample.get("module"), str)
            and isinstance(sample.get("action"), str)
            and isinstance(sample.get("created_at"), str)
        )
        if regress_ok:
            ok("1.4 既有字段类型未被破坏（id/module/action/created_at）")
        else:
            fail("1.4 既有字段类型异常", f"sample={sample}")
    else:
        fail("1.3/1.4 audit_logs 列表为空，无法检查字段")
else:
    fail("1.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 2. 已有记录 request_summary 非 null → 应为 JSON 对象/数组（非字符串）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}2. 已有记录 request_summary 非 null 时应为 JSON 对象/数组{RESET}")

if admin_token:
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
        try:
            db_parsed = json.loads(db_raw)
        except Exception:
            db_parsed = None

        resp_summary = item.get("request_summary")
        if isinstance(resp_summary, (dict, list)):
            ok(f"2.1 记录 id={log_id} request_summary 是 JSON 对象/数组",
               f"type={type(resp_summary).__name__}")
        else:
            fail(f"2.1 记录 id={log_id} request_summary 不是 JSON 对象/数组",
                 f"实际类型={type(resp_summary).__name__}, value={resp_summary!r}")

        if db_parsed is not None and resp_summary == db_parsed:
            ok(f"2.2 记录 id={log_id} request_summary 与 DB 反序列化结果一致")
        else:
            fail(f"2.2 记录 id={log_id} request_summary 与 DB 不一致",
                 f"resp={resp_summary}, db={db_parsed}")

        non_null_checked = True
        break

    if not non_null_checked:
        fail("2.x 前 20 条记录中未找到 request_summary 非 NULL 的记录", f"items_count={len(items)}")
else:
    fail("2.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 3. request_summary 为 NULL 的记录 → 响应应为 null
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}3. request_summary 为 NULL 的记录，响应应为 null{RESET}")

null_log_id = None
if admin_token:
    rows, err = db_query("SELECT id FROM audit_logs WHERE request_summary IS NULL ORDER BY id DESC LIMIT 1")
    if err:
        fail("3.x 查询 audit_logs 失败", err)
    elif rows:
        null_log_id = int(rows[0][0])
        print(f"  发现已存在 request_summary=NULL 的记录 id={null_log_id}")
    else:
        ok_ins, err = db_exec(
            "INSERT INTO audit_logs (operator_id, module, action, target_type, target_id, ip, "
            "request_summary, created_at) VALUES "
            f"({admin_user_id}, 'qa_pr42', 'null_summary_probe', 'user', '{TARGET_USER_ID}', "
            "'127.0.0.1', NULL, NOW())"
        )
        if not ok_ins:
            fail("3.x 插入临时 NULL 记录失败", err)
        else:
            rows, err = db_query(
                "SELECT id FROM audit_logs WHERE module='qa_pr42' AND action='null_summary_probe' "
                "ORDER BY id DESC LIMIT 1"
            )
            if rows and not err:
                null_log_id = int(rows[0][0])
                print(f"  已插入临时 NULL 记录 id={null_log_id}")
            else:
                fail("3.x 未能查到刚插入的临时 NULL 记录", f"err={err}")

    if null_log_id is not None:
        status, resp = http("GET", "/api/admin/audit-logs?module=qa_pr42&page=1&page_size=5", token=admin_token)
        target_item = None
        if status == 200:
            for item in resp.get("data", {}).get("items", []):
                if item.get("id") == null_log_id:
                    target_item = item
                    break
        if target_item is None:
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
                total = resp.get("data", {}).get("total", 0)
                if page * 50 >= total:
                    break
                page += 1

        if target_item is not None:
            if "request_summary" in target_item and target_item["request_summary"] is None:
                ok(f"3.1 记录 id={null_log_id} request_summary 在响应中为 null")
            else:
                fail(f"3.1 记录 id={null_log_id} request_summary 不为 null", f"item={target_item}")
        else:
            fail(f"3.1 未在响应分页中找到记录 id={null_log_id}")
    else:
        fail("3.1 跳过（未获取到 NULL request_summary 记录）")
else:
    fail("3.x 跳过（管理员登录失败）")


# ── 审计日志查询工具 ──────────────────────────────────────────

def latest_audit_log(module, action, token):
    status, resp = http(
        "GET", f"/api/admin/audit-logs?module={module}&action={action}&page=1&page_size=1",
        token=token
    )
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
# 4. A-13 四个操作 → 触发审计记录，验证 request_summary 与 DB 一致
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}4. A-13 四个操作触发审计记录，验证 request_summary 内容{RESET}")

override_id = None

# 4.1 AssignRole
print(f"\n  {BOLD}4.1 AssignRole → assign_role 审计记录{RESET}")
if admin_token and TEST_ROLE_ID:
    status, resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/roles",
                        {"role_id": TEST_ROLE_ID, "reason": "PR42验收-分配角色"},
                        token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.1.0 AssignRole 响应契约：HTTP 200, code=0, data=null")
    else:
        fail("4.1.0 AssignRole 响应契约不符合预期", f"HTTP={status}, resp={resp}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "assign_role", admin_token)
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        if isinstance(resp_summary, dict) and resp_summary.get("user_id") == TARGET_USER_ID and resp_summary.get("role_id") == TEST_ROLE_ID:
            ok("4.1.1 request_summary 为对象，含正确 user_id/role_id", f"resp_summary={resp_summary}")
        else:
            fail("4.1.1 request_summary 内容不符合预期", f"resp_summary={resp_summary}")
        if resp_summary == db_payload:
            ok("4.1.2 request_summary 与 DB 反序列化结果一致")
        else:
            fail("4.1.2 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.1.1/4.1.2 未查询到 assign_role 审计记录", f"HTTP={status}")
else:
    fail("4.1.x 跳过（admin_token 或 TEST_ROLE_ID 不可用）")

# 4.2 SetPermissionOverride
print(f"\n  {BOLD}4.2 SetPermissionOverride → set_permission_override 审计记录{RESET}")
if admin_token and TEST_PERM_ID:
    status, resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/permission-overrides",
                        {"permission_id": TEST_PERM_ID, "effect": "allow", "reason": "PR42验收-设置权限覆盖"},
                        token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.2.0 SetPermissionOverride 响应契约：HTTP 200, code=0, data=null")
    else:
        fail("4.2.0 SetPermissionOverride 响应契约不符合预期", f"HTTP={status}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "set_permission_override", admin_token)
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("permission_id") == TEST_PERM_ID
                and resp_summary.get("permission_code") == TEST_PERM_CODE
                and resp_summary.get("effect") == "allow"):
            ok("4.2.1 request_summary 含正确 user_id/permission_id/permission_code/effect")
        else:
            fail("4.2.1 request_summary 内容不符合预期", f"resp_summary={resp_summary}")
        if resp_summary == db_payload:
            ok("4.2.2 request_summary 与 DB 一致")
        else:
            fail("4.2.2 request_summary 与 DB 不一致", f"resp={resp_summary}, db={db_payload}")
    else:
        fail("4.2.1/4.2.2 未查询到 set_permission_override 审计记录")

    rows, err = db_query(
        f"SELECT id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID} AND permission_id={TEST_PERM_ID}"
    )
    if rows and not err:
        override_id = int(rows[0][0])
        print(f"         override_id={override_id}")
    else:
        fail("4.2.x 未能查到新建的 permission override 记录", f"err={err}")
else:
    fail("4.2.x 跳过（admin_token 或 TEST_PERM_ID 不可用）")

# 4.3 DeletePermissionOverride
print(f"\n  {BOLD}4.3 DeletePermissionOverride → delete_permission_override 审计记录{RESET}")
if admin_token and override_id:
    status, resp = http("DELETE",
                        f"/api/admin/users/{TARGET_USER_ID}/permission-overrides/{override_id}",
                        token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.3.0 DeletePermissionOverride 响应契约：HTTP 200, code=0, data=null")
    else:
        fail("4.3.0 DeletePermissionOverride 响应契约不符合预期", f"HTTP={status}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "delete_permission_override", admin_token)
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("override_id") == override_id):
            ok("4.3.1 request_summary 含正确 user_id/override_id")
        else:
            fail("4.3.1 request_summary 内容不符合预期", f"resp_summary={resp_summary}")
        if resp_summary == db_payload:
            ok("4.3.2 request_summary 与 DB 一致")
        else:
            fail("4.3.2 request_summary 与 DB 不一致")
    else:
        fail("4.3.1/4.3.2 未查询到 delete_permission_override 审计记录")

    rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE id={override_id}")
    if not rows and not err:
        ok("4.3.3 user_permission_overrides 记录已删除")
    else:
        fail("4.3.3 user_permission_overrides 记录未删除", f"rows={rows}")
else:
    fail("4.3.x 跳过（override_id 未获取到）")

# 4.4 RevokeRole
print(f"\n  {BOLD}4.4 RevokeRole → revoke_role 审计记录{RESET}")
if admin_token and TEST_ROLE_ID:
    status, resp = http("DELETE",
                        f"/api/admin/users/{TARGET_USER_ID}/roles/{TEST_ROLE_ID}",
                        token=admin_token)
    print(f"    调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.4.0 RevokeRole 响应契约：HTTP 200, code=0, data=null")
    else:
        fail("4.4.0 RevokeRole 响应契约不符合预期", f"HTTP={status}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "revoke_role", admin_token)
    if status == 200 and log is not None:
        db_payload = db_audit_payload(log["id"])
        resp_summary = log.get("request_summary")
        if (isinstance(resp_summary, dict)
                and resp_summary.get("user_id") == TARGET_USER_ID
                and resp_summary.get("role_id") == TEST_ROLE_ID):
            ok("4.4.1 request_summary 含正确 user_id/role_id")
        else:
            fail("4.4.1 request_summary 内容不符合预期", f"resp_summary={resp_summary}")
        if resp_summary == db_payload:
            ok("4.4.2 request_summary 与 DB 一致")
        else:
            fail("4.4.2 request_summary 与 DB 不一致")
    else:
        fail("4.4.1/4.4.2 未查询到 revoke_role 审计记录")

    rows, err = db_query(
        f"SELECT id FROM user_roles WHERE user_id={TARGET_USER_ID} AND role_id={TEST_ROLE_ID}"
    )
    if not rows and not err:
        ok("4.4.3 user_roles 记录已删除（恢复初始状态）")
    else:
        fail("4.4.3 user_roles 记录未删除", f"rows={rows}")
else:
    fail("4.4.x 跳过（admin_token 或 TEST_ROLE_ID 不可用）")


# ═══════════════════════════════════════════════════════════════
# 5. 权限校验：目标普通用户调用 /api/admin/audit-logs → 403
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}5. 权限校验：非管理员调用 /api/admin/audit-logs{RESET}")

status, resp = http("POST", "/api/auth/login/email",
                    {"email": TARGET_EMAIL, "password": TARGET_PASSWORD})
print(f"  普通用户登录  HTTP {status}: code={resp.get('code')}")
normal_token = resp.get("data", {}).get("access_token") if status == 200 else None

if normal_token:
    status, resp = http("GET", "/api/admin/audit-logs", token=normal_token)
    print(f"  调用 audit-logs HTTP {status}: code={resp.get('code')}")
    if status == 403:
        ok("5.1 普通用户调用 /api/admin/audit-logs 返回 403", f"code={resp.get('code')}")
    else:
        fail("5.1 普通用户调用应返回 403", f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("5.1 跳过（普通用户登录失败）", json.dumps(resp, ensure_ascii=False))


# ═══════════════════════════════════════════════════════════════
# 清理
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除临时测试数据{RESET}")
db_exec("DELETE FROM audit_logs WHERE module='qa_pr42' AND action='null_summary_probe'")
print("  已清理 qa_pr42 临时审计记录")

for uid in [admin_user_id, target_user_id]:
    if uid:
        db_exec(f"DELETE FROM user_roles WHERE user_id={uid}")
        db_exec(f"DELETE FROM user_permission_overrides WHERE user_id={uid}")
        db_exec(f"DELETE FROM user_sessions WHERE user_id={uid}")
        db_exec(f"DELETE FROM users WHERE id={uid}")
print(f"  已清理测试账号: admin_id={admin_user_id}, target_id={target_user_id}")


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

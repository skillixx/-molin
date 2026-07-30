#!/usr/bin/env python3
"""
PR#47（feature/backend-a-seed-identity-review-permission, A-15）
identity:review 权限种子修复 — 接口验收脚本

修复记录：
  - 管理员/普通用户账号改为动态创建（原硬编码 aisiqin@example.com、pr31norm）
  - 测试用户 A/B 改为通过 register_user_via_api 注册（原依赖 DB hash 复制）
  - D-89：审核接口格式 {"approve":true/false} → {"action":"approve"/"reject","reject_reason":...}
  - D-90：普通用户自查路由 /me → /latest

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 tests/test_pr47_identity_review_permission.py
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


# ── 注册工具：DB 插入验证码 + API 注册 ──────────────────────

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

ADMIN_EMAIL    = f"pr47adm{TS}@testmail.io"
ADMIN_PHONE    = f"170{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr47Admin"

NORMAL_EMAIL   = f"pr47norm{TS}@testmail.io"
NORMAL_PHONE   = f"173{TS % 100000000:08d}"
NORMAL_PASSWORD = "Test@Pr47Norm"

USER_A_EMAIL    = f"pr47_approve_{TS}@testmail.io"
USER_A_PHONE    = f"138{TS % 100000000:08d}"
USER_A_PASSWORD = "Test@Pr47UserA"

USER_B_EMAIL    = f"pr47_reject_{TS}@testmail.io"
USER_B_PHONE    = f"139{TS % 100000000:08d}"
USER_B_PASSWORD = "Test@Pr47UserB"

print(f"\n{BOLD}{CYAN}PR#47 identity:review 权限种子修复 — 接口验收{RESET}")
print(f"  API_BASE    : {API_BASE}")
print(f"  MYSQL       : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  时间戳      : {TS}")
print()


# ═══════════════════════════════════════════════════════════════
# 1. DB 确认：permissions 表包含 identity:review
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}1. DB 确认：permissions 表包含 identity:review 行{RESET}")

rows, err = db_query("SELECT code, name, resource, action FROM permissions WHERE code='identity:review'")
if err:
    fail("1.1 查询 permissions 表失败", err)
elif not rows:
    fail("1.1 permissions 表中不存在 code='identity:review'（migration 未生效）")
else:
    code, name, resource, action = rows[0]
    if code == "identity:review" and resource == "identity" and action == "review":
        ok("1.1 permissions 行存在且字段正确",
           f"code={code}, name={name}, resource={resource}, action={action}")
    else:
        fail("1.1 permissions 行字段不符合预期",
             f"code={code}, name={name}, resource={resource}, action={action}")


# ═══════════════════════════════════════════════════════════════
# 2. DB 确认：admin 角色已绑定 identity:review
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}2. DB 确认：admin 角色已绑定 identity:review 权限{RESET}")

rows, err = db_query(
    "SELECT r.code FROM roles r "
    "JOIN role_permissions rp ON rp.role_id = r.id "
    "JOIN permissions p ON p.id = rp.permission_id "
    "WHERE p.code = 'identity:review' AND r.code = 'admin'"
)
if err:
    fail("2.1 查询 role_permissions 失败", err)
elif not rows:
    fail("2.1 admin 角色未绑定 identity:review（role_permissions 缺失）")
else:
    ok("2.1 admin 角色已绑定 identity:review", f"role_code={rows[0][0]}")


# ═══════════════════════════════════════════════════════════════
# 前置准备：创建测试账号
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：创建测试账号{RESET}")

rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = int(rows[0][0]) if rows else None
if not admin_role_id:
    db_exec("INSERT IGNORE INTO roles (code, name) VALUES ('admin', '管理员')")
    rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
    admin_role_id = int(rows[0][0]) if rows else None

print(f"\n  创建管理员账号 {ADMIN_EMAIL}...")
admin_user_id, _ = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"pr47adm{TS}")
if not admin_user_id:
    print(f"  {RED}管理员注册失败，中止{RESET}")
    raise SystemExit(1)
if admin_role_id:
    db_exec(f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
            f"SELECT {admin_role_id}, p.id FROM permissions p")
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({admin_user_id}, {admin_role_id})")
db_exec(f"UPDATE users SET admin_phone_verified_at=NOW(), admin_email_verified_at=NOW() WHERE id={admin_user_id}")
print(f"  管理员注册成功: id={admin_user_id}")

print(f"\n  创建普通用户 {NORMAL_EMAIL}...")
normal_user_id, _ = register_user_via_api(NORMAL_EMAIL, NORMAL_PHONE, NORMAL_PASSWORD, f"pr47norm{TS}")
if normal_user_id:
    print(f"  普通用户注册成功: id={normal_user_id}")
else:
    print(f"  {YELLOW}普通用户注册失败，权限边界测试将跳过{RESET}")


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
# 3. 核心修复验证：管理员可访问 GET /api/admin/identity-verifications
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}3. 核心修复验证：管理员访问 GET /api/admin/identity-verifications{RESET}")

if admin_token:
    status, resp = http("GET", "/api/admin/identity-verifications?page=1&page_size=5", token=admin_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        items = data.get("items")
        if isinstance(items, list) and "pagination" not in data and {"page", "page_size", "total"} <= set(data.keys()):
            ok("3.1 管理员访问返回 200，分页结构已扁平化（D-95）",
               f"items_count={len(items)}, total={data.get('total')}")
        else:
            fail("3.1 响应结构不符合预期", f"data keys={sorted(data.keys())}")
    elif status == 403:
        fail("3.1 管理员访问返回 403（权限种子仍未生效）",
             f"code={resp.get('code')}, message={resp.get('message')}")
    else:
        fail("3.1 管理员访问返回非预期状态码",
             f"HTTP={status}, code={resp.get('code')}")
else:
    fail("3.1 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 4. 权限边界：普通用户调用 /api/admin/identity-verifications → 403
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}4. 权限边界：普通用户访问 /api/admin/identity-verifications{RESET}")

status, resp = http("POST", "/api/auth/login/email",
                    {"email": NORMAL_EMAIL, "password": NORMAL_PASSWORD})
print(f"  普通用户登录  HTTP {status}: code={resp.get('code')}")
normal_token = resp.get("data", {}).get("access_token") if status == 200 else None

if normal_token:
    status, resp = http("GET", "/api/admin/identity-verifications", token=normal_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 403:
        ok("4.1 普通用户调用 /api/admin/identity-verifications 返回 403",
           f"code={resp.get('code')}")
    else:
        fail("4.1 普通用户调用应返回 403", f"实际 HTTP={status}")
else:
    fail("4.1 跳过（普通用户登录失败）", json.dumps(resp, ensure_ascii=False))


# ═══════════════════════════════════════════════════════════════
# 5. 端到端流程 — 测试用户 A（approve 路径）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}5. 端到端流程（approve）：{USER_A_EMAIL}{RESET}")

user_a_id = None
user_a_token = None
verif_a_id = None

print(f"\n  {BOLD}5.0 注册测试用户 A{RESET}")
user_a_id, user_a_token = register_user_via_api(
    USER_A_EMAIL, USER_A_PHONE, USER_A_PASSWORD, f"pr47ua{TS}"
)
if user_a_id:
    ok("5.0 测试用户 A 注册成功", f"user_id={user_a_id}")
else:
    fail("5.0 测试用户 A 注册失败")

print(f"\n  {BOLD}5.2 用户 A 提交实名认证（POST /api/identity/verifications）{RESET}")
if user_a_token:
    status, resp = http("POST", "/api/identity/verifications",
                        {"real_name": "张三测试", "id_card_no": "110101199001011234",
                         "verification_type": "id_card"},
                        token=user_a_token)
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status in (200, 201) and resp.get("code") == 0:
        data = resp.get("data", {})
        verif_a_id = data.get("id")
        status_val = data.get("status")
        if verif_a_id and status_val == "pending":
            ok("5.2 用户 A 提交成功，初始 status=pending",
               f"HTTP={status}, verification_id={verif_a_id}")
        else:
            fail("5.2 提交响应字段不符合预期", f"id={verif_a_id}, status={status_val}")
    else:
        fail("5.2 用户 A 提交失败",
             f"HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
        rows, err = db_query(
            f"SELECT id FROM identity_verifications WHERE user_id={user_a_id} ORDER BY id DESC LIMIT 1"
        )
        if rows and not err:
            verif_a_id = int(rows[0][0])
else:
    fail("5.2 跳过（用户 A 注册失败）")

print(f"\n  {BOLD}5.3 管理员查列表 + DB 确认记录{RESET}")
if admin_token and user_a_id:
    status, resp = http("GET", "/api/admin/identity-verifications?page=1&page_size=10", token=admin_token)
    print(f"    列表接口  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        ok("5.3.1 管理员列表接口正常（HTTP 200 code=0）",
           f"items_count={len(data.get('items', []))}, total={data.get('total')}")
    else:
        fail("5.3.1 管理员列表接口返回非预期值", f"HTTP={status}")

    if verif_a_id:
        rows, err = db_query(
            f"SELECT user_id, status FROM identity_verifications WHERE id={verif_a_id}"
        )
        if rows and not err:
            db_uid, db_st = int(rows[0][0]), rows[0][1]
            if db_uid == user_a_id and db_st == "pending":
                ok("5.3.2 DB 确认认证记录已写入", f"verif_id={verif_a_id}, status={db_st}")
            else:
                fail("5.3.2 DB 记录不符合预期", f"db_uid={db_uid}, db_st={db_st}")
        else:
            fail("5.3.2 DB 查询失败", f"err={err}")
    else:
        fail("5.3.2 跳过（verif_a_id 不可用）")
else:
    fail("5.3 跳过（前置条件未满足）")

print(f"\n  {BOLD}5.4 管理员查单条详情{RESET}")
DETAIL_EXPECTED_FIELDS = {"id", "user_id", "real_name", "id_card_no_masked", "status", "submitted_at"}
if admin_token and verif_a_id:
    status, resp = http("GET", f"/api/admin/identity-verifications/{verif_a_id}", token=admin_token)
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        detail = resp.get("data", {})
        missing = DETAIL_EXPECTED_FIELDS - set(detail.keys())
        if not missing:
            ok("5.4.1 详情包含全部预期字段", f"fields={sorted(detail.keys())}")
        else:
            fail("5.4.1 详情缺少预期字段", f"缺少={missing}")

        masked = detail.get("id_card_no_masked", "")
        if len(masked) == 18 and masked[6:14] == "********":
            ok("5.4.2 id_card_no_masked 脱敏格式正确（前6后4中间8位*）", f"masked={masked}")
        else:
            fail("5.4.2 id_card_no_masked 格式异常", f"masked={masked!r}")

        if detail.get("status") == "pending":
            ok("5.4.3 详情 status=pending")
        else:
            fail("5.4.3 status 不符合预期", f"实际={detail.get('status')}")
    else:
        fail("5.4 查详情失败", f"HTTP={status}")
else:
    fail("5.4 跳过（verif_a_id 未获取到）")

# 5.5 审核通过（D-89：action=approve）
print(f"\n  {BOLD}5.5 管理员审核通过 action=approve（D-89 新格式）{RESET}")
if admin_token and verif_a_id:
    status, resp = http(
        "PATCH",
        f"/api/admin/identity-verifications/{verif_a_id}/review",
        {"action": "approve"},
        token=admin_token
    )
    print(f"    调用  HTTP {status}: code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0:
        ok("5.5.1 审核通过接口返回 200 code=0")
    else:
        fail("5.5.1 审核通过接口返回非预期值",
             f"HTTP={status}, code={resp.get('code')}")

    time.sleep(0.3)
    rows, err = db_query(f"SELECT status FROM identity_verifications WHERE id={verif_a_id}")
    db_status = rows[0][0] if rows and not err else None
    if db_status == "verified":
        ok("5.5.2 DB 认证状态已更新为 verified")
    else:
        fail("5.5.2 DB 认证状态未更新为 verified", f"实际={db_status}")

    rows, err = db_query(f"SELECT real_name_status FROM users WHERE id={user_a_id}")
    user_rn_status = rows[0][0] if rows and not err else None
    if user_rn_status == "verified":
        ok("5.5.3 users.real_name_status 已同步为 verified")
    else:
        fail("5.5.3 users.real_name_status 未同步", f"实际={user_rn_status}")
else:
    fail("5.5 跳过（前置条件未满足）")


# ═══════════════════════════════════════════════════════════════
# 6. 端到端流程 — 测试用户 B（reject 路径）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}6. 端到端流程（reject）：{USER_B_EMAIL}{RESET}")

user_b_id = None
user_b_token = None
verif_b_id = None

print(f"\n  {BOLD}6.0 注册测试用户 B{RESET}")
user_b_id, user_b_token = register_user_via_api(
    USER_B_EMAIL, USER_B_PHONE, USER_B_PASSWORD, f"pr47ub{TS}"
)
if user_b_id:
    ok("6.0 测试用户 B 注册成功", f"user_id={user_b_id}")
else:
    fail("6.0 测试用户 B 注册失败")

print(f"\n  {BOLD}6.2 用户 B 提交实名认证{RESET}")
if user_b_token:
    status, resp = http("POST", "/api/identity/verifications",
                        {"real_name": "李四测试", "id_card_no": "110101199002025678",
                         "verification_type": "id_card"},
                        token=user_b_token)
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status in (200, 201) and resp.get("code") == 0:
        verif_b_id = resp.get("data", {}).get("id")
        ok("6.2 用户 B 提交成功", f"HTTP={status}, verification_id={verif_b_id}")
    else:
        fail("6.2 用户 B 提交失败",
             f"HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
        rows, err = db_query(
            f"SELECT id FROM identity_verifications WHERE user_id={user_b_id} ORDER BY id DESC LIMIT 1"
        )
        if rows and not err:
            verif_b_id = int(rows[0][0])
else:
    fail("6.2 跳过（用户 B 注册失败）")

# 6.3 审核拒绝（D-89：action=reject, reject_reason=...）
print(f"\n  {BOLD}6.3 管理员审核拒绝 action=reject（D-89 新格式）{RESET}")
if admin_token and verif_b_id:
    status, resp = http(
        "PATCH",
        f"/api/admin/identity-verifications/{verif_b_id}/review",
        {"action": "reject", "reject_reason": "PR47验收-拒绝原因：资料不清晰"},
        token=admin_token
    )
    print(f"    调用  HTTP {status}: code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0:
        ok("6.3.1 审核拒绝接口返回 200 code=0")
    else:
        fail("6.3.1 审核拒绝接口返回非预期值", f"HTTP={status}, code={resp.get('code')}")

    time.sleep(0.3)
    rows, err = db_query(
        f"SELECT status, reject_reason FROM identity_verifications WHERE id={verif_b_id}"
    )
    if rows and not err:
        db_status, db_reason = rows[0]
        if db_status == "rejected":
            ok("6.3.2 DB 认证状态已更新为 rejected")
        else:
            fail("6.3.2 DB 认证状态未更新为 rejected", f"实际={db_status}")
        if db_reason and "资料不清晰" in db_reason:
            ok("6.3.3 reject_reason 已正确保存", f"reason={db_reason}")
        else:
            fail("6.3.3 reject_reason 未正确保存", f"reason={db_reason!r}")
    else:
        fail("6.3.2/6.3.3 查询 DB 失败", f"err={err}")
else:
    fail("6.3 跳过（前置条件未满足）")


# ═══════════════════════════════════════════════════════════════
# 7. 回归检查：GET /api/identity/verifications/latest（D-90 路由更新）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}7. 回归检查：GET /api/identity/verifications/latest（用户 A 已 verified）{RESET}")

if user_a_token:
    status, resp = http("GET", "/api/identity/verifications/latest", token=user_a_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        me_status = data.get("status")
        ok("7.1 GET /api/identity/verifications/latest 返回 200", f"status={me_status}")
        if me_status == "verified":
            ok("7.2 用户 A 认证状态为 verified（与审核结果一致）")
        else:
            fail("7.2 用户 A 认证状态不符合预期（应为 verified）", f"实际={me_status}")
    elif status == 404:
        fail("7.1 返回 404（不符合预期，用户 A 已提交认证）")
    else:
        fail("7.1 返回非预期状态码",
             f"HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)[:200]}")
else:
    fail("7.x 跳过（用户 A token 不可用）")


# ═══════════════════════════════════════════════════════════════
# 8. 无 token 访问管理员接口 → 401
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}8. 无 token 访问管理员接口应返回 401{RESET}")

status, resp = http("GET", "/api/admin/identity-verifications")
print(f"  调用  HTTP {status}: code={resp.get('code')}")
if status == 401:
    ok("8.1 无 token 访问 /api/admin/identity-verifications 返回 401")
else:
    fail("8.1 无 token 访问应返回 401", f"实际 HTTP={status}")


# ═══════════════════════════════════════════════════════════════
# 清理
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除临时测试数据{RESET}")

for uid in [user_a_id, user_b_id]:
    if uid:
        db_exec(f"DELETE FROM identity_verification_logs WHERE user_id={uid}")
        db_exec(f"DELETE FROM identity_verifications WHERE user_id={uid}")

for uid in [admin_user_id, normal_user_id, user_a_id, user_b_id]:
    if uid:
        db_exec(f"DELETE FROM user_roles WHERE user_id={uid}")
        db_exec(f"DELETE FROM user_sessions WHERE user_id={uid}")
        db_exec(f"DELETE FROM users WHERE id={uid}")

print(f"  已清理所有测试账号")


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
    print(f"{BOLD}{GREEN}结论：PR#47 全部测试用例通过，identity:review 权限种子修复验收通过。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#47 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

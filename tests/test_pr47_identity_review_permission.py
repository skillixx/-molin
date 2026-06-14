#!/usr/bin/env python3
"""
PR#47（feature/backend-a-seed-identity-review-permission, A-15）
identity:review 权限种子修复 — 接口验收脚本

验收范围：
  1. DB 确认：permissions 表包含 code='identity:review' 且字段正确
  2. DB 确认：admin 角色已绑定 identity:review 权限（role_permissions）
  3. 管理员可访问列表接口（核心修复验证）：
     GET /api/admin/identity-verifications 应返回 200，之前返回 403/40003
  4. 权限边界：普通用户调用同接口应返回 403
  5. 端到端流程：
     a. 动态创建测试普通用户，提交实名认证（POST /api/identity/verifications）
     b. 管理员通过 GET /api/admin/identity-verifications 确认接口正常，DB 确认记录存在
     c. 管理员调用 GET /api/admin/identity-verifications/{id} 查看详情，字段符合约定
     d. 管理员调用 PATCH /api/admin/identity-verifications/{id}/review（approve=true）
        确认返回正常，认证状态变为 verified
     e. 再用另一条记录测试 approve=false（reject）流程，确认 status=rejected
  6. 回归检查：GET /api/identity/verifications/me（普通用户查自己状态）仍正常
  7. 清理：删除本脚本创建的测试用户和认证记录

注：POST /api/identity/verifications 实现返回 HTTP 201 Created（code=0），
    已在脚本中兼容处理（接受 200 或 201）。

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr47_identity_review_permission.py
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


# ── MySQL 工具 ────────────────────────────────────────────────

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
    """执行非查询 SQL（INSERT/UPDATE/DELETE），返回 (success, err)。"""
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


# ── 测试账号配置 ────────────────────────────────────────────────

TS = int(time.time())
# 管理员账号（已存在，双重认证状态通过 DB 直接刷新以确保有效）
ADMIN_EMAIL    = "aisiqin@163.com"
ADMIN_PASSWORD = "123456"
ADMIN_ID       = 90  # DB 中已知的 admin user_id

# 动态创建的测试普通用户（两个：一个用于 approve，一个用于 reject）
USER_A_EMAIL    = f"pr47_approve_{TS}@testmail.io"
USER_A_PHONE    = f"138{TS % 100000000:08d}"

USER_B_EMAIL    = f"pr47_reject_{TS}@testmail.io"
USER_B_PHONE    = f"139{TS % 100000000:08d}"

print(f"\n{BOLD}{CYAN}PR#47 identity:review 权限种子修复 — 接口验收{RESET}")
print(f"  API_BASE    : {API_BASE}")
print(f"  MYSQL       : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  管理员账号  : {ADMIN_EMAIL}")
print(f"  测试用户A   : {USER_A_EMAIL} (approve 流程)")
print(f"  测试用户B   : {USER_B_EMAIL} (reject 流程)")
print()


# ═══════════════════════════════════════════════════════════════
# 1. DB 确认：permissions 表包含 identity:review
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}1. DB 确认：permissions 表包含 identity:review 行{RESET}")

rows, err = db_query("SELECT code, name, resource, action FROM permissions WHERE code='identity:review'")
if err:
    fail("1.1 查询 permissions 表失败", err)
elif not rows:
    fail("1.1 permissions 表中不存在 code='identity:review' 的行（migration 未生效）")
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
# 准备：刷新管理员双重认证时间戳（确保测试期间始终有效）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}准备：刷新管理员双重认证时间戳{RESET}")
ok_upd, err = db_exec(
    f"UPDATE users SET admin_phone_verified_at=NOW(), admin_email_verified_at=NOW() "
    f"WHERE id={ADMIN_ID}"
)
if not ok_upd:
    print(f"  {YELLOW}警告：刷新双重认证时间戳失败（{err}），依赖现有状态继续{RESET}")
else:
    print(f"  已将 admin_phone_verified_at / admin_email_verified_at 更新为 NOW()")


# ═══════════════════════════════════════════════════════════════
# 准备：管理员登录
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}准备：管理员登录{RESET}")
status, resp = http("POST", "/api/auth/login/email", {
    "email": ADMIN_EMAIL, "password": ADMIN_PASSWORD
})
print(f"       响应 HTTP {status}: code={resp.get('code')}")
if status != 200 or resp.get("code") != 0:
    fail("管理员登录失败，后续测试无法进行", json.dumps(resp, ensure_ascii=False))
    admin_token = None
else:
    admin_token = resp["data"]["access_token"]
    ok("管理员登录成功")


# ═══════════════════════════════════════════════════════════════
# 3. 核心修复验证：管理员可访问 GET /api/admin/identity-verifications
#    此前因权限种子缺失而返回 403/40003，修复后应返回 200
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}3. 核心修复验证：管理员访问 GET /api/admin/identity-verifications{RESET}")

if admin_token:
    status, resp = http("GET", "/api/admin/identity-verifications?page=1&page_size=5", token=admin_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        items = data.get("items")
        # D-95：分页结构已扁平化，page/page_size/total 与 items 同级，data 中不应再有 pagination 子对象
        if isinstance(items, list) and "pagination" not in data and {"page", "page_size", "total"} <= set(data.keys()):
            ok("3.1 管理员访问 GET /api/admin/identity-verifications 返回 200（修复验证通过，分页结构已扁平化）",
               f"items_count={len(items)}, page={data.get('page')}, page_size={data.get('page_size')}, total={data.get('total')}")
        else:
            fail("3.1 响应结构不符合预期（items 应为数组，page/page_size/total 应与 items 同级，data 中不应有 pagination）",
                 f"resp={json.dumps(resp, ensure_ascii=False)[:500]}")
    elif status == 403:
        fail("3.1 管理员访问返回 403（权限种子仍未生效）",
             f"code={resp.get('code')}, message={resp.get('message')}")
    else:
        fail("3.1 管理员访问返回非预期状态码",
             f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
else:
    fail("3.1 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 4. 权限边界：普通用户调用 /api/admin/identity-verifications 应返回 403
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}4. 权限边界：普通用户访问 /api/admin/identity-verifications{RESET}")

# 使用已知的普通用户账号（无任何角色）
NORMAL_EMAIL    = "pr31norm81328894@testmail.io"
NORMAL_PASSWORD = "Test@Pr31x"

status, resp = http("POST", "/api/auth/login/email", {
    "email": NORMAL_EMAIL, "password": NORMAL_PASSWORD
})
print(f"  普通用户登录  HTTP {status}: code={resp.get('code')}")
normal_token = resp.get("data", {}).get("access_token") if status == 200 else None

if normal_token:
    status, resp = http("GET", "/api/admin/identity-verifications", token=normal_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 403:
        ok("4.1 普通用户调用 /api/admin/identity-verifications 返回 403",
           f"code={resp.get('code')}, message={resp.get('message')}")
    else:
        fail("4.1 普通用户调用应返回 403",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("4.1 跳过（普通用户登录失败，无法验证权限边界）",
         json.dumps(resp, ensure_ascii=False))


# ═══════════════════════════════════════════════════════════════
# 5. 端到端流程 — 测试用户 A（approve 路径）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}5. 端到端流程（approve）：{USER_A_EMAIL}{RESET}")

user_a_token = None
user_a_id = None
verif_a_id = None  # 认证记录 ID

# 5.0 注册测试用户 A（通过 DB 直接插入，避免依赖 OTP 短信/邮件）
print(f"\n  {BOLD}5.0 注册测试用户 A（DB 直接插入）{RESET}")
# 复用已知账号的 bcrypt hash（pr31norm 账号密码为 "Test@Pr31x"）
rows, err = db_query("SELECT password_hash FROM users WHERE email='pr31norm81328894@testmail.io'")
known_hash = rows[0][0] if rows and not err else None

if known_hash:
    ok_ins, err = db_exec(
        f"INSERT INTO users (email, email_verified, phone, phone_verified, password_hash, "
        f"real_name_status, status, created_at, updated_at) VALUES "
        f"('{USER_A_EMAIL}', 1, '{USER_A_PHONE}', 1, '{known_hash}', "
        f"'unverified', 'active', NOW(), NOW())"
    )
    if not ok_ins:
        fail("5.0 插入测试用户 A 失败", err)
    else:
        rows, err = db_query(f"SELECT id FROM users WHERE email='{USER_A_EMAIL}'")
        if rows and not err:
            user_a_id = int(rows[0][0])
            ok("5.0 测试用户 A 注册成功（DB 插入）", f"user_id={user_a_id}")
        else:
            fail("5.0 未能查到刚创建的测试用户 A", f"err={err}")
else:
    fail("5.0 无法获取已知 bcrypt hash，跳过用户 A 创建", f"err={err}")

# 5.1 测试用户 A 登录
print(f"\n  {BOLD}5.1 测试用户 A 登录{RESET}")
if user_a_id:
    # 登录密码与 pr31norm 相同（"Test@Pr31x"），因为复用了同一个 hash
    status, resp = http("POST", "/api/auth/login/email", {
        "email": USER_A_EMAIL, "password": "Test@Pr31x"
    })
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        user_a_token = resp["data"]["access_token"]
        ok("5.1 测试用户 A 登录成功")
    else:
        fail("5.1 测试用户 A 登录失败", json.dumps(resp, ensure_ascii=False))

# 5.2 用户 A 提交实名认证
# 注：POST /api/identity/verifications 实现返回 HTTP 201 Created（code=0），非 200
print(f"\n  {BOLD}5.2 用户 A 提交实名认证（POST /api/identity/verifications）{RESET}")
if user_a_token:
    status, resp = http("POST", "/api/identity/verifications",
                        {
                            "real_name": "张三测试",
                            "id_card_no": "110101199001011234",
                            "verification_type": "id_card"
                        },
                        token=user_a_token)
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    # 接口实现返回 201 Created，兼容 200 和 201
    if status in (200, 201) and resp.get("code") == 0:
        data = resp.get("data", {})
        verif_a_id = data.get("id")
        status_val = data.get("status")
        if verif_a_id and status_val == "pending":
            ok("5.2 用户 A 提交实名认证成功，初始 status=pending",
               f"HTTP={status}, verification_id={verif_a_id}, status={status_val}")
        else:
            fail("5.2 提交实名认证响应字段不符合预期",
                 f"id={verif_a_id}, status={status_val}, resp={resp}")
    else:
        fail("5.2 用户 A 提交实名认证失败",
             f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
        # 尝试从 DB 查 verification ID
        rows, err = db_query(
            f"SELECT id FROM identity_verifications WHERE user_id={user_a_id} ORDER BY id DESC LIMIT 1"
        )
        if rows and not err:
            verif_a_id = int(rows[0][0])
            print(f"    从 DB 获取 verif_a_id={verif_a_id}")
else:
    fail("5.2 跳过（用户 A 登录失败）")

# 5.3 管理员查列表（验证接口工作正常）+ DB 确认认证记录存在
# 注：GET /api/admin/identity-verifications 的 user_id 过滤参数在当前实现中可能未支持，
#     改为调用不带过滤的列表接口验证正常，再通过 DB 直接确认记录
print(f"\n  {BOLD}5.3 管理员查列表（验证接口正常）+ DB 确认用户 A 的认证记录{RESET}")
if admin_token and user_a_id:
    # 5.3.1 调用列表接口，验证返回 200 且结构正常
    status, resp = http(
        "GET",
        "/api/admin/identity-verifications?page=1&page_size=10",
        token=admin_token
    )
    print(f"    列表接口  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        items = data.get("items", [])
        ok("5.3.1 管理员列表接口正常（HTTP 200 code=0）",
           f"items_count={len(items)}, total={data.get('total')}")
    else:
        fail("5.3.1 管理员列表接口返回非预期值",
             f"HTTP={status}, code={resp.get('code')}")

    # 5.3.2 通过 DB 确认认证记录已写入且属于 user_a_id
    if verif_a_id:
        rows, err = db_query(
            f"SELECT user_id, status FROM identity_verifications WHERE id={verif_a_id}"
        )
        if rows and not err:
            db_uid = int(rows[0][0])
            db_st = rows[0][1]
            if db_uid == user_a_id and db_st == "pending":
                ok("5.3.2 DB 确认用户 A 的认证记录已写入（user_id 和 status 正确）",
                   f"verif_id={verif_a_id}, user_id={db_uid}, status={db_st}")
            else:
                fail("5.3.2 DB 中认证记录不符合预期",
                     f"verif_id={verif_a_id}, db_user_id={db_uid}, db_status={db_st}")
        else:
            fail("5.3.2 DB 查询 identity_verifications 失败", f"err={err}")
    else:
        fail("5.3.2 跳过（verif_a_id 不可用）")
else:
    fail("5.3 跳过（前置条件未满足）")

# 5.4 管理员查单条详情
print(f"\n  {BOLD}5.4 管理员查单条详情 GET /api/admin/identity-verifications/{{id}}{RESET}")
DETAIL_EXPECTED_FIELDS = {"id", "user_id", "real_name", "id_card_no_masked", "status", "submitted_at"}
if admin_token and verif_a_id:
    status, resp = http(
        "GET",
        f"/api/admin/identity-verifications/{verif_a_id}",
        token=admin_token
    )
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        detail = resp.get("data", {})
        actual_fields = set(detail.keys())
        missing = DETAIL_EXPECTED_FIELDS - actual_fields
        if not missing:
            ok("5.4.1 详情响应包含全部预期字段",
               f"fields={sorted(actual_fields)}")
        else:
            fail("5.4.1 详情响应缺少预期字段",
                 f"缺少={missing}, 实际={sorted(actual_fields)}")

        # 验证 id_card_no_masked 格式（应脱敏，前6后4中间8位*）
        masked = detail.get("id_card_no_masked", "")
        if len(masked) == 18 and masked[6:14] == "********":
            ok("5.4.2 id_card_no_masked 脱敏格式正确（前6后4中间8位*）",
               f"masked={masked}")
        else:
            fail("5.4.2 id_card_no_masked 格式异常",
                 f"masked={masked!r}")

        # status 应为 pending
        if detail.get("status") == "pending":
            ok("5.4.3 详情 status=pending（尚未审核）")
        else:
            fail("5.4.3 详情 status 不符合预期（应为 pending）",
                 f"实际 status={detail.get('status')}")
    else:
        fail("5.4 管理员查详情失败",
             f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
else:
    fail("5.4 跳过（前置条件未满足：verif_a_id 未获取到）")

# 5.5 管理员审核通过（approve=true）
print(f"\n  {BOLD}5.5 管理员审核通过 PATCH /api/admin/identity-verifications/{{id}}/review (approve=true){RESET}")
if admin_token and verif_a_id:
    status, resp = http(
        "PATCH",
        f"/api/admin/identity-verifications/{verif_a_id}/review",
        {"approve": True, "reason": "PR47验收-审核通过"},
        token=admin_token
    )
    print(f"    调用  HTTP {status}: code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0:
        ok("5.5.1 审核通过接口返回 200 code=0")
    else:
        fail("5.5.1 审核通过接口返回非预期值",
             f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")

    # 验证 DB 中认证状态已变为 verified
    time.sleep(0.3)
    rows, err = db_query(f"SELECT status FROM identity_verifications WHERE id={verif_a_id}")
    db_status = rows[0][0] if rows and not err else None
    if db_status == "verified":
        ok("5.5.2 DB 中认证状态已更新为 verified", f"verif_id={verif_a_id}")
    else:
        fail("5.5.2 DB 中认证状态未更新为 verified",
             f"实际 status={db_status}, err={err}")

    # 验证用户 real_name_status 已同步为 verified
    rows, err = db_query(f"SELECT real_name_status FROM users WHERE id={user_a_id}")
    user_status = rows[0][0] if rows and not err else None
    if user_status == "verified":
        ok("5.5.3 users.real_name_status 已同步为 verified", f"user_id={user_a_id}")
    else:
        fail("5.5.3 users.real_name_status 未同步为 verified",
             f"实际={user_status}, err={err}")
else:
    fail("5.5 跳过（前置条件未满足）")


# ═══════════════════════════════════════════════════════════════
# 6. 端到端流程 — 测试用户 B（reject 路径）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}6. 端到端流程（reject）：{USER_B_EMAIL}{RESET}")

user_b_token = None
user_b_id = None
verif_b_id = None

# 6.0 注册测试用户 B
print(f"\n  {BOLD}6.0 注册测试用户 B（DB 直接插入）{RESET}")
if known_hash:
    ok_ins, err = db_exec(
        f"INSERT INTO users (email, email_verified, phone, phone_verified, password_hash, "
        f"real_name_status, status, created_at, updated_at) VALUES "
        f"('{USER_B_EMAIL}', 1, '{USER_B_PHONE}', 1, '{known_hash}', "
        f"'unverified', 'active', NOW(), NOW())"
    )
    if not ok_ins:
        fail("6.0 插入测试用户 B 失败", err)
    else:
        rows, err = db_query(f"SELECT id FROM users WHERE email='{USER_B_EMAIL}'")
        if rows and not err:
            user_b_id = int(rows[0][0])
            ok("6.0 测试用户 B 注册成功（DB 插入）", f"user_id={user_b_id}")
        else:
            fail("6.0 未能查到刚创建的测试用户 B", f"err={err}")
else:
    fail("6.0 跳过（known_hash 不可用）")

# 6.1 用户 B 登录
print(f"\n  {BOLD}6.1 测试用户 B 登录{RESET}")
if user_b_id:
    status, resp = http("POST", "/api/auth/login/email", {
        "email": USER_B_EMAIL, "password": "Test@Pr31x"
    })
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        user_b_token = resp["data"]["access_token"]
        ok("6.1 测试用户 B 登录成功")
    else:
        fail("6.1 测试用户 B 登录失败", json.dumps(resp, ensure_ascii=False))

# 6.2 用户 B 提交实名认证（使用不同身份证号以避免 HMAC 冲突）
# 注：POST /api/identity/verifications 实现返回 HTTP 201 Created（code=0），非 200
print(f"\n  {BOLD}6.2 用户 B 提交实名认证{RESET}")
if user_b_token:
    status, resp = http("POST", "/api/identity/verifications",
                        {
                            "real_name": "李四测试",
                            "id_card_no": "110101199002025678",
                            "verification_type": "id_card"
                        },
                        token=user_b_token)
    print(f"    调用  HTTP {status}: code={resp.get('code')}")
    # 接口实现返回 201 Created，兼容 200 和 201
    if status in (200, 201) and resp.get("code") == 0:
        verif_b_id = resp.get("data", {}).get("id")
        ok("6.2 用户 B 提交实名认证成功", f"HTTP={status}, verification_id={verif_b_id}")
    else:
        fail("6.2 用户 B 提交实名认证失败",
             f"HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")
        rows, err = db_query(
            f"SELECT id FROM identity_verifications WHERE user_id={user_b_id} ORDER BY id DESC LIMIT 1"
        )
        if rows and not err:
            verif_b_id = int(rows[0][0])
else:
    fail("6.2 跳过（用户 B 登录失败）")

# 6.3 管理员审核拒绝（approve=false）
print(f"\n  {BOLD}6.3 管理员审核拒绝 PATCH /api/admin/identity-verifications/{{id}}/review (approve=false){RESET}")
if admin_token and verif_b_id:
    status, resp = http(
        "PATCH",
        f"/api/admin/identity-verifications/{verif_b_id}/review",
        {"approve": False, "reason": "PR47验收-拒绝原因：资料不清晰"},
        token=admin_token
    )
    print(f"    调用  HTTP {status}: code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0:
        ok("6.3.1 审核拒绝接口返回 200 code=0")
    else:
        fail("6.3.1 审核拒绝接口返回非预期值",
             f"HTTP={status}, code={resp.get('code')}")

    # 验证 DB 中认证状态已变为 rejected
    time.sleep(0.3)
    rows, err = db_query(
        f"SELECT status, reject_reason FROM identity_verifications WHERE id={verif_b_id}"
    )
    if rows and not err:
        db_status, db_reason = rows[0]
        if db_status == "rejected":
            ok("6.3.2 DB 中认证状态已更新为 rejected", f"verif_id={verif_b_id}")
        else:
            fail("6.3.2 DB 中认证状态未更新为 rejected",
                 f"实际 status={db_status}")
        if db_reason and "资料不清晰" in db_reason:
            ok("6.3.3 reject_reason 已正确保存", f"reason={db_reason}")
        else:
            fail("6.3.3 reject_reason 未正确保存", f"reason={db_reason!r}")
    else:
        fail("6.3.2/6.3.3 查询 DB 失败", f"err={err}")
else:
    fail("6.3 跳过（前置条件未满足）")


# ═══════════════════════════════════════════════════════════════
# 7. 回归检查：GET /api/identity/verifications/me（普通用户查自己认证状态）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}7. 回归检查：GET /api/identity/verifications/me（用户 A 已 verified）{RESET}")

if user_a_token:
    status, resp = http("GET", "/api/identity/verifications/me", token=user_a_token)
    print(f"  调用  HTTP {status}: code={resp.get('code')}")
    if status == 200 and resp.get("code") == 0:
        data = resp.get("data", {})
        me_status = data.get("status")
        ok("7.1 GET /api/identity/verifications/me 返回 200",
           f"status={me_status}, data={data}")
        # 审核通过后应为 verified
        if me_status == "verified":
            ok("7.2 用户 A 的认证状态为 verified（与审核结果一致）")
        else:
            fail("7.2 用户 A 的认证状态不符合预期（应为 verified）",
                 f"实际 status={me_status}")
    elif status == 404:
        fail("7.1 GET /api/identity/verifications/me 返回 404（不符合预期，用户 A 已提交认证）",
             f"resp={json.dumps(resp, ensure_ascii=False)[:200]}")
    else:
        fail("7.1 GET /api/identity/verifications/me 返回非预期状态码",
             f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:200]}")
else:
    fail("7.x 跳过（用户 A token 不可用）")


# ═══════════════════════════════════════════════════════════════
# 8. 无 token 访问 /api/admin/identity-verifications 应返回 401
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}8. 无 token 访问管理员接口应返回 401{RESET}")

status, resp = http("GET", "/api/admin/identity-verifications")
print(f"  调用  HTTP {status}: code={resp.get('code')}")
if status == 401:
    ok("8.1 无 token 访问 /api/admin/identity-verifications 返回 401",
       f"code={resp.get('code')}")
else:
    fail("8.1 无 token 访问应返回 401",
         f"实际 HTTP={status}, code={resp.get('code')}")


# ═══════════════════════════════════════════════════════════════
# 清理：删除本脚本临时创建的测试用户和认证记录
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除临时测试数据{RESET}")

# 清理认证记录（先删子表 identity_verification_logs，再删主表）
for uid in [user_a_id, user_b_id]:
    if uid:
        ok_del, err = db_exec(
            f"DELETE FROM identity_verification_logs WHERE user_id={uid}"
        )
        ok_del2, err2 = db_exec(
            f"DELETE FROM identity_verifications WHERE user_id={uid}"
        )
        if ok_del and ok_del2:
            print(f"  已清理 user_id={uid} 的认证记录")
        else:
            print(f"  {YELLOW}清理认证记录时出现问题: logs_err={err}, verifs_err={err2}{RESET}")

# 清理用户
for email in [USER_A_EMAIL, USER_B_EMAIL]:
    ok_del, err = db_exec(f"DELETE FROM users WHERE email='{email}'")
    if ok_del:
        print(f"  已清理用户 {email}")
    else:
        print(f"  {YELLOW}清理用户时出现问题: {err}{RESET}")


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
    print(f"{BOLD}{GREEN}结论：PR#47 全部测试用例通过，identity:review 权限种子修复验收通过，建议可以合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#47 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

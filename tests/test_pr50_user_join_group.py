#!/usr/bin/env python3
"""
PR#50（A-16）接口验收脚本 — POST /api/user-groups/join 用户凭邀请码加入群组

验收范围（12 个用例）：
  T01  正常加入群组：有效邀请码 + 已登录普通用户 → 200，data.group_id 和 data.group_role 正确
  T02  验证 used_count 递增：加入后 GET invite-codes，found code.used_count = 1
  T03  重复加入同一群组：同一用户再次 POST join → 409/40900
  T04  码无效：使用不存在的邀请码 → 400/40000
  T05  已禁用的码：PATCH disable 后再尝试加入 → 400/40000
  T06  max_uses 超限：创建 max_uses=1 的码，用完后换另一用户加入 → 400/40000
  T07  未登录：不带 Authorization 头 → 401
  T08  default_group_role=admin 的码：加入后 group_role 应为 "admin"
  T09  已过期的码：expires_at 设为过去时间（SQL 修改）→ 400/40000
  T10  缺少 invite_code 字段（空 body）→ 400/40000
  T11  invite_code 为空字符串 → 400/40000
  T12  管理员创建邀请码时 code 重复 → 409/40900

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr50_user_join_group.py
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

passed  = 0
failed  = 0
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


# ── HTTP 工具 ─────────────────────────────────────────────────────────────────

def http(method, path, body=None, token=None):
    """发起 HTTP 请求，返回 (http_status, response_body_dict)。"""
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


# ── MySQL 工具 ────────────────────────────────────────────────────────────────

def db_query(sql):
    """执行 SELECT，返回 (rows, err)，rows 为 List[List[str]]。"""
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
    """执行非查询 SQL，返回 (success, err)。"""
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


# ── 测试账号配置 ───────────────────────────────────────────────────────────────

TS = int(time.time())

# 管理员账号（DB 中已知）
ADMIN_EMAIL    = "aisiqin@example.com"
ADMIN_PASSWORD = "123456"
ADMIN_ID       = 90

# 测试专用群组（使用 DB 中已有的 id=1 群组，code=qa_group_a）
TEST_GROUP_ID   = 1
TEST_GROUP_CODE = "qa_group_a"

# 动态创建的两个普通测试用户
USER_A_EMAIL = f"pr50_usera_{TS}@testmail.io"
USER_A_PHONE = f"131{TS % 100000000:08d}"

USER_B_EMAIL = f"pr50_userb_{TS}@testmail.io"
USER_B_PHONE = f"132{TS % 100000000:08d}"

print(f"\n{BOLD}{CYAN}PR#50 A-16 POST /api/user-groups/join — 接口验收{RESET}")
print(f"  API_BASE    : {API_BASE}")
print(f"  MYSQL       : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  管理员账号  : {ADMIN_EMAIL}")
print(f"  测试用户A   : {USER_A_EMAIL}")
print(f"  测试用户B   : {USER_B_EMAIL}")
print(f"  测试群组    : id={TEST_GROUP_ID} code={TEST_GROUP_CODE}")
print()

# ═══════════════════════════════════════════════════════════════════════════════
# 前置：刷新管理员双重认证时间戳
# ═══════════════════════════════════════════════════════════════════════════════
print(f"{BOLD}前置：刷新管理员双重认证时间戳{RESET}")
ok_upd, err = db_exec(
    f"UPDATE users SET admin_phone_verified_at=NOW(), admin_email_verified_at=NOW() "
    f"WHERE id={ADMIN_ID}"
)
if not ok_upd:
    print(f"  {YELLOW}警告：刷新失败（{err}），依赖现有状态继续{RESET}")
else:
    print(f"  已更新 admin_phone_verified_at / admin_email_verified_at = NOW()")

# ═══════════════════════════════════════════════════════════════════════════════
# 前置：管理员登录
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置：管理员登录{RESET}")
status, resp = http("POST", "/api/auth/login/email", {
    "email": ADMIN_EMAIL, "password": ADMIN_PASSWORD
})
print(f"  HTTP {status}: code={resp.get('code')}")
if status != 200 or resp.get("code") != 0:
    print(f"  {RED}管理员登录失败，无法继续测试{RESET}")
    raise SystemExit(1)
admin_token = resp["data"]["access_token"]
print(f"  管理员登录成功")

# ═══════════════════════════════════════════════════════════════════════════════
# 前置：从 DB 获取已知 bcrypt hash，用于快速插入测试用户
# ═══════════════════════════════════════════════════════════════════════════════
rows, err = db_query("SELECT password_hash FROM users WHERE email='pr31norm81328894@testmail.io'")
known_hash = rows[0][0] if rows and not err else None
if not known_hash:
    print(f"  {RED}无法获取 bcrypt hash，无法创建测试用户{RESET}")
    raise SystemExit(1)
# known_hash 对应密码 "Test@Pr31x"
KNOWN_PASSWORD = "Test@Pr31x"

# ═══════════════════════════════════════════════════════════════════════════════
# 前置：创建测试用户 A 和 B
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置：DB 直接插入测试用户 A 和 B{RESET}")

user_a_id = None
user_b_id = None

for email, phone, var_name in [
    (USER_A_EMAIL, USER_A_PHONE, "A"),
    (USER_B_EMAIL, USER_B_PHONE, "B"),
]:
    ok_ins, err = db_exec(
        f"INSERT INTO users (email, email_verified, phone, phone_verified, password_hash, "
        f"real_name_status, status, created_at, updated_at) VALUES "
        f"('{email}', 1, '{phone}', 1, '{known_hash}', "
        f"'unverified', 'active', NOW(), NOW())"
    )
    if not ok_ins:
        print(f"  {RED}插入用户 {var_name} 失败: {err}{RESET}")
        continue
    rows, err = db_query(f"SELECT id FROM users WHERE email='{email}'")
    if rows and not err:
        uid = int(rows[0][0])
        if var_name == "A":
            user_a_id = uid
        else:
            user_b_id = uid
        print(f"  用户 {var_name} 已创建: id={uid}, email={email}")
    else:
        print(f"  {RED}查不到刚创建的用户 {var_name}: {err}{RESET}")

if not user_a_id or not user_b_id:
    print(f"  {RED}测试用户创建失败，中止测试{RESET}")
    raise SystemExit(1)

# 登录用户 A
status, resp = http("POST", "/api/auth/login/email", {
    "email": USER_A_EMAIL, "password": KNOWN_PASSWORD
})
if status != 200 or resp.get("code") != 0:
    print(f"  {RED}用户 A 登录失败{RESET}")
    raise SystemExit(1)
user_a_token = resp["data"]["access_token"]
print(f"  用户 A 登录成功")

# 登录用户 B
status, resp = http("POST", "/api/auth/login/email", {
    "email": USER_B_EMAIL, "password": KNOWN_PASSWORD
})
if status != 200 or resp.get("code") != 0:
    print(f"  {RED}用户 B 登录失败{RESET}")
    raise SystemExit(1)
user_b_token = resp["data"]["access_token"]
print(f"  用户 B 登录成功")

# ═══════════════════════════════════════════════════════════════════════════════
# 前置：为测试群组创建主测试邀请码（max_uses=0 即不限次数）
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置：创建主测试邀请码（group_id={TEST_GROUP_ID}，max_uses=0）{RESET}")
MAIN_CODE = f"PR50TEST{TS % 10000:04d}"
status, resp = http(
    "POST",
    f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes",
    {"code": MAIN_CODE, "default_group_role": "member", "max_uses": 0},
    token=admin_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status not in (200, 201) or resp.get("code") != 0:
    print(f"  {RED}创建主测试邀请码失败: {json.dumps(resp, ensure_ascii=False)}{RESET}")
    raise SystemExit(1)
main_invite_data = resp.get("data", {})
main_invite_id = main_invite_data.get("id")
print(f"  创建成功: code={MAIN_CODE}, invite_id={main_invite_id}")

# ═══════════════════════════════════════════════════════════════════════════════
# T01  正常加入群组：有效邀请码 + 已登录普通用户 → 200，data.group_id 正确，group_role="member"
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T01  正常加入群组（有效码 + 登录用户）{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {"invite_code": MAIN_CODE},
    token=user_a_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 200 and resp.get("code") == 0:
    data = resp.get("data", {})
    gid  = data.get("group_id")
    role = data.get("group_role")
    if gid == TEST_GROUP_ID and role == "member":
        ok("T01  正常加入群组 → 200，group_id 正确，group_role=member",
           f"group_id={gid}, group_role={role}")
    else:
        fail("T01  响应字段不符合预期",
             f"期望 group_id={TEST_GROUP_ID} group_role=member，实际 group_id={gid} group_role={role}")
else:
    fail("T01  正常加入群组返回非预期 HTTP 状态码",
         f"HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:300]}")

# ═══════════════════════════════════════════════════════════════════════════════
# T02  验证 used_count 递增：GET invite-codes，found code.used_count = 1
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T02  验证 used_count 递增（加入后应 = 1）{RESET}")
status, resp = http(
    "GET",
    f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes?page=1&page_size=50",
    token=admin_token
)
print(f"  GET invite-codes HTTP {status}: code={resp.get('code')}")
if status == 200 and resp.get("code") == 0:
    invite_list = resp.get("data", {}).get("list", [])
    target = next((ic for ic in invite_list if ic.get("id") == main_invite_id), None)
    if target is None:
        # 尝试直接查 DB
        rows, err = db_query(f"SELECT used_count FROM group_invite_codes WHERE id={main_invite_id}")
        db_count = int(rows[0][0]) if rows and not err else None
        if db_count == 1:
            ok("T02  DB 确认 used_count=1（API 响应中列表未找到该码，但 DB 正确）",
               f"invite_id={main_invite_id}, used_count={db_count}")
        else:
            fail("T02  invite-codes 列表找不到目标码，DB 中 used_count 也不符合预期",
                 f"db_count={db_count}, err={err}")
    else:
        used = target.get("used_count")
        if used == 1:
            ok("T02  used_count 已递增为 1",
               f"invite_id={main_invite_id}, used_count={used}")
        else:
            fail("T02  used_count 未正确递增",
                 f"期望 1，实际 used_count={used}")
else:
    fail("T02  GET invite-codes 返回非预期值",
         f"HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T03  重复加入同一群组：同一用户 A 再次 POST join → 期望 409/40900
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T03  重复加入同一群组 → 期望 409/40900{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {"invite_code": MAIN_CODE},
    token=user_a_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 409 and resp.get("code") == 40900:
    ok("T03  重复加入同一群组 → 409/40900",
       f"message={resp.get('message')}")
else:
    fail("T03  重复加入应返回 409/40900",
         f"实际 HTTP={status}, code={resp.get('code')}, resp={json.dumps(resp, ensure_ascii=False)[:200]}")

# ═══════════════════════════════════════════════════════════════════════════════
# T04  码无效：使用不存在的邀请码 → 期望 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T04  不存在的邀请码 → 期望 400/40000{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {"invite_code": "NOTEXIST9999"},
    token=user_b_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 400 and resp.get("code") == 40000:
    ok("T04  不存在的邀请码 → 400/40000",
       f"message={resp.get('message')}")
else:
    fail("T04  不存在的邀请码应返回 400/40000",
         f"实际 HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T05  已禁用的码：先创建码，PATCH disable，再尝试加入 → 期望 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T05  已禁用的码 → 期望 400/40000{RESET}")

# 创建一个专用于此测试的邀请码
DISABLE_CODE = f"PR50DIS{TS % 10000:04d}"
status, resp = http(
    "POST",
    f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes",
    {"code": DISABLE_CODE, "default_group_role": "member", "max_uses": 0},
    token=admin_token
)
print(f"  创建禁用测试码 HTTP {status}: code={resp.get('code')}")
if status in (200, 201) and resp.get("code") == 0:
    disable_invite_id = resp["data"]["id"]
    # PATCH disable
    d_status, d_resp = http(
        "PATCH",
        f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes/{disable_invite_id}/disable",
        token=admin_token
    )
    print(f"  PATCH disable HTTP {d_status}: code={d_resp.get('code')}")
    if d_status == 200 and d_resp.get("code") == 0:
        # 用用户 B 尝试加入
        j_status, j_resp = http(
            "POST", "/api/user-groups/join",
            {"invite_code": DISABLE_CODE},
            token=user_b_token
        )
        print(f"  使用已禁用码加入 HTTP {j_status}: code={j_resp.get('code')}")
        if j_status == 400 and j_resp.get("code") == 40000:
            ok("T05  已禁用的码 → 400/40000",
               f"message={j_resp.get('message')}")
        else:
            fail("T05  已禁用的码应返回 400/40000",
                 f"实际 HTTP={j_status}, code={j_resp.get('code')}")
    else:
        fail("T05  PATCH disable 失败，无法完成测试",
             f"HTTP={d_status}, code={d_resp.get('code')}")
else:
    fail("T05  创建禁用测试码失败，跳过",
         f"HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T06  max_uses 超限：创建 max_uses=1 的码，用户 B 用一次后，再换另一用户加入 → 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T06  max_uses=1 超限后 → 期望 400/40000{RESET}")

# 创建 max_uses=1 的邀请码
MAXUSE_CODE = f"PR50MX1{TS % 10000:04d}"
# 先创建一个新群组供此测试（避免用户 B 因已是 TEST_GROUP_ID 成员而返回 409 干扰结果）
NEW_GROUP_CODE = f"pr50testg{TS % 10000:04d}"
g_status, g_resp = http(
    "POST",
    "/api/admin/user-groups",
    {"code": NEW_GROUP_CODE, "name": f"PR50临时测试组{TS}", "type": "custom"},
    token=admin_token
)
print(f"  创建临时测试群组 HTTP {g_status}: code={g_resp.get('code')}")
new_group_id = None
if g_status in (200, 201) and g_resp.get("code") == 0:
    new_group_id = g_resp["data"]["id"]
    print(f"  临时群组 id={new_group_id} 创建成功")

    # 创建 max_uses=1 的邀请码（在新群组内）
    ic_status, ic_resp = http(
        "POST",
        f"/api/admin/user-groups/{new_group_id}/invite-codes",
        {"code": MAXUSE_CODE, "default_group_role": "member", "max_uses": 1},
        token=admin_token
    )
    print(f"  创建 max_uses=1 邀请码 HTTP {ic_status}: code={ic_resp.get('code')}")
    if ic_status in (200, 201) and ic_resp.get("code") == 0:
        # 用户 A 加入（first use，应成功）
        # 注意：用户 A 不是该新群组的成员，可以正常加入
        j1_status, j1_resp = http(
            "POST", "/api/user-groups/join",
            {"invite_code": MAXUSE_CODE},
            token=user_a_token
        )
        print(f"  第1次加入（用户A） HTTP {j1_status}: code={j1_resp.get('code')}")
        if j1_status == 200 and j1_resp.get("code") == 0:
            print(f"  第1次加入成功（used_count 应变为 1 = max_uses）")
            # 用户 B 再次加入（second use，超限，应返回 400/40000）
            j2_status, j2_resp = http(
                "POST", "/api/user-groups/join",
                {"invite_code": MAXUSE_CODE},
                token=user_b_token
            )
            print(f"  第2次加入（用户B，超限） HTTP {j2_status}: code={j2_resp.get('code')}")
            if j2_status == 400 and j2_resp.get("code") == 40000:
                ok("T06  max_uses=1 超限 → 400/40000",
                   f"message={j2_resp.get('message')}")
            else:
                fail("T06  超过 max_uses 后应返回 400/40000",
                     f"实际 HTTP={j2_status}, code={j2_resp.get('code')}")
        else:
            fail("T06  第1次加入失败，无法完成超限测试",
                 f"HTTP={j1_status}, code={j1_resp.get('code')}, resp={json.dumps(j1_resp, ensure_ascii=False)[:200]}")
    else:
        fail("T06  创建 max_uses=1 邀请码失败，跳过",
             f"HTTP={ic_status}, code={ic_resp.get('code')}")
else:
    fail("T06  创建临时测试群组失败，跳过",
         f"HTTP={g_status}, code={g_resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T07  未登录：不带 Authorization 头 → 期望 401
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T07  未登录（无 Authorization 头）→ 期望 401{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {"invite_code": MAIN_CODE}
    # 不传 token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 401:
    ok("T07  未登录访问 → 401",
       f"code={resp.get('code')}, message={resp.get('message')}")
else:
    fail("T07  未登录应返回 401",
         f"实际 HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T08  default_group_role=admin 的码：创建时指定 admin，加入后 group_role 应为 "admin"
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T08  default_group_role=admin 的码 → 加入后 group_role=admin{RESET}")

# 在新群组（若已创建）或 TEST_GROUP_ID 中创建 admin 码
admin_role_group_id = new_group_id if new_group_id else TEST_GROUP_ID
ADMIN_ROLE_CODE = f"PR50ADM{TS % 10000:04d}"
ic_status, ic_resp = http(
    "POST",
    f"/api/admin/user-groups/{admin_role_group_id}/invite-codes",
    {"code": ADMIN_ROLE_CODE, "default_group_role": "admin", "max_uses": 0},
    token=admin_token
)
print(f"  创建 admin 角色邀请码 HTTP {ic_status}: code={ic_resp.get('code')}")
if ic_status in (200, 201) and ic_resp.get("code") == 0:
    # 用户 B 用 admin 码加入（用户 B 尚未加入此群组）
    j_status, j_resp = http(
        "POST", "/api/user-groups/join",
        {"invite_code": ADMIN_ROLE_CODE},
        token=user_b_token
    )
    print(f"  加入 HTTP {j_status}: code={j_resp.get('code')}")
    if j_status == 200 and j_resp.get("code") == 0:
        role = j_resp.get("data", {}).get("group_role")
        gid  = j_resp.get("data", {}).get("group_id")
        if role == "admin" and gid == admin_role_group_id:
            ok("T08  default_group_role=admin 码加入 → group_role=admin",
               f"group_id={gid}, group_role={role}")
        else:
            fail("T08  加入后 group_role 不符合预期",
                 f"期望 admin，实际 role={role}, gid={gid}")
    else:
        fail("T08  使用 admin 角色码加入失败",
             f"HTTP={j_status}, code={j_resp.get('code')}, resp={json.dumps(j_resp, ensure_ascii=False)[:200]}")
else:
    fail("T08  创建 admin 角色邀请码失败，跳过",
         f"HTTP={ic_status}, code={ic_resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T09  已过期的码：通过 SQL 直接修改 expires_at 为过去时间 → 期望 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T09  已过期的码 → 期望 400/40000{RESET}")

# 创建一个普通邀请码
EXPIRED_CODE = f"PR50EXP{TS % 10000:04d}"
ic_status, ic_resp = http(
    "POST",
    f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes",
    {"code": EXPIRED_CODE, "default_group_role": "member", "max_uses": 0},
    token=admin_token
)
print(f"  创建过期测试码 HTTP {ic_status}: code={ic_resp.get('code')}")
if ic_status in (200, 201) and ic_resp.get("code") == 0:
    expired_invite_id = ic_resp["data"]["id"]
    # 通过 SQL 将 expires_at 改为过去时间
    ok_upd, upd_err = db_exec(
        f"UPDATE group_invite_codes SET expires_at='2020-01-01 00:00:00' "
        f"WHERE id={expired_invite_id}"
    )
    if ok_upd:
        print(f"  已将 invite_id={expired_invite_id} 的 expires_at 设为 2020-01-01")
        # 用用户 B 尝试加入（用户 B 已在 TEST_GROUP_ID 中，会触发 409 而不是 400）
        # 因此需要检查：如果用户 B 已在该组，先移除
        db_exec(f"DELETE FROM user_group_members WHERE user_id={user_b_id} AND group_id={TEST_GROUP_ID}")
        j_status, j_resp = http(
            "POST", "/api/user-groups/join",
            {"invite_code": EXPIRED_CODE},
            token=user_b_token
        )
        print(f"  使用过期码加入 HTTP {j_status}: code={j_resp.get('code')}")
        if j_status == 400 and j_resp.get("code") == 40000:
            ok("T09  已过期的码 → 400/40000",
               f"message={j_resp.get('message')}")
        else:
            fail("T09  已过期的码应返回 400/40000",
                 f"实际 HTTP={j_status}, code={j_resp.get('code')}")
    else:
        fail("T09  无法修改 expires_at，跳过", f"err={upd_err}")
else:
    fail("T09  创建过期测试码失败，跳过",
         f"HTTP={ic_status}, code={ic_resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T10  缺少 invite_code 字段（空 body {}）→ 期望 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T10  缺少 invite_code 字段（空 body）→ 期望 400/40000{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {},
    token=user_b_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 400 and resp.get("code") == 40000:
    ok("T10  缺少 invite_code → 400/40000",
       f"message={resp.get('message')}")
else:
    fail("T10  缺少 invite_code 应返回 400/40000",
         f"实际 HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T11  invite_code 为空字符串 → 期望 400/40000
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T11  invite_code 为空字符串 → 期望 400/40000{RESET}")
status, resp = http(
    "POST", "/api/user-groups/join",
    {"invite_code": ""},
    token=user_b_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 400 and resp.get("code") == 40000:
    ok("T11  invite_code 为空字符串 → 400/40000",
       f"message={resp.get('message')}")
else:
    fail("T11  invite_code 为空字符串应返回 400/40000",
         f"实际 HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# T12  code 重复创建邀请码 → 期望 409/40900
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}T12  管理员创建重复 code 的邀请码 → 期望 409/40900{RESET}")
# MAIN_CODE 在前置步骤已经创建过，再次创建同一 code 应返回 409
status, resp = http(
    "POST",
    f"/api/admin/user-groups/{TEST_GROUP_ID}/invite-codes",
    {"code": MAIN_CODE, "default_group_role": "member", "max_uses": 0},
    token=admin_token
)
print(f"  HTTP {status}: code={resp.get('code')}")
if status == 409 and resp.get("code") == 40900:
    ok("T12  重复 code 创建邀请码 → 409/40900",
       f"message={resp.get('message')}")
else:
    fail("T12  重复 code 应返回 409/40900",
         f"实际 HTTP={status}, code={resp.get('code')}")

# ═══════════════════════════════════════════════════════════════════════════════
# 清理：删除测试过程中创建的数据
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除测试数据{RESET}")

# 删除临时测试群组的成员记录（邀请码由群组级联或手动删除）
if new_group_id:
    db_exec(f"DELETE FROM user_group_members WHERE group_id={new_group_id}")
    db_exec(f"DELETE FROM group_invite_codes WHERE group_id={new_group_id}")
    db_exec(f"DELETE FROM user_groups WHERE id={new_group_id}")
    print(f"  已删除临时测试群组 id={new_group_id} 及其关联数据")

# 删除测试用户 A/B 在 TEST_GROUP_ID 中的成员记录
for uid in [user_a_id, user_b_id]:
    if uid:
        db_exec(f"DELETE FROM user_group_members WHERE user_id={uid}")

# 删除本次测试创建的邀请码（通过 code 字段精确删除，避免影响其他数据）
for code_val in [MAIN_CODE, DISABLE_CODE, EXPIRED_CODE]:
    db_exec(f"DELETE FROM group_invite_codes WHERE code='{code_val}'")

# 删除测试用户
for email in [USER_A_EMAIL, USER_B_EMAIL]:
    ok_del, err = db_exec(f"DELETE FROM users WHERE email='{email}'")
    if ok_del:
        print(f"  已删除用户 {email}")
    else:
        print(f"  {YELLOW}删除用户 {email} 时出现问题: {err}{RESET}")

# ═══════════════════════════════════════════════════════════════════════════════
# 汇总
# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{CYAN}═══ PR#50 A-16 测试结果汇总 ═══{RESET}")
total = passed + failed
print(f"  总计: {total}  {GREEN}通过: {passed}{RESET}  {RED}失败: {failed}{RESET}")
print()
for status_label, label, detail in results:
    color = GREEN if status_label == "PASS" else RED
    line = f"  {color}[{status_label}]{RESET} {label}"
    print(line)

print()
if failed == 0:
    print(f"{BOLD}{GREEN}结论：PR#50 A-16 全部 {total} 个测试用例通过，建议可以合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#50 A-16 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

#!/usr/bin/env python3
"""
PR#69（A-24）接口验收脚本 — iam 角色权限模块第四轮缺陷修复验收 D-57~D-66

验收范围：
  D-57  CreateRole 增加 code/name 必填校验；code 重复返回 409（不是 500）
  D-58  UpdateRole 拒绝 name 为空字符串覆盖 → 400，原 name 不被清空
  D-59  ReplaceUserRoles 校验 roleID 存在性；无效 role_id → 400，不写入孤儿记录
  D-60  CreateRole/UpdateRole 新增字段长度校验（code/name<=128, description<=512）
  D-61  DeleteRole 引用检查：(a) 角色分配给用户后删除 → 409；
        (b) 角色仅设置权限（无用户分配）后删除 → 200，role_permissions 被清理
  D-62  AddGroupPermission 校验 permission_code 存在性 → 不存在返回 400
  D-63  ReplaceUserOverrides/SetRolePermissions/ReplaceUserRoles 批量接口数量上限 200
  D-64  migration 000019 补全 8 个权限码并绑定 admin 角色（DB 验证）
  D-65  UpdateRole 接受 code 字段时拒绝更新 → 400，角色 code 不变
  D-66  invalidateGroupMembersCache batchSize=500 分批循环（代码审查确认，SKIP）

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/test_pr69_a24_iam_role_round4.py
"""

import json
import os
import subprocess
import time
import hashlib
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
    msg = f"  {GREEN}[PASS]{RESET} {label}"
    if detail:
        msg += f"\n         {YELLOW}{detail}{RESET}"
    print(msg)
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}[FAIL]{RESET} {label}"
    if detail:
        msg += f"\n         {RED}{detail}{RESET}"
    print(msg)
    results.append(("FAIL", label, detail))


def skip(label, reason=""):
    msg = f"  {YELLOW}[SKIP]{RESET} {label}"
    if reason:
        msg += f"\n         {YELLOW}{reason}{RESET}"
    print(msg)
    results.append(("SKIP", label, reason))


# ── HTTP 工具 ──────────────────────────────────────────────────────────────────

def http(method, path, body=None, token=None, extra_headers=None):
    """发起 HTTP 请求，返回 (http_status, response_body_dict)。"""
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if extra_headers:
        headers.update(extra_headers)
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL 工具 ─────────────────────────────────────────────────────────────────

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


# ── 注册工具：DB 插入验证码 + API 注册 ────────────────────────────────────────

def register_user_via_api(email, phone, password, username=None):
    """
    通过 DB 插入验证码后，调用 POST /api/auth/register 注册账号。
    返回 (user_id, access_token) 或 (None, None)。
    """
    otp_code     = "888888"
    otp_code_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql   = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"

    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")

    ok_p, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone', '{phone}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_p:
        print(f"    {RED}插入手机验证码失败: {err}{RESET}")
        return None, None

    ok_e, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email', '{email}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_e:
        print(f"    {RED}插入邮箱验证码失败: {err}{RESET}")
        return None, None

    body = {
        "email":       email,
        "phone":       phone,
        "password":    password,
        "phone_code":  otp_code,
        "email_code":  otp_code,
    }
    if username:
        body["username"] = username

    status, resp = http("POST", "/api/auth/register", body)
    if status in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
        return user_id, token
    else:
        print(f"    {RED}注册接口返回非预期: HTTP={status}, {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
        return None, None


def login_email(email, password):
    """邮箱密码登录，返回 (access_token, refresh_token) 或 (None, None)。"""
    s, r = http("POST", "/api/auth/login/email", {"email": email, "password": password})
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        return data.get("access_token"), data.get("refresh_token")
    return None, None


# ── 初始化时间戳，用于区分本次测试数据 ──────────────────────────────────────

TS = int(time.time())

# 主管理员账号
ADMIN_EMAIL    = f"pr69adm{TS}@testmail.io"
ADMIN_PHONE    = f"170{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr69Admin"

# D-59 测试用户（用于 ReplaceUserRoles 无效 role_id 测试）
D59_EMAIL    = f"pr69d59{TS}@testmail.io"
D59_PHONE    = f"171{TS % 100000000:08d}"
D59_PASSWORD = "Test@D59User123"

# D-61(a) 测试用户（角色将分配给该用户）
D61_EMAIL    = f"pr69d61{TS}@testmail.io"
D61_PHONE    = f"172{TS % 100000000:08d}"
D61_PASSWORD = "Test@D61User123"

print(f"\n{BOLD}{CYAN}PR#69（A-24）iam 角色权限模块第四轮缺陷修复 D-57~D-66 — 接口验收{RESET}")
print(f"  API_BASE : {API_BASE}")
print(f"  MYSQL    : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  时间戳   : {TS}")
print()


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 1：确保 admin 角色和相关权限存在
# ════════════════════════════════════════════════════════════════════════════════

print(f"{BOLD}前置准备 Step 1：确保 admin 角色与权限{RESET}")

rows, err = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = None
if rows and not err:
    admin_role_id = int(rows[0][0])
    print(f"  admin 角色已存在: id={admin_role_id}")
else:
    db_exec("INSERT IGNORE INTO roles (code, name, description) VALUES ('admin', '管理员', '系统管理员角色')")
    rows, err = db_query("SELECT id FROM roles WHERE code='admin'")
    if rows and not err:
        admin_role_id = int(rows[0][0])
        print(f"  已创建 admin 角色: id={admin_role_id}")
    else:
        print(f"  {RED}admin 角色创建失败，中止{RESET}")
        raise SystemExit(1)

# 绑定所有现有权限到 admin 角色
if admin_role_id:
    db_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
        f"SELECT {admin_role_id}, p.id FROM permissions p"
    )
    print(f"  已确保 admin 角色绑定所有现有权限")


def setup_admin_user(user_id):
    """给用户绑定 admin 角色并设置双重认证"""
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {admin_role_id})")
    db_exec(
        f"UPDATE users SET admin_email_verified_at=NOW(), admin_phone_verified_at=NOW() "
        f"WHERE id={user_id}"
    )


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 2：注册测试账号
# ════════════════════════════════════════════════════════════════════════════════

print(f"\n{BOLD}前置准备 Step 2：注册测试账号{RESET}")

print(f"\n  注册主管理员 {ADMIN_EMAIL}...")
admin_user_id, _ = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, "adm69")
if admin_user_id:
    setup_admin_user(admin_user_id)
    print(f"  管理员注册成功: id={admin_user_id}")
else:
    print(f"  {RED}管理员注册失败，中止{RESET}")
    raise SystemExit(1)

print(f"\n  重新登录管理员...")
admin_token, admin_refresh = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
if not admin_token:
    print(f"  {RED}管理员登录失败，中止{RESET}")
    raise SystemExit(1)
print(f"  管理员登录成功")

print(f"\n  注册 D-59 测试账号 {D59_EMAIL}...")
d59_user_id, _ = register_user_via_api(D59_EMAIL, D59_PHONE, D59_PASSWORD, "d59pr69")
if d59_user_id:
    print(f"  D-59 用户注册成功: id={d59_user_id}")
else:
    print(f"  {RED}D-59 用户注册失败{RESET}")
    d59_user_id = None

print(f"\n  注册 D-61 测试账号 {D61_EMAIL}...")
d61_user_id, _ = register_user_via_api(D61_EMAIL, D61_PHONE, D61_PASSWORD, "d61pr69")
if d61_user_id:
    print(f"  D-61 用户注册成功: id={d61_user_id}")
else:
    print(f"  {RED}D-61 用户注册失败{RESET}")
    d61_user_id = None

print(f"\n  所有测试账号准备完毕")

# 用于记录本次测试中创建的角色 ID，方便最终清理
created_role_ids = []
d57_role_id = None  # 由 D-57 创建，供 D-58/D-60/D-63/D-65 复用


# ════════════════════════════════════════════════════════════════════════════════
# D-57：CreateRole 增加 code/name 必填校验；code 重复返回 409（不是 500）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-57  CreateRole code/name 必填校验；code 重复 → 409{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    print(f"\n  D-57.1 不传 code（应返回 400）...")
    s1, r1 = http("POST", "/api/admin/roles", {"name": f"d57testname{TS}"}, token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-57.a  不传 code → HTTP 400（必填校验生效）", f"code={r1.get('code')}")
    else:
        fail("D-57.a  不传 code 应返回 400", f"HTTP={s1}, code={r1.get('code')}")

    print(f"\n  D-57.2 不传 name（应返回 400）...")
    s2, r2 = http("POST", "/api/admin/roles", {"code": f"d57code{TS}"}, token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")
    if s2 == 400:
        ok("D-57.b  不传 name → HTTP 400（必填校验生效）", f"code={r2.get('code')}")
    else:
        fail("D-57.b  不传 name 应返回 400", f"HTTP={s2}, code={r2.get('code')}")

    print(f"\n  D-57.3 正常创建角色...")
    role_code = f"d57role{TS}"
    s3, r3 = http("POST", "/api/admin/roles",
                  {"code": role_code, "name": f"D57测试角色{TS}", "description": "D57正常创建"},
                  token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}")
    d57_role_id = None
    if s3 in (200, 201) and r3.get("code") == 0:
        d57_role_id = r3.get("data", {}).get("id")
        if d57_role_id:
            d57_role_id = int(d57_role_id)
            created_role_ids.append(d57_role_id)
        ok("D-57.c  正常创建角色 → 200/201", f"role_id={d57_role_id}")
    else:
        fail("D-57.c  正常创建角色失败", f"HTTP={s3}, code={r3.get('code')}")

    print(f"\n  D-57.4 使用相同 code 再次创建（应返回 409，不是 500）...")
    s4, r4 = http("POST", "/api/admin/roles",
                  {"code": role_code, "name": f"D57重复角色{TS}", "description": "D57重复创建"},
                  token=admin_token)
    print(f"    HTTP {s4}: code={r4.get('code')}, message={r4.get('message', '')!r}")
    if s4 == 409:
        ok("D-57.d  code 重复 → HTTP 409（D-57 修复生效，不再 500）", f"code={r4.get('code')}")
    elif s4 == 500:
        fail("D-57.d  code 重复仍返回 HTTP 500（D-57 修复未生效）", f"HTTP={s4}, code={r4.get('code')}")
    else:
        fail("D-57.d  code 重复返回非预期状态", f"HTTP={s4}, code={r4.get('code')}")
else:
    skip("D-57", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-58：UpdateRole 拒绝 name 为空字符串覆盖
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-58  UpdateRole name='' → 400，原 name 不被清空{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and d57_role_id:
    # 先查询当前 name
    rows, _ = db_query(f"SELECT name FROM roles WHERE id={d57_role_id}")
    orig_name = rows[0][0] if rows else None
    print(f"  当前 name={orig_name!r}")

    print(f"\n  D-58.1 PATCH/PUT /api/admin/roles/{d57_role_id} 传 {{'name': ''}}（应返回 400）...")
    s1, r1 = http("PUT", f"/api/admin/roles/{d57_role_id}", {"name": ""}, token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")

    if s1 == 400:
        ok("D-58.a  name='' → HTTP 400（D-58 修复生效）", f"code={r1.get('code')}")
    else:
        fail("D-58.a  name='' 应返回 400", f"HTTP={s1}, code={r1.get('code')}")

    # 验证 DB 中 name 未被清空
    rows2, _ = db_query(f"SELECT name FROM roles WHERE id={d57_role_id}")
    new_name = rows2[0][0] if rows2 else None
    print(f"  修改后 name={new_name!r}")
    if new_name == orig_name and new_name not in (None, ""):
        ok("D-58.b  原 name 未被清空", f"name={new_name!r}")
    else:
        fail("D-58.b  原 name 被清空或修改", f"期望={orig_name!r}, 实际={new_name!r}")
else:
    skip("D-58", "admin_token 或 d57_role_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-65：UpdateRole 接受 code 字段时拒绝更新 → 400，角色 code 不变
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-65  UpdateRole 传 code → 400，角色 code 不变{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and d57_role_id:
    rows, _ = db_query(f"SELECT code FROM roles WHERE id={d57_role_id}")
    orig_code = rows[0][0] if rows else None
    print(f"  当前 code={orig_code!r}")

    print(f"\n  D-65.1 PUT /api/admin/roles/{d57_role_id} 传 {{'code': 'new_code'}}（应返回 400）...")
    s1, r1 = http("PUT", f"/api/admin/roles/{d57_role_id}",
                  {"code": f"newcode{TS}", "name": f"D65修改后名称{TS}"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")

    if s1 == 400:
        ok("D-65.a  传 code 字段 → HTTP 400（D-65 修复生效）", f"code={r1.get('code')}")
    else:
        fail("D-65.a  传 code 字段应返回 400", f"HTTP={s1}, code={r1.get('code')}")

    rows2, _ = db_query(f"SELECT code FROM roles WHERE id={d57_role_id}")
    new_code = rows2[0][0] if rows2 else None
    print(f"  修改后 code={new_code!r}")
    if new_code == orig_code:
        ok("D-65.b  角色 code 未被修改", f"code={new_code!r}")
    else:
        fail("D-65.b  角色 code 被修改", f"期望={orig_code!r}, 实际={new_code!r}")
else:
    skip("D-65", "admin_token 或 d57_role_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-60：CreateRole/UpdateRole 字段长度校验
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-60  CreateRole/UpdateRole 长度校验（code/name<=128, description<=512）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    code_129 = "c" * 129
    name_129 = "n" * 129
    desc_513 = "d" * 513

    print(f"\n  D-60.1 CreateRole code=129字符（应返回 400，而非 DB 500）...")
    s1, r1 = http("POST", "/api/admin/roles",
                  {"code": code_129, "name": f"D60TestName{TS}"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-60.a  CreateRole code=129字符 → HTTP 400（D-60 修复生效）", f"code={r1.get('code')}")
    elif s1 == 500:
        fail("D-60.a  CreateRole code=129字符仍返回 HTTP 500（D-60 修复未生效）", f"HTTP={s1}")
    else:
        fail("D-60.a  CreateRole code=129字符返回非预期状态", f"HTTP={s1}, code={r1.get('code')}")

    print(f"\n  D-60.2 CreateRole name=129字符（应返回 400）...")
    s2, r2 = http("POST", "/api/admin/roles",
                  {"code": f"d60code2{TS}", "name": name_129},
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")
    if s2 == 400:
        ok("D-60.b  CreateRole name=129字符 → HTTP 400", f"code={r2.get('code')}")
    elif s2 == 500:
        fail("D-60.b  CreateRole name=129字符仍返回 HTTP 500", f"HTTP={s2}")
    else:
        fail("D-60.b  CreateRole name=129字符返回非预期状态", f"HTTP={s2}, code={r2.get('code')}")

    print(f"\n  D-60.3 CreateRole description=513字符（应返回 400）...")
    s3, r3 = http("POST", "/api/admin/roles",
                  {"code": f"d60code3{TS}", "name": f"D60TestName3{TS}", "description": desc_513},
                  token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}")
    if s3 == 400:
        ok("D-60.c  CreateRole description=513字符 → HTTP 400", f"code={r3.get('code')}")
    elif s3 == 500:
        fail("D-60.c  CreateRole description=513字符仍返回 HTTP 500", f"HTTP={s3}")
    else:
        fail("D-60.c  CreateRole description=513字符返回非预期状态", f"HTTP={s3}, code={r3.get('code')}")

    # UpdateRole 长度校验（使用 D-57 创建的角色）
    if d57_role_id:
        print(f"\n  D-60.4 UpdateRole name=129字符（应返回 400）...")
        s4, r4 = http("PUT", f"/api/admin/roles/{d57_role_id}", {"name": name_129}, token=admin_token)
        print(f"    HTTP {s4}: code={r4.get('code')}, message={r4.get('message', '')!r}")
        if s4 == 400:
            ok("D-60.d  UpdateRole name=129字符 → HTTP 400", f"code={r4.get('code')}")
        elif s4 == 500:
            fail("D-60.d  UpdateRole name=129字符仍返回 HTTP 500", f"HTTP={s4}")
        else:
            fail("D-60.d  UpdateRole name=129字符返回非预期状态", f"HTTP={s4}, code={r4.get('code')}")

        print(f"\n  D-60.5 UpdateRole description=513字符（应返回 400）...")
        s5, r5 = http("PUT", f"/api/admin/roles/{d57_role_id}",
                      {"name": f"D60UpdateOK{TS}", "description": desc_513},
                      token=admin_token)
        print(f"    HTTP {s5}: code={r5.get('code')}, message={r5.get('message', '')!r}")
        if s5 == 400:
            ok("D-60.e  UpdateRole description=513字符 → HTTP 400", f"code={r5.get('code')}")
        elif s5 == 500:
            fail("D-60.e  UpdateRole description=513字符仍返回 HTTP 500", f"HTTP={s5}")
        else:
            fail("D-60.e  UpdateRole description=513字符返回非预期状态", f"HTTP={s5}, code={r5.get('code')}")
    else:
        skip("D-60.d/e", "d57_role_id 不可用")
else:
    skip("D-60", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-59：ReplaceUserRoles 校验 roleID 存在性
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-59  ReplaceUserRoles 无效 role_id → 400，不写入孤儿记录{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and d59_user_id:
    invalid_role_id = 9999999

    # 记录修改前 user_roles 记录数
    rows_before, _ = db_query(f"SELECT COUNT(*) FROM user_roles WHERE user_id={d59_user_id}")
    count_before = int(rows_before[0][0]) if rows_before else 0
    print(f"  修改前 user_roles 记录数: {count_before}")

    print(f"\n  D-59.1 PATCH /api/admin/users/{d59_user_id}/roles 传 role_ids=[{invalid_role_id}]（应返回 400）...")
    s1, r1 = http("PATCH", f"/api/admin/users/{d59_user_id}/roles",
                  {"role_ids": [invalid_role_id]},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")

    rows_after, _ = db_query(
        f"SELECT COUNT(*) FROM user_roles WHERE user_id={d59_user_id} AND role_id={invalid_role_id}"
    )
    orphan_count = int(rows_after[0][0]) if rows_after else 0

    if s1 == 400:
        ok("D-59.a  无效 role_id → HTTP 400（D-59 修复生效）",
           f"code={r1.get('code')}, 孤儿记录数={orphan_count}")
    elif s1 in (200, 201):
        fail("D-59.a  无效 role_id 仍返回成功（D-59 修复未生效）",
             f"HTTP={s1}, 孤儿记录数={orphan_count}")
    else:
        fail("D-59.a  无效 role_id 返回非预期状态", f"HTTP={s1}, code={r1.get('code')}")

    if orphan_count == 0:
        ok("D-59.b  user_roles 中无孤儿记录")
    else:
        fail("D-59.b  user_roles 中存在孤儿记录", f"孤儿记录数={orphan_count}")
        db_exec(f"DELETE FROM user_roles WHERE user_id={d59_user_id} AND role_id={invalid_role_id}")
else:
    skip("D-59", "admin_token 或 d59_user_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-63：批量接口数量上限 200
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-63  批量接口数量上限 200（ReplaceUserOverrides/SetRolePermissions/ReplaceUserRoles）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and d57_role_id:
    print(f"\n  D-63.1 PATCH /api/admin/roles/{d57_role_id}/permissions 传 201 个 permission_id（应返回 400）...")
    perm_ids_201 = list(range(1, 202))
    s1, r1 = http("PATCH", f"/api/admin/roles/{d57_role_id}/permissions",
                  {"permission_ids": perm_ids_201},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-63.a  SetRolePermissions 201条 → HTTP 400（D-63 修复生效）", f"code={r1.get('code')}")
    else:
        fail("D-63.a  SetRolePermissions 201条应返回 400", f"HTTP={s1}, code={r1.get('code')}")
else:
    skip("D-63.a", "admin_token 或 d57_role_id 不可用")

if admin_token and d59_user_id:
    print(f"\n  D-63.2 PATCH /api/admin/users/{d59_user_id}/roles 传 201 个 role_id（应返回 400）...")
    role_ids_201 = list(range(1, 202))
    s2, r2 = http("PATCH", f"/api/admin/users/{d59_user_id}/roles",
                  {"role_ids": role_ids_201},
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")
    if s2 == 400:
        ok("D-63.b  ReplaceUserRoles 201条 → HTTP 400（D-63 修复生效）", f"code={r2.get('code')}")
    else:
        fail("D-63.b  ReplaceUserRoles 201条应返回 400", f"HTTP={s2}, code={r2.get('code')}")

    print(f"\n  D-63.3 PATCH /api/admin/users/{d59_user_id}/permission-overrides 传 201 个 item（应返回 400）...")
    items_201 = [{"permission_id": i, "effect": "allow"} for i in range(1, 202)]
    s3, r3 = http("PATCH", f"/api/admin/users/{d59_user_id}/permission-overrides",
                  {"items": items_201},
                  token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}")
    if s3 == 400:
        ok("D-63.c  ReplaceUserOverrides 201条 → HTTP 400（D-63 修复生效）", f"code={r3.get('code')}")
    else:
        fail("D-63.c  ReplaceUserOverrides 201条应返回 400", f"HTTP={s3}, code={r3.get('code')}")
else:
    skip("D-63.b/c", "admin_token 或 d59_user_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-62：AddGroupPermission 校验 permission_code 存在性
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-62  AddGroupPermission 不存在 permission_code → 400{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

d62_group_id = None
if admin_token:
    print(f"\n  D-62.1 创建测试分组...")
    s_g, r_g = http("POST", "/api/admin/user-groups",
                    {"code": f"d62grp{TS}", "name": f"D62TestGroup{TS}", "type": "custom"},
                    token=admin_token)
    print(f"    创建分组 HTTP {s_g}: code={r_g.get('code')}")
    if s_g in (200, 201) and r_g.get("code") == 0:
        d62_group_id = r_g.get("data", {}).get("id")
        if d62_group_id:
            d62_group_id = int(d62_group_id)
            print(f"    分组 id={d62_group_id}")

    if d62_group_id:
        fake_perm_code = "fake:permission"
        print(f"\n  D-62.2 POST /api/admin/user-groups/{d62_group_id}/permissions 传 permission_code={fake_perm_code!r}（应返回 400）...")
        s1, r1 = http("POST", f"/api/admin/user-groups/{d62_group_id}/permissions",
                      {"permission_code": fake_perm_code},
                      token=admin_token)
        print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")

        # 检查是否静默写入了 group_permissions
        rows_gp, _ = db_query(
            f"SELECT COUNT(*) FROM group_permissions WHERE group_id={d62_group_id} AND permission_code='{fake_perm_code}'"
        )
        gp_count = int(rows_gp[0][0]) if rows_gp else 0

        if s1 == 400:
            ok("D-62.a  不存在 permission_code → HTTP 400（D-62 修复生效）",
               f"code={r1.get('code')}, group_permissions记录数={gp_count}")
        elif s1 in (200, 201):
            fail("D-62.a  不存在 permission_code 仍静默成功（D-62 修复未生效）",
                 f"HTTP={s1}, group_permissions记录数={gp_count}")
            if gp_count > 0:
                db_exec(f"DELETE FROM group_permissions WHERE group_id={d62_group_id} AND permission_code='{fake_perm_code}'")
        else:
            fail("D-62.a  不存在 permission_code 返回非预期状态", f"HTTP={s1}, code={r1.get('code')}")
    else:
        skip("D-62", "创建测试分组失败")
else:
    skip("D-62", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-64：migration 000019 补全 8 个权限码并绑定 admin 角色
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-64  migration 000019 补全 8 个权限码并绑定 admin 角色（DB 验证）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

expected_codes = [
    "asset:view", "asset:manage", "content:manage",
    "membership:view", "membership:manage",
    "product:create", "product:edit", "wallet:view",
]

print(f"\n  D-64.1 查询 permissions 表中 8 个权限码是否存在...")
rows, err = db_query(
    "SELECT code FROM permissions WHERE code IN ("
    + ",".join(f"'{c}'" for c in expected_codes)
    + ")"
)
found_codes = set(r[0] for r in rows) if rows and not err else set()
print(f"    找到 {len(found_codes)}/{len(expected_codes)} 个: {sorted(found_codes)}")

missing_codes = set(expected_codes) - found_codes
if not missing_codes:
    ok("D-64.a  permissions 表中 8 个权限码均存在", f"codes={sorted(found_codes)}")
else:
    fail("D-64.a  permissions 表缺少部分权限码", f"缺失={sorted(missing_codes)}")

print(f"\n  D-64.2 查询 admin 角色是否绑定这 8 个权限...")
rows2, err2 = db_query(
    "SELECT p.code FROM role_permissions rp "
    "JOIN permissions p ON p.id = rp.permission_id "
    "JOIN roles r ON r.id = rp.role_id "
    f"WHERE r.code='admin' AND p.code IN ("
    + ",".join(f"'{c}'" for c in expected_codes)
    + ")"
)
bound_codes = set(r[0] for r in rows2) if rows2 and not err2 else set()
print(f"    admin 角色已绑定 {len(bound_codes)}/{len(expected_codes)} 个: {sorted(bound_codes)}")

unbound_codes = set(expected_codes) - bound_codes
if not unbound_codes:
    ok("D-64.b  admin 角色已绑定这 8 个权限", f"codes={sorted(bound_codes)}")
else:
    fail("D-64.b  admin 角色未绑定部分权限", f"未绑定={sorted(unbound_codes)}")


# ════════════════════════════════════════════════════════════════════════════════
# D-61：DeleteRole 引用检查
#   (a) 创建角色并分配给用户后删除 → 409
#   (b) 创建角色并设置权限（未分配用户）后删除 → 200，role_permissions 被清理
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-61  DeleteRole 引用检查：分配给用户→409；仅设权限未分配→200且清理{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# --- D-61(a): 创建角色，分配给用户，删除应返回 409 ---
d61a_role_id = None
if admin_token and d61_user_id:
    print(f"\n  D-61(a).1 创建专用角色（用于分配给用户）...")
    role_code_a = f"d61arole{TS}"
    sa1, ra1 = http("POST", "/api/admin/roles",
                    {"code": role_code_a, "name": f"D61A测试角色{TS}", "description": "D61a-分配用户后删除"},
                    token=admin_token)
    print(f"    创建角色 HTTP {sa1}: code={ra1.get('code')}")
    if sa1 in (200, 201) and ra1.get("code") == 0:
        d61a_role_id = ra1.get("data", {}).get("id")
        if d61a_role_id:
            d61a_role_id = int(d61a_role_id)
            created_role_ids.append(d61a_role_id)
            print(f"    角色 id={d61a_role_id}")

    if d61a_role_id:
        print(f"\n  D-61(a).2 将角色分配给用户 {d61_user_id}...")
        sa2, ra2 = http("POST", f"/api/admin/users/{d61_user_id}/roles",
                        {"role_id": d61a_role_id},
                        token=admin_token)
        print(f"    分配角色 HTTP {sa2}: code={ra2.get('code')}, message={ra2.get('message', '')!r}")

        if sa2 in (200, 201) and ra2.get("code") == 0:
            print(f"\n  D-61(a).3 删除该角色（应返回 409）...")
            sa3, ra3 = http("DELETE", f"/api/admin/roles/{d61a_role_id}", token=admin_token)
            print(f"    DELETE HTTP {sa3}: code={ra3.get('code')}, message={ra3.get('message', '')!r}")

            if sa3 == 409:
                ok("D-61.a  角色分配给用户后删除 → HTTP 409（D-61 修复生效）",
                   f"code={ra3.get('code')}, message={ra3.get('message', '')!r}")
            elif sa3 == 200:
                fail("D-61.a  角色分配给用户后删除仍返回 200（D-61 修复未生效）", f"HTTP={sa3}")
            else:
                fail("D-61.a  删除返回非预期状态", f"HTTP={sa3}, code={ra3.get('code')}")

            # 验证角色仍存在
            rows_chk, _ = db_query(f"SELECT COUNT(*) FROM roles WHERE id={d61a_role_id}")
            still_exists = int(rows_chk[0][0]) > 0 if rows_chk else False
            if sa3 == 409 and still_exists:
                ok("D-61.a2  角色未被删除（数据完整性保护）")
            elif sa3 == 409 and not still_exists:
                fail("D-61.a2  返回409但角色已被删除（响应与实际不一致）")
        else:
            fail("D-61(a)", f"分配角色失败 HTTP={sa2}, code={ra2.get('code')}")
    else:
        fail("D-61(a)", "创建角色失败")
else:
    skip("D-61(a)", "admin_token 或 d61_user_id 不可用")


# --- D-61(b): 创建角色，设置权限（不分配用户），删除应返回 200 且 role_permissions 被清理 ---
d61b_role_id = None
if admin_token:
    print(f"\n  D-61(b).1 创建专用角色（用于设置权限后删除）...")
    role_code_b = f"d61brole{TS}"
    sb1, rb1 = http("POST", "/api/admin/roles",
                    {"code": role_code_b, "name": f"D61B测试角色{TS}", "description": "D61b-设置权限未分配用户"},
                    token=admin_token)
    print(f"    创建角色 HTTP {sb1}: code={rb1.get('code')}")
    if sb1 in (200, 201) and rb1.get("code") == 0:
        d61b_role_id = rb1.get("data", {}).get("id")
        if d61b_role_id:
            d61b_role_id = int(d61b_role_id)
            print(f"    角色 id={d61b_role_id}")

    if d61b_role_id:
        # 获取一个有效的 permission_id
        rows_p, _ = db_query("SELECT id FROM permissions LIMIT 1")
        test_perm_id = int(rows_p[0][0]) if rows_p else None

        if test_perm_id:
            print(f"\n  D-61(b).2 为角色设置权限 permission_id={test_perm_id}...")
            sb2, rb2 = http("PATCH", f"/api/admin/roles/{d61b_role_id}/permissions",
                            {"permission_ids": [test_perm_id]},
                            token=admin_token)
            print(f"    设置权限 HTTP {sb2}: code={rb2.get('code')}, message={rb2.get('message', '')!r}")

            rows_rp_before, _ = db_query(
                f"SELECT COUNT(*) FROM role_permissions WHERE role_id={d61b_role_id}"
            )
            rp_count_before = int(rows_rp_before[0][0]) if rows_rp_before else 0
            print(f"    设置后 role_permissions 记录数={rp_count_before}")

            print(f"\n  D-61(b).3 删除该角色（未分配给任何用户，应返回 200）...")
            sb3, rb3 = http("DELETE", f"/api/admin/roles/{d61b_role_id}", token=admin_token)
            print(f"    DELETE HTTP {sb3}: code={rb3.get('code')}, message={rb3.get('message', '')!r}")

            if sb3 == 200 and rb3.get("code") == 0:
                ok("D-61.b  角色仅设置权限（未分配用户）删除 → HTTP 200")
            else:
                fail("D-61.b  角色删除失败", f"HTTP={sb3}, code={rb3.get('code')}, message={rb3.get('message', '')!r}")

            # 验证 role_permissions 被清理
            rows_rp_after, _ = db_query(
                f"SELECT COUNT(*) FROM role_permissions WHERE role_id={d61b_role_id}"
            )
            rp_count_after = int(rows_rp_after[0][0]) if rows_rp_after else -1
            print(f"    删除后 role_permissions 记录数={rp_count_after}")

            if rp_count_after == 0:
                ok("D-61.c  role_permissions 关联记录已被清理", f"删除后记录数={rp_count_after}")
            else:
                fail("D-61.c  role_permissions 关联记录未被清理", f"删除后记录数={rp_count_after}")

            # 验证 roles 表中该角色已不存在
            rows_role_after, _ = db_query(f"SELECT COUNT(*) FROM roles WHERE id={d61b_role_id}")
            role_exists_after = int(rows_role_after[0][0]) > 0 if rows_role_after else True
            if not role_exists_after:
                ok("D-61.d  roles 表中该角色记录已被删除")
            else:
                fail("D-61.d  roles 表中该角色记录仍存在", f"role_id={d61b_role_id}")
        else:
            skip("D-61(b)", "无可用 permission_id")
    else:
        fail("D-61(b)", "创建角色失败")
else:
    skip("D-61(b)", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-66：invalidateGroupMembersCache batchSize=500 分批循环（代码审查确认，无接口行为变更）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-66  invalidateGroupMembersCache batchSize=500 分批循环{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

skip("D-66", "代码审查确认（无接口层行为变更，性能优化），不通过 API/DB 验证")


# ════════════════════════════════════════════════════════════════════════════════
# 清理本次测试创建的临时数据
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}清理本次测试创建的临时数据{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# 清理 d62 测试分组
if d62_group_id:
    db_exec(f"DELETE FROM group_permissions WHERE group_id={d62_group_id}")
    db_exec(f"DELETE FROM user_group_members WHERE group_id={d62_group_id}")
    db_exec(f"DELETE FROM user_groups WHERE id={d62_group_id}")
    print(f"  已清理 D-62 测试分组 id={d62_group_id}")

# 清理 D-57/D-61a 创建的角色（先清理 role_permissions/user_roles，再删 roles）
for rid in created_role_ids:
    db_exec(f"DELETE FROM role_permissions WHERE role_id={rid}")
    db_exec(f"DELETE FROM user_roles WHERE role_id={rid}")
    db_exec(f"DELETE FROM roles WHERE id={rid}")
print(f"  已清理本次创建的角色: {created_role_ids}")

print(f"  清理完成")


# ════════════════════════════════════════════════════════════════════════════════
# 汇总
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*70}{RESET}")
print(f"{BOLD}测试汇总{RESET}")
print(f"{BOLD}{'='*70}{RESET}")

total = passed + failed
print(f"\n  总计: {total}  {GREEN}PASS={passed}{RESET}  {RED}FAIL={failed}{RESET}")

if failed > 0:
    print(f"\n{BOLD}{RED}失败项:{RESET}")
    for status, label, detail in results:
        if status == "FAIL":
            print(f"  {RED}[FAIL]{RESET} {label}")
            if detail:
                print(f"         {detail}")

print()
if failed == 0:
    print(f"{BOLD}{GREEN}全部通过 PASS{RESET}")
else:
    print(f"{BOLD}{RED}存在失败项 FAIL{RESET}")

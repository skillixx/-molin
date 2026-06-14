#!/usr/bin/env python3
"""
PR#93 验收测试 — D-96 补全 bind_phone/bind_email/admin_verify 场景的认证态发码接口

背景：
  D-52 给公开发码接口 POST /api/auth/verification-codes/phone|email 加了 allowedPublicScenes
  白名单（仅 register/login/reset_password），导致 bind_phone/bind_email/admin_verify 三种 scene
  完全无法获取验证码，破坏了"修改手机号/邮箱"和"管理员双重认证"功能。
  D-96 新增 4 个需登录认证的专用发码接口来恢复这两个功能。

测试用例：
  D96-01  POST /api/me/verification-codes/phone（新手机号）→ 200，data.code 返回明文验证码
  D96-02  用该 code 调用 PATCH /api/me/phone 完成换绑 → 200，GET /api/me 确认手机号已更新
          边界：传入已被占用的手机号 → 40000 ErrPhoneAlreadyExists
  D96-03  POST /api/me/verification-codes/email（新邮箱）→ 200，data.code 返回明文验证码
  D96-04  用该 code 调用 PATCH /api/me/email 完成换绑 → 200，GET /api/me 确认邮箱已更新
          边界：传入已被占用的邮箱 → 40000 ErrEmailAlreadyExists
  D96-05  管理员 POST /api/admin/auth/verification-codes/phone → 200，data.code 返回明文验证码
  D96-06  用该 code 调用 POST /api/admin/auth/verify-phone → 200，GET /api/me 确认 admin_phone_verified=true
  D96-07  管理员 POST /api/admin/auth/verification-codes/email → 200，data.code 返回明文验证码
  D96-08  用该 code 调用 POST /api/admin/auth/verify-email → 200，GET /api/me 确认 admin_email_verified=true
          边界：账号未绑定手机号/邮箱时调用对应发码接口 → 40000 ErrPhoneNotBound/ErrEmailNotBound
  D96-09  无 user:manage 权限的普通用户调用管理员发码接口 → 403/40003
  D96-regression  公开接口 scene=bind_phone/bind_email/admin_verify 仍应被 D-52 白名单拒绝（400 ErrInvalidScene）

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr93_d96_bind_admin_verify_code.py
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

GREEN, RED, YELLOW, CYAN, BOLD, RESET = "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m"

passed = failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}[PASS]{RESET} {label}" + (f"\n         {detail}" if detail else ""))
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}[FAIL]{RESET} {label}" + (f"\n         {RED}{detail}{RESET}" if detail else ""))
    results.append(("FAIL", label, detail))


def skip(label, reason=""):
    print(f"  {YELLOW}[SKIP]{RESET} {label}" + (f"\n         {YELLOW}{reason}{RESET}" if reason else ""))
    results.append(("SKIP", label, reason))


# ── HTTP 工具 ──────────────────────────────────────────────────────────────────

def http(method, path, body=None, token=None, extra_headers=None):
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
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    if result.returncode != 0:
        return None, result.stderr.strip()
    rows = []
    for line in result.stdout.strip().split("\n"):
        if line:
            rows.append(line.split("\t"))
    return rows, None


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return result.returncode == 0, (result.stderr.strip() if result.returncode != 0 else None)


# ── 注册 / 登录工具 ─────────────────────────────────────────────────────────────

def register_user_via_api(email, phone, password, username=None):
    """通过 DB 插入 register 验证码后，调用 POST /api/auth/register 注册账号。返回 (user_id, access_token) 或 (None, None)。"""
    otp_code = "888888"
    otp_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"

    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('phone', '{phone}', '{otp_sha}', 'register', {expire_sql})")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('email', '{email}', '{otp_sha}', 'register', {expire_sql})")

    body = {"email": email, "phone": phone, "password": password, "phone_code": otp_code, "email_code": otp_code}
    if username:
        body["username"] = username

    status, resp = http("POST", "/api/auth/register", body)
    if status in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
        return user_id, token
    print(f"    {RED}注册接口返回非预期: HTTP={status}, {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
    return None, None


def login_email(email, password):
    s, r = http("POST", "/api/auth/login/email", {"email": email, "password": password})
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        return data.get("access_token"), data.get("refresh_token")
    return None, None


# ────────────────────────────────────────────────────────────────────────────────
TS = int(time.time())

ADMIN_EMAIL    = f"d96adm{TS}@testmail.io"
ADMIN_PHONE    = f"190{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@D96Admin"

PLAIN_EMAIL    = f"d96u{TS}@testmail.io"
PLAIN_PHONE    = f"191{TS % 100000000:08d}"
PLAIN_PASSWORD = "Test@D96User"

# 占用目标（用于"已被占用"边界）
OCCUPIED_EMAIL = f"d96occ{TS}@testmail.io"
OCCUPIED_PHONE = f"192{TS % 100000000:08d}"
OCCUPIED_PASSWORD = "Test@D96Occ"

# 未绑定手机号的第二个管理员（用于 D96-05 的"未绑定"边界）
UNBOUND_ADMIN_EMAIL    = f"d96adm2{TS}@testmail.io"
UNBOUND_ADMIN_PHONE    = f"193{TS % 100000000:08d}"
UNBOUND_ADMIN_PASSWORD = "Test@D96Admin2"

# 未绑定邮箱的第三个管理员（用于 D96-07 的"未绑定"边界）
UNBOUND_ADMIN2_EMAIL    = f"d96adm3{TS}@testmail.io"
UNBOUND_ADMIN2_PHONE    = f"194{TS % 100000000:08d}"
UNBOUND_ADMIN2_PASSWORD = "Test@D96Admin3"

# 新手机号/新邮箱（D96-01/03 换绑目标）
NEW_PHONE = f"195{TS % 100000000:08d}"
NEW_EMAIL = f"d96new{TS}@testmail.io"

print(f"{BOLD}{CYAN}PR#93 验收测试 — D-96 绑定/管理员双重认证发码接口{RESET}")
print(f"  API_BASE: {API_BASE}  TS={TS}\n")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：admin 角色 + 权限
# ════════════════════════════════════════════════════════════════════════════════
print(f"{BOLD}前置准备：确保 admin 角色与权限{RESET}")

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

db_exec(f"INSERT IGNORE INTO role_permissions (role_id, permission_id) SELECT {admin_role_id}, p.id FROM permissions p")
print(f"  已确保 admin 角色绑定所有现有权限（含 user:manage）")


def grant_admin_role(user_id):
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {admin_role_id})")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：注册测试账号
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：注册测试账号{RESET}")

print(f"\n  注册管理员账号 {ADMIN_EMAIL} ...")
admin_user_id, admin_token = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"d96adm{TS}")
if not admin_user_id:
    print(f"  {RED}管理员账号注册失败，中止{RESET}")
    raise SystemExit(1)
grant_admin_role(admin_user_id)
print(f"  管理员账号注册成功: id={admin_user_id}，已绑定 admin 角色（不预先设置双重认证）")

print(f"\n  注册普通用户 {PLAIN_EMAIL} ...")
plain_user_id, plain_token = register_user_via_api(PLAIN_EMAIL, PLAIN_PHONE, PLAIN_PASSWORD, f"d96u{TS}")
if not plain_user_id:
    print(f"  {RED}普通用户注册失败，中止{RESET}")
    raise SystemExit(1)
print(f"  普通用户注册成功: id={plain_user_id}")

print(f"\n  注册占用目标账号 {OCCUPIED_EMAIL} ...")
occ_user_id, _ = register_user_via_api(OCCUPIED_EMAIL, OCCUPIED_PHONE, OCCUPIED_PASSWORD, f"d96occ{TS}")
if not occ_user_id:
    print(f"  {RED}占用目标账号注册失败，中止{RESET}")
    raise SystemExit(1)
print(f"  占用目标账号注册成功: id={occ_user_id}（其 phone={OCCUPIED_PHONE}, email={OCCUPIED_EMAIL} 将用于已占用边界测试）")

print(f"\n  注册第二管理员账号（用于未绑定手机号边界） {UNBOUND_ADMIN_EMAIL} ...")
unbound_admin_id, unbound_admin_token = register_user_via_api(UNBOUND_ADMIN_EMAIL, UNBOUND_ADMIN_PHONE, UNBOUND_ADMIN_PASSWORD, f"d96adm2{TS}")
if unbound_admin_id:
    grant_admin_role(unbound_admin_id)
    # 注册时已取得 access_token（JWT 只编码 user_id，不受后续清空 phone/email 影响），
    # 后续会清空该账号的手机号，用于测试 ErrPhoneNotBound
    print(f"  第二管理员账号注册成功: id={unbound_admin_id}，已绑定 admin 角色")
else:
    print(f"  {YELLOW}第二管理员账号注册失败，未绑定手机号边界测试将被跳过{RESET}")

print(f"\n  注册第三管理员账号（用于未绑定邮箱边界） {UNBOUND_ADMIN2_EMAIL} ...")
unbound_admin2_id, unbound_admin2_token = register_user_via_api(UNBOUND_ADMIN2_EMAIL, UNBOUND_ADMIN2_PHONE, UNBOUND_ADMIN2_PASSWORD, f"d96adm3{TS}")
if unbound_admin2_id:
    grant_admin_role(unbound_admin2_id)
    # 同理，先保留注册时拿到的 access_token，后续会清空该账号的邮箱，用于测试 ErrEmailNotBound
    print(f"  第三管理员账号注册成功: id={unbound_admin2_id}，已绑定 admin 角色")
else:
    print(f"  {YELLOW}第三管理员账号注册失败，未绑定邮箱边界测试将被跳过{RESET}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-01  POST /api/me/verification-codes/phone（新手机号）→ 200，data.code 明文返回{RESET}")
s, r = http("POST", "/api/me/verification-codes/phone", {"phone": NEW_PHONE}, token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")

new_phone_code = None
if s == 200 and r.get("code") == 0:
    new_phone_code = r.get("data", {}).get("code")
    if new_phone_code:
        ok("D96-01  发码成功，data.code 返回明文验证码", f"code={new_phone_code}")
    else:
        fail("D96-01  data.code 为空（非生产环境应返回明文验证码）", f"data={r.get('data')}")
else:
    fail("D96-01  请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-02  用该 code 调用 PATCH /api/me/phone 完成换绑 → 200，GET /api/me 确认更新{RESET}")
if new_phone_code:
    s, r = http("PATCH", "/api/me/phone", {"phone": NEW_PHONE, "code": new_phone_code}, token=plain_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        ok("D96-02  PATCH /api/me/phone 换绑成功")
        s2, r2 = http("GET", "/api/me", token=plain_token)
        me = r2.get("data", {})
        masked = me.get("phone")
        expect_masked = NEW_PHONE[:3] + "****" + NEW_PHONE[-4:]
        if masked == expect_masked:
            ok("D96-02  GET /api/me 手机号已更新为新号码（脱敏一致）", f"phone={masked}")
        else:
            fail("D96-02  GET /api/me 手机号未更新或脱敏不符", f"期望={expect_masked} 实际={masked}")
        if me.get("phone_verified") is True:
            ok("D96-02  GET /api/me phone_verified=true")
        else:
            fail("D96-02  GET /api/me phone_verified 非 true", f"phone_verified={me.get('phone_verified')}")
    else:
        fail("D96-02  PATCH /api/me/phone 失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D96-02  前置失败：未取得新手机号验证码（D96-01 失败）", "")

# 注：docs/full-api-design.md L453 明确规定 D-96 绑定发码接口在手机号已被占用时返回
# 409/40900（与 handleAuthError 中 ErrPhoneAlreadyExists 的统一映射一致），而非 40000。
print(f"\n  边界：传入已被占用的手机号 {OCCUPIED_PHONE} → 期望 409/40900 ErrPhoneAlreadyExists（按 full-api-design.md L453）")
s, r = http("POST", "/api/me/verification-codes/phone", {"phone": OCCUPIED_PHONE}, token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
if s == 409 and r.get("code") == 40900:
    ok("D96-02b  已被占用的手机号发码 → 409/40900（符合 full-api-design.md L453）", f"msg={r.get('message')}")
else:
    fail("D96-02b  已被占用的手机号发码未返回预期 409/40900", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-03  POST /api/me/verification-codes/email（新邮箱）→ 200，data.code 明文返回{RESET}")
s, r = http("POST", "/api/me/verification-codes/email", {"email": NEW_EMAIL}, token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")

new_email_code = None
if s == 200 and r.get("code") == 0:
    new_email_code = r.get("data", {}).get("code")
    if new_email_code:
        ok("D96-03  发码成功，data.code 返回明文验证码", f"code={new_email_code}")
    else:
        fail("D96-03  data.code 为空（非生产环境应返回明文验证码）", f"data={r.get('data')}")
else:
    fail("D96-03  请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-04  用该 code 调用 PATCH /api/me/email 完成换绑 → 200，GET /api/me 确认更新{RESET}")
if new_email_code:
    s, r = http("PATCH", "/api/me/email", {"email": NEW_EMAIL, "code": new_email_code}, token=plain_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        ok("D96-04  PATCH /api/me/email 换绑成功")
        s2, r2 = http("GET", "/api/me", token=plain_token)
        me = r2.get("data", {})
        masked = me.get("email")
        at_idx = NEW_EMAIL.index("@")
        if at_idx <= 2:
            expect_masked = NEW_EMAIL[:at_idx] + "@" + NEW_EMAIL[at_idx+1:]
        else:
            expect_masked = NEW_EMAIL[:2] + "***" + NEW_EMAIL[at_idx:]
        if masked == expect_masked:
            ok("D96-04  GET /api/me 邮箱已更新为新邮箱（脱敏一致）", f"email={masked}")
        else:
            fail("D96-04  GET /api/me 邮箱未更新或脱敏不符", f"期望={expect_masked} 实际={masked}")
        if me.get("email_verified") is True:
            ok("D96-04  GET /api/me email_verified=true")
        else:
            fail("D96-04  GET /api/me email_verified 非 true", f"email_verified={me.get('email_verified')}")
    else:
        fail("D96-04  PATCH /api/me/email 失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D96-04  前置失败：未取得新邮箱验证码（D96-03 失败）", "")

# 注：docs/full-api-design.md L473 明确规定 D-96 绑定发码接口在邮箱已被占用时返回
# 409/40900（与 handleAuthError 中 ErrEmailAlreadyExists 的统一映射一致），而非 40000。
print(f"\n  边界：传入已被占用的邮箱 {OCCUPIED_EMAIL} → 期望 409/40900 ErrEmailAlreadyExists（按 full-api-design.md L473）")
s, r = http("POST", "/api/me/verification-codes/email", {"email": OCCUPIED_EMAIL}, token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
if s == 409 and r.get("code") == 40900:
    ok("D96-04b  已被占用的邮箱发码 → 409/40900（符合 full-api-design.md L473）", f"msg={r.get('message')}")
else:
    fail("D96-04b  已被占用的邮箱发码未返回预期 409/40900", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}★ D96-05  管理员 POST /api/admin/auth/verification-codes/phone → 200，data.code 明文返回{RESET}")

# 重新登录管理员，确保 token 有效
admin_token, admin_refresh = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
admin_phone_code = None
if not admin_token:
    fail("D96-05  前置失败：管理员重新登录失败", "")
else:
    s, r = http("POST", "/api/admin/auth/verification-codes/phone", token=admin_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        admin_phone_code = r.get("data", {}).get("code")
        if admin_phone_code:
            ok("D96-05  管理员手机验证码发送成功，data.code 返回明文验证码", f"code={admin_phone_code}")
        else:
            fail("D96-05  data.code 为空（非生产环境应返回明文验证码）", f"data={r.get('data')}")
    else:
        fail("D96-05  请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}★ D96-06  用该 code 调用 POST /api/admin/auth/verify-phone → 200，GET /api/me 确认 admin_phone_verified=true{RESET}")

if admin_token and admin_phone_code:
    s, r = http("POST", "/api/admin/auth/verify-phone", {"code": admin_phone_code}, token=admin_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        ok("D96-06  POST /api/admin/auth/verify-phone 成功")
        s2, r2 = http("GET", "/api/me", token=admin_token)
        me = r2.get("data", {})
        if me.get("admin_phone_verified") is True:
            ok("D96-06  GET /api/me admin_phone_verified=true")
        else:
            fail("D96-06  GET /api/me admin_phone_verified 非 true", f"admin_phone_verified={me.get('admin_phone_verified')}")
    else:
        fail("D96-06  POST /api/admin/auth/verify-phone 失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D96-06  前置失败：管理员手机验证码未取得（D96-05 失败）", "")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}★ D96-07  管理员 POST /api/admin/auth/verification-codes/email → 200，data.code 明文返回{RESET}")

admin_email_code = None
if admin_token:
    s, r = http("POST", "/api/admin/auth/verification-codes/email", token=admin_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        admin_email_code = r.get("data", {}).get("code")
        if admin_email_code:
            ok("D96-07  管理员邮箱验证码发送成功，data.code 返回明文验证码", f"code={admin_email_code}")
        else:
            fail("D96-07  data.code 为空（非生产环境应返回明文验证码）", f"data={r.get('data')}")
    else:
        fail("D96-07  请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D96-07  前置失败：管理员 token 不可用", "")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}★ D96-08  用该 code 调用 POST /api/admin/auth/verify-email → 200，GET /api/me 确认 admin_email_verified=true{RESET}")
print(f"  （规范 2.15：verify-email 要求手机已在有效期内完成验证，本测试已先于 D96-06 完成手机验证）")

if admin_token and admin_email_code:
    s, r = http("POST", "/api/admin/auth/verify-email", {"code": admin_email_code}, token=admin_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 200 and r.get("code") == 0:
        ok("D96-08  POST /api/admin/auth/verify-email 成功")
        s2, r2 = http("GET", "/api/me", token=admin_token)
        me = r2.get("data", {})
        if me.get("admin_email_verified") is True:
            ok("D96-08  GET /api/me admin_email_verified=true")
        else:
            fail("D96-08  GET /api/me admin_email_verified 非 true", f"admin_email_verified={me.get('admin_email_verified')}")
    else:
        fail("D96-08  POST /api/admin/auth/verify-email 失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D96-08  前置失败：管理员邮箱验证码未取得（D96-07 失败）", "")


# ── D96-05/07 边界：账号未绑定手机号/邮箱 ──────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-05/07 边界：账号未绑定手机号/邮箱 → 40000 ErrPhoneNotBound/ErrEmailNotBound{RESET}")

if unbound_admin_id and unbound_admin_token:
    # 清空该账号的手机号，用于测试 ErrPhoneNotBound
    # access_token 在注册时已取得（JWT 只编码 user_id），不受后续清空 phone 字段影响
    db_exec(f"UPDATE users SET phone=NULL, phone_verified=0 WHERE id={unbound_admin_id}")

    s0, r0 = http("GET", "/api/me", token=unbound_admin_token)
    me0 = r0.get("data", {})
    print(f"  GET /api/me（清空手机号后）: phone={me0.get('phone')!r}")

    s, r = http("POST", "/api/admin/auth/verification-codes/phone", token=unbound_admin_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 400 and r.get("code") == 40000:
        ok("D96-05边界  未绑定手机号调用发码接口 → 400/40000", f"msg={r.get('message')}")
    else:
        fail("D96-05边界  未绑定手机号调用发码接口未返回预期 40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    skip("D96-05边界  未绑定手机号场景", "缺少可用的第二管理员测试账号（注册失败）")

if unbound_admin2_id and unbound_admin2_token:
    # 清空该账号的邮箱，用于测试 ErrEmailNotBound
    # access_token 在注册时已取得（JWT 只编码 user_id），不受后续清空 email 字段影响
    db_exec(f"UPDATE users SET email=NULL, email_verified=0 WHERE id={unbound_admin2_id}")

    s0, r0 = http("GET", "/api/me", token=unbound_admin2_token)
    me0 = r0.get("data", {})
    print(f"  GET /api/me（清空邮箱后）: email={me0.get('email')!r}")

    s, r = http("POST", "/api/admin/auth/verification-codes/email", token=unbound_admin2_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
    if s == 400 and r.get("code") == 40000:
        ok("D96-07边界  未绑定邮箱调用发码接口 → 400/40000", f"msg={r.get('message')}")
    else:
        fail("D96-07边界  未绑定邮箱调用发码接口未返回预期 40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    skip("D96-07边界  未绑定邮箱场景", "缺少可用的第三管理员测试账号（注册失败）")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-09  无 user:manage 权限的普通用户调用管理员发码接口 → 403/40003{RESET}")

s, r = http("POST", "/api/admin/auth/verification-codes/phone", token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
if s == 403:
    ok("D96-09  普通用户调用 /api/admin/auth/verification-codes/phone → 403", f"code={r.get('code')} msg={r.get('message')}")
else:
    fail("D96-09  普通用户调用 /api/admin/auth/verification-codes/phone 未返回 403", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

s, r = http("POST", "/api/admin/auth/verification-codes/email", token=plain_token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
if s == 403:
    ok("D96-09  普通用户调用 /api/admin/auth/verification-codes/email → 403", f"code={r.get('code')} msg={r.get('message')}")
else:
    fail("D96-09  普通用户调用 /api/admin/auth/verification-codes/email 未返回 403", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D96-regression  公开接口 scene=bind_phone/bind_email/admin_verify 仍应被 D-52 白名单拒绝{RESET}")

for scene in ("bind_phone", "bind_email", "admin_verify"):
    s, r = http("POST", "/api/auth/verification-codes/phone", {"phone": f"199{TS % 100000000:08d}", "scene": scene})
    print(f"  HTTP {s}  scene={scene}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000:
        ok(f"D96-regression  公开接口 /verification-codes/phone scene={scene} → 400/40000", f"msg={r.get('message')}")
    else:
        fail(f"D96-regression  公开接口 /verification-codes/phone scene={scene} 未返回预期 40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    s, r = http("POST", "/api/auth/verification-codes/email", {"email": f"d96reg{scene}{TS}@testmail.io", "scene": scene})
    print(f"  HTTP {s}  scene={scene}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000:
        ok(f"D96-regression  公开接口 /verification-codes/email scene={scene} → 400/40000", f"msg={r.get('message')}")
    else:
        fail(f"D96-regression  公开接口 /verification-codes/email scene={scene} 未返回预期 40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
total = passed + failed
print(f"{BOLD}总计：{passed}/{total} PASS（{failed} FAIL）{RESET}")
if failed == 0:
    print(f"{GREEN}所有用例通过，D-96 验收通过。{RESET}")
else:
    print(f"{RED}{failed} 个用例失败，请检查上方详情。{RESET}")
    raise SystemExit(1)

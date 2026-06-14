#!/usr/bin/env python3
"""
PR#95 验收测试 — D-94 统一密码长度校验（6-72 位）

背景：
  D-94（P2）发现密码长度校验缺失/不一致：部分"设置密码"接口对密码长度
  无任何限制，超长密码（>72字节）会触发 bcrypt.GenerateFromPassword 的硬性
  错误并被透传为 500；部分接口的 <6 位下限校验文案不统一。
  本次修复新增共享函数 validatePasswordLength（[6,72] 闷区间），统一应用到
  4 个"设置/修改密码"接口：
    - POST /api/auth/register              (RegisterReq.Password)
    - PATCH /api/me/password                (ChangePasswordReq.NewPassword)
    - POST /api/auth/password/reset         (ResetPasswordReq.NewPassword)
    - POST /api/admin/users                 (CreateAdminUserReq.Password, A-28)
  登录接口（login/email、login/phone）不受影响。

测试用例：
  D94-01~04   POST /api/auth/register         密码 5/6/72/73 位
  D94-05~08   PATCH /api/me/password          new_password 5/6/72/73 位
  D94-09~12   POST /api/auth/password/reset    new_password 5/6/72/73 位（OTP 校验）
  D94-13~16   POST /api/admin/users           （A-28）password 5/6/72/73 位
  D94-regression  邮箱密码登录不受影响

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr95_d94_password_length.py
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
    """通过 DB 插入 register 验证码后，调用 POST /api/auth/register 注册账号。返回 (status, resp, user_id, access_token)。"""
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
    user_id = None
    token = None
    if status in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
    return status, resp, user_id, token


def issue_register_otp(email, phone):
    """重新插入一对有效的 register OTP（用于重复注册请求时确保验证码未过期/未使用）。"""
    otp_code = "888888"
    otp_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('phone', '{phone}', '{otp_sha}', 'register', {expire_sql})")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('email', '{email}', '{otp_sha}', 'register', {expire_sql})")
    return otp_code


def login_email(email, password):
    s, r = http("POST", "/api/auth/login/email", {"email": email, "password": password})
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        return s, r, data.get("access_token"), data.get("refresh_token")
    return s, r, None, None


def send_reset_code(email):
    """调用 POST /api/auth/verification-codes/email scene=reset_password，返回明文验证码 (非生产环境)。"""
    s, r = http("POST", "/api/auth/verification-codes/email", {"email": email, "scene": "reset_password"})
    if s == 200 and r.get("code") == 0:
        return r.get("data", {}).get("code")
    return None


def grant_admin_role(user_id, admin_role_id):
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {admin_role_id})")


def mark_admin_double_verified(user_id):
    """直接写库使该用户通过管理员双重认证（手机+邮箱），绕过 OTP 流程，模拟已完成 D-96 双重认证。"""
    db_exec(f"UPDATE users SET admin_phone_verified_at = NOW(), admin_email_verified_at = NOW() WHERE id = {user_id}")


# ────────────────────────────────────────────────────────────────────────────────
TS = int(time.time())

print(f"{BOLD}{CYAN}PR#95 验收测试 — D-94 统一密码长度校验（6-72 位）{RESET}")
print(f"  API_BASE: {API_BASE}  TS={TS}\n")


# ════════════════════════════════════════════════════════════════════════════════
# D94-01~04  POST /api/auth/register — 密码 5/6/72/73 位
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D94-01~04  POST /api/auth/register — 密码长度边界 5/6/72/73 位{RESET}")

REG_EMAIL_5  = f"d94reg5_{TS}@testmail.io"
REG_PHONE_5  = f"180{TS % 100000000:08d}"
REG_EMAIL_6  = f"d94reg6_{TS}@testmail.io"
REG_PHONE_6  = f"181{TS % 100000000:08d}"
REG_EMAIL_72 = f"d94reg72_{TS}@testmail.io"
REG_PHONE_72 = f"182{TS % 100000000:08d}"
REG_EMAIL_73 = f"d94reg73_{TS}@testmail.io"
REG_PHONE_73 = f"183{TS % 100000000:08d}"

PWD_5  = "a" * 5
PWD_6  = "a" * 6
PWD_72 = "a" * 72
PWD_73 = "a" * 73

# D94-01: 5 位 → 400 40000「不能少于 6 位」
s, r, uid, tok = register_user_via_api(REG_EMAIL_5, REG_PHONE_5, PWD_5, f"d94r5_{TS}")
print(f"  D94-01 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 400 and r.get("code") == 40000 and "不能少于 6 位" in r.get("message", ""):
    ok("D94-01  注册密码 5 位 → 400/40000「不能少于 6 位」", f"msg={r.get('message')}")
else:
    fail("D94-01  注册密码 5 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# D94-02: 6 位 → 201 注册成功
s, r, reg6_uid, reg6_token = register_user_via_api(REG_EMAIL_6, REG_PHONE_6, PWD_6, f"d94r6_{TS}")
print(f"  D94-02 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 201 and r.get("code") == 0 and reg6_uid:
    ok("D94-02  注册密码 6 位 → 201 注册成功", f"user_id={reg6_uid}")
else:
    fail("D94-02  注册密码 6 位 未返回 201", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# D94-03: 72 位 → 201 注册成功
s, r, reg72_uid, reg72_token = register_user_via_api(REG_EMAIL_72, REG_PHONE_72, PWD_72, f"d94r72_{TS}")
print(f"  D94-03 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 201 and r.get("code") == 0 and reg72_uid:
    ok("D94-03  注册密码 72 位 → 201 注册成功", f"user_id={reg72_uid}")
else:
    fail("D94-03  注册密码 72 位 未返回 201", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# D94-04: 73 位 → 400 40000「不能超过 72 位」
s, r, uid, tok = register_user_via_api(REG_EMAIL_73, REG_PHONE_73, PWD_73, f"d94r73_{TS}")
print(f"  D94-04 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 400 and r.get("code") == 40000 and "不能超过 72 位" in r.get("message", ""):
    ok("D94-04  注册密码 73 位 → 400/40000「不能超过 72 位」", f"msg={r.get('message')}")
else:
    fail("D94-04  注册密码 73 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# 附加验证：用 D94-02 注册的 6 位密码登录验证
if reg6_uid:
    s, r, atok, rtok = login_email(REG_EMAIL_6, PWD_6)
    if s == 200 and atok:
        ok("D94-02附加  6 位密码注册后可正常登录", f"email={REG_EMAIL_6}")
    else:
        fail("D94-02附加  6 位密码注册后登录失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# 附加验证：用 D94-03 注册的 72 位密码登录验证
if reg72_uid:
    s, r, atok, rtok = login_email(REG_EMAIL_72, PWD_72)
    if s == 200 and atok:
        ok("D94-03附加  72 位密码注册后可正常登录", f"email={REG_EMAIL_72}")
    else:
        fail("D94-03附加  72 位密码注册后登录失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
# D94-05~08  PATCH /api/me/password — new_password 5/6/72/73 位
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D94-05~08  PATCH /api/me/password — new_password 长度边界 5/6/72/73 位{RESET}")

CP_EMAIL = f"d94cp_{TS}@testmail.io"
CP_PHONE = f"184{TS % 100000000:08d}"
CP_PASSWORD_0 = "Test@D94cp0"

s, r, cp_uid, cp_token = register_user_via_api(CP_EMAIL, CP_PHONE, CP_PASSWORD_0, f"d94cp_{TS}")
print(f"  前置：注册 ChangePassword 测试账号 HTTP {s}  user_id={cp_uid}")
if not cp_uid or not cp_token:
    fail("D94-05~08 前置失败：测试账号注册失败，后续用例跳过", "")
else:
    cur_password = CP_PASSWORD_0

    # D94-05: new_password 5 位 → 400 40000「不能少于 6 位」
    s, r = http("PATCH", "/api/me/password", {"old_password": cur_password, "new_password": PWD_5}, token=cp_token)
    print(f"  D94-05 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000 and "不能少于 6 位" in r.get("message", ""):
        ok("D94-05  new_password 5 位 → 400/40000「不能少于 6 位」", f"msg={r.get('message')}")
    else:
        fail("D94-05  new_password 5 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-06: new_password 6 位 + old_password 正确 → 200，并可用新密码登录
    s, r = http("PATCH", "/api/me/password", {"old_password": cur_password, "new_password": PWD_6}, token=cp_token)
    print(f"  D94-06 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 200 and r.get("code") == 0:
        ok("D94-06  new_password 6 位 → 200")
        ls, lr, atok, rtok = login_email(CP_EMAIL, PWD_6)
        if ls == 200 and atok:
            ok("D94-06附加  改密后可用新密码（6位）登录")
            cur_password = PWD_6
            cp_token = atok
        else:
            fail("D94-06附加  改密后无法用新密码（6位）登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
    else:
        fail("D94-06  new_password 6 位 未返回 200", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-07: new_password 72 位 + old_password(当前) 正确 → 200
    s, r = http("PATCH", "/api/me/password", {"old_password": cur_password, "new_password": PWD_72}, token=cp_token)
    print(f"  D94-07 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 200 and r.get("code") == 0:
        ok("D94-07  new_password 72 位 → 200")
        ls, lr, atok, rtok = login_email(CP_EMAIL, PWD_72)
        if ls == 200 and atok:
            ok("D94-07附加  改密后可用新密码（72位）登录")
            cur_password = PWD_72
            cp_token = atok
        else:
            fail("D94-07附加  改密后无法用新密码（72位）登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
    else:
        fail("D94-07  new_password 72 位 未返回 200", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-08: new_password 73 位 → 400 40000「不能超过 72 位」
    s, r = http("PATCH", "/api/me/password", {"old_password": cur_password, "new_password": PWD_73}, token=cp_token)
    print(f"  D94-08 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000 and "不能超过 72 位" in r.get("message", ""):
        ok("D94-08  new_password 73 位 → 400/40000「不能超过 72 位」", f"msg={r.get('message')}")
    else:
        fail("D94-08  new_password 73 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
# D94-09~12  POST /api/auth/password/reset — new_password 5/6/72/73 位（OTP）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D94-09~12  POST /api/auth/password/reset — new_password 长度边界 5/6/72/73 位{RESET}")

RP_EMAIL = f"d94rp_{TS}@testmail.io"
RP_PHONE = f"185{TS % 100000000:08d}"
RP_PASSWORD_0 = "Test@D94rp0"

s, r, rp_uid, rp_token = register_user_via_api(RP_EMAIL, RP_PHONE, RP_PASSWORD_0, f"d94rp_{TS}")
print(f"  前置：注册 ResetPassword 测试账号 HTTP {s}  user_id={rp_uid}")
if not rp_uid:
    fail("D94-09~12 前置失败：测试账号注册失败，后续用例跳过", "")
else:
    # D94-09: new_password 5 位 → 400 40000「不能少于 6 位」
    # 注意：根据 handler 实现，密码长度校验在 OTP 校验（ResetPassword service 内）之前完成，
    # 因此即使发送了验证码，本次失败请求不会消耗该验证码（CheckAndMarkUsed 未被调用）。
    code1 = send_reset_code(RP_EMAIL)
    print(f"  获取 reset_password 验证码: code={code1}")
    if not code1:
        fail("D94-09~12 前置失败：获取 reset_password 验证码失败", "")
    else:
        s, r = http("POST", "/api/auth/password/reset", {
            "target": RP_EMAIL, "target_type": "email", "code": code1, "new_password": PWD_5,
        })
        print(f"  D94-09 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
        if s == 400 and r.get("code") == 40000 and "不能少于 6 位" in r.get("message", ""):
            ok("D94-09  new_password 5 位 → 400/40000「不能少于 6 位」", f"msg={r.get('message')}")
        else:
            fail("D94-09  new_password 5 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

        # 验证 OTP 是否被消耗：若校验先于密码长度校验执行，code1 会被标记 used，
        # 后续 D94-10 用同一 code 将失败（ErrInvalidCode）。这里直接复用 code1 验证。
        # D94-10: new_password 6 位 + 正确 code → 200，重置成功
        s, r = http("POST", "/api/auth/password/reset", {
            "target": RP_EMAIL, "target_type": "email", "code": code1, "new_password": PWD_6,
        })
        print(f"  D94-10 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
        if s == 200 and r.get("code") == 0:
            ok("D94-10  new_password 6 位 + 正确 code（D94-09 未消耗）→ 200")
            ls, lr, atok, rtok = login_email(RP_EMAIL, PWD_6)
            if ls == 200 and atok:
                ok("D94-10附加  重置后可用新密码（6位）登录")
            else:
                fail("D94-10附加  重置后无法用新密码（6位）登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
        else:
            # 如果 OTP 被 D94-09 消耗了，这里会失败；记录详细信息并重新获取 code 重试一次
            fail("D94-10  new_password 6 位 未返回 200（可能 D94-09 已消耗验证码，见日志说明）",
                 f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
            code2 = send_reset_code(RP_EMAIL)
            print(f"  D94-10 重试：重新获取验证码 code={code2}")
            if code2:
                s, r = http("POST", "/api/auth/password/reset", {
                    "target": RP_EMAIL, "target_type": "email", "code": code2, "new_password": PWD_6,
                })
                print(f"  D94-10 重试 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
                if s == 200 and r.get("code") == 0:
                    ok("D94-10重试  new_password 6 位 + 新 code → 200")
                    ls, lr, atok, rtok = login_email(RP_EMAIL, PWD_6)
                    if ls == 200 and atok:
                        ok("D94-10重试附加  重置后可用新密码（6位）登录")
                    else:
                        fail("D94-10重试附加  重置后无法用新密码（6位）登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")

        # D94-11: new_password 72 位 + 正确 code → 200
        code3 = send_reset_code(RP_EMAIL)
        print(f"  获取新 reset_password 验证码（D94-11）: code={code3}")
        if code3:
            s, r = http("POST", "/api/auth/password/reset", {
                "target": RP_EMAIL, "target_type": "email", "code": code3, "new_password": PWD_72,
            })
            print(f"  D94-11 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
            if s == 200 and r.get("code") == 0:
                ok("D94-11  new_password 72 位 + 正确 code → 200")
                ls, lr, atok, rtok = login_email(RP_EMAIL, PWD_72)
                if ls == 200 and atok:
                    ok("D94-11附加  重置后可用新密码（72位）登录")
                else:
                    fail("D94-11附加  重置后无法用新密码（72位）登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
            else:
                fail("D94-11  new_password 72 位 未返回 200", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
        else:
            fail("D94-11 前置失败：获取 reset_password 验证码失败", "")

        # D94-12: new_password 73 位 → 400 40000「不能超过 72 位」
        # 密码长度校验在 OTP 校验之前，因此即使不传 code 或传错误 code 也应优先返回长度错误。
        s, r = http("POST", "/api/auth/password/reset", {
            "target": RP_EMAIL, "target_type": "email", "code": "000000", "new_password": PWD_73,
        })
        print(f"  D94-12 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
        if s == 400 and r.get("code") == 40000 and "不能超过 72 位" in r.get("message", ""):
            ok("D94-12  new_password 73 位 → 400/40000「不能超过 72 位」", f"msg={r.get('message')}")
        else:
            fail("D94-12  new_password 73 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
# D94-13~16  POST /api/admin/users（A-28）— password 5/6/72/73 位
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D94-13~16  POST /api/admin/users（A-28）— password 长度边界 5/6/72/73 位{RESET}")

print(f"\n{BOLD}前置准备：admin 角色 + user:manage 权限 + 双重认证{RESET}")
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
        print(f"  {RED}admin 角色创建失败，D94-13~16 跳过{RESET}")
        admin_role_id = None

if admin_role_id:
    db_exec(f"INSERT IGNORE INTO role_permissions (role_id, permission_id) SELECT {admin_role_id}, p.id FROM permissions p")
    print(f"  已确保 admin 角色绑定所有现有权限（含 user:manage）")

ADMIN_EMAIL = f"d94adm_{TS}@testmail.io"
ADMIN_PHONE = f"186{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@D94Admin"

admin_token = None
if admin_role_id:
    s, r, admin_uid, admin_token = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"d94adm_{TS}")
    print(f"  注册管理员账号 HTTP {s}  user_id={admin_uid}")
    if admin_uid:
        grant_admin_role(admin_uid, admin_role_id)
        mark_admin_double_verified(admin_uid)
        # 重新登录以确保 token 有效（注册返回的 token 同样有效，但重新登录更接近真实流程）
        ls, lr, admin_token, _ = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
        print(f"  管理员账号已绑定 admin 角色 + 标记双重认证完成，登录 HTTP {ls}")
    else:
        admin_token = None

if not admin_token:
    skip("D94-13~16  全部跳过", "管理员账号准备失败（注册或权限配置失败）")
else:
    NEW_ADMIN_EMAIL_5  = f"d94cau5_{TS}@testmail.io"
    NEW_ADMIN_PHONE_5  = f"187{TS % 100000000:08d}"
    NEW_ADMIN_EMAIL_6  = f"d94cau6_{TS}@testmail.io"
    NEW_ADMIN_PHONE_6  = f"170{TS % 100000000:08d}"
    NEW_ADMIN_EMAIL_72 = f"d94cau72_{TS}@testmail.io"
    NEW_ADMIN_PHONE_72 = f"171{TS % 100000000:08d}"
    NEW_ADMIN_EMAIL_73 = f"d94cau73_{TS}@testmail.io"
    NEW_ADMIN_PHONE_73 = f"172{TS % 100000000:08d}"

    # D94-13: password 5 位 → 400 40000「不能少于 6 位」
    s, r = http("POST", "/api/admin/users",
                 {"email": NEW_ADMIN_EMAIL_5, "phone": NEW_ADMIN_PHONE_5, "password": PWD_5},
                 token=admin_token)
    print(f"  D94-13 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000 and "不能少于 6 位" in r.get("message", ""):
        ok("D94-13  password 5 位 → 400/40000「不能少于 6 位」", f"msg={r.get('message')}")
    else:
        fail("D94-13  password 5 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-14: password 6 位 → 201/200 创建成功
    s, r = http("POST", "/api/admin/users",
                 {"email": NEW_ADMIN_EMAIL_6, "phone": NEW_ADMIN_PHONE_6, "password": PWD_6},
                 token=admin_token)
    print(f"  D94-14 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s in (200, 201) and r.get("code") == 0:
        ok("D94-14  password 6 位 → 创建成功", f"HTTP={s}")
        ls, lr, atok, rtok = login_email(NEW_ADMIN_EMAIL_6, PWD_6)
        if ls == 200 and atok:
            ok("D94-14附加  新建账号可用 6 位密码登录")
        else:
            fail("D94-14附加  新建账号无法用 6 位密码登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
    else:
        fail("D94-14  password 6 位 未返回成功", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-15: password 72 位 → 创建成功
    s, r = http("POST", "/api/admin/users",
                 {"email": NEW_ADMIN_EMAIL_72, "phone": NEW_ADMIN_PHONE_72, "password": PWD_72},
                 token=admin_token)
    print(f"  D94-15 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s in (200, 201) and r.get("code") == 0:
        ok("D94-15  password 72 位 → 创建成功", f"HTTP={s}")
        ls, lr, atok, rtok = login_email(NEW_ADMIN_EMAIL_72, PWD_72)
        if ls == 200 and atok:
            ok("D94-15附加  新建账号可用 72 位密码登录")
        else:
            fail("D94-15附加  新建账号无法用 72 位密码登录", f"HTTP={ls} code={lr.get('code')} msg={lr.get('message','')}")
    else:
        fail("D94-15  password 72 位 未返回成功", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

    # D94-16: password 73 位 → 400 40000「不能超过 72 位」（本次新增校验）
    s, r = http("POST", "/api/admin/users",
                 {"email": NEW_ADMIN_EMAIL_73, "phone": NEW_ADMIN_PHONE_73, "password": PWD_73},
                 token=admin_token)
    print(f"  D94-16 HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
    if s == 400 and r.get("code") == 40000 and "不能超过 72 位" in r.get("message", ""):
        ok("D94-16  password 73 位 → 400/40000「不能超过 72 位」（本次新增校验）", f"msg={r.get('message')}")
    else:
        fail("D94-16  password 73 位 未返回预期 400/40000", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
# D94-regression  登录接口不受影响
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D94-regression  邮箱密码登录不受密码长度校验改动影响{RESET}")

# 使用 D94-02 注册成功的账号（密码已在 D94-02附加 中验证可登录，密码即 PWD_6）
s, r, atok, rtok = login_email(REG_EMAIL_6, PWD_6)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 200 and r.get("code") == 0 and atok:
    ok("D94-regression  已存在账号邮箱密码登录 → 200，登录流程未受影响", f"email={REG_EMAIL_6}")
else:
    fail("D94-regression  邮箱密码登录失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*60}{RESET}")
total = passed + failed
print(f"{BOLD}总计：{passed}/{total} PASS（{failed} FAIL）{RESET}")
if failed == 0:
    print(f"{GREEN}所有用例通过，D-94 验收通过。{RESET}")
else:
    print(f"{RED}{failed} 个用例失败，请检查上方详情。{RESET}")
    raise SystemExit(1)

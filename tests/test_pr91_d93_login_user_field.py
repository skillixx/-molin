#!/usr/bin/env python3
"""
PR#91 验收测试 — D-93 登录/注册/刷新响应补充 user 字段

测试用例：
  D93-01  POST /api/auth/register 成功后 data.user 字段齐全且类型正确
  D93-02  POST /api/auth/login/email 成功后 data.user.id 与注册返回一致，字段齐全
  D93-03  POST /api/auth/login/phone（scene=login）成功后 data.user 字段齐全，user.id 一致
  D93-04  POST /api/auth/refresh 成功后 data.user 字段齐全，user.id 一致
  D93-05  回归：以上接口原有 access_token/refresh_token/expires_in 字段仍正常返回，类型正确
  D93-06  脱敏正确性：user.email/user.phone 不为明文，格式与 GET /api/me 一致
"""

import hashlib
import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN, RED, YELLOW, CYAN, BOLD, RESET = "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m"

passed = failed = 0


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}[PASS]{RESET} {label}" + (f"\n         {detail}" if detail else ""))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}[FAIL]{RESET} {label}" + (f"\n         {RED}{detail}{RESET}" if detail else ""))


def http(method, path, body=None, token=None, params=None):
    url = API_BASE + path
    if params:
        url = url + "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
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


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return result.returncode == 0, (result.stderr.strip() if result.returncode != 0 else None)


# 测试账号
TS = int(time.time())
OTP = "888888"
OTP_SHA = hashlib.sha256(OTP.encode()).hexdigest()

TEST_EMAIL    = f"d93u{TS}@testmail.io"
TEST_PHONE    = f"181{TS % 100000000:08d}"
TEST_PASSWORD = "Test@D93Login"
TEST_USERNAME = f"d93u{TS}"


def seed_otp(target_type, target_value, scene):
    """直接向数据库写入已知 OTP，绕过短信/邮件发送。"""
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{target_value}' AND scene='{scene}'")
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('{target_type}','{target_value}','{OTP_SHA}','{scene}',DATE_ADD(NOW(), INTERVAL 490 MINUTE))"
    )


# 期望的 user 摘要字段集合
EXPECTED_USER_FIELDS = {"id", "email", "phone", "real_name_status", "status"}
VALID_REAL_NAME_STATUS = {"unverified", "pending", "verified", "rejected"}
VALID_STATUS = {"active", "disabled"}


def check_user_summary(label, user, expect_email=None, expect_phone=None, expect_unverified=True):
    """校验 user 摘要字段：齐全、类型正确、脱敏格式正确。"""
    sub_passed = True

    # 字段齐全
    missing = EXPECTED_USER_FIELDS - set(user.keys())
    if missing:
        fail(f"{label}  user 字段缺失", f"缺少字段: {missing}, 实际 user={json.dumps(user, ensure_ascii=False)}")
        return False
    ok(f"{label}  user 字段齐全", f"keys={sorted(user.keys())}")

    # id 为数字
    if isinstance(user.get("id"), int) and user["id"] > 0:
        ok(f"{label}  user.id 为正整数", f"id={user['id']}")
    else:
        fail(f"{label}  user.id 类型/取值错误", f"id={user.get('id')!r} type={type(user.get('id'))}")
        sub_passed = False

    # email 脱敏格式：xx***@domain
    email = user.get("email")
    if expect_email is not None:
        if email == expect_email:
            ok(f"{label}  user.email 脱敏格式正确", f"email={email}")
        else:
            fail(f"{label}  user.email 脱敏格式不符", f"期望={expect_email} 实际={email}")
            sub_passed = False
        # 明文校验：不应等于原始 email
        if email == TEST_EMAIL:
            fail(f"{label}  user.email 为明文！", f"email={email}")
            sub_passed = False

    # phone 脱敏格式：前3****后4
    phone = user.get("phone")
    if expect_phone is not None:
        if phone == expect_phone:
            ok(f"{label}  user.phone 脱敏格式正确", f"phone={phone}")
        else:
            fail(f"{label}  user.phone 脱敏格式不符", f"期望={expect_phone} 实际={phone}")
            sub_passed = False
        if phone == TEST_PHONE:
            fail(f"{label}  user.phone 为明文！", f"phone={phone}")
            sub_passed = False

    # real_name_status
    rns = user.get("real_name_status")
    if rns in VALID_REAL_NAME_STATUS:
        if expect_unverified and rns != "unverified":
            fail(f"{label}  user.real_name_status 期望 unverified", f"实际={rns}")
            sub_passed = False
        else:
            ok(f"{label}  user.real_name_status 合法", f"real_name_status={rns}")
    else:
        fail(f"{label}  user.real_name_status 不合法", f"real_name_status={rns!r}")
        sub_passed = False

    # status
    status = user.get("status")
    if status in VALID_STATUS:
        ok(f"{label}  user.status 合法", f"status={status}")
    else:
        fail(f"{label}  user.status 不合法", f"status={status!r}")
        sub_passed = False

    return sub_passed


def check_token_fields(label, data):
    """回归检查：access_token/refresh_token/expires_in 字段仍正常返回，类型正确。"""
    at = data.get("access_token")
    rt = data.get("refresh_token")
    ei = data.get("expires_in")

    sub_passed = True
    if isinstance(at, str) and at:
        ok(f"{label}  access_token 字段正常", f"len={len(at)}")
    else:
        fail(f"{label}  access_token 缺失或类型错误", f"access_token={at!r}")
        sub_passed = False

    if isinstance(rt, str) and rt:
        ok(f"{label}  refresh_token 字段正常", f"len={len(rt)}")
    else:
        fail(f"{label}  refresh_token 缺失或类型错误", f"refresh_token={rt!r}")
        sub_passed = False

    if isinstance(ei, int) and ei > 0:
        ok(f"{label}  expires_in 字段正常", f"expires_in={ei}")
    else:
        fail(f"{label}  expires_in 缺失或类型错误", f"expires_in={ei!r}")
        sub_passed = False

    return sub_passed, at, rt


# ────────────────────────────────────────────────────────────────────────────────
print(f"{BOLD}{CYAN}PR#91 验收测试 — D-93 登录/注册/刷新响应 user 字段{RESET}")
print(f"  API_BASE: {API_BASE}  TS={TS}")
print(f"  TEST_EMAIL={TEST_EMAIL}  TEST_PHONE={TEST_PHONE}\n")

expect_email_masked = None
expect_phone_masked = None

# ────────────────────────────────────────────────────────────────────────────────
print(f"{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D93-01  POST /api/auth/register → data.user 字段齐全且类型正确{RESET}")
seed_otp("phone", TEST_PHONE, "register")
seed_otp("email", TEST_EMAIL, "register")
s, r = http("POST", "/api/auth/register", {
    "email": TEST_EMAIL,
    "phone": TEST_PHONE,
    "password": TEST_PASSWORD,
    "phone_code": OTP,
    "email_code": OTP,
    "username": TEST_USERNAME,
})
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:400]}")

register_user_id = None
if s in (200, 201) and r.get("code") == 0:
    data = r.get("data", {})
    user = data.get("user")
    if isinstance(user, dict):
        # 推算期望的脱敏格式
        # email 脱敏: @前2位+***@domain
        at_idx = TEST_EMAIL.index("@")
        if at_idx <= 2:
            expect_email_masked = TEST_EMAIL[:at_idx] + "@" + TEST_EMAIL[at_idx+1:]
        else:
            expect_email_masked = TEST_EMAIL[:2] + "***" + TEST_EMAIL[at_idx:]
        # phone 脱敏: 前3后4，中间****
        expect_phone_masked = TEST_PHONE[:3] + "****" + TEST_PHONE[-4:]

        passed_check = check_user_summary("D93-01", user, expect_email=expect_email_masked,
                                           expect_phone=expect_phone_masked, expect_unverified=True)
        register_user_id = user.get("id")
        # 回归：token 字段
        check_token_fields("D93-01", data)
    else:
        fail("D93-01  data.user 不存在或不是对象", f"data={json.dumps(data, ensure_ascii=False)[:300]}")
else:
    fail("D93-01  注册请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
    raise SystemExit(1)

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D93-02  POST /api/auth/login/email → data.user.id 与注册一致{RESET}")
s, r = http("POST", "/api/auth/login/email", {
    "email": TEST_EMAIL,
    "password": TEST_PASSWORD,
})
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:400]}")

login_email_access_token = None
login_email_refresh_token = None
if s == 200 and r.get("code") == 0:
    data = r.get("data", {})
    user = data.get("user")
    if isinstance(user, dict):
        check_user_summary("D93-02", user, expect_email=expect_email_masked,
                            expect_phone=expect_phone_masked, expect_unverified=True)
        if user.get("id") == register_user_id:
            ok("D93-02  user.id 与注册返回一致", f"id={user.get('id')}")
        else:
            fail("D93-02  user.id 与注册返回不一致", f"注册id={register_user_id} 登录id={user.get('id')}")
        _, login_email_access_token, login_email_refresh_token = check_token_fields("D93-02", data)
    else:
        fail("D93-02  data.user 不存在或不是对象", f"data={json.dumps(data, ensure_ascii=False)[:300]}")
else:
    fail("D93-02  邮箱登录请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D93-03  POST /api/auth/login/phone（scene=login）→ data.user 字段齐全，user.id 一致{RESET}")
# 先种入 scene=login 的验证码（绕过短信发送）
seed_otp("phone", TEST_PHONE, "login")
s, r = http("POST", "/api/auth/login/phone", {
    "phone": TEST_PHONE,
    "code": OTP,
})
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:400]}")

if s == 200 and r.get("code") == 0:
    data = r.get("data", {})
    user = data.get("user")
    if isinstance(user, dict):
        check_user_summary("D93-03", user, expect_email=expect_email_masked,
                            expect_phone=expect_phone_masked, expect_unverified=True)
        if user.get("id") == register_user_id:
            ok("D93-03  user.id 与注册返回一致", f"id={user.get('id')}")
        else:
            fail("D93-03  user.id 与注册返回不一致", f"注册id={register_user_id} 手机登录id={user.get('id')}")
        check_token_fields("D93-03", data)
    else:
        fail("D93-03  data.user 不存在或不是对象", f"data={json.dumps(data, ensure_ascii=False)[:300]}")
else:
    fail("D93-03  手机号登录请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D93-04  POST /api/auth/refresh → data.user 字段齐全，user.id 一致{RESET}")
if login_email_refresh_token:
    s, r = http("POST", "/api/auth/refresh", {"refresh_token": login_email_refresh_token})
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:400]}")
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        user = data.get("user")
        if isinstance(user, dict):
            check_user_summary("D93-04", user, expect_email=expect_email_masked,
                                expect_phone=expect_phone_masked, expect_unverified=True)
            if user.get("id") == register_user_id:
                ok("D93-04  user.id 与注册返回一致", f"id={user.get('id')}")
            else:
                fail("D93-04  user.id 与注册返回不一致", f"注册id={register_user_id} 刷新id={user.get('id')}")
            check_token_fields("D93-04", data)
        else:
            fail("D93-04  data.user 不存在或不是对象", f"data={json.dumps(data, ensure_ascii=False)[:300]}")
    else:
        fail("D93-04  刷新令牌请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D93-04  前置失败：未取得 refresh_token（D93-02 失败）", "")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}D93-05  脱敏正确性：与 GET /api/me 一致{RESET}")
if login_email_access_token:
    s, r = http("GET", "/api/me", token=login_email_access_token)
    print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:400]}")
    if s == 200 and r.get("code") == 0:
        me = r.get("data", {})
        me_email = me.get("email")
        me_phone = me.get("phone")
        if me_email == expect_email_masked:
            ok("D93-05  GET /api/me 的 email 脱敏格式与登录响应一致", f"email={me_email}")
        else:
            fail("D93-05  GET /api/me 的 email 与预期脱敏格式不符", f"期望={expect_email_masked} 实际={me_email}")
        if me_phone == expect_phone_masked:
            ok("D93-05  GET /api/me 的 phone 脱敏格式与登录响应一致", f"phone={me_phone}")
        else:
            fail("D93-05  GET /api/me 的 phone 与预期脱敏格式不符", f"期望={expect_phone_masked} 实际={me_phone}")
    else:
        fail("D93-05  GET /api/me 请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("D93-05  前置失败：未取得 access_token（D93-02 失败）", "")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
total = passed + failed
print(f"{BOLD}总计：{passed}/{total} PASS{RESET}")
if failed == 0:
    print(f"{GREEN}所有用例通过，D-93 验收通过。{RESET}")
else:
    print(f"{RED}{failed} 个用例失败，请检查上方详情。{RESET}")
    raise SystemExit(1)

#!/usr/bin/env python3
"""
用户资料与新版认证接口测试脚本（N-01 ~ N-10）
测试范围：统一注册 / OTP密码重置 / 管理员双重认证 / 个人信息修改 / 资料增强字段

用法：
    API_BASE=http://8.130.9.163:8080 python3 tests/test_user_profile.py

注意：
    - 不依赖 MySQL 直连，所有数据通过 HTTP 接口操作
    - 测试数据使用随机后缀，可多次运行不冲突
    - 非生产环境下验证码接口会在响应体直接返回明文 code
"""

import json
import os
import random
import string
import sys
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE = os.getenv("API_BASE", "http://localhost:8080")

# ── 颜色输出 ──────────────────────────────────────────────
GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed = failed = 0


def ok(label):
    global passed
    passed += 1
    print(f"  {GREEN}✅ {label}{RESET}")


def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}❌ {label}{RESET}"
    if detail:
        msg += f"\n     {RED}{detail}{RESET}"
    print(msg)


def info(msg):
    print(f"  {YELLOW}ℹ  {msg}{RESET}")


def section(title):
    print(f"\n{BOLD}{CYAN}{'─'*50}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'─'*50}{RESET}")


# ── HTTP 工具 ─────────────────────────────────────────────
def request(method, path, body=None, token=None, idempotency_key=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else b""
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=10)
        return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}


def post(path, body=None, token=None):
    return request("POST", path, body, token)


def get(path, token=None):
    return request("GET", path, token=token)


def patch(path, body=None, token=None):
    return request("PATCH", path, body, token)


def assert_status(label, status, expected, body):
    if status == expected:
        ok(f"{label}  →  HTTP {status}")
    else:
        msg = body.get("message", "") if isinstance(body, dict) else ""
        fail(f"{label}  →  HTTP {status}（期望 {expected}）", msg)
    return status == expected


def assert_field(label, value, expected):
    """断言响应体某字段等于期望值。"""
    if value == expected:
        ok(f"{label}  →  {value!r}")
    else:
        fail(f"{label}  →  实际 {value!r}，期望 {expected!r}")
    return value == expected


def assert_field_present(label, value):
    """断言响应体某字段存在且非空。"""
    if value is not None and value != "":
        ok(f"{label}  →  字段存在（{value!r}）")
    else:
        fail(f"{label}  →  字段缺失或为空")
    return bool(value is not None and value != "")


def get_data(body):
    return body.get("data") or {}


def rand_suffix(n=6):
    """生成 n 位随机小写字母数字后缀，避免测试数据冲突。"""
    return "".join(random.choices(string.ascii_lowercase + string.digits, k=n))


# ── 验证码辅助 ────────────────────────────────────────────
def send_phone_code(phone, scene):
    """发送手机验证码并返回明文 code（非生产环境）。"""
    status, body = post("/api/auth/verification-codes/phone",
                        {"target": phone, "scene": scene})
    code = get_data(body).get("code", "")
    info(f"手机验证码 [{scene}] → {code}（HTTP {status}）")
    return status, code


def send_email_code(email, scene):
    """发送邮箱验证码并返回明文 code（非生产环境）。"""
    status, body = post("/api/auth/verification-codes/email",
                        {"target": email, "scene": scene})
    code = get_data(body).get("code", "")
    info(f"邮箱验证码 [{scene}] → {code}（HTTP {status}）")
    return status, code


def login_email(email, password):
    """邮箱登录，返回 (access_token, refresh_token)。"""
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": password})
    d = get_data(body)
    return d.get("access_token", ""), d.get("refresh_token", "")


# ════════════════════════════════════════════════════════
# N-01  统一注册（用户名 + 手机 + 邮箱 + 密码，双OTP）
# ════════════════════════════════════════════════════════
def test_n01():
    section("N-01  统一注册接口（POST /api/auth/register）")
    suffix  = rand_suffix()
    phone   = f"151{suffix}0001"[:11]   # 保持11位
    email   = f"reg_{suffix}@example.com"
    uname   = f"user_{suffix}"
    passwd  = "Test@1234"

    # 1. 发双路验证码
    _, phone_code = send_phone_code(phone, "register")
    _, email_code = send_email_code(email, "register")

    # 2. 正常注册（happy path）
    status, body = post("/api/auth/register", {
        "username":   uname,
        "phone":      phone,
        "email":      email,
        "password":   passwd,
        "phone_code": phone_code,
        "email_code": email_code,
    })
    assert_status("统一注册（正常流程）", status, 201, body)
    d = get_data(body)
    token = d.get("access_token", "")
    info(f"注册成功，access_token 前20: {token[:20]}")

    # 3. 手机号已注册 → 重复注册应被拦截（400/409）
    _, phone_code2 = send_phone_code(phone, "register")
    _, email_code2 = send_email_code(f"dup_{suffix}@example.com", "register")
    status2, body2 = post("/api/auth/register", {
        "username":   f"dup_{suffix}",
        "phone":      phone,        # 已注册的手机号
        "email":      f"dup_{suffix}@example.com",
        "password":   passwd,
        "phone_code": phone_code2,
        "email_code": email_code2,
    })
    if status2 in (400, 409):
        ok(f"手机号重复注册被拦截  →  HTTP {status2}")
    else:
        fail(f"手机号重复注册未拦截  →  HTTP {status2}")

    # 4. 邮箱已注册 → 重复注册应被拦截
    suffix3  = rand_suffix()
    phone3   = f"152{suffix3}0002"[:11]
    _, phone_code3 = send_phone_code(phone3, "register")
    _, email_code3 = send_email_code(email, "register")
    status3, body3 = post("/api/auth/register", {
        "username":   f"dup2_{suffix3}",
        "phone":      phone3,
        "email":      email,        # 已注册的邮箱
        "password":   passwd,
        "phone_code": phone_code3,
        "email_code": email_code3,
    })
    if status3 in (400, 409):
        ok(f"邮箱重复注册被拦截  →  HTTP {status3}")
    else:
        fail(f"邮箱重复注册未拦截  →  HTTP {status3}")

    # 5. 用户名已注册 → 重复注册应被拦截
    suffix4  = rand_suffix()
    phone4   = f"153{suffix4}0003"[:11]
    email4   = f"dup3_{suffix4}@example.com"
    _, phone_code4 = send_phone_code(phone4, "register")
    _, email_code4 = send_email_code(email4, "register")
    status4, body4 = post("/api/auth/register", {
        "username":   uname,        # 已注册的用户名
        "phone":      phone4,
        "email":      email4,
        "password":   passwd,
        "phone_code": phone_code4,
        "email_code": email_code4,
    })
    if status4 in (400, 409):
        ok(f"用户名重复注册被拦截  →  HTTP {status4}")
    else:
        fail(f"用户名重复注册未拦截  →  HTTP {status4}")

    # 6. 错误手机验证码 → 期望 400
    suffix5  = rand_suffix()
    phone5   = f"154{suffix5}0004"[:11]
    email5   = f"err_{suffix5}@example.com"
    _, email_code5 = send_email_code(email5, "register")
    status5, body5 = post("/api/auth/register", {
        "username":   f"err_{suffix5}",
        "phone":      phone5,
        "email":      email5,
        "password":   passwd,
        "phone_code": "000000",     # 错误验证码
        "email_code": email_code5,
    })
    assert_status("错误手机验证码被拦截（期望 400）", status5, 400, body5)

    # 7. 错误邮箱验证码 → 期望 400
    suffix6  = rand_suffix()
    phone6   = f"155{suffix6}0005"[:11]
    email6   = f"err6_{suffix6}@example.com"
    _, phone_code6 = send_phone_code(phone6, "register")
    status6, body6 = post("/api/auth/register", {
        "username":   f"err6_{suffix6}",
        "phone":      phone6,
        "email":      email6,
        "password":   passwd,
        "phone_code": phone_code6,
        "email_code": "000000",     # 错误验证码
    })
    assert_status("错误邮箱验证码被拦截（期望 400）", status6, 400, body6)

    # 8. 用户名边界：1位（太短）→ 期望 400
    suffix7  = rand_suffix()
    phone7   = f"156{suffix7}0006"[:11]
    email7   = f"short_{suffix7}@example.com"
    _, phone_code7 = send_phone_code(phone7, "register")
    _, email_code7 = send_email_code(email7, "register")
    status7, body7 = post("/api/auth/register", {
        "username":   "x",          # 仅1位，低于2位最小值
        "phone":      phone7,
        "email":      email7,
        "password":   passwd,
        "phone_code": phone_code7,
        "email_code": email_code7,
    })
    assert_status("用户名过短（1位）被拦截（期望 400）", status7, 400, body7)

    # 9. 用户名边界：33位（太长）→ 期望 400
    suffix8  = rand_suffix()
    phone8   = f"157{suffix8}0007"[:11]
    email8   = f"long_{suffix8}@example.com"
    _, phone_code8 = send_phone_code(phone8, "register")
    _, email_code8 = send_email_code(email8, "register")
    long_name = "a" * 33
    status8, body8 = post("/api/auth/register", {
        "username":   long_name,    # 33位，超过32位最大值
        "phone":      phone8,
        "email":      email8,
        "password":   passwd,
        "phone_code": phone_code8,
        "email_code": email_code8,
    })
    assert_status("用户名过长（33位）被拦截（期望 400）", status8, 400, body8)

    # 10. 用户名边界：非法字符（含空格）→ 期望 400
    suffix9  = rand_suffix()
    phone9   = f"158{suffix9}0008"[:11]
    email9   = f"inv_{suffix9}@example.com"
    _, phone_code9 = send_phone_code(phone9, "register")
    _, email_code9 = send_email_code(email9, "register")
    status9, body9 = post("/api/auth/register", {
        "username":   "bad name!",  # 含空格和感叹号
        "phone":      phone9,
        "email":      email9,
        "password":   passwd,
        "phone_code": phone_code9,
        "email_code": email_code9,
    })
    assert_status("用户名含非法字符被拦截（期望 400）", status9, 400, body9)

    return token, email, phone, passwd, uname


# ════════════════════════════════════════════════════════
# N-02  OTP密码重置（无需旧密码，重置后所有会话失效）
# ════════════════════════════════════════════════════════
def test_n02(reg_email, reg_phone, old_password):
    section("N-02  OTP密码重置（POST /api/auth/password/reset）")
    new_passwd = "NewPass@5678"

    # 1. 通过手机 OTP 重置密码（happy path）
    status_code, phone_code = send_phone_code(reg_phone, "reset_password")
    assert_status("发送重置密码手机验证码", status_code, 200,
                  {"message": "ok"} if status_code == 200 else {})

    status, body = post("/api/auth/password/reset", {
        "target":       reg_phone,
        "target_type":  "phone",
        "code":         phone_code,
        "new_password": new_passwd,
    })
    assert_status("手机 OTP 重置密码（正常流程）", status, 200, body)

    # 2. 旧密码登录应失效（所有会话应失效）
    token_old, _ = login_email(reg_email, old_password)
    if not token_old:
        ok("旧密码登录已失效（无法获取 token，符合预期）")
    else:
        # 旧密码还能登录说明重置未生效
        fail("旧密码登录未失效（期望无法登录）")

    # 3. 新密码登录成功
    token_new, refresh_new = login_email(reg_email, new_passwd)
    if token_new:
        ok("新密码登录成功")
    else:
        fail("新密码登录失败")

    # 4. 通过邮箱 OTP 重置密码（happy path）
    passwd_v2 = "PassV2@9999"
    status_code2, email_code = send_email_code(reg_email, "reset_password")
    assert_status("发送重置密码邮箱验证码", status_code2, 200,
                  {"message": "ok"} if status_code2 == 200 else {})

    status2, body2 = post("/api/auth/password/reset", {
        "target":       reg_email,
        "target_type":  "email",
        "code":         email_code,
        "new_password": passwd_v2,
    })
    assert_status("邮箱 OTP 重置密码（正常流程）", status2, 200, body2)

    # 5. 错误验证码 → 期望 400
    status_code3, _ = send_phone_code(reg_phone, "reset_password")
    status3, body3 = post("/api/auth/password/reset", {
        "target":       reg_phone,
        "target_type":  "phone",
        "code":         "000000",   # 错误验证码
        "new_password": "Bypass@000",
    })
    assert_status("错误验证码重置密码被拦截（期望 400）", status3, 400, body3)

    # 6. 不存在的手机号 → 期望 400/404
    status4, body4 = post("/api/auth/password/reset", {
        "target":       "10000000000",
        "target_type":  "phone",
        "code":         "123456",
        "new_password": "Bypass@000",
    })
    if status4 in (400, 404):
        ok(f"不存在手机号重置被拦截  →  HTTP {status4}")
    else:
        fail(f"不存在手机号未被拦截  →  HTTP {status4}")

    # 7. target_type 非法值 → 期望 400
    status5, body5 = post("/api/auth/password/reset", {
        "target":       reg_phone,
        "target_type":  "wechat",   # 非法类型
        "code":         "123456",
        "new_password": "Bypass@000",
    })
    assert_status("非法 target_type 被拦截（期望 400）", status5, 400, body5)

    return passwd_v2


# ════════════════════════════════════════════════════════
# N-03 / N-04  管理员双重认证
# ════════════════════════════════════════════════════════
def test_n03_n04(admin_token, admin_phone, admin_email):
    section("N-03/N-04  管理员双重认证（手机 + 邮箱）")

    if not admin_token:
        info("跳过 N-03/N-04：未提供管理员 Token")
        return

    # ── N-03  手机双重认证 ──────────────────────────────
    # 1. 发管理员手机验证码
    status_code, phone_code = send_phone_code(admin_phone, "admin_verify")
    assert_status("发送管理员手机验证码", status_code, 200,
                  {"message": "ok"} if status_code == 200 else {})

    # 2. 正常手机认证（happy path）
    status, body = post("/api/admin/auth/verify-phone",
                        {"code": phone_code},
                        token=admin_token)
    assert_status("N-03 管理员手机认证（正常流程）", status, 200, body)

    # 3. 错误验证码 → 期望 400
    _, _ = send_phone_code(admin_phone, "admin_verify")
    status2, body2 = post("/api/admin/auth/verify-phone",
                          {"code": "000000"},
                          token=admin_token)
    assert_status("N-03 错误手机验证码被拦截（期望 400）", status2, 400, body2)

    # 4. 无 Token → 期望 401
    status3, body3 = post("/api/admin/auth/verify-phone", {"code": phone_code})
    assert_status("N-03 无 Token 访问被拦截（期望 401）", status3, 401, body3)

    # ── N-04  邮箱双重认证（需先完成手机认证）──────────
    # 5. 发管理员邮箱验证码
    status_code4, email_code = send_email_code(admin_email, "admin_verify")
    assert_status("发送管理员邮箱验证码", status_code4, 200,
                  {"message": "ok"} if status_code4 == 200 else {})

    # 6. 正常邮箱认证（happy path）
    status4, body4 = post("/api/admin/auth/verify-email",
                          {"code": email_code},
                          token=admin_token)
    assert_status("N-04 管理员邮箱认证（正常流程）", status4, 200, body4)

    # 7. 错误邮箱验证码 → 期望 400
    _, _ = send_email_code(admin_email, "admin_verify")
    status5, body5 = post("/api/admin/auth/verify-email",
                          {"code": "000000"},
                          token=admin_token)
    assert_status("N-04 错误邮箱验证码被拦截（期望 400）", status5, 400, body5)

    # 8. 无 Token → 期望 401
    status6, body6 = post("/api/admin/auth/verify-email", {"code": email_code})
    assert_status("N-04 无 Token 访问被拦截（期望 401）", status6, 401, body6)


# ════════════════════════════════════════════════════════
# N-05  修改用户名（PATCH /api/me/username）
# ════════════════════════════════════════════════════════
def test_n05(token):
    section("N-05  修改用户名（PATCH /api/me/username）")

    if not token:
        info("跳过 N-05：未提供用户 Token")
        return

    suffix = rand_suffix()
    new_name = f"newname_{suffix}"

    # 1. 正常修改用户名（happy path）
    status, body = patch("/api/me/username", {"username": new_name}, token=token)
    assert_status("修改用户名（正常流程）", status, 200, body)

    # 2. GET /api/me 验证新用户名已生效
    status2, body2 = get("/api/me", token=token)
    if status2 == 200:
        actual = get_data(body2).get("username", "")
        assert_field("修改后用户名已生效", actual, new_name)
    else:
        fail("验证用户名修改：GET /api/me 失败", f"HTTP {status2}")

    # 3. 用户名过短（1位）→ 期望 400
    status3, body3 = patch("/api/me/username", {"username": "x"}, token=token)
    assert_status("用户名过短（1位）被拦截（期望 400）", status3, 400, body3)

    # 4. 用户名过长（33位）→ 期望 400
    status4, body4 = patch("/api/me/username",
                           {"username": "a" * 33}, token=token)
    assert_status("用户名过长（33位）被拦截（期望 400）", status4, 400, body4)

    # 5. 用户名含非法字符 → 期望 400
    status5, body5 = patch("/api/me/username",
                           {"username": "bad name!"}, token=token)
    assert_status("用户名含非法字符被拦截（期望 400）", status5, 400, body5)

    # 6. 无 Token → 期望 401
    status6, body6 = patch("/api/me/username", {"username": new_name})
    assert_status("无 Token 修改用户名被拦截（期望 401）", status6, 401, body6)

    return new_name


# ════════════════════════════════════════════════════════
# N-06  修改手机号（PATCH /api/me/phone）
# ════════════════════════════════════════════════════════
def test_n06(token):
    section("N-06  修改手机号（PATCH /api/me/phone）")

    if not token:
        info("跳过 N-06：未提供用户 Token")
        return

    suffix      = rand_suffix(8)
    new_phone   = f"159{suffix}"[:11]

    # 1. 发绑定手机验证码
    _, phone_code = send_phone_code(new_phone, "bind_phone")

    # 2. 正常修改手机号（happy path）
    status, body = patch("/api/me/phone",
                         {"phone": new_phone, "code": phone_code},
                         token=token)
    assert_status("修改手机号（正常流程）", status, 200, body)

    # 3. GET /api/me 验证手机号脱敏格式
    status2, body2 = get("/api/me", token=token)
    if status2 == 200:
        masked_phone = get_data(body2).get("phone", "")
        info(f"脱敏手机号: {masked_phone}")
        # 脱敏格式：前3后4，中间*（如 159****0001）
        if masked_phone and "*" in masked_phone:
            ok("手机号脱敏格式正确（含 *）")
        else:
            fail("手机号脱敏格式异常", f"实际值: {masked_phone!r}")
    else:
        fail("验证手机号修改：GET /api/me 失败", f"HTTP {status2}")

    # 4. 错误验证码 → 期望 400
    suffix2   = rand_suffix(8)
    new_phone2 = f"180{suffix2}"[:11]
    _, _ = send_phone_code(new_phone2, "bind_phone")
    status3, body3 = patch("/api/me/phone",
                           {"phone": new_phone2, "code": "000000"},
                           token=token)
    assert_status("错误验证码修改手机号被拦截（期望 400）", status3, 400, body3)

    # 5. 无 Token → 期望 401
    status4, body4 = patch("/api/me/phone",
                           {"phone": new_phone2, "code": phone_code})
    assert_status("无 Token 修改手机号被拦截（期望 401）", status4, 401, body4)

    return new_phone


# ════════════════════════════════════════════════════════
# N-07  修改邮箱（PATCH /api/me/email）
# ════════════════════════════════════════════════════════
def test_n07(token):
    section("N-07  修改邮箱（PATCH /api/me/email）")

    if not token:
        info("跳过 N-07：未提供用户 Token")
        return

    suffix    = rand_suffix()
    new_email = f"updated_{suffix}@example.com"

    # 1. 发绑定邮箱验证码
    _, email_code = send_email_code(new_email, "bind_email")

    # 2. 正常修改邮箱（happy path）
    status, body = patch("/api/me/email",
                         {"email": new_email, "code": email_code},
                         token=token)
    assert_status("修改邮箱（正常流程）", status, 200, body)

    # 3. GET /api/me 验证邮箱脱敏格式
    status2, body2 = get("/api/me", token=token)
    if status2 == 200:
        masked_email = get_data(body2).get("email", "")
        info(f"脱敏邮箱: {masked_email}")
        # 脱敏格式：@前保留2位+***（如 up***@example.com）
        if masked_email and "*" in masked_email and "@" in masked_email:
            ok("邮箱脱敏格式正确（含 *）")
        else:
            fail("邮箱脱敏格式异常", f"实际值: {masked_email!r}")
    else:
        fail("验证邮箱修改：GET /api/me 失败", f"HTTP {status2}")

    # 4. 错误验证码 → 期望 400
    suffix3    = rand_suffix()
    new_email3 = f"err_{suffix3}@example.com"
    _, _ = send_email_code(new_email3, "bind_email")
    status3, body3 = patch("/api/me/email",
                           {"email": new_email3, "code": "000000"},
                           token=token)
    assert_status("错误验证码修改邮箱被拦截（期望 400）", status3, 400, body3)

    # 5. 无 Token → 期望 401
    status4, body4 = patch("/api/me/email",
                           {"email": new_email3, "code": email_code})
    assert_status("无 Token 修改邮箱被拦截（期望 401）", status4, 401, body4)

    return new_email


# ════════════════════════════════════════════════════════
# N-08  GET /api/me 增强字段验证
# ════════════════════════════════════════════════════════
def test_n08(token):
    section("N-08  GET /api/me 增强字段验证")

    if not token:
        info("跳过 N-08：未提供用户 Token")
        return

    status, body = get("/api/me", token=token)
    assert_status("GET /api/me（期望 200）", status, 200, body)

    d = get_data(body)

    # 必须存在的基础字段
    assert_field_present("字段 id",         d.get("id"))
    assert_field_present("字段 status",     d.get("status"))
    assert_field_present("字段 created_at", d.get("created_at"))

    # 增强字段（可为空/false，但必须存在于响应体）
    enhanced_fields = [
        "username",
        "phone",
        "email",
        "email_verified",
        "phone_verified",
        "real_name_status",
        "admin_phone_verified",
        "admin_email_verified",
        "last_login_at",
    ]
    for field in enhanced_fields:
        if field in d:
            ok(f"增强字段 '{field}' 存在  →  {d[field]!r}")
        else:
            fail(f"增强字段 '{field}' 缺失")

    # 脱敏校验：phone 和 email 如果非空，必须含有 *
    phone = d.get("phone", "")
    email = d.get("email", "")
    if phone and "*" not in phone:
        fail("手机号未脱敏（期望含 *）", f"实际: {phone!r}")
    elif phone:
        ok(f"手机号已脱敏  →  {phone!r}")

    if email and "*" not in email:
        fail("邮箱未脱敏（期望含 *）", f"实际: {email!r}")
    elif email:
        ok(f"邮箱已脱敏  →  {email!r}")

    # real_name_status 枚举值校验
    rns = d.get("real_name_status", "")
    valid_rns = {"unverified", "pending", "approved", "rejected"}
    if rns in valid_rns:
        ok(f"real_name_status 枚举值合法  →  {rns!r}")
    elif rns:
        fail(f"real_name_status 枚举值非法  →  {rns!r}")

    # 无 Token → 期望 401
    status2, body2 = get("/api/me")
    assert_status("GET /api/me 无 Token 被拦截（期望 401）", status2, 401, body2)


# ════════════════════════════════════════════════════════
# N-09  邮箱注册兼容性（POST /api/auth/register/email，带 username）
# ════════════════════════════════════════════════════════
def test_n09():
    section("N-09  邮箱注册兼容性（带 username 字段）")
    suffix = rand_suffix()
    email  = f"compat_{suffix}@example.com"
    uname  = f"compat_{suffix}"
    passwd = "Test@1234"

    # 1. 发验证码
    _, email_code = send_email_code(email, "register")

    # 2. 邮箱注册（带 username）
    status, body = post("/api/auth/register/email", {
        "email":    email,
        "password": passwd,
        "code":     email_code,
        "username": uname,
    })
    assert_status("邮箱注册（带 username，正常流程）", status, 201, body)
    token = get_data(body).get("access_token", "")

    # 3. GET /api/me 验证 username 已保存
    if token:
        status2, body2 = get("/api/me", token=token)
        if status2 == 200:
            actual = get_data(body2).get("username", "")
            assert_field("邮箱注册后 username 已保存", actual, uname)
        else:
            fail("验证 username：GET /api/me 失败", f"HTTP {status2}")

    # 4. 邮箱注册（不带 username，兼容旧行为）
    suffix2  = rand_suffix()
    email2   = f"noname_{suffix2}@example.com"
    _, email_code2 = send_email_code(email2, "register")
    status3, body3 = post("/api/auth/register/email", {
        "email":    email2,
        "password": passwd,
        "code":     email_code2,
        # 不携带 username
    })
    assert_status("邮箱注册（不带 username，兼容旧行为）", status3, 201, body3)


# ════════════════════════════════════════════════════════
# N-10  手机注册兼容性（POST /api/auth/register/phone，带 username）
# ════════════════════════════════════════════════════════
def test_n10():
    section("N-10  手机注册兼容性（带 username 字段）")
    suffix = rand_suffix(8)
    phone  = f"160{suffix}"[:11]
    uname  = f"phuser_{suffix[:6]}"
    passwd = "Test@1234"

    # 1. 发验证码
    _, phone_code = send_phone_code(phone, "register")

    # 2. 手机注册（带 username）
    status, body = post("/api/auth/register/phone", {
        "phone":    phone,
        "password": passwd,
        "code":     phone_code,
        "username": uname,
    })
    assert_status("手机注册（带 username，正常流程）", status, 201, body)
    token = get_data(body).get("access_token", "")

    # 3. GET /api/me 验证 username 已保存
    if token:
        status2, body2 = get("/api/me", token=token)
        if status2 == 200:
            actual = get_data(body2).get("username", "")
            assert_field("手机注册后 username 已保存", actual, uname)
        else:
            fail("验证 username：GET /api/me 失败", f"HTTP {status2}")

    # 4. 手机注册（不带 username，兼容旧行为）
    suffix2  = rand_suffix(8)
    phone2   = f"161{suffix2}"[:11]
    _, phone_code2 = send_phone_code(phone2, "register")
    status3, body3 = post("/api/auth/register/phone", {
        "phone":    phone2,
        "password": passwd,
        "code":     phone_code2,
        # 不携带 username
    })
    assert_status("手机注册（不带 username，兼容旧行为）", status3, 201, body3)


# ════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  Molin 用户资料与新版认证接口测试{RESET}")
    print(f"{BOLD}  测试范围：N-01 ~ N-10{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}")

    # ── N-01  统一注册 ────────────────────────────────
    token_n01, reg_email, reg_phone, reg_passwd, reg_uname = test_n01()

    # ── N-02  OTP密码重置 ─────────────────────────────
    # 重置后返回最新密码，后续步骤用新密码登录
    latest_passwd = test_n02(reg_email, reg_phone, reg_passwd)

    # ── 用最新密码重新登录获取 token ──────────────────
    section("前置准备：用最新密码重新登录")
    current_token = ""
    if latest_passwd:
        current_token, _ = login_email(reg_email, latest_passwd)
        if current_token:
            ok(f"重新登录成功，用于 N-05~N-08 测试")
        else:
            fail("重新登录失败，N-05~N-08 将跳过")
    else:
        info("N-02 未返回最新密码，尝试使用 N-01 初始 token")
        current_token = token_n01

    # ── N-03 / N-04  管理员双重认证 ───────────────────
    # 说明：管理员账号、手机、邮箱需在测试服务器已存在
    #       此处用环境变量注入，避免硬编码敏感信息
    admin_token = os.getenv("ADMIN_TOKEN", "")
    admin_phone = os.getenv("ADMIN_PHONE", "")
    admin_email = os.getenv("ADMIN_EMAIL", "")
    if not (admin_token and admin_phone and admin_email):
        section("N-03/N-04  管理员双重认证（跳过）")
        info("未设置 ADMIN_TOKEN / ADMIN_PHONE / ADMIN_EMAIL 环境变量，跳过管理员认证测试")
        info("如需运行：ADMIN_TOKEN=... ADMIN_PHONE=... ADMIN_EMAIL=... python3 ...")
    else:
        test_n03_n04(admin_token, admin_phone, admin_email)

    # ── N-05  修改用户名 ──────────────────────────────
    test_n05(current_token)

    # ── N-06  修改手机号 ──────────────────────────────
    test_n06(current_token)

    # ── N-07  修改邮箱 ────────────────────────────────
    test_n07(current_token)

    # ── N-08  GET /api/me 增强字段 ────────────────────
    # 用修改后的 token 验证，字段应全部存在
    test_n08(current_token)

    # ── N-09  邮箱注册兼容性 ──────────────────────────
    test_n09()

    # ── N-10  手机注册兼容性 ──────────────────────────
    test_n10()

    # ── 汇总 ──────────────────────────────────────────
    total = passed + failed
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  测试结果：{passed}/{total} 通过{RESET}")
    if failed == 0:
        print(f"{BOLD}{GREEN}  结论：全部通过{RESET}")
    else:
        print(f"{BOLD}{RED}  结论：{failed} 项失败，请检查上方输出{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}\n")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
BUG-001 修复验证脚本
验证内容：
  1. 邮箱验证码 target_value 不为空
  2. 手机验证码 target_value 不为空
  3. 完整注册流程（双OTP + 注册 + 返回 token pair）
  4. 密码重置流程（邮箱OTP + 重置密码）
  5. 管理员双重认证（手机OTP + verify-phone）

用法：
  在测试服务器上执行：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_bug001_fix.py
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# 颜色输出
GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed = 0
failed = 0
results = []  # 保存每个测试结果供最终汇总

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
    data = json.dumps(body).encode() if body else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL 查询工具 ────────────────────────────────────────────

def db_query(sql):
    """通过 mysql 命令行执行查询，返回行列表（每行为字符串列表）。"""
    import subprocess
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


# ── 测试数据（使用时间戳避免冲突）────────────────────────────

TS = str(int(time.time()))[-6:]  # 取最后6位时间戳

TEST_EMAIL = f"bug001test{TS}@testmail.io"
TEST_PHONE = f"1380000{TS}"
TEST_PASSWORD = "Test@Bug001"

RESET_EMAIL = f"resettest{TS}@testmail.io"
RESET_PHONE = f"1381111{TS}"
RESET_PASSWORD_NEW = "Reset@NewPass1"

ADMIN_EMAIL = "admintest_1781147075@example.com"
ADMIN_PASSWORD = "Admin@Test123!"

print(f"\n{BOLD}{CYAN}BUG-001 修复验证 — 开始{RESET}")
print(f"  API_BASE  : {API_BASE}")
print(f"  MYSQL     : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  测试账号  : email={TEST_EMAIL}, phone={TEST_PHONE}")
print()


# ═══════════════════════════════════════════════════════════════
# T-01  邮箱验证码 target_value 正确性（P0）
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}T-01  邮箱验证码 target_value 正确性（P0）{RESET}")

status, resp = http("POST", "/api/auth/verification-codes/email", {
    "email": TEST_EMAIL,
    "scene": "register"
})

print(f"       响应 HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")

if status != 200 or resp.get("code") != 0:
    fail("POST /api/auth/verification-codes/email 应返回 200/code=0",
         f"实际 HTTP={status}, code={resp.get('code')}, message={resp.get('message')}")
    email_code = None
else:
    email_code = resp.get("data", {}).get("code")

    # 检查数据库 target_value
    rows, err = db_query(
        f"SELECT target_value FROM verification_codes "
        f"WHERE target_type='email' AND scene='register' "
        f"ORDER BY id DESC LIMIT 1"
    )
    if err:
        fail("查询 verification_codes 失败", err)
    elif not rows:
        fail("DB 中未找到验证码记录")
    else:
        target_value = rows[0][0]
        if target_value == "":
            fail("target_value 为空字符串（BUG-001 未修复）",
                 f"DB target_value='{target_value}'，期望='{TEST_EMAIL}'")
        elif target_value != TEST_EMAIL.lower():
            fail(f"target_value 与预期不符",
                 f"DB='{target_value}'，期望='{TEST_EMAIL.lower()}'")
        else:
            ok("DB target_value 正确", f"target_value='{target_value}'")


# ═══════════════════════════════════════════════════════════════
# T-02  手机验证码 target_value 正确性（P0）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}T-02  手机验证码 target_value 正确性（P0）{RESET}")

status, resp = http("POST", "/api/auth/verification-codes/phone", {
    "phone": TEST_PHONE,
    "scene": "register"
})

print(f"       响应 HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")

if status != 200 or resp.get("code") != 0:
    fail("POST /api/auth/verification-codes/phone 应返回 200/code=0",
         f"实际 HTTP={status}, code={resp.get('code')}, message={resp.get('message')}")
    phone_code = None
else:
    phone_code = resp.get("data", {}).get("code")

    rows, err = db_query(
        f"SELECT target_value FROM verification_codes "
        f"WHERE target_type='phone' AND scene='register' "
        f"ORDER BY id DESC LIMIT 1"
    )
    if err:
        fail("查询 verification_codes 失败", err)
    elif not rows:
        fail("DB 中未找到手机验证码记录")
    else:
        target_value = rows[0][0]
        if target_value == "":
            fail("手机 target_value 为空字符串（BUG-001 未修复）",
                 f"DB target_value='{target_value}'，期望='{TEST_PHONE}'")
        elif target_value != TEST_PHONE:
            fail(f"手机 target_value 与预期不符",
                 f"DB='{target_value}'，期望='{TEST_PHONE}'")
        else:
            ok("DB 手机 target_value 正确", f"target_value='{target_value}'")


# ═══════════════════════════════════════════════════════════════
# T-03  完整注册流程（P0）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}T-03  完整注册流程（P0）{RESET}")

if email_code is None or phone_code is None:
    fail("跳过注册测试（前置验证码获取失败）")
    register_token = None
else:
    print(f"       email_code={email_code}, phone_code={phone_code}")
    status, resp = http("POST", "/api/auth/register", {
        "phone":      TEST_PHONE,
        "email":      TEST_EMAIL,
        "password":   TEST_PASSWORD,
        "phone_code": phone_code,
        "email_code": email_code,
    })
    print(f"       响应 HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")

    if status != 201 or resp.get("code") != 0:
        fail("POST /api/auth/register 应返回 201/code=0",
             f"实际 HTTP={status}, code={resp.get('code')}, message={resp.get('message')}")
        register_token = None
    else:
        data = resp.get("data", {})
        access_token = data.get("access_token", "")
        refresh_token = data.get("refresh_token", "")
        if access_token and refresh_token:
            ok("注册成功，返回 token pair",
               f"access_token 前20位='{access_token[:20]}...'")
            register_token = access_token
        else:
            fail("注册成功但 token pair 缺失",
                 f"data={json.dumps(data, ensure_ascii=False)}")
            register_token = None

    # 验证 phone_verified 和 email_verified 均为 true
    if register_token:
        status2, resp2 = http("GET", "/api/me", token=register_token)
        print(f"       GET /api/me HTTP {status2}: {json.dumps(resp2, ensure_ascii=False)}")
        user_data = resp2.get("data", {})
        if status2 == 200 and resp2.get("code") == 0:
            phone_verified = user_data.get("phone_verified", False)
            email_verified = user_data.get("email_verified", False)
            if phone_verified and email_verified:
                ok("注册后 phone_verified=true, email_verified=true")
            else:
                fail(f"注册后 verified 标志未正确置为 true",
                     f"phone_verified={phone_verified}, email_verified={email_verified}")
        else:
            fail("GET /api/me 失败", f"HTTP={status2}, code={resp2.get('code')}")


# ═══════════════════════════════════════════════════════════════
# T-04  密码重置流程（P0）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}T-04  密码重置流程（P0）{RESET}")

# 先注册一个用于重置测试的账号
# 获取手机和邮箱验证码
status_e, resp_e = http("POST", "/api/auth/verification-codes/email", {
    "email": RESET_EMAIL, "scene": "register"
})
status_p, resp_p = http("POST", "/api/auth/verification-codes/phone", {
    "phone": RESET_PHONE, "scene": "register"
})

rc_email = resp_e.get("data", {}).get("code") if status_e == 200 else None
rc_phone = resp_p.get("data", {}).get("code") if status_p == 200 else None

if rc_email and rc_phone:
    status_r, resp_r = http("POST", "/api/auth/register", {
        "phone": RESET_PHONE, "email": RESET_EMAIL,
        "password": TEST_PASSWORD,
        "phone_code": rc_phone, "email_code": rc_email,
    })
    reset_registered = (status_r == 201 and resp_r.get("code") == 0)
    print(f"       重置测试账号注册: HTTP {status_r}, code={resp_r.get('code')}")
else:
    reset_registered = False
    print(f"       重置测试账号验证码获取失败: email_code={rc_email}, phone_code={rc_phone}")

if not reset_registered:
    fail("跳过密码重置流程（前置注册失败）")
else:
    # 发 reset_password 场景邮箱验证码
    status_rc, resp_rc = http("POST", "/api/auth/verification-codes/email", {
        "email": RESET_EMAIL, "scene": "reset_password"
    })
    print(f"       发重置验证码 HTTP {status_rc}: {json.dumps(resp_rc, ensure_ascii=False)}")

    reset_code = resp_rc.get("data", {}).get("code") if status_rc == 200 else None

    if not reset_code:
        fail("获取 reset_password 邮箱验证码失败",
             f"HTTP={status_rc}, code={resp_rc.get('code')}")
    else:
        # 验证数据库 target_value（reset_password 场景）
        rows_r, err_r = db_query(
            f"SELECT target_value FROM verification_codes "
            f"WHERE target_type='email' AND scene='reset_password' "
            f"ORDER BY id DESC LIMIT 1"
        )
        if err_r or not rows_r:
            fail("查询 reset_password 验证码 DB 记录失败", err_r or "无记录")
        else:
            tv = rows_r[0][0]
            if tv == "":
                fail("reset_password 场景 target_value 为空（BUG-001 未修复）", f"DB='{tv}'")
            elif tv != RESET_EMAIL.lower():
                fail("reset_password target_value 不符", f"DB='{tv}'，期望='{RESET_EMAIL.lower()}'")
            else:
                ok("reset_password 场景 target_value 正确", f"DB='{tv}'")

        # 调用密码重置接口
        status_rp, resp_rp = http("POST", "/api/auth/password/reset", {
            "target":       RESET_EMAIL,
            "target_type":  "email",
            "code":         reset_code,
            "new_password": RESET_PASSWORD_NEW,
        })
        print(f"       密码重置 HTTP {status_rp}: {json.dumps(resp_rp, ensure_ascii=False)}")

        if status_rp == 200 and resp_rp.get("code") == 0:
            ok("密码重置成功（返回 200/code=0）")
            # 验证新密码可以登录
            status_l, resp_l = http("POST", "/api/auth/login/email", {
                "email": RESET_EMAIL, "password": RESET_PASSWORD_NEW
            })
            if status_l == 200 and resp_l.get("code") == 0:
                ok("新密码登录成功")
            else:
                fail("新密码登录失败", f"HTTP={status_l}, code={resp_l.get('code')}")
        else:
            fail("密码重置失败",
                 f"HTTP={status_rp}, code={resp_rp.get('code')}, message={resp_rp.get('message')}")


# ═══════════════════════════════════════════════════════════════
# T-05  管理员双重认证（P1）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}T-05  管理员双重认证（P1）{RESET}")

# 先获取管理员 token
status_al, resp_al = http("POST", "/api/auth/login/email", {
    "email": ADMIN_EMAIL, "password": ADMIN_PASSWORD
})
print(f"       管理员登录 HTTP {status_al}: code={resp_al.get('code')}, "
      f"message={resp_al.get('message')}")

if status_al != 200 or resp_al.get("code") != 0:
    fail("管理员登录失败，跳过双重认证测试",
         f"HTTP={status_al}, message={resp_al.get('message')}")
else:
    admin_token = resp_al.get("data", {}).get("access_token", "")
    admin_phone_raw = resp_al.get("data", {}).get("user", {}).get("phone", "")

    # 查管理员实际手机号（DB 中未脱敏）
    rows_ap, err_ap = db_query(
        f"SELECT phone FROM users WHERE email='{ADMIN_EMAIL}' LIMIT 1"
    )
    if err_ap or not rows_ap or not rows_ap[0][0]:
        fail("获取管理员手机号失败", err_ap or "无记录或 phone 为 NULL")
    else:
        admin_phone = rows_ap[0][0]
        print(f"       管理员手机号（DB）: {admin_phone}")

        # 发 admin_verify 手机验证码
        status_av, resp_av = http("POST", "/api/auth/verification-codes/phone", {
            "phone": admin_phone, "scene": "admin_verify"
        })
        print(f"       发管理员手机验证码 HTTP {status_av}: {json.dumps(resp_av, ensure_ascii=False)}")

        admin_otp = resp_av.get("data", {}).get("code") if status_av == 200 else None

        if not admin_otp:
            fail("获取管理员手机验证码失败",
                 f"HTTP={status_av}, code={resp_av.get('code')}")
        else:
            # 验证 DB target_value（admin_verify 场景）
            rows_adb, err_adb = db_query(
                f"SELECT target_value FROM verification_codes "
                f"WHERE target_type='phone' AND scene='admin_verify' "
                f"ORDER BY id DESC LIMIT 1"
            )
            if err_adb or not rows_adb:
                fail("查询 admin_verify 验证码 DB 记录失败", err_adb or "无记录")
            else:
                tv_a = rows_adb[0][0]
                if tv_a == "":
                    fail("admin_verify 场景 target_value 为空（BUG-001 未修复）", f"DB='{tv_a}'")
                elif tv_a != admin_phone:
                    fail("admin_verify target_value 不符", f"DB='{tv_a}'，期望='{admin_phone}'")
                else:
                    ok("admin_verify 场景 target_value 正确", f"DB='{tv_a}'")

            # 调用管理员手机认证接口
            status_vp, resp_vp = http("POST", "/api/admin/auth/verify-phone", {
                "code": admin_otp
            }, token=admin_token)
            print(f"       verify-phone HTTP {status_vp}: {json.dumps(resp_vp, ensure_ascii=False)}")

            if status_vp == 200 and resp_vp.get("code") == 0:
                ok("管理员手机双重认证成功（返回 200/code=0）")
            else:
                fail("管理员手机双重认证失败",
                     f"HTTP={status_vp}, code={resp_vp.get('code')}, message={resp_vp.get('message')}")


# ═══════════════════════════════════════════════════════════════
# 汇总
# ═══════════════════════════════════════════════════════════════
print(f"\n{'═'*60}")
print(f"{BOLD}BUG-001 修复验证 — 测试汇总{RESET}")
print(f"  总计: {passed + failed}  通过: {GREEN}{passed}{RESET}  失败: {RED}{failed}{RESET}")
print()
print(f"  {'测试项':<50} {'结果'}")
print(f"  {'-'*56}")
for status_r, label, detail in results:
    status_str = f"{GREEN}PASS{RESET}" if status_r == "PASS" else f"{RED}FAIL{RESET}"
    print(f"  {label:<50} {status_str}")

print()
if failed == 0:
    print(f"{BOLD}{GREEN}结论：BUG-001 已修复，全部测试项通过。{RESET}")
else:
    print(f"{BOLD}{RED}结论：BUG-001 未完全修复，仍有 {failed} 项失败。{RESET}")

sys.exit(0 if failed == 0 else 1)

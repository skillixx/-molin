#!/usr/bin/env python3
"""
PR#25 复测脚本：登录接口对未注册账号返回 404/40404（修复 BUG-PR20-E）

背景：
  PR#24 测试报告中发现 BUG-PR20-E（P2）：
    - POST /api/auth/login/phone 对未注册手机号返回 401/40001，应为 404/40404
    - POST /api/auth/login/email（同根因，未单独测过）

  PR#25 修复 auth_service.go 中 LoginEmail/LoginPhone，FindByEmail/FindByPhone
  查询失败时分别改为返回 ErrEmailNotRegistered/ErrPhoneNotRegistered（映射为 404/40404）。

测试范围：
  1. BUG-PR20-E 主用例：未注册手机号 POST /api/auth/login/phone（任意 code）→ 期望 404/40404，message 含"未注册"
  2. 同根因用例：未注册邮箱 POST /api/auth/login/email（任意密码）→ 期望 404/40404，message 含"未注册"
  3. 回归用例：
     - 已注册手机号 + 正确验证码登录 → 200，返回 token pair
     - 退出登录后，旧 access_token 调用 /api/me → 401/40001（PR#22 吊销逻辑仍正常）
     - 错误验证码登录已注册手机号 → 仍为 400/40000（未被本次改动误改为 404）

用法：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/test_pr25_login_not_registered.py
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE = os.getenv("API_BASE", "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB = os.getenv("MYSQL_DATABASE", "molin")

# ── 颜色输出 ──────────────────────────────────────────────
GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
CYAN = "\033[96m"
BOLD = "\033[1m"
RESET = "\033[0m"

passed = failed = 0
bugs = []


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
def request(method, path, body=None, token=None, base=API_BASE):
    url = base + path
    data = json.dumps(body).encode() if body is not None else b""
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=10)
        raw = resp.read()
        try:
            return resp.status, json.loads(raw)
        except Exception:
            return resp.status, {"raw": raw.decode(errors="ignore")}
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}


def post(path, body=None, token=None, base=API_BASE):
    return request("POST", path, body, token, base=base)


def get(path, token=None, base=API_BASE):
    return request("GET", path, token=token, base=base)


def assert_status(label, status, expected, body):
    if status == expected:
        ok(f"{label}  →  HTTP {status}")
    else:
        msg = body.get("message", "") if isinstance(body, dict) else ""
        fail(f"{label}  →  HTTP {status}（期望 {expected}）", msg)
    return status == expected


def assert_code(label, status, expected_status, body, expected_code):
    actual_code = body.get("code") if isinstance(body, dict) else None
    if status == expected_status and actual_code == expected_code:
        ok(f"{label}  →  HTTP {status} code={actual_code}")
        return True
    else:
        msg = body.get("message", "") if isinstance(body, dict) else ""
        fail(f"{label}  →  HTTP {status} code={actual_code}（期望 HTTP {expected_status} code={expected_code}）", msg)
        return False


def get_data(body):
    return body.get("data") or {}


def record_bug(bug_id, title, detail):
    bugs.append((bug_id, title, detail))
    print(f"  {RED}{BOLD}🐞 {bug_id}: {title}{RESET}")
    for line in detail.splitlines():
        print(f"     {RED}{line}{RESET}")


# ── MySQL 工具 ────────────────────────────────────────────
def mysql_exec(sql, fetch=False):
    try:
        import pymysql
        conn = pymysql.connect(
            host=MYSQL_HOST, port=MYSQL_PORT,
            user=MYSQL_USER, password=MYSQL_PASS, database=MYSQL_DB,
            charset="utf8mb4"
        )
        with conn:
            with conn.cursor() as cur:
                result = None
                for stmt in sql.strip().split(";"):
                    stmt = stmt.strip()
                    if stmt:
                        cur.execute(stmt)
                        if fetch:
                            result = cur.fetchall()
            conn.commit()
        return result if fetch else True
    except ImportError:
        pass
    except Exception as e:
        print(f"  {RED}MySQL 执行失败: {e}{RESET}")
        return None if fetch else False

    import subprocess
    try:
        proc = subprocess.run(
            ["mysql", f"-h{MYSQL_HOST}", f"-P{MYSQL_PORT}",
             f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
             "-e", sql],
            capture_output=True, text=True, timeout=10
        )
        if fetch:
            return proc.stdout
        return proc.returncode == 0
    except FileNotFoundError:
        print(f"  {RED}未找到 mysql 命令，且 pymysql 未安装{RESET}")
        return None if fetch else False


# ════════════════════════════════════════════════════════
# 前置准备：注册一个测试用户（手机+邮箱）
# ════════════════════════════════════════════════════════
def register_test_user():
    section("前置准备：注册测试用户")
    ts = int(time.time())
    phone = f"138{ts % 100000000:08d}"
    email = f"pr25_{ts}@example.com"
    username = f"pr25_{ts}"
    password = "Test1234!"

    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "register"})
    assert_status("发送手机注册验证码", status, 200, body)
    phone_code = get_data(body).get("code", "")
    info(f"手机注册验证码: {phone_code}")

    status, body = post("/api/auth/verification-codes/email",
                         {"email": email, "scene": "register"})
    assert_status("发送邮箱注册验证码", status, 200, body)
    email_code = get_data(body).get("code", "")
    info(f"邮箱注册验证码: {email_code}")

    status, body = post("/api/auth/register", {
        "username": username,
        "phone": phone,
        "email": email,
        "password": password,
        "phone_code": phone_code,
        "email_code": email_code,
    })
    assert_status("统一注册", status, 201, body)
    info(f"注册用户：phone={phone}, email={email}, username={username}")

    return {
        "phone": phone,
        "email": email,
        "username": username,
        "password": password,
    }


# ════════════════════════════════════════════════════════
# 用例 1：BUG-PR20-E 主用例 —— 未注册手机号 login/phone → 404/40404
# ════════════════════════════════════════════════════════
def test_unregistered_phone_login():
    section("用例1：未注册手机号 POST /api/auth/login/phone → 期望 404/40404")

    ts = int(time.time())
    unregistered_phone = f"199{ts % 100000000:08d}"

    status, body = post("/api/auth/login/phone", {"phone": unregistered_phone, "code": "123456"})
    ok1 = assert_code("未注册手机号登录（期望 404/40404）", status, 404, body, 40404)
    msg = body.get("message", "") if isinstance(body, dict) else ""
    if ok1:
        if "未注册" in msg:
            ok(f"message 含「未注册」：{msg}")
        else:
            fail("message 未包含「未注册」", f"实际 message={msg}")
            record_bug("BUG-PR25-A", "未注册手机号登录返回 404/40404，但 message 未包含「未注册」",
                       f"请求: POST /api/auth/login/phone {{\"phone\":\"{unregistered_phone}\",\"code\":\"123456\"}}\n"
                       f"期望: message 包含「未注册」\n"
                       f"实际: message=\"{msg}\"")
    else:
        record_bug("BUG-PR20-E-retest", "未注册手机号调用 login/phone 仍未返回 404/40404（PR#25 修复未生效）",
                   f"请求: POST /api/auth/login/phone {{\"phone\":\"{unregistered_phone}\",\"code\":\"123456\"}}（手机号未注册）\n"
                   f"期望: HTTP 404 code=40404\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")


# ════════════════════════════════════════════════════════
# 用例 2：同根因 —— 未注册邮箱 login/email → 404/40404
# ════════════════════════════════════════════════════════
def test_unregistered_email_login():
    section("用例2：未注册邮箱 POST /api/auth/login/email → 期望 404/40404")

    ts = int(time.time())
    unregistered_email = f"pr25_unreg_{ts}@example.com"

    status, body = post("/api/auth/login/email", {"email": unregistered_email, "password": "AnyPassword1!"})
    ok1 = assert_code("未注册邮箱登录（期望 404/40404）", status, 404, body, 40404)
    msg = body.get("message", "") if isinstance(body, dict) else ""
    if ok1:
        if "未注册" in msg:
            ok(f"message 含「未注册」：{msg}")
        else:
            fail("message 未包含「未注册」", f"实际 message={msg}")
            record_bug("BUG-PR25-B", "未注册邮箱登录返回 404/40404，但 message 未包含「未注册」",
                       f"请求: POST /api/auth/login/email {{\"email\":\"{unregistered_email}\",\"password\":\"AnyPassword1!\"}}\n"
                       f"期望: message 包含「未注册」\n"
                       f"实际: message=\"{msg}\"")
    else:
        record_bug("BUG-PR25-C", "未注册邮箱调用 login/email 未返回 404/40404",
                   f"请求: POST /api/auth/login/email {{\"email\":\"{unregistered_email}\",\"password\":\"AnyPassword1!\"}}（邮箱未注册）\n"
                   f"期望: HTTP 404 code=40404\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")


# ════════════════════════════════════════════════════════
# 用例 3：回归 —— 已注册手机号 + 正确验证码登录 → 200，返回 token pair
# ════════════════════════════════════════════════════════
def test_regression_correct_login(user):
    section("用例3：回归——已注册手机号 + 正确验证码登录 → 200，返回 token pair")

    phone = user["phone"]

    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"})
    assert_status("发送登录验证码（已注册手机号）", status, 200, body)
    code = get_data(body).get("code", "")
    info(f"登录验证码: {code}")

    status, body = post("/api/auth/login/phone", {"phone": phone, "code": code})
    ok1 = assert_status("正确验证码登录成功", status, 200, body)
    d = get_data(body)
    access_token = d.get("access_token", "")
    refresh_token = d.get("refresh_token", "")
    if ok1:
        if access_token and refresh_token:
            ok("响应包含 access_token / refresh_token")
        else:
            fail("响应缺少 access_token / refresh_token", json.dumps(d, ensure_ascii=False))
            record_bug("BUG-PR25-D", "正常登录响应缺少 token 字段（回归）",
                       f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"{code}\"}}\n"
                       f"期望: data 含 access_token/refresh_token\n"
                       f"实际: data={json.dumps(d, ensure_ascii=False)}")

    return access_token, refresh_token


# ════════════════════════════════════════════════════════
# 用例 4：回归 —— 退出登录后旧 access_token 调用 /api/me → 401/40001
# ════════════════════════════════════════════════════════
def test_regression_logout_revoke(access_token, refresh_token):
    section("用例4：回归——退出登录后旧 access_token 调用 /api/me → 401/40001")

    if not access_token:
        fail("未获得有效 access_token，跳过本用例")
        return

    status, body = get("/api/me", token=access_token)
    assert_status("退出前：access_token 调用 /api/me", status, 200, body)

    status, body = post("/api/auth/logout", {"refresh_token": refresh_token}, token=access_token)
    assert_status("退出登录", status, 200, body)

    status, body = get("/api/me", token=access_token)
    revoke_ok = assert_code("退出后：旧 access_token 调用 /api/me（期望 401/40001）", status, 401, body, 40001)
    if not revoke_ok:
        record_bug("BUG-PR25-E", "退出登录后旧 access_token 仍可访问受保护接口（PR#22 吊销逻辑回归失败）",
                   f"请求: GET /api/me (Authorization: Bearer <已退出的 access_token>)\n"
                   f"期望: HTTP 401 code=40001\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")


# ════════════════════════════════════════════════════════
# 用例 5：回归 —— 已注册手机号 + 错误验证码登录 → 仍为 400/40000（未被误改为 404）
# ════════════════════════════════════════════════════════
def test_regression_wrong_code_login(user):
    section("用例5：回归——已注册手机号 + 错误验证码登录 → 仍为 400/40000")

    phone = user["phone"]

    # 先发一个验证码（保证用户存在登录验证码记录，但本次用错误验证码登录）
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"})
    assert_status("发送登录验证码（用于错误验证码用例）", status, 200, body)

    status, body = post("/api/auth/login/phone", {"phone": phone, "code": "000000"})
    wrong_ok = assert_code("错误验证码登录已注册手机号（期望 400/40000）", status, 400, body, 40000)
    if not wrong_ok:
        record_bug("BUG-PR25-F", "已注册手机号错误验证码登录未返回 400/40000（疑似被本次改动误改为 404）",
                   f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"000000\"}}（手机号已注册，验证码错误）\n"
                   f"期望: HTTP 400 code=40000\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")


# ════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  PR#25 复测：登录接口对未注册账号返回 404/40404{RESET}")
    print(f"{BOLD}  （修复 BUG-PR20-E）{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}")

    user = register_test_user()

    test_unregistered_phone_login()
    test_unregistered_email_login()
    access_token, refresh_token = test_regression_correct_login(user)
    test_regression_logout_revoke(access_token, refresh_token)
    test_regression_wrong_code_login(user)

    total = passed + failed
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  测试结果：{passed}/{total} 通过{RESET}")
    if bugs:
        print(f"\n{BOLD}{RED}  发现 {len(bugs)} 个缺陷：{RESET}")
        for bug_id, title, _ in bugs:
            print(f"    {RED}- {bug_id}: {title}{RESET}")
    if failed == 0:
        print(f"{BOLD}{GREEN}  结论：全部通过 ✅{RESET}")
    else:
        print(f"{BOLD}{RED}  结论：{failed} 项失败 ❌，请检查上方输出{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}\n")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()

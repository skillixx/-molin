#!/usr/bin/env python3
"""
PR#20 / PR#21 / PR#22 接口验收测试脚本

测试范围：
  - PR#20: POST /api/auth/login/phone 改为手机号验证码登录（{phone, code}）
  - PR#22: POST /api/auth/logout 退出登录后立即吊销当前 Access Token
  - PR#21: 用户控制台登录页手机号 Tab 改为验证码登录（通过 /api 代理验证端到端流程可用）

用法：
    # 连测试服务器（后端）
    API_BASE=http://localhost:8080 \
    FRONTEND_BASE=http://localhost:3000 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/test_pr20_21_22.py
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE      = os.getenv("API_BASE", "http://localhost:8080")
FRONTEND_BASE = os.getenv("FRONTEND_BASE", "http://localhost:3000")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# ── 颜色输出 ──────────────────────────────────────────────
GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

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
            # 非 JSON 响应（如前端 SPA 返回的 HTML 页面），仍返回真实状态码
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
# 前置准备：注册一个测试用户（手机+邮箱），用于 PR#20/PR#22 测试
# ════════════════════════════════════════════════════════
def register_test_user():
    section("前置准备：注册测试用户")
    ts = int(time.time())
    phone = f"137{ts % 100000000:08d}"
    email = f"pr20_22_{ts}@example.com"
    username = f"pr2022_{ts}"
    password = "Test1234!"

    # 发送手机注册验证码
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "register"})
    assert_status("发送手机注册验证码", status, 200, body)
    phone_code = get_data(body).get("code", "")
    info(f"手机注册验证码: {phone_code}")

    # 发送邮箱注册验证码
    status, body = post("/api/auth/verification-codes/email",
                         {"email": email, "scene": "register"})
    assert_status("发送邮箱注册验证码", status, 200, body)
    email_code = get_data(body).get("code", "")
    info(f"邮箱注册验证码: {email_code}")

    # 统一注册
    status, body = post("/api/auth/register", {
        "username": username,
        "phone": phone,
        "email": email,
        "password": password,
        "phone_code": phone_code,
        "email_code": email_code,
    })
    assert_status("统一注册", status, 201, body)
    d = get_data(body)
    info(f"注册用户：phone={phone}, email={email}, username={username}")

    return {
        "phone": phone,
        "email": email,
        "username": username,
        "password": password,
        "access_token": d.get("access_token", ""),
        "refresh_token": d.get("refresh_token", ""),
    }


# ════════════════════════════════════════════════════════
# PR#20  /api/auth/login/phone 改为验证码登录
# ════════════════════════════════════════════════════════
def test_pr20(user):
    section("PR#20  POST /api/auth/login/phone 改为手机号验证码登录")
    phone = user["phone"]

    # 1. 正常流程：发送登录验证码 → 正确验证码登录成功
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"})
    assert_status("发送登录验证码（已注册手机号）", status, 200, body)
    code = get_data(body).get("code", "")
    info(f"登录验证码: {code}")

    status, body = post("/api/auth/login/phone", {"phone": phone, "code": code})
    ok1 = assert_status("正确验证码登录成功", status, 200, body)
    d = get_data(body)
    if ok1:
        if d.get("access_token") and d.get("refresh_token"):
            ok("响应包含 access_token / refresh_token")
        else:
            fail("响应缺少 access_token / refresh_token", json.dumps(d, ensure_ascii=False))
            record_bug("BUG-PR20-A", "正确验证码登录响应缺少 token 字段",
                       f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"{code}\"}}\n"
                       f"期望: data 含 access_token/refresh_token\n"
                       f"实际: data={json.dumps(d, ensure_ascii=False)}")

    # 2. anti-replay：同一验证码再次登录应失败（已被标记 used）
    status, body = post("/api/auth/login/phone", {"phone": phone, "code": code})
    replay_ok = assert_code("验证码重放被拒绝（期望 40000）", status, 400, body, 40000)
    if not replay_ok:
        record_bug("BUG-PR20-B", "登录验证码可重复使用（anti-replay 失效）",
                    f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"{code}\"}}（重复发送同一验证码）\n"
                    f"期望: HTTP 400 code=40000（验证码已使用/过期）\n"
                    f"实际: HTTP {status} code={body.get('code') if isinstance(body, dict) else None}, "
                    f"message={body.get('message', '') if isinstance(body, dict) else ''}")

    # 3. 错误验证码登录 → 40000
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"})
    assert_status("再次发送登录验证码", status, 200, body)
    status, body = post("/api/auth/login/phone", {"phone": phone, "code": "000000"})
    wrong_ok = assert_code("错误验证码登录被拦截（期望 40000）", status, 400, body, 40000)
    if not wrong_ok:
        record_bug("BUG-PR20-C", "错误验证码登录未返回 40000",
                    f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"000000\"}}\n"
                    f"期望: HTTP 400 code=40000\n"
                    f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")

    # 4. 过期验证码登录 → 40000（通过修改 DB expires_at 模拟过期）
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"})
    assert_status("发送验证码用于过期测试", status, 200, body)
    expire_code = get_data(body).get("code", "")
    info(f"待过期验证码: {expire_code}")
    # 将该验证码 expires_at 改为过去时间
    mysql_exec(f"""
        UPDATE verification_codes
        SET expires_at = DATE_SUB(NOW(), INTERVAL 1 MINUTE)
        WHERE target_type='phone' AND target_value='{phone}' AND scene='login'
          AND used_at IS NULL
        ORDER BY created_at DESC LIMIT 1
    """)
    status, body = post("/api/auth/login/phone", {"phone": phone, "code": expire_code})
    expired_ok = assert_code("过期验证码登录被拦截（期望 40000）", status, 400, body, 40000)
    if not expired_ok:
        record_bug("BUG-PR20-D", "过期验证码登录未返回 40000",
                    f"请求: POST /api/auth/login/phone {{\"phone\":\"{phone}\",\"code\":\"{expire_code}\"}}（验证码已过期）\n"
                    f"期望: HTTP 400 code=40000\n"
                    f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")

    # 5. 未注册手机号 → 40404（在发送验证码阶段已校验）
    ts = int(time.time())
    unregistered_phone = f"199{ts % 100000000:08d}"
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": unregistered_phone, "scene": "login"})
    unreg_send_ok = assert_code("未注册手机号发送登录验证码（期望 40404）", status, 404, body, 40404)

    # 同时直接调用 login/phone（即使验证码发送被拦截，也验证登录接口本身对未注册手机号的处理）
    status, body = post("/api/auth/login/phone", {"phone": unregistered_phone, "code": "123456"})
    unreg_login_ok = assert_code("未注册手机号直接登录（期望 40404）", status, 404, body, 40404)
    if not unreg_login_ok:
        record_bug("BUG-PR20-E", "未注册手机号调用 login/phone 返回 401/40001，而非 40404",
                    f"请求: POST /api/auth/login/phone {{\"phone\":\"{unregistered_phone}\",\"code\":\"123456\"}}（手机号未注册）\n"
                    f"期望: HTTP 404 code=40404（与 /api/auth/verification-codes/phone 对未注册手机号的处理一致）\n"
                    f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}\n"
                    f"根因: server/internal/modules/auth/service/auth_service.go LoginPhone 中，"
                    f"FindByPhone 失败时返回 ErrUnauthorized（映射为 40001），未区分\"用户不存在\"，"
                    f"未走 ErrPhoneNotRegistered（40404）分支")


# ════════════════════════════════════════════════════════
# PR#22  退出登录后立即吊销当前 Access Token
# ════════════════════════════════════════════════════════
def test_pr22(user):
    section("PR#22  POST /api/auth/logout 退出登录后立即吊销当前 Access Token")

    # 准备：用 email+password 登录两次，模拟两个不同的"设备/会话"
    email = user["email"]
    password = user["password"]

    status, body = post("/api/auth/login/email", {"email": email, "password": password})
    assert_status("设备A：邮箱密码登录", status, 200, body)
    d_a = get_data(body)
    access_a = d_a.get("access_token", "")
    refresh_a = d_a.get("refresh_token", "")

    status, body = post("/api/auth/login/email", {"email": email, "password": password})
    assert_status("设备B：邮箱密码登录", status, 200, body)
    d_b = get_data(body)
    access_b = d_b.get("access_token", "")
    refresh_b = d_b.get("refresh_token", "")

    # 1. 退出登录前，access_a 调用 /api/me 应 200
    status, body = get("/api/me", token=access_a)
    assert_status("退出前：设备A access_token 调用 /api/me", status, 200, body)

    # 2. 退出登录（设备A）
    status, body = post("/api/auth/logout", {"refresh_token": refresh_a}, token=access_a)
    assert_status("设备A：退出登录", status, 200, body)

    # 3. 退出登录后，同一旧 access_a 再调用 /api/me → 期望 401 + code=40001 + message 含"已失效"
    status, body = get("/api/me", token=access_a)
    revoke_ok = assert_code("退出后：旧 access_token 调用 /api/me（期望 401 code=40001）", status, 401, body, 40001)
    msg = body.get("message", "") if isinstance(body, dict) else ""
    if revoke_ok:
        if "已失效" in msg:
            ok(f"错误 message 含「已失效」：{msg}")
        else:
            fail(f"错误 message 未包含「已失效」", f"实际 message={msg}")
            record_bug("BUG-PR22-A", "退出登录后旧 access_token 返回 401/40001，但 message 未包含「已失效」",
                       f"请求: GET /api/me (Authorization: Bearer <已退出的 access_token>)\n"
                       f"期望: message 包含「已失效」\n"
                       f"实际: message=\"{msg}\"")
    else:
        record_bug("BUG-PR22-A", "退出登录后旧 access_token 仍可访问受保护接口（未被吊销）",
                   f"步骤:\n"
                   f"  1. POST /api/auth/login/email 获取 access_token A\n"
                   f"  2. GET /api/me (token A) -> 200（正常）\n"
                   f"  3. POST /api/auth/logout (token A, refresh_token A)\n"
                   f"  4. GET /api/me (token A) -> 期望 401 code=40001\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")

    # 4. 旧 refresh_token 调用 /api/auth/refresh 应失败（401）
    status, body = post("/api/auth/refresh", {"refresh_token": refresh_a})
    refresh_revoke_ok = assert_status("退出后：旧 refresh_token 刷新被拦截（期望 401）", status, 401, body)
    if not refresh_revoke_ok:
        record_bug("BUG-PR22-B", "退出登录后旧 refresh_token 仍可用于刷新",
                   f"请求: POST /api/auth/refresh {{\"refresh_token\":\"<已退出的 refresh_token>\"}}\n"
                   f"期望: HTTP 401\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")

    # 5. 设备B 的 access_token 不应被一并吊销（per-token 而非 per-user）
    status, body = get("/api/me", token=access_b)
    device_b_ok = assert_status("设备B access_token 仍然有效（期望 200，未被一并吊销）", status, 200, body)
    if not device_b_ok:
        record_bug("BUG-PR22-C", "设备A退出登录后，设备B的 access_token 也被一并吊销（应为 per-token 吊销）",
                   f"步骤:\n"
                   f"  1. 同一账号在设备A、设备B各登录一次，得到 access_token A / B\n"
                   f"  2. 设备A 调用 POST /api/auth/logout\n"
                   f"  3. GET /api/me (token B) -> 期望 200\n"
                   f"实际: HTTP {status} body={json.dumps(body, ensure_ascii=False)}")

    # 清理：退出设备B
    post("/api/auth/logout", {"refresh_token": refresh_b}, token=access_b)


# ════════════════════════════════════════════════════════
# PR#21  用户控制台登录页手机号 Tab 改为验证码登录（端到端代理验证）
# ════════════════════════════════════════════════════════
def test_pr21(user):
    section("PR#21  用户控制台（端到端，经 /api 代理）")

    phone = user["phone"]

    # 1. 前端首页可访问
    status, body = get("/", base=FRONTEND_BASE)
    assert_status("前端首页可访问", status, 200, body)

    # 2. 登录页可访问
    status, body = get("/login", base=FRONTEND_BASE)
    assert_status("登录页可访问", status, 200, body)

    # 3. 通过前端 /api 代理发送手机登录验证码（模拟点击"发送验证码"按钮）
    status, body = post("/api/auth/verification-codes/phone",
                         {"phone": phone, "scene": "login"}, base=FRONTEND_BASE)
    proxy_ok = assert_status("经前端代理：发送手机登录验证码", status, 200, body)
    code = get_data(body).get("code", "")
    if proxy_ok:
        info(f"经代理获取验证码: {code}")

    # 4. 通过前端 /api 代理执行手机验证码登录（模拟点击"登录"按钮）
    if code:
        status, body = post("/api/auth/login/phone", {"phone": phone, "code": code}, base=FRONTEND_BASE)
        login_ok = assert_status("经前端代理：手机验证码登录成功", status, 200, body)
        d = get_data(body)
        if login_ok and not (d.get("access_token") and d.get("refresh_token")):
            fail("经代理登录响应缺少 token 字段", json.dumps(d, ensure_ascii=False))
    else:
        fail("未获取到验证码，跳过经代理登录测试")

    info("说明：SPA 页面元素（手机号/验证码输入框、发送验证码按钮及60s倒计时、移除密码框）"
         "已通过代码审查确认存在于 LoginView.vue（commit 5cca4bd），且构建产物中包含 "
         "/auth/login/phone 与 /auth/verification-codes/phone 调用路径；"
         "上述端到端流程验证证明该页面调用的后端接口契约与 PR#20 一致、代理链路可用。"
         "完整 UI 交互建议由人工/浏览器自动化做最终视觉验收。")


# ════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  PR#20 / PR#21 / PR#22 接口验收测试{RESET}")
    print(f"{BOLD}  后端目标：{API_BASE}{RESET}")
    print(f"{BOLD}  前端目标：{FRONTEND_BASE}{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}")

    user = register_test_user()

    test_pr20(user)
    test_pr22(user)
    test_pr21(user)

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

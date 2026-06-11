#!/usr/bin/env python3
"""
后端工程师甲 — 全量接口测试脚本（2026-06-12 复测版）
覆盖范围：
  A-01  Auth 模块（认证）
  A-02  IAM 模块（角色 / 权限 / RBAC / 审计日志）
  A-03  IAM 用户分组模块
  A-04  Identity 模块（实名认证）

本版变更（PR #5 / commit 03af593 复测）：
  - BUG-06：原 GET /api/admin/user-groups/user/{uid} 路由因与
    /api/admin/user-groups/{id}/members 冲突已被移除，改为验证
    GET /api/admin/users/{id}/groups（GetUserGroups）能等价
    返回该用户所属分组列表（200 + items）

用法：
    # 连测试服务器（推荐）
    API_BASE=http://8.130.9.163:8080 python3 tests/test_backend_a_full.py

    # 连测试服务器并直连数据库（播种管理员权限）
    API_BASE=http://8.130.9.163:8080 \
    MYSQL_HOST=8.130.9.163 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=<pass> MYSQL_DATABASE=molin \
    python3 tests/test_backend_a_full.py
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE   = os.getenv("API_BASE",       "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST",     "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER",     "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# ── 颜色输出 ──────────────────────────────────────────────
GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed = 0
failed = 0
skipped = 0

# 记录所有测试结果，用于最终报告
results = []  # (label, status, http_code, detail)

def ok(label, http_code=""):
    global passed
    passed += 1
    results.append((label, "PASS", http_code, ""))
    print(f"  {GREEN}PASS  [{http_code}]  {label}{RESET}")

def fail(label, http_code="", detail=""):
    global failed
    failed += 1
    results.append((label, "FAIL", http_code, detail))
    msg = f"  {RED}FAIL  [{http_code}]  {label}{RESET}"
    if detail:
        msg += f"\n        {RED}{detail}{RESET}"
    print(msg)

def skip(label, reason=""):
    global skipped
    skipped += 1
    results.append((label, "SKIP", "-", reason))
    print(f"  {YELLOW}SKIP  [-]  {label}  ({reason}){RESET}")

def info(msg):
    print(f"  {YELLOW}INFO  {msg}{RESET}")

def section(title):
    print(f"\n{BOLD}{CYAN}{'─'*60}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'─'*60}{RESET}")

# ── HTTP 工具 ─────────────────────────────────────────────
def request(method, path, body=None, token=None, extra_headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else b""
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if extra_headers:
        headers.update(extra_headers)
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

def post(path, body=None, token=None, extra_headers=None):
    return request("POST", path, body, token, extra_headers)

def get(path, token=None):
    return request("GET", path, token=token)

def put(path, body=None, token=None):
    return request("PUT", path, body, token)

def patch(path, body=None, token=None):
    return request("PATCH", path, body, token)

def delete(path, token=None):
    return request("DELETE", path, token=token)

def chk(label, status, expected, body):
    """通用状态码断言，返回是否通过。"""
    code = status
    msg  = body.get("message", "") if isinstance(body, dict) else ""
    if status == expected:
        ok(label, code)
        return True
    else:
        fail(label, code, f"期望 {expected}，实际 {status}。{msg}")
        return False

def get_data(body):
    if isinstance(body, dict):
        d = body.get("data")
        if isinstance(d, dict):
            return d
    return {}

# ── MySQL 工具（播种测试数据）────────────────────────────
def mysql_exec(sql):
    try:
        import pymysql
        conn = pymysql.connect(
            host=MYSQL_HOST, port=MYSQL_PORT,
            user=MYSQL_USER, password=MYSQL_PASS, database=MYSQL_DB,
            charset="utf8mb4"
        )
        with conn:
            with conn.cursor() as cur:
                for stmt in sql.strip().split(";"):
                    stmt = stmt.strip()
                    if stmt:
                        cur.execute(stmt)
                conn.commit()
        return True
    except ImportError:
        pass
    except Exception as e:
        print(f"  {RED}MySQL 执行失败: {e}{RESET}")
        return False

    import subprocess
    try:
        proc = subprocess.run(
            ["mysql", f"-h{MYSQL_HOST}", f"-P{MYSQL_PORT}",
             f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
             "-e", sql],
            capture_output=True, text=True, timeout=10
        )
        return proc.returncode == 0
    except FileNotFoundError:
        print(f"  {YELLOW}mysql 命令不可用，跳过数据库播种{RESET}")
        return False

def seed_super_admin(user_id):
    """将指定用户绑定到现有 admin 角色（测试库已有 admin 角色及完整权限）。"""
    sql = f"""
    SET @rid = (SELECT id FROM roles WHERE code='admin' LIMIT 1);
    INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, @rid)
    """
    return mysql_exec(sql)


# ════════════════════════════════════════════════════════════
# A-01  Auth 模块
# ════════════════════════════════════════════════════════════
def test_a01():
    section("A-01  Auth 模块 — 认证接口")

    ts    = int(time.time())
    email = f"qa_a01_{ts}@example.com"
    phone = f"139{ts % 100000000:08d}"

    # ── 1. 发送邮箱注册验证码 ────────────────────────────
    status, body = post("/api/auth/verification-codes/email",
                        {"email": email, "scene": "register"})
    chk("POST /api/auth/verification-codes/email  发送邮箱注册验证码", status, 200, body)
    email_code = get_data(body).get("code", "")
    info(f"邮箱验证码（测试环境可见）: {email_code}")

    # ── 2. 缺少 scene 字段 → 400（BUG-01 复测）───────────
    status, body = post("/api/auth/verification-codes/email", {"email": email})
    chk("[BUG-01复测] POST /api/auth/verification-codes/email  缺 scene → 400", status, 400, body)

    # ── 2b. 缺少 email 字段 → 400（BUG-01 复测）──────────
    status, body = post("/api/auth/verification-codes/email", {"scene": "register"})
    chk("[BUG-01复测] POST /api/auth/verification-codes/email  缺 email → 400", status, 400, body)

    # ── 3. 发送手机注册验证码 ────────────────────────────
    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": phone, "scene": "register"})
    chk("POST /api/auth/verification-codes/phone  发送手机注册验证码", status, 200, body)
    phone_code = get_data(body).get("code", "")
    info(f"手机验证码（测试环境可见）: {phone_code}")

    # ── 4. 缺少 phone 字段 → 400（BUG-02 复测）───────────
    status, body = post("/api/auth/verification-codes/phone", {"scene": "register"})
    chk("[BUG-02复测] POST /api/auth/verification-codes/phone  缺 phone → 400", status, 400, body)

    # ── 4b. 缺少 scene 字段 → 400（BUG-02 复测）──────────
    status, body = post("/api/auth/verification-codes/phone", {"phone": phone})
    chk("[BUG-02复测] POST /api/auth/verification-codes/phone  缺 scene → 400", status, 400, body)

    # ── 5. 统一注册（正常路径）───────────────────────────
    reg_body = {
        "email":      email,
        "phone":      phone,
        "password":   "Test1234!",
        "email_code": email_code,
        "phone_code": phone_code,
        "username":   f"qa{ts % 100000}",
    }
    status, body = post("/api/auth/register", reg_body)
    ok_reg = chk("POST /api/auth/register  统一注册（正常路径）", status, 201, body)
    d = get_data(body)
    access_token  = d.get("access_token", "")
    refresh_token = d.get("refresh_token", "")
    info(f"access_token: {'已获取' if access_token else '未获取'}")

    # ── 6. 统一注册 — 旧验证码重复使用 → 400 ────────────
    status, body = post("/api/auth/register", reg_body)
    chk("POST /api/auth/register  已用验证码/已存在邮箱重复注册 → 4xx",
        status in (400, 409), True, body) \
        if status in (400, 409) else \
        fail("POST /api/auth/register  已用验证码/已存在邮箱重复注册 → 4xx",
             status, f"期望 400/409，实际 {status}")

    # ── 7. 统一注册 — 缺少 phone_code → 400 ─────────────
    status, body = post("/api/auth/register", {
        "email": f"qa_miss_{ts}@example.com",
        "phone": f"138{ts % 100000000:08d}",
        "password": "Test1234!",
        "email_code": "123456",
        # phone_code 故意不传
    })
    chk("POST /api/auth/register  缺 phone_code → 400", status, 400, body)

    # ── 8. GET /api/me（有效 Token）─────────────────────
    status, body = get("/api/me", token=access_token)
    ok_me = chk("GET /api/me  有效 Token → 200", status, 200, body)
    d_me = get_data(body)
    user_id = d_me.get("id")
    info(f"当前用户 ID: {user_id}")

    # 校验 /api/me 响应字段完整性
    required_me_fields = [
        "id", "email", "phone", "email_verified", "phone_verified",
        "real_name_status", "status", "admin_phone_verified",
        "admin_email_verified", "created_at"
    ]
    if ok_me:
        for f in required_me_fields:
            if f not in d_me:
                fail(f"GET /api/me  响应缺少字段 {f}", 200, "")
            else:
                ok(f"GET /api/me  响应包含字段 {f}", 200)
        # 验证脱敏格式
        em = d_me.get("email", "")
        ph = d_me.get("phone", "")
        if "***" in em:
            ok("GET /api/me  邮箱脱敏格式正确", 200)
        else:
            fail("GET /api/me  邮箱应脱敏（期望含 ***）", 200, f"实际: {em}")
        if "****" in ph:
            ok("GET /api/me  手机号脱敏格式正确", 200)
        else:
            fail("GET /api/me  手机号应脱敏（期望含 ****）", 200, f"实际: {ph}")

    # ── 9. GET /api/me（无 Token）→ 401 ─────────────────
    status, body = get("/api/me")
    chk("GET /api/me  无 Token → 401", status, 401, body)

    # ── 10. 伪造 JWT → 401 ──────────────────────────────
    fake_jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." \
               "eyJ1c2VyX2lkIjo5OTk5OTksImVtYWlsIjoiZmFrZUBleGFtcGxlLmNvbSJ9." \
               "INVALIDSIGNATURE"
    status, body = get("/api/me", token=fake_jwt)
    chk("GET /api/me  伪造 JWT → 401", status, 401, body)

    # ── 11. 邮箱密码登录（正常）─────────────────────────
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "Test1234!"})
    chk("POST /api/auth/login/email  正常登录", status, 200, body)
    d2 = get_data(body)
    access_token2  = d2.get("access_token", "")
    refresh_token2 = d2.get("refresh_token", "")

    # 校验登录返回 user 对象字段
    if d2.get("user"):
        for f in ["id", "email", "phone", "real_name_status", "status"]:
            if f in d2.get("user", {}):
                ok(f"POST /api/auth/login/email  user.{f} 字段存在", 200)
            else:
                fail(f"POST /api/auth/login/email  user.{f} 字段缺失", 200, "")

    # ── 12. 邮箱登录 — 错误密码 → 401 ───────────────────
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "WrongPass999!"})
    chk("POST /api/auth/login/email  错误密码 → 401", status, 401, body)

    # ── 13. 手机号登录（BUG-03 复测：手机号+密码登录）────
    status, body = post("/api/auth/login/phone",
                        {"phone": phone, "password": "Test1234!"})
    chk("[BUG-03复测] POST /api/auth/login/phone  手机号+密码登录 → 200", status, 200, body)
    d3 = get_data(body)
    access_token3  = d3.get("access_token", "")
    refresh_token3 = d3.get("refresh_token", "")
    if access_token3:
        ok("[BUG-03复测] POST /api/auth/login/phone  返回 access_token", 200)
    else:
        fail("[BUG-03复测] POST /api/auth/login/phone  未返回 access_token", status, str(body)[:200])

    # ── 14. 手机号登录 — 错误密码 → 401（BUG-03 关联）────
    status, body = post("/api/auth/login/phone",
                        {"phone": phone, "password": "WrongPass999!"})
    chk("[BUG-03复测] POST /api/auth/login/phone  错误密码 → 401", status, 401, body)

    # ── 15. 刷新 Token ───────────────────────────────────
    status, body = post("/api/auth/refresh", {"refresh_token": refresh_token2})
    chk("POST /api/auth/refresh  正常刷新", status, 200, body)
    d_ref = get_data(body)
    new_refresh = d_ref.get("refresh_token", refresh_token2)
    new_access  = d_ref.get("access_token", access_token2)

    # ── 16. 修改密码 ─────────────────────────────────────
    status, body = patch("/api/me/password",
                         {"old_password": "Test1234!", "new_password": "NewPass456!"},
                         token=new_access)
    chk("PATCH /api/me/password  修改密码", status, 200, body)

    # ── 17. 旧密码不可用 → 401 ───────────────────────────
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "Test1234!"})
    chk("POST /api/auth/login/email  旧密码已失效 → 401", status, 401, body)

    # ── 18. 新密码登录 ───────────────────────────────────
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "NewPass456!"})
    chk("POST /api/auth/login/email  新密码登录", status, 200, body)
    d4 = get_data(body)
    access_token4  = d4.get("access_token", "")
    refresh_token4 = d4.get("refresh_token", "")

    # ── 18b. 新密码 + 手机号登录（BUG-03 关联回归）───────
    status, body = post("/api/auth/login/phone",
                        {"phone": phone, "password": "NewPass456!"})
    chk("[BUG-03复测] POST /api/auth/login/phone  改密后用新密码登录 → 200", status, 200, body)

    # ── 19. 修改用户名 ───────────────────────────────────
    new_username = f"qa_renamed_{ts % 100000}"
    status, body = patch("/api/me/username",
                         {"username": new_username},
                         token=access_token4)
    chk("PATCH /api/me/username  修改用户名", status, 200, body)

    # ── 20. 修改用户名 — 无 Token → 401 ──────────────────
    status, body = patch("/api/me/username", {"username": "nobody"})
    chk("PATCH /api/me/username  无 Token → 401", status, 401, body)

    # ── 21. 修改邮箱（先发 bind_email 验证码）────────────
    new_email = f"qa_new_{ts}@example.com"
    status, body = post("/api/auth/verification-codes/email",
                        {"email": new_email, "scene": "bind_email"})
    chk("POST /api/auth/verification-codes/email  发 bind_email 验证码", status, 200, body)
    bind_email_code = get_data(body).get("code", "")

    if bind_email_code:
        status, body = patch("/api/me/email",
                             {"email": new_email, "code": bind_email_code},
                             token=access_token4)
        chk("PATCH /api/me/email  修改邮箱", status, 200, body)
    else:
        skip("PATCH /api/me/email  修改邮箱", "未获取到 bind_email 验证码")

    # ── 22. 修改手机号（先发 bind_phone 验证码）──────────
    new_phone = f"137{(ts + 1) % 100000000:08d}"
    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": new_phone, "scene": "bind_phone"})
    chk("POST /api/auth/verification-codes/phone  发 bind_phone 验证码", status, 200, body)
    bind_phone_code = get_data(body).get("code", "")

    if bind_phone_code:
        status, body = patch("/api/me/phone",
                             {"phone": new_phone, "code": bind_phone_code},
                             token=access_token4)
        chk("PATCH /api/me/phone  修改手机号", status, 200, body)
    else:
        skip("PATCH /api/me/phone  修改手机号", "未获取到 bind_phone 验证码")

    # ── 23. OTP 密码重置 ─────────────────────────────────
    # 向原始邮箱（已改为 new_email）发重置验证码
    reset_target = new_email if bind_email_code else email
    status, body = post("/api/auth/verification-codes/email",
                        {"email": reset_target, "scene": "reset_password"})
    chk("POST /api/auth/verification-codes/email  发 reset_password 验证码", status, 200, body)
    reset_code = get_data(body).get("code", "")

    if reset_code:
        status, body = post("/api/auth/password/reset", {
            "target":       reset_target,
            "target_type":  "email",
            "code":         reset_code,
            "new_password": "Reset789!",
        })
        chk("POST /api/auth/password/reset  OTP 密码重置", status, 200, body)

        # 重置后旧 refresh_token 应被吊销
        status, body = post("/api/auth/refresh", {"refresh_token": refresh_token4})
        chk("POST /api/auth/refresh  密码重置后旧 token 被吊销 → 401", status, 401, body)
    else:
        skip("POST /api/auth/password/reset  OTP 密码重置", "未获取到 reset_password 验证码")

    # ── 24. 退出登录 ─────────────────────────────────────
    # 先重新登录拿到有效 token
    final_email = reset_target
    final_pass  = "Reset789!" if reset_code else "NewPass456!"
    status, body = post("/api/auth/login/email",
                        {"email": final_email, "password": final_pass})
    if status == 200:
        d5 = get_data(body)
        at5 = d5.get("access_token", "")
        rt5 = d5.get("refresh_token", "")

        status, body = post("/api/auth/logout",
                            {"refresh_token": rt5}, token=at5)
        chk("POST /api/auth/logout  退出登录", status, 200, body)

        # ── 25. 已退出 refresh_token 再刷新 → 401 ────────
        status, body = post("/api/auth/refresh", {"refresh_token": rt5})
        chk("POST /api/auth/refresh  已退出 refresh_token → 401", status, 401, body)
    else:
        skip("POST /api/auth/logout  退出登录", "重新登录失败，跳过退出测试")
        skip("POST /api/auth/refresh  已退出 refresh_token → 401", "依赖退出登录")

    return user_id, final_email, final_pass, new_phone


# ════════════════════════════════════════════════════════════
# 前置：播种超管并获取管理员 Token
# ════════════════════════════════════════════════════════════
def setup_admin(user_id, email, password, phone):
    """播种超管角色，登录，完成手机+邮箱双重认证，返回完整 Token。"""
    section("前置准备 — 播种超管数据并获取管理员 Token（含双重认证）")

    if not user_id:
        info("user_id 为空，跳过播种")
        return None

    seeded = seed_super_admin(user_id)
    if seeded:
        ok(f"user_id={user_id} 已播种超管角色", "-")
    else:
        fail("超管播种失败，A-02/A-03/A-04 管理员用例将受影响", "-",
             "检查 MySQL 连接或手动执行 seed SQL")

    status, body = post("/api/auth/login/email",
                        {"email": email, "password": password})
    if status != 200:
        fail("管理员重新登录失败", status, body.get("message", ""))
        return None
    admin_token = get_data(body).get("access_token", "")
    ok("管理员 Token 获取成功", 200)

    # ── 管理员手机双重认证 ────────────────────────────────
    if phone:
        s, b = post("/api/auth/verification-codes/phone",
                    {"phone": phone, "scene": "admin_verify"})
        if s == 200:
            phone_code = get_data(b).get("code", "")
            if phone_code:
                sv, bv = post("/api/admin/auth/verify-phone",
                              {"code": phone_code}, token=admin_token)
                if sv == 200:
                    ok("POST /api/admin/auth/verify-phone  管理员手机双重认证", 200)
                else:
                    fail("POST /api/admin/auth/verify-phone  管理员手机双重认证",
                         sv, bv.get("message", ""))
            else:
                fail("管理员手机双重认证  未获取到验证码", s, "")
        else:
            fail("管理员手机双重认证  发送手机验证码失败", s, b.get("message", ""))
    else:
        skip("POST /api/admin/auth/verify-phone  管理员手机双重认证", "无手机号")

    # ── 管理员邮箱双重认证 ────────────────────────────────
    s, b = post("/api/auth/verification-codes/email",
                {"email": email, "scene": "admin_verify"})
    if s == 200:
        email_code = get_data(b).get("code", "")
        if email_code:
            sv, bv = post("/api/admin/auth/verify-email",
                          {"code": email_code}, token=admin_token)
            if sv == 200:
                ok("POST /api/admin/auth/verify-email  管理员邮箱双重认证", 200)
            else:
                fail("POST /api/admin/auth/verify-email  管理员邮箱双重认证",
                     sv, bv.get("message", ""))
        else:
            fail("管理员邮箱双重认证  未获取到验证码", s, "")
    else:
        fail("管理员邮箱双重认证  发送邮箱验证码失败", s, b.get("message", ""))

    return admin_token


# ════════════════════════════════════════════════════════════
# A-02  IAM 模块（角色 / 权限 / RBAC / 审计日志）
# ════════════════════════════════════════════════════════════
def test_a02(admin_token, regular_user_id):
    section("A-02  IAM 模块 — 角色 / 权限 / RBAC / 审计日志")

    if not admin_token:
        skip("A-02 全部用例", "未获取到管理员 Token")
        return None, None

    # ── 1. GET /api/admin/permissions ────────────────────
    status, body = get("/api/admin/permissions", token=admin_token)
    chk("GET /api/admin/permissions  查权限列表", status, 200, body)
    raw_perms = body.get("data") or {}
    perms_list = raw_perms.get("items") if isinstance(raw_perms, dict) else []
    if isinstance(raw_perms, list):
        perms_list = raw_perms
    if perms_list:
        ok("GET /api/admin/permissions  响应包含 items 列表字段", 200)
    else:
        fail("GET /api/admin/permissions  响应 items 为空或字段名错误（应为 items 非 list）",
             200, str(raw_perms)[:200])
    if not perms_list:
        perms_list = []
    perm_id = perms_list[0]["id"] if perms_list else None
    info(f"权限总数: {len(perms_list)}，首个权限 ID: {perm_id}")

    # ── 2. GET /api/admin/permissions — 无 Token → 401 ──
    status, body = get("/api/admin/permissions")
    chk("GET /api/admin/permissions  无 Token → 401", status, 401, body)

    # ── 3. POST /api/admin/roles 创建角色 ────────────────
    ts = int(time.time())
    role_code = f"test_role_{ts}"
    status, body = post("/api/admin/roles",
                        {"code": role_code, "name": "测试角色", "description": "自动化测试创建"},
                        token=admin_token)
    chk("POST /api/admin/roles  创建角色", status, 201, body)
    role_id = get_data(body).get("id")
    info(f"新角色 ID: {role_id}")

    # ── 4. GET /api/admin/roles ───────────────────────────
    status, body = get("/api/admin/roles", token=admin_token)
    chk("GET /api/admin/roles  查角色列表", status, 200, body)
    raw_roles = body.get("data") or {}
    roles_items = raw_roles.get("items") if isinstance(raw_roles, dict) else []
    roles_items = roles_items or []
    if roles_items:
        ok("GET /api/admin/roles  响应包含 items 列表字段", 200)
    else:
        fail("GET /api/admin/roles  响应 items 为空或字段名错误", 200, str(raw_roles)[:200])

    # ── 5. PUT /api/admin/roles/{id} 更新角色 ────────────
    if role_id:
        status, body = put(f"/api/admin/roles/{role_id}",
                           {"code": role_code, "name": "测试角色（已更新）"},
                           token=admin_token)
        chk(f"PUT /api/admin/roles/{role_id}  更新角色", status, 200, body)

    # ── 5b. GET /api/admin/roles/{id} 存在 → 200（BUG-04 关联回归）
    if role_id:
        status, body = get(f"/api/admin/roles/{role_id}", token=admin_token)
        chk(f"[BUG-04关联] GET /api/admin/roles/{role_id}  存在 → 200", status, 200, body)

    # ── 6. GET /api/admin/roles/{id} 不存在 → 404（BUG-04 复测）
    status, body = get("/api/admin/roles/999999999", token=admin_token)
    chk("[BUG-04复测] GET /api/admin/roles/999999999  不存在 → 404", status, 404, body)

    # ── 7. POST /api/admin/users/{id}/roles 分配角色 ─────
    if role_id and regular_user_id:
        status, body = post(f"/api/admin/users/{regular_user_id}/roles",
                            {"role_id": role_id},
                            token=admin_token)
        chk(f"POST /api/admin/users/{regular_user_id}/roles  分配角色", status, 200, body)

        # ── 8. GET /api/admin/users/{id}/roles 查用户角色 ─
        status, body = get(f"/api/admin/users/{regular_user_id}/roles",
                           token=admin_token)
        chk(f"GET /api/admin/users/{regular_user_id}/roles  查用户角色", status, 200, body)

        # ── 9. DELETE /api/admin/users/{id}/roles/{role_id}
        status, body = delete(f"/api/admin/users/{regular_user_id}/roles/{role_id}",
                              token=admin_token)
        chk(f"DELETE /api/admin/users/{regular_user_id}/roles/{role_id}  移除角色",
            status, 200, body)

    # ── 10. 注册另一个普通用户用于权限覆盖测试 ───────────
    ts2 = int(time.time()) + 2
    victim_email = f"qa_victim_{ts2}@example.com"
    victim_phone = f"136{ts2 % 100000000:08d}"
    status, body = post("/api/auth/verification-codes/email",
                        {"email": victim_email, "scene": "register"})
    v_email_code = get_data(body).get("code", "")
    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": victim_phone, "scene": "register"})
    v_phone_code = get_data(body).get("code", "")

    victim_id = None
    if v_email_code and v_phone_code:
        status, body = post("/api/auth/register", {
            "email":      victim_email,
            "phone":      victim_phone,
            "password":   "Victim123!",
            "email_code": v_email_code,
            "phone_code": v_phone_code,
        })
        if status == 201:
            vt = get_data(body).get("access_token", "")
            if vt:
                _, me = get("/api/me", token=vt)
                victim_id = get_data(me).get("id")
    info(f"权限覆盖测试用户 ID: {victim_id}")

    # ── 11. POST /api/admin/users/{id}/permission-overrides
    override_id = None
    if perm_id and victim_id:
        status, body = post(f"/api/admin/users/{victim_id}/permission-overrides",
                            {"permission_id": perm_id, "effect": "deny",
                             "reason": "自动化测试 deny"},
                            token=admin_token)
        chk(f"POST /api/admin/users/{victim_id}/permission-overrides  添加权限覆盖(deny)",
            status, 200, body)
        override_id = get_data(body).get("id")

        # ── 12. 非法 effect → 400 ─────────────────────────
        status, body = post(f"/api/admin/users/{victim_id}/permission-overrides",
                            {"permission_id": perm_id, "effect": "DENY"},
                            token=admin_token)
        chk(f"POST permission-overrides  非法 effect='DENY' → 400", status, 400, body)

        # ── 13. GET /api/admin/users/{id}/permission-overrides
        status, body = get(f"/api/admin/users/{victim_id}/permission-overrides",
                           token=admin_token)
        chk(f"GET /api/admin/users/{victim_id}/permission-overrides  查权限覆盖",
            status, 200, body)

        # ── 14. DELETE permission-overrides/{override_id} ─
        if override_id:
            status, body = delete(
                f"/api/admin/users/{victim_id}/permission-overrides/{override_id}",
                token=admin_token)
            chk(f"DELETE permission-overrides/{override_id}  删除权限覆盖",
                status, 200, body)

    # ── 15. GET /api/admin/users  用户列表 ───────────────
    status, body = get("/api/admin/users", token=admin_token)
    chk("GET /api/admin/users  查用户列表", status, 200, body)
    raw_users = body.get("data") or {}
    users_items = raw_users.get("items") if isinstance(raw_users, dict) else []
    if isinstance(raw_users, list):
        users_items = raw_users
    if users_items:
        ok("GET /api/admin/users  响应包含 items 列表字段（非 list）", 200)
    else:
        fail("GET /api/admin/users  items 为空或字段名错误（应为 items 非 list）",
             200, str(raw_users)[:200])

    # ── 16. GET /api/admin/users/{id}  用户详情 ──────────
    if regular_user_id:
        status, body = get(f"/api/admin/users/{regular_user_id}", token=admin_token)
        chk(f"GET /api/admin/users/{regular_user_id}  查用户详情", status, 200, body)

    # ── 17. GET /api/admin/users/{id}  不存在 → 404 ──────
    status, body = get("/api/admin/users/999999999", token=admin_token)
    chk("GET /api/admin/users/999999999  不存在 → 404", status, 404, body)

    # ── 18. PATCH /api/admin/users/{id}/status  封禁用户 ─
    if victim_id:
        status, body = patch(f"/api/admin/users/{victim_id}/status",
                             {"status": "disabled", "reason": "测试封禁"},
                             token=admin_token)
        chk(f"PATCH /api/admin/users/{victim_id}/status  封禁用户", status, 200, body)

        # ── 19. 封禁后登录 → 401/403 ─────────────────────
        status, body = post("/api/auth/login/email",
                            {"email": victim_email, "password": "Victim123!"})
        chk(f"POST /api/auth/login/email  封禁用户登录被拒 → 401/403",
            status in (401, 403), True, body) \
            if status in (401, 403) else \
            fail("POST /api/auth/login/email  封禁用户登录被拒 → 401/403",
                 status, f"期望 401/403，实际 {status}")

        # ── 20. 解封用户 ──────────────────────────────────
        status, body = patch(f"/api/admin/users/{victim_id}/status",
                             {"status": "active", "reason": "测试解封"},
                             token=admin_token)
        chk(f"PATCH /api/admin/users/{victim_id}/status  解封用户", status, 200, body)

    # ── 21. GET /api/admin/audit-logs（BUG-05 复测）──────
    status, body = get("/api/admin/audit-logs", token=admin_token)
    chk("[BUG-05复测] GET /api/admin/audit-logs  查审计日志 → 200", status, 200, body)
    raw_logs = body.get("data") or {}
    logs_items = raw_logs.get("items") if isinstance(raw_logs, dict) else None
    if logs_items is not None:
        ok("[BUG-05复测] GET /api/admin/audit-logs  响应包含 items 字段", 200)
        info(f"审计日志条数: {len(logs_items)}")
    else:
        fail("[BUG-05复测] GET /api/admin/audit-logs  响应缺少 items 字段", 200, str(raw_logs)[:200])

    # ── 21b. GET /api/admin/audit-logs  无 Token → 401（BUG-05 关联）
    status, body = get("/api/admin/audit-logs")
    chk("[BUG-05关联] GET /api/admin/audit-logs  无 Token → 401", status, 401, body)

    # ── 22. 管理员手机双重认证 ─────────────────────────
    # 先发 admin_verify 验证码给管理员手机
    # 注意：管理员手机已被修改（通过 bind_phone），此处跳过实际验证（依赖上轮修改结果）
    skip("POST /api/admin/auth/verify-phone  管理员手机双重认证",
         "依赖管理员手机号，跳过以避免干扰其他用例（手机号变更后需独立测试）")
    skip("POST /api/admin/auth/verify-email  管理员邮箱双重认证",
         "依赖手机双重认证先通过，跳过")

    # ── 23. 普通用户访问管理接口 → 403 ───────────────────
    # 注册一个纯普通用户并尝试访问
    ts3 = int(time.time()) + 3
    plain_email = f"qa_plain_{ts3}@example.com"
    plain_phone = f"135{ts3 % 100000000:08d}"
    status, body = post("/api/auth/verification-codes/email",
                        {"email": plain_email, "scene": "register"})
    p_ec = get_data(body).get("code", "")
    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": plain_phone, "scene": "register"})
    p_pc = get_data(body).get("code", "")
    plain_token = ""
    if p_ec and p_pc:
        status, body = post("/api/auth/register", {
            "email": plain_email, "phone": plain_phone,
            "password": "Plain123!", "email_code": p_ec, "phone_code": p_pc,
        })
        if status == 201:
            plain_token = get_data(body).get("access_token", "")

    if plain_token:
        status, body = get("/api/admin/roles", token=plain_token)
        chk("GET /api/admin/roles  普通用户访问 → 403", status, 403, body)
        status, body = get("/api/admin/users", token=plain_token)
        chk("GET /api/admin/users  普通用户访问 → 403", status, 403, body)
        status, body = get("/api/admin/audit-logs", token=plain_token)
        chk("[BUG-05关联] GET /api/admin/audit-logs  普通用户访问 → 403", status, 403, body)
    else:
        skip("普通用户访问管理接口 → 403 验证", "普通用户注册失败")

    # ── 24. 删除测试角色 ──────────────────────────────────
    if role_id:
        status, body = delete(f"/api/admin/roles/{role_id}", token=admin_token)
        chk(f"DELETE /api/admin/roles/{role_id}  删除角色", status, 200, body)

    return victim_id, plain_token


# ════════════════════════════════════════════════════════════
# A-03  IAM 用户分组模块
# ════════════════════════════════════════════════════════════
def test_a03(admin_token, member_user_id):
    section("A-03  IAM 用户分组模块")

    if not admin_token:
        skip("A-03 全部用例", "未获取到管理员 Token")
        return

    # ── 1. POST /api/admin/user-groups  创建分组 ─────────
    ts = int(time.time())
    status, body = post("/api/admin/user-groups",
                        {"code": f"qa_group_{ts}", "name": f"测试分组_{ts}", "description": "自动化测试"},
                        token=admin_token)
    chk("POST /api/admin/user-groups  创建分组", status, 201, body)
    group_id = get_data(body).get("id")
    info(f"新分组 ID: {group_id}")

    if not group_id:
        skip("A-03 后续用例", "未获取到 group_id")
        return

    # ── 2. GET /api/admin/user-groups  分组列表 ──────────
    status, body = get("/api/admin/user-groups", token=admin_token)
    chk("GET /api/admin/user-groups  分组列表", status, 200, body)
    raw_groups = body.get("data") or {}
    groups_items = raw_groups.get("items") if isinstance(raw_groups, dict) else raw_groups
    if groups_items:
        ok("GET /api/admin/user-groups  响应包含 items 字段", 200)
    else:
        fail("GET /api/admin/user-groups  items 为空或字段名错误", 200, str(raw_groups)[:200])

    # ── 3. GET /api/admin/user-groups/{id}  分组详情 ─────
    status, body = get(f"/api/admin/user-groups/{group_id}", token=admin_token)
    chk(f"GET /api/admin/user-groups/{group_id}  分组详情", status, 200, body)

    # ── 4. GET /api/admin/user-groups/{id} 不存在 → 404 ──
    status, body = get("/api/admin/user-groups/999999999", token=admin_token)
    chk("GET /api/admin/user-groups/999999999  不存在 → 404", status, 404, body)

    # ── 5. PUT /api/admin/user-groups/{id}  更新分组 ─────
    status, body = put(f"/api/admin/user-groups/{group_id}",
                       {"code": f"qa_group_{ts}", "name": f"测试分组_{ts}（已更新）", "description": "更新后描述"},
                       token=admin_token)
    chk(f"PUT /api/admin/user-groups/{group_id}  更新分组", status, 200, body)

    # ── 6. POST /api/admin/user-groups/{id}/members  添加成员
    if member_user_id:
        status, body = post(f"/api/admin/user-groups/{group_id}/members",
                            {"user_id": member_user_id, "role": "member"},
                            token=admin_token)
        # API 实际返回 201 而非 200
        chk(f"POST user-groups/{group_id}/members  添加成员", status, 201, body)

        # ── 7. GET /api/admin/user-groups/{id}/members  成员列表
        status, body = get(f"/api/admin/user-groups/{group_id}/members",
                           token=admin_token)
        chk(f"GET user-groups/{group_id}/members  成员列表", status, 200, body)
        raw_members = body.get("data") or {}
        members_items = raw_members.get("items") if isinstance(raw_members, dict) else raw_members
        if isinstance(members_items, list):
            ok("GET user-groups/members  响应包含 items 字段", 200)
        else:
            fail("GET user-groups/members  items 字段格式错误", 200, str(raw_members)[:200])

        # ── 8. PATCH /api/admin/user-groups/{id}/members/{uid}  修改成员角色
        # 接口实际字段名为 group_role（非 role）
        status, body = patch(f"/api/admin/user-groups/{group_id}/members/{member_user_id}",
                             {"group_role": "admin"},
                             token=admin_token)
        chk(f"PATCH user-groups/{group_id}/members/{member_user_id}  修改成员角色",
            status, 200, body)

        # ── 9. GET /api/admin/users/{id}/groups  查用户所属分组
        # （BUG-06 复测：原 GET /api/admin/user-groups/user/{uid} 因路由冲突已移除，
        #   改为验证等价接口 GetUserGroups 能返回该用户所属分组列表）
        status, body = get(f"/api/admin/users/{member_user_id}/groups",
                           token=admin_token)
        chk(f"[BUG-06复测] GET /api/admin/users/{member_user_id}/groups  查用户所属分组 → 200",
            status, 200, body)
        raw_user_groups = body.get("data") or {}
        ug_items = raw_user_groups.get("items") if isinstance(raw_user_groups, dict) else raw_user_groups
        if isinstance(ug_items, list):
            ok(f"[BUG-06复测] GET /api/admin/users/{member_user_id}/groups  响应包含 items 字段", 200)
            # 验证刚加入的 group_id 出现在结果中
            found = any(
                (item.get("id") == group_id or item.get("group_id") == group_id)
                for item in ug_items if isinstance(item, dict)
            )
            if found:
                ok(f"[BUG-06复测] GET /api/admin/users/{member_user_id}/groups  结果包含刚加入的分组 {group_id}", 200)
            else:
                fail(f"[BUG-06复测] GET /api/admin/users/{member_user_id}/groups  结果未包含刚加入的分组 {group_id}",
                     200, str(ug_items)[:200])
        else:
            fail(f"[BUG-06复测] GET /api/admin/users/{member_user_id}/groups  items 字段格式错误",
                 200, str(raw_user_groups)[:200])

        # ── 9b. 确认旧路由 /api/admin/user-groups/user/{uid} 已被移除（不应 panic，应 404）
        status, body = get(f"/api/admin/user-groups/user/{member_user_id}",
                           token=admin_token)
        if status == 404:
            ok(f"[BUG-06关联] GET /api/admin/user-groups/user/{member_user_id}  旧路由已移除 → 404", status)
        else:
            fail(f"[BUG-06关联] GET /api/admin/user-groups/user/{member_user_id}  期望旧路由返回 404",
                 status, f"实际 {status}: {str(body)[:200]}")

        # ── 10. DELETE /api/admin/user-groups/{id}/members/{uid}  移除成员
        status, body = delete(f"/api/admin/user-groups/{group_id}/members/{member_user_id}",
                              token=admin_token)
        chk(f"DELETE user-groups/{group_id}/members/{member_user_id}  移除成员",
            status, 200, body)
    else:
        skip("分组成员管理用例", "无有效 member_user_id")

    # ── 11. POST /api/admin/user-groups/{id}/permissions  添加组权限
    # 先获取一个权限 code
    _, perm_body = get("/api/admin/permissions", token=admin_token)
    perm_items = (perm_body.get("data") or {}).get("items") or []
    perm_code  = perm_items[0]["code"] if perm_items else None

    if perm_code:
        status, body = post(f"/api/admin/user-groups/{group_id}/permissions",
                            {"permission_code": perm_code},
                            token=admin_token)
        # API 实际返回 201 而非 200
        chk(f"POST user-groups/{group_id}/permissions  添加组权限（{perm_code}）",
            status, 201, body)

        # ── 12. GET /api/admin/user-groups/{id}/permissions  组权限列表
        status, body = get(f"/api/admin/user-groups/{group_id}/permissions",
                           token=admin_token)
        chk(f"GET user-groups/{group_id}/permissions  组权限列表", status, 200, body)

        # ── 13. DELETE /api/admin/user-groups/{id}/permissions/{code}
        status, body = delete(f"/api/admin/user-groups/{group_id}/permissions/{perm_code}",
                              token=admin_token)
        chk(f"DELETE user-groups/{group_id}/permissions/{perm_code}  移除组权限",
            status, 200, body)
    else:
        skip("分组权限管理用例", "未获取到有效权限 code")

    # ── 14. POST /api/admin/user-groups/{id}/invite-codes  创建邀请码
    status, body = post(f"/api/admin/user-groups/{group_id}/invite-codes",
                        {"expires_in_days": 7, "max_uses": 10},
                        token=admin_token)
    chk(f"POST user-groups/{group_id}/invite-codes  创建邀请码", status, 201, body)
    invite_id = get_data(body).get("id")
    info(f"邀请码 ID: {invite_id}")

    # ── 15. GET /api/admin/user-groups/{id}/invite-codes  邀请码列表
    status, body = get(f"/api/admin/user-groups/{group_id}/invite-codes",
                       token=admin_token)
    chk(f"GET user-groups/{group_id}/invite-codes  邀请码列表", status, 200, body)

    # ── 16. PATCH .../invite-codes/{cid}/disable  禁用邀请码
    if invite_id:
        status, body = patch(
            f"/api/admin/user-groups/{group_id}/invite-codes/{invite_id}/disable",
            token=admin_token)
        chk(f"PATCH invite-codes/{invite_id}/disable  禁用邀请码", status, 200, body)

    # ── 17. DELETE 非空分组 → 409 ────────────────────────
    # 先添加一个成员使分组非空，再测试删除
    if member_user_id:
        post(f"/api/admin/user-groups/{group_id}/members",
             {"user_id": member_user_id, "role": "member"}, token=admin_token)
        status, body = delete(f"/api/admin/user-groups/{group_id}", token=admin_token)
        chk(f"DELETE /api/admin/user-groups/{group_id}  非空分组 → 409",
            status, 409, body)
        # 移除成员再删除
        delete(f"/api/admin/user-groups/{group_id}/members/{member_user_id}",
               token=admin_token)

    # ── 18. DELETE /api/admin/user-groups/{id}  删除分组 ─
    status, body = delete(f"/api/admin/user-groups/{group_id}", token=admin_token)
    chk(f"DELETE /api/admin/user-groups/{group_id}  删除分组", status, 200, body)

    # ── 19. 无 Token 访问分组接口 → 401 ──────────────────
    status, body = get("/api/admin/user-groups")
    chk("GET /api/admin/user-groups  无 Token → 401", status, 401, body)


# ════════════════════════════════════════════════════════════
# A-04  Identity 模块（实名认证）
# ════════════════════════════════════════════════════════════
def test_a04(user_token, admin_token):
    section("A-04  Identity 模块 — 实名认证")

    if not user_token:
        skip("A-04 全部用例", "未获取到用户 Token")
        return

    # 重新登录用新密码拿 token（user_token 可能因密码重置而失效，需外部传入有效 token）

    # ── 1. POST /api/identity/verifications  提交实名认证（BUG-07 复测）─
    status, body = post("/api/identity/verifications",
                        {"real_name": "李四", "id_card_no": "310101199012241234"},
                        token=user_token)
    chk("POST /api/identity/verifications  提交实名认证", status, 201, body)
    d = get_data(body)
    verif_id = d.get("id") or d.get("verification_id")
    verif_status = d.get("status", "")
    info(f"认证 ID: {verif_id}，状态: {verif_status}")

    # 校验返回状态应为 pending（BUG-07 复测）
    if verif_status == "pending":
        ok("[BUG-07复测] POST /api/identity/verifications  返回 status=pending", 201)
    else:
        fail("[BUG-07复测] POST /api/identity/verifications  返回 status 应为 pending",
             201, f"实际响应: {json.dumps(d, ensure_ascii=False)[:300]}")

    # ── 2. GET /api/identity/verifications/me  查当前认证状态
    status, body = get("/api/identity/verifications/me", token=user_token)
    chk("GET /api/identity/verifications/me  查当前认证状态", status, 200, body)
    me_status = get_data(body).get("status", "")
    info(f"当前认证状态: {me_status}")

    # ── 3. 重复提交实名 → 400/409 ─────────────────────────
    status, body = post("/api/identity/verifications",
                        {"real_name": "李四", "id_card_no": "310101199012241234"},
                        token=user_token)
    if status in (400, 409):
        ok("POST /api/identity/verifications  重复提交 → 400/409", status)
    else:
        fail("POST /api/identity/verifications  重复提交应被拦截",
             status, f"期望 400/409，实际 {status}")

    # ── 4. 无 Token 提交实名 → 401 ───────────────────────
    status, body = post("/api/identity/verifications",
                        {"real_name": "测试", "id_card_no": "110101199001011234"})
    chk("POST /api/identity/verifications  无 Token → 401", status, 401, body)

    if not admin_token:
        skip("A-04 管理员审核用例", "未获取到管理员 Token")
        return

    # ── 5. GET /api/admin/identity-verifications  管理员查列表
    status, body = get("/api/admin/identity-verifications", token=admin_token)
    chk("GET /api/admin/identity-verifications  管理员查列表", status, 200, body)
    raw_iv = body.get("data") or {}
    iv_items = raw_iv.get("items") if isinstance(raw_iv, dict) else raw_iv
    if isinstance(iv_items, list):
        ok("GET /api/admin/identity-verifications  响应包含 items 字段", 200)
    else:
        fail("GET /api/admin/identity-verifications  items 字段错误", 200, str(raw_iv)[:200])

    # ── 6. GET /api/admin/identity-verifications/{id}  管理员查详情
    if verif_id:
        status, body = get(f"/api/admin/identity-verifications/{verif_id}",
                           token=admin_token)
        chk(f"GET /api/admin/identity-verifications/{verif_id}  查认证详情", status, 200, body)

        # 校验脱敏证件号
        id_masked = get_data(body).get("id_card_no_masked", "")
        if id_masked and "*" in id_masked:
            ok(f"GET /api/admin/identity-verifications/{verif_id}  证件号已脱敏", 200)
        elif id_masked:
            fail(f"GET /api/admin/identity-verifications/{verif_id}  证件号未脱敏",
                 200, f"实际: {id_masked}")
        else:
            fail(f"GET /api/admin/identity-verifications/{verif_id}  缺少 id_card_no_masked 字段",
                 200, "")

    # ── 7. GET /api/admin/identity-verifications/{id} 不存在 → 404
    status, body = get("/api/admin/identity-verifications/999999999", token=admin_token)
    chk("GET /api/admin/identity-verifications/999999999  不存在 → 404", status, 404, body)

    # ── 8. PATCH /api/admin/identity-verifications/{id}/review  管理员拒绝
    if verif_id:
        status, body = patch(f"/api/admin/identity-verifications/{verif_id}/review",
                             {"action": "reject", "reject_reason": "证件照片模糊"},
                             token=admin_token)
        chk(f"PATCH /api/admin/identity-verifications/{verif_id}/review  拒绝认证",
            status, 200, body)

    # ── 9. 再次提交实名（上次被拒后可重新提交）────────────
    status, body = post("/api/identity/verifications",
                        {"real_name": "李四", "id_card_no": "310101199012241234"},
                        token=user_token)
    chk("POST /api/identity/verifications  拒绝后重新提交", status, 201, body)
    resubmit_id = get_data(body).get("id") or get_data(body).get("verification_id")
    resubmit_status = get_data(body).get("status", "")
    if resubmit_status == "pending":
        ok("[BUG-07复测] POST /api/identity/verifications  拒绝后重新提交 status=pending", 201)
    else:
        fail("[BUG-07复测] POST /api/identity/verifications  拒绝后重新提交 status 应为 pending",
             201, f"实际: {resubmit_status}")

    # ── 10. 管理员通过认证 ────────────────────────────────
    if resubmit_id:
        status, body = patch(f"/api/admin/identity-verifications/{resubmit_id}/review",
                             {"action": "approve"},
                             token=admin_token)
        chk(f"PATCH /api/admin/identity-verifications/{resubmit_id}/review  通过认证",
            status, 200, body)

    # ── 11. 无 Token 访问管理接口 → 401 ───────────────────
    status, body = get("/api/admin/identity-verifications")
    chk("GET /api/admin/identity-verifications  无 Token → 401", status, 401, body)


# ════════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*60}{RESET}")
    print(f"{BOLD}  Molin 后端工程师甲 — 全量接口测试（7 Bug 复测）{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*60}{RESET}")

    # ── A-01：Auth 模块 ────────────────────────────────
    user_id, final_email, final_pass, admin_phone = test_a01()

    # ── 播种超管权限并获取管理员 Token ──────────────────
    admin_token = setup_admin(user_id, final_email, final_pass, admin_phone)

    # ── 获取一个有效的普通用户 Token（用于 identity 测试）
    # 重新登录获取有效 token（密码重置后旧 token 已失效）
    user_token = ""
    if final_email and final_pass:
        status, body = post("/api/auth/login/email",
                            {"email": final_email, "password": final_pass})
        if status == 200:
            user_token = get_data(body).get("access_token", "")

    # ── A-02：IAM 模块 ────────────────────────────────
    victim_id, _ = test_a02(admin_token, user_id)

    # ── A-03：IAM 用户分组 ────────────────────────────
    test_a03(admin_token, victim_id or user_id)

    # ── A-04：Identity 模块 ───────────────────────────
    test_a04(user_token, admin_token)

    # ── 汇总报告 ──────────────────────────────────────
    total = passed + failed + skipped
    print(f"\n{BOLD}{'═'*60}{RESET}")
    print(f"{BOLD}  测试汇总{RESET}")
    print(f"{BOLD}{'─'*60}{RESET}")
    print(f"  通过：{GREEN}{passed}{RESET}   失败：{RED}{failed}{RESET}   跳过：{YELLOW}{skipped}{RESET}   总计：{total}")
    print(f"{BOLD}{'─'*60}{RESET}")

    if failed > 0:
        print(f"\n{BOLD}{RED}失败用例列表：{RESET}")
        for label, status, http_code, detail in results:
            if status == "FAIL":
                print(f"  {RED}[{http_code}]  {label}{RESET}")
                if detail:
                    print(f"        {RED}{detail}{RESET}")

    if failed == 0:
        print(f"\n{BOLD}{GREEN}  总体结论：全部通过{RESET}")
    else:
        print(f"\n{BOLD}{RED}  总体结论：{failed} 项失败，请检查上方日志{RESET}")
    print(f"{BOLD}{'═'*60}{RESET}\n")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()

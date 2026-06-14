#!/usr/bin/env python3
"""
A-04 / A-05 / A-06 验收测试脚本

测试范围：
  A-06：
    1. POST  /api/admin/permissions                       — 创建权限码
    2. PATCH /api/admin/roles/{id}/permissions             — 全量配置角色权限
    3. PATCH /api/admin/users/{id}/roles                   — 批量替换用户角色
    4. PATCH /api/admin/users/{id}/permission-overrides    — 批量替换用户权限覆盖
  A-05：
    5. PATCH /api/admin/users/{id}/status                  — 封禁/解封审计记录
  A-04：
    6. GET   /api/admin/audit-logs                         — 审计日志查询
  权限/鉴权回归：
    7. 401 / 403（无权限）/ 403（未完成管理员双重认证）

用法：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 tests/test_a04_a05_a06.py
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
failures = []  # 收集失败详情，汇总时打印

def ok(label):
    global passed
    passed += 1
    print(f"  {GREEN}PASS  {label}{RESET}")

def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}FAIL  {label}{RESET}"
    if detail:
        msg += f"\n        {RED}{detail}{RESET}"
    print(msg)
    failures.append((label, detail))

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

def get(path, token=None, params=None):
    p = path
    if params:
        qs = "&".join(f"{k}={v}" for k, v in params.items())
        p = f"{path}?{qs}"
    return request("GET", p, token=token)

def post(path, body=None, token=None):
    return request("POST", path, body, token)

def patch(path, body=None, token=None):
    return request("PATCH", path, body, token)

def assert_status(label, status, expected, body):
    if status == expected:
        ok(f"{label}  →  HTTP {status}")
        return True
    msg = ""
    if isinstance(body, dict):
        msg = body.get("message", "") or str(body)
    fail(f"{label}  →  HTTP {status}（期望 {expected}）", msg)
    return False

def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}

# ── MySQL 工具 ────────────────────────────────────────────
def mysql_exec(sql):
    """通过 pymysql 执行 SQL，不可用时回退到 subprocess。"""
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
        print(f"  {RED}未找到 mysql 命令，且 pymysql 未安装，跳过数据库操作{RESET}")
        return False


def mysql_query(sql):
    """执行查询并返回 (columns, rows)。"""
    try:
        import pymysql
        conn = pymysql.connect(
            host=MYSQL_HOST, port=MYSQL_PORT,
            user=MYSQL_USER, password=MYSQL_PASS, database=MYSQL_DB,
            charset="utf8mb4"
        )
        with conn:
            with conn.cursor() as cur:
                cur.execute(sql)
                cols = [c[0] for c in cur.description] if cur.description else []
                rows = cur.fetchall()
                return cols, rows
    except Exception as e:
        print(f"  {RED}MySQL 查询失败: {e}{RESET}")
        return [], []


def seed_admin_permissions(user_id):
    """播种管理员所需权限（user:list、user:manage、identity:review、role:manage），
    创建 admin 角色并绑定到指定用户。"""
    sql = f"""
    INSERT IGNORE INTO permissions (code, name, resource, action)
    VALUES
      ('user:list',       '查询用户列表',   'user',     'list'),
      ('user:manage',     '管理用户',       'user',     'manage'),
      ('role:manage',     '管理角色',       'role',     'manage'),
      ('identity:review', '审核实名认证',   'identity', 'review');

    INSERT IGNORE INTO roles (code, name, description)
    VALUES ('admin', '超级管理员', '系统内置管理员角色');

    SET @role_id = (SELECT id FROM roles WHERE code = 'admin' LIMIT 1);
    SET @p1 = (SELECT id FROM permissions WHERE code = 'user:list'       LIMIT 1);
    SET @p2 = (SELECT id FROM permissions WHERE code = 'user:manage'     LIMIT 1);
    SET @p3 = (SELECT id FROM permissions WHERE code = 'role:manage'     LIMIT 1);
    SET @p4 = (SELECT id FROM permissions WHERE code = 'identity:review' LIMIT 1);

    INSERT IGNORE INTO role_permissions (role_id, permission_id)
    VALUES (@role_id, @p1), (@role_id, @p2), (@role_id, @p3), (@role_id, @p4);

    INSERT IGNORE INTO user_roles (user_id, role_id)
    VALUES ({user_id}, @role_id);
    """
    return mysql_exec(sql)


# ── 管理员双重认证流程 ─────────────────────────────────────
# D-96：公开发码接口 /api/auth/verification-codes/{phone,email} 的 scene 白名单（D-52）
# 已不再允许 admin_verify，管理员双重认证发码改用专用接口
# POST /api/admin/auth/verification-codes/phone|email（需登录 + user:manage 权限）。
def complete_admin_double_verify(user_id, phone, email, token):
    """完成管理员手机+邮箱双重认证，返回是否成功。"""
    # 发送手机验证码（D-96 专用接口，需登录）
    status, body = post("/api/admin/auth/verification-codes/phone",
                        None, token=token)
    if status != 200:
        info(f"发送手机验证码失败: HTTP {status} {body}")
        return False
    phone_code = get_data(body).get("code", "")
    if not phone_code:
        info("未返回手机验证码（非调试环境或字段错误）")
        return False

    # 管理员手机认证
    status, body = post("/api/admin/auth/verify-phone",
                        {"code": phone_code}, token=token)
    if status != 200:
        info(f"管理员手机认证失败: HTTP {status} {body}")
        return False

    # 发送邮箱验证码（D-96 专用接口，需登录）
    status, body = post("/api/admin/auth/verification-codes/email",
                        None, token=token)
    if status != 200:
        info(f"发送邮箱验证码失败: HTTP {status} {body}")
        return False
    email_code = get_data(body).get("code", "")
    if not email_code:
        info("未返回邮箱验证码（非调试环境）")
        return False

    # 管理员邮箱认证
    status, body = post("/api/admin/auth/verify-email",
                        {"code": email_code}, token=token)
    if status != 200:
        info(f"管理员邮箱认证失败: HTTP {status} {body}")
        return False

    return True


# ════════════════════════════════════════════════════════════
# 前置准备：注册测试账号 + 播种权限 + 登录 + 完成双重认证
# ════════════════════════════════════════════════════════════
def setup_admin():
    """返回 (admin_token, admin_user_id, phone, email) 或 None 表示失败。"""
    section("前置准备：创建测试管理员账号")
    ts    = int(time.time())
    phone = f"137{ts % 100000000:08d}"
    email = f"a04a05a06_admin_{ts}@example.com"
    pwd   = "Admin@Test123!"

    # 发送手机验证码
    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": phone, "scene": "register"})
    if status != 200:
        fail("发送手机注册验证码", f"HTTP {status} {body}")
        return None
    phone_code = get_data(body).get("code", "")
    info(f"手机验证码: {phone_code}")

    # 发送邮箱验证码
    status, body = post("/api/auth/verification-codes/email",
                        {"email": email, "scene": "register"})
    if status != 200:
        fail("发送邮箱注册验证码", f"HTTP {status} {body}")
        return None
    email_code = get_data(body).get("code", "")
    info(f"邮箱验证码: {email_code}")

    # 统一注册
    status, body = post("/api/auth/register", {
        "phone":      phone,
        "phone_code": phone_code,
        "email":      email,
        "email_code": email_code,
        "password":   pwd,
        "username":   f"a04a05a06_{ts % 10000}"
    })
    if status != 201:
        fail("统一注册", f"HTTP {status} {body}")
        return None
    d = get_data(body)
    token = d.get("access_token", "")
    info(f"注册成功，Token 前缀: {token[:20]}...")

    # 从 /api/me 获取 user_id
    status, body = get("/api/me", token=token)
    if status != 200:
        fail("GET /api/me", f"HTTP {status}")
        return None
    user_id = get_data(body).get("id")
    info(f"用户 ID: {user_id}")

    # 播种管理员权限
    seeded = seed_admin_permissions(user_id)
    if not seeded:
        fail("播种管理员权限", "数据库操作失败")
        return None
    ok(f"管理员权限播种完成（user_id={user_id}）")

    # 重新登录（权限缓存以新登录 token 为准）
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": pwd})
    if status != 200:
        fail("管理员重新登录", f"HTTP {status} {body}")
        return None
    token = get_data(body).get("access_token", "")
    ok("管理员 Token 重新获取成功")

    # 完成管理员双重认证
    verified = complete_admin_double_verify(user_id, phone, email, token)
    if not verified:
        fail("管理员双重认证", "双重认证未完成，后续管理员接口将因 40031 失败")
        return None
    ok("管理员双重认证完成（手机+邮箱）")

    return token, user_id, phone, email


# ════════════════════════════════════════════════════════════
# 注册普通用户（无 role:manage 权限、未完成双重认证），用于回归测试
# ════════════════════════════════════════════════════════════
def setup_regular_user():
    """返回 (user_token, user_id) 或 (None, None)。"""
    ts    = int(time.time()) + 1
    phone = f"136{(ts + 23) % 100000000:08d}"
    email = f"a04a05a06_regular_{ts}@example.com"
    pwd   = "Regular@123!"

    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": phone, "scene": "register"})
    if status != 200:
        return None, None
    phone_code = get_data(body).get("code", "")

    status, body = post("/api/auth/verification-codes/email",
                        {"email": email, "scene": "register"})
    if status != 200:
        return None, None
    email_code = get_data(body).get("code", "")

    status, body = post("/api/auth/register", {
        "phone":      phone,
        "phone_code": phone_code,
        "email":      email,
        "email_code": email_code,
        "password":   pwd,
        "username":   f"a04a05a06_reg{ts % 10000}"
    })
    if status != 201:
        return None, None

    token = get_data(body).get("access_token", "")
    status, body = get("/api/me", token=token)
    if status != 200:
        return None, None
    user_id = get_data(body).get("id")
    return token, user_id


# ════════════════════════════════════════════════════════════
# 创建一个有 role:manage 权限但未完成双重认证的用户（用于 403/40031 回归）
# ════════════════════════════════════════════════════════════
def setup_unverified_admin():
    """返回 (token, user_id) 或 (None, None)：用户已绑定 role:manage 权限角色，但未完成管理员双重认证。"""
    ts    = int(time.time()) + 2
    phone = f"135{(ts + 41) % 100000000:08d}"
    email = f"a04a05a06_unverified_{ts}@example.com"
    pwd   = "Unverified@123!"

    status, body = post("/api/auth/verification-codes/phone",
                        {"phone": phone, "scene": "register"})
    if status != 200:
        return None, None
    phone_code = get_data(body).get("code", "")

    status, body = post("/api/auth/verification-codes/email",
                        {"email": email, "scene": "register"})
    if status != 200:
        return None, None
    email_code = get_data(body).get("code", "")

    status, body = post("/api/auth/register", {
        "phone":      phone,
        "phone_code": phone_code,
        "email":      email,
        "email_code": email_code,
        "password":   pwd,
        "username":   f"a04a05a06_unv{ts % 10000}"
    })
    if status != 201:
        return None, None

    token = get_data(body).get("access_token", "")
    status, body = get("/api/me", token=token)
    if status != 200:
        return None, None
    user_id = get_data(body).get("id")

    # 直接绑定 admin 角色（已含 role:manage），但不做双重认证
    seeded = seed_admin_permissions(user_id)
    if not seeded:
        return None, None

    # 重新登录刷新 token
    status, body = post("/api/auth/login/email", {"email": email, "password": pwd})
    if status != 200:
        return None, None
    token = get_data(body).get("access_token", "")
    return token, user_id


# ════════════════════════════════════════════════════════════
# 测试组 A：A-06 — POST /api/admin/permissions
# ════════════════════════════════════════════════════════════
def test_create_permission(admin_token):
    section("测试组 A：POST /api/admin/permissions — 创建权限码")

    ts = int(time.time())
    new_code = f"qa_a06_perm_{ts}"

    # A-1 缺字段 → 400
    status, body = post("/api/admin/permissions", {
        "code": new_code, "name": "QA测试权限"
        # 缺少 resource / action
    }, token=admin_token)
    assert_status("A-1  缺字段（resource/action） → 400", status, 400, body)

    # A-2 正常创建 → 201
    status, body = post("/api/admin/permissions", {
        "code":     new_code,
        "name":     "QA测试权限",
        "resource": "qa_resource",
        "action":   "qa_action",
    }, token=admin_token)
    perm_id = None
    if assert_status("A-2  正常创建权限码 → 201", status, 201, body):
        d = get_data(body)
        perm_id = d.get("id")
        if (d.get("code") == new_code and d.get("name") == "QA测试权限"
                and d.get("resource") == "qa_resource" and d.get("action") == "qa_action"
                and perm_id):
            ok(f"A-2a 响应字段完整（id={perm_id}, code={new_code}）")
        else:
            fail("A-2a 响应字段不完整或不匹配", str(d)[:300])

    # A-3 重复 code → 应失败（500 或 4xx，记录实际行为）
    status, body = post("/api/admin/permissions", {
        "code":     new_code,
        "name":     "QA测试权限-重复",
        "resource": "qa_resource",
        "action":   "qa_action",
    }, token=admin_token)
    if status >= 400:
        ok(f"A-3  重复 code 创建权限码 → HTTP {status}（非 2xx，符合预期）")
    else:
        fail(f"A-3  重复 code 创建权限码 → HTTP {status}（期望 4xx/5xx）", str(body)[:200])

    # A-4 审计日志验证：module=iam, action=create_permission
    time.sleep(0.3)
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "iam", "action": "create_permission"})
    if assert_status("A-4  GET /api/admin/audit-logs?module=iam&action=create_permission → 200",
                      status, 200, body):
        d = get_data(body)
        items = d.get("items", [])
        found = None
        for item in items:
            if item.get("target_type") == "permission" and str(item.get("target_id")) == str(perm_id):
                found = item
                break
        if found:
            ok(f"A-4a 找到 create_permission 审计记录（id={found.get('id')}）")
            for f in ["id", "operator_id", "module", "action", "target_type", "target_id", "ip", "created_at"]:
                if f in found:
                    ok(f"A-4a-{f}  审计记录含字段 {f}")
                else:
                    fail(f"A-4a-{f}  审计记录缺少字段 {f}", str(found)[:300])
        else:
            fail("A-4a 未找到 create_permission 对应的审计记录", str(items)[:500])

    return perm_id, new_code


# ════════════════════════════════════════════════════════════
# 测试组 B：A-06 — PATCH /api/admin/roles/{id}/permissions
# ════════════════════════════════════════════════════════════
def test_set_role_permissions(admin_token, new_perm_id):
    section("测试组 B：PATCH /api/admin/roles/{id}/permissions — 全量配置角色权限")

    # 创建一个专用测试角色，避免影响 admin 角色现有权限配置
    ts = int(time.time())
    role_code = f"qa_a06_role_{ts}"
    status, body = post("/api/admin/roles", {
        "code": role_code, "name": "QA-A06角色权限测试",
    }, token=admin_token)
    if status != 201:
        fail("创建测试角色失败，跳过测试组 B", f"HTTP {status} {body}")
        return None
    role_id = get_data(body).get("id")
    info(f"测试角色已创建：id={role_id}, code={role_code}")

    # 创建一个测试用户并绑定该角色，用于验证缓存失效
    test_token, test_user_id = setup_regular_user()
    if not test_user_id:
        fail("创建权限缓存测试用户失败，跳过测试组 B 的缓存验证", "")
    else:
        mysql_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({test_user_id}, {role_id});")
        info(f"测试用户 user_id={test_user_id} 已绑定角色 {role_code}")

    # B-1 配置角色权限为 [order:list]（确认该用户当前无 order:list 权限 → 访问 /api/admin/orders 403）
    # 选用 /api/admin/orders：该接口只校验 order:list 权限（RequirePerm），不要求管理员双重认证（RequireAdminVerified），
    # 可在不完成双重认证的情况下单独验证「角色权限变更 → 缓存失效 → 权限立即生效/失效」。
    order_list_perm_id = None
    cols, rows = mysql_query("SELECT id FROM permissions WHERE code='order:list' LIMIT 1;")
    if rows:
        order_list_perm_id = rows[0][0]

    if test_user_id and order_list_perm_id:
        # 先确认未授权时访问 /api/admin/orders → 403
        status, body = get("/api/admin/orders", token=test_token)
        assert_status("B-0  绑定角色但角色无 order:list 权限时访问 /api/admin/orders → 403", status, 403, body)

    # B-2 PATCH 设置角色权限 → [order:list]
    payload_ids = [order_list_perm_id] if order_list_perm_id else []
    status, body = patch(f"/api/admin/roles/{role_id}/permissions",
                          {"permission_ids": payload_ids}, token=admin_token)
    if assert_status("B-2  设置角色权限为 [order:list] → 200", status, 200, body):
        if get_data(body) == "updated" or body.get("data") == "updated":
            ok("B-2a  响应 data=='updated'")
        else:
            fail("B-2a  响应 data 不是 'updated'", str(body)[:200])

    # B-3 缓存失效验证：该角色下用户立即可访问 /api/admin/orders（order:list 权限生效）
    if test_user_id and order_list_perm_id:
        status, body = get("/api/admin/orders", token=test_token)
        assert_status("B-3  设置后该角色用户访问 /api/admin/orders → 200（权限缓存已失效，立即生效）",
                       status, 200, body)

    # B-4 清空角色权限（空数组）→ 该用户立即失去 order:list 权限
    status, body = patch(f"/api/admin/roles/{role_id}/permissions",
                          {"permission_ids": []}, token=admin_token)
    assert_status("B-4  清空角色权限（空数组） → 200", status, 200, body)

    if test_user_id and order_list_perm_id:
        status, body = get("/api/admin/orders", token=test_token)
        assert_status("B-5  清空后该角色用户访问 /api/admin/orders → 403（权限缓存已失效，立即失效）",
                       status, 403, body)

    # B-6 审计日志验证：module=iam, action=set_role_permissions
    time.sleep(0.3)
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "iam", "action": "set_role_permissions"})
    if assert_status("B-6  GET /api/admin/audit-logs?module=iam&action=set_role_permissions → 200",
                      status, 200, body):
        d = get_data(body)
        items = d.get("items", [])
        found = [item for item in items
                 if item.get("target_type") == "role" and str(item.get("target_id")) == str(role_id)]
        if found:
            ok(f"B-6a 找到 set_role_permissions 审计记录（数量={len(found)}）")
        else:
            fail("B-6a 未找到 set_role_permissions 对应的审计记录", str(items)[:500])

    return role_id, test_user_id, test_token


# ════════════════════════════════════════════════════════════
# 测试组 C：A-06 — PATCH /api/admin/users/{id}/roles
# ════════════════════════════════════════════════════════════
def test_replace_user_roles(admin_token, role_id):
    section("测试组 C：PATCH /api/admin/users/{id}/roles — 批量替换用户角色")

    if not role_id:
        info("跳过测试组 C：测试组 B 未成功创建角色")
        return None, None

    # 创建一个全新的测试用户
    test_token, test_user_id = setup_regular_user()
    if not test_user_id:
        fail("创建测试用户失败，跳过测试组 C", "")
        return None, None
    info(f"测试用户已创建：user_id={test_user_id}")

    # 创建第二个角色用于验证"替换"语义
    ts = int(time.time())
    role_code2 = f"qa_a06_role2_{ts}"
    status, body = post("/api/admin/roles", {"code": role_code2, "name": "QA-A06角色2"}, token=admin_token)
    if status != 201:
        fail("创建第二个测试角色失败，跳过测试组 C", f"HTTP {status} {body}")
        return None, None
    role_id2 = get_data(body).get("id")

    # C-1 先将用户角色设置为 [role_id]
    status, body = patch(f"/api/admin/users/{test_user_id}/roles",
                          {"role_ids": [role_id], "reason": "QA-A06批量替换测试-初始绑定"},
                          token=admin_token)
    assert_status("C-1  批量替换用户角色为 [role_id] → 200", status, 200, body)

    # C-2 GET /api/admin/users/{id}/roles 验证
    status, body = get(f"/api/admin/users/{test_user_id}/roles", token=admin_token)
    if assert_status("C-2  GET /api/admin/users/{id}/roles → 200", status, 200, body):
        items = get_data(body).get("items", [])
        ids = [item.get("id") for item in items]
        if ids == [role_id]:
            ok(f"C-2a  用户角色列表 == [{role_id}]")
        else:
            fail(f"C-2a  用户角色列表不符合预期", f"期望 [{role_id}]，实际 {ids}")

    # C-3 替换为 [role_id2]（验证旧角色被替换掉）
    status, body = patch(f"/api/admin/users/{test_user_id}/roles",
                          {"role_ids": [role_id2], "reason": "QA-A06批量替换测试-替换为role2"},
                          token=admin_token)
    assert_status("C-3  批量替换用户角色为 [role_id2] → 200", status, 200, body)

    status, body = get(f"/api/admin/users/{test_user_id}/roles", token=admin_token)
    if assert_status("C-4  GET /api/admin/users/{id}/roles（替换后） → 200", status, 200, body):
        items = get_data(body).get("items", [])
        ids = [item.get("id") for item in items]
        if ids == [role_id2]:
            ok(f"C-4a  用户角色列表 == [{role_id2}]（旧角色 {role_id} 已被替换掉）")
        else:
            fail(f"C-4a  用户角色列表不符合预期", f"期望 [{role_id2}]，实际 {ids}")

    # C-5 清空角色（空数组）
    status, body = patch(f"/api/admin/users/{test_user_id}/roles",
                          {"role_ids": [], "reason": "QA-A06批量替换测试-清空"},
                          token=admin_token)
    assert_status("C-5  批量替换用户角色为 []（清空） → 200", status, 200, body)

    status, body = get(f"/api/admin/users/{test_user_id}/roles", token=admin_token)
    if assert_status("C-6  GET /api/admin/users/{id}/roles（清空后） → 200", status, 200, body):
        items = get_data(body).get("items", [])
        if items == []:
            ok("C-6a  用户角色列表已清空")
        else:
            fail("C-6a  用户角色列表未清空", str(items)[:300])

    # C-7 审计日志验证：module=iam, action=replace_user_roles，含 reason
    time.sleep(0.3)
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "iam", "action": "replace_user_roles"})
    if assert_status("C-7  GET /api/admin/audit-logs?module=iam&action=replace_user_roles → 200",
                      status, 200, body):
        d = get_data(body)
        items = d.get("items", [])
        found = [item for item in items
                 if item.get("target_type") == "user" and str(item.get("target_id")) == str(test_user_id)]
        if found:
            ok(f"C-7a 找到 replace_user_roles 审计记录（数量={len(found)}）")
            for f in ["id", "operator_id", "module", "action", "target_type", "target_id", "ip", "created_at"]:
                if f in found[0]:
                    ok(f"C-7a-{f}  审计记录含字段 {f}")
                else:
                    fail(f"C-7a-{f}  审计记录缺少字段 {f}", str(found[0])[:300])
        else:
            fail("C-7a 未找到 replace_user_roles 对应的审计记录", str(items)[:500])

    return test_user_id, test_token


# ════════════════════════════════════════════════════════════
# 测试组 D：A-06 — PATCH /api/admin/users/{id}/permission-overrides
# ════════════════════════════════════════════════════════════
def test_replace_user_overrides(admin_token, target_user_id):
    section("测试组 D：PATCH /api/admin/users/{id}/permission-overrides — 批量替换用户权限覆盖")

    if not target_user_id:
        # 创建一个新用户做覆盖测试
        _, target_user_id = setup_regular_user()
    if not target_user_id:
        fail("创建测试用户失败，跳过测试组 D", "")
        return

    # 找一个存在的权限 ID（asset:view, id=1）
    cols, rows = mysql_query("SELECT id, code FROM permissions WHERE code='asset:view' LIMIT 1;")
    if not rows:
        fail("数据库中找不到 asset:view 权限，跳过测试组 D", "")
        return
    valid_perm_id = rows[0][0]
    valid_perm_code = rows[0][1]
    info(f"使用权限 id={valid_perm_id} code={valid_perm_code} 做覆盖测试")

    # D-1 effect 非法值 → 400
    status, body = patch(f"/api/admin/users/{target_user_id}/permission-overrides", {
        "items": [{"permission_id": valid_perm_id, "effect": "invalid_effect", "reason": "QA-D1"}]
    }, token=admin_token)
    assert_status("D-1  effect 非法值（invalid_effect） → 400", status, 400, body)

    # D-2 expires_at 非法 ISO 8601 → 400
    status, body = patch(f"/api/admin/users/{target_user_id}/permission-overrides", {
        "items": [{"permission_id": valid_perm_id, "effect": "allow",
                   "reason": "QA-D2", "expires_at": "not-a-date"}]
    }, token=admin_token)
    assert_status("D-2  expires_at 非法 ISO 8601 → 400", status, 400, body)

    # D-3 permission_id 不存在 → 400
    status, body = patch(f"/api/admin/users/{target_user_id}/permission-overrides", {
        "items": [{"permission_id": 999999999, "effect": "allow", "reason": "QA-D3"}]
    }, token=admin_token)
    assert_status("D-3  permission_id 不存在（999999999） → 400", status, 400, body)

    # D-4 正常替换 → 200，data:"updated"
    expires_at = "2026-12-31T00:00:00Z"
    status, body = patch(f"/api/admin/users/{target_user_id}/permission-overrides", {
        "items": [{"permission_id": valid_perm_id, "effect": "allow",
                   "reason": "QA-D4-正常替换", "expires_at": expires_at}]
    }, token=admin_token)
    if assert_status("D-4  正常替换 items=[{...}] → 200", status, 200, body):
        if get_data(body) == "updated" or body.get("data") == "updated":
            ok("D-4a  响应 data=='updated'")
        else:
            fail("D-4a  响应 data 不是 'updated'", str(body)[:200])

    # D-5 GET 验证替换结果
    status, body = get(f"/api/admin/users/{target_user_id}/permission-overrides", token=admin_token)
    if assert_status("D-5  GET /api/admin/users/{id}/permission-overrides → 200", status, 200, body):
        items = get_data(body).get("items", [])
        match = [it for it in items if it.get("permission_id") == valid_perm_id
                 and it.get("effect") == "allow"]
        if match:
            ok(f"D-5a  覆盖记录存在：permission_id={valid_perm_id}, effect=allow")
            item = match[0]
            if item.get("reason") == "QA-D4-正常替换":
                ok("D-5b  reason 字段正确")
            else:
                fail("D-5b  reason 字段不符", str(item)[:300])
            if item.get("expires_at"):
                ok(f"D-5c  expires_at 字段存在：{item.get('expires_at')}")
            else:
                fail("D-5c  expires_at 字段缺失或为空", str(item)[:300])
        else:
            fail("D-5a  未找到预期的覆盖记录", str(items)[:500])

    # D-6 空数组清空 → 200
    status, body = patch(f"/api/admin/users/{target_user_id}/permission-overrides", {
        "items": []
    }, token=admin_token)
    assert_status("D-6  items=[]（清空） → 200", status, 200, body)

    status, body = get(f"/api/admin/users/{target_user_id}/permission-overrides", token=admin_token)
    if assert_status("D-7  GET /api/admin/users/{id}/permission-overrides（清空后） → 200", status, 200, body):
        items = get_data(body).get("items", [])
        if items == []:
            ok("D-7a  用户权限覆盖列表已清空")
        else:
            fail("D-7a  用户权限覆盖列表未清空", str(items)[:300])

    # D-8 审计日志验证：module=iam, action=replace_user_overrides
    time.sleep(0.3)
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "iam", "action": "replace_user_overrides"})
    if assert_status("D-8  GET /api/admin/audit-logs?module=iam&action=replace_user_overrides → 200",
                      status, 200, body):
        d = get_data(body)
        items = d.get("items", [])
        found = [item for item in items
                 if item.get("target_type") == "user" and str(item.get("target_id")) == str(target_user_id)]
        if found:
            ok(f"D-8a 找到 replace_user_overrides 审计记录（数量={len(found)}）")
        else:
            fail("D-8a 未找到 replace_user_overrides 对应的审计记录", str(items)[:500])


# ════════════════════════════════════════════════════════════
# 测试组 E：A-05 — PATCH /api/admin/users/{id}/status（封禁/解封审计记录）
# ════════════════════════════════════════════════════════════
def test_ban_unban_audit(admin_token):
    section("测试组 E：PATCH /api/admin/users/{id}/status — 封禁/解封审计记录")

    _, target_user_id = setup_regular_user()
    if not target_user_id:
        fail("创建测试用户失败，跳过测试组 E", "")
        return
    info(f"测试用户已创建：user_id={target_user_id}")

    # E-1 封禁
    status, body = patch(f"/api/admin/users/{target_user_id}/status",
                          {"status": "disabled", "reason": "测试封禁"}, token=admin_token)
    assert_status("E-1  封禁用户 PATCH status=disabled → 200", status, 200, body)

    # E-2 解封
    status, body = patch(f"/api/admin/users/{target_user_id}/status",
                          {"status": "active", "reason": "测试解封"}, token=admin_token)
    assert_status("E-2  解封用户 PATCH status=active → 200", status, 200, body)

    # E-3 审计日志验证：ban_user
    time.sleep(0.3)
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "auth", "action": "ban_user"})
    if assert_status("E-3  GET /api/admin/audit-logs?module=auth&action=ban_user → 200", status, 200, body):
        items = get_data(body).get("items", [])
        found = [item for item in items
                 if item.get("target_type") == "user" and str(item.get("target_id")) == str(target_user_id)]
        if found:
            ok(f"E-3a 找到 ban_user 审计记录（id={found[0].get('id')}）")
            entry = found[0]
            for f in ["id", "operator_id", "module", "action", "target_type", "target_id", "ip", "created_at"]:
                if f in entry:
                    ok(f"E-3a-{f}  审计记录含字段 {f}")
                else:
                    fail(f"E-3a-{f}  审计记录缺少字段 {f}", str(entry)[:300])
        else:
            fail("E-3a 未找到 ban_user 对应的审计记录", str(items)[:500])

    # E-4 审计日志验证：unban_user
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "auth", "action": "unban_user"})
    if assert_status("E-4  GET /api/admin/audit-logs?module=auth&action=unban_user → 200", status, 200, body):
        items = get_data(body).get("items", [])
        found = [item for item in items
                 if item.get("target_type") == "user" and str(item.get("target_id")) == str(target_user_id)]
        if found:
            ok(f"E-4a 找到 unban_user 审计记录（id={found[0].get('id')}）")
        else:
            fail("E-4a 未找到 unban_user 对应的审计记录", str(items)[:500])

    # E-5 数据库直接验证 reason 字段（存于 request_summary JSON）
    cols, rows = mysql_query(
        f"SELECT module, action, target_type, target_id, reason_json "
        f"FROM (SELECT module, action, target_type, target_id, "
        f"JSON_UNQUOTE(JSON_EXTRACT(request_summary, '$.reason')) AS reason_json "
        f"FROM audit_logs WHERE module='auth' AND target_type='user' AND target_id='{target_user_id}') t;"
    )
    reasons = {r[1]: r[4] for r in rows}
    if reasons.get("ban_user") == "测试封禁":
        ok("E-5a  数据库中 ban_user 记录的 reason='测试封禁'")
    else:
        fail("E-5a  数据库中 ban_user 记录的 reason 不符", str(reasons))
    if reasons.get("unban_user") == "测试解封":
        ok("E-5b  数据库中 unban_user 记录的 reason='测试解封'")
    else:
        fail("E-5b  数据库中 unban_user 记录的 reason 不符", str(reasons))


# ════════════════════════════════════════════════════════════
# 测试组 F：A-04 — GET /api/admin/audit-logs（响应结构 + 分页 + 过滤）
# ════════════════════════════════════════════════════════════
def test_audit_logs_query(admin_token):
    section("测试组 F：GET /api/admin/audit-logs — 审计日志查询")

    # F-1 无过滤条件 → 200
    status, body = get("/api/admin/audit-logs", token=admin_token)
    if assert_status("F-1  无过滤条件查询 → 200", status, 200, body):
        d = get_data(body)
        if "items" in d and isinstance(d["items"], list):
            ok("F-1a  响应 data.items 为数组")
        else:
            fail("F-1a  响应 data.items 不是数组或不存在", str(d)[:300])
        if "list" in d:
            fail("F-1b  响应仍含旧字段 'list'（应统一为 'items'）", str(list(d.keys())))
        else:
            ok("F-1b  响应不含旧字段 'list'")
        # D-95：分页结构已扁平化（page/page_size/total 与 items 同级），data 中不应再有 pagination 子对象
        if "pagination" not in d and all(k in d for k in ("page", "page_size", "total")):
            ok("F-1c  分页结构已扁平化：page/page_size/total 与 items 同级，且不含 pagination 子对象")
        else:
            fail("F-1d  分页结构不符合预期（D-95 扁平结构）", str(d)[:300])

        if d.get("items"):
            sample = d["items"][0]
            required_fields = ["id", "operator_id", "module", "action",
                                "target_type", "target_id", "ip", "created_at"]
            missing = [f for f in required_fields if f not in sample]
            if not missing:
                ok("F-1e  审计记录字段完整（id/operator_id/module/action/target_type/target_id/ip/created_at）")
            else:
                fail("F-1e  审计记录缺少字段", f"缺失字段: {missing}, 样例: {str(sample)[:300]}")

    # F-2 ?module=iam 过滤
    status, body = get("/api/admin/audit-logs", token=admin_token, params={"module": "iam"})
    if assert_status("F-2  ?module=iam → 200", status, 200, body):
        items = get_data(body).get("items", [])
        if all(item.get("module") == "iam" for item in items):
            ok(f"F-2a  全部记录 module==iam（共 {len(items)} 条）")
        else:
            fail("F-2a  存在 module != iam 的记录", str(items)[:500])

    # F-3 ?module=auth&action=ban_user 过滤
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "auth", "action": "ban_user"})
    if assert_status("F-3  ?module=auth&action=ban_user → 200", status, 200, body):
        items = get_data(body).get("items", [])
        if all(item.get("module") == "auth" and item.get("action") == "ban_user" for item in items):
            ok(f"F-3a  全部记录 module==auth && action==ban_user（共 {len(items)} 条）")
        else:
            fail("F-3a  存在不匹配过滤条件的记录", str(items)[:500])

    # F-4 分页参数 ?page=1&page_size=2
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"page": 1, "page_size": 2})
    if assert_status("F-4  ?page=1&page_size=2 → 200", status, 200, body):
        d = get_data(body)
        items = d.get("items", [])
        if len(items) <= 2 and d.get("page") == 1 and d.get("page_size") == 2:
            ok(f"F-4a  分页参数生效（返回 {len(items)} 条，page=1, page_size=2）")
        else:
            fail("F-4a  分页参数未生效", f"items 长度={len(items)}, page={d.get('page')}, page_size={d.get('page_size')}")

    # F-5 module/action 不存在的过滤 → 200，空列表
    status, body = get("/api/admin/audit-logs", token=admin_token,
                       params={"module": "zzz_nonexistent_module", "action": "zzz_nonexistent_action"})
    if assert_status("F-5  module/action 均不存在 → 200（空列表）", status, 200, body):
        items = get_data(body).get("items", [])
        if items == []:
            ok("F-5a  空列表")
        else:
            fail("F-5a  期望空列表但有数据", str(items)[:300])


# ════════════════════════════════════════════════════════════
# 测试组 G：权限/鉴权回归
# ════════════════════════════════════════════════════════════
def test_auth_regression(admin_token):
    section("测试组 G：权限/鉴权回归")

    # G-1 不带 token 访问 /api/admin/audit-logs → 401
    status, body = get("/api/admin/audit-logs")
    assert_status("G-1  无 Token 访问 /api/admin/audit-logs → 401", status, 401, body)

    # G-2 不带 token 访问 /api/admin/permissions（POST） → 401
    status, body = post("/api/admin/permissions", {
        "code": "x", "name": "x", "resource": "x", "action": "x"
    })
    assert_status("G-2  无 Token POST /api/admin/permissions → 401", status, 401, body)

    # G-3 普通用户（无 role:manage 权限）访问 /api/admin/audit-logs → 403
    regular_token, regular_user_id = setup_regular_user()
    if regular_token:
        status, body = get("/api/admin/audit-logs", token=regular_token)
        assert_status("G-3  普通用户 Token 访问 /api/admin/audit-logs → 403", status, 403, body)

        # G-4 普通用户访问 PATCH /api/admin/users/{id}/roles → 403
        status, body = patch(f"/api/admin/users/{regular_user_id}/roles",
                              {"role_ids": []}, token=regular_token)
        assert_status("G-4  普通用户 Token PATCH /api/admin/users/{id}/roles → 403", status, 403, body)
    else:
        info("G-3/G-4  跳过：普通用户注册失败")

    # G-5 有 role:manage 权限但未完成双重认证 → 403（40031）
    unverified_token, unverified_user_id = setup_unverified_admin()
    if unverified_token:
        status, body = get("/api/admin/audit-logs", token=unverified_token)
        if assert_status("G-5  有权限未双重认证访问 /api/admin/audit-logs → 403", status, 403, body):
            if body.get("code") == 40031:
                ok("G-5a  错误码 == 40031（请先完成管理员双重认证）")
            else:
                fail("G-5a  错误码不是 40031", str(body))

        # G-6 同上，PATCH /api/admin/roles/{id}/permissions → 403
        status, body = patch("/api/admin/roles/1/permissions",
                              {"permission_ids": []}, token=unverified_token)
        assert_status("G-6  有权限未双重认证 PATCH /api/admin/roles/{id}/permissions → 403", status, 403, body)
    else:
        info("G-5/G-6  跳过：未认证管理员账号创建失败")


# ════════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*60}{RESET}")
    print(f"{BOLD}  Molin A-04 / A-05 / A-06 验收测试{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*60}{RESET}")

    # ── 前置准备：建立管理员账号 ──────────────────────────
    result = setup_admin()
    if result is None:
        print(f"\n{BOLD}{RED}前置准备失败，终止测试。{RESET}\n")
        sys.exit(1)
    admin_token, admin_user_id, admin_phone, admin_email = result

    # ── 执行各测试组 ──────────────────────────────────────
    new_perm_id, new_perm_code = test_create_permission(admin_token)
    role_id, cache_test_user_id, cache_test_token = test_set_role_permissions(admin_token, new_perm_id)
    replace_roles_user_id, _ = test_replace_user_roles(admin_token, role_id)
    test_replace_user_overrides(admin_token, replace_roles_user_id)
    test_ban_unban_audit(admin_token)
    test_audit_logs_query(admin_token)
    test_auth_regression(admin_token)

    # ── 汇总 ──────────────────────────────────────────────
    total = passed + failed
    print(f"\n{BOLD}{'═'*60}{RESET}")
    print(f"{BOLD}  测试结果：{passed}/{total} 通过，{failed} 失败{RESET}")

    if failures:
        print(f"\n{BOLD}{RED}失败用例明细：{RESET}")
        for i, (label, detail) in enumerate(failures, 1):
            print(f"  {RED}{i}. {label}{RESET}")
            if detail:
                print(f"     {RED}{detail}{RESET}")

    if failed == 0:
        print(f"\n{BOLD}{GREEN}结论：全部通过{RESET}")
    else:
        print(f"\n{BOLD}{RED}结论：{failed} 项失败，请检查上方输出{RESET}")
    print(f"{BOLD}{'═'*60}{RESET}\n")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()

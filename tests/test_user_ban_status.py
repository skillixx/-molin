#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
"用户权限被禁用后无法访问应用" 验收用例 —— 复测脚本
（针对 PATCH /api/admin/users/{id}/status 接口，commit 32645e0/37642e1）

自包含测试方案：全程注册新账号 + SQL 直接绑定角色权限，不登录任何已存在账号。

覆盖点：
  1. 管理员调用 PATCH /api/admin/users/{id}/status，status=disabled 封禁普通用户
  2. 验证：被封禁用户立即无法访问需要登录的接口（/api/me、/api/products），返回 401/40101
  3. 验证：被封禁用户的 refresh token 立即失效（POST /api/auth/refresh 返回 401）
  4. 管理员调用 status=active 解封，验证用户恢复正常访问
  5. 验证非管理员调用该接口被拦截（403/40003）
  6. 验证非法 status 取值返回 400/40000

执行方式（在测试服务器上）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/test_user_ban_status.py
"""

import os
import sys
import json
import time
import urllib.request
import urllib.error
import urllib.parse
import subprocess

API_BASE   = os.getenv("API_BASE", "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

PASS_COUNT = 0
FAIL_COUNT = 0
FAIL_LIST  = []


def _c(code, text):
    return f"\033[{code}m{text}\033[0m"

def ok(name):
    global PASS_COUNT
    PASS_COUNT += 1
    print(f"  {_c('32;1','PASS')}  {name}")

def fail(name, detail=""):
    global FAIL_COUNT
    FAIL_COUNT += 1
    msg = f"{name}: {detail}" if detail else name
    FAIL_LIST.append(msg)
    print(f"  {_c('31;1','FAIL')}  {name}")
    if detail:
        print(f"         {_c('33', str(detail)[:400])}")

def info(msg):
    print(f"  {_c('36','ℹ')}  {msg}")

def section(title):
    print()
    print(_c('1;36', '─' * 60))
    print(_c('1;36', f'  {title}'))
    print(_c('1;36', '─' * 60))


# ════════════════════════════════════════════════════════
# HTTP 工具
# ════════════════════════════════════════════════════════
def http_req(method, path, body=None, token=None, headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if headers:
        h.update(headers)
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=15)
        raw = resp.read()
        return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}

def get(path, token=None):
    return http_req("GET", path, token=token)

def post(path, body=None, token=None, headers=None):
    return http_req("POST", path, body=body, token=token, headers=headers)

def patch(path, body=None, token=None, headers=None):
    return http_req("PATCH", path, body=body, token=token, headers=headers)

def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}

def assert_code(name, status, body, expected_http, expected_code=None):
    if status != expected_http:
        fail(name, f"HTTP {status}（期望 {expected_http}），body={str(body)[:300]}")
        return False
    if expected_code is not None:
        actual = body.get("code") if isinstance(body, dict) else None
        if actual != expected_code:
            fail(name, f"HTTP {status} 正确，但 code={actual}（期望 {expected_code}），body={str(body)[:300]}")
            return False
    ok(name)
    return True


# ════════════════════════════════════════════════════════
# MySQL 辅助（仅用于必要的 INSERT 播种和断言查询，不做删除/覆盖/结构变更）
# ════════════════════════════════════════════════════════
def mysql_exec(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT),
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        info(f"SQL 执行失败: {r.stderr[:300]}")
    return r.returncode == 0

def mysql_query(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT),
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        return None
    return r.stdout.strip() or None


# ════════════════════════════════════════════════════════
# 账号操作
# ════════════════════════════════════════════════════════
def register_email(email, password="Test1234!"):
    s, b = post("/api/auth/verification-codes/email", {"target": email, "scene": "register"})
    if s != 200:
        return None, None, None, (s, b)
    code = get_data(b).get("code", "")
    s, b = post("/api/auth/register/email", {"email": email, "password": password, "code": code})
    if s not in (200, 201):
        return None, None, None, (s, b)
    d = get_data(b)
    token = d.get("access_token", "")
    refresh = d.get("refresh_token", "")
    s2, b2 = get("/api/me", token=token)
    uid = get_data(b2).get("id") if s2 == 200 else None
    return uid, token, refresh, (s, b)

def login_email(email, password="Test1234!"):
    return post("/api/auth/login/email", {"email": email, "password": password})


# ════════════════════════════════════════════════════════
# 准备：自包含测试身份
#   - 管理员账号：注册新账号 + SQL 直接 INSERT permissions(user:manage) +
#                 创建专属角色 qa_ban_admin_role 授予该权限 + 绑定给该账号
#   - 普通用户账号：纯新注册账号，无任何特殊角色
# ════════════════════════════════════════════════════════
ADMIN_EMAIL = None
ADMIN_TOKEN = None
ADMIN_UID   = None

USER_EMAIL  = None
USER_TOKEN  = None
USER_REFRESH = None
USER_UID    = None

NONADMIN_EMAIL = None
NONADMIN_TOKEN = None
NONADMIN_UID   = None


def setup_ban_admin():
    """
    自包含准备一个具备 user:manage 权限的管理员账号：
    1. 注册全新账号
    2. SQL: 若 permissions 表无 'user:manage' 记录则 INSERT 一条（纯种子数据，非结构变更）
    3. SQL: 创建专属角色 qa_ban_admin_role_<ts>，绑定该权限（role_permissions）
    4. SQL: 将新账号绑定该角色（user_roles）
    """
    global ADMIN_EMAIL, ADMIN_TOKEN, ADMIN_UID
    section("准备 1：自包含创建具备 user:manage 权限的管理员账号")
    ts = int(time.time())
    ADMIN_EMAIL = f"qa_ban_admin_{ts}@molin.io"
    uid, token, refresh, _ = register_email(ADMIN_EMAIL)
    if not uid:
        fail("注册管理员测试账号", "注册失败")
        return False
    ok(f"注册管理员测试账号成功 user_id={uid}")
    ADMIN_UID = uid

    # 1) 确保 permissions 表中存在 user:manage（无则 INSERT，纯种子数据）
    perm_id = mysql_query("SELECT id FROM permissions WHERE code='user:manage' LIMIT 1;")
    if not perm_id:
        mysql_exec(
            "INSERT INTO permissions (code, name, resource, action, created_at, updated_at) "
            "VALUES ('user:manage', 'QA-用户封禁管理（测试播种）', 'user', 'manage', NOW(), NOW());"
        )
        perm_id = mysql_query("SELECT id FROM permissions WHERE code='user:manage' LIMIT 1;")
        if perm_id:
            ok(f"已为权限码 user:manage 播种 permissions 记录（id={perm_id}，原表中缺失该权限码）")
        else:
            fail("播种 user:manage 权限失败", "INSERT 后仍查询不到记录")
            return False
    else:
        ok(f"permissions 表中已存在 user:manage（id={perm_id}）")

    # 2) 创建专属角色并授权
    role_code = f"qa_ban_admin_role_{ts}"
    mysql_exec(
        f"INSERT INTO roles (code, name, description, created_at, updated_at) "
        f"VALUES ('{role_code}', 'QA封禁接口测试管理员角色', 'E2E 临时角色，仅用于 user:manage 权限测试', NOW(), NOW());"
    )
    role_id = mysql_query(f"SELECT id FROM roles WHERE code='{role_code}' LIMIT 1;")
    if not role_id:
        fail("创建测试管理员角色失败")
        return False
    ok(f"已创建测试角色 {role_code}（role_id={role_id}）")

    mysql_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id, created_at) "
        f"VALUES ({role_id}, {perm_id}, NOW());"
    )
    ok(f"已为角色 {role_code} 绑定 user:manage 权限（role_permissions，INSERT IGNORE）")

    mysql_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id, created_at) VALUES ({uid}, {role_id}, NOW());")
    ok(f"已将测试账号 user_id={uid} 绑定角色 {role_code}（user_roles，INSERT IGNORE）")

    s, b = login_email(ADMIN_EMAIL)
    if s != 200:
        fail("管理员测试账号登录失败", f"HTTP {s} {b}")
        return False
    ADMIN_TOKEN = get_data(b).get("access_token")
    ok("管理员测试账号登录成功，已获取新 Token（含最新角色权限）")
    return True


def setup_target_user():
    """注册一个全新的、无任何特殊角色的普通用户账号，作为被封禁对象。"""
    global USER_EMAIL, USER_TOKEN, USER_REFRESH, USER_UID
    section("准备 2：注册被测普通用户账号（无任何特殊角色）")
    ts = int(time.time())
    USER_EMAIL = f"qa_ban_target_{ts}@molin.io"
    uid, token, refresh, (s, b) = register_email(USER_EMAIL)
    if not uid:
        fail("注册被测普通用户账号", f"HTTP {s} {b}")
        return False
    USER_UID, USER_TOKEN, USER_REFRESH = uid, token, refresh
    ok(f"注册被测普通用户账号成功 user_id={uid}")
    if not refresh:
        fail("获取被测用户 refresh_token", "注册响应中未返回 refresh_token")
        return False
    ok("已获取被测用户 refresh_token（用于后续验证封禁后立即失效）")
    return True


def setup_nonadmin_user():
    """注册另一个全新普通账号，用于验证“非管理员调用封禁接口被拦截”。"""
    global NONADMIN_EMAIL, NONADMIN_TOKEN, NONADMIN_UID
    section("准备 3：注册非管理员测试账号（用于验证权限拦截）")
    ts = int(time.time()) + 1
    NONADMIN_EMAIL = f"qa_ban_nonadmin_{ts}@molin.io"
    uid, token, refresh, (s, b) = register_email(NONADMIN_EMAIL)
    if not uid:
        fail("注册非管理员测试账号", f"HTTP {s} {b}")
        return False
    NONADMIN_UID, NONADMIN_TOKEN = uid, token
    ok(f"注册非管理员测试账号成功 user_id={uid}（无 user:manage 权限）")
    return True


# ════════════════════════════════════════════════════════
# 核心用例：封禁/解封
# ════════════════════════════════════════════════════════
def case_ban_unban_full_cycle():
    section("核心用例：管理员封禁用户 → 验证立即失效 → 解封 → 恢复正常访问")

    # --- 封禁前基线：被测用户可正常访问 ---
    s, b = get("/api/me", token=USER_TOKEN)
    assert_code("封禁前：被测用户可正常访问 GET /api/me", s, b, 200)

    s, b = get("/api/products", token=USER_TOKEN)
    if s in (200,):
        ok("封禁前：被测用户可正常访问 GET /api/products")
    else:
        fail("封禁前：被测用户可正常访问 GET /api/products", f"HTTP {s} {b}")

    # --- 管理员调用封禁接口 ---
    section("步骤 1：管理员调用 PATCH /api/admin/users/{id}/status，status=disabled 封禁")
    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": "disabled", "reason": "QA 自动化测试：封禁接口验证"}, token=ADMIN_TOKEN)
    assert_code("管理员封禁接口调用成功", s, b, 200)

    # 数据库交叉验证：status 字段已更新为 disabled
    db_status = mysql_query(f"SELECT status FROM users WHERE id={USER_UID};")
    if db_status == "disabled":
        ok(f"DB 交叉验证：users.status 已更新为 disabled（user_id={USER_UID}）")
    else:
        fail("DB 交叉验证：users.status 应为 disabled", f"实际为 {db_status}")

    # 数据库交叉验证：该用户全部 refresh token 已被吊销（revoked_at 非空）
    active_sessions = mysql_query(
        f"SELECT COUNT(*) FROM user_sessions WHERE user_id={USER_UID} AND revoked_at IS NULL;"
    )
    if active_sessions == "0":
        ok("DB 交叉验证：被封禁用户的全部 user_sessions 均已吊销（revoked_at 非空）")
    else:
        fail("DB 交叉验证：被封禁用户应无存活会话", f"仍有 {active_sessions} 条未吊销会话")

    time.sleep(0.5)

    # --- 验证：被封禁用户立即无法访问需登录接口 ---
    section("步骤 2：验证被封禁用户立即无法访问需要登录的接口")
    s, b = get("/api/me", token=USER_TOKEN)
    assert_code("封禁后：GET /api/me 立即返回 401", s, b, 401, expected_code=40101)

    s, b = get("/api/products", token=USER_TOKEN)
    if s == 401:
        ok(f"封禁后：GET /api/products 立即返回 401（code={b.get('code') if isinstance(b, dict) else None}）")
    else:
        fail("封禁后：GET /api/products 应返回 401", f"HTTP {s} {b}")

    # --- 验证：refresh token 立即失效 ---
    section("步骤 3：验证被封禁用户的 refresh token 立即失效")
    s, b = post("/api/auth/refresh", {"refresh_token": USER_REFRESH})
    assert_code("封禁后：POST /api/auth/refresh 返回 401（refresh token 已吊销）", s, b, 401)

    # --- 管理员解封 ---
    section("步骤 4：管理员调用 status=active 解封")
    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": "active", "reason": "QA 自动化测试：解封"}, token=ADMIN_TOKEN)
    assert_code("管理员解封接口调用成功", s, b, 200)

    db_status2 = mysql_query(f"SELECT status FROM users WHERE id={USER_UID};")
    if db_status2 == "active":
        ok(f"DB 交叉验证：users.status 已恢复为 active（user_id={USER_UID}）")
    else:
        fail("DB 交叉验证：users.status 应恢复为 active", f"实际为 {db_status2}")

    time.sleep(0.5)

    # --- 验证：解封后旧 access token 立即恢复访问（黑名单已清除） ---
    section("步骤 5：验证解封后用户恢复正常访问")
    s, b = get("/api/me", token=USER_TOKEN)
    assert_code("解封后：旧 Access Token 立即恢复访问 GET /api/me（200）", s, b, 200)

    # 旧 refresh token 在封禁时已被吊销（轮换设计：吊销后不会被恢复），重新登录验证账号可正常使用
    s, b = login_email(USER_EMAIL)
    if assert_code("解封后：被测用户可使用原密码正常重新登录", s, b, 200):
        new_token = get_data(b).get("access_token")
        s2, b2 = get("/api/me", token=new_token)
        assert_code("解封后：重新登录获取的新 Token 可正常访问 GET /api/me", s2, b2, 200)


def case_nonadmin_forbidden():
    section("权限拦截用例：非管理员调用封禁接口应被拦截（403/40003）")
    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": "disabled"}, token=NONADMIN_TOKEN)
    assert_code("非管理员调用 PATCH /api/admin/users/{id}/status 返回 403/40003", s, b, 403, expected_code=40003)

    # 交叉验证：未授权调用不应产生任何副作用
    db_status = mysql_query(f"SELECT status FROM users WHERE id={USER_UID};")
    if db_status == "active":
        ok("交叉验证：未授权调用未产生副作用，被测用户 status 仍为 active")
    else:
        fail("交叉验证：未授权调用不应改变用户状态", f"实际 status={db_status}")


def case_invalid_status():
    section("参数校验用例：非法 status 取值应返回 400/40000")
    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": "frozen"}, token=ADMIN_TOKEN)
    assert_code("status='frozen'（非法值）返回 400/40000", s, b, 400, expected_code=40000)

    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": ""}, token=ADMIN_TOKEN)
    assert_code("status=''（空值）返回 400/40000", s, b, 400, expected_code=40000)

    db_status = mysql_query(f"SELECT status FROM users WHERE id={USER_UID};")
    if db_status == "active":
        ok("交叉验证：非法 status 请求未产生副作用，用户状态仍为 active")
    else:
        fail("交叉验证：非法请求不应改变用户状态", f"实际 status={db_status}")


def case_no_token_unauthorized():
    section("旁路验证：无 Token 调用封禁接口应返回 401")
    s, b = patch(f"/api/admin/users/{USER_UID}/status", {"status": "disabled"})
    if s == 401:
        ok(f"无 Token 调用返回 401（code={b.get('code') if isinstance(b, dict) else None}）")
    else:
        fail("无 Token 调用应返回 401", f"HTTP {s} {b}")


# ════════════════════════════════════════════════════════
# 主流程
# ════════════════════════════════════════════════════════
def main():
    print(_c('1;35', "═" * 60))
    print(_c('1;35', "  PATCH /api/admin/users/{id}/status 封禁/解封接口复测"))
    print(_c('1;35', f"  API_BASE = {API_BASE}"))
    print(_c('1;35', "═" * 60))

    if not setup_ban_admin():
        print(_c('31;1', "管理员测试身份准备失败，终止测试"))
        sys.exit(1)
    if not setup_target_user():
        print(_c('31;1', "被测用户准备失败，终止测试"))
        sys.exit(1)
    if not setup_nonadmin_user():
        print(_c('31;1', "非管理员账号准备失败，终止测试"))
        sys.exit(1)

    case_ban_unban_full_cycle()
    case_nonadmin_forbidden()
    case_invalid_status()
    case_no_token_unauthorized()

    section("汇总")
    total = PASS_COUNT + FAIL_COUNT
    print(f"  总用例数：{total}")
    print(f"  {_c('32;1', f'通过：{PASS_COUNT}')}")
    print(f"  {_c('31;1', f'失败：{FAIL_COUNT}')}")
    if FAIL_LIST:
        print()
        print(_c('31;1', "失败明细："))
        for m in FAIL_LIST:
            print(f"  - {m}")
    print()
    if FAIL_COUNT == 0:
        print(_c('32;1', "全部用例通过 ✅"))
        sys.exit(0)
    else:
        print(_c('31;1', f"存在 {FAIL_COUNT} 条失败用例 ❌"))
        sys.exit(1)


if __name__ == "__main__":
    main()

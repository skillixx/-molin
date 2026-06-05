#!/usr/bin/env python3
"""
后端工程师甲（A-01 / A-02 / A-03）接口测试脚本
测试范围：auth / iam / identity 全部路由
用法：
    # 连本地
    python3 tests/test_backend_a.py
    # 连测试服务器
    API_BASE=http://8.130.9.163:8080 \
    MYSQL_HOST=8.130.9.163 \
    python3 tests/test_backend_a.py
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
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
def request(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else b""
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
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

def put(path, body=None, token=None):
    return request("PUT", path, body, token)

def patch(path, body=None, token=None):
    return request("PATCH", path, body, token)

def delete(path, token=None):
    return request("DELETE", path, token=token)

def assert_status(label, status, expected, body):
    if status == expected:
        ok(f"{label}  →  HTTP {status}")
    else:
        msg = body.get("message", "") if isinstance(body, dict) else ""
        fail(f"{label}  →  HTTP {status}（期望 {expected}）", msg)
    return status == expected

def get_data(body):
    return body.get("data") or {}

# ── MySQL 工具（用于测试前置数据播种）────────────────────
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

    # 回退：subprocess + mysql 命令行
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
        print(f"  {RED}未找到 mysql 命令，且 pymysql 未安装，跳过数据库播种{RESET}")
        print(f"  {YELLOW}提示：pip install pymysql{RESET}")
        return False

def seed_admin_user(user_id):
    """播种管理员权限：创建 role:manage 和 identity:review 权限及角色，绑定到指定用户。"""
    sql = f"""
    INSERT IGNORE INTO permissions (code, name, resource, action)
    VALUES
      ('role:manage',     '角色管理',   'role',     'manage'),
      ('identity:review', '实名审核',   'identity', 'review');

    INSERT IGNORE INTO roles (code, name, description)
    VALUES ('admin', '超级管理员', '系统内置管理员角色');

    SET @role_id = (SELECT id FROM roles WHERE code = 'admin' LIMIT 1);
    SET @perm1   = (SELECT id FROM permissions WHERE code = 'role:manage' LIMIT 1);
    SET @perm2   = (SELECT id FROM permissions WHERE code = 'identity:review' LIMIT 1);

    INSERT IGNORE INTO role_permissions (role_id, permission_id)
    VALUES (@role_id, @perm1), (@role_id, @perm2);

    INSERT IGNORE INTO user_roles (user_id, role_id)
    VALUES ({user_id}, @role_id);
    """
    return mysql_exec(sql)


# ════════════════════════════════════════════════════════
# A-01  认证模块
# ════════════════════════════════════════════════════════
def test_a01():
    section("A-01  认证模块（auth）")
    ts    = int(time.time())
    email = f"test_a01_{ts}@example.com"
    phone = f"138{ts % 100000000:08d}"

    # 1. 发邮箱验证码
    status, body = post("/api/auth/verification-codes/email",
                        {"target": email, "scene": "register"})
    assert_status("发送邮箱注册验证码", status, 200, body)
    email_code = get_data(body).get("code", "")
    info(f"验证码（本地可见）: {email_code}")

    # 2. 邮箱注册
    status, body = post("/api/auth/register/email",
                        {"email": email, "password": "Test1234!", "code": email_code})
    ok_reg = assert_status("邮箱注册", status, 201, body)
    d = get_data(body)
    access_token  = d.get("access_token", "")
    refresh_token = d.get("refresh_token", "")
    user_id = None  # 稍后从 /api/me 获取

    # 3. 错误验证码注册（期望 400）
    status, body = post("/api/auth/register/email",
                        {"email": email, "password": "Test1234!", "code": "000000"})
    assert_status("错误验证码注册被拦截", status, 400, body)

    # 4. GET /api/me
    status, body = get("/api/me", token=access_token)
    assert_status("GET /api/me（有效 Token）", status, 200, body)
    user_id = get_data(body).get("id")
    info(f"当前用户 ID: {user_id}")

    # 5. 无 Token 鉴权
    status, body = get("/api/me")
    assert_status("GET /api/me（无 Token，期望 401）", status, 401, body)

    # 6. 邮箱密码登录
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "Test1234!"})
    assert_status("邮箱密码登录", status, 200, body)
    d2 = get_data(body)
    access_token2  = d2.get("access_token", "")
    refresh_token2 = d2.get("refresh_token", "")

    # 7. 错误密码
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "Wrong123!"})
    assert_status("错误密码登录被拦截（期望 401）", status, 401, body)

    # 8. 发手机验证码
    status, body = post("/api/auth/verification-codes/phone",
                        {"target": phone, "scene": "login"})
    assert_status("发送手机验证码", status, 200, body)

    # 9. 刷新 Token
    status, body = post("/api/auth/refresh", {"refresh_token": refresh_token2})
    assert_status("刷新 Token", status, 200, body)
    new_refresh = get_data(body).get("refresh_token", "")

    # 10. 修改密码
    status, body = patch("/api/me/password",
                         {"old_password": "Test1234!", "new_password": "NewPass456!"},
                         token=access_token2)
    assert_status("修改密码", status, 200, body)

    # 11. 旧密码登录（期望 401）
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "Test1234!"})
    assert_status("旧密码已失效（期望 401）", status, 401, body)

    # 12. 新密码登录
    status, body = post("/api/auth/login/email",
                        {"email": email, "password": "NewPass456!"})
    assert_status("新密码登录", status, 200, body)
    d3 = get_data(body)
    access_token3  = d3.get("access_token", "")
    refresh_token3 = d3.get("refresh_token", "")

    # 13. 退出登录
    status, body = post("/api/auth/logout",
                        {"refresh_token": refresh_token3}, token=access_token3)
    assert_status("退出登录", status, 200, body)

    # 14. 已吊销 Token 再刷新（期望 401）
    status, body = post("/api/auth/refresh", {"refresh_token": refresh_token3})
    assert_status("已退出 refresh_token 刷新被拦截（期望 401）", status, 401, body)

    return user_id, email, access_token, access_token2


# ════════════════════════════════════════════════════════
# A-02  IAM 模块
# ════════════════════════════════════════════════════════
def test_a02(admin_token, regular_user_id):
    section("A-02  IAM 模块（角色 / 权限 / RBAC）")

    if not admin_token:
        info("跳过 A-02：未获取到管理员 Token")
        return

    # 1. 获取权限列表
    status, body = get("/api/admin/permissions", token=admin_token)
    assert_status("GET 权限列表", status, 200, body)
    perms = get_data(body) if isinstance(get_data(body), list) else \
            (body.get("data") or [])
    perm_id = perms[0]["id"] if perms else None
    info(f"权限总数: {len(perms)}，首个权限 ID: {perm_id}")

    # 2. 创建角色
    ts = int(time.time())
    status, body = post("/api/admin/roles",
                        {"code": f"test_role_{ts}", "name": "测试角色"},
                        token=admin_token)
    assert_status("POST 创建角色", status, 201, body)
    role_id = get_data(body).get("id") if isinstance(get_data(body), dict) else None
    info(f"新角色 ID: {role_id}")

    # 3. 获取角色列表
    status, body = get("/api/admin/roles", token=admin_token)
    assert_status("GET 角色列表", status, 200, body)

    # 4. 更新角色
    if role_id:
        status, body = put(f"/api/admin/roles/{role_id}",
                           {"code": f"test_role_{ts}", "name": "测试角色（已更新）"},
                           token=admin_token)
        assert_status("PUT 更新角色", status, 200, body)

    # 5. 为用户分配角色
    if role_id and regular_user_id:
        status, body = post(f"/api/admin/users/{regular_user_id}/roles",
                            {"role_id": role_id},
                            token=admin_token)
        assert_status("POST 分配角色给用户", status, 200, body)

        # 6. 查询用户角色
        status, body = get(f"/api/admin/users/{regular_user_id}/roles",
                           token=admin_token)
        assert_status("GET 用户角色列表", status, 200, body)

        # 7. 撤销角色
        status, body = delete(f"/api/admin/users/{regular_user_id}/roles/{role_id}",
                              token=admin_token)
        assert_status("DELETE 撤销用户角色", status, 200, body)

    # 8. 另注册一个普通用户，作为 override 操作的目标（避免对 admin 自身设 deny）
    ts2 = int(time.time()) + 1
    victim_email = f"victim_{ts2}@example.com"
    status, body = post("/api/auth/verification-codes/email",
                        {"target": victim_email, "scene": "register"})
    victim_code = get_data(body).get("code", "")
    status, body = post("/api/auth/register/email",
                        {"email": victim_email, "password": "Test1234!", "code": victim_code})
    victim_id = get_data(body).get("id") if isinstance(get_data(body), dict) else None
    if not victim_id:
        victim_data = body.get("data") or {}
        # victim_id 从 /api/me 获取
        victim_token = (victim_data.get("access_token") or "")
        if victim_token:
            _, me = get("/api/me", token=victim_token)
            victim_id = (me.get("data") or {}).get("id")
    info(f"测试用普通用户 ID: {victim_id}")

    # 设置权限覆盖（deny）
    if perm_id and victim_id:
        status, body = post(f"/api/admin/users/{victim_id}/permission-overrides",
                            {"permission_id": perm_id, "effect": "deny",
                             "reason": "测试 deny override"},
                            token=admin_token)
        assert_status("POST 设置权限覆盖（deny）", status, 200, body)
        override_id = (get_data(body) or {}).get("id") if isinstance(get_data(body), dict) else None

        # 9. 非法 effect 被拦截（admin 自身无 deny，仍有权限访问）
        status, body = post(f"/api/admin/users/{victim_id}/permission-overrides",
                            {"permission_id": perm_id, "effect": "DENY"},
                            token=admin_token)
        assert_status("POST 非法 effect='DENY' 被拦截（期望 400）", status, 400, body)

        # 10. 查询权限覆盖
        status, body = get(f"/api/admin/users/{victim_id}/permission-overrides",
                           token=admin_token)
        assert_status("GET 用户权限覆盖列表", status, 200, body)

        # 11. 删除权限覆盖
        if override_id:
            status, body = delete(
                f"/api/admin/users/{victim_id}/permission-overrides/{override_id}",
                token=admin_token)
            assert_status("DELETE 删除权限覆盖", status, 200, body)

    # 12. 删除角色
    if role_id:
        status, body = delete(f"/api/admin/roles/{role_id}", token=admin_token)
        assert_status("DELETE 删除角色", status, 200, body)

    # 13. 普通用户访问管理接口（期望 403）
    status, body = get("/api/admin/roles")  # 无 token
    assert_status("无 Token 访问管理接口被拦截（期望 401）", status, 401, body)


# ════════════════════════════════════════════════════════
# A-03  实名认证模块
# ════════════════════════════════════════════════════════
def test_a03(user_token, admin_token):
    section("A-03  实名认证模块（identity）")

    if not user_token:
        info("跳过 A-03：未获取到用户 Token")
        return

    # 1. 提交实名认证
    status, body = post("/api/identity/verifications",
                        {"real_name": "张三",
                         "id_card_no": "110101199001011234"},
                        token=user_token)
    assert_status("POST 提交实名认证", status, 201, body)
    verif_id = get_data(body).get("id") if isinstance(get_data(body), dict) else None
    info(f"认证记录 ID: {verif_id}，脱敏号: "
         f"{get_data(body).get('id_card_no_masked', '') if isinstance(get_data(body), dict) else ''}")

    # 2. 查询我的认证状态
    status, body = get("/api/identity/verifications/me", token=user_token)
    assert_status("GET 查询我的实名状态", status, 200, body)
    info(f"认证状态: {get_data(body).get('status', '') if isinstance(get_data(body), dict) else ''}")

    # 3. 重复提交（期望 400/409）
    status, body = post("/api/identity/verifications",
                        {"real_name": "张三", "id_card_no": "110101199001011234"},
                        token=user_token)
    assert_status("重复提交实名被拦截（期望 4xx）", status in (400, 409), True,
                  body) if status in (400, 409) else \
        fail("重复提交实名未被拦截", f"HTTP {status}")

    if not admin_token:
        info("跳过管理员审核测试：未获取到管理员 Token")
        return

    # 4. 管理员查看待审列表
    status, body = get("/api/admin/identity-verifications", token=admin_token)
    assert_status("GET 管理员查看待审列表", status, 200, body)
    items = body.get("data") or []
    info(f"待审记录数: {len(items) if isinstance(items, list) else '?'}")

    # 5. 管理员查看详情
    if verif_id:
        status, body = get(f"/api/admin/identity-verifications/{verif_id}",
                           token=admin_token)
        assert_status("GET 管理员查看认证详情", status, 200, body)

        # 6. 管理员拒绝
        status, body = patch(f"/api/admin/identity-verifications/{verif_id}/review",
                             {"approve": False, "reason": "照片不清晰"},
                             token=admin_token)
        assert_status("PATCH 管理员拒绝认证", status, 200, body)

    # 7. 无权限用户访问管理接口（期望 401）
    status, body = get("/api/admin/identity-verifications")
    assert_status("无 Token 访问审核接口被拦截（期望 401）", status, 401, body)


# ════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  Molin 后端 A 接口测试{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}")

    # ── A-01 ──────────────────────────────────────────
    user_id, email, access_token, access_token2 = test_a01()

    # ── 播种管理员权限 ────────────────────────────────
    section("前置准备：播种管理员数据")
    admin_token = None
    if user_id:
        seeded = seed_admin_user(user_id)
        if seeded:
            ok(f"已将 user_id={user_id} 设为管理员（admin 角色）")
            # 重新登录获取包含权限的 token（缓存会自动刷新）
            status, body = post("/api/auth/login/email",
                                {"email": email, "password": "NewPass456!"})
            if status == 200:
                admin_token = get_data(body).get("access_token")
                ok("管理员 Token 获取成功")
            else:
                fail("管理员重新登录失败", body.get("message", ""))
        else:
            fail("管理员数据播种失败，跳过 A-02/A-03 管理员相关用例")
    else:
        fail("未获取到 user_id，跳过管理员播种")

    # ── A-02 ──────────────────────────────────────────
    test_a02(admin_token, user_id)

    # ── A-03 ──────────────────────────────────────────
    test_a03(access_token, admin_token)

    # ── 汇总 ──────────────────────────────────────────
    total = passed + failed
    print(f"\n{BOLD}{'═'*50}{RESET}")
    print(f"{BOLD}  测试结果：{passed}/{total} 通过{RESET}")
    if failed == 0:
        print(f"{BOLD}{GREEN}  结论：全部通过 ✅{RESET}")
    else:
        print(f"{BOLD}{RED}  结论：{failed} 项失败 ❌，请检查上方输出{RESET}")
    print(f"{BOLD}{'═'*50}{RESET}\n")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()

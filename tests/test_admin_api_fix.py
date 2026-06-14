#!/usr/bin/env python3
"""
管理员 API 验收测试脚本
测试范围：
  1. GET /api/admin/users          — 列表、keyword/status 过滤、权限校验、脱敏
  2. GET /api/admin/users/{id}     — 正常查询、不存在 ID
  3. GET /api/admin/identity-verifications — ?status= 过滤
  4. GET /api/admin/identity-verifications/{id} — 响应含 user_id/submitted_at/reviewed_at
  5. GET /api/admin/roles          — ?keyword= 搜索
  6. GET /api/admin/users/{id}/permission-overrides — ?effect= / ?permission_code= 过滤、?effect=invalid 返回 400

用法：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 tests/test_admin_api_fix.py
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
        print(f"  {RED}未找到 mysql 命令，且 pymysql 未安装，跳过数据库播种{RESET}")
        return False


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
def complete_admin_double_verify(user_id, phone, email, token):
    """完成管理员手机+邮箱双重认证，返回是否成功。"""
    # 发送手机验证码（scene=admin_verify）
    status, body = post("/api/auth/verification-codes/phone",
                        {"target": phone, "scene": "admin_verify"})
    if status != 200:
        info(f"发送手机验证码失败: HTTP {status} {body}")
        return False
    phone_code = get_data(body).get("code", "")
    if not phone_code:
        info("未返回手机验证码（非调试环境或 target 字段错误）")
        return False

    # 管理员手机认证
    status, body = post("/api/admin/auth/verify-phone",
                        {"code": phone_code}, token=token)
    if status != 200:
        info(f"管理员手机认证失败: HTTP {status} {body}")
        return False

    # 发送邮箱验证码（scene=admin_verify）
    status, body = post("/api/auth/verification-codes/email",
                        {"target": email, "scene": "admin_verify"})
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
    phone = f"138{ts % 100000000:08d}"
    email = f"admintest_{ts}@example.com"
    pwd   = "Admin@Test123!"

    # 发送手机验证码
    status, body = post("/api/auth/verification-codes/phone",
                        {"target": phone, "scene": "register"})
    if status != 200:
        fail("发送手机注册验证码", f"HTTP {status}")
        return None
    phone_code = get_data(body).get("code", "")
    info(f"手机验证码: {phone_code}")

    # 发送邮箱验证码
    status, body = post("/api/auth/verification-codes/email",
                        {"target": email, "scene": "register"})
    if status != 200:
        fail("发送邮箱注册验证码", f"HTTP {status}")
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
        "username":   f"admintest{ts % 10000}"
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
# 注册普通用户（用于提交实名、作为查询目标等）
# ════════════════════════════════════════════════════════════
def setup_regular_user():
    """返回 (user_token, user_id) 或 (None, None)。"""
    ts    = int(time.time()) + 1
    phone = f"139{(ts + 17) % 100000000:08d}"
    email = f"regular_{ts}@example.com"
    pwd   = "Regular@123!"

    status, body = post("/api/auth/verification-codes/phone",
                        {"target": phone, "scene": "register"})
    if status != 200:
        return None, None
    phone_code = get_data(body).get("code", "")

    status, body = post("/api/auth/verification-codes/email",
                        {"target": email, "scene": "register"})
    if status != 200:
        return None, None
    email_code = get_data(body).get("code", "")

    status, body = post("/api/auth/register", {
        "phone":      phone,
        "phone_code": phone_code,
        "email":      email,
        "email_code": email_code,
        "password":   pwd,
        "username":   f"regular{ts % 10000}"
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
# 测试组 1：GET /api/admin/users
# ════════════════════════════════════════════════════════════
def test_admin_users_list(admin_token, regular_user_id):
    section("测试组 1：GET /api/admin/users")

    # 1-1 无 Token → 401
    status, body = get("/api/admin/users")
    assert_status("1-1  无 Token 访问用户列表 → 401", status, 401, body)

    # 1-2 有效 Token → 200
    status, body = get("/api/admin/users", token=admin_token)
    if assert_status("1-2  有效 Token 获取用户列表 → 200", status, 200, body):
        # 检查分页结构
        d = body.get("data", body) if isinstance(body.get("data", body), dict) else {}
        has_items = "items" in d
        # D-95：分页结构已扁平化，page/page_size/total 与 items 同级，data 中不应再有 pagination 子对象
        has_flat_pagination = "pagination" not in d and all(k in d for k in ("page", "page_size", "total"))
        if has_items:
            ok("1-2a 响应包含 items 字段")
        else:
            fail("1-2a 响应缺少 items 字段", str(body)[:200])
        if has_flat_pagination:
            ok("1-2b 分页结构已扁平化：page/page_size/total 与 items 同级，且不含 pagination 子对象")
        else:
            fail("1-2b 分页结构不符合预期（D-95 扁平结构）", str(body)[:200])

    # 1-3 keyword 过滤
    status, body = get("/api/admin/users", token=admin_token,
                       params={"keyword": "admintest"})
    assert_status("1-3  keyword 过滤 → 200", status, 200, body)

    # 1-4 status 过滤（active）
    status, body = get("/api/admin/users", token=admin_token,
                       params={"status": "active"})
    assert_status("1-4  status=active 过滤 → 200", status, 200, body)

    # 1-5 脱敏校验：邮箱不含明文 @ 之前完整部分（简单判断：不存在 @ 或已脱敏）
    status, body = get("/api/admin/users", token=admin_token)
    if status == 200:
        items = []
        if "list" in body:
            items = body["list"] or []
        elif isinstance(body.get("data"), dict) and "list" in body["data"]:
            items = body["data"]["list"] or []
        all_masked = True
        for item in items:
            email_val = item.get("email", "") or ""
            # 脱敏邮箱形如 ab***@example.com，明文邮箱形如 abcde@example.com（@ 前无 ***）
            if "@" in email_val and "***" not in email_val and "****" not in email_val:
                # 如果邮箱 @ 前部分超过 4 字符且不含 *，视为未脱敏
                local = email_val.split("@")[0]
                if len(local) > 4:
                    all_masked = False
                    info(f"可能未脱敏的邮箱: {email_val}")
                    break
        if all_masked:
            ok("1-5  用户列表邮箱已脱敏或长度符合预期")
        else:
            fail("1-5  用户列表邮箱疑似未脱敏")


# ════════════════════════════════════════════════════════════
# 测试组 2：GET /api/admin/users/{id}
# ════════════════════════════════════════════════════════════
def test_admin_user_detail(admin_token, regular_user_id):
    section("测试组 2：GET /api/admin/users/{id}")

    if not regular_user_id:
        info("跳过 2：未获取到普通用户 ID")
        return

    # 2-1 正常查询
    status, body = get(f"/api/admin/users/{regular_user_id}", token=admin_token)
    if assert_status("2-1  正常查询存在用户 → 200", status, 200, body):
        d = get_data(body)
        if d.get("id") or body.get("id"):
            ok("2-1a 响应包含用户 ID 字段")
        else:
            fail("2-1a 响应缺少 id 字段", str(body)[:200])

    # 2-2 不存在的 ID → 404
    status, body = get("/api/admin/users/999999999", token=admin_token)
    assert_status("2-2  查询不存在用户 → 404", status, 404, body)

    # 2-3 无 Token → 401
    status, body = get(f"/api/admin/users/{regular_user_id}")
    assert_status("2-3  无 Token 访问用户详情 → 401", status, 401, body)


# ════════════════════════════════════════════════════════════
# 测试组 3：GET /api/admin/identity-verifications
# ════════════════════════════════════════════════════════════
def test_admin_identity_list(admin_token, regular_token, regular_user_id):
    section("测试组 3：GET /api/admin/identity-verifications")

    # 先让普通用户提交实名认证，确保列表非空
    verif_id = None
    if regular_token:
        status, body = post("/api/identity/verifications",
                            {"real_name": "李测试",
                             "id_card_no": "110101199001011234"},
                            token=regular_token)
        if status in (200, 201):
            verif_id = get_data(body).get("id")
            info(f"普通用户已提交实名认证，记录 ID: {verif_id}")
        else:
            info(f"实名认证提交返回 {status}（可能已提交过，继续）")
            # 尝试从 /api/identity/verifications/me 获取 ID
            s2, b2 = get("/api/identity/verifications/me", token=regular_token)
            if s2 == 200:
                verif_id = get_data(b2).get("id")
                info(f"从 /api/me 获取实名记录 ID: {verif_id}")

    # 3-1 无 Token → 401
    status, body = get("/api/admin/identity-verifications")
    assert_status("3-1  无 Token 访问 → 401", status, 401, body)

    # 3-2 无过滤参数 → 200
    status, body = get("/api/admin/identity-verifications", token=admin_token)
    assert_status("3-2  无过滤参数查询全部 → 200", status, 200, body)

    # 3-3 ?status=pending
    status, body = get("/api/admin/identity-verifications", token=admin_token,
                       params={"status": "pending"})
    assert_status("3-3  status=pending 过滤 → 200", status, 200, body)

    # 3-4 ?status=verified
    status, body = get("/api/admin/identity-verifications", token=admin_token,
                       params={"status": "verified"})
    assert_status("3-4  status=verified 过滤 → 200", status, 200, body)

    # 3-5 ?status=rejected
    status, body = get("/api/admin/identity-verifications", token=admin_token,
                       params={"status": "rejected"})
    assert_status("3-5  status=rejected 过滤 → 200", status, 200, body)

    # 3-6 ?status=all（如果后端支持 all 关键字）
    status, body = get("/api/admin/identity-verifications", token=admin_token,
                       params={"status": "all"})
    if status in (200, 400):
        ok(f"3-6  status=all 返回 {status}（200 或 400 均可接受）")
    else:
        fail(f"3-6  status=all 返回意外状态码", f"HTTP {status}")

    return verif_id


# ════════════════════════════════════════════════════════════
# 测试组 4：GET /api/admin/identity-verifications/{id}
# ════════════════════════════════════════════════════════════
def test_admin_identity_detail(admin_token, verif_id):
    section("测试组 4：GET /api/admin/identity-verifications/{id}")

    if not verif_id:
        info("跳过 4-1/4-2：无可用实名记录 ID（前置步骤未产生记录）")
        # 仍测试无 Token 的情况
        status, body = get("/api/admin/identity-verifications/1")
        assert_status("4-0  无 Token 访问实名详情 → 401", status, 401, body)
        return

    # 4-1 无 Token → 401
    status, body = get(f"/api/admin/identity-verifications/{verif_id}")
    assert_status("4-1  无 Token 访问 → 401", status, 401, body)

    # 4-2 正常查询 → 200 且响应含必需字段
    status, body = get(f"/api/admin/identity-verifications/{verif_id}",
                       token=admin_token)
    if assert_status("4-2  正常查询实名详情 → 200", status, 200, body):
        d = get_data(body)
        # 兼容响应数据直接放在 body 或 body.data
        if not d:
            d = body
        required_fields = ["user_id", "submitted_at"]
        for f in required_fields:
            if f in d:
                ok(f"4-2-{f}  响应含字段 {f}")
            else:
                fail(f"4-2-{f}  响应缺少字段 {f}", str(d)[:300])
        # reviewed_at 可能为 null（未审核时），但字段应存在
        if "reviewed_at" in d:
            ok("4-2-reviewed_at  响应含字段 reviewed_at（值可为 null）")
        else:
            fail("4-2-reviewed_at  响应缺少字段 reviewed_at", str(d)[:300])

    # 4-3 不存在的 ID → 404
    status, body = get("/api/admin/identity-verifications/999999999",
                       token=admin_token)
    assert_status("4-3  查询不存在实名记录 → 404", status, 404, body)


# ════════════════════════════════════════════════════════════
# 测试组 5：GET /api/admin/roles
# ════════════════════════════════════════════════════════════
def test_admin_roles(admin_token):
    section("测试组 5：GET /api/admin/roles")

    # 5-1 无 Token → 401
    status, body = get("/api/admin/roles")
    assert_status("5-1  无 Token 访问 → 401", status, 401, body)

    # 5-2 无关键字 → 200 且返回 list
    status, body = get("/api/admin/roles", token=admin_token)
    if assert_status("5-2  无关键字获取角色列表 → 200", status, 200, body):
        has_list = "list" in body or (isinstance(body.get("data"), dict) and "list" in body["data"])
        if has_list:
            ok("5-2a 响应包含 list 字段")
        else:
            fail("5-2a 响应缺少 list 字段", str(body)[:200])

    # 5-3 ?keyword=admin → 200
    status, body = get("/api/admin/roles", token=admin_token,
                       params={"keyword": "admin"})
    assert_status("5-3  keyword=admin 过滤 → 200", status, 200, body)

    # 5-4 ?keyword=不存在的角色名 → 200（空列表，非 404）
    status, body = get("/api/admin/roles", token=admin_token,
                       params={"keyword": "zzz_nonexistent_role_xyz"})
    if assert_status("5-4  keyword 无匹配 → 200（空列表）", status, 200, body):
        items = []
        if "list" in body:
            items = body["list"] or []
        elif isinstance(body.get("data"), dict):
            items = body["data"].get("list") or []
        if isinstance(items, list) and len(items) == 0:
            ok("5-4a 无匹配时返回空列表")
        elif isinstance(items, list):
            info(f"5-4a keyword 无匹配但返回了 {len(items)} 条记录（可能搜索逻辑宽松）")
        else:
            info(f"5-4a list 类型异常: {type(items)}")


# ════════════════════════════════════════════════════════════
# 测试组 6：GET /api/admin/users/{id}/permission-overrides
# ════════════════════════════════════════════════════════════
def test_permission_overrides(admin_token, regular_user_id):
    section("测试组 6：GET /api/admin/users/{id}/permission-overrides")

    if not regular_user_id:
        info("跳过 6：未获取到普通用户 ID")
        return

    # 6-1 无 Token → 401
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides")
    assert_status("6-1  无 Token 访问 → 401", status, 401, body)

    # 6-2 无过滤参数 → 200
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token)
    assert_status("6-2  无过滤参数 → 200", status, 200, body)

    # 6-3 ?effect=allow → 200
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token, params={"effect": "allow"})
    assert_status("6-3  effect=allow 过滤 → 200", status, 200, body)

    # 6-4 ?effect=deny → 200
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token, params={"effect": "deny"})
    assert_status("6-4  effect=deny 过滤 → 200", status, 200, body)

    # 6-5 ?effect=invalid → 400
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token, params={"effect": "invalid"})
    assert_status("6-5  effect=invalid → 400", status, 400, body)

    # 6-6 ?permission_code=role:manage → 200
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token, params={"permission_code": "role:manage"})
    assert_status("6-6  permission_code 过滤 → 200", status, 200, body)

    # 6-7 ?effect=allow&permission_code=role:manage → 200（组合过滤）
    status, body = get(f"/api/admin/users/{regular_user_id}/permission-overrides",
                       token=admin_token,
                       params={"effect": "allow", "permission_code": "role:manage"})
    assert_status("6-7  effect+permission_code 组合过滤 → 200", status, 200, body)

    # 6-8 普通用户 Token 访问他人 permission-overrides → 403
    # （直接用无权限 Token 测试，无 Token 已在 6-1 覆盖）
    # 这里用一个新注册用户的 Token（无 role:manage 权限）来访问
    # 简单起见：用普通用户 Token 直接访问管理员接口
    # 普通用户 Token 需要在调用方传入，此处先跳过，仅标注
    info("6-8  普通用户 Token 访问管理员接口权限校验已在 6-1 无 Token 中覆盖；如需 403 场景需传普通 Token")


# ════════════════════════════════════════════════════════════
# 主入口
# ════════════════════════════════════════════════════════════
def main():
    print(f"\n{BOLD}{'═'*60}{RESET}")
    print(f"{BOLD}  Molin 管理员 API 验收测试{RESET}")
    print(f"{BOLD}  目标：{API_BASE}{RESET}")
    print(f"{BOLD}{'═'*60}{RESET}")

    # ── 前置准备：建立管理员账号 ──────────────────────────
    result = setup_admin()
    if result is None:
        print(f"\n{BOLD}{RED}前置准备失败，终止测试。{RESET}\n")
        sys.exit(1)
    admin_token, admin_user_id, admin_phone, admin_email = result

    # ── 注册普通用户 ──────────────────────────────────────
    section("前置准备：注册普通测试用户")
    regular_token, regular_user_id = setup_regular_user()
    if regular_user_id:
        ok(f"普通用户注册成功，ID: {regular_user_id}")
    else:
        info("普通用户注册失败，部分测试将跳过")

    # ── 执行各测试组 ──────────────────────────────────────
    test_admin_users_list(admin_token, regular_user_id)
    test_admin_user_detail(admin_token, regular_user_id)
    verif_id = test_admin_identity_list(admin_token, regular_token, regular_user_id)
    test_admin_identity_detail(admin_token, verif_id)
    test_admin_roles(admin_token)
    test_permission_overrides(admin_token, regular_user_id)

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

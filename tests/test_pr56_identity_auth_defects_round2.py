#!/usr/bin/env python3
"""
PR#56（A-18）接口验收脚本 — identity/auth 第二轮缺陷修复验收（D-06/D-08~D-12）

验收范围（6 个缺陷）：
  D-06  拒绝审核必须填写理由（reason 为空或纯空格 → 400/40000；非空 → 200 正常）
  D-08  ListPending 死代码删除后 ListPaged 回归（status=pending / 不传 status 均正常 200）
  D-09  CreateLog 错误检查后正常路径回归（审核成功后 identity_verification_logs 有对应记录）
  D-10  审核通过时不应写入 reject_reason（approve=true 即使传 reason 也不应落库到 reject_reason）
  D-11  并发审核只有一个成功，另一个返回 409/40900（行级并发保护，且日志只有 1 条）
  D-12  封禁/解封 DB 与 Redis 一致性（disabled → DB+Redis+会话吊销；active → DB 恢复+Redis 删除）

注册策略：通过 DB 插入验证码（绕过短信/邮件 OTP 外部依赖），再走正常注册 API。
这样 bcrypt hash 由 API 生成，密码完全准确，无需复用他人 hash。

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr56_identity_auth_defects_round2.py
"""

import json
import os
import subprocess
import threading
import time
import hashlib
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed  = 0
failed  = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    msg = f"  {GREEN}[PASS]{RESET} {label}"
    if detail:
        msg += f"\n         {YELLOW}{detail}{RESET}"
    print(msg)
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}[FAIL]{RESET} {label}"
    if detail:
        msg += f"\n         {RED}{detail}{RESET}"
    print(msg)
    results.append(("FAIL", label, detail))


def skip(label, reason=""):
    msg = f"  {YELLOW}[SKIP]{RESET} {label}"
    if reason:
        msg += f"\n         {YELLOW}{reason}{RESET}"
    print(msg)
    results.append(("SKIP", label, reason))


# ── HTTP 工具 ──────────────────────────────────────────────────────────────────

def http(method, path, body=None, token=None):
    """发起 HTTP 请求，返回 (http_status, response_body_dict)。"""
    url = API_BASE + path
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


# ── MySQL 工具 ─────────────────────────────────────────────────────────────────

def db_query(sql):
    """执行 SELECT，返回 (rows, err)，rows 为 List[List[str]]。"""
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


def db_exec(sql):
    """执行非查询 SQL，返回 (success, err)。"""
    cmd = [
        "mysql",
        "-h", MYSQL_HOST,
        f"-P{MYSQL_PORT}",
        f"-u{MYSQL_USER}",
        f"-p{MYSQL_PASS}",
        MYSQL_DB,
        "-e", sql,
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return False, result.stderr.strip()
        return True, None
    except Exception as ex:
        return False, str(ex)


# ── Redis 工具（通过 docker exec molin-redis redis-cli）──────────────────────

def redis_cli(*args):
    """执行 docker exec molin-redis redis-cli <args>，返回 (stdout_str, err)。"""
    cmd = ["docker", "exec", "molin-redis", "redis-cli"] + list(args)
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return None, result.stderr.strip()
        return result.stdout.strip(), None
    except Exception as ex:
        return None, str(ex)


# ── 注册工具：DB 插入验证码 + API 注册 ────────────────────────────────────────

def register_user_via_api(email, phone, password, username=None):
    """
    通过以下步骤注册账号：
    1. DB 直接插入手机验证码（scene=register）
    2. DB 直接插入邮箱验证码（scene=register）
    3. 调用 POST /api/auth/register（含验证码）
    4. 返回 (user_id, access_token) 或 (None, None)
    """
    otp_code     = "888888"
    # 验证服务存储的是 SHA-256(code)，不存明文（见 verification_service.go:Send）
    otp_code_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    # API 使用 CST(UTC+8) 时区，DB 使用 UTC，插入 expires_at 需加 8h 偏移 + 10min 有效期 = 490min
    expire_sql   = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"

    # 清理可能残留的同 target 旧验证码
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")

    # 插入手机验证码（code 字段存 SHA-256 hash，target_value 存原始手机号）
    ok_p, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone', '{phone}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_p:
        print(f"    {RED}插入手机验证码失败: {err}{RESET}")
        return None, None

    # 插入邮箱验证码（code 字段存 SHA-256 hash，target_value 存原始邮箱地址）
    ok_e, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email', '{email}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_e:
        print(f"    {RED}插入邮箱验证码失败: {err}{RESET}")
        return None, None

    # 调用注册接口
    body = {
        "email":       email,
        "phone":       phone,
        "password":    password,
        "phone_code":  otp_code,
        "email_code":  otp_code,
    }
    if username:
        body["username"] = username

    status, resp = http("POST", "/api/auth/register", body)
    if status in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        # 从 DB 查 user_id
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
        return user_id, token
    else:
        print(f"    {RED}注册接口返回非预期: HTTP={status}, {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
        return None, None


# ── 初始化时间戳，用于区分本次测试数据 ───────────────────────────────────────

TS = int(time.time())

ADMIN_EMAIL    = f"pr56admin{TS}@testmail.io"
ADMIN_PHONE    = f"160{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr56Admin"

USER_D06_EMAIL    = f"pr56d06{TS}@testmail.io"
USER_D06_PHONE    = f"161{TS % 100000000:08d}"
USER_D06_PASSWORD = "Test@Pr56D06"

USER_D10_EMAIL    = f"pr56d10{TS}@testmail.io"
USER_D10_PHONE    = f"162{TS % 100000000:08d}"
USER_D10_PASSWORD = "Test@Pr56D10"

USER_D11_EMAIL    = f"pr56d11{TS}@testmail.io"
USER_D11_PHONE    = f"163{TS % 100000000:08d}"
USER_D11_PASSWORD = "Test@Pr56D11"

USER_D12_EMAIL    = f"pr56d12{TS}@testmail.io"
USER_D12_PHONE    = f"164{TS % 100000000:08d}"
USER_D12_PASSWORD = "Test@Pr56D12"

print(f"\n{BOLD}{CYAN}PR#56（A-18）identity/auth 第二轮缺陷修复 — 接口验收{RESET}")
print(f"  API_BASE    : {API_BASE}")
print(f"  MYSQL       : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  测试管理员  : {ADMIN_EMAIL}")
print(f"  用户 D06    : {USER_D06_EMAIL} (D-06 拒绝理由校验)")
print(f"  用户 D10    : {USER_D10_EMAIL} (D-10 审核通过不写 reject_reason)")
print(f"  用户 D11    : {USER_D11_EMAIL} (D-11 并发审核)")
print(f"  用户 D12    : {USER_D12_EMAIL} (D-12 封禁/解封一致性)")
print()


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 1：确保 admin 角色和必要权限存在
# ════════════════════════════════════════════════════════════════════════════════

print(f"{BOLD}前置准备 Step 1：确保 admin 角色与权限存在{RESET}")

# 确保 admin 角色存在
rows, err = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = None
if rows and not err:
    admin_role_id = int(rows[0][0])
    print(f"  admin 角色已存在: id={admin_role_id}")
else:
    db_exec("INSERT IGNORE INTO roles (code, name, description) VALUES ('admin', '管理员', '系统管理员角色')")
    rows, err = db_query("SELECT id FROM roles WHERE code='admin'")
    if rows and not err:
        admin_role_id = int(rows[0][0])
        print(f"  已创建 admin 角色: id={admin_role_id}")
    else:
        print(f"  {RED}admin 角色创建失败: {err}，中止测试{RESET}")
        raise SystemExit(1)

# 确保 identity:review / user:manage 权限存在
for perm_code, perm_name, resource, action in [
    ("identity:review", "实名认证审核", "identity", "review"),
    ("user:manage",     "用户管理",     "user",     "manage"),
]:
    rows, err = db_query(f"SELECT id FROM permissions WHERE code='{perm_code}'")
    if rows and not err:
        print(f"  {perm_code} 权限已存在: id={rows[0][0]}")
    else:
        db_exec(
            f"INSERT IGNORE INTO permissions (code, name, resource, action) "
            f"VALUES ('{perm_code}', '{perm_name}', '{resource}', '{action}')"
        )
        rows, err = db_query(f"SELECT id FROM permissions WHERE code='{perm_code}'")
        if rows and not err:
            print(f"  已创建 {perm_code} 权限: id={rows[0][0]}")
        else:
            print(f"  {RED}无法获取 {perm_code} 权限 id: {err}{RESET}")

# 绑定所有现有权限到 admin 角色（确保管理员能调用所有接口）
if admin_role_id:
    db_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
        f"SELECT {admin_role_id}, p.id FROM permissions p"
    )
    print(f"  已确保 admin 角色绑定所有现有权限")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 2：注册测试账号（DB 插入验证码 + API 注册）
# ════════════════════════════════════════════════════════════════════════════════

print(f"\n{BOLD}前置准备 Step 2：注册测试账号{RESET}")

# 注册管理员测试账号
print(f"\n  注册管理员账号 {ADMIN_EMAIL}...")
admin_user_id, admin_token = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, "pr56admin")
if admin_user_id:
    print(f"  管理员注册成功: id={admin_user_id}")
    # 绑定 admin 角色
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({admin_user_id}, {admin_role_id})")
    # 设置管理员双重认证（使其能调用 admin 接口）
    db_exec(
        f"UPDATE users SET admin_email_verified_at=NOW(), admin_phone_verified_at=NOW() "
        f"WHERE id={admin_user_id}"
    )
    print(f"  已绑定 admin 角色并设置双重认证")
else:
    print(f"  {RED}管理员账号注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 重新登录以获取有效 token
print(f"\n  重新登录管理员以刷新 token...")
s, r = http("POST", "/api/auth/login/email", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
print(f"  HTTP {s}: code={r.get('code')}")
if s == 200 and r.get("code") == 0:
    admin_token = r["data"]["access_token"]
    print(f"  管理员登录成功")
else:
    print(f"  {RED}管理员登录失败: {json.dumps(r, ensure_ascii=False)[:200]}，中止测试{RESET}")
    raise SystemExit(1)

# 注册 D-06 用户
print(f"\n  注册用户 D06 {USER_D06_EMAIL}...")
user_d06_id, user_d06_token = register_user_via_api(USER_D06_EMAIL, USER_D06_PHONE, USER_D06_PASSWORD, "pr56d06")
if user_d06_id:
    print(f"  用户 D06 注册成功: id={user_d06_id}")
else:
    print(f"  {RED}用户 D06 注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 注册 D-10 用户
print(f"\n  注册用户 D10 {USER_D10_EMAIL}...")
user_d10_id, user_d10_token = register_user_via_api(USER_D10_EMAIL, USER_D10_PHONE, USER_D10_PASSWORD, "pr56d10")
if user_d10_id:
    print(f"  用户 D10 注册成功: id={user_d10_id}")
else:
    print(f"  {RED}用户 D10 注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 注册 D-11 用户
print(f"\n  注册用户 D11 {USER_D11_EMAIL}...")
user_d11_id, user_d11_token = register_user_via_api(USER_D11_EMAIL, USER_D11_PHONE, USER_D11_PASSWORD, "pr56d11")
if user_d11_id:
    print(f"  用户 D11 注册成功: id={user_d11_id}")
else:
    print(f"  {RED}用户 D11 注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 注册 D-12 用户
print(f"\n  注册用户 D12 {USER_D12_EMAIL}...")
user_d12_id, user_d12_token = register_user_via_api(USER_D12_EMAIL, USER_D12_PHONE, USER_D12_PASSWORD, "pr56d12")
if user_d12_id:
    print(f"  用户 D12 注册成功: id={user_d12_id}")
else:
    print(f"  {RED}用户 D12 注册失败，中止测试{RESET}")
    raise SystemExit(1)

print(f"\n  所有测试账号准备完毕")
print(f"  admin_user_id={admin_user_id}, user_d06_id={user_d06_id}, user_d10_id={user_d10_id}, "
      f"user_d11_id={user_d11_id}, user_d12_id={user_d12_id}")


# ════════════════════════════════════════════════════════════════════════════════
# 辅助：用户提交实名认证
# ════════════════════════════════════════════════════════════════════════════════

def submit_identity(token, user_id, real_name, id_card_no):
    """用户提交实名认证，返回 verification_id 或 None。"""
    status, resp = http("POST", "/api/identity/verifications",
                        {"real_name": real_name, "id_card_no": id_card_no},
                        token=token)
    print(f"    提交实名认证  HTTP {status}: code={resp.get('code')}")
    if status in (200, 201) and resp.get("code") == 0:
        vid = resp.get("data", {}).get("id")
        if vid:
            return int(vid)
    elif status == 409:
        print(f"    {YELLOW}409 冲突（已有认证记录），从 DB 查最新 verif_id{RESET}")
    else:
        print(f"    {YELLOW}提交失败: {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
    # 兜底：从 DB 查最新记录
    rows, _ = db_query(
        f"SELECT id FROM identity_verifications WHERE user_id={user_id} "
        f"AND status='pending' ORDER BY id DESC LIMIT 1"
    )
    if rows:
        return int(rows[0][0])
    # 尝试 DB 直接插入
    db_exec(
        f"INSERT INTO identity_verifications (user_id, real_name, id_card_no_hash, "
        f"id_card_no_masked, status, submitted_at, created_at, updated_at) VALUES "
        f"({user_id}, '{real_name}', 'hmac_{user_id}_{TS}', '110101*****{user_id:04d}', "
        f"'pending', NOW(), NOW(), NOW())"
    )
    rows, _ = db_query(
        f"SELECT id FROM identity_verifications WHERE user_id={user_id} "
        f"ORDER BY id DESC LIMIT 1"
    )
    return int(rows[0][0]) if rows else None


# ════════════════════════════════════════════════════════════════════════════════
# D-06：拒绝审核必须填写理由
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-06  拒绝审核必须填写理由{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-06.1 用户 D06 提交实名认证...")
verif_d06_id = submit_identity(user_d06_token, user_d06_id, "张三D06验收", "110101199006061006")
if verif_d06_id:
    print(f"  已获取认证记录 verif_id={verif_d06_id}")
else:
    print(f"  {RED}无法获取用户 D06 的认证记录 ID{RESET}")

if admin_token and verif_d06_id:
    # D-06.a：reason 为空字符串
    print(f"\n  D-06.2 拒绝审核 reason='' （空字符串）...")
    s1, r1 = http("PATCH", f"/api/admin/identity-verifications/{verif_d06_id}/review",
                  {"approve": False, "reason": ""},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message')!r}")
    if s1 == 400 and r1.get("code") == 40000 and "理由" in (r1.get("message") or ""):
        ok("D-06.a  reason='' → HTTP 400, code=40000, message含'驳回时必须填写理由'",
           f"message={r1.get('message')!r}")
    elif s1 == 400 and r1.get("code") == 40000:
        ok("D-06.a  reason='' → HTTP 400, code=40000（message 未严格匹配'理由'但业务码正确）",
           f"message={r1.get('message')!r}")
    else:
        fail("D-06.a  reason='' 应返回 HTTP 400, code=40000",
             f"实际 HTTP={s1}, code={r1.get('code')}, message={r1.get('message')!r}")

    # D-06.b：reason 为纯空格
    print(f"\n  D-06.3 拒绝审核 reason='   ' （纯空格，TrimSpace 检查）...")
    s2, r2 = http("PATCH", f"/api/admin/identity-verifications/{verif_d06_id}/review",
                  {"approve": False, "reason": "   "},
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message')!r}")
    if s2 == 400 and r2.get("code") == 40000:
        ok("D-06.b  reason='   '（纯空格）→ HTTP 400, code=40000（TrimSpace 检查生效）",
           f"message={r2.get('message')!r}")
    else:
        fail("D-06.b  reason='   '（纯空格）应返回 HTTP 400, code=40000",
             f"实际 HTTP={s2}, code={r2.get('code')}, message={r2.get('message')!r}")

    # 验证记录仍为 pending（前两次失败请求不应改变状态）
    rows, _ = db_query(f"SELECT status FROM identity_verifications WHERE id={verif_d06_id}")
    if rows and rows[0][0] == "pending":
        ok("D-06.c  前两次校验失败请求未改变记录状态（仍为 pending）",
           f"status={rows[0][0]!r}")
    else:
        fail("D-06.c  记录状态被意外改变",
             f"实际 status={rows[0][0] if rows else None!r}，期望 pending")

    # D-06.d：reason 非空，正常路径回归
    print(f"\n  D-06.4 拒绝审核 reason='身份证照片模糊'（非空，正常路径）...")
    s3, r3 = http("PATCH", f"/api/admin/identity-verifications/{verif_d06_id}/review",
                  {"approve": False, "reason": "身份证照片模糊"},
                  token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}")
    if s3 == 200 and r3.get("code") == 0:
        ok("D-06.d  reason='身份证照片模糊'（非空）→ HTTP 200，审核成功（正常路径回归）")
    else:
        fail("D-06.d  非空 reason 审核应返回 HTTP 200",
             f"实际 HTTP={s3}, code={r3.get('code')}, resp={json.dumps(r3, ensure_ascii=False)[:200]}")

    # DB 验证最终状态
    rows, _ = db_query(f"SELECT status, reject_reason FROM identity_verifications WHERE id={verif_d06_id}")
    if rows:
        print(f"    最终 status={rows[0][0]!r}, reject_reason={rows[0][1]!r}")
else:
    skip("D-06", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-08：ListPending 死代码删除（回归检查，无行为变更预期）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-08  ListPending 死代码删除（ListPaged 回归检查）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    print(f"\n  D-08.1 GET /api/admin/identity-verifications?status=pending&page=1&page_size=20...")
    s1, r1 = http("GET", "/api/admin/identity-verifications?status=pending&page=1&page_size=20",
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        data = r1.get("data", {})
        items = data.get("list") or data.get("items") or []
        ok("D-08.a  GET ?status=pending&page=1&page_size=20 → HTTP 200，分页列表正常返回",
           f"返回 {len(items)} 条，total={data.get('total')}")
    else:
        fail("D-08.a  GET ?status=pending 应返回 HTTP 200",
             f"实际 HTTP={s1}, code={r1.get('code')}, resp={json.dumps(r1, ensure_ascii=False)[:200]}")

    print(f"\n  D-08.2 GET /api/admin/identity-verifications?page=1&page_size=20（不传 status，查全部）...")
    s2, r2 = http("GET", "/api/admin/identity-verifications?page=1&page_size=20",
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        data = r2.get("data", {})
        items = data.get("list") or data.get("items") or []
        ok("D-08.b  GET 不传 status（查全部状态）→ HTTP 200，正常返回",
           f"返回 {len(items)} 条，total={data.get('total')}")
    else:
        fail("D-08.b  GET 不传 status 应返回 HTTP 200",
             f"实际 HTTP={s2}, code={r2.get('code')}, resp={json.dumps(r2, ensure_ascii=False)[:200]}")
else:
    skip("D-08", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-09 + D-10：审核通过路径回归 + reject_reason 不应被写入
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-09  CreateLog 错误检查（正常路径回归：审核日志有记录）{RESET}")
print(f"{BOLD}D-10  审核通过时不应写入 reject_reason{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-09/10.1 用户 D10 提交实名认证...")
verif_d10_id = submit_identity(user_d10_token, user_d10_id, "李四D10验收", "110101199010101010")
if verif_d10_id:
    print(f"  已获取认证记录 verif_id={verif_d10_id}")

if admin_token and verif_d10_id:
    # approve=true 但同时传一个非空 reason（用于验证 D-10：通过时不写 reject_reason）
    print(f"\n  D-09/10.2 管理员审核通过，同时传非空 reason='审核通过备注信息'...")
    s1, r1 = http("PATCH", f"/api/admin/identity-verifications/{verif_d10_id}/review",
                  {"approve": True, "reason": "审核通过备注信息"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        ok("D-09/10.a  approve=true + 非空 reason → HTTP 200，审核成功")
    else:
        fail("D-09/10.a  审核通过应返回 HTTP 200",
             f"实际 HTTP={s1}, code={r1.get('code')}, resp={json.dumps(r1, ensure_ascii=False)[:200]}")

    time.sleep(0.3)

    # D-09: DB 验证 identity_verification_logs 有对应记录
    print(f"\n  D-09.3 DB 查询 identity_verification_logs ...")
    rows, err = db_query(
        f"SELECT id, verification_id, action, operator_id FROM identity_verification_logs "
        f"WHERE verification_id={verif_d10_id}"
    )
    if rows and not err:
        actions = [r[2] for r in rows]
        ok("D-09.a  identity_verification_logs 中存在 verification_id 对应记录（事务正常提交）",
           f"共 {len(rows)} 条，actions={actions}")
        if len(rows) == 1:
            ok("D-09.b  日志记录数为 1（与单次审核操作一致）")
        else:
            fail("D-09.b  日志记录数不为 1",
                 f"实际 {len(rows)} 条：{rows}")
    else:
        fail("D-09.a  identity_verification_logs 中未找到 verification_id 对应记录",
             f"rows={rows!r}, err={err}")

    # D-10: DB 验证 reject_reason 为 NULL/空
    print(f"\n  D-10.2 DB 查询 identity_verifications.reject_reason ...")
    rows, err = db_query(
        f"SELECT status, IFNULL(reject_reason, '<NULL>') FROM identity_verifications WHERE id={verif_d10_id}"
    )
    if rows and not err:
        status_val, reject_reason_val = rows[0][0], rows[0][1]
        print(f"    status={status_val!r}, reject_reason={reject_reason_val!r}")
        if status_val == "verified":
            ok("D-10.a  DB status=verified（审核通过）")
        else:
            fail("D-10.a  DB status 不为 verified",
                 f"实际 status={status_val!r}")

        if reject_reason_val in ("<NULL>", "", None):
            ok("D-10.b  审核通过时 reject_reason 为 NULL/空（未被写入'审核通过备注信息'）",
               f"reject_reason={reject_reason_val!r}")
        else:
            fail("D-10.b  审核通过时 reject_reason 被错误写入",
                 f"实际 reject_reason={reject_reason_val!r}，期望 NULL/空")
    else:
        fail("D-10  DB 查询 identity_verifications 失败",
             f"rows={rows!r}, err={err}")

    # D-10: GET 详情接口验证 reject_reason 字段也应为空
    print(f"\n  D-10.3 GET 详情接口验证 reject_reason 字段...")
    s2, r2 = http("GET", f"/api/admin/identity-verifications/{verif_d10_id}", token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        detail = r2.get("data", {})
        reject_reason_resp = detail.get("reject_reason")
        status_resp = detail.get("status")
        print(f"    status={status_resp!r}, reject_reason={reject_reason_resp!r}")
        if reject_reason_resp in (None, ""):
            ok("D-10.c  GET 详情接口 reject_reason 字段为空（与 DB 一致）",
               f"reject_reason={reject_reason_resp!r}")
        else:
            fail("D-10.c  GET 详情接口 reject_reason 字段非空",
                 f"实际 reject_reason={reject_reason_resp!r}，期望 NULL/空")
    else:
        fail("D-10.c  GET 详情接口返回非预期",
             f"HTTP={s2}, code={r2.get('code')}, resp={json.dumps(r2, ensure_ascii=False)[:200]}")
else:
    skip("D-09/D-10", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-11：并发审核只有一个成功，另一个返回 409
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-11  并发审核只有一个成功，另一个返回 409/40900{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-11.1 用户 D11 提交实名认证...")
verif_d11_id = submit_identity(user_d11_token, user_d11_id, "王五D11验收", "110101199011111111")
if verif_d11_id:
    print(f"  已获取认证记录 verif_id={verif_d11_id}")

if admin_token and verif_d11_id:
    print(f"\n  D-11.2 并发发起两个审核请求（一个 approve=true，一个 approve=false reason='并发测试'）...")

    concurrent_results = {}

    def do_review(name, body):
        s, r = http("PATCH", f"/api/admin/identity-verifications/{verif_d11_id}/review",
                    body, token=admin_token)
        concurrent_results[name] = (s, r)

    t1 = threading.Thread(target=do_review, args=("approve", {"approve": True, "reason": "并发测试-通过"}))
    t2 = threading.Thread(target=do_review, args=("reject",  {"approve": False, "reason": "并发测试"}))

    t1.start()
    t2.start()
    t1.join()
    t2.join()

    s_a, r_a = concurrent_results["approve"]
    s_r, r_r = concurrent_results["reject"]
    print(f"    approve 请求 → HTTP {s_a}: code={r_a.get('code')}, message={r_a.get('message')!r}")
    print(f"    reject  请求 → HTTP {s_r}: code={r_r.get('code')}, message={r_r.get('message')!r}")

    statuses = [s_a, s_r]
    codes    = [r_a.get("code"), r_r.get("code")]

    success_count = sum(1 for s in statuses if s == 200)
    conflict_count = sum(1 for s, c in zip(statuses, codes) if s == 409 and c == 40900)

    if success_count == 1:
        ok("D-11.a  并发审核恰好一个请求返回 HTTP 200",
           f"approve={s_a}, reject={s_r}")
    else:
        fail("D-11.a  并发审核应恰好一个请求返回 HTTP 200",
             f"实际成功数={success_count}（approve={s_a}, reject={s_r}）")

    if conflict_count == 1:
        ok("D-11.b  并发审核恰好一个请求返回 HTTP 409, code=40900",
           f"approve=({s_a},{r_a.get('code')}), reject=({s_r},{r_r.get('code')})")
    else:
        fail("D-11.b  并发审核应恰好一个请求返回 HTTP 409, code=40900",
             f"实际 409/40900 数={conflict_count}（approve=({s_a},{r_a.get('code')}), "
             f"reject=({s_r},{r_r.get('code')})）")

    time.sleep(0.3)

    # DB 验证：最终 status 应为 verified 或 rejected 之一，且与"赢家"一致
    print(f"\n  D-11.3 DB 验证最终状态与日志数量...")
    rows, err = db_query(f"SELECT status FROM identity_verifications WHERE id={verif_d11_id}")
    final_status = rows[0][0] if rows and not err else None
    print(f"    最终 status={final_status!r}")

    if final_status in ("verified", "rejected"):
        ok("D-11.c  DB 最终 status 为 verified 或 rejected 之一",
           f"final_status={final_status!r}")
        # 与赢家一致性检查
        expected_status = None
        if s_a == 200:
            expected_status = "verified"
        elif s_r == 200:
            expected_status = "rejected"
        if expected_status and final_status == expected_status:
            ok("D-11.d  最终 status 与返回 200 的请求一致",
               f"赢家请求对应 status={expected_status!r}, DB 最终 status={final_status!r}")
        elif expected_status:
            fail("D-11.d  最终 status 与返回 200 的请求不一致",
                 f"赢家请求对应 status={expected_status!r}, 但 DB 最终 status={final_status!r}")
        else:
            skip("D-11.d", "无法判断赢家请求（两个请求均非 200）")
    else:
        fail("D-11.c  DB 最终 status 不是 verified/rejected",
             f"final_status={final_status!r}")

    rows, err = db_query(
        f"SELECT id, action FROM identity_verification_logs WHERE verification_id={verif_d11_id}"
    )
    if rows and not err:
        if len(rows) == 1:
            ok("D-11.e  identity_verification_logs 中该 verification_id 只有 1 条记录（无重复日志）",
               f"记录={rows}")
        else:
            fail("D-11.e  identity_verification_logs 中该 verification_id 记录数不为 1",
                 f"共 {len(rows)} 条：{rows}")
    else:
        fail("D-11.e  identity_verification_logs 查询失败或无记录",
             f"rows={rows!r}, err={err}")
else:
    skip("D-11", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-12：封禁/解封 DB 与 Redis 一致性
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-12  封禁/解封 DB 与 Redis 一致性{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and user_d12_id and user_d12_token:
    redis_key = f"blocked:user:{user_d12_id}"

    # 确认 user_sessions 中有该用户的记录（注册时已生成）
    rows, _ = db_query(f"SELECT COUNT(*) FROM user_sessions WHERE user_id={user_d12_id}")
    print(f"\n  D-12.0 注册后 user_sessions 记录数={rows[0][0] if rows else '?'}")

    # ── 封禁 ──
    print(f"\n  D-12.1 管理员封禁用户 D12 (status=disabled)...")
    s1, r1 = http("PATCH", f"/api/admin/users/{user_d12_id}/status",
                  {"status": "disabled", "reason": "D12验收-封禁测试"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        ok("D-12.a  封禁请求 → HTTP 200")
    else:
        fail("D-12.a  封禁请求应返回 HTTP 200",
             f"实际 HTTP={s1}, code={r1.get('code')}, resp={json.dumps(r1, ensure_ascii=False)[:200]}")

    time.sleep(0.3)

    # DB: users.status = disabled
    rows, err = db_query(f"SELECT status FROM users WHERE id={user_d12_id}")
    db_status = rows[0][0] if rows and not err else None
    print(f"    DB users.status={db_status!r}")
    if db_status == "disabled":
        ok("D-12.b  DB users.status = 'disabled'", f"status={db_status!r}")
    else:
        fail("D-12.b  DB users.status 应为 'disabled'", f"实际 status={db_status!r}")

    # Redis: blocked:user:{userID} 存在，值为 "1"
    redis_val, rerr = redis_cli("get", redis_key)
    print(f"    Redis GET {redis_key} = {redis_val!r}")
    if redis_val == "1":
        ok("D-12.c  Redis key blocked:user:{userID} 存在，值为 '1'", f"value={redis_val!r}")
    else:
        fail("D-12.c  Redis key blocked:user:{userID} 应存在且值为 '1'",
             f"实际 value={redis_val!r}, err={rerr}")

    # DB: user_sessions 该用户所有记录 revoked_at 非 NULL
    rows, err = db_query(
        f"SELECT COUNT(*) FROM user_sessions WHERE user_id={user_d12_id} AND revoked_at IS NULL"
    )
    not_revoked_count = int(rows[0][0]) if rows and not err else -1
    print(f"    user_sessions 中 revoked_at IS NULL 的记录数={not_revoked_count}")
    if not_revoked_count == 0:
        ok("D-12.d  封禁后 user_sessions 该用户所有记录 revoked_at 非 NULL（全部已吊销）")
    else:
        fail("D-12.d  封禁后 user_sessions 存在 revoked_at 为 NULL 的记录（未全部吊销）",
             f"未吊销记录数={not_revoked_count}")

    # ── 解封 ──
    print(f"\n  D-12.2 管理员解封用户 D12 (status=active)...")
    s2, r2 = http("PATCH", f"/api/admin/users/{user_d12_id}/status",
                  {"status": "active", "reason": "D12验收-解封测试"},
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        ok("D-12.e  解封请求 → HTTP 200")
    else:
        fail("D-12.e  解封请求应返回 HTTP 200",
             f"实际 HTTP={s2}, code={r2.get('code')}, resp={json.dumps(r2, ensure_ascii=False)[:200]}")

    time.sleep(0.3)

    # DB: users.status = active
    rows, err = db_query(f"SELECT status FROM users WHERE id={user_d12_id}")
    db_status2 = rows[0][0] if rows and not err else None
    print(f"    DB users.status={db_status2!r}")
    if db_status2 == "active":
        ok("D-12.f  DB users.status = 'active'", f"status={db_status2!r}")
    else:
        fail("D-12.f  DB users.status 应为 'active'", f"实际 status={db_status2!r}")

    # Redis: blocked:user:{userID} 已被删除（不存在）
    redis_val2, rerr2 = redis_cli("get", redis_key)
    print(f"    Redis GET {redis_key} = {redis_val2!r}")
    if redis_val2 in (None, "", "(nil)"):
        ok("D-12.g  Redis key blocked:user:{userID} 已被删除（不存在）", f"value={redis_val2!r}")
    else:
        fail("D-12.g  Redis key blocked:user:{userID} 应已被删除",
             f"实际 value={redis_val2!r}, err={rerr2}")
else:
    skip("D-12", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# 清理：删除测试过程中创建的数据（按照外键约束顺序）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除测试临时数据{RESET}")

all_test_uids = [uid for uid in [admin_user_id, user_d06_id, user_d10_id, user_d11_id, user_d12_id] if uid]

# 清理依赖子表（顺序：子表先删）
for uid in all_test_uids:
    db_exec(f"DELETE FROM identity_verification_logs WHERE user_id={uid}")
    db_exec(f"DELETE FROM identity_verifications WHERE user_id={uid}")
    db_exec(f"DELETE FROM user_permission_overrides WHERE user_id={uid}")
    db_exec(f"DELETE FROM user_roles WHERE user_id={uid}")
    db_exec(f"DELETE FROM user_sessions WHERE user_id={uid}")

# 清理审计日志（本次测试管理员的操作）
db_exec(f"DELETE FROM audit_logs WHERE operator_id={admin_user_id}")
print(f"  已清理测试审计日志（operator_id={admin_user_id}）")

# 清理 Redis 残留 key（如有）
if user_d12_id:
    redis_cli("del", f"blocked:user:{user_d12_id}")
    print(f"  已清理 Redis key blocked:user:{user_d12_id}（如有残留）")

# 删除测试用户
for email in [ADMIN_EMAIL, USER_D06_EMAIL, USER_D10_EMAIL, USER_D11_EMAIL, USER_D12_EMAIL]:
    ok_del, err = db_exec(f"DELETE FROM users WHERE email='{email}'")
    if ok_del:
        print(f"  已删除用户: {email}")
    else:
        print(f"  {YELLOW}删除用户 {email} 时出现问题: {err}{RESET}")

# 注意：admin 角色、identity:review / user:manage 权限保留供后续测试复用
print(f"  注意：admin 角色（id={admin_role_id}）和相关权限已保留")


# ════════════════════════════════════════════════════════════════════════════════
# 汇总
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{CYAN}{'═'*70}{RESET}")
print(f"{BOLD}{CYAN}PR#56（A-18）测试结果汇总{RESET}")
print(f"{BOLD}{CYAN}{'═'*70}{RESET}")

total_pass  = sum(1 for r in results if r[0] == "PASS")
total_fail  = sum(1 for r in results if r[0] == "FAIL")
total_skip  = sum(1 for r in results if r[0] == "SKIP")
total_cases = total_pass + total_fail + total_skip

print(f"  总计: {total_cases}  {GREEN}通过: {total_pass}{RESET}  "
      f"{RED}失败: {total_fail}{RESET}  {YELLOW}跳过: {total_skip}{RESET}")
print()

for status_label, label, detail in results:
    color = GREEN if status_label == "PASS" else (RED if status_label == "FAIL" else YELLOW)
    print(f"  {color}[{status_label}]{RESET} {label}")

print()

# 按缺陷维度汇总
print(f"{BOLD}按缺陷维度：{RESET}")
defect_map = {
    "D-06": "拒绝审核必须填写理由",
    "D-08": "ListPending 死代码删除（ListPaged 回归）",
    "D-09": "CreateLog 错误检查（正常路径回归）",
    "D-10": "审核通过时不应写入 reject_reason",
    "D-11": "并发审核只有一个成功，另一个返回 409",
    "D-12": "封禁/解封 DB 与 Redis 一致性",
}
for dname, ddesc in defect_map.items():
    dlist  = [r for r in results if dname in r[1]]
    fails  = [r for r in dlist if r[0] == "FAIL"]
    passes = [r for r in dlist if r[0] == "PASS"]
    skips  = [r for r in dlist if r[0] == "SKIP"]
    if not dlist:
        print(f"  {YELLOW}{dname} [{ddesc}]: 无用例{RESET}")
    elif fails:
        print(f"  {RED}{dname} [{ddesc}]: 未通过（{len(fails)} 项失败）{RESET}")
    elif passes:
        print(f"  {GREEN}{dname} [{ddesc}]: 通过（{len(passes)} 项）{RESET}")
    else:
        print(f"  {YELLOW}{dname} [{ddesc}]: 跳过（{len(skips)} 项）{RESET}")

print()
if total_fail == 0 and total_skip == 0:
    print(f"{BOLD}{GREEN}结论：PR#56 A-18 全部 {total_pass} 个测试用例通过，6 项缺陷修复验收通过，建议可以合并。{RESET}")
elif total_fail == 0 and total_skip > 0:
    print(f"{BOLD}{YELLOW}结论：PR#56 A-18 通过 {total_pass} 项，跳过 {total_skip} 项（请确认跳过原因），建议谨慎合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#56 A-18 存在 {total_fail} 项未通过，需修复后再合并。{RESET}")

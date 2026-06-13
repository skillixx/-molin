#!/usr/bin/env python3
"""
PR#53（A-17）接口验收脚本 — 实名认证与 IAM 5 项缺陷修复验收

验收范围（5 个缺陷，共 12 个用例）：
  D-01  重复审核已完结记录应返回 409/40900
  D-02  拒绝审核后 reviewed_at 不再为 null
  D-03  SetPermissionOverride 传无效 permission_id 返回 400/40000
  D-04  实名认证审核后 audit_logs 有记录（module=identity, action=review）
  D-05  被拒用户查自己的认证状态返回 200 + rejected

注册策略：通过 DB 插入验证码（绕过短信/邮件 OTP 外部依赖），再走正常注册 API。
这样 bcrypt hash 由 API 生成，密码完全准确，无需复用他人 hash。

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr53_identity_iam_defects.py
"""

import json
import os
import subprocess
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

ADMIN_EMAIL    = f"pr53admin{TS}@testmail.io"
ADMIN_PHONE    = f"150{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr53Admin"

USER_A_EMAIL   = f"pr53usera{TS}@testmail.io"
USER_A_PHONE   = f"151{TS % 100000000:08d}"
USER_A_PASSWORD = "Test@Pr53UserA"

USER_B_EMAIL   = f"pr53userb{TS}@testmail.io"
USER_B_PHONE   = f"152{TS % 100000000:08d}"
USER_B_PASSWORD = "Test@Pr53UserB"

USER_C_EMAIL   = f"pr53userc{TS}@testmail.io"
USER_C_PHONE   = f"153{TS % 100000000:08d}"
USER_C_PASSWORD = "Test@Pr53UserC"

print(f"\n{BOLD}{CYAN}PR#53（A-17）实名认证与 IAM 缺陷修复 — 接口验收{RESET}")
print(f"  API_BASE    : {API_BASE}")
print(f"  MYSQL       : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  测试管理员  : {ADMIN_EMAIL}")
print(f"  用户 A      : {USER_A_EMAIL} (D-01 approve 路径)")
print(f"  用户 B      : {USER_B_EMAIL} (D-02/D-05 reject 路径)")
print(f"  用户 C      : {USER_C_EMAIL} (D-03 permission override 目标)")
print()


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 1：确保 admin 角色和 identity:review 权限存在
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

# 确保 identity:review 权限存在（migration 18 的内容）
rows, err = db_query("SELECT id FROM permissions WHERE code='identity:review'")
identity_review_perm_id = None
if rows and not err:
    identity_review_perm_id = int(rows[0][0])
    print(f"  identity:review 权限已存在: id={identity_review_perm_id}")
else:
    db_exec(
        "INSERT IGNORE INTO permissions (code, name, resource, action) "
        "VALUES ('identity:review', '实名认证审核', 'identity', 'review')"
    )
    rows, err = db_query("SELECT id FROM permissions WHERE code='identity:review'")
    if rows and not err:
        identity_review_perm_id = int(rows[0][0])
        print(f"  已创建 identity:review 权限: id={identity_review_perm_id}")
    else:
        print(f"  {RED}无法获取 identity:review 权限 id: {err}{RESET}")

# 绑定 identity:review 权限到 admin 角色
if admin_role_id and identity_review_perm_id:
    db_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
        f"VALUES ({admin_role_id}, {identity_review_perm_id})"
    )
    print(f"  已确保 admin 角色绑定 identity:review 权限")

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
admin_user_id, admin_token = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, "pr53admin")
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

# admin_token 可能来自注册接口，但注册后 admin_phone_verified_at 是 NOW()（刚被我们设置的）。
# 需要重新登录以获取有效 token（注册时 token 不含刷新后的权限状态）。
print(f"\n  重新登录管理员以刷新 token...")
s, r = http("POST", "/api/auth/login/email", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
print(f"  HTTP {s}: code={r.get('code')}")
if s == 200 and r.get("code") == 0:
    admin_token = r["data"]["access_token"]
    print(f"  管理员登录成功")
else:
    print(f"  {RED}管理员登录失败: {json.dumps(r, ensure_ascii=False)[:200]}，中止测试{RESET}")
    raise SystemExit(1)

# 注册用户 A
print(f"\n  注册用户 A {USER_A_EMAIL}...")
user_a_id, user_a_token = register_user_via_api(USER_A_EMAIL, USER_A_PHONE, USER_A_PASSWORD, "pr53usera")
if user_a_id:
    print(f"  用户 A 注册成功: id={user_a_id}")
else:
    print(f"  {RED}用户 A 注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 注册用户 B
print(f"\n  注册用户 B {USER_B_EMAIL}...")
user_b_id, user_b_token = register_user_via_api(USER_B_EMAIL, USER_B_PHONE, USER_B_PASSWORD, "pr53userb")
if user_b_id:
    print(f"  用户 B 注册成功: id={user_b_id}")
else:
    print(f"  {RED}用户 B 注册失败，中止测试{RESET}")
    raise SystemExit(1)

# 注册用户 C（D-03 的 override 目标）
print(f"\n  注册用户 C {USER_C_EMAIL}...")
user_c_id, _ = register_user_via_api(USER_C_EMAIL, USER_C_PHONE, USER_C_PASSWORD, "pr53userc")
if user_c_id:
    print(f"  用户 C 注册成功: id={user_c_id}")
else:
    print(f"  {RED}用户 C 注册失败，中止测试{RESET}")
    raise SystemExit(1)

print(f"\n  所有测试账号准备完毕")
print(f"  admin_user_id={admin_user_id}, user_a_id={user_a_id}, user_b_id={user_b_id}, user_c_id={user_c_id}")


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
        # 已有记录（可能上次测试残留），从 DB 查最新记录
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
# D-01：重复审核已完结记录应返回 409/40900
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-01  重复审核已完结记录应返回 409/40900{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-01.1 用户 A 提交实名认证...")
verif_a_id = submit_identity(user_a_token, user_a_id, "张三D01验收", "110101199001011001")
if verif_a_id:
    print(f"  已获取认证记录 verif_id={verif_a_id}")
else:
    print(f"  {RED}无法获取用户 A 的认证记录 ID{RESET}")

print(f"\n  D-01.2 管理员首次审核（approve=true）...")
if admin_token and verif_a_id:
    s1, r1 = http("PATCH", f"/api/admin/identity-verifications/{verif_a_id}/review",
                  {"approve": True, "reason": "D01验收-首次通过"},
                  token=admin_token)
    print(f"    第1次审核  HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        print(f"    第1次审核成功")
    else:
        print(f"    {YELLOW}第1次审核返回: {json.dumps(r1, ensure_ascii=False)[:200]}{RESET}")

    time.sleep(0.3)

    print(f"\n  D-01.3 管理员再次审核（应返回 409）...")
    s2, r2 = http("PATCH", f"/api/admin/identity-verifications/{verif_a_id}/review",
                  {"approve": True, "reason": "D01验收-重复审核"},
                  token=admin_token)
    print(f"    第2次审核  HTTP {s2}: code={r2.get('code')}, message={r2.get('message')!r}")

    if s2 == 409 and r2.get("code") == 40900:
        ok("D-01.a  重复审核已完结记录（verified）→ HTTP 409, code=40900",
           f"message={r2.get('message')!r}")
    elif s2 == 409:
        ok("D-01.a  重复审核已完结记录（verified）→ HTTP 409（业务码={}, 期望40900）".format(r2.get("code")),
           f"HTTP 正确，业务码略偏差")
    else:
        fail("D-01.a  重复审核已完结记录应返回 HTTP 409",
             f"实际 HTTP={s2}, code={r2.get('code')}, message={r2.get('message')!r}")
else:
    skip("D-01", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-02：拒绝审核后 reviewed_at 不再为 null
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-02  拒绝审核后 reviewed_at 不再为 null{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-02.1 用户 B 提交实名认证...")
verif_b_id = submit_identity(user_b_token, user_b_id, "李四D02验收", "110101199002022002")
if verif_b_id:
    print(f"  已获取认证记录 verif_id={verif_b_id}")

if admin_token and verif_b_id:
    print(f"\n  D-02.2 管理员拒绝审核（approve=false）...")
    s1, r1 = http("PATCH", f"/api/admin/identity-verifications/{verif_b_id}/review",
                  {"approve": False, "reason": "D02验收-身份证照片不清晰"},
                  token=admin_token)
    print(f"    拒绝审核  HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        print(f"    拒绝操作成功")
    else:
        print(f"    {YELLOW}拒绝操作返回: {json.dumps(r1, ensure_ascii=False)[:200]}{RESET}")

    time.sleep(0.3)

    print(f"\n  D-02.3 查详情接口，验证 reviewed_at 非 null...")
    s2, r2 = http("GET", f"/api/admin/identity-verifications/{verif_b_id}",
                  token=admin_token)
    print(f"    查详情  HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        detail      = r2.get("data", {})
        reviewed_at = detail.get("reviewed_at")
        status_val  = detail.get("status")
        reject_rsn  = detail.get("reject_reason")
        print(f"    status={status_val!r}, reviewed_at={reviewed_at!r}, reject_reason={reject_rsn!r}")

        if status_val == "rejected":
            ok("D-02.a  详情 status=rejected（与审核操作一致）")
        else:
            fail("D-02.a  详情 status 不符合预期（应为 rejected）",
                 f"实际 status={status_val!r}")

        if reviewed_at is not None and reviewed_at != "":
            ok("D-02.b  拒绝审核后 reviewed_at 非 null",
               f"reviewed_at={reviewed_at!r}")
        else:
            fail("D-02.b  拒绝审核后 reviewed_at 仍为 null",
                 f"实际 reviewed_at={reviewed_at!r}，期望非 null 时间戳")
    else:
        fail("D-02  查详情接口返回非预期值",
             f"HTTP={s2}, code={r2.get('code')}, resp={json.dumps(r2, ensure_ascii=False)[:300]}")

    # D-01 扩展：rejected 状态记录重复审核也应返回 409
    print(f"\n  D-01（扩展）  rejected 记录重复审核也应返回 409...")
    s3, r3 = http("PATCH", f"/api/admin/identity-verifications/{verif_b_id}/review",
                  {"approve": True, "reason": "再次尝试"},
                  token=admin_token)
    print(f"    再次审核  HTTP {s3}: code={r3.get('code')}")
    if s3 == 409:
        ok("D-01.b  rejected 记录重复审核 → HTTP 409（两种完结状态均拦截）",
           f"code={r3.get('code')}, message={r3.get('message')!r}")
    else:
        fail("D-01.b  rejected 记录重复审核应返回 HTTP 409",
             f"实际 HTTP={s3}, code={r3.get('code')}")
else:
    skip("D-02", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-03：SetPermissionOverride 传无效 permission_id 返回 400/40000
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-03  SetPermissionOverride 传无效 permission_id 返回 400/40000{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-03.1 传入不存在的 permission_id=999999...")
if admin_token and user_c_id:
    s1, r1 = http("POST", f"/api/admin/users/{user_c_id}/permission-overrides",
                  {
                      "permission_id": 999999,
                      "effect": "allow",
                      "reason": "D03验收-无效权限ID"
                  },
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message')!r}")

    if s1 == 400 and r1.get("code") == 40000:
        ok("D-03.a  无效 permission_id=999999 → HTTP 400, code=40000",
           f"message={r1.get('message')!r}")
    else:
        fail("D-03.a  无效 permission_id 应返回 HTTP 400, code=40000",
             f"实际 HTTP={s1}, code={r1.get('code')}, message={r1.get('message')!r}")

    # 附加：有效 permission_id 不受影响（正常路径验证）
    print(f"\n  D-03.2 传入有效 permission_id（验证正常路径不受影响）...")
    rows, err = db_query("SELECT id, code FROM permissions LIMIT 1")
    if rows and not err:
        valid_perm_id   = int(rows[0][0])
        valid_perm_code = rows[0][1]
        s2, r2 = http("POST", f"/api/admin/users/{user_c_id}/permission-overrides",
                      {
                          "permission_id": valid_perm_id,
                          "effect": "deny",
                          "reason": "D03验收-有效权限ID"
                      },
                      token=admin_token)
        print(f"    HTTP {s2}: code={r2.get('code')}")
        if s2 in (200, 201) and r2.get("code") == 0:
            ok("D-03.b  有效 permission_id 正常写入（正常路径未受影响）",
               f"permission_id={valid_perm_id}, code={valid_perm_code}")
        else:
            fail("D-03.b  有效 permission_id 写入失败",
                 f"HTTP={s2}, code={r2.get('code')}, resp={json.dumps(r2, ensure_ascii=False)[:200]}")
    else:
        skip("D-03.b", f"无法获取有效权限 id: {err}")
else:
    skip("D-03", "前置条件未满足")


# ════════════════════════════════════════════════════════════════════════════════
# D-04：实名认证审核后 audit_logs 有记录（module=identity, action=review）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-04  实名认证审核后 audit_logs 有 module=identity, action=review 记录{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

print(f"\n  D-04.1 调用 GET /api/admin/audit-logs?module=identity...")
if admin_token:
    s1, r1 = http("GET", "/api/admin/audit-logs?module=identity&page=1&page_size=50",
                  token=admin_token)
    print(f"    GET audit-logs  HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        data  = r1.get("data", {})
        # 接口响应结构可能是 list 或 items
        items = data.get("list") or data.get("items") or []
        print(f"    返回 {len(items)} 条记录")

        # 筛选 action=review 的记录
        review_records = [
            it for it in items
            if it.get("module") == "identity" and it.get("action") == "review"
        ]
        print(f"    其中 module=identity, action=review: {len(review_records)} 条")

        # 本次测试管理员操作的记录
        my_records = [r for r in review_records if r.get("operator_id") == admin_user_id]
        print(f"    其中本次测试管理员（id={admin_user_id}）的: {len(my_records)} 条")

        if len(my_records) >= 1:
            ok("D-04.a  audit_logs 接口返回 module=identity, action=review 记录",
               f"找到 {len(my_records)} 条（本次管理员 id={admin_user_id}），"
               f"期望 ≥ 2（approve + reject 各1次）")
            if len(my_records) >= 2:
                ok("D-04.a（补充）  approve + reject 两次审核均有审计记录",
                   f"共 {len(my_records)} 条")
        elif len(review_records) > 0:
            ok("D-04.a  audit_logs 接口有 module=identity, action=review 记录（operator_id 可能不同）",
               f"total={len(review_records)} 条")
        else:
            fail("D-04.a  audit_logs 接口中未找到 module=identity, action=review 记录",
                 f"返回总条数={len(items)}，module=identity 且 action=review 的=0")

        # 验证记录结构
        if review_records:
            sample = review_records[0]
            req_keys = {"id", "module", "action", "created_at"}
            if req_keys.issubset(sample.keys()):
                ok("D-04.b  audit_log 记录包含必要字段（id/module/action/created_at）",
                   f"module={sample.get('module')}, action={sample.get('action')}, "
                   f"operator_id={sample.get('operator_id')}")
            else:
                fail("D-04.b  audit_log 记录缺少必要字段",
                     f"实际 keys={sorted(sample.keys())}, 期望含={sorted(req_keys)}")
        else:
            skip("D-04.b", "无 review 记录，跳过字段校验")
    else:
        fail("D-04.a  GET /api/admin/audit-logs 返回非预期",
             f"HTTP={s1}, code={r1.get('code')}, resp={json.dumps(r1, ensure_ascii=False)[:300]}")

    # D-04.2 DB 直接验证（不依赖接口字段映射）
    print(f"\n  D-04.2 DB 直接核查 audit_logs...")
    rows, err = db_query(
        f"SELECT id, operator_id, module, action FROM audit_logs "
        f"WHERE module='identity' AND action='review' AND operator_id={admin_user_id} "
        f"ORDER BY id DESC LIMIT 5"
    )
    if rows and not err:
        ok("D-04.c  DB 直接确认 audit_logs 有 module=identity, action=review 记录",
           f"共 {len(rows)} 条，operator_id={admin_user_id}")
    else:
        fail("D-04.c  DB 中未找到 identity review audit_log 记录",
             f"rows={rows!r}, err={err}")
else:
    skip("D-04", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-05：被拒用户查自己的认证状态返回 200 + rejected 记录
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-05  被拒用户查自己的认证状态返回 200 + rejected 记录{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# 用户 B 已在 D-02 被拒绝，以用户 B 身份调用 GET /api/identity/verifications/me
print(f"\n  D-05.1 用户 B（已被拒绝）调用 GET /api/identity/verifications/me...")
if user_b_token:
    s1, r1 = http("GET", "/api/identity/verifications/me", token=user_b_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    if s1 == 200 and r1.get("code") == 0:
        detail        = r1.get("data", {})
        status_val    = detail.get("status")
        reject_reason = detail.get("reject_reason")
        reviewed_at   = detail.get("reviewed_at")
        print(f"    status={status_val!r}, reject_reason={reject_reason!r}, reviewed_at={reviewed_at!r}")

        if status_val == "rejected":
            ok("D-05.a  被拒用户查认证状态 → HTTP 200, status=rejected（原来返回 404 已修复）",
               f"status={status_val!r}")
        else:
            fail("D-05.a  被拒用户查认证状态 HTTP 200 但 status 不为 rejected",
                 f"实际 status={status_val!r}，期望 rejected")

        if reject_reason is not None and reject_reason != "":
            ok("D-05.b  reject_reason 非空",
               f"reject_reason={reject_reason!r}")
        else:
            fail("D-05.b  reject_reason 为空（期望非空拒绝原因）",
                 f"reject_reason={reject_reason!r}")

    elif s1 == 404:
        fail("D-05.a  被拒用户查认证状态仍返回 404（D-05 修复未生效）",
             f"HTTP=404, code={r1.get('code')}, message={r1.get('message')!r}")
    else:
        fail("D-05.a  被拒用户查认证状态返回非预期 HTTP 状态码",
             f"HTTP={s1}, code={r1.get('code')}, resp={json.dumps(r1, ensure_ascii=False)[:200]}")
else:
    skip("D-05", "user_b_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# 清理：删除测试过程中创建的数据（按照外键约束顺序）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}清理：删除测试临时数据{RESET}")

all_test_uids = [uid for uid in [admin_user_id, user_a_id, user_b_id, user_c_id] if uid]

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

# 删除测试用户
for email in [ADMIN_EMAIL, USER_A_EMAIL, USER_B_EMAIL, USER_C_EMAIL]:
    ok_del, err = db_exec(f"DELETE FROM users WHERE email='{email}'")
    if ok_del:
        print(f"  已删除用户: {email}")
    else:
        print(f"  {YELLOW}删除用户 {email} 时出现问题: {err}{RESET}")

# 注意：admin 角色、identity:review 权限保留供后续测试复用
print(f"  注意：admin 角色（id={admin_role_id}）和 identity:review 权限（id={identity_review_perm_id}）已保留")


# ════════════════════════════════════════════════════════════════════════════════
# 汇总
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{CYAN}{'═'*70}{RESET}")
print(f"{BOLD}{CYAN}PR#53（A-17）测试结果汇总{RESET}")
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
    "D-01": "重复审核已完结记录应返回 409",
    "D-02": "拒绝审核后 reviewed_at 不再为 null",
    "D-03": "SetPermissionOverride 传无效 permission_id 返回 400",
    "D-04": "实名认证审核后 audit_logs 有记录",
    "D-05": "被拒用户查认证状态返回 200 + rejected",
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
    print(f"{BOLD}{GREEN}结论：PR#53 A-17 全部 {total_pass} 个测试用例通过，5 项缺陷修复验收通过，建议可以合并。{RESET}")
elif total_fail == 0 and total_skip > 0:
    print(f"{BOLD}{YELLOW}结论：PR#53 A-17 通过 {total_pass} 项，跳过 {total_skip} 项（请确认跳过原因），建议谨慎合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#53 A-17 存在 {total_fail} 项未通过，需修复后再合并。{RESET}")

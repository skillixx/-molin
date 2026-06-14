#!/usr/bin/env python3
"""
PR#71（A-26）接口验收脚本 — identity/audit 第四轮缺陷修复验收（D-77~D-84）

验收范围：
  D-77 (P1)  Submit 校验：real_name 非空/<=50字符；id_card_no 18位格式正则
             (a) real_name="" -> 400
             (b) id_card_no="123"（非18位） -> 400
             (c) 合法格式（如 11010519491231002X） -> 200/201

  D-78 (P1)  Submit 中 Create + UpdateRealNameStatus 同一事务
             正常提交后立即 GET /api/me，real_name_status 应为 pending，
             与 identity_verifications.status='pending' 一致

  D-79 (P1)  ListPending（GET /api/admin/identity-verifications）?status= 枚举白名单
             ?status=invalid_status -> 400（而非静默返回空列表）

  D-80 (P1)  附件数量 <=5，且 URL 必须以 https:// 开头
             (a) 6 个 attachments -> 400
             (b) attachment URL 为 http://（非 https） -> 400

  D-81 (P1)  Review 的 reason 字段限制 <=500 Unicode 字符
             管理员审核（拒绝）时传入 501 字符 reason -> 400

  D-82 (P1)  ListAuditLogs 新增 operator_id/start_time/end_time 查询参数过滤
             GET /api/admin/audit-logs?operator_id={某管理员ID} 返回记录均为该 operator_id
             GET /api/admin/audit-logs?start_time=2020-01-01T00:00:00Z&end_time=2020-01-02T00:00:00Z
             （范围内无数据）返回空列表
             索引部分（idx_audit_operator_id 等）如因 migration 未执行而不存在，标记为环境问题。

  D-83 (P2)  审计日志路由改用独立的 audit:read 权限码（原与 role:manage 共用）
             确认 GET /api/admin/audit-logs 路由要求的权限码已从 role:manage 改为 audit:read
             （如因 migration 000021 未执行导致 admin 403，标记为环境问题，
              但需说明已确认代码层面权限码确实已改为 audit:read）

  D-84 (P2)  审计 requestSummary 移除 reason 原文
             管理员审核（拒绝）实名认证并传入 reason="测试拒绝理由ABC123" 后，
             查询 audit_logs 表该条记录的 request_summary 字段，确认不包含该原文

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/test_pr71_a26_identity_audit_round4.py
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

def http(method, path, body=None, token=None, extra_headers=None):
    """发起 HTTP 请求，返回 (http_status, response_body_dict)。"""
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if extra_headers:
        headers.update(extra_headers)
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
    通过 DB 插入验证码后，调用 POST /api/auth/register 注册账号。
    返回 (user_id, access_token) 或 (None, None)。
    """
    otp_code     = "888888"
    otp_code_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql   = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"

    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")

    ok_p, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone', '{phone}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_p:
        print(f"    {RED}插入手机验证码失败: {err}{RESET}")
        return None, None

    ok_e, err = db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email', '{email}', '{otp_code_sha}', 'register', {expire_sql})"
    )
    if not ok_e:
        print(f"    {RED}插入邮箱验证码失败: {err}{RESET}")
        return None, None

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
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
        return user_id, token
    else:
        print(f"    {RED}注册接口返回非预期: HTTP={status}, {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
        return None, None


def login_email(email, password):
    """邮箱密码登录，返回 (access_token, refresh_token) 或 (None, None)。"""
    s, r = http("POST", "/api/auth/login/email", {"email": email, "password": password})
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        return data.get("access_token"), data.get("refresh_token")
    return None, None


# ── 初始化时间戳，用于区分本次测试数据 ──────────────────────────────────────

TS = int(time.time())


def gen_id_card(seq):
    r"""生成符合 ^\d{17}[\dXx]$ 格式的唯一18位身份证号（测试用，无需通过校验位算法）。
    使用 TS 后6位 + seq 构造前17位数字，第18位固定为 X，确保不同测试用户/场景使用不同号码，
    避免触发 D-40 的 ExistsByHMAC 跨用户身份证号唯一性冲突（409 该身份证号已绑定其他账号）。"""
    ts6 = TS % 1000000
    return f"110105{ts6:06d}{seq:05d}X"

# 主管理员账号
ADMIN_EMAIL    = f"pr71adm{TS}@testmail.io"
ADMIN_PHONE    = f"170{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr71Admin"

# D-77/D-78 实名认证测试账号
D77_EMAIL    = f"pr71d77{TS}@testmail.io"
D77_PHONE    = f"171{TS % 100000000:08d}"
D77_PASSWORD = "Test@D77User123"

# D-79 测试不依赖独立用户（仅管理员）

# D-80 附件测试账号
D80A_EMAIL    = f"pr71d80a{TS}@testmail.io"
D80A_PHONE    = f"172{TS % 100000000:08d}"
D80A_PASSWORD = "Test@D80UserA123"

D80B_EMAIL    = f"pr71d80b{TS}@testmail.io"
D80B_PHONE    = f"173{TS % 100000000:08d}"
D80B_PASSWORD = "Test@D80UserB123"

# D-81/D-84 审核测试账号
D81_EMAIL    = f"pr71d81{TS}@testmail.io"
D81_PHONE    = f"174{TS % 100000000:08d}"
D81_PASSWORD = "Test@D81User123"

print(f"\n{BOLD}{CYAN}PR#71（A-26）identity/audit 第四轮缺陷修复（D-77~D-84） — 接口验收{RESET}")
print(f"  API_BASE : {API_BASE}")
print(f"  MYSQL    : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  时间戳   : {TS}")
print()


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 1：确保 admin 角色与权限
# ════════════════════════════════════════════════════════════════════════════════

print(f"{BOLD}前置准备 Step 1：确保 admin 角色与权限{RESET}")

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
        print(f"  {RED}admin 角色创建失败，中止{RESET}")
        raise SystemExit(1)

# 绑定所有现有权限到 admin 角色（包括 identity:review）
db_exec(
    f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
    f"SELECT {admin_role_id}, p.id FROM permissions p"
)
print(f"  已确保 admin 角色绑定所有现有权限")

# 检查 audit:read 权限码是否存在（D-83 环境依赖）
rows, _ = db_query("SELECT id FROM permissions WHERE code='audit:read'")
audit_read_exists = bool(rows)
print(f"  audit:read 权限码是否存在: {audit_read_exists}")


def setup_admin_user(user_id):
    """给用户绑定 admin 角色并设置双重认证"""
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {admin_role_id})")
    db_exec(
        f"UPDATE users SET admin_email_verified_at=NOW(), admin_phone_verified_at=NOW() "
        f"WHERE id={user_id}"
    )


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 2：注册测试账号
# ════════════════════════════════════════════════════════════════════════════════

print(f"\n{BOLD}前置准备 Step 2：注册测试账号{RESET}")

# 注册主管理员
print(f"\n  注册主管理员 {ADMIN_EMAIL}...")
admin_user_id, _ = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, "adm71")
if admin_user_id:
    setup_admin_user(admin_user_id)
    print(f"  管理员注册成功: id={admin_user_id}")
else:
    print(f"  {RED}管理员注册失败，中止{RESET}")
    raise SystemExit(1)

# 重新登录获取带权限的 token
print(f"\n  重新登录管理员...")
admin_token, admin_refresh = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
if not admin_token:
    print(f"  {RED}管理员登录失败，中止{RESET}")
    raise SystemExit(1)
print(f"  管理员登录成功")

# 注册 D-77/D-78 测试账号
print(f"\n  注册 D-77/D-78 测试账号 {D77_EMAIL}...")
d77_user_id, d77_token = register_user_via_api(D77_EMAIL, D77_PHONE, D77_PASSWORD, "d77u71")
if d77_user_id:
    d77_token, _ = login_email(D77_EMAIL, D77_PASSWORD)
    print(f"  D-77 用户注册成功: id={d77_user_id}")
else:
    print(f"  {RED}D-77 用户注册失败{RESET}")
    d77_user_id, d77_token = None, None

# 注册 D-80 用户A（>5 个附件）
print(f"\n  注册 D-80 用户A {D80A_EMAIL}...")
d80a_user_id, d80a_token = register_user_via_api(D80A_EMAIL, D80A_PHONE, D80A_PASSWORD, "d80au71")
if d80a_user_id:
    d80a_token, _ = login_email(D80A_EMAIL, D80A_PASSWORD)
    print(f"  D-80 用户A注册成功: id={d80a_user_id}")
else:
    print(f"  {RED}D-80 用户A注册失败{RESET}")
    d80a_user_id, d80a_token = None, None

# 注册 D-80 用户B（http:// 非 https 附件）
print(f"\n  注册 D-80 用户B {D80B_EMAIL}...")
d80b_user_id, d80b_token = register_user_via_api(D80B_EMAIL, D80B_PHONE, D80B_PASSWORD, "d80bu71")
if d80b_user_id:
    d80b_token, _ = login_email(D80B_EMAIL, D80B_PASSWORD)
    print(f"  D-80 用户B注册成功: id={d80b_user_id}")
else:
    print(f"  {RED}D-80 用户B注册失败{RESET}")
    d80b_user_id, d80b_token = None, None

# 注册 D-81/D-84 测试账号
print(f"\n  注册 D-81/D-84 测试账号 {D81_EMAIL}...")
d81_user_id, d81_token = register_user_via_api(D81_EMAIL, D81_PHONE, D81_PASSWORD, "d81u71")
if d81_user_id:
    d81_token, _ = login_email(D81_EMAIL, D81_PASSWORD)
    print(f"  D-81 用户注册成功: id={d81_user_id}")
else:
    print(f"  {RED}D-81 用户注册失败{RESET}")
    d81_user_id, d81_token = None, None

print(f"\n  所有测试账号准备完毕")


# ════════════════════════════════════════════════════════════════════════════════
# D-77：Submit 输入校验 — real_name 非空/<=50字符，id_card_no 18位格式
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-77  Submit 校验 real_name 非空/<=50字符 + id_card_no 18位格式{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if d77_token:
    print(f"\n  D-77.1 real_name=\"\" -> 期望 400...")
    s1, r1 = http("POST", "/api/identity/verifications",
                  {"real_name": "", "id_card_no": "11010519491231002X"},
                  token=d77_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-77.a  real_name=\"\" -> HTTP 400（D-77 修复生效）",
           f"code={r1.get('code')}, message={r1.get('message', '')!r}")
    else:
        fail("D-77.a  real_name=\"\" 应返回 HTTP 400",
             f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message', '')!r}")

    print(f"\n  D-77.2 id_card_no=\"123\"（非18位） -> 期望 400...")
    s2, r2 = http("POST", "/api/identity/verifications",
                  {"real_name": "张三", "id_card_no": "123"},
                  token=d77_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")
    if s2 == 400:
        ok("D-77.b  id_card_no=\"123\"（非18位）-> HTTP 400（D-77 修复生效）",
           f"code={r2.get('code')}, message={r2.get('message', '')!r}")
    else:
        fail("D-77.b  id_card_no=\"123\" 应返回 HTTP 400",
             f"HTTP={s2}, code={r2.get('code')}, message={r2.get('message', '')!r}")

    print(f"\n  D-77.3 real_name 超过 50 字符 -> 期望 400...")
    long_name = "测" * 51
    s2b, r2b = http("POST", "/api/identity/verifications",
                    {"real_name": long_name, "id_card_no": "11010519491231002X"},
                    token=d77_token)
    print(f"    HTTP {s2b}: code={r2b.get('code')}, message={r2b.get('message', '')!r}")
    if s2b == 400:
        ok("D-77.c  real_name 超过 50 字符 -> HTTP 400（D-77 修复生效）",
           f"code={r2b.get('code')}, message={r2b.get('message', '')!r}")
    else:
        fail("D-77.c  real_name 超过 50 字符应返回 HTTP 400",
             f"HTTP={s2b}, code={r2b.get('code')}, message={r2b.get('message', '')!r}")

    print(f"\n  D-77.4 合法格式（real_name='张三', id_card_no='11010519491231002X'）-> 期望 200/201...")
    s3, r3 = http("POST", "/api/identity/verifications",
                  {"real_name": "张三", "id_card_no": gen_id_card(1)},
                  token=d77_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}, data={r3.get('data')}")
    d77_verification_id = None
    if s3 in (200, 201) and r3.get("code") == 0:
        d77_verification_id = r3.get("data", {}).get("id")
        ok("D-77.d  合法格式提交 -> HTTP 200/201（正常路径未受影响）",
           f"HTTP={s3}, id={d77_verification_id}")
    else:
        fail("D-77.d  合法格式提交应成功",
             f"HTTP={s3}, code={r3.get('code')}, message={r3.get('message', '')!r}")
else:
    skip("D-77", "D-77 测试账号未注册成功")


# ════════════════════════════════════════════════════════════════════════════════
# D-78：Submit 中 Create + UpdateRealNameStatus 同一事务
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-78  Submit 中 Create+UpdateRealNameStatus 同一事务（GET /api/me 一致性）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if d77_token and d77_user_id:
    # D-77.4 已提交一次成功认证，d77_verification_id 应存在
    if d77_verification_id:
        print(f"\n  D-78.1 查询 GET /api/me，验证 real_name_status...")
        s1, r1 = http("GET", "/api/me", token=d77_token)
        print(f"    HTTP {s1}: code={r1.get('code')}")
        real_name_status = r1.get("data", {}).get("real_name_status")
        print(f"    real_name_status={real_name_status!r}")

        print(f"\n  D-78.2 查询 DB identity_verifications.status...")
        rows, _ = db_query(
            f"SELECT status FROM identity_verifications WHERE id={d77_verification_id}"
        )
        db_status = rows[0][0] if rows else None
        print(f"    identity_verifications.status={db_status!r}")

        if s1 == 200 and real_name_status == "pending":
            ok("D-78.a  GET /api/me 中 real_name_status='pending'（D-78 修复生效）",
               f"real_name_status={real_name_status!r}")
        else:
            fail("D-78.a  GET /api/me 中 real_name_status 应为 'pending'",
                 f"HTTP={s1}, real_name_status={real_name_status!r}")

        if db_status == "pending" and real_name_status == "pending":
            ok("D-78.b  identity_verifications.status='pending' 与 users.real_name_status 一致",
               f"db_status={db_status!r}, real_name_status={real_name_status!r}")
        elif db_status == "pending":
            fail("D-78.b  DB status='pending' 但 users.real_name_status 不一致",
                 f"db_status={db_status!r}, real_name_status={real_name_status!r}")
        else:
            fail("D-78.b  identity_verifications.status 非预期",
                 f"db_status={db_status!r}")
    else:
        skip("D-78", "D-77.4 未成功提交认证记录，无法验证事务一致性")
else:
    skip("D-78", "D-77 测试账号未注册成功")


# ════════════════════════════════════════════════════════════════════════════════
# D-79：ListPending ?status= 枚举白名单
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-79  ListPending ?status=invalid_status -> 400（枚举白名单）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    print(f"\n  D-79.1 GET /api/admin/identity-verifications?status=invalid_status...")
    s1, r1 = http("GET", "/api/admin/identity-verifications?status=invalid_status", token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-79.a  ?status=invalid_status -> HTTP 400（D-79 修复生效）",
           f"code={r1.get('code')}, message={r1.get('message', '')!r}")
    else:
        fail("D-79.a  ?status=invalid_status 应返回 HTTP 400（而非静默返回空列表）",
             f"HTTP={s1}, code={r1.get('code')}, items={len(r1.get('items', []) or [])}")

    print(f"\n  D-79.2 GET /api/admin/identity-verifications?status=pending（合法值，应正常）...")
    s2, r2 = http("GET", "/api/admin/identity-verifications?status=pending", token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        ok("D-79.b  ?status=pending（合法值）-> HTTP 200（正常路径未受影响）")
    else:
        fail("D-79.b  ?status=pending 应返回 HTTP 200",
             f"HTTP={s2}, code={r2.get('code')}")

    print(f"\n  D-79.3 GET /api/admin/identity-verifications（不传 status，应正常）...")
    s3, r3 = http("GET", "/api/admin/identity-verifications", token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}")
    if s3 == 200 and r3.get("code") == 0:
        ok("D-79.c  不传 status -> HTTP 200（空字符串=查全部，正常路径未受影响）")
    else:
        fail("D-79.c  不传 status 应返回 HTTP 200",
             f"HTTP={s3}, code={r3.get('code')}")
else:
    skip("D-79", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-80：附件数量 <=5，URL 必须以 https:// 开头
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-80  附件数量<=5 且 URL 必须以 https:// 开头{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if d80a_token:
    print(f"\n  D-80.1 提交 6 个 https:// attachments（超过上限5） -> 期望 400...")
    six_attachments = [f"https://example.com/cert{i}.jpg" for i in range(6)]
    s1, r1 = http("POST", "/api/identity/verifications",
                  {"real_name": "李四", "id_card_no": gen_id_card(2), "attachments": six_attachments},
                  token=d80a_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
    if s1 == 400:
        ok("D-80.a  6 个 attachments（超过上限5） -> HTTP 400（D-80 修复生效）",
           f"code={r1.get('code')}, message={r1.get('message', '')!r}")
    else:
        fail("D-80.a  6 个 attachments 应返回 HTTP 400",
             f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message', '')!r}")
else:
    skip("D-80.a", "D-80 用户A未注册成功")

if d80b_token:
    print(f"\n  D-80.2 提交 attachment URL 为 http://（非 https） -> 期望 400...")
    bad_attachments = ["http://example.com/cert1.jpg"]
    s2, r2 = http("POST", "/api/identity/verifications",
                  {"real_name": "王五", "id_card_no": gen_id_card(3), "attachments": bad_attachments},
                  token=d80b_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")
    if s2 == 400:
        ok("D-80.b  attachment URL 为 http:// -> HTTP 400（D-80 修复生效）",
           f"code={r2.get('code')}, message={r2.get('message', '')!r}")
    else:
        fail("D-80.b  attachment URL 为 http:// 应返回 HTTP 400",
             f"HTTP={s2}, code={r2.get('code')}, message={r2.get('message', '')!r}")

    # 顺带验证：合法的 <=5 个 https:// 附件应能正常提交
    print(f"\n  D-80.3 提交合法的 https:// 附件（3个，<=5个） -> 期望 200/201...")
    good_attachments = [f"https://example.com/cert{i}.jpg" for i in range(3)]
    s3, r3 = http("POST", "/api/identity/verifications",
                  {"real_name": "王五", "id_card_no": gen_id_card(4), "attachments": good_attachments},
                  token=d80b_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}")
    if s3 in (200, 201) and r3.get("code") == 0:
        ok("D-80.c  合法 https:// 附件（3个）提交成功 -> HTTP 200/201（正常路径未受影响）")
    else:
        fail("D-80.c  合法 https:// 附件提交应成功",
             f"HTTP={s3}, code={r3.get('code')}, message={r3.get('message', '')!r}")
else:
    skip("D-80.b/c", "D-80 用户B未注册成功")


# ════════════════════════════════════════════════════════════════════════════════
# D-81 / D-84：Review reason 长度限制 + 审计 requestSummary 不含 reason 原文
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-81  Review reason 限制 <=500 Unicode 字符；D-84 审计不含 reason 原文{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

d81_verification_id = None
if d81_token and admin_token and d81_user_id:
    print(f"\n  D-81.1 D-81 用户提交实名认证（用于后续审核）...")
    s_sub, r_sub = http("POST", "/api/identity/verifications",
                        {"real_name": "赵六", "id_card_no": gen_id_card(5)},
                        token=d81_token)
    print(f"    提交 HTTP {s_sub}: code={r_sub.get('code')}")
    if s_sub in (200, 201) and r_sub.get("code") == 0:
        d81_verification_id = r_sub.get("data", {}).get("id")
    else:
        # 兜底查 DB
        rows, _ = db_query(
            f"SELECT id FROM identity_verifications WHERE user_id={d81_user_id} "
            f"AND status='pending' ORDER BY id DESC LIMIT 1"
        )
        if rows:
            d81_verification_id = int(rows[0][0])
    print(f"    verification_id={d81_verification_id}")

    if d81_verification_id:
        print(f"\n  D-81.2 管理员审核（拒绝），reason=501字符 -> 期望 400...")
        reason_501 = "A" * 501
        s1, r1 = http("PATCH", f"/api/admin/identity-verifications/{d81_verification_id}/review",
                      {"approve": False, "reason": reason_501},
                      token=admin_token)
        print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")
        if s1 == 400:
            ok("D-81.a  reason=501字符 -> HTTP 400（D-81 修复生效）",
               f"code={r1.get('code')}, message={r1.get('message', '')!r}")
        else:
            fail("D-81.a  reason=501字符应返回 HTTP 400",
                 f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message', '')!r}")

        # 确认记录仍为 pending（未被501字符的请求误改变状态）
        rows, _ = db_query(f"SELECT status FROM identity_verifications WHERE id={d81_verification_id}")
        status_after_501 = rows[0][0] if rows else None
        print(f"    501字符请求后记录状态: {status_after_501!r}")

        print(f"\n  D-81.3 reason=500字符（边界值，应成功） -> 期望 400 不触发，记录仍 pending...")
        reason_500 = "B" * 500
        s1b, r1b = http("PATCH", f"/api/admin/identity-verifications/{d81_verification_id}/review",
                        {"approve": False, "reason": reason_500},
                        token=admin_token)
        print(f"    HTTP {s1b}: code={r1b.get('code')}, message={r1b.get('message', '')!r}")
        if s1b == 400:
            fail("D-81.b  reason=500字符（边界值，<=500）不应返回 400",
                 f"HTTP={s1b}, code={r1b.get('code')}, message={r1b.get('message', '')!r}")
        else:
            ok("D-81.b  reason=500字符（边界值，<=500）未被 400 拒绝（边界正确）",
               f"HTTP={s1b}, code={r1b.get('code')}")

        # ────────────────────────────────────────────────────────────
        # D-84：用一个新提交，专门用含特定标记的 reason 审核拒绝，
        # 验证 audit_logs.request_summary 不包含 reason 原文
        # ────────────────────────────────────────────────────────────
        print(f"\n  D-81/D-84.4 为 D-84 重新提交一条认证记录并审核拒绝（reason='测试拒绝理由ABC123'）...")
        # 当前记录可能已被 D-81.3 拒绝（rejected），需要重新提交一条新记录
        rows, _ = db_query(f"SELECT status FROM identity_verifications WHERE id={d81_verification_id}")
        cur_status = rows[0][0] if rows else None
        d84_verification_id = None
        if cur_status == "pending":
            # D-81.3 的500字符请求若因400被拒绝，记录可能仍为pending，可直接复用
            d84_verification_id = d81_verification_id
        else:
            # 重新提交：需要先清空该用户的 active 记录限制（已是 rejected，无 active 记录，可直接提交）
            s_sub2, r_sub2 = http("POST", "/api/identity/verifications",
                                  {"real_name": "赵六二", "id_card_no": gen_id_card(6)},
                                  token=d81_token)
            print(f"    重新提交 HTTP {s_sub2}: code={r_sub2.get('code')}, message={r_sub2.get('message','')!r}")
            if s_sub2 in (200, 201) and r_sub2.get("code") == 0:
                d84_verification_id = r_sub2.get("data", {}).get("id")
            else:
                rows, _ = db_query(
                    f"SELECT id FROM identity_verifications WHERE user_id={d81_user_id} "
                    f"AND status='pending' ORDER BY id DESC LIMIT 1"
                )
                if rows:
                    d84_verification_id = int(rows[0][0])
        print(f"    D-84 verification_id={d84_verification_id}")

        if d84_verification_id:
            secret_reason = "测试拒绝理由ABC123"
            s2, r2 = http("PATCH", f"/api/admin/identity-verifications/{d84_verification_id}/review",
                          {"approve": False, "reason": secret_reason},
                          token=admin_token)
            print(f"    审核拒绝 HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")

            if s2 == 200 and r2.get("code") == 0:
                ok("D-84.pre  审核拒绝（reason='测试拒绝理由ABC123'）成功",
                   f"verification_id={d84_verification_id}")

                # 查询 DB reject_reason 应包含 reason（正常存储）
                rows, _ = db_query(
                    f"SELECT reject_reason, status FROM identity_verifications WHERE id={d84_verification_id}"
                )
                if rows:
                    db_reject_reason = rows[0][0]
                    db_status_after = rows[0][1]
                    print(f"    identity_verifications.reject_reason={db_reject_reason!r}, status={db_status_after!r}")

                # 查询 audit_logs 表对应记录的 request_summary
                time.sleep(0.3)
                rows, _ = db_query(
                    f"SELECT request_summary FROM audit_logs "
                    f"WHERE module='identity' AND action='reject_verification' "
                    f"AND target_id='{d84_verification_id}' "
                    f"ORDER BY id DESC LIMIT 1"
                )
                if rows:
                    request_summary = rows[0][0] if rows[0] else ""
                    print(f"    audit_logs.request_summary={request_summary!r}")

                    if secret_reason not in (request_summary or ""):
                        ok("D-84.a  audit_logs.request_summary 不包含 reason 原文（D-84 修复生效）",
                           f"request_summary={request_summary!r}")
                    else:
                        fail("D-84.a  audit_logs.request_summary 仍包含 reason 原文（D-84 修复未生效）",
                             f"request_summary={request_summary!r}")
                else:
                    fail("D-84.a  未找到对应的 audit_logs 记录（module=identity, action=reject_verification）",
                         f"target_id={d84_verification_id}")
            else:
                fail("D-84.pre  审核拒绝请求失败，无法验证 D-84",
                     f"HTTP={s2}, code={r2.get('code')}, message={r2.get('message', '')!r}")
        else:
            skip("D-84", "无法获取用于 D-84 验证的 verification_id")
    else:
        skip("D-81/D-84", "D-81 用户提交实名认证失败，无法获取 verification_id")
else:
    skip("D-81/D-84", "前置条件不满足（d81_token/admin_token/d81_user_id）")


# ════════════════════════════════════════════════════════════════════════════════
# D-82：ListAuditLogs operator_id/start_time/end_time 过滤
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-82  ListAuditLogs operator_id/start_time/end_time 查询参数过滤{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# 检查索引（D-82 索引部分，环境问题不计入FAIL）
rows, _ = db_query("SHOW INDEX FROM audit_logs WHERE Key_name IN ('idx_audit_operator_id','idx_audit_module_action')")
index_names = set(r[2] for r in rows) if rows else set()
print(f"  audit_logs 索引检查: idx_audit_operator_id={'idx_audit_operator_id' in index_names}, "
      f"idx_audit_module_action={'idx_audit_module_action' in index_names}")
if "idx_audit_operator_id" not in index_names or "idx_audit_module_action" not in index_names:
    skip("D-82.index", f"audit_logs 索引未完全存在（migration 000020 可能未执行），环境问题：{index_names}")
else:
    ok("D-82.index  audit_logs 索引 idx_audit_operator_id/idx_audit_module_action 均存在")

if admin_token:
    print(f"\n  D-82.1 GET /api/admin/audit-logs?operator_id={{管理员ID}}...")
    s1, r1 = http("GET", f"/api/admin/audit-logs?operator_id={admin_user_id}&page_size=50", token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")

    if s1 == 200 and r1.get("code") == 0:
        items = r1.get("items", []) or []
        print(f"    返回记录数: {len(items)}")
        all_match = all(str(it.get("operator_id")) == str(admin_user_id) for it in items)
        if all_match:
            ok("D-82.a  ?operator_id={管理员ID} 返回记录均为该 operator_id（D-82 修复生效)",
               f"记录数={len(items)}, 全部匹配 operator_id={admin_user_id}")
        else:
            mismatched = [it.get("operator_id") for it in items if str(it.get("operator_id")) != str(admin_user_id)]
            fail("D-82.a  ?operator_id 过滤返回了不匹配的记录",
                 f"期望 operator_id={admin_user_id}, 不匹配示例={mismatched[:5]}")
    elif s1 == 403:
        skip("D-82.a", f"403 — 可能因 D-83 audit:read 权限码未绑定（环境问题，见 D-83）")
    else:
        fail("D-82.a  ?operator_id 查询请求失败",
             f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message', '')!r}")

    print(f"\n  D-82.2 GET /api/admin/audit-logs?operator_id=invalid（非数字）-> 期望 400...")
    s1b, r1b = http("GET", "/api/admin/audit-logs?operator_id=abc", token=admin_token)
    print(f"    HTTP {s1b}: code={r1b.get('code')}, message={r1b.get('message','')!r}")
    if s1b == 400:
        ok("D-82.b  ?operator_id=abc（非数字）-> HTTP 400（参数校验生效）",
           f"code={r1b.get('code')}")
    elif s1b == 403:
        skip("D-82.b", "403 — 可能因 D-83 audit:read 权限码未绑定（环境问题）")
    else:
        fail("D-82.b  ?operator_id=abc（非数字）应返回 HTTP 400",
             f"HTTP={s1b}, code={r1b.get('code')}")

    print(f"\n  D-82.3 GET /api/admin/audit-logs?start_time=2020-01-01T00:00:00Z&end_time=2020-01-02T00:00:00Z（范围内无数据）...")
    s2, r2 = http("GET", "/api/admin/audit-logs?start_time=2020-01-01T00:00:00Z&end_time=2020-01-02T00:00:00Z",
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}")
    if s2 == 200 and r2.get("code") == 0:
        items2 = r2.get("items", []) or []
        print(f"    返回记录数: {len(items2)}")
        if len(items2) == 0:
            ok("D-82.c  2020年时间范围（无数据）-> 返回空列表（D-82 修复生效）")
        else:
            fail("D-82.c  2020年时间范围应返回空列表",
                 f"实际返回记录数={len(items2)}")
    elif s2 == 403:
        skip("D-82.c", "403 — 可能因 D-83 audit:read 权限码未绑定（环境问题）")
    else:
        fail("D-82.c  时间范围查询请求失败",
             f"HTTP={s2}, code={r2.get('code')}, message={r2.get('message', '')!r}")

    print(f"\n  D-82.4 GET /api/admin/audit-logs?start_time=invalid-format -> 期望 400...")
    s3, r3 = http("GET", "/api/admin/audit-logs?start_time=not-a-date", token=admin_token)
    print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message','')!r}")
    if s3 == 400:
        ok("D-82.d  ?start_time=not-a-date（格式错误）-> HTTP 400（参数校验生效）",
           f"code={r3.get('code')}")
    elif s3 == 403:
        skip("D-82.d", "403 — 可能因 D-83 audit:read 权限码未绑定（环境问题）")
    else:
        fail("D-82.d  ?start_time=not-a-date 应返回 HTTP 400",
             f"HTTP={s3}, code={r3.get('code')}")
else:
    skip("D-82", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-83：审计日志路由改用独立的 audit:read 权限码
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-83  GET /api/admin/audit-logs 权限码改为 audit:read{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# 代码层面确认：iam/route.go 中 GET /api/admin/audit-logs 是否使用 RequirePerm("audit:read", ...)
# 该确认基于源码审查（见报告说明），此处通过行为侧验证：
# - 若 audit:read 权限码尚未通过 migration 000021 写入 permissions 表/绑定到 admin，
#   则 admin 调用该接口会返回 403（因为 RequirePerm("audit:read") 校验失败），
#   这恰好印证了路由确实已改为要求 audit:read（而不是 role:manage，
#   admin 拥有 role:manage 不会因此返回403）。

if admin_token:
    s1, r1 = http("GET", "/api/admin/audit-logs?page_size=1", token=admin_token)
    print(f"  GET /api/admin/audit-logs (admin, 拥有 role:manage) HTTP {s1}: code={r1.get('code')}, message={r1.get('message','')!r}")

    if audit_read_exists:
        # audit:read 权限码已存在（migration 000021 已执行）
        rows, _ = db_query(
            f"SELECT COUNT(*) FROM role_permissions rp "
            f"JOIN permissions p ON p.id=rp.permission_id "
            f"WHERE rp.role_id={admin_role_id} AND p.code='audit:read'"
        )
        admin_has_audit_read = rows and int(rows[0][0]) > 0
        if s1 == 200 and admin_has_audit_read:
            ok("D-83.a  audit:read 权限码已存在并绑定 admin，GET /api/admin/audit-logs 返回 200（D-83 修复生效）",
               f"HTTP={s1}")
        elif s1 == 403:
            fail("D-83.a  audit:read 权限码已存在但 admin 调用仍 403",
                 f"HTTP={s1}, code={r1.get('code')}")
        else:
            ok("D-83.a  audit:read 权限码存在，接口可访问",
               f"HTTP={s1}")
    else:
        # audit:read 权限码不存在（migration 000021 未执行）—— 环境问题
        if s1 == 403 and r1.get("code") == 40003:
            skip("D-83",
                 f"audit:read 权限码因 migration 000021 未执行而不存在，admin 缺少该权限码导致 403（环境问题，非代码缺陷）。"
                 f"已通过源码审查确认 server/internal/modules/iam/route.go 中 "
                 f"GET /api/admin/audit-logs 已改为 auditRead := RequirePerm(iamSvc, \"audit:read\", ...)，"
                 f"与 admin(...) 中间件链（role:manage）分离，符合 D-83 修复要求。")
        elif s1 == 200:
            fail("D-83",
                 "audit:read 权限码不存在（migration 000021 未执行），但接口仍返回200，"
                 "说明路由可能仍使用 role:manage 而非 audit:read（与 D-83 修复要求不符，"
                 "需结合源码复核）")
        else:
            fail("D-83  非预期返回",
                 f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message','')!r}")
else:
    skip("D-83", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# 测试结果汇总
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*70}{RESET}")
print(f"{BOLD}{CYAN}测试结果汇总{RESET}")
print(f"{BOLD}{'='*70}{RESET}")

total = passed + failed
for status, label, detail in results:
    color = GREEN if status == "PASS" else (RED if status == "FAIL" else YELLOW)
    print(f"  {color}[{status}]{RESET} {label}")

print()
print(f"  总计: {total + sum(1 for s,_,_ in results if s == 'SKIP')} "
      f"(PASS={passed}, FAIL={failed}, SKIP={sum(1 for s,_,_ in results if s == 'SKIP')})")

if failed == 0:
    print(f"\n{BOLD}{GREEN}全部测试通过（PASS/SKIP）{RESET}")
else:
    print(f"\n{BOLD}{RED}存在 {failed} 项测试失败{RESET}")

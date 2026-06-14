#!/usr/bin/env python3
"""
PR#70（A-25）接口验收脚本 — iam 用户分组（user-groups）模块第四轮缺陷修复验收

验收范围（D-67 ~ D-76）：
  D-67  IncrUsedCountAtomic 增加 AND status='active' 条件
        （邀请码先禁用，再用其加入分组 → 不应成功递增 used_count / 不应成功加入）
  D-68  CreateInviteCode 的 default_group_role 枚举校验（仅 admin/member）
        （传 "superadmin" → 400）
  D-69  CreateInviteCode 拒绝 max_uses < 0（传 -1 → 400）
  D-70  GenerateCode 改为 crypto/rand（代码审查确认，标记 skip；验证邀请码生成功能本身正常）
  D-71  AddMember 新增 ExistsGroupByID 校验（不存在的 groupID → 404）
  D-72  CreateInviteCode 拒绝 expires_at 为过去时间（传昨天 → 400）
  D-73  CreateGroup/UpdateGroup 的 type 字段枚举校验（region/org/custom，非法值 → 400）
  D-74  GetGroup 区分 ErrGroupNotFound（404）与其他 DB 错误（500）
        （GET 不存在的分组 ID → 404，非泛化 500）
  D-75  DeleteGroup 事务内清理所有邀请码记录（含 disabled 状态）
        （创建分组→创建邀请码→禁用邀请码→删除分组 → DB 中无残留邀请码记录）
  D-76  CreateGroup/UpdateGroup/CreateInviteCode 字符串字段长度上限校验
        （CreateGroup name=300字符 → 400）

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/test_pr70_a25_iam_group_round4.py
"""

import json
import os
import time
import hashlib
import urllib.error
import urllib.request
import subprocess
import datetime

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

ADMIN_EMAIL    = f"pr70adm{TS}@testmail.io"
ADMIN_PHONE    = f"170{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr70Admin"

# D-67 普通成员用户（用其加入分组）
D67_EMAIL    = f"pr70d67{TS}@testmail.io"
D67_PHONE    = f"171{TS % 100000000:08d}"
D67_PASSWORD = "Test@Pr70D67"

print(f"\n{BOLD}{CYAN}PR#70（A-25）iam 用户分组（user-groups）模块第四轮缺陷修复 — 接口验收{RESET}")
print(f"  API_BASE : {API_BASE}")
print(f"  MYSQL    : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  时间戳   : {TS}")
print()


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备 Step 1：确保 admin 角色拥有 group:manage 权限
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

# group:manage 权限
rows, err = db_query("SELECT id FROM permissions WHERE code='group:manage'")
group_manage_perm_id = None
if rows and not err:
    group_manage_perm_id = int(rows[0][0])
else:
    db_exec("INSERT IGNORE INTO permissions (code, name, resource, action) VALUES ('group:manage', '分组管理', 'group', 'manage')")
    rows, _ = db_query("SELECT id FROM permissions WHERE code='group:manage'")
    if rows:
        group_manage_perm_id = int(rows[0][0])

# 绑定所有现有权限到 admin 角色（含 group:manage / role:manage）
if admin_role_id:
    db_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
        f"SELECT {admin_role_id}, p.id FROM permissions p"
    )
    print(f"  已确保 admin 角色绑定所有现有权限（含 group:manage）")


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

print(f"\n  注册主管理员 {ADMIN_EMAIL}...")
admin_user_id, _ = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, "adm70")
if admin_user_id:
    setup_admin_user(admin_user_id)
    print(f"  管理员注册成功: id={admin_user_id}")
else:
    print(f"  {RED}管理员注册失败，中止{RESET}")
    raise SystemExit(1)

print(f"\n  重新登录管理员...")
admin_token, admin_refresh = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
if not admin_token:
    print(f"  {RED}管理员登录失败，中止{RESET}")
    raise SystemExit(1)
print(f"  管理员登录成功")

print(f"\n  注册 D-67 测试账号 {D67_EMAIL}...")
d67_user_id, d67_token = register_user_via_api(D67_EMAIL, D67_PHONE, D67_PASSWORD, "d67u70")
if d67_user_id:
    d67_token, _ = login_email(D67_EMAIL, D67_PASSWORD)
    print(f"  D-67 用户注册成功: id={d67_user_id}")
else:
    print(f"  {RED}D-67 用户注册失败{RESET}")
    d67_user_id, d67_token = None, None

print(f"\n  所有测试账号准备完毕")


# 记录本次创建的分组 ID，便于结束清理
created_group_ids = []


# ════════════════════════════════════════════════════════════════════════════════
# D-73 / D-76：CreateGroup 的 type 枚举校验 + name 长度上限校验
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-73 / D-76  CreateGroup type 枚举校验 + name 长度上限校验{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    print(f"\n  D-73.1 POST /api/admin/user-groups type='invalid_type'（应返回 400）...")
    s1, r1 = http("POST", "/api/admin/user-groups",
                  {"code": f"d73grp{TS}", "name": f"D73Group{TS}", "type": "invalid_type"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}, message={r1.get('message', '')!r}")

    if s1 == 400:
        ok("D-73.a  CreateGroup type='invalid_type' → HTTP 400（D-73 修复生效）",
           f"code={r1.get('code')}, message={r1.get('message', '')!r}")
    elif s1 in (200, 201):
        gid = r1.get("data", {}).get("id")
        if gid:
            created_group_ids.append(int(gid))
        fail("D-73.a  CreateGroup type='invalid_type' 仍创建成功（D-73 修复未生效）",
             f"HTTP={s1}, group_id={gid}")
    else:
        fail("D-73.a  CreateGroup type='invalid_type' 返回非预期状态",
             f"HTTP={s1}, code={r1.get('code')}, message={r1.get('message', '')!r}")

    print(f"\n  D-76.1 POST /api/admin/user-groups name=300字符超长字符串（应返回 400）...")
    long_name = "N" * 300
    s2, r2 = http("POST", "/api/admin/user-groups",
                  {"code": f"d76grp{TS}", "name": long_name, "type": "custom"},
                  token=admin_token)
    print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')[:80]!r}")

    if s2 == 400:
        ok("D-76.a  CreateGroup name=300字符 → HTTP 400（D-76 修复生效，非 DB 500/截断）",
           f"code={r2.get('code')}, message={r2.get('message', '')!r}")
    elif s2 in (200, 201):
        gid = r2.get("data", {}).get("id")
        if gid:
            created_group_ids.append(int(gid))
        fail("D-76.a  CreateGroup name=300字符 仍创建成功（D-76 修复未生效）",
             f"HTTP={s2}, group_id={gid}")
    elif s2 == 500:
        fail("D-76.a  CreateGroup name=300字符 → HTTP 500（DB 截断/报错，D-76 修复未生效）",
             f"code={r2.get('code')}, message={r2.get('message', '')!r}")
    else:
        fail("D-76.a  CreateGroup name=300字符 返回非预期状态",
             f"HTTP={s2}, code={r2.get('code')}, message={r2.get('message', '')!r}")
else:
    skip("D-73/D-76", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# 前置：创建本批次主测试分组（供后续 D-67/D-71/D-72/D-75/D-68/D-69/D-73(update) 使用）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}前置：创建本批次主测试分组{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

main_group_id = None
if admin_token:
    s, r = http("POST", "/api/admin/user-groups",
               {"code": f"pr70grp{TS}", "name": f"PR70TestGroup{TS}", "type": "custom"},
               token=admin_token)
    print(f"  创建分组 HTTP {s}: code={r.get('code')}")
    if s in (200, 201) and r.get("code") == 0:
        main_group_id = r.get("data", {}).get("id")
        if main_group_id:
            main_group_id = int(main_group_id)
            created_group_ids.append(main_group_id)
        print(f"  主测试分组 id={main_group_id}")
    else:
        print(f"  {RED}创建主测试分组失败: {json.dumps(r, ensure_ascii=False)[:200]}{RESET}")
else:
    print(f"  {RED}admin_token 不可用，跳过{RESET}")


# ════════════════════════════════════════════════════════════════════════════════
# D-73（UpdateGroup）：type 枚举校验
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-73(b)  UpdateGroup type='invalid_type' → 400{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and main_group_id:
    s, r = http("PUT", f"/api/admin/user-groups/{main_group_id}",
               {"type": "invalid_type"},
               token=admin_token)
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    if s == 400:
        ok("D-73.b  UpdateGroup type='invalid_type' → HTTP 400（D-73 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}")
    elif s == 200:
        # 检查 DB 是否真的写入了非法 type
        rows, _ = db_query(f"SELECT type FROM user_groups WHERE id={main_group_id}")
        cur_type = rows[0][0] if rows else None
        fail("D-73.b  UpdateGroup type='invalid_type' 仍返回 200（D-73 修复未生效）",
             f"HTTP=200, DB.type={cur_type!r}")
    else:
        fail("D-73.b  UpdateGroup type='invalid_type' 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-73.b", "admin_token 或 main_group_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-74：GetGroup 不存在 ID → 404（非泛化 500）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-74  GetGroup 不存在 ID → 404（非泛化 500）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    nonexistent_group_id = 9999999
    s, r = http("GET", f"/api/admin/user-groups/{nonexistent_group_id}", token=admin_token)
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    if s == 404:
        ok("D-74.a  GetGroup 不存在ID → HTTP 404（D-74 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}")
    elif s == 500:
        fail("D-74.a  GetGroup 不存在ID → HTTP 500（泛化错误，D-74 修复未生效）",
             f"code={r.get('code')}, message={r.get('message', '')!r}")
    else:
        fail("D-74.a  GetGroup 不存在ID 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-74", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-71：AddMember 不存在的 groupID → 404
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-71  AddMember 不存在的 groupID → 404（ExistsGroupByID 校验生效）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and d67_user_id:
    nonexistent_group_id = 999999
    s, r = http("POST", f"/api/admin/user-groups/{nonexistent_group_id}/members",
               {"user_id": d67_user_id, "group_role": "member"},
               token=admin_token)
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    # 检查是否产生了孤立成员记录
    rows_ghost, _ = db_query(
        f"SELECT COUNT(*) FROM user_group_members "
        f"WHERE group_id={nonexistent_group_id} AND user_id={d67_user_id}"
    )
    ghost_count = int(rows_ghost[0][0]) if rows_ghost else 0

    if s == 404:
        ok("D-71.a  AddMember 不存在groupID → HTTP 404（D-71 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}, 孤立记录数={ghost_count}")
        if ghost_count > 0:
            fail("D-71.b  虽返回404但仍写入了孤立成员记录",
                 f"孤立记录数={ghost_count}")
            db_exec(f"DELETE FROM user_group_members WHERE group_id={nonexistent_group_id} AND user_id={d67_user_id}")
    elif s in (200, 201):
        fail("D-71.a  AddMember 不存在groupID 仍静默成功（D-71 修复未生效）",
             f"HTTP={s}, 孤立记录数={ghost_count}")
        if ghost_count > 0:
            db_exec(f"DELETE FROM user_group_members WHERE group_id={nonexistent_group_id} AND user_id={d67_user_id}")
    else:
        fail("D-71.a  AddMember 不存在groupID 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}, 孤立记录数={ghost_count}")
else:
    skip("D-71", "admin_token 或 d67_user_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-68：CreateInviteCode default_group_role 枚举校验
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-68  CreateInviteCode default_group_role='superadmin' → 400{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and main_group_id:
    s, r = http("POST", f"/api/admin/user-groups/{main_group_id}/invite-codes",
               {"default_group_role": "superadmin"},
               token=admin_token)
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    if s == 400:
        ok("D-68.a  CreateInviteCode default_group_role='superadmin' → HTTP 400（D-68 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}")
    elif s in (200, 201):
        # 检查是否真的写入了非法角色
        code_val = r.get("data", {}).get("code")
        if code_val:
            db_exec(f"DELETE FROM group_invite_codes WHERE code='{code_val}'")
        fail("D-68.a  CreateInviteCode default_group_role='superadmin' 仍创建成功（D-68 修复未生效）",
             f"HTTP={s}, data={json.dumps(r.get('data', {}), ensure_ascii=False)}")
    else:
        fail("D-68.a  CreateInviteCode default_group_role='superadmin' 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-68", "admin_token 或 main_group_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-69：CreateInviteCode max_uses < 0 → 400
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-69  CreateInviteCode max_uses=-1 → 400{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and main_group_id:
    s, r = http("POST", f"/api/admin/user-groups/{main_group_id}/invite-codes",
               {"max_uses": -1},
               token=admin_token)
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    if s == 400:
        ok("D-69.a  CreateInviteCode max_uses=-1 → HTTP 400（D-69 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}")
    elif s in (200, 201):
        code_val = r.get("data", {}).get("code")
        if code_val:
            db_exec(f"DELETE FROM group_invite_codes WHERE code='{code_val}'")
        fail("D-69.a  CreateInviteCode max_uses=-1 仍创建成功（D-69 修复未生效）",
             f"HTTP={s}, data={json.dumps(r.get('data', {}), ensure_ascii=False)}")
    else:
        fail("D-69.a  CreateInviteCode max_uses=-1 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-69", "admin_token 或 main_group_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-72：CreateInviteCode expires_at 为过去时间 → 400
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-72  CreateInviteCode expires_at 为昨天 → 400{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and main_group_id:
    yesterday = datetime.datetime.utcnow() - datetime.timedelta(days=1)
    yesterday_iso = yesterday.strftime("%Y-%m-%dT%H:%M:%S+00:00")
    s, r = http("POST", f"/api/admin/user-groups/{main_group_id}/invite-codes",
               {"expires_at": yesterday_iso},
               token=admin_token)
    print(f"    expires_at={yesterday_iso}")
    print(f"    HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")

    if s == 400:
        ok("D-72.a  CreateInviteCode expires_at=昨天 → HTTP 400（D-72 修复生效）",
           f"code={r.get('code')}, message={r.get('message', '')!r}")
    elif s in (200, 201):
        code_val = r.get("data", {}).get("code")
        if code_val:
            db_exec(f"DELETE FROM group_invite_codes WHERE code='{code_val}'")
        fail("D-72.a  CreateInviteCode expires_at=昨天 仍创建成功（D-72 修复未生效）",
             f"HTTP={s}, data={json.dumps(r.get('data', {}), ensure_ascii=False)}")
    else:
        fail("D-72.a  CreateInviteCode expires_at=昨天 返回非预期状态",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-72", "admin_token 或 main_group_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-70：GenerateCode 改为 crypto/rand（无法通过接口验证随机性来源，标记 skip）
# 但验证邀请码生成功能本身正常（code 为空时自动生成）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-70  GenerateCode 改为 crypto/rand（代码审查确认，验证生成功能可用）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

d70_invite_code_id = None
d70_invite_code_str = None
if admin_token and main_group_id:
    s, r = http("POST", f"/api/admin/user-groups/{main_group_id}/invite-codes",
               {"default_group_role": "member"},
               token=admin_token)
    print(f"    创建邀请码（code为空，自动生成）HTTP {s}: code={r.get('code')}")
    if s in (200, 201) and r.get("code") == 0:
        data = r.get("data", {})
        d70_invite_code_id = data.get("id")
        d70_invite_code_str = data.get("code")
        if d70_invite_code_id:
            d70_invite_code_id = int(d70_invite_code_id)
        print(f"    生成的邀请码: id={d70_invite_code_id}, code={d70_invite_code_str!r}")
        if d70_invite_code_str and len(d70_invite_code_str) == 8:
            ok("D-70.a  自动生成邀请码功能正常（8位字符）",
               f"code={d70_invite_code_str!r}")
        else:
            fail("D-70.a  自动生成邀请码格式异常",
                 f"code={d70_invite_code_str!r}")
        skip("D-70.b  GenerateCode 随机性来源（crypto/rand vs math/rand）",
             "不可通过接口验证，已通过代码审查确认（GroupRepository.GenerateCode 使用 crypto/rand.Int）")
    else:
        fail("D-70  创建邀请码失败",
             f"HTTP={s}, code={r.get('code')}, message={r.get('message', '')!r}")
else:
    skip("D-70", "admin_token 或 main_group_id 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D-67：IncrUsedCountAtomic 增加 AND status='active' 条件
#   创建邀请码 → 禁用 → 用其加入分组 → 应失败，不应递增 used_count / 不应加入成员
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-67  IncrUsedCountAtomic 增加 status='active' 条件{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token and main_group_id and d67_token and d67_user_id:
    print(f"\n  D-67.1 创建一个新邀请码（用于本测试，避免与 D-70 邀请码混用）...")
    s1, r1 = http("POST", f"/api/admin/user-groups/{main_group_id}/invite-codes",
                  {"default_group_role": "member"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    d67_invite_id = None
    d67_invite_code_str = None
    if s1 in (200, 201) and r1.get("code") == 0:
        data = r1.get("data", {})
        d67_invite_id = data.get("id")
        d67_invite_code_str = data.get("code")
        if d67_invite_id:
            d67_invite_id = int(d67_invite_id)
        print(f"    邀请码创建成功: id={d67_invite_id}, code={d67_invite_code_str!r}")
    else:
        print(f"    {RED}创建邀请码失败: {json.dumps(r1, ensure_ascii=False)[:200]}{RESET}")

    if d67_invite_id and d67_invite_code_str:
        print(f"\n  D-67.2 禁用该邀请码...")
        s2, r2 = http("PATCH",
                      f"/api/admin/user-groups/{main_group_id}/invite-codes/{d67_invite_id}/disable",
                      token=admin_token)
        print(f"    HTTP {s2}: code={r2.get('code')}, message={r2.get('message', '')!r}")

        if s2 == 200 and r2.get("code") == 0:
            # 确认 DB 中 status 为 disabled
            rows, _ = db_query(f"SELECT status, used_count FROM group_invite_codes WHERE id={d67_invite_id}")
            db_status = rows[0][0] if rows else None
            db_used_before = int(rows[0][1]) if rows else None
            print(f"    DB: status={db_status!r}, used_count={db_used_before}")

            print(f"\n  D-67.3 用 D-67 测试用户尝试用该（已禁用）邀请码加入分组...")
            s3, r3 = http("POST", "/api/user-groups/join",
                          {"invite_code": d67_invite_code_str},
                          token=d67_token)
            print(f"    HTTP {s3}: code={r3.get('code')}, message={r3.get('message', '')!r}")

            # 检查 DB used_count 是否被递增 / 是否被成功加入成员
            rows_after, _ = db_query(f"SELECT used_count FROM group_invite_codes WHERE id={d67_invite_id}")
            db_used_after = int(rows_after[0][0]) if rows_after else None

            rows_member, _ = db_query(
                f"SELECT COUNT(*) FROM user_group_members "
                f"WHERE group_id={main_group_id} AND user_id={d67_user_id}"
            )
            member_count = int(rows_member[0][0]) if rows_member else 0

            print(f"    DB after: used_count={db_used_after}, member_count={member_count}")

            join_succeeded = (s3 == 200 and r3.get("code") == 0)
            used_count_unchanged = (db_used_after == db_used_before)

            if not join_succeeded and used_count_unchanged and member_count == 0:
                ok("D-67.a  使用已禁用邀请码加入分组 → 失败，used_count 未递增，未写入成员（D-67 修复生效）",
                   f"HTTP={s3}, code={r3.get('code')}, used_count {db_used_before}→{db_used_after}, member_count={member_count}")
            elif join_succeeded:
                fail("D-67.a  使用已禁用邀请码仍成功加入分组（D-67 修复未生效）",
                     f"HTTP={s3}, code={r3.get('code')}, member_count={member_count}")
                # 清理写入的成员
                if member_count > 0:
                    db_exec(f"DELETE FROM user_group_members WHERE group_id={main_group_id} AND user_id={d67_user_id}")
            elif not used_count_unchanged:
                fail("D-67.a  使用已禁用邀请码 join 失败，但 used_count 仍被递增（原子条件未生效）",
                     f"used_count {db_used_before}→{db_used_after}")
            else:
                fail("D-67.a  使用已禁用邀请码 join 行为非预期",
                     f"HTTP={s3}, code={r3.get('code')}, used_count {db_used_before}→{db_used_after}, member_count={member_count}")
        else:
            fail("D-67  禁用邀请码失败，无法继续测试",
                 f"HTTP={s2}, code={r2.get('code')}")
    else:
        skip("D-67", "邀请码创建失败")
else:
    skip("D-67", "前置条件不满足（admin_token/main_group_id/d67_token/d67_user_id）")


# ════════════════════════════════════════════════════════════════════════════════
# D-75：DeleteGroup 事务内清理所有邀请码记录（含 disabled 状态）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}D-75  DeleteGroup 清理所有邀请码记录（含 disabled 状态）{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

if admin_token:
    print(f"\n  D-75.1 创建专用分组（用于删除测试）...")
    s1, r1 = http("POST", "/api/admin/user-groups",
                  {"code": f"d75grp{TS}", "name": f"D75DeleteGroup{TS}", "type": "custom"},
                  token=admin_token)
    print(f"    HTTP {s1}: code={r1.get('code')}")
    d75_group_id = None
    if s1 in (200, 201) and r1.get("code") == 0:
        d75_group_id = r1.get("data", {}).get("id")
        if d75_group_id:
            d75_group_id = int(d75_group_id)
        print(f"    分组 id={d75_group_id}")
    else:
        print(f"    {RED}创建分组失败: {json.dumps(r1, ensure_ascii=False)[:200]}{RESET}")

    if d75_group_id:
        print(f"\n  D-75.2 创建一个邀请码...")
        s2, r2 = http("POST", f"/api/admin/user-groups/{d75_group_id}/invite-codes",
                      {"default_group_role": "member"},
                      token=admin_token)
        print(f"    HTTP {s2}: code={r2.get('code')}")
        d75_invite_id = None
        if s2 in (200, 201) and r2.get("code") == 0:
            d75_invite_id = r2.get("data", {}).get("id")
            if d75_invite_id:
                d75_invite_id = int(d75_invite_id)
            print(f"    邀请码 id={d75_invite_id}, code={r2.get('data', {}).get('code')!r}")
        else:
            print(f"    {RED}创建邀请码失败: {json.dumps(r2, ensure_ascii=False)[:200]}{RESET}")

        if d75_invite_id:
            print(f"\n  D-75.3 禁用该邀请码...")
            s3, r3 = http("PATCH",
                          f"/api/admin/user-groups/{d75_group_id}/invite-codes/{d75_invite_id}/disable",
                          token=admin_token)
            print(f"    HTTP {s3}: code={r3.get('code')}")

            if s3 == 200 and r3.get("code") == 0:
                print(f"\n  D-75.4 删除分组...")
                s4, r4 = http("DELETE", f"/api/admin/user-groups/{d75_group_id}", token=admin_token)
                print(f"    HTTP {s4}: code={r4.get('code')}, message={r4.get('message', '')!r}")

                if s4 == 200 and r4.get("code") == 0:
                    print(f"\n  D-75.5 查询 group_invite_codes 表确认该分组无残留邀请码记录...")
                    rows, _ = db_query(
                        f"SELECT COUNT(*) FROM group_invite_codes WHERE group_id={d75_group_id}"
                    )
                    remaining = int(rows[0][0]) if rows else -1
                    print(f"    残留邀请码记录数={remaining}")

                    if remaining == 0:
                        ok("D-75.a  DeleteGroup 后 group_invite_codes 表中无残留记录（D-75 修复生效）",
                           f"残留记录数={remaining}")
                    else:
                        fail("D-75.a  DeleteGroup 后仍有残留邀请码记录（D-75 修复未生效）",
                             f"残留记录数={remaining}")
                        # 清理残留
                        db_exec(f"DELETE FROM group_invite_codes WHERE group_id={d75_group_id}")

                    # 同时确认分组主记录已删除
                    rows_g, _ = db_query(f"SELECT COUNT(*) FROM user_groups WHERE id={d75_group_id}")
                    g_remaining = int(rows_g[0][0]) if rows_g else -1
                    if g_remaining == 0:
                        ok("D-75.b  DeleteGroup 后分组主记录已删除")
                    else:
                        fail("D-75.b  DeleteGroup 后分组主记录仍存在",
                             f"残留记录数={g_remaining}")
                else:
                    fail("D-75  删除分组失败，无法继续验证",
                         f"HTTP={s4}, code={r4.get('code')}, message={r4.get('message', '')!r}")
                    # 兜底清理
                    db_exec(f"DELETE FROM group_invite_codes WHERE group_id={d75_group_id}")
                    db_exec(f"DELETE FROM user_groups WHERE id={d75_group_id}")
            else:
                fail("D-75  禁用邀请码失败，无法继续测试",
                     f"HTTP={s3}, code={r3.get('code')}")
                db_exec(f"DELETE FROM group_invite_codes WHERE group_id={d75_group_id}")
                db_exec(f"DELETE FROM user_groups WHERE id={d75_group_id}")
        else:
            skip("D-75", "邀请码创建失败")
            db_exec(f"DELETE FROM user_groups WHERE id={d75_group_id}")
    else:
        skip("D-75", "专用分组创建失败")
else:
    skip("D-75", "admin_token 不可用")


# ════════════════════════════════════════════════════════════════════════════════
# 测试结束：清理本次测试创建的临时数据
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'─'*70}{RESET}")
print(f"{BOLD}清理本次测试创建的临时数据{RESET}")
print(f"{BOLD}{'─'*70}{RESET}")

# 清理 main_group_id（D-70/D-67 创建的邀请码会随分组删除一起清理，D-75 验证了该行为）
if admin_token and main_group_id:
    # 先移除可能残留的成员（如果 D-67 测试失败导致写入了成员）
    if d67_user_id:
        db_exec(f"DELETE FROM user_group_members WHERE group_id={main_group_id} AND user_id={d67_user_id}")
    s, r = http("DELETE", f"/api/admin/user-groups/{main_group_id}", token=admin_token)
    print(f"  删除主测试分组 id={main_group_id}  HTTP {s}: code={r.get('code')}, message={r.get('message', '')!r}")
    if not (s == 200 and r.get("code") == 0):
        # 兜底直接 DB 删除
        db_exec(f"DELETE FROM group_invite_codes WHERE group_id={main_group_id}")
        db_exec(f"DELETE FROM group_permissions WHERE group_id={main_group_id}")
        db_exec(f"DELETE FROM user_groups WHERE id={main_group_id}")
        print(f"  {YELLOW}已通过 DB 兜底清理主测试分组{RESET}")

# 清理用户、角色绑定
if d67_user_id:
    db_exec(f"DELETE FROM user_roles WHERE user_id={d67_user_id}")
    db_exec(f"DELETE FROM user_sessions WHERE user_id={d67_user_id}")
    db_exec(f"DELETE FROM users WHERE id={d67_user_id}")
    print(f"  已清理 D-67 测试用户 id={d67_user_id}")

if admin_user_id:
    db_exec(f"DELETE FROM user_roles WHERE user_id={admin_user_id}")
    db_exec(f"DELETE FROM user_sessions WHERE user_id={admin_user_id}")
    db_exec(f"DELETE FROM users WHERE id={admin_user_id}")
    print(f"  已清理管理员测试用户 id={admin_user_id}")

# 清理 verification_codes
for email, phone in [(ADMIN_EMAIL, ADMIN_PHONE), (D67_EMAIL, D67_PHONE)]:
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' OR target_value='{phone}'")
print(f"  已清理验证码记录")


# ════════════════════════════════════════════════════════════════════════════════
# 汇总
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*70}{RESET}")
print(f"{BOLD}测试汇总{RESET}")
print(f"{BOLD}{'='*70}{RESET}")

total = passed + failed
print(f"\n  总用例数: {total + sum(1 for r in results if r[0] == 'SKIP')}")
print(f"  {GREEN}PASS: {passed}{RESET}")
print(f"  {RED}FAIL: {failed}{RESET}")
print(f"  {YELLOW}SKIP: {sum(1 for r in results if r[0] == 'SKIP')}{RESET}")

if failed > 0:
    print(f"\n{BOLD}{RED}失败用例列表：{RESET}")
    for status, label, detail in results:
        if status == "FAIL":
            print(f"  {RED}- {label}{RESET}")
            if detail:
                print(f"    {detail}")

print()
if failed == 0:
    print(f"{BOLD}{GREEN}全部测试通过（或仅 SKIP）{RESET}")
else:
    print(f"{BOLD}{RED}存在 {failed} 个失败用例，请检查{RESET}")

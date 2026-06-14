#!/usr/bin/env python3
"""
PR#97 验收测试 — D-95 管理后台列表接口分页响应结构改为扁平结构

背景：
  D-95（P2）修复前，11 个管理后台列表接口的 data 结构为嵌套：
    {"items": [...], "pagination": {"page": 1, "page_size": 20, "total": 100}}
  修复后改为扁平（符合 docs/full-api-design.md 1.2 节规范）：
    {"items": [...], "page": 1, "page_size": 20, "total": 100}
  即 data.pagination 子对象不应再存在，page/page_size/total 应与 items 同级。

测试用例（D95-01~11，均使用 ?page=1&page_size=2）：
  D95-01  GET /api/admin/users
  D95-02  GET /api/admin/users/{id}/login-logs
  D95-03  GET /api/admin/roles
  D95-04  GET /api/admin/permissions
  D95-05  GET /api/admin/users/{id}/roles
  D95-06  GET /api/admin/users/{id}/permission-overrides
  D95-07  GET /api/admin/audit-logs
  D95-08  GET /api/admin/user-groups
  D95-09  GET /api/admin/user-groups/{id}/members
  D95-10  GET /api/admin/user-groups/{id}/invite-codes
  D95-11  GET /api/admin/identity-verifications

每项断言：
  - data 中存在 items / page / page_size / total 四个 key，且均在顶层（与 items 同级）
  - data 中不存在 pagination key
  - page==1、page_size==2（回显请求参数），total 为非负整数
  - items 为数组，长度 <= page_size

前置准备：
  - 注册一个测试账号，直接写 user_roles/role_permissions 赋予一个拥有
    user:list / role:manage / audit:read / group:manage / identity:review / user:manage
    全部权限码的角色（user:manage 用于完成 D-96 管理员双重认证发码/验证）
  - 走管理员双重认证流程（POST /api/admin/auth/verification-codes/phone|email
    -> verify-phone/verify-email，D-96 已验收通过）
  - 创建一个测试分组（含成员、邀请码）和一个待审核实名认证记录，
    保证 D95-09/10/11 的 items 非空

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr97_d95_pagination_flat.py
"""

import hashlib
import json
import os
import subprocess
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN, RED, YELLOW, CYAN, BOLD, RESET = "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m"

passed = failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}[PASS]{RESET} {label}" + (f"\n         {detail}" if detail else ""))
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}[FAIL]{RESET} {label}" + (f"\n         {RED}{detail}{RESET}" if detail else ""))
    results.append(("FAIL", label, detail))


def skip(label, reason=""):
    print(f"  {YELLOW}[SKIP]{RESET} {label}" + (f"\n         {YELLOW}{reason}{RESET}" if reason else ""))
    results.append(("SKIP", label, reason))


# ── HTTP 工具 ──────────────────────────────────────────────────────────────────

def http(method, path, body=None, token=None, extra_headers=None):
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
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    if result.returncode != 0:
        return None, result.stderr.strip()
    rows = []
    for line in result.stdout.strip().split("\n"):
        if line:
            rows.append(line.split("\t"))
    return rows, None


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return result.returncode == 0, (result.stderr.strip() if result.returncode != 0 else None)


# ── 注册 / 登录工具 ─────────────────────────────────────────────────────────────

def register_user_via_api(email, phone, password, username=None):
    """通过 DB 插入 register 验证码后，调用 POST /api/auth/register 注册账号。返回 (user_id, access_token) 或 (None, None)。"""
    otp_code = "888888"
    otp_sha = hashlib.sha256(otp_code.encode()).hexdigest()
    expire_sql = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"

    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('phone', '{phone}', '{otp_sha}', 'register', {expire_sql})")
    db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
            f"VALUES ('email', '{email}', '{otp_sha}', 'register', {expire_sql})")

    body = {"email": email, "phone": phone, "password": password, "phone_code": otp_code, "email_code": otp_code}
    if username:
        body["username"] = username

    status, resp = http("POST", "/api/auth/register", body)
    if status in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        user_id = int(rows[0][0]) if rows else None
        return user_id, token
    print(f"    {RED}注册接口返回非预期: HTTP={status}, {json.dumps(resp, ensure_ascii=False)[:200]}{RESET}")
    return None, None


def login_email(email, password):
    s, r = http("POST", "/api/auth/login/email", {"email": email, "password": password})
    if s == 200 and r.get("code") == 0:
        data = r.get("data", {})
        return data.get("access_token"), data.get("refresh_token")
    return None, None


# ── 通用断言：D-95 扁平分页结构 ─────────────────────────────────────────────────

def assert_flat_pagination(case_id, path, token, expected_page=1, expected_page_size=2):
    """对单个分页接口发起 GET 请求，断言响应符合 D-95 扁平分页结构。"""
    sep = "&" if "?" in path else "?"
    full_path = f"{path}{sep}page={expected_page}&page_size={expected_page_size}"
    s, r = http("GET", full_path, token=token)
    print(f"  {CYAN}{case_id}{RESET}  GET {full_path}")
    print(f"    HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")

    if s != 200 or r.get("code") != 0:
        fail(f"{case_id}  请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
        return

    data = r.get("data", {})
    if not isinstance(data, dict):
        fail(f"{case_id}  data 不是对象", f"data={data!r}")
        return

    # 1) items/page/page_size/total 四个 key 均在顶层
    required_keys = {"items", "page", "page_size", "total"}
    missing = required_keys - set(data.keys())
    if not missing:
        ok(f"{case_id}  data 顶层包含 items/page/page_size/total", f"keys={sorted(data.keys())}")
    else:
        fail(f"{case_id}  data 顶层缺少字段", f"缺少={missing}, 实际keys={sorted(data.keys())}")

    # 2) 不存在 pagination 子对象
    if "pagination" not in data:
        ok(f"{case_id}  data 中不存在 pagination 子对象（扁平结构）")
    else:
        fail(f"{case_id}  data 中仍存在 pagination 子对象", f"pagination={data.get('pagination')}")

    # 3) items 为数组，长度 <= page_size
    items = data.get("items")
    page_size = data.get("page_size")
    if isinstance(items, list):
        if isinstance(page_size, int) and len(items) <= page_size:
            ok(f"{case_id}  items 为数组且长度 <= page_size", f"len(items)={len(items)}, page_size={page_size}")
        else:
            fail(f"{case_id}  items 长度超过 page_size", f"len(items)={len(items)}, page_size={page_size}")
    else:
        fail(f"{case_id}  items 不是数组", f"items={items!r}")

    # 4) page/page_size 回显请求参数
    if data.get("page") == expected_page and data.get("page_size") == expected_page_size:
        ok(f"{case_id}  page/page_size 正确回显请求参数", f"page={data.get('page')}, page_size={data.get('page_size')}")
    else:
        fail(f"{case_id}  page/page_size 未正确回显",
             f"期望 page={expected_page} page_size={expected_page_size}，实际 page={data.get('page')} page_size={data.get('page_size')}")

    # 5) total 为非负整数
    total = data.get("total")
    if isinstance(total, int) and total >= 0:
        ok(f"{case_id}  total 为非负整数", f"total={total}")
    else:
        fail(f"{case_id}  total 不是非负整数", f"total={total!r}")


# ════════════════════════════════════════════════════════════════════════════════
TS = int(time.time())

ADMIN_EMAIL    = f"d95adm{TS}@testmail.io"
ADMIN_PHONE    = f"196{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@D95Admin"

# 用于 D95-05/06（GET /api/admin/users/{id}/roles, /permission-overrides）的目标用户
TARGET_EMAIL    = f"d95tgt{TS}@testmail.io"
TARGET_PHONE    = f"197{TS % 100000000:08d}"
TARGET_PASSWORD = "Test@D95Target"

# 用于 D95-11（GET /api/admin/identity-verifications）的实名认证提交用户
IDV_EMAIL    = f"d95idv{TS}@testmail.io"
IDV_PHONE    = f"198{TS % 100000000:08d}"
IDV_PASSWORD = "Test@D95Idv"

print(f"{BOLD}{CYAN}PR#97 验收测试 — D-95 管理后台列表接口分页响应结构扁平化{RESET}")
print(f"  API_BASE: {API_BASE}  TS={TS}\n")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：D95-test 角色（拥有 user:list/role:manage/audit:read/group:manage/identity:review）
# ════════════════════════════════════════════════════════════════════════════════
print(f"{BOLD}前置准备：确保 d95_test 角色与全量权限{RESET}")

ROLE_CODE = "d95_test_role"
db_exec(f"INSERT IGNORE INTO roles (code, name, description) VALUES "
        f"('{ROLE_CODE}', 'D95测试角色', 'D-95 分页扁平结构验收专用角色')")
rows, err = db_query(f"SELECT id FROM roles WHERE code='{ROLE_CODE}'")
if not rows or err:
    print(f"  {RED}d95_test_role 角色创建失败，中止: err={err}{RESET}")
    raise SystemExit(1)
test_role_id = int(rows[0][0])
print(f"  d95_test_role 角色 id={test_role_id}")

NEEDED_PERMS = ["user:list", "role:manage", "audit:read", "group:manage", "identity:review", "user:manage"]
for code in NEEDED_PERMS:
    db_exec(
        f"INSERT IGNORE INTO role_permissions (role_id, permission_id) "
        f"SELECT {test_role_id}, p.id FROM permissions p WHERE p.code='{code}'"
    )
rows, err = db_query(
    f"SELECT p.code FROM role_permissions rp JOIN permissions p ON p.id=rp.permission_id "
    f"WHERE rp.role_id={test_role_id}"
)
granted = {row[0] for row in rows} if rows and not err else set()
missing_perms = set(NEEDED_PERMS) - granted
if not missing_perms:
    ok("前置准备  d95_test_role 已拥有全部所需权限码", f"granted={sorted(granted)}")
else:
    fail("前置准备  d95_test_role 缺少权限码", f"missing={missing_perms}")


def grant_test_role(user_id):
    db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {test_role_id})")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：注册测试账号
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：注册测试账号{RESET}")

print(f"\n  注册管理员账号 {ADMIN_EMAIL} ...")
admin_user_id, admin_token = register_user_via_api(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"d95adm{TS}")
if not admin_user_id:
    print(f"  {RED}管理员账号注册失败，中止{RESET}")
    raise SystemExit(1)
grant_test_role(admin_user_id)
print(f"  管理员账号注册成功: id={admin_user_id}，已绑定 d95_test_role 角色")

print(f"\n  注册目标用户账号 {TARGET_EMAIL} ...")
target_user_id, target_token = register_user_via_api(TARGET_EMAIL, TARGET_PHONE, TARGET_PASSWORD, f"d95tgt{TS}")
if not target_user_id:
    print(f"  {RED}目标用户账号注册失败，中止{RESET}")
    raise SystemExit(1)
print(f"  目标用户账号注册成功: id={target_user_id}")

print(f"\n  注册实名认证提交用户 {IDV_EMAIL} ...")
idv_user_id, idv_token = register_user_via_api(IDV_EMAIL, IDV_PHONE, IDV_PASSWORD, f"d95idv{TS}")
if not idv_user_id:
    print(f"  {YELLOW}实名认证提交用户注册失败，D95-11 items 可能为空（仍可校验分页结构）{RESET}")
print(f"  实名认证提交用户注册成功: id={idv_user_id}")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：管理员双重认证（D-96 流程）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：管理员双重认证（POST /api/admin/auth/verification-codes/phone|email -> verify-phone/verify-email）{RESET}")

# 重新登录，确保 token 有效
admin_token, admin_refresh = login_email(ADMIN_EMAIL, ADMIN_PASSWORD)
if not admin_token:
    print(f"  {RED}管理员重新登录失败，中止{RESET}")
    raise SystemExit(1)

s, r = http("POST", "/api/admin/auth/verification-codes/phone", token=admin_token)
phone_code = r.get("data", {}).get("code") if s == 200 else None
if phone_code:
    s, r = http("POST", "/api/admin/auth/verify-phone", {"code": phone_code}, token=admin_token)
    if s == 200 and r.get("code") == 0:
        ok("前置准备  管理员手机号双重认证完成")
    else:
        fail("前置准备  管理员手机号双重认证失败", f"HTTP={s} resp={r}")
else:
    fail("前置准备  获取管理员手机验证码失败", f"HTTP={s} resp={r}")

s, r = http("POST", "/api/admin/auth/verification-codes/email", token=admin_token)
email_code = r.get("data", {}).get("code") if s == 200 else None
if email_code:
    s, r = http("POST", "/api/admin/auth/verify-email", {"code": email_code}, token=admin_token)
    if s == 200 and r.get("code") == 0:
        ok("前置准备  管理员邮箱双重认证完成")
    else:
        fail("前置准备  管理员邮箱双重认证失败", f"HTTP={s} resp={r}")
else:
    fail("前置准备  获取管理员邮箱验证码失败", f"HTTP={s} resp={r}")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：给目标用户分配一个角色 + 一条权限覆盖（保证 D95-05/06 items 非空）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：给目标用户分配角色 + 权限覆盖{RESET}")

# 5.1 分配 d95_test_role 给目标用户（用于 D95-05 GetUserRoles 列表非空）
s, r = http("POST", f"/api/admin/users/{target_user_id}/roles", {"role_id": test_role_id}, token=admin_token)
if s in (200, 201) and r.get("code") == 0:
    ok("前置准备  为目标用户分配角色成功（D95-05 items 非空）")
elif s == 409:
    ok("前置准备  目标用户已拥有该角色（409，幂等可接受）")
else:
    fail("前置准备  为目标用户分配角色失败", f"HTTP={s} resp={r}")

# 5.2 设置一条权限覆盖（用于 D95-06 GetPermissionOverrides 列表非空）
rows, err = db_query("SELECT id FROM permissions WHERE code='user:list'")
user_list_perm_id = int(rows[0][0]) if rows and not err else None
if user_list_perm_id:
    s, r = http("POST", f"/api/admin/users/{target_user_id}/permission-overrides",
                 {"permission_id": user_list_perm_id, "effect": "allow", "reason": "D95验收"},
                 token=admin_token)
    if s in (200, 201) and r.get("code") == 0:
        ok("前置准备  为目标用户设置权限覆盖成功（D95-06 items 非空）")
    else:
        fail("前置准备  为目标用户设置权限覆盖失败", f"HTTP={s} resp={r}")
else:
    skip("前置准备  设置权限覆盖", "未找到 user:list 权限 id")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：创建测试分组（含成员 + 邀请码，用于 D95-09/10 items 非空）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：创建测试分组 + 成员 + 邀请码{RESET}")

GROUP_CODE = f"d95grp{TS}"
test_group_id = None
s, r = http("POST", "/api/admin/user-groups",
             {"code": GROUP_CODE, "name": "D95验收分组", "type": "custom"},
             token=admin_token)
if s in (200, 201) and r.get("code") == 0:
    test_group_id = r.get("data", {}).get("id")
    ok("前置准备  创建测试分组成功", f"group_id={test_group_id}")
else:
    fail("前置准备  创建测试分组失败", f"HTTP={s} resp={r}")

if test_group_id:
    # 添加目标用户为分组成员（D95-09 items 非空）
    s, r = http("POST", f"/api/admin/user-groups/{test_group_id}/members",
                 {"user_id": target_user_id, "group_role": "member"}, token=admin_token)
    if s in (200, 201) and r.get("code") == 0:
        ok("前置准备  添加分组成员成功（D95-09 items 非空）")
    else:
        fail("前置准备  添加分组成员失败", f"HTTP={s} resp={r}")

    # 创建邀请码（D95-10 items 非空）
    s, r = http("POST", f"/api/admin/user-groups/{test_group_id}/invite-codes",
                 {"default_group_role": "member", "max_uses": 0}, token=admin_token)
    if s in (200, 201) and r.get("code") == 0:
        ok("前置准备  创建邀请码成功（D95-10 items 非空）")
    else:
        fail("前置准备  创建邀请码失败", f"HTTP={s} resp={r}")
else:
    skip("前置准备  分组成员/邀请码", "测试分组创建失败")


# ════════════════════════════════════════════════════════════════════════════════
# 前置准备：提交一条实名认证记录（用于 D95-11 items 非空）
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置准备：提交一条实名认证记录{RESET}")

if idv_token:
    idv_id_card = f"1101011990{TS % 100000000:08d}"
    s, r = http("POST", "/api/identity/verifications",
                 {"real_name": "D95验收用户", "id_card_no": idv_id_card, "verification_type": "id_card"},
                 token=idv_token)
    if s in (200, 201) and r.get("code") == 0:
        ok("前置准备  提交实名认证成功（D95-11 items 非空）", f"id={r.get('data',{}).get('id')}")
    else:
        fail("前置准备  提交实名认证失败", f"HTTP={s} resp={r}")
else:
    skip("前置准备  提交实名认证", "实名认证提交用户不可用")


# ════════════════════════════════════════════════════════════════════════════════
# D95-01 ~ D95-11
# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*70}{RESET}")
print(f"{BOLD}D95-01~11  11 个管理后台列表接口分页结构扁平化验收（?page=1&page_size=2）{RESET}")
print(f"{BOLD}{'='*70}{RESET}\n")

assert_flat_pagination("D95-01", "/api/admin/users", admin_token)
assert_flat_pagination("D95-02", f"/api/admin/users/{target_user_id}/login-logs", admin_token)
assert_flat_pagination("D95-03", "/api/admin/roles", admin_token)
assert_flat_pagination("D95-04", "/api/admin/permissions", admin_token)
assert_flat_pagination("D95-05", f"/api/admin/users/{target_user_id}/roles", admin_token)
assert_flat_pagination("D95-06", f"/api/admin/users/{target_user_id}/permission-overrides", admin_token)
assert_flat_pagination("D95-07", "/api/admin/audit-logs", admin_token)
assert_flat_pagination("D95-08", "/api/admin/user-groups", admin_token)

if test_group_id:
    assert_flat_pagination("D95-09", f"/api/admin/user-groups/{test_group_id}/members", admin_token)
    assert_flat_pagination("D95-10", f"/api/admin/user-groups/{test_group_id}/invite-codes", admin_token)
else:
    skip("D95-09  GET /api/admin/user-groups/{id}/members", "测试分组创建失败")
    skip("D95-10  GET /api/admin/user-groups/{id}/invite-codes", "测试分组创建失败")

assert_flat_pagination("D95-11", "/api/admin/identity-verifications", admin_token)


# ════════════════════════════════════════════════════════════════════════════════
print(f"\n{BOLD}{'='*70}{RESET}")
total = passed + failed
print(f"{BOLD}总计：{passed}/{total} PASS（{failed} FAIL）{RESET}")
if failed == 0:
    print(f"{GREEN}所有用例通过，D-95（PR#97）分页扁平结构验收通过。{RESET}")
else:
    print(f"{RED}{failed} 个用例失败，请检查上方详情。{RESET}")
    raise SystemExit(1)

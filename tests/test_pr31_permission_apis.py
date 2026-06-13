#!/usr/bin/env python3
"""
PR#31（feature/backend-a-iam-permission-query-apis）权限查询接口验收脚本

验收范围：
  A-10  GET /api/me/permissions
  A-11  GET /api/admin/roles/{id}/permissions
  A-12  GET /api/admin/users/{id}/effective-permissions

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 ~/molin/test_pr31_permission_apis.py
"""

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

GREEN  = "\033[92m"
RED    = "\033[91m"
YELLOW = "\033[93m"
CYAN   = "\033[96m"
BOLD   = "\033[1m"
RESET  = "\033[0m"

passed = 0
failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    msg = f"  {GREEN}PASS{RESET} {label}"
    if detail:
        msg += f"\n       {YELLOW}{detail}{RESET}"
    print(msg)
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}FAIL{RESET} {label}"
    if detail:
        msg += f"\n       {RED}{detail}{RESET}"
    print(msg)
    results.append(("FAIL", label, detail))


# ── HTTP 工具 ────────────────────────────────────────────────

def http(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL 查询工具 ────────────────────────────────────────────

def db_query(sql):
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


# ── 测试数据 ────────────────────────────────────────────────

TS = str(int(time.time()))[-8:]

ADMIN_EMAIL = "aisiqin@163.com"
ADMIN_PASSWORD = "123456"

NORMAL_EMAIL = f"pr31norm{TS}@testmail.io"
NORMAL_PHONE = f"139{TS}"
NORMAL_PASSWORD = "Test@Pr31x"

# A-12.2 overrides 叠加测试目标用户：选用一个已有角色权限（非admin）的现有账号，
# 以便能从其基线 permissions 中选取一个权限码做 deny 测试。
# qa_ban_admin_1780884229@molin.io（user_id=54）的角色 qa_ban_admin_role_1780884229
# 绑定了 user:manage 权限，且当前无任何 user_permission_overrides 记录（已确认）。
OVERRIDE_TARGET_USER_ID = 54

print(f"\n{BOLD}{CYAN}PR#31 权限查询接口验收 — 开始{RESET}")
print(f"  API_BASE  : {API_BASE}")
print(f"  MYSQL     : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print()


# ═══════════════════════════════════════════════════════════════
# 准备：管理员登录
# ═══════════════════════════════════════════════════════════════
print(f"{BOLD}准备：管理员登录{RESET}")
status, resp = http("POST", "/api/auth/login/email", {
    "email": ADMIN_EMAIL, "password": ADMIN_PASSWORD
})
print(f"       响应 HTTP {status}: code={resp.get('code')}")
if status != 200 or resp.get("code") != 0:
    fail("管理员登录失败，后续测试无法进行", json.dumps(resp, ensure_ascii=False))
    admin_token = None
    admin_user_id = None
else:
    admin_token = resp["data"]["access_token"]
    ok("管理员登录成功")
    # 查 admin 的 user_id
    rows, err = db_query(f"SELECT id FROM users WHERE email='{ADMIN_EMAIL}'")
    admin_user_id = int(rows[0][0]) if rows else None
    print(f"       admin_user_id={admin_user_id}")


# ═══════════════════════════════════════════════════════════════
# 准备：注册普通用户（双 OTP）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}准备：注册普通测试用户{RESET}")

status_e, resp_e = http("POST", "/api/auth/verification-codes/email", {
    "email": NORMAL_EMAIL, "scene": "register"
})
status_p, resp_p = http("POST", "/api/auth/verification-codes/phone", {
    "phone": NORMAL_PHONE, "scene": "register"
})
email_code = resp_e.get("data", {}).get("code") if status_e == 200 else None
phone_code = resp_p.get("data", {}).get("code") if status_p == 200 else None

normal_token = None
normal_user_id = None
if email_code and phone_code:
    status_r, resp_r = http("POST", "/api/auth/register", {
        "phone": NORMAL_PHONE, "email": NORMAL_EMAIL,
        "password": NORMAL_PASSWORD,
        "phone_code": phone_code, "email_code": email_code,
    })
    if status_r == 201 and resp_r.get("code") == 0:
        normal_token = resp_r["data"]["access_token"]
        ok("普通用户注册成功")
        rows, err = db_query(f"SELECT id FROM users WHERE email='{NORMAL_EMAIL}'")
        normal_user_id = int(rows[0][0]) if rows else None
        print(f"       normal_user_id={normal_user_id}")
    else:
        fail("普通用户注册失败", json.dumps(resp_r, ensure_ascii=False))
else:
    fail("普通用户验证码获取失败",
         f"email_code={email_code}, phone_code={phone_code}")


# ═══════════════════════════════════════════════════════════════
# A-10  GET /api/me/permissions
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}A-10  GET /api/me/permissions{RESET}")

# 测试点1：不带 Token 调用 → 401
status, resp = http("GET", "/api/me/permissions")
print(f"  [1] 无Token  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
if status == 401 and resp.get("code") == 40001:
    ok("A-10.1 无Token调用返回 401/40001")
else:
    fail("A-10.1 无Token调用应返回 401/40001",
         f"实际 HTTP={status}, code={resp.get('code')}")

# 测试点2：普通用户 Token 调用 → 200，permissions 为数组
if normal_token:
    status, resp = http("GET", "/api/me/permissions", token=normal_token)
    print(f"  [2] 普通用户  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    data = resp.get("data", {})
    if status == 200 and resp.get("code") == 0 and isinstance(data.get("permissions"), list):
        ok("A-10.2 普通用户调用返回 200，permissions 为数组",
           f"permissions={data.get('permissions')}")
    else:
        fail("A-10.2 普通用户调用应返回 200，permissions 为数组",
             f"实际 HTTP={status}, data={json.dumps(data, ensure_ascii=False)}")
else:
    fail("A-10.2 跳过（普通用户注册失败）")

# 测试点3：管理员 Token 调用 → 200，permissions 包含 role:manage 等
admin_permissions_a10 = None
if admin_token:
    status, resp = http("GET", "/api/me/permissions", token=admin_token)
    print(f"  [3] 管理员    HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    data = resp.get("data", {})
    perms = data.get("permissions")
    if status == 200 and resp.get("code") == 0 and isinstance(perms, list):
        admin_permissions_a10 = perms
        if "role:manage" in perms:
            ok("A-10.3 管理员调用返回 200，permissions 包含 role:manage",
               f"共 {len(perms)} 个权限码")
        else:
            fail("A-10.3 permissions 应包含 role:manage", f"perms={perms}")
    else:
        fail("A-10.3 管理员调用应返回 200，permissions 为数组",
             f"实际 HTTP={status}, data={json.dumps(data, ensure_ascii=False)}")
else:
    fail("A-10.3 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# A-11  GET /api/admin/roles/{id}/permissions
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}A-11  GET /api/admin/roles/{{id}}/permissions{RESET}")

# 查 admin 角色 id
rows, err = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = int(rows[0][0]) if rows and not err else None
print(f"       admin 角色 id={admin_role_id}")

# DB 对照：admin 角色的权限码
db_admin_perm_codes = None
if admin_role_id:
    rows, err = db_query(
        "SELECT p.code FROM role_permissions rp "
        "JOIN permissions p ON p.id=rp.permission_id "
        "JOIN roles r ON r.id=rp.role_id WHERE r.code='admin'"
    )
    if not err:
        db_admin_perm_codes = sorted(r[0] for r in rows)
        print(f"       DB role_permissions(admin) 共 {len(db_admin_perm_codes)} 个: {db_admin_perm_codes}")

# 测试点1：用管理员账号查 admin 角色权限 → 200，与 DB 对照一致
if admin_token and admin_role_id:
    status, resp = http("GET", f"/api/admin/roles/{admin_role_id}/permissions", token=admin_token)
    print(f"  [1] 查admin角色权限  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    data = resp.get("data", {})
    perms = data.get("permissions")
    if status == 200 and resp.get("code") == 0 and isinstance(perms, list):
        if sorted(perms) == db_admin_perm_codes:
            ok("A-11.1 admin角色权限码与DB role_permissions一致",
               f"共 {len(perms)} 个")
        else:
            fail("A-11.1 admin角色权限码与DB role_permissions不一致",
                 f"接口返回(排序)={sorted(perms)}\n       DB(排序)={db_admin_perm_codes}")
    else:
        fail("A-11.1 应返回 200，permissions 为数组",
             f"实际 HTTP={status}, data={json.dumps(data, ensure_ascii=False)}")
else:
    fail("A-11.1 跳过（前置条件不满足）")

# 测试点2：不存在的角色 id → 404/40400
if admin_token:
    status, resp = http("GET", "/api/admin/roles/999999/permissions", token=admin_token)
    print(f"  [2] 角色不存在  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 404 and resp.get("code") == 40400:
        ok("A-11.2 不存在角色id返回 404/40400")
    else:
        fail("A-11.2 不存在角色id应返回 404/40400",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("A-11.2 跳过（管理员登录失败）")

# 测试点3：普通用户调用 → 403
if normal_token and admin_role_id:
    status, resp = http("GET", f"/api/admin/roles/{admin_role_id}/permissions", token=normal_token)
    print(f"  [3] 普通用户调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 403:
        ok("A-11.3 普通用户调用返回 403", f"code={resp.get('code')}")
    else:
        fail("A-11.3 普通用户调用应返回 403",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("A-11.3 跳过（前置条件不满足）")


# ═══════════════════════════════════════════════════════════════
# A-12  GET /api/admin/users/{id}/effective-permissions
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}A-12  GET /api/admin/users/{{id}}/effective-permissions{RESET}")

# 测试点1：管理员查自己 → permissions 应与 A-10 自查一致
if admin_token and admin_user_id is not None:
    status, resp = http("GET", f"/api/admin/users/{admin_user_id}/effective-permissions", token=admin_token)
    print(f"  [1] 管理员查自己  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    data = resp.get("data", {})
    perms = data.get("permissions")
    overrides = data.get("overrides")
    if status == 200 and resp.get("code") == 0 and isinstance(perms, list) and isinstance(overrides, list):
        if admin_permissions_a10 is not None and sorted(perms) == sorted(admin_permissions_a10):
            ok("A-12.1 管理员自查 permissions 与 A-10 (/api/me/permissions) 一致",
               f"共 {len(perms)} 个, overrides={overrides}")
        else:
            fail("A-12.1 管理员自查 permissions 与 A-10 结果不一致",
                 f"A-12={sorted(perms)}\n       A-10={sorted(admin_permissions_a10 or [])}")
    else:
        fail("A-12.1 应返回 200，permissions/overrides 均为数组",
             f"实际 HTTP={status}, data={json.dumps(data, ensure_ascii=False)}")
else:
    fail("A-12.1 跳过（前置条件不满足）")

# 测试点2：overrides 叠加逻辑（allow / deny）
# 使用 OVERRIDE_TARGET_USER_ID（已有角色权限的现有账号）作为目标，
# 因为新注册的普通用户没有任何角色，基线 permissions 为空，无法选取 deny 目标。
if admin_token and OVERRIDE_TARGET_USER_ID is not None:
    status, resp = http("GET", f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/effective-permissions", token=admin_token)
    print(f"  [2] 目标用户基线  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    baseline_perms = resp.get("data", {}).get("permissions", []) if status == 200 else []
    baseline_overrides = resp.get("data", {}).get("overrides", []) if status == 200 else []

    if status != 200:
        fail("A-12.2 跳过（查目标用户基线失败）", json.dumps(resp, ensure_ascii=False))
    elif baseline_overrides:
        fail("A-12.2 跳过（目标用户已存在 overrides，可能污染测试数据，请更换目标用户）",
             f"baseline_overrides={baseline_overrides}")
    else:
        # report:export (id=64) 这个权限码不在目标用户的角色权限中，用于 allow 覆盖测试
        ALLOW_PERM_ID = 64
        ALLOW_PERM_CODE = "report:export"

        # 选一个 baseline_perms 中已有的权限码用于 deny 覆盖测试
        if baseline_perms:
            deny_perm_code = baseline_perms[0]
            rows, err = db_query(f"SELECT id FROM permissions WHERE code='{deny_perm_code}'")
            deny_perm_id = int(rows[0][0]) if rows and not err else None
        else:
            deny_perm_code, deny_perm_id = None, None

        print(f"       基线 permissions={baseline_perms}")
        print(f"       allow覆盖: permission_id={ALLOW_PERM_ID} code={ALLOW_PERM_CODE}")
        print(f"       deny覆盖 : permission_id={deny_perm_id} code={deny_perm_code}")

        if ALLOW_PERM_CODE in baseline_perms:
            fail("A-12.2 跳过（report:export 已在基线权限中，无法验证allow叠加效果）")
        elif deny_perm_id is None:
            fail("A-12.2 跳过（目标用户基线权限为空，无法选取deny覆盖目标）")
        else:
            # 添加 allow 覆盖
            status_a, resp_a = http("POST", f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/permission-overrides",
                                     {"permission_id": ALLOW_PERM_ID, "effect": "allow", "reason": "PR31验收-allow测试"},
                                     token=admin_token)
            print(f"       添加allow覆盖  HTTP {status_a}: {json.dumps(resp_a, ensure_ascii=False)}")

            # 添加 deny 覆盖
            status_d, resp_d = http("POST", f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/permission-overrides",
                                     {"permission_id": deny_perm_id, "effect": "deny", "reason": "PR31验收-deny测试"},
                                     token=admin_token)
            print(f"       添加deny覆盖   HTTP {status_d}: {json.dumps(resp_d, ensure_ascii=False)}")

            if status_a != 200 or status_d != 200:
                fail("A-12.2 添加权限覆盖失败，无法继续验证",
                     f"allow HTTP={status_a}, deny HTTP={status_d}")
            else:
                # 再次查询 effective-permissions
                status2, resp2 = http("GET", f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/effective-permissions", token=admin_token)
                print(f"       叠加后查询  HTTP {status2}: {json.dumps(resp2, ensure_ascii=False)}")
                data2 = resp2.get("data", {})
                perms2 = data2.get("permissions", [])
                overrides2 = data2.get("overrides", [])

                if deny_perm_code in perms2:
                    fail("A-12.2 deny权限码仍出现在permissions中", f"deny_code={deny_perm_code}, perms={perms2}")
                else:
                    ok("A-12.2a deny权限码已从permissions中移除", f"deny_code={deny_perm_code}")

                if ALLOW_PERM_CODE not in perms2:
                    fail("A-12.2 allow权限码未出现在permissions中", f"allow_code={ALLOW_PERM_CODE}, perms={perms2}")
                else:
                    ok("A-12.2b allow权限码已出现在permissions中", f"allow_code={ALLOW_PERM_CODE}")

                override_codes_effects = {(o.get("code"), o.get("effect")) for o in overrides2}
                if (ALLOW_PERM_CODE, "allow") in override_codes_effects and (deny_perm_code, "deny") in override_codes_effects:
                    ok("A-12.2c overrides 数组包含新增的 allow/deny 记录",
                       f"overrides={overrides2}")
                else:
                    fail("A-12.2c overrides 数组未包含预期的 allow/deny 记录",
                         f"overrides={overrides2}")

            # 清理：查询 override 列表，删除刚才添加的两条
            status_l, resp_l = http("GET", f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/permission-overrides?page_size=100", token=admin_token)
            items = resp_l.get("data", {}).get("items", []) if status_l == 200 else []
            cleaned = 0
            for item in items:
                if item.get("permission_id") in (ALLOW_PERM_ID, deny_perm_id):
                    oid = item.get("id")
                    status_del, resp_del = http("DELETE",
                        f"/api/admin/users/{OVERRIDE_TARGET_USER_ID}/permission-overrides/{oid}", token=admin_token)
                    if status_del == 200:
                        cleaned += 1
                    else:
                        print(f"       {YELLOW}清理override id={oid} 失败 HTTP={status_del}: {resp_del}{RESET}")
            print(f"       清理完成：删除 {cleaned} 条新增 override 记录")
else:
    fail("A-12.2 跳过（前置条件不满足）")

# 测试点3：普通用户调用 → 403
if normal_token and normal_user_id is not None:
    status, resp = http("GET", f"/api/admin/users/{normal_user_id}/effective-permissions", token=normal_token)
    print(f"  [3] 普通用户调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 403:
        ok("A-12.3 普通用户调用返回 403", f"code={resp.get('code')}")
    else:
        fail("A-12.3 普通用户调用应返回 403",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("A-12.3 跳过（前置条件不满足）")


# ═══════════════════════════════════════════════════════════════
# 汇总
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}{CYAN}═══ 测试结果汇总 ═══{RESET}")
total = passed + failed
print(f"  总计: {total}  {GREEN}通过: {passed}{RESET}  {RED}失败: {failed}{RESET}")
print()
for status_label, label, detail in results:
    color = GREEN if status_label == "PASS" else RED
    print(f"  {color}[{status_label}]{RESET} {label}")

print()
if failed == 0:
    print(f"{BOLD}{GREEN}结论：PR#31 全部测试用例通过，建议可以合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#31 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

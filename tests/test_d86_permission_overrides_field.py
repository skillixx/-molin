#!/usr/bin/env python3
"""
D-86 验收测试 — GET /api/admin/users/:id 响应含 permission_overrides 字段

修复内容：用户详情接口响应新增 permission_overrides 数组字段，结构为：
  [{id, permission_id, permission_code, effect, reason?, expires_at?, created_by?, created_at}]
- 详情接口（GET /api/admin/users/:id）：如该用户在 user_permission_overrides 表中有记录，
  应如实返回；无记录则返回 []（不为 null）。
- 列表接口（GET /api/admin/users）：每个条目固定返回 permission_overrides: []。

设置 override 通过 PATCH /api/admin/users/:id/permission-overrides（全量替换）实现。
"""

import json
import os
import time
import hashlib
import urllib.error
import urllib.request
import subprocess

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN, RED, YELLOW, CYAN, BOLD, RESET = "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m"

passed = failed = 0


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}[PASS]{RESET} {label}" + (f"\n         {detail}" if detail else ""))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}[FAIL]{RESET} {label}" + (f"\n         {RED}{detail}{RESET}" if detail else ""))


def http(method, path, body=None, token=None, params=None, headers_extra=None):
    url = API_BASE + path
    if params:
        qs = "&".join(f"{k}={v}" for k, v in params.items())
        url = url + "?" + qs
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if headers_extra:
        headers.update(headers_extra)
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


def db_query(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    rows = []
    if result.returncode == 0:
        for line in result.stdout.strip().split("\n"):
            if line:
                rows.append(line.split("\t"))
    return rows, (result.stderr.strip() if result.returncode != 0 else None)


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return result.returncode == 0, (result.stderr.strip() if result.returncode != 0 else None)


TS = int(time.time())
ADMIN_EMAIL    = f"d86adm{TS}@testmail.io"
ADMIN_PHONE    = f"175{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@D86Admin"

USER1_EMAIL    = f"d86usr1{TS}@testmail.io"
USER1_PHONE    = f"176{TS % 100000000:08d}"
USER1_PASSWORD = "Test@D86User1"

USER2_EMAIL    = f"d86usr2{TS}@testmail.io"
USER2_PHONE    = f"177{TS % 100000000:08d}"
USER2_PASSWORD = "Test@D86User2"

print(f"{BOLD}{CYAN}D-86 验收测试 — GET /api/admin/users/:id 响应含 permission_overrides 字段{RESET}")
print(f"  API_BASE: {API_BASE}\n")

otp = "888888"
otp_sha = hashlib.sha256(otp.encode()).hexdigest()


def register_user(email, phone, password, username):
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{phone}' AND scene='register'")
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{email}' AND scene='register'")
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone','{phone}','{otp_sha}','register',DATE_ADD(NOW(), INTERVAL 490 MINUTE))"
    )
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email','{email}','{otp_sha}','register',DATE_ADD(NOW(), INTERVAL 490 MINUTE))"
    )
    s, r = http("POST", "/api/auth/register", {
        "email": email, "phone": phone, "password": password,
        "phone_code": otp, "email_code": otp, "username": username,
    })
    if s not in (200, 201) or r.get("code") != 0:
        print(f"  {RED}注册失败({email}): HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
        raise SystemExit(1)
    rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
    return int(rows[0][0])


# ── 前置：注册并配置管理员账号 ───────────────────────────────────────────────
print(f"{BOLD}[前置] 注册管理员账号{RESET}")
admin_id = register_user(ADMIN_EMAIL, ADMIN_PHONE, ADMIN_PASSWORD, f"d86adm{TS}")

rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
if not rows:
    print(f"  {RED}数据库中未找到 admin 角色，请先执行 seed{RESET}")
    raise SystemExit(1)
admin_role_id = int(rows[0][0])

db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({admin_id}, {admin_role_id})")
db_exec(f"UPDATE users SET admin_email_verified_at=NOW(), admin_phone_verified_at=NOW() WHERE id={admin_id}")

s, r = http("POST", "/api/auth/login/email", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
if s != 200 or r.get("code") != 0:
    print(f"  {RED}管理员登录失败: HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
    raise SystemExit(1)
admin_token = r["data"]["access_token"]
print(f"  管理员账号准备完成: user_id={admin_id}, admin_role_id={admin_role_id}")

# ── 前置：注册两个普通用户（user1 将设置 override，user2 不设置） ──────────────
print(f"\n{BOLD}[前置] 注册测试用户 user1（将设置 override）和 user2（不设置）{RESET}")
user1_id = register_user(USER1_EMAIL, USER1_PHONE, USER1_PASSWORD, f"d86usr1{TS}")
user2_id = register_user(USER2_EMAIL, USER2_PHONE, USER2_PASSWORD, f"d86usr2{TS}")
print(f"  user1_id={user1_id}, user2_id={user2_id}")

# ── 前置：从 permissions 表任选一个权限作为测试数据 ────────────────────────────
rows, _ = db_query("SELECT id, code FROM permissions ORDER BY id LIMIT 1")
if not rows:
    print(f"  {RED}数据库中未找到任何 permission 记录{RESET}")
    raise SystemExit(1)
test_perm_id = int(rows[0][0])
test_perm_code = rows[0][1]
print(f"  测试权限: permission_id={test_perm_id}, permission_code={test_perm_code}\n")


# ── 辅助：从列表找用户 ─────────────────────────────────────────────────────
def find_user_in_list(users_list, target_id):
    for u in users_list:
        uid = u.get("id") or u.get("user_id")
        if uid == target_id:
            return u
    return None


def get_all_users(token, page_size=100):
    s, r = http("GET", "/api/admin/users", token=token, params={"page": "1", "page_size": str(page_size)})
    return s, r


def extract_users(r):
    data = r.get("data", {})
    if isinstance(data, list):
        return data
    elif isinstance(data, dict):
        return data.get("list") or data.get("items") or data.get("users") or []
    return []


def extract_user_obj(r):
    data = r.get("data", {})
    return data if isinstance(data, dict) and ("permission_overrides" in data or "id" in data) else data.get("user", data)


# ── D86-01：设置 override 后，详情接口 permission_overrides 含该条目 ────────────
print(f"{BOLD}D86-01  PATCH .../permission-overrides 设置 override 后，GET 详情包含该条目{RESET}")
reason_text = "测试D86-自动化-allow"
s, r = http(
    "PATCH", f"/api/admin/users/{user1_id}/permission-overrides",
    body={"items": [{"permission_id": test_perm_id, "effect": "allow", "reason": reason_text, "expires_at": None}]},
    token=admin_token,
)
print(f"    PATCH HTTP {s}, resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s != 200 or r.get("code") != 0:
    fail("D86-01  PATCH permission-overrides 请求失败", f"HTTP={s}, code={r.get('code')}, msg={r.get('message','')}")
else:
    s2, r2 = http("GET", f"/api/admin/users/{user1_id}", token=admin_token)
    print(f"    GET HTTP {s2}")
    if s2 != 200 or r2.get("code") != 0:
        fail("D86-01  GET 用户详情请求失败", f"HTTP={s2}, code={r2.get('code')}, msg={r2.get('message','')}")
    else:
        user_obj = extract_user_obj(r2)
        overrides = user_obj.get("permission_overrides")
        if overrides is None:
            fail("D86-01  详情缺少 permission_overrides 字段", f"data keys: {list(user_obj.keys())}")
        elif not isinstance(overrides, list) or len(overrides) == 0:
            fail("D86-01  permission_overrides 为空", f"overrides={overrides}")
        else:
            entry = next((o for o in overrides if o.get("permission_id") == test_perm_id), None)
            if entry is None:
                fail("D86-01  permission_overrides 不含设置的权限", f"overrides={overrides}")
            else:
                errs = []
                if entry.get("permission_code") != test_perm_code:
                    errs.append(f"permission_code 不匹配: got={entry.get('permission_code')!r}, want={test_perm_code!r}")
                if entry.get("effect") != "allow":
                    errs.append(f"effect 不匹配: got={entry.get('effect')!r}, want='allow'")
                if entry.get("reason") != reason_text:
                    errs.append(f"reason 不匹配: got={entry.get('reason')!r}, want={reason_text!r}")
                if not entry.get("id"):
                    errs.append(f"id 为空: {entry.get('id')!r}")
                if not entry.get("created_at"):
                    errs.append(f"created_at 为空: {entry.get('created_at')!r}")
                if errs:
                    fail("D86-01  permission_overrides 条目字段不符合预期", "; ".join(errs) + f" | entry={entry}")
                else:
                    ok("D86-01  permission_overrides 含设置的条目，字段一致且 id/created_at 非空", f"entry={entry}")

# ── D86-02：未设置过 override 的用户，详情 permission_overrides 为 [] ─────────
print(f"\n{BOLD}D86-02  GET /api/admin/users/{user2_id} — 未设置 override 用户 permission_overrides 为 []{RESET}")
s, r = http("GET", f"/api/admin/users/{user2_id}", token=admin_token)
print(f"    HTTP {s}")
if s != 200 or r.get("code") != 0:
    fail("D86-02  详情接口请求失败", f"HTTP={s}, code={r.get('code')}, msg={r.get('message','')}")
else:
    user_obj = extract_user_obj(r)
    overrides = user_obj.get("permission_overrides")
    if overrides is None:
        fail("D86-02  详情缺少 permission_overrides 字段", f"data keys: {list(user_obj.keys())}")
    elif overrides == []:
        ok("D86-02  未设置 override 的用户 permission_overrides 为 []", f"overrides={overrides}")
    else:
        fail("D86-02  permission_overrides 不为空", f"overrides={overrides}")

# ── D86-03：列表接口每条记录含 permission_overrides: [] ─────────────────────
print(f"\n{BOLD}D86-03  GET /api/admin/users — 所有记录均含 permission_overrides 字段且为 []{RESET}")
s, r = get_all_users(admin_token)
print(f"    HTTP {s}")
if s != 200 or r.get("code") != 0:
    fail("D86-03  列表接口请求失败", f"HTTP={s}, code={r.get('code')}, msg={r.get('message','')}")
else:
    users = extract_users(r)
    if len(users) == 0:
        fail("D86-03  列表为空，无法验证", "返回用户数=0")
    else:
        missing = []
        non_empty = []
        for u in users:
            uid = u.get("id") or u.get("user_id")
            if "permission_overrides" not in u:
                missing.append(uid)
            elif u.get("permission_overrides") != []:
                non_empty.append((uid, u.get("permission_overrides")))
        if missing:
            fail("D86-03  以下用户条目缺少 permission_overrides 字段", f"user_id 列表: {missing[:10]}")
        elif non_empty:
            fail("D86-03  以下用户条目 permission_overrides 非空数组", f"{non_empty[:5]}")
        else:
            # 特别验证 user1（已设置 override）在列表中也是 []
            user1_entry = find_user_in_list(users, user1_id)
            extra = f"; user1（已设 override）在列表中 permission_overrides={user1_entry.get('permission_overrides')}" if user1_entry else ""
            ok("D86-03  所有用户条目均含 permission_overrides 字段且为 []", f"共检查 {len(users)} 条记录{extra}")

# ── D86-04（可选）：reason/expires_at/created_by 为 null 时不报错，字段省略或为 null ──
print(f"\n{BOLD}D86-04  设置 expires_at=null, reason=null 时详情响应正常，字段省略或为 null{RESET}")
s, r = http(
    "PATCH", f"/api/admin/users/{user2_id}/permission-overrides",
    body={"items": [{"permission_id": test_perm_id, "effect": "deny", "reason": None, "expires_at": None}]},
    token=admin_token,
)
print(f"    PATCH HTTP {s}, resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s != 200 or r.get("code") != 0:
    fail("D86-04  PATCH permission-overrides 请求失败", f"HTTP={s}, code={r.get('code')}, msg={r.get('message','')}")
else:
    s2, r2 = http("GET", f"/api/admin/users/{user2_id}", token=admin_token)
    print(f"    GET HTTP {s2}")
    if s2 != 200 or r2.get("code") != 0:
        fail("D86-04  GET 用户详情请求失败", f"HTTP={s2}, code={r2.get('code')}, msg={r2.get('message','')}")
    else:
        user_obj = extract_user_obj(r2)
        overrides = user_obj.get("permission_overrides")
        if not isinstance(overrides, list) or len(overrides) == 0:
            fail("D86-04  permission_overrides 为空，无法验证", f"overrides={overrides}")
        else:
            entry = next((o for o in overrides if o.get("permission_id") == test_perm_id), None)
            if entry is None:
                fail("D86-04  permission_overrides 不含设置的权限", f"overrides={overrides}")
            else:
                errs = []
                # reason/expires_at/created_by 为 null：要么不存在该 key（omitempty），要么值为 None
                for field in ("reason", "expires_at"):
                    if field in entry and entry.get(field) is not None:
                        errs.append(f"{field} 应为 null 或省略，但实际为 {entry.get(field)!r}")
                if entry.get("effect") != "deny":
                    errs.append(f"effect 不匹配: got={entry.get('effect')!r}, want='deny'")
                if not entry.get("id") or not entry.get("created_at"):
                    errs.append(f"id/created_at 异常: id={entry.get('id')!r}, created_at={entry.get('created_at')!r}")
                if errs:
                    fail("D86-04  null 字段处理不符合预期", "; ".join(errs) + f" | entry={entry}")
                else:
                    ok("D86-04  reason/expires_at 为 null 时响应正常（省略或为 null），无报错", f"entry={entry}")

# ── 清理测试数据 ────────────────────────────────────────────────────────────
print(f"\n{BOLD}[清理] 删除测试数据{RESET}")
db_exec(f"DELETE FROM user_permission_overrides WHERE user_id IN ({user1_id}, {user2_id})")
db_exec(f"DELETE FROM user_roles WHERE user_id IN ({admin_id}, {user1_id}, {user2_id})")
db_exec(f"DELETE FROM users WHERE id IN ({admin_id}, {user1_id}, {user2_id})")
db_exec(
    f"DELETE FROM verification_codes WHERE target_value IN "
    f"('{ADMIN_PHONE}','{ADMIN_EMAIL}','{USER1_PHONE}','{USER1_EMAIL}','{USER2_PHONE}','{USER2_EMAIL}')"
)
print(f"  已清理 user_id={admin_id}, {user1_id}, {user2_id}")

# ── 汇总 ────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*55}{RESET}")
print(f"{BOLD}总计: PASS={passed}, FAIL={failed}{RESET}")
if failed == 0:
    print(f"{GREEN}{BOLD}全部通过 — D-86 permission_overrides 字段验收通过{RESET}")
else:
    print(f"{RED}{BOLD}存在失败项 — D-86 permission_overrides 字段验收未通过{RESET}")

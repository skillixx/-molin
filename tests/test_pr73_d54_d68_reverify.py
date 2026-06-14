#!/usr/bin/env python3
"""
PR#73 修复验证 — D-54、D-68 重新验证

D-54  PATCH /api/admin/users/{不存在ID}/status → 应返回 404/40400（修复前为 500）
D-68  POST /api/admin/user-groups/{id}/invite-codes，default_group_role="superadmin" → 应返回 400/40000（修复前为 500）
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

GREEN, RED, YELLOW, CYAN, BOLD, RESET = "\033[92m","\033[91m","\033[93m","\033[96m","\033[1m","\033[0m"

passed = failed = 0


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}[PASS]{RESET} {label}" + (f"\n         {detail}" if detail else ""))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}[FAIL]{RESET} {label}" + (f"\n         {RED}{detail}{RESET}" if detail else ""))


def http(method, path, body=None, token=None):
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
ADMIN_EMAIL    = f"pr73adm{TS}@testmail.io"
ADMIN_PHONE    = f"172{TS % 100000000:08d}"
ADMIN_PASSWORD = "Test@Pr73Admin"

print(f"{BOLD}{CYAN}PR#73 修复重新验证 — D-54 / D-68{RESET}")
print(f"  API_BASE: {API_BASE}\n")

# ── 前置：注册管理员账号并赋予 admin 角色 ──────────────────────────────────
otp = "888888"
otp_sha = hashlib.sha256(otp.encode()).hexdigest()
db_exec(f"DELETE FROM verification_codes WHERE target_value='{ADMIN_PHONE}' AND scene='register'")
db_exec(f"DELETE FROM verification_codes WHERE target_value='{ADMIN_EMAIL}' AND scene='register'")
db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('phone','{ADMIN_PHONE}','{otp_sha}','register',DATE_ADD(NOW(), INTERVAL 490 MINUTE))")
db_exec(f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('email','{ADMIN_EMAIL}','{otp_sha}','register',DATE_ADD(NOW(), INTERVAL 490 MINUTE))")

s, r = http("POST", "/api/auth/register", {
    "email": ADMIN_EMAIL, "phone": ADMIN_PHONE, "password": ADMIN_PASSWORD,
    "phone_code": otp, "email_code": otp, "username": f"pr73adm{TS}",
})
if s not in (200, 201) or r.get("code") != 0:
    print(f"  {RED}注册失败: HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
    raise SystemExit(1)

rows, _ = db_query(f"SELECT id FROM users WHERE email='{ADMIN_EMAIL}'")
user_id = int(rows[0][0])

rows, _ = db_query("SELECT id FROM roles WHERE code='admin'")
admin_role_id = int(rows[0][0])

db_exec(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {admin_role_id})")
db_exec(f"UPDATE users SET admin_email_verified_at=NOW(), admin_phone_verified_at=NOW() WHERE id={user_id}")

s, r = http("POST", "/api/auth/login/email", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
if s != 200 or r.get("code") != 0:
    print(f"  {RED}登录失败: HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
    raise SystemExit(1)
admin_token = r["data"]["access_token"]
print(f"  管理员账号准备完成: user_id={user_id}, role={admin_role_id}\n")

# ── D-54：PATCH /api/admin/users/{不存在}/status → 404/40400 ──────────────
print(f"{BOLD}D-54  PATCH /api/admin/users/999999999/status（不存在用户）{RESET}")
s, r = http("PATCH", "/api/admin/users/999999999/status", {"status": "disabled", "reason": "D54测试"}, token=admin_token)
print(f"    HTTP {s}: {json.dumps(r, ensure_ascii=False)}")
if s == 404 and r.get("code") == 40400:
    ok("D-54  不存在用户 → HTTP 404 / code=40400（修复生效）")
else:
    fail("D-54  返回非预期", f"HTTP={s}, code={r.get('code')}")

# ── D-68：POST /api/admin/user-groups/{id}/invite-codes, default_group_role=superadmin → 400/40000 ──
print(f"\n{BOLD}D-68  CreateInviteCode default_group_role='superadmin' → 400{RESET}")
s, r = http("POST", "/api/admin/user-groups", {"code": f"pr73grp{TS}", "name": f"PR73Grp{TS}", "type": "custom"}, token=admin_token)
if s not in (200, 201) or r.get("code") != 0:
    fail("D-68  前置创建分组失败", f"HTTP={s} {json.dumps(r, ensure_ascii=False)[:200]}")
else:
    group_id = r["data"]["id"]
    s, r = http("POST", f"/api/admin/user-groups/{group_id}/invite-codes", {"default_group_role": "superadmin"}, token=admin_token)
    print(f"    HTTP {s}: {json.dumps(r, ensure_ascii=False)}")
    if s == 400 and r.get("code") == 40000:
        ok("D-68  default_group_role='superadmin' → HTTP 400 / code=40000（修复生效）")
    else:
        fail("D-68  返回非预期", f"HTTP={s}, code={r.get('code')}")
    db_exec(f"DELETE FROM user_groups WHERE id={group_id}")

# ── 清理 ────────────────────────────────────────────────────────────────
db_exec(f"DELETE FROM user_roles WHERE user_id={user_id}")
db_exec(f"DELETE FROM users WHERE id={user_id}")

# ── 汇总 ────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*50}{RESET}")
print(f"{BOLD}总计: PASS={passed}, FAIL={failed}{RESET}")
if failed == 0:
    print(f"{GREEN}{BOLD}全部通过{RESET}")
else:
    print(f"{RED}{BOLD}存在失败项{RESET}")

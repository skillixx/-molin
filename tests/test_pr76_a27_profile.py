#!/usr/bin/env python3
"""
PR#76 验收测试 — A-27 PATCH /api/me/profile 个人资料更新接口

测试用例：
  A27-01  未认证请求 → 401/40001
  A27-02  仅更新 nickname（不传 avatar_url） → 200，GET /api/me 中 nickname 已变更
  A27-03  仅更新 avatar_url（https:// 开头，不传 nickname） → 200，GET /api/me 中 avatar_url 已变更
  A27-04  nickname + avatar_url 同时更新 → 200，两字段均已更新
  A27-05  GET /api/me 响应包含 nickname 和 avatar_url 字段（可为 null）
  A27-06  nickname 超过 64 字符 → 400/40000
  A27-07  avatar_url 不以 https:// 开头（http://） → 400/40000
  A27-08  avatar_url 超过 512 字符 → 400/40000
"""

import hashlib
import json
import os
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

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


def http(method, path, body=None, token=None, params=None):
    url = API_BASE + path
    if params:
        url = url + "?" + urllib.parse.urlencode(params)
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


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return result.returncode == 0, (result.stderr.strip() if result.returncode != 0 else None)


def db_query(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    rows = []
    if result.returncode == 0:
        for line in result.stdout.strip().split("\n"):
            if line:
                rows.append(line.split("\t"))
    return rows, (result.stderr.strip() if result.returncode != 0 else None)


# 测试账号
TS = int(time.time())
OTP = "888888"
OTP_SHA = hashlib.sha256(OTP.encode()).hexdigest()

TEST_EMAIL    = f"a27u{TS}@testmail.io"
TEST_PHONE    = f"180{TS % 100000000:08d}"
TEST_PASSWORD = "Test@A27Profile"
TEST_USERNAME = f"a27u{TS}"


def seed_otp(target_type, target_value, scene="register"):
    """直接向数据库写入已知 OTP，绕过短信/邮件发送。"""
    db_exec(f"DELETE FROM verification_codes WHERE target_value='{target_value}' AND scene='{scene}'")
    db_exec(
        f"INSERT INTO verification_codes (target_type, target_value, code, scene, expires_at) "
        f"VALUES ('{target_type}','{target_value}','{OTP_SHA}','{scene}',DATE_ADD(NOW(), INTERVAL 490 MINUTE))"
    )


def register_and_login():
    """注册测试账号并登录，返回 access_token。"""
    seed_otp("phone", TEST_PHONE, "register")
    seed_otp("email", TEST_EMAIL, "register")
    s, r = http("POST", "/api/auth/register", {
        "email": TEST_EMAIL,
        "phone": TEST_PHONE,
        "password": TEST_PASSWORD,
        "phone_code": OTP,
        "email_code": OTP,
        "username": TEST_USERNAME,
    })
    if s not in (200, 201) or r.get("code") != 0:
        print(f"  {RED}注册失败: HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
        raise SystemExit(1)

    s, r = http("POST", "/api/auth/login/email", {
        "email": TEST_EMAIL,
        "password": TEST_PASSWORD,
    })
    if s != 200 or r.get("code") != 0:
        print(f"  {RED}登录失败: HTTP={s} {json.dumps(r, ensure_ascii=False)[:300]}{RESET}")
        raise SystemExit(1)
    return r["data"]["access_token"]


# ────────────────────────────────────────────────────────────────────────────────
print(f"{BOLD}{CYAN}PR#76 验收测试 — A-27 PATCH /api/me/profile{RESET}")
print(f"  API_BASE: {API_BASE}  TS={TS}\n")

# 前置：注册并登录，获取有效 token
print(f"{BOLD}[前置] 注册测试账号并登录{RESET}")
token = register_and_login()
print(f"  账号准备完成: email={TEST_EMAIL}\n")

# ────────────────────────────────────────────────────────────────────────────────
print(f"{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-01  未认证请求 PATCH /api/me/profile → 401/40001{RESET}")
s, r = http("PATCH", "/api/me/profile", {"nickname": "test"})
print(f"  HTTP {s}  code={r.get('code')}")
if s in (401, 403) or r.get("code") == 40001:
    ok("A27-01  未认证请求被拒绝", f"HTTP={s} code={r.get('code')}")
else:
    fail("A27-01  期望 401/40001，实际未被拒绝", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-02  仅更新 nickname（不传 avatar_url）{RESET}")
new_nick = f"昵称{TS}"
s, r = http("PATCH", "/api/me/profile", {"nickname": new_nick}, token=token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 200 and r.get("code") == 0:
    # 通过 GET /api/me 确认 nickname 已实际更新
    s2, r2 = http("GET", "/api/me", token=token)
    me_data = r2.get("data", {})
    if me_data.get("nickname") == new_nick:
        ok("A27-02  nickname 更新成功", f"nickname={me_data.get('nickname')}")
    else:
        fail("A27-02  PATCH 返回 200，但 GET /api/me 中 nickname 未更新",
             f"期望 nickname={new_nick}，实际={me_data.get('nickname')}")
else:
    fail("A27-02  PATCH 请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-03  仅更新 avatar_url（https:// 开头，不传 nickname）{RESET}")
new_avatar = f"https://cdn.example.com/avatar_{TS}.png"
s, r = http("PATCH", "/api/me/profile", {"avatar_url": new_avatar}, token=token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 200 and r.get("code") == 0:
    s2, r2 = http("GET", "/api/me", token=token)
    me_data = r2.get("data", {})
    if me_data.get("avatar_url") == new_avatar:
        ok("A27-03  avatar_url 更新成功", f"avatar_url={me_data.get('avatar_url')}")
    else:
        fail("A27-03  PATCH 返回 200，但 GET /api/me 中 avatar_url 未更新",
             f"期望={new_avatar}，实际={me_data.get('avatar_url')}")
else:
    fail("A27-03  PATCH 请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-04  nickname + avatar_url 同时更新{RESET}")
new_nick2   = f"双更新昵称{TS}"
new_avatar2 = f"https://cdn.example.com/both_{TS}.jpg"
s, r = http("PATCH", "/api/me/profile", {"nickname": new_nick2, "avatar_url": new_avatar2}, token=token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:200]}")
if s == 200 and r.get("code") == 0:
    s2, r2 = http("GET", "/api/me", token=token)
    me_data = r2.get("data", {})
    nick_ok   = me_data.get("nickname") == new_nick2
    avatar_ok = me_data.get("avatar_url") == new_avatar2
    if nick_ok and avatar_ok:
        ok("A27-04  nickname + avatar_url 同时更新成功",
           f"nickname={me_data.get('nickname')} avatar_url={me_data.get('avatar_url')}")
    else:
        fail("A27-04  部分字段未更新",
             f"nickname期望={new_nick2} 实际={me_data.get('nickname')} | "
             f"avatar_url期望={new_avatar2} 实际={me_data.get('avatar_url')}")
else:
    fail("A27-04  PATCH 请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-05  GET /api/me 响应包含 nickname 和 avatar_url 字段{RESET}")
s, r = http("GET", "/api/me", token=token)
print(f"  HTTP {s}  resp={json.dumps(r, ensure_ascii=False)[:300]}")
if s == 200 and r.get("code") == 0:
    me_data = r.get("data", {})
    has_nickname   = "nickname" in me_data
    has_avatar_url = "avatar_url" in me_data
    if has_nickname and has_avatar_url:
        ok("A27-05  GET /api/me 包含 nickname 和 avatar_url 字段",
           f"nickname={me_data.get('nickname')} avatar_url={me_data.get('avatar_url')}")
    else:
        missing = []
        if not has_nickname:
            missing.append("nickname")
        if not has_avatar_url:
            missing.append("avatar_url")
        fail("A27-05  GET /api/me 响应缺少字段", f"缺少字段: {', '.join(missing)}")
else:
    fail("A27-05  GET /api/me 请求失败", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-06  nickname 超过 64 字符 → 400/40000{RESET}")
long_nick = "昵" * 65  # 65 个汉字，超过 64 字符限制
s, r = http("PATCH", "/api/me/profile", {"nickname": long_nick}, token=token)
print(f"  HTTP {s}  code={r.get('code')} msg={r.get('message','')}")
if s == 400 and r.get("code") == 40000:
    ok("A27-06  nickname 超长被拒绝", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("A27-06  期望 400/40000，实际结果不符",
         f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-07  avatar_url 不以 https:// 开头（用 http://）→ 400/40000{RESET}")
http_url = f"http://cdn.example.com/avatar_{TS}.png"
s, r = http("PATCH", "/api/me/profile", {"avatar_url": http_url}, token=token)
print(f"  HTTP {s}  code={r.get('code')} msg={r.get('message','')}")
if s == 400 and r.get("code") == 40000:
    ok("A27-07  非 https:// 开头的 avatar_url 被拒绝",
       f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("A27-07  期望 400/40000，实际结果不符",
         f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
print(f"{BOLD}A27-08  avatar_url 超过 512 字符 → 400/40000{RESET}")
# 构造一个超过 512 字符的 https:// URL
long_url = "https://cdn.example.com/" + "a" * 500  # 总长度约 524，超过 512
s, r = http("PATCH", "/api/me/profile", {"avatar_url": long_url}, token=token)
print(f"  HTTP {s}  code={r.get('code')} msg={r.get('message','')}")
if s == 400 and r.get("code") == 40000:
    ok("A27-08  avatar_url 超长被拒绝", f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")
else:
    fail("A27-08  期望 400/40000，实际结果不符",
         f"HTTP={s} code={r.get('code')} msg={r.get('message','')}")

# ────────────────────────────────────────────────────────────────────────────────
print(f"\n{BOLD}{'='*60}{RESET}")
total = passed + failed
print(f"{BOLD}总计：{passed}/{total} PASS{RESET}")
if failed == 0:
    print(f"{GREEN}所有用例通过，A-27 验收通过。{RESET}")
else:
    print(f"{RED}{failed} 个用例失败，请检查上方详情。{RESET}")
    raise SystemExit(1)

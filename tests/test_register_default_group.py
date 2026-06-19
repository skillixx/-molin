#!/usr/bin/env python3
"""
注册落组（邀请码 / 默认组兜底）端到端验收脚本

被测分支：feature/backend-auth-register-default-group
核心逻辑：
  - server/internal/modules/auth/service/auth_service.go  Register → groupJoiner.AssignOnRegister
  - server/internal/modules/iam/service/group_service.go  AssignOnRegister / joinDefaultGroup
  - server/internal/modules/iam/repository/group_repo.go  FindDefaultGroup

验收用例：
  A  不带 invite_code 注册       → 落默认组（is_default=1），group_role=member
  B  带有效 invite_code 注册     → 落邀请码对应组，group_role=邀请码 default_group_role(admin)；used_count+1
  C  带无效 invite_code 注册     → 不存在的码：方案 A 降级落默认组，注册仍成功
  C2 带已满 invite_code 注册     → max_uses 用尽：方案 A 降级落默认组，注册仍成功
  D  未配置默认组 + 不带 invite_code → 注册成功，但不落任何组

前置数据由脚本自建并在结束时清理：
  - 默认组            code=e2e_def_grp   is_default=1
  - 邀请组            code=e2e_inv_grp   is_default=0
  - 有效邀请码        code=E2EVALID01    -> 邀请组, default_group_role=admin, max_uses=0(不限)
  - 已满邀请码        code=E2EFULL001    -> 邀请组, max_uses=1, used_count=1

实现说明：
  注册接口需要双 OTP（phone_code + email_code）。验证码存库为 SHA-256(明文)，
  且 send-code 接口有 IP 限流（10 次/分钟）。为避免限流抖动，本脚本直接向
  verification_codes 表插入 SHA-256(FIXED_CODE) 的已用前记录，使用已知明文走注册，
  与真实发码路径等价（service.Check 也是对明文做 SHA-256 后比对）。

用法（本地或测试服务器，需 API 已运行新二进制 + 可直连 MySQL）：
  API_BASE=http://127.0.0.1:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  NO_PROXY='*' python3 tests/test_register_default_group.py
"""

import hashlib
import json
import os
import subprocess
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://127.0.0.1:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# 绕过沙箱 HTTP 代理（否则 urllib 会走代理打不到 localhost）
os.environ.setdefault("NO_PROXY", "*")
os.environ.setdefault("no_proxy", "*")

FIXED_CODE = "654321"  # 固定明文验证码（脚本自行写入 verification_codes 的 SHA-256）

GREEN, RED, YELLOW, CYAN, BOLD, RESET = (
    "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m")

passed = 0
failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}PASS{RESET} {label}" + (f"\n       {YELLOW}{detail}{RESET}" if detail else ""))
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}FAIL{RESET} {label}" + (f"\n       {RED}{detail}{RESET}" if detail else ""))
    results.append(("FAIL", label, detail))


# ── HTTP 工具（urllib，显式禁用代理）──────────────────────────────────────────
_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def http(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with _opener.open(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL 工具 ────────────────────────────────────────────────────────────────
def _mysql(args, sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}",
           f"-p{MYSQL_PASS}", MYSQL_DB] + args + ["-e", sql]
    return subprocess.run(cmd, capture_output=True, text=True, timeout=15)


def db_query(sql):
    r = _mysql(["-N", "-s"], sql)
    if r.returncode != 0:
        return None, r.stderr.strip()
    rows = [ln.split("\t") for ln in r.stdout.strip().split("\n") if ln]
    return rows, None


def db_exec(sql):
    r = _mysql([], sql)
    if r.returncode != 0:
        return False, r.stderr.strip()
    return True, None


def sha256hex(s):
    return hashlib.sha256(s.encode()).hexdigest()


# ── 前置数据 ──────────────────────────────────────────────────────────────────
DEF_CODE = "e2e_def_grp"
INV_CODE = "e2e_inv_grp"
VALID_INVITE = "E2EVALID01"
FULL_INVITE = "E2EFULL001"
_created_user_emails = []


def setup():
    teardown(silent=True)  # 幂等清理历史残留
    db_exec(f"INSERT INTO user_groups (code,name,type,is_default,created_at,updated_at) "
            f"VALUES ('{DEF_CODE}','E2E默认组','custom',1,NOW(),NOW());")
    db_exec(f"INSERT INTO user_groups (code,name,type,is_default,created_at,updated_at) "
            f"VALUES ('{INV_CODE}','E2E邀请组','custom',0,NOW(),NOW());")
    rows, _ = db_query(f"SELECT id FROM user_groups WHERE code='{INV_CODE}';")
    inv_id = rows[0][0]
    db_exec(f"INSERT INTO group_invite_codes (code,group_id,default_group_role,max_uses,used_count,status,created_at,updated_at) "
            f"VALUES ('{VALID_INVITE}',{inv_id},'admin',0,0,'active',NOW(),NOW());")
    db_exec(f"INSERT INTO group_invite_codes (code,group_id,default_group_role,max_uses,used_count,status,created_at,updated_at) "
            f"VALUES ('{FULL_INVITE}',{inv_id},'member',1,1,'active',NOW(),NOW());")
    drows, _ = db_query(f"SELECT id FROM user_groups WHERE code='{DEF_CODE}';")
    return drows[0][0], inv_id


def teardown(silent=False):
    if _created_user_emails:
        emails = ",".join("'%s'" % e for e in _created_user_emails)
        phones = ",".join("'%s'" % p for p in _created_user_phones)
        db_exec(f"DELETE FROM user_group_members WHERE user_id IN (SELECT id FROM users WHERE email IN ({emails}));")
        db_exec(f"DELETE FROM user_sessions WHERE user_id IN (SELECT id FROM users WHERE email IN ({emails}));")
        db_exec(f"DELETE FROM users WHERE email IN ({emails});")
        db_exec(f"DELETE FROM verification_codes WHERE target_value IN ({emails}) OR target_value IN ({phones});")
    db_exec(f"DELETE FROM group_invite_codes WHERE code IN ('{VALID_INVITE}','{FULL_INVITE}');")
    db_exec(f"DELETE FROM user_group_members WHERE group_id IN "
            f"(SELECT id FROM user_groups WHERE code IN ('{DEF_CODE}','{INV_CODE}'));")
    db_exec(f"DELETE FROM user_groups WHERE code IN ('{DEF_CODE}','{INV_CODE}');")
    if not silent:
        print(f"{CYAN}前置/测试数据已清理{RESET}")


# ── 注册辅助 ──────────────────────────────────────────────────────────────────
_seq = 0
_created_user_phones = []


def _seed_code(target_type, target_value):
    """向 verification_codes 表写入一条 scene=register、未使用、有效期足够长（规避 DB/应用时区差）的验证码（存 SHA-256）。"""
    code_hash = sha256hex(FIXED_CODE)
    db_exec(
        "INSERT INTO verification_codes (target_type,target_value,code,scene,expires_at,created_at) "
        f"VALUES ('{target_type}','{target_value}','{code_hash}','register',"
        "DATE_ADD(NOW(), INTERVAL 1 DAY), NOW());")


def register(invite_code=None):
    """直接注入双 OTP（绕过 send-code 限流）→ 调注册接口。返回 (status, body, email)。"""
    global _seq
    _seq += 1
    base = int(time.time() * 1000) % 1_000_000_000
    phone = f"13{(base + _seq) % 1000000000:09d}"
    email = f"e2e_reg_{base}_{_seq}@molin.test"
    uname = f"e2ereg{base}{_seq}"
    _created_user_emails.append(email)
    _created_user_phones.append(phone)

    # 注入双 OTP（明文 FIXED_CODE）
    _seed_code("phone", phone)
    _seed_code("email", email)

    body = {"username": uname, "phone": phone, "email": email,
            "password": "Test@123456",
            "phone_code": FIXED_CODE, "email_code": FIXED_CODE}
    if invite_code is not None:
        body["invite_code"] = invite_code
    s, b = http("POST", "/api/auth/register", body)
    return s, b, email


def user_id_of(email):
    rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}';")
    return rows[0][0] if rows else None


def memberships_of(uid):
    """返回 [(group_id, group_role, group_code, is_default), ...]"""
    rows, _ = db_query(
        f"SELECT m.group_id,m.group_role,g.code,g.is_default "
        f"FROM user_group_members m JOIN user_groups g ON g.id=m.group_id "
        f"WHERE m.user_id={uid};")
    return rows or []


# ── 用例 ─────────────────────────────────────────────────────────────────────
def case_A(def_id):
    print(f"\n{BOLD}[A] 不带 invite_code 注册 → 落默认组 member{RESET}")
    s, b, email = register(invite_code=None)
    if s not in (200, 201) or b.get("code") != 0:
        return fail("A 注册应成功", f"status={s} body={b}")
    uid = user_id_of(email)
    ms = memberships_of(uid)
    if len(ms) != 1:
        return fail("A 应恰好落 1 个组", f"user={uid} memberships={ms}")
    gid, role, code, isdef = ms[0]
    if gid == str(def_id) and role == "member" and isdef == "1":
        ok("A 落入默认组且 group_role=member", f"user={uid} group_id={gid} role={role} code={code}")
    else:
        fail("A 落组不符合预期", f"user={uid} memberships={ms} 期望 group_id={def_id}/member/is_default=1")


def case_B(inv_id):
    print(f"\n{BOLD}[B] 带有效 invite_code 注册 → 落邀请组 + group_role=admin{RESET}")
    s, b, email = register(invite_code=VALID_INVITE)
    if s not in (200, 201) or b.get("code") != 0:
        return fail("B 注册应成功", f"status={s} body={b}")
    uid = user_id_of(email)
    ms = memberships_of(uid)
    if len(ms) != 1:
        return fail("B 应恰好落 1 个组（邀请组，不重复落默认组）", f"user={uid} memberships={ms}")
    gid, role, code, isdef = ms[0]
    if gid == str(inv_id) and role == "admin":
        ok("B 落入邀请码对应组且 group_role=邀请码配置(admin)",
           f"user={uid} group_id={gid} role={role} code={code}")
    else:
        fail("B 落组不符合预期", f"user={uid} memberships={ms} 期望 group_id={inv_id}/admin")
    rows, _ = db_query(f"SELECT used_count FROM group_invite_codes WHERE code='{VALID_INVITE}';")
    if rows and rows[0][0] == "1":
        ok("B 邀请码 used_count 递增为 1", f"used_count={rows[0][0]}")
    else:
        fail("B 邀请码 used_count 未正确递增", f"rows={rows}")


def case_C(def_id):
    print(f"\n{BOLD}[C] 带【不存在】invite_code 注册 → 方案A 降级落默认组，注册成功{RESET}")
    s, b, email = register(invite_code="NOSUCHCODE999")
    if s not in (200, 201) or b.get("code") != 0:
        return fail("C 注册仍应成功（方案A，不阻断）", f"status={s} body={b}")
    uid = user_id_of(email)
    ms = memberships_of(uid)
    if len(ms) == 1 and ms[0][0] == str(def_id) and ms[0][3] == "1":
        ok("C 无效码降级落默认组", f"user={uid} memberships={ms}")
    else:
        fail("C 未按预期降级落默认组", f"user={uid} memberships={ms} 期望 group_id={def_id}")


def case_C2(def_id):
    print(f"\n{BOLD}[C2] 带【已满】invite_code 注册 → 方案A 降级落默认组，注册成功{RESET}")
    s, b, email = register(invite_code=FULL_INVITE)
    if s not in (200, 201) or b.get("code") != 0:
        return fail("C2 注册仍应成功（方案A，不阻断）", f"status={s} body={b}")
    uid = user_id_of(email)
    ms = memberships_of(uid)
    if len(ms) == 1 and ms[0][0] == str(def_id) and ms[0][3] == "1":
        ok("C2 已满码降级落默认组", f"user={uid} memberships={ms}")
    else:
        fail("C2 未按预期降级落默认组", f"user={uid} memberships={ms} 期望 group_id={def_id}")


def case_D():
    print(f"\n{BOLD}[D] 未配置默认组 + 不带 invite_code → 注册成功但不落任何组{RESET}")
    db_exec(f"UPDATE user_groups SET is_default=0 WHERE code='{DEF_CODE}';")
    rows, _ = db_query("SELECT COUNT(*) FROM user_groups WHERE is_default=1;")
    if rows and rows[0][0] != "0":
        db_exec(f"UPDATE user_groups SET is_default=1 WHERE code='{DEF_CODE}';")
        return fail("D 跳过：环境存在其他默认组", f"is_default 计数={rows[0][0]}")
    try:
        s, b, email = register(invite_code=None)
        if s not in (200, 201) or b.get("code") != 0:
            return fail("D 注册仍应成功", f"status={s} body={b}")
        uid = user_id_of(email)
        ms = memberships_of(uid)
        if len(ms) == 0:
            ok("D 无默认组时注册成功且不落组", f"user={uid} memberships=[]")
        else:
            fail("D 不应落任何组", f"user={uid} memberships={ms}")
    finally:
        db_exec(f"UPDATE user_groups SET is_default=1 WHERE code='{DEF_CODE}';")


def main():
    print(f"{BOLD}{CYAN}=== 注册落组 端到端验收 ==={RESET}")
    print(f"API_BASE={API_BASE}  MYSQL={MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
    s, b = http("GET", "/api/health")
    if s != 200:
        print(f"{RED}API 健康检查失败 status={s} body={b}，中止{RESET}")
        return
    def_id, inv_id = setup()
    print(f"前置数据就绪：默认组 id={def_id}，邀请组 id={inv_id}")
    try:
        case_A(def_id)
        case_B(inv_id)
        case_C(def_id)
        case_C2(def_id)
        case_D()
    finally:
        teardown()

    print(f"\n{BOLD}=== 结果汇总 ==={RESET}")
    for st, label, _ in results:
        mark = f"{GREEN}PASS{RESET}" if st == "PASS" else f"{RED}FAIL{RESET}"
        print(f"  [{mark}] {label}")
    print(f"\n{BOLD}通过 {GREEN}{passed}{RESET}{BOLD} / 失败 {RED}{failed}{RESET}{BOLD} / 共 {passed + failed}{RESET}")
    raise SystemExit(1 if failed else 0)


if __name__ == "__main__":
    main()

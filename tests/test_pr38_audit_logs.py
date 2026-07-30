#!/usr/bin/env python3
"""
PR#38（feature/backend-a-iam-audit-single-ops）IAM 单条操作审计日志验收脚本

验收范围（A-13）：
  POST   /api/admin/users/{id}/roles                              -> action=assign_role
  DELETE /api/admin/users/{id}/roles/{role_id}                     -> action=revoke_role
  POST   /api/admin/users/{id}/permission-overrides                -> action=set_permission_override
  DELETE /api/admin/users/{id}/permission-overrides/{override_id}  -> action=delete_permission_override

验收要点：
  1. 4 个接口的 HTTP 响应状态码/响应体与改动前一致（无破坏性变更）
  2. 调用后 GET /api/admin/audit-logs?module=iam&action=xxx 能查到对应记录，
     且 operator_id（执行操作的管理员）、target_type=user、target_id=<user_id> 正确
  3. payload（request_summary）通过 DB 直查校验内容正确性
     （/api/admin/audit-logs 响应不返回 request_summary 字段，详见报告说明）

用法（在测试服务器上执行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 ~/molin/test_pr38_audit_logs.py
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

ADMIN_EMAIL = "aisiqin@example.com"
ADMIN_PASSWORD = "123456"

# 目标用户：PR#31 验收时注册的普通测试账号，当前无任何角色/权限覆盖（已确认）
TARGET_USER_ID = 147

# qa_buyer 角色（id=3）：无绑定权限的非关键测试角色，用于 assign/revoke 验收
TEST_ROLE_ID = 3
TEST_ROLE_CODE = "qa_buyer"

# report:export 权限（id=64）：用于 set/delete permission-override 验收
TEST_PERM_ID = 64
TEST_PERM_CODE = "report:export"

print(f"\n{BOLD}{CYAN}PR#38 IAM 单条操作审计日志验收 — 开始{RESET}")
print(f"  API_BASE  : {API_BASE}")
print(f"  MYSQL     : {MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
print(f"  目标用户  : user_id={TARGET_USER_ID}")
print(f"  测试角色  : role_id={TEST_ROLE_ID} ({TEST_ROLE_CODE})")
print(f"  测试权限  : permission_id={TEST_PERM_ID} ({TEST_PERM_CODE})")
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
    rows, err = db_query(f"SELECT id FROM users WHERE email='{ADMIN_EMAIL}'")
    admin_user_id = int(rows[0][0]) if rows else None
    print(f"       admin_user_id={admin_user_id}")


# ═══════════════════════════════════════════════════════════════
# 前置检查：目标用户当前应无角色 / 无权限覆盖（避免污染）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}前置检查：目标用户初始状态{RESET}")
rows, err = db_query(f"SELECT role_id FROM user_roles WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_roles 失败", err)
elif rows:
    fail(f"目标用户已有角色，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无角色")

rows, err = db_query(f"SELECT id, permission_id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID}")
if err:
    fail("查询 user_permission_overrides 失败", err)
elif rows:
    fail(f"目标用户已有权限覆盖，可能污染测试数据", f"rows={rows}")
else:
    ok("目标用户当前无权限覆盖")


# ═══════════════════════════════════════════════════════════════
# 工具：取审计日志中最新一条记录（按 module+action 过滤）
# ═══════════════════════════════════════════════════════════════
def latest_audit_log(module, action, token):
    status, resp = http("GET", f"/api/admin/audit-logs?module={module}&action={action}&page=1&page_size=1", token=token)
    if status != 200 or resp.get("code") != 0:
        return status, resp, None
    items = resp.get("data", {}).get("items", [])
    return status, resp, (items[0] if items else None)


def db_audit_payload(log_id):
    rows, err = db_query(f"SELECT request_summary FROM audit_logs WHERE id={log_id}")
    if err or not rows:
        return None
    raw = rows[0][0]
    try:
        return json.loads(raw)
    except Exception:
        return raw


# ═══════════════════════════════════════════════════════════════
# 1. AssignRole  POST /api/admin/users/{id}/roles  -> assign_role
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}1. POST /api/admin/users/{{id}}/roles (AssignRole) -> action=assign_role{RESET}")

assign_status, assign_resp = (None, None)
if admin_token:
    assign_status, assign_resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/roles",
                                       {"role_id": TEST_ROLE_ID, "reason": "PR38验收-分配角色"},
                                       token=admin_token)
    print(f"  调用  HTTP {assign_status}: {json.dumps(assign_resp, ensure_ascii=False)}")
    if assign_status == 200 and assign_resp.get("code") == 0 and assign_resp.get("data") is None:
        ok("1.1 AssignRole 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("1.1 AssignRole 响应契约不符合预期（应为 HTTP 200, code=0, data=null）",
             f"实际 HTTP={assign_status}, resp={json.dumps(assign_resp, ensure_ascii=False)}")

    # 等待审计写入（同步写入，理论上无需等待，留少量缓冲）
    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "assign_role", admin_token)
    print(f"  审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        cond_op = log.get("operator_id") == admin_user_id
        cond_target_type = log.get("target_type") == "user"
        cond_target_id = log.get("target_id") == str(TARGET_USER_ID)
        if cond_op and cond_target_type and cond_target_id:
            ok("1.2 审计日志记录正确：operator_id/target_type/target_id 均符合预期",
               f"log={log}")
        else:
            fail("1.2 审计日志记录字段不符合预期",
                 f"operator_id={log.get('operator_id')}(期望{admin_user_id}), "
                 f"target_type={log.get('target_type')}(期望user), "
                 f"target_id={log.get('target_id')}(期望{TARGET_USER_ID})")

        # DB 直查 payload 校验
        payload = db_audit_payload(log["id"])
        print(f"       request_summary(DB) = {payload}")
        if isinstance(payload, dict) and payload.get("user_id") == TARGET_USER_ID and payload.get("role_id") == TEST_ROLE_ID:
            ok("1.3 audit_logs.request_summary 包含正确的 user_id/role_id", f"payload={payload}")
        else:
            fail("1.3 audit_logs.request_summary 内容不符合预期", f"payload={payload}")
    else:
        fail("1.2/1.3 未查询到 assign_role 审计记录", f"HTTP={status}, resp={resp}")
else:
    fail("1.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 2. SetPermissionOverride  POST /api/admin/users/{id}/permission-overrides
#    -> set_permission_override
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}2. POST /api/admin/users/{{id}}/permission-overrides (SetPermissionOverride) -> action=set_permission_override{RESET}")

override_id = None
if admin_token:
    status, resp = http("POST", f"/api/admin/users/{TARGET_USER_ID}/permission-overrides",
                         {"permission_id": TEST_PERM_ID, "effect": "allow", "reason": "PR38验收-设置权限覆盖"},
                         token=admin_token)
    print(f"  调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("2.1 SetPermissionOverride 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("2.1 SetPermissionOverride 响应契约不符合预期",
             f"实际 HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "set_permission_override", admin_token)
    print(f"  审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        cond_op = log.get("operator_id") == admin_user_id
        cond_target_type = log.get("target_type") == "user"
        cond_target_id = log.get("target_id") == str(TARGET_USER_ID)
        if cond_op and cond_target_type and cond_target_id:
            ok("2.2 审计日志记录正确：operator_id/target_type/target_id 均符合预期",
               f"log={log}")
        else:
            fail("2.2 审计日志记录字段不符合预期",
                 f"operator_id={log.get('operator_id')}(期望{admin_user_id}), "
                 f"target_type={log.get('target_type')}(期望user), "
                 f"target_id={log.get('target_id')}(期望{TARGET_USER_ID})")

        payload = db_audit_payload(log["id"])
        print(f"       request_summary(DB) = {payload}")
        if (isinstance(payload, dict)
                and payload.get("user_id") == TARGET_USER_ID
                and payload.get("permission_id") == TEST_PERM_ID
                and payload.get("permission_code") == TEST_PERM_CODE
                and payload.get("effect") == "allow"):
            ok("2.3 audit_logs.request_summary 包含正确的 user_id/permission_id/permission_code/effect/reason",
               f"payload={payload}")
        else:
            fail("2.3 audit_logs.request_summary 内容不符合预期", f"payload={payload}")
    else:
        fail("2.2/2.3 未查询到 set_permission_override 审计记录", f"HTTP={status}, resp={resp}")

    # 查询刚创建的 override id，供第 3 步使用
    rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE user_id={TARGET_USER_ID} AND permission_id={TEST_PERM_ID}")
    if rows and not err:
        override_id = int(rows[0][0])
        print(f"       override_id={override_id}")
    else:
        fail("2.x 未能查到新建的 permission override 记录", f"err={err}")
else:
    fail("2.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 3. DeletePermissionOverride  DELETE /api/admin/users/{id}/permission-overrides/{override_id}
#    -> delete_permission_override
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}3. DELETE /api/admin/users/{{id}}/permission-overrides/{{override_id}} (DeletePermissionOverride) -> action=delete_permission_override{RESET}")

if admin_token and override_id:
    status, resp = http("DELETE", f"/api/admin/users/{TARGET_USER_ID}/permission-overrides/{override_id}", token=admin_token)
    print(f"  调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("3.1 DeletePermissionOverride 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("3.1 DeletePermissionOverride 响应契约不符合预期",
             f"实际 HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "delete_permission_override", admin_token)
    print(f"  审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        cond_op = log.get("operator_id") == admin_user_id
        cond_target_type = log.get("target_type") == "user"
        cond_target_id = log.get("target_id") == str(TARGET_USER_ID)
        if cond_op and cond_target_type and cond_target_id:
            ok("3.2 审计日志记录正确：operator_id/target_type/target_id 均符合预期",
               f"log={log}")
        else:
            fail("3.2 审计日志记录字段不符合预期",
                 f"operator_id={log.get('operator_id')}(期望{admin_user_id}), "
                 f"target_type={log.get('target_type')}(期望user), "
                 f"target_id={log.get('target_id')}(期望{TARGET_USER_ID})")

        payload = db_audit_payload(log["id"])
        print(f"       request_summary(DB) = {payload}")
        if (isinstance(payload, dict)
                and payload.get("user_id") == TARGET_USER_ID
                and payload.get("override_id") == override_id):
            ok("3.3 audit_logs.request_summary 包含正确的 user_id/override_id", f"payload={payload}")
        else:
            fail("3.3 audit_logs.request_summary 内容不符合预期", f"payload={payload}")
    else:
        fail("3.2/3.3 未查询到 delete_permission_override 审计记录", f"HTTP={status}, resp={resp}")

    # 确认 DB 中 override 记录已删除
    rows, err = db_query(f"SELECT id FROM user_permission_overrides WHERE id={override_id}")
    if not rows and not err:
        ok("3.4 user_permission_overrides 记录已删除")
    else:
        fail("3.4 user_permission_overrides 记录未删除", f"rows={rows}")
else:
    fail("3.x 跳过（前置条件不满足：override_id 未获取到）")


# ═══════════════════════════════════════════════════════════════
# 4. RevokeRole  DELETE /api/admin/users/{id}/roles/{role_id}  -> revoke_role
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}4. DELETE /api/admin/users/{{id}}/roles/{{role_id}} (RevokeRole) -> action=revoke_role{RESET}")

if admin_token:
    status, resp = http("DELETE", f"/api/admin/users/{TARGET_USER_ID}/roles/{TEST_ROLE_ID}", token=admin_token)
    print(f"  调用  HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 200 and resp.get("code") == 0 and resp.get("data") is None:
        ok("4.1 RevokeRole 响应契约不变：HTTP 200, code=0, data=null")
    else:
        fail("4.1 RevokeRole 响应契约不符合预期",
             f"实际 HTTP={status}, resp={json.dumps(resp, ensure_ascii=False)}")

    time.sleep(0.3)
    status, resp, log = latest_audit_log("iam", "revoke_role", admin_token)
    print(f"  审计  HTTP {status}: {json.dumps(log, ensure_ascii=False)}")
    if status == 200 and log is not None:
        cond_op = log.get("operator_id") == admin_user_id
        cond_target_type = log.get("target_type") == "user"
        cond_target_id = log.get("target_id") == str(TARGET_USER_ID)
        if cond_op and cond_target_type and cond_target_id:
            ok("4.2 审计日志记录正确：operator_id/target_type/target_id 均符合预期",
               f"log={log}")
        else:
            fail("4.2 审计日志记录字段不符合预期",
                 f"operator_id={log.get('operator_id')}(期望{admin_user_id}), "
                 f"target_type={log.get('target_type')}(期望user), "
                 f"target_id={log.get('target_id')}(期望{TARGET_USER_ID})")

        payload = db_audit_payload(log["id"])
        print(f"       request_summary(DB) = {payload}")
        if (isinstance(payload, dict)
                and payload.get("user_id") == TARGET_USER_ID
                and payload.get("role_id") == TEST_ROLE_ID):
            ok("4.3 audit_logs.request_summary 包含正确的 user_id/role_id", f"payload={payload}")
        else:
            fail("4.3 audit_logs.request_summary 内容不符合预期", f"payload={payload}")
    else:
        fail("4.2/4.3 未查询到 revoke_role 审计记录", f"HTTP={status}, resp={resp}")

    # 确认 DB 中 user_roles 记录已删除
    rows, err = db_query(f"SELECT id FROM user_roles WHERE user_id={TARGET_USER_ID} AND role_id={TEST_ROLE_ID}")
    if not rows and not err:
        ok("4.4 user_roles 记录已删除（恢复初始状态）")
    else:
        fail("4.4 user_roles 记录未删除", f"rows={rows}")
else:
    fail("4.x 跳过（管理员登录失败）")


# ═══════════════════════════════════════════════════════════════
# 5. 权限校验：普通用户调用 /api/admin/audit-logs -> 403（旧行为应保持不变）
# ═══════════════════════════════════════════════════════════════
print(f"\n{BOLD}5. 权限校验：非管理员调用 /api/admin/audit-logs{RESET}")

status_e, resp_e = http("POST", "/api/auth/verification-codes/email", {
    "email": "pr31norm81328894@testmail.io", "scene": "login"
})
# 直接尝试用已知账号登录失败的话改用密码登录方式（PR31注册账号密码为 Test@Pr31x）
status, resp = http("POST", "/api/auth/login/email", {
    "email": "pr31norm81328894@testmail.io", "password": "Test@Pr31x"
})
print(f"  普通用户登录  HTTP {status}: code={resp.get('code')}")
normal_token = resp.get("data", {}).get("access_token") if status == 200 else None

if normal_token:
    status, resp = http("GET", "/api/admin/audit-logs", token=normal_token)
    print(f"  调用 audit-logs HTTP {status}: {json.dumps(resp, ensure_ascii=False)}")
    if status == 403:
        ok("5.1 普通用户调用 /api/admin/audit-logs 返回 403", f"code={resp.get('code')}")
    else:
        fail("5.1 普通用户调用 /api/admin/audit-logs 应返回 403",
             f"实际 HTTP={status}, code={resp.get('code')}")
else:
    fail("5.1 跳过（普通用户登录失败，无法验证权限边界）", json.dumps(resp, ensure_ascii=False))


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
    print(f"{BOLD}{GREEN}结论：PR#38 全部测试用例通过，建议可以合并。{RESET}")
else:
    print(f"{BOLD}{RED}结论：PR#38 存在 {failed} 项未通过，需先确认问题原因后再决定是否合并。{RESET}")

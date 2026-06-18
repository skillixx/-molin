#!/usr/bin/env python3
"""
后端丙 #168 新增能力接口验收。

被测契约（docs/frontend-api-reference.md §11.1b / §11.2 / §11.5）：
  1. 新端点 GET /api/memberships/{id}/benefits  (公开，无需登录)
       - 仅返回 status=active 的权益
       - 等级不存在 或 未上架(status!=active) -> 404 / code 40400 「会员等级不存在」
       - active 等级但无 active 权益 -> {items:[]}
  2. M2  GET /api/my/membership          (需登录) 内联 level_code/level_name
  3. M9  GET /api/admin/user-memberships (需 membership:view) 内联 level_code/level_name

纪律：仅操作本脚本自造的用户/等级/权益/会员；跑完按精确主键清理；不批量 LIKE 删除。

用法（测试服本机执行）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_membership_benefits_inline.py
"""

import json
import os
import time
import urllib.error
import urllib.request
import pymysql

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN="\033[92m"; RED="\033[91m"; YELLOW="\033[93m"; CYAN="\033[96m"; BOLD="\033[1m"; RESET="\033[0m"

passed = failed = 0
failures = []

def ok(label, detail=""):
    global passed; passed += 1
    print(f"  {GREEN}PASS{RESET} {label}" + (f"  | {detail}" if detail else ""))

def fail(label, detail=""):
    global failed; failed += 1
    failures.append((label, detail))
    print(f"  {RED}FAIL{RESET} {label}")
    if detail: print(f"       {RED}{detail}{RESET}")

def info(msg): print(f"  {YELLOW}--  {msg}{RESET}")

def section(t):
    print(f"\n{BOLD}{CYAN}{'='*66}{RESET}\n{BOLD}{CYAN}  {t}{RESET}\n{BOLD}{CYAN}{'='*66}{RESET}")

def request(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token: headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=15)
        return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try: return e.code, json.loads(e.read() or b"{}")
        except Exception: return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}

def get(p,t=None):           return request("GET",p,token=t)
def post(p,b=None,t=None):   return request("POST",p,b,t)
def patch(p,b=None,t=None):  return request("PATCH",p,b,t)

def data_of(body):
    if isinstance(body, dict) and isinstance(body.get("data"), dict): return body["data"]
    if isinstance(body, dict): return body.get("data") or {}
    return {}

def membership_of(body):
    d = body.get("data") if isinstance(body, dict) else None
    if isinstance(d, dict):
        if "membership" in d: return d["membership"]
        if "id" in d: return d
        if not d: return None
    return None

def mysql_query(sql, fetch=False, args=None):
    conn = pymysql.connect(host=MYSQL_HOST, port=MYSQL_PORT, user=MYSQL_USER,
                           password=MYSQL_PASS, database=MYSQL_DB, charset="utf8mb4")
    with conn:
        with conn.cursor() as cur:
            cur.execute(sql, args or ())
            rows = cur.fetchall() if fetch else None
            conn.commit()
            return rows

# ── 自造数据主键登记表（用于精确清理）──
created = {"users": [], "roles": [], "user_roles": [], "levels": [], "benefits": [], "memberships": []}

def grant_admin(user_id):
    mysql_query("INSERT IGNORE INTO roles (code, name, description) VALUES ('admin','超级管理员','内置')")
    mysql_query("INSERT IGNORE INTO user_roles (user_id, role_id) SELECT %s, id FROM roles WHERE code='admin' LIMIT 1", args=(user_id,))

_reg_seq = [0]
def _send_code(path, payload):
    for attempt in range(6):
        s, b = post(path, payload)
        if isinstance(b, dict) and b.get("code") == 42900:
            info(f"OTP 限流(42900)，等待 12s 重试 {path} ({attempt+1}/6)")
            time.sleep(12); continue
        return data_of(b).get("code", "")
    return ""

def register_user(tag):
    _reg_seq[0] += 1
    ts = int(time.time()*1000) + _reg_seq[0]
    email = f"mbi_{tag}_{ts}@testmail.io"
    phone = "139" + str(ts)[-8:]
    ecode = _send_code("/api/auth/verification-codes/email", {"email": email, "scene": "register"})
    pcode = _send_code("/api/auth/verification-codes/phone", {"phone": phone, "scene": "register"})
    if not ecode or not pcode:
        raise RuntimeError(f"未拿到验证码 email={ecode} phone={pcode}")
    s, b = post("/api/auth/register", {"email": email, "phone": phone, "password": "Test1234!",
                                       "email_code": ecode, "phone_code": pcode})
    token = data_of(b).get("access_token", "")
    if not token: raise RuntimeError(f"注册失败 {s} {b}")
    s2, b2 = get("/api/me", t=token)
    uid = data_of(b2).get("id")
    created["users"].append(uid)
    return uid, token, email

def create_level_active(admin_token, tag):
    code = f"mbi_{tag}_{int(time.time()*1000)%100000000}"
    s, b = post("/api/admin/membership-levels",
                {"level_code": code, "name": f"内联测试等级{tag}", "description": "#168 验收", "sort_order": 99},
                t=admin_token)
    lid = data_of(b).get("id")
    if not lid: raise RuntimeError(f"建等级失败 {s} {b}")
    created["levels"].append(lid)
    return lid, code, f"内联测试等级{tag}"

def create_benefit(admin_token, level_id, btype, value):
    s, b = post("/api/admin/membership-benefits",
                {"level_id": level_id, "benefit_type": btype, "benefit_value": value}, t=admin_token)
    bid = data_of(b).get("id")
    if not bid: raise RuntimeError(f"建权益失败 {s} {b}")
    created["benefits"].append(bid)
    return bid

def set_benefit_status(admin_token, bid, status):
    s, b = patch(f"/api/admin/membership-benefits/{bid}", {"status": status}, t=admin_token)
    return s, b

def set_level_status(admin_token, level_id, status):
    s, b = patch(f"/api/admin/membership-levels/{level_id}", {"status": status}, t=admin_token)
    return s, b

def open_membership(admin_token, user_id, level_id, duration_days=30):
    s, b = post("/api/admin/user-memberships",
                {"user_id": user_id, "level_id": level_id, "duration_days": duration_days}, t=admin_token)
    return s, b

def remember_membership(user_id, level_id):
    rows = mysql_query("SELECT id FROM user_memberships WHERE user_id=%s AND level_id=%s",
                       fetch=True, args=(user_id, level_id))
    for r in rows:
        if r[0] not in created["memberships"]:
            created["memberships"].append(r[0])


def main():
    section("准备：管理员 / 普通用户 / 目标用户")
    admin_id, admin_token, _ = register_user("admin")
    grant_admin(admin_id)
    user_id, user_token, _   = register_user("user")    # 目标会员用户 + 充当无权限用户
    info(f"admin_id={admin_id} user_id={user_id}")

    s, b = get("/api/admin/membership-levels", t=admin_token)
    if s == 200: ok("管理员 token self-check 可访问 /api/admin/membership-levels")
    else:
        fail("管理员 self-check 失败，终止", f"status={s} {str(b)[:160]}"); return

    # ================================================================
    section("§11.1b  GET /api/memberships/{id}/benefits（公开端点）")

    # 等级 A：active 等级，含 1 active + 1 inactive 权益
    Lid_a, code_a, name_a = create_level_active(admin_token, "benefA")
    bid_active   = create_benefit(admin_token, Lid_a, "discount", "{\"rate\":0.8}")
    bid_inactive = create_benefit(admin_token, Lid_a, "gift", "{\"item\":\"mug\"}")
    set_benefit_status(admin_token, bid_inactive, "inactive")
    info(f"等级A id={Lid_a} active权益id={bid_active} inactive权益id={bid_inactive}")

    # 用例 (a) 无 token 也能访问（公开）
    s, b = get(f"/api/memberships/{Lid_a}/benefits")  # 不带 token
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:300]}"
    if s == 200 and b.get("code") == 0:
        ok("(a) 公开访问无需 token -> HTTP 200 code 0", detail)
    else:
        fail("(a) 公开访问应 HTTP 200 code 0", detail)

    # 用例 (b) 仅返回 active 权益
    items = data_of(b).get("items", [])
    ids = [it.get("id") for it in items]
    if ids == [bid_active]:
        ok("(b) 仅返回 active 权益（恰好 1 条且 id 匹配）", f"items_ids={ids}")
    else:
        fail("(b) 应仅返回 active 权益那一条", f"期望=[{bid_active}] 实际={ids} | {detail}")
    if items:
        it = items[0]
        need = {"id","level_id","benefit_type","benefit_value","status","created_at","updated_at"}
        missing = need - set(it.keys())
        if not missing: ok("(b) 权益对象字段齐全", f"keys={sorted(it.keys())}")
        else: fail("(b) 权益对象缺字段", f"缺={missing} 实有={sorted(it.keys())}")
        checks = (it.get("level_id")==Lid_a, it.get("status")=="active", it.get("benefit_type")=="discount")
        if all(checks): ok("(b) 权益字段值正确（level_id/status/benefit_type）", f"{it}")
        else: fail("(b) 权益字段值不符", f"{it}")

    # 用例 (c) 不存在的 level_id -> 404 / 40400
    bogus = 999999999
    s, b = get(f"/api/memberships/{bogus}/benefits")
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:200]}"
    if s == 404 and b.get("code") == 40400 and "会员等级不存在" in str(b.get("message","")):
        ok("(c) 不存在 level_id -> 404 / 40400 / 会员等级不存在", detail)
    else:
        fail("(c) 不存在 level_id 应 404/40400/会员等级不存在", detail)

    # 用例 (d) inactive 等级 -> 404 / 40400（防泄露）
    Lid_d, code_d, name_d = create_level_active(admin_token, "benefD")
    bid_d = create_benefit(admin_token, Lid_d, "discount", "{\"rate\":0.5}")  # 即使有 active 权益
    set_level_status(admin_token, Lid_d, "inactive")
    s, b = get(f"/api/memberships/{Lid_d}/benefits")
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:200]}"
    if s == 404 and b.get("code") == 40400:
        ok("(d) inactive 等级 -> 404 / 40400（不泄露未上架等级）", detail)
    else:
        fail("(d) inactive 等级应 404/40400", detail)

    # 用例 (e) active 等级但无 active 权益 -> items:[]
    Lid_e, code_e, name_e = create_level_active(admin_token, "benefE")  # 不建任何权益
    s, b = get(f"/api/memberships/{Lid_e}/benefits")
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:200]}"
    items_e = data_of(b).get("items", None)
    if s == 200 and b.get("code") == 0 and items_e == []:
        ok("(e) active 等级无 active 权益 -> items:[]", detail)
    else:
        fail("(e) 应 HTTP 200 items:[]", detail)

    # 补充：active 等级、全部权益均 inactive -> items:[]（佐证过滤而非按等级返回）
    Lid_f, _, _ = create_level_active(admin_token, "benefF")
    bid_f = create_benefit(admin_token, Lid_f, "gift", "{\"x\":1}")
    set_benefit_status(admin_token, bid_f, "inactive")
    s, b = get(f"/api/memberships/{Lid_f}/benefits")
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:200]}"
    if s == 200 and data_of(b).get("items") == []:
        ok("(补) active 等级但权益全 inactive -> items:[]", detail)
    else:
        fail("(补) 应 items:[]", detail)

    # ================================================================
    section("§11.2  GET /api/my/membership 内联 level_code/level_name")

    # 给 user 开通等级A 会员
    s, b = open_membership(admin_token, user_id, Lid_a, 30)
    if s != 200:
        fail("准备：为 user 开通会员失败", f"HTTP {s} {b}")
    else:
        remember_membership(user_id, Lid_a)

    # 无会员用户断言 null（用 admin 自己，未开通会员）
    s, b = get("/api/my/membership", t=admin_token)
    m = membership_of(b)
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:200]}"
    if s == 200 and m is None:
        ok("M2 无会员用户 -> membership:null", detail)
    else:
        fail("M2 无会员用户应 membership:null", detail)

    # 有会员用户断言内联字段
    s, b = get("/api/my/membership", t=user_token)
    m = membership_of(b)
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:300]}"
    if s == 200 and isinstance(m, dict):
        ok("M2 有会员 -> 返回会员对象", detail)
        if m.get("level_id") == Lid_a:
            ok("M2 保留 level_id", f"level_id={m.get('level_id')}")
        else:
            fail("M2 level_id 应保留且匹配", f"期望={Lid_a} 实际={m.get('level_id')}")
        if m.get("level_code") == code_a and m.get("level_name") == name_a:
            ok("M2 新增 level_code/level_name 正确", f"code={m.get('level_code')} name={m.get('level_name')}")
        else:
            fail("M2 level_code/level_name 应与等级一致",
                 f"期望 code={code_a} name={name_a} 实际 code={m.get('level_code')} name={m.get('level_name')}")
        need = {"id","user_id","asset_id","status","started_at","expires_at","level_id"}
        missing = need - set(m.keys())
        if not missing: ok("M2 原字段全保留", f"keys={sorted(m.keys())}")
        else: fail("M2 原字段缺失", f"缺={missing}")
    else:
        fail("M2 有会员应返回会员对象", detail)

    # ================================================================
    section("§11.5  GET /api/admin/user-memberships 内联 + 批量映射（无 N+1 佐证）")

    # 鉴权：无 token -> 401
    s, b = get(f"/api/admin/user-memberships?user_id={user_id}")
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:160]}"
    if s == 401: ok("M9 无 token -> 401", detail)
    else: fail("M9 无 token 应 401", detail)

    # 鉴权：无 membership:view 普通用户 -> 403/40003
    s, b = get(f"/api/admin/user-memberships?user_id={user_id}", t=user_token)
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:160]}"
    if s == 403 and b.get("code") == 40003:
        ok("M9 无权限普通用户 -> 403 / 40003", detail)
    else:
        fail("M9 无权限应 403/40003", detail)

    # 关键用例：同一用户、同一页含两个不同 level_id 的会员，断言各自等级名不串味
    Lid_g, code_g, name_g = create_level_active(admin_token, "m9G")
    s, b = open_membership(admin_token, user_id, Lid_g, 60)
    if s == 200: remember_membership(user_id, Lid_g)
    else: fail("准备：为 user 开通第二等级失败", f"HTTP {s} {b}")

    s, b = get(f"/api/admin/user-memberships?user_id={user_id}&page=1&page_size=20", t=admin_token)
    detail = f"HTTP {s} {json.dumps(b,ensure_ascii=False)[:500]}"
    d = data_of(b)
    items = d.get("items", [])
    if s == 200 and all(k in d for k in ("items","page","page_size","total")):
        ok("M9 扁平分页结构 {items,page,page_size,total}", f"page={d.get('page')} page_size={d.get('page_size')} total={d.get('total')}")
    else:
        fail("M9 应为扁平分页结构", detail)

    by_level = {it.get("level_id"): it for it in items}
    # 校验等级A
    if Lid_a in by_level:
        it = by_level[Lid_a]
        if it.get("level_code")==code_a and it.get("level_name")==name_a:
            ok("M9 同页 level_id=A 等级名正确不串味", f"code={it.get('level_code')} name={it.get('level_name')}")
        else:
            fail("M9 level_id=A 等级名错", f"期望 {code_a}/{name_a} 实际 {it.get('level_code')}/{it.get('level_name')}")
    else:
        fail("M9 列表缺等级A记录", f"levels in items={list(by_level.keys())}")
    # 校验等级G
    if Lid_g in by_level:
        it = by_level[Lid_g]
        if it.get("level_code")==code_g and it.get("level_name")==name_g:
            ok("M9 同页 level_id=G 等级名正确不串味", f"code={it.get('level_code')} name={it.get('level_name')}")
        else:
            fail("M9 level_id=G 等级名错", f"期望 {code_g}/{name_g} 实际 {it.get('level_code')}/{it.get('level_name')}")
    else:
        fail("M9 列表缺等级G记录", f"levels in items={list(by_level.keys())}")

    # items 含 created_at/updated_at 且原字段全保留
    if items:
        it = items[0]
        need = {"id","user_id","level_id","level_code","level_name","asset_id","status",
                "started_at","expires_at","created_at","updated_at"}
        missing = need - set(it.keys())
        if not missing: ok("M9 items 含 created_at/updated_at + 内联字段，原字段全保留", f"keys={sorted(it.keys())}")
        else: fail("M9 items 字段缺失", f"缺={missing} 实有={sorted(it.keys())}")

    # 异常等级佐证：直接 DB 造一条指向不存在 level_id 的会员，断言等级名留空但不报错
    section("§11.5 异常等级：level_id 无对应等级 -> 等级名留空但不报错")
    BAD_LEVEL = 988888888
    mysql_query("INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at) "
                "VALUES (%s,%s,'active',NOW(),NULL)", args=(user_id, BAD_LEVEL))
    row = mysql_query("SELECT id FROM user_memberships WHERE user_id=%s AND level_id=%s",
                      fetch=True, args=(user_id, BAD_LEVEL))
    bad_mid = row[0][0]
    created["memberships"].append(bad_mid)
    s, b = get(f"/api/admin/user-memberships?user_id={user_id}&page=1&page_size=20", t=admin_token)
    d = data_of(b); items = d.get("items", [])
    bad_it = next((it for it in items if it.get("level_id")==BAD_LEVEL), None)
    detail = f"HTTP {s} bad_it={json.dumps(bad_it,ensure_ascii=False) if bad_it else None}"
    if s == 200 and bad_it is not None and bad_it.get("level_code") in ("", None) and bad_it.get("level_name") in ("", None):
        ok("M9 缺失等级 -> 等级名留空且接口不报错", detail)
    else:
        fail("M9 缺失等级应留空且不报错", detail)

    # ================================================================
    section("数据清理（按精确主键）")
    # 顺序：会员 -> 权益 -> user_roles -> users -> 等级
    for mid in created["memberships"]:
        mysql_query("DELETE FROM user_memberships WHERE id=%s", args=(mid,))
    for bid in created["benefits"]:
        mysql_query("DELETE FROM membership_benefits WHERE id=%s", args=(bid,))
    for uid in created["users"]:
        mysql_query("DELETE FROM user_roles WHERE user_id=%s", args=(uid,))
        mysql_query("DELETE FROM user_sessions WHERE user_id=%s", args=(uid,))
        mysql_query("DELETE FROM users WHERE id=%s", args=(uid,))
    for lid in created["levels"]:
        mysql_query("DELETE FROM membership_levels WHERE id=%s", args=(lid,))

    # 核对自造数据计数=0
    def count(sql, args):
        return mysql_query(sql, fetch=True, args=args)[0][0]
    leftovers = {}
    if created["memberships"]:
        ids = tuple(created["memberships"])
        ph = ",".join(["%s"]*len(ids))
        leftovers["user_memberships"] = count(f"SELECT COUNT(*) FROM user_memberships WHERE id IN ({ph})", ids)
    if created["benefits"]:
        ids = tuple(created["benefits"]); ph = ",".join(["%s"]*len(ids))
        leftovers["membership_benefits"] = count(f"SELECT COUNT(*) FROM membership_benefits WHERE id IN ({ph})", ids)
    if created["users"]:
        ids = tuple(created["users"]); ph = ",".join(["%s"]*len(ids))
        leftovers["users"] = count(f"SELECT COUNT(*) FROM users WHERE id IN ({ph})", ids)
    if created["levels"]:
        ids = tuple(created["levels"]); ph = ",".join(["%s"]*len(ids))
        leftovers["membership_levels"] = count(f"SELECT COUNT(*) FROM membership_levels WHERE id IN ({ph})", ids)
    info(f"自造主键: {created}")
    if all(v == 0 for v in leftovers.values()):
        ok("清理核对：所有自造数据计数=0", f"{leftovers}")
    else:
        fail("清理核对：仍有残留", f"{leftovers}")

    # ================================================================
    section("结果汇总")
    total = passed + failed
    print(f"  总用例 {total}  {GREEN}通过 {passed}{RESET}  {RED}失败 {failed}{RESET}")
    if failures:
        print(f"\n{BOLD}{RED}失败明细：{RESET}")
        for lab, det in failures:
            print(f"  - {lab}\n      {det}")
    print()

if __name__ == "__main__":
    main()

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
PR#270 验收测试 — 商品访问规则 / 价格回显 GET 接口

被测接口（只读、非分页、全量返回 data={items:[...]}）：
  GET /api/admin/products/{id}/access  —— 回显角色访问规则，需 product:view
  GET /api/admin/products/{id}/prices  —— 回显跨套餐全部价格，需 product:view

契约（SSOT：docs/frontend-api-reference.md §5.3 / docs/full-api-design.md §4.10/§4.11）：
  access item: id, product_id, role_id, can_view, can_buy, can_use, created_at, updated_at
  price  item: id, product_plan_id, role_id, membership_level_id, price_amount(字符串),
               currency, created_at, updated_at
  - 默认价：role_id 与 membership_level_id 必须为 null（键存在且为 null，PR#270 P2 修复点）
  - 无配置时 items=[]（非 null）

用例：
  1 鉴权：无 token→401；普通用户(无 product:view)→403；管理员→200
  2 access 写入→回显闭环（role_id + 三个布尔位一致）
  3 prices 写入→回显闭环（默认价/角色价/会员价；默认价层级字段 null；price_amount 字符串）
  4 空集：未配置商品 GET→items=[]
  5 排序确定性（弱断言）：access 按 role_id、prices 按 product_plan_id 有序
  6 不存在的商品 id：记录实际行为

执行（测试服上）：
  API_BASE=http://localhost:8080 MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 ~/molin/test_pr270_product_access_prices_echo.py
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

G, R, Y, C, B, X = "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m"
passed = failed = 0
FAILS = []


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {G}[PASS]{X} {label}" + (f"\n         {detail}" if detail else ""))


def fail(label, detail=""):
    global failed
    failed += 1
    FAILS.append(label + (f" — {detail}" if detail else ""))
    print(f"  {R}[FAIL]{X} {label}" + (f"\n         {R}{detail}{X}" if detail else ""))


def info(msg):
    print(f"  {C}[INFO]{X} {msg}")


def http(method, path, body=None, token=None, extra=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if extra:
        headers.update(extra)
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
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}",
           f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql]
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    if r.returncode != 0:
        return None, r.stderr.strip()
    rows = [ln.split("\t") for ln in r.stdout.strip().split("\n") if ln]
    return rows, None


def db_exec(sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}",
           f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql]
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    return r.returncode == 0, (r.stderr.strip() if r.returncode != 0 else None)


def register(email, phone, password, username=None):
    """通过 DB 写入 register OTP 后调用注册接口，返回 (user_id, access_token)。"""
    otp = "888888"
    otp_sha = hashlib.sha256(otp.encode()).hexdigest()
    exp = "DATE_ADD(NOW(), INTERVAL 490 MINUTE)"
    for tt, tv in (("phone", phone), ("email", email)):
        db_exec(f"DELETE FROM verification_codes WHERE target_value='{tv}' AND scene='register'")
        db_exec(f"INSERT INTO verification_codes (target_type,target_value,code,scene,expires_at) "
                f"VALUES ('{tt}','{tv}','{otp_sha}','register',{exp})")
    body = {"email": email, "phone": phone, "password": password,
            "phone_code": otp, "email_code": otp}
    if username:
        body["username"] = username
    s, resp = http("POST", "/api/auth/register", body)
    if s in (200, 201) and resp.get("code") == 0:
        token = resp.get("data", {}).get("access_token")
        rows, _ = db_query(f"SELECT id FROM users WHERE email='{email}'")
        return (int(rows[0][0]) if rows else None), token
    print(f"    {R}注册失败 HTTP={s} {json.dumps(resp, ensure_ascii=False)[:200]}{X}")
    return None, None


TS = int(time.time())
print(f"{B}{C}PR#270 验收测试 — 商品 access/prices 回显 GET 接口{X}")
print(f"  API_BASE={API_BASE}  TS={TS}\n")

# ── 前置：测试角色（product:view/edit/create）──────────────────────────────
print(f"{B}前置：创建测试角色并赋 product:view/edit/create 权限{X}")
ROLE_CODE = f"pr270_role_{TS}"
db_exec(f"INSERT IGNORE INTO roles (code,name,description) VALUES "
        f"('{ROLE_CODE}','PR270测试角色','PR#270 access/prices 回显验收')")
rows, err = db_query(f"SELECT id FROM roles WHERE code='{ROLE_CODE}'")
if not rows:
    print(f"  {R}测试角色创建失败 err={err}，中止{X}")
    raise SystemExit(1)
admin_role_id = int(rows[0][0])
for code in ("product:view", "product:edit", "product:create"):
    db_exec(f"INSERT IGNORE INTO role_permissions (role_id,permission_id) "
            f"SELECT {admin_role_id}, p.id FROM permissions p WHERE p.code='{code}'")
rows, _ = db_query(f"SELECT p.code FROM role_permissions rp JOIN permissions p "
                   f"ON p.id=rp.permission_id WHERE rp.role_id={admin_role_id}")
granted = {r[0] for r in rows} if rows else set()
need = {"product:view", "product:edit", "product:create"}
if need <= granted:
    ok("测试角色已具备 product:view/edit/create", f"granted={sorted(granted & need)}")
else:
    fail("测试角色缺少权限码", f"missing={need - granted}")

# ── 前置：注册管理员 + 普通用户 ────────────────────────────────────────────
print(f"\n{B}前置：注册账号{X}")
adm_id, adm_token = register(f"pr270adm{TS}@testmail.io", f"196{TS % 100000000:08d}",
                             "Test@Pr270Adm", f"pr270adm{TS}")
if not adm_id:
    raise SystemExit(1)
db_exec(f"INSERT IGNORE INTO user_roles (user_id,role_id) VALUES ({adm_id},{admin_role_id})")
info(f"管理员 user_id={adm_id}，已绑定测试角色")

usr_id, usr_token = register(f"pr270usr{TS}@testmail.io", f"197{TS % 100000000:08d}",
                             "Test@Pr270Usr", f"pr270usr{TS}")
if not usr_id:
    raise SystemExit(1)
# 校验普通用户确实没有 product:view（默认注册角色）
rows, _ = db_query(
    f"SELECT p.code FROM user_roles ur JOIN role_permissions rp ON rp.role_id=ur.role_id "
    f"JOIN permissions p ON p.id=rp.permission_id "
    f"WHERE ur.user_id={usr_id} AND p.code='product:view'")
if rows:
    info(f"注意：普通用户默认角色已含 product:view（403 用例将据此判断）")
else:
    info(f"普通用户 user_id={usr_id}，确认无 product:view 权限")

# ── 前置：创建商品 + 套餐 ──────────────────────────────────────────────────
print(f"\n{B}前置：创建商品 + 套餐{X}")
s, b = http("POST", "/api/admin/products", {
    "product_type": "app", "product_code": f"pr270_prod_{TS}",
    "name": f"PR270商品{TS}", "description": "access/prices 回显验收", "status": "active",
}, token=adm_token)
prod_id = b.get("data", {}).get("id")
if s == 201 and prod_id:
    ok("创建商品成功", f"product_id={prod_id}")
else:
    fail("创建商品失败", f"HTTP={s} resp={json.dumps(b, ensure_ascii=False)[:200]}")
    raise SystemExit(1)

# 创建第二个商品（用于空集用例）
s, b2 = http("POST", "/api/admin/products", {
    "product_type": "app", "product_code": f"pr270_empty_{TS}",
    "name": f"PR270空商品{TS}", "status": "active",
}, token=adm_token)
empty_prod_id = b2.get("data", {}).get("id")
info(f"空集用商品 product_id={empty_prod_id}")

# 创建两个套餐
plan_ids = []
for i in range(2):
    s, b = http("POST", f"/api/admin/products/{prod_id}/plans", {
        "plan_code": f"pr270_plan{i}_{TS}", "name": f"套餐{i}",
        "billing_type": "one_time", "status": "active",
    }, token=adm_token)
    pid = b.get("data", {}).get("id")
    if s == 201 and pid:
        plan_ids.append(pid)
    else:
        fail(f"创建套餐{i}失败", f"HTTP={s} resp={json.dumps(b, ensure_ascii=False)[:200]}")
if len(plan_ids) == 2:
    ok("创建两个套餐成功", f"plan_ids={plan_ids}")

# 取一个真实 membership_level_id 用于会员价
rows, _ = db_query("SELECT id FROM membership_levels ORDER BY id LIMIT 1")
mlevel_id = int(rows[0][0]) if rows else None
info(f"会员等级 membership_level_id={mlevel_id}")

# 第二个角色（用于 access 多条 + 角色价）
ROLE2_CODE = f"pr270_role2_{TS}"
db_exec(f"INSERT IGNORE INTO roles (code,name,description) VALUES "
        f"('{ROLE2_CODE}','PR270测试角色2','access 第二条')")
rows, _ = db_query(f"SELECT id FROM roles WHERE code='{ROLE2_CODE}'")
role2_id = int(rows[0][0]) if rows else None
info(f"第二角色 role2_id={role2_id}；第一角色 role_id={admin_role_id}")

# ════════════════════════════════════════════════════════════════════════════
# 用例 1：鉴权
# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}用例 1：鉴权{X}")
for ep in ("access", "prices"):
    s, _ = http("GET", f"/api/admin/products/{prod_id}/{ep}")
    (ok if s == 401 else fail)(f"1.1 无 token GET .../{ep} → 401", f"实际 HTTP={s}")
    s, _ = http("GET", f"/api/admin/products/{prod_id}/{ep}", token=usr_token)
    (ok if s == 403 else fail)(f"1.2 普通用户 GET .../{ep} → 403", f"实际 HTTP={s}")
    s, body = http("GET", f"/api/admin/products/{prod_id}/{ep}", token=adm_token)
    (ok if (s == 200 and body.get("code") == 0) else fail)(
        f"1.3 管理员 GET .../{ep} → 200", f"实际 HTTP={s} code={body.get('code')}")

# ════════════════════════════════════════════════════════════════════════════
# 用例 4（先于 2/3，因写入会改变集合）：空集
# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}用例 4：未配置商品 GET → items=[]{X}")
if empty_prod_id:
    for ep in ("access", "prices"):
        s, body = http("GET", f"/api/admin/products/{empty_prod_id}/{ep}", token=adm_token)
        data = body.get("data") if isinstance(body, dict) else None
        items = data.get("items") if isinstance(data, dict) else "MISSING"
        if s == 200 and isinstance(items, list) and len(items) == 0:
            ok(f"4 空商品 .../{ep} items=[]（空数组非 null）")
        else:
            fail(f"4 空商品 .../{ep} 未返回空数组",
                 f"HTTP={s} data={json.dumps(data, ensure_ascii=False)[:200]}")
else:
    fail("4 空集用例无法执行", "空商品创建失败")

# ════════════════════════════════════════════════════════════════════════════
# 用例 2：access 写入 → 回显闭环
# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}用例 2：access 写入→回显闭环{X}")
# 写入两条规则（不同 role_id、不同布尔组合）
access_in = [
    {"role_id": admin_role_id, "can_view": True,  "can_buy": True,  "can_use": False},
    {"role_id": role2_id,      "can_view": True,  "can_buy": False, "can_use": True},
]
s, b = http("PATCH", f"/api/admin/products/{prod_id}/access",
            {"items": access_in}, token=adm_token)
if s == 200 and b.get("code") == 0:
    ok("2.1 PATCH .../access 写入两条规则 → 200")
else:
    fail("2.1 PATCH .../access 写入失败", f"HTTP={s} resp={json.dumps(b, ensure_ascii=False)[:200]}")

s, b = http("GET", f"/api/admin/products/{prod_id}/access", token=adm_token)
items = (b.get("data") or {}).get("items") if isinstance(b, dict) else None
if not isinstance(items, list):
    fail("2.2 GET .../access 回显结构异常", f"HTTP={s} data={json.dumps(b.get('data'), ensure_ascii=False)[:200]}")
else:
    # 字段完整性
    AFIELDS = {"id", "product_id", "role_id", "can_view", "can_buy", "can_use", "created_at", "updated_at"}
    if items:
        miss = AFIELDS - set(items[0].keys())
        (ok if not miss else fail)("2.3 access item 字段完整", f"keys={sorted(items[0].keys())}" if not miss else f"缺失={miss}")
    # 写入一致性
    got = {it["role_id"]: (it.get("can_view"), it.get("can_buy"), it.get("can_use"),
                           it.get("product_id")) for it in items}
    all_match = True
    for a in access_in:
        exp = (a["can_view"], a["can_buy"], a["can_use"])
        g = got.get(a["role_id"])
        if not g or g[:3] != exp:
            all_match = False
            fail("2.4 access 回显与写入一致", f"role_id={a['role_id']} 期望={exp} 实际={g}")
        elif g[3] != prod_id:
            all_match = False
            fail("2.4 access product_id 不匹配", f"role_id={a['role_id']} product_id={g[3]} 期望={prod_id}")
    if all_match and len(items) == len(access_in):
        ok("2.4 access 回显 role_id+三个布尔位与写入完全一致", f"共 {len(items)} 条")
    elif all_match and len(items) != len(access_in):
        fail("2.4 access 回显条数不符", f"写入 {len(access_in)} 实际 {len(items)}")
    # 排序（弱）
    rids = [it["role_id"] for it in items]
    (ok if rids == sorted(rids) else info)(
        f"5.1 access 按 role_id 升序（弱断言）", f"role_ids={rids}")

# ════════════════════════════════════════════════════════════════════════════
# 用例 3：prices 写入 → 回显闭环
# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}用例 3：prices 写入→回显闭环（默认/角色/会员价）{X}")
price_in = [
    {"product_plan_id": plan_ids[0], "price_amount": "50.000000", "currency": "CNY"},                       # 默认价
    {"product_plan_id": plan_ids[0], "role_id": admin_role_id, "price_amount": "40.000000", "currency": "CNY"},  # 角色价
    {"product_plan_id": plan_ids[1], "membership_level_id": mlevel_id, "price_amount": "30.000000", "currency": "CNY"},  # 会员价（另一套餐）
]
s, b = http("PATCH", f"/api/admin/products/{prod_id}/prices", {"items": price_in}, token=adm_token)
if s == 200 and b.get("code") == 0:
    ok("3.1 PATCH .../prices 写入默认/角色/会员价 → 200")
else:
    fail("3.1 PATCH .../prices 写入失败", f"HTTP={s} resp={json.dumps(b, ensure_ascii=False)[:200]}")

s, b = http("GET", f"/api/admin/products/{prod_id}/prices", token=adm_token)
data = b.get("data") if isinstance(b, dict) else None
items = data.get("items") if isinstance(data, dict) else None
print(f"    {C}回显 raw data={json.dumps(data, ensure_ascii=False)[:600]}{X}")
if not isinstance(items, list):
    fail("3.2 GET .../prices 回显结构异常", f"HTTP={s} data={json.dumps(data, ensure_ascii=False)[:200]}")
else:
    # 跨套餐全部返回
    if len(items) == len(price_in):
        ok("3.2 prices 跨套餐全量返回", f"共 {len(items)} 条（写入 {len(price_in)} 条）")
    else:
        fail("3.2 prices 返回条数不符", f"写入 {len(price_in)} 实际 {len(items)}")

    PFIELDS = {"id", "product_plan_id", "role_id", "membership_level_id",
               "price_amount", "currency", "created_at", "updated_at"}
    if items:
        miss = PFIELDS - set(items[0].keys())
        (ok if not miss else fail)("3.3 price item 字段完整", f"keys={sorted(items[0].keys())}" if not miss else f"缺失={miss}")

    # price_amount 为字符串
    bad_type = [it for it in items if not isinstance(it.get("price_amount"), str)]
    (ok if not bad_type else fail)(
        "3.4 price_amount 为字符串", "全部字符串" if not bad_type else f"非字符串项={bad_type[:1]}")

    # 找默认价（plan0 + price 50）→ role_id 与 membership_level_id 必须为 null（键存在且 null）
    def find(plan, amt):
        for it in items:
            if it.get("product_plan_id") == plan and str(it.get("price_amount")).startswith(amt):
                return it
        return None

    default_row = find(plan_ids[0], "50")
    if default_row is None:
        fail("3.5 默认价行未找到", f"items={json.dumps(items, ensure_ascii=False)[:300]}")
    else:
        keys = default_row.keys()
        cond_keys = ("role_id" in keys) and ("membership_level_id" in keys)
        cond_null = default_row.get("role_id") is None and default_row.get("membership_level_id") is None
        if cond_keys and cond_null:
            ok("3.5 [P2修复] 默认价 role_id/membership_level_id 键存在且为 null",
               f"row={json.dumps(default_row, ensure_ascii=False)[:200]}")
        elif not cond_keys:
            fail("3.5 [P2修复失败] 默认价缺失 role_id/membership_level_id 键（omitempty 未去除）",
                 f"keys={sorted(keys)}")
        else:
            fail("3.5 [P2修复] 默认价层级字段非 null", f"role_id={default_row.get('role_id')} mlevel={default_row.get('membership_level_id')}")

    # 角色价：role_id 非空、membership_level_id 为 null
    role_row = find(plan_ids[0], "40")
    if role_row and role_row.get("role_id") == admin_role_id and role_row.get("membership_level_id") is None:
        ok("3.6 角色价 role_id 正确且 membership_level_id=null", f"role_id={role_row.get('role_id')}")
    else:
        fail("3.6 角色价层级字段不符", f"row={json.dumps(role_row, ensure_ascii=False)[:200] if role_row else None}")

    # 会员价：membership_level_id 非空、role_id 为 null
    mem_row = find(plan_ids[1], "30")
    if mem_row and mem_row.get("membership_level_id") == mlevel_id and mem_row.get("role_id") is None:
        ok("3.7 会员价 membership_level_id 正确且 role_id=null", f"mlevel={mem_row.get('membership_level_id')}")
    else:
        fail("3.7 会员价层级字段不符", f"row={json.dumps(mem_row, ensure_ascii=False)[:200] if mem_row else None}")

    # 排序（弱）：按 product_plan_id
    pids = [it.get("product_plan_id") for it in items]
    (ok if pids == sorted(pids) else info)(
        "5.2 prices 按 product_plan_id 升序（弱断言）", f"plan_ids={pids}")

# ════════════════════════════════════════════════════════════════════════════
# 用例 6：不存在的商品 id
# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}用例 6：不存在的商品 id 行为记录{X}")
ghost = 999999999
for ep in ("access", "prices"):
    s, body = http("GET", f"/api/admin/products/{ghost}/{ep}", token=adm_token)
    data = body.get("data") if isinstance(body, dict) else None
    items = data.get("items") if isinstance(data, dict) else None
    if s == 404:
        info(f"6 GET 不存在商品 .../{ep} → HTTP 404（按文档约定之一）")
        ok(f"6 不存在商品 .../{ep} 行为已记录（404）")
    elif s == 200 and isinstance(items, list) and len(items) == 0:
        info(f"6 GET 不存在商品 .../{ep} → HTTP 200 items=[]（无存在性校验，按文档约定之一）")
        ok(f"6 不存在商品 .../{ep} 行为已记录（200 空集）")
    else:
        fail(f"6 不存在商品 .../{ep} 行为异常",
             f"HTTP={s} data={json.dumps(data, ensure_ascii=False)[:200]}")

# ════════════════════════════════════════════════════════════════════════════
print(f"\n{B}{'='*64}{X}")
print(f"{B}结果：{G}{passed} PASS{X} / {R}{failed} FAIL{X}")
if FAILS:
    print(f"{R}失败项：{X}")
    for f_ in FAILS:
        print(f"  - {f_}")
else:
    print(f"{G}全部通过{X}")
print(f"{B}{'='*64}{X}")
raise SystemExit(1 if failed else 0)

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
D-008 / D-009 缺陷专项回归测试脚本

D-008: GET /api/wallet 和 GET /api/admin/users/{uid}/wallet
       响应中钱包 ID 字段名应为 wallet_id（不是 id）
       修复版本：PR #135（3471126）

D-009: PUT /api/admin/products/{pid}/plans/prices（批量设置价格）
       请求 body 结构：items[].product_plan_id（不在顶层）
       修复版本：PR #135（3471126）

执行方式（在测试服务器本地运行）：
  python3 ~/test_d008_d009.py

注意：
  - D-010（已知）：INTERNAL_API_TOKEN 未注入，F1 消费事件接口会 403，标记 SKIP
  - 接口路径以实际路由为准（/api/wallet 非 /api/my/wallet）
"""

import hashlib
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request

# ══════════════════════════════════════════════════════════════
# 配置
# ══════════════════════════════════════════════════════════════
API_BASE   = "http://localhost:8080"
MYSQL_HOST = "127.0.0.1"
MYSQL_PORT = "13306"
MYSQL_USER = "molin"
MYSQL_PASS = "molin_password"
MYSQL_DB   = "molin"

OTP_PLAIN = "777999"
OTP_HASH  = hashlib.sha256(OTP_PLAIN.encode()).hexdigest()

# ══════════════════════════════════════════════════════════════
# 统计
# ══════════════════════════════════════════════════════════════
RESULTS    = []
PASS_COUNT = 0
FAIL_COUNT = 0
SKIP_COUNT = 0


def _c(code, text):
    return f"\033[{code}m{text}\033[0m"


def ok(case_id, iface, note=""):
    global PASS_COUNT
    PASS_COUNT += 1
    RESULTS.append((case_id, iface, "PASS", note))
    print(f"  {_c('32;1','PASS')}  [{case_id}] {iface}" + (f" — {note}" if note else ""))


def fail(case_id, iface, note=""):
    global FAIL_COUNT
    FAIL_COUNT += 1
    RESULTS.append((case_id, iface, "FAIL", note))
    print(f"  {_c('31;1','FAIL')}  [{case_id}] {iface}" + (f" — {note}" if note else ""))


def skip(case_id, iface, note=""):
    global SKIP_COUNT
    SKIP_COUNT += 1
    RESULTS.append((case_id, iface, "SKIP", note))
    print(f"  {_c('33;1','SKIP')}  [{case_id}] {iface}" + (f" — {note}" if note else ""))


def info(msg):
    print(f"  {_c('36','INFO')}  {msg}")


def section(title):
    print(f"\n{'═'*64}")
    print(f"  {title}")
    print('═' * 64)


# ══════════════════════════════════════════════════════════════
# HTTP 辅助
# ══════════════════════════════════════════════════════════════
def http_req(method, path, body=None, token=None, extra_headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if extra_headers:
        h.update(extra_headers)
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            raw = resp.read().decode()
            try:
                return resp.status, json.loads(raw)
            except Exception:
                return resp.status, {"_raw": raw[:400]}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"_raw": raw[:400]}
    except Exception as e:
        return 0, {"error": str(e)}


def GET(path, token=None):
    return http_req("GET", path, token=token)


def POST(path, body=None, token=None, extra_headers=None):
    return http_req("POST", path, body=body, token=token, extra_headers=extra_headers)


def PATCH(path, body=None, token=None):
    return http_req("PATCH", path, body=body, token=token)


def PUT(path, body=None, token=None):
    return http_req("PUT", path, body=body, token=token)


def gdata(b):
    if isinstance(b, dict):
        return b.get("data") or {}
    return {}


# ══════════════════════════════════════════════════════════════
# MySQL 辅助
# ══════════════════════════════════════════════════════════════
def mysql_exec(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT,
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        info(f"[MySQL ERR] {r.stderr[:200]}")
    return r.returncode == 0


def mysql_query(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT,
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        return None
    return r.stdout.strip() or None


# ══════════════════════════════════════════════════════════════
# 账号辅助
# ══════════════════════════════════════════════════════════════
def seed_otp(email, phone):
    mysql_exec(
        f"DELETE FROM verification_codes "
        f"WHERE target_value IN ('{email}', '{phone}') AND scene='register';"
    )
    mysql_exec(
        f"INSERT INTO verification_codes "
        f"(target_type, target_value, code, scene, expires_at) VALUES "
        f"('email', '{email}', '{OTP_HASH}', 'register', '2099-01-01 00:00:00'), "
        f"('phone', '{phone}', '{OTP_HASH}', 'register', '2099-01-01 00:00:00');"
    )


def register_user(email, phone, password="Test1234!"):
    seed_otp(email, phone)
    s, b = POST("/api/auth/register", {
        "email": email, "phone": phone, "password": password,
        "email_code": OTP_PLAIN, "phone_code": OTP_PLAIN
    })
    if s not in (200, 201):
        info(f"注册失败: HTTP {s}, {b}")
        return None, None
    d = gdata(b)
    token = d.get("access_token", "")
    s2, b2 = GET("/api/me", token=token)
    uid = gdata(b2).get("id") if s2 == 200 else None
    return uid, token


def login_user(email, password="Test1234!"):
    s, b = POST("/api/auth/login/email", {"email": email, "password": password})
    if s != 200:
        return None
    return gdata(b).get("access_token")


def db_recharge(user_id, amount_yuan):
    exists = mysql_query(f"SELECT id FROM wallets WHERE user_id={user_id} LIMIT 1;")
    if not exists:
        mysql_exec(
            f"INSERT INTO wallets (user_id, balance_amount, frozen_amount, currency, version) "
            f"VALUES ({user_id}, 0, 0, 'CNY', 0);"
        )
    return mysql_exec(
        f"UPDATE wallets SET balance_amount = balance_amount + {amount_yuan}, "
        f"version = version + 1 WHERE user_id = {user_id};"
    )


# ══════════════════════════════════════════════════════════════
# 初始化
# ══════════════════════════════════════════════════════════════
def setup():
    section("账号 & 数据初始化")
    ts = int(time.time())
    ctx = {}

    # 注册管理员
    admin_email = f"d089_admin_{ts}@example.com"
    admin_phone = f"138{ts % 100000000:08d}"
    uid_admin, admin_token = register_user(admin_email, admin_phone)
    if not uid_admin:
        info("管理员注册失败，终止")
        sys.exit(2)
    mysql_exec(
        f"INSERT IGNORE INTO user_roles (user_id, role_id) "
        f"SELECT {uid_admin}, id FROM roles WHERE code='admin' LIMIT 1;"
    )
    admin_token = login_user(admin_email)
    info(f"admin uid={uid_admin} token={'ok' if admin_token else 'FAIL'}")
    ctx["admin_token"] = admin_token
    ctx["admin_uid"]   = uid_admin

    # 注册普通用户（用于 wallet 查询）
    buyer_email = f"d089_buyer_{ts}@example.com"
    buyer_phone = f"139{ts % 100000000:08d}"
    uid_buyer, buyer_token = register_user(buyer_email, buyer_phone)
    if not uid_buyer:
        info("buyer 注册失败，终止")
        sys.exit(2)
    db_recharge(uid_buyer, 200)
    info(f"buyer uid={uid_buyer} token={'ok' if buyer_token else 'FAIL'}")
    ctx["buyer_uid"]   = uid_buyer
    ctx["buyer_token"] = buyer_token

    # 创建测试商品
    s, b = POST("/api/admin/products", {
        "product_type": "saas",
        "product_code": f"d089_prod_{ts}",
        "name": f"D008-D009测试商品_{ts}",
        "description": "D-008/D-009 专项回归"
    }, token=admin_token)
    if s not in (200, 201):
        info(f"商品创建失败: HTTP {s}, {b}")
        return ctx
    product_id = gdata(b).get("id")
    info(f"商品 product_id={product_id}")
    ctx["product_id"] = product_id

    # 激活商品
    PATCH(f"/api/admin/products/{product_id}/status", {"status": "active"}, token=admin_token)

    # 创建套餐 A
    s, b = POST(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"d089_plan_a_{ts}",
        "name": "D089 套餐A",
        "billing_type": "one_time",
        "status": "active"
    }, token=admin_token)
    if s not in (200, 201):
        info(f"套餐A创建失败: HTTP {s}, {b}")
        return ctx
    plan_id_a = gdata(b).get("id")
    info(f"套餐A plan_id={plan_id_a}")
    ctx["plan_id_a"] = plan_id_a

    # 创建套餐 B（用于多 item 价格测试）
    s, b = POST(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"d089_plan_b_{ts}",
        "name": "D089 套餐B",
        "billing_type": "one_time",
        "status": "active"
    }, token=admin_token)
    if s not in (200, 201):
        info(f"套餐B创建失败: HTTP {s}, {b}")
    plan_id_b = gdata(b).get("id") if s in (200, 201) else None
    info(f"套餐B plan_id={plan_id_b}")
    ctx["plan_id_b"] = plan_id_b

    info("初始化完成\n")
    return ctx


# ══════════════════════════════════════════════════════════════
# D-008: wallet_id 字段名
# ══════════════════════════════════════════════════════════════
def test_d008(ctx):
    section("D-008 专项：wallet_id 字段名验证")
    buyer_token = ctx.get("buyer_token")
    buyer_uid   = ctx.get("buyer_uid")
    admin_token = ctx.get("admin_token")

    # ── T-D008-1：用户端查钱包 ────────────────────────────────
    # 接口路径：GET /api/wallet（非 /api/my/wallet，以实际路由为准）
    s, b = GET("/api/wallet", token=buyer_token)
    d = gdata(b)
    info(f"T-D008-1 响应: HTTP {s}, data keys={list(d.keys())}, body={json.dumps(b)[:300]}")

    if s == 200:
        has_wallet_id = "wallet_id" in d
        has_old_id    = d.get("id") is not None and "wallet_id" not in d

        if has_wallet_id:
            ok("T-D008-1", "GET /api/wallet",
               f"wallet_id={d['wallet_id']} balance={d.get('balance_amount')} 字段名正确")
        elif has_old_id:
            fail("T-D008-1", "GET /api/wallet",
                 f"仍使用旧字段名 'id'={d.get('id')}，未修复为 wallet_id；实际 keys={list(d.keys())}")
        else:
            fail("T-D008-1", "GET /api/wallet",
                 f"响应缺 wallet_id 字段；实际 keys={list(d.keys())}")
    else:
        fail("T-D008-1", "GET /api/wallet",
             f"接口异常 HTTP {s}，body={json.dumps(b)[:200]}")

    # ── T-D008-2：管理端查指定用户钱包 ───────────────────────
    if not buyer_uid:
        skip("T-D008-2", "GET /api/admin/users/{uid}/wallet", "buyer_uid 未获取")
        return

    s, b = GET(f"/api/admin/users/{buyer_uid}/wallet", token=admin_token)
    d = gdata(b)
    info(f"T-D008-2 响应: HTTP {s}, data keys={list(d.keys())}, body={json.dumps(b)[:300]}")

    if s == 200:
        has_wallet_id = "wallet_id" in d
        has_old_id    = d.get("id") is not None and "wallet_id" not in d

        if has_wallet_id:
            ok("T-D008-2", f"GET /api/admin/users/{buyer_uid}/wallet",
               f"wallet_id={d['wallet_id']} balance={d.get('balance_amount')} 字段名正确")
        elif has_old_id:
            fail("T-D008-2", f"GET /api/admin/users/{buyer_uid}/wallet",
                 f"仍使用旧字段名 'id'={d.get('id')}，未修复为 wallet_id；keys={list(d.keys())}")
        else:
            fail("T-D008-2", f"GET /api/admin/users/{buyer_uid}/wallet",
                 f"响应缺 wallet_id 字段；keys={list(d.keys())}")
    else:
        fail("T-D008-2", f"GET /api/admin/users/{buyer_uid}/wallet",
             f"接口异常 HTTP {s}，body={json.dumps(b)[:200]}")


# ══════════════════════════════════════════════════════════════
# D-009: 批量价格设置 body 结构
# ══════════════════════════════════════════════════════════════
def test_d009(ctx):
    section("D-009 专项：批量价格设置 body 结构验证")
    admin_token = ctx.get("admin_token")
    buyer_token = ctx.get("buyer_token")
    product_id  = ctx.get("product_id")
    plan_id_a   = ctx.get("plan_id_a")
    plan_id_b   = ctx.get("plan_id_b")

    if not product_id or not plan_id_a:
        skip("T-D009-1", "PATCH /api/admin/products/{pid}/prices", "商品/套餐未创建")
        skip("T-D009-2", "PATCH /api/admin/products/{pid}/prices 单 item", "商品/套餐未创建")
        skip("T-D009-3", "PATCH /api/admin/products/{pid}/prices product_plan_id=0", "商品未创建")
        return

    # 根据是否有 plan_b，决定 items 数量
    items = []
    if plan_id_b:
        items = [
            {"product_plan_id": plan_id_a, "price_amount": "99.00",  "currency": "CNY"},
            {"product_plan_id": plan_id_b, "price_amount": "199.00", "currency": "CNY"},
        ]
    else:
        items = [
            {"product_plan_id": plan_id_a, "price_amount": "99.00", "currency": "CNY"},
        ]

    # ── T-D009-1：items 内含 product_plan_id → 期望 HTTP 200 ──
    request_body = {"items": items}
    info(f"T-D009-1 请求 body: {json.dumps(request_body)}")

    s, b = PATCH(f"/api/admin/products/{product_id}/prices",
                 request_body, token=admin_token)
    info(f"T-D009-1 响应: HTTP {s}, body={json.dumps(b)[:300]}")

    if s in (200, 201, 204) and b.get("code") == 0:
        ok("T-D009-1",
           f"PATCH /api/admin/products/{product_id}/prices",
           f"items 内含 product_plan_id，HTTP {s} 正确")
    else:
        fail("T-D009-1",
             f"PATCH /api/admin/products/{product_id}/prices",
             f"期望 HTTP 200 code=0，实际 HTTP {s} code={b.get('code')} body={json.dumps(b)[:200]}")

    # ── T-D009-2：单 item 正常格式 → HTTP 200 ────────────────
    single_item_body = {
        "items": [{"product_plan_id": plan_id_a, "price_amount": "88.00", "currency": "CNY"}]
    }
    info(f"T-D009-2 请求 body: {json.dumps(single_item_body)}")

    s, b = PATCH(f"/api/admin/products/{product_id}/prices",
                 single_item_body, token=admin_token)
    info(f"T-D009-2 响应: HTTP {s}, body={json.dumps(b)[:300]}")

    if s in (200, 201, 204) and b.get("code") == 0:
        ok("T-D009-2",
           f"PATCH /api/admin/products/{product_id}/prices 单item",
           f"单 item 设置，HTTP {s} 正确")
    else:
        fail("T-D009-2",
             f"PATCH /api/admin/products/{product_id}/prices 单item",
             f"期望 HTTP 200 code=0，实际 HTTP {s} code={b.get('code')} body={json.dumps(b)[:200]}")

    # ── T-D009-3：items 中 product_plan_id=0 → 期望 HTTP 400 (参数校验拒绝) ──
    invalid_body = {
        "items": [{"product_plan_id": 0, "price_amount": "99.00", "currency": "CNY"}]
    }
    info(f"T-D009-3 请求 body: {json.dumps(invalid_body)}")

    s, b = PATCH(f"/api/admin/products/{product_id}/prices",
                 invalid_body, token=admin_token)
    info(f"T-D009-3 响应: HTTP {s}, body={json.dumps(b)[:300]}")

    if s == 400:
        ok("T-D009-3",
           f"PATCH /api/admin/products/{product_id}/prices product_plan_id=0",
           f"HTTP 400 参数校验拒绝，code={b.get('code')}")
    else:
        fail("T-D009-3",
             f"PATCH /api/admin/products/{product_id}/prices product_plan_id=0",
             f"期望 HTTP 400，实际 HTTP {s} body={json.dumps(b)[:200]}")

    # ── T-D009-4：验证价格确实写入 DB ────────────────────────
    price_in_db = mysql_query(
        f"SELECT price_amount FROM product_prices "
        f"WHERE product_plan_id={plan_id_a} ORDER BY id DESC LIMIT 1;"
    )
    info(f"T-D009-4 DB 价格验证: product_plan_id={plan_id_a} price_amount={price_in_db}")

    if price_in_db:
        # 期望最后一次写入的值（T-D009-2 写的 88.00）
        expected = "88.00"
        actual   = str(price_in_db).strip()
        if actual == expected:
            ok("T-D009-4",
               f"DB 价格验证 product_plan_id={plan_id_a}",
               f"price_amount={actual} 符合预期")
        else:
            # 可能是精度显示问题，检查浮点近似
            try:
                if abs(float(actual) - float(expected)) < 0.01:
                    ok("T-D009-4",
                       f"DB 价格验证 product_plan_id={plan_id_a}",
                       f"price_amount={actual}≈{expected} 符合预期")
                else:
                    fail("T-D009-4",
                         f"DB 价格验证 product_plan_id={plan_id_a}",
                         f"price_amount={actual}，期望约 {expected}")
            except ValueError:
                fail("T-D009-4",
                     f"DB 价格验证 product_plan_id={plan_id_a}",
                     f"price_amount={actual} 非数字")
    else:
        fail("T-D009-4",
             f"DB 价格验证 product_plan_id={plan_id_a}",
             "DB 中未找到价格记录，接口虽返回 200 但未实际写入")

    # ── T-D009-5：权限校验（非 admin → 403）─────────────────
    s, b = PATCH(f"/api/admin/products/{product_id}/prices",
                 {"items": [{"product_plan_id": plan_id_a, "price_amount": "1.00", "currency": "CNY"}]},
                 token=buyer_token)
    info(f"T-D009-5 非admin响应: HTTP {s}, body={json.dumps(b)[:200]}")

    if s == 403:
        ok("T-D009-5",
           f"PATCH /api/admin/products/{product_id}/prices 权限校验",
           "非 admin 返回 HTTP 403 正确")
    else:
        fail("T-D009-5",
             f"PATCH /api/admin/products/{product_id}/prices 权限校验",
             f"期望 403，实际 HTTP {s}")


# ══════════════════════════════════════════════════════════════
# 主程序
# ══════════════════════════════════════════════════════════════
def main():
    print("\n" + "=" * 64)
    print("  Molin — D-008 / D-009 缺陷专项回归测试")
    print(f"  API_BASE = {API_BASE}")
    print(f"  时间     = {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 64)

    # 健康检查
    s, b = GET("/api/health")
    if s != 200:
        print(f"  [ERROR] API 不可用: HTTP {s}")
        sys.exit(2)
    info(f"健康检查: {b}")

    ctx = setup()
    test_d008(ctx)
    test_d009(ctx)

    # ── 汇总 ─────────────────────────────────────────────────
    total = PASS_COUNT + FAIL_COUNT + SKIP_COUNT
    print(f"\n{'═'*64}")
    print(f"  D-008/D-009 专项：{total} 个用例  "
          f"{_c('32;1', str(PASS_COUNT)+' PASS')}  "
          f"{_c('31;1', str(FAIL_COUNT)+' FAIL')}  "
          f"{_c('33;1', str(SKIP_COUNT)+' SKIP')}")
    print('═' * 64)

    print(f"\n{'─'*64}")
    print(f"  {'用例ID':<18} {'状态':<10} {'备注'}")
    print('─' * 64)
    for (cid, iface, status, note) in RESULTS:
        sc = (_c("32;1", "PASS") if status == "PASS"
              else _c("31;1", "FAIL") if status == "FAIL"
              else _c("33;1", "SKIP"))
        note_s = note[:70] if note else ""
        print(f"  {cid:<18} {sc:<21} {note_s}")
    print('─' * 64)

    if FAIL_COUNT > 0:
        print(f"\n  {_c('31;1','FAIL 明细')}")
        for (cid, iface, status, note) in RESULTS:
            if status == "FAIL":
                print(f"  {_c('31','x')} [{cid}] {iface}")
                if note:
                    print(f"       {_c('33', note[:300])}")

    d008_cases  = [r for r in RESULTS if r[0].startswith("T-D008")]
    d009_cases  = [r for r in RESULTS if r[0].startswith("T-D009")]
    d008_pass   = all(r[2] == "PASS" for r in d008_cases)
    d009_pass   = all(r[2] == "PASS" for r in d009_cases)

    print(f"\n  结论: D-008 [{'PASS' if d008_pass else 'FAIL'}]  "
          f"D-009 [{'PASS' if d009_pass else 'FAIL'}]")
    print()
    sys.exit(0 if FAIL_COUNT == 0 else 1)


if __name__ == "__main__":
    main()

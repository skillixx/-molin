#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
D-005 / D-006 / D-007 缺陷回归测试脚本

D-005: GET /api/products/:id/plans 和 /api/admin/products/:id/plans
       响应应为扁平分页格式 {items:[...], page, page_size, total}
       而非嵌套在 data.plans 数组中

D-006: PATCH /api/admin/products/:id/plans/999999（不存在 plan_id）
       应返回 HTTP 404，code=40400
       正常更新已存在套餐 → 仍返回 200

D-007: POST /api/orders/:id/pay 对已支付订单再次调用
       应返回 HTTP 400，code=60002
       正常首次支付 → 仍返回 200，响应无误导性零值字段

执行方式（测试服务器本地）：
  python3 /home/pc/test_d005_d006_d007.py
"""

import hashlib
import json
import sys
import time
import urllib.error
import urllib.request
import subprocess
from decimal import Decimal

# ── 配置 ──────────────────────────────────────────────────
API_BASE   = "http://localhost:8080"
MYSQL_HOST = "127.0.0.1"
MYSQL_PORT = "13306"
MYSQL_USER = "molin"
MYSQL_PASS = "molin_password"
MYSQL_DB   = "molin"

OTP_PLAIN = "777999"
OTP_HASH  = hashlib.sha256(OTP_PLAIN.encode()).hexdigest()

# ── 统计 ──────────────────────────────────────────────────
PASS_COUNT = 0
FAIL_COUNT = 0
SKIP_COUNT = 0
RESULTS    = []


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


# ── HTTP 辅助 ─────────────────────────────────────────────
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


def gdata(b):
    if isinstance(b, dict):
        return b.get("data") or {}
    return {}


# ── MySQL 辅助 ────────────────────────────────────────────
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


# ── 注册/登录辅助 ─────────────────────────────────────────
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


def db_set_verified(user_id):
    return mysql_exec(f"UPDATE users SET real_name_status='verified' WHERE id={user_id};")


# ── 初始化 ────────────────────────────────────────────────
def setup():
    section("账号 & 数据初始化")
    ts = int(time.time())
    ctx = {}

    # 注册管理员
    admin_email = f"d0567_admin_{ts}@example.com"
    admin_phone = f"138{ts % 100000000:08d}"
    uid_admin, admin_token = register_user(admin_email, admin_phone)
    if not uid_admin:
        info("管理员注册失败，终止测试")
        sys.exit(2)
    mysql_exec(
        f"INSERT IGNORE INTO user_roles (user_id, role_id) "
        f"SELECT {uid_admin}, id FROM roles WHERE code='admin' LIMIT 1;"
    )
    # 重新登录以获取含管理员权限的 token
    admin_token = login_user(admin_email)
    info(f"admin uid={uid_admin}")
    ctx["admin_token"] = admin_token

    # 注册普通购买用户
    buyer_email = f"d0567_buyer_{ts}@example.com"
    buyer_phone = f"139{ts % 100000000:08d}"
    uid_buyer, buyer_token = register_user(buyer_email, buyer_phone)
    if not uid_buyer:
        info("buyer 注册失败，终止测试")
        sys.exit(2)
    info(f"buyer uid={uid_buyer}")

    # 实名认证 + 充值（DB 直写）
    db_set_verified(uid_buyer)
    db_recharge(uid_buyer, 300)

    # 确认购买角色（优先使用 qa_buyer，不存在则创建）
    role_id = mysql_query("SELECT id FROM roles WHERE code='qa_buyer' LIMIT 1;")
    if not role_id:
        s, b = POST("/api/admin/roles", {
            "code": f"qa_buyer_{ts}",
            "name": f"QA购买角色_{ts}",
            "description": "自动化测试购买角色"
        }, token=admin_token)
        if s in (200, 201):
            role_id = str(gdata(b).get("id", ""))
            info(f"新建购买角色 role_id={role_id}")
        else:
            info(f"购买角色创建失败: {s}, {b}")
    else:
        info(f"使用已有购买角色 role_id={role_id}")

    if role_id:
        mysql_exec(
            f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid_buyer}, {role_id});"
        )

    ctx["buyer_uid"] = uid_buyer
    ctx["buyer_token"] = buyer_token
    ctx["buyer_email"] = buyer_email
    ctx["role_id"] = role_id

    # 创建测试商品（管理员接口）
    s, b = POST("/api/admin/products", {
        "product_type": "saas",
        "product_code": f"d0567_prod_{ts}",
        "name": f"D567测试商品_{ts}",
        "description": "D-005/006/007 回归测试用"
    }, token=admin_token)
    if s not in (200, 201):
        info(f"商品创建失败: {s}, {b}")
        return ctx
    product_id = gdata(b).get("id")
    info(f"商品 product_id={product_id} (状态=draft)")
    ctx["product_id"] = product_id

    # 激活商品（draft → active）
    s, b = PATCH(f"/api/admin/products/{product_id}/status", {
        "status": "active"
    }, token=admin_token)
    info(f"激活商品: HTTP {s}, {json.dumps(b)[:80]}")

    # 设置角色访问权限（使用正确的 items 格式）
    # 后端 ReplaceAccessReq.Items = [{role_id, can_view, can_buy, can_use}]
    if role_id:
        s, b = PATCH(f"/api/admin/products/{product_id}/access", {
            "items": [{
                "role_id": int(role_id),
                "can_view": True,
                "can_buy": True,
                "can_use": True
            }]
        }, token=admin_token)
        info(f"访问权限设置: HTTP {s}, {json.dumps(b)[:80]}")
        # 也直接写 DB 确保权限存在（防止接口格式问题）
        mysql_exec(
            f"INSERT INTO product_role_access "
            f"(product_id, role_id, can_view, can_buy, can_use) "
            f"VALUES ({product_id}, {role_id}, 1, 1, 1) "
            f"ON DUPLICATE KEY UPDATE can_view=1, can_buy=1, can_use=1;"
        )

    # 创建套餐
    s, b = POST(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"d0567_plan_{ts}",
        "name": "D567标准套餐",
        "billing_type": "one_time",
        "status": "active"
    }, token=admin_token)
    if s not in (200, 201):
        info(f"套餐创建失败: {s}, {b}")
        return ctx
    plan_id = gdata(b).get("id")
    info(f"套餐 plan_id={plan_id}")
    ctx["plan_id"] = plan_id

    # 设置套餐价格
    # 后端实现：顶层 plan_id + items（不是 items[].product_plan_id）
    # 注意：这与 API 文档不一致（文档要求 items[].product_plan_id），但按实际后端实现测试
    s, b = PATCH(f"/api/admin/products/{product_id}/prices", {
        "plan_id": plan_id,
        "items": [{
            "price_amount": "50.00",
            "currency": "CNY"
        }]
    }, token=admin_token)
    info(f"价格设置(顶层plan_id): HTTP {s}, {json.dumps(b)[:100]}")

    if s not in (200, 201, 204):
        # 降级：直接 DB 写入价格
        info("接口设价失败，降级用 DB 直写价格")
        mysql_exec(
            f"INSERT INTO product_prices (product_plan_id, price_amount, currency) "
            f"VALUES ({plan_id}, 50.00, 'CNY') "
            f"ON DUPLICATE KEY UPDATE price_amount=50.00;"
        )

    # 验证价格写入
    price_check = mysql_query(
        f"SELECT price_amount FROM product_prices WHERE product_plan_id={plan_id} LIMIT 1;"
    )
    info(f"价格验证(DB): {price_check}")

    info("账号 & 数据初始化完成\n")
    return ctx


# ── D-005: 套餐列表分页格式 ──────────────────────────────
def test_d005(ctx):
    section("D-005 回归：套餐列表扁平分页格式")
    admin_token  = ctx.get("admin_token")
    buyer_token  = ctx.get("buyer_token")
    product_id   = ctx.get("product_id")

    if not product_id:
        skip("D005-1", "GET /api/products/:id/plans", "商品未创建，跳过")
        skip("D005-2", "GET /api/admin/products/:id/plans", "商品未创建，跳过")
        return

    # --- 用户视角 ---
    s, b = GET(f"/api/products/{product_id}/plans", token=buyer_token)
    info(f"D005 用户侧响应: HTTP {s}, {json.dumps(b)[:300]}")
    d = gdata(b)
    if s != 200:
        fail("D005-1", "GET /api/products/:id/plans",
             f"期望200，实际HTTP {s}，body={json.dumps(b)[:200]}")
    else:
        has_items      = "items" in d
        has_page       = "page" in d
        has_page_size  = "page_size" in d
        has_total      = "total" in d
        nested_plans   = "plans" in d   # 旧缺陷：嵌套在 plans key
        if has_items and has_page and has_page_size and has_total and not nested_plans:
            ok("D005-1", "GET /api/products/:id/plans",
               f"扁平分页格式正确 items={len(d['items'])} total={d['total']}")
        else:
            note = (f"has_items={has_items} has_page={has_page} "
                    f"has_page_size={has_page_size} has_total={has_total} "
                    f"nested_plans={nested_plans}")
            fail("D005-1", "GET /api/products/:id/plans", note)

    # --- 管理员视角 ---
    s, b = GET(f"/api/admin/products/{product_id}/plans", token=admin_token)
    info(f"D005 管理员侧响应: HTTP {s}, {json.dumps(b)[:300]}")
    d = gdata(b)
    if s != 200:
        fail("D005-2", "GET /api/admin/products/:id/plans",
             f"期望200，实际HTTP {s}")
    else:
        has_items      = "items" in d
        has_page       = "page" in d
        has_page_size  = "page_size" in d
        has_total      = "total" in d
        nested_plans   = "plans" in d
        if has_items and has_page and has_page_size and has_total and not nested_plans:
            ok("D005-2", "GET /api/admin/products/:id/plans",
               f"扁平分页格式正确 items={len(d['items'])} total={d['total']}")
        else:
            note = (f"has_items={has_items} has_page={has_page} "
                    f"has_page_size={has_page_size} has_total={has_total} "
                    f"nested_plans={nested_plans}")
            fail("D005-2", "GET /api/admin/products/:id/plans", note)


# ── D-006: 编辑不存在套餐返回 404 ───────────────────────
def test_d006(ctx):
    section("D-006 回归：编辑不存在套餐返回 404")
    admin_token = ctx.get("admin_token")
    product_id  = ctx.get("product_id")
    plan_id     = ctx.get("plan_id")

    if not product_id:
        skip("D006-1", "PATCH /api/admin/products/:id/plans/999999", "商品未创建，跳过")
        skip("D006-2", "PATCH /api/admin/products/:id/plans/:valid_id", "商品未创建，跳过")
        return

    # --- 不存在的 plan_id → 404，code=40400 ---
    s, b = PATCH(f"/api/admin/products/{product_id}/plans/999999", {
        "name": "不存在套餐编辑测试",
        "status": "active"
    }, token=admin_token)
    info(f"D006 不存在套餐 HTTP {s}, {json.dumps(b)[:200]}")
    if s == 404 and b.get("code") == 40400:
        ok("D006-1", "PATCH /api/admin/products/:id/plans/999999",
           "HTTP 404 code=40400 正确")
    elif s == 404:
        fail("D006-1", "PATCH /api/admin/products/:id/plans/999999",
             f"HTTP 404 但 code={b.get('code')}（期望 40400），body={b}")
    else:
        fail("D006-1", "PATCH /api/admin/products/:id/plans/999999",
             f"期望 404，实际 HTTP {s}，body={b}")

    # --- 正常 plan_id → 仍返回 200 ---
    if plan_id:
        s, b = PATCH(f"/api/admin/products/{product_id}/plans/{plan_id}", {
            "name": "D006回归编辑测试",
            "status": "active"
        }, token=admin_token)
        info(f"D006 正常编辑 HTTP {s}, {json.dumps(b)[:200]}")
        if s == 200 and b.get("code") == 0:
            ok("D006-2", f"PATCH /api/admin/products/{product_id}/plans/{plan_id}",
               "正常更新返回 HTTP 200")
        else:
            fail("D006-2", f"PATCH /api/admin/products/{product_id}/plans/{plan_id}",
                 f"期望 200，实际 HTTP {s}, {b}")
    else:
        skip("D006-2", "PATCH /api/admin/products/:id/plans/:valid_id", "套餐未创建，跳过")


# ── D-007: 已支付订单重复调用 pay 返回 400 ───────────────
def test_d007(ctx):
    section("D-007 回归：已支付订单重复支付返回 400")
    buyer_token = ctx.get("buyer_token")
    plan_id     = ctx.get("plan_id")
    product_id  = ctx.get("product_id")
    buyer_uid   = ctx.get("buyer_uid")

    if not plan_id or not product_id:
        skip("D007-1", "POST /api/products/:id/purchase", "套餐未创建，跳过")
        skip("D007-2", "POST /api/orders/:id/pay 正常支付", "套餐未创建，跳过")
        skip("D007-3", "POST /api/orders/:id/pay 重复支付", "套餐未创建，跳过")
        return

    # 验证购买前余额
    bal_before = mysql_query(
        f"SELECT balance_amount FROM wallets WHERE user_id={buyer_uid} LIMIT 1;"
    )
    info(f"D007 购买前余额: {bal_before}")

    idem_key_buy = f"d007_buy_{int(time.time())}_{buyer_uid}"

    # --- 购买：生成订单 ---
    s, b = POST(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id,
        "quantity": 1
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem_key_buy})
    info(f"D007 购买响应: HTTP {s}, {json.dumps(b)[:300]}")

    if s not in (200, 201):
        fail("D007-1", "POST /api/products/:id/purchase",
             f"购买失败 HTTP {s}: {b}")
        skip("D007-2", "POST /api/orders/:id/pay 正常支付", "购买失败，跳过")
        skip("D007-3", "POST /api/orders/:id/pay 重复支付", "购买失败，跳过")
        return

    purchase_data = gdata(b)
    order_id  = purchase_data.get("order_id")
    asset_id  = purchase_data.get("asset_id")
    info(f"D007 order_id={order_id} asset_id={asset_id}")

    if not order_id:
        fail("D007-1", "POST /api/products/:id/purchase",
             f"响应缺 order_id，data={purchase_data}")
        skip("D007-2", "POST /api/orders/:id/pay 正常支付", "无 order_id，跳过")
        skip("D007-3", "POST /api/orders/:id/pay 重复支付", "无 order_id，跳过")
        return

    ok("D007-1", "POST /api/products/:id/purchase",
       f"购买成功 order_id={order_id} asset_id={asset_id}")

    # 检查 asset_id 无零值（D-004 回归验证）
    if asset_id == 0:
        fail("D007-1b", "POST /api/products/:id/purchase D-004 asset_id",
             f"asset_id={asset_id}（期望非零正整数或 null，不应为 0）")
    elif asset_id is None:
        ok("D007-1b", "POST /api/products/:id/purchase D-004 asset_id",
           "asset_id=null（异步开通，符合规范）")
    else:
        ok("D007-1b", "POST /api/products/:id/purchase D-004 asset_id",
           f"asset_id={asset_id} 正常")

    # 检查 DB 中订单状态
    order_status_db = mysql_query(
        f"SELECT status FROM orders WHERE id={order_id} LIMIT 1;"
    )
    info(f"D007 订单 DB 状态: {order_status_db}")

    idem_key_pay  = f"d007_pay_{int(time.time())}_{order_id}"
    idem_key_pay2 = f"d007_pay2_{int(time.time())}_{order_id}"

    if order_status_db == "paid":
        # 购买即扣款，订单已 paid，直接测试重复支付
        info("订单状态已为 paid（钱包购买自动扣费），直接测试重复支付")
        skip("D007-2", "POST /api/orders/:id/pay 正常支付",
             "钱包购买已自动扣费，跳过首次 pay")

        s, b = POST(f"/api/orders/{order_id}/pay", {
            "pay_method": "wallet"
        }, token=buyer_token, extra_headers={"Idempotency-Key": idem_key_pay2})
        info(f"D007 重复支付响应: HTTP {s}, {json.dumps(b)[:300]}")

        if s == 400 and b.get("code") == 60002:
            ok("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
               "HTTP 400 code=60002 正确")
        elif s == 400:
            fail("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
                 f"HTTP 400 但 code={b.get('code')}（期望 60002），body={b}")
        else:
            fail("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
                 f"期望 HTTP 400，实际 HTTP {s}，body={b}")
        return

    # 订单 pending 状态 → 先正常支付一次
    s, b = POST(f"/api/orders/{order_id}/pay", {
        "pay_method": "wallet"
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem_key_pay})
    info(f"D007 首次支付响应: HTTP {s}, {json.dumps(b)[:300]}")

    if s == 200 and b.get("code") == 0:
        pay_data = gdata(b)
        # 检查响应无误导性零值字段
        zero_fields = [k for k, v in pay_data.items()
                       if isinstance(v, (int, float)) and v == 0]
        if zero_fields:
            fail("D007-2", f"POST /api/orders/{order_id}/pay 正常支付",
                 f"响应含整数零值字段 {zero_fields}（可能是 D-007 误导性零值）")
        else:
            ok("D007-2", f"POST /api/orders/{order_id}/pay 正常支付",
               "HTTP 200 无误导性零值字段")
    else:
        fail("D007-2", f"POST /api/orders/{order_id}/pay 正常支付",
             f"期望 HTTP 200，实际 HTTP {s}，body={b}")
        skip("D007-3", f"POST /api/orders/{order_id}/pay 重复支付", "首次支付失败，跳过")
        return

    # --- 重复支付同一 paid 订单 → 期望 400，code=60002 ---
    s, b = POST(f"/api/orders/{order_id}/pay", {
        "pay_method": "wallet"
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem_key_pay2})
    info(f"D007 重复支付响应: HTTP {s}, {json.dumps(b)[:300]}")

    if s == 400 and b.get("code") == 60002:
        ok("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
           "HTTP 400 code=60002 正确")
    elif s == 400:
        fail("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
             f"HTTP 400 但 code={b.get('code')}（期望 60002），body={b}")
    else:
        fail("D007-3", f"POST /api/orders/{order_id}/pay 重复支付",
             f"期望 HTTP 400，实际 HTTP {s}，body={b}")


# ── 主程序 ────────────────────────────────────────────────
def main():
    print("\n" + "=" * 64)
    print("  Molin 云管理平台 — D-005 / D-006 / D-007 缺陷回归测试")
    print(f"  API_BASE = {API_BASE}")
    print(f"  时间 = {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 64)

    # 健康检查
    s, b = GET("/api/health")
    if s != 200:
        print(f"  [ERROR] API 不可用: {s}")
        sys.exit(2)
    info(f"健康检查: {b}")

    ctx = setup()

    test_d005(ctx)
    test_d006(ctx)
    test_d007(ctx)

    # ── 汇总 ──────────────────────────────────────────────
    total = PASS_COUNT + FAIL_COUNT + SKIP_COUNT
    print(f"\n{'═'*64}")
    print(f"  D-005/D-006/D-007 回归：{total} 个用例  "
          f"{_c('32;1', str(PASS_COUNT)+' PASS')}  "
          f"{_c('31;1', str(FAIL_COUNT)+' FAIL')}  "
          f"{_c('33;1', str(SKIP_COUNT)+' SKIP')}")
    print('═' * 64)

    print(f"\n{'─'*64}")
    print(f"  {'用例ID':<14} {'状态':<10} {'备注'}")
    print(f"{'─'*64}")
    for (cid, iface, status, note) in RESULTS:
        sc = (_c("32;1", "PASS") if status == "PASS"
              else _c("31;1", "FAIL") if status == "FAIL"
              else _c("33;1", "SKIP"))
        note_short = note[:60] if note else ""
        print(f"  {cid:<14} {sc:<21} {note_short}")
    print(f"{'─'*64}")

    if FAIL_COUNT > 0:
        print(f"\n  {_c('31;1','FAIL 明细')}")
        for (cid, iface, status, note) in RESULTS:
            if status == "FAIL":
                print(f"  {_c('31','x')} [{cid}] {iface}")
                if note:
                    print(f"       {_c('33', note[:300])}")

    print()
    sys.exit(0 if FAIL_COUNT == 0 else 1)


if __name__ == "__main__":
    main()

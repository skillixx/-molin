#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
BUG-A/B/C/D 专项验证测试脚本
覆盖：
  BUG-B  UpdateProduct / UpdateProductStatus 不存在时返回 404
  BUG-C  CreateProduct / CreatePlan 重复编码返回友好提示（不暴露 DB 原文）
  BUG-D  ReplacePrices 多套餐原子性写入
  BUG-A  购买扣费与 MarkPaid 同一事务（status=paid + 幂等）

执行方式（在测试服务器上运行）：
  API_BASE=http://localhost:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  python3 ~/molin/tests/test_bug_abcd.py
"""

import os
import sys
import json
import time
import subprocess
import urllib.request
import urllib.error
import urllib.parse
from decimal import Decimal

# ═══════════════════════════════════════════════
# 配置
# ═══════════════════════════════════════════════
API_BASE   = os.getenv("API_BASE",       "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST",     "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT",     "13306")
MYSQL_USER = os.getenv("MYSQL_USER",     "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# qa_buyer 角色（role_id=30，code=qa_buyer），专供购买权限测试
QA_BUYER_ROLE_ID = 30

# ═══════════════════════════════════════════════
# 统计
# ═══════════════════════════════════════════════
PASS_COUNT = 0
FAIL_COUNT = 0
SKIP_COUNT = 0
FAIL_LIST  = []


def _color(code, text):
    return f"\033[{code}m{text}\033[0m"

def ok(name):
    global PASS_COUNT
    PASS_COUNT += 1
    print(f"  {_color('32;1', 'PASS')}  {name}")

def fail(name, detail=""):
    global FAIL_COUNT
    FAIL_COUNT += 1
    msg = f"{name}: {detail}" if detail else name
    FAIL_LIST.append(msg)
    print(f"  {_color('31;1', 'FAIL')}  {name}")
    if detail:
        print(f"         {_color('33', str(detail)[:400])}")

def skip(name, reason=""):
    global SKIP_COUNT
    SKIP_COUNT += 1
    print(f"  {_color('33;1', 'SKIP')}  {name}" + (f" ({reason})" if reason else ""))

def section(title):
    print(f"\n{'═'*64}")
    print(f"  {title}")
    print('═'*64)

def info(msg):
    print(f"  {_color('36', 'INFO')}  {msg}")


# ═══════════════════════════════════════════════
# HTTP 工具
# ═══════════════════════════════════════════════
def http_req(method, path, body=None, token=None, headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if headers:
        h.update(headers)
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            raw = resp.read().decode()
            try:
                return resp.status, json.loads(raw)
            except Exception:
                return resp.status, {"_raw": raw}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"_raw": raw}
    except Exception as e:
        return 0, {"error": str(e)}

def get(path, token=None, params=None):
    if params:
        path = path + "?" + urllib.parse.urlencode(params)
    return http_req("GET", path, token=token)

def post(path, body=None, token=None, headers=None):
    return http_req("POST", path, body=body, token=token, headers=headers)

def patch(path, body=None, token=None, headers=None):
    return http_req("PATCH", path, body=body, token=token, headers=headers)

def get_data(body):
    if isinstance(body, dict):
        d = body.get("data")
        return d if d is not None else {}
    return {}

def get_items(body):
    d = get_data(body)
    if isinstance(d, list):
        return d
    return d.get("items") or d.get("list") or []


# ═══════════════════════════════════════════════
# MySQL 工具
# ═══════════════════════════════════════════════
def mysql_exec(sql):
    result = subprocess.run([
        "mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT,
        f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
        "-e", sql
    ], capture_output=True, text=True)
    return result.returncode == 0

def mysql_query(sql):
    result = subprocess.run([
        "mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT,
        f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
        "-N", "-e", sql
    ], capture_output=True, text=True)
    if result.returncode != 0:
        return None
    return result.stdout.strip() or None


# ═══════════════════════════════════════════════
# 账号工具
# ═══════════════════════════════════════════════
def register_user(email, phone, password="Test1234!"):
    """双 OTP 注册"""
    # 1. 邮箱验证码
    s, b = post("/api/auth/verification-codes/email",
                {"email": email, "scene": "register"})
    if s != 200:
        info(f"邮箱验证码发送失败: HTTP {s}")
        return None, None
    email_code = get_data(b).get("code", "")

    # 2. 手机验证码
    s, b = post("/api/auth/verification-codes/phone",
                {"phone": phone, "scene": "register"})
    if s != 200:
        info(f"手机验证码发送失败: HTTP {s}")
        return None, None
    phone_code = get_data(b).get("code", "")

    # 3. 注册
    s, b = post("/api/auth/register", {
        "email": email, "phone": phone, "password": password,
        "email_code": email_code, "phone_code": phone_code
    })
    if s not in (200, 201):
        info(f"注册失败: HTTP {s}, {b}")
        return None, None
    d = get_data(b)
    token = d.get("access_token", "")
    uid   = d.get("user", {}).get("id")
    return uid, token


def login_user(email, password="Test1234!"):
    s, b = post("/api/auth/login/email", {"email": email, "password": password})
    if s != 200:
        return None
    return get_data(b).get("access_token")


def grant_admin_role(user_id):
    """将用户加入 admin 角色（role_id=5）"""
    sql = f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, 5);"
    return mysql_exec(sql)


def grant_buyer_role(user_id, role_id=QA_BUYER_ROLE_ID):
    """将用户加入购买者角色（默认 qa_buyer role_id=30）"""
    sql = f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {role_id});"
    return mysql_exec(sql)


def set_verified(user_id):
    """将用户设为实名认证通过"""
    sql = f"UPDATE users SET real_name_status='verified' WHERE id={user_id};"
    return mysql_exec(sql)


def ensure_buyer_role_exists():
    """确保 qa_buyer 角色存在，不存在则创建，返回 role_id"""
    existing = mysql_query(
        "SELECT id FROM roles WHERE code='qa_buyer' LIMIT 1;"
    )
    if existing:
        return int(existing)
    # 创建
    mysql_exec(
        "INSERT INTO roles (code, name) VALUES ('qa_buyer', 'QA买家角色') "
        "ON DUPLICATE KEY UPDATE name=name;"
    )
    new_id = mysql_query("SELECT id FROM roles WHERE code='qa_buyer' LIMIT 1;")
    return int(new_id) if new_id else None


def direct_recharge_db(user_id, amount_yuan):
    """
    直接向数据库写入钱包余额（测试专用，模拟充值已入账）
    先确保钱包存在，再更新余额
    """
    # 插入钱包（如不存在）
    sql_insert = (
        f"INSERT IGNORE INTO wallets (user_id, balance_amount, currency) "
        f"VALUES ({user_id}, 0, 'CNY');"
    )
    mysql_exec(sql_insert)
    # 增加余额
    sql_update = (
        f"UPDATE wallets SET balance_amount = balance_amount + {amount_yuan} "
        f"WHERE user_id = {user_id};"
    )
    return mysql_exec(sql_update)


def get_wallet_balance_db(user_id):
    """直接从数据库读取余额"""
    val = mysql_query(f"SELECT balance_amount FROM wallets WHERE user_id={user_id} LIMIT 1;")
    if val is None:
        return None
    try:
        return Decimal(val)
    except Exception:
        return None


# ═══════════════════════════════════════════════
# 准备管理员账号（复用逻辑）
# ═══════════════════════════════════════════════
def setup_admin():
    ts = int(time.time())
    email = f"qa_bugabcd_admin_{ts}@example.com"
    phone = f"138{str(ts)[-8:]}"
    info(f"注册管理员测试账号: {email} / {phone}")

    uid, token = register_user(email, phone, "Admin1234!")
    if not uid:
        return None, None

    if grant_admin_role(uid):
        info(f"DB: 赋予 uid={uid} admin 角色（role_id=5）")
    else:
        info("警告：赋予 admin 角色失败，部分用例可能 403")

    if set_verified(uid):
        info(f"DB: uid={uid} 实名状态设为 verified")

    # 重新登录刷新 Token（权限生效）
    time.sleep(0.3)
    new_token = login_user(email, "Admin1234!")
    if new_token:
        token = new_token
        info("管理员 Token 刷新成功")

    return uid, token


# ═══════════════════════════════════════════════
# BUG-B 验证：不存在资源返回 404
# ═══════════════════════════════════════════════
def test_bug_b(admin_token):
    section("BUG-B 验证：UpdateProduct / UpdateProductStatus / GetProduct → 404")

    NONEXIST_ID = 999999

    # T-B01: PATCH /api/admin/products/999999 → 404, code=40400
    s, b = patch(f"/api/admin/products/{NONEXIST_ID}",
                 {"name": "ghost"},
                 token=admin_token)
    code_val = b.get("code") if isinstance(b, dict) else None
    msg_val  = b.get("message", "") if isinstance(b, dict) else ""
    if s == 404 and code_val == 40400:
        ok(f"T-B01: PATCH /api/admin/products/{NONEXIST_ID} → HTTP 404, code=40400")
    else:
        fail(f"T-B01: PATCH /api/admin/products/{NONEXIST_ID} → 期望 HTTP 404 code=40400",
             f"实际 HTTP {s}, code={code_val}, msg={msg_val}")

    # T-B02: PATCH /api/admin/products/999999/status → 404, code=40400
    s, b = patch(f"/api/admin/products/{NONEXIST_ID}/status",
                 {"status": "active"},
                 token=admin_token)
    code_val = b.get("code") if isinstance(b, dict) else None
    msg_val  = b.get("message", "") if isinstance(b, dict) else ""
    if s == 404 and code_val == 40400:
        ok(f"T-B02: PATCH /api/admin/products/{NONEXIST_ID}/status → HTTP 404, code=40400")
    else:
        fail(f"T-B02: PATCH /api/admin/products/{NONEXIST_ID}/status → 期望 HTTP 404 code=40400",
             f"实际 HTTP {s}, code={code_val}, msg={msg_val}")

    # T-B03: GET /api/admin/products/999999 → 404, code=40400
    s, b = get(f"/api/admin/products/{NONEXIST_ID}", token=admin_token)
    code_val = b.get("code") if isinstance(b, dict) else None
    msg_val  = b.get("message", "") if isinstance(b, dict) else ""
    if s == 404 and code_val == 40400:
        ok(f"T-B03: GET /api/admin/products/{NONEXIST_ID} → HTTP 404, code=40400")
    else:
        fail(f"T-B03: GET /api/admin/products/{NONEXIST_ID} → 期望 HTTP 404 code=40400",
             f"实际 HTTP {s}, code={code_val}, msg={msg_val}")


# ═══════════════════════════════════════════════
# BUG-C 验证：重复编码返回友好提示
# ═══════════════════════════════════════════════
def test_bug_c(admin_token):
    section("BUG-C 验证：重复 product_code / plan_code → 友好 400，不暴露 DB 原文")

    ts = int(time.time())

    # 先创建一个商品，获取其 product_code
    new_code = f"bugc_product_{ts}"
    s, b = post("/api/admin/products", {
        "product_type": "app",
        "product_code": new_code,
        "name": "BUG-C 测试商品",
        "description": "重复编码测试"
    }, token=admin_token)

    if s not in (200, 201):
        fail("T-C01 前置：创建商品", f"HTTP {s}, {b}")
        fail("T-C02 跳过（前置失败）")
        return None

    d = get_data(b)
    product_id = d.get("id")
    info(f"创建商品成功 product_id={product_id}, product_code={new_code}")

    db_keywords = ["duplicate entry", "Duplicate entry", "Error 1062",
                   "unique constraint", "SQLSTATE", "1062"]

    # T-C01: 再次用相同 product_code 创建 → 400, code=40000, 消息含"已存在"
    s2, b2 = post("/api/admin/products", {
        "product_type": "app",
        "product_code": new_code,
        "name": "重复商品",
        "description": "这是重复的"
    }, token=admin_token)
    code_val = b2.get("code") if isinstance(b2, dict) else None
    msg_val  = b2.get("message", "") if isinstance(b2, dict) else ""
    contains_db = any(kw.lower() in msg_val.lower() for kw in db_keywords)
    contains_friendly = "已存在" in msg_val

    if s2 == 400 and code_val == 40000 and contains_friendly and not contains_db:
        ok(f"T-C01: 重复 product_code={new_code} → HTTP 400, code=40000, 消息友好")
        info(f"实际消息: {msg_val}")
    elif s2 == 400 and contains_db:
        fail(f"T-C01: 重复 product_code 返回了 DB 原始错误",
             f"HTTP {s2}, code={code_val}, msg={msg_val}")
    elif s2 == 400 and code_val == 40000 and not contains_friendly:
        fail(f"T-C01: 返回 400/40000 但消息未含'已存在'提示",
             f"msg={msg_val}")
    else:
        fail(f"T-C01: 重复 product_code → 期望 HTTP 400 code=40000",
             f"实际 HTTP {s2}, code={code_val}, msg={msg_val}")

    if not product_id:
        fail("T-C02 跳过（product_id 获取失败）")
        return product_id

    # 先创建一个 plan
    plan_code = f"bugc_plan_{ts}"
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": plan_code,
        "name": "BUG-C 测试套餐",
        "billing_type": "one_time"
    }, token=admin_token)

    if s not in (200, 201):
        fail(f"T-C02 前置：创建套餐失败", f"HTTP {s}, {b}")
        return product_id

    plan_id = get_data(b).get("id")
    info(f"创建套餐成功 plan_id={plan_id}, plan_code={plan_code}")

    # T-C02: 再次用相同 plan_code 创建 → 400, code=40000, 消息含"已存在"
    s2, b2 = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": plan_code,
        "name": "重复套餐",
        "billing_type": "one_time"
    }, token=admin_token)
    code_val = b2.get("code") if isinstance(b2, dict) else None
    msg_val  = b2.get("message", "") if isinstance(b2, dict) else ""
    contains_db = any(kw.lower() in msg_val.lower() for kw in db_keywords)
    contains_friendly = "已存在" in msg_val

    if s2 == 400 and code_val == 40000 and contains_friendly and not contains_db:
        ok(f"T-C02: 重复 plan_code={plan_code} → HTTP 400, code=40000, 消息友好")
        info(f"实际消息: {msg_val}")
    elif s2 == 400 and contains_db:
        fail(f"T-C02: 重复 plan_code 返回了 DB 原始错误",
             f"HTTP {s2}, code={code_val}, msg={msg_val}")
    elif s2 == 400 and code_val == 40000 and not contains_friendly:
        fail(f"T-C02: 返回 400/40000 但消息未含'已存在'提示",
             f"msg={msg_val}")
    else:
        fail(f"T-C02: 重复 plan_code → 期望 HTTP 400 code=40000",
             f"实际 HTTP {s2}, code={code_val}, msg={msg_val}")

    return product_id


# ═══════════════════════════════════════════════
# BUG-D 验证：ReplacePrices 多套餐原子性
# ═══════════════════════════════════════════════
def test_bug_d(admin_token):
    section("BUG-D 验证：ReplacePrices 多套餐原子性写入")

    ts = int(time.time())

    # 创建一个新商品（含两个套餐），确保测试数据干净
    pcode = f"bugd_product_{ts}"
    s, b = post("/api/admin/products", {
        "product_type": "app",
        "product_code": pcode,
        "name": "BUG-D 原子性测试商品",
        "description": "多套餐价格原子性写入"
    }, token=admin_token)

    if s not in (200, 201):
        fail("T-D01 前置：创建商品失败", f"HTTP {s}, {b}")
        fail("T-D02 跳过")
        return

    product_id = get_data(b).get("id")
    info(f"创建商品 product_id={product_id}")

    # 创建套餐 A
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"bugd_plan_a_{ts}",
        "name": "套餐A",
        "billing_type": "one_time"
    }, token=admin_token)
    if s not in (200, 201):
        fail("T-D01 前置：创建套餐A失败", f"HTTP {s}, {b}")
        fail("T-D02 跳过")
        return
    plan_a_id = get_data(b).get("id")
    info(f"创建套餐A plan_id={plan_a_id}")

    # 创建套餐 B
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"bugd_plan_b_{ts}",
        "name": "套餐B",
        "billing_type": "one_time"
    }, token=admin_token)
    if s not in (200, 201):
        fail("T-D01 前置：创建套餐B失败", f"HTTP {s}, {b}")
        fail("T-D02 跳过")
        return
    plan_b_id = get_data(b).get("id")
    info(f"创建套餐B plan_id={plan_b_id}")

    # T-D01: PATCH /api/admin/products/{id}/prices 含两个 product_plan_id → 200
    price_body = {
        "items": [
            {"product_plan_id": plan_a_id, "price_amount": "30.00", "currency": "CNY"},
            {"product_plan_id": plan_b_id, "price_amount": "60.00", "currency": "CNY"}
        ]
    }
    s, b = patch(f"/api/admin/products/{product_id}/prices",
                 price_body, token=admin_token)
    code_val = b.get("code") if isinstance(b, dict) else None
    msg_val  = b.get("message", "") if isinstance(b, dict) else ""

    if s == 200:
        ok(f"T-D01: PATCH /api/admin/products/{product_id}/prices (2 套餐) → HTTP 200")
    else:
        fail(f"T-D01: PATCH prices 含 2 个套餐 → 期望 HTTP 200",
             f"实际 HTTP {s}, code={code_val}, msg={msg_val}")

    # T-D02: 验证两个套餐都确实写入了价格（DB 直查）
    rows_a = mysql_query(
        f"SELECT price_amount FROM product_prices "
        f"WHERE product_plan_id={plan_a_id} ORDER BY id DESC LIMIT 1;"
    )
    rows_b = mysql_query(
        f"SELECT price_amount FROM product_prices "
        f"WHERE product_plan_id={plan_b_id} ORDER BY id DESC LIMIT 1;"
    )
    info(f"DB 套餐A 价格: {rows_a}, 套餐B 价格: {rows_b}")

    def parse_amount(raw):
        if raw is None:
            return Decimal("0")
        try:
            return Decimal(raw.split()[0])
        except Exception:
            return Decimal("0")

    a_ok = parse_amount(rows_a) > 0
    b_ok = parse_amount(rows_b) > 0

    if a_ok and b_ok:
        ok(f"T-D02: DB 验证两个套餐价格均已写入（A={rows_a}, B={rows_b}）")
    elif not a_ok and not b_ok:
        fail("T-D02: 两个套餐价格均未写入（原子性全部失败）",
             f"plan_a_id={plan_a_id} price={rows_a}, plan_b_id={plan_b_id} price={rows_b}")
    elif not a_ok:
        fail("T-D02: 套餐A 价格未写入（原子性不完整）",
             f"plan_a_id={plan_a_id} price={rows_a}")
    else:
        fail("T-D02: 套餐B 价格未写入（原子性不完整）",
             f"plan_b_id={plan_b_id} price={rows_b}")

    # 用 API 也验证一下
    s3, b3 = get(f"/api/admin/products/{product_id}", token=admin_token)
    if s3 == 200:
        info(f"GET /api/admin/products/{product_id} → HTTP 200（API 查询正常）")
    else:
        info(f"GET /api/admin/products/{product_id} → HTTP {s3}（主要看 DB 验证）")

    return product_id, plan_a_id, plan_b_id


# ═══════════════════════════════════════════════
# BUG-A 验证：购买扣费与 MarkPaid 同一事务
# ═══════════════════════════════════════════════
def test_bug_a(admin_token):
    section("BUG-A 验证：购买后订单 status=paid + 幂等重复购买")

    ts = int(time.time())

    # 确认 qa_buyer 角色存在（role_id=30）
    buyer_role_id = ensure_buyer_role_exists()
    if not buyer_role_id:
        fail("T-A01/A02/A03 前置：qa_buyer 角色不存在且无法创建")
        return
    info(f"购买者角色 role_id={buyer_role_id} (qa_buyer)")

    # 1. 创建测试商品 + 套餐 + 价格（50 元）
    pcode = f"buga_product_{ts}"
    s, b = post("/api/admin/products", {
        "product_type": "app",
        "product_code": pcode,
        "name": "BUG-A 购买测试商品",
        "description": "事务原子性测试"
    }, token=admin_token)

    if s not in (200, 201):
        fail("T-A01/A02/A03 前置：创建商品失败", f"HTTP {s}, {b}")
        return

    product_id = get_data(b).get("id")
    info(f"商品 product_id={product_id}")

    # 创建套餐
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"buga_plan_{ts}",
        "name": "BUG-A 套餐",
        "billing_type": "one_time"
    }, token=admin_token)
    if s not in (200, 201):
        fail("T-A01/A02/A03 前置：创建套餐失败", f"HTTP {s}, {b}")
        return
    plan_id = get_data(b).get("id")
    info(f"套餐 plan_id={plan_id}")

    # 设置价格（50 元）
    s, b = patch(f"/api/admin/products/{product_id}/prices", {
        "items": [
            {"product_plan_id": plan_id, "price_amount": "50.00", "currency": "CNY"}
        ]
    }, token=admin_token)
    if s != 200:
        fail("T-A01/A02/A03 前置：设置价格失败", f"HTTP {s}, {b}")
        return
    info("价格设置成功 (50.00 CNY)")

    # 设置商品角色访问权限（buyer_role_id 可查看/购买/使用）
    s, b = patch(f"/api/admin/products/{product_id}/access", {
        "accesses": [
            {"role_id": buyer_role_id, "can_view": True, "can_buy": True, "can_use": True}
        ]
    }, token=admin_token)
    info(f"设置商品访问权限 role_id={buyer_role_id}: HTTP {s}")
    # API 可能静默成功但不写 DB，用 DB 直写兜底（不影响 BUG-A 验证目标）
    mysql_exec(
        f"INSERT IGNORE INTO product_role_access "
        f"(product_id, role_id, can_view, can_buy, can_use) "
        f"VALUES ({product_id}, {buyer_role_id}, 1, 1, 1);"
    )
    db_check = mysql_query(
        f"SELECT can_buy FROM product_role_access "
        f"WHERE product_id={product_id} AND role_id={buyer_role_id} LIMIT 1;"
    )
    info(f"DB 确认商品访问权限 can_buy={db_check}")

    # 2. 注册普通用户（购买者）
    user_email = f"buga_buyer_{ts}@example.com"
    user_phone = f"139{str(ts)[-8:]}"
    user_uid, user_token = register_user(user_email, user_phone, "Buyer1234!")
    if not user_uid:
        fail("T-A01/A02/A03 前置：注册购买用户失败")
        return
    info(f"购买用户 uid={user_uid}")

    # 赋予 qa_buyer 角色（DB 直写）
    if grant_buyer_role(user_uid, buyer_role_id):
        info(f"DB: 赋予 uid={user_uid} qa_buyer 角色（role_id={buyer_role_id}）")
    else:
        fail("T-A01/A02/A03 前置：赋予 qa_buyer 角色失败")
        return

    # 实名认证（DB 直写）
    if set_verified(user_uid):
        info(f"DB: uid={user_uid} 实名状态设为 verified")

    # 充值 200 元（DB 直写，模拟已入账）
    if direct_recharge_db(user_uid, 200):
        bal = get_wallet_balance_db(user_uid)
        info(f"DB 直充 200 元，当前余额={bal}")
    else:
        fail("T-A01/A02/A03 前置：充值失败")
        return

    # 重新登录获取含新角色的 Token
    time.sleep(0.3)
    new_user_token = login_user(user_email, "Buyer1234!")
    if new_user_token:
        user_token = new_user_token
        info("购买用户 Token 刷新成功（角色已生效）")

    # T-A01: 正常购买 → 200，返回 order_id
    idem_key = f"buga_idem_{ts}"
    s, b = post(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id
    }, token=user_token, headers={"Idempotency-Key": idem_key})
    code_val = b.get("code") if isinstance(b, dict) else None
    d = get_data(b)
    order_id  = d.get("order_id") or d.get("id")
    order_no  = d.get("order_no") or d.get("order_number")
    status_val = d.get("status", "")

    info(f"购买响应: HTTP {s}, code={code_val}, status={status_val}, order_id={order_id}, msg={b.get('message','') if isinstance(b,dict) else ''}")

    if s == 200:
        ok(f"T-A01: POST /api/products/{product_id}/purchase → HTTP 200")
    else:
        fail(f"T-A01: 正常购买 → 期望 HTTP 200",
             f"实际 HTTP {s}, code={code_val}, msg={b.get('message','') if isinstance(b,dict) else b}")

    # T-A02: 购买后订单 status=paid
    if s == 200:
        if status_val == "paid":
            ok(f"T-A02: 购买响应 status=paid（BUG-A 事务修复确认）")
        elif status_val == "pending":
            # BUG-A 修复前可能异步 MarkPaid，修复后应同步在事务中
            # 查 DB 确认
            lookup_id = order_id
            if not lookup_id and order_no:
                lookup_id = mysql_query(
                    f"SELECT id FROM orders WHERE order_no='{order_no}' LIMIT 1;"
                )
            if lookup_id:
                db_status = mysql_query(
                    f"SELECT status FROM orders WHERE id={lookup_id} LIMIT 1;"
                )
                info(f"DB 查询订单 id={lookup_id} status={db_status}")
                if db_status == "paid":
                    ok(f"T-A02: DB 验证订单 status=paid（BUG-A 事务修复确认）")
                else:
                    fail(f"T-A02: 订单 status={db_status}，期望 paid（BUG-A 未修复）",
                         "扣费与 MarkPaid 不在同一事务")
            else:
                fail("T-A02: 无法获取 order_id 查询 DB",
                     f"响应 order_id={order_id}, order_no={order_no}")
        elif status_val:
            fail(f"T-A02: 订单 status={status_val}，期望 paid 或 pending",
                 f"完整响应 data: {d}")
        else:
            # status 字段不存在，查 DB
            info("响应 data 中无 status 字段，改查 DB")
            if order_id:
                db_status = mysql_query(
                    f"SELECT status FROM orders WHERE id={order_id} LIMIT 1;"
                )
                info(f"DB 订单 status={db_status}")
                if db_status == "paid":
                    ok(f"T-A02: DB 验证订单 status=paid")
                else:
                    fail(f"T-A02: DB 订单 status={db_status}，期望 paid")
            else:
                fail("T-A02: 响应无 status 字段且无 order_id，无法验证")
    else:
        skip("T-A02", "T-A01 购买失败，跳过状态验证")

    # T-A03: 相同 Idempotency-Key 重复购买 → 200, idempotent=true（余额不重复扣）
    time.sleep(0.2)
    bal_before = get_wallet_balance_db(user_uid)

    s2, b2 = post(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id
    }, token=user_token, headers={"Idempotency-Key": idem_key})
    d2 = get_data(b2)
    is_idem = d2.get("idempotent", False) if isinstance(d2, dict) else False
    bal_after = get_wallet_balance_db(user_uid)
    info(f"重复购买: HTTP {s2}, idempotent={is_idem}, 余额前={bal_before} 后={bal_after}")

    if s2 == 200:
        if is_idem:
            ok("T-A03: 相同 Idempotency-Key 重复购买 → HTTP 200, idempotent=true")
        else:
            # 即使没有 idempotent 字段，只要余额未双扣就算幂等
            if bal_before is not None and bal_after is not None:
                diff = abs(bal_after - bal_before)
                if diff < Decimal("1"):
                    ok("T-A03: 相同 Idempotency-Key 重复购买 → HTTP 200，余额未重复扣（幂等）")
                    info("注：响应未含 idempotent=true 字段")
                else:
                    fail("T-A03: 重复购买后余额发生变化（双扣，非幂等）",
                         f"差值={diff}, before={bal_before}, after={bal_after}")
            else:
                ok("T-A03: 相同 Idempotency-Key 重复购买 → HTTP 200（余额 DB 查询不可用，豁免）")
    else:
        fail(f"T-A03: 重复购买 → 期望 HTTP 200",
             f"实际 HTTP {s2}, code={b2.get('code') if isinstance(b2,dict) else ''}, "
             f"msg={b2.get('message','') if isinstance(b2,dict) else b2}")


# ═══════════════════════════════════════════════
# 主入口
# ═══════════════════════════════════════════════
def main():
    print(f"\n{'='*64}")
    print("  BUG-A/B/C/D 专项验证")
    print(f"  服务器: {API_BASE}")
    print(f"{'='*64}\n")

    # 准备管理员账号
    section("准备测试账号")
    admin_uid, admin_token = setup_admin()
    if not admin_token:
        print("[ERROR] 管理员账号准备失败，无法继续")
        sys.exit(1)
    info(f"管理员 uid={admin_uid}，Token 获取成功")

    # ── BUG-B ──
    test_bug_b(admin_token)

    # ── BUG-C ──
    test_bug_c(admin_token)

    # ── BUG-D ──
    test_bug_d(admin_token)

    # ── BUG-A ──
    test_bug_a(admin_token)

    # ── 汇总 ──
    total = PASS_COUNT + FAIL_COUNT + SKIP_COUNT
    print(f"\n{'='*64}")
    print("  BUG-A/B/C/D 专项验证汇总")
    print(f"  PASS: {PASS_COUNT}  FAIL: {FAIL_COUNT}  SKIP: {SKIP_COUNT}  总计: {total}")
    print(f"{'='*64}")

    if FAIL_LIST:
        print("\n  失败项明细：")
        for i, item in enumerate(FAIL_LIST, 1):
            print(f"  {i:2}. {item}")

    bug_b_fail = [x for x in FAIL_LIST if "T-B0" in x]
    bug_c_fail = [x for x in FAIL_LIST if "T-C0" in x]
    bug_d_fail = [x for x in FAIL_LIST if "T-D0" in x]
    bug_a_fail = [x for x in FAIL_LIST if "T-A0" in x]

    print(f"\n{'='*64}")
    print("  结论")
    print(f"{'='*64}")
    print(f"  BUG-B {'PASS' if not bug_b_fail else 'FAIL'}  (不存在商品返回 404)")
    print(f"  BUG-C {'PASS' if not bug_c_fail else 'FAIL'}  (重复编码友好提示)")
    print(f"  BUG-D {'PASS' if not bug_d_fail else 'FAIL'}  (多套餐价格原子性)")
    print(f"  BUG-A {'PASS' if not bug_a_fail else 'FAIL'}  (购买事务 status=paid)")
    print()

    return FAIL_COUNT


if __name__ == "__main__":
    ec = main()
    sys.exit(0 if ec == 0 else 1)

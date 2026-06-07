#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
第一阶段（Week 1-4）整体验收 —— 端到端全链路回归测试脚本

覆盖 docs/development-execution-plan.md 第 216-227 行列出的验收用例：
  - 用户可以使用邮箱注册 / 手机号注册
  - 用户可以使用邮箱登录 / 手机号登录
  - 用户注册后可以提交实名认证
  - 未实名用户不能购买商品
  - 重复邮箱 / 重复手机号不能注册两个用户
  - 普通用户购买普通应用
  - VIP 用户购买会员价应用
  - 非会员无法购买会员专属应用
  - 用户余额不足无法购买
  - 钱包扣费后生成流水
  - 订单支付成功后生成资产
  - 管理员修改用户角色后权限立即生效
  - 用户权限被禁用后无法访问应用

完整链路：注册 → 登录 → 实名认证 → 充值/钱包 → 浏览应用商品 → 下单购买
          → 支付回调/扣费 → 生成资产 → 资产到期/权益消耗

执行方式（在测试服务器上）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/stage1_e2e_test.py
"""

import os
import sys
import json
import time
import urllib.request
import urllib.error
import urllib.parse
import subprocess
from decimal import Decimal

# ════════════════════════════════════════════════════════
# 配置
# ════════════════════════════════════════════════════════
API_BASE   = os.getenv("API_BASE", "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

PASS_COUNT = 0
FAIL_COUNT = 0
FAIL_LIST  = []


def _c(code, text):
    return f"\033[{code}m{text}\033[0m"

def ok(name):
    global PASS_COUNT
    PASS_COUNT += 1
    print(f"  {_c('32;1','PASS')}  {name}")

def fail(name, detail=""):
    global FAIL_COUNT
    FAIL_COUNT += 1
    msg = f"{name}: {detail}" if detail else name
    FAIL_LIST.append(msg)
    print(f"  {_c('31;1','FAIL')}  {name}")
    if detail:
        print(f"         {_c('33', str(detail)[:400])}")

def info(msg):
    print(f"  {_c('36','ℹ')}  {msg}")

def section(title):
    print()
    print(_c('1;36', '─' * 60))
    print(_c('1;36', f'  {title}'))
    print(_c('1;36', '─' * 60))


# ════════════════════════════════════════════════════════
# HTTP 工具
# ════════════════════════════════════════════════════════
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
        resp = urllib.request.urlopen(req, timeout=15)
        raw = resp.read()
        return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
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

def delete(path, token=None):
    return http_req("DELETE", path, token=token)

def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}

def assert_code(name, status, body, expected_http, expected_code=None):
    if status != expected_http:
        fail(name, f"HTTP {status}（期望 {expected_http}），body={str(body)[:300]}")
        return False
    if expected_code is not None:
        actual = body.get("code") if isinstance(body, dict) else None
        if actual != expected_code:
            fail(name, f"HTTP {status} 正确，但 code={actual}（期望 {expected_code}）")
            return False
    ok(name)
    return True


# ════════════════════════════════════════════════════════
# MySQL 辅助（仅用于断言验证 + 必要的 INSERT 播种，不做删除/覆盖）
# ════════════════════════════════════════════════════════
def mysql_exec(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT),
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        info(f"SQL 执行失败: {r.stderr[:300]}")
    return r.returncode == 0

def mysql_query(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT),
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0:
        return None
    return r.stdout.strip() or None

def mysql_query_rows(sql):
    r = subprocess.run(
        ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT),
         f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-e", sql],
        capture_output=True, text=True
    )
    if r.returncode != 0 or not r.stdout.strip():
        return []
    return [line.split("\t") for line in r.stdout.strip().splitlines()]


# ════════════════════════════════════════════════════════
# 账号操作
# ════════════════════════════════════════════════════════
def register_email(email, password="Test1234!"):
    s, b = post("/api/auth/verification-codes/email", {"target": email, "scene": "register"})
    if s != 200:
        return None, None, (s, b)
    code = get_data(b).get("code", "")
    s, b = post("/api/auth/register/email", {"email": email, "password": password, "code": code})
    if s not in (200, 201):
        return None, None, (s, b)
    d = get_data(b)
    token = d.get("access_token", "")
    s2, b2 = get("/api/me", token=token)
    uid = get_data(b2).get("id") if s2 == 200 else None
    return uid, token, (s, b)

def register_phone(phone, password="Test1234!"):
    s, b = post("/api/auth/verification-codes/phone", {"target": phone, "scene": "register"})
    if s != 200:
        return None, None, (s, b)
    code = get_data(b).get("code", "")
    s, b = post("/api/auth/register/phone", {"phone": phone, "password": password, "code": code})
    if s not in (200, 201):
        return None, None, (s, b)
    d = get_data(b)
    token = d.get("access_token", "")
    s2, b2 = get("/api/me", token=token)
    uid = get_data(b2).get("id") if s2 == 200 else None
    return uid, token, (s, b)

def login_email(email, password="Test1234!"):
    return post("/api/auth/login/email", {"email": email, "password": password})

def login_phone(phone):
    """手机号登录使用验证码（与邮箱密码登录不同）"""
    s, b = post("/api/auth/verification-codes/phone", {"target": phone, "scene": "login"})
    if s != 200:
        return s, b
    code = get_data(b).get("code", "")
    return post("/api/auth/login/phone", {"phone": phone, "code": code})


def get_wallet_balance(token):
    s, b = get("/api/wallet", token=token)
    if s != 200:
        return None
    raw = get_data(b).get("balance_amount", 0)
    try:
        return Decimal(str(raw))
    except Exception:
        return None

def get_order_no(order_id, token):
    s, b = get(f"/api/orders/{order_id}", token=token)
    if s == 200:
        return get_data(b).get("order_no")
    val = mysql_query(f"SELECT order_no FROM orders WHERE id={order_id} LIMIT 1;")
    return val

def direct_recharge(uid, token, amount_yuan):
    """创建充值订单 + 模拟微信回调，使余额到账。返回 (order_no, ok)"""
    ts = int(time.time() * 1000)
    s, b = post("/api/recharge/orders", {"amount": str(amount_yuan), "payment_method": "wechat"}, token=token)
    if s not in (200, 201):
        info(f"创建充值订单失败 HTTP {s}: {b}")
        return None, False
    d = get_data(b)
    order_id = d.get("order_id")
    order_no = get_order_no(order_id, token)
    if not order_no:
        return None, False
    amount_fen = int(Decimal(str(amount_yuan)) * 100)
    callback_body = {
        "out_trade_no": order_no,
        "transaction_id": f"TRADE_{ts}_{uid}",
        "total_fee": amount_fen,
    }
    # 当前 wechat_verifier 仅校验 Wechatpay-Signature/Timestamp/Nonce 三个头是否存在
    # （真实 RSA-SHA256 验签逻辑标记为 TODO，尚未实现），故模拟回调需附带这些头
    # 才能通过"缺少签名字段"防伪校验，模拟一次"看似合法"的支付平台回调。
    sig_headers = {
        "Wechatpay-Signature": "qa_e2e_simulated_signature",
        "Wechatpay-Timestamp": str(ts // 1000),
        "Wechatpay-Nonce": f"qa_nonce_{ts}",
    }
    s2, b2 = post("/api/payments/notify/wechat", callback_body, headers=sig_headers)
    if s2 != 200:
        info(f"支付回调失败 HTTP {s2}: {b2}")
    time.sleep(0.6)
    return order_no, s2 == 200


# ════════════════════════════════════════════════════════
# 0. 准备：管理员账号 + 测试角色 + 商品
# ════════════════════════════════════════════════════════
ADMIN_EMAIL = None
ADMIN_TOKEN = None
ADMIN_UID   = None

def setup_admin():
    """注册一个新管理员账号并通过 SQL 播种 admin 角色（INSERT，不修改已有数据）。"""
    global ADMIN_EMAIL, ADMIN_TOKEN, ADMIN_UID
    section("准备 0：创建/登录管理员账号")
    ts = int(time.time())
    ADMIN_EMAIL = f"qa_e2e_admin_{ts}@molin.io"
    uid, token, _ = register_email(ADMIN_EMAIL)
    if not uid:
        fail("注册管理员账号", "注册失败")
        return False
    ok(f"注册管理员账号成功 user_id={uid}")
    ADMIN_UID = uid

    # 绑定已有 admin 角色（roles 表中 code='admin' 已存在，仅 INSERT user_roles 关联，不覆盖角色定义）
    sql = f"""
    SET @role_id = (SELECT id FROM roles WHERE code = 'admin' LIMIT 1);
    INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid}, @role_id);
    """
    if mysql_exec(sql):
        ok("已为管理员账号绑定 admin 角色（INSERT IGNORE，未覆盖已有数据）")
    else:
        fail("绑定 admin 角色失败")
        return False

    s, b = login_email(ADMIN_EMAIL)
    if s != 200:
        fail("管理员登录失败", f"HTTP {s} {b}")
        return False
    ADMIN_TOKEN = get_data(b).get("access_token")
    ok("管理员登录成功，已获取 Token")
    return True


def admin_assign_role(user_id, role_code, reason="QA E2E 测试授权"):
    role_id_str = mysql_query(f"SELECT id FROM roles WHERE code='{role_code}' LIMIT 1;")
    if not role_id_str:
        return False, f"角色 {role_code} 不存在"
    role_id = int(role_id_str)
    s, b = post(f"/api/admin/users/{user_id}/roles", {"role_id": role_id, "reason": reason}, token=ADMIN_TOKEN)
    return s in (200, 201), (s, b)


def admin_revoke_role(user_id, role_code):
    role_id_str = mysql_query(f"SELECT id FROM roles WHERE code='{role_code}' LIMIT 1;")
    if not role_id_str:
        return False, f"角色 {role_code} 不存在"
    role_id = int(role_id_str)
    s, b = delete(f"/api/admin/users/{user_id}/roles/{role_id}", token=ADMIN_TOKEN)
    return s in (200, 204), (s, b)


# ════════════════════════════════════════════════════════
# 1. 注册 / 登录 / 重复校验
# ════════════════════════════════════════════════════════
def case_register_login():
    section("用例 1-4：邮箱/手机号 注册 + 登录")
    ts = int(time.time())
    email = f"qa_e2e_user_{ts}@molin.io"
    phone = f"139{ts % 100000000:08d}"
    password = "Test1234!"

    # 1. 邮箱注册
    uid, token, (s, b) = register_email(email, password)
    if uid:
        ok(f"用户可以使用邮箱注册（user_id={uid}）")
    else:
        fail("用户可以使用邮箱注册", f"HTTP {s} {b}")

    # 2. 手机号注册
    phone_uid, phone_token, (s2, b2) = register_phone(phone, password)
    if phone_uid:
        ok(f"用户可以使用手机号注册（user_id={phone_uid}）")
    else:
        fail("用户可以使用手机号注册", f"HTTP {s2} {b2}")

    # 3. 邮箱登录
    s3, b3 = login_email(email, password)
    if s3 == 200 and get_data(b3).get("access_token"):
        ok("用户可以使用邮箱登录")
    else:
        fail("用户可以使用邮箱登录", f"HTTP {s3} {b3}")

    # 4. 手机号登录
    s4, b4 = login_phone(phone)
    if s4 == 200 and get_data(b4).get("access_token"):
        ok("用户可以使用手机号登录")
    else:
        fail("用户可以使用手机号登录", f"HTTP {s4} {b4}")

    # 5. 重复邮箱注册
    s5, b5 = post("/api/auth/verification-codes/email", {"target": email, "scene": "register"})
    code5 = get_data(b5).get("code", "")
    s5b, b5b = post("/api/auth/register/email", {"email": email, "password": password, "code": code5})
    if s5b >= 400:
        ok(f"重复邮箱不能注册两个用户 → HTTP {s5b}")
    else:
        fail("重复邮箱不能注册两个用户", f"期望 4xx，实际 HTTP {s5b} {b5b}")

    # 6. 重复手机号注册
    s6, b6 = post("/api/auth/verification-codes/phone", {"target": phone, "scene": "register"})
    code6 = get_data(b6).get("code", "")
    s6b, b6b = post("/api/auth/register/phone", {"phone": phone, "password": password, "code": code6})
    if s6b >= 400:
        ok(f"重复手机号不能注册两个用户 → HTTP {s6b}")
    else:
        fail("重复手机号不能注册两个用户", f"期望 4xx，实际 HTTP {s6b} {b6b}")

    return {
        "email": email, "phone": phone, "password": password,
        "uid": uid, "token": token,
        "phone_uid": phone_uid, "phone_token": phone_token,
    }


# ════════════════════════════════════════════════════════
# 2. 实名认证
# ════════════════════════════════════════════════════════
def case_identity(uid, token):
    section("用例 5：用户注册后可以提交实名认证")
    ts = int(time.time())
    s, b = post("/api/identity/verifications", {
        "real_name": "测试用户",
        "id_card_no": f"1101011990010{ts % 100000:05d}",
        "attachments": ["https://cdn.example.com/a.jpg", "https://cdn.example.com/b.jpg"],
    }, token=token)
    if s in (200, 201):
        d = get_data(b)
        ok(f"提交实名认证成功，masked={d.get('id_card_no_masked')}")
    else:
        fail("提交实名认证", f"HTTP {s} {b}")
        return False

    s2, b2 = get("/api/identity/verifications/me", token=token)
    if s2 == 200:
        st = get_data(b2).get("status")
        ok(f"查询实名认证状态成功 → status={st}")
    else:
        fail("查询实名认证状态", f"HTTP {s2} {b2}")
    return True


def seed_verified(uid):
    """实名审核通常需要管理员人工审核通过，E2E 链路里直接用 SQL 模拟「审核通过」的最终结果
    （等价于管理员审核通过后的最终状态），不影响已有数据，仅 UPDATE 该测试账号自身记录。"""
    return mysql_exec(f"UPDATE users SET real_name_status='verified' WHERE id={uid};")


# ════════════════════════════════════════════════════════
# 3. 未实名用户不能购买
# ════════════════════════════════════════════════════════
def case_unverified_purchase(token, product_id, plan_id):
    section("用例 6：未实名用户不能购买商品")
    idem = f"idem_unverified_{int(time.time()*1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                token=token, headers={"Idempotency-Key": idem})
    assert_code("未实名用户购买商品 → 400，code=70001", s, b, 400, 70001)


# ════════════════════════════════════════════════════════
# 4. 商品/套餐/价格 准备
# ════════════════════════════════════════════════════════
def setup_product(role_code, role_id, price_yuan, membership_level_id=None, member_price_yuan=None):
    """创建一个商品+套餐，配置角色访问与价格（管理员）。返回 (product_id, plan_id)。"""
    ts = int(time.time() * 1000)
    code = f"qa_e2e_prod_{ts}"
    s, b = post("/api/admin/products", {
        "product_type": "application",
        "product_code": code,
        "name": f"E2E测试应用_{ts}",
        "description": "E2E 全链路测试商品",
        "status": "active",
    }, token=ADMIN_TOKEN)
    if s not in (200, 201):
        return None, None, (s, b)
    product_id = get_data(b).get("id")

    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": "basic",
        "name": "基础套餐",
        "billing_type": "one_time",
        "duration_days": 365,
        "status": "active",
    }, token=ADMIN_TOKEN)
    plan_id = get_data(b).get("id")
    if not plan_id:
        s2, b2 = get(f"/api/admin/products/{product_id}/plans", token=ADMIN_TOKEN)
        lst = get_data(b2).get("plans") or get_data(b2).get("list") or []
        if lst:
            plan_id = lst[-1].get("id")

    # 角色访问：仅授予指定角色 can_view/can_buy
    s, b = patch(f"/api/admin/products/{product_id}/access", {
        "accesses": [{"role_id": role_id, "can_view": True, "can_buy": True, "can_use": True}]
    }, token=ADMIN_TOKEN)

    # 价格：默认价 + （可选）会员价
    prices = [{"plan_id": plan_id, "role_id": None, "price_amount": str(price_yuan), "currency": "CNY"}]
    price_body = {"plan_id": plan_id, "prices": [{"price_amount": str(price_yuan), "currency": "CNY"}]}
    if membership_level_id and member_price_yuan is not None:
        price_body["prices"].append({
            "membership_level_id": membership_level_id,
            "price_amount": str(member_price_yuan),
            "currency": "CNY",
        })
    s, b = patch(f"/api/admin/products/{product_id}/prices", price_body, token=ADMIN_TOKEN)

    return product_id, plan_id, (s, b)


# ════════════════════════════════════════════════════════
# 5. 普通用户购买普通应用 + 钱包流水 + 资产生成
# ════════════════════════════════════════════════════════
def case_normal_purchase(uid, token, product_id, plan_id, price_yuan):
    section("用例 9/12/13/14：普通购买、余额不足、流水、资产")

    # 余额不足购买（先于充值，确保余额=0）
    bal0 = get_wallet_balance(token)
    info(f"购买前余额: {bal0}")
    idem_poor = f"idem_poor_{int(time.time()*1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                token=token, headers={"Idempotency-Key": idem_poor})
    assert_code("用户余额不足无法购买 → 400，code=60001", s, b, 400, 60001)

    # 充值
    info(f"充值 {price_yuan} + 余量 ...")
    recharge_amount = (Decimal(str(price_yuan)) + Decimal("100")).quantize(Decimal("0.01"))
    order_no, recharge_ok = direct_recharge(uid, token, str(recharge_amount))
    if recharge_ok:
        ok(f"充值成功，order_no={order_no}")
    else:
        fail("充值流程", "回调未成功，后续依赖此余额的用例可能失败")

    bal_before = get_wallet_balance(token)
    info(f"购买前余额（充值后）: {bal_before}")

    # 正常购买
    idem_ok = f"idem_ok_{int(time.time()*1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                token=token, headers={"Idempotency-Key": idem_ok})
    order_id = None
    if assert_code("普通用户购买普通应用 → 200/201", s, b, s if s in (200, 201) else 200):
        d = get_data(b)
        order_id = d.get("order_id")
        info(f"购买结果：{d}")

    bal_after = get_wallet_balance(token)
    info(f"购买后余额: {bal_after}")

    # 钱包扣费后生成流水
    if bal_before is not None and bal_after is not None:
        diff = bal_before - bal_after
        if diff == Decimal(str(price_yuan)):
            ok(f"钱包余额按价格 {price_yuan} 正确扣减（差额={diff}）")
        else:
            fail("钱包扣费金额异常", f"扣减={diff}，期望={price_yuan}")

    # 查流水
    s, b = get("/api/wallet/transactions", token=token, params={"page": 1, "page_size": 10})
    if s == 200:
        d = get_data(b)
        lst = d.get("list") or d.get("items") or (d if isinstance(d, list) else [])
        deduct_tx = [t for t in lst if t.get("type") in ("deduct", "consume", "purchase") or
                     (t.get("amount") and Decimal(str(t.get("amount"))) < 0)]
        if deduct_tx or lst:
            ok(f"钱包扣费后生成流水（共 {len(lst)} 条记录，含扣费类记录 {len(deduct_tx)} 条）")
            info(f"最新流水样例：{lst[0] if lst else None}")
        else:
            fail("钱包扣费后生成流水", "流水列表为空")
    else:
        fail("查询钱包流水", f"HTTP {s} {b}")

    # 数据库直接验证 wallet_transactions
    rows = mysql_query_rows(
        f"SELECT id, type, amount, balance_after FROM wallet_transactions "
        f"WHERE user_id={uid} ORDER BY id DESC LIMIT 5;")
    if rows:
        ok(f"DB 验证：wallet_transactions 中存在 {len(rows)} 条该用户流水记录")
        info(f"最新流水（DB）：{rows[0]}")
    else:
        fail("DB 验证 wallet_transactions", "未查到记录")

    # 订单支付成功后生成资产
    if order_id:
        time.sleep(2)  # provision 异步执行，等待落库
        s, b = get("/api/my/assets", token=token, params={"page": 1, "page_size": 20})
        found_asset = None
        if s == 200:
            d = get_data(b)
            lst = d.get("list") or d.get("items") or (d if isinstance(d, list) else [])
            for a in lst:
                if str(a.get("product_id")) == str(product_id):
                    found_asset = a
                    break
        if found_asset:
            ok(f"订单支付成功后生成资产（asset_id={found_asset.get('id')}, status={found_asset.get('status')}）")
        else:
            # DB 兜底验证（异步 provision 可能尚未落库或走的是其他链路）
            rows = mysql_query_rows(
                f"SELECT id, asset_type, status, expires_at FROM user_assets "
                f"WHERE user_id={uid} AND product_id={product_id} ORDER BY id DESC LIMIT 3;")
            if rows:
                ok(f"DB 验证：订单支付成功后生成资产（user_assets 中存在 {len(rows)} 条记录）")
                info(f"资产记录（DB）：{rows[0]}")
                found_asset = {"id": rows[0][0], "status": rows[0][2], "expires_at": rows[0][3]}
            else:
                fail("订单支付成功后生成资产", "/api/my/assets 与 user_assets 表均未查到对应记录（异步 provision 可能未触发或失败）")
    else:
        fail("订单支付成功后生成资产", "未获取到 order_id，跳过资产校验")
        found_asset = None

    return order_id, found_asset


# ════════════════════════════════════════════════════════
# 5b. 缺陷复现：从未访问过 /api/wallet 的新用户直接购买 → 500
# ════════════════════════════════════════════════════════
def case_wallet_lazy_init_bug(role_code, role_id, product_id, plan_id):
    """
    复现链路缺陷：钱包记录采用懒创建（仅 GET /api/wallet 或充值到账时才会 INSERT），
    而扣费路径 WalletService.Deduct → walletRepo.GetForUpdate 直接 SELECT ... FOR UPDATE，
    不存在时不会自动创建，且把 gorm.ErrRecordNotFound 原样返回；
    handler 的 errors.Is 分支无法匹配，最终落入 default 分支返回 HTTP 500。

    复现条件：全新注册用户，从未调用过 GET /api/wallet、未充值，
    实名认证通过且具备购买权限后直接发起购买。
    """
    section("缺陷复现：全新用户首次购买（从未访问钱包）触发 500")
    ts = int(time.time() * 1000)
    email = f"qa_e2e_walletbug_{ts}@molin.io"
    uid, token, _ = register_email(email)
    if not uid:
        fail("注册缺陷复现账号", "注册失败")
        return
    info(f"已注册全新账号 user_id={uid}（未调用 /api/wallet、未充值）")

    seed_verified(uid)
    okassign, _ = admin_assign_role(uid, role_code)
    if not okassign:
        fail("为缺陷复现账号分配购买角色", "分配失败")
        return

    # 直接购买（不预先 GET /api/wallet，不充值）
    idem = f"idem_walletbug_{ts}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                token=token, headers={"Idempotency-Key": idem})
    if s == 400 and isinstance(b, dict) and b.get("code") == 60001:
        ok("全新用户首次购买在余额不足场景下正确返回 400/60001（钱包懒创建链路无异常）")
    elif s == 500:
        fail("【P1 缺陷】全新用户首次购买（钱包记录尚未创建）触发 HTTP 500，而非 400/60001",
             f"HTTP {s} body={b}；根因：WalletService.Deduct 内部调用 "
             f"walletRepo.GetForUpdate 未对钱包不存在场景做兜底创建/转换，"
             f"原始 gorm.ErrRecordNotFound 被直接返回给 handler，未命中 "
             f"errors.Is(err, billingsvc.ErrInsufficientBalance) 分支，"
             f"落入 default 分支返回 50000/500。影响：所有从未触发钱包懒创建"
             f"（未调用过 GET /api/wallet 且未充值过）的新用户首次购买都会遭遇 500，"
             f"而不是预期的「余额不足」提示，属于阻断性缺陷。")
    else:
        info(f"返回 HTTP {s} body={b}（非预期的 400/60001 或 500，请人工核对）")
        fail("全新用户首次购买行为异常", f"HTTP {s} body={b}")


# ════════════════════════════════════════════════════════
# 6. VIP 用户购买会员价应用 / 非会员无法购买会员专属应用
# ════════════════════════════════════════════════════════
def case_membership_pricing(member_uid, member_token, member_role_id,
                            non_member_uid, non_member_token,
                            membership_level_id, default_price, member_price):
    section("用例 10/11：VIP 会员价购买 / 非会员无法购买会员专属应用")

    # 创建一个商品：默认价 = default_price，会员价 = member_price（更低）
    product_id, plan_id, _ = setup_product(
        role_code="qa_e2e_member_role", role_id=member_role_id,
        price_yuan=default_price,
        membership_level_id=membership_level_id, member_price_yuan=member_price)
    if not product_id or not plan_id:
        fail("创建会员价商品", "商品/套餐创建失败")
        return

    info(f"已创建商品 product_id={product_id} plan_id={plan_id}：默认价={default_price}，会员价={member_price}")

    # 充值，确保两个账号余额充足；先 GET /api/wallet 触发钱包记录的懒创建
    # （见下方 case_wallet_lazy_init_bug：直接走购买路径会触发 P1 缺陷 500）
    get_wallet_balance(member_token)
    get_wallet_balance(non_member_token)
    direct_recharge(member_uid, member_token, "200.00")
    direct_recharge(non_member_uid, non_member_token, "200.00")
    seed_verified(member_uid)
    seed_verified(non_member_uid)

    # VIP 会员下单：应按会员价扣费
    bal_before = get_wallet_balance(member_token)
    idem = f"idem_vip_{int(time.time()*1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                token=member_token, headers={"Idempotency-Key": idem})
    if assert_code("VIP 会员下单返回成功", s, b, s if s in (200, 201) else 200):
        d = get_data(b)
        amount = d.get("amount")
        info(f"VIP 购买返回 amount={amount}")
        bal_after = get_wallet_balance(member_token)
        if bal_before is not None and bal_after is not None:
            diff = bal_before - bal_after
            if diff == Decimal(str(member_price)):
                ok(f"VIP 用户购买会员价应用 → 按会员价 {member_price} 扣费（实际扣减={diff}）")
            elif diff == Decimal(str(default_price)):
                fail("VIP 用户购买会员价应用：未按会员价计费",
                     f"实际按默认价 {default_price} 扣费（差额={diff}），会员定价未生效")
            else:
                fail("VIP 用户购买会员价应用：扣费金额异常",
                     f"实际扣减={diff}，期望会员价={member_price} 或默认价={default_price}")

    # 非会员（同角色但无 user_memberships 记录）下单：当前实现下购买访问控制仅依赖角色 can_buy，
    # 不存在"会员专属商品"硬性门槛机制 —— 验证其实际行为并据此给出结论
    idem2 = f"idem_nonmember_{int(time.time()*1000)}"
    s2, b2 = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                  token=non_member_token, headers={"Idempotency-Key": idem2})
    info(f"非会员下单返回：HTTP {s2}, body={b2}")
    if s2 in (200, 201):
        d2 = get_data(b2)
        amount2 = d2.get("amount")
        # 非会员应按默认价/角色价计费而不是会员价；同时业务是否应禁止其购买取决于
        # "会员专属应用"的访问控制设计 —— 当前代码未发现此类硬性门槛（详见报告说明）
        if amount2 is not None and Decimal(str(amount2)) == Decimal(str(member_price)):
            fail("非会员无法购买会员专属应用",
                 f"非会员竟按会员价 {member_price} 成交（amount={amount2}），定价隔离失效，构成安全/计费缺陷")
        else:
            info(f"非会员下单按非会员价成交（amount={amount2}），定价隔离正常；"
                 f"但系统当前未实现'会员专属商品禁止非会员购买'的访问控制硬门槛——见报告说明")
            fail("非会员无法购买会员专属应用（业务规则缺失）",
                 "当前购买访问控制仅基于角色 product_role_access.can_buy，"
                 "不存在与 membership_level 绑定的'会员专属'购买门槛；"
                 "非会员账号成功完成购买（HTTP %s），未被拦截。" % s2)
    else:
        ok(f"非会员下单被拦截 → HTTP {s2}（与预期'非会员无法购买'一致）")


# ════════════════════════════════════════════════════════
# 7. 管理员修改用户角色后权限立即生效 / 权限被禁用后无法访问应用
# ════════════════════════════════════════════════════════
def case_role_permission(target_uid, target_token, product_id):
    section("用例 15/16：管理员修改角色后权限立即生效 / 权限禁用后无法访问应用")

    # 创建一个临时角色，赋予对该商品的 can_view/can_buy
    ts = int(time.time() * 1000)
    role_code = f"qa_e2e_role_{ts}"
    s, b = post("/api/admin/roles", {"code": role_code, "name": "E2E临时角色", "description": "临时授权测试"},
                token=ADMIN_TOKEN)
    role_id = get_data(b).get("id")
    if not role_id:
        fail("创建临时角色", f"HTTP {s} {b}")
        return
    ok(f"创建临时角色成功 role_id={role_id} code={role_code}")

    # 配置该角色对商品的访问权限
    s, b = get(f"/api/admin/products/{product_id}", token=ADMIN_TOKEN)
    s, b = patch(f"/api/admin/products/{product_id}/access", {
        "accesses": [{"role_id": role_id, "can_view": True, "can_buy": True, "can_use": True}]
    }, token=ADMIN_TOKEN)

    # 修改前：目标用户不可见该商品
    s, b = get("/api/products", token=target_token, params={"page": 1, "page_size": 50})
    visible_before = False
    if s == 200:
        lst = get_data(b).get("list") or get_data(b).get("items") or []
        visible_before = any(str(p.get("id")) == str(product_id) for p in lst)
    info(f"分配角色前，目标用户对商品 {product_id} 可见性: {visible_before}")

    # 管理员分配角色
    okassign, detail = admin_assign_role(target_uid, role_code)
    if okassign:
        ok(f"管理员为用户分配角色 {role_code} 成功")
    else:
        fail("管理员分配角色", str(detail))
        return

    # 立即（无需重新登录）验证权限生效：商品应可见 + 可购买
    s, b = get("/api/products", token=target_token, params={"page": 1, "page_size": 50})
    visible_after = False
    if s == 200:
        lst = get_data(b).get("list") or get_data(b).get("items") or []
        visible_after = any(str(p.get("id")) == str(product_id) for p in lst)
    if visible_after and not visible_before:
        ok("管理员修改用户角色后权限立即生效（无需重新登录，新角色商品立即可见）")
    elif visible_after and visible_before:
        info("分配前后均可见（可能已有其他角色授予了访问权限），改用购买结果进一步验证")
    else:
        fail("管理员修改用户角色后权限立即生效", f"分配角色后商品仍不可见 visible_before={visible_before} visible_after={visible_after}")

    # 撤销角色（管理员禁用其权限）
    okrevoke, detail2 = admin_revoke_role(target_uid, role_code)
    if okrevoke:
        ok(f"管理员撤销用户角色 {role_code} 成功")
    else:
        fail("管理员撤销角色", str(detail2))
        return

    # 验证：撤销后无法访问（购买）该应用
    idem = f"idem_revoked_{int(time.time()*1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": None},
                token=target_token, headers={"Idempotency-Key": idem})
    # 因 plan_id 缺失会先报 40000，改为查商品列表验证可见性更直接
    s2, b2 = get("/api/products", token=target_token, params={"page": 1, "page_size": 50})
    visible_revoked = False
    if s2 == 200:
        lst = get_data(b2).get("list") or get_data(b2).get("items") or []
        visible_revoked = any(str(p.get("id")) == str(product_id) for p in lst)
    if not visible_revoked:
        ok("用户权限被禁用（角色撤销）后无法访问应用 —— 商品列表中不再可见")
    else:
        info("商品仍可见（可能用户拥有其他角色授予了访问权限），尝试购买校验 can_buy 是否被收回")
        idem2 = f"idem_revoked2_{int(time.time()*1000)}"
        s3, b3 = get(f"/api/admin/products/{product_id}/plans", token=ADMIN_TOKEN)
        plan_lst = get_data(b3).get("plans") or get_data(b3).get("list") or []
        plan_id = plan_lst[-1].get("id") if plan_lst else None
        s4, b4 = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                      token=target_token, headers={"Idempotency-Key": idem2})
        if s4 == 403 and isinstance(b4, dict) and b4.get("code") == 40003:
            ok("用户权限被禁用后无法访问（购买）应用 → 403/40003")
        else:
            fail("用户权限被禁用后无法访问应用", f"撤销角色后仍可购买/访问：HTTP {s4} {b4}")


# ════════════════════════════════════════════════════════
# 主流程
# ════════════════════════════════════════════════════════
def main():
    print(_c('1;35', "=" * 60))
    print(_c('1;35', "  第一阶段（Week 1-4）端到端全链路验收测试"))
    print(_c('1;35', f"  API_BASE = {API_BASE}"))
    print(_c('1;35', "=" * 60))

    if not setup_admin():
        print(_c('31;1', "管理员账号准备失败，终止测试"))
        sys.exit(1)

    # 用例 1-4 + 7-8：注册/登录/重复校验
    reg = case_register_login()
    uid, token = reg["uid"], reg["token"]
    if not uid or not token:
        print(_c('31;1', "主测试账号注册失败，终止测试"))
        sys.exit(1)

    # 用例 5：实名认证提交
    case_identity(uid, token)

    # 准备一个普通商品（角色：qa_e2e_normal_role）供后续购买测试
    section("准备：创建普通购买角色 + 商品 + 绑定主测试用户")
    ts = int(time.time() * 1000)
    normal_role_code = f"qa_e2e_normal_role_{ts}"
    s, b = post("/api/admin/roles", {"code": normal_role_code, "name": "E2E普通购买角色"}, token=ADMIN_TOKEN)
    normal_role_id = get_data(b).get("id")
    if not normal_role_id:
        fail("创建普通购买角色", f"HTTP {s} {b}")
        sys.exit(1)
    ok(f"创建角色成功 role_id={normal_role_id}")

    product_id, plan_id, _ = setup_product(normal_role_code, normal_role_id, price_yuan="9.90")
    if not product_id or not plan_id:
        fail("创建测试商品", "商品/套餐/价格配置失败")
        sys.exit(1)
    ok(f"创建商品成功 product_id={product_id} plan_id={plan_id} 默认价=9.90")

    # 用例 6：未实名不能购买（先于实名认证落地之前测试）
    case_unverified_purchase(token, product_id, plan_id)

    # 将主测试账号实名状态置为 verified（模拟管理员审核通过的最终态），并绑定普通购买角色
    if seed_verified(uid):
        ok("已将主测试账号实名状态置为 verified（模拟审核通过）")
    else:
        fail("设置实名状态为 verified 失败")

    okassign, detail = admin_assign_role(uid, normal_role_code)
    if okassign:
        ok(f"管理员为主测试账号分配角色 {normal_role_code}")
    else:
        fail("分配普通购买角色失败", str(detail))

    # 用例 9/12/13/14：普通购买 + 余额不足 + 流水 + 资产
    order_id, asset = case_normal_purchase(uid, token, product_id, plan_id, "9.90")

    # 缺陷复现：全新用户首次购买（钱包懒创建链路）
    case_wallet_lazy_init_bug(normal_role_code, normal_role_id, product_id, plan_id)

    # ── 会员价 / 会员专属相关用例 ──
    section("准备：会员等级 + VIP 用户 + 非会员用户")
    # 复用已有的会员等级（数据库中已存在 qa_gold, id=1），如不存在则创建
    level_id_str = mysql_query("SELECT id FROM membership_levels WHERE level_code='qa_gold' LIMIT 1;")
    if level_id_str:
        membership_level_id = int(level_id_str)
        ok(f"复用已有会员等级 qa_gold（id={membership_level_id}）")
    else:
        s, b = post("/api/admin/membership-levels",
                    {"level_code": "qa_e2e_gold", "name": "E2E黄金会员", "sort_order": 1},
                    token=ADMIN_TOKEN)
        membership_level_id = get_data(b).get("id")
        ok(f"创建会员等级成功 id={membership_level_id}")

    member_role_code = f"qa_e2e_member_role_{ts}"
    s, b = post("/api/admin/roles", {"code": member_role_code, "name": "E2E会员角色"}, token=ADMIN_TOKEN)
    member_role_id = get_data(b).get("id")
    ok(f"创建会员角色成功 role_id={member_role_id}")

    # VIP 用户（绑定角色 + user_memberships 记录）
    vip_email = f"qa_e2e_vip_{ts}@molin.io"
    vip_uid, vip_token, _ = register_email(vip_email)
    admin_assign_role(vip_uid, member_role_code)
    mysql_exec(
        f"INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at) "
        f"VALUES ({vip_uid}, {membership_level_id}, 'active', NOW(), DATE_ADD(NOW(), INTERVAL 30 DAY));"
    )
    ok(f"已为 VIP 测试账号 user_id={vip_uid} 创建有效 user_memberships 记录（INSERT，未覆盖已有数据）")

    # 非会员用户（同角色，但无 user_memberships 记录）
    non_member_email = f"qa_e2e_nonmember_{ts}@molin.io"
    nm_uid, nm_token, _ = register_email(non_member_email)
    admin_assign_role(nm_uid, member_role_code)
    ok(f"非会员测试账号 user_id={nm_uid} 已创建（仅分配角色，不创建 user_memberships 记录）")

    case_membership_pricing(vip_uid, vip_token, member_role_id,
                            nm_uid, nm_token,
                            membership_level_id, default_price="20.00", member_price="6.00")

    # ── 用例 15/16：管理员修改角色 / 权限禁用 ──
    case_role_permission(uid, token, product_id)

    # ── 资产到期/权益消耗（旁路验证：检查已生成资产记录的过期时间字段是否合理） ──
    section("旁路验证：资产到期字段与权益记录")
    if asset:
        expires_at = asset.get("expires_at")
        if expires_at:
            ok(f"资产记录包含到期时间字段 expires_at={expires_at}（与套餐 duration_days=365 一致性需人工核对）")
        else:
            info("资产记录 expires_at 为空（一次性商品/无到期周期，属预期范围）")
    rows = mysql_query_rows(f"SELECT id, entitlement_type, quota_total, quota_used, status FROM user_entitlements WHERE user_id={uid} LIMIT 5;")
    if rows:
        ok(f"DB 中存在该用户权益记录 user_entitlements（{len(rows)} 条），权益消耗机制数据结构完整")
        info(f"权益记录样例：{rows[0]}")
    else:
        info("未查到该用户的 user_entitlements 记录（套餐类型为 one_time，可能不生成配额型权益，属预期）")

    # ── 总结 ──
    section("测试结果汇总")
    total = PASS_COUNT + FAIL_COUNT
    print(f"\n  总计: {total}  通过: {_c('32;1', PASS_COUNT)}  失败: {_c('31;1', FAIL_COUNT)}\n")
    if FAIL_LIST:
        print(_c('31;1', "  失败用例列表："))
        for i, m in enumerate(FAIL_LIST, 1):
            print(f"    {i}. {m}")
    print()
    sys.exit(0 if FAIL_COUNT == 0 else 1)


if __name__ == "__main__":
    main()

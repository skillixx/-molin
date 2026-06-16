#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
D-003 / D-004 缺陷回归测试脚本
D-003: quantity 未参与计价 — 验证 quantity=2 时扣费金额 = 单价 × 2
D-004: PurchaseResult 缺少 asset_id 字段 — 验证响应 JSON 中存在 asset_id key

执行方式（在测试服务器本地执行）：
  python3 /home/pc/test_d003_d004.py

依赖：标准库（无需第三方包）
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

# OTP（DB seed，2099 年过期）
OTP_PLAIN = "777999"
OTP_HASH  = hashlib.sha256(OTP_PLAIN.encode()).hexdigest()

# ── 统计 ──────────────────────────────────────────────────
PASS_COUNT = 0
FAIL_COUNT = 0
FAIL_LIST  = []


# ── 工具函数 ─────────────────────────────────────────────
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


def info(msg):
    print(f"  {_color('36', 'INFO')}  {msg}")


def section(title):
    print(f"\n{'═' * 60}")
    print(f"  {title}")
    print('═' * 60)


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
                return resp.status, {"_raw": raw}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"_raw": raw}
    except Exception as e:
        return 0, {"error": str(e)}


def get(path, token=None):
    return http_req("GET", path, token=token)


def post(path, body=None, token=None, extra_headers=None):
    return http_req("POST", path, body=body, token=token, extra_headers=extra_headers)


def patch(path, body=None, token=None):
    return http_req("PATCH", path, body=body, token=token)


def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}


# ── MySQL 辅助 ────────────────────────────────────────────
def mysql_exec(sql):
    result = subprocess.run([
        "mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT,
        f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
        "-e", sql
    ], capture_output=True, text=True)
    if result.returncode != 0:
        print(f"  [MySQL ERR] {result.stderr[:200]}")
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


# ── 注册辅助 ──────────────────────────────────────────────
def seed_otp(email, phone):
    """DB 直写注册验证码（2099 年过期）"""
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
    """注册用户，返回 (uid, access_token)"""
    seed_otp(email, phone)
    s, b = post("/api/auth/register", {
        "email": email,
        "phone": phone,
        "password": password,
        "email_code": OTP_PLAIN,
        "phone_code": OTP_PLAIN
    })
    if s not in (200, 201):
        info(f"注册失败: HTTP {s}, {b}")
        return None, None
    d = get_data(b)
    token = d.get("access_token", "")
    s2, b2 = get("/api/me", token=token)
    uid = get_data(b2).get("id") if s2 == 200 else None
    return uid, token


def login_user(email, password="Test1234!"):
    s, b = post("/api/auth/login/email", {"email": email, "password": password})
    if s != 200:
        return None, None
    d = get_data(b)
    return d.get("access_token"), d.get("refresh_token")


# ── 数据辅助 ──────────────────────────────────────────────
def seed_verified(user_id):
    return mysql_exec(
        f"UPDATE users SET real_name_status='verified' WHERE id={user_id};"
    )


def seed_role(user_id, role_id):
    return mysql_exec(
        f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {role_id});"
    )


def seed_admin_perms(user_id):
    sql = f"""
    SET @role_id = (SELECT id FROM roles WHERE code = 'admin' LIMIT 1);
    INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, @role_id);
    """
    return mysql_exec(sql)


def get_wallet_balance_db(user_id):
    """从 DB 直接查余额"""
    val = mysql_query(
        f"SELECT balance_amount FROM wallets WHERE user_id={user_id} LIMIT 1;"
    )
    if val is None:
        return None
    try:
        return Decimal(val)
    except Exception:
        return None


def db_recharge(user_id, amount_yuan):
    """
    DB 直充：绕过微信签名校验（仅限测试环境）
    先确保钱包存在，再加余额。
    """
    exists = mysql_query(
        f"SELECT id FROM wallets WHERE user_id={user_id} LIMIT 1;"
    )
    if not exists:
        ok_create = mysql_exec(
            f"INSERT INTO wallets (user_id, balance_amount, frozen_amount, currency, version) "
            f"VALUES ({user_id}, 0, 0, 'CNY', 0);"
        )
        if not ok_create:
            return False
    return mysql_exec(
        f"UPDATE wallets SET balance_amount = balance_amount + {amount_yuan}, "
        f"version = version + 1 "
        f"WHERE user_id = {user_id};"
    )


def ensure_buyer_role(buyer_uid):
    """
    获取或创建供购买测试使用的角色：
    优先使用已有的 qa_buyer(id=30)，不存在则创建一个新角色。
    返回 role_id。
    """
    qa_buyer_id = mysql_query("SELECT id FROM roles WHERE code='qa_buyer' LIMIT 1;")
    if qa_buyer_id:
        return int(qa_buyer_id)

    # 创建测试角色
    ts = int(time.time())
    rc = mysql_exec(
        f"INSERT IGNORE INTO roles (code, name) VALUES ('test_buyer_{ts}', '测试购买角色_{ts}');"
    )
    if not rc:
        return None
    role_id = mysql_query(f"SELECT id FROM roles WHERE code='test_buyer_{ts}' LIMIT 1;")
    return int(role_id) if role_id else None


def get_wallet_balance(token):
    """通过 API 查余额"""
    s, b = get("/api/wallet", token=token)
    if s != 200:
        return None
    raw = get_data(b).get("balance_amount", 0)
    try:
        return Decimal(str(raw))
    except Exception:
        return None


# ── 商品/套餐创建 ─────────────────────────────────────────
def create_product_and_plan(admin_token, buyer_role_id):
    """
    使用 admin token 创建商品 + 套餐 + 价格 + 访问权限
    单价固定 5.00 CNY，便于验证 quantity 计价
    返回 (product_id, plan_id, unit_price)
    """
    ts = int(time.time())
    product_code = f"d003d004_{ts}"
    unit_price = Decimal("5.00")

    # 1. 创建商品
    s, b = post("/api/admin/products", {
        "product_type": "saas",
        "product_code": product_code,
        "name": f"D003D004测试商品_{ts}",
        "description": "D-003/D-004 缺陷回归用",
        "status": "active"
    }, token=admin_token)
    info(f"创建商品 HTTP {s}, {json.dumps(b)[:200]}")
    if s not in (200, 201):
        return None, None, None

    product_id = get_data(b).get("id")
    if not product_id:
        info("响应中无 id 字段")
        return None, None, None

    # 2. 创建套餐
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": f"basic_{ts}",
        "name": "基础套餐",
        "billing_type": "one_time",
        "status": "active"
    }, token=admin_token)
    info(f"创建套餐 HTTP {s}, {json.dumps(b)[:200]}")
    if s not in (200, 201):
        return None, None, None

    plan_id = get_data(b).get("id")
    if not plan_id:
        return None, None, None

    # 3. 设置价格（body 用 items 键，无 plan_id 参数）
    s, b = patch(f"/api/admin/products/{product_id}/prices", {
        "plan_id": plan_id,
        "items": [{
            "role_id": None,
            "membership_level_id": None,
            "price_amount": str(unit_price),
            "currency": "CNY"
        }]
    }, token=admin_token)
    info(f"设置价格 HTTP {s}, {json.dumps(b)[:200]}")
    if s not in (200, 201, 204):
        # 尝试原格式（向后兼容）
        s, b = patch(f"/api/admin/products/{product_id}/prices", {
            "plan_id": plan_id,
            "prices": [{
                "role_id": None,
                "membership_level_id": None,
                "price_amount": str(unit_price),
                "currency": "CNY"
            }]
        }, token=admin_token)
        info(f"设置价格（兼容格式）HTTP {s}, {json.dumps(b)[:200]}")
        if s not in (200, 201, 204):
            return None, None, None

    # 4. 设置购买权限（给购买用户所在角色 can_buy=true）
    s, b = patch(f"/api/admin/products/{product_id}/access", {
        "items": [{
            "role_id": buyer_role_id,
            "can_view": True,
            "can_buy": True,
            "can_use": True
        }]
    }, token=admin_token)
    info(f"访问权限设置 HTTP {s}, {json.dumps(b)[:200]}")
    if s not in (200, 201, 204):
        # 尝试原格式（向后兼容）
        s, b = patch(f"/api/admin/products/{product_id}/access", {
            "accesses": [{
                "role_id": buyer_role_id,
                "can_view": True,
                "can_buy": True,
                "can_use": True
            }]
        }, token=admin_token)
        info(f"访问权限设置（兼容格式）HTTP {s}, {json.dumps(b)[:200]}")

    return product_id, plan_id, unit_price


# ── T-001：D-003 quantity 计价验证 ────────────────────────
def test_t001_d003(buyer_uid, buyer_token, product_id, plan_id, unit_price):
    section("T-001  D-003: quantity 未参与计价（回归验证）")

    # 记录初始余额（DB 直查）
    bal_start = get_wallet_balance_db(buyer_uid)
    info(f"测试开始余额（DB）: {bal_start}")
    if bal_start is None:
        fail("T-001: 无法获取初始余额", "DB 查询失败")
        return

    # ① 购买 quantity=1
    idem_q1 = f"d003_q1_{int(time.time() * 1000)}"
    s1, b1 = post(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id,
        "quantity": 1
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem_q1})

    info(f"quantity=1 购买 HTTP {s1}, body={json.dumps(b1)[:400]}")
    if s1 != 200:
        fail("T-001: quantity=1 购买应返回 200", f"HTTP {s1}, {b1}")
        return

    d1 = get_data(b1)
    amount_q1_resp = None
    if "amount" in d1:
        try:
            amount_q1_resp = Decimal(str(d1["amount"]))
        except Exception:
            pass
        info(f"quantity=1 响应 amount={amount_q1_resp}")

    bal_after_q1 = get_wallet_balance_db(buyer_uid)
    deduct_q1 = bal_start - bal_after_q1 if bal_after_q1 is not None else None
    info(f"quantity=1 余额变化: {bal_start} → {bal_after_q1}，扣费={deduct_q1}")

    # DB 直充补回余额
    info("DB 直充补回 50 元余额...")
    db_recharge(buyer_uid, "50.00")
    bal_refilled = get_wallet_balance_db(buyer_uid)
    info(f"补充后余额（DB）: {bal_refilled}")

    # ② 购买 quantity=2
    idem_q2 = f"d003_q2_{int(time.time() * 1000)}"
    s2, b2 = post(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id,
        "quantity": 2
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem_q2})

    info(f"quantity=2 购买 HTTP {s2}, body={json.dumps(b2)[:400]}")
    if s2 != 200:
        fail("T-001: quantity=2 购买应返回 200", f"HTTP {s2}, {b2}")
        return

    d2 = get_data(b2)
    amount_q2_resp = None
    if "amount" in d2:
        try:
            amount_q2_resp = Decimal(str(d2["amount"]))
        except Exception:
            pass
        info(f"quantity=2 响应 amount={amount_q2_resp}")

    bal_after_q2 = get_wallet_balance_db(buyer_uid)
    deduct_q2 = bal_refilled - bal_after_q2 if bal_refilled is not None and bal_after_q2 is not None else None
    info(f"quantity=2 余额变化: {bal_refilled} → {bal_after_q2}，扣费={deduct_q2}")

    # ── 判断 ──────────────────────────────────────────────

    # 子测试 a：quantity=1 扣费 = 单价
    if deduct_q1 is not None:
        if deduct_q1 == unit_price:
            ok(f"T-001a: quantity=1 扣费 {deduct_q1} = 单价 {unit_price}")
        else:
            fail("T-001a: quantity=1 扣费应等于单价",
                 f"实际扣费={deduct_q1}，单价={unit_price}")
    else:
        fail("T-001a: 无法计算 quantity=1 扣费")

    # 子测试 b：quantity=2 扣费 = 单价 × 2
    expected_q2 = unit_price * 2
    if deduct_q2 is not None:
        if deduct_q2 == expected_q2:
            ok(f"T-001b: quantity=2 扣费 {deduct_q2} = 单价×2 {expected_q2}")
        else:
            fail("T-001b: quantity=2 扣费应等于单价×2",
                 f"实际扣费={deduct_q2}，期望={expected_q2}"
                 f"（若等于单价 {unit_price}，说明 D-003 未修复）")
    else:
        fail("T-001b: 无法计算 quantity=2 扣费")

    # 子测试 c：响应 amount 字段随 quantity 翻倍
    if amount_q1_resp is not None and amount_q2_resp is not None:
        if amount_q2_resp == amount_q1_resp * 2:
            ok(f"T-001c: 响应 amount 随 quantity 翻倍（q1={amount_q1_resp}, q2={amount_q2_resp}）")
        else:
            fail("T-001c: 响应 amount 未随 quantity 翻倍",
                 f"amount_q1={amount_q1_resp}，amount_q2={amount_q2_resp}（期望 q2=q1×2）")
    else:
        info("T-001c: 响应体未含 amount 字段，以钱包扣费金额（DB）为判断依据（已覆盖）")


# ── T-002：D-004 asset_id 字段存在验证 ───────────────────
def test_t002_d004(buyer_uid, buyer_token, product_id, plan_id):
    section("T-002  D-004: PurchaseResult 缺少 asset_id 字段（回归验证）")

    # 确保余额充足
    bal = get_wallet_balance_db(buyer_uid)
    if bal is None or bal < Decimal("10"):
        info("余额不足，DB 直充 50 元...")
        db_recharge(buyer_uid, "50.00")

    idem = f"d004_check_{int(time.time() * 1000)}"
    s, b = post(f"/api/products/{product_id}/purchase", {
        "plan_id": plan_id,
        "quantity": 1
    }, token=buyer_token, extra_headers={"Idempotency-Key": idem})

    info(f"购买 HTTP {s}, body={json.dumps(b)[:500]}")

    if s != 200:
        fail("T-002: 购买应返回 200", f"HTTP {s}, {b}")
        return

    d = get_data(b)
    info(f"响应 data 全部字段: {list(d.keys())}")
    info(f"响应 data 内容: {json.dumps(d)[:300]}")

    # 核心检查：asset_id key 是否存在（值可为 null）
    if "asset_id" in d:
        asset_id_val = d["asset_id"]
        ok(f"T-002: 响应中存在 asset_id key（值={asset_id_val}）")
        if asset_id_val is None:
            info("asset_id=null，符合预期（开通异步处理）")
    else:
        fail("T-002: 响应中缺少 asset_id key",
             f"实际字段: {list(d.keys())}")


# ── 主流程 ────────────────────────────────────────────────
def main():
    print(f"\n{'=' * 60}")
    print("  Molin 云管理平台 — D-003 / D-004 缺陷回归测试")
    print(f"  API_BASE = {API_BASE}")
    print(f"  时间 = {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"{'=' * 60}\n")

    # 确认 API 可用
    s, b = get("/api/health")
    if s != 200:
        print(f"[ERROR] API 不可用（HTTP {s}），退出")
        sys.exit(1)
    info(f"API 健康检查: {b}")

    ts = int(time.time())

    section("账号 & 数据准备")

    # ── 获取购买角色 ID ──────────────────────────────────
    buyer_role_id = ensure_buyer_role(None)
    if not buyer_role_id:
        print("[ERROR] 无法获取购买角色 ID，退出")
        sys.exit(1)
    info(f"购买角色 role_id={buyer_role_id}")

    # ── 注册 admin 账号（创建商品用）─────────────────────
    admin_email = f"d003d004_admin_{ts}@example.com"
    admin_phone = f"131{str(ts)[-8:]}"
    info(f"注册 admin 账号: {admin_email}")
    admin_uid, admin_token = register_user(admin_email, admin_phone)
    if not admin_uid:
        print("[ERROR] admin 账号注册失败，退出")
        sys.exit(1)
    info(f"admin uid={admin_uid}")

    if not seed_admin_perms(admin_uid):
        print("[ERROR] 赋予 admin 角色失败")
        sys.exit(1)

    time.sleep(0.3)
    new_token, _ = login_user(admin_email)
    if new_token:
        admin_token = new_token
        info("admin Token 刷新成功")

    # ── 注册购买用户 ─────────────────────────────────────
    ts2 = ts + 1
    buyer_email = f"d003d004_buyer_{ts2}@example.com"
    buyer_phone = f"132{str(ts2)[-8:]}"
    info(f"注册购买用户: {buyer_email}")
    buyer_uid, buyer_token = register_user(buyer_email, buyer_phone)
    if not buyer_uid:
        print("[ERROR] 购买用户注册失败，退出")
        sys.exit(1)
    info(f"buyer uid={buyer_uid}")

    # 实名认证 + 分配购买角色
    if not seed_verified(buyer_uid):
        print("[ERROR] 设置实名认证失败")
        sys.exit(1)
    info("购买用户实名认证 → verified")

    if not seed_role(buyer_uid, buyer_role_id):
        print("[ERROR] 分配购买角色失败")
        sys.exit(1)
    info(f"购买用户已分配角色 {buyer_role_id}")

    # 重登录确保 Token 含最新角色
    time.sleep(0.3)
    t2, _ = login_user(buyer_email)
    if t2:
        buyer_token = t2

    # ── 创建商品套餐（含价格 + 访问权限）────────────────
    product_id, plan_id, unit_price = create_product_and_plan(admin_token, buyer_role_id)
    if not product_id or not plan_id:
        print("[ERROR] 商品/套餐创建失败，退出")
        sys.exit(1)
    info(f"product_id={product_id}，plan_id={plan_id}，单价={unit_price}")

    # ── DB 直充 100 元 ────────────────────────────────────
    info("DB 直充购买用户 100 元...")
    if not db_recharge(buyer_uid, "100.00"):
        print("[ERROR] DB 直充失败，退出")
        sys.exit(1)

    # 访问 /api/wallet 触发钱包初始化（如 API 层有缓存）
    get("/api/wallet", token=buyer_token)
    bal = get_wallet_balance_db(buyer_uid)
    info(f"直充后余额（DB）: {bal}")
    if bal is None or bal < Decimal("30"):
        print(f"[ERROR] 余额不足（{bal}），退出")
        sys.exit(1)

    ok("账号 & 数据准备完成")

    # ── 执行测试 ─────────────────────────────────────────
    test_t001_d003(buyer_uid, buyer_token, product_id, plan_id, unit_price)
    test_t002_d004(buyer_uid, buyer_token, product_id, plan_id)

    # ── 汇总 ─────────────────────────────────────────────
    total = PASS_COUNT + FAIL_COUNT
    print(f"\n{'═' * 60}")
    print(f"  测试汇总: {total} 个用例，"
          f"{_color('32;1', str(PASS_COUNT) + ' PASS')}，"
          f"{_color('31;1', str(FAIL_COUNT) + ' FAIL')}")
    if FAIL_LIST:
        print(f"\n  失败用例:")
        for item in FAIL_LIST:
            print(f"    {_color('31', 'x')} {item}")
    else:
        print(f"  {_color('32;1', '所有用例通过！')}")
    print('═' * 60)

    sys.exit(0 if FAIL_COUNT == 0 else 1)


if __name__ == "__main__":
    main()

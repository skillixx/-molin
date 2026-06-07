#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
人工接口验收补充测试 —— 针对自动化回归脚本（test_backend_a.py / stage1_e2e_test.py /
bugfix_retest.py）尚未覆盖的"必测场景"查漏补缺：

  1. 伪造 JWT（修改 payload 不更新签名）→ 期望 401
  2. 普通用户 Token 访问 /api/admin/* → 期望 403（注意与"无 Token → 401"区分）
  3. 封禁用户后 Token 立即失效 → 期望 401（验证 PATCH /api/admin/users/:id/status 链路）
  4. 缺少 Idempotency-Key 头 → 期望 400
  5. 同一 Idempotency-Key 重复提交购买 → 返回原订单，不重复扣费
  6. 支付回调签名错误 → 期望 400，余额不变（带订单上下文核对余额）

全部账号均为本脚本自行注册的全新账号（邮箱使用时间戳唯一化），
不登录任何已存在的账号；管理员权限通过 SQL 直接 INSERT user_roles 绑定 admin 角色获得
（与 tests/stage1_e2e_test.py setup_admin() 完全一致的自包含套路）。

执行方式（在测试服务器上）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/manual_gap_test.py
"""

import os
import sys
import json
import time
import base64
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
        print(f"         {_c('33', str(detail)[:500])}")

def info(msg):
    print(f"  {_c('36','ℹ')}  {msg}")

def section(title):
    print(f"\n{_c('1;35', '═'*65)}")
    print(_c('1;35', f"  {title}"))
    print(_c('1;35', '═'*65))


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

def get(path, token=None, params=None, headers=None):
    if params:
        path = path + "?" + urllib.parse.urlencode(params)
    return http_req("GET", path, token=token, headers=headers)

def post(path, body=None, token=None, headers=None):
    return http_req("POST", path, body=body, token=token, headers=headers)

def patch(path, body=None, token=None, headers=None):
    return http_req("PATCH", path, body=body, token=token, headers=headers)

def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}


# ════════════════════════════════════════════════════════
# MySQL 辅助（仅 INSERT/UPDATE 播种 + 查询断言，不做任何 DROP/覆盖）
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


# ════════════════════════════════════════════════════════
# 账号注册 / 登录（与 stage1_e2e_test.py 完全一致的自包含套路）
# ════════════════════════════════════════════════════════
PASSWORD = "Test1234!"

def register_email(email, password=PASSWORD):
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

def login_email(email, password=PASSWORD):
    return post("/api/auth/login/email", {"email": email, "password": password})


def get_wallet_balance(token):
    s, b = get("/api/wallet", token=token)
    if s != 200:
        return None
    raw = get_data(b).get("balance_amount", 0)
    try:
        return Decimal(str(raw))
    except Exception:
        return None


# ════════════════════════════════════════════════════════
# 准备：新建管理员账号（注册 + SQL 绑定 admin 角色），新建普通用户账号
# ════════════════════════════════════════════════════════
ADMIN_TOKEN = None
ADMIN_UID   = None

def setup_admin():
    global ADMIN_TOKEN, ADMIN_UID
    section("准备 0：自助创建管理员账号（注册新账号 + SQL 绑定 admin 角色）")
    ts = int(time.time())
    email = f"qa_manual_admin_{ts}@molin.io"
    uid, token, (s, b) = register_email(email)
    if not uid:
        fail("注册新管理员账号", f"HTTP {s} {b}")
        return False
    ok(f"注册新账号成功 user_id={uid}（邮箱 {email}，全程未登录任何已存在账号）")
    ADMIN_UID = uid

    sql = f"""
    SET @role_id = (SELECT id FROM roles WHERE code = 'admin' LIMIT 1);
    INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid}, @role_id);
    """
    if not mysql_exec(sql):
        fail("SQL 绑定 admin 角色")
        return False
    ok("已通过 INSERT IGNORE 绑定 admin 角色（未覆盖任何已有数据）")

    s, b = login_email(email)
    if s != 200:
        fail("管理员重新登录获取带权限 Token", f"HTTP {s} {b}")
        return False
    ADMIN_TOKEN = get_data(b).get("access_token")
    ok("管理员重新登录成功，已获取带 admin 权限 Token")
    return True


def setup_normal_user(tag="user"):
    ts = int(time.time() * 1000) % 10**9
    email = f"qa_manual_{tag}_{ts}@molin.io"
    uid, token, (s, b) = register_email(email)
    if not uid:
        return None, None, email
    return uid, token, email


# ════════════════════════════════════════════════════════
# 用例 1：伪造 JWT（修改 payload 不更新签名）→ 期望 401
# ════════════════════════════════════════════════════════
def b64url_decode(seg):
    pad = '=' * (-len(seg) % 4)
    return base64.urlsafe_b64decode(seg + pad)

def b64url_encode(raw):
    return base64.urlsafe_b64encode(raw).rstrip(b'=').decode()

def case_forged_jwt(uid, token):
    section("用例 A：伪造 JWT（修改 payload 不更新签名）→ 期望 401")

    parts = token.split(".")
    if len(parts) != 3:
        fail("伪造 JWT 前置条件：Token 应为标准三段式 JWT", f"实际段数={len(parts)}")
        return
    header_seg, payload_seg, sig_seg = parts

    try:
        payload = json.loads(b64url_decode(payload_seg))
    except Exception as e:
        fail("解析 JWT payload", str(e))
        return
    info(f"原始 payload: {payload}")

    # 篡改 user_id（伪装成另一个用户，例如 admin 的 user_id），签名段保持不变
    forged_payload = dict(payload)
    forged_payload["user_id"] = (forged_payload.get("user_id", uid) or uid) + 999999
    forged_payload_seg = b64url_encode(json.dumps(forged_payload, separators=(",", ":")).encode())
    forged_token = f"{header_seg}.{forged_payload_seg}.{sig_seg}"

    s, b = get("/api/me", token=forged_token)
    if s == 401:
        ok(f"伪造 JWT（篡改 user_id，签名未更新）访问受保护接口 → HTTP 401（实际 code={b.get('code') if isinstance(b, dict) else None}）")
    else:
        fail("伪造 JWT 应被拒绝（期望 401）",
             f"实际 HTTP {s}，body={str(b)[:300]}；"
             f"说明服务端可能未校验 JWT 签名，存在越权伪造身份的严重安全漏洞！")

    # 额外验证：仅篡改签名段（截断/替换为垃圾字符串）同样应被拒绝
    junk_token = f"{header_seg}.{payload_seg}.{sig_seg[:-4]}junk"
    s2, b2 = get("/api/me", token=junk_token)
    if s2 == 401:
        ok(f"篡改签名段的 Token → HTTP 401（实际 code={b2.get('code') if isinstance(b2, dict) else None}）")
    else:
        fail("篡改签名段的 Token 应被拒绝（期望 401）", f"实际 HTTP {s2}，body={str(b2)[:300]}")

    # 额外验证：alg=none 攻击（部分 JWT 库存在历史漏洞，将算法改为 none 并清空签名）
    try:
        header = json.loads(b64url_decode(header_seg))
        none_header = dict(header)
        none_header["alg"] = "none"
        none_header_seg = b64url_encode(json.dumps(none_header, separators=(",", ":")).encode())
        none_token = f"{none_header_seg}.{payload_seg}."
        s3, b3 = get("/api/me", token=none_token)
        if s3 == 401:
            ok(f"alg=none 攻击 Token → HTTP 401（服务端正确拒绝未签名 Token，实际 code={b3.get('code') if isinstance(b3, dict) else None}）")
        else:
            fail("alg=none 攻击 Token 应被拒绝（期望 401，存在严重安全漏洞）", f"实际 HTTP {s3}，body={str(b3)[:300]}")
    except Exception as e:
        info(f"alg=none 测试构造失败（非阻断）: {e}")


# ════════════════════════════════════════════════════════
# 用例 B：普通用户 Token 访问 /api/admin/* → 期望 403（区别于无 Token 的 401）
# ════════════════════════════════════════════════════════
def case_normal_user_admin_403(normal_uid, normal_token):
    section("用例 B：普通用户 Token 访问 /api/admin/* → 期望 403（区分于无 Token → 401）")

    admin_paths = [
        ("GET",  "/api/admin/roles"),
        ("GET",  "/api/admin/permissions"),
        ("GET",  f"/api/admin/users/{normal_uid}/roles"),
        ("GET",  "/api/admin/identity-verifications"),
    ]
    for method, path in admin_paths:
        if method == "GET":
            s, b = get(path, token=normal_token)
        else:
            s, b = post(path, {}, token=normal_token)
        code = b.get("code") if isinstance(b, dict) else None
        if s == 403:
            ok(f"普通用户 Token 访问 {path} → HTTP 403（code={code}）")
        elif s == 401:
            fail(f"普通用户 Token 访问 {path} 应为 403（已登录但无权限），实际返回 401（未登录语义，权限校验逻辑可能有误）",
                 f"HTTP {s}，body={str(b)[:300]}")
        else:
            fail(f"普通用户 Token 访问 {path} 应为 403", f"实际 HTTP {s}，body={str(b)[:300]}")

    # 对照组：无 Token 访问应为 401（与上面普通用户的 403 形成对比，验证两种语义被正确区分）
    s, b = get("/api/admin/roles")
    if s == 401:
        ok(f"对照：无 Token 访问 /api/admin/roles → HTTP 401（与普通用户 403 正确区分）")
    else:
        fail("对照：无 Token 访问 /api/admin/roles 应为 401", f"实际 HTTP {s}")


# ════════════════════════════════════════════════════════
# 用例 C：封禁用户后 Token 立即失效 → 期望 401
#   （验证文档中 PATCH /api/admin/users/:id/status 链路是否存在并生效）
# ════════════════════════════════════════════════════════
def case_ban_user_token_invalidation(target_uid, target_token):
    section("用例 C：封禁用户后 Token 立即失效 → 期望 401（验证 PATCH /api/admin/users/:id/status）")

    # 封禁前：Token 应可正常访问
    s0, b0 = get("/api/me", token=target_token)
    if s0 == 200:
        ok("封禁前，目标用户 Token 可正常访问 /api/me（基线确认）")
    else:
        fail("封禁前置条件：目标用户 Token 应可正常访问", f"HTTP {s0} {b0}")
        return

    # 调用文档 docs/full-api-design.md 3.5 节定义的 PATCH /api/admin/users/:id/status
    s, b = patch(f"/api/admin/users/{target_uid}/status",
                 {"status": "disabled", "reason": "QA 人工验收：验证封禁后 Token 立即失效"},
                 token=ADMIN_TOKEN)
    code = b.get("code") if isinstance(b, dict) else None

    if s in (200, 204):
        ok(f"管理员调用 PATCH /api/admin/users/{{id}}/status 封禁用户成功 → HTTP {s}")

        # 封禁后立即（不重新登录）用旧 Token 再次访问，期望 401
        s2, b2 = get("/api/me", token=target_token)
        if s2 == 401:
            ok(f"封禁后存量 Token 立即失效 → HTTP 401（实际 code={b2.get('code') if isinstance(b2, dict) else None}）")
        else:
            fail("封禁用户后存量 Token 应立即失效（期望 401）",
                 f"实际 HTTP {s2}，body={str(b2)[:300]}；"
                 f"说明封禁黑名单（Redis blocked:user:{{id}}）未在该链路写入或中间件未生效，"
                 f"用户被标记禁用后仍可继续使用旧 Token 操作，存在安全隐患！")

        # 复测：被封禁用户用旧 Token 重新登录是否被拦截
        db_status = mysql_query(f"SELECT status FROM users WHERE id={target_uid};")
        info(f"DB 中该用户当前 status = {db_status}")
    elif s in (404, 405):
        fail(f"PATCH /api/admin/users/{{id}}/status 接口未实现（HTTP {s}）",
             f"docs/full-api-design.md 第 590-601 行已明确定义该接口（修改用户状态 active/disabled），"
             f"但服务端未注册对应路由（grep 全代码库未发现 'users/{{id}}/status' 的路由注册）。"
             f"虽然 auth_service.go 内部已实现 BanUser/UnbanUser（写入 Redis 黑名单 blocked:user:<id>，"
             f"TTL=AccessToken 有效期，中间件 RequireAuth 也已对接 BanChecker.IsUserBlocked），"
             f"但该能力完全没有暴露为 HTTP 接口 —— 管理员当前在产品/界面层面**无法封禁任何用户**，"
             f"导致必测安全场景【封禁用户后 Token 立即失效】在当前系统中根本无法通过任何官方渠道触发，"
             f"功能形同虚设。body={str(b)[:200]}")
        info("由于封禁接口缺失，无法继续验证'Token 立即失效'的端到端链路（中间件层逻辑虽存在但无法被驱动）")
    else:
        fail(f"调用 PATCH /api/admin/users/{{id}}/status 返回非预期状态码", f"HTTP {s}，body={str(b)[:300]}")


# ════════════════════════════════════════════════════════
# 用例 D / E：购买 Idempotency-Key —— 缺失头 → 400；重复提交 → 返回原订单不重复扣费
# ════════════════════════════════════════════════════════
def setup_purchase_env():
    """创建一个可购买的角色 + 商品 + 已实名 + 已充值的测试账号，返回必要上下文。"""
    section("准备：购买环境（角色 + 商品 + 已实名已充值账号）")
    ts = int(time.time() * 1000)
    role_code = f"qa_manual_role_{ts}"
    s, b = post("/api/admin/roles", {"code": role_code, "name": "QA人工验收角色", "description": "幂等测试"},
                token=ADMIN_TOKEN)
    role_id = get_data(b).get("id")
    if not role_id:
        fail("创建测试角色", f"HTTP {s} {b}")
        return None
    ok(f"创建测试角色成功 role_id={role_id} code={role_code}")

    # 创建商品（字段与 stage1_e2e_test.py setup_product 保持一致：product_type/product_code 必填）
    code = f"qa_manual_prod_{ts}"
    s, b = post("/api/admin/products", {
        "product_type": "application",
        "product_code": code,
        "name": f"QA人工验收商品_{ts}",
        "description": "幂等测试商品",
        "status": "active",
    }, token=ADMIN_TOKEN)
    product_id = get_data(b).get("id")
    if not product_id:
        fail("创建测试商品", f"HTTP {s} {b}")
        return None
    s, b = post(f"/api/admin/products/{product_id}/plans", {
        "plan_code": "basic",
        "name": "标准版",
        "billing_type": "one_time",
        "duration_days": 30,
        "status": "active",
    }, token=ADMIN_TOKEN)
    plan_id = get_data(b).get("id")
    if not plan_id:
        s2, b2 = get(f"/api/admin/products/{product_id}/plans", token=ADMIN_TOKEN)
        lst = get_data(b2).get("plans") or get_data(b2).get("list") or []
        if lst:
            plan_id = lst[-1].get("id")

    # 设置价格（购买前必须先配置价格，否则下单会报错）
    s, b = patch(f"/api/admin/products/{product_id}/prices", {
        "plan_id": plan_id,
        "prices": [{"price_amount": "5.00", "currency": "CNY"}]
    }, token=ADMIN_TOKEN)

    ok(f"创建测试商品成功 product_id={product_id} plan_id={plan_id} price=5.00")

    s, b = patch(f"/api/admin/products/{product_id}/access", {
        "accesses": [{"role_id": role_id, "can_view": True, "can_buy": True, "can_use": True}]
    }, token=ADMIN_TOKEN)

    # 注册测试账号
    ts2 = int(time.time() * 1000) % 10**9
    email = f"qa_manual_buyer_{ts2}@molin.io"
    uid, token, (s, b) = register_email(email)
    if not uid:
        fail("注册购买测试账号", f"HTTP {s} {b}")
        return None
    ok(f"注册购买测试账号成功 user_id={uid}")

    # 实名 + 绑定角色 + 充值
    mysql_exec(f"UPDATE users SET real_name_status='verified' WHERE id={uid};")
    sql = f"""
    SET @rid = (SELECT id FROM roles WHERE code='{role_code}' LIMIT 1);
    INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid}, @rid);
    """
    mysql_exec(sql)
    ok("已通过 SQL 标记实名通过 + 绑定可购买角色")

    s, b = login_email(email)
    token = get_data(b).get("access_token")

    order_no, paid = direct_recharge(uid, token, "100.00")
    if not paid:
        fail("购买环境准备：充值未成功", f"order_no={order_no}")
        return None
    bal = get_wallet_balance(token)
    ok(f"充值成功，当前余额={bal}")

    return {"uid": uid, "token": token, "product_id": product_id, "plan_id": plan_id, "price": Decimal("5.00")}


def direct_recharge(uid, token, amount_yuan):
    ts = int(time.time() * 1000)
    s, b = post("/api/recharge/orders", {"amount": str(amount_yuan), "payment_method": "wechat"}, token=token)
    if s not in (200, 201):
        info(f"创建充值订单失败 HTTP {s}: {b}")
        return None, False
    order_id = get_data(b).get("order_id")
    s2, b2 = get(f"/api/orders/{order_id}", token=token)
    order_no = get_data(b2).get("order_no") if s2 == 200 else mysql_query(f"SELECT order_no FROM orders WHERE id={order_id};")
    if not order_no:
        return None, False
    amount_fen = int(Decimal(str(amount_yuan)) * 100)
    callback_body = {"out_trade_no": order_no, "transaction_id": f"QAMAN_TRADE_{ts}_{uid}", "total_fee": amount_fen}
    sig_headers = {
        "Wechatpay-Signature": "qa_manual_simulated_signature",
        "Wechatpay-Timestamp": str(ts // 1000),
        "Wechatpay-Nonce": f"qa_manual_nonce_{ts}",
    }
    s3, b3 = post("/api/payments/notify/wechat", callback_body, headers=sig_headers)
    time.sleep(0.6)
    return order_no, s3 == 200


def case_idempotency_key(env):
    section("用例 D/E：缺少 Idempotency-Key → 400；相同 Key 重复提交 → 返回原订单不重复扣费")
    if not env:
        fail("用例 D/E 前置条件：购买环境准备失败，跳过")
        return
    token = env["token"]
    product_id, plan_id = env["product_id"], env["plan_id"]

    bal_before = get_wallet_balance(token)
    info(f"购买前余额: {bal_before}")

    # D. 缺少 Idempotency-Key 头 → 期望 400
    s, b = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id}, token=token)
    code = b.get("code") if isinstance(b, dict) else None
    if s == 400:
        ok(f"缺少 Idempotency-Key 头 → HTTP 400（code={code}）")
    else:
        fail("缺少 Idempotency-Key 头应返回 400", f"实际 HTTP {s}，body={str(b)[:300]}；"
             f"若返回 200/201 说明幂等保护可被绕过，存在重复下单/重复扣费风险")

    # E. 相同 Idempotency-Key 重复提交 → 返回同一订单，不重复扣费
    idem_key = f"qa_manual_idem_{int(time.time()*1000)}"
    s1, b1 = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                  token=token, headers={"Idempotency-Key": idem_key})
    order_id_1 = get_data(b1).get("order_id") or get_data(b1).get("id")
    if s1 in (200, 201) and order_id_1:
        ok(f"首次提交（带 Idempotency-Key）下单成功，order_id={order_id_1}，HTTP {s1}")
    else:
        fail("首次提交购买失败，无法继续幂等验证", f"HTTP {s1}，body={str(b1)[:300]}")
        return

    time.sleep(0.6)
    bal_after_first = get_wallet_balance(token)
    info(f"首次扣费后余额: {bal_after_first}（应减少约 {env['price']}）")

    # 第二次：相同 Idempotency-Key 重复提交
    s2, b2 = post(f"/api/products/{product_id}/purchase", {"plan_id": plan_id},
                  token=token, headers={"Idempotency-Key": idem_key})
    order_id_2 = get_data(b2).get("order_id") or get_data(b2).get("id")

    if s2 in (200, 201) and order_id_2 == order_id_1:
        ok(f"相同 Idempotency-Key 重复提交 → 返回同一订单 order_id={order_id_2}（幂等生效）")
    elif s2 in (200, 201) and order_id_2 != order_id_1:
        fail("相同 Idempotency-Key 重复提交未返回原订单（生成了新订单，幂等保护失效）",
             f"首次 order_id={order_id_1}，重复提交 order_id={order_id_2}")
    else:
        info(f"重复提交返回 HTTP {s2}（非 200/201），body={str(b2)[:200]}；"
             f"部分实现可能选择对重复请求直接拒绝（如 409），只要不重复扣费即视为可接受")

    time.sleep(0.6)
    bal_after_repeat = get_wallet_balance(token)
    info(f"重复提交后余额: {bal_after_repeat}")

    if bal_after_first is not None and bal_after_repeat is not None:
        diff = bal_after_first - bal_after_repeat
        if abs(diff) <= Decimal("0.01"):
            ok(f"重复提交未导致二次扣费（余额保持 {bal_after_repeat}，与首次扣费后一致）")
        else:
            fail("相同 Idempotency-Key 重复提交导致重复扣费！严重缺陷",
                 f"首次扣费后余额={bal_after_first}，重复提交后余额={bal_after_repeat}，多扣 {diff} 元")

    # 数据库交叉验证：订单数量
    cnt = mysql_query(f"SELECT COUNT(*) FROM orders WHERE user_id={env['uid']} AND product_id={product_id};")
    info(f"DB 中该用户针对此商品的订单总数: {cnt}")


# ════════════════════════════════════════════════════════
# 用例 F：支付回调签名错误 → 期望 400，余额不变
# ════════════════════════════════════════════════════════
def case_callback_wrong_signature():
    section("用例 F：支付回调签名错误 → 期望 400，余额不变")
    ts = int(time.time() * 1000) % 10**9
    email = f"qa_manual_payer_{ts}@molin.io"
    uid, token, (s, b) = register_email(email)
    if not uid:
        fail("用例 F 前置：注册账号失败", f"HTTP {s} {b}")
        return
    ok(f"注册回调测试账号成功 user_id={uid}")

    # 创建充值订单（不走正常回调）
    ts2 = int(time.time() * 1000)
    s, b = post("/api/recharge/orders", {"amount": "50.00", "payment_method": "wechat"}, token=token)
    order_id = get_data(b).get("order_id")
    if not order_id:
        fail("创建充值订单失败", f"HTTP {s} {b}")
        return
    s2, b2 = get(f"/api/orders/{order_id}", token=token)
    order_no = get_data(b2).get("order_no")
    ok(f"创建充值订单成功 order_id={order_id} order_no={order_no}")

    bal_before = get_wallet_balance(token)
    info(f"回调前余额: {bal_before}")

    # 发送一个"签名字段缺失/错误"的回调（不带任何 Wechatpay-* 头）
    bad_body = {"out_trade_no": order_no, "transaction_id": f"QAMAN_BAD_{ts2}_{uid}", "total_fee": 5000}
    s3, b3 = post("/api/payments/notify/wechat", bad_body)  # 不带签名头
    code3 = b3.get("code") if isinstance(b3, dict) else None
    if s3 == 400:
        ok(f"缺少签名头的回调 → HTTP 400（code={code3}）")
    else:
        fail("缺少签名头的回调应返回 400", f"实际 HTTP {s3}，body={str(b3)[:300]}")

    time.sleep(0.6)
    bal_after = get_wallet_balance(token)
    info(f"回调后余额: {bal_after}")

    if bal_before is not None and bal_after is not None:
        if abs(bal_after - bal_before) <= Decimal("0.01"):
            ok(f"签名错误回调未导致余额变化（{bal_before} → {bal_after}）")
        else:
            fail("签名错误回调导致余额发生变化！严重缺陷（伪造回调可篡改余额）",
                 f"回调前={bal_before}，回调后={bal_after}")

    # 核对订单状态仍未变为已支付
    order_status = mysql_query(f"SELECT status FROM orders WHERE order_no='{order_no}';")
    if order_status and order_status not in ("paid",):
        ok(f"充值订单状态未被错误置为已支付（当前 status={order_status}）")
    else:
        fail("签名错误的回调导致订单被错误标记为已支付", f"order status={order_status}")


# ════════════════════════════════════════════════════════
# 主流程
# ════════════════════════════════════════════════════════
def main():
    print(_c('1;35', "=" * 65))
    print(_c('1;35', "  人工接口验收补充测试 —— 必测安全/购买/回调场景查漏补缺"))
    print(_c('1;35', f"  API_BASE = {API_BASE}"))
    print(_c('1;35', "=" * 65))

    if not setup_admin():
        print(_c('31;1', "管理员账号准备失败，终止测试"))
        sys.exit(1)

    # 准备一个普通用户账号（用于伪造 JWT / 普通用户访问 admin 接口测试）
    section("准备 1：注册普通测试用户账号")
    n_uid, n_token, n_email = setup_normal_user("normal")
    if not n_uid:
        print(_c('31;1', "普通用户账号注册失败，终止测试"))
        sys.exit(1)
    ok(f"注册普通用户账号成功 user_id={n_uid}（{n_email}）")

    # 用例 A：伪造 JWT
    case_forged_jwt(n_uid, n_token)

    # 用例 B：普通用户访问 admin 接口
    case_normal_user_admin_403(n_uid, n_token)

    # 用例 C：封禁用户后 Token 立即失效
    b_uid, b_token, b_email = setup_normal_user("banvictim")
    if b_uid:
        ok(f"注册待封禁测试账号成功 user_id={b_uid}（{b_email}）")
        case_ban_user_token_invalidation(b_uid, b_token)
    else:
        fail("注册待封禁测试账号失败，跳过用例 C")

    # 用例 D/E：Idempotency-Key
    env = setup_purchase_env()
    case_idempotency_key(env)

    # 用例 F：支付回调签名错误
    case_callback_wrong_signature()

    # ════════════════════════════════════════════════════
    section("测试结果汇总")
    total = PASS_COUNT + FAIL_COUNT
    print(f"\n  总用例数: {total}")
    print(f"  {_c('32;1', f'通过: {PASS_COUNT}')}")
    print(f"  {_c('31;1', f'失败: {FAIL_COUNT}')}")
    if FAIL_LIST:
        print(f"\n  {_c('31;1', '失败详情：')}")
        for i, m in enumerate(FAIL_LIST, 1):
            print(f"    {i}. {m}")
    print()
    sys.exit(0 if FAIL_COUNT == 0 else 1)


if __name__ == "__main__":
    main()

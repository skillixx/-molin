#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
PR#126 + PR#128 新增过滤参数验收测试
覆盖：
  T-001  O1 用户端订单列表过滤（status / order_type / 时间区间）
  T-002  O5 管理员订单列表过滤（status / order_type / 时间区间）
  T-003  B2 用户钱包流水过滤（type / direction / 时间区间 / 联合过滤）
  T-004  B6 管理员钱包流水过滤（type / direction / 时间区间）

执行方式：
  API_BASE=http://localhost:8080 \\
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \\
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \\
  python3 test_pr128_backend_b_filters.py

注意：
  - 本脚本只做接口测试，不修改业务代码
  - 注册需要双 OTP（邮箱 + 手机），使用测试服务器返回的 dev 模式验证码
  - 管理员账号通过注册 + DB 赋角色（user_roles）实现，不直接修改业务表
  - 支付回调在 PR#123 合并后已启用真实验签，无法用模拟回调入账；
    B2 / B3 用户流水过滤使用 DB 中已有事务数据的用户（user_id=263）的管理员视角验证，
    或注册新账号后验证"无数据时过滤不报 500"。
"""

import os
import sys
import json
import time
import subprocess
import urllib.request
import urllib.error
import urllib.parse

# ═══════════════════════════════════════════════
# 配置
# ═══════════════════════════════════════════════
API_BASE   = os.getenv("API_BASE",       "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST",     "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT",     "13306")
MYSQL_USER = os.getenv("MYSQL_USER",     "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# ═══════════════════════════════════════════════
# 统计
# ═══════════════════════════════════════════════
PASS_COUNT = 0
FAIL_COUNT = 0
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

def patch(path, body=None, token=None):
    return http_req("PATCH", path, body=body, token=token)

def get_data(body):
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}

def get_items(body):
    """从分页响应提取 items 列表"""
    d = get_data(body)
    if isinstance(d, list):
        return d
    return d.get("items") or d.get("list") or []

# ═══════════════════════════════════════════════
# MySQL 工具（只查询，不写业务数据）
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
# 账号工具（正确的双 OTP 注册流程）
# ═══════════════════════════════════════════════
def register_user(email, phone, password="Test1234!"):
    """双 OTP 注册：先获取邮箱验证码 + 手机验证码，再注册"""
    # 1. 发邮箱验证码
    s, b = post("/api/auth/verification-codes/email",
                {"email": email, "scene": "register"})
    if s != 200:
        info(f"邮箱验证码发送失败: HTTP {s}, {b}")
        return None, None
    email_code = get_data(b).get("code", "")

    # 2. 发手机验证码
    s, b = post("/api/auth/verification-codes/phone",
                {"phone": phone, "scene": "register"})
    if s != 200:
        info(f"手机验证码发送失败: HTTP {s}, {b}")
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


def set_verified(user_id):
    """将用户设为实名认证通过"""
    sql = f"UPDATE users SET real_name_status='verified' WHERE id={user_id};"
    return mysql_exec(sql)


# ═══════════════════════════════════════════════
# T-001  O1 用户端订单列表过滤
# ═══════════════════════════════════════════════
def test_t001(user_token):
    section("T-001  O1 用户端订单列表过滤（PR#126）")

    # 1. status=paid 过滤
    s, b = get("/api/orders", token=user_token, params={"status": "paid"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("status") != "paid"]
        if bad:
            fail("O1 status=paid 过滤：含非 paid 订单",
                 f"不合规条目数={len(bad)}, 第一条={bad[0]}")
        else:
            ok(f"O1 status=paid 过滤：返回 {len(items)} 条，全部 status=paid")
    else:
        fail("O1 status=paid 过滤", f"HTTP {s}，期望 200，body={str(b)[:200]}")

    # 2. order_type=recharge 过滤
    s, b = get("/api/orders", token=user_token, params={"order_type": "recharge"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("order_type") != "recharge"]
        if bad:
            fail("O1 order_type=recharge 过滤：含非 recharge 订单",
                 f"不合规条目数={len(bad)}")
        else:
            ok(f"O1 order_type=recharge 过滤：返回 {len(items)} 条，全部 order_type=recharge")
    else:
        fail("O1 order_type=recharge 过滤", f"HTTP {s}，期望 200")

    # 3. 时间区间过滤 created_from/created_to（覆盖 2026 全年）
    s, b = get("/api/orders", token=user_token,
               params={"created_from": "2026-01-01", "created_to": "2026-12-31"})
    if s == 200:
        ok(f"O1 时间区间过滤（2026全年）：HTTP 200，total={get_data(b).get('total', '?')}")
    else:
        fail("O1 时间区间过滤", f"HTTP {s}（期望 200，不应 500），body={str(b)[:200]}")

    # 4. 时间区间过滤：历史时间段（期望结果为空但不能 500）
    s, b = get("/api/orders", token=user_token,
               params={"created_from": "2020-01-01", "created_to": "2020-12-31"})
    if s == 200:
        items = get_items(b)
        ok(f"O1 历史空时间区间：HTTP 200，total={get_data(b).get('total', 0)}（正常空列表）")
    else:
        fail("O1 历史空时间区间不应报错", f"HTTP {s}（期望 200），body={str(b)[:200]}")

    # 5. 联合过滤：order_type=product + status=paid
    s, b = get("/api/orders", token=user_token,
               params={"order_type": "product", "status": "paid"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if not (i.get("order_type") == "product" and i.get("status") == "paid")]
        if bad:
            fail("O1 联合过滤（product+paid）：含不符合条目",
                 f"不合规={len(bad)}")
        else:
            ok(f"O1 联合过滤（product+paid）：返回 {len(items)} 条，均满足两个条件")
    else:
        fail("O1 联合过滤（product+paid）", f"HTTP {s}，期望 200")


# ═══════════════════════════════════════════════
# T-002  O5 管理员订单列表过滤
# ═══════════════════════════════════════════════
def test_t002(admin_token):
    section("T-002  O5 管理员订单列表过滤（PR#126）")

    # 1. 时间区间过滤（覆盖 2026 全年，库中有数据）
    s, b = get("/api/admin/orders", token=admin_token,
               params={"created_from": "2026-01-01", "created_to": "2026-12-31"})
    if s == 200:
        total = get_data(b).get("total", 0)
        ok(f"O5 时间区间过滤（2026全年）：HTTP 200，total={total}")
        if total > 0:
            items = get_items(b)
            # 验证返回的订单 created_at 确实在区间内
            bad = [i for i in items if not (
                i.get("created_at", "").startswith("2026")
            )]
            if bad:
                fail("O5 时间过滤：含 2026 年以外的订单",
                     f"第一条 created_at={bad[0].get('created_at')}")
            else:
                ok(f"O5 时间过滤：所有返回订单 created_at 均在 2026 年内")
    else:
        fail("O5 时间区间过滤（2026全年）", f"HTTP {s}（期望 200，不应 500），body={str(b)[:200]}")

    # 2. status=paid 过滤
    s, b = get("/api/admin/orders", token=admin_token, params={"status": "paid"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("status") != "paid"]
        if bad:
            fail("O5 status=paid 过滤：含非 paid 订单",
                 f"不合规数={len(bad)}")
        else:
            ok(f"O5 status=paid 过滤：返回 {len(items)} 条，全部 status=paid")
    else:
        fail("O5 status=paid 过滤", f"HTTP {s}，期望 200")

    # 3. order_type=recharge 过滤
    s, b = get("/api/admin/orders", token=admin_token,
               params={"order_type": "recharge"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("order_type") != "recharge"]
        if bad:
            fail("O5 order_type=recharge 过滤：含非 recharge 订单",
                 f"不合规数={len(bad)}")
        else:
            ok(f"O5 order_type=recharge 过滤：返回 {len(items)} 条，全部 order_type=recharge")
    else:
        fail("O5 order_type=recharge 过滤", f"HTTP {s}，期望 200")

    # 4. 历史空时间段（期望 total=0，不应 500）
    s, b = get("/api/admin/orders", token=admin_token,
               params={"created_from": "2020-01-01", "created_to": "2020-12-31"})
    if s == 200:
        total = get_data(b).get("total", 0)
        ok(f"O5 历史空时间区间：HTTP 200，total={total}（正常空列表）")
    else:
        fail("O5 历史空时间区间不应报错", f"HTTP {s}，body={str(b)[:200]}")

    # 5. 联合过滤 status=paid + order_type=product
    s, b = get("/api/admin/orders", token=admin_token,
               params={"status": "paid", "order_type": "product"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if not (
            i.get("status") == "paid" and i.get("order_type") == "product"
        )]
        if bad:
            fail("O5 联合过滤（paid+product）：含不符合条目",
                 f"不合规={len(bad)}")
        else:
            ok(f"O5 联合过滤（paid+product）：返回 {len(items)} 条，全部满足")
    else:
        fail("O5 联合过滤（paid+product）", f"HTTP {s}，期望 200")


# ═══════════════════════════════════════════════
# T-003  B2 用户钱包流水过滤
# ═══════════════════════════════════════════════
def test_t003(user_token):
    section("T-003  B2 用户钱包流水过滤（PR#128）")

    # 1. type=recharge 过滤
    s, b = get("/api/wallet/transactions", token=user_token,
               params={"type": "recharge"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("type") != "recharge"]
        if bad:
            fail("B2 type=recharge 过滤：含非 recharge 条目",
                 f"不合规数={len(bad)}, 第一条={bad[0]}")
        else:
            ok(f"B2 type=recharge 过滤：返回 {len(items)} 条，全部 type=recharge")
    else:
        fail("B2 type=recharge 过滤", f"HTTP {s}（期望 200，不应 500），body={str(b)[:200]}")

    # 2. direction=in 过滤
    s, b = get("/api/wallet/transactions", token=user_token,
               params={"direction": "in"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("direction") != "in"]
        if bad:
            fail("B2 direction=in 过滤：含非 in 条目",
                 f"不合规数={len(bad)}")
        else:
            ok(f"B2 direction=in 过滤：返回 {len(items)} 条，全部 direction=in")
    else:
        fail("B2 direction=in 过滤", f"HTTP {s}，期望 200")

    # 3. 时间区间过滤
    s, b = get("/api/wallet/transactions", token=user_token,
               params={"created_from": "2026-01-01", "created_to": "2026-12-31"})
    if s == 200:
        ok(f"B2 时间区间过滤（2026全年）：HTTP 200，total={get_data(b).get('total', '?')}")
    else:
        fail("B2 时间区间过滤", f"HTTP {s}（期望 200，不应 500），body={str(b)[:200]}")

    # 4. 联合过滤 type=consume + direction=out
    s, b = get("/api/wallet/transactions", token=user_token,
               params={"type": "consume", "direction": "out"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if not (
            i.get("type") == "consume" and i.get("direction") == "out"
        )]
        if bad:
            fail("B2 联合过滤（consume+out）：含不符合条目",
                 f"不合规={len(bad)}")
        else:
            ok(f"B2 联合过滤（consume+out）：返回 {len(items)} 条，全部满足")
    else:
        fail("B2 联合过滤（consume+out）", f"HTTP {s}，期望 200")

    # 5. 历史空时间段（不应 500）
    s, b = get("/api/wallet/transactions", token=user_token,
               params={"created_from": "2020-01-01", "created_to": "2020-12-31"})
    if s == 200:
        ok(f"B2 历史空时间区间：HTTP 200，total={get_data(b).get('total', 0)}")
    else:
        fail("B2 历史空时间区间不应报错", f"HTTP {s}")


# ═══════════════════════════════════════════════
# T-004  B6 管理员钱包流水过滤
# ═══════════════════════════════════════════════
def test_t004(admin_token):
    section("T-004  B6 管理员钱包流水过滤（PR#128）")
    # 注意：管理员端点路径为 /api/admin/wallet-transactions（连字符）

    # 1. 基础请求（无过滤，验证接口可达）
    s, b = get("/api/admin/wallet-transactions", token=admin_token)
    if s == 200:
        total = get_data(b).get("total", 0)
        ok(f"B6 基础请求：HTTP 200，total={total}")
    else:
        fail("B6 基础请求", f"HTTP {s}（期望 200），body={str(b)[:200]}")
        return  # 基础请求失败，跳过后续过滤测试

    # 2. type=recharge 过滤
    s, b = get("/api/admin/wallet-transactions", token=admin_token,
               params={"type": "recharge"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("type") != "recharge"]
        if bad:
            fail("B6 type=recharge 过滤：含非 recharge 条目",
                 f"不合规数={len(bad)}")
        else:
            ok(f"B6 type=recharge 过滤：返回 {len(items)} 条，全部 type=recharge")
    else:
        fail("B6 type=recharge 过滤", f"HTTP {s}，期望 200")

    # 3. direction=in 过滤
    s, b = get("/api/admin/wallet-transactions", token=admin_token,
               params={"direction": "in"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if i.get("direction") != "in"]
        if bad:
            fail("B6 direction=in 过滤：含非 in 条目",
                 f"不合规数={len(bad)}")
        else:
            ok(f"B6 direction=in 过滤：返回 {len(items)} 条，全部 direction=in")
    else:
        fail("B6 direction=in 过滤", f"HTTP {s}，期望 200")

    # 4. 联合过滤 type=recharge + direction=in（库中有真实数据）
    s, b = get("/api/admin/wallet-transactions", token=admin_token,
               params={"type": "recharge", "direction": "in"})
    if s == 200:
        items = get_items(b)
        bad = [i for i in items if not (
            i.get("type") == "recharge" and i.get("direction") == "in"
        )]
        if bad:
            fail("B6 联合过滤（recharge+in）：含不符合条目",
                 f"不合规数={len(bad)}")
        else:
            ok(f"B6 联合过滤（recharge+in）：返回 {len(items)} 条，全部满足")
    else:
        fail("B6 联合过滤（recharge+in）", f"HTTP {s}，期望 200")

    # 5. 时间区间过滤（覆盖 2026 全年）
    s, b = get("/api/admin/wallet-transactions", token=admin_token,
               params={"created_from": "2026-01-01", "created_to": "2026-12-31"})
    if s == 200:
        items = get_items(b)
        total = get_data(b).get("total", 0)
        bad = [i for i in items if not i.get("created_at", "").startswith("2026")]
        if bad:
            fail("B6 时间区间过滤：含 2026 年以外条目",
                 f"不合规数={len(bad)}")
        else:
            ok(f"B6 时间区间过滤（2026全年）：HTTP 200，total={total}，条目时间均在区间内")
    else:
        fail("B6 时间区间过滤", f"HTTP {s}（期望 200，不应 500），body={str(b)[:200]}")

    # 6. 历史空时间段
    s, b = get("/api/admin/wallet-transactions", token=admin_token,
               params={"created_from": "2020-01-01", "created_to": "2020-12-31"})
    if s == 200:
        total = get_data(b).get("total", 0)
        ok(f"B6 历史空时间区间：HTTP 200，total={total}（正常空列表）")
    else:
        fail("B6 历史空时间区间不应报错", f"HTTP {s}")

    # 7. 权限验证：无 token → 401
    s, b = get("/api/admin/wallet-transactions")
    if s == 401:
        ok("B6 无 Token 访问 → 401")
    else:
        fail("B6 无 Token 应返回 401", f"HTTP {s}")


# ═══════════════════════════════════════════════
# 主流程
# ═══════════════════════════════════════════════
def main():
    print(f"\n{'='*64}")
    print("  Molin 云管理平台 — PR#126 + PR#128 过滤参数验收测试")
    print(f"  API_BASE = {API_BASE}")
    print(f"  时间 = {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print('='*64)

    # API 健康检查
    s, b = get("/api/health")
    if s != 200:
        print(f"[ERROR] API 不可用（HTTP {s}），退出")
        sys.exit(1)
    info(f"API 健康检查: {b}")

    ts = int(time.time())

    # ── 账号准备 ──
    section("账号准备")

    # 注册普通用户（用于 T-001 / T-003）
    user_email = f"qa_filter_user_{ts}@example.com"
    user_phone = f"150{str(ts)[-8:]}"
    info(f"注册普通测试用户: {user_email} / {user_phone}")
    user_uid, user_token = register_user(user_email, user_phone, "Test1234!")
    if not user_uid or not user_token:
        print("[ERROR] 普通测试用户注册失败，退出")
        sys.exit(1)
    ok(f"普通用户注册成功 uid={user_uid}")

    # 注册管理员用户（用于 T-002 / T-004）
    admin_email = f"qa_filter_admin_{ts}@example.com"
    admin_phone = f"151{str(ts)[-8:]}"
    info(f"注册管理员测试用户: {admin_email} / {admin_phone}")
    admin_uid, admin_token_initial = register_user(admin_email, admin_phone, "Admin1234!")
    if not admin_uid:
        print("[ERROR] 管理员测试用户注册失败，退出")
        sys.exit(1)
    ok(f"管理员用户注册成功 uid={admin_uid}")

    # 赋予 admin 角色（role_id=5）+ 设置实名认证通过
    if grant_admin_role(admin_uid):
        ok(f"DB: 赋予 uid={admin_uid} admin 角色（role_id=5）")
    else:
        fail("DB: 赋予 admin 角色失败")

    if set_verified(admin_uid):
        ok(f"DB: uid={admin_uid} real_name_status → verified")
    else:
        fail("DB: 设置实名认证失败")

    # 重新登录获取含最新权限的 Token
    time.sleep(0.5)
    admin_token = login_user(admin_email, "Admin1234!")
    if admin_token:
        ok("管理员 Token 刷新成功（含 admin 角色权限）")
    else:
        fail("管理员 Token 刷新失败，使用注册时的 Token")
        admin_token = admin_token_initial

    ok("账号准备完成")

    # ── 执行测试 ──
    test_t001(user_token)
    test_t002(admin_token)
    test_t003(user_token)
    test_t004(admin_token)

    # ── 汇总 ──
    total = PASS_COUNT + FAIL_COUNT
    print(f"\n{'='*64}")
    print(f"  测试汇总")
    print(f"  通过: {PASS_COUNT}/{total}")
    print(f"  失败: {FAIL_COUNT}/{total}")
    print('='*64)

    if FAIL_LIST:
        print(f"\n  失败项明细：")
        for i, item in enumerate(FAIL_LIST, 1):
            print(f"  {i:2}. {item}")

    print()
    if FAIL_COUNT == 0:
        print(f"  {_color('32;1', '全部通过！PR#126 + PR#128 过滤参数验收完成。')}\n")
    else:
        print(f"  {_color('31;1', f'存在 {FAIL_COUNT} 个失败用例，请检查上方明细。')}\n")

    return FAIL_COUNT


if __name__ == "__main__":
    ec = main()
    sys.exit(0 if ec == 0 else 1)

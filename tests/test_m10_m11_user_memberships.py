#!/usr/bin/env python3
"""
后端丙会员模块 M10/M11 管理端接口测试。

被测接口（docs/frontend-api-reference.md §11.6，权限码 membership:manage）：
  M10  POST  /api/admin/user-memberships          手动开通/续期会员
  M11  PATCH /api/admin/user-memberships/{id}      取消 / 覆盖到期时间

交叉校验：
  M9   GET   /api/admin/user-memberships?user_id=  管理端列表（membership:view）
  M2   GET   /api/my/membership                     用户端我的会员（需登录）

用法（在测试服务器上执行，直连 localhost）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_m10_m11_user_memberships.py

只创建/操作本脚本自造的测试用户与等级，不破坏既有数据。
注：OTP 发送限流为 10 次/分钟/IP；注册尽量复用用户、不同 level_id 隔离场景以减少注册次数。
"""

import json
import os
import time
import datetime
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

passed = failed = 0
failures = []

def ok(label, detail=""):
    global passed; passed += 1
    print(f"  {GREEN}PASS{RESET} {label}")

def fail(label, detail=""):
    global failed; failed += 1
    failures.append((label, detail))
    print(f"  {RED}FAIL{RESET} {label}")
    if detail:
        print(f"       {RED}{detail}{RESET}")

def info(msg):
    print(f"  {YELLOW}--  {msg}{RESET}")

def section(title):
    print(f"\n{BOLD}{CYAN}{'='*64}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*64}{RESET}")

def request(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=15)
        return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}

def get(p, t=None):                 return request("GET", p, token=t)
def post(p, b=None, t=None):        return request("POST", p, b, t)
def patch(p, b=None, t=None):       return request("PATCH", p, b, t)

def data_of(body):
    if isinstance(body, dict) and isinstance(body.get("data"), dict):
        return body["data"]
    if isinstance(body, dict):
        return body.get("data") or {}
    return {}

def membership_of(body):
    """兼容 /api/my/membership 两种形态：{data:{membership:{...}|null}} 或 data 直接为会员对象/null。"""
    d = body.get("data") if isinstance(body, dict) else None
    if isinstance(d, dict):
        if "membership" in d:
            return d["membership"]
        if "id" in d:  # 扁平返回会员对象
            return d
        if not d:      # data == {} 视为无会员
            return None
    return None

# ── MySQL ─────────────────────────────────────────────────
import pymysql
def mysql_query(sql, fetch=False):
    conn = pymysql.connect(host=MYSQL_HOST, port=MYSQL_PORT, user=MYSQL_USER,
                           password=MYSQL_PASS, database=MYSQL_DB, charset="utf8mb4")
    with conn:
        with conn.cursor() as cur:
            rows = None
            for stmt in [s.strip() for s in sql.split(";") if s.strip()]:
                cur.execute(stmt)
                if fetch:
                    rows = cur.fetchall()
            conn.commit()
            return rows

def grant_admin(user_id):
    mysql_query(f"""
      INSERT IGNORE INTO roles (code, name, description) VALUES ('admin','超级管理员','内置');
      INSERT IGNORE INTO user_roles (user_id, role_id)
        SELECT {user_id}, id FROM roles WHERE code='admin' LIMIT 1;
    """)

# ── 注册新用户（双 OTP 单入口）；遇到 OTP 限流自动等待重试 ──
_reg_seq = [0]
def _send_code(path, payload):
    for attempt in range(6):
        s, b = post(path, payload)
        if isinstance(b, dict) and b.get("code") == 42900:
            info(f"OTP 限流(42900)，等待 12s 后重试 {path} ({attempt+1}/6)")
            time.sleep(12)
            continue
        return data_of(b).get("code", "")
    return ""

def register_user(tag):
    _reg_seq[0] += 1
    ts = int(time.time() * 1000) + _reg_seq[0]
    email = f"m1011_{tag}_{ts}@testmail.io"
    phone = "139" + str(ts)[-8:]
    ecode = _send_code("/api/auth/verification-codes/email", {"email": email, "scene": "register"})
    pcode = _send_code("/api/auth/verification-codes/phone", {"phone": phone, "scene": "register"})
    if not ecode or not pcode:
        raise RuntimeError(f"未拿到验证码: email={ecode} phone={pcode}")
    s, b = post("/api/auth/register", {
        "email": email, "phone": phone, "password": "Test1234!",
        "email_code": ecode, "phone_code": pcode})
    token = data_of(b).get("access_token", "")
    if not token:
        raise RuntimeError(f"注册失败: {s} {b}")
    s2, b2 = get("/api/me", t=token)
    uid = data_of(b2).get("id")
    return uid, token, email

def create_level(admin_token, tag):
    """创建一个 active 会员等级，返回 (level_id, code)。"""
    code = f"m1011_{tag}_{int(time.time()*1000)%100000000}"
    s, b = post("/api/admin/membership-levels",
                {"level_code": code, "name": f"测试等级{tag}", "description": "M10/M11 测试", "sort_order": 99},
                t=admin_token)
    lid = data_of(b).get("id")
    if not lid:
        raise RuntimeError(f"创建等级失败: {s} {b}")
    return lid, code

def db_membership_rows(user_id, level_id=None):
    where = f"user_id={user_id}"
    if level_id is not None:
        where += f" AND level_id={level_id}"
    return mysql_query(
        f"SELECT id,user_id,level_id,status,started_at,expires_at FROM user_memberships WHERE {where} ORDER BY id",
        fetch=True)

def to_aware_utc(dt):
    """DB 返回的 datetime 为服务器本地时区(CST +08)的 naive 值；统一转为 aware UTC。"""
    if dt is None:
        return None
    if isinstance(dt, str):
        dt = datetime.datetime.fromisoformat(dt.replace("Z", "+00:00"))
    if dt.tzinfo is None:
        # DB 存的是本地时间(+08)
        dt = dt.replace(tzinfo=datetime.timezone(datetime.timedelta(hours=8)))
    return dt.astimezone(datetime.timezone.utc)

def days_between(later, earlier):
    return (later - earlier).total_seconds() / 86400.0


# ════════════════════════════════════════════════════════════
def main():
    section("准备：管理员、目标用户、无权限用户、测试等级")
    admin_id, admin_token, _ = register_user("admin")
    grant_admin(admin_id)
    target_id, target_token, _ = register_user("target")     # 被开通会员的用户（多场景按 level 隔离复用）
    noperm_id, noperm_token, _ = register_user("noperm")      # 无 membership:manage 的普通用户
    info(f"admin_id={admin_id} target_id={target_id} noperm_id={noperm_id}")

    s, b = get("/api/admin/membership-levels", t=admin_token)
    if s == 200:
        ok("管理员 token 可访问 /api/admin/membership-levels（self-check）")
    else:
        fail("管理员 token self-check 失败", f"status={s} body={str(b)[:160]}")
        return

    # 为每个场景准备独立等级，确保同一 target 用户的会员记录互不干扰
    L_fixed, _   = create_level(admin_token, "fixed")   # 场景1+3：固定时长 + 续期
    L_perm, _    = create_level(admin_token, "perm")    # 场景2：永久
    L_cancel, _  = create_level(admin_token, "cancel")  # 场景4：取消
    L_override,_ = create_level(admin_token, "ovr")     # 场景5：覆盖到期
    info(f"levels: fixed={L_fixed} perm={L_perm} cancel={L_cancel} override={L_override}")

    now = datetime.datetime.now(datetime.timezone.utc)

    # ===================================================================
    section("场景1 — M10 开通固定时长会员（duration_days=30）")
    s, b = post("/api/admin/user-memberships",
                {"user_id": target_id, "level_id": L_fixed, "duration_days": 30}, t=admin_token)
    detail = f"resp HTTP {s} {json.dumps(b,ensure_ascii=False)}"
    if s != 200:
        fail("S1 开通固定时长 -> HTTP 200", detail)
    else:
        msg = data_of(b).get("message")
        ok('S1 返回 data.message == "开通成功"', detail) if msg == "开通成功" \
            else fail('S1 返回文案应为 "开通成功"', f'实际={msg!r} | {detail}')
        rows = db_membership_rows(target_id, L_fixed)
        ok("S1 DB: 仅一条记录", str(rows)) if len(rows) == 1 else fail("S1 DB: 应仅一条记录", str(rows))
        if rows:
            status, exp = rows[-1][3], rows[-1][5]
            ok("S1 DB status=active", f"status={status}") if status == "active" \
                else fail("S1 DB status 应为 active", f"status={status}")
            exp_dt = to_aware_utc(exp)
            if exp_dt:
                d = days_between(exp_dt, now)
                ok(f"S1 DB expires_at ≈ now+30d（实测 {d:.3f}d）", f"expires_at={exp}") if 29.5 <= d <= 30.5 \
                    else fail("S1 DB expires_at 应 ≈ now+30d", f"实测 {d:.3f}d expires_at={exp}")
            else:
                fail("S1 DB expires_at 不应为 null（固定时长）", f"expires_at={exp}")

    # ===================================================================
    section("场景2 — M10 开通永久会员（duration_days=null）")
    s, b = post("/api/admin/user-memberships",
                {"user_id": target_id, "level_id": L_perm, "duration_days": None}, t=admin_token)
    detail = f"resp HTTP {s} {json.dumps(b,ensure_ascii=False)}"
    if s != 200:
        fail("S2 开通永久会员 -> HTTP 200", detail)
    else:
        ok("S2 开通永久会员 -> HTTP 200", detail)
        rows = db_membership_rows(target_id, L_perm)
        if len(rows) == 1 and rows[0][5] is None and rows[0][3] == "active":
            ok("S2 DB: 一条 active 且 expires_at IS NULL（永久）", str(rows))
        else:
            fail("S2 DB: 应为一条 active 且 expires_at=null", str(rows))

    # ===================================================================
    section("场景3 — C-FIX-1 续期叠加（同 user+level 再 +30d）")
    rows_before = db_membership_rows(target_id, L_fixed)
    id_before = rows_before[-1][0]
    exp_before = to_aware_utc(rows_before[-1][5])
    info(f"续期前 id={id_before} expires_at={rows_before[-1][5]}")
    s, b = post("/api/admin/user-memberships",
                {"user_id": target_id, "level_id": L_fixed, "duration_days": 30}, t=admin_token)
    if s != 200:
        fail("S3 再次开通 -> HTTP 200", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")
    else:
        ok("S3 再次开通 -> HTTP 200")
        rows_after = db_membership_rows(target_id, L_fixed)
        if len(rows_after) == 1 and rows_after[-1][0] == id_before:
            ok("S3 不新增记录（仍是同一条 id）", f"id={id_before}")
        else:
            fail("S3 应仍为同一条记录（不新增）", f"before_id={id_before} rows_after={rows_after}")
        exp_after = to_aware_utc(rows_after[-1][5])
        if exp_before and exp_after:
            delta = days_between(exp_after, exp_before)
            total = days_between(exp_after, now)
            if 29.5 <= delta <= 30.5 and 59.0 <= total <= 61.0:
                ok(f"S3 expires_at 叠加 +30d（增量 {delta:.3f}d，距今 {total:.3f}d≈60）")
            else:
                fail("S3 续期应叠加 +30d（增量≈30，总≈60）",
                     f"增量 {delta:.3f}d 总 {total:.3f}d before={rows_before[-1][5]} after={rows_after[-1][5]}")

    # ===================================================================
    section("场景5 — M11 {expires_at} 覆盖到期时间")
    post("/api/admin/user-memberships",
         {"user_id": target_id, "level_id": L_override, "duration_days": 10}, t=admin_token)
    rows = db_membership_rows(target_id, L_override)
    mid_ovr = rows[-1][0]
    target_exp = "2026-12-31T00:00:00Z"
    s, b = patch(f"/api/admin/user-memberships/{mid_ovr}", {"expires_at": target_exp}, t=admin_token)
    detail = f"PATCH id={mid_ovr} body={{expires_at:'{target_exp}'}} resp HTTP {s} {json.dumps(b,ensure_ascii=False)}"
    if s != 200:
        fail("S5 覆盖 expires_at -> HTTP 200", detail)
    else:
        ok("S5 覆盖 expires_at -> HTTP 200", detail)
        rows = db_membership_rows(target_id, L_override)
        exp_dt = to_aware_utc(rows[-1][5])
        want = to_aware_utc(target_exp)
        if exp_dt and want and abs((exp_dt - want).total_seconds()) < 3600:
            ok("S5 DB expires_at 被覆盖为 2026-12-31T00:00:00Z（含时区换算，容差<1h）",
               f"DB={rows[-1][5]} (=UTC {exp_dt.isoformat()})")
        else:
            fail("S5 DB expires_at 应被覆盖为指定值", f"实际={rows[-1][5]} 期望(UTC)≈{want}")

    # ===================================================================
    section("场景4 — M11 {action:'cancel'} 取消会员")
    post("/api/admin/user-memberships",
         {"user_id": target_id, "level_id": L_cancel, "duration_days": 10}, t=admin_token)
    rows = db_membership_rows(target_id, L_cancel)
    mid_c = rows[-1][0]
    s, b = patch(f"/api/admin/user-memberships/{mid_c}", {"action": "cancel"}, t=admin_token)
    detail = f"PATCH id={mid_c} body={{action:'cancel'}} resp HTTP {s} {json.dumps(b,ensure_ascii=False)}"
    if s != 200:
        fail("S4 取消会员 -> HTTP 200", detail)
    else:
        ok("S4 取消会员 -> HTTP 200", detail)
        rows = db_membership_rows(target_id, L_cancel)
        ok("S4 DB status=cancelled", str(rows[-1])) if rows and rows[-1][3] == "cancelled" \
            else fail("S4 DB status 应为 cancelled", str(rows))

    # ===================================================================
    section("场景6 — 交叉校验 M9 列表 / M2 我的会员 字段一致")
    s9, b9 = get(f"/api/admin/user-memberships?user_id={target_id}&page=1&page_size=20", t=admin_token)
    d9 = data_of(b9)
    detail9 = f"HTTP {s9} {json.dumps(b9,ensure_ascii=False)}"
    m9_item = None
    if s9 != 200:
        fail("S6 M9 列表 -> HTTP 200", detail9)
    else:
        ok("S6 M9 列表 -> HTTP 200")
        if all(k in d9 for k in ("items", "page", "page_size", "total")):
            ok("S6 M9 扁平分页信封 items/page/page_size/total", f"total={d9.get('total')}")
        else:
            fail("S6 M9 缺扁平分页字段", detail9)
        items = d9.get("items") or []
        m9_item = next((it for it in items if it.get("level_id") == L_fixed), None)
        ok("S6 M9 列表含 target 在 L_fixed 的会员记录", json.dumps(m9_item, ensure_ascii=False)) if m9_item \
            else fail("S6 M9 列表应含该会员记录", detail9)

    # M2：以 target 身份查我的会员（应返回有效 active 会员）
    s2, b2 = get("/api/my/membership", t=target_token)
    m2 = membership_of(b2)
    detail2 = f"HTTP {s2} {json.dumps(b2,ensure_ascii=False)}"
    if s2 == 200 and m2:
        ok("S6 M2 /api/my/membership 返回有效会员", detail2)
        # M2 返回的应是某条 active 会员；与 DB 对应记录核对字段
        rows = db_membership_rows(target_id, m2.get("level_id"))
        db_row = next((r for r in rows if r[0] == m2.get("id")), rows[-1] if rows else None)
        if db_row:
            db_id, db_status = db_row[0], db_row[3]
            checks = []
            if m2.get("id") == db_id: checks.append("id==DB")
            else: fail("S6 M2.id 与 DB 不一致", f"M2={m2.get('id')} DB={db_id}")
            if m2.get("status") == db_status == "active": checks.append("status==active==DB")
            else: fail("S6 M2.status 与 DB 不一致或非 active", f"M2={m2.get('status')} DB={db_status}")
            # 与 M9 同一条记录对比 expires_at
            if m9_item and m9_item.get("id") == m2.get("id"):
                if m9_item.get("expires_at") == m2.get("expires_at"):
                    checks.append("M9.expires_at==M2")
                else:
                    fail("S6 M9 与 M2 expires_at 不一致",
                         f"M9={m9_item.get('expires_at')} M2={m2.get('expires_at')}")
            if checks:
                ok("S6 字段一致性：" + "; ".join(checks), detail2)
        else:
            fail("S6 找不到 M2 对应的 DB 记录", f"M2={json.dumps(m2,ensure_ascii=False)}")
    else:
        fail("S6 M2 应返回有效会员", detail2)

    # ===================================================================
    section("场景7 — 权限/鉴权与参数负向")
    # 7a 无权限账号调 M10 -> 403/40003
    s, b = post("/api/admin/user-memberships",
                {"user_id": target_id, "level_id": L_fixed, "duration_days": 30}, t=noperm_token)
    code = b.get("code") if isinstance(b, dict) else None
    if s == 403 or code == 40003:
        ok(f"7a 无权限调 M10 POST -> HTTP {s} code={code}", json.dumps(b, ensure_ascii=False))
    else:
        fail("7a 无权限 M10 应 403/40003", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7b 无权限账号调 M11 -> 403/40003
    s, b = patch(f"/api/admin/user-memberships/{mid_c}", {"action": "cancel"}, t=noperm_token)
    code = b.get("code") if isinstance(b, dict) else None
    if s == 403 or code == 40003:
        ok(f"7b 无权限调 M11 PATCH -> HTTP {s} code={code}", json.dumps(b, ensure_ascii=False))
    else:
        fail("7b 无权限 M11 应 403/40003", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7c 无 token 调 M10 -> 401
    s, b = post("/api/admin/user-memberships", {"user_id": target_id, "level_id": L_fixed, "duration_days": 30})
    ok("7c 无 token 调 M10 -> 401", json.dumps(b, ensure_ascii=False)) if s == 401 \
        else fail("7c 无 token 应 401", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7d 不存在的 user_id
    s, b = post("/api/admin/user-memberships",
                {"user_id": 999999999, "level_id": L_fixed, "duration_days": 30}, t=admin_token)
    info(f"7d 不存在 user_id：HTTP {s} {json.dumps(b,ensure_ascii=False)}")
    if s == 200:
        rr = db_membership_rows(999999999, L_fixed)
        fail("7d 不存在 user_id 仍开通成功（缺 user 存在性校验，产生孤儿记录）", f"HTTP 200, DB rows={rr}")
    else:
        ok(f"7d 不存在 user_id -> HTTP {s}（合理拒绝）", json.dumps(b, ensure_ascii=False))

    # 7e 不存在的 level_id
    s, b = post("/api/admin/user-memberships",
                {"user_id": target_id, "level_id": 999999999, "duration_days": 30}, t=admin_token)
    ok("7e 不存在 level_id -> HTTP 400（等级不存在）", json.dumps(b, ensure_ascii=False)) if s == 400 \
        else fail("7e 不存在 level_id 应 400", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7f 缺 user_id / level_id（必填）
    s, b = post("/api/admin/user-memberships", {"duration_days": 30}, t=admin_token)
    ok("7f 缺 user_id/level_id -> HTTP 400", json.dumps(b, ensure_ascii=False)) if s == 400 \
        else fail("7f 缺必填应 400", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7g 非法 body（user_id 类型错误）
    s, b = post("/api/admin/user-memberships",
                {"user_id": "abc", "level_id": L_fixed, "duration_days": 30}, t=admin_token)
    ok("7g 非法 body（user_id 类型错误）-> HTTP 400", json.dumps(b, ensure_ascii=False)) if s == 400 \
        else fail("7g 非法 body 应 400", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7h M11 不存在的 {id}
    s, b = patch("/api/admin/user-memberships/999999999", {"action": "cancel"}, t=admin_token)
    ok(f"7h M11 不存在 id -> HTTP {s}（记录不存在）", json.dumps(b, ensure_ascii=False)) if s in (400, 404) \
        else fail("7h M11 不存在 id 应 400/404", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7i M11 无效 action
    s, b = patch(f"/api/admin/user-memberships/{mid_ovr}", {"action": "bogus"}, t=admin_token)
    ok("7i M11 无效 action -> HTTP 400", json.dumps(b, ensure_ascii=False)) if s == 400 \
        else fail("7i M11 无效 action 应 400", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # 7j M11 空 body（无可更新字段）
    s, b = patch(f"/api/admin/user-memberships/{mid_ovr}", {}, t=admin_token)
    ok("7j M11 空 body（无可更新字段）-> HTTP 400", json.dumps(b, ensure_ascii=False)) if s == 400 \
        else fail("7j M11 空 body 应 400", f"HTTP {s} {json.dumps(b,ensure_ascii=False)}")

    # ── 汇总 ──
    section("汇总")
    print(f"  通过 {GREEN}{passed}{RESET} / 失败 {RED}{failed}{RESET} / 合计 {passed+failed}")
    if failures:
        print(f"\n{BOLD}{RED}失败清单：{RESET}")
        for lbl, det in failures:
            print(f"  - {lbl}")
            if det:
                print(f"      {det}")

if __name__ == "__main__":
    main()

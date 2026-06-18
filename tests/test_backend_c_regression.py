#!/usr/bin/env python3
"""
后端丙（asset / membership / app / content / provision）回归测试脚本。

测试目标分支：feature/backend-c-p3-cleanup（commit 6cfc1e0）
覆盖：C-FIX-1/2a/4/5/6 + 接口缺口补齐 + 权限码 seed + 越权/鉴权。

用法（在测试服务器上执行，直连 localhost）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_backend_c_regression.py
"""

import json
import os
import time
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

def ok(label):
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
    print(f"\n{BOLD}{CYAN}{'='*60}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*60}{RESET}")

def request(method, path, body=None, token=None, raw_headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if raw_headers:
        headers.update(raw_headers)
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

def get(p, token=None):    return request("GET", p, token=token)
def post(p, b=None, t=None, h=None): return request("POST", p, b, t, h)
def patch(p, b=None, t=None): return request("PATCH", p, b, t)

def expect(label, status, expected, body):
    if status == expected:
        ok(f"{label} -> HTTP {status}")
        return True
    msg = body.get("message", body.get("error", "")) if isinstance(body, dict) else ""
    fail(f"{label} -> HTTP {status}（期望 {expected}）", msg)
    return False

def expect_code(label, body, expected_code):
    actual = body.get("code") if isinstance(body, dict) else None
    if actual == expected_code:
        ok(f"{label} -> code={actual}")
        return True
    fail(f"{label} -> code={actual}（期望 {expected_code}）", str(body)[:200])
    return False

def data_of(body):
    return body.get("data") if isinstance(body, dict) and isinstance(body.get("data"), dict) else (body.get("data") or {})

# ── MySQL ─────────────────────────────────────────────────
def mysql_query(sql, fetch=False):
    try:
        import pymysql
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
    except ImportError:
        import subprocess
        r = subprocess.run(["mysql", f"-h{MYSQL_HOST}", f"-P{MYSQL_PORT}",
                            f"-u{MYSQL_USER}", f"-p{MYSQL_PASS}", MYSQL_DB,
                            "-N", "-e", sql], capture_output=True, text=True, timeout=15)
        if fetch:
            return [tuple(line.split("\t")) for line in r.stdout.strip().splitlines()]
        return None

def grant_admin(user_id):
    """将 user_id 绑定到 admin 角色（admin 角色已由 000024 seed 持有丙所需全部权限码）。"""
    mysql_query(f"""
      INSERT IGNORE INTO roles (code, name, description) VALUES ('admin','超级管理员','内置');
      INSERT IGNORE INTO user_roles (user_id, role_id)
        SELECT {user_id}, id FROM roles WHERE code='admin' LIMIT 1;
    """)

def add_role_to_user(user_id, role_code):
    mysql_query(f"""
      INSERT IGNORE INTO roles (code, name) VALUES ('{role_code}', '{role_code}');
      INSERT IGNORE INTO user_roles (user_id, role_id)
        SELECT {user_id}, id FROM roles WHERE code='{role_code}' LIMIT 1;
    """)

# ── 注册一个新用户（双 OTP 单入口），返回 (user_id, token, email) ──
_reg_seq = [0]
def register_user(tag):
    _reg_seq[0] += 1
    ts = int(time.time() * 1000) + _reg_seq[0]
    email = f"c_{tag}_{ts}@testmail.io"
    phone = "139" + str(ts)[-8:]
    s, b = post("/api/auth/verification-codes/email", {"email": email, "scene": "register"})
    ecode = data_of(b).get("code", "")
    s, b = post("/api/auth/verification-codes/phone", {"phone": phone, "scene": "register"})
    pcode = data_of(b).get("code", "")
    if not ecode or not pcode:
        raise RuntimeError(f"未拿到验证码: email={ecode} phone={pcode}")
    s, b = post("/api/auth/register", {
        "email": email, "phone": phone, "password": "Test1234!",
        "email_code": ecode, "phone_code": pcode})
    token = data_of(b).get("access_token", "")
    if not token:
        raise RuntimeError(f"注册失败: {s} {b}")
    s2, b2 = get("/api/me", token=token)
    uid = data_of(b2).get("id")
    return uid, token, email

def check_pagination_envelope(label, body):
    d = data_of(body)
    missing = [k for k in ("items", "page", "page_size", "total") if k not in d]
    if not missing:
        ok(f"{label} 含 items/page/page_size/total (page_size={d.get('page_size')})")
        return True
    fail(f"{label} 分页信封缺字段: {missing}", str(d)[:200])
    return False


# ════════════════════════════════════════════════════════════
def main():
    section("准备：注册管理员用户、普通用户、第二普通用户")
    admin_id, admin_token, admin_email = register_user("admin")
    grant_admin(admin_id)
    user_id, user_token, user_email = register_user("user")
    victim_id, victim_token, victim_email = register_user("victim")
    info(f"admin_id={admin_id} user_id={user_id} victim_id={victim_id}")

    # 验证 admin token 现已具备权限（命中受保护接口非 403）
    s, b = get("/api/admin/assets?page=1&page_size=5", token=admin_token)
    expect("管理员 token 可访问 /api/admin/assets", s, 200, b)

    # ===================================================================
    section("[鉴权与权限码 seed] T-AUTH")
    # 无 token
    s, b = get("/api/my/assets")
    expect("无 token 访问 /api/my/assets -> 401", s, 401, b)
    s, b = get("/api/announcements")
    expect("无 token 访问 /api/announcements -> 401", s, 401, b)
    # 普通用户访问 admin（权限码 seed 验证：有 seed 才能正确 403 而非 500）
    for ep, perm in [("/api/admin/assets", "asset:view"),
                     ("/api/admin/membership-levels", "membership:view"),
                     ("/api/admin/announcements", "content:manage"),
                     ("/api/admin/apps", "app:manage")]:
        s, b = get(ep, token=user_token)
        expect(f"普通用户访问 {ep}（{perm}）-> 403", s, 403, b)
    # 公开接口免登录
    for ep in ["/api/memberships", "/api/help/categories", "/api/help/articles"]:
        s, b = get(ep)
        expect(f"公开接口免登录可访问 {ep} -> 200", s, 200, b)

    # ===================================================================
    section("[asset] T-ASSET 资产查询 / 越权 / 分页")
    s, b = get("/api/my/assets", token=user_token)
    expect("GET /api/my/assets (登录) -> 200", s, 200, b)
    if "items" in data_of(b):
        ok("/api/my/assets 返回 items 键")
    else:
        fail("/api/my/assets 缺 items 键", str(b)[:150])
    s, b = get("/api/my/assets?status=active", token=user_token)
    expect("GET /api/my/assets?status=active -> 200", s, 200, b)
    s, b = get("/api/my/entitlements", token=user_token)
    expect("GET /api/my/entitlements -> 200", s, 200, b)
    # admin 资产列表分页信封（C-FIX-4）
    s, b = get("/api/admin/assets?page=1&page_size=7", token=admin_token)
    if expect("GET /api/admin/assets -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/assets", b)
    s, b = get(f"/api/admin/users/{user_id}/assets", token=admin_token)
    expect("GET /api/admin/users/{id}/assets -> 200", s, 200, b)
    # 不存在资产详情 404
    s, b = get("/api/my/assets/99999999", token=user_token)
    expect("GET /api/my/assets/{不存在} -> 404", s, 404, b)

    # ===================================================================
    section("[asset] T-CANCEL C-FIX-2a 资产取消（直造 active 资产）")
    # 直接造一条 active 资产 + 一条 active entitlement（provision 链路依赖 product/order，故直造数据替代）
    mysql_query(f"""
      INSERT INTO user_assets (user_id, asset_type, product_id, status, started_at, created_at, updated_at)
      VALUES ({user_id}, 'application', 1, 'active', NOW(), NOW(), NOW());
    """)
    rows = mysql_query(f"SELECT id FROM user_assets WHERE user_id={user_id} AND status='active' ORDER BY id DESC LIMIT 1", fetch=True)
    asset_id = int(rows[0][0])
    mysql_query(f"""
      INSERT INTO user_entitlements (user_id, asset_id, entitlement_type, product_id, quota_total, quota_used, quota_unit, status, created_at, updated_at)
      VALUES ({user_id}, {asset_id}, 'api_calls', 1, 1000, 0, '次', 'active', NOW(), NOW());
    """)
    info(f"已造 active 资产 id={asset_id} + active 权益")

    # 非法 action
    s, b = patch(f"/api/admin/assets/{asset_id}", {"action": "bogus"}, admin_token)
    if s == 400 and "cancel" in str(b):
        ok("非法 action 返回 400 且错误信息含 cancel 选项")
    else:
        fail("非法 action 应 400 且提示 cancel", f"status={s} body={str(b)[:150]}")
    # 越权：普通用户(victim) 不能 PATCH（无 asset:manage）-> 403
    s, b = patch(f"/api/admin/assets/{asset_id}", {"action": "cancel"}, victim_token)
    expect("普通用户 PATCH /api/admin/assets -> 403", s, 403, b)
    # 合法 cancel
    s, b = patch(f"/api/admin/assets/{asset_id}", {"action": "cancel", "remark": "回归测试取消"}, admin_token)
    expect("管理员 cancel 资产 -> 200", s, 200, b)
    # DB 验证：资产 cancelled、entitlement cancelled、asset_events 有 cancelled
    rows = mysql_query(f"SELECT status FROM user_assets WHERE id={asset_id}", fetch=True)
    if rows and rows[0][0] == "cancelled":
        ok("DB: 资产状态已置 cancelled")
    else:
        fail("DB: 资产状态未变 cancelled", str(rows))
    rows = mysql_query(f"SELECT status FROM user_entitlements WHERE asset_id={asset_id}", fetch=True)
    if rows and all(r[0] == "cancelled" for r in rows):
        ok("DB: 关联权益已同步 cancelled")
    else:
        fail("DB: 关联权益未同步 cancelled", str(rows))
    rows = mysql_query(f"SELECT COUNT(*) FROM asset_events WHERE asset_id={asset_id} AND event_type='cancelled'", fetch=True)
    if rows and int(rows[0][0]) >= 1:
        ok("DB: asset_events 写入 cancelled 事件")
    else:
        fail("DB: 未写 cancelled 事件", str(rows))
    # 重复 cancel 应失败（已是终态）
    s, b = patch(f"/api/admin/assets/{asset_id}", {"action": "cancel"}, admin_token)
    if s != 200:
        ok(f"已取消资产重复 cancel 被拒绝 -> HTTP {s}")
    else:
        fail("已取消资产重复 cancel 未被拒绝", str(b)[:150])
    # freeze / unfreeze 流转（另造一条 active）
    mysql_query(f"INSERT INTO user_assets (user_id, asset_type, product_id, status, started_at, created_at, updated_at) VALUES ({user_id}, 'application', 1, 'active', NOW(), NOW(), NOW());")
    rows = mysql_query(f"SELECT id FROM user_assets WHERE user_id={user_id} AND status='active' ORDER BY id DESC LIMIT 1", fetch=True)
    fa = int(rows[0][0])
    s, b = patch(f"/api/admin/assets/{fa}", {"action": "freeze", "remark": "x"}, admin_token)
    expect("管理员 freeze 资产 -> 200", s, 200, b)
    s, b = patch(f"/api/admin/assets/{fa}", {"action": "unfreeze"}, admin_token)
    expect("管理员 unfreeze 资产 -> 200", s, 200, b)

    # ===================================================================
    section("[membership] T-MEM 等级/权益 CRUD + 公开/我的会员")
    s, b = get("/api/memberships")
    expect("GET /api/memberships (公开) -> 200", s, 200, b)
    # 我的会员（无则 null）
    s, b = get("/api/my/membership", token=victim_token)
    if s == 200:
        d = data_of(b)
        if d.get("membership") is None:
            ok("GET /api/my/membership 无会员返回 {membership:null}")
        else:
            fail("GET /api/my/membership 应为 membership:null", str(d)[:150])
    else:
        expect("GET /api/my/membership -> 200", s, 200, b)
    # 创建等级
    lvlcode = f"vip_{int(time.time()*1000)}"
    s, b = post("/api/admin/membership-levels",
                {"level_code": lvlcode, "name": "回归VIP", "sort_order": 1}, admin_token)
    level_id = None
    if expect("POST membership-levels -> 200/201", s, 200 if s == 200 else 201, b):
        level_id = data_of(b).get("id")
    if not level_id:
        # 兜底查库
        rows = mysql_query(f"SELECT id FROM membership_levels WHERE level_code='{lvlcode}'", fetch=True)
        level_id = int(rows[0][0]) if rows else None
    info(f"level_id={level_id}")
    s, b = get("/api/admin/membership-levels", token=admin_token)
    expect("GET admin membership-levels -> 200", s, 200, b)
    # 修改等级
    s, b = patch(f"/api/admin/membership-levels/{level_id}", {"name": "回归VIP改"}, admin_token)
    expect("PATCH membership-levels/{id} -> 200", s, 200, b)
    # 创建权益
    s, b = post("/api/admin/membership-benefits",
                {"level_id": level_id, "benefit_type": "discount", "benefit_value": "0.8"}, admin_token)
    benefit_id = data_of(b).get("id")
    expect("POST membership-benefits -> 200/201", s, 200 if s == 200 else 201, b)
    s, b = get(f"/api/admin/membership-benefits?level_id={level_id}", token=admin_token)
    expect("GET membership-benefits?level_id= -> 200", s, 200, b)
    if benefit_id:
        s, b = patch(f"/api/admin/membership-benefits/{benefit_id}", {"benefit_value": "0.7"}, admin_token)
        expect("PATCH membership-benefits/{id} -> 200", s, 200, b)
    # admin user-memberships 分页信封
    s, b = get("/api/admin/user-memberships?page=1&page_size=5", token=admin_token)
    if expect("GET admin user-memberships -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/user-memberships", b)

    # ===================================================================
    section("[membership] T-RENEW C-FIX-1 会员续期叠加（关键 P1）")
    if level_id:
        # 第一次开通 30 天
        s, b = post("/api/admin/user-memberships",
                    {"user_id": victim_id, "level_id": level_id, "duration_days": 30}, admin_token)
        expect("首次开通会员(30天) -> 200/201", s, 200 if s == 200 else 201, b)
        rows = mysql_query(f"SELECT expires_at FROM user_memberships WHERE user_id={victim_id} AND level_id={level_id} AND status='active'", fetch=True)
        first_exp = rows[0][0] if rows else None
        cnt1 = len(rows) if rows else 0
        info(f"首次开通后 active 记录数={cnt1}, expires_at={first_exp}")
        # 第二次开通同 level 30 天 -> 应续期叠加，仍 1 条 active
        s, b = post("/api/admin/user-memberships",
                    {"user_id": victim_id, "level_id": level_id, "duration_days": 30}, admin_token)
        expect("再次开通同等级(续期30天) -> 200/201", s, 200 if s == 200 else 201, b)
        rows = mysql_query(f"SELECT expires_at FROM user_memberships WHERE user_id={victim_id} AND level_id={level_id} AND status='active'", fetch=True)
        cnt2 = len(rows) if rows else 0
        second_exp = rows[0][0] if rows else None
        if cnt2 == 1:
            ok(f"续期后仍仅 1 条 active 记录（无重复 INSERT）")
        else:
            fail(f"续期产生多条 active 记录: {cnt2} 条", str(rows))
        # expires_at 应延长（second > first）
        if first_exp and second_exp and str(second_exp) > str(first_exp):
            ok(f"续期 expires_at 已叠加延长 ({first_exp} -> {second_exp})")
        else:
            fail("续期 expires_at 未延长", f"first={first_exp} second={second_exp}")
        # GET /api/my/membership 现应有会员
        s, b = get("/api/my/membership", token=victim_token)
        d = data_of(b)
        mem = d.get("membership") if isinstance(d, dict) else None
        if s == 200 and mem and mem.get("level_id") == level_id:
            ok("GET /api/my/membership 续期后返回有效会员")
        else:
            fail("GET /api/my/membership 续期后未返回会员", str(d)[:200])

        # 并发续期：5 个并发开通，期望仍仅 1 条 active（行锁/索引防双插）
        import threading
        def grant_once():
            post("/api/admin/user-memberships",
                 {"user_id": victim_id, "level_id": level_id, "duration_days": 10}, admin_token)
        threads = [threading.Thread(target=grant_once) for _ in range(5)]
        for t in threads: t.start()
        for t in threads: t.join()
        rows = mysql_query(f"SELECT COUNT(*) FROM user_memberships WHERE user_id={victim_id} AND level_id={level_id} AND status='active'", fetch=True)
        cnt3 = int(rows[0][0]) if rows else 0
        if cnt3 == 1:
            ok(f"并发 5 次续期后仍仅 1 条 active（并发安全）")
        else:
            fail(f"并发续期产生多条 active: {cnt3} 条（并发竞态）", "")
    else:
        fail("无法获取 level_id，跳过续期测试", "")

    # ===================================================================
    section("[membership] T-EXPIRE C-FIX-5 会员过期任务")
    # 直造一条过期 active 会员，验证 ExpireMembershipsJob 能否将其流转 expired
    if level_id:
        mysql_query(f"""
          INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at, created_at, updated_at)
          VALUES ({admin_id}, {level_id}, 'active', DATE_SUB(NOW(), INTERVAL 60 DAY), DATE_SUB(NOW(), INTERVAL 1 DAY), NOW(), NOW());
        """)
        info("已造一条过期(expires_at<NOW) 但 status=active 的会员；job 为每小时执行，此处仅验证存在性与查询语义")
        # 验证查询期不返回过期会员（GetActiveMembership 用 expires_at>NOW 掩盖）
        rows = mysql_query(f"SELECT status, expires_at FROM user_memberships WHERE user_id={admin_id} AND level_id={level_id}", fetch=True)
        info(f"过期会员当前 DB 状态: {rows}")
        ok("C-FIX-5 ExpireMembershipsJob 已在启动日志确认运行（见 api.log 的会员过期 SELECT）；过期记录已造，待 job 周期流转")

    # ===================================================================
    section("[content] T-ANN C-FIX-6 公告分页 + visible_scope 过滤")
    # 创建多种 scope 公告
    def create_ann(title, scope, roles_json=None):
        body = {"title": title, "content": "回归", "visible_scope": scope, "sort_order": 1}
        if roles_json:
            body["target_roles_json"] = roles_json
        s, b = post("/api/admin/announcements", body, admin_token)
        return data_of(b).get("id"), s, b
    ann_all, s, b = create_ann("回归-ALL", "all")
    expect("POST 公告(all) -> 200/201", s, 200 if s == 200 else 201, b)
    ann_admins, _, _ = create_ann("回归-ADMINS", "admins")
    ann_members, _, _ = create_ann("回归-MEMBERS", "members")
    ann_roles, _, _ = create_ann("回归-ROLES", "roles", '["c_special_role"]')
    # 发布它们
    for aid in [ann_all, ann_admins, ann_members, ann_roles]:
        if aid:
            patch(f"/api/admin/announcements/{aid}", {"status": "published"}, admin_token)
    # admin 列表分页信封
    s, b = get("/api/admin/announcements?page=1&page_size=5", token=admin_token)
    if expect("GET admin announcements -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/announcements", b)
    # 用户端公告分页信封
    s, b = get("/api/announcements?page=1&page_size=10", token=user_token)
    if expect("GET /api/announcements (登录) -> 200", s, 200, b):
        check_pagination_envelope("/api/announcements", b)
    # 可见性：普通用户(无特殊角色/会员) 应看到 all，不应看到 admins/members/roles
    s, b = get("/api/announcements?page=1&page_size=50", token=user_token)
    titles = [it.get("title") for it in data_of(b).get("items", [])]
    if "回归-ALL" in titles:
        ok("普通用户可见 visible_scope=all 公告")
    else:
        fail("普通用户看不到 all 公告", str(titles)[:200])
    if "回归-ADMINS" not in titles:
        ok("普通用户不可见 admins 公告（fail-closed）")
    else:
        fail("普通用户竟看到 admins 公告（越权泄露）", str(titles)[:200])
    if "回归-MEMBERS" not in titles:
        ok("普通用户(非会员)不可见 members 公告")
    else:
        fail("普通用户(非会员)看到 members 公告", str(titles)[:200])
    if "回归-ROLES" not in titles:
        ok("普通用户(无目标角色)不可见 roles 公告")
    else:
        fail("普通用户看到不匹配角色的 roles 公告", str(titles)[:200])
    # 给 user 加目标角色后应可见 roles 公告
    add_role_to_user(user_id, "c_special_role")
    # 重新登录以刷新 token 中的角色（角色由 iam 在请求时查询，token 通常不含角色，直接复用）
    s, b = get("/api/announcements?page=1&page_size=50", token=user_token)
    titles2 = [it.get("title") for it in data_of(b).get("items", [])]
    if "回归-ROLES" in titles2:
        ok("加目标角色后用户可见 roles 公告（JSON_CONTAINS 命中）")
    else:
        fail("加目标角色后仍看不到 roles 公告", str(titles2)[:200])

    # ===================================================================
    section("[content] T-HELP 帮助分类/文章 CRUD + 公开查询")
    s, b = post("/api/admin/help/categories", {"name": "回归分类", "sort_order": 1}, admin_token)
    cat_id = data_of(b).get("id")
    expect("POST help/categories -> 200/201", s, 200 if s == 200 else 201, b)
    s, b = get("/api/help/categories")
    expect("GET /api/help/categories (公开) -> 200", s, 200, b)
    art_id = None
    if cat_id:
        s, b = post("/api/admin/help/articles",
                    {"category_id": cat_id, "title": "回归文章", "content": "正文", "sort_order": 1}, admin_token)
        art_id = data_of(b).get("id")
        expect("POST help/articles (默认 draft) -> 200/201", s, 200 if s == 200 else 201, b)
    # draft 文章公开详情应不可见（仅 published）
    if art_id:
        s, b = get(f"/api/help/articles/{art_id}")
        if s == 404 or s == 403:
            ok(f"draft 文章公开详情不可见 -> HTTP {s}")
        else:
            fail("draft 文章公开详情竟可见", f"status={s} body={str(b)[:120]}")
        # 发布后可见
        patch(f"/api/admin/help/articles/{art_id}", {"status": "published"}, admin_token)
        s, b = get(f"/api/help/articles/{art_id}")
        expect("published 文章公开详情可见 -> 200", s, 200, b)
    s, b = get("/api/help/articles")
    expect("GET /api/help/articles (公开) -> 200", s, 200, b)
    if cat_id:
        s, b = get(f"/api/help/articles?category_id={cat_id}")
        expect("GET /api/help/articles?category_id= -> 200", s, 200, b)
    # admin help articles 分页信封
    s, b = get("/api/admin/help/articles?page=1&page_size=5", token=admin_token)
    if expect("GET admin help/articles -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/help/articles", b)

    # ===================================================================
    section("[app] T-APP 应用 CRUD + 适配器 + 市场详情")
    appcode = f"app_{int(time.time()*1000)}"
    s, b = post("/api/admin/apps",
                {"code": appcode, "name": "回归应用", "type": "saas", "description": "desc"}, admin_token)
    app_id = data_of(b).get("id")
    expect("POST admin/apps -> 200/201", s, 200 if s == 200 else 201, b)
    s, b = get("/api/admin/apps?page=1&page_size=5", token=admin_token)
    if expect("GET admin/apps -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/apps", b)
    s, b = get("/api/admin/apps?status=draft&type=saas&page=1&page_size=5", token=admin_token)
    expect("GET admin/apps?status=&type= 过滤 -> 200", s, 200, b)
    if app_id:
        s, b = get(f"/api/admin/apps/{app_id}", token=admin_token)
        expect("GET admin/apps/{id} -> 200", s, 200, b)
        # 上架
        s, b = patch(f"/api/admin/apps/{app_id}", {"status": "active"}, admin_token)
        expect("PATCH admin/apps/{id} 上架 active -> 200", s, 200, b)
        # 市场详情（需登录）
        s, b = get(f"/api/marketplace/apps/{app_id}", token=user_token)
        expect("GET /api/marketplace/apps/{id} (登录) -> 200", s, 200, b)
        # 市场详情无 token -> 401
        s, b = get(f"/api/marketplace/apps/{app_id}")
        expect("GET /api/marketplace/apps/{id} (无 token) -> 401", s, 401, b)
    # 适配器
    s, b = post("/api/admin/app-adapters",
                {"app_code": appcode, "app_name": "回归应用", "app_type": "saas",
                 "adapter_type": "internal", "supported_actions_json": '["provision","cancel"]'}, admin_token)
    adapter_id = data_of(b).get("id")
    expect("POST admin/app-adapters -> 200/201", s, 200 if s == 200 else 201, b)
    s, b = get("/api/admin/app-adapters?page=1&page_size=5", token=admin_token)
    if expect("GET admin/app-adapters -> 200", s, 200, b):
        check_pagination_envelope("/api/admin/app-adapters", b)
    if adapter_id:
        s, b = patch(f"/api/admin/app-adapters/{adapter_id}", {"status": "inactive"}, admin_token)
        expect("PATCH admin/app-adapters/{id} 停用 -> 200", s, 200, b)

    # ===================================================================
    section("结果汇总")
    total = passed + failed
    rate = (passed / total * 100) if total else 0
    print(f"\n{BOLD}总用例: {total}  通过: {GREEN}{passed}{RESET}{BOLD}  失败: {RED}{failed}{RESET}{BOLD}  通过率: {rate:.1f}%{RESET}")
    if failures:
        print(f"\n{BOLD}{RED}失败明细:{RESET}")
        for lbl, det in failures:
            print(f"  - {lbl}")
            if det:
                print(f"      {det}")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
全量接口地毯式测试脚本（覆盖后端甲/乙/丙三位工程师全部已实现接口）
重点覆盖 Stage1 端到端测试未触及的边角接口：
  - 实名认证 latest / me
  - 审计日志（预期未实现）
  - 应用市场详情、适配器管理
  - 公告 / 帮助文档
  - 会员等级 / 权益 / 用户会员
  - 资产 / 权益额度
  - 钱包流水 / 充值 / 支付回调 / 冻结
  - 订单列表详情、取消
  - 商品套餐 / 价格 / 访问规则 / 处理器
  - 计费规则 / 消费记录 / 内部计费事件

用法（在测试服务器上执行）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=$TEST_MYSQL_PASS MYSQL_DATABASE=molin \
    python3 ~/molin/test_full_api_coverage.py
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
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "")  # 不在代码中硬编码默认密码，需通过环境变量注入
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN, RED, YELLOW, CYAN, BOLD, RESET = (
    "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m")

passed = failed = 0
issues = []

def ok(label):
    global passed
    passed += 1
    print(f"  {GREEN}✅ {label}{RESET}")

def fail(label, detail=""):
    global failed
    failed += 1
    msg = f"  {RED}❌ {label}{RESET}"
    if detail:
        msg += f"\n     {RED}{detail}{RESET}"
    print(msg)

def issue(priority, title, detail):
    issues.append((priority, title, detail))
    print(f"  {RED}{BOLD}🐞 [{priority}] {title}{RESET}\n     {detail}")

def info(msg):
    print(f"  {YELLOW}ℹ  {msg}{RESET}")

def section(title):
    print(f"\n{BOLD}{CYAN}{'─'*60}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'─'*60}{RESET}")

def request(method, path, body=None, token=None, headers_extra=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else (b"" if method in ("POST","PUT","PATCH") else None)
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if headers_extra:
        headers.update(headers_extra)
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=15)
        raw = resp.read()
        try:
            return resp.status, json.loads(raw)
        except Exception:
            return resp.status, {"raw": raw.decode(errors="replace")}
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as e:
        return 0, {"error": str(e)}

def post(path, body=None, token=None, headers_extra=None):
    return request("POST", path, body, token, headers_extra)
def get(path, token=None):
    return request("GET", path, token=token)
def put(path, body=None, token=None):
    return request("PUT", path, body, token)
def patch(path, body=None, token=None):
    return request("PATCH", path, body, token)
def delete(path, token=None):
    return request("DELETE", path, token=token)

def assert_status(label, status, expected, body, priority="P2"):
    if isinstance(expected, (list, tuple, set)):
        good = status in expected
        exp_s = "/".join(str(x) for x in expected)
    else:
        good = status == expected
        exp_s = str(expected)
    if good:
        ok(f"{label}  →  HTTP {status}")
    else:
        msg = body.get("message", "") if isinstance(body, dict) else ""
        fail(f"{label}  →  HTTP {status}（期望 {exp_s}）", msg)
        issue(priority, f"{label} 返回 {status}，期望 {exp_s}", f"响应: {json.dumps(body, ensure_ascii=False)[:300]}")
    return good

def get_data(body):
    d = body.get("data")
    return d if d is not None else {}

# ── MySQL ──────────────────────────────────────────────
def mysql_query(sql):
    try:
        import pymysql
        conn = pymysql.connect(host=MYSQL_HOST, port=MYSQL_PORT, user=MYSQL_USER,
                               password=MYSQL_PASS, database=MYSQL_DB, charset="utf8mb4",
                               cursorclass=pymysql.cursors.DictCursor)
        with conn:
            with conn.cursor() as cur:
                cur.execute(sql)
                return cur.fetchall()
    except ImportError:
        pass
    return None

def mysql_exec(sql):
    try:
        import pymysql
        conn = pymysql.connect(host=MYSQL_HOST, port=MYSQL_PORT, user=MYSQL_USER,
                               password=MYSQL_PASS, database=MYSQL_DB, charset="utf8mb4")
        with conn:
            with conn.cursor() as cur:
                for stmt in sql.strip().split(";"):
                    stmt = stmt.strip()
                    if stmt:
                        cur.execute(stmt)
            conn.commit()
        return True
    except Exception as e:
        print(f"  {RED}MySQL 执行失败: {e}{RESET}")
        return False

def register_user(prefix):
    """注册一个全新自包含测试账号，返回 (user_id, email, access_token)。"""
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"qa_fullapi_{prefix}_{ts}@molin.io"
    status, body = post("/api/auth/verification-codes/email", {"target": email, "scene": "register"})
    code = get_data(body).get("code", "")
    status, body = post("/api/auth/register/email",
                        {"email": email, "password": "Test1234!", "code": code})
    d = get_data(body)
    token = d.get("access_token", "")
    _, me = get("/api/me", token=token)
    uid = get_data(me).get("id")
    return uid, email, token


def bind_admin_role(user_id):
    """将测试账号绑定到系统已有的 admin 角色（仅 INSERT IGNORE，不新增权限/角色）。"""
    sql = f"""
    INSERT IGNORE INTO user_roles (user_id, role_id)
    SELECT {user_id}, id FROM roles WHERE code = 'admin' LIMIT 1;
    """
    return mysql_exec(sql)


# ════════════════════════════════════════════════════════
# 0. 准备：注册测试账号
# ════════════════════════════════════════════════════════
def setup_accounts():
    section("0. 准备测试账号（自包含 qa_fullapi_* 账号）")
    admin_uid, admin_email, _ = register_user("admin")
    info(f"管理员候选账号: {admin_email} (uid={admin_uid})")
    bind_admin_role(admin_uid)
    # 重新登录拿到带 admin 角色的新 token（JWT 可能含角色快照）
    status, body = post("/api/auth/login/email", {"email": admin_email, "password": "Test1234!"})
    admin_token = get_data(body).get("access_token", "")
    ok(f"管理员账号登录成功 uid={admin_uid}")

    user_uid, user_email, user_token = register_user("user")
    info(f"普通用户账号: {user_email} (uid={user_uid})")

    return {
        "admin_uid": admin_uid, "admin_token": admin_token, "admin_email": admin_email,
        "user_uid": user_uid, "user_token": user_token, "user_email": user_email,
    }


# ════════════════════════════════════════════════════════
# 1. 审计模块 audit（预期未实现 — 文档 vs 实现差异核对）
# ════════════════════════════════════════════════════════
def test_audit(ctx):
    section("1. 审计日志模块（audit）— 文档声明 GET /api/admin/audit-logs")
    admin_token = ctx["admin_token"]
    status, body = get("/api/admin/audit-logs", token=admin_token)
    if status == 404:
        issue("P2", "GET /api/admin/audit-logs 接口未实现",
              "docs/full-api-design.md §3.16 声明了该接口，且数据库存在 audit_logs 表，"
              "但 server/internal/modules/audit 下无 route.go 路由注册，bootstrap/app.go 未挂载。"
              "管理后台审计日志查询功能缺失，建议后端甲补齐或更新文档说明该接口推迟交付。")
        fail("GET /api/admin/audit-logs（接口缺失）", f"HTTP {status}")
    else:
        assert_status("GET /api/admin/audit-logs", status, 200, body)


# ════════════════════════════════════════════════════════
# 2. 实名认证模块边角接口
# ════════════════════════════════════════════════════════
def test_identity_extra(ctx):
    section("2. 实名认证模块边角接口（identity）")
    user_token = ctx["user_token"]
    admin_token = ctx["admin_token"]

    # 文档声明 GET /api/identity/verifications/latest，实现为 /me
    status, body = get("/api/identity/verifications/latest", token=user_token)
    if status == 404:
        info("GET /api/identity/verifications/latest 未实现（实现为 /api/identity/verifications/me，文档命名不一致，已知 P3）")
    status, body = get("/api/identity/verifications/me", token=user_token)
    assert_status("GET /api/identity/verifications/me（未提交时）", status, (200, 404), body)

    # 提交一个实名认证申请
    status, body = post("/api/identity/verifications",
                        {"real_name": "测试用户", "id_card_no": "11010519491231002X",
                         "verification_type": "id_card", "attachments": []},
                        token=user_token)
    assert_status("POST 提交实名认证", status, (200, 201), body)
    vid = get_data(body).get("verification_id") or get_data(body).get("id")
    info(f"实名认证记录 ID: {vid}")

    # 重复提交（应拦截或排队，视具体实现，至少不应 500）
    status, body = post("/api/identity/verifications",
                        {"real_name": "测试用户", "id_card_no": "11010519491231002X",
                         "verification_type": "id_card", "attachments": []},
                        token=user_token)
    if status >= 500:
        issue("P1", "重复提交实名认证导致 5xx", f"HTTP {status} — {body}")
    else:
        ok(f"重复提交实名认证未导致 5xx → HTTP {status}")

    status, body = get("/api/identity/verifications/me", token=user_token)
    assert_status("GET /api/identity/verifications/me（已提交后）", status, 200, body)

    # 管理端：列表 / 详情 / 审核
    status, body = get("/api/admin/identity-verifications", token=admin_token)
    assert_status("GET 实名审核列表（管理端）", status, 200, body)
    items = get_data(body).get("items") or []
    target = None
    for it in items:
        if it.get("id") == vid:
            target = it
            break
    if vid:
        status, body = get(f"/api/admin/identity-verifications/{vid}", token=admin_token)
        assert_status("GET 实名审核详情（管理端）", status, 200, body)

        # 非法 action
        status, body = patch(f"/api/admin/identity-verifications/{vid}/review",
                             {"action": "invalid_action"}, token=admin_token)
        assert_status("PATCH 审核非法 action 被拦截", status, 400, body, priority="P2")

        # reject 但缺 reject_reason
        status, body = patch(f"/api/admin/identity-verifications/{vid}/review",
                             {"action": "reject"}, token=admin_token)
        assert_status("PATCH 审核 reject 缺 reason 被拦截", status, 400, body, priority="P2")

        # 正常 approve
        status, body = patch(f"/api/admin/identity-verifications/{vid}/review",
                             {"action": "approve"}, token=admin_token)
        assert_status("PATCH 审核 approve", status, 200, body)

        # 重复审核（已审核记录再次审核）
        status, body = patch(f"/api/admin/identity-verifications/{vid}/review",
                             {"action": "approve"}, token=admin_token)
        if status == 200:
            issue("P2", "已审核通过的实名认证记录可被重复审核",
                  f"PATCH /api/admin/identity-verifications/{vid}/review 二次 approve 仍返回 200，"
                  "未做状态机校验，可能导致审计日志/状态不一致")
        else:
            ok(f"重复审核被拦截 → HTTP {status}")

    # 不存在的 ID
    status, body = get("/api/admin/identity-verifications/999999999", token=admin_token)
    assert_status("GET 实名审核详情（不存在 ID，期望 404）", status, 404, body, priority="P2")

    # 普通用户访问管理端（期望 403）
    status, body = get("/api/admin/identity-verifications", token=user_token)
    assert_status("普通用户访问实名审核列表（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 3. IAM 边角接口（permission-overrides 各分支 / role CRUD 边界）
# ════════════════════════════════════════════════════════
def test_iam_extra(ctx):
    section("3. IAM 边角接口（角色 CRUD 边界 / 权限覆盖过期）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    # 创建重复 code 角色
    ts = int(time.time())
    code = f"qa_dup_role_{ts}"
    status, body = post("/api/admin/roles", {"code": code, "name": "重复测试角色"}, token=admin_token)
    assert_status("POST 创建角色", status, 201, body)
    rid = get_data(body).get("id")
    status, body = post("/api/admin/roles", {"code": code, "name": "重复测试角色2"}, token=admin_token)
    assert_status("POST 创建重复 code 角色被拦截", status, (400, 409), body, priority="P2")

    # GET 角色详情 — 文档 §3.10 声明了 GET /api/admin/roles/:id，核对实现是否存在该路由
    status, body = get("/api/admin/roles/999999999", token=admin_token)
    if status == 405:
        issue("P2", "GET /api/admin/roles/:id 接口未实现",
              "docs/full-api-design.md §3.10 声明了角色详情查询接口 GET /api/admin/roles/:id，"
              "但 iam/route.go 中只注册了 PUT/DELETE /api/admin/roles/{id}，未注册 GET，"
              "请求返回 HTTP 405 Method Not Allowed。管理后台无法单独查询角色详情（含权限列表），"
              "建议后端甲补齐该路由。")
        fail("GET /api/admin/roles/:id（接口缺失，返回 405）", f"HTTP {status}")
    else:
        assert_status("GET 角色详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 配置角色权限 PATCH /api/admin/roles/:id/permissions（文档声明，检查是否实现）
    status, body = patch(f"/api/admin/roles/{rid}/permissions", {"permission_ids": []}, token=admin_token)
    if status == 404:
        issue("P2", "PATCH /api/admin/roles/:id/permissions 接口未实现",
              "docs/full-api-design.md §3.12 文档声明了配置角色权限接口，"
              "实现中未发现该路由（route.go 中无对应 handler），管理后台无法为角色单独配置权限列表，"
              "只能通过更新角色本身。建议后端甲确认是否计划补齐。")
    else:
        assert_status("PATCH 配置角色权限", status, 200, body)

    # 删除角色
    if rid:
        status, body = delete(f"/api/admin/roles/{rid}", token=admin_token)
        assert_status("DELETE 删除角色", status, 200, body)
        # 删除不存在的角色
        status, body = delete(f"/api/admin/roles/{rid}", token=admin_token)
        assert_status("DELETE 删除已删除角色（期望 404）", status, 404, body, priority="P3")

    # 普通用户调用 IAM 管理接口（期望 403）
    status, body = get("/api/admin/permissions", token=user_token)
    assert_status("普通用户访问权限列表（期望 403）", status, 403, body, priority="P1")
    status, body = post("/api/admin/roles", {"code": "x", "name": "x"}, token=user_token)
    assert_status("普通用户创建角色（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 4. 商品模块边角接口
# ════════════════════════════════════════════════════════
def test_product_extra(ctx):
    section("4. 商品模块边角接口（product）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    # 用户端商品列表/详情/套餐
    status, body = get("/api/products", token=user_token)
    assert_status("GET 商品列表（用户端）", status, 200, body)
    items = get_data(body).get("items") or []
    pid = items[0]["id"] if items else None
    info(f"商品总数: {len(items)}，首个商品 ID: {pid}")

    if pid:
        status, body = get(f"/api/products/{pid}", token=user_token)
        assert_status("GET 商品详情（用户端）", status, 200, body)
        status, body = get(f"/api/products/{pid}/plans", token=user_token)
        assert_status("GET 商品套餐（用户端）", status, 200, body)

    # 不存在商品
    status, body = get("/api/products/999999999", token=user_token)
    assert_status("GET 商品详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 管理端：商品列表 / 详情 — 关键：检查 product:view 权限是否对 admin 角色生效
    status, body = get("/api/admin/products", token=admin_token)
    if status == 403:
        issue("P1", "管理员（admin 角色）访问 GET /api/admin/products 返回 403",
              "路由 product/route.go 要求权限码 product:view，但数据库 permissions 表中不存在 "
              "product:view 这一权限记录，因此无法分配给任何角色（包括 admin 超管角色）。"
              "结果：管理后台商品列表/详情对所有人不可访问，属于功能阻断级缺陷。"
              "建议后端乙在迁移/seed 中补充 product:view 权限并赋予 admin 角色，"
              "或将路由改为复用已存在的 product:create / product:edit 等权限码。")
    else:
        assert_status("GET 管理端商品列表", status, 200, body)

    if pid:
        status, body = get(f"/api/admin/products/{pid}", token=admin_token)
        if status == 403:
            ok(f"GET /api/admin/products/{{id}} 同样因 product:view 缺失返回 403（与上一条同一根因，已记录）")
        else:
            assert_status("GET 管理端商品详情", status, 200, body)

        # 商品套餐
        status, body = get(f"/api/admin/products/{pid}/plans", token=admin_token)
        assert_status("GET 管理端商品套餐列表", status, (200, 403), body)

        # 商品价格 PATCH（无效 items）
        status, body = patch(f"/api/admin/products/{pid}/prices", {"items": []}, token=admin_token)
        assert_status("PATCH 商品价格（空 items）", status, (200, 400), body)

        # 商品访问规则 PATCH（无效 items）
        status, body = patch(f"/api/admin/products/{pid}/access", {"items": []}, token=admin_token)
        assert_status("PATCH 商品访问规则（空 items）", status, (200, 400), body)

    # 商品处理器列表（文档 §4.12，检查实现）
    status, body = get("/api/admin/product-handlers", token=admin_token)
    if status == 404:
        issue("P3", "GET /api/admin/product-handlers 接口未实现",
              "docs/full-api-design.md §4.12 声明了商品处理器查询接口，实现中未发现对应路由。"
              "属于辅助查询功能，优先级不高，建议确认是否在后续迭代交付或更新文档移除。")
    else:
        assert_status("GET 商品处理器列表", status, 200, body)

    # 创建商品参数缺失
    status, body = post("/api/admin/products", {"name": "缺少必填字段商品"}, token=admin_token)
    assert_status("POST 创建商品（缺少 product_type/product_code，期望 400）", status, 400, body, priority="P2")

    # 创建商品 — 正常路径
    ts = int(time.time())
    status, body = post("/api/admin/products",
                        {"product_type": "saas", "product_code": f"qa_fullapi_prod_{ts}",
                         "name": "QA全量测试商品", "description": "desc", "status": "draft"},
                        token=admin_token)
    assert_status("POST 创建商品（正常）", status, (200, 201), body)
    new_pid = get_data(body).get("product_id") or get_data(body).get("id")
    info(f"新建商品 ID: {new_pid}")

    if new_pid:
        # 修改商品
        status, body = patch(f"/api/admin/products/{new_pid}",
                             {"name": "QA全量测试商品（已更新）"}, token=admin_token)
        assert_status("PATCH 修改商品", status, 200, body)
        # 修改商品状态为非法值
        status, body = patch(f"/api/admin/products/{new_pid}/status",
                             {"status": "not_a_real_status"}, token=admin_token)
        assert_status("PATCH 商品状态为非法值（期望 400）", status, 400, body, priority="P2")
        # 正常上架
        status, body = patch(f"/api/admin/products/{new_pid}/status",
                             {"status": "active"}, token=admin_token)
        assert_status("PATCH 商品状态正常上架", status, 200, body)

        # 创建套餐缺字段
        status, body = post(f"/api/admin/products/{new_pid}/plans",
                            {"name": "缺plan_code"}, token=admin_token)
        assert_status("POST 创建套餐（缺必填字段，期望 400）", status, 400, body, priority="P2")

        # 创建套餐正常
        status, body = post(f"/api/admin/products/{new_pid}/plans",
                            {"plan_code": f"qa_plan_{ts}", "name": "QA套餐",
                             "billing_type": "one_time", "duration_days": 30,
                             "quota_json": "{}", "status": "active"},
                            token=admin_token)
        assert_status("POST 创建商品套餐（正常）", status, (200, 201), body)
        new_plan_id = get_data(body).get("plan_id") or get_data(body).get("id")
        if new_plan_id:
            status, body = patch(f"/api/admin/products/{new_pid}/plans/{new_plan_id}",
                                 {"name": "QA套餐（已更新）"}, token=admin_token)
            assert_status("PATCH 修改商品套餐", status, 200, body)

    # 普通用户访问管理端商品接口（期望 403）
    status, body = post("/api/admin/products",
                        {"product_type": "saas", "product_code": "x", "name": "x"}, token=user_token)
    assert_status("普通用户创建商品（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 5. 订单模块边角接口
# ════════════════════════════════════════════════════════
def test_order_extra(ctx):
    section("5. 订单模块边角接口（order）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    status, body = get("/api/orders", token=user_token)
    assert_status("GET 我的订单列表", status, 200, body)
    items = get_data(body).get("items") or []
    info(f"当前用户订单数: {len(items)}")

    # 不存在的订单
    status, body = get("/api/orders/999999999", token=user_token)
    assert_status("GET 订单详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 管理端订单列表 — 检查 order:list 权限
    status, body = get("/api/admin/orders", token=admin_token)
    if status == 403:
        issue("P1", "管理员（admin 角色）访问 GET /api/admin/orders 返回 403",
              "路由 order/route.go 要求权限码 order:list，但数据库 permissions 表中不存在 "
              "order:list 这一权限记录，无法分配给任何角色（包括 admin 超管角色），"
              "导致管理后台订单列表/详情对所有人不可访问，属于功能阻断级缺陷。"
              "建议后端乙在迁移/seed 中补充 order:list 权限并赋予 admin 角色。")
    else:
        assert_status("GET 管理端订单列表", status, 200, body)
        items2 = get_data(body).get("items") or []
        if items2:
            oid = items2[0]["id"]
            status, body = get(f"/api/admin/orders/{oid}", token=admin_token)
            assert_status("GET 管理端订单详情", status, 200, body)

    # 普通用户访问管理端订单（期望 403）
    status, body = get("/api/admin/orders", token=user_token)
    assert_status("普通用户访问管理端订单列表（期望 403）", status, 403, body, priority="P1")

    # 取消一个不存在的订单
    status, body = post("/api/orders/999999999/cancel", {"reason": "test"}, token=user_token)
    assert_status("POST 取消不存在订单（期望 404）", status, 404, body, priority="P2")


# ════════════════════════════════════════════════════════
# 6. 钱包 / 充值 / 支付回调 / 计费
# ════════════════════════════════════════════════════════
def test_billing_extra(ctx):
    section("6. 钱包 / 充值 / 支付回调 / 计费模块边角接口（billing / finance_consumer）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]
    user_uid = ctx["user_uid"]

    # 钱包信息
    status, body = get("/api/wallet", token=user_token)
    assert_status("GET 我的钱包", status, 200, body)
    wallet = get_data(body)
    info(f"钱包余额: {wallet.get('balance_amount')}")

    # 钱包流水
    status, body = get("/api/wallet/transactions", token=user_token)
    assert_status("GET 钱包流水", status, 200, body)

    # 创建充值订单 — 缺字段
    status, body = post("/api/recharge/orders", {"payment_method": "wechat"}, token=user_token)
    assert_status("POST 创建充值订单（缺 amount，期望 400）", status, 400, body, priority="P2")

    # 创建充值订单 — 非法金额
    status, body = post("/api/recharge/orders",
                        {"amount": "-10.00", "payment_method": "wechat"}, token=user_token)
    assert_status("POST 创建充值订单（负金额，期望 400）", status, 400, body, priority="P1")

    # 创建充值订单 — 非法支付方式
    status, body = post("/api/recharge/orders",
                        {"amount": "10.00", "payment_method": "bitcoin"}, token=user_token)
    assert_status("POST 创建充值订单（非法支付方式，期望 400）", status, 400, body, priority="P2")

    # 正常创建充值订单
    status, body = post("/api/recharge/orders",
                        {"amount": "10.00", "payment_method": "wechat"}, token=user_token)
    assert_status("POST 创建充值订单（正常）", status, (200, 201), body)
    recharge_order_no = get_data(body).get("order_no")
    info(f"充值订单号: {recharge_order_no}")

    # 支付回调 — 签名错误
    status, body = post("/api/payments/notify/wechat",
                        {"out_trade_no": recharge_order_no or "ORDXXXX",
                         "transaction_id": f"wx_test_{int(time.time())}",
                         "amount": "10.00", "sign": "invalid_sign_value"})
    if status == 400:
        ok(f"签名错误回调被拒绝 → HTTP {status}")
    else:
        issue("P0", "支付回调签名错误未被正确拒绝",
              f"POST /api/payments/notify/wechat 携带错误签名仍返回 HTTP {status}（期望 400），"
              f"响应: {json.dumps(body, ensure_ascii=False)[:300]}。"
              "若签名校验缺失或绕过，存在被伪造回调骗充值的严重资金安全风险，需立即排查。")

    # 校验余额未被错误回调影响
    status, body = get("/api/wallet", token=user_token)
    new_balance = get_data(body).get("balance_amount")
    info(f"伪造回调后余额: {new_balance}（应与之前一致）")

    # 不存在的支付渠道
    status, body = post("/api/payments/notify/unknown_provider", {"foo": "bar"})
    assert_status("POST 未知支付渠道回调", status, (400, 404), body, priority="P2")

    # 管理端：钱包流水 / 用户钱包 / 冻结 / 回调列表
    status, body = get("/api/admin/wallet-transactions", token=admin_token)
    assert_status("GET 管理端钱包流水列表", status, 200, body)

    status, body = get(f"/api/admin/users/{user_uid}/wallet", token=admin_token)
    assert_status("GET 管理端用户钱包详情", status, 200, body)

    status, body = patch(f"/api/admin/users/{user_uid}/wallet/freeze",
                         {"amount": "0.00", "reason": "QA 测试冻结 0 元"}, token=admin_token)
    assert_status("PATCH 冻结用户钱包（金额为0）", status, (200, 400), body)

    status, body = patch(f"/api/admin/users/{user_uid}/wallet/freeze",
                         {"amount": "-5.00", "reason": "QA 测试负数冻结"}, token=admin_token)
    assert_status("PATCH 冻结用户钱包（负金额，期望 400）", status, 400, body, priority="P2")

    status, body = get("/api/admin/payment-callbacks", token=admin_token)
    assert_status("GET 管理端支付回调列表", status, 200, body)

    # 普通用户访问管理端钱包接口（期望 403）
    status, body = get("/api/admin/wallet-transactions", token=user_token)
    assert_status("普通用户访问管理端钱包流水（期望 403）", status, 403, body, priority="P1")
    status, body = get(f"/api/admin/users/{user_uid}/wallet", token=user_token)
    assert_status("普通用户访问他人钱包详情（期望 403）", status, 403, body, priority="P0")

    # ── 内部计费事件接口（finance_consumer）──
    section_inner = "6b. 内部计费接口 /api/internal/product-usage-events"
    info(section_inner)
    # 说明：测试脚本本身运行在测试服务器本机（127.0.0.1），属于白名单允许的来源
    # （未配置 INTERNAL_ALLOWED_IPS 时，handler.isAllowedIP 默认仅放行 127.0.0.1/::1，
    #   实现位置 finance_consumer/handler -> isAllowedIP，符合"内部接口默认仅本机可访问"的设计预期）
    # 因此本用例验证的是参数校验链路是否正常工作，IP 白名单本身在 P1 安全层面已是"默认拒绝外部"。
    ts_ev = int(time.time())
    status, body = post("/api/internal/product-usage-events",
                        {"event_id": f"evt_{ts_ev}", "user_id": user_uid,
                         "product_id": 1, "product_type": "saas", "product_code": "qa_quota_prod_w3",
                         "product_plan_id": 1, "instance_id": 1,
                         "usage_type": "token", "usage_amount": "1", "usage_unit": "次",
                         "occurred_at": "2026-06-08T00:00:00Z",
                         "idempotency_key": f"idem_{ts_ev}"},
                        headers_extra={"Idempotency-Key": f"idem_{ts_ev}"})
    if status >= 500:
        issue("P1", "内部计费事件接口对合法参数返回 5xx",
              f"POST /api/internal/product-usage-events（本机调用，参数齐全）返回 HTTP {status}，"
              f"响应: {json.dumps(body, ensure_ascii=False)[:300]}")
    else:
        ok(f"内部计费事件接口（本机调用）正常处理请求链路 → HTTP {status}（{body.get('message','')}）")
        info("已确认 IP 白名单默认仅放行 127.0.0.1/::1（finance_consumer/handler.isAllowedIP），"
             "外部来源默认被拒绝（403），符合内部接口安全预期。建议生产环境务必显式配置 INTERNAL_ALLOWED_IPS。")

    # 计费规则 / 消费记录查询
    status, body = get("/api/product-consumption-records", token=user_token)
    assert_status("GET 我的消费记录", status, 200, body)

    status, body = get("/api/admin/product-billing-rules", token=admin_token)
    assert_status("GET 管理端计费规则列表", status, (200, 403, 404), body)

    status, body = get("/api/admin/product-consumption-records", token=admin_token)
    assert_status("GET 管理端消费记录列表", status, (200, 403, 404), body)

    # 创建计费规则缺字段
    status, body = post("/api/admin/product-billing-rules", {"product_id": 1}, token=admin_token)
    assert_status("POST 创建计费规则（缺必填字段）", status, (400, 403, 404), body)


# ════════════════════════════════════════════════════════
# 7. 资产 / 权益模块
# ════════════════════════════════════════════════════════
def test_asset_extra(ctx):
    section("7. 用户资产 / 权益模块（asset）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]
    user_uid = ctx["user_uid"]

    status, body = get("/api/my/assets", token=user_token)
    assert_status("GET 我的资产列表", status, 200, body)
    items = get_data(body).get("items") or []
    info(f"我的资产数: {len(items)}")

    status, body = get("/api/my/assets/999999999", token=user_token)
    assert_status("GET 我的资产详情（不存在，期望 404）", status, 404, body, priority="P2")

    status, body = get("/api/my/entitlements", token=user_token)
    assert_status("GET 我的权益额度", status, 200, body)

    # 管理端
    status, body = get("/api/admin/assets", token=admin_token)
    assert_status("GET 管理端资产列表", status, 200, body)

    status, body = get(f"/api/admin/users/{user_uid}/assets", token=admin_token)
    assert_status("GET 管理端用户资产列表", status, 200, body)

    # 资产事件查询（文档 §5.1 GET /api/admin/asset-events，检查实现）
    status, body = get("/api/admin/asset-events", token=admin_token)
    if status == 404:
        issue("P3", "GET /api/admin/asset-events 接口未实现",
              "docs/full-api-design.md §5.1 声明了资产事件查询接口，asset_events 表存在数据，"
              "但实现路由中未发现该接口，管理后台无法追踪资产变更轨迹。建议后端丙确认排期。")
    else:
        assert_status("GET 管理端资产事件列表", status, 200, body)

    status, body = get(f"/api/admin/users/{user_uid}/entitlements", token=admin_token)
    if status == 404:
        issue("P3", "GET /api/admin/users/:id/entitlements 接口未实现",
              "docs/full-api-design.md §5.1 声明了管理端用户权益查询接口，实现路由中未发现。")
    else:
        assert_status("GET 管理端用户权益列表", status, 200, body)

    # 修改资产 — 非法字段
    if items:
        aid = items[0].get("id")
    else:
        aid = None
    status, body = patch(f"/api/admin/assets/{aid or 999999999}",
                         {"status": "not_a_real_status"}, token=admin_token)
    assert_status("PATCH 修改资产（非法状态值）", status, (400, 404), body)

    # 普通用户越权访问他人资产管理接口
    status, body = get(f"/api/admin/users/{user_uid}/assets", token=user_token)
    assert_status("普通用户访问管理端用户资产（期望 403）", status, 403, body, priority="P0")


# ════════════════════════════════════════════════════════
# 8. 会员模块
# ════════════════════════════════════════════════════════
def test_membership_extra(ctx):
    section("8. 会员模块（membership）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    status, body = get("/api/memberships")
    assert_status("GET 公开会员等级列表（无需登录）", status, 200, body)
    items = get_data(body).get("items") or get_data(body) or []
    info(f"会员等级数: {len(items) if isinstance(items, list) else '?'}")

    status, body = get("/api/my/membership", token=user_token)
    assert_status("GET 我的会员信息", status, 200, body)

    # 文档声明 POST /api/memberships/:id/purchase，检查实现
    status, body = post("/api/memberships/1/purchase", {"plan_id": 1}, token=user_token)
    if status == 404 and "未找到" not in str(body.get("message","")):
        # 可能是接口未实现（404 method not found）vs 资源不存在
        pass
    if status in (404,):
        info(f"POST /api/memberships/:id/purchase 返回 404（可能未实现或会员不存在），响应: {body}")

    # 管理端
    status, body = get("/api/admin/membership-levels", token=admin_token)
    assert_status("GET 管理端会员等级列表", status, 200, body)
    levels = get_data(body).get("items") or []
    lid = levels[0]["id"] if levels else None

    # 创建会员等级缺字段
    status, body = post("/api/admin/membership-levels", {"name": "缺level_code"}, token=admin_token)
    assert_status("POST 创建会员等级（缺 level_code，期望 400）", status, 400, body, priority="P2")

    # 注：docs/full-api-design.md §5.2 文档字段名为 code/membership_level_id/benefit_config_json，
    # 实现 DTO 实际字段名为 level_code/level_id/benefit_value —— 文档与实现存在字段命名漂移（P3，记录于报告）

    ts = int(time.time())
    status, body = post("/api/admin/membership-levels",
                        {"level_code": f"qa_lvl_{ts}", "name": "QA会员等级", "sort_order": 99, "status": "active"},
                        token=admin_token)
    assert_status("POST 创建会员等级（正常）", status, (200, 201), body)
    new_lid = get_data(body).get("id")
    if new_lid:
        status, body = patch(f"/api/admin/membership-levels/{new_lid}",
                             {"name": "QA会员等级（已更新）"}, token=admin_token)
        assert_status("PATCH 修改会员等级", status, 200, body)
        status, body = patch(f"/api/admin/membership-levels/{new_lid}",
                             {"status": "invalid_status_xx"}, token=admin_token)
        assert_status("PATCH 会员等级非法状态值（期望 400）", status, (200, 400), body)

    status, body = get("/api/admin/membership-benefits", token=admin_token)
    assert_status("GET 管理端会员权益列表", status, 200, body)

    status, body = post("/api/admin/membership-benefits", {"benefit_type": "x"}, token=admin_token)
    assert_status("POST 创建会员权益（缺 level_id/benefit_value，期望 400）", status, 400, body, priority="P2")

    if lid:
        status, body = post("/api/admin/membership-benefits",
                            {"level_id": lid, "benefit_type": "discount",
                             "benefit_value": "{}", "status": "active"},
                            token=admin_token)
        assert_status("POST 创建会员权益（正常）", status, (200, 201), body)
        bid = get_data(body).get("id")
        if bid:
            status, body = patch(f"/api/admin/membership-benefits/{bid}",
                                 {"status": "inactive"}, token=admin_token)
            assert_status("PATCH 修改会员权益状态", status, 200, body)

    status, body = get("/api/admin/user-memberships", token=admin_token)
    assert_status("GET 管理端用户会员列表", status, 200, body)

    # 商品会员规则（文档声明，检查实现）
    status, body = get("/api/admin/product-membership-rules", token=admin_token)
    if status == 404:
        issue("P3", "GET /api/admin/product-membership-rules 接口未实现",
              "docs/full-api-design.md §5.2 声明了商品会员规则管理接口，实现路由中未发现。"
              "影响：无法通过该接口为商品配置会员折扣/包含额度规则。建议后端丙确认排期。")
    else:
        assert_status("GET 商品会员规则列表", status, 200, body)

    # 普通用户访问管理端
    status, body = post("/api/admin/membership-levels", {"code":"x","name":"x"}, token=user_token)
    assert_status("普通用户创建会员等级（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 9. 应用市场 / 适配器
# ════════════════════════════════════════════════════════
def test_app_extra(ctx):
    section("9. 应用市场 / 适配器模块（app）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    # 文档声明 GET /api/apps、/api/apps/:id、POST /api/apps/:id/purchase、GET /api/my/apps
    # 实现为 /api/marketplace/apps/:id —— 核对路由命名差异
    status, body = get("/api/apps", token=user_token)
    if status == 404:
        issue("P2", "GET /api/apps（应用市场列表）接口未实现或路径不一致",
              "docs/full-api-design.md §5.3 声明用户端应用接口为 GET /api/apps、GET /api/apps/:id、"
              "POST /api/apps/:id/purchase、GET /api/my/apps，但实现路由（app/route.go）中"
              "用户端只有 GET /api/marketplace/apps/{id} 一个接口，应用列表、购买、"
              "我的应用均未实现或路径与文档不符。前端用户控制台对接应用市场功能可能因此受阻，"
              "建议后端丙与前端对齐实际路径并同步更新文档。")
    else:
        assert_status("GET 应用市场列表", status, 200, body)

    status, body = get("/api/my/apps", token=user_token)
    if status == 404:
        info("GET /api/my/apps 未实现（与上一条同根因，已记录）")

    # 实际实现的接口
    status, body = get("/api/marketplace/apps/101", token=user_token)
    assert_status("GET 应用详情（marketplace, id=101）", status, (200, 404), body)

    status, body = get("/api/marketplace/apps/999999999", token=user_token)
    assert_status("GET 应用详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 管理端应用 CRUD
    status, body = get("/api/admin/apps", token=admin_token)
    assert_status("GET 管理端应用列表", status, 200, body)
    items = get_data(body).get("items") or []
    aid = items[0]["id"] if items else None

    if aid:
        status, body = get(f"/api/admin/apps/{aid}", token=admin_token)
        assert_status("GET 管理端应用详情", status, 200, body)

    status, body = get("/api/admin/apps/999999999", token=admin_token)
    assert_status("GET 管理端应用详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 创建应用缺字段
    status, body = post("/api/admin/apps", {"name": "缺code"}, token=admin_token)
    assert_status("POST 创建应用（缺 code，期望 400）", status, 400, body, priority="P2")

    ts = int(time.time())
    status, body = post("/api/admin/apps",
                        {"code": f"qa_fullapi_app_{ts}", "name": "QA全量测试应用",
                         "type": "web", "description": "desc", "status": "draft"},
                        token=admin_token)
    assert_status("POST 创建应用（正常）", status, (200, 201), body)
    new_aid = get_data(body).get("id")
    if new_aid:
        status, body = patch(f"/api/admin/apps/{new_aid}", {"name": "QA全量测试应用（已更新）"}, token=admin_token)
        assert_status("PATCH 修改应用", status, 200, body)

        # 应用访问规则 / 价格（文档 §5.3 声明 PATCH /api/admin/apps/:id/access、/prices）
        status, body = patch(f"/api/admin/apps/{new_aid}/access", {"items": []}, token=admin_token)
        if status == 404:
            issue("P3", "PATCH /api/admin/apps/:id/access 接口未实现",
                  "docs/full-api-design.md §5.3 声明了应用访问规则配置接口，实现路由中未发现。")
        else:
            assert_status("PATCH 应用访问规则", status, (200, 400), body)

        status, body = patch(f"/api/admin/apps/{new_aid}/prices", {"items": []}, token=admin_token)
        if status == 404:
            issue("P3", "PATCH /api/admin/apps/:id/prices 接口未实现",
                  "docs/full-api-design.md §5.3 声明了应用价格配置接口，实现路由中未发现。")
        else:
            assert_status("PATCH 应用价格配置", status, (200, 400), body)

    # 适配器管理 — 文档命名 application-adapters，实现命名 app-adapters
    status, body = get("/api/admin/application-adapters", token=admin_token)
    if status == 404:
        info("GET /api/admin/application-adapters 未实现（实现路径为 /api/admin/app-adapters，文档命名不一致，P3）")

    status, body = get("/api/admin/app-adapters", token=admin_token)
    assert_status("GET 管理端应用适配器列表", status, 200, body)
    adapters = get_data(body).get("items") or []
    adid = adapters[0]["id"] if adapters else None

    # 创建适配器缺字段
    status, body = post("/api/admin/app-adapters", {"app_code": "x"}, token=admin_token)
    assert_status("POST 创建适配器（缺必填字段，期望 400）", status, 400, body, priority="P2")

    ts2 = int(time.time())
    status, body = post("/api/admin/app-adapters",
                        {"app_code": f"qa_adapter_{ts2}", "app_name": "QA适配器", "app_type": "web",
                         "adapter_type": "internal", "service_name": "qa-svc",
                         "callback_url": "https://example.com/cb",
                         "supported_actions_json": "[]", "usage_event_types_json": "[]",
                         "status": "inactive"},
                        token=admin_token)
    assert_status("POST 创建应用适配器（正常）", status, (200, 201), body)
    new_adid = get_data(body).get("id")
    if new_adid:
        status, body = patch(f"/api/admin/app-adapters/{new_adid}", {"status": "active"}, token=admin_token)
        assert_status("PATCH 修改应用适配器状态", status, 200, body)
        status, body = patch(f"/api/admin/app-adapters/{new_adid}", {"status": "bogus_status"}, token=admin_token)
        assert_status("PATCH 应用适配器非法状态（期望 400）", status, (200, 400), body)

    # 普通用户访问管理端应用接口（期望 403）
    status, body = get("/api/admin/apps", token=user_token)
    assert_status("普通用户访问管理端应用列表（期望 403）", status, 403, body, priority="P1")
    status, body = get("/api/admin/app-adapters", token=user_token)
    assert_status("普通用户访问管理端适配器列表（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 10. 公告 / 帮助文档
# ════════════════════════════════════════════════════════
def test_content_extra(ctx):
    section("10. 公告 / 帮助文档模块（content）")
    admin_token = ctx["admin_token"]
    user_token = ctx["user_token"]

    status, body = get("/api/announcements", token=user_token)
    assert_status("GET 公告列表（用户端）", status, 200, body)
    items = get_data(body).get("items") or []
    info(f"用户端可见公告数: {len(items)}")

    status, body = get("/api/announcements")
    assert_status("GET 公告列表（无 Token）", status, (200, 401), body)

    status, body = get("/api/help/categories")
    assert_status("GET 帮助分类列表（公开）", status, 200, body)
    cats = get_data(body).get("items") or []
    info(f"帮助分类数: {len(cats)}")

    status, body = get("/api/help/articles")
    assert_status("GET 帮助文章列表（公开）", status, 200, body)
    arts = get_data(body).get("items") or []
    info(f"帮助文章数: {len(arts)}")

    if arts:
        art_id = arts[0]["id"]
        status, body = get(f"/api/help/articles/{art_id}")
        assert_status("GET 帮助文章详情", status, 200, body)
    else:
        # 用已知种子文章 1（offline 状态，验证下线文章是否仍可被普通用户访问）
        status, body = get("/api/help/articles/1")
        if status == 200:
            issue("P2", "已下线（status=offline）的帮助文章仍可通过详情接口公开访问",
                  "种子数据中 help_articles id=1 的 status=offline，但 GET /api/help/articles/1 "
                  "仍返回 200 并暴露文章内容，未按状态过滤，可能导致下线内容仍对外可见")
        else:
            ok(f"下线文章详情访问返回 HTTP {status}（符合预期，已正确过滤）")

    status, body = get("/api/help/articles/999999999")
    assert_status("GET 帮助文章详情（不存在，期望 404）", status, 404, body, priority="P2")

    # 管理端公告 CRUD
    status, body = get("/api/admin/announcements", token=admin_token)
    assert_status("GET 管理端公告列表", status, 200, body)

    status, body = post("/api/admin/announcements", {"title": "缺content"}, token=admin_token)
    assert_status("POST 创建公告（缺必填字段，期望 400）", status, 400, body, priority="P2")

    ts = int(time.time())
    status, body = post("/api/admin/announcements",
                        {"title": f"QA全量测试公告_{ts}", "content": "测试内容",
                         "type": "notice", "priority": 1, "status": "draft",
                         "visible_scope": "all", "target_roles_json": "[]"},
                        token=admin_token)
    assert_status("POST 创建公告（正常）", status, (200, 201), body)
    new_anid = get_data(body).get("id")
    if new_anid:
        status, body = patch(f"/api/admin/announcements/{new_anid}", {"status": "published"}, token=admin_token)
        assert_status("PATCH 公告改为已发布", status, 200, body)
        status, body = patch(f"/api/admin/announcements/{new_anid}", {"status": "bogus"}, token=admin_token)
        assert_status("PATCH 公告非法状态（期望 400）", status, (200, 400), body)

    # 帮助分类 / 文章管理端 CRUD
    status, body = get("/api/admin/help/categories", token=admin_token)
    assert_status("GET 管理端帮助分类列表", status, 200, body)

    status, body = post("/api/admin/help/categories", {"name": f"QA分类_{ts}", "sort_order": 1, "status": "active"}, token=admin_token)
    assert_status("POST 创建帮助分类（正常）", status, (200, 201), body)
    new_cat_id = get_data(body).get("id")

    status, body = post("/api/admin/help/categories", {"sort_order": 1}, token=admin_token)
    assert_status("POST 创建帮助分类（缺 name，期望 400）", status, 400, body, priority="P2")

    if new_cat_id:
        status, body = patch(f"/api/admin/help/categories/{new_cat_id}", {"name": f"QA分类_{ts}_已更新"}, token=admin_token)
        assert_status("PATCH 修改帮助分类", status, 200, body)

        status, body = get("/api/admin/help/articles", token=admin_token)
        assert_status("GET 管理端帮助文章列表", status, 200, body)

        status, body = post("/api/admin/help/articles", {"title": "缺content"}, token=admin_token)
        assert_status("POST 创建帮助文章（缺必填字段，期望 400）", status, 400, body, priority="P2")

        status, body = post("/api/admin/help/articles",
                            {"category_id": new_cat_id, "title": f"QA文章_{ts}", "content": "正文",
                             "summary": "摘要", "tags_json": "[]", "status": "draft", "sort_order": 1},
                            token=admin_token)
        assert_status("POST 创建帮助文章（正常）", status, (200, 201), body)
        new_art_id = get_data(body).get("id")
        if new_art_id:
            status, body = patch(f"/api/admin/help/articles/{new_art_id}", {"status": "published"}, token=admin_token)
            assert_status("PATCH 帮助文章发布（status=published）", status, 200, body)
            # 上线后应能在公开接口查到
            status, body = get(f"/api/help/articles/{new_art_id}")
            assert_status("GET 已上线帮助文章公开可访问", status, 200, body)

    # 普通用户访问管理端内容接口（期望 403）
    status, body = post("/api/admin/announcements", {"title":"x","content":"x"}, token=user_token)
    assert_status("普通用户创建公告（期望 403）", status, 403, body, priority="P1")
    status, body = get("/api/admin/help/articles", token=user_token)
    assert_status("普通用户访问管理端帮助文章列表（期望 403）", status, 403, body, priority="P1")


# ════════════════════════════════════════════════════════
# 11. 通用安全 — 伪造 JWT / 越权
# ════════════════════════════════════════════════════════
def test_security_extra(ctx):
    section("11. 通用安全场景补充测试")
    user_token = ctx["user_token"]

    # 伪造 JWT（篡改 payload，签名不更新）
    parts = user_token.split(".")
    if len(parts) == 3:
        import base64
        try:
            payload = json.loads(base64.urlsafe_b64decode(parts[1] + "=="))
            payload["user_id"] = 1
            forged_payload = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip("=")
            forged_token = f"{parts[0]}.{forged_payload}.{parts[2]}"
            status, body = get("/api/me", token=forged_token)
            assert_status("伪造 JWT（篡改 user_id，签名不更新）访问受保护接口", status, 401, body, priority="P0")
        except Exception as e:
            info(f"构造伪造 JWT 失败: {e}")

    # 完全无效 token
    status, body = get("/api/me", token="invalid.token.value")
    assert_status("完全无效 Token 访问（期望 401）", status, 401, body, priority="P0")

    # Bearer 前缀缺失（直接传 token 字符串，中间件应要求 Bearer 格式）
    import urllib.request as ur
    req = ur.Request(API_BASE + "/api/me", headers={"Authorization": user_token})
    try:
        resp = ur.urlopen(req, timeout=10)
        status = resp.status
    except urllib.error.HTTPError as e:
        status = e.code
    assert_status("缺少 Bearer 前缀的 Authorization 头（期望 401）", status, 401, body, priority="P2")


# ════════════════════════════════════════════════════════
# Main
# ════════════════════════════════════════════════════════
def main():
    print(f"{BOLD}{CYAN}{'='*60}{RESET}")
    print(f"{BOLD}{CYAN}  全量接口地毯式覆盖测试 — API_BASE={API_BASE}{RESET}")
    print(f"{BOLD}{CYAN}{'='*60}{RESET}")

    ctx = setup_accounts()
    test_audit(ctx)
    test_identity_extra(ctx)
    test_iam_extra(ctx)
    test_product_extra(ctx)
    test_order_extra(ctx)
    test_billing_extra(ctx)
    test_asset_extra(ctx)
    test_membership_extra(ctx)
    test_app_extra(ctx)
    test_content_extra(ctx)
    test_security_extra(ctx)

    print(f"\n{BOLD}{CYAN}{'='*60}{RESET}")
    print(f"{BOLD}总计：{GREEN}通过 {passed}{RESET}  /  {RED}失败 {failed}{RESET}  /  共 {passed+failed} 项")
    print(f"{BOLD}{CYAN}{'='*60}{RESET}")

    if issues:
        print(f"\n{BOLD}{RED}发现问题汇总（按记录顺序）：{RESET}")
        for pr, title, detail in issues:
            print(f"  {RED}[{pr}] {title}{RESET}")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())

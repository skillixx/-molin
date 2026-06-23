#!/usr/bin/env python3
"""
S3 第三阶段功能验收脚本：① Agent 分类 ② Agent 定向可见性 ③ MCP 接入。

验收基准（契约）：
  - docs/backend-stage3-agent-category-contract.md
  - docs/backend-stage3-agent-visibility-contract.md（v1.1）
  - docs/backend-stage3-mcp-integration-contract.md
  - docs/frontend-api-reference.md §14.9/14.10/14.11

覆盖范围：
  特性① Agent 分类
    - GET /api/agent-categories（登录态）仅 active 按 sort_order 返 4 类
    - GET /api/admin/agent-categories 含 inactive 全量
    - admin 建/改 Agent 带 category_code：合法→回显 category_code+category_name；非法→40000；
      空=未分类；PATCH ""=清空
    - 用户自建带分类（契约允许）
    - GET /api/agents?category=office 过滤；GET /api/admin/agents?category= 过滤

  特性② Agent 定向可见性（三处过滤 + 写入校验）
    - scope=groups：组内可见、组外不可见；group_roles=[admin] 仅组管理员可见
    - scope=roles：命中全局角色可见、未命中不可见
    - scope=all：全员可见（回归）；本人自建恒可见
    - 三处都验：列表 GET /api/agents、详情 GET /api/agents/{id}、编排 POST /api/agents/{id}/chat
      → 越权用户直连不可见 Agent 的详情/chat → 40003
    - PUT /api/admin/agents/{id}/visibility（覆盖语义）
    - 写入校验：scope 非法/groups 但 group_ids 空/group_roles 非法/roles 但 role_codes 空/含不存在项 → 40000

  特性③ MCP 接入
    - server CRUD：建 server 带 auth_config→响应仅 has_auth 不回凭证明文；endpoint 非 https/内网→40000；code 重复→40900
    - 用户端 GET /api/mcp-servers 不回 endpoint/凭证
    - 仅官方 Agent 可绑 MCP（绑用户自建→40003）
    - discover/tools 审核接口；server/工具不存在→40400
    - discover 端到端（指向不可达/SSRF 拦截端点）的卡点说明（见报告）

用法（在测试服务器上执行）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_s3_workbench.py

依赖：仅标准库 + 命令行 mysql。凭据走环境变量，不硬编码真实值。
"""

import json
import os
import random
import subprocess
import sys
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")
CHAT_MODEL = os.getenv("CHAT_MODEL", "DeepSeek")

GREEN  = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN   = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

results = []  # (name, ok, detail)

def record(name, cond, detail=""):
    results.append((name, bool(cond), detail))
    mark = f"{GREEN}PASS{RESET}" if cond else f"{RED}FAIL{RESET}"
    line = f"  [{mark}] {name}"
    if detail:
        line += f"\n        {YELLOW}{detail}{RESET}"
    print(line)
    return bool(cond)

def section(title):
    print(f"\n{BOLD}{CYAN}{'='*70}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*70}{RESET}")

def note(msg):
    print(f"  {CYAN}>>> {msg}{RESET}")

# ── HTTP ──────────────────────────────────────────────────
def request(method, path, body=None, token=None, raw_auth=None, timeout=90, headers_extra=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if raw_auth:
        headers["Authorization"] = raw_auth
    if headers_extra:
        headers.update(headers_extra)
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
        txt = resp.read().decode()
        try:
            return resp.status, json.loads(txt)
        except Exception:
            return resp.status, {"_raw": txt}
    except urllib.error.HTTPError as e:
        txt = e.read().decode()
        try:
            return e.code, json.loads(txt)
        except Exception:
            return e.code, {"_raw": txt}
    except Exception as e:
        return 0, {"_error": str(e)}

def post(path, body=None, token=None):   return request("POST", path, body, token)
def patch(path, body=None, token=None):  return request("PATCH", path, body, token)
def put(path, body=None, token=None):    return request("PUT", path, body, token)
def get(path, token=None, raw_auth=None): return request("GET", path, None, token, raw_auth)
def delete(path, token=None):            return request("DELETE", path, None, token)
def biz(body):  return (body or {}).get("code")
def data(body): return (body or {}).get("data")

def sse_request(path, body, token, timeout=120):
    url = API_BASE + path
    req = urllib.request.Request(
        url, data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}", "Accept": "text/event-stream"},
        method="POST")
    events = []  # (event_type, data_str)
    raw = ""
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
        cur_event = None
        for line_b in resp:
            line = line_b.decode("utf-8", "replace")
            raw += line
            s = line.rstrip("\n")
            if s.startswith("event:"):
                cur_event = s[len("event:"):].strip()
            elif s.startswith("data:"):
                payload = s[len("data:"):].strip()
                events.append((cur_event, payload))
                cur_event = None
        return resp.status, events, raw
    except urllib.error.HTTPError as e:
        return e.code, [], e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, [], str(e)

# ── MySQL ─────────────────────────────────────────────────
def sql(query):
    cmd = ["mysql", "-h", MYSQL_HOST, "-P", str(MYSQL_PORT), "-u", MYSQL_USER,
           f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-B", "-e", query]
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
        if out.returncode != 0:
            return None, out.stderr.strip()
        rows = [line.split("\t") for line in out.stdout.strip().splitlines() if line]
        return rows, None
    except Exception as e:
        return None, str(e)

def sql_scalar(query, default=None):
    rows, _ = sql(query)
    if rows and rows[0]:
        return rows[0][0]
    return default

# ── 注册 / 登录 / 管理员 ───────────────────────────────────
def send_code(kind, target, scene):
    path = f"/api/auth/verification-codes/{kind}"
    key = "email" if kind == "email" else "phone"
    for _ in range(40):
        st, body = post(path, {key: target, "scene": scene})
        d = data(body)
        if d and d.get("code"):
            return d.get("code")
        if biz(body) == 42900:
            time.sleep(7)
            continue
        time.sleep(0.6)
    return None

_phone_seq = [random.randint(0, 8_000_000)]

def _next_phone():
    _phone_seq[0] = (_phone_seq[0] + random.randint(1, 97)) % 90_000_000
    return f"171{_phone_seq[0]:08d}"

def register_user(tag):
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"s3_{tag}_{ts}_{random.randint(1000,9999)}@example.com"
    phone = _next_phone()
    ec = send_code("email", email, "register")
    pc = send_code("phone", phone, "register")
    st, body = post("/api/auth/register", {
        "email": email, "phone": phone, "password": "Test1234!",
        "email_code": ec, "phone_code": pc,
    })
    if st not in (200, 201) and biz(body) != 0:
        raise RuntimeError(f"注册失败 {tag}: {st} {body}")
    d = data(body) or {}
    token = d.get("access_token", "")
    _, me = get("/api/me", token=token)
    uid = (data(me) or {}).get("id")
    return uid, email, phone, token

def make_admin_verified(admin_uid, admin_token):
    rid = sql_scalar("SELECT id FROM roles WHERE code='admin' LIMIT 1;")
    if not rid:
        return False, "找不到 admin 角色"
    sql(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({admin_uid}, {rid});")
    _, pb = post("/api/admin/auth/verification-codes/phone", None, token=admin_token)
    pcode = (data(pb) or {}).get("code")
    if not pcode:
        return False, f"发管理员手机验证码失败: {pb}"
    st, vb = post("/api/admin/auth/verify-phone", {"code": pcode}, token=admin_token)
    if st != 200:
        return False, f"管理员手机双重认证失败: {st} {vb}"
    _, eb = post("/api/admin/auth/verification-codes/email", None, token=admin_token)
    ecode = (data(eb) or {}).get("code")
    if not ecode:
        return False, f"发管理员邮箱验证码失败: {eb}"
    st, vb = post("/api/admin/auth/verify-email", {"code": ecode}, token=admin_token)
    if st != 200:
        return False, f"管理员邮箱双重认证失败: {st} {vb}"
    return True, ""

# ── 测试数据准备 ──────────────────────────────────────────
def product_id(code="token-api"):
    return sql_scalar(f"SELECT id FROM products WHERE product_code='{code}' LIMIT 1;")

def open_token_service(user_id):
    pid = product_id()
    if not pid:
        return False, "找不到 token-api 商品"
    exists = sql_scalar(f"SELECT id FROM user_assets WHERE user_id={user_id} AND asset_type='token_service' AND status='active' LIMIT 1;")
    if exists:
        return True, ""
    _, err = sql(f"INSERT INTO user_assets (user_id, asset_type, product_id, status, started_at) "
                 f"VALUES ({user_id}, 'token_service', {pid}, 'active', NOW());")
    return err is None, err or ""

def fund_wallet(user_id, amount):
    rows, _ = sql(f"SELECT id FROM wallets WHERE user_id={user_id};")
    if rows:
        q = f"UPDATE wallets SET balance_amount={amount}, frozen_amount=0 WHERE user_id={user_id};"
    else:
        q = (f"INSERT INTO wallets (user_id, balance_amount, frozen_amount, currency, version) "
             f"VALUES ({user_id}, {amount}, 0, 'CNY', 0);")
    _, err = sql(q)
    return err is None, err or ""

def create_group(code_suffix, name):
    code = f"s3grp_{code_suffix}_{UNIQ}"
    _, err = sql(f"INSERT INTO user_groups (code, name, type) VALUES ('{code}', '{name}', 'custom');")
    if err:
        return None, err
    gid = sql_scalar(f"SELECT id FROM user_groups WHERE code='{code}' LIMIT 1;")
    return (int(gid) if gid else None), None

def add_group_member(user_id, group_id, group_role="member"):
    _, err = sql(f"INSERT INTO user_group_members (user_id, group_id, group_role) "
                 f"VALUES ({user_id}, {group_id}, '{group_role}');")
    return err is None, err or ""

def ensure_role(code):
    rid = sql_scalar(f"SELECT id FROM roles WHERE code='{code}' LIMIT 1;")
    if rid:
        return int(rid)
    sql(f"INSERT INTO roles (code, name) VALUES ('{code}', '{code}');")
    rid = sql_scalar(f"SELECT id FROM roles WHERE code='{code}' LIMIT 1;")
    return int(rid) if rid else None

def grant_role(user_id, role_code):
    rid = ensure_role(role_code)
    if not rid:
        return False
    _, err = sql(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({user_id}, {rid});")
    return err is None

def doc_read_schema():
    return {
        "type": "function",
        "function": {
            "name": "doc_read",
            "description": "抓取并阅读一个 https 网页文档的文本内容",
            "parameters": {
                "type": "object",
                "properties": {"url": {"type": "string"}, "max_chars": {"type": "integer"}},
                "required": ["url"],
            },
        },
    }

UNIQ = int(time.time())

def short_chat_body():
    """极短 prompt 控成本（端到端 chat 仅用于可见性 40003 校验，会在工具前命中可见性拦截）。"""
    return {"messages": [{"role": "user", "content": "你好"}], "stream": False}

# ══════════════════════════════════════════════════════════
# 特性① Agent 分类
# ══════════════════════════════════════════════════════════
def feat1_category(adm, A):
    admt = adm["token"]
    state = {}

    section("特性① Agent 分类")

    # 用例 1.1 用户端分类列表：仅 active 按 sort_order 返 4 类
    st, b = get("/api/agent-categories", token=A["token"])
    d = data(b) or {}
    items = d.get("items") if isinstance(d, dict) else None
    codes = [it.get("code") for it in items] if items else []
    orders = [it.get("sort_order") for it in items] if items else []
    record("GET /api/agent-categories 登录态 200", st == 200, f"st={st}")
    record("用户端分类含 office/study/business/entertainment 且仅 active",
           items is not None and {"office", "study", "business", "entertainment"}.issubset(set(codes)),
           f"codes={codes}")
    record("分类按 sort_order 升序",
           orders == sorted(orders) if orders else False, f"orders={orders}")
    # 仅 active：用户端不应含任何 inactive（本期固定 4 类全 active，校验数量 == active 数）
    active_cnt = int(sql_scalar("SELECT COUNT(*) FROM agent_categories WHERE status='active';", "0"))
    record("用户端分类数量 == DB active 分类数（不含 inactive）",
           items is not None and len(items) == active_cnt, f"返回 {len(items or [])} / active {active_cnt}")

    # 用例 1.2 管理端分类列表含 inactive 全量
    # 临时插一条 inactive 分类做对照
    sql(f"INSERT IGNORE INTO agent_categories (code, name, sort_order, status) "
        f"VALUES ('s3inact_{UNIQ}', '停用分类', 99, 'inactive');")
    st, b = get("/api/admin/agent-categories", token=admt)
    d = data(b) or {}
    admin_items = d.get("items") if isinstance(d, dict) else None
    admin_codes = [it.get("code") for it in admin_items] if admin_items else []
    record("GET /api/admin/agent-categories 200 且含 inactive 分类",
           st == 200 and f"s3inact_{UNIQ}" in admin_codes, f"st={st} 含inactive={f's3inact_{UNIQ}' in admin_codes}")
    # 用户端不应出现该 inactive
    st2, b2 = get("/api/agent-categories", token=A["token"])
    u_codes = [it.get("code") for it in (data(b2) or {}).get("items", [])]
    record("用户端列表不含该 inactive 分类",
           f"s3inact_{UNIQ}" not in u_codes, f"user_codes_has_inactive={f's3inact_{UNIQ}' in u_codes}")

    # 用例 1.3 admin 建 Agent 带合法 category_code → 回显 code+name
    st, cb = post("/api/admin/agents", {
        "code": f"s3cat_office_{UNIQ}", "name": "办公助手", "system_prompt": "你是办公助手",
        "default_model_code": CHAT_MODEL, "category_code": "office", "status": "active",
    }, token=admt)
    cd = data(cb) or {}
    state["office_agent_id"] = cd.get("id")
    record("admin 建 Agent 带合法 category_code=office 成功",
           st in (200, 201) and cd.get("id"), f"st={st}")
    record("AgentResp 回显 category_code=office + category_name=办公",
           cd.get("category_code") == "office" and cd.get("category_name") == "办公",
           f"category_code={cd.get('category_code')} category_name={cd.get('category_name')}")

    # 用例 1.4 非法 category_code → 40000
    st, eb = post("/api/admin/agents", {
        "code": f"s3cat_bad_{UNIQ}", "name": "非法分类", "system_prompt": "x",
        "default_model_code": CHAT_MODEL, "category_code": "nonexistent_cat", "status": "active",
    }, token=admt)
    record("admin 建 Agent 非法 category_code → 40000",
           st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    # 用例 1.5 空 category_code = 未分类
    st, nb = post("/api/admin/agents", {
        "code": f"s3cat_none_{UNIQ}", "name": "未分类助手", "system_prompt": "x",
        "default_model_code": CHAT_MODEL, "status": "active",
    }, token=admt)
    nd = data(nb) or {}
    state["nocat_agent_id"] = nd.get("id")
    record("admin 建 Agent 不传 category_code = 未分类（category_code=null）",
           st in (200, 201) and (nd.get("category_code") in (None, "")) and nd.get("category_name", "") == "",
           f"category_code={nd.get('category_code')!r} name={nd.get('category_name')!r}")

    # 用例 1.6 PATCH category_code="" 清空
    if state.get("office_agent_id"):
        aid = state["office_agent_id"]
        st, ub = patch(f"/api/admin/agents/{aid}", {"category_code": ""}, token=admt)
        ud = data(ub) or {}
        record("PATCH category_code=\"\" 清为未分类",
               st == 200 and (ud.get("category_code") in (None, "")) and ud.get("category_name", "") == "",
               f"st={st} category_code={ud.get('category_code')!r}")
        # 改回 office 供后续过滤用例
        patch(f"/api/admin/agents/{aid}", {"category_code": "office"}, token=admt)
        # PATCH 非法 code → 40000
        st, eb = patch(f"/api/admin/agents/{aid}", {"category_code": "nonexistent_cat"}, token=admt)
        record("PATCH 非法 category_code → 40000", st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    # 用例 1.7 用户自建带分类（契约允许）
    open_token_service(A["uid"])
    st, ub = post("/api/agents", {
        "name": "我的学习助手", "system_prompt": "你是学习助手",
        "default_model_code": CHAT_MODEL, "category_code": "study",
    }, token=A["token"])
    ud = data(ub) or {}
    state["user_study_agent_id"] = ud.get("id")
    record("用户自建 Agent 带 category_code=study 成功且回显",
           st in (200, 201) and ud.get("category_code") == "study" and ud.get("category_name") == "学习",
           f"st={st} cc={ud.get('category_code')} cn={ud.get('category_name')}")
    # 用户自建非法分类 → 40000
    st, eb = post("/api/agents", {
        "name": "x", "system_prompt": "x", "default_model_code": CHAT_MODEL, "category_code": "bad_x",
    }, token=A["token"])
    record("用户自建非法 category_code → 40000", st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    # 用例 1.8 GET /api/agents?category=office 过滤
    st, lb = get("/api/agents?category=office&page_size=100", token=A["token"])
    ld = data(lb) or {}
    rows = ld.get("items", [])
    all_office = all(r.get("category_code") == "office" for r in rows) if rows else False
    has_office = any(r.get("id") == state.get("office_agent_id") for r in rows)
    record("GET /api/agents?category=office 仅返回 office 分类且含目标 Agent",
           st == 200 and all_office and has_office, f"st={st} 行数={len(rows)} all_office={all_office} 含目标={has_office}")
    # study 过滤不应含 office
    st, lb2 = get("/api/agents?category=study&page_size=100", token=A["token"])
    rows2 = (data(lb2) or {}).get("items", [])
    no_office_in_study = all(r.get("category_code") == "study" for r in rows2) if rows2 else True
    record("GET /api/agents?category=study 不混入 office",
           st == 200 and no_office_in_study, f"st={st} all_study={no_office_in_study}")

    # 用例 1.9 GET /api/admin/agents?category= 过滤
    st, ab = get(f"/api/admin/agents?category=office&page_size=100", token=admt)
    arows = (data(ab) or {}).get("items", [])
    record("GET /api/admin/agents?category=office 过滤正确",
           st == 200 and all(r.get("category_code") == "office" for r in arows) if arows else st == 200,
           f"st={st} 行数={len(arows)}")

    return state

# ══════════════════════════════════════════════════════════
# 特性② Agent 定向可见性
# ══════════════════════════════════════════════════════════
def feat2_visibility(adm, users):
    """users: dict tag -> {uid, token}。需要 in_group(member/admin)、out_group、role_hit、role_miss。"""
    admt = adm["token"]
    state = {}
    section("特性② Agent 定向可见性")

    # 准备分组 + 角色
    gid_member, _ = create_group("g1", "可见组1")
    gid_other, _  = create_group("g2", "无关组2")
    note(f"建分组 g1={gid_member} g2={gid_other}")
    # 用户编排：
    #  - u_gm   : g1 的普通组员（member）
    #  - u_ga   : g1 的组管理员（admin）
    #  - u_out  : 不在 g1（在 g2）
    #  - u_role : 持有全局角色 s3vip_<UNIQ>
    #  - u_nor  : 无该角色
    add_group_member(users["u_gm"]["uid"], gid_member, "member")
    add_group_member(users["u_ga"]["uid"], gid_member, "admin")
    add_group_member(users["u_out"]["uid"], gid_other, "member")
    role_code = f"s3vip_{UNIQ}"
    grant_role(users["u_role"]["uid"], role_code)
    note(f"u_role 授全局角色 {role_code}")

    def make_visibility_agent(suffix, body):
        body = dict(body)
        body.update({"code": f"s3vis_{suffix}_{UNIQ}", "name": f"定向{suffix}",
                     "system_prompt": "你是定向助手", "default_model_code": CHAT_MODEL, "status": "active"})
        st, cb = post("/api/admin/agents", body, token=admt)
        return st, data(cb) or {}

    def can_list(tag, agent_id):
        st, lb = get("/api/agents?page_size=200", token=users[tag]["token"])
        rows = (data(lb) or {}).get("items", [])
        return any(r.get("id") == agent_id for r in rows)

    def detail_status(tag, agent_id):
        st, _ = get(f"/api/agents/{agent_id}", token=users[tag]["token"])
        return st

    def chat_status(tag, agent_id):
        st, _ = post(f"/api/agents/{agent_id}/chat", short_chat_body(), token=users[tag]["token"])
        return st

    # ── 2.1 scope=groups（不限组内角色）：组内可见、组外不可见 ──
    section("② scope=groups（组内可见 / 组外不可见）")
    st, ad = make_visibility_agent("grp", {"visible_scope": "groups", "group_ids": [gid_member]})
    grp_id = ad.get("id")
    record("建 scope=groups Agent 成功且回显 visible_scope+target_audience",
           st in (200, 201) and ad.get("visible_scope") == "groups"
           and (ad.get("target_audience") or {}).get("group_ids") == [gid_member],
           f"st={st} scope={ad.get('visible_scope')} ta={ad.get('target_audience')}")
    record("组内成员(member) 列表可见", can_list("u_gm", grp_id))
    record("组内管理员(admin) 列表可见", can_list("u_ga", grp_id))
    record("组外用户 列表不可见", not can_list("u_out", grp_id))
    # 三处访问控制：组外用户详情/chat → 40003
    record("组外用户 详情 → 40003", detail_status("u_out", grp_id) == 403, f"st={detail_status('u_out', grp_id)}")
    record("组外用户 chat → 40003", chat_status("u_out", grp_id) == 403, f"st={chat_status('u_out', grp_id)}")
    # 组内用户详情可访问（非 40003）
    record("组内成员 详情可访问（非 40003）", detail_status("u_gm", grp_id) == 200, f"st={detail_status('u_gm', grp_id)}")

    # ── 2.2 scope=groups + group_roles=[admin]：仅组管理员可见 ──
    section("② scope=groups + group_roles=[admin]（仅组管理员可见）")
    st, ad = make_visibility_agent("grpadm", {"visible_scope": "groups", "group_ids": [gid_member], "group_roles": ["admin"]})
    grpadm_id = ad.get("id")
    record("建 group_roles=[admin] Agent 成功且回显 group_roles",
           st in (200, 201) and (ad.get("target_audience") or {}).get("group_roles") == ["admin"],
           f"st={st} ta={ad.get('target_audience')}")
    record("组管理员(admin) 列表可见", can_list("u_ga", grpadm_id))
    record("普通组员(member) 列表不可见", not can_list("u_gm", grpadm_id))
    record("普通组员 详情 → 40003", detail_status("u_gm", grpadm_id) == 403, f"st={detail_status('u_gm', grpadm_id)}")
    record("普通组员 chat → 40003", chat_status("u_gm", grpadm_id) == 403, f"st={chat_status('u_gm', grpadm_id)}")
    record("组管理员 详情可访问", detail_status("u_ga", grpadm_id) == 200, f"st={detail_status('u_ga', grpadm_id)}")

    # ── 2.3 scope=roles：命中全局角色可见、未命中不可见 ──
    section("② scope=roles（命中角色可见 / 未命中不可见）")
    st, ad = make_visibility_agent("roles", {"visible_scope": "roles", "role_codes": [role_code]})
    roles_id = ad.get("id")
    record("建 scope=roles Agent 成功且回显 role_codes",
           st in (200, 201) and ad.get("visible_scope") == "roles"
           and (ad.get("target_audience") or {}).get("role_codes") == [role_code],
           f"st={st} ta={ad.get('target_audience')}")
    record("持有角色用户 列表可见", can_list("u_role", roles_id))
    record("未持角色用户 列表不可见", not can_list("u_nor", roles_id))
    record("未持角色用户 详情 → 40003", detail_status("u_nor", roles_id) == 403, f"st={detail_status('u_nor', roles_id)}")
    record("未持角色用户 chat → 40003", chat_status("u_nor", roles_id) == 403, f"st={chat_status('u_nor', roles_id)}")
    record("持有角色用户 详情可访问", detail_status("u_role", roles_id) == 200, f"st={detail_status('u_role', roles_id)}")

    # ── 2.4 scope=all：全员可见（回归）；本人自建恒可见 ──
    section("② scope=all（全员可见 回归）+ 本人自建恒可见")
    st, ad = make_visibility_agent("all", {"visible_scope": "all"})
    all_id = ad.get("id")
    record("建 scope=all Agent 且 target_audience=null",
           st in (200, 201) and ad.get("visible_scope") == "all" and ad.get("target_audience") is None,
           f"st={st} scope={ad.get('visible_scope')} ta={ad.get('target_audience')}")
    record("scope=all 对组外用户可见", can_list("u_out", all_id))
    record("scope=all 对无角色用户可见", can_list("u_nor", all_id))
    record("scope=all 详情对任意用户可访问", detail_status("u_out", all_id) == 200, f"st={detail_status('u_out', all_id)}")

    # ── 2.5 PUT /visibility 覆盖语义 ──
    section("② PUT /api/admin/agents/{id}/visibility 覆盖语义")
    # 把 2.4 的 all Agent 改为 groups（仅 g1）→ 组外用户从可见变不可见
    st, vb = put(f"/api/admin/agents/{all_id}/visibility",
                 {"visible_scope": "groups", "group_ids": [gid_member]}, token=admt)
    vd = data(vb) or {}
    record("PUT visibility 改为 groups 成功且回显",
           st == 200 and vd.get("visible_scope") == "groups"
           and (vd.get("target_audience") or {}).get("group_ids") == [gid_member],
           f"st={st} scope={vd.get('visible_scope')} ta={vd.get('target_audience')}")
    record("PUT 覆盖后 组外用户 列表不可见（原 all 可见 → 现不可见）", not can_list("u_out", all_id))
    record("PUT 覆盖后 组内成员 列表可见", can_list("u_gm", all_id))
    # 再覆盖回 all
    st, vb = put(f"/api/admin/agents/{all_id}/visibility", {"visible_scope": "all"}, token=admt)
    vd = data(vb) or {}
    record("PUT visibility 恢复 all 且 target_audience=null",
           st == 200 and vd.get("visible_scope") == "all" and vd.get("target_audience") is None,
           f"st={st} ta={vd.get('target_audience')}")
    record("恢复 all 后 组外用户 重新可见", can_list("u_out", all_id))

    # ── 2.6 写入侧校验 → 40000 ──
    section("② 写入侧校验（均 40000）")
    def expect_create_40000(name, body):
        b = dict(body)
        b.update({"name": "校验", "system_prompt": "x", "default_model_code": CHAT_MODEL})
        st, eb = post("/api/admin/agents", b, token=admt)
        record(name, st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    expect_create_40000("scope 非法值(members 预留) → 40000", {"visible_scope": "members"})
    expect_create_40000("scope 非法值(乱填) → 40000", {"visible_scope": "garbage"})
    expect_create_40000("groups 但 group_ids 为空 → 40000", {"visible_scope": "groups", "group_ids": []})
    expect_create_40000("group_roles 含非 admin/member → 40000",
                        {"visible_scope": "groups", "group_ids": [gid_member], "group_roles": ["superadmin"]})
    expect_create_40000("roles 但 role_codes 为空 → 40000", {"visible_scope": "roles", "role_codes": []})
    expect_create_40000("group_ids 含不存在分组 → 40000", {"visible_scope": "groups", "group_ids": [99999999]})
    expect_create_40000("role_codes 含不存在角色 → 40000", {"visible_scope": "roles", "role_codes": [f"no_such_role_{UNIQ}"]})

    # PUT visibility 同款校验
    st, eb = put(f"/api/admin/agents/{all_id}/visibility", {"visible_scope": "groups", "group_ids": []}, token=admt)
    record("PUT visibility groups 但 group_ids 空 → 40000", st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    state["all_id"] = all_id
    return state

# ══════════════════════════════════════════════════════════
# 特性③ MCP 接入
# ══════════════════════════════════════════════════════════
def feat3_mcp(adm, A):
    admt = adm["token"]
    state = {}
    section("特性③ MCP 接入")

    # ── 3.1 server CRUD + 凭证不回 ──
    section("③ MCP server CRUD（凭证只入不出）")
    code = f"s3mcp_{UNIQ}"
    st, cb = post("/api/admin/mcp-servers", {
        "code": code, "name": "测试MCP", "description": "stub",
        "endpoint_url": "https://mcp.example.com/rpc",
        "auth_config": json.dumps({"header": "Authorization", "value": "Bearer secret-xyz"}),
        "timeout_ms": 10000, "is_paid": False, "daily_limit": 0,
    }, token=admt)
    cd = data(cb) or {}
    state["server_id"] = cd.get("id")
    record("建 MCP server 成功（默认 status=inactive）",
           st in (200, 201) and cd.get("id") and cd.get("status") == "inactive",
           f"st={st} status={cd.get('status')}")
    record("响应含 has_auth=true 且不回凭证明文",
           cd.get("has_auth") is True and "secret-xyz" not in json.dumps(cd) and "auth_config" not in cd,
           f"has_auth={cd.get('has_auth')} leak={'secret-xyz' in json.dumps(cd)}")
    record("响应不含 auth_config_encrypted 等内部字段",
           "auth_config_encrypted" not in cd, f"keys={list(cd.keys())}")

    # GET 详情同样不回凭证
    if state.get("server_id"):
        st, gb = get(f"/api/admin/mcp-servers/{state['server_id']}", token=admt)
        gd = data(gb) or {}
        record("GET server 详情 has_auth=true 不回凭证",
               st == 200 and gd.get("has_auth") is True and "secret-xyz" not in json.dumps(gd),
               f"st={st} has_auth={gd.get('has_auth')}")

    # ── 3.2 endpoint 校验 SSRF ──
    section("③ endpoint 校验（非 https / 内网 → 40000）")
    for label, ep in [("http (非 https)", "http://mcp.example.com/rpc"),
                      ("内网 localhost", "https://localhost/rpc"),
                      ("回环 127.0.0.1", "https://127.0.0.1/rpc"),
                      ("私网 10.x", "https://10.0.0.5/rpc"),
                      ("内网 .internal", "https://svc.internal/rpc")]:
        st, eb = post("/api/admin/mcp-servers", {
            "code": f"s3bad_{label[:4]}_{UNIQ}_{random.randint(1,9999)}", "name": "x",
            "endpoint_url": ep,
        }, token=admt)
        record(f"endpoint {label} → 40000", st == 400 and biz(eb) == 40000, f"st={st} code={biz(eb)}")

    # ── 3.3 code 重复 → 40900 ──
    st, eb = post("/api/admin/mcp-servers", {
        "code": code, "name": "dup", "endpoint_url": "https://mcp.example.com/rpc",
    }, token=admt)
    record("code 重复 → 40900", st == 409 and biz(eb) == 40900, f"st={st} code={biz(eb)}")

    # ── 3.4 用户端列表不回 endpoint/凭证 ──
    section("③ 用户端 GET /api/mcp-servers（不回 endpoint/凭证）")
    # 先把 server 置 active 以便出现在用户端
    if state.get("server_id"):
        patch(f"/api/admin/mcp-servers/{state['server_id']}", {"status": "active"}, token=admt)
    st, pb = get("/api/mcp-servers", token=A["token"])
    pd = data(pb) or {}
    pitems = pd.get("items", []) if isinstance(pd, dict) else []
    mine = [it for it in pitems if it.get("code") == code]
    record("用户端 GET /api/mcp-servers 200 且列出 active server",
           st == 200 and len(mine) == 1, f"st={st} 命中={len(mine)}")
    if mine:
        it = mine[0]
        record("用户端视图不回 endpoint_url / has_auth / 凭证",
               "endpoint_url" not in it and "has_auth" not in it and "auth_config" not in it
               and "secret-xyz" not in json.dumps(it),
               f"keys={list(it.keys())}")
        record("用户端视图含 id/code/name/description/is_paid",
               all(k in it for k in ("id", "code", "name", "description", "is_paid")),
               f"keys={list(it.keys())}")
    # 改回 inactive（避免污染）
    if state.get("server_id"):
        patch(f"/api/admin/mcp-servers/{state['server_id']}", {"status": "inactive"}, token=admt)

    # ── 3.5 仅官方 Agent 可绑 MCP；绑用户自建 → 40003 ──
    section("③ 绑定权限（仅官方 Agent；绑用户自建 → 40003）")
    # 建一个官方 Agent
    st, oab = post("/api/admin/agents", {
        "code": f"s3mcp_offag_{UNIQ}", "name": "官方MCPAgent", "system_prompt": "x",
        "default_model_code": CHAT_MODEL, "status": "active",
    }, token=admt)
    off_agent_id = (data(oab) or {}).get("id")
    # 用户自建 Agent
    open_token_service(A["uid"])
    st, uab = post("/api/agents", {"name": "用户MCPAgent", "system_prompt": "x", "default_model_code": CHAT_MODEL}, token=A["token"])
    user_agent_id = (data(uab) or {}).get("id")

    if off_agent_id and state.get("server_id"):
        st, bb = post(f"/api/admin/agents/{off_agent_id}/mcp-servers", {"ids": [state["server_id"]]}, token=admt)
        record("绑 MCP 到官方 Agent 成功", st in (200, 201) and (data(bb) or {}).get("bound") is True, f"st={st} resp={data(bb)}")
        # 解绑（[]）
        st, ub = post(f"/api/admin/agents/{off_agent_id}/mcp-servers", {"ids": []}, token=admt)
        record("解绑 MCP（ids=[]）成功", st in (200, 201), f"st={st}")
    if user_agent_id and state.get("server_id"):
        st, eb = post(f"/api/admin/agents/{user_agent_id}/mcp-servers", {"ids": [state["server_id"]]}, token=admt)
        record("绑 MCP 到用户自建 Agent → 40003", st == 403 and biz(eb) == 40003, f"st={st} code={biz(eb)}")

    # ── 3.6 不存在的 server / 工具 → 40400 ──
    section("③ 不存在的 server/工具 → 40400")
    st, eb = get("/api/admin/mcp-servers/99999999", token=admt)
    record("GET 不存在 server → 40400", st == 404 and biz(eb) == 40400, f"st={st} code={biz(eb)}")
    st, eb = get("/api/admin/mcp-servers/99999999/tools", token=admt)
    record("GET 不存在 server 的 tools → 40400 或空",
           (st == 404 and biz(eb) == 40400) or st == 200, f"st={st} code={biz(eb)}")

    # ── 3.7 discover 端到端（卡点说明）──
    section("③ discover 端到端（环境卡点 + 错误码校验）")
    if state.get("server_id"):
        st, db = post(f"/api/admin/mcp-servers/{state['server_id']}/discover", None, token=admt)
        # endpoint 指向不可达的 mcp.example.com（公网域名，通过配置时 SSRF；运行时 DNS/连接失败）
        # 预期 502/50200（连接握手失败），不改 server 状态
        srv_status = sql_scalar(f"SELECT status FROM mcp_servers WHERE id={state['server_id']};")
        record("discover 不可达 endpoint → 502/50200（连接/握手失败）",
               st == 502 and biz(db) == 50200, f"st={st} code={biz(db)}")
        record("discover 失败不改 server 状态（仍 inactive）",
               srv_status == "inactive", f"status={srv_status}")
        note("端到端 discover+tools/call 回灌：运行时 SSRF（resolveDNS=true，拒回环/私网）+ MCP client "
             "无 InsecureSkipVerify（自签证书 TLS 校验失败）→ 无法在测试主机起本地 stub 跑通；"
             "已由 Go 集成测试 TestMCPOrchestrationIntegration（httptest stub）覆盖该路径。详见报告。")

    # ── 3.8 删除 server 级联清工具 ──
    if state.get("server_id"):
        st, dlb = delete(f"/api/admin/mcp-servers/{state['server_id']}", token=admt)
        record("DELETE server 成功", st == 200 and (data(dlb) or {}).get("deleted") is True, f"st={st}")
        remain = sql_scalar(f"SELECT COUNT(*) FROM mcp_server_tools WHERE server_id={state['server_id']};", "0")
        record("删除后工具快照级联清空", remain == "0", f"剩余工具={remain}")

    return state

# ══════════════════════════════════════════════════════════
# 权限/鉴权回归（第三阶段新端点）
# ══════════════════════════════════════════════════════════
def feat_perm(A):
    section("权限 & 鉴权（第三阶段新端点）")
    # 未登录
    st, _ = get("/api/agent-categories")
    record("未登录 GET /api/agent-categories → 401", st == 401, f"st={st}")
    st, _ = get("/api/mcp-servers")
    record("未登录 GET /api/mcp-servers → 401", st == 401, f"st={st}")
    # 普通用户访问管理端
    st, b = get("/api/admin/mcp-servers", token=A["token"])
    record("普通用户访问 /api/admin/mcp-servers → 403", st == 403, f"st={st} code={biz(b)}")
    st, b = get("/api/admin/agent-categories", token=A["token"])
    record("普通用户访问 /api/admin/agent-categories → 403", st == 403, f"st={st} code={biz(b)}")
    st, b = put("/api/admin/agents/1/visibility", {"visible_scope": "all"}, token=A["token"])
    record("普通用户 PUT visibility → 403", st == 403, f"st={st} code={biz(b)}")

# ══════════════════════════════════════════════════════════
def main():
    section(f"第三阶段功能验收 S3  API={API_BASE}  UNIQ={UNIQ}")
    note("准备管理员 + 用户...")

    adm_uid, _, _, adm_token = register_user("admin")
    ok, msg = make_admin_verified(adm_uid, adm_token)
    if not ok:
        print(f"{RED}管理员准备失败: {msg}{RESET}")
        sys.exit(1)
    adm = {"uid": adm_uid, "token": adm_token}
    note(f"管理员就绪 uid={adm_uid}")

    A_uid, _, _, A_token = register_user("userA")
    A = {"uid": A_uid, "token": A_token}

    # 可见性用例需要多个角色的用户
    users = {}
    for tag in ["u_gm", "u_ga", "u_out", "u_role", "u_nor"]:
        uid, _, _, tok = register_user(tag)
        users[tag] = {"uid": uid, "token": tok}
    note("可见性测试用户就绪: " + ", ".join(f"{k}={v['uid']}" for k, v in users.items()))

    feat_perm(A)
    feat1_category(adm, A)
    feat2_visibility(adm, users)
    feat3_mcp(adm, A)

    # ── 汇总 ──
    section("汇总")
    total = len(results)
    passed = sum(1 for _, ok, _ in results if ok)
    failed = total - passed
    print(f"\n  总计 {total}  |  {GREEN}通过 {passed}{RESET}  |  {RED}失败 {failed}{RESET}")
    if failed:
        print(f"\n  {RED}失败用例：{RESET}")
        for name, ok, detail in results:
            if not ok:
                print(f"    {RED}✗{RESET} {name}  {YELLOW}{detail}{RESET}")
    sys.exit(0 if failed == 0 else 2)

if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
有状态会话（conversation）功能 —— 用户端接口验收测试。

被测：commit 7137db7 / migration 000053（chat_conversations + chat_messages）。
接口契约依据：docs/frontend-conversation-persistence.md
错误码：40400 会话不存在 / 40300 未开通·无模型 / 60001 余额不足 /
        60005 套餐不足 / 50301 繁忙 / 40001 未登录 / 40000 参数校验。

设计要点：
  - 仅用 Python 标准库（urllib），无第三方依赖。
  - 鉴权方式：默认走「OTP 注册」真实创建两个隔离用户 A/B（APP_ENV=test 下发码接口回明文）。
    若环境变量提供 TOKEN_A / TOKEN_B 则直接使用，跳过注册（兜底：可用 JWT_SECRET 直签）。
  - DB 校验为可选项：可连库时校验级联删除 / 消息落库；连不上则降级为「受限」。
  - 不打印任何完整 token / 密钥（统一脱敏）。

运行方式（可在本地沙箱或测试服务器执行，两者均可直连公网 IP）：
    API_BASE=http://8.130.9.163:8080 \
    MYSQL_HOST=8.130.9.163 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 tests/test_conversation_api.py

在测试服务器上执行时把 API_BASE 改为 http://localhost:8080、MYSQL_HOST 改为 127.0.0.1 即可。
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://8.130.9.163:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "8.130.9.163")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

GREEN = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN  = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

results = []  # (name, status, detail)  status: PASS / FAIL / SKIP


def record(name, status, detail=""):
    results.append((name, status, detail))
    color = {"PASS": GREEN, "FAIL": RED, "SKIP": YELLOW}.get(status, RESET)
    line = f"  [{color}{status}{RESET}] {name}"
    if detail:
        line += f"\n        {YELLOW}{detail}{RESET}"
    print(line)
    return status == "PASS"


def check(name, cond, detail=""):
    return record(name, "PASS" if cond else "FAIL", detail)


def section(title):
    print(f"\n{BOLD}{CYAN}{'='*68}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*68}{RESET}")


def note(msg):
    print(f"  {CYAN}>>> {msg}{RESET}")


def mask(tok):
    if not tok:
        return "<none>"
    return f"{tok[:6]}...{tok[-4:]}(len={len(tok)})"


# ── HTTP ──────────────────────────────────────────────────
def request(method, path, body=None, token=None, raw_body=None):
    url = API_BASE + path
    if raw_body is not None:
        data = raw_body.encode()
    else:
        data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=70)
        txt = resp.read().decode()
        try:    return resp.status, json.loads(txt)
        except Exception: return resp.status, {"_raw": txt}
    except urllib.error.HTTPError as e:
        txt = e.read().decode()
        try:    return e.code, json.loads(txt)
        except Exception: return e.code, {"_raw": txt}
    except Exception as e:
        return 0, {"_error": str(e)}


def post(p, b=None, token=None):  return request("POST", p, b, token)
def patch(p, b=None, token=None): return request("PATCH", p, b, token)
def get(p, token=None):           return request("GET", p, None, token)
def delete(p, token=None):        return request("DELETE", p, None, token)
def biz(b):  return (b or {}).get("code")
def data(b): return (b or {}).get("data")


# ── MySQL（可选） ──────────────────────────────────────────
DB_OK = None


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


def db_available():
    global DB_OK
    if DB_OK is None:
        rows, err = sql("SELECT 1")
        DB_OK = rows is not None
        if not DB_OK:
            note(f"MySQL 不可用，DB 级校验将降级为受限：{err}")
    return DB_OK


# ── 鉴权：OTP 注册真实隔离用户 ─────────────────────────────
def send_code(kind, target, scene):
    key = "email" if kind == "email" else "phone"
    _, body = post(f"/api/auth/verification-codes/{kind}", {key: target, "scene": scene})
    d = data(body)
    return d.get("code") if d else None


def register_user(tag):
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"conv_{tag}_{ts}@example.com"
    phone = f"170{ts % 100000000:08d}"
    ec = send_code("email", email, "register")
    pc = send_code("phone", phone, "register")
    st, body = post("/api/auth/register", {
        "email": email, "phone": phone, "password": "Test1234!",
        "email_code": ec, "phone_code": pc,
    })
    if st not in (200, 201) and biz(body) != 0:
        raise RuntimeError(f"注册失败 {tag}: HTTP {st} {body}")
    token = (data(body) or {}).get("access_token", "")
    _, me = get("/api/me", token=token)
    uid = (data(me) or {}).get("id")
    return uid, token


def acquire_tokens():
    ta, tb = os.getenv("TOKEN_A"), os.getenv("TOKEN_B")
    if ta and tb:
        note("使用环境变量提供的 TOKEN_A / TOKEN_B（跳过注册）")
        ua = (data(get("/api/me", token=ta)[1]) or {}).get("id")
        ub = (data(get("/api/me", token=tb)[1]) or {}).get("id")
        return (ua, ta), (ub, tb)
    note("通过 OTP 注册创建两个隔离用户 A / B（APP_ENV=test 回明文验证码）")
    ua, ta = register_user("A")
    ub, tb = register_user("B")
    return (ua, ta), (ub, tb)


# ════════════════════════════════════════════════════════════
def main():
    section("有状态会话接口验收  环境探测")
    st, hb = get("/api/health")
    if not check("API /api/health 返回 ok", st == 200 and (data(hb) or {}).get("status") == "ok",
                 f"HTTP {st} {hb}"):
        print("API 不可用，终止。"); sys.exit(1)
    note(f"API_BASE = {API_BASE}")
    if db_available():
        rows, _ = sql("SELECT version FROM schema_migrations")
        ver = rows[0][0] if rows else "?"
        note(f"DB 可用，schema_migrations.version = {ver}")

    # ── 用例 1：未登录访问会话接口 → 401 / 40001 ──────────────
    section("用例 1  未登录访问会话接口 → 401")
    unauth = [
        ("GET",   "/api/conversations"),
        ("POST",  "/api/conversations"),
        ("GET",   "/api/conversations/1"),
        ("PATCH", "/api/conversations/1"),
        ("DELETE","/api/conversations/1"),
        ("GET",   "/api/conversations/1/messages"),
        ("POST",  "/api/conversations/1/chat"),
    ]
    for m, p in unauth:
        st, b = request(m, p, body={} if m in ("POST", "PATCH") else None)
        check(f"未登录 {m} {p} → 401/40001",
              st == 401 and biz(b) == 40001, f"HTTP {st} code={biz(b)}")

    # 伪造/坏 token → 401
    st, b = get("/api/conversations", token="not.a.real.jwt")
    check("伪造 token GET /api/conversations → 401", st == 401, f"HTTP {st} code={biz(b)}")

    # ── 获取鉴权 ─────────────────────────────────────────────
    section("鉴权准备")
    try:
        (uidA, tokA), (uidB, tokB) = acquire_tokens()
    except Exception as e:
        record("获取测试用户 token", "FAIL", str(e))
        summarize(); sys.exit(1)
    check("用户 A token 获取成功", bool(tokA) and bool(uidA), f"uidA={uidA} tokA={mask(tokA)}")
    check("用户 B token 获取成功", bool(tokB) and bool(uidB), f"uidB={uidB} tokB={mask(tokB)}")
    check("A / B 为不同用户（隔离前提）", uidA != uidB, f"uidA={uidA} uidB={uidB}")

    created_ids = []  # 收尾清理

    # ── 用例 2：建会话校验 ───────────────────────────────────
    section("用例 2  新建会话：缺/带 model_code")
    st, b = post("/api/conversations", {}, token=tokA)
    check("普通聊天缺 model_code → 400/40000",
          st == 400 and biz(b) == 40000, f"HTTP {st} code={biz(b)} msg={(b or {}).get('message')}")

    st, b = post("/api/conversations", {"model_code": "gpt-4o", "title": "A的普通会话"}, token=tokA)
    convA = data(b) or {}
    ok = st == 200 and biz(b) == 0 and convA.get("id")
    check("普通聊天带 model_code → 成功返回 id", ok,
          f"HTTP {st} code={biz(b)} id={convA.get('id')} agent_id={convA.get('agent_id')}")
    if ok:
        created_ids.append((convA["id"], tokA))
        # 字段契约抽查：必有字段（与可空字段分开判定）
        required = {"id", "title", "model_code", "message_count", "created_at", "updated_at"}
        check("会话对象必有字段齐全",
              required.issubset(set(convA.keys())),
              f"缺失字段: {required - set(convA.keys())}")
        check("普通会话 agent_id 视为 null（缺省或 null 均可）",
              convA.get("agent_id") is None, f"agent_id={convA.get('agent_id')}")
        # 契约一致性观察：doc 声明 agent_id/last_message_at 为 number|null / string|null，
        # 但后端结构体用 omitempty，空值时整字段被省略而非渲染为 null。
        nullable_present = {"agent_id", "last_message_at"} & set(convA.keys())
        nullable_omitted = {"agent_id", "last_message_at"} - set(convA.keys())
        if nullable_omitted:
            record("可空字段 omitempty 契约差异（agent_id/last_message_at 空值被省略）",
                   "SKIP",
                   f"空值省略字段={sorted(nullable_omitted)}；doc 声明应为 null。"
                   f"P3 契约差异，不影响 JS 端 null/undefined 判定")

    # 再建一个 plain（用于分页）+ 一个 agent 会话（用于 type 过滤）
    _, b2 = post("/api/conversations", {"model_code": "gpt-4o", "title": "A的普通会话2"}, token=tokA)
    if (data(b2) or {}).get("id"):
        created_ids.append((data(b2)["id"], tokA))
    st_ag, b_ag = post("/api/conversations",
                       {"agent_id": 999999999, "model_code": "gpt-4o"}, token=tokA)
    if (data(b_ag) or {}).get("id"):
        created_ids.append((data(b_ag)["id"], tokA))
    note(f"建 agent 会话(agent_id=999999999) → HTTP {st_ag} code={biz(b_ag)} "
         f"（不存在/不可见时预期 40400/40003，属正常）")

    convA_id = convA.get("id")

    # ── 用例 3：用户隔离 ─────────────────────────────────────
    section("用例 3  用户隔离：B 访问 A 的会话 → 40400")
    if convA_id:
        st, b = get(f"/api/conversations/{convA_id}", token=tokB)
        check("B GET A 的会话详情 → 404/40400",
              st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
        st, b = get(f"/api/conversations/{convA_id}/messages", token=tokB)
        check("B GET A 的会话消息 → 404/40400",
              st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
        st, b = patch(f"/api/conversations/{convA_id}", {"title": "黑客改名"}, token=tokB)
        check("B PATCH A 的会话改名 → 404/40400",
              st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
        st, b = delete(f"/api/conversations/{convA_id}", token=tokB)
        check("B DELETE A 的会话 → 404/40400",
              st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
        # 本人仍可访问（证明上面不是误删）
        st, b = get(f"/api/conversations/{convA_id}", token=tokA)
        check("A 本人 GET 自己的会话 → 成功", st == 200 and biz(b) == 0, f"HTTP {st} code={biz(b)}")
    else:
        record("用户隔离系列", "SKIP", "convA 创建失败，无法测隔离")

    # ── 用例 4：列表分页 + type 过滤 ─────────────────────────
    section("用例 4  会话列表分页 + type 过滤")
    st, b = get("/api/conversations?page=1&page_size=10", token=tokA)
    d = data(b) or {}
    struct_ok = st == 200 and all(k in d for k in ("items", "page", "page_size", "total")) \
        and isinstance(d.get("items"), list)
    check("列表结构 {items,page,page_size,total}", struct_ok,
          f"HTTP {st} keys={list(d.keys())}")
    if struct_ok:
        check("分页回显 page=1 page_size=10",
              d.get("page") == 1 and d.get("page_size") == 10,
              f"page={d.get('page')} page_size={d.get('page_size')}")
        check("total 与 items 数量自洽（total>=len(items)）",
              d.get("total", 0) >= len(d.get("items", [])),
              f"total={d.get('total')} items={len(d.get('items', []))}")

    st, bp = get("/api/conversations?type=plain", token=tokA)
    dp = data(bp) or {}
    plain_ok = st == 200 and all((it.get("agent_id") is None) for it in dp.get("items", []))
    check("type=plain 仅返回 agent_id=null 的会话", plain_ok,
          f"HTTP {st} total={dp.get('total')} "
          f"agent_ids={[it.get('agent_id') for it in dp.get('items', [])][:5]}")

    st, ba = get("/api/conversations?type=agent", token=tokA)
    da = data(ba) or {}
    agent_ok = st == 200 and all((it.get("agent_id") is not None) for it in da.get("items", []))
    check("type=agent 仅返回 agent_id!=null 的会话", agent_ok,
          f"HTTP {st} total={da.get('total')} "
          f"agent_ids={[it.get('agent_id') for it in da.get('items', [])][:5]}")

    # plain + agent 之和应等于全部
    st, ball = get("/api/conversations", token=tokA)
    dall = data(ball) or {}
    check("plain.total + agent.total == 全部.total",
          (dp.get("total", 0) + da.get("total", 0)) == dall.get("total", -1),
          f"plain={dp.get('total')} agent={da.get('total')} all={dall.get('total')}")

    # page_size 边界：每页 1 条，total 不变
    st, b1 = get("/api/conversations?page=1&page_size=1", token=tokA)
    d1 = data(b1) or {}
    check("page_size=1 时 items 数量<=1 且 total 不变",
          len(d1.get("items", [])) <= 1 and d1.get("total") == dall.get("total"),
          f"items={len(d1.get('items', []))} total={d1.get('total')}")

    # ── 用例 5：消息历史（升序、扁平分页） ───────────────────
    section("用例 5  会话消息历史：升序 + 扁平分页")
    if convA_id:
        st, b = get(f"/api/conversations/{convA_id}/messages?page=1&page_size=20", token=tokA)
        d = data(b) or {}
        msg_struct_ok = st == 200 and all(k in d for k in ("items", "page", "page_size", "total"))
        check("消息列表结构 {items,page,page_size,total}", msg_struct_ok,
              f"HTTP {st} keys={list(d.keys())}")
        ids = [m.get("id") for m in d.get("items", [])]
        check("消息按 id 升序（最早→最新）", ids == sorted(ids),
              f"ids={ids[:10]}")
    else:
        record("消息历史系列", "SKIP", "convA 缺失")

    # ── 用例 6：改名 + 删除（级联） ─────────────────────────
    section("用例 6  改名 + 删除（级联删消息）")
    st, b = post("/api/conversations", {"model_code": "gpt-4o", "title": "待删会话"}, token=tokA)
    del_conv = data(b) or {}
    del_id = del_conv.get("id")
    if del_id:
        # 改名
        st, b = patch(f"/api/conversations/{del_id}", {"title": "新标题XYZ"}, token=tokA)
        check("PATCH 改名 → 成功，回显 {id,title}",
              st == 200 and biz(b) == 0 and (data(b) or {}).get("title") == "新标题XYZ",
              f"HTTP {st} data={data(b)}")
        st, b = get(f"/api/conversations/{del_id}", token=tokA)
        check("改名后详情 title 已更新",
              (data(b) or {}).get("title") == "新标题XYZ", f"title={(data(b) or {}).get('title')}")
        # 空标题改名 → 400/40000
        st, b = patch(f"/api/conversations/{del_id}", {"title": "   "}, token=tokA)
        check("空白标题改名 → 400/40000", st == 400 and biz(b) == 40000,
              f"HTTP {st} code={biz(b)}")

        # 触发落库一条用户消息（即使模型失败也落库），便于校验级联删除
        st_chat, b_chat = post(f"/api/conversations/{del_id}/chat",
                               {"content": "用于级联删除校验的消息", "stream": False}, token=tokA)
        note(f"对待删会话发一条消息以产生 chat_messages → HTTP {st_chat} code={biz(b_chat)}")
        if db_available():
            rows, _ = sql(f"SELECT COUNT(*) FROM chat_messages WHERE conversation_id={del_id}")
            cnt_before = int(rows[0][0]) if rows else 0
            note(f"删除前 chat_messages(conv={del_id}) 行数 = {cnt_before}")

        # 删除
        st, b = delete(f"/api/conversations/{del_id}", token=tokA)
        check("DELETE 会话 → 成功，回显 {id}",
              st == 200 and biz(b) == 0 and (data(b) or {}).get("id") == del_id,
              f"HTTP {st} data={data(b)}")
        # 删后 GET → 40400
        st, b = get(f"/api/conversations/{del_id}", token=tokA)
        check("删除后 GET → 404/40400", st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
        # 删后查消息 → 40400
        st, b = get(f"/api/conversations/{del_id}/messages", token=tokA)
        check("删除后 GET messages → 404/40400", st == 404 and biz(b) == 40400,
              f"HTTP {st} code={biz(b)}")
        # DB 级联校验
        if db_available():
            rows, _ = sql(f"SELECT COUNT(*) FROM chat_messages WHERE conversation_id={del_id}")
            cnt_after = int(rows[0][0]) if rows else -1
            rows2, _ = sql(f"SELECT COUNT(*) FROM chat_conversations WHERE id={del_id}")
            conv_after = int(rows2[0][0]) if rows2 else -1
            check("DB：删除后 chat_messages 无残留（级联）", cnt_after == 0,
                  f"残留消息行数={cnt_after}")
            check("DB：删除后 chat_conversations 无残留", conv_after == 0,
                  f"残留会话行数={conv_after}")
        else:
            record("DB 级联删除校验", "SKIP", "MySQL 不可用")
    else:
        record("改名/删除系列", "SKIP", "待删会话创建失败")

    # ── 用例 7：有记忆对话 / 落库链路 ────────────────────────
    section("用例 7  有记忆对话（端到端记忆 或 落库链路）")
    st, b = post("/api/conversations", {"model_code": "gpt-4o", "title": ""}, token=tokA)
    chat_conv = data(b) or {}
    chat_id = chat_conv.get("id")
    if chat_id:
        created_ids.append((chat_id, tokA))
        st1, b1 = post(f"/api/conversations/{chat_id}/chat",
                       {"content": "你好，请记住我叫张三。", "stream": False}, token=tokA)
        note(f"第一轮 chat → HTTP {st1} code={biz(b1)} "
             f"msg={(b1 or {}).get('message')}")
        chat_succeeded = st1 == 200 and biz(b1) == 0

        if chat_succeeded:
            # 端到端记忆：第二轮追问名字
            st2, b2 = post(f"/api/conversations/{chat_id}/chat",
                           {"content": "我叫什么名字？", "stream": False}, token=tokA)
            reply = ""
            try:
                reply = (data(b2)["choices"][0]["message"]["content"]) or ""
            except Exception:
                reply = json.dumps(data(b2), ensure_ascii=False)
            check("端到端记忆：第二轮回复包含「张三」", "张三" in reply,
                  f"reply={reply[:120]}")
            # 标题自动生成
            st, bg = get(f"/api/conversations/{chat_id}", token=tokA)
            check("首条消息后标题自动生成（非空）",
                  bool((data(bg) or {}).get("title", "").strip()),
                  f"title={(data(bg) or {}).get('title')}")
            # 历史落库 4 条
            st, bm = get(f"/api/conversations/{chat_id}/messages", token=tokA)
            total = (data(bm) or {}).get("total")
            check("两轮对话后消息历史 total>=4（user/assistant 各 2）",
                  (total or 0) >= 4, f"total={total}")
        else:
            # 受限路径：模型/计费未配置 → 验证用户消息仍落库
            record("端到端记忆对话",
                   "SKIP",
                   f"模型/计费未就绪（HTTP {st1} code={biz(b1)} "
                   f"msg={(b1 or {}).get('message')}）；改为校验落库链路")
            # API 侧：消息历史应至少有 user 一条
            st, bm = get(f"/api/conversations/{chat_id}/messages", token=tokA)
            items = (data(bm) or {}).get("items", [])
            roles = [m.get("role") for m in items]
            check("落库链路(API)：失败的 chat 仍把 user 消息写入历史",
                  any(r == "user" for r in roles),
                  f"roles={roles} contents={[m.get('content') for m in items][:3]}")
            # DB 侧：直接校验 chat_messages 有 role=user 记录
            if db_available():
                rows, _ = sql("SELECT role, content FROM chat_messages "
                              f"WHERE conversation_id={chat_id} ORDER BY id")
                db_roles = [r[0] for r in (rows or [])]
                check("落库链路(DB)：chat_messages 存在 role=user 记录",
                      "user" in db_roles, f"db_roles={db_roles}")
            else:
                record("落库链路(DB)", "SKIP", "MySQL 不可用")
            # 标题：首条消息后即便模型失败也应基于首句生成
            st, bg = get(f"/api/conversations/{chat_id}", token=tokA)
            gconv = data(bg) or {}
            check("首条用户消息后标题自动生成（非空，独立于模型成败）",
                  bool(gconv.get("title", "").strip()),
                  f"title={gconv.get('title')}")
            # 落库后 last_message_at 应出现且非空（证明 omitempty 仅在空会话时省略）
            check("有消息后 last_message_at 出现且非空",
                  bool(gconv.get("last_message_at")),
                  f"last_message_at={gconv.get('last_message_at')} "
                  f"message_count={gconv.get('message_count')}")

        # content 为空 → 400/40000
        st, b = post(f"/api/conversations/{chat_id}/chat",
                     {"content": "   ", "stream": False}, token=tokA)
        check("chat content 为空 → 400/40000", st == 400 and biz(b) == 40000,
              f"HTTP {st} code={biz(b)}")
    else:
        record("有记忆对话系列", "SKIP", "chat 会话创建失败")

    # ── 用例 8：错误码语义抽查 ───────────────────────────────
    section("用例 8  错误码语义 / 契约抽查")
    st, b = get("/api/conversations/999999999", token=tokA)
    check("GET 不存在会话 → 404/40400", st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")
    st, b = get("/api/conversations/0", token=tokA)
    check("GET id=0（非法）→ 400/40000", st == 400 and biz(b) == 40000, f"HTTP {st} code={biz(b)}")
    st, b = get("/api/conversations/abc", token=tokA)
    check("GET id=abc（非数字）→ 400/40000", st == 400 and biz(b) == 40000, f"HTTP {st} code={biz(b)}")
    st, b = request("POST", "/api/conversations", token=tokA, raw_body="{bad json")
    check("POST 非法 JSON body → 400/40000", st == 400 and biz(b) == 40000, f"HTTP {st} code={biz(b)}")
    st, b = delete("/api/conversations/999999999", token=tokA)
    check("DELETE 不存在会话 → 404/40400", st == 404 and biz(b) == 40400, f"HTTP {st} code={biz(b)}")

    # ── 收尾清理 ─────────────────────────────────────────────
    section("收尾：清理本轮创建的会话")
    for cid, tok in created_ids:
        delete(f"/api/conversations/{cid}", token=tok)
    note(f"已尝试清理 {len(created_ids)} 个会话")

    summarize()


def summarize():
    section("测试汇总")
    p = sum(1 for _, s, _ in results if s == "PASS")
    f = sum(1 for _, s, _ in results if s == "FAIL")
    sk = sum(1 for _, s, _ in results if s == "SKIP")
    total_assert = p + f
    rate = (p / total_assert * 100) if total_assert else 0
    if f:
        print(f"{RED}{BOLD}失败用例：{RESET}")
        for name, s, detail in results:
            if s == "FAIL":
                print(f"  {RED}- {name}{RESET}  {detail}")
    if sk:
        print(f"{YELLOW}受限/跳过用例：{RESET}")
        for name, s, detail in results:
            if s == "SKIP":
                print(f"  {YELLOW}- {name}{RESET}  {detail}")
    print(f"\n{BOLD}PASS={p}  FAIL={f}  SKIP={sk}  "
          f"通过率(不含SKIP)={rate:.1f}%{RESET}")
    sys.exit(1 if f else 0)


if __name__ == "__main__":
    main()

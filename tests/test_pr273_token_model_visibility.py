#!/usr/bin/env python3
"""
PR #273 验收：token 网关模型目录「按角色/分组定向显示」端到端测试。

被测点（commit b7f4974 / migration 000052）：
  管理端 POST/PATCH/GET /api/admin/token/models 新增 visible_scope + 定向目标
  用户端 GET /api/token/models 按当前用户分组/角色过滤
  转发前置闸 POST /api/token/chat/completions 对不可见模型按「模型不可用」拒绝

用法（在测试服务器上执行）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    python3 ~/molin/test_pr273_token_model_visibility.py

依赖：仅标准库 + 命令行 mysql。非生产环境（AppEnv=test）发码接口直接回明文验证码。
测试数据（角色/分组/成员关系）通过直连 MySQL INSERT 准备，属测试范围。
"""

import json
import os
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

GREEN = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN  = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

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
    print(f"\n{BOLD}{CYAN}{'='*64}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*64}{RESET}")

def note(msg):
    print(f"  {CYAN}>>> {msg}{RESET}")

# ── HTTP ──────────────────────────────────────────────────
def request(method, path, body=None, token=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=60)
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
def biz(b):  return (b or {}).get("code")
def data(b): return (b or {}).get("data")

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

# ── 注册 / 登录 / 双重认证 ─────────────────────────────────
def send_code(kind, target, scene):
    key = "email" if kind == "email" else "phone"
    _, body = post(f"/api/auth/verification-codes/{kind}", {key: target, "scene": scene})
    d = data(body)
    return d.get("code") if d else None

def register_user(tag):
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"pr273_{tag}_{ts}@example.com"
    phone = f"170{ts % 100000000:08d}"
    ec = send_code("email", email, "register")
    pc = send_code("phone", phone, "register")
    st, body = post("/api/auth/register", {
        "email": email, "phone": phone, "password": "Test1234!",
        "email_code": ec, "phone_code": pc,
    })
    if st not in (200, 201) and biz(body) != 0:
        raise RuntimeError(f"注册失败 {tag}: {st} {body}")
    token = (data(body) or {}).get("access_token", "")
    _, me = get("/api/me", token=token)
    uid = (data(me) or {}).get("id")
    return uid, email, phone, token

def make_admin(uid, token):
    rows, _ = sql("SELECT id FROM roles WHERE code='admin' LIMIT 1;")
    if not rows:
        return False, "找不到 admin 角色"
    sql(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid}, {rows[0][0]});")
    _, pb = post("/api/admin/auth/verification-codes/phone", None, token=token)
    pcode = (data(pb) or {}).get("code")
    if not pcode: return False, f"发管理员手机验证码失败: {pb}"
    st, vb = post("/api/admin/auth/verify-phone", {"code": pcode}, token=token)
    if st != 200: return False, f"管理员手机双重认证失败: {st} {vb}"
    _, eb = post("/api/admin/auth/verification-codes/email", None, token=token)
    ecode = (data(eb) or {}).get("code")
    if not ecode: return False, f"发管理员邮箱验证码失败: {eb}"
    st, vb = post("/api/admin/auth/verify-email", {"code": ecode}, token=token)
    if st != 200: return False, f"管理员邮箱双重认证失败: {st} {vb}"
    return True, ""

# ── 工具 ──────────────────────────────────────────────────
def visible_codes(token):
    st, body = get("/api/token/models?page_size=100", token=token)
    d = data(body) or {}
    items = d.get("items", []) if isinstance(d, dict) else (d if isinstance(d, list) else [])
    return st, {it.get("logical_model_code") for it in items}

def create_model(token, code, scope="all", group_ids=None, group_roles=None,
                 role_codes=None, channel_id=None, upstream=None):
    body = {
        "logical_model_code": code,
        "display_name": code,
        "modality": "chat",
        "status": "active",
        "visible_scope": scope,
    }
    if group_ids is not None:   body["group_ids"] = group_ids
    if group_roles is not None: body["group_roles"] = group_roles
    if role_codes is not None:  body["role_codes"] = role_codes
    if channel_id is not None:  body["channel_id"] = channel_id
    if upstream is not None:    body["upstream_model"] = upstream
    return post("/api/admin/token/models", body, token=token)

# ══════════════════════════════════════════════════════════
def main():
    section("PR#273 验收：环境探测 + 测试数据准备")
    st, h = get("/api/health")
    if not record("API 健康检查 /api/health", st == 200 and biz(h) == 0, f"{st} {h}"):
        print(f"{RED}API 不可用，终止{RESET}"); summary(); return

    rows, err = sql("SELECT 1;")
    if rows is None:
        print(f"{RED}MySQL 不可用：{err}，终止{RESET}"); summary(); return

    ts = int(time.time())
    note("注册用户：admin / uA / uB / uC …")
    uADM, _, _, tADM = register_user("adm")
    uA, _, _, tA = register_user("uA")
    uB, _, _, tB = register_user("uB")
    uC, _, _, tC = register_user("uC")
    note(f"admin={uADM} uA={uA} uB={uB} uC={uC}")

    ok, msg = make_admin(uADM, tADM)
    if not record("管理员授 admin 角色 + 双重认证通过", ok, msg):
        print(f"{RED}无法取得管理员上下文，终止{RESET}"); summary(); return

    # 造定向数据：角色 + 分组 + 成员关系
    role_code = f"pr273_role_{ts}"
    sql(f"INSERT INTO roles (code, name) VALUES ('{role_code}', 'PR273测试角色');")
    rrows, _ = sql(f"SELECT id FROM roles WHERE code='{role_code}';")
    role_id = int(rrows[0][0]) if rrows else None
    sql(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uA}, {role_id});")

    grp_code = f"pr273_grp_{ts}"
    sql(f"INSERT INTO user_groups (code, name, type, is_default) VALUES ('{grp_code}', 'PR273测试组', 'custom', 0);")
    grows, _ = sql(f"SELECT id FROM user_groups WHERE code='{grp_code}';")
    grp_id = int(grows[0][0]) if grows else None
    # uA = 组内 member；uC = 组内 admin；uB 不在该组
    sql(f"INSERT INTO user_group_members (user_id, group_id, group_role) VALUES ({uA}, {grp_id}, 'member');")
    sql(f"INSERT INTO user_group_members (user_id, group_id, group_role) VALUES ({uC}, {grp_id}, 'admin');")
    record("测试数据就绪（角色/分组/成员）",
           role_id is not None and grp_id is not None,
           f"role_id={role_id} role_code={role_code} group_id={grp_id}")
    if role_id is None or grp_id is None:
        print(f"{RED}测试数据缺失，终止{RESET}"); summary(); return

    # 复用一个已配置渠道的存量模型，给转发前置闸用例提供 channel_id+upstream
    crow, _ = sql("SELECT channel_id, upstream_model FROM token_models "
                  "WHERE channel_id IS NOT NULL AND upstream_model IS NOT NULL "
                  "AND status='active' LIMIT 1;")
    gate_channel = int(crow[0][0]) if crow else None
    gate_upstream = crow[0][1] if crow else None
    note(f"前置闸用例复用 channel_id={gate_channel} upstream={gate_upstream}")

    # ── 用例 1：写入校验（400）────────────────────────────
    section("用例 1  写入校验（期望 400 code=40000）")
    st, b = create_model(tADM, f"pr273-inv-g-empty-{ts}", scope="groups", group_ids=[])
    record("groups 但 group_ids 空 → 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    st, b = create_model(tADM, f"pr273-inv-g-bad-{ts}", scope="groups", group_ids=[99999999])
    record("groups 含不存在分组 → 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    st, b = create_model(tADM, f"pr273-inv-grole-{ts}", scope="groups",
                         group_ids=[grp_id], group_roles=["owner"])
    record("group_roles 含非 admin/member → 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    st, b = create_model(tADM, f"pr273-inv-r-empty-{ts}", scope="roles", role_codes=[])
    record("roles 但 role_codes 空 → 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    st, b = create_model(tADM, f"pr273-inv-r-bad-{ts}", scope="roles", role_codes=["no_such_role_xyz"])
    record("roles 含不存在角色 → 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    st, b = create_model(tADM, f"pr273-inv-scope-{ts}", scope="members", role_codes=["x"])
    record("非法 visible_scope（members 预留）→ 400", st == 400 and biz(b) == 40000, f"{st} {b.get('message')}")

    # ── 用例 2：all 可见性 + 回显 ──────────────────────────
    section("用例 2  scope=all 对任意登录用户可见")
    code_all = f"pr273-all-{ts}"
    st, b = create_model(tADM, code_all, scope="all")
    d = data(b) or b
    record("创建 all 模型 → 201", st == 201, f"{st} {b.get('message') if st!=201 else ''}")
    record("create 回显 visible_scope=all 且无 target_audience",
           d.get("visible_scope") == "all" and d.get("target_audience") is None,
           f"visible_scope={d.get('visible_scope')} target_audience={d.get('target_audience')}")
    all_id = d.get("id")
    for u, tk in (("uA", tA), ("uB", tB), ("uC", tC)):
        sc, codes = visible_codes(tk)
        record(f"all 模型对 {u} 可见", sc == 200 and code_all in codes, f"{sc} 命中={code_all in codes}")

    # ── 用例 3：roles 定向 ─────────────────────────────────
    section("用例 3  scope=roles 仅持有该角色用户可见")
    code_role = f"pr273-roles-{ts}"
    st, b = create_model(tADM, code_role, scope="roles", role_codes=[role_code])
    d = data(b) or b
    role_model_id = d.get("id")
    ta = d.get("target_audience") or {}
    record("创建 roles 模型 → 201", st == 201, f"{st} {b.get('message') if st!=201 else ''}")
    record("create 回显 scope=roles + role_codes 一致",
           d.get("visible_scope") == "roles" and ta.get("role_codes") == [role_code],
           f"visible_scope={d.get('visible_scope')} target_audience={ta}")
    _, ca = visible_codes(tA)
    _, cb = visible_codes(tB)
    _, cc = visible_codes(tC)
    record("有该角色的 uA 可见", code_role in ca, f"命中={code_role in ca}")
    record("无该角色的 uB 不可见", code_role not in cb, f"命中={code_role in cb}")
    record("无该角色的 uC 不可见", code_role not in cc, f"命中={code_role in cc}")

    # ── 用例 4：groups 定向 ────────────────────────────────
    section("用例 4  scope=groups 按分组 + 组内角色过滤")
    code_grp = f"pr273-groups-{ts}"
    st, b = create_model(tADM, code_grp, scope="groups", group_ids=[grp_id])
    d = data(b) or b
    ta = d.get("target_audience") or {}
    record("创建 groups 模型（不限组内角色）→ 201", st == 201, f"{st} {b.get('message') if st!=201 else ''}")
    record("create 回显 scope=groups + group_ids 一致",
           d.get("visible_scope") == "groups" and ta.get("group_ids") == [int(grp_id)],
           f"target_audience={ta}")
    _, ca = visible_codes(tA); _, cb = visible_codes(tB); _, cc = visible_codes(tC)
    record("组成员 uA(member) 可见", code_grp in ca, f"命中={code_grp in ca}")
    record("组成员 uC(admin) 可见", code_grp in cc, f"命中={code_grp in cc}")
    record("非成员 uB 不可见", code_grp not in cb, f"命中={code_grp in cb}")

    code_grp_adm = f"pr273-groups-admin-{ts}"
    st, b = create_model(tADM, code_grp_adm, scope="groups", group_ids=[grp_id], group_roles=["admin"])
    d = data(b) or b
    ta = d.get("target_audience") or {}
    record("创建 groups 模型（限组内 admin）→ 201", st == 201, f"{st} {b.get('message') if st!=201 else ''}")
    record("create 回显 group_roles=[admin]", ta.get("group_roles") == ["admin"], f"target_audience={ta}")
    _, ca = visible_codes(tA); _, cb = visible_codes(tB); _, cc = visible_codes(tC)
    record("组内 admin 的 uC 可见", code_grp_adm in cc, f"命中={code_grp_adm in cc}")
    record("组内 member 的 uA 不可见", code_grp_adm not in ca, f"命中={code_grp_adm in ca}")
    record("非成员 uB 不可见", code_grp_adm not in cb, f"命中={code_grp_adm in cb}")

    # ── 用例 5：转发前置闸 ─────────────────────────────────
    section("用例 5  chat 转发前置闸：不可见模型按「模型不可用」拒绝")
    if gate_channel is None:
        record("前置闸用例（需已配置渠道的模型）", False, "DB 无 channel_id+upstream 的存量模型，跳过")
    else:
        code_gate = f"pr273-gate-{ts}"
        st, b = create_model(tADM, code_gate, scope="roles", role_codes=[role_code],
                             channel_id=gate_channel, upstream=gate_upstream)
        gd = data(b) or b
        record("创建已配置渠道的 roles 定向模型 → 201", st == 201, f"{st} {b.get('message') if st!=201 else ''}")
        chat_body = {"model": code_gate, "messages": [{"role": "user", "content": "hi"}], "stream": False}
        # uB 无该角色：前置闸应在转发前拒掉 → 40000「模型不可用」（不泄漏存在性）
        st, b = post("/api/token/chat/completions", chat_body, token=tB)
        record("不可见用户 uB 调用 → 拒绝且不是 200",
               st != 200 and biz(b) == 40000,
               f"{st} code={biz(b)} msg={b.get('message')}")
        record("uB 错误为「模型不可用」类（前置闸生效、未泄漏存在性）",
               biz(b) == 40000 and "模型" in str(b.get("message", "")),
               f"msg={b.get('message')}")
        # uA 有该角色：通过可见性闸，后续被资产门禁 40300 拦下（证明已越过可见性闸，并非 40000）
        st, b = post("/api/token/chat/completions", chat_body, token=tA)
        passed_gate = biz(b) != 40000  # 不是「模型不可用」即说明越过了可见性前置闸
        record("可见用户 uA 越过可见性闸（错误非 40000，落到资产门禁/上游）",
               passed_gate, f"{st} code={biz(b)} msg={b.get('message')}")

    # ── 用例 6：GET 回显 + 改回 all 清空定向 ───────────────
    section("用例 6  GET 回显一致 + 改回 all 清空 target_audience")
    st, b = get(f"/api/admin/token/models/{role_model_id}", token=tADM)
    d = data(b) or b
    record("GET roles 模型回显 visible_scope+target_audience 一致",
           st == 200 and d.get("visible_scope") == "roles"
           and (d.get("target_audience") or {}).get("role_codes") == [role_code],
           f"{st} visible_scope={d.get('visible_scope')} ta={d.get('target_audience')}")

    # PATCH：roles → all（应清空定向目标）
    st, b = patch(f"/api/admin/token/models/{role_model_id}", {"visible_scope": "all"}, token=tADM)
    d = data(b) or b
    record("PATCH 改回 all → 200 且 target_audience 消失",
           st == 200 and d.get("visible_scope") == "all" and d.get("target_audience") is None,
           f"{st} visible_scope={d.get('visible_scope')} ta={d.get('target_audience')}")
    st, b = get(f"/api/admin/token/models/{role_model_id}", token=tADM)
    d = data(b) or b
    record("GET 复核 all 后 target_audience 持久为空",
           d.get("visible_scope") == "all" and d.get("target_audience") is None,
           f"visible_scope={d.get('visible_scope')} ta={d.get('target_audience')}")
    # 改回 all 后，原本看不到的 uB 现在应可见（覆盖语义生效）
    _, cb = visible_codes(tB)
    record("改回 all 后 uB 可见该模型（定向已清空）", code_role in cb, f"命中={code_role in cb}")

    # PATCH 覆盖语义：再把同一模型从 all 改为 groups
    st, b = patch(f"/api/admin/token/models/{role_model_id}",
                  {"visible_scope": "groups", "group_ids": [grp_id]}, token=tADM)
    d = data(b) or b
    record("PATCH all → groups 覆盖生效",
           st == 200 and d.get("visible_scope") == "groups"
           and (d.get("target_audience") or {}).get("group_ids") == [int(grp_id)],
           f"{st} ta={d.get('target_audience')}")
    _, cb = visible_codes(tB)
    record("覆盖为 groups 后非成员 uB 重新不可见", code_role not in cb, f"命中={code_role in cb}")

    # ── 用例 7：fail-safe ──────────────────────────────────
    section("用例 7  fail-safe：定向模型对无分组/无角色用户一律不可见")
    _, cb = visible_codes(tB)
    leaked = [c for c in (code_role, code_grp, code_grp_adm) if c in cb]
    record("无定向归属的 uB 看不到任何定向模型", len(leaked) == 0, f"泄漏={leaked}")

    summary()

def summary():
    section("测试汇总")
    total = len(results)
    passed = sum(1 for _, ok, _ in results if ok)
    failed = total - passed
    for name, ok, detail in results:
        if not ok:
            print(f"  {RED}FAIL{RESET} {name}  {YELLOW}{detail}{RESET}")
    print(f"\n  {BOLD}总计 {total}，{GREEN}通过 {passed}{RESET}{BOLD}，{RED}失败 {failed}{RESET}")
    print(f"  {BOLD}结论：{(GREEN+'全部通过') if failed == 0 else (RED+'存在失败用例')}{RESET}")
    sys.exit(0 if failed == 0 else 1)

if __name__ == "__main__":
    main()

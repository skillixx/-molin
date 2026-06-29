#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
多套餐 launch/verify 透传 entitlement_id 端到端验收（PR #306 + #307）

背景：同一应用下用户购买多个套餐时，应用原先只能识别第一个套餐。修复后
launch 请求新增可选 entitlement_id（= user_entitlements.id），后端校验「权益归属本人 +
e.status=active + 父 user_assets.status=active + 商品 product_type=application 且
business_ref_id=appID」后反推 product_id 并写入一次性票据，verify 返回给应用。

执行（必须在测试服务器 localhost 上跑，verify 走 IP 白名单 + X-Internal-Token）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
    INTERNAL_API_TOKEN=<infra/.env.test 里的值> \
    python3 ~/molin/test_app_launch_entitlement.py

设计：所有自建数据使用高位 ID（99xxxx），测完无条件清理，不污染既有数据。
"""
import json
import os
import random
import subprocess
import time
import urllib.request
import urllib.error

API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")
INTERNAL_TOKEN = os.getenv("INTERNAL_API_TOKEN", "")

GREEN = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN  = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

# 高位 ID 段，测试专用，避免与既有自增主键冲突
APP_A   = 990001   # 目标应用
APP_B   = 990002   # 他应用（用于他应用拒签）
P_A1    = 990101   # 套餐 A（appA）
P_A2    = 990102   # 套餐 B（appA）
P_OTHER = 990103   # 套餐（appB）
P_FRZ   = 990104   # 套餐（appA，父资产冻结用）
P_USERB = 990105   # 套餐（appA，userB 持有）
AS_A1   = 990201   # 资产 -> 套餐 A
AS_A2   = 990202   # 资产 -> 套餐 B
AS_OTH  = 990203   # 资产 -> appB
AS_FRZ  = 990204   # 资产 -> 冻结（suspended）
AS_USRB = 990205   # 资产 -> userB
EN_A1   = 990301   # 权益 A
EN_A2   = 990302   # 权益 B
EN_OTH  = 990303   # 权益 appB
EN_FRZ  = 990304   # 权益（父资产 suspended）
EN_USRB = 990305   # 权益 userB

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
def request(method, path, body=None, token=None, headers_extra=None, timeout=60):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
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


def post(path, body=None, token=None, headers_extra=None):
    return request("POST", path, body, token, headers_extra)


def get(path, token=None):
    return request("GET", path, None, token)


def biz(body):  return (body or {}).get("code")
def data(body): return (body or {}).get("data")


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


# ── 注册 / 登录 ───────────────────────────────────────────
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
    return f"170{_phone_seq[0]:08d}"


def register_user(tag):
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"launchent_{tag}_{ts}_{random.randint(1000,9999)}@example.com"
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
    return {"uid": int(uid), "email": email, "phone": phone, "token": token}


# ── 测试数据准备 / 清理 ───────────────────────────────────
def cleanup():
    sql(f"DELETE FROM user_entitlements WHERE id IN ({EN_A1},{EN_A2},{EN_OTH},{EN_FRZ},{EN_USRB});")
    sql(f"DELETE FROM user_assets WHERE id IN ({AS_A1},{AS_A2},{AS_OTH},{AS_FRZ},{AS_USRB});")
    sql(f"DELETE FROM products WHERE id IN ({P_A1},{P_A2},{P_OTHER},{P_FRZ},{P_USERB});")
    sql(f"DELETE FROM applications WHERE id IN ({APP_A},{APP_B});")


def seed(uid_a, uid_b):
    ts = int(time.time())
    # 应用（active + https access_url）
    sql(f"""INSERT INTO applications (id, code, name, type, description, access_url, status)
            VALUES
            ({APP_A}, 'tlaunch_appA_{ts}', '测试应用A', 'netdisk', 'launch ent 测试', 'https://appa.test.local/enter', 'active'),
            ({APP_B}, 'tlaunch_appB_{ts}', '测试应用B', 'netdisk', 'launch ent 测试-他应用', 'https://appb.test.local/enter', 'active');""")
    # 商品（product_type=application，business_ref_id 指向对应应用）
    sql(f"""INSERT INTO products (id, product_type, product_code, name, status, business_ref_id)
            VALUES
            ({P_A1},    'application', 'tlaunch_pA1_{ts}',   '套餐A',   'active', {APP_A}),
            ({P_A2},    'application', 'tlaunch_pA2_{ts}',   '套餐B',   'active', {APP_A}),
            ({P_OTHER}, 'application', 'tlaunch_pOth_{ts}',  '套餐-他应用','active', {APP_B}),
            ({P_FRZ},   'application', 'tlaunch_pFrz_{ts}',  '套餐-冻结','active', {APP_A}),
            ({P_USERB}, 'application', 'tlaunch_pUsrB_{ts}', '套餐-userB','active', {APP_A});""")
    # 资产
    sql(f"""INSERT INTO user_assets (id, user_id, asset_type, product_id, status, started_at)
            VALUES
            ({AS_A1},  {uid_a}, 'application', {P_A1},    'active',    NOW()),
            ({AS_A2},  {uid_a}, 'application', {P_A2},    'active',    NOW()),
            ({AS_OTH}, {uid_a}, 'application', {P_OTHER}, 'active',    NOW()),
            ({AS_FRZ}, {uid_a}, 'application', {P_FRZ},   'suspended', NOW()),
            ({AS_USRB},{uid_b}, 'application', {P_USERB}, 'active',    NOW());""")
    # 权益（注意 EN_FRZ 自身 active，但父资产 AS_FRZ=suspended）
    sql(f"""INSERT INTO user_entitlements (id, user_id, asset_id, entitlement_type, product_id, status, started_at)
            VALUES
            ({EN_A1},  {uid_a}, {AS_A1},  'app_access', {P_A1},    'active', NOW()),
            ({EN_A2},  {uid_a}, {AS_A2},  'app_access', {P_A2},    'active', NOW()),
            ({EN_OTH}, {uid_a}, {AS_OTH}, 'app_access', {P_OTHER}, 'active', NOW()),
            ({EN_FRZ}, {uid_a}, {AS_FRZ}, 'app_access', {P_FRZ},   'active', NOW()),
            ({EN_USRB},{uid_b}, {AS_USRB},'app_access', {P_USERB}, 'active', NOW());""")


# ── launch / verify 封装 ──────────────────────────────────
def launch(token, app_id, entitlement_id=None):
    body = {}
    if entitlement_id is not None:
        body["entitlement_id"] = entitlement_id
    # entitlement_id is None -> 传空 body 测向后兼容
    return post(f"/api/apps/{app_id}/launch", body if body else {}, token=token)


def verify(ticket, token_override=None, with_token=True):
    headers = {}
    if with_token:
        headers["X-Internal-Token"] = INTERNAL_TOKEN if token_override is None else token_override
    return post("/api/internal/app-launch/verify", {"launch_ticket": ticket}, headers_extra=headers)


# ──────────────────────────────────────────────────────────
def main():
    print(f"{BOLD}多套餐 launch/verify 透传 entitlement_id 验收  "
          f"API={API_BASE}  DB={MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}{RESET}")
    if not INTERNAL_TOKEN:
        print(f"{RED}缺少 INTERNAL_API_TOKEN 环境变量，verify 用例无法执行{RESET}")
        return 1

    section("准备：注册用户 A / B，写入测试数据")
    cleanup()  # 防御性：清掉上次残留
    ua = register_user("a")
    ub = register_user("b")
    note(f"用户 A uid={ua['uid']}  用户 B uid={ub['uid']}")
    seed(ua["uid"], ub["uid"])
    cnt = sql_scalar(f"SELECT COUNT(*) FROM user_entitlements WHERE id IN ({EN_A1},{EN_A2},{EN_OTH},{EN_FRZ},{EN_USRB});")
    record("测试数据已写入（5 条权益）", cnt == "5", f"实际写入 {cnt} 条")

    # ── 场景 1：多套餐精确绑定 ──
    section("场景 1：多套餐各自精确识别（核心）")
    st_a, lb_a = launch(ua["token"], APP_A, EN_A1)
    tk_a = (data(lb_a) or {}).get("launch_ticket")
    record("套餐A launch 200 且签发票据", st_a == 200 and bool(tk_a),
           f"status={st_a} body={lb_a}")
    st_b, lb_b = launch(ua["token"], APP_A, EN_A2)
    tk_b = (data(lb_b) or {}).get("launch_ticket")
    record("套餐B launch 200 且签发票据", st_b == 200 and bool(tk_b),
           f"status={st_b} body={lb_b}")

    vst_a, vb_a = verify(tk_a) if tk_a else (0, {})
    va = data(vb_a) or {}
    vst_b, vb_b = verify(tk_b) if tk_b else (0, {})
    vb = data(vb_b) or {}

    record("套餐A verify 返回 entitlement_id=A 且 product_id=套餐A",
           vst_a == 200 and va.get("entitlement_id") == EN_A1 and va.get("product_id") == P_A1
           and va.get("user_id") == ua["uid"] and va.get("app_id") == APP_A,
           f"status={vst_a} data={va}")
    record("套餐B verify 返回 entitlement_id=B 且 product_id=套餐B",
           vst_b == 200 and vb.get("entitlement_id") == EN_A2 and vb.get("product_id") == P_A2
           and vb.get("user_id") == ua["uid"] and vb.get("app_id") == APP_A,
           f"status={vst_b} data={vb}")
    record("两套餐不再都识别成第一个（A≠B 且各自精确）",
           va.get("entitlement_id") != vb.get("entitlement_id")
           and va.get("product_id") != vb.get("product_id")
           and {va.get("product_id"), vb.get("product_id")} == {P_A1, P_A2},
           f"A.product={va.get('product_id')} B.product={vb.get('product_id')}")

    # ── 场景 2：向后兼容（不传 entitlement_id）──
    section("场景 2：向后兼容（空 body，不传 entitlement_id）")
    st_c, lb_c = post(f"/api/apps/{APP_A}/launch", {}, token=ua["token"])  # 空 body
    tk_c = (data(lb_c) or {}).get("launch_ticket")
    record("空 body launch 仍 200 且签发票据", st_c == 200 and bool(tk_c),
           f"status={st_c} body={lb_c}")
    vst_c, vb_c = verify(tk_c) if tk_c else (0, {})
    vc = data(vb_c) or {}
    record("verify 返回 entitlement_id=0（旧行为）", vst_c == 200 and vc.get("entitlement_id") == 0,
           f"status={vst_c} data={vc}")
    record("verify product_id 为该应用某 active 资产（A1/A2 之一）",
           vc.get("product_id") in (P_A1, P_A2),
           f"product_id={vc.get('product_id')} 期望∈{{{P_A1},{P_A2}}}")

    # ── 场景 3：越权拒签（他人 entitlement）──
    section("场景 3：越权拒签 40003（A 的 JWT 带 B 他人的 entitlement_id）")
    st_d, lb_d = launch(ua["token"], APP_A, EN_USRB)
    record("用他人权益 launch 被拒 403/40003 且不签票",
           st_d == 403 and biz(lb_d) == 40003 and not (data(lb_d) or {}).get("launch_ticket"),
           f"status={st_d} body={lb_d}")

    # ── 场景 4：他应用拒签 ──
    section("场景 4：他应用拒签 40003（本人持有但商品挂在别的 app）")
    st_e, lb_e = launch(ua["token"], APP_A, EN_OTH)
    record("用他应用权益 launch 被拒 403/40003",
           st_e == 403 and biz(lb_e) == 40003,
           f"status={st_e} body={lb_e}")

    # ── 场景 5：父资产冻结拒签 ──
    section("场景 5：父资产冻结拒签 40003（权益 active 但父 user_asset.suspended）")
    # 前置确认构造态：权益 active、父资产 suspended
    en_st = sql_scalar(f"SELECT status FROM user_entitlements WHERE id={EN_FRZ};")
    as_st = sql_scalar(f"SELECT status FROM user_assets WHERE id={AS_FRZ};")
    note(f"构造态确认：entitlement.status={en_st}  parent_asset.status={as_st}")
    st_f, lb_f = launch(ua["token"], APP_A, EN_FRZ)
    record("父资产冻结时 launch 被拒 403/40003",
           en_st == "active" and as_st == "suspended" and st_f == 403 and biz(lb_f) == 40003,
           f"status={st_f} body={lb_f}")

    # ── 场景 6a：不存在的 entitlement_id ──
    section("场景 6a：不存在的 entitlement_id 40003")
    st_g, lb_g = launch(ua["token"], APP_A, 99990001)  # 不存在
    record("不存在 entitlement_id launch 被拒 403/40003",
           st_g == 403 and biz(lb_g) == 40003,
           f"status={st_g} body={lb_g}")

    # ── 场景 6b：票据一次性 ──
    section("场景 6b：票据一次性（同票 verify 两次，第二次失败）")
    st_h, lb_h = launch(ua["token"], APP_A, EN_A1)
    tk_h = (data(lb_h) or {}).get("launch_ticket")
    v1_st, v1_b = verify(tk_h) if tk_h else (0, {})
    v2_st, v2_b = verify(tk_h) if tk_h else (0, {})
    record("第一次 verify 成功",
           v1_st == 200 and (data(v1_b) or {}).get("entitlement_id") == EN_A1,
           f"status={v1_st} data={data(v1_b)}")
    record("第二次 verify 失败 403/40003（票据已被消费）",
           v2_st == 403 and biz(v2_b) == 40003,
           f"status={v2_st} body={v2_b}")

    # ── 场景 6c：票据过期（TTL 60s，可选）──
    section("场景 6c（可选）：票据过期（TTL 60s）")
    if os.getenv("RUN_TTL_TEST") == "1":
        st_t, lb_t = launch(ua["token"], APP_A, EN_A1)
        tk_t = (data(lb_t) or {}).get("launch_ticket")
        exp = (data(lb_t) or {}).get("expires_in")
        note(f"expires_in={exp}，等待 {int(exp)+3}s 后 verify……")
        time.sleep(int(exp) + 3)
        vt_st, vt_b = verify(tk_t) if tk_t else (0, {})
        record("过期票据 verify 失败 403/40003",
               vt_st == 403 and biz(vt_b) == 40003, f"status={vt_st} body={vt_b}")
    else:
        note("跳过（设 RUN_TTL_TEST=1 启用；约耗时 65s）")

    # ── 场景 7：verify 鉴权边界 ──
    section("场景 7：verify 鉴权边界（X-Internal-Token）")
    st_i, lb_i = launch(ua["token"], APP_A, EN_A1)
    tk_i = (data(lb_i) or {}).get("launch_ticket")
    # 7a 无 token
    no_st, no_b = verify(tk_i, with_token=False)
    record("无 X-Internal-Token 被拒 403/40003",
           no_st == 403 and biz(no_b) == 40003, f"status={no_st} body={no_b}")
    # 7b 错 token
    bad_st, bad_b = verify(tk_i, token_override="wrong-token-xxxx")
    record("错误 X-Internal-Token 被拒 403/40003",
           bad_st == 403 and biz(bad_b) == 40003, f"status={bad_st} body={bad_b}")
    # 7c 鉴权失败不应消费票据：用正确 token 仍可成功 verify
    ok_st, ok_b = verify(tk_i)
    record("鉴权失败未消费票据（随后正确 token verify 仍成功）",
           ok_st == 200 and (data(ok_b) or {}).get("entitlement_id") == EN_A1,
           f"status={ok_st} data={data(ok_b)}")

    # ── 清理 ──
    section("清理测试数据")
    cleanup()
    left = sql_scalar(f"SELECT COUNT(*) FROM applications WHERE id IN ({APP_A},{APP_B});")
    record("测试数据已清理", left == "0", f"残留 applications={left}")
    # 注册的临时用户保留（注册链路无删除接口；不影响既有业务数据）

    # ── 汇总 ──
    section("汇总")
    total = len(results)
    passed = sum(1 for _, ok, _ in results if ok)
    for name, ok, detail in results:
        if not ok:
            print(f"  {RED}FAIL{RESET} {name}  {YELLOW}{detail}{RESET}")
    rate = passed / total * 100 if total else 0
    color = GREEN if passed == total else RED
    print(f"\n{BOLD}{color}通过 {passed}/{total}  ({rate:.1f}%){RESET}")
    return 0 if passed == total else 2


if __name__ == "__main__":
    raise SystemExit(main())

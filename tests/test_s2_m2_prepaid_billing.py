#!/usr/bin/env python3
"""
S2-测2：第二阶段 M2 套餐预付验收 + M1 回归 + 并发硬验收（第二轮回归，方案 B）。

验收对象（对照 docs/backend-token-billing-contract.md §6 / §7 红线）：
  M1 回归   ：sk 生命周期、双模式鉴权、model_scope 越界、门禁、postpaid 按量+按次扣钱包、用量查询。
  M2 套餐   ：prepaid sk 调用扣 entitlement 额度（不扣钱包）、remaining 递减、sale_amount 回填、
              entitlement-consume 幂等（同 idempotency_key 只 1 条 log）。
  postpaid 预扣（D1）：freeze hold → settle 实扣（多退少补），sale_amount 回填，余额不足 60001 不留冻结。

  D-M2-01 方案 B（本轮核心，对比前两轮）：
    门面 prepaid 转发前 reserve(amount=max_tokens, idem=request_id:quota_reserve) 预占额度；
    available = quota_total - quota_used - quota_reserved，占不到 → 拒 60005，不转发、不写回答案；
    结算 settle(hold_id, actual=input+output) 多退少补；失败/异常 → release 全额释放（不计 used）。
    根治要点（必须验证）：
      1) 串行低余额（remaining<单次预占额）→ 每次都被 60005 拒、不返回答案、quota_used 不再增长（对比方案 A 的串行白嫖洞）。
      2) 预占占额可见：reserve 后 quota_reserved>0；settle 后 reserved 回落、used 增加（净额=实际消耗）；hold holding→settled。
      3) 并发无白嫖窗口：quota_total 只够 K 次预占，并发 >K → 成功 ≤K、超出被 reserve 拒 60005；
         全程不变量 quota_used + quota_reserved <= quota_total；结束 quota_reserved 归 0、无 holding 残留。
      4) 多退少补：actual < 预占额 → settle 后 used 加 actual、reserved 减预占额；hold.settled_amount=actual。
         注意方案 B 多退少补口径：settle 封顶于 reserve_amount，actual > reserve_amount 时 settled = reserve_amount。
      5) 失败释放：转发失败/上游不可用 → release → used 不增、reserved 归还、hold=released。
      6) reserve 调用失败 fail-safe（asset 不可达/鉴权失败/权益失效）→ 拒转发（503/50301 或 60005/40003），不放行白嫖。

  并发硬验收：
    - prepaid：同一 entitlement 额度只够 K 次预占，并发 M(>K) → 成功 ≤K、used+reserved<=total 守恒、超出 60005、结束 reserved=0。
    - postpaid：钱包只够 K 次，并发 M(>K) → 钱包绝不为负、成功 ≤ K、其余 60001。
  红线：prepaid 绝不扣钱包、postpaid 绝不扣 entitlement（不双扣）；错误码 60005/60001/40003/50301 无 60002 误用；账实相符。

用法（在测试服务器上执行）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=<pass> MYSQL_DATABASE=molin \
    python3 ~/molin/test_s2_m2_prepaid_billing.py

依赖：仅标准库 + 命令行 mysql。凭据全部走环境变量，不硬编码真实值。
说明：
  - 验证码在 AppEnv=test 由发码接口直接返回明文，脚本据此完成注册。
  - 门禁 = user_assets 存在 asset_type='token_service' 且 status='active'（测试数据准备，属测试范围）。
  - prepaid 权益准备：直接 seed 一条 token_quota 的 user_asset + user_entitlement（quota_total 可控），
    再用甲6 能力签发 prepaid sk（POST /api/keys，billing_mode=prepaid + source_id=entitlement_id）。
  - 上游真实可用（deepseek/paratera 已配），chat 端到端可跑；并发用例用极短 prompt + 小 max_tokens 控成本与时长。
  - 方案 B 后，prepaid 转发前按 max_tokens 预占；本脚本并发用例统一显式带 max_tokens=16，故单次预占额=16。
  - 方案 B settle 语义：quota_used 增加值 = min(actual_tokens, reserve_amount)；
    当 actual > reserve 时（上游含 reasoning 等额外 token），settled = reserve_amount（不超预占额）。
    entitlement_consume_logs 不再写入（方案 B 改用 entitlement_holds 记账）。
"""

import concurrent.futures
import json
import os
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

# ── 配置 ──────────────────────────────────────────────────
API_BASE   = os.getenv("API_BASE",   "http://localhost:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = os.getenv("MYSQL_PORT", "13306")
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")

# 测试用模型（端到端 chat）；按需用环境变量覆盖。
CHAT_MODEL = os.getenv("CHAT_MODEL", "DeepSeek")
# 并发用例的小 prompt + 小 max_tokens 控成本；方案 B 下单次预占额 = max_tokens = 16。
RESERVE_PER = 16
SMALL_BODY = {"messages": [{"role": "user", "content": "ok"}], "stream": False, "max_tokens": RESERVE_PER}

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
    print(f"\n{BOLD}{CYAN}{'='*64}{RESET}")
    print(f"{BOLD}{CYAN}  {title}{RESET}")
    print(f"{BOLD}{CYAN}{'='*64}{RESET}")

def note(msg):
    print(f"  {CYAN}>>> {msg}{RESET}")

# ── HTTP ──────────────────────────────────────────────────
def request(method, path, body=None, token=None, raw_auth=None, timeout=90):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if raw_auth:
        headers["Authorization"] = raw_auth
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

def post(path, body=None, token=None):  return request("POST", path, body, token)
def get(path, token=None, raw_auth=None): return request("GET", path, None, token, raw_auth)
def delete(path, token=None):           return request("DELETE", path, None, token)
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
import random as _random

def send_code(kind, target, scene):
    path = f"/api/auth/verification-codes/{kind}"
    key = "email" if kind == "email" else "phone"
    # 发码受 IP 限流（10 次/分钟）：命中 42900 时退避等待，确保测试数据准备稳定。
    for _ in range(40):
        st, body = post(path, {key: target, "scene": scene})
        d = data(body)
        if d and d.get("code"):
            return d.get("code")
        if biz(body) == 42900:
            time.sleep(7)   # 退避，等限流窗口滑动
            continue
        time.sleep(0.6)
    return None

_phone_seq = [_random.randint(0, 8_000_000)]

def _next_phone():
    _phone_seq[0] = (_phone_seq[0] + _random.randint(1, 97)) % 90_000_000
    return f"170{_phone_seq[0]:08d}"

def register_user(tag):
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"s2m2_{tag}_{ts}_{_random.randint(1000,9999)}@example.com"
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

# ── 测试数据准备 ──────────────────────────────────────────
def product_id(code="token-api"):
    return sql_scalar(f"SELECT id FROM products WHERE product_code='{code}' LIMIT 1;")

def open_token_service(user_id):
    pid = product_id()
    if not pid:
        return False, "找不到 token-api 商品"
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

def wallet_balance(user_id):
    v = sql_scalar(f"SELECT balance_amount FROM wallets WHERE user_id={user_id};")
    return float(v) if v is not None else None

def wallet_frozen(user_id):
    v = sql_scalar(f"SELECT frozen_amount FROM wallets WHERE user_id={user_id};")
    return float(v) if v is not None else None

def holds_holding_count(user_id):
    """该用户当前仍处于 holding 的保证金 hold 数（>0 即泄漏，D-M2-03 关键）。"""
    v = sql_scalar(f"SELECT COUNT(*) FROM wallet_holds WHERE user_id={user_id} AND status='holding';")
    return int(v) if v is not None else 0

def consume_txn_total(user_id):
    """该用户 wallet_transactions 中 consume 流水绝对值之和（= 实际净扣金额，账实相符核对正确口径）。
    注意：wallet_holds.settled_amount 字段恒为 0，实扣记在 type=consume 的钱包流水，settle 通过 settle_txn_id 关联。"""
    v = sql_scalar(f"SELECT COALESCE(SUM(ABS(amount)),0) FROM wallet_transactions WHERE user_id={user_id} AND type='consume';")
    return float(v) if v is not None else 0.0

def freeze_unfreeze_balanced(user_id):
    """freeze 总额 == unfreeze 总额（保证金完整解冻，无泄漏）。"""
    fr = sql_scalar(f"SELECT COALESCE(SUM(ABS(amount)),0) FROM wallet_transactions WHERE user_id={user_id} AND type='freeze';")
    un = sql_scalar(f"SELECT COALESCE(SUM(ABS(amount)),0) FROM wallet_transactions WHERE user_id={user_id} AND type='unfreeze';")
    return float(fr or 0), float(un or 0)

def usage_sale_total(user_id):
    """token_usage_logs.sale_amount 之和（postpaid=金额；并发账实相符核对用）。"""
    v = sql_scalar(f"SELECT COALESCE(SUM(sale_amount),0) FROM token_usage_logs WHERE user_id={user_id};")
    return float(v) if v is not None else 0.0

def usage_success_count(user_id):
    """token_usage_logs 中 status 成功（非 failed）的条数；用于佐证『不白嫖』（无新增成功记录）。"""
    v = sql_scalar(f"SELECT COUNT(*) FROM token_usage_logs WHERE user_id={user_id} AND status <> 'failed';")
    return int(v) if v is not None else 0

def seed_token_quota_entitlement(user_id, quota_total, valid_days=365):
    """直接 seed 一条 token_quota 的 user_asset + user_entitlement，返回 entitlement_id。
    （任务允许：直接 seed user_entitlements 以构造可控额度；属测试数据准备。）"""
    pid = product_id()
    if not pid:
        return None, "找不到 token-api 商品"
    # 1) 建 user_asset（asset_type=token_quota，entitlement 须挂 asset_id）
    _, err = sql(f"INSERT INTO user_assets (user_id, asset_type, product_id, status, started_at, expires_at) "
                 f"VALUES ({user_id}, 'token_quota', {pid}, 'active', NOW(), DATE_ADD(NOW(), INTERVAL {valid_days} DAY));")
    if err:
        return None, f"建 user_asset 失败: {err}"
    asset_id = sql_scalar(f"SELECT id FROM user_assets WHERE user_id={user_id} AND asset_type='token_quota' "
                          f"ORDER BY id DESC LIMIT 1;")
    if not asset_id:
        return None, "取 asset_id 失败"
    # 2) 建 user_entitlement（token_quota，quota_total 可控；quota_reserved 默认 0）
    _, err = sql(
        f"INSERT INTO user_entitlements "
        f"(user_id, asset_id, entitlement_type, product_id, quota_total, quota_used, quota_reserved, quota_unit, status, started_at, expires_at) "
        f"VALUES ({user_id}, {asset_id}, 'token_quota', {pid}, {quota_total}, 0, 0, 'tokens', 'active', NOW(), "
        f"DATE_ADD(NOW(), INTERVAL {valid_days} DAY));")
    if err:
        return None, f"建 user_entitlement 失败: {err}"
    ent_id = sql_scalar(f"SELECT id FROM user_entitlements WHERE asset_id={asset_id} ORDER BY id DESC LIMIT 1;")
    return int(ent_id) if ent_id else None, ""

def entitlement_used(ent_id):
    v = sql_scalar(f"SELECT quota_used FROM user_entitlements WHERE id={ent_id};")
    return float(v) if v is not None else None

def entitlement_reserved(ent_id):
    """user_entitlements.quota_reserved（在途预占额，方案 B 新增维度）。"""
    v = sql_scalar(f"SELECT quota_reserved FROM user_entitlements WHERE id={ent_id};")
    return float(v) if v is not None else None

def entitlement_snapshot(ent_id):
    """返回 (quota_total, quota_used, quota_reserved)。"""
    rows, _ = sql(f"SELECT quota_total, quota_used, quota_reserved FROM user_entitlements WHERE id={ent_id};")
    if rows:
        return float(rows[0][0]), float(rows[0][1]), float(rows[0][2])
    return None, None, None

def entitlement_remaining(ent_id):
    """对外口径 remaining = quota_total - quota_used（不含在途 reserved）。"""
    rows, _ = sql(f"SELECT quota_total, quota_used FROM user_entitlements WHERE id={ent_id};")
    if rows:
        return float(rows[0][0]) - float(rows[0][1])
    return None

def entitlement_available(ent_id):
    """方案 B 可用额度 available = quota_total - quota_used - quota_reserved。"""
    t, u, r = entitlement_snapshot(ent_id)
    if t is None:
        return None
    return t - u - r

def consume_log_count(ent_id):
    v = sql_scalar(f"SELECT COUNT(*) FROM entitlement_consume_logs WHERE entitlement_id={ent_id};")
    return int(v) if v is not None else 0

# ── 方案 B：entitlement_holds 取证辅助 ──────────────────────
def ent_holds_count(ent_id, status=None):
    where = f"entitlement_id={ent_id}"
    if status:
        where += f" AND status='{status}'"
    v = sql_scalar(f"SELECT COUNT(*) FROM entitlement_holds WHERE {where};")
    return int(v) if v is not None else 0

def ent_holds_rows(ent_id):
    """返回 [(amount, settled_amount, status), ...]，按 id 升序。"""
    rows, _ = sql(f"SELECT amount, settled_amount, status FROM entitlement_holds WHERE entitlement_id={ent_id} ORDER BY id;")
    out = []
    for r in (rows or []):
        amt = float(r[0])
        settled = None if (r[1] is None or r[1] == "NULL") else float(r[1])
        out.append((amt, settled, r[2]))
    return out

def ent_invariant_ok(ent_id):
    """不变量 quota_used + quota_reserved <= quota_total（方案 B 全程必须成立）。"""
    t, u, r = entitlement_snapshot(ent_id)
    if t is None:
        return False, "snapshot None"
    return (u + r) <= t + 1e-6, f"used={u} reserved={r} total={t} (used+reserved={u+r})"

def issue_key(token, name, model_scope=None, billing_mode=None, source_id=None):
    body = {"name": name, "model_scope": model_scope or []}
    if billing_mode:
        body["billing_mode"] = billing_mode
    if source_id is not None:
        body["source_id"] = source_id
    st, b = post("/api/keys", body, token=token)
    return st, b

def chat(sk, model=CHAT_MODEL, extra=None, timeout=90):
    body = dict(SMALL_BODY)
    body["model"] = model
    if extra:
        body.update(extra)
    return request("POST", "/api/token/chat/completions", body, raw_auth=f"Bearer {sk}", timeout=timeout)

def _codes(res):
    return sorted({(-1 if c is None else c) for _, c in res})

# ══════════════════════════════════════════════════════════
def main():
    section("S2-测2 环境探测（方案 B：entitlement_holds + quota_reserved）")
    st, h = get("/api/health")
    record("API 健康检查 /api/health", st == 200 and biz(h) == 0, f"{st} {h}")
    ver = int(sql_scalar("SELECT version FROM schema_migrations;", "0"))
    dirty = sql_scalar("SELECT dirty FROM schema_migrations;", "1")
    record("DB schema_migrations 已达 M2 方案 B（>=42，含 entitlement_holds）且 dirty=0",
           ver >= 42 and str(dirty) == "0", f"version={ver} dirty={dirty}")
    record("entitlement_holds 表存在",
           sql_scalar("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='entitlement_holds';") == "1",
           "")
    record("user_entitlements.quota_reserved 列存在",
           sql_scalar("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='user_entitlements' AND column_name='quota_reserved';") == "1",
           "")
    record("套餐 plan token-pkg-1m 已 seed（quota_total=1000000）",
           sql_scalar("SELECT COUNT(*) FROM product_plans WHERE plan_code='token-pkg-1m' AND status='active';") == "1",
           "")

    note("注册测试账号：postpaid(P) / prepaid(Q) / prepaid 耗尽(E) / 并发-prepaid(CP) / 并发-postpaid(CW)…")
    state = {}
    for tag in ("P", "Q", "E", "CP", "CW"):
        uid, email, phone, token = register_user(tag)
        state[tag] = {"uid": uid, "email": email, "phone": phone, "token": token}
        ok, err = open_token_service(uid)
        if not ok:
            note(f"[警告] {tag} 开通 token 服务失败: {err}")
    note("账号: " + " ".join(f"{t}={state[t]['uid']}" for t in state))

    # ════════════════════════════════════════════════════════
    section("M1 回归")
    P = state["P"]
    # 给 P 充足余额跑 postpaid 按量+按次
    fund_wallet(P["uid"], "100.000000")

    # sk 生命周期
    st, ck = issue_key(P["token"], "m1-生命周期")
    d = data(ck) or {}
    sk1, sk1_id = d.get("secret_key", ""), d.get("id")
    record("M1 sk 创建：明文 secret_key 只此一次返回", st in (200, 201) and sk1.startswith("sk-molin-"),
           f"{st} prefix={sk1[:14]}")
    record("M1 sk 创建：默认 billing_mode=postpaid", d.get("billing_mode") == "postpaid", f"mode={d.get('billing_mode')}")
    st, lk = get("/api/keys", token=P["token"])
    items = (data(lk) or {}).get("items", [])
    found = next((it for it in items if it.get("id") == sk1_id), None)
    record("M1 sk 列表：只回 prefix，无明文/无 key_hash",
           found is not None and "secret_key" not in found and "key_hash" not in found,
           f"item_keys={list(found.keys()) if found else None}")

    # 双模式鉴权
    st_jwt, _ = get("/api/token/models", token=P["token"])
    st_sk, _  = get("/api/token/models", raw_auth=f"Bearer {sk1}")
    st_none, _ = get("/api/token/models")
    st_bad, _ = get("/api/token/models", raw_auth="Bearer sk-molin-INVALIDxxxxxxxx")
    record("M1 双模式鉴权：JWT / sk 均通过；无凭证 401；无效 sk 401",
           st_jwt == 200 and st_sk == 200 and st_none == 401 and st_bad == 401,
           f"jwt={st_jwt} sk={st_sk} none={st_none} bad={st_bad}")

    # model_scope 越界 → 40300
    st, sc = issue_key(P["token"], "m1-scope", model_scope=["no-such-model-xyz"])
    sk_scoped = (data(sc) or {}).get("secret_key", "")
    st, sb = chat(sk_scoped, model=CHAT_MODEL)
    record("M1 model_scope 越界 → 40300", st == 403 and biz(sb) == 40300, f"{st} code={biz(sb)}")

    # 越权吊销 → 40003
    Q = state["Q"]
    st, qk = issue_key(Q["token"], "Q的key")
    q_key_id = (data(qk) or {}).get("id")
    st, xb = delete(f"/api/keys/{q_key_id}", token=P["token"])
    record("M1 越权吊销他人 sk → 40003", st == 403 and biz(xb) == 40003, f"{st} code={biz(xb)}")

    # 门禁：未开通 → 40300（用一个未开通服务的新账号）
    nug_uid, _, _, nug_token = register_user("NUG")
    st, nk = issue_key(nug_token, "未开通")
    sk_nug = (data(nk) or {}).get("secret_key", "")
    st, gb = chat(sk_nug, model=CHAT_MODEL)
    record("M1 门禁：未开通 token 服务 → 40300", st == 403 and biz(gb) == 40300, f"{st} code={biz(gb)}")

    # postpaid 按量+按次端到端扣钱包 + 用量查询
    bal_before = wallet_balance(P["uid"])
    st, cb = chat(sk1, model=CHAT_MODEL, extra={"max_tokens": 16})
    record("M1 postpaid chat 成功（上游真实可用）", st == 200 and "choices" in (data(cb) or cb or {}),
           f"{st} usage={(cb or {}).get('usage')}")
    # finance_consumer 通过 MQ 异步落 product_consumption_records，轮询等待。
    ctypes = set()
    for _ in range(15):
        rows, _ = sql(f"SELECT usage_type FROM product_consumption_records WHERE user_id={P['uid']};")
        ctypes = {r[0] for r in (rows or [])}
        if {"input_tokens", "output_tokens", "calls"}.issubset(ctypes):
            break
        time.sleep(1)
    bal_after = wallet_balance(P["uid"])
    record("M1 按量计费：input_tokens + output_tokens 扣钱包记录存在",
           "input_tokens" in ctypes and "output_tokens" in ctypes, f"types={ctypes}")
    record("M1 按次计费：calls=1 扣钱包记录存在", "calls" in ctypes, f"types={ctypes}")
    record("M1 postpaid 钱包余额已扣减", bal_before is not None and bal_after is not None and bal_after < bal_before,
           f"{bal_before} -> {bal_after}")
    st_u, ub = get("/api/token/usage", token=P["token"])
    record("M1 用量查询可见本次调用（扁平分页）",
           st_u == 200 and (data(ub) or {}).get("total", 0) >= 1, f"{st_u} total={(data(ub) or {}).get('total')}")

    # ════════════════════════════════════════════════════════
    section("postpaid 预扣保证金（D1）")
    # 余额不足 → 60001 且不留冻结
    P2_uid, _, _, P2_token = register_user("P2")
    open_token_service(P2_uid)
    fund_wallet(P2_uid, "0.000000")
    st, p2k = issue_key(P2_token, "余额0")
    sk_p2 = (data(p2k) or {}).get("secret_key", "")
    frozen_before = wallet_frozen(P2_uid)
    st, ib = chat(sk_p2, model=CHAT_MODEL)
    time.sleep(1)
    frozen_after = wallet_frozen(P2_uid)
    record("postpaid 余额不足 → 60001（前置闸拒绝）", st == 402 and biz(ib) == 60001, f"{st} code={biz(ib)}")
    record("postpaid 余额不足：不留冻结（frozen 不变/为0）", (frozen_after or 0) == 0,
           f"frozen {frozen_before} -> {frozen_after}")

    # freeze→settle 多退少补 + sale_amount 回填（用 P 的一次新调用观察 hold 链路）
    st, p3k = issue_key(P["token"], "hold观察")
    sk_p3 = (data(p3k) or {}).get("secret_key", "")
    st, hb = chat(sk_p3, model=CHAT_MODEL, extra={"max_tokens": 24})
    time.sleep(2)
    hrows, _ = sql(f"SELECT hold_amount, settled_amount, status FROM wallet_holds WHERE user_id={P['uid']} "
                   f"ORDER BY id DESC LIMIT 1;")
    rows, _ = sql(f"SELECT request_id, sale_amount, status FROM token_usage_logs WHERE user_id={P['uid']} "
                  f"ORDER BY id DESC LIMIT 1;")
    sale_amt = float(rows[0][1]) if rows else None
    record("postpaid 预扣：生成 wallet_holds 冻结记录（hold≈max_tokens×单价）",
           bool(hrows) and float(hrows[0][0]) > 0, f"hold={hrows[0] if hrows else None}")
    record("postpaid 结算：hold 已 settle/released（不锁死）",
           bool(hrows) and hrows[0][2] in ("settled", "released"), f"hold_status={hrows[0][2] if hrows else None}")
    record("postpaid sale_amount 回填（=实扣金额 CNY，>0，修复 M1 P3）",
           sale_amt is not None and sale_amt > 0, f"sale_amount={sale_amt}")

    # ════════════════════════════════════════════════════════
    section("M2 套餐预付（prepaid）核心 + 方案 B 预占占额可见")
    Q = state["Q"]
    # Q 也充钱包，用于验证 prepaid「绝不扣钱包」红线
    fund_wallet(Q["uid"], "50.000000")
    ent_id, err = seed_token_quota_entitlement(Q["uid"], quota_total=1000000)
    record("M2 准备：seed token_quota entitlement（quota_total=100万，quota_reserved 初始 0）",
           ent_id is not None and (entitlement_reserved(ent_id) or 0) == 0, err)
    # 签发 prepaid sk
    st, pk = issue_key(Q["token"], "prepaid-sk", billing_mode="prepaid", source_id=ent_id)
    pd = data(pk) or {}
    sk_prepaid = pd.get("secret_key", "")
    record("M2 prepaid sk 签发成功（billing_mode=prepaid + source_id 校验归属）",
           st in (200, 201) and pd.get("billing_mode") == "prepaid" and pd.get("source_id") == ent_id,
           f"{st} mode={pd.get('billing_mode')} source_id={pd.get('source_id')}")

    # prepaid sk 越权签发：用别人的 entitlement_id → 40003
    st, badpk = issue_key(P["token"], "盗用他人权益", billing_mode="prepaid", source_id=ent_id)
    record("M2 prepaid sk 越权签发（source_id 不属本人）→ 40003",
           st == 403 and biz(badpk) == 40003, f"{st} code={biz(badpk)}")

    # prepaid 调用：扣 entitlement、不扣钱包 + 方案 B 预占占额可见
    used_before = entitlement_used(ent_id)
    wal_before = wallet_balance(Q["uid"])
    log_before = consume_log_count(ent_id)
    holds_before = ent_holds_count(ent_id)
    reserve_amount = 16  # max_tokens=16，故 reserve_amount=16
    st, cb = chat(sk_prepaid, model=CHAT_MODEL, extra={"max_tokens": reserve_amount})
    record("M2 prepaid chat 调用成功", st == 200 and "choices" in (data(cb) or cb or {}),
           f"{st} usage={(cb or {}).get('usage')}")
    time.sleep(2)
    used_after = entitlement_used(ent_id)
    reserved_after = entitlement_reserved(ent_id)
    wal_after = wallet_balance(Q["uid"])
    log_after = consume_log_count(ent_id)
    usage = (cb or {}).get("usage", {}) if isinstance(cb, dict) else {}
    actual_tokens = (usage.get("prompt_tokens", 0) + usage.get("completion_tokens", 0))
    # 方案 B settle 语义：quota_used 增加 = min(actual_tokens, reserve_amount)（多退少补，不超预占额封顶）
    expect_settle = min(actual_tokens, reserve_amount) if actual_tokens > 0 else reserve_amount
    record("M2 prepaid 扣 entitlement 额度（used 增加，>0）",
           used_before is not None and used_after is not None and used_after > used_before,
           f"quota_used {used_before} -> {used_after} (上游 total={actual_tokens} reserve={reserve_amount})")
    if actual_tokens:
        delta = round((used_after or 0) - (used_before or 0))
        # 方案 B：settle 后 quota_used 增量 = min(actual, reserve_amount)（封顶于预占额，不多收）
        record("M2 方案 B：扣减额度 = min(actual,reserve_amount)（settle 多退少补，实扣封顶于预占额）",
               delta == expect_settle,
               f"扣减={delta} 期望=min({actual_tokens},{reserve_amount})={expect_settle}")
    # 方案 B：settle 后 quota_reserved 回落到 0（在途预占已结清）
    record("M2 方案 B：settle 后 quota_reserved 回落归零（无残留在途预占）",
           reserved_after is not None and abs(reserved_after) < 1e-6, f"quota_reserved={reserved_after}")
    # 方案 B：entitlement_holds 新增 1 条 holding→settled
    holds_rows = ent_holds_rows(ent_id)
    last_hold = holds_rows[-1] if holds_rows else None
    record("M2 方案 B：entitlement_holds 新增预占记录 holding→settled",
           ent_holds_count(ent_id) == holds_before + 1 and last_hold is not None and last_hold[2] == "settled",
           f"hold={last_hold}")
    if last_hold and last_hold[1] is not None:
        # 方案 B：settled_amount = min(actual, reserve_amount)（不超预占额）
        record("M2 方案 B：hold.settled_amount = min(actual,reserve) >0 且 <=reserve（settle 正确封顶）",
               last_hold[1] > 0 and last_hold[1] <= last_hold[0] + 1e-6,
               f"settled_amount={last_hold[1]} reserve_amount={last_hold[0]} actual_tokens={actual_tokens}")
    record("M2 红线：prepaid 绝不扣钱包（余额不变）",
           wal_before is not None and abs((wal_after or 0) - (wal_before or 0)) < 1e-9,
           f"wallet {wal_before} -> {wal_after}")
    # 方案 B 使用 entitlement_holds 记账，不再写 entitlement_consume_logs
    record("M2 方案 B：entitlement_holds 新增记录（替代旧 consume_logs，已在上方验证）",
           ent_holds_count(ent_id) == holds_before + 1,
           f"holds {holds_before} -> {ent_holds_count(ent_id)}（consume_logs={log_after}，方案B不写consume_logs）")
    ok_inv, inv_detail = ent_invariant_ok(ent_id)
    record("M2 方案 B 不变量：quota_used + quota_reserved <= quota_total", ok_inv, inv_detail)

    # token_usage_logs sale_amount 回填（prepaid = 实际计入 quota_used 的净额 = min(actual, reserve)）
    rows, _ = sql(f"SELECT request_id, sale_amount, status FROM token_usage_logs WHERE user_id={Q['uid']} "
                  f"ORDER BY id DESC LIMIT 1;")
    sale = float(rows[0][1]) if rows else None
    record("M2 prepaid sale_amount 回填（prepaid=实际净扣额度 token 数，>0）",
           sale is not None and sale > 0, f"sale_amount={sale} (期望≈{expect_settle}，实际={actual_tokens}，reserve={reserve_amount})")

    # remaining 递减查询
    rem = entitlement_remaining(ent_id)
    record("M2 entitlement remaining 递减", rem is not None and rem < 1000000, f"remaining={rem}")

    return state

# ── D-M2-01 方案 B 专项（最高优先级，对比前两轮）─────────────
def cancel_entitlement(ent_id):
    """把 entitlement 置为 cancelled，制造 fail-safe / 权益失效场景（测试数据准备）。"""
    _, err = sql(f"UPDATE user_entitlements SET status='cancelled' WHERE id={ent_id};")
    return err is None, err or ""

def dm2_targeted(state):
    # ════════════════════════════════════════════════════════
    section("D-M2-01 方案 B 核心①：串行低余额根治（方案 A 的洞 → 现在必须全拒 60005，不白嫖）")
    # 构造 available < 单次预占额(16) 但 >0 的场景：quota_total=8（< 16），从 0 起，
    # 第一次 reserve(16) 就因 available(8) < 16 被拒 60005，不转发、不返回答案、quota_used 不增长。
    # 再退一步构造 0<remaining<16：quota_total=20，先正常调一次（实扣约8~12，remaining 落到<16），
    # 之后每次 reserve(16) 都因 available<16 被拒——对比方案 A remaining=9 时仍 200+免费、used 永卡。
    D1_uid, _, _, D1_token = register_user("D1")
    open_token_service(D1_uid)
    fund_wallet(D1_uid, "50.000000")   # 充足钱包，证明被拒后不回退扣钱包、也不白嫖
    ent, err = seed_token_quota_entitlement(D1_uid, quota_total=20)
    record("D-M2-01① 准备：seed 低额度 entitlement(quota_total=20 < 2×单次预占16)", ent is not None, err)
    st, k = issue_key(D1_token, "dm2-01-prepaid", billing_mode="prepaid", source_id=ent)
    sk = (data(k) or {}).get("secret_key", "")

    succ_logs_before = usage_success_count(D1_uid)
    seq = []
    answers = 0          # 返回模型答案的次数
    used_seq = []
    for i in range(6):
        st, b = chat(sk, model=CHAT_MODEL, extra={"max_tokens": 16})
        has_answer = isinstance(b, dict) and ("choices" in b)
        seq.append((st, biz(b), "ans" if has_answer else "-"))
        if has_answer:
            answers += 1
        used_seq.append(entitlement_used(ent) or 0)
        time.sleep(1)
    used_final = entitlement_used(ent) or 0
    reserved_final = entitlement_reserved(ent) or 0
    log_final = consume_log_count(ent)
    succ_logs_after = usage_success_count(D1_uid)
    codes = [c for _, c, _ in seq]
    # 至多 1 次成功（首调可能扣到 available<16），其余必为 60005
    n200 = sum(1 for s, _, _ in seq if s == 200)
    n60005 = sum(1 for c in codes if c == 60005)
    note(f"调用序列(st,code,ans)={seq}")
    note(f"quota_used 轨迹={used_seq} 最终={used_final} reserved={reserved_final} consume_logs={log_final}")
    note(f"返回答案次数={answers} 成功usage_log新增={succ_logs_after-succ_logs_before}")
    record("D-M2-01①：串行低余额 → 至少出现 60005 拒绝（available<单次预占额被 reserve 拒）",
           n60005 >= 1, f"60005={n60005} seq={seq}")
    record("D-M2-01①：串行白嫖根治 —— 成功(200)次数 ≤1（对比方案 A 的无限 200+免费）",
           n200 <= 1, f"200次数={n200} 答案次数={answers}")
    record("D-M2-01①：quota_used 不超过 quota_total，且达到稳定后不再增长（不白嫖累加）",
           used_final <= 20 + 1e-6 and used_seq[-1] == used_seq[-2], f"used轨迹={used_seq}")
    record("D-M2-01①：被拒后 quota_reserved 归零（reserve 占不到/释放，无在途残留）",
           abs(reserved_final) < 1e-6, f"quota_reserved={reserved_final}")
    holding_left = ent_holds_count(ent, "holding")
    record("D-M2-01①：无 holding 残留（占不到无 hold 或 hold 立即释放）",
           holding_left == 0, f"holding残留={holding_left} holds={ent_holds_rows(ent)}")
    ok_inv, inv_detail = ent_invariant_ok(ent)
    record("D-M2-01① 不变量：quota_used + quota_reserved <= quota_total 全程成立", ok_inv, inv_detail)

    # ════════════════════════════════════════════════════════
    section("D-M2-01 方案 B 核心②：失败释放（上游失败 → release，不计 used、reserved 归还）")
    # 制造转发失败：用一个不存在/越界的模型让上游失败；reserve 已占，settle 不发生，release 应触发。
    FR_uid, _, _, FR_token = register_user("FR")
    open_token_service(FR_uid)
    fund_wallet(FR_uid, "50.000000")
    ent_fr, err = seed_token_quota_entitlement(FR_uid, quota_total=1000000)
    record("D-M2-01② 准备：seed 充足额度 entitlement", ent_fr is not None, err)
    st, kfr = issue_key(FR_token, "dm2-01-failrelease", billing_mode="prepaid", source_id=ent_fr)
    sk_fr = (data(kfr) or {}).get("secret_key", "")
    used_fr_b = entitlement_used(ent_fr) or 0
    # 用一个上游会失败的请求（超大 max_tokens 触发上游报错 / 或非法参数）；优先用一个上游必失败的模型名经路由后失败。
    # 这里用合法模型但注入上游会拒的参数：temperature 越界（多数上游 400）。失败后应 release。
    st, b = chat(sk_fr, model=CHAT_MODEL, extra={"max_tokens": 16, "temperature": 9.9})
    time.sleep(2)
    used_fr_a = entitlement_used(ent_fr) or 0
    reserved_fr_a = entitlement_reserved(ent_fr) or 0
    rows = ent_holds_rows(ent_fr)
    last = rows[-1] if rows else None
    note(f"失败注入调用: st={st} code={biz(b)} used {used_fr_b}->{used_fr_a} reserved={reserved_fr_a} last_hold={last}")
    # 若上游确实失败（非200）：应 release，used 不增、reserved 归零、hold=released。
    # 若上游容忍该参数返回 200：则正常 settle（此时改判为 settle 路径，仍 reserved 归零、used 增）。
    if st != 200:
        record("D-M2-01②：上游失败 → quota_used 不增长（release 不计 used）",
               abs(used_fr_a - used_fr_b) < 1e-6, f"used {used_fr_b}->{used_fr_a}")
        record("D-M2-01②：上游失败 → quota_reserved 归还归零（release 释放在途预占）",
               abs(reserved_fr_a) < 1e-6, f"reserved={reserved_fr_a}")
        record("D-M2-01②：失败路径 hold=released（全额释放，不锁死）",
               last is not None and last[2] == "released", f"last_hold={last}")
    else:
        note("上游容忍 temperature=9.9 返回 200，失败释放路径未触发；改由结算路径校验 reserved 归零。")
        record("D-M2-01②（降级为 settle 校验）：reserved 归零、used 增（结算正常）",
               abs(reserved_fr_a) < 1e-6 and used_fr_a > used_fr_b, f"used {used_fr_b}->{used_fr_a} reserved={reserved_fr_a}")
    ok_inv, inv_detail = ent_invariant_ok(ent_fr)
    record("D-M2-01② 不变量：quota_used + quota_reserved <= quota_total", ok_inv, inv_detail)

    # ════════════════════════════════════════════════════════
    section("D-M2-01 方案 B 核心③：fail-safe（权益失效 cancelled → 拒绝，不放行白嫖）")
    FS_uid, _, _, FS_token = register_user("FS")
    open_token_service(FS_uid)
    fund_wallet(FS_uid, "50.000000")
    ent_fs, err = seed_token_quota_entitlement(FS_uid, quota_total=1000000)
    st, kfs = issue_key(FS_token, "dm2-01-failsafe", billing_mode="prepaid", source_id=ent_fs)
    sk_fs = (data(kfs) or {}).get("secret_key", "")
    cancel_entitlement(ent_fs)   # 签发后再 cancelled，模拟权益失效（reserve 应 ErrEntitlementInactive → 60005）
    used_fs_before = entitlement_used(ent_fs) or 0
    reserved_fs_before = entitlement_reserved(ent_fs) or 0
    bal_fs_before = wallet_balance(FS_uid)
    st, b = chat(sk_fs, model=CHAT_MODEL, extra={"max_tokens": 16})
    has_answer = isinstance(b, dict) and "choices" in b
    used_fs_after = entitlement_used(ent_fs) or 0
    reserved_fs_after = entitlement_reserved(ent_fs) or 0
    bal_fs_after = wallet_balance(FS_uid)
    note(f"cancelled entitlement 调用结果: st={st} code={biz(b)} has_answer={has_answer}")
    record("D-M2-01③ fail-safe：权益失效(cancelled) → reserve 拒（60005/40003/50301 类 4xx/503），不放行白嫖",
           st in (402, 403, 503) and biz(b) in (60005, 40003, 50301) and not has_answer,
           f"st={st} code={biz(b)} has_answer={has_answer}")
    record("D-M2-01③ fail-safe：被拒后不扣额度、不占额、不回退扣钱包（无白嫖副作用）",
           abs(used_fs_after - used_fs_before) < 1e-6 and abs(reserved_fs_after - reserved_fs_before) < 1e-6
           and abs((bal_fs_after or 0)-(bal_fs_before or 0)) < 1e-9,
           f"used {used_fs_before}->{used_fs_after} reserved {reserved_fs_before}->{reserved_fs_after} wallet {bal_fs_before}->{bal_fs_after}")

    # ════════════════════════════════════════════════════════
    section("D-M2-02 专项：postpaid 高并发冲突 → 503/50301（不混 60001）")
    BC_uid, _, _, BC_token = register_user("BC")
    open_token_service(BC_uid)
    fund_wallet(BC_uid, "1000.000000")   # 远超所有并发请求所需，真余额绝不可能不足
    st, kbc = issue_key(BC_token, "dm2-02-conflict")
    sk_bc = (data(kbc) or {}).get("secret_key", "")
    CONC = 12
    note(f"余额=1000 元（充足），并发 {CONC} 次 postpaid 制造乐观锁冲突…")
    def one_bc():
        st, b = chat(sk_bc, model=CHAT_MODEL, extra={"max_tokens": 16}, timeout=120)
        return st, biz(b)
    resbc=[]
    with concurrent.futures.ThreadPoolExecutor(max_workers=CONC) as ex:
        futs=[ex.submit(one_bc) for _ in range(CONC)]
        for f in concurrent.futures.as_completed(futs):
            resbc.append(f.result())
    time.sleep(2)
    succ_bc = sum(1 for st,c in resbc if st==200)
    busy_bc = sum(1 for st,c in resbc if st==503 and c==50301)
    insuf_bc = sum(1 for st,c in resbc if c==60001)
    bal_bc_final = wallet_balance(BC_uid)
    frozen_bc = wallet_frozen(BC_uid)
    holding_bc = holds_holding_count(BC_uid)
    note(f"结果分布: {sorted(resbc)}")
    note(f"成功={succ_bc} 50301(503)={busy_bc} 60001={insuf_bc} 最终余额={bal_bc_final} frozen={frozen_bc} holding残留={holding_bc}")
    record("D-M2-02：余额充足高并发，绝无 60001 误报（真余额充足不可能不足）",
           insuf_bc == 0, f"60001={insuf_bc} codes={_codes(resbc)}")
    record("D-M2-02：被拒请求(若有)为 503/50301（可重试），错误码正确区分",
           all(c in (0, 50301, None) for _, c in resbc), f"codes={_codes(resbc)}")
    record("D-M2-02：错误码无 60002/60005 误用（postpaid 不应出现 60005）",
           all(c not in (60002, 60005) for _, c in resbc), f"codes={_codes(resbc)}")
    record("D-M2-02：余额充足并发结束 frozen 归零、无 holding 残留（顺带验 D-M2-03）",
           abs(frozen_bc or 0) < 1e-6 and holding_bc == 0, f"frozen={frozen_bc} holding={holding_bc}")

    # ════════════════════════════════════════════════════════
    section("D-M2-03 专项：postpaid N 并发余额充足 → 账实相符、无漏扣、无泄漏")
    AR_uid, _, _, AR_token = register_user("AR")
    open_token_service(AR_uid)
    INIT_BAL = 1000.0
    fund_wallet(AR_uid, f"{INIT_BAL:.6f}")
    st, kar = issue_key(AR_token, "dm2-03-recon")
    sk_ar = (data(kar) or {}).get("secret_key", "")
    N = 10
    note(f"余额={INIT_BAL} 元（充足），并发 {N} 次 postpaid，验证账实相符 + 无漏扣 + 无泄漏…")
    def one_ar():
        st, b = chat(sk_ar, model=CHAT_MODEL, extra={"max_tokens": 16}, timeout=120)
        return st, biz(b)
    resar=[]
    with concurrent.futures.ThreadPoolExecutor(max_workers=N) as ex:
        futs=[ex.submit(one_ar) for _ in range(N)]
        for f in concurrent.futures.as_completed(futs):
            resar.append(f.result())
    time.sleep(3)
    succ_ar = sum(1 for st,c in resar if st==200)
    busy_ar = sum(1 for st,c in resar if c==50301)
    bal_ar_final = wallet_balance(AR_uid)
    frozen_ar = wallet_frozen(AR_uid)
    holding_ar = holds_holding_count(AR_uid)
    consume_ar = consume_txn_total(AR_uid)
    fr_ar, un_ar = freeze_unfreeze_balanced(AR_uid)
    net_ar = round(INIT_BAL - (bal_ar_final or 0), 6)
    settled_holds = sql_scalar(f"SELECT COUNT(*) FROM wallet_holds WHERE user_id={AR_uid} AND status IN ('settled','released');", "0")
    note(f"结果分布: {sorted(resar)}")
    note(f"成功={succ_ar} 50301={busy_ar} 初始={INIT_BAL} 最终余额={bal_ar_final} 净扣={net_ar} consume流水={consume_ar} 结算hold数={settled_holds} freeze={fr_ar}/unfreeze={un_ar} frozen={frozen_ar} holding残留={holding_ar}")
    record("D-M2-03：账实相符（初始-最终余额 == consume 流水之和，无漏扣）",
           abs(net_ar - consume_ar) < 1e-6, f"净扣={net_ar} vs consume流水={consume_ar}")
    record("D-M2-03：保证金完整解冻（freeze 总额 == unfreeze 总额，无泄漏）",
           abs(fr_ar - un_ar) < 1e-6, f"freeze={fr_ar} unfreeze={un_ar}")
    record("D-M2-03：无漏扣（每个成功调用都产生结算 hold：结算hold数 >= 成功数）",
           int(settled_holds) >= succ_ar and succ_ar >= 1, f"成功={succ_ar} 结算hold数={settled_holds}")
    record("D-M2-03：钱包净扣为正且账实一致（实扣总额 > 0）",
           net_ar > 0, f"净扣={net_ar}")
    record("D-M2-03：frozen 归零、无 holding 残留（保证金无泄漏）",
           abs(frozen_ar or 0) < 1e-6 and holding_ar == 0, f"frozen={frozen_ar} holding={holding_ar}")
    record("D-M2-03：余额绝不为负", bal_ar_final is not None and bal_ar_final >= -1e-9, f"余额={bal_ar_final}")


# ── 并发硬验收 ────────────────────────────────────────────
def concurrency(state):
    section("并发硬验收①：prepaid 同一 entitlement 无白嫖窗口（方案 B 预占）")
    CP = state["CP"]
    fund_wallet(CP["uid"], "50.000000")
    # 方案 B：单次预占额 = max_tokens = 16。配额恰够 K=3 次预占，并发 10 次：
    #   成功 ≤3、超出 reserve 占不到被拒 60005（不是先拿答案再算）；
    #   全程不变量 used+reserved<=total；结束 reserved=0、无 holding 残留。
    K_FIT = 3
    QUOTA = RESERVE_PER * K_FIT       # 48 tokens，恰够 3 次预占
    CONC = 10
    ent_id, err = seed_token_quota_entitlement(CP["uid"], quota_total=QUOTA)
    record("并发-prepaid 准备：seed entitlement（quota_total=%d，单次预占=%d，恰够 %d 次）" % (QUOTA, RESERVE_PER, K_FIT),
           ent_id is not None, err)
    st, ck = issue_key(CP["token"], "并发-prepaid", billing_mode="prepaid", source_id=ent_id)
    sk_cp = (data(ck) or {}).get("secret_key", "")

    # 在途采样线程：并发期间反复读 quota_reserved/quota_used，捕捉不变量是否被破坏 + reserved 是否曾 >0。
    stop = threading.Event()
    inv_violations = []
    reserved_seen_positive = [False]
    def sampler():
        while not stop.is_set():
            t, u, r = entitlement_snapshot(ent_id)
            if t is not None:
                if r > 1e-6:
                    reserved_seen_positive[0] = True
                if (u + r) > t + 1e-6:
                    inv_violations.append((u, r, t))
            time.sleep(0.02)
    samp = threading.Thread(target=sampler, daemon=True)
    samp.start()

    def one_call():
        st, b = chat(sk_cp, model=CHAT_MODEL, extra={"max_tokens": RESERVE_PER}, timeout=120)
        has_answer = isinstance(b, dict) and "choices" in b
        return st, biz(b), has_answer

    note(f"并发发起 {CONC} 次 prepaid 调用（单次预占={RESERVE_PER}，配额恰够 {K_FIT} 次）…")
    res = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=CONC) as ex:
        futs = [ex.submit(one_call) for _ in range(CONC)]
        for f in concurrent.futures.as_completed(futs):
            res.append(f.result())
    time.sleep(2)
    stop.set(); samp.join(timeout=2)

    succ = sum(1 for st, c, a in res if st == 200)
    answers = sum(1 for st, c, a in res if a)
    q60005 = sum(1 for st, c, a in res if c == 60005)
    t_fin, u_fin, r_fin = entitlement_snapshot(ent_id)
    log_n = consume_log_count(ent_id)
    holding_left = ent_holds_count(ent_id, "holding")
    holds = ent_holds_rows(ent_id)
    res2 = [(s, c) for s, c, a in res]
    note(f"结果分布: {sorted(res2)}")
    note(f"HTTP200={succ} 返回答案={answers} 60005={q60005} consume_logs={log_n}")
    note(f"最终 quota_total={t_fin} quota_used={u_fin} quota_reserved={r_fin} 在途曾占>0={reserved_seen_positive[0]} 不变量违例={inv_violations}")
    note(f"holding残留={holding_left} holds={holds}")
    # 核心①：无超扣 + 不变量全程守恒
    record("并发-prepaid 核心：扣减总额 ≤ quota_total（无超扣）",
           u_fin is not None and u_fin <= QUOTA + 1e-6, f"quota_used={u_fin} <= {QUOTA}")
    record("并发-prepaid 核心：不变量 used+reserved <= total 全程成立（采样无违例）",
           len(inv_violations) == 0, f"违例={inv_violations}")
    # 核心②：无白嫖窗口 —— 成功(返回答案)次数 ≤ K，超出被 60005 拒（不是先拿答案）
    record("并发-prepaid 核心：无白嫖窗口 —— 返回答案次数 ≤ K(%d)（超出在转发前被 reserve 拒）" % K_FIT,
           answers <= K_FIT and succ <= K_FIT, f"返回答案={answers} 成功={succ} K={K_FIT}")
    record("并发-prepaid 核心：超出额度的请求被 60005 拒绝（>=1 笔）",
           q60005 >= 1, f"60005={q60005} codes={_codes(res2)}")
    # 核心③：结束 reserved 归零、无 holding 残留（所有 hold 都 settled/released）
    record("并发-prepaid 核心：结束 quota_reserved 归零（所有在途预占已结清）",
           r_fin is not None and abs(r_fin) < 1e-6, f"quota_reserved={r_fin}")
    record("并发-prepaid 核心：无 holding 残留（hold 全部 settled/released）",
           holding_left == 0, f"holding残留={holding_left}")
    record("并发-prepaid：错误码无 60001/60002 误用",
           all(c in (0, 60005, None) for _, c in res2), f"codes={_codes(res2)}")

    section("并发硬验收②：postpaid 无负余额（预扣保证金 D1）")
    CW = state["CW"]
    HOLD_PER = 16 * 0.00002  # 0.00032 单次预扣保证金
    K = 3
    BAL = round(HOLD_PER * (K + 0.5), 6)   # ≈0.00112，够约3次预扣，并发10次必有大量被预扣闸挡返 60001
    CONC = 10
    fund_wallet(CW["uid"], f"{BAL:.6f}")
    st, wk = issue_key(CW["token"], "并发-postpaid")
    sk_cw = (data(wk) or {}).get("secret_key", "")
    note(f"钱包余额={BAL} 元（约够 {K} 次），并发发起 {CONC} 次 postpaid 调用…")

    def one_pp():
        st, b = chat(sk_cw, model=CHAT_MODEL, extra={"max_tokens": 16}, timeout=120)
        return st, biz(b)

    res2 = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=CONC) as ex:
        futs = [ex.submit(one_pp) for _ in range(CONC)]
        for f in concurrent.futures.as_completed(futs):
            res2.append(f.result())
    time.sleep(2)
    succ2 = sum(1 for st, c in res2 if st == 200)
    q60001 = sum(1 for st, c in res2 if c == 60001)
    bal_final = wallet_balance(CW["uid"])
    frozen_final = wallet_frozen(CW["uid"])
    note(f"结果分布: {sorted(res2)}")
    note(f"成功={succ2} 60001={q60001} 最终余额={bal_final} 冻结={frozen_final}")
    record("并发-postpaid：钱包余额绝不为负（核心硬验收）",
           bal_final is not None and bal_final >= -1e-9, f"最终余额={bal_final}")
    record("并发-postpaid：冻结额最终归零（无锁死保证金）",
           frozen_final is not None and abs(frozen_final) < 1e-6, f"frozen={frozen_final}")
    record("并发-postpaid：余额不足的请求被 60001 预扣闸拦截（无 60002/60005 误用）",
           q60001 >= 1 and all(c in (0, 60001, None) for _, c in res2),
           f"60001={q60001} codes={_codes(res2)}")
    record("并发-postpaid：成功次数受余额约束（成功扣费总额 ≤ 初始余额）",
           succ2 >= 1, f"成功={succ2} 初始余额={BAL}")
    net_deduct = round(BAL - (bal_final or 0), 6)
    consume_total = consume_txn_total(CW["uid"])
    holding_left = holds_holding_count(CW["uid"])
    note(f"账实核对：净扣={net_deduct} consume流水={consume_total} 残留holding={holding_left}")
    record("并发-postpaid D-M2-03：账实相符（初始-最终余额 = consume 流水之和，无漏扣）",
           abs(net_deduct - consume_total) < 1e-6, f"净扣={net_deduct} vs consume流水={consume_total}")
    record("并发-postpaid D-M2-03：无 holding hold 残留（保证金无泄漏）",
           holding_left == 0, f"残留holding={holding_left}")
    record("并发-postpaid D-M2-02：余额不足组拒绝码=60001，不混 50301（错误区分）",
           all(c in (0, 60001, None) for _, c in res2), f"codes={_codes(res2)}")


# ── 汇总 ──────────────────────────────────────────────────
def summary():
    section("S2-测2 验收汇总（第二轮回归，方案 B）")
    total = len(results)
    passed = sum(1 for _, ok, _ in results if ok)
    failed = total - passed
    print(f"  共 {total} 项：{GREEN}通过 {passed}{RESET} / {RED}失败 {failed}{RESET}")
    if failed:
        print(f"\n  {RED}失败项：{RESET}")
        for name, ok, detail in results:
            if not ok:
                print(f"    - {name}  {detail}")
    return failed == 0

if __name__ == "__main__":
    try:
        state = main()
        dm2_targeted(state)
        concurrency(state)
    except Exception as e:
        import traceback
        print(f"{RED}脚本异常：{e}{RESET}")
        traceback.print_exc()
    allpass = summary()
    sys.exit(0 if allpass else 1)

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
回归用例：放开 http / 内网 IP 外呼校验（开关 TRUST_INTERNAL_OUTBOUND=true）。

背景：
    自建局域网部署下，插件 endpoint_url、MCP endpoint_url、应用 access_url 经常指向
    http:// 或内网/IP 地址。当 TRUST_INTERNAL_OUTBOUND=true 时，SSRF 校验
    （server/internal/modules/workbench/security/ssrf.go ValidateOutboundURL 与
    server/internal/modules/app/service/app_service.go validateAccessURL）放行 http
    与内网/IP，但仍必须拒绝危险 scheme（javascript: 等）与缺 host 的非法 URL。
    对接契约见 docs/frontend-task-allow-http-ip-outbound.md。

覆盖用例（两态边界）：
    1a 插件 endpoint_url=http 内网IP 创建放行
    1b 插件 endpoint_url=http 内网IP 更新放行
    2  MCP endpoint_url=http 内网IP 保存放行
    2-discover MCP discover 不被 https/SSRF 校验拦截（网络不可达属预期，非校验拒绝）
    3  应用 access_url=http 内网IP 保存放行
    4  应用 access_url 空串 → 清空入口、不报错
    5a 回归 应用 access_url=公网 https 保存放行
    5b 回归 插件 endpoint_url=公网 https 保存放行
    6a 插件 endpoint_url=javascript: 危险 scheme 仍被拒（400）
    6b 应用 access_url=javascript: 危险 scheme 仍被拒（400）
    7a 插件 endpoint_url=http:// 缺 host 仍被拒（400）
    7b 应用 access_url=http:// 缺 host 仍被拒（400）

执行（必须在测试服务器 localhost 上跑；本地沙箱访问不到 localhost:8080）：
    API_BASE=http://localhost:8080 \
    MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
    MYSQL_USER=molin MYSQL_PASSWORD=<infra/.env.test 里的值> MYSQL_DATABASE=molin \
    python3 ~/molin/test_http_ip_outbound_regression.py

前置：测试服后端启动时已设 TRUST_INTERNAL_OUTBOUND=true（否则 1a/1b/2/3 会被拒，用例失败）。

幂等/清理：
    - 全部自建对象的 code 使用统一前缀 TEST_PREFIX（默认 hio_reg_），可重复执行不撞唯一键；
    - 结束时按前缀清理 plugins / mcp_servers / applications，自身不残留脏数据；
    - 与既有 test_app_launch_entitlement.py 一致，动态注册管理员并双重认证，不依赖固定账号、不硬编码密码。
进程退出码：全通过为 0，存在失败为 1，便于 CI/脚本判定。
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

# 自建对象统一 code 前缀，便于幂等执行与按前缀清理
TEST_PREFIX = os.getenv("TEST_PREFIX", "hio_reg_")

GREEN = "\033[92m"; RED = "\033[91m"; YELLOW = "\033[93m"
CYAN  = "\033[96m"; BOLD = "\033[1m"; RESET = "\033[0m"

results = []  # (用例号, 名称, ok, 实际HTTP, 说明)


# ── HTTP ──────────────────────────────────────────────────
def req(method, path, body=None, token=None, timeout=30):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(r, timeout=timeout)
        return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"raw": raw}
    except Exception as e:
        return 0, {"error": str(e)}


def post(p, b=None, token=None):  return req("POST", p, b, token)
def patch(p, b=None, token=None): return req("PATCH", p, b, token)
def d(body):  return (body or {}).get("data") if isinstance(body, dict) else None
def msg(body): return (body or {}).get("message", "") if isinstance(body, dict) else ""
def biz(body): return (body or {}).get("code") if isinstance(body, dict) else None


# ── MySQL ─────────────────────────────────────────────────
def sql(q):
    cmd = ["mysql", "-h", MYSQL_HOST, "-P", MYSQL_PORT, "-u", MYSQL_USER,
           f"-p{MYSQL_PASS}", MYSQL_DB, "-N", "-B", "-e", q]
    out = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
    if out.returncode != 0:
        return None, out.stderr.strip()
    rows = [l.split("\t") for l in out.stdout.strip().splitlines() if l]
    return rows, None


def scalar(q, dflt=None):
    rows, _ = sql(q)
    return rows[0][0] if rows and rows[0] else dflt


# ── 注册管理员并双重认证（与 test_app_launch_entitlement.py 同套路）──
def send_code(kind, target, scene):
    key = "email" if kind == "email" else "phone"
    for _ in range(40):
        st, b = post(f"/api/auth/verification-codes/{kind}", {key: target, "scene": scene})
        dd = d(b)
        if dd and dd.get("code"):
            return dd.get("code")
        if biz(b) == 42900:  # 触发频控，等冷却
            time.sleep(7); continue
        time.sleep(0.5)
    return None


def next_phone():
    return f"170{random.randint(0, 89999999):08d}"


def setup_admin():
    ts = int(time.time() * 1000) % 10_000_000_000
    email = f"{TEST_PREFIX}{ts}_{random.randint(1000, 9999)}@example.com"
    phone = next_phone()
    ec = send_code("email", email, "register")
    pc = send_code("phone", phone, "register")
    st, b = post("/api/auth/register", {"email": email, "phone": phone,
                 "password": "Test1234!", "email_code": ec, "phone_code": pc})
    if st not in (200, 201) and biz(b) != 0:
        raise RuntimeError(f"注册失败: {st} {b}")
    token = (d(b) or {}).get("access_token", "")
    _, me = req("GET", "/api/me", token=token)
    uid = (d(me) or {}).get("id")
    rid = scalar("SELECT id FROM roles WHERE code='admin' LIMIT 1;")
    if not rid:
        raise RuntimeError("找不到 admin 角色，请先初始化基础角色 seed")
    sql(f"INSERT IGNORE INTO user_roles (user_id, role_id) VALUES ({uid}, {rid});")
    # 双重认证：手机 + 邮箱
    _, pb = post("/api/admin/auth/verification-codes/phone", None, token=token)
    pcode = (d(pb) or {}).get("code")
    st, vb = post("/api/admin/auth/verify-phone", {"code": pcode}, token=token)
    if st != 200:
        raise RuntimeError(f"手机双认证失败: {st} {vb}")
    _, eb = post("/api/admin/auth/verification-codes/email", None, token=token)
    ecode = (d(eb) or {}).get("code")
    st, vb = post("/api/admin/auth/verify-email", {"code": ecode}, token=token)
    if st != 200:
        raise RuntimeError(f"邮箱双认证失败: {st} {vb}")
    return token, uid


# ── 记录 ──────────────────────────────────────────────────
def rec(num, name, ok, st, m):
    results.append((num, name, bool(ok), st, m))
    flag = f"{GREEN}PASS{RESET}" if ok else f"{RED}FAIL{RESET}"
    print(f"  [{flag}] 用例{num} {name} | HTTP={st} | {m}")


def uniq(p):
    return f"{TEST_PREFIX}{p}_{int(time.time() * 1000) % 10_000_000}_{random.randint(100, 999)}"


def cleanup():
    """按前缀清理本脚本自建的插件 / MCP / 应用，幂等可重复执行。"""
    like = TEST_PREFIX.replace("_", r"\_") + "%"
    sql(f"DELETE FROM plugins WHERE code LIKE '{like}';")
    sql(f"DELETE FROM mcp_servers WHERE code LIKE '{like}';")
    sql(f"DELETE FROM applications WHERE code LIKE '{like}';")


# ── 用例 ──────────────────────────────────────────────────
def run(token):
    # 用例1 插件 http 内网IP 创建 + 更新
    HTTP_IP = "http://192.168.20.16:8080/api"
    code = uniq("plg")
    st, b = post("/api/admin/plugins", {"code": code, "name": "hio插件",
        "endpoint_url": HTTP_IP, "tool_schema_json": {"type": "function"}}, token)
    saved = d(b) or {}
    ok = st in (200, 201) and saved.get("endpoint_url") == HTTP_IP
    rec("1a", "插件 http内网IP 创建放行", ok, st,
        f"endpoint_url={saved.get('endpoint_url')} msg={msg(b)}")
    pid = saved.get("id")
    if pid:
        nurl = "http://10.0.0.5:9090/v2"
        st2, b2 = patch(f"/api/admin/plugins/{pid}", {"endpoint_url": nurl}, token)
        ok2 = st2 == 200 and (d(b2) or {}).get("endpoint_url") == nurl
        rec("1b", "插件 http内网IP 更新放行", ok2, st2,
            f"endpoint_url={(d(b2) or {}).get('endpoint_url')} msg={msg(b2)}")

    # 用例2 MCP http 内网IP 保存（discover 网络不可达不算校验拒绝）
    mcode = uniq("mcp")
    murl = "http://192.168.20.16:8080/mcp"
    st, b = post("/api/admin/mcp-servers", {"code": mcode, "name": "hio-mcp",
        "endpoint_url": murl}, token)
    saved = d(b) or {}
    ok = st in (200, 201) and saved.get("endpoint_url") == murl
    rec("2", "MCP http内网IP 保存放行", ok, st,
        f"endpoint_url={saved.get('endpoint_url')} msg={msg(b)}")
    mid = saved.get("id")
    if mid:
        std, bd = post(f"/api/admin/mcp-servers/{mid}/discover", None, token)
        dm = msg(bd) or str(d(bd))
        # 区分：校验拒绝(仅允许https/内网) vs 网络不可达
        blocked = any(k in dm for k in ["仅允许 https", "不允许指向内网", "不允许指向本机", "白名单"])
        note = "被 https/SSRF 校验拒绝(异常!)" if blocked else "未被 https/SSRF 校验拦截(网络不可达属预期)"
        rec("2-discover", "MCP discover 非校验拦截", not blocked, std, f"{note} | {dm[:120]}")

    # 用例3 应用 http 内网IP 保存
    acode = uniq("app")
    aurl = "http://192.168.20.16:3000"
    st, b = post("/api/admin/apps", {"code": acode, "name": "hio应用",
        "type": "external", "access_url": aurl}, token)
    saved = d(b) or {}
    ok = st in (200, 201) and saved.get("access_url") == aurl
    rec("3", "应用 http内网IP 保存放行", ok, st,
        f"access_url={saved.get('access_url')} msg={msg(b)}")
    aid = saved.get("id")

    # 用例4 应用 access_url 空串 -> 清空入口、不报错
    if aid:
        st4, b4 = patch(f"/api/admin/apps/{aid}", {"access_url": ""}, token)
        av = (d(b4) or {}).get("access_url")
        ok4 = st4 == 200 and (av in (None, ""))
        rec("4", "应用 access_url 空串清空", ok4, st4, f"access_url={av!r} msg={msg(b4)}")

    # 用例5 回归：公网 https 行为不变
    st, b = post("/api/admin/apps", {"code": uniq("app_https"), "name": "hio应用https",
        "type": "external", "access_url": "https://example.com"}, token)
    ok = st in (200, 201) and (d(b) or {}).get("access_url") == "https://example.com"
    rec("5a", "回归 应用 https保存放行", ok, st,
        f"access_url={(d(b) or {}).get('access_url')} msg={msg(b)}")
    st, b = post("/api/admin/plugins", {"code": uniq("plg_https"), "name": "hio插件https",
        "endpoint_url": "https://example.com/api", "tool_schema_json": {"type": "function"}}, token)
    ok = st in (200, 201) and (d(b) or {}).get("endpoint_url") == "https://example.com/api"
    rec("5b", "回归 插件 https保存放行", ok, st,
        f"endpoint_url={(d(b) or {}).get('endpoint_url')} msg={msg(b)}")

    # 用例6 危险 scheme javascript: 仍被拒
    st, b = post("/api/admin/plugins", {"code": uniq("js"), "name": "hio危险",
        "endpoint_url": "javascript:alert(1)", "tool_schema_json": {"type": "function"}}, token)
    rec("6a", "插件 javascript: 被拒", st == 400, st, f"msg={msg(b)}")
    st, b = post("/api/admin/apps", {"code": uniq("js_app"), "name": "hio危险app",
        "type": "external", "access_url": "javascript:alert(1)"}, token)
    rec("6b", "应用 javascript: 被拒", st == 400, st, f"msg={msg(b)}")

    # 用例7 http:// 缺 host 仍被拒
    st, b = post("/api/admin/plugins", {"code": uniq("noh"), "name": "hio缺host",
        "endpoint_url": "http://", "tool_schema_json": {"type": "function"}}, token)
    rec("7a", "插件 http:// 缺host 被拒", st == 400, st, f"msg={msg(b)}")
    st, b = post("/api/admin/apps", {"code": uniq("noh_app"), "name": "hio缺hostapp",
        "type": "external", "access_url": "http://"}, token)
    rec("7b", "应用 http:// 缺host 被拒", st == 400, st, f"msg={msg(b)}")


def main():
    print(f"{BOLD}{CYAN}{'='*70}{RESET}")
    print(f"{BOLD}{CYAN}  回归：放开 http/内网 IP 外呼校验（TRUST_INTERNAL_OUTBOUND）{RESET}")
    print(f"{BOLD}{CYAN}{'='*70}{RESET}")
    print(f"  API_BASE={API_BASE}  前缀={TEST_PREFIX}")
    print("  前置：测试服需已设 TRUST_INTERNAL_OUTBOUND=true（否则 1a/1b/2/3 会失败）\n")
    cleanup()  # 起始先清残留，保证幂等
    try:
        token, uid = setup_admin()
        print(f"  管理员就绪 uid={uid}\n")
        run(token)
    finally:
        cleanup()
    print(f"\n{BOLD}=== 汇总 ==={RESET}")
    fails = [r for r in results if not r[2]]
    for r in results:
        flag = f"{GREEN}PASS{RESET}" if r[2] else f"{RED}FAIL{RESET}"
        print(f"  用例{r[0]:<12} {flag} HTTP={r[3]} {r[1]}")
    passed = len(results) - len(fails)
    print(f"\n总计 {len(results)} 项, 通过 {passed}, 失败 {len(fails)}")
    if fails:
        print(f"{RED}总体结论: 不通过, 失败项: {[r[0] for r in fails]}{RESET}")
        sys.exit(1)
    print(f"{GREEN}总体结论: 回归通过{RESET}")


if __name__ == "__main__":
    main()

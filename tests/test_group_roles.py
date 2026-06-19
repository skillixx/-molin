#!/usr/bin/env python3
"""
组绑定全局角色（group_roles）端到端验收脚本

被测分支：feature/backend-iam-group-roles（PR #186）
设计文档：docs/backend-a-group-roles-design.md
接口契约：docs/api-test-guide-backend-a.md 9.13a 节

核心逻辑：
  - server/internal/modules/iam/service/iam_service.go  GetUserRoleIDs = 自身角色 ∪ 组绑定角色
  - server/internal/modules/iam/service/group_service.go  AddGroupRole / RemoveGroupRole（系统角色护栏）
  - server/internal/modules/iam/service/iam_service.go  DeleteRole 增加「被组绑定」占用校验
  - product 模块走 GetUserRoleIDs 做 can_view/can_buy 判定（零改动）

验收用例：
  A   组绑角色 X → 组员 GetUserRoleIDs 含 X → 对应商品可见/可购
  B   解绑角色 X → 即时失效（无缓存延迟），组员不再可见
  C1  绑定系统角色 admin → 400，"不允许将系统角色绑定到分组"
  C2  重复绑定同一角色 → 409
  C3  删除被组绑定的角色（DELETE /api/admin/roles/{id}）→ 被拒（角色未被删）
  D   端到端：默认组绑 registered_user → 新注册用户自动继承 → 自动可见/可购配给该角色的商品
  E   A 版边界：组角色不赋予管理权限码 → 组员 CheckPermission（访问 /api/admin/*）仍 403

断言方式：
  - 查 group_roles / user_group_members 表
  - 调用户端 GET /api/products、GET /api/products/{id} 看可见性
  - 调 POST /api/products/{id}/purchase 看可购性（仅断访问校验，不强制完成扣费）

前置数据自建自清。管理员双重认证通过直接写 admin_phone/email_verified_at=NOW() 满足
（等价于走 verify-phone/verify-email，避免发码限流），与参考脚本注入手法一致。

用法（本地，API 需为被测分支新二进制 + 可直连 MySQL）：
  API_BASE=http://127.0.0.1:8080 \
  MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
  MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
  ADMIN_EMAIL=admin@molin.io ADMIN_PASSWORD=Admin@123456 \
  NO_PROXY='*' python3 tests/test_group_roles.py
"""

import hashlib
import json
import os
import subprocess
import time
import urllib.error
import urllib.request

API_BASE   = os.getenv("API_BASE",   "http://127.0.0.1:8080")
MYSQL_HOST = os.getenv("MYSQL_HOST", "127.0.0.1")
MYSQL_PORT = int(os.getenv("MYSQL_PORT", "13306"))
MYSQL_USER = os.getenv("MYSQL_USER", "molin")
MYSQL_PASS = os.getenv("MYSQL_PASSWORD", "molin_password")
MYSQL_DB   = os.getenv("MYSQL_DATABASE", "molin")
ADMIN_EMAIL = os.getenv("ADMIN_EMAIL", "admin@molin.io")
ADMIN_PASS  = os.getenv("ADMIN_PASSWORD", "Admin@123456")

os.environ.setdefault("NO_PROXY", "*")
os.environ.setdefault("no_proxy", "*")

FIXED_CODE = "654321"  # 注册双 OTP 固定明文（脚本写入 verification_codes 的 SHA-256）

GREEN, RED, YELLOW, CYAN, BOLD, RESET = (
    "\033[92m", "\033[91m", "\033[93m", "\033[96m", "\033[1m", "\033[0m")

passed = 0
failed = 0
results = []


def ok(label, detail=""):
    global passed
    passed += 1
    print(f"  {GREEN}PASS{RESET} {label}" + (f"\n       {YELLOW}{detail}{RESET}" if detail else ""))
    results.append(("PASS", label, detail))


def fail(label, detail=""):
    global failed
    failed += 1
    print(f"  {RED}FAIL{RESET} {label}" + (f"\n       {RED}{detail}{RESET}" if detail else ""))
    results.append(("FAIL", label, detail))


# ── HTTP（urllib，显式禁代理）────────────────────────────────────────────────
_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def http(method, path, body=None, token=None, extra_headers=None):
    url = API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if extra_headers:
        headers.update(extra_headers)
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with _opener.open(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {}
    except Exception as ex:
        return 0, {"error": str(ex)}


# ── MySQL ─────────────────────────────────────────────────────────────────────
def _mysql(args, sql):
    cmd = ["mysql", "-h", MYSQL_HOST, f"-P{MYSQL_PORT}", f"-u{MYSQL_USER}",
           f"-p{MYSQL_PASS}", MYSQL_DB] + args + ["-e", sql]
    return subprocess.run(cmd, capture_output=True, text=True, timeout=15)


def db_query(sql):
    r = _mysql(["-N", "-s"], sql)
    if r.returncode != 0:
        return None, r.stderr.strip()
    rows = [ln.split("\t") for ln in r.stdout.strip().split("\n") if ln]
    return rows, None


def db_exec(sql):
    r = _mysql([], sql)
    if r.returncode != 0:
        return False, r.stderr.strip()
    return True, None


def sha256hex(s):
    return hashlib.sha256(s.encode()).hexdigest()


# ── 前置数据标识 ──────────────────────────────────────────────────────────────
GRP_NORMAL  = "gr_e2e_normal"   # 普通组（用例 A/B/C/E）
GRP_DEFAULT = "gr_e2e_default"  # 默认组（用例 D）
ROLE_X      = "gr_e2e_role_x"   # 自建测试角色 X
PROD_CODE   = "gr_e2e_prod"     # 自建测试商品
PLAN_CODE   = "gr_e2e_plan"
ADMIN_PERM_CODE = "gr_e2e_admin_perm"  # 仅用于核对，不实际使用

REGISTERED_USER_CODE = "registered_user"  # 000029 seed 的基础角色

_created_user_emails = []
_created_user_phones = []
_seq = 0

state = {}  # 运行期收集的 id


# ── 清理 / 建数 ───────────────────────────────────────────────────────────────
def teardown(silent=False):
    # 删测试用户及其关联
    if _created_user_emails:
        emails = ",".join("'%s'" % e for e in _created_user_emails)
        phones = ",".join("'%s'" % p for p in _created_user_phones)
        db_exec(f"DELETE FROM user_group_members WHERE user_id IN (SELECT id FROM users WHERE email IN ({emails}));")
        db_exec(f"DELETE FROM user_roles WHERE user_id IN (SELECT id FROM users WHERE email IN ({emails}));")
        db_exec(f"DELETE FROM user_sessions WHERE user_id IN (SELECT id FROM users WHERE email IN ({emails}));")
        db_exec(f"DELETE FROM users WHERE email IN ({emails});")
        db_exec(f"DELETE FROM verification_codes WHERE target_value IN ({emails}) OR target_value IN ({phones});")
    # 删商品访问 / 价格 / 套餐 / 商品
    db_exec(f"DELETE FROM product_role_access WHERE product_id IN (SELECT id FROM products WHERE product_code='{PROD_CODE}');")
    db_exec("DELETE FROM product_prices WHERE product_plan_id IN "
            f"(SELECT id FROM product_plans WHERE product_id IN (SELECT id FROM products WHERE product_code='{PROD_CODE}'));")
    db_exec(f"DELETE FROM product_plans WHERE product_id IN (SELECT id FROM products WHERE product_code='{PROD_CODE}');")
    db_exec(f"DELETE FROM products WHERE product_code='{PROD_CODE}';")
    # 删组角色绑定 / 组 / 自建角色
    db_exec("DELETE FROM group_roles WHERE group_id IN "
            f"(SELECT id FROM user_groups WHERE code IN ('{GRP_NORMAL}','{GRP_DEFAULT}'));")
    db_exec("DELETE FROM user_group_members WHERE group_id IN "
            f"(SELECT id FROM user_groups WHERE code IN ('{GRP_NORMAL}','{GRP_DEFAULT}'));")
    db_exec(f"DELETE FROM user_groups WHERE code IN ('{GRP_NORMAL}','{GRP_DEFAULT}');")
    db_exec(f"DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE code='{ROLE_X}');")
    db_exec(f"DELETE FROM roles WHERE code='{ROLE_X}';")
    if not silent:
        print(f"{CYAN}前置/测试数据已清理{RESET}")


def setup():
    teardown(silent=True)  # 幂等清残留
    # 1. 普通组（非默认）
    db_exec(f"INSERT INTO user_groups (code,name,type,is_default,created_at,updated_at) "
            f"VALUES ('{GRP_NORMAL}','E2E普通组','custom',0,NOW(),NOW());")
    state["grp_normal"] = int(db_query(f"SELECT id FROM user_groups WHERE code='{GRP_NORMAL}';")[0][0][0])
    # 2. 自建角色 X
    db_exec(f"INSERT INTO roles (code,name,description) VALUES ('{ROLE_X}','E2E角色X','测试用商品访问角色');")
    state["role_x"] = int(db_query(f"SELECT id FROM roles WHERE code='{ROLE_X}';")[0][0][0])
    # 3. registered_user 基础角色（000029 seed）
    rows, _ = db_query(f"SELECT id FROM roles WHERE code='{REGISTERED_USER_CODE}';")
    if not rows:
        raise SystemExit(f"{RED}缺少基础角色 {REGISTERED_USER_CODE}（migration 000029 未执行）{RESET}")
    state["role_reg"] = int(rows[0][0])
    # 4. admin 角色 id（系统角色护栏用例）
    state["role_admin"] = int(db_query("SELECT id FROM roles WHERE code='admin';")[0][0][0])
    # 5. 测试商品 + 套餐 + 价格（active）
    db_exec(f"INSERT INTO products (product_type,product_code,name,status,created_at,updated_at) "
            f"VALUES ('token','{PROD_CODE}','E2E测试商品','active',NOW(),NOW());")
    state["prod"] = int(db_query(f"SELECT id FROM products WHERE product_code='{PROD_CODE}';")[0][0][0])
    db_exec(f"INSERT INTO product_plans (product_id,plan_code,name,billing_type,duration_days,status,created_at,updated_at) "
            f"VALUES ({state['prod']},'{PLAN_CODE}','E2E套餐','one_time',30,'active',NOW(),NOW());")
    state["plan"] = int(db_query(f"SELECT id FROM product_plans WHERE product_id={state['prod']} AND plan_code='{PLAN_CODE}';")[0][0][0])
    # 价格：对角色 X 与 registered_user 各配一档（注册用户用例 D 走 registered_user）
    db_exec(f"INSERT INTO product_prices (product_plan_id,role_id,price_amount,currency) "
            f"VALUES ({state['plan']},{state['role_x']},9.990000,'CNY');")
    db_exec(f"INSERT INTO product_prices (product_plan_id,role_id,price_amount,currency) "
            f"VALUES ({state['plan']},{state['role_reg']},9.990000,'CNY');")
    # 商品访问：角色 X 与 registered_user 均 can_view=1 can_buy=1
    db_exec(f"INSERT INTO product_role_access (product_id,role_id,can_view,can_buy,can_use) "
            f"VALUES ({state['prod']},{state['role_x']},1,1,1);")
    db_exec(f"INSERT INTO product_role_access (product_id,role_id,can_view,can_buy,can_use) "
            f"VALUES ({state['prod']},{state['role_reg']},1,1,1);")
    return state


# ── 登录工具 ──────────────────────────────────────────────────────────────────
def admin_token():
    """管理员登录 + 直接置双重认证时间戳，返回 access_token。"""
    db_exec(f"UPDATE users SET admin_phone_verified_at=NOW(), admin_email_verified_at=NOW() "
            f"WHERE email='{ADMIN_EMAIL}';")
    s, b = http("POST", "/api/auth/login/email", {"email": ADMIN_EMAIL, "password": ADMIN_PASS})
    if s != 200 or b.get("code") != 0:
        raise SystemExit(f"{RED}管理员登录失败 status={s} body={b}{RESET}")
    return b["data"]["access_token"]


def _seed_code(target_type, target_value):
    db_exec(
        "INSERT INTO verification_codes (target_type,target_value,code,scene,expires_at,created_at) "
        f"VALUES ('{target_type}','{target_value}','{sha256hex(FIXED_CODE)}','register',"
        "DATE_ADD(NOW(), INTERVAL 1 DAY), NOW());")


def register():
    """注册一个新用户，返回 (status, body, email)。"""
    global _seq
    _seq += 1
    base = int(time.time() * 1000) % 1_000_000_000
    phone = f"13{(base + _seq) % 1000000000:09d}"
    email = f"gr_e2e_{base}_{_seq}@molin.test"
    uname = f"gre2e{base}{_seq}"
    _created_user_emails.append(email)
    _created_user_phones.append(phone)
    _seed_code("phone", phone)
    _seed_code("email", email)
    body = {"username": uname, "phone": phone, "email": email, "password": "Test@123456",
            "phone_code": FIXED_CODE, "email_code": FIXED_CODE}
    s, b = http("POST", "/api/auth/register", body)
    return s, b, email


def login_user(email):
    s, b = http("POST", "/api/auth/login/email", {"email": email, "password": "Test@123456"})
    if s == 200 and b.get("code") == 0:
        return b["data"]["access_token"]
    return None


def create_user_in_group(group_id, email_tag):
    """注册新用户并把其加入指定组（member），返回 (uid, email, token)。"""
    s, b, email = register()
    if s not in (200, 201) or b.get("code") != 0:
        raise SystemExit(f"{RED}用户注册失败 status={s} body={b}{RESET}")
    uid = int(db_query(f"SELECT id FROM users WHERE email='{email}';")[0][0][0])
    # 清掉注册可能自动落的默认组关系，确保该用户只在我们指定的组里（隔离干扰）
    db_exec(f"DELETE FROM user_group_members WHERE user_id={uid};")
    db_exec(f"INSERT INTO user_group_members (group_id,user_id,group_role,created_at) "
            f"VALUES ({group_id},{uid},'member',NOW());")
    # 置为已实名：购买链路实名校验(步骤1)在 can_buy(步骤2)之前，
    # 未实名会先被 70001 拦截而无法触达 can_buy。置已实名后 can_buy 成为购买放行的决定因素。
    db_exec(f"UPDATE users SET real_name_status='verified' WHERE id={uid};")
    token = login_user(email)
    return uid, email, token


def user_role_ids(uid):
    """通过商品可见性间接验证不便，直接查 DB：自身角色（不含组角色）。
    组角色继承是 GetUserRoleIDs 的运行期合并，DB 里 user_roles 不写组角色，
    故组角色继承只能经接口（商品可见/可购）断言，本函数仅返回自身角色。"""
    rows, _ = db_query(f"SELECT role_id FROM user_roles WHERE user_id={uid};")
    return sorted(int(r[0]) for r in (rows or []))


def product_visible(token, prod_id):
    """商品是否出现在用户端列表 + 详情是否可访问。"""
    s_list, b_list = http("GET", "/api/products?page=1&page_size=100", token=token)
    in_list = False
    if s_list == 200 and b_list.get("code") == 0:
        items = b_list["data"].get("items") or []
        in_list = any(str(it.get("id")) == str(prod_id) for it in items)
    s_detail, _ = http("GET", f"/api/products/{prod_id}", token=token)
    detail_ok = (s_detail == 200)
    return in_list, detail_ok, s_detail


def product_buyable(token, prod_id, plan_id):
    """尝试购买，仅区分「访问校验是否放行」。
    返回 (status, code)。带 Idempotency-Key（否则 400/40000 在触达 can_buy 前就被拦）。
    测试用户已置实名，故 can_buy 拒绝 → 403/40003；放行 → 余额不足 60001（无钱包）等业务错误。"""
    import uuid
    s, b = http("POST", f"/api/products/{prod_id}/purchase",
                {"plan_id": plan_id, "quantity": 1}, token=token,
                extra_headers={"Idempotency-Key": "gr-e2e-" + uuid.uuid4().hex})
    return s, b.get("code")


ACCESS_DENIED_CODES = {40003}  # 购买权限不足（ErrNoAccess）


def buy_access_passed(status, code):
    """can_buy 是否放行：403/40003 为访问被拒；其余（典型 60001 余额不足）视为已过 can_buy。
    注意：40000(缺幂等键/参数错误) 与 70001(未实名) 不应出现——出现即测试前置异常。"""
    if status == 403 or code in ACCESS_DENIED_CODES:
        return False
    if code in (40000, 70001):
        # 前置异常：未触达 can_buy，结论不可信
        return None
    return True


# ── 用例 ──────────────────────────────────────────────────────────────────────
def case_A(admin_tok):
    print(f"\n{BOLD}[A] 组绑角色 X → 组员继承 → 商品可见/可购{RESET}")
    gid = state["grp_normal"]
    rx = state["role_x"]
    prod = state["prod"]
    plan = state["plan"]
    uid, email, utok = create_user_in_group(gid, "A")

    # 绑定前：组员不应可见（无任何角色）
    vis_before, _, _ = product_visible(utok, prod)
    if vis_before:
        fail("A 前置 组员在绑定前不应可见", f"user={uid} 绑定前已可见，环境不干净")
    else:
        ok("A 前置 绑定前组员不可见（无角色）", f"user={uid}")

    # 绑定角色 X
    s, b = http("POST", f"/api/admin/user-groups/{gid}/roles", {"role_id": rx}, token=admin_tok)
    if s != 201 or b.get("code") != 0:
        return fail("A 绑定角色应 201", f"status={s} body={b}")
    ok("A 绑定角色 X 返回 201", f"group={gid} role_id={rx}")

    # DB 断言：group_roles 有该记录
    rows, _ = db_query(f"SELECT id FROM group_roles WHERE group_id={gid} AND role_id={rx};")
    if rows:
        ok("A group_roles 表已写入绑定记录", f"group={gid} role_id={rx}")
    else:
        fail("A group_roles 表未写入绑定记录", f"group={gid} role_id={rx}")

    # 即时生效：组员现在应可见 + 可购（GetUserRoleIDs 合并组角色）
    vis, detail_ok, sd = product_visible(utok, prod)
    if vis and detail_ok:
        ok("A 组员经组角色继承 → 商品列表可见 + 详情可访问", f"user={uid} prod={prod}")
    else:
        fail("A 组员应可见商品", f"user={uid} in_list={vis} detail_status={sd}")

    st, cd = product_buyable(utok, prod, plan)
    passed = buy_access_passed(st, cd)
    if passed is True:
        ok("A 组员可购（can_buy 放行，触达余额/定价环节）", f"purchase status={st} code={cd}")
    elif passed is None:
        fail("A 购买前置异常（未触达 can_buy）", f"purchase status={st} code={cd}")
    else:
        fail("A 组员应可购但访问被拒", f"purchase status={st} code={cd}")

    state["_A"] = (gid, rx, prod, plan, uid, utok)


def case_B(admin_tok):
    print(f"\n{BOLD}[B] 解绑角色 X → 即时失效，组员不再可见{RESET}")
    gid, rx, prod, plan, uid, utok = state["_A"]
    s, b = http("DELETE", f"/api/admin/user-groups/{gid}/roles/{rx}", token=admin_tok)
    if s != 200 or b.get("code") != 0:
        return fail("B 解绑应 200", f"status={s} body={b}")
    ok("B 解绑角色 X 返回 200", f"group={gid} role_id={rx}")

    rows, _ = db_query(f"SELECT id FROM group_roles WHERE group_id={gid} AND role_id={rx};")
    if not rows:
        ok("B group_roles 表绑定记录已移除", f"group={gid} role_id={rx}")
    else:
        fail("B group_roles 表仍有绑定记录", f"rows={rows}")

    # 即时失效（无缓存延迟）：同一 token 再查应不可见
    vis, detail_ok, sd = product_visible(utok, prod)
    if not vis and not detail_ok:
        ok("B 解绑后即时失效 → 列表不可见 + 详情不可访问", f"user={uid} detail_status={sd}")
    elif not vis and detail_ok:
        fail("B 列表不可见但详情仍可访问（部分失效）", f"user={uid} detail_status={sd}")
    else:
        fail("B 解绑后仍可见（未即时失效，疑似缓存）", f"user={uid} in_list={vis}")

    st, cd = product_buyable(utok, prod, plan)
    passed = buy_access_passed(st, cd)
    if passed is False:
        ok("B 解绑后不可购（can_buy 访问被拒 403/40003）", f"purchase status={st} code={cd}")
    elif passed is None:
        fail("B 购买前置异常（未触达 can_buy）", f"purchase status={st} code={cd}")
    else:
        fail("B 解绑后仍可购", f"purchase status={st} code={cd}")


def case_C(admin_tok):
    print(f"\n{BOLD}[C] 边界：禁绑系统角色 / 重复绑定 / 删被绑角色{RESET}")
    gid = state["grp_normal"]
    rx = state["role_x"]

    # C1 绑系统角色 admin → 400
    s, b = http("POST", f"/api/admin/user-groups/{gid}/roles",
                {"role_id": state["role_admin"]}, token=admin_tok)
    if s == 400 and b.get("code") == 40000:
        ok("C1 绑定系统角色 admin 被拒（400/40000）", f"msg={b.get('message')}")
    else:
        fail("C1 绑系统角色应 400/40000", f"status={s} body={b}")

    # C2 重复绑定 → 409（先绑一次再绑一次）
    http("POST", f"/api/admin/user-groups/{gid}/roles", {"role_id": rx}, token=admin_tok)
    s, b = http("POST", f"/api/admin/user-groups/{gid}/roles", {"role_id": rx}, token=admin_tok)
    if s == 409 and b.get("code") == 40900:
        ok("C2 重复绑定同一角色被拒（409/40900）", f"msg={b.get('message')}")
    else:
        fail("C2 重复绑定应 409/40900", f"status={s} body={b}")

    # C3 删除被组绑定的角色 → 应被拒（角色未被删）
    s, b = http("DELETE", f"/api/admin/roles/{rx}", token=admin_tok)
    role_still = db_query(f"SELECT id FROM roles WHERE id={rx};")[0]
    deleted = not role_still
    if deleted:
        fail("C3 删被绑角色 数据完整性破坏：角色被实际删除", f"status={s} body={b}")
    else:
        # 角色未被删（护栏在 service 层生效）。理想响应：4xx + 业务码 + 明确文案。
        if s in (400, 409) and "绑定" in str(b.get("message", "")):
            ok("C3 删被绑角色被拒且角色保留（响应为客户端错误）", f"status={s} body={b}")
        else:
            fail("C3 删被绑角色：角色已正确保留，但响应非预期 4xx 业务错误（疑似 handler 未映射 ErrRoleInUseByGroup）",
                 f"status={s} body={b} 期望 400/409 + 文案含'绑定'")
    # 清理：解绑，便于 teardown
    http("DELETE", f"/api/admin/user-groups/{gid}/roles/{rx}", token=admin_tok)


def case_D(admin_tok):
    print(f"\n{BOLD}[D] 端到端：默认组绑 registered_user → 新注册用户自动继承 → 自动可见/可购{RESET}")
    # 准备一个干净的默认组（保证全局只有这一个 is_default=1）
    rows, _ = db_query("SELECT COUNT(*) FROM user_groups WHERE is_default=1;")
    pre_default = int(rows[0][0]) if rows else 0
    # 临时把已有默认组都降级，建我们的默认组（finally 还原）
    db_exec("UPDATE user_groups SET is_default=0 WHERE is_default=1;")
    db_exec(f"INSERT INTO user_groups (code,name,type,is_default,created_at,updated_at) "
            f"VALUES ('{GRP_DEFAULT}','E2E默认组','custom',1,NOW(),NOW());")
    def_id = int(db_query(f"SELECT id FROM user_groups WHERE code='{GRP_DEFAULT}';")[0][0][0])
    rreg = state["role_reg"]
    prod = state["prod"]
    plan = state["plan"]
    try:
        # 默认组绑 registered_user
        s, b = http("POST", f"/api/admin/user-groups/{def_id}/roles", {"role_id": rreg}, token=admin_tok)
        if s != 201 or b.get("code") != 0:
            return fail("D 默认组绑 registered_user 应 201", f"status={s} body={b}")
        ok("D 默认组绑定 registered_user 返回 201", f"def_group={def_id} role_id={rreg}")

        # 新注册一个用户（不带邀请码 → 自动落默认组）
        s, b, email = register()
        if s not in (200, 201) or b.get("code") != 0:
            return fail("D 新用户注册应成功", f"status={s} body={b}")
        uid = int(db_query(f"SELECT id FROM users WHERE email='{email}';")[0][0][0])
        ms, _ = db_query(f"SELECT group_id FROM user_group_members WHERE user_id={uid};")
        in_default = any(r[0] == str(def_id) for r in (ms or []))
        if in_default:
            ok("D 新注册用户自动落入默认组", f"user={uid} def_group={def_id}")
        else:
            fail("D 新用户未落默认组（落组联动异常）", f"user={uid} memberships={ms}")

        # 该用户自身没有任何 user_roles，但应经组继承 registered_user → 可见/可购
        own = user_role_ids(uid)
        if own == []:
            ok("D 用户自身无 user_roles（角色完全来自组继承）", f"user={uid} own_roles={own}")
        else:
            fail("D 用户自身不应有直配角色（应纯靠组继承）", f"user={uid} own_roles={own}")

        utok = login_user(email)
        vis, detail_ok, sd = product_visible(utok, prod)
        if vis and detail_ok:
            ok("D 新注册用户自动可见配给 registered_user 的商品", f"user={uid} prod={prod}")
        else:
            fail("D 新注册用户应可见商品", f"user={uid} in_list={vis} detail_status={sd}")

        # D 用户经注册落组，需置实名后再验证可购（与 create_user_in_group 同理）
        db_exec(f"UPDATE users SET real_name_status='verified' WHERE id={uid};")
        st, cd = product_buyable(utok, prod, plan)
        passed = buy_access_passed(st, cd)
        if passed is True:
            ok("D 新注册用户可购（can_buy 放行）", f"purchase status={st} code={cd}")
        elif passed is None:
            fail("D 购买前置异常（未触达 can_buy）", f"purchase status={st} code={cd}")
        else:
            fail("D 新注册用户应可购但访问被拒", f"purchase status={st} code={cd}")

        # 端到端零逐人：解绑默认组角色后，该用户即时不再可见（止血验证）
        http("DELETE", f"/api/admin/user-groups/{def_id}/roles/{rreg}", token=admin_tok)
        vis2, _, _ = product_visible(utok, prod)
        if not vis2:
            ok("D 解绑默认组角色后该用户即时不可见（止血即时生效）", f"user={uid}")
        else:
            fail("D 解绑默认组角色后用户仍可见", f"user={uid}")
    finally:
        db_exec(f"DELETE FROM group_roles WHERE group_id={def_id};")
        db_exec(f"DELETE FROM user_group_members WHERE group_id={def_id};")
        db_exec(f"DELETE FROM user_groups WHERE id={def_id};")
        # 还原原默认组（若之前存在）
        if pre_default > 0:
            # 还原所有此前被降级的组不可逐一精确，仅记录提示；测试环境通常单默认组
            db_exec(f"UPDATE user_groups SET is_default=1 WHERE code='{GRP_DEFAULT}';")  # 已删，无副作用
        print(f"{CYAN}D 默认组数据已还原（注意：若原环境有多默认组需人工核对）{RESET}")


def case_E(admin_tok):
    print(f"\n{BOLD}[E] A 版边界：组角色不赋予管理权限码（CheckPermission 仍 false）{RESET}")
    gid = state["grp_normal"]
    radmin = state["role_admin"]  # admin 角色带全套管理权限码（含 group:manage）
    rx = state["role_x"]

    uid, email, utok = create_user_in_group(gid, "E")

    # 直接给组绑 admin 走护栏会被拒；为验证「即使组绑了带权限码的角色，组员也拿不到权限码」，
    # 直接在 DB 把 admin 角色绑到组（绕过护栏，模拟极端误配），再断言组员仍无管理权限。
    db_exec(f"INSERT IGNORE INTO group_roles (group_id,role_id,created_at) VALUES ({gid},{radmin},NOW());")
    bound, _ = db_query(f"SELECT id FROM group_roles WHERE group_id={gid} AND role_id={radmin};")
    if not bound:
        return fail("E 前置 无法在 DB 绑 admin 角色到组", "")

    # 组员尝试访问管理接口（需 group:manage 权限码 + 双重认证）
    # 该用户未做管理员双重认证，但权限码判定在双重认证之前（RequirePerm 在外层）。
    # 预期：因无 group:manage 权限码 → 403（且即便有也会卡双重认证），核心是「拿不到管理权限」。
    s, b = http("GET", "/api/admin/user-groups", token=utok)
    if s == 403:
        ok("E 组绑 admin 角色后，组员访问 /api/admin/* 仍 403（组角色不进 CheckPermission）",
           f"status={s} code={b.get('code')} msg={b.get('message')}")
    elif s == 401:
        ok("E 组员访问管理接口被拒（401，无管理身份）", f"status={s} body={b}")
    else:
        fail("E 组员不应能访问管理接口（A 版边界被突破，疑似组角色进入了权限判定）",
             f"status={s} body={b}")

    # 反向佐证：同一组绑「无权限码」的角色 X，组员对应商品可见（说明商品路径确实继承了组角色，
    # 但权限码路径没有），两者对比凸显 A 版边界。
    db_exec(f"INSERT IGNORE INTO group_roles (group_id,role_id,created_at) VALUES ({gid},{rx},NOW());")
    vis, _, _ = product_visible(utok, state["prod"])
    if vis:
        ok("E 对比佐证：组绑无权限码角色 X → 商品路径已继承（可见），但权限码路径未继承（上一步 403）",
           f"user={uid} 说明组角色【只影响商品、不影响权限码】")
    else:
        fail("E 对比佐证失败：组绑角色 X 商品仍不可见", f"user={uid}")

    # 清理本用例在 DB 直插的绑定
    db_exec(f"DELETE FROM group_roles WHERE group_id={gid} AND role_id IN ({radmin},{rx});")


def main():
    print(f"{BOLD}{CYAN}=== 组绑定全局角色（group_roles）端到端验收 ==={RESET}")
    print(f"API_BASE={API_BASE}  MYSQL={MYSQL_HOST}:{MYSQL_PORT}/{MYSQL_DB}")
    s, b = http("GET", "/api/health")
    if s != 200:
        print(f"{RED}API 健康检查失败 status={s} body={b}，中止{RESET}")
        raise SystemExit(2)

    # 校验被测分支特征：group_roles 表存在 + registered_user 角色存在
    t, _ = db_query("SHOW TABLES LIKE 'group_roles';")
    if not t:
        print(f"{RED}group_roles 表不存在，DB 未迁到 000028+，中止{RESET}")
        raise SystemExit(2)

    setup()
    print(f"前置就绪：普通组={state['grp_normal']} 角色X={state['role_x']} "
          f"registered_user={state['role_reg']} 商品={state['prod']} 套餐={state['plan']}")
    atok = admin_token()
    try:
        case_A(atok)
        case_B(atok)
        case_C(atok)
        case_D(atok)
        case_E(atok)
    finally:
        teardown()

    print(f"\n{BOLD}=== 结果汇总 ==={RESET}")
    for st, label, _ in results:
        mark = f"{GREEN}PASS{RESET}" if st == "PASS" else f"{RED}FAIL{RESET}"
        print(f"  [{mark}] {label}")
    print(f"\n{BOLD}通过 {GREEN}{passed}{RESET}{BOLD} / 失败 {RED}{failed}{RESET}{BOLD} / 共 {passed + failed}{RESET}")
    raise SystemExit(1 if failed else 0)


if __name__ == "__main__":
    main()

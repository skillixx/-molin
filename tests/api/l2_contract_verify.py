#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
L2 前端 API 契约一致性核验脚本（只读核验 + 精确清理）。
复用测试服既有 QA 方法：DB 直写 OTP 绕过限流，注册 admin 后 SQL 授予 admin 角色再重登。
仅核对后端实际响应的端点/方法/字段/分页结构是否与前端 src/api + src/types 期望一致。
造数全部记录 PK，结束按精确主键删除，禁止 LIKE 批量删。
"""
import os, json, time, hashlib, subprocess, urllib.request, urllib.error

API = os.getenv("API_BASE", "http://localhost:8080")
MH = os.getenv("MYSQL_HOST", "127.0.0.1")
MP = os.getenv("MYSQL_PORT", "13306")
MU = os.getenv("MYSQL_USER", "molin")
MPW = os.getenv("MYSQL_PASSWORD", "molin_password")
MDB = os.getenv("MYSQL_DATABASE", "molin")

PASS, FAIL = [], []
CREATED = {"user_ids": [], "level_ids": [], "benefit_ids": [], "ann_ids": [],
           "cat_ids": [], "art_ids": [], "app_ids": [], "adapter_ids": []}

def mysql(sql, fetch=False):
    r = subprocess.run(["mysql", "-h", MH, "-P", MP, "-u", MU, f"-p{MPW}", MDB, "-N", "-e", sql],
                       capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(f"mysql err: {r.stderr.strip()}\nSQL={sql}")
    if fetch:
        return [tuple(l.split("\t")) for l in r.stdout.strip().splitlines() if l]
    return None

def req(method, path, token=None, body=None):
    url = API + path
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        resp = urllib.request.urlopen(r, timeout=15)
        return resp.status, json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode())
        except Exception:
            return e.code, {}

def data_of(b):
    return (b or {}).get("data") or {}

def ok(name, cond, detail=""):
    (PASS if cond else FAIL).append((name, detail))
    print(("  [PASS] " if cond else "  [FAIL] ") + name + (f"  -- {detail}" if detail else ""))

def has_keys(obj, keys):
    miss = [k for k in keys if k not in obj]
    return (len(miss) == 0, "缺字段: " + ",".join(miss) if miss else "")

def is_flat_page(d):
    return all(k in d for k in ("items", "page", "page_size", "total")) and "pagination" not in d

def seed_otp(email, phone, code="888888", scene="register"):
    h = hashlib.sha256(code.encode()).hexdigest()
    exp = "DATE_ADD(NOW(), INTERVAL 1 DAY)"
    mysql(f"INSERT INTO verification_codes (target_type,target_value,code,scene,expires_at) VALUES ('email','{email}','{h}','{scene}',{exp});")
    mysql(f"INSERT INTO verification_codes (target_type,target_value,code,scene,expires_at) VALUES ('phone','{phone}','{h}','{scene}',{exp});")

_seq = [0]
def register(tag):
    _seq[0] += 1
    ts = int(time.time() * 1000) + _seq[0]
    email = f"l2_{tag}_{ts}@testmail.io"; phone = "139" + str(ts)[-8:]
    seed_otp(email, phone, "888888")
    s, b = req("POST", "/api/auth/register", body={"email": email, "phone": phone,
              "password": "Test1234!", "email_code": "888888", "phone_code": "888888"})
    d = data_of(b)
    tok = d.get("access_token", "")
    uid = (d.get("user") or {}).get("id")
    if not uid:
        _, mb = req("GET", "/api/me", tok); uid = data_of(mb).get("id")
    if not tok or not uid:
        raise RuntimeError(f"注册失败 {s} {b}")
    CREATED["user_ids"].append(int(uid))
    return int(uid), tok, email

def login(email):
    _, b = req("POST", "/api/auth/login/email", body={"email": email, "password": "Test1234!"})
    return data_of(b).get("access_token", "")

def main():
    print("== 准备 admin / 普通用户 ==")
    aid, _t, aemail = register("adm")
    mysql(f"INSERT IGNORE INTO user_roles (user_id, role_id) SELECT {aid}, id FROM roles WHERE code='admin' LIMIT 1;")
    atok = login(aemail)
    uid, utok, _ = register("usr")
    s, b = req("GET", "/api/admin/apps?page=1&page_size=5", atok)
    ok("admin token 可访问 /api/admin/apps", s == 200, f"status={s}")

    # ---------- FA-09 会员 ----------
    print("== FA-09 会员（membership-admin.ts）==")
    s, b = req("GET", "/api/admin/membership-levels", atok)
    d = data_of(b)
    ok("M3 GET /admin/membership-levels 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    lvname = f"L2级{int(time.time())}"
    s, b = req("POST", "/api/admin/membership-levels", atok,
               {"level_code": f"l2lvl{int(time.time()*1000)}", "name": lvname, "sort_order": 9})
    lvl = data_of(b); lvid = lvl.get("id")
    if lvid:
        CREATED["level_ids"].append(int(lvid))
    ok("M4 POST 创建等级返回对象含 id/level_code/name", s in (200, 201) and all(k in lvl for k in ("id", "level_code", "name")), f"status={s} keys={list(lvl)}")

    s, b = req("POST", "/api/admin/membership-benefits", atok,
               {"level_id": lvid, "benefit_type": "discount", "benefit_value": "{\"rate\":0.9}"})
    bf = data_of(b); bfid = bf.get("id")
    if bfid:
        CREATED["benefit_ids"].append(int(bfid))
    s, b = req("GET", f"/api/admin/membership-benefits?level_id={lvid}", atok)
    d = data_of(b)
    ok("M6 GET /admin/membership-benefits 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    if d.get("items"):
        c, m = has_keys(d["items"][0], ["id", "level_id", "benefit_type", "benefit_value", "status"])
        ok("M6 权益字段名一致 (benefit_value 为字符串)", c and isinstance(d["items"][0]["benefit_value"], str), m)

    # M10 grant duration_days=null 永久
    s, b = req("POST", "/api/admin/user-memberships", atok,
               {"user_id": uid, "level_id": lvid, "duration_days": None})
    gm = data_of(b)
    ok("M10 grant duration_days=null 成功(永久)", s in (200, 201) and gm.get("id"),
       f"status={s} expires_at={gm.get('expires_at')}")
    umid = gm.get("id")
    s, b = req("GET", f"/api/admin/user-memberships?user_id={uid}", atok)
    d = data_of(b)
    ok("M9 GET /admin/user-memberships 扁平分页", is_flat_page(d), f"keys={list(d)}")
    if d.get("items"):
        c, m = has_keys(d["items"][0], ["id", "user_id", "level_id", "level_code", "level_name", "asset_id", "status"])
        ok("M9 内联 level_name + asset_id 字段恒在", c, m)
    # M11 改期
    if umid:
        s, b = req("PATCH", f"/api/admin/user-memberships/{umid}", atok, {"expires_at": "2027-01-01T00:00:00+08:00"})
        ok("M11 PATCH 改期(expires_at)成功", s == 200, f"status={s} {b.get('message')}")
        s, b = req("PATCH", f"/api/admin/user-memberships/{umid}", atok, {"action": "cancel"})
        ok("M11 PATCH 取消(action=cancel)成功", s == 200, f"status={s} {b.get('message')}")

    # ---------- FA-06 资产 ----------
    print("== FA-06 资产（asset-admin.ts）==")
    s, b = req("GET", "/api/admin/assets?page=1&page_size=5", atok)
    d = data_of(b)
    ok("AS4 GET /admin/assets 扁平分页", is_flat_page(d), f"keys={list(d)}")
    if d.get("items"):
        c, m = has_keys(d["items"][0], ["id", "user_id", "asset_type", "product_id", "status", "created_at"])
        ok("AS4 资产字段名一致", c, m)
    s, b = req("GET", f"/api/admin/users/{uid}/assets", atok)
    d = data_of(b)
    ok("AS5 GET /admin/users/{id}/assets 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    # AS6 操作不存在资产, 仅核对 action+remark 契约被后端接受(404/400 而非 415/字段错误)
    s, b = req("PATCH", "/api/admin/assets/99999999", atok, {"action": "cancel", "remark": "L2核验"})
    ok("AS6 PATCH action+remark 契约被接受(非参数错误)", s in (400, 404) and b.get("code") not in (40000,) or s == 404,
       f"status={s} code={b.get('code')} msg={b.get('message')}")

    # ---------- FA-07 内容 ----------
    print("== FA-07 内容（content-admin.ts）==")
    s, b = req("GET", "/api/admin/announcements?page=1&page_size=5", atok)
    d = data_of(b)
    ok("C5 GET /admin/announcements 扁平分页", is_flat_page(d), f"keys={list(d)}")
    s, b = req("POST", "/api/admin/announcements", atok,
               {"title": "L2公告", "content": "x", "visible_scope": "all", "target_roles_json": None, "sort_order": 1})
    an = data_of(b); anid = an.get("id")
    if anid:
        CREATED["ann_ids"].append(int(anid))
    ok("C6 POST 公告(target_roles_json 字符串契约)成功", s in (200, 201) and anid, f"status={s} keys={list(an)}")
    s, b = req("GET", "/api/admin/help/categories", atok)
    d = data_of(b)
    ok("C7 GET /admin/help/categories 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    s, b = req("POST", "/api/admin/help/categories", atok, {"name": "L2分类", "sort_order": 1})
    cat = data_of(b); catid = cat.get("id")
    if catid:
        CREATED["cat_ids"].append(int(catid))
    s, b = req("GET", "/api/admin/help/articles?page=1&page_size=5", atok)
    d = data_of(b)
    ok("C8 GET /admin/help/articles 扁平分页", is_flat_page(d), f"keys={list(d)}")
    if catid:
        s, b = req("POST", "/api/admin/help/articles", atok,
                   {"category_id": catid, "title": "L2文章", "content": "x", "sort_order": 1})
        art = data_of(b); artid = art.get("id")
        if artid:
            CREATED["art_ids"].append(int(artid))
        ok("C9 POST 帮助文章成功", s in (200, 201) and artid, f"status={s}")

    # ---------- FA-10 应用 ----------
    print("== FA-10 应用（app-admin.ts）==")
    s, b = req("GET", "/api/admin/apps?page=1&page_size=5", atok)
    d = data_of(b)
    ok("AP2 GET /admin/apps 扁平分页", is_flat_page(d), f"keys={list(d)}")
    code = f"l2app{int(time.time()*1000)}"
    s, b = req("POST", "/api/admin/apps", atok,
               {"code": code, "name": "L2应用", "type": "tool",
                "adapter_config_json": "{\"k\":1}"})
    ap = data_of(b); apid = ap.get("id")
    if apid:
        CREATED["app_ids"].append(int(apid))
    c, m = has_keys(ap, ["id", "code", "name", "type", "adapter_config_json", "status"])
    ok("AP4 POST 应用 含 adapter_config_json(字符串) 字段", s in (200, 201) and c, f"status={s} {m}")
    s, b = req("GET", "/api/admin/app-adapters", atok)
    d = data_of(b)
    ok("AP5 GET /admin/app-adapters 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    s, b = req("POST", "/api/admin/app-adapters", atok,
               {"app_code": code, "app_name": "L2应用", "app_type": "tool",
                "adapter_type": "internal", "service_name": "svc",
                "supported_actions_json": "[\"provision\"]", "usage_event_types_json": "[]"})
    ad = data_of(b); adid = ad.get("id")
    if adid:
        CREATED["adapter_ids"].append(int(adid))
    c, m = has_keys(ad, ["id", "app_code", "app_name", "adapter_type", "service_name",
                         "supported_actions_json", "usage_event_types_json", "status"])
    ok("AP6 POST 适配器 三个 JSON 字符串字段齐全", s in (200, 201) and c, f"status={s} {m}")

    # ---------- FB-07/08/09 用户端 ----------
    print("== FB-07/08/09 用户端（asset.ts/membership.ts/content.ts）==")
    s, b = req("GET", "/api/my/assets", utok)
    d = data_of(b)
    ok("AS1 GET /my/assets 返回 {items} 不分页", "items" in d and "page" not in d, f"keys={list(d)}")
    s, b = req("GET", "/api/my/entitlements", utok)
    d = data_of(b)
    ok("AS3 GET /my/entitlements 返回 {items}", "items" in d, f"keys={list(d)}")
    s, b = req("GET", "/api/my/membership", utok)
    d = data_of(b)
    ok("M1/M2 GET /my/membership 返回 {membership} (对称, 无会员=null)", "membership" in d, f"keys={list(d)}")
    s, b = req("GET", "/api/announcements?page=1&page_size=5", utok)
    d = data_of(b)
    ok("C1 GET /announcements 扁平分页", is_flat_page(d), f"keys={list(d)}")

    # ---------- 后端乙抽查 ----------
    print("== 后端乙抽查（wallet）==")
    s, b = req("GET", "/api/wallet", utok)
    d = data_of(b)
    c, m = has_keys(d, ["wallet_id", "balance_amount", "frozen_amount", "currency"])
    ok("钱包返回 wallet_id/balance_amount 字段", s == 200 and c, f"status={s} {m}")
    s, b = req("GET", "/api/wallet/transactions?page=1&page_size=5", utok)
    d = data_of(b)
    ok("钱包流水 扁平分页 {items,page,page_size,total}", is_flat_page(d), f"keys={list(d)}")

def cleanup():
    print("== 精确主键清理 ==")
    def din(table, col, ids):
        ids = [str(int(i)) for i in ids if i]
        if not ids:
            return
        mysql(f"DELETE FROM {table} WHERE {col} IN ({','.join(ids)});")
    # 依赖顺序
    din("user_memberships", "user_id", CREATED["user_ids"])
    din("membership_benefits", "id", CREATED["benefit_ids"])
    din("membership_levels", "id", CREATED["level_ids"])
    din("announcements", "id", CREATED["ann_ids"])
    din("help_articles", "id", CREATED["art_ids"])
    din("help_categories", "id", CREATED["cat_ids"])
    din("application_adapters", "id", CREATED["adapter_ids"])
    din("applications", "id", CREATED["app_ids"])
    for uid in CREATED["user_ids"]:
        mysql(f"DELETE FROM user_roles WHERE user_id={int(uid)};")
        mysql(f"DELETE FROM user_sessions WHERE user_id={int(uid)};")
        mysql(f"DELETE FROM wallet_transactions WHERE user_id={int(uid)};")
        mysql(f"DELETE FROM wallets WHERE user_id={int(uid)};")
        mysql(f"DELETE FROM verification_codes WHERE target_value IN (SELECT email FROM users WHERE id={int(uid)}) OR target_value IN (SELECT phone FROM users WHERE id={int(uid)});")
    din("users", "id", CREATED["user_ids"])
    # 计数核对
    leftover = 0
    if CREATED["user_ids"]:
        ids = ",".join(str(int(i)) for i in CREATED["user_ids"])
        rows = mysql(f"SELECT COUNT(*) FROM users WHERE id IN ({ids});", fetch=True)
        leftover = int(rows[0][0]) if rows else -1
    print(f"  造数用户残留计数 = {leftover} (期望 0)")

if __name__ == "__main__":
    try:
        main()
    finally:
        try:
            cleanup()
        except Exception as e:
            print("清理异常:", e)
    print(f"\n汇总: PASS={len(PASS)} FAIL={len(FAIL)}")
    for n, d in FAIL:
        print("  FAIL:", n, "|", d)

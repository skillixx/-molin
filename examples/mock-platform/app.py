"""本地 mock 平台 —— 仅用于本地演示示例应用，不是真平台！

用内存实现示例应用会调到的几个平台内部接口，并模拟"我的资产 → 进入应用 → 签发一次性票据"。
这样两个示例应用（postpaid/prepaid）的代码可以**原样不改**地跑通完整链路，
你能在浏览器里点按钮、看到真实的计费/扣额度返回。

真平台是 Go 实现（server/）；本文件只复刻对接所需的最小行为，便于离线体验。

启动：  uvicorn app:app --port 8080
"""
from __future__ import annotations

import uuid
from decimal import Decimal

from fastapi import FastAPI, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse

app = FastAPI(title="mock 平台（仅演示）")

# —— 与示例应用 .env 里一致的内部密钥 ——
DEMO_TOKEN = "demo-internal-token-123"

# —— 两个示例应用的登记信息（演示写死）——
APPS = {
    "postpaid": {"app_id": 1, "product_id": 7, "enter": "http://127.0.0.1:9001/enter"},
    "prepaid":  {"app_id": 2, "product_id": 8, "enter": "http://127.0.0.1:9002/enter"},
}
DEMO_USER_ID = 1001

# —— 内存状态 ——
WALLET = {DEMO_USER_ID: Decimal("5.00")}        # 钱包余额（postpaid 扣这里），单价 1.00/次 → 够 5 次
UNIT_PRICE = Decimal("1.00")
ENTITLEMENTS = {                                 # 积分权益（prepaid 扣这里），key=(user_id, product_id)
    (DEMO_USER_ID, 8): {
        "entitlement_id": 5001, "user_id": DEMO_USER_ID, "product_id": 8,
        "quota_total": Decimal("100"), "quota_used": Decimal("0"), "quota_reserved": Decimal("0"),
        "status": "active", "expires_at": None,
    }
}
TICKETS: dict[str, dict] = {}                    # 一次性票据：lt_xxx -> claims
HOLDS: dict[int, dict] = {}                      # 预占：hold_id -> {entitlement_key, amount, status}
_hold_seq = [9000]
USAGE_IDEM: dict[str, dict] = {}                 # postpaid 幂等
RESERVE_IDEM: dict[str, dict] = {}              # reserve 幂等


def ok(data):
    return {"code": 0, "message": "ok", "data": data}


def err(code: int, message: str, http: int = 400):
    return JSONResponse({"code": code, "message": message, "data": None}, status_code=http)


def _check_token(request: Request):
    if request.headers.get("X-Internal-Token") != DEMO_TOKEN:
        return err(40003, "内部接口鉴权失败", 403)
    return None


def _balance_snapshot(e: dict) -> dict:
    total, used, reserved = e["quota_total"], e["quota_used"], e["quota_reserved"]
    remaining = total - used - reserved
    usable = e["status"] == "active" and remaining > 0
    return {
        "entitlement_id": e["entitlement_id"], "user_id": e["user_id"],
        "quota_total": str(total), "quota_used": str(used), "quota_reserved": str(reserved),
        "remaining": str(remaining), "status": e["status"], "expires_at": e["expires_at"],
        "usable": usable,
    }


# ============== 模拟用户端：我的资产 → 进入应用 ==============

@app.get("/", response_class=HTMLResponse)
def home():
    w = WALLET[DEMO_USER_ID]
    e = ENTITLEMENTS[(DEMO_USER_ID, 8)]
    rem = e["quota_total"] - e["quota_used"] - e["quota_reserved"]
    return f"""
    <h2>墨灵 · 我的资产（mock 平台，仅演示）</h2>
    <p>当前测试用户 user_id = <b>{DEMO_USER_ID}</b></p>
    <ul>
      <li>钱包余额（按量付费用）：<b>{w}</b> 元（单价 {UNIT_PRICE}/次）</li>
      <li>积分额度（预付用）：已用 <b>{e['quota_used']}</b> / 总 <b>{e['quota_total']}</b>，可用 <b>{rem}</b></li>
    </ul>
    <p>点「进入应用」会签发一次性票据并跳转到对应示例应用：</p>
    <p>
      <a href="/launch?app=postpaid"><button>进入「文本转换」(按量付费)</button></a>
      &nbsp;&nbsp;
      <a href="/launch?app=prepaid"><button>进入「AI 文案生成」(预付扣积分)</button></a>
    </p>
    <p><a href="/state">查看实时状态(JSON)</a></p>
    """


@app.get("/launch")
def launch(app: str = "postpaid"):
    """模拟平台签发一次性票据并 302 跳转到应用入口。"""
    info = APPS.get(app)
    if not info:
        return err(40000, "未知应用", 400)
    ticket = "lt_" + uuid.uuid4().hex[:16]
    TICKETS[ticket] = {"user_id": DEMO_USER_ID, "app_id": info["app_id"], "product_id": info["product_id"]}
    return RedirectResponse(url=f"{info['enter']}?ticket={ticket}", status_code=302)


@app.get("/state")
def state():
    return ok({
        "wallet": {str(k): str(v) for k, v in WALLET.items()},
        "entitlement": _balance_snapshot(ENTITLEMENTS[(DEMO_USER_ID, 8)]),
        "open_holds": [h for h in HOLDS.values() if h["status"] == "reserved"],
    })


# ============== 平台内部接口（示例应用调用） ==============

@app.post("/api/internal/app-launch/verify")
async def verify(request: Request):
    if (e := _check_token(request)) is not None:
        return e
    body = await request.json()
    ticket = body.get("launch_ticket", "")
    claims = TICKETS.pop(ticket, None)          # 一次性：pop 后即失效
    if claims is None:
        return err(40003, "票据无效、已过期或已被使用", 403)
    return ok(claims)


@app.post("/api/internal/product-usage-events")
async def usage_events(request: Request):
    if (e := _check_token(request)) is not None:
        return e
    body = await request.json()
    idem = body.get("idempotency_key", "")
    if idem in USAGE_IDEM:                        # 幂等：重复上报返回原结果
        return ok(USAGE_IDEM[idem])
    user_id = body["user_id"]
    amount = UNIT_PRICE * Decimal(str(body.get("usage_amount", "1")))
    if WALLET.get(user_id, Decimal("0")) < amount:
        return err(60001, "钱包余额不足", 400)
    WALLET[user_id] -= amount
    result = {
        "consumption_record_id": uuid.uuid4().int % 100000,
        "amount": str(amount),
        "idempotency_key": idem,
        "wallet_transaction_id": uuid.uuid4().int % 100000,
    }
    USAGE_IDEM[idem] = result
    return ok(result)


@app.get("/api/internal/user-entitlements")
def user_entitlements(request: Request, user_id: int, product_id: int):
    if (e := _check_token(request)) is not None:
        return e
    ent = ENTITLEMENTS.get((user_id, product_id))
    items = [_balance_snapshot(ent)] if ent else []
    return ok({"entitlements": items})


@app.post("/api/internal/entitlement-reserve")
async def reserve(request: Request):
    if (e := _check_token(request)) is not None:
        return e
    body = await request.json()
    idem = body.get("idempotency_key", "")
    if idem in RESERVE_IDEM:
        return ok(RESERVE_IDEM[idem])
    ent = _find_ent(body["entitlement_id"])
    if ent is None:
        return err(40400, "权益不存在", 404)
    amount = Decimal(str(body["amount"]))
    available = ent["quota_total"] - ent["quota_used"] - ent["quota_reserved"]
    if available < amount:
        return err(60005, "权益额度不足", 400)
    ent["quota_reserved"] += amount
    _hold_seq[0] += 1
    hold_id = _hold_seq[0]
    HOLDS[hold_id] = {"entitlement_id": ent["entitlement_id"], "amount": amount, "status": "reserved"}
    result = {
        "hold_id": hold_id, "reserved": str(amount),
        "available": str(ent["quota_total"] - ent["quota_used"] - ent["quota_reserved"]),
        "status": "reserved",
    }
    RESERVE_IDEM[idem] = result
    return ok(result)


@app.post("/api/internal/entitlement-settle")
async def settle(request: Request):
    if (e := _check_token(request)) is not None:
        return e
    body = await request.json()
    hold = HOLDS.get(body["hold_id"])
    if hold is None or hold["status"] != "reserved":
        return err(40400, "预占不存在或已结算", 404)
    ent = _find_ent(hold["entitlement_id"])
    actual = min(Decimal(str(body["actual_amount"])), hold["amount"])
    ent["quota_reserved"] -= hold["amount"]      # 释放预占
    ent["quota_used"] += actual                   # 计入实际消耗（多退少补）
    hold["status"] = "settled"
    return ok(_settle_result(hold["hold_id"] if "hold_id" in hold else body["hold_id"], ent, actual, "settled"))


@app.post("/api/internal/entitlement-release")
async def release(request: Request):
    if (e := _check_token(request)) is not None:
        return e
    body = await request.json()
    hold = HOLDS.get(body["hold_id"])
    if hold is None or hold["status"] != "reserved":
        return err(40400, "预占不存在或已结算", 404)
    ent = _find_ent(hold["entitlement_id"])
    ent["quota_reserved"] -= hold["amount"]      # 仅归还预占，不计 quota_used
    hold["status"] = "released"
    return ok(_settle_result(body["hold_id"], ent, Decimal("0"), "released"))


def _find_ent(entitlement_id: int):
    for ent in ENTITLEMENTS.values():
        if ent["entitlement_id"] == entitlement_id:
            return ent
    return None


def _settle_result(hold_id: int, ent: dict, settled: Decimal, status: str) -> dict:
    available = ent["quota_total"] - ent["quota_used"] - ent["quota_reserved"]
    return {
        "hold_id": hold_id, "status": status, "settled_amount": str(settled),
        "quota_used": str(ent["quota_used"]), "quota_reserved": str(ent["quota_reserved"]),
        "available": str(available),
    }

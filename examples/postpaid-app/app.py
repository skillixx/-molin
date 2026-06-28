"""按量付费（postpaid）示例应用 —— 一个"文本转换"小工具。

业务很简单：用户输入文本，应用把它转成大写并统计字数；每转换一次，
就向平台上报一次用量，平台按计费规则从用户钱包扣费。

你要关注的三件事（对接核心）：
  ① 身份：用户从平台点「进入应用」带 ?ticket= 进来 → 调 verify 换 user_id（免登）
  ② 用前：本例不需要额外查资产——平台签发票据前已校验用户持有 active 资产
  ③ 用时：动作完成后调 product-usage-events 上报用量（带幂等键）

启动：
  pip install -r requirements.txt
  cp .env.example .env   # 填入平台方给的 INTERNAL_API_TOKEN
  uvicorn app:app --reload --port 9001
"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone

from fastapi import FastAPI, Request, Form
from fastapi.responses import HTMLResponse, RedirectResponse, JSONResponse
from itsdangerous import URLSafeSerializer, BadSignature

import config
import platform_client as platform

app = FastAPI(title="按量付费示例应用")

# 用签名 Cookie 维持登录态（应用自有会话，与平台 JWT 无关）。
_serializer = URLSafeSerializer(config.SESSION_SECRET, salt="demo-session")
SESSION_COOKIE = "demo_session"


def _set_session(resp, data: dict) -> None:
    resp.set_cookie(SESSION_COOKIE, _serializer.dumps(data), httponly=True, samesite="lax")


def _get_session(request: Request) -> dict | None:
    raw = request.cookies.get(SESSION_COOKIE)
    if not raw:
        return None
    try:
        return _serializer.loads(raw)
    except BadSignature:
        return None


@app.get("/", response_class=HTMLResponse)
def index():
    """落地说明页。真实场景下用户不会先到这里，而是从平台点「进入应用」带票据进来。"""
    return """
    <h2>按量付费示例应用</h2>
    <p>正确入口：在平台「我的资产」点「进入应用」，会带一次性票据跳到 <code>/enter?ticket=...</code>。</p>
    <p>本页仅作说明。</p>
    """


@app.get("/enter")
def enter(ticket: str = ""):
    """SSO 入口：收到平台跳转过来的一次性票据，换取用户身份后建立本应用会话。"""
    if not ticket:
        return HTMLResponse("<h3>缺少 ticket，请从平台「进入应用」入口进入</h3>", status_code=400)
    try:
        claims = platform.verify_ticket(ticket)   # → {user_id, app_id, product_id}
    except platform.PlatformError as e:
        # 票据无效/过期/已用：让用户重新从平台进入，不要重试同一张票据
        return HTMLResponse(f"<h3>进入失败：{e.message}</h3><p>请回平台重新点「进入应用」。</p>", status_code=403)

    resp = RedirectResponse(url="/workspace", status_code=302)
    _set_session(resp, {
        "user_id": claims["user_id"],
        "product_id": claims["product_id"],
        "app_id": claims.get("app_id"),
    })
    return resp


@app.get("/workspace", response_class=HTMLResponse)
def workspace(request: Request):
    """工作台：免登后用户看到的功能页。"""
    sess = _get_session(request)
    if not sess:
        return HTMLResponse("<h3>未登录，请从平台「进入应用」进入</h3>", status_code=401)
    return f"""
    <h2>文本转换工具（按量付费）</h2>
    <p>当前用户 user_id = <b>{sess['user_id']}</b>，每转换一次按量扣费。</p>
    <form onsubmit="return doConvert(event)">
      <textarea id="text" rows="4" cols="50" placeholder="输入要转换的文本"></textarea><br/>
      <button type="submit">转换并计费</button>
    </form>
    <pre id="out"></pre>
    <script>
    async function doConvert(e) {{
      e.preventDefault();
      const text = document.getElementById('text').value;
      const r = await fetch('/api/convert', {{
        method: 'POST',
        headers: {{'Content-Type': 'application/x-www-form-urlencoded'}},
        body: 'text=' + encodeURIComponent(text)
      }});
      document.getElementById('out').textContent = JSON.stringify(await r.json(), null, 2);
      return false;
    }}
    </script>
    """


@app.post("/api/convert")
def convert(request: Request, text: str = Form("")):
    """核心动作：做业务 + 上报用量计费。"""
    sess = _get_session(request)
    if not sess:
        return JSONResponse({"error": "未登录"}, status_code=401)

    # ① 业务本身（你的应用功能）
    result = text.upper()
    char_count = len(text)

    # ② 上报用量计费。每次转换算 1 次用量（usage_amount="1"）。
    #    幂等键全局唯一且可复算——这里用一次性 UUID；真实场景建议用"业务事件ID:类型"。
    event_id = str(uuid.uuid4())
    try:
        billing = platform.report_usage(
            event_id=event_id,
            user_id=sess["user_id"],
            product_id=sess["product_id"],
            usage_amount="1",
            idempotency_key=f"{event_id}:{config.USAGE_TYPE}",
            occurred_at=datetime.now(timezone.utc).isoformat(),
        )
    except platform.PlatformError as e:
        if e.is_no_billing_rule:
            # 平台没给这个商品配该类计费规则：业务照常返回，不计费、不报错
            return {"result": result, "char_count": char_count, "billed": None, "note": "未配计费规则，本次不计费"}
        if e.is_wallet_insufficient:
            return JSONResponse({"error": "钱包余额不足，请充值后再试"}, status_code=402)
        # 其它错误（如鉴权失败）：交由你的监控/日志，提示用户稍后重试
        return JSONResponse({"error": f"计费失败：{e.message}"}, status_code=502)

    return {
        "result": result,
        "char_count": char_count,
        "billed_amount": billing.get("amount"),            # 本次实扣金额
        "consumption_record_id": billing.get("consumption_record_id"),
    }

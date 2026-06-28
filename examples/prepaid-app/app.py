"""预付/扣积分（prepaid）示例应用 —— 一个"AI 文案生成"小工具。

业务：用户给一句主题，应用"生成"一段文案；生成成本事前不确定（像 LLM 出多少字不一定），
所以用「预占 → 结算」：先按上限占住积分，生成完拿到实际消耗再多退少补。

你要关注的四步（对接核心）：
  ① 身份：?ticket= → verify 换 user_id（免登）
  ② 定位权益：票据没有 entitlement_id → 调 user-entitlements 按 user_id+product_id 解析
  ③ 预占：reserve(预估上限)，额度不足直接拒
  ④ 结算：业务完成后 settle(实际消耗)；失败则 release 回滚

启动：
  pip install -r requirements.txt
  cp .env.example .env   # 填入平台方给的 INTERNAL_API_TOKEN
  uvicorn app:app --reload --port 9002
"""
from __future__ import annotations

import uuid

from fastapi import FastAPI, Request, Form
from fastapi.responses import HTMLResponse, RedirectResponse, JSONResponse
from itsdangerous import URLSafeSerializer, BadSignature

import config
import platform_client as platform

app = FastAPI(title="预付扣积分示例应用")

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


def _generate_copy(topic: str) -> str:
    """模拟一次"生成"业务。真实场景这里会调你自己的模型/算法。"""
    topic = topic.strip() or "（空主题）"
    return f"关于「{topic}」的文案：{topic}，让美好发生。{topic}，不止于此。"


@app.get("/", response_class=HTMLResponse)
def index():
    return """
    <h2>预付扣积分示例应用</h2>
    <p>正确入口：在平台「我的资产」点「进入应用」，会带一次性票据跳到 <code>/enter?ticket=...</code>。</p>
    """


@app.get("/enter")
def enter(ticket: str = ""):
    """SSO 入口：票据换身份 → 解析积分权益 → 建立会话。"""
    if not ticket:
        return HTMLResponse("<h3>缺少 ticket，请从平台「进入应用」入口进入</h3>", status_code=400)
    try:
        claims = platform.verify_ticket(ticket)            # → {user_id, app_id, product_id}
    except platform.PlatformError as e:
        return HTMLResponse(f"<h3>进入失败：{e.message}</h3><p>请回平台重新点「进入应用」。</p>", status_code=403)

    # 票据没有 entitlement_id，按 user_id+product_id 解析该用户的可用积分权益
    try:
        ent = platform.resolve_entitlement(claims["user_id"], claims["product_id"])
    except platform.PlatformError as e:
        return HTMLResponse(f"<h3>查询权益失败：{e.message}</h3>", status_code=502)
    if ent is None:
        return HTMLResponse("<h3>你还没有可用的积分额度</h3><p>请先在平台购买积分套餐。</p>", status_code=403)

    resp = RedirectResponse(url="/workspace", status_code=302)
    _set_session(resp, {
        "user_id": claims["user_id"],
        "product_id": claims["product_id"],
        "entitlement_id": ent["entitlement_id"],
    })
    return resp


@app.get("/workspace", response_class=HTMLResponse)
def workspace(request: Request):
    sess = _get_session(request)
    if not sess:
        return HTMLResponse("<h3>未登录，请从平台「进入应用」进入</h3>", status_code=401)
    return f"""
    <h2>AI 文案生成（按积分计费）</h2>
    <p>user_id = <b>{sess['user_id']}</b>，entitlement_id = <b>{sess['entitlement_id']}</b></p>
    <form onsubmit="return doGen(event)">
      <input id="topic" size="40" placeholder="输入一个主题，如：夏日饮品"/>
      <button type="submit">生成（扣积分）</button>
    </form>
    <pre id="out"></pre>
    <script>
    async function doGen(e) {{
      e.preventDefault();
      const topic = document.getElementById('topic').value;
      const r = await fetch('/api/generate', {{
        method: 'POST',
        headers: {{'Content-Type': 'application/x-www-form-urlencoded'}},
        body: 'topic=' + encodeURIComponent(topic)
      }});
      document.getElementById('out').textContent = JSON.stringify(await r.json(), null, 2);
      return false;
    }}
    </script>
    """


@app.post("/api/generate")
def generate(request: Request, topic: str = Form("")):
    """核心动作：预占积分 → 生成 → 结算（失败则释放）。"""
    sess = _get_session(request)
    if not sess:
        return JSONResponse({"error": "未登录"}, status_code=401)

    entitlement_id = sess["entitlement_id"]
    user_id = sess["user_id"]
    req_id = str(uuid.uuid4())   # 一次请求的幂等基准

    # ③ 预占：按预估上限占住积分，额度不足直接拒（防并发透支由平台行锁保证，不要自己查余额判断）
    try:
        hold = platform.reserve(
            entitlement_id, user_id, config.RESERVE_ESTIMATE, idempotency_key=f"{req_id}:reserve"
        )
    except platform.PlatformError as e:
        if e.is_quota_insufficient:
            return JSONResponse({"error": "积分不足，请充值后再试"}, status_code=402)
        return JSONResponse({"error": f"预占失败：{e.message}"}, status_code=502)

    hold_id = hold["hold_id"]
    try:
        # 业务：生成文案。actual_cost = 实际消耗，这里用输出长度折算（封顶到预占额）
        text = _generate_copy(topic)
        actual_cost = min(config.RESERVE_ESTIMATE, max(1, len(text) // 10))
    except Exception as ex:
        # 业务异常：释放预占，积分不被扣
        platform.release(hold_id, idempotency_key=f"{req_id}:release")
        return JSONResponse({"error": f"生成失败，已释放预占：{ex}"}, status_code=500)

    # ④ 结算：按实际消耗多退少补
    try:
        settled = platform.settle(hold_id, actual_cost, idempotency_key=f"{req_id}:settle")
    except platform.PlatformError as e:
        return JSONResponse({"error": f"结算失败：{e.message}"}, status_code=502)

    return {
        "result": text,
        "reserved": config.RESERVE_ESTIMATE,        # 预占额
        "actual_cost": actual_cost,                 # 实际扣的积分
        "quota_used": settled.get("quota_used"),    # 该权益累计已用
        "available": settled.get("available"),      # 结算后可用余额
    }

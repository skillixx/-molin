"""平台对接封装（按量付费 postpaid）。

把"调平台接口"这件事收敛在这一个文件，应用业务代码只调这里的函数。
你接其它平台时，照着改这一层即可。

平台统一响应信封：{ "code": 0, "message": "...", "data": {...} }，code==0 为成功。
内部接口（/api/internal/*）一律带 X-Internal-Token 头。
"""
from __future__ import annotations

import httpx

import config

# —— 业务错误码（与平台约定）——
CODE_OK = 0
CODE_PARAM_OR_NO_RULE = 40000   # 参数错误 / 无匹配计费规则（靠 message 区分）
CODE_AUTH_FAILED = 40003        # 鉴权失败（token / IP）
CODE_WALLET_INSUFFICIENT = 60001  # 钱包余额不足
MSG_NO_BILLING_RULE = "未找到匹配的计费规则"


class PlatformError(Exception):
    """平台返回的业务错误，携带 code/message，便于上层按错误码分类处理。"""

    def __init__(self, code: int, message: str, http_status: int = 0):
        super().__init__(f"[{code}] {message}")
        self.code = code
        self.message = message
        self.http_status = http_status

    @property
    def is_no_billing_rule(self) -> bool:
        """无匹配计费规则：应静默跳过（说明该商品未配该类计费），不要当错误重试。"""
        return self.code == CODE_PARAM_OR_NO_RULE and MSG_NO_BILLING_RULE in self.message

    @property
    def is_wallet_insufficient(self) -> bool:
        return self.code == CODE_WALLET_INSUFFICIENT


def _internal_headers() -> dict:
    return {"X-Internal-Token": config.INTERNAL_API_TOKEN}


def _unwrap(resp: httpx.Response) -> dict:
    """解开 {code,message,data} 信封：code==0 返回 data，否则抛 PlatformError。"""
    try:
        body = resp.json()
    except Exception:
        raise PlatformError(-1, f"平台返回非 JSON（HTTP {resp.status_code}）：{resp.text[:200]}", resp.status_code)
    code = body.get("code", -1)
    if code != CODE_OK:
        raise PlatformError(code, body.get("message", "未知错误"), resp.status_code)
    return body.get("data") or {}


def verify_ticket(launch_ticket: str) -> dict:
    """用 SSO 一次性票据换取用户身份（票据一次性、60s 过期、防重放）。

    POST /api/internal/app-launch/verify
    返回：{ "user_id": int, "app_id": int, "product_id": int }
    票据无效/过期/已用 → PlatformError(code=40003)。
    """
    url = f"{config.PLATFORM_BASE_URL}/api/internal/app-launch/verify"
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json={"launch_ticket": launch_ticket})
    return _unwrap(resp)


def report_usage(
    *,
    event_id: str,
    user_id: int,
    product_id: int,
    usage_amount: str,
    idempotency_key: str,
    occurred_at: str,
) -> dict:
    """上报一次用量，平台据计费规则从用户钱包扣费（postpaid）。

    POST /api/internal/product-usage-events
    返回：{ consumption_record_id, amount, idempotency_key, wallet_transaction_id }
    - amount 为本次实扣金额（落在免费额度内时为 "0" 且不扣费）。
    - 无匹配规则 → PlatformError.is_no_billing_rule（静默跳过）。
    - 余额不足 → PlatformError.is_wallet_insufficient（提示用户充值）。
    """
    url = f"{config.PLATFORM_BASE_URL}/api/internal/product-usage-events"
    body = {
        "event_id": event_id,
        "user_id": user_id,
        "product_id": product_id,
        "usage_type": config.USAGE_TYPE,         # 必须与平台计费规则一字不差
        "usage_amount": usage_amount,            # 字符串 decimal，如 "1"、"25"
        "usage_unit": config.USAGE_UNIT,
        "occurred_at": occurred_at,              # RFC3339
        "idempotency_key": idempotency_key,      # 全局唯一，重复上报不二次扣费
    }
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json=body)
    return _unwrap(resp)

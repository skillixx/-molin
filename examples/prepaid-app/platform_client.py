"""平台对接封装（预付/扣积分 prepaid）。

预付场景：用户先买"积分套餐"（开通生成 user_entitlements 额度），使用时不扣钱包、扣积分额度。
额度接口在 asset 模块，全部走内部接口（X-Internal-Token + IP 白名单）。

关键点：SSO 票据只换得 {user_id, app_id, product_id}，**没有 entitlement_id**。
应用先用 resolve_entitlement（按 user_id+product_id 解析），拿到 entitlement_id 再扣额度。

贵动作（如生成，事前不知道实际消耗）用「预占→结算/释放」防并发透支：
  reserve(预估上限) → 执行业务 → settle(实际, 多退少补) / release(失败回滚)
"""
from __future__ import annotations

import httpx

import config

CODE_OK = 0
CODE_PARAM = 40000
CODE_AUTH_FAILED = 40003           # 鉴权失败 / 权益不属于该用户
CODE_NOT_FOUND = 40400             # 权益不存在
CODE_QUOTA_INSUFFICIENT = 60005    # 额度不足 / 权益不可用


class PlatformError(Exception):
    def __init__(self, code: int, message: str, http_status: int = 0):
        super().__init__(f"[{code}] {message}")
        self.code = code
        self.message = message
        self.http_status = http_status

    @property
    def is_quota_insufficient(self) -> bool:
        return self.code == CODE_QUOTA_INSUFFICIENT


def _internal_headers() -> dict:
    return {"X-Internal-Token": config.INTERNAL_API_TOKEN}


def _unwrap(resp: httpx.Response) -> dict:
    try:
        body = resp.json()
    except Exception:
        raise PlatformError(-1, f"平台返回非 JSON（HTTP {resp.status_code}）：{resp.text[:200]}", resp.status_code)
    code = body.get("code", -1)
    if code != CODE_OK:
        raise PlatformError(code, body.get("message", "未知错误"), resp.status_code)
    return body.get("data") or {}


def verify_ticket(launch_ticket: str) -> dict:
    """SSO 一次性票据换身份。返回 {user_id, app_id, product_id}。"""
    url = f"{config.PLATFORM_BASE_URL}/api/internal/app-launch/verify"
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json={"launch_ticket": launch_ticket})
    return _unwrap(resp)


def resolve_entitlement(user_id: int, product_id: int) -> dict | None:
    """按 user_id + product_id 解析该用户在本商品下"可用"的积分权益。

    GET /api/internal/user-entitlements?user_id=&product_id=
    返回 data.entitlements: [{entitlement_id, quota_total, quota_used, quota_reserved,
                              remaining, status, expires_at, usable}]
    本函数挑第一个 usable=true 的返回；没有可用权益返回 None。
    """
    url = f"{config.PLATFORM_BASE_URL}/api/internal/user-entitlements"
    params = {"user_id": user_id, "product_id": product_id}
    with httpx.Client(timeout=5.0) as client:
        resp = client.get(url, headers=_internal_headers(), params=params)
    data = _unwrap(resp)
    for ent in data.get("entitlements", []):
        if ent.get("usable"):
            return ent
    return None


def reserve(entitlement_id: int, user_id: int, amount, idempotency_key: str) -> dict:
    """预占额度（转发前先占住预估上限，防并发透支）。

    返回 {hold_id, reserved, available, status}；额度不足 → PlatformError.is_quota_insufficient。
    """
    url = f"{config.PLATFORM_BASE_URL}/api/internal/entitlement-reserve"
    body = {
        "entitlement_id": entitlement_id,
        "user_id": user_id,
        "amount": str(amount),
        "idempotency_key": idempotency_key,
    }
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json=body)
    return _unwrap(resp)


def settle(hold_id: int, actual_amount, idempotency_key: str) -> dict:
    """结算预占（多退少补，actual≤预占额 计入 quota_used）。

    返回 {hold_id, status, settled_amount, quota_used, quota_reserved, available}。
    """
    url = f"{config.PLATFORM_BASE_URL}/api/internal/entitlement-settle"
    body = {"hold_id": hold_id, "actual_amount": str(actual_amount), "idempotency_key": idempotency_key}
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json=body)
    return _unwrap(resp)


def release(hold_id: int, idempotency_key: str) -> dict:
    """释放预占（失败/异常路径，不计 quota_used）。"""
    url = f"{config.PLATFORM_BASE_URL}/api/internal/entitlement-release"
    body = {"hold_id": hold_id, "idempotency_key": idempotency_key}
    with httpx.Client(timeout=5.0) as client:
        resp = client.post(url, headers=_internal_headers(), json=body)
    return _unwrap(resp)

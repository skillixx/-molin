#!/usr/bin/env python3
"""通过 Bifrost 对固定 OpenRouter 千问模型执行一次最小可用性探针。"""

import json
import os
import re
import sys
import urllib.error
import urllib.request


BIFROST_BASE_URL = "http://127.0.0.1:18080"
MODEL = "openrouter/qwen/qwen3.8-max"
CHAT_PATH = "/v1/chat/completions"
HEALTH_PATH = "/health"
MAX_RESPONSE_BYTES = 262_144
TOKEN_PATTERN = re.compile(r"^[A-Za-z0-9._~+/=-]{32,256}$")


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """禁止跟随重定向，避免请求离开固定 Bifrost 回环入口。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _fixed_result(reason):
    """只返回固定低敏分类，不携带上游正文、标识或凭据。"""
    return {
        "BIFROST_HEALTH": "FAILED",
        "CHAT_ATTEMPTED": "NO",
        "HTTP_SUCCESS": "NO",
        "RESPONSE_ID_PRESENT": "NO",
        "CONTENT_MATCH": "NO",
        "USAGE_PRESENT": "NO",
        "MODEL_AVAILABLE": "FAILED",
        "ERROR_CLASS": reason,
        "ZERO_RETRY": "YES",
    }


def probe_once(internal_token, opener=None):
    """先检查健康，再且仅再发送一次固定非流式 Chat。"""
    if not TOKEN_PATTERN.fullmatch(internal_token or ""):
        raise ValueError("internal_token_invalid")

    client = opener or urllib.request.build_opener(NoRedirectHandler())
    result = _fixed_result("not_started")

    health_request = urllib.request.Request(
        BIFROST_BASE_URL + HEALTH_PATH,
        method="GET",
        headers={"Accept": "application/json"},
    )
    try:
        with client.open(health_request, timeout=5) as response:
            if response.status != 200:
                result["ERROR_CLASS"] = "bifrost_health_failed"
                return result
            response.read(MAX_RESPONSE_BYTES)
    except (OSError, urllib.error.URLError, urllib.error.HTTPError):
        result["ERROR_CLASS"] = "bifrost_health_failed"
        return result
    result["BIFROST_HEALTH"] = "PASS"

    request_body = {
        "model": MODEL,
        "messages": [{"role": "user", "content": "只回复 OK"}],
        "temperature": 0,
        "max_tokens": 4,
        "n": 1,
        "stream": False,
    }
    chat_request = urllib.request.Request(
        BIFROST_BASE_URL + CHAT_PATH,
        data=json.dumps(request_body, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        method="POST",
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "Authorization": "Bearer " + internal_token,
        },
    )

    # 从这一行开始视为唯一一次真实请求已经尝试，后续任何失败都不得重试。
    result["CHAT_ATTEMPTED"] = "YES"
    try:
        with client.open(chat_request, timeout=60) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
            if response.status < 200 or response.status >= 300:
                result["ERROR_CLASS"] = "chat_http_failed"
                return result
            if len(raw) > MAX_RESPONSE_BYTES:
                result["ERROR_CLASS"] = "response_too_large"
                return result
    except urllib.error.HTTPError:
        result["ERROR_CLASS"] = "chat_http_failed"
        return result
    except (OSError, urllib.error.URLError):
        result["ERROR_CLASS"] = "chat_transport_failed"
        return result

    result["HTTP_SUCCESS"] = "YES"
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        result["ERROR_CLASS"] = "response_invalid"
        return result
    if not isinstance(payload, dict) or payload.get("error") or payload.get("is_bifrost_error"):
        result["ERROR_CLASS"] = "provider_rejected"
        return result

    response_id = payload.get("id")
    result["RESPONSE_ID_PRESENT"] = "YES" if isinstance(response_id, str) and response_id else "NO"
    choices = payload.get("choices")
    content = ""
    if isinstance(choices, list) and len(choices) == 1 and isinstance(choices[0], dict):
        message = choices[0].get("message")
        if isinstance(message, dict) and isinstance(message.get("content"), str):
            content = message["content"].strip().strip("。.!！ ").upper()
    result["CONTENT_MATCH"] = "YES" if content == "OK" else "NO"

    usage = payload.get("usage")
    usage_ok = isinstance(usage, dict) and all(
        isinstance(usage.get(key), int) and usage[key] >= 0
        for key in ("prompt_tokens", "completion_tokens", "total_tokens")
    )
    result["USAGE_PRESENT"] = "YES" if usage_ok else "NO"

    if (
        result["RESPONSE_ID_PRESENT"] == "YES"
        and result["CONTENT_MATCH"] == "YES"
        and result["USAGE_PRESENT"] == "YES"
    ):
        result["MODEL_AVAILABLE"] = "PASS"
        result["ERROR_CLASS"] = "none"
    else:
        result["ERROR_CLASS"] = "response_contract_failed"
    return result


def main(argv=None, environ=None, output=None):
    """要求双重显式开关，防止查看帮助或误运行时产生真实费用。"""
    args = list(sys.argv[1:] if argv is None else argv)
    env = os.environ if environ is None else environ
    stream = sys.stdout if output is None else output
    authorized = args == ["--execute-once"] and env.get("G8_ALLOW_REAL_MODEL_TEST") == "YES"
    if not authorized:
        print("EXECUTION_AUTHORIZED=NO", file=stream)
        print("USAGE=G8_ALLOW_REAL_MODEL_TEST=YES python3 probe-openrouter-bifrost-model-once.py --execute-once", file=stream)
        return 2

    token = env.get("BIFROST_INTERNAL_TOKEN", "")
    try:
        result = probe_once(token)
    except ValueError:
        result = _fixed_result("internal_token_invalid")
    for key in (
        "BIFROST_HEALTH",
        "CHAT_ATTEMPTED",
        "HTTP_SUCCESS",
        "RESPONSE_ID_PRESENT",
        "CONTENT_MATCH",
        "USAGE_PRESENT",
        "MODEL_AVAILABLE",
        "ERROR_CLASS",
        "ZERO_RETRY",
    ):
        print(f"{key}={result[key]}", file=stream)
    return 0 if result["MODEL_AVAILABLE"] == "PASS" else 2


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env bash

set -euo pipefail

gateway_url="${BIFROST_GATEWAY_URL:-http://127.0.0.1:18080}"
bailian_body="$(mktemp)"
openrouter_body="$(mktemp)"
trap 'rm -f "${bailian_body}" "${openrouter_body}"' EXIT

# 通过负载均衡入口验证百炼自定义 Provider 的模型解析、转发和 usage 返回。
http_code="$(curl -sS \
  --connect-timeout 5 \
  --max-time 45 \
  -o "${bailian_body}" \
  -w '%{http_code}' \
  "${gateway_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{"model":"bailian/qwen-turbo","messages":[{"role":"user","content":"只回答OK"}],"max_tokens":1}')"

echo "BIFROST_HTTP=${http_code}"
if command -v jq >/dev/null 2>&1; then
  if [[ "${http_code}" == "200" ]]; then
    jq -r '"BIFROST_BAILIAN_MODEL=" + (.model // "unknown"), "BIFROST_BAILIAN_TOTAL_TOKENS=" + ((.usage.total_tokens // 0) | tostring)' "${bailian_body}"
  else
    jq -r '(.error.message // .message // "未返回错误信息") | "BIFROST_BAILIAN_ERROR=" + .' "${bailian_body}"
  fi
fi

[[ "${http_code}" == "200" ]]

# 使用已验证可访问的免费模型检查 OpenRouter 内置 Provider 路由。
openrouter_code="$(curl -sS \
  --connect-timeout 5 \
  --max-time 45 \
  -o "${openrouter_body}" \
  -w '%{http_code}' \
  "${gateway_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{"model":"openrouter/cohere/north-mini-code:free","messages":[{"role":"user","content":"Reply OK"}],"max_tokens":1}')"

echo "BIFROST_OPENROUTER_HTTP=${openrouter_code}"
if command -v jq >/dev/null 2>&1; then
  if [[ "${openrouter_code}" == "200" ]]; then
    jq -r '"BIFROST_OPENROUTER_MODEL=" + (.model // "unknown"), "BIFROST_OPENROUTER_TOTAL_TOKENS=" + ((.usage.total_tokens // 0) | tostring)' "${openrouter_body}"
  else
    jq -r '(.error.message // .message // "未返回错误信息") | "BIFROST_OPENROUTER_ERROR=" + .' "${openrouter_body}"
  fi
fi

[[ "${openrouter_code}" == "200" ]]

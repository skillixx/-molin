#!/usr/bin/env bash

set -euo pipefail

gateway_url="${BIFROST_GATEWAY_URL:-http://127.0.0.1:18080}"

if [[ "${AI_GATEWAY_BIFROST_CHECK_APPROVED:-NO}" != "YES" ]]; then
  echo "BIFROST_CHECK=APPROVAL_REQUIRED paid_model_calls=2 max_output_tokens_each=1"
	exit 3
fi

: "${BIFROST_INTERNAL_TOKEN:?请通过安全环境变量注入 BIFROST_INTERNAL_TOKEN}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
bailian_body="${tmp_dir}/bailian.body"
openrouter_body="${tmp_dir}/openrouter.body"
gateway_headers="${tmp_dir}/gateway.headers"
printf 'Authorization: Bearer %s\n' "${BIFROST_INTERNAL_TOKEN}" >"${gateway_headers}"
chmod 600 "${gateway_headers}"

# 内部入口必须拒绝缺失、错误和重复 Authorization，防止绕过墨灵鉴权与资产门禁。
for auth_mode in missing wrong duplicate; do
  auth_args=()
  case "${auth_mode}" in
    wrong) auth_args=(-H 'Authorization: Bearer invalid-internal-token') ;;
    duplicate) auth_args=(-H @"${gateway_headers}" -H 'Authorization: Bearer duplicate-token') ;;
  esac
  auth_code="$(curl -sS -o /dev/null -w '%{http_code}' "${gateway_url}/v1/chat/completions" \
    -H 'Content-Type: application/json' "${auth_args[@]}" -d '{"model":"bailian/qwen-turbo","messages":[]}')"
  if [[ "${auth_mode}" == "duplicate" ]]; then
    [[ "${auth_code}" == "400" || "${auth_code}" == "401" ]]
  else
    [[ "${auth_code}" == "401" ]]
  fi
done

# 通过负载均衡入口验证百炼自定义 Provider 的模型解析、转发和 usage 返回。
http_code="$(curl -sS \
  --connect-timeout 5 \
  --max-time 45 \
  -o "${bailian_body}" \
  -w '%{http_code}' \
  "${gateway_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -H @"${gateway_headers}" \
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
  -H @"${gateway_headers}" \
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

#!/usr/bin/env bash

set -euo pipefail

if [[ "${AI_GATEWAY_UPSTREAM_CHECK_APPROVED:-NO}" != "YES" ]]; then
  echo "UPSTREAM_CHECK=APPROVAL_REQUIRED network_requests=2 paid_model_calls=1 max_output_tokens=1"
  exit 3
fi

# 仅从服务器受限环境文件读取密钥，避免密钥出现在命令参数和日志中。
env_file="${1:-/home/pc/molin/secrets/bifrost.env}"
if [[ ! -f "${env_file}" ]]; then
  echo "ENV_FILE=MISSING"
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "${env_file}"
set +a

: "${BAILIAN_API_KEY:?BAILIAN_API_KEY 未配置}"
: "${OPENROUTER_API_KEY:?OPENROUTER_API_KEY 未配置}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
openrouter_body="${tmp_dir}/openrouter.body"
bailian_body="${tmp_dir}/bailian.body"
openrouter_headers="${tmp_dir}/openrouter.headers"
bailian_headers="${tmp_dir}/bailian.headers"
printf 'Authorization: Bearer %s\n' "${OPENROUTER_API_KEY}" >"${openrouter_headers}"
printf 'Authorization: Bearer %s\n' "${BAILIAN_API_KEY}" >"${bailian_headers}"
chmod 600 "${openrouter_headers}" "${bailian_headers}"

# OpenRouter 的密钥信息接口只验证鉴权，不调用计费模型。
openrouter_code="$(curl -sS \
  --connect-timeout 8 \
  --max-time 25 \
  -o "${openrouter_body}" \
  -w '%{http_code}' \
  'https://openrouter.ai/api/v1/auth/key' \
  -H @"${openrouter_headers}")"
echo "OPENROUTER_HTTP=${openrouter_code}"

if [[ "${openrouter_code}" != "200" ]]; then
  if command -v jq >/dev/null 2>&1; then
    jq -r '(.error.message // .message // "未返回错误信息") | "OPENROUTER_ERROR=" + .' "${openrouter_body}"
  fi
fi

# 百炼使用最小输出请求，同时验证鉴权、模型权限和兼容接口可用性。
bailian_code="$(curl -sS \
  --connect-timeout 8 \
  --max-time 40 \
  -o "${bailian_body}" \
  -w '%{http_code}' \
  'https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions' \
  -H @"${bailian_headers}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen-turbo","messages":[{"role":"user","content":"只回答OK"}],"max_tokens":1}')"
echo "BAILIAN_HTTP=${bailian_code}"

if [[ "${bailian_code}" == "200" ]]; then
  if command -v jq >/dev/null 2>&1; then
    jq -r '"BAILIAN_MODEL=" + (.model // "unknown"), "BAILIAN_TOTAL_TOKENS=" + ((.usage.total_tokens // 0) | tostring)' "${bailian_body}"
  fi
else
  if command -v jq >/dev/null 2>&1; then
    jq -r '(.error.message // .message // "未返回错误信息") | "BAILIAN_ERROR=" + .' "${bailian_body}"
  fi
fi

if [[ "${openrouter_code}" != "200" || "${bailian_code}" != "200" ]]; then
  exit 2
fi

echo "UPSTREAM_CHECK=OK"

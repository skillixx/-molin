#!/usr/bin/env bash

set -Eeuo pipefail

gateway_url="${BIFROST_GATEWAY_URL:-http://127.0.0.1:18080}"

# 当前测试入口可能仍是旧 Nginx；为防鉴权探测穿透到真实模型，任何网络请求前都必须显式批准。
if [[ "${AI_GATEWAY_G1_POC_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_POC=APPROVAL_REQUIRED network_requests=7 paid_model_calls=4 max_output_tokens_each=1"
	exit 3
fi

: "${BIFROST_INTERNAL_TOKEN:?请通过测试服务器受限环境变量注入 BIFROST_INTERNAL_TOKEN}"
command -v curl >/dev/null 2>&1 || { echo "G1_POC=FAILED reason=curl_missing"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "G1_POC=FAILED reason=jq_missing"; exit 2; }

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
gateway_headers="${tmp_dir}/gateway.headers"
printf 'Authorization: Bearer %s\n' "${BIFROST_INTERNAL_TOKEN}" >"${gateway_headers}"
chmod 600 "${gateway_headers}"

# 内部入口鉴权检查使用无效请求体，但仍只能在上方批准门通过后执行，防止旧入口意外穿透。
for auth_mode in missing wrong duplicate; do
  auth_args=()
  case "${auth_mode}" in
    wrong) auth_args=(-H 'Authorization: Bearer invalid-internal-token') ;;
    duplicate) auth_args=(-H @"${gateway_headers}" -H 'Authorization: Bearer duplicate-token') ;;
  esac
  auth_code="$(curl -sS --connect-timeout 5 --max-time 10 -o /dev/null -w '%{http_code}' \
    "${gateway_url}/v1/chat/completions" -H 'Content-Type: application/json' "${auth_args[@]}" \
    -d '{"model":"bailian/qwen-turbo","messages":[]}')"
  expected_auth_codes="401"
  if [[ "${auth_mode}" == "duplicate" ]]; then
    expected_auth_codes="400|401"
  fi
  if [[ ! "${auth_code}" =~ ^(${expected_auth_codes})$ ]]; then
    echo "G1_POC=FAILED reason=internal_auth_${auth_mode} http=${auth_code}"
    exit 2
  fi
  echo "G1_INTERNAL_AUTH_MODE=${auth_mode} http=${auth_code}"
done
echo "G1_INTERNAL_AUTH=PASS"

run_non_stream() {
  local label="$1"
  local model="$2"
  local body_file="${tmp_dir}/${label}-json.body"
  local metrics
  metrics="$(curl -sS --connect-timeout 5 --max-time 60 -o "${body_file}" -w '%{http_code} %{time_starttransfer} %{time_total}' \
    "${gateway_url}/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -H @"${gateway_headers}" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply OK\"}],\"max_tokens\":1}")"
  read -r http_code ttft total_time <<<"${metrics}"
  if [[ "${http_code}" != "200" ]] || ! jq -e '
    (.usage | type == "object") and
    (.usage.prompt_tokens | type == "number") and
    (.usage.completion_tokens | type == "number") and
    (.usage.total_tokens | type == "number")
  ' "${body_file}" >/dev/null; then
    echo "G1_POC=FAILED provider=${label} mode=json http=${http_code}"
    exit 2
  fi
  total_tokens="$(jq -r '.usage.total_tokens' "${body_file}")"
  echo "G1_PROVIDER=${label} mode=json http=200 total_tokens=${total_tokens} ttft_seconds=${ttft} total_seconds=${total_time}"
}

run_stream() {
  local label="$1"
  local model="$2"
  local body_file="${tmp_dir}/${label}-sse.body"
  local header_file="${tmp_dir}/${label}-sse.headers"
  local metrics
  metrics="$(curl -sS -N --connect-timeout 5 --max-time 90 -D "${header_file}" -o "${body_file}" -w '%{http_code} %{time_starttransfer} %{time_total}' \
    "${gateway_url}/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -H @"${gateway_headers}" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply OK\"}],\"max_tokens\":1,\"stream\":true,\"stream_options\":{\"include_usage\":true}}")"
  read -r http_code ttft total_time <<<"${metrics}"
  tr -d '\r' <"${body_file}" >"${tmp_dir}/${label}-sse.normalized"
  body_file="${tmp_dir}/${label}-sse.normalized"
  if [[ "${http_code}" != "200" ]] || ! grep -Eiq '^content-type:[[:space:]]*text/event-stream' "${header_file}" || ! grep -Fxq 'data: [DONE]' "${body_file}"; then
    echo "G1_POC=FAILED provider=${label} mode=sse http=${http_code} done=false"
    exit 2
  fi

  # 只剥离 SSE data 前缀，JSON 内容始终交给 jq 解析，禁止用字符串拼接判断 Usage。
  sed -n 's/^data: //p' "${body_file}" | grep -Fvx '[DONE]' >"${tmp_dir}/${label}-events.jsonl"
  if ! jq -s -e '
    map(select(.usage != null)) as $usage_events |
    ($usage_events | length) >= 1 and
    ($usage_events[-1].usage.prompt_tokens | type == "number") and
    ($usage_events[-1].usage.completion_tokens | type == "number")
  ' "${tmp_dir}/${label}-events.jsonl" >/dev/null; then
    echo "G1_POC=FAILED provider=${label} mode=sse http=200 usage=false"
    exit 2
  fi
  total_tokens="$(jq -s -r 'map(select(.usage != null))[-1].usage.total_tokens' "${tmp_dir}/${label}-events.jsonl")"
  echo "G1_PROVIDER=${label} mode=sse http=200 total_tokens=${total_tokens} ttft_seconds=${ttft} total_seconds=${total_time}"
}

run_non_stream "bailian" "bailian/qwen-turbo"
run_stream "bailian" "bailian/qwen-turbo"
run_non_stream "openrouter" "openrouter/cohere/north-mini-code:free"
run_stream "openrouter" "openrouter/cohere/north-mini-code:free"

echo "G1_POC=PASS providers=2 paid_calls=4 max_output_tokens_each=1"

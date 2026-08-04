#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G1_FAILOVER_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_FAILOVER=APPROVAL_REQUIRED node_stops=2 paid_model_calls=2 max_output_tokens_each=1"
  exit 3
fi

gateway_url="${BIFROST_GATEWAY_URL:-http://127.0.0.1:18080}"
: "${BIFROST_INTERNAL_TOKEN:?请通过测试服务器受限环境变量注入 Bifrost 内部 Token}"
for command_name in docker curl jq; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "G1_FAILOVER=FAILED reason=command_missing command=${command_name}"; exit 2; }
done

tmp_dir="$(mktemp -d)"
gateway_headers="${tmp_dir}/gateway.headers"
printf 'Authorization: Bearer %s\n' "${BIFROST_INTERNAL_TOKEN}" >"${gateway_headers}"
chmod 600 "${gateway_headers}"

recover_nodes() {
  for node in bifrost-1 bifrost-2; do
    if [[ "$(docker inspect --format '{{.State.Running}}' "${node}" 2>/dev/null || true)" != "true" ]]; then
      docker start "${node}" >/dev/null || true
    fi
  done
  rm -f "${tmp_dir}/gateway.headers" "${tmp_dir}/bifrost-1.body" "${tmp_dir}/bifrost-2.body"
  rmdir "${tmp_dir}" 2>/dev/null || true
}
trap recover_nodes EXIT

wait_healthy() {
  local node="$1"
  for _ in $(seq 1 60); do
    if [[ "$(docker inspect --format '{{.State.Health.Status}}' "${node}" 2>/dev/null || true)" == "healthy" ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

for node in bifrost-1 bifrost-2; do
  wait_healthy "${node}"
done

for node in bifrost-1 bifrost-2; do
  body_file="${tmp_dir}/${node}.body"
  docker stop "${node}" >/dev/null
  health_code="$(curl -sS --connect-timeout 5 --max-time 15 -o /dev/null -w '%{http_code}' "${gateway_url}/health")"
  model_code="$(curl -sS --connect-timeout 5 --max-time 60 -o "${body_file}" -w '%{http_code}' \
    "${gateway_url}/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -H @"${gateway_headers}" \
    -d '{"model":"bailian/qwen-turbo","messages":[{"role":"user","content":"Reply OK"}],"max_tokens":1}')"
  if [[ "${health_code}" != "200" || "${model_code}" != "200" ]] || ! jq -e '(.choices | length) > 0 and (.usage.total_tokens | type == "number")' "${body_file}" >/dev/null; then
    echo "G1_FAILOVER=FAILED node=${node} health=${health_code} model=${model_code}"
    exit 2
  fi
  docker start "${node}" >/dev/null
  wait_healthy "${node}"
  echo "G1_FAILOVER_NODE=${node} health=200 model=200 recovered=healthy"
done

echo "G1_FAILOVER=PASS node_stops=2 paid_model_calls=2"

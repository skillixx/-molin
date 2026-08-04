#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G1_CONTROLLED_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_CONTROLLED_LATENCY=APPROVAL_REQUIRED target=test_linux restart_nodes=true"
  exit 3
fi

expected_host="${AI_GATEWAY_G1_EXPECTED_HOST:-pc-Z790-UD-AX}"
if [[ "$(hostname)" != "${expected_host}" ]]; then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=unexpected_host"
  exit 2
fi

: "${BIFROST_INTERNAL_TOKEN:?请通过测试服务器受限环境变量注入 Bifrost 内部 Token}"
: "${G1_CONTROLLED_EVIDENCE_FILE:?请指定本轮全新脱敏 TSV 证据文件的绝对路径}"
: "${G1_EXPECTED_CONFIG_SHA256:?请配置批准测试时冻结的正式配置 SHA256}"

config_file="${BIFROST_CONFIG_FILE:-/home/pc/molin/bifrost/config.json}"
mock_source="${G1_MOCK_SOURCE:-/home/pc/molin/bifrost/g1-staging/fixed-openai-upstream/main.go}"
benchmark_script="${G1_BENCHMARK_SCRIPT:-/home/pc/molin/bifrost/g1-staging/scripts/run-bifrost-g1-benchmark.sh}"
mock_port="${G1_MOCK_PORT:-19090}"
mock_container="bifrost-g1-mock"
warmup_groups="${G1_CONTROLLED_WARMUP_GROUPS:-5}"
if [[ ! "${warmup_groups}" =~ ^[0-9]+$ ]] || (( warmup_groups < 1 || warmup_groups > 20 )); then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=invalid_warmup_groups expected=1..20"
  exit 2
fi

for command_name in curl docker jq sha256sum ss; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "G1_CONTROLLED_LATENCY=FAILED reason=command_missing command=${command_name}"
    exit 2
  }
done
[[ -f "${config_file}" && ! -L "${config_file}" ]] || { echo "G1_CONTROLLED_LATENCY=FAILED reason=config_invalid"; exit 2; }
[[ -f "${mock_source}" && -f "${benchmark_script}" ]] || { echo "G1_CONTROLLED_LATENCY=FAILED reason=script_missing"; exit 2; }
if docker container inspect "${mock_container}" >/dev/null 2>&1; then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=mock_container_exists"
  exit 2
fi
if ss -H -ltn "sport = :${mock_port}" | grep -q .; then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=mock_port_in_use"
  exit 2
fi

work_dir="$(mktemp -d)"
backup_file="${work_dir}/config.json.backup"
candidate_file="${work_dir}/config.json.candidate"
mock_binary="${work_dir}/g1-fixed-upstream"
preflight_body="${work_dir}/preflight.json"
preflight_headers="${work_dir}/preflight.headers"
cp --preserve=mode,timestamps "${config_file}" "${backup_file}"
before_sha="$(sha256sum "${config_file}" | awk '{print $1}')"
if [[ "${before_sha}" != "${G1_EXPECTED_CONFIG_SHA256}" ]]; then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=unexpected_config_sha"
  rm -f "${backup_file}" "${candidate_file}" "${mock_binary}" "${preflight_body}" "${preflight_headers}"
  rmdir "${work_dir}" 2>/dev/null || true
  exit 2
fi
config_changed=0

wait_nodes_healthy() {
  local attempt node health
  # Bifrost 插件初始化和容器健康检查在测试机上可能接近一分钟。
  for attempt in $(seq 1 90); do
    for node in bifrost-1 bifrost-2; do
      health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${node}" 2>/dev/null || true)"
      [[ "${health}" == "healthy" || "${health}" == "running" ]] || break
    done
    [[ "${node:-}" == "bifrost-2" && ( "${health}" == "healthy" || "${health}" == "running" ) ]] && return 0
    sleep 1
  done
  return 1
}

cleanup() {
  local exit_code="$?"
  local restore_ok=1 restart_ok=1 rollback_healthy=1 mock_removed=1 cleanup_files=1
  trap - EXIT INT TERM
  if (( config_changed == 1 )); then
    if cp --preserve=mode,timestamps "${backup_file}" "${config_file}"; then
      docker restart bifrost-1 >/dev/null 2>&1 || restart_ok=0
      docker restart bifrost-2 >/dev/null 2>&1 || restart_ok=0
      wait_nodes_healthy || rollback_healthy=0
    else
      restore_ok=0
      restart_ok=0
      rollback_healthy=0
    fi
  fi
  if docker container inspect "${mock_container}" >/dev/null 2>&1; then
    if [[ "$(docker inspect --format '{{index .Config.Labels "molin.g1-controlled"}}' "${mock_container}" 2>/dev/null || true)" == "true" ]]; then
      docker rm -f "${mock_container}" >/dev/null 2>&1 || mock_removed=0
    else
      mock_removed=0
    fi
    docker container inspect "${mock_container}" >/dev/null 2>&1 && mock_removed=0
  fi
  after_sha="$(sha256sum "${config_file}" 2>/dev/null | awk '{print $1}' || true)"
  rm -f "${backup_file}" "${candidate_file}" "${mock_binary}" "${preflight_body}" "${preflight_headers}" || cleanup_files=0
  for cleanup_file in "${backup_file}" "${candidate_file}" "${mock_binary}" "${preflight_body}" "${preflight_headers}"; do
    [[ ! -e "${cleanup_file}" ]] || cleanup_files=0
  done
  rmdir "${work_dir}" 2>/dev/null || cleanup_files=0
  if [[ "${after_sha}" != "${before_sha}" || "${restore_ok}" != "1" || "${restart_ok}" != "1" || "${rollback_healthy}" != "1" || "${mock_removed}" != "1" || "${cleanup_files}" != "1" ]]; then
    echo "G1_CONTROLLED_ROLLBACK=FAILED before_sha=${before_sha} after_sha=${after_sha} restore_ok=${restore_ok} restart_ok=${restart_ok} nodes_healthy=${rollback_healthy} mock_removed=${mock_removed} cleanup_files=${cleanup_files}"
    exit 2
  fi
  echo "G1_CONTROLLED_ROLLBACK=PASS config_sha256=${after_sha}"
  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

docker run --rm \
  --env CGO_ENABLED=0 \
  --volume "$(dirname "${mock_source}"):/src:ro" \
  --volume "${work_dir}:/out" \
  golang:1.25 go build -trimpath -o /out/g1-fixed-upstream /src/main.go
docker run --detach \
  --name "${mock_container}" \
  --label molin.g1-controlled=true \
  --network bifrost-net \
  --publish "127.0.0.1:${mock_port}:8080" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --pids-limit 64 \
  --memory 64m \
  --volume "${mock_binary}:/g1-fixed-upstream:ro" \
  nginx:1.27-alpine /g1-fixed-upstream --host 0.0.0.0 --port 8080 >/dev/null
for _ in $(seq 1 20); do
  curl -fsS --max-time 1 "http://127.0.0.1:${mock_port}/health" >/dev/null && break
  sleep 0.2
done
curl -fsS --max-time 1 "http://127.0.0.1:${mock_port}/health" >/dev/null || {
  docker logs "${mock_container}" 2>&1 | tail -20
  echo "G1_CONTROLLED_LATENCY=FAILED reason=mock_unhealthy"
  exit 2
}

# 临时 Provider 只指向本机 Docker 网桥，不携带真实密钥，也不允许重试。
jq --arg base_url "http://bifrost-g1-mock:8080" '
  .providers.g1mock = {
    "keys": [{"name":"g1-local-only","value":"g1-local-only","weight":1,"models":["g1-fixed"]}],
    "network_config": {"base_url":$base_url,"default_request_timeout_in_seconds":5,"max_retries":0,"allow_private_network":true},
    "custom_provider_config": {
      "base_provider_type":"openai",
      "allowed_requests":{"chat_completion":true,"chat_completion_stream":true},
      "request_path_overrides":{"chat_completion":"/v1/chat/completions","chat_completion_stream":"/v1/chat/completions"}
    }
  }
' "${config_file}" >"${candidate_file}"
jq -e . "${candidate_file}" >/dev/null
config_changed=1
cp --preserve=mode,timestamps "${candidate_file}" "${config_file}"
docker restart bifrost-1 >/dev/null
docker restart bifrost-2 >/dev/null
wait_nodes_healthy || { echo "G1_CONTROLLED_LATENCY=FAILED reason=bifrost_unhealthy"; exit 2; }

printf 'Authorization: Bearer %s\n' "${BIFROST_INTERNAL_TOKEN}" >"${preflight_headers}"
chmod 600 "${preflight_headers}"
preflight_code="$(curl -sS --max-time 5 -o "${preflight_body}" -w '%{http_code}' \
  http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' -H @"${preflight_headers}" \
  -d '{"model":"g1mock/g1-fixed","messages":[{"role":"user","content":"x"}],"max_tokens":1}')"
if [[ "${preflight_code}" != "200" ]]; then
  preflight_message="$(jq -r '.error.message // .message // "unknown"' "${preflight_body}" | tr '\n' ' ' | cut -c1-240)"
  mock_requests="$(curl -fsS --max-time 1 "http://127.0.0.1:${mock_port}/count" | jq -r '.requests')"
  echo "G1_CONTROLLED_LATENCY=FAILED reason=bifrost_preflight http=${preflight_code} mock_requests=${mock_requests} message=${preflight_message}"
  exit 2
fi

# 预热不计入正式 20 组样本，四条路径使用相同次数。
for _ in $(seq 1 "${warmup_groups}"); do
  curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:"${mock_port}"/v1/chat/completions \
    -H 'Content-Type: application/json' -d '{"model":"g1-fixed","messages":[{"role":"user","content":"x"}],"max_tokens":1}'
  curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:18080/v1/chat/completions \
    -H 'Content-Type: application/json' -H @"${preflight_headers}" \
    -d '{"model":"g1mock/g1-fixed","messages":[{"role":"user","content":"x"}],"max_tokens":1}'
  curl -fsS -N --max-time 5 -o /dev/null http://127.0.0.1:"${mock_port}"/v1/chat/completions \
    -H 'Content-Type: application/json' -d '{"model":"g1-fixed","messages":[{"role":"user","content":"x"}],"max_tokens":1,"stream":true}'
  curl -fsS -N --max-time 5 -o /dev/null http://127.0.0.1:18080/v1/chat/completions \
    -H 'Content-Type: application/json' -H @"${preflight_headers}" \
    -d '{"model":"g1mock/g1-fixed","messages":[{"role":"user","content":"x"}],"max_tokens":1,"stream":true}'
done
echo "G1_CONTROLLED_WARMUP=PASS groups=${warmup_groups} requests=$((warmup_groups * 4))"

set +e
AI_GATEWAY_G1_BENCHMARK_APPROVED=YES \
AI_GATEWAY_G1_PERF_SAMPLES=20 \
G1_BENCHMARK_MODE=controlled \
BIFROST_GATEWAY_URL=http://127.0.0.1:18080 \
G1_NATIVE_CHAT_URL="http://127.0.0.1:${mock_port}/v1/chat/completions" \
G1_NATIVE_API_KEY=g1-local-only \
G1_NATIVE_MODEL=g1-fixed \
G1_BIFROST_MODEL=g1mock/g1-fixed \
G1_BENCHMARK_EVIDENCE_FILE="${G1_CONTROLLED_EVIDENCE_FILE}" \
bash "${benchmark_script}"
benchmark_exit="$?"
set -e
echo "G1_CONTROLLED_EVIDENCE_FILE=${G1_CONTROLLED_EVIDENCE_FILE}"
if (( benchmark_exit != 0 )); then
  echo "G1_CONTROLLED_LATENCY=FAILED reason=benchmark_failed exit_code=${benchmark_exit}"
  exit "${benchmark_exit}"
fi
echo "G1_CONTROLLED_LATENCY=PASS"

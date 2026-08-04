#!/usr/bin/env bash

set -Eeuo pipefail

samples="${AI_GATEWAY_G1_PERF_SAMPLES:-20}"
benchmark_mode="${G1_BENCHMARK_MODE:-}"
if [[ ! "${samples}" =~ ^[0-9]+$ ]] || (( samples < 20 || samples > 100 || samples % 2 != 0 )); then
  echo "G1_BENCHMARK=FAILED reason=invalid_sample_count expected=even_number_between_20_and_100"
  exit 2
fi
if [[ "${benchmark_mode}" != "controlled" && "${benchmark_mode}" != "real_upstream_observation" ]]; then
  echo "G1_BENCHMARK=FAILED reason=invalid_mode expected=controlled_or_real_upstream_observation"
  exit 2
fi

# 20 组包含 Native/Bifrost 的普通和流式配对，共 80 次真实请求，必须单独批准。
if [[ "${AI_GATEWAY_G1_BENCHMARK_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_BENCHMARK=APPROVAL_REQUIRED samples=${samples} model_calls=$((samples * 4)) max_output_tokens_each=1"
  exit 3
fi

: "${BIFROST_GATEWAY_URL:?请配置测试环境 Bifrost 统一入口}"
: "${BIFROST_INTERNAL_TOKEN:?请通过测试服务器受限环境变量注入 Bifrost 内部 Token}"
: "${G1_NATIVE_CHAT_URL:?请配置同一上游的 Native Chat Completions 完整地址}"
: "${G1_NATIVE_API_KEY:?请通过测试服务器受限环境变量注入同一上游 SK}"
: "${G1_NATIVE_MODEL:?请配置 Native 上游模型代码}"
: "${G1_BIFROST_MODEL:?请配置 Bifrost Provider/模型代码}"
: "${G1_BENCHMARK_EVIDENCE_FILE:?请指定本轮全新脱敏 TSV 证据文件的绝对路径}"

if [[ "${G1_BENCHMARK_EVIDENCE_FILE}" != /* || -e "${G1_BENCHMARK_EVIDENCE_FILE}" || -L "${G1_BENCHMARK_EVIDENCE_FILE}" ]]; then
  echo "G1_BENCHMARK=FAILED reason=invalid_or_existing_evidence_file"
  exit 2
fi
evidence_dir="$(dirname "${G1_BENCHMARK_EVIDENCE_FILE}")"
[[ -d "${evidence_dir}" && ! -L "${evidence_dir}" ]] || { echo "G1_BENCHMARK=FAILED reason=evidence_directory_invalid"; exit 2; }
umask 077
: >"${G1_BENCHMARK_EVIDENCE_FILE}"
chmod 600 "${G1_BENCHMARK_EVIDENCE_FILE}"
printf 'mode\tsequence\torder\tnative_json_seconds\tbifrost_json_seconds\tjson_delta_ms\tnative_sse_ttft_seconds\tbifrost_sse_ttft_seconds\tsse_delta_ms\n' >"${G1_BENCHMARK_EVIDENCE_FILE}"

for command_name in curl jq sort awk grep sed tr; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "G1_BENCHMARK=FAILED reason=command_missing command=${command_name}"
    exit 2
  }
done

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
native_headers="${tmp_dir}/native.headers"
bifrost_headers="${tmp_dir}/bifrost.headers"
printf 'Authorization: Bearer %s\n' "${G1_NATIVE_API_KEY}" >"${native_headers}"
printf 'Authorization: Bearer %s\n' "${BIFROST_INTERNAL_TOKEN}" >"${bifrost_headers}"
chmod 600 "${native_headers}" "${bifrost_headers}"

run_json_sample() {
  local driver="$1" url="$2" header_file="$3" model="$4" sequence="$5"
  local body_file="${tmp_dir}/${driver}-json-${sequence}.body" metrics http_code ttft total_time
  if ! metrics="$(curl -sS --connect-timeout 5 --max-time 60 -o "${body_file}" -w '%{http_code} %{time_starttransfer} %{time_total}' \
    "${url}" -H 'Content-Type: application/json' -H @"${header_file}" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply OK\"}],\"max_tokens\":1}")"; then
    return 1
  fi
  read -r http_code ttft total_time <<<"${metrics}"
  [[ "${http_code}" == "200" ]] || return 1
  jq -e '(.choices | type == "array" and length > 0) and (.usage.prompt_tokens | type == "number") and (.usage.completion_tokens | type == "number")' "${body_file}" >/dev/null || return 1
  printf '%s\n' "${total_time}" >>"${tmp_dir}/${driver}-json.times"
  sample_time="${total_time}"
}

run_sse_sample() {
  local driver="$1" url="$2" header_file="$3" model="$4" sequence="$5"
  local body_file="${tmp_dir}/${driver}-sse-${sequence}.body" normalized_file="${tmp_dir}/${driver}-sse-${sequence}.normalized"
  local response_headers="${tmp_dir}/${driver}-sse-${sequence}.headers" metrics http_code ttft total_time
  if ! metrics="$(curl -sS -N --connect-timeout 5 --max-time 90 -D "${response_headers}" -o "${body_file}" -w '%{http_code} %{time_starttransfer} %{time_total}' \
    "${url}" -H 'Content-Type: application/json' -H @"${header_file}" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply OK\"}],\"max_tokens\":1,\"stream\":true,\"stream_options\":{\"include_usage\":true}}")"; then
    return 1
  fi
  read -r http_code ttft total_time <<<"${metrics}"
  tr -d '\r' <"${body_file}" >"${normalized_file}"
  [[ "${http_code}" == "200" ]] || return 1
  grep -Eiq '^content-type:[[:space:]]*text/event-stream' "${response_headers}" || return 1
  grep -Fxq 'data: [DONE]' "${normalized_file}" || return 1
  sed -n 's/^data: //p' "${normalized_file}" | grep -Fvx '[DONE]' >"${tmp_dir}/${driver}-sse-${sequence}.jsonl"
  jq -s -e 'map(select(.usage != null))[-1].usage.total_tokens | type == "number"' "${tmp_dir}/${driver}-sse-${sequence}.jsonl" >/dev/null || return 1
  printf '%s\n' "${ttft}" >>"${tmp_dir}/${driver}-sse.times"
  sample_time="${ttft}"
}

native_json_success=0
bifrost_json_success=0
native_sse_success=0
bifrost_sse_success=0
for ((sequence = 1; sequence <= samples; sequence++)); do
  native_json_time=""
  bifrost_json_time=""
  native_sse_time=""
  bifrost_sse_time=""
  if (( sequence % 2 == 1 )); then
    sample_order="native_first"
    if run_json_sample native "${G1_NATIVE_CHAT_URL}" "${native_headers}" "${G1_NATIVE_MODEL}" "${sequence}"; then native_json_time="${sample_time}"; ((native_json_success += 1)); fi
    if run_json_sample bifrost "${BIFROST_GATEWAY_URL%/}/v1/chat/completions" "${bifrost_headers}" "${G1_BIFROST_MODEL}" "${sequence}"; then bifrost_json_time="${sample_time}"; ((bifrost_json_success += 1)); fi
    if run_sse_sample native "${G1_NATIVE_CHAT_URL}" "${native_headers}" "${G1_NATIVE_MODEL}" "${sequence}"; then native_sse_time="${sample_time}"; ((native_sse_success += 1)); fi
    if run_sse_sample bifrost "${BIFROST_GATEWAY_URL%/}/v1/chat/completions" "${bifrost_headers}" "${G1_BIFROST_MODEL}" "${sequence}"; then bifrost_sse_time="${sample_time}"; ((bifrost_sse_success += 1)); fi
  else
    sample_order="bifrost_first"
    if run_json_sample bifrost "${BIFROST_GATEWAY_URL%/}/v1/chat/completions" "${bifrost_headers}" "${G1_BIFROST_MODEL}" "${sequence}"; then bifrost_json_time="${sample_time}"; ((bifrost_json_success += 1)); fi
    if run_json_sample native "${G1_NATIVE_CHAT_URL}" "${native_headers}" "${G1_NATIVE_MODEL}" "${sequence}"; then native_json_time="${sample_time}"; ((native_json_success += 1)); fi
    if run_sse_sample bifrost "${BIFROST_GATEWAY_URL%/}/v1/chat/completions" "${bifrost_headers}" "${G1_BIFROST_MODEL}" "${sequence}"; then bifrost_sse_time="${sample_time}"; ((bifrost_sse_success += 1)); fi
    if run_sse_sample native "${G1_NATIVE_CHAT_URL}" "${native_headers}" "${G1_NATIVE_MODEL}" "${sequence}"; then native_sse_time="${sample_time}"; ((native_sse_success += 1)); fi
  fi
  json_pair_delta="NA"
  sse_pair_delta="NA"
  if [[ -n "${native_json_time}" && -n "${bifrost_json_time}" ]]; then
    json_pair_delta="$(awk -v native="${native_json_time}" -v bifrost="${bifrost_json_time}" 'BEGIN { printf "%.3f", (bifrost - native) * 1000 }')"
    printf '%s\n' "${json_pair_delta}" >>"${tmp_dir}/json-paired-delta-ms.times"
  fi
  if [[ -n "${native_sse_time}" && -n "${bifrost_sse_time}" ]]; then
    sse_pair_delta="$(awk -v native="${native_sse_time}" -v bifrost="${bifrost_sse_time}" 'BEGIN { printf "%.3f", (bifrost - native) * 1000 }')"
    printf '%s\n' "${sse_pair_delta}" >>"${tmp_dir}/sse-paired-delta-ms.times"
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "${benchmark_mode}" "${sequence}" "${sample_order}" "${native_json_time:-NA}" "${bifrost_json_time:-NA}" "${json_pair_delta}" \
    "${native_sse_time:-NA}" "${bifrost_sse_time:-NA}" "${sse_pair_delta}" >>"${G1_BENCHMARK_EVIDENCE_FILE}"
done

if (( native_json_success != samples || bifrost_json_success != samples || native_sse_success != samples || bifrost_sse_success != samples )); then
  echo "G1_BENCHMARK=FAILED reason=request_failure native_json=${native_json_success}/${samples} bifrost_json=${bifrost_json_success}/${samples} native_sse=${native_sse_success}/${samples} bifrost_sse=${bifrost_sse_success}/${samples}"
  exit 2
fi

p95() {
  local file="$1"
  sort -n "${file}" | awk -v count="${samples}" 'NR == int((count * 95 + 99) / 100) { print; exit }'
}

native_json_p95="$(p95 "${tmp_dir}/native-json.times")"
bifrost_json_p95="$(p95 "${tmp_dir}/bifrost-json.times")"
native_sse_p95="$(p95 "${tmp_dir}/native-sse.times")"
bifrost_sse_p95="$(p95 "${tmp_dir}/bifrost-sse.times")"
json_delta_ms="$(p95 "${tmp_dir}/json-paired-delta-ms.times")"
sse_delta_ms="$(p95 "${tmp_dir}/sse-paired-delta-ms.times")"

echo "G1_BENCHMARK_RESULT mode=${benchmark_mode} samples=${samples} observed_success_rate_difference_pp=0 native_json_p95_seconds=${native_json_p95} bifrost_json_p95_seconds=${bifrost_json_p95} native_sse_ttft_p95_seconds=${native_sse_p95} bifrost_sse_ttft_p95_seconds=${bifrost_sse_p95} json_paired_delta_p95_ms=${json_delta_ms} sse_paired_delta_p95_ms=${sse_delta_ms}"
echo "G1_BENCHMARK_EVIDENCE_SHA256=$(sha256sum "${G1_BENCHMARK_EVIDENCE_FILE}" | awk '{print $1}')"
if [[ "${benchmark_mode}" == "real_upstream_observation" ]]; then
  echo "G1_BENCHMARK=OBSERVATION mode=${benchmark_mode} samples=${samples} model_calls=$((samples * 4))"
  exit 0
fi
if ! awk -v json_delta="${json_delta_ms}" -v sse_delta="${sse_delta_ms}" 'BEGIN { exit !(json_delta <= 20 && sse_delta <= 30) }'; then
  echo "G1_BENCHMARK=FAILED reason=latency_threshold json_limit_ms=20 sse_ttft_limit_ms=30"
  exit 2
fi

echo "G1_BENCHMARK=PASS mode=${benchmark_mode} samples=${samples} model_calls=$((samples * 4))"

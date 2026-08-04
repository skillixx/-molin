#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G1_DEPLOY_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_DEPLOY=APPROVAL_REQUIRED target=test_linux rolling_restart=true credential_change=true"
  exit 3
fi

expected_host="${AI_GATEWAY_G1_EXPECTED_HOST:-pc-Z790-UD-AX}"
if [[ "$(hostname)" != "${expected_host}" ]]; then
  echo "G1_DEPLOY=FAILED reason=unexpected_host"
  exit 2
fi

root_dir="/home/pc/molin"
bifrost_dir="${root_dir}/bifrost"
staging_dir="${bifrost_dir}/g1-staging"
env_file="${root_dir}/secrets/bifrost.env"
config_file="${bifrost_dir}/config.json"
nginx_file="${bifrost_dir}/nginx.conf"
backup_dir="${bifrost_dir}/backups/g1-deploy-$(date +%Y%m%dT%H%M%S)"

for required_file in "${staging_dir}/config.json" "${staging_dir}/nginx.conf.template" "${env_file}"; do
  [[ -f "${required_file}" ]] || { echo "G1_DEPLOY=FAILED reason=required_file_missing"; exit 2; }
done
for command_name in docker jq openssl envsubst curl; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "G1_DEPLOY=FAILED reason=command_missing command=${command_name}"; exit 2; }
done

mkdir -p -m 700 "${backup_dir}"
cp -a "${config_file}" "${backup_dir}/config.json"
cp -a "${nginx_file}" "${backup_dir}/nginx.conf"
cp -a "${env_file}" "${backup_dir}/bifrost.env"

recreate_node() {
  local node_number="$1" container_name="bifrost-${1}" data_dir="${bifrost_dir}/data/node-${1}"
  mkdir -p "${data_dir}"
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container stop "${container_name}" >/dev/null
    docker container rm "${container_name}" >/dev/null
  fi
  docker run -d \
    --name "${container_name}" \
    --restart unless-stopped \
    --network bifrost-net \
    --env-file "${env_file}" \
    -v "${data_dir}:/app/data" \
    -v "${config_file}:/app/data/config.json:ro" \
    maximhq/bifrost:v1.6.6 >/dev/null
}

wait_node_healthy() {
  local container_name="$1"
  for _ in $(seq 1 60); do
    if [[ "$(docker inspect --format '{{.State.Health.Status}}' "${container_name}" 2>/dev/null || true)" == "healthy" ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

recreate_lb() {
  if docker container inspect bifrost-lb >/dev/null 2>&1; then
    docker container stop bifrost-lb >/dev/null
    docker container rm bifrost-lb >/dev/null
  fi
  docker run -d \
    --name bifrost-lb \
    --restart unless-stopped \
    --network bifrost-net \
    -p 127.0.0.1:18080:8080 \
    -v "${nginx_file}:/etc/nginx/nginx.conf:ro" \
    nginx:1.27-alpine >/dev/null
}

rollback() {
  local exit_code=$?
  trap - ERR
  echo "G1_DEPLOY=ROLLBACK_STARTED"
  cp "${backup_dir}/bifrost.env" "${env_file}"
  cp "${backup_dir}/config.json" "${config_file}"
  cp "${backup_dir}/nginx.conf" "${nginx_file}"
  chmod 600 "${env_file}"
  recreate_node 1 || true
  wait_node_healthy bifrost-1 || true
  recreate_node 2 || true
  wait_node_healthy bifrost-2 || true
  recreate_lb || true
  echo "G1_DEPLOY=ROLLED_BACK"
  exit "${exit_code}"
}
trap rollback ERR

umask 077
token_count="$(grep -c '^BIFROST_INTERNAL_TOKEN=' "${env_file}" || true)"
if [[ "${token_count}" == "0" ]]; then
  printf 'BIFROST_INTERNAL_TOKEN=%s\n' "$(openssl rand -hex 32)" >>"${env_file}"
elif [[ "${token_count}" != "1" ]]; then
  echo "G1_DEPLOY=FAILED reason=internal_token_duplicate"
  false
fi
chmod 600 "${env_file}"

set -a
# shellcheck disable=SC1090
source "${env_file}"
set +a
printf '%s' "${BIFROST_INTERNAL_TOKEN}" | grep -Eq '^[A-Za-z0-9._~+/=-]{32,256}$'

install -m 640 "${staging_dir}/config.json" "${config_file}.new"
mv "${config_file}.new" "${config_file}"
install -m 600 "${staging_dir}/nginx.conf.template" "${bifrost_dir}/nginx.conf.template"
envsubst '${BIFROST_INTERNAL_TOKEN}' <"${bifrost_dir}/nginx.conf.template" >"${nginx_file}.new"
chmod 600 "${nginx_file}.new"
jq -e . "${config_file}" >/dev/null
docker run --rm --network bifrost-net \
  -v "${nginx_file}.new:/etc/nginx/nginx.conf:ro" \
  nginx:1.27-alpine nginx -t >/dev/null
mv "${nginx_file}.new" "${nginx_file}"

recreate_node 1
wait_node_healthy bifrost-1
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health)" == "200" ]]
recreate_node 2
wait_node_healthy bifrost-2
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health)" == "200" ]]
recreate_lb

for _ in $(seq 1 30); do
  if [[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health 2>/dev/null || true)" == "200" ]]; then
    break
  fi
  sleep 1
done
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health)" == "200" ]]

trap - ERR
echo "G1_DEPLOY=PASS"
echo "G1_DEPLOY_BACKUP=${backup_dir}"

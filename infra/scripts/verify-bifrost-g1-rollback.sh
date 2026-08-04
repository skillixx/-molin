#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G1_ROLLBACK_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_ROLLBACK=APPROVAL_REQUIRED config_restore=true container_recreate=true"
  exit 3
fi

: "${G1_ROLLBACK_SOURCE_DIR:?请指定本轮部署前的受控备份目录}"
source_dir="$(realpath "${G1_ROLLBACK_SOURCE_DIR}")"
case "${source_dir}" in
  /home/pc/molin/bifrost/backups/g1-deploy-*) ;;
  *) echo "G1_ROLLBACK=FAILED reason=invalid_backup_path"; exit 2 ;;
esac

root_dir="/home/pc/molin"
bifrost_dir="${root_dir}/bifrost"
env_file="${root_dir}/secrets/bifrost.env"
config_file="${bifrost_dir}/config.json"
nginx_file="${bifrost_dir}/nginx.conf"
staging_dir="${bifrost_dir}/g1-staging"
current_snapshot="${bifrost_dir}/backups/g1-current-$(date +%Y%m%dT%H%M%S)"

for required_file in "${source_dir}/config.json" "${source_dir}/nginx.conf" "${source_dir}/bifrost.env" \
  "${staging_dir}/deploy-bifrost-g1-test.sh"; do
  [[ -f "${required_file}" ]] || { echo "G1_ROLLBACK=FAILED reason=required_file_missing"; exit 2; }
done

mkdir -p -m 700 "${current_snapshot}"
cp -a "${config_file}" "${current_snapshot}/config.json"
cp -a "${nginx_file}" "${current_snapshot}/nginx.conf"
cp -a "${env_file}" "${current_snapshot}/bifrost.env"

recreate_node() {
  local node_number="$1" container_name="bifrost-${1}" data_dir="${bifrost_dir}/data/node-${1}"
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container stop "${container_name}" >/dev/null
    docker container rm "${container_name}" >/dev/null
  fi
  docker run -d --name "${container_name}" --restart unless-stopped --network bifrost-net \
    --env-file "${env_file}" -v "${data_dir}:/app/data" -v "${config_file}:/app/data/config.json:ro" \
    maximhq/bifrost:v1.6.6 >/dev/null
}

wait_healthy() {
  local node="$1"
  for _ in $(seq 1 60); do
    [[ "$(docker inspect --format '{{.State.Health.Status}}' "${node}" 2>/dev/null || true)" == "healthy" ]] && return 0
    sleep 1
  done
  return 1
}

recreate_lb() {
  if docker container inspect bifrost-lb >/dev/null 2>&1; then
    docker container stop bifrost-lb >/dev/null
    docker container rm bifrost-lb >/dev/null
  fi
  docker run -d --name bifrost-lb --restart unless-stopped --network bifrost-net \
    -p 127.0.0.1:18080:8080 -v "${nginx_file}:/etc/nginx/nginx.conf:ro" nginx:1.27-alpine >/dev/null
}

restore_snapshot() {
  local exit_code=$?
  trap - ERR
  echo "G1_ROLLBACK=RECOVERY_STARTED"
  cp "${current_snapshot}/bifrost.env" "${env_file}"
  cp "${current_snapshot}/config.json" "${config_file}"
  cp "${current_snapshot}/nginx.conf" "${nginx_file}"
  chmod 600 "${env_file}"
  recreate_node 1 || true
  wait_healthy bifrost-1 || true
  recreate_node 2 || true
  wait_healthy bifrost-2 || true
  recreate_lb || true
  echo "G1_ROLLBACK=RECOVERED_CURRENT_SNAPSHOT"
  exit "${exit_code}"
}
trap restore_snapshot ERR

cp "${source_dir}/bifrost.env" "${env_file}"
cp "${source_dir}/config.json" "${config_file}"
cp "${source_dir}/nginx.conf" "${nginx_file}"
chmod 600 "${env_file}"

recreate_node 1
wait_healthy bifrost-1
recreate_node 2
wait_healthy bifrost-2
recreate_lb
for _ in $(seq 1 30); do
  [[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health 2>/dev/null || true)" == "200" ]] && break
  sleep 1
done
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health)" == "200" ]]
[[ "$(sha256sum "${config_file}" | awk '{print $1}')" == "$(sha256sum "${source_dir}/config.json" | awk '{print $1}')" ]]
echo "G1_ROLLBACK_OLD_CONFIG=PASS image=maximhq/bifrost:v1.6.6 health=200"

AI_GATEWAY_G1_DEPLOY_APPROVED=YES bash "${staging_dir}/deploy-bifrost-g1-test.sh" >/dev/null
[[ "$(sha256sum "${config_file}" | awk '{print $1}')" == "$(sha256sum "${staging_dir}/config.json" | awk '{print $1}')" ]]
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/health)" == "200" ]]
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:18080/v1/chat/completions)" == "401" ]]

trap - ERR
echo "G1_ROLLBACK=PASS old_config_restored=true fixed_image_recreated=true g1_config_reapplied=true internal_auth=401"
echo "G1_ROLLBACK_CURRENT_SNAPSHOT=${current_snapshot}"

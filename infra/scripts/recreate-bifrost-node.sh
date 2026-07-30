#!/usr/bin/env bash

set -euo pipefail

# 仅允许滚动重建既定的两个 Bifrost 节点，避免误操作其他容器。
node_number="${1:-}"
if [[ "${node_number}" != "1" && "${node_number}" != "2" ]]; then
  echo "用法：$0 <1|2>"
  exit 1
fi

container_name="bifrost-${node_number}"
data_dir="/home/pc/molin/bifrost/data/node-${node_number}"
config_file="/home/pc/molin/bifrost/config.json"
env_file="/home/pc/molin/secrets/bifrost.env"

test -f "${config_file}"
test -f "${env_file}"
mkdir -p "${data_dir}"

# 容器配置不会自动重新读取 env 文件，因此需要保留数据卷后重新创建容器。
if docker container inspect "${container_name}" >/dev/null 2>&1; then
  docker container stop "${container_name}" >/dev/null 2>&1 || true
  docker container rm "${container_name}" >/dev/null
fi

docker run -d \
  --name "${container_name}" \
  --restart unless-stopped \
  --network bifrost-net \
  --env-file "${env_file}" \
  -v "${data_dir}:/app/data" \
  -v "${config_file}:/app/data/config.json:ro" \
  maximhq/bifrost:v1.6.6

echo "RECREATED=${container_name}"

set -Eeuo pipefail

expected_binary_sha=d211268ec0b13e5c92ba1992d41b98f4a0c3415ae4fd348deb9ac843614854a4
expected_cycle_shas=(9e1242742fe1fbbc44e8abe4ab9b0ac8f2d2be1071a6e2f8c843ff1d1a2a6dbc d6696c7e0b76952a04cedc6ee7212ceed098c4f9ef6ab276b082560f74fb479e)

sort_and_compare_cycle_sha_sets() {
  mapfile -t expected_sorted < <(/usr/bin/printf '%s\n' "${expected_cycle_shas[@]}" | /usr/bin/sort)
  mapfile -t actual_sorted < <(/usr/bin/printf '%s\n' "${cycle_shas[@]}" | /usr/bin/sort)
  [[ "${actual_sorted[0]}" == "${expected_sorted[0]}" && "${actual_sorted[1]}" == "${expected_sorted[1]}" ]]
}

rollback_dir=/home/pc/molin/rollback
# metadata 必须覆盖 postcheck 的完整父链门禁，避免先返回冻结参数、随后才因上级目录不安全而失败。
parent_dirs=(/home/pc /home/pc/molin "$rollback_dir")
for parent_dir in "${parent_dirs[@]}"; do
  [[ -d "$parent_dir" && ! -L "$parent_dir" ]]
  [[ "$(/usr/bin/stat -c '%u' -- "$parent_dir")" == "$(/usr/bin/id -u)" ]]
  parent_mode=$(/usr/bin/stat -c '%a' -- "$parent_dir")
  [[ "$parent_mode" =~ ^[0-7]{3,4}$ && $(( 8#$parent_mode & 022 )) == 0 ]]
done
rollback_identity=$(/usr/bin/stat -c '%u:%a:%d:%i' -- "$rollback_dir")
[[ "$rollback_identity" =~ ^$(/usr/bin/id -u):700:[0-9]+:[0-9]+$ ]]

# 状态文件已在成功清理后按契约移除，因此只能从受限恢复目录中唯一发现冻结恢复点。
mapfile -t recovery_candidates < <(/usr/bin/find "$rollback_dir" -mindepth 1 -maxdepth 1 -type f -name 'molin-email-unknown-*.sql' -print | /usr/bin/sort)
(( ${#recovery_candidates[@]} == 1 ))
recovery_file=${recovery_candidates[0]}
[[ "$recovery_file" =~ ^/home/pc/molin/rollback/(molin-email-unknown-[a-f0-9]{32}\.sql)$ ]]
recovery_name=${BASH_REMATCH[1]}
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")
[[ "$recovery_identity" =~ ^$(/usr/bin/id -u):600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
[[ "$recovery_sha" =~ ^[a-f0-9]{64}$ && "$recovery_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]

verification_binary=/home/pc/molin/rollback/email-unknown-restart-cleanup.test
[[ -f "$verification_binary" && ! -L "$verification_binary" ]]
binary_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$verification_binary")
[[ "$binary_identity" =~ ^$(/usr/bin/id -u):500:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
[[ "$(/usr/bin/sha256sum -- "$verification_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]

mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}
  container_identity=${container_line#*|}
  container_identity=${container_identity,,}
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in
    *mysql*) mysql_ids+=("$container_id") ;;
  esac
done
(( ${#mysql_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}

mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print | /usr/bin/sort)
(( ${#cycle_markers[@]} == 2 ))
cycle_dumps=()
cycle_identities=()
cycle_shas=()
for marker in "${cycle_markers[@]}"; do
  [[ "$marker" =~ ^(/root/molin-000057-schema57-cycle-run-[a-f0-9]{32})/evidence/cycle_completed$ ]]
  cycle_dump="${BASH_REMATCH[1]}/evidence/molin_source_schema57.sql"
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_dump" ]]
  cycle_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dump")
  [[ "$cycle_identity" =~ ^0:600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
  cycle_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
  [[ "$cycle_sha" =~ ^[a-f0-9]{64}$ ]]
  cycle_dumps+=("$cycle_dump")
  cycle_identities+=("$cycle_identity")
  cycle_shas+=("$cycle_sha")
done
sort_and_compare_cycle_sha_sets

# 输出前再次冻结全部身份，防止只读窗口内的文件替换或内容漂移。
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i' -- "$rollback_dir")" == "$rollback_identity" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")" == "$recovery_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')" == "$recovery_sha" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$verification_binary")" == "$binary_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$verification_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
for index in 0 1; do
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_dumps[$index]}")" == "${cycle_identities[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_dumps[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_shas[$index]}" ]]
done

/usr/bin/printf 'status=pass recovery_filename=%s recovery_sha256=%s cycle_sha256_one=%s cycle_sha256_two=%s\n' "$recovery_name" "$recovery_sha" "${actual_sorted[0]}" "${actual_sorted[1]}"

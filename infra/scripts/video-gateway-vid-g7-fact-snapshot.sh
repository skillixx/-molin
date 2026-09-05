#!/usr/bin/env bash
# 在单一RR一致性快照中生成低敏表级摘要；列manifest可跨Expand Schema复用。
set -euo pipefail
database="${1:?缺少数据库名}"
output="${2:?缺少输出路径}"
mode="${3:?缺少base或expanded模式}"
input_manifest="${4:-}"
[[ "$database" =~ ^[A-Za-z0-9_]{1,64}$ ]]
[[ "$mode" = base || "$mode" = expanded ]]
[[ "$output" = /* ]]
test -d "$(dirname "$output")"
test ! -L "$(dirname "$output")"
test ! -e "$output"
: "${MYSQL_HOST:?}" "${MYSQL_PORT:?}" "${MYSQL_USER:?}" "${MYSQL_PASSWORD:?}"
[[ "$MYSQL_PORT" =~ ^[0-9]{1,5}$ ]]
set -o noclobber
mysql_cmd=(mysql --protocol=tcp --host="$MYSQL_HOST" --port="$MYSQL_PORT" --user="$MYSQL_USER" --database="$database" --batch --skip-column-names --raw)

specs=(
  "api_keys|1=1" "api_key_model_scopes|1=1" "wallets|1=1" "wallet_transactions|1=1"
  "ai_requests|capability IN ('chat.completions','image.generate','video.generate')"
  "ai_gateway_quotes|capability='video.generate'"
  "ai_gateway_tasks|capability='video.generate'"
  "ai_gateway_task_inputs|task_id IN (SELECT id FROM ai_gateway_tasks WHERE capability='video.generate')"
  "ai_gateway_task_payloads|task_id IN (SELECT id FROM ai_gateway_tasks WHERE capability='video.generate')"
  "ai_gateway_provider_callback_events|task_id IN (SELECT id FROM ai_gateway_tasks WHERE capability='video.generate')"
  "ai_usage_items|request_id IN (SELECT request_id FROM ai_requests WHERE capability='video.generate')"
  "ai_gateway_assets|task_id IN (SELECT id FROM ai_gateway_tasks WHERE capability='video.generate')"
  "ai_gateway_task_events|task_id IN (SELECT id FROM ai_gateway_tasks WHERE capability='video.generate')"
  "ai_outbox_events|aggregate_type='video_request'"
  "wallet_holds|id IN (SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id IN (SELECT request_id FROM ai_requests WHERE capability='video.generate'))"
  "ai_request_wallet_links|request_id IN (SELECT request_id FROM ai_requests WHERE capability='video.generate')"
  "ai_compensation_tasks|task_type LIKE 'video_%'"
  "ai_upload_sessions|purpose='video_reference_image'"
  "audit_logs|action LIKE 'video_%' OR target_type LIKE 'video_%'"
)
full_video_tables=(
  ai_gateway_input_assets ai_project_video_rights_acceptances ai_video_adjustment_approval_executions ai_video_adjustment_approvals
  ai_video_admin_archive_commands ai_video_admin_cancellation_commands ai_video_admin_input_quarantines ai_video_admin_output_quarantines ai_video_admin_poll_commands
  ai_video_asset_delete_commands ai_video_asset_deletions ai_video_asset_save_commands ai_video_asset_save_scopes ai_video_asset_saves
  ai_video_callback_nonces ai_video_cancellation_commands ai_video_download_leases ai_video_download_scopes ai_video_input_cleanup_facts ai_video_input_deletion_requests ai_video_input_imports
  ai_video_media_delete_commands ai_video_media_deletions ai_video_model_draft_commands ai_video_model_draft_states ai_video_model_publication_guard
  ai_video_object_reconciliation_observations ai_video_object_scan_cursors ai_video_output_release_executions ai_video_output_release_requests ai_video_output_retention_facts
  ai_video_project_grant_commands ai_video_queue_admission_guard ai_video_rabbit_poison_fuses ai_video_rights_declarations ai_video_rights_policies ai_video_upload_controls ai_video_upload_session_retention_facts
)
introduced_after_109() {
  case "$1" in
    ai_video_object_reconciliation_observations|ai_video_object_scan_cursors|ai_video_upload_session_retention_facts|ai_video_output_retention_facts|ai_video_rabbit_poison_fuses) return 0 ;;
    *) return 1 ;;
  esac
}

quote_identifier() { printf '`%s`' "${1//\`/\`\`}"; }
snapshot_sql='SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;'
tables=()
append_snapshot_sql() {
  local table="$1" where="$2" columns_csv="$3" order_csv="$4" column expression select_list='' order_list=''
  [[ "$table" =~ ^[A-Za-z0-9_]+$ ]]
  IFS=',' read -r -a columns <<<"$columns_csv"
  for column in "${columns[@]}"; do
    [[ "$column" =~ ^[A-Za-z0-9_]+$ ]]
    expression="IF($(quote_identifier "$column") IS NULL,'N',CONCAT('H',HEX(CAST($(quote_identifier "$column") AS BINARY))))"
    select_list="${select_list:+$select_list,}$expression"
  done
  IFS=',' read -r -a order_columns <<<"$order_csv"
  for column in "${order_columns[@]}"; do
    [[ "$column" =~ ^[A-Za-z0-9_]+$ ]]
    order_list="${order_list:+$order_list,}$(quote_identifier "$column")"
  done
  snapshot_sql+=" SELECT CONCAT('TABLE|$table|',COUNT(*)) FROM $(quote_identifier "$table") WHERE $where;"
  snapshot_sql+=" SELECT CONCAT('ROW|$table|',SHA2(CONCAT_WS('|',$select_list),256)) FROM $(quote_identifier "$table") WHERE $where ORDER BY $order_list;"
  tables+=("$table")
}

# 输入manifest只能复用本脚本按相同模式生成的表和WHERE合同；列清单可来自旧Schema，
# 但不能借manifest注入额外SQL、扩大事实范围或跳过必需表。
expected_specs=("${specs[@]}")
for table in "${full_video_tables[@]}"; do
  if [[ "$mode" = base ]] && introduced_after_109 "$table"; then continue; fi
  expected_specs+=("$table|1=1")
done

if [[ -n "$input_manifest" ]]; then
  [[ "$input_manifest" = /* ]]
  test -f "$input_manifest"
  test ! -L "$input_manifest"
  expected_index=0
  while IFS='|' read -r table where columns_csv order_csv; do
    [[ -n "$table" && -n "$where" && -n "$columns_csv" && -n "$order_csv" ]]
    [[ "$expected_index" -lt "${#expected_specs[@]}" ]]
    IFS='|' read -r expected_table expected_where <<<"${expected_specs[$expected_index]}"
    [[ "$table" = "$expected_table" && "$where" = "$expected_where" ]]
    exists="$("${mysql_cmd[@]}" -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='$table'")"
    test "$exists" = 1
    append_snapshot_sql "$table" "$where" "$columns_csv" "$order_csv"
    expected_index=$((expected_index + 1))
  done <"$input_manifest"
  [[ "$expected_index" -eq "${#expected_specs[@]}" ]]
else
  manifest="${output}.columns"
  test ! -e "$manifest"
  : >"$manifest"
  for spec in "${expected_specs[@]}"; do
    IFS='|' read -r table where <<<"$spec"
    exists="$("${mysql_cmd[@]}" -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='$table'")"
    test "$exists" = 1
    mapfile -t columns < <("${mysql_cmd[@]}" -e "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='$table' ORDER BY ordinal_position")
    ((${#columns[@]} > 0))
    columns_csv="$(IFS=,; printf '%s' "${columns[*]}")"
    mapfile -t order_columns < <("${mysql_cmd[@]}" -e "SELECT column_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='$table' AND index_name='PRIMARY' ORDER BY seq_in_index")
    if ((${#order_columns[@]} == 0)); then order_columns=("${columns[@]}"); fi
    order_csv="$(IFS=,; printf '%s' "${order_columns[*]}")"
    printf '%s|%s|%s|%s\n' "$table" "$where" "$columns_csv" "$order_csv" >>"$manifest"
    append_snapshot_sql "$table" "$where" "$columns_csv" "$order_csv"
  done
  chmod 0600 "$manifest"
fi
((${#tables[@]} > 0))
snapshot_sql+=' COMMIT;'
raw="${output}.rows.$$.tmp"
test ! -e "$raw"
cleanup_raw() { test ! -e "$raw" || rm -f -- "$raw"; }
trap cleanup_raw EXIT
printf '%s\n' "$snapshot_sql" | "${mysql_cmd[@]}" >"$raw"
: >"$output"
for table in "${tables[@]}"; do
  count="$(awk -F'|' -v table="$table" '$1=="TABLE" && $2==table {print $3}' "$raw")"
  [[ "$count" =~ ^[0-9]+$ ]]
  digest="$(awk -F'|' -v table="$table" '$1=="ROW" && $2==table {print $3}' "$raw" | sha256sum | cut -d' ' -f1)"
  printf '%s|%s|%s\n' "$table" "$count" "$digest" >>"$output"
done
cleanup_raw
trap - EXIT
chmod 0600 "$output"
sha256sum "$output"

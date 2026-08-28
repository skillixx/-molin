#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${VIDEO_GATEWAY_G1_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "VIDEO_G1_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network_name="molin-video-g1-${suffix}"
container_name="molin-video-g1-mysql-${suffix}"
database_name="molin_video_g1_contract"
root_password="$(openssl rand -hex 24)"

cleanup() {
  # 清理范围只包含本轮生成且名称精确匹配的容器和内部网络。
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null
  fi
}
trap cleanup EXIT

# 内部网络无外网出口；MySQL不映射宿主端口，数据只写本轮tmpfs。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" \
  --name "${container_name}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" \
  -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

docker exec "${container_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${container_name}:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

formal_ready=false
ready_count=0
for _ in $(seq 1 90); do
  if docker logs "${container_name}" 2>&1 | grep -q 'MySQL init process done. Ready for start up.'; then
    formal_ready=true
  fi
  if [[ "${formal_ready}" == "true" ]] && mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then
    ready_count=$((ready_count + 1))
  else
    ready_count=0
  fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${file}'"
}

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

# 先从空库真实执行1..71；在000072 ALTER前写入合法Chat/Image事实，验证真实升级兼容，而非只测新Schema写入。
for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  if [[ "${version_text}" =~ ^[0-9]{6}$ ]] && [[ $((10#${version_text})) -le 71 ]]; then
    apply_file "${base}" >/dev/null
  fi
done

mysql_exec <<'SQL'
INSERT INTO users(id,password_hash,real_name_status,status) VALUES
  (97201,'fixture','verified','active'),(97202,'fixture','verified','active');
INSERT INTO ai_projects(id,user_id,name,status) VALUES
  (97201,97201,'VID-G1主租户','active'),(97202,97202,'VID-G1隔离租户','active');
INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES
  (97200,'molin/chat-g1-preexisting','VID-G1旧Chat夹具','chat','inactive'),
  (97201,'molin/image-g1-preexisting','VID-G1旧图片夹具','image','inactive');
INSERT INTO ai_price_versions(
  id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
  failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by
) VALUES
  (97200,'molin/chat-g1-preexisting','chat.completions','token',1,'CNY',1,'draft',0.2,1000,100,NULL,0.01,'test_fixture','vid-g1-chat','test_fixture','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),97201),
  (97201,'molin/image-g1-preexisting','image.generate','image_variant',1,'CNY',1,'draft',0.2,NULL,NULL,JSON_OBJECT('sizes',JSON_ARRAY('1024x1024')),0.01,'test_fixture','vid-g1-image','test_fixture','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),97201);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (97200,'input_tokens',NULL,REPEAT('8',64),0.01,0.02,1000,'CNY'),
  (97201,'image_count',JSON_OBJECT('size','1024x1024'),REPEAT('9',64),0.10,0.20,1,'CNY');

INSERT INTO ai_requests(request_id,user_id,project_id,logical_model_code,modality,capability,delivery_status)
VALUES
  ('vid-g1-chat-preexisting',97201,97201,'molin/chat-g1-preexisting','chat','chat.completions','not_applicable'),
  ('vid-g1-image-preexisting',97201,97201,'molin/image-g1-preexisting','image','image.generate','pending');
INSERT INTO ai_usage_items(request_id,meter_type,source,record_kind,sequence_no,quantity,usage_unit,unit_size)
VALUES('vid-g1-chat-preexisting','input_tokens','provider','legacy_chat',0,12,'tokens',1);
INSERT INTO ai_gateway_quotes(
  id,public_id,user_id,project_id,logical_model_code,capability,request_fingerprint,request_variant_hash,
  price_version_id,price_snapshot_json,quoted_amount,currency,expires_at,consumed_request_id,consumed_at
) VALUES(
  97200,'quote-vid-g1-image-preexisting',97201,97201,'molin/image-g1-preexisting','image.generate',REPEAT('a',64),REPEAT('9',64),
  97201,JSON_OBJECT('amount','0.20000000'),0.20,'CNY',DATE_ADD(NOW(),INTERVAL 5 MINUTE),'vid-g1-image-preexisting',NOW()
);
INSERT INTO ai_gateway_tasks(
  id,public_id,request_id,quote_id,user_id,project_id,logical_model_code,capability,status,progress,input_json,completed_at
) VALUES(
  97200,'task-vid-g1-image-preexisting','vid-g1-image-preexisting',97200,97201,97201,'molin/image-g1-preexisting','image.generate','succeeded',100,JSON_OBJECT('size','1024x1024'),NOW()
);
INSERT INTO ai_gateway_assets(
  id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,
  mime_type,size_bytes,sha256,width,height,source,moderation_status,explicit_label_status,implicit_label_status,
  lifecycle_state,retention_policy_id,expires_at
) VALUES(
  97200,'asset-vid-g1-image-preexisting',97201,97201,'vid-g1-image-preexisting',97200,0,'primary_output',1,'image-result','vid-g1/source.png',
  'image/png',68,REPEAT('b',64),1,1,'provider_base64','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY)
);
INSERT INTO ai_usage_items(
  request_id,meter_type,source,record_kind,price_version_id,variant_hash,variant_json,sequence_no,quantity,usage_unit,unit_size,unit_price,amount,currency
) VALUES(
  'vid-g1-image-preexisting','image_count','gateway','sale_line',97201,REPEAT('9',64),JSON_OBJECT('size','1024x1024'),0,1,'count',1,0.20,0.20,'CNY'
);
SQL

# 执行本阶段两份migration，形成真实1..73全链。
apply_file 000072_expand_video_gateway_schema.up.sql >/dev/null
apply_file 000073_seed_video_gateway_permissions.up.sql >/dev/null

assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_upload_sessions','ai_gateway_input_assets','ai_gateway_task_inputs','ai_gateway_task_events','ai_gateway_provider_callback_events','ai_gateway_task_payloads')" "6" "video_expand_tables"
assert_scalar "SELECT COUNT(*) FROM permissions WHERE code LIKE 'video:%'" "10" "video_permission_seed"
assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id WHERE r.code='admin' AND p.code LIKE 'video:%'" "10" "video_admin_permission_seed"
assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id WHERE r.code<>'admin' AND p.code LIKE 'video:%'" "0" "video_permission_admin_only"
assert_scalar "SELECT CONCAT(modality,':',capability,':',COALESCE(operation,'NULL')) FROM ai_requests WHERE request_id='vid-g1-chat-preexisting'" "chat:chat.completions:NULL" "preexisting_chat_request"
assert_scalar "SELECT CONCAT(capability,':',COALESCE(operation,'NULL'),':',quoted_amount,':',request_variant_hash) FROM ai_gateway_quotes WHERE id=97200" "image.generate:NULL:0.20000000:9999999999999999999999999999999999999999999999999999999999999999" "preexisting_image_quote"
assert_scalar "SELECT CONCAT(capability,':',COALESCE(operation,'NULL'),':',status) FROM ai_gateway_tasks WHERE id=97200" "image.generate:NULL:succeeded" "preexisting_image_task"
assert_scalar "SELECT CONCAT(modality,':',lifecycle_state,':',mime_type,':',COALESCE(duration_seconds,'NULL'),':',COALESCE(frame_rate,'NULL'),':',COALESCE(container,'NULL'),':',COALESCE(video_codec,'NULL'),':',COALESCE(audio_codec,'NULL'),':',COALESCE(has_audio,'NULL')) FROM ai_gateway_assets WHERE id=97200" "image:available:image/png:NULL:NULL:NULL:NULL:NULL:NULL" "preexisting_image_asset"
assert_scalar "SELECT CONCAT(COUNT(*),':',SUM(amount),':',SUM(operation IS NULL)) FROM ai_usage_items WHERE request_id IN ('vid-g1-chat-preexisting','vid-g1-image-preexisting')" "2:0.20000000:2" "preexisting_usage_facts"

# 重复执行两份up，验证新增列、CHECK、外键、索引和seed均可断点重跑。
apply_file 000072_expand_video_gateway_schema.up.sql >/dev/null
apply_file 000073_seed_video_gateway_permissions.up.sql >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name IN ('operation','bifrost_provider','bifrost_task_id','bifrost_compound_id')" "4" "repeat_up_task_columns"

# 继续构造新二进制Chat/Image写入和两种视频operation；测试数据仍只存在本轮tmpfs。
mysql_exec <<'SQL'
INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES
  (97202,'molin/video-g1-fixture','VID-G1视频夹具','video','inactive');
INSERT INTO ai_price_versions(
  id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
  failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by
) VALUES(
  97202,'molin/video-g1-fixture','video.generate','video_seconds',1,'CNY',1,'draft',0.2,
  NULL,NULL,JSON_OBJECT('supported_operations',JSON_ARRAY('text_to_video','image_to_video')),0.01,'test_fixture','vid-g1','test_fixture',
  'confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),97201
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (97202,'video_seconds',JSON_OBJECT('operation','text_to_video','seconds',5,'size','1280x720'),REPEAT('c',64),0.01,0.02,1,'CNY'),
  (97202,'video_seconds',JSON_OBJECT('operation','image_to_video','seconds',5,'size','1280x720'),REPEAT('2',64),0.01,0.02,1,'CNY');

-- 新二进制仍可按旧Chat/Image写法省略operation。
INSERT INTO ai_requests(request_id,user_id,project_id,logical_model_code,modality,capability,delivery_status)
VALUES
  ('vid-g1-chat-after',97201,97201,'molin/chat-g1-preexisting','chat','chat.completions','not_applicable'),
  ('vid-g1-image-after',97201,97201,'molin/image-g1-preexisting','image','image.generate','pending');

-- 文生和图生视频共用请求、Quote与Task，只以operation区分。
INSERT INTO ai_requests(request_id,user_id,project_id,logical_model_code,modality,capability,operation,delivery_status)
VALUES
  ('vid-g1-t2v',97201,97201,'molin/video-g1-fixture','video','video.generate','text_to_video','pending'),
  ('vid-g1-i2v',97201,97201,'molin/video-g1-fixture','video','video.generate','image_to_video','pending');
INSERT INTO ai_gateway_quotes(
  id,public_id,user_id,project_id,logical_model_code,capability,operation,request_fingerprint,request_variant_hash,
  price_version_id,price_snapshot_json,quoted_amount,currency,expires_at,consumed_request_id,consumed_at
) VALUES
  (97201,'quote-vid-g1-t2v',97201,97201,'molin/video-g1-fixture','video.generate','text_to_video',REPEAT('d',64),REPEAT('e',64),97202,JSON_OBJECT('fixture',true),0.10,'CNY',DATE_ADD(NOW(),INTERVAL 5 MINUTE),'vid-g1-t2v',NOW()),
  (97202,'quote-vid-g1-i2v',97201,97201,'molin/video-g1-fixture','video.generate','image_to_video',REPEAT('f',64),REPEAT('0',64),97202,JSON_OBJECT('fixture',true),0.10,'CNY',DATE_ADD(NOW(),INTERVAL 5 MINUTE),'vid-g1-i2v',NOW());
INSERT INTO ai_gateway_tasks(
  id,public_id,request_id,quote_id,user_id,project_id,logical_model_code,capability,operation,status,progress,input_json
) VALUES
  (97201,'video-vid-g1-t2v','vid-g1-t2v',97201,97201,97201,'molin/video-g1-fixture','video.generate','text_to_video','created',0,JSON_OBJECT('seconds',5)),
  (97202,'video-vid-g1-i2v','vid-g1-i2v',97202,97201,97201,'molin/video-g1-fixture','video.generate','image_to_video','created',0,JSON_OBJECT('seconds',5));

INSERT INTO ai_upload_sessions(
  id,public_id,user_id,project_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at,expired_at
) VALUES
  (97201,'upload-vid-g1-i2v',97201,97201,'video_reference_image','platform_presigned','image/png',68,'private-input','vid-g1/input.png','created',DATE_ADD(NOW(),INTERVAL 10 MINUTE),NULL),
  (97204,'upload-vid-g1-expired',97201,97201,'video_reference_image','platform_presigned','image/jpeg',128,'private-input','vid-g1/expired.jpg','expired',DATE_SUB(NOW(),INTERVAL 1 MINUTE),NOW()),
  (97205,'upload-vid-g1-duplicate-complete',97201,97201,'video_reference_image','platform_presigned','image/png',68,'private-input','vid-g1/duplicate.png','created',DATE_ADD(NOW(),INTERVAL 10 MINUTE),NULL),
  (97206,'upload-vid-g1-cross-owner',97202,97202,'video_reference_image','platform_presigned','image/png',68,'private-input','vid-g1/cross-owner.png','created',DATE_ADD(NOW(),INTERVAL 10 MINUTE),NULL);
INSERT INTO ai_gateway_input_assets(
  id,public_id,user_id,project_id,source_type,upload_session_id,original_sha256,
  moderation_status,version_no,lifecycle_state,expires_at
) VALUES(
  97201,'input-vid-g1-i2v',97201,97201,'platform_presigned',97201,REPEAT('f',64),
  'pending',1,'normalizing',DATE_ADD(NOW(),INTERVAL 1 DAY));
UPDATE ai_gateway_input_assets SET bucket='private-input',object_key='vid-g1/normalized.png',normalized_sha256=REPEAT('1',64),
  mime_type='image/png',size_bytes=68,width=1,height=1,moderation_policy_version='fixture-v1',moderation_status='passed',lifecycle_state='ready'
WHERE id=97201 AND lifecycle_state='normalizing';
-- completed缺少ETag/version必须失败，随后写入ETag才能合法完成。
SQL
if mysql_exec -e "UPDATE ai_upload_sessions SET status='completed',final_input_asset_id=97201,completed_at=NOW() WHERE id=97201" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=completed_without_source_version_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_upload_sessions SET status='completed',source_etag='   ',source_version_id='',final_input_asset_id=97201,completed_at=NOW() WHERE id=97201" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=completed_with_empty_source_version_allowed"; exit 2
fi
mysql_exec <<'SQL'
UPDATE ai_upload_sessions SET status='completed',source_etag='fixture-etag',final_input_asset_id=97201,completed_at=NOW() WHERE id=97201;
INSERT INTO ai_gateway_task_inputs(task_id,input_asset_id,user_id,project_id,role,ordinal,normalized_sha256,input_version)
VALUES(97202,97201,97201,97201,'reference_image',0,REPEAT('1',64),1);

-- 从已有图片资产生成独立私有快照；源对象定位不复制，只保存source_gateway_asset_id和新的规范化对象。
INSERT INTO ai_gateway_input_assets(
  id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,
  mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at
) VALUES(
  97203,'input-vid-g1-source-snapshot',97201,97201,'gateway_asset_snapshot',97200,'private-input','vid-g1/source-snapshot.png',REPEAT('b',64),REPEAT('6',64),
  'image/png',68,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY)
);

INSERT INTO ai_gateway_provider_callback_events(
  task_id,user_id,project_id,provider_code,provider_task_id,external_event_id,body_sha256,signature_status,application_result_json,process_status,processed_at
) VALUES(
  97202,97201,97201,'fake','provider-task-1','event-1',REPEAT('2',64),'valid',JSON_OBJECT('applied',true),'applied',NOW());
INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,source,event_type,from_status,to_status,safe_detail_json)
VALUES('task-event-vid-g1-1',97202,97201,97201,'worker','status_changed','created','queued',JSON_OBJECT('fixture',true));
SQL

assert_scalar "SELECT GROUP_CONCAT(CONCAT(modality,':',capability,':',COALESCE(operation,'NULL')) ORDER BY request_id SEPARATOR '|') FROM ai_requests WHERE request_id IN ('vid-g1-chat-after','vid-g1-image-after')" "chat:chat.completions:NULL|image:image.generate:NULL" "legacy_binary_new_writes"
assert_scalar "SELECT GROUP_CONCAT(operation ORDER BY operation SEPARATOR ',') FROM ai_gateway_tasks WHERE id IN (97201,97202)" "image_to_video,text_to_video" "video_operations"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs WHERE task_id=97201" "0" "text_to_video_zero_input"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs WHERE task_id=97202 AND lease_released_at IS NULL" "1" "image_to_video_single_input_lease"
assert_scalar "SELECT COUNT(*) FROM ai_price_skus WHERE price_version_id=97202 AND meter_type='video_seconds' AND JSON_UNQUOTE(JSON_EXTRACT(variant_json,'$.operation')) IN ('text_to_video','image_to_video')" "2" "video_price_operation_variants"
assert_scalar "SELECT CONCAT(status,':',final_input_asset_id IS NULL,':',expired_at IS NOT NULL) FROM ai_upload_sessions WHERE id=97204" "expired:1:1" "upload_expiry_shape"
assert_scalar "SELECT CONCAT(normalized_sha256,':',version_no,':',source_gateway_asset_id) FROM ai_gateway_input_assets WHERE id=97203" "6666666666666666666666666666666666666666666666666666666666666666:1:97200" "source_snapshot_before_change"

# 有效AES-GCM载荷和无法关联本地任务的全NULL owner回调均应合法保存。
mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'prompt',X'010203',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('a',64),REPEAT('b',64));"
mysql_exec -e "INSERT INTO ai_gateway_provider_callback_events(task_id,user_id,project_id,provider_code,provider_task_id,external_event_id,body_sha256,signature_status,process_status) VALUES(NULL,NULL,NULL,'fake','provider-unlinked','event-unlinked',REPEAT('4',64),'unverified','received');"
assert_scalar "SELECT CONCAT(OCTET_LENGTH(ciphertext),':',OCTET_LENGTH(nonce),':',payload_kind) FROM ai_gateway_task_payloads WHERE task_id=97202 AND payload_kind='prompt'" "3:12:prompt" "valid_encrypted_payload"
assert_scalar "SELECT CONCAT(task_id IS NULL,':',user_id IS NULL,':',project_id IS NULL,':',process_status) FROM ai_gateway_provider_callback_events WHERE external_event_id='event-unlinked'" "1:1:1:received" "unlinked_callback_owner_shape"

# 加密载荷的密文、nonce、hash、唯一键和组合归属全部失败关闭。
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'provider_request',X'01',UNHEX('00112233445566778899aa'),'fixture-key-v1',REPEAT('a',64),REPEAT('b',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_bad_nonce_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'provider_request',X'',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('a',64),REPEAT('b',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_empty_ciphertext_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'provider_request',X'01',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('g',64),REPEAT('b',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_bad_aad_hash_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'provider_request',X'01',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('a',64),REPEAT('z',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_bad_ciphertext_hash_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97201,97201,'prompt',X'04',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('a',64),REPEAT('b',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_duplicate_kind_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_payloads(task_id,user_id,project_id,payload_kind,ciphertext,nonce,key_version,aad_sha256,ciphertext_sha256) VALUES(97202,97202,97202,'provider_request',X'01',UNHEX('00112233445566778899aabb'),'fixture-key-v1',REPEAT('a',64),REPEAT('b',64))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=payload_cross_owner_allowed"; exit 2
fi

# 回调owner必须三列全空或全非空，处理状态与processed_at必须成对。
if mysql_exec -e "INSERT INTO ai_gateway_provider_callback_events(task_id,user_id,project_id,provider_code,provider_task_id,external_event_id,body_sha256,signature_status,process_status) VALUES(97202,97201,NULL,'fake','provider-partial','event-partial',REPEAT('5',64),'valid','received')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=callback_partial_owner_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_provider_callback_events(provider_code,provider_task_id,external_event_id,body_sha256,signature_status,process_status,processed_at) VALUES('fake','provider-received-time','event-received-time',REPEAT('5',64),'valid','received',NOW())" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=callback_received_with_processed_at_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_provider_callback_events(provider_code,provider_task_id,external_event_id,body_sha256,signature_status,process_status) VALUES('fake','provider-applied-no-time','event-applied-no-time',REPEAT('5',64),'valid','applied')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=callback_applied_without_processed_at_allowed"; exit 2
fi

# 上传完成、组合归属、唯一输入和两类重放必须由数据库拒绝。
if mysql_exec -e "INSERT INTO ai_upload_sessions(id,public_id,user_id,project_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at) VALUES(97207,'upload-vid-g1-same-object',97201,97201,'video_reference_image','platform_presigned','image/png',68,'private-input','vid-g1/input.png','created',DATE_ADD(NOW(),INTERVAL 10 MINUTE))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_upload_object_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,upload_session_id,original_sha256,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97207,'input-vid-g1-duplicate-upload',97201,97201,'platform_presigned',97201,REPEAT('7',64),'pending',1,'normalizing',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_upload_snapshot_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_upload_sessions SET status='completed',source_etag='duplicate',final_input_asset_id=97201,completed_at=NOW() WHERE id=97205" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_complete_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_upload_sessions SET status='completed',source_etag='cross-owner',final_input_asset_id=97203,completed_at=NOW() WHERE id=97206" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=cross_owner_complete_allowed"; exit 2
fi
mysql_exec -e "INSERT INTO ai_upload_sessions(id,public_id,user_id,project_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at) VALUES(97209,'upload-vid-g1-expired-verifying',97201,97201,'video_reference_image','platform_presigned','image/png',68,'private-input','vid-g1/expired-verifying.png','verifying',DATE_SUB(NOW(),INTERVAL 1 MINUTE));"
if mysql_exec -e "UPDATE ai_upload_sessions SET status='completed',source_etag='expired-etag',final_input_asset_id=97203,completed_at=NOW() WHERE id=97209" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=expired_upload_completed"; exit 2
fi
assert_scalar "SELECT CONCAT(status,':',final_input_asset_id IS NULL,':',completed_at IS NULL) FROM ai_upload_sessions WHERE id=97209" "verifying:1:1" "expired_complete_rejected"
if mysql_exec -e "INSERT INTO ai_upload_sessions(id,public_id,user_id,project_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at) VALUES(97208,'upload-vid-g1-bad-expired',97201,97201,'video_reference_image','platform_presigned','image/jpeg',128,'private-input','vid-g1/bad-expired.jpg','expired',DATE_SUB(NOW(),INTERVAL 1 MINUTE))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=expired_without_timestamp_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES(97202,'video_seconds',JSON_OBJECT('operation','wrong_operation','seconds',5,'size','1280x720'),REPEAT('3',64),0.01,0.02,1,'CNY')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=invalid_video_price_operation_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES(97202,'video_seconds',JSON_OBJECT('seconds',5,'size','1280x720'),REPEAT('4',64),0.01,0.02,1,'CNY')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=missing_video_price_operation_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES(97202,'video_seconds',JSON_OBJECT('operation',NULL,'seconds',5,'size','1280x720'),REPEAT('5',64),0.01,0.02,1,'CNY')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=null_video_price_operation_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_requests SET operation=NULL WHERE request_id='vid-g1-t2v'" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=null_video_request_operation_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_gateway_quotes SET operation=NULL WHERE id=97201" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=null_video_quote_operation_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_gateway_tasks SET operation=NULL WHERE id=97201" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=null_video_task_operation_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_usage_items(request_id,meter_type,source,record_kind,operation,price_version_id,variant_hash,variant_json,sequence_no,quantity,usage_unit,unit_size) VALUES('vid-g1-t2v','video_seconds','gateway','usage_fact',NULL,97202,REPEAT('c',64),JSON_OBJECT('operation','text_to_video'),0,5,'seconds',1)" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=null_video_usage_operation_allowed"; exit 2
fi

# 视频available资产的每项媒体字段均须显式非空，不能利用MySQL CHECK UNKNOWN进入可交付状态。
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97401,'video-asset-no-mime',97201,97201,'vid-g1-t2v',97201,0,'content',1,'video-result','vid-g1/no-mime.mp4','video',NULL,1024,REPEAT('8',64),1280,720,5,24,'mp4','h264',NULL,0,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_without_mime_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97402,'video-asset-no-duration',97201,97201,'vid-g1-t2v',97201,0,'content',1,'video-result','vid-g1/no-duration.mp4','video','video/mp4',1024,REPEAT('8',64),1280,720,NULL,24,'mp4','h264',NULL,0,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_without_duration_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97403,'video-asset-no-frame-rate',97201,97201,'vid-g1-t2v',97201,0,'content',1,'video-result','vid-g1/no-frame-rate.mp4','video','video/mp4',1024,REPEAT('8',64),1280,720,5,NULL,'mp4','h264',NULL,0,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_without_frame_rate_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97404,'video-asset-no-audio-flag',97201,97201,'vid-g1-t2v',97201,0,'content',1,'video-result','vid-g1/no-audio-flag.mp4','video','video/mp4',1024,REPEAT('8',64),1280,720,5,24,'mp4','h264',NULL,NULL,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_without_audio_flag_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97405,'video-asset-empty-bucket',97201,97201,'vid-g1-t2v',97201,0,'content',1,'   ','vid-g1/empty-bucket.mp4','video','video/mp4',1024,REPEAT('8',64),1280,720,5,24,'mp4','h264',NULL,0,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_with_empty_bucket_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(id,public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,modality,mime_type,size_bytes,sha256,width,height,duration_seconds,frame_rate,container,video_codec,audio_codec,has_audio,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES(97406,'video-asset-empty-object',97201,97201,'vid-g1-t2v',97201,0,'content',1,'video-result','   ','video','video/mp4',1024,REPEAT('8',64),1280,720,5,24,'mp4','h264',NULL,0,'provider_url','passed','applied','applied','available','fixture-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=video_available_with_empty_object_allowed"; exit 2
fi

# ready输入的每项规范化元数据都必须非空；MySQL UNKNOWN不得绕过CHECK。
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97301,'input-ready-no-hash',97201,97201,'gateway_asset_snapshot',97200,'private-input','vid-g1/no-hash.png',REPEAT('b',64),NULL,'image/png',68,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=ready_without_normalized_hash_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97302,'input-ready-no-mime',97201,97201,'gateway_asset_snapshot',97200,'private-input','vid-g1/no-mime.png',REPEAT('b',64),REPEAT('7',64),NULL,68,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=ready_without_mime_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97303,'input-ready-no-size',97201,97201,'gateway_asset_snapshot',97200,'private-input','vid-g1/no-size.png',REPEAT('b',64),REPEAT('7',64),'image/png',NULL,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=ready_without_size_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97304,'input-ready-empty-bucket',97201,97201,'gateway_asset_snapshot',97200,'   ','vid-g1/empty-bucket.png',REPEAT('b',64),REPEAT('7',64),'image/png',68,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=ready_with_empty_bucket_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,source_gateway_asset_id,bucket,object_key,original_sha256,normalized_sha256,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at) VALUES(97305,'input-ready-empty-object',97201,97201,'gateway_asset_snapshot',97200,'private-input','   ',REPEAT('b',64),REPEAT('7',64),'image/png',68,1,1,'fixture-v1','passed',1,'ready',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=ready_with_empty_object_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_inputs(task_id,input_asset_id,user_id,project_id,role,ordinal,normalized_sha256,input_version) VALUES(97202,97201,97202,97202,'reference_image',0,REPEAT('1',64),1)" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=cross_tenant_task_input_allowed"; exit 2
fi

# Bifrost任务引用和复合ID分别全局唯一；native任务NULL不受影响，轮询索引保持可用。
mysql_exec -e "UPDATE ai_gateway_tasks SET bifrost_provider='fake',bifrost_task_id='bifrost-task-1',bifrost_compound_id='fake:bifrost-task-1' WHERE id=97201;"
if mysql_exec -e "UPDATE ai_gateway_tasks SET bifrost_provider='fake',bifrost_task_id='bifrost-task-1',bifrost_compound_id='fake:bifrost-task-2' WHERE id=97202" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_bifrost_provider_task_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_gateway_tasks SET bifrost_provider='fake',bifrost_task_id='bifrost-task-2',bifrost_compound_id='fake:bifrost-task-1' WHERE id=97202" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_bifrost_compound_allowed"; exit 2
fi
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND index_name='idx_ai_gateway_tasks_bifrost_poll'" "4" "bifrost_poll_index"
assert_scalar "SELECT CONCAT(bifrost_provider IS NULL,':',bifrost_task_id IS NULL,':',bifrost_compound_id IS NULL) FROM ai_gateway_tasks WHERE id=97202" "1:1:1" "bifrost_duplicate_updates_rolled_back"

# 源图片进入隔离后，独立snapshot的规范化hash/version保持不变；外键阻止源资产事实被删除。
mysql_exec -e "UPDATE ai_gateway_assets SET moderation_status='error',lifecycle_state='quarantined' WHERE id=97200;"
assert_scalar "SELECT CONCAT(normalized_sha256,':',version_no,':',lifecycle_state) FROM ai_gateway_input_assets WHERE id=97203" "6666666666666666666666666666666666666666666666666666666666666666:1:ready" "source_snapshot_after_change"
if mysql_exec -e "DELETE FROM ai_gateway_assets WHERE id=97200" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=source_asset_fact_delete_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_inputs(task_id,input_asset_id,user_id,project_id,role,ordinal,normalized_sha256,input_version) VALUES(97202,97201,97201,97201,'reference_image',0,REPEAT('1',64),1)" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=duplicate_reference_input_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_provider_callback_events(task_id,user_id,project_id,provider_code,provider_task_id,external_event_id,body_sha256,signature_status,process_status) VALUES(97202,97201,97201,'fake','provider-task-1','event-1',REPEAT('3',64),'valid','received')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=callback_replay_allowed"; exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,source,event_type) VALUES('task-event-vid-g1-1',97202,97201,97201,'worker','duplicate')" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=task_event_replay_allowed"; exit 2
fi
if mysql_exec -e "UPDATE ai_gateway_task_events SET event_type='rewritten' WHERE event_id='task-event-vid-g1-1'" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=task_event_update_allowed"; exit 2
fi
if mysql_exec -e "DELETE FROM ai_gateway_task_events WHERE event_id='task-event-vid-g1-1'" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=task_event_delete_allowed"; exit 2
fi
assert_scalar "SELECT CONCAT(COUNT(*),':',MAX(event_type)) FROM ai_gateway_task_events WHERE event_id='task-event-vid-g1-1'" "1:status_changed" "task_event_append_only"

# legal_hold必须阻止pending_delete。活动租约跨表不伪装成数据库CHECK，只验证扫描索引存在并由Service事务负责不更新。
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_task_inputs' AND index_name='idx_ai_gateway_task_inputs_lease'" "3" "active_lease_scan_index"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs WHERE task_id=97202 AND lease_released_at IS NULL" "1" "active_lease_service_guard_fixture"
if mysql_exec -e "UPDATE ai_gateway_input_assets SET legal_hold=1,lifecycle_state='pending_delete',delete_requested_at=NOW(),pending_delete_at=NOW() WHERE id=97203" >/dev/null 2>&1; then
  echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=legal_hold_pending_delete_allowed"; exit 2
fi
assert_scalar "SELECT CONCAT(lifecycle_state,':',legal_hold,':',pending_delete_at IS NULL) FROM ai_gateway_input_assets WHERE id=97203" "ready:0:1" "pending_delete_legal_hold_guard"

# created与pending_reconcile都必须继续持有租约；只有任务安全成功且请求已结算后才允许本地释放。
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs ti JOIN ai_gateway_tasks t ON t.id=ti.task_id WHERE ti.task_id=97202 AND t.status='created' AND ti.lease_released_at IS NULL" "1" "created_lease_held"
mysql_exec -e "UPDATE ai_gateway_tasks SET status='pending_reconcile' WHERE id=97202;"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs ti JOIN ai_gateway_tasks t ON t.id=ti.task_id WHERE ti.task_id=97202 AND t.status='pending_reconcile' AND ti.lease_released_at IS NULL" "1" "pending_reconcile_lease_held"
mysql_exec -e "UPDATE ai_gateway_tasks SET status='succeeded',progress=100,completed_at=NOW() WHERE id=97202; UPDATE ai_requests SET execution_status='succeeded',billing_status='settled',delivery_status='available',completed_at=NOW() WHERE request_id='vid-g1-i2v';"
mysql_exec -e "UPDATE ai_gateway_task_inputs ti JOIN ai_gateway_tasks t ON t.id=ti.task_id JOIN ai_requests r ON r.request_id=t.request_id SET ti.lease_released_at=NOW() WHERE ti.task_id=97202 AND ti.lease_released_at IS NULL AND t.status='succeeded' AND t.completed_at IS NOT NULL AND r.billing_status='settled';"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_task_inputs WHERE task_id=97202 AND lease_released_at IS NOT NULL" "1" "lease_release"
lease_released_before="$(mysql_exec -e "SELECT DATE_FORMAT(lease_released_at,'%Y-%m-%d %H:%i:%s') FROM ai_gateway_task_inputs WHERE task_id=97202")"
mysql_exec -e "UPDATE ai_gateway_task_inputs ti JOIN ai_gateway_tasks t ON t.id=ti.task_id JOIN ai_requests r ON r.request_id=t.request_id SET ti.lease_released_at=NOW() WHERE ti.task_id=97202 AND ti.lease_released_at IS NULL AND t.status='succeeded' AND t.completed_at IS NOT NULL AND r.billing_status='settled';"
lease_released_after="$(mysql_exec -e "SELECT DATE_FORMAT(lease_released_at,'%Y-%m-%d %H:%i:%s') FROM ai_gateway_task_inputs WHERE task_id=97202")"
[[ "${lease_released_before}" == "${lease_released_after}" ]] || { echo "VIDEO_G1_MYSQL_MIGRATION=FAILED reason=lease_released_twice"; exit 2; }

# down只输出保留策略；re-up必须在既有事实存在时继续成功。
apply_file 000073_seed_video_gateway_permissions.down.sql >/dev/null
apply_file 000072_expand_video_gateway_schema.down.sql >/dev/null
assert_scalar "SELECT COUNT(*) FROM ai_gateway_tasks WHERE id IN (97201,97202)" "2" "down_task_facts_retained"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_provider_callback_events WHERE external_event_id='event-1'" "1" "down_callback_fact_retained"
apply_file 000072_expand_video_gateway_schema.up.sql >/dev/null
apply_file 000073_seed_video_gateway_permissions.up.sql >/dev/null
assert_scalar "SELECT COUNT(*) FROM ai_gateway_input_assets WHERE id=97201" "1" "reup_input_fact_retained"

echo "VIDEO_G1_MYSQL_MIGRATION=PASS mysql=8.0 isolated=true full_chain_1_to_73=true first_up=true repeat_up=true down_reup=true preexisting_chat_image=true legacy_chat_image=true text_to_video=true image_to_video=true ownership=true uniqueness=true lease=true safe_lease_release=true callback_replay=true callback_state_shape=true upload_expiry=true expired_complete_rejected=true duplicate_complete=true cross_owner_complete=true source_snapshot=true price_operation_variant=true null_fail_closed=true empty_string_fail_closed=true pending_delete_guard=true task_event_append_only=true video_asset_null_fail_closed=true payload_crypto=true bifrost_uniqueness=true permission_admin_only=true project_database=false provider_calls=0 wallet_writes=0"

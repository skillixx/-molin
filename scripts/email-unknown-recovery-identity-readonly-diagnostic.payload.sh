#!/bin/bash
set -uo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

set +e
# find、stat、id 的 stdout、stderr 与退出码均由 Python 在内存中分别捕获，shell 只接收固定分类。
parser_output=$(/usr/bin/python3 - 2>&1 <<'RECOVERY_IDENTITY_DIAGNOSTIC'
__MOLIN_RECOVERY_IDENTITY_DIAGNOSTIC_PARSER__
RECOVERY_IDENTITY_DIAGNOSTIC
)
parser_exit=$?
set -e

if [[ $parser_exit -eq 0 && "$parser_output" == 'SAFE_RECOVERY_IDENTITY=pass' ]]; then
  /usr/bin/printf 'status=pass parser_pass=true classification=pass candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\n'
  exit 0
fi

if [[ $parser_exit -ne 0 && "$parser_output" =~ ^SAFE_RECOVERY_IDENTITY=([a-z_]+)$ ]]; then
  classification=${BASH_REMATCH[1]}
  case "$classification" in
    recovery_find)
      /usr/bin/printf 'status=pass parser_pass=false classification=recovery_find candidate_unique=false file_identity=false writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\n'
      exit 0
      ;;
    recovery_stat|recovery_uid)
      /usr/bin/printf 'status=pass parser_pass=false classification=%s candidate_unique=true file_identity=false writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\n' "$classification"
      exit 0
      ;;
    artifact_name|dump_header|dump_trailer|sql_lexer|ddl_statement_count|ddl_shape|table_options|column_signature|primary_key|insert_statement_count|insert_shape|row_parse|schema_ddl|schema_migrations|fixture_nonce|fixture_rows|fixture_hmac|fixture_idempotency_hash|fixture_scope|fixture_contract|fixture_related|fixture_fingerprint|fixture_ownership|unclassified)
      /usr/bin/printf 'status=pass parser_pass=false classification=%s candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\n' "$classification"
      exit 0
      ;;
  esac
fi

/usr/bin/printf 'status=failed parser_pass=false classification=diagnostic_protocol candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\n'
exit 2

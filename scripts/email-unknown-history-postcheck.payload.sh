set -Eeuo pipefail
stage=shell_options

# 远端错误统一收敛为固定阶段，禁止原始命令错误、路径和配置值进入输出。
exec 2>/dev/null
fail() {
  local failed_stage=$1
  trap - ERR
  /usr/bin/cat >/dev/null || true
  printf 'status=failed stage=%s\n' "$failed_stage"
  exit 2
}
trap 'fail "$stage"' ERR

if ! shopt -qo errexit || ! shopt -qo nounset || ! shopt -qo pipefail; then
  fail shell_options
fi
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

read_process_env() {
  local process_id=$1 key=$2
  local -a values=()
  mapfile -t values < <(/usr/bin/tr '\0' '\n' < "/proc/${process_id}/environ" | /usr/bin/sed -n "s/^${key}=//p")
  (( ${#values[@]} == 1 ))
  printf '%s' "${values[0]}"
}

mysql_scalar() {
  local sql=$1 normalized result
  normalized=${sql//$'\n'/ }
  normalized=${normalized^^}
  [[ "$normalized" =~ ^[[:space:]]*SELECT[[:space:]] ]]
  [[ ! "$normalized" =~ (^|[[:space:]])(INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD|LOCK|UNLOCK|SET)([[:space:]]|$) ]]
  result=$(MYSQL_PWD="$mysql_password" /usr/bin/docker exec -e MYSQL_PWD="$mysql_password" "$mysql_id" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user="$mysql_user" --database="$mysql_database" --batch --skip-column-names --raw --execute="$sql")
  [[ "$result" != *$'\n'* ]]
  printf '%s' "$result"
}

cycle_schema_root_scalar() {
  local schema_name=$1 result
  [[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
  result=$(/usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- "$schema_name" <<'ROOT_SCHEMA_QUERY'
set -Eeuo pipefail
schema_name=$1
[[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
sql="SELECT CONCAT(
  (SELECT version FROM schema_migrations LIMIT 1), CHAR(9),
  (SELECT IF(dirty,1,0) FROM schema_migrations LIMIT 1), CHAR(9),
  (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'), CHAR(9),
  (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000057_email_receipt_time_backup'), CHAR(9),
  (SELECT IF(COUNT(*)=1 AND SUM(data_type='datetime' AND datetime_precision=0 AND is_nullable='NO')=1,1,0) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts' AND column_name='created_at')
);"
result=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user=root --database="$schema_name" --batch --skip-column-names --raw --execute="$sql")
[[ "$result" == $'57\t0\t69\t1\t1' ]]
printf '%s' "$result"
ROOT_SCHEMA_QUERY
  )
  [[ "$result" == $'57\t0\t69\t1\t1' ]]
  printf '%s' "$result"
}

strict_api_status() {
  local expected=$1 body=$2
  /usr/bin/python3 - "$expected" "$body" <<'STRICT_API_JSON'
import json
import sys

expected, raw = sys.argv[1:]

def no_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate")
        value[key] = item
    return value

value = json.loads(raw, object_pairs_hook=no_duplicates)
if not isinstance(value, dict) or set(value) != {"code", "message", "data"}:
    raise ValueError("top_fields")
if type(value["code"]) is not int or value["code"] != 0 or value["message"] != "ok":
    raise ValueError("envelope")
data = value["data"]
if not isinstance(data, dict) or set(data) != {"status"} or data["status"] != expected:
    raise ValueError("status")
print("true")
STRICT_API_JSON
}

stage=api_identity
mapfile -t api_pids < <(/usr/bin/pgrep -x molin-api)
(( ${#api_pids[@]} == 1 ))
api_pid=${api_pids[0]}
[[ "$api_pid" =~ ^[1-9][0-9]*$ ]]

stage=api_environment
app_env=$(read_process_env "$api_pid" APP_ENV)
email_adapter=$(read_process_env "$api_pid" EMAIL_ADAPTER)
[[ "$app_env" == test && -n "$email_adapter" && "$email_adapter" != mock ]]

stage=health_transport
health_response=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --write-out $'\n%{http_code}' http://127.0.0.1:8080/api/health)
health_code=${health_response##*$'\n'}
health_body=${health_response%$'\n'*}
[[ "$health_code" == 200 ]]
stage=health_json
[[ "$(strict_api_status ok "$health_body")" == true ]]

stage=ready_transport
ready_response=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --write-out $'\n%{http_code}' http://127.0.0.1:8080/api/ready)
ready_code=${ready_response##*$'\n'}
ready_body=${ready_response%$'\n'*}
[[ "$ready_code" == 200 ]]
stage=ready_json
[[ "$(strict_api_status ready "$ready_body")" == true ]]

stage=required_environment
mysql_database=$(read_process_env "$api_pid" MYSQL_DATABASE)
mysql_user=$(read_process_env "$api_pid" MYSQL_USER)
mysql_password=$(read_process_env "$api_pid" MYSQL_PASSWORD)
redis_password=$(read_process_env "$api_pid" REDIS_PASSWORD)
redis_db=$(read_process_env "$api_pid" REDIS_DB)
[[ "$mysql_database" == molin && "$mysql_user" =~ ^[A-Za-z0-9_]+$ && -n "$mysql_password" && "$redis_db" =~ ^[0-9]+$ ]]

stage=container_identity
mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
redis_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}
  container_identity=${container_line#*|}
  container_identity=${container_identity,,}
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in
    *mysql*) mysql_ids+=("$container_id") ;;
    *redis*) redis_ids+=("$container_id") ;;
  esac
done
(( ${#mysql_ids[@]} == 1 && ${#redis_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}
redis_id=${redis_ids[0]}

stage=recovery_gate
rollback_dir=/home/pc/molin/rollback
recovery_file="${rollback_dir}/__MOLIN_RECOVERY_FILENAME__"
[[ "$recovery_file" =~ ^/home/pc/molin/rollback/molin-email-unknown-([a-f0-9]{32})\.sql$ ]]
parent_dirs=(/home/pc /home/pc/molin "$rollback_dir")
parent_identities=()
for parent_dir in "${parent_dirs[@]}"; do
  [[ -d "$parent_dir" && ! -L "$parent_dir" ]]
  [[ "$(/usr/bin/stat -c '%u' -- "$parent_dir")" == "$(/usr/bin/id -u)" ]]
  parent_mode=$(/usr/bin/stat -c '%a' -- "$parent_dir")
  [[ "$parent_mode" =~ ^[0-7]{3,4}$ && $(( 8#$parent_mode & 022 )) == 0 ]]
  parent_identities+=("$(/usr/bin/stat -c '%u:%a:%d:%i' -- "$parent_dir")")
done
rollback_identity=$(/usr/bin/stat -c '%u:%a:%d:%i' -- "$rollback_dir")
[[ -O "$rollback_dir" ]]
rollback_mode=$(/usr/bin/stat -c '%a' -- "$rollback_dir")
[[ "$rollback_mode" =~ ^[0-7]{3,4}$ && $(( 8#$rollback_mode & 022 )) == 0 ]]
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_metadata=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")
[[ "$recovery_metadata" =~ ^$(/usr/bin/id -u):600:[0-9]+:[0-9]+:([1-9][0-9]*)$ ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
expected_recovery_sha=__MOLIN_EXPECTED_RECOVERY_SHA256__
[[ "$expected_recovery_sha" =~ ^[a-f0-9]{64}$ && "$expected_recovery_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
[[ "$recovery_sha" == "$expected_recovery_sha" ]]

stage=recovery_identity
# 恢复点解析器从模板行唯一识别 fixture nonce；文件名 nonce 只约束 artifact 格式，不参与业务身份计算。
identity_json=$(/usr/bin/python3 - "$recovery_file" <<'RECOVERY_IDENTITY'
import hashlib
import hmac
import json
import os
import re
import sys

path = sys.argv[1]
artifact_match = re.fullmatch(r"molin-email-unknown-([a-f0-9]{32})\.sql", os.path.basename(path))
if not artifact_match:
    raise ValueError("name")
fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
try:
    with os.fdopen(fd, "r", encoding="utf-8", errors="strict") as stream:
        raw_dump = stream.read()
except Exception:
    raise

lines = raw_dump.splitlines(keepends=True)
if not lines or not re.fullmatch(r"-- MySQL dump 10\.13  Distrib [^\r\n]+\n", lines[0]):
    raise ValueError("header")
if len(lines) < 3 or lines[1] != "--\n" or not re.fullmatch(r"-- Host: [A-Za-z0-9_.:-]+[ \t]+Database: molin\n", lines[2]):
    raise ValueError("header_database")
completion_line_pattern = r"-- Dump completed(?: on [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})?"
dated_variable_width_spaced_pattern = r"-- Dump completed on ([0-9]{4})-([0-9]{1,2})-([0-9]{1,2}) {2,8}([0-9]{1,2}):([0-9]{1,2}):([0-9]{1,2})"

def is_completion_line(line):
    # 原有无日期和固定宽度日期白名单保持不变；可变宽格式使用独立分支，避免放宽原契约。
    if re.fullmatch(completion_line_pattern, line):
        return True
    match = re.fullmatch(dated_variable_width_spaced_pattern, line)
    if not match:
        return False
    _, month_text, day_text, hour_text, minute_text, second_text = match.groups()
    month, day, hour, minute, second = map(int, (month_text, day_text, hour_text, minute_text, second_text))
    # 仅校验明确的日历和时钟边界；不推断具体月份天数或闰年，避免引入新语义。
    return 1 <= month <= 12 and 1 <= day <= 31 and 0 <= hour <= 23 and 0 <= minute <= 59 and 0 <= second <= 59
# 兼容 mysqldump 固定宽度、无日期及已确认的可变宽多空格结束行；三者都必须紧贴完整文件 EOF。
completion_at_eof = re.search(r"(?m)^(-- Dump completed[^\r\n]*)(?:\r?\n)?\Z", raw_dump)
if not completion_at_eof or not is_completion_line(completion_at_eof.group(1)):
    raise ValueError("completion")
if sum(1 for line in raw_dump.splitlines() if is_completion_line(line)) != 1:
    raise ValueError("completion_count")

class Token:
    def __init__(self, kind, text, start, end):
        self.kind = kind
        self.text = text
        self.start = start
        self.end = end

class Statement:
    def __init__(self, tokens, start, end):
        self.tokens = tokens
        self.start = start
        self.end = end

    def core(self):
        return raw_dump[self.tokens[0].start:self.end].strip()

def executable_comment_view(text):
    # MySQL 版本注释会执行内部 SQL；保持等长映射，确保内部内容复用普通 SQL 的词法与语句边界检查。
    view = list(text)
    index = 0
    while index < len(text):
        char = text[index]
        if char == "#" or (char == "-" and index + 2 < len(text) and text[index:index + 2] == "--" and text[index + 2].isspace()):
            newline = text.find("\n", index)
            index = len(text) if newline < 0 else newline + 1
            continue
        if char in ("'", '"', "`"):
            quote = char
            index += 1
            escaped = False
            while index < len(text):
                current = text[index]
                if quote != "`" and escaped:
                    escaped = False
                elif quote != "`" and current == "\\":
                    escaped = True
                elif current == quote:
                    if index + 1 < len(text) and text[index + 1] == quote:
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
            else:
                raise ValueError("quoted_text")
            continue
        if text.startswith("/*", index):
            close = text.find("*/", index + 2)
            if close < 0:
                raise ValueError("block_comment")
            if text.startswith("/*!", index):
                body_start = index + 3
                if text.find("/*", body_start, close) >= 0:
                    raise ValueError("executable_comment_nested")
                cursor = body_start
                while cursor < close and text[cursor].isspace():
                    cursor += 1
                if cursor < close and text[cursor].isdigit():
                    version_start = cursor
                    while cursor < close and text[cursor].isdigit():
                        cursor += 1
                    if cursor - version_start not in (5, 6) or cursor >= close or not text[cursor].isspace():
                        raise ValueError("executable_comment_version")
                    while cursor < close and text[cursor].isspace():
                        cursor += 1
                if cursor >= close:
                    raise ValueError("executable_comment_empty")
                # 仅抹除包装、版本号和前导空白；保留换行以识别跨行目标语句。
                for position in list(range(index, cursor)) + list(range(close, close + 2)):
                    if view[position] not in ("\r", "\n"):
                        view[position] = " "
            index = close + 2
            continue
        index += 1
    return "".join(view)

def scan_sql(text):
    scan_text = executable_comment_view(text)
    statements = []
    tokens = []
    statement_start = 0
    index = 0
    while index < len(scan_text):
        char = scan_text[index]
        if char.isspace():
            index += 1
            continue
        if char == "#" or (char == "-" and index + 2 < len(scan_text) and scan_text[index:index + 2] == "--" and scan_text[index + 2].isspace()):
            newline = scan_text.find("\n", index)
            index = len(scan_text) if newline < 0 else newline + 1
            continue
        if scan_text.startswith("/*", index):
            close = scan_text.find("*/", index + 2)
            if close < 0:
                raise ValueError("block_comment")
            index = close + 2
            continue
        if char in ("'", '"'):
            quote = char
            start = index
            index += 1
            escaped = False
            while index < len(scan_text):
                current = scan_text[index]
                if escaped:
                    escaped = False
                elif current == "\\":
                    escaped = True
                elif current == quote:
                    if index + 1 < len(scan_text) and scan_text[index + 1] == quote:
                        index += 1
                    else:
                        index += 1
                        break
                index += 1
            else:
                raise ValueError("string")
            tokens.append(Token("STRING", scan_text[start:index], start, index))
            continue
        if char == "`":
            start = index
            index += 1
            value = []
            while index < len(scan_text):
                if scan_text[index] == "`":
                    if index + 1 < len(scan_text) and scan_text[index + 1] == "`":
                        value.append("`")
                        index += 2
                        continue
                    index += 1
                    break
                value.append(scan_text[index])
                index += 1
            else:
                raise ValueError("identifier")
            tokens.append(Token("IDENT", "".join(value), start, index))
            continue
        if char.isascii() and (char.isalnum() or char in "_$"):
            start = index
            while index < len(scan_text) and scan_text[index].isascii() and (scan_text[index].isalnum() or scan_text[index] in "_$"):
                index += 1
            tokens.append(Token("WORD", scan_text[start:index], start, index))
            continue
        tokens.append(Token("SYMBOL", char, index, index + 1))
        index += 1
        if char == ";":
            if tokens:
                statements.append(Statement(tokens, statement_start, index))
            tokens = []
            statement_start = index
    if tokens:
        raise ValueError("unterminated_statement")
    return statements

tables = {
    "email_send_logs": "logs",
    "email_test_recipient_allowlist": "allowlist",
    "email_provider_templates": "template",
    "schema_migrations": "schema",
}
prefixes = {name: f"INSERT INTO `{table}` VALUES " for table, name in tables.items()}
bodies = {name: [] for name in tables.values()}
creates = {table: [] for table in tables}

def upper(token):
    return token.text.upper() if token.kind in ("WORD", "IDENT") else token.text

def target_after(tokens, keyword_index, keyword):
    for position in range(keyword_index + 1, min(len(tokens), keyword_index + 8)):
        if upper(tokens[position]) == keyword:
            for candidate in tokens[position + 1:min(len(tokens), position + 6)]:
                normalized = candidate.text.lower() if candidate.kind in ("WORD", "IDENT") else ""
                if normalized in tables:
                    return normalized, position, tokens.index(candidate)
            return None, position, None
    return None, None, None

for statement in scan_sql(raw_dump):
    tokens = statement.tokens
    for token_index, token in enumerate(tokens):
        if upper(token) == "INSERT":
            table, into_index, table_index = target_after(tokens, token_index, "INTO")
            if table is None:
                continue
            name = tables[table]
            core = statement.core()
            prefix = prefixes[name]
            if token_index != 0 or into_index != 1 or table_index != 2 or len(tokens) < 5 or upper(tokens[3]) != "VALUES":
                raise ValueError("insert_tokens")
            if "\n" in core or not core.startswith(prefix) or not core.endswith(";"):
                raise ValueError("insert_shape")
            body = core[len(prefix):-1]
            if not body:
                raise ValueError("insert_body")
            bodies[name].append(body)
    for token_index, token in enumerate(tokens):
        if upper(token) == "CREATE":
            table, table_keyword_index, table_index = target_after(tokens, token_index, "TABLE")
            if table is None:
                continue
            if token_index != 0 or table_keyword_index != 1 or table_index != 2:
                raise ValueError("create_tokens")
            core = statement.core()
            expected_prefix = f"CREATE TABLE `{table}` ("
            if not core.startswith(expected_prefix) or not core.endswith(";"):
                raise ValueError("create_shape")
            creates[table].append((statement, table_index))

if any(len(values) != 1 for values in bodies.values()) or any(len(values) != 1 for values in creates.values()):
    raise ValueError("table_statement_count")

def structure_segments(statement, table_index):
    tokens = statement.tokens
    open_index = table_index + 1
    if open_index >= len(tokens) or tokens[open_index].text != "(":
        raise ValueError("create_open")
    depth = 0
    segment = []
    segments = []
    close_index = None
    for position in range(open_index, len(tokens)):
        token = tokens[position]
        if token.text == "(":
            depth += 1
            if depth > 1:
                segment.append(token)
        elif token.text == ")":
            depth -= 1
            if depth < 0:
                raise ValueError("create_depth")
            if depth == 0:
                if segment:
                    segments.append(segment)
                close_index = position
                break
            segment.append(token)
        elif token.text == "," and depth == 1:
            if not segment:
                raise ValueError("create_segment")
            segments.append(segment)
            segment = []
        elif depth >= 1:
            segment.append(token)
    if close_index is None or close_index + 1 >= len(tokens) or upper(tokens[close_index + 1]) != "ENGINE":
        raise ValueError("create_close")
    return segments, tokens[close_index + 1:]

def ddl_signature(tokens):
    result = []
    for token in tokens:
        value = upper(token)
        # SHOW CREATE TABLE 可能给数值默认值加单引号；DDL 契约按相同数值语义归一化。
        if token.kind == "STRING" and re.fullmatch(r"'[0-9]+'", token.text):
            value = token.text[1:-1]
        result.append(value)
    return result

expected_columns = {
    "email_send_logs": [
        "ID BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
        "BUSINESS_REQUEST_NO VARCHAR ( 64 ) NOT NULL",
        "VERIFICATION_CODE_ID BIGINT UNSIGNED DEFAULT NULL",
        "TEMPLATE_ID BIGINT UNSIGNED NOT NULL",
        "PROVIDER_TEMPLATE_ID VARCHAR ( 64 ) NOT NULL",
        "SCENE VARCHAR ( 32 ) NOT NULL",
        "PURPOSE VARCHAR ( 16 ) NOT NULL",
        "RECIPIENT_HMAC CHAR ( 64 ) CHARACTER SET ASCII COLLATE ASCII_BIN NOT NULL",
        "RECIPIENT_MASKED VARCHAR ( 191 ) NOT NULL",
        "IDEMPOTENCY_SCOPE VARCHAR ( 191 ) NOT NULL",
        "IDEMPOTENCY_KEY_HASH CHAR ( 64 ) CHARACTER SET ASCII COLLATE ASCII_BIN NOT NULL",
        "REQUEST_FINGERPRINT CHAR ( 64 ) CHARACTER SET ASCII COLLATE ASCII_BIN NOT NULL",
        "PROVIDER VARCHAR ( 32 ) NOT NULL",
        "PROVIDER_REQUEST_ID VARCHAR ( 128 ) DEFAULT NULL",
        "STATUS VARCHAR ( 16 ) NOT NULL",
        "FAILURE_REASON VARCHAR ( 64 ) DEFAULT NULL",
        "EXPIRES_AT DATETIME DEFAULT NULL",
        "SUBMITTED_AT DATETIME NOT NULL",
        "CREATED_AT DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
    ],
    "email_test_recipient_allowlist": [
        "ID BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
        "EMAIL_HMAC CHAR ( 64 ) CHARACTER SET ASCII COLLATE ASCII_BIN NOT NULL",
        "EMAIL_MASKED VARCHAR ( 191 ) NOT NULL",
        "STATUS VARCHAR ( 16 ) NOT NULL",
        "VERSION BIGINT UNSIGNED NOT NULL DEFAULT 1",
        "CREATED_BY BIGINT UNSIGNED NOT NULL",
        "UPDATED_BY BIGINT UNSIGNED NOT NULL",
        "CREATED_AT DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
        "UPDATED_AT DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP",
        "REVOKED_AT DATETIME DEFAULT NULL",
    ],
    "email_provider_templates": [
        "ID BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
        "PROVIDER VARCHAR ( 32 ) NOT NULL",
        "PROVIDER_TEMPLATE_ID VARCHAR ( 64 ) NOT NULL",
        "NAME VARCHAR ( 64 ) NOT NULL",
        "SUBJECT VARCHAR ( 256 ) NOT NULL",
        "SENDER_NICKNAME VARCHAR ( 64 ) DEFAULT NULL",
        "TEMPLATE_TEXT MEDIUMTEXT NOT NULL",
        "VARIABLES_JSON JSON NOT NULL",
        "CONTENT_SHA256 CHAR ( 64 ) CHARACTER SET ASCII COLLATE ASCII_BIN NOT NULL",
        "PROVIDER_STATUS VARCHAR ( 16 ) NOT NULL",
        "REVIEW_COMMENT VARCHAR ( 512 ) DEFAULT NULL",
        "VARIABLES_COMPLETE TINYINT ( 1 ) NOT NULL DEFAULT 0",
        "LOCAL_ENABLED TINYINT ( 1 ) NOT NULL DEFAULT 0",
        "MISSING TINYINT ( 1 ) NOT NULL DEFAULT 0",
        "MISSING_SINCE DATETIME DEFAULT NULL",
        "PROVIDER_CREATED_AT DATETIME DEFAULT NULL",
        "LAST_SYNCED_AT DATETIME NOT NULL",
        "VERSION BIGINT UNSIGNED NOT NULL DEFAULT 1",
        "CREATED_AT DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
        "UPDATED_AT DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP",
    ],
}
expected_columns = {table: [definition.split() for definition in definitions] for table, definitions in expected_columns.items()}
expected_table_options = "ENGINE = INNODB DEFAULT CHARSET = UTF8MB4 COLLATE = UTF8MB4_0900_AI_CI ;".split()
auto_increment_tables = {"email_send_logs", "email_test_recipient_allowlist", "email_provider_templates"}

def normalized_table_options(table, tokens):
    signature = ddl_signature(tokens)
    # mysqldump 会把当前自增序列写成动态表选项；仅剔除三张确有自增主键业务表中位于 ENGINE 后的一次正整数值。
    dynamic_prefix = ["ENGINE", "=", "INNODB", "AUTO_INCREMENT", "="]
    if table in auto_increment_tables and signature[:5] == dynamic_prefix and len(signature) > 5 and re.fullmatch(r"[1-9][0-9]*", signature[5]):
        signature = signature[:3] + signature[6:]
    return signature

for table, values in creates.items():
    segments, table_options = structure_segments(*values[0])
    if not segments:
        raise ValueError("create_empty")
    if normalized_table_options(table, table_options) != expected_table_options:
        raise ValueError("table_options")
    if table == "schema_migrations":
        if len(segments) != 3:
            raise ValueError("schema_ddl_count")
        version, dirty, primary = segments
        if [upper(token) for token in version] != ["VERSION", "BIGINT", "NOT", "NULL"]:
            raise ValueError("schema_version_column")
        if [upper(token) for token in dirty] != ["DIRTY", "TINYINT", "(", "1", ")", "NOT", "NULL"]:
            raise ValueError("schema_dirty_column")
        if [upper(token) for token in primary] != ["PRIMARY", "KEY", "(", "VERSION", ")"]:
            raise ValueError("schema_primary_key")
        continue
    columns = [ddl_signature(segment) for segment in segments if segment and segment[0].kind == "IDENT"]
    if columns != expected_columns[table]:
        raise ValueError("business_columns")
    primary_keys = [ddl_signature(segment) for segment in segments if segment and upper(segment[0]) == "PRIMARY"]
    if primary_keys != [["PRIMARY", "KEY", "(", "ID", ")"]]:
        raise ValueError("business_primary_key")

def split_items(raw, separator):
    values, start, quoted, escaped, depth = [], 0, False, False, 0
    for index, char in enumerate(raw):
        if quoted:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == "'":
                quoted = False
            continue
        if char == "'":
            quoted = True
        elif char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
            if depth < 0:
                raise ValueError("depth")
        elif char == separator and depth == 0:
            values.append(raw[start:index])
            start = index + 1
    if quoted or escaped or depth != 0:
        raise ValueError("syntax")
    values.append(raw[start:])
    return values

def rows(raw):
    result = []
    for item in split_items(raw, ","):
        if len(item) < 2 or item[0] != "(" or item[-1] != ")":
            raise ValueError("tuple")
        result.append(split_items(item[1:-1], ","))
    return result

escapes = {"0": "\0", "b": "\b", "n": "\n", "r": "\r", "t": "\t", "Z": "\x1a", "\\": "\\", "'": "'", '"': '"'}
def scalar(token):
    if token == "NULL":
        return None
    if re.fullmatch(r"[0-9]+", token):
        return int(token)
    if len(token) >= 2 and token[0] == "'" and token[-1] == "'":
        raw, output, index = token[1:-1], [], 0
        while index < len(raw):
            if raw[index] == "\\":
                index += 1
                if index >= len(raw) or raw[index] not in escapes:
                    raise ValueError("escape")
                output.append(escapes[raw[index]])
            else:
                output.append(raw[index])
            index += 1
        return "".join(output)
    raise ValueError("scalar")

parsed = {name: [list(map(scalar, row)) for body in values for row in rows(body)] for name, values in bodies.items()}
if parsed["schema"] != [[57, 0]]:
    raise ValueError("schema")
template_nonce_candidates = []
for row in parsed["template"]:
    if len(row) == 20 and isinstance(row[2], str):
        match = re.fullmatch(r"qa-phase4-([a-f0-9]{32})", row[2])
        if match:
            template_nonce_candidates.append(match.group(1))
if len(template_nonce_candidates) != 1:
    raise ValueError("fixture_nonce")
nonce = template_nonce_candidates[0]
email = f"phase4-{nonce}@example.invalid"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
provider_template = f"qa-phase4-{nonce}"
old_hash = hashlib.sha256(f"phase4-old-{nonce}".encode()).hexdigest()
new_hash = hashlib.sha256(f"phase4-new-{nonce}".encode()).hexdigest()

log_candidates = [row for row in parsed["logs"] if len(row) == 19 and row[7] == recipient_hmac]
if len(log_candidates) != 2:
    raise ValueError("logs")
template_ids = {row[3] for row in log_candidates}
operators = set()
if len(template_ids) != 1 or {row[10] for row in log_candidates} != {old_hash, new_hash}:
    raise ValueError("log_identity")
scope_values = {row[9] for row in log_candidates}
if len(scope_values) != 1:
    raise ValueError("scope")
scope = next(iter(scope_values))
if len([row for row in parsed["logs"] if len(row) == 19 and row[9] == scope]) != 2:
    raise ValueError("scope_rows")
if any(row[4] != provider_template or row[5] != "register" or row[6] != "test" or row[12] != "aliyun_directmail" or row[14] != "failed" or row[15] != "provider_outcome_unknown" for row in log_candidates):
    raise ValueError("log_contract")

allow_candidates = [row for row in parsed["allowlist"] if len(row) == 10 and row[1] == recipient_hmac]
template_candidates = [row for row in parsed["template"] if len(row) == 20 and row[2] == provider_template]
if len(allow_candidates) != 1 or len(template_candidates) != 1:
    raise ValueError("related")
allowlist = allow_candidates[0]
template = template_candidates[0]
if allowlist[5] != allowlist[6] or allowlist[3] != "active" or allowlist[4] != 1 or allowlist[9] is not None:
    raise ValueError("allowlist")
operator_id = allowlist[5]
template_id = next(iter(template_ids))
expected_scope = f"admin-email-template-test:admin:{operator_id}:template:{template_id}:scene:register:recipient:{recipient_hmac}"
if scope != expected_scope or template[0] != template_id or template[1] != "aliyun_directmail":
    raise ValueError("binding")
lock_key = "lock:email:dispatch:" + hmac.new(b"qa-phase4-idempotency-secret-32-bytes", scope.encode(), hashlib.sha256).hexdigest()
primary = next(row for row in log_candidates if row[10] == old_hash)
unexpected = next(row for row in log_candidates if row[10] == new_hash)
result = {
    "operator_id": operator_id,
    "template_id": template_id,
    "allowlist_id": allowlist[0],
    "primary_id": primary[0],
    "unexpected_id": unexpected[0],
    "recipient_hmac": recipient_hmac,
    "scope_hex": scope.encode().hex(),
    "provider_template_hex": provider_template.encode().hex(),
    "lock_key": lock_key,
}
print(json.dumps(result, separators=(",", ":"), sort_keys=True))
RECOVERY_IDENTITY
)

stage=identity_json
identity_values=$(/usr/bin/python3 - "$identity_json" <<'STRICT_IDENTITY_JSON'
import json
import re
import sys

raw = sys.argv[1]
def no_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate")
        value[key] = item
    return value

value = json.loads(raw, object_pairs_hook=no_duplicates)
required = {"operator_id", "template_id", "allowlist_id", "primary_id", "unexpected_id", "recipient_hmac", "scope_hex", "provider_template_hex", "lock_key"}
if not isinstance(value, dict) or set(value) != required:
    raise ValueError("fields")
for key in ("operator_id", "template_id", "allowlist_id", "primary_id", "unexpected_id"):
    if type(value[key]) is not int or value[key] <= 0:
        raise ValueError("ids")
if value["primary_id"] == value["unexpected_id"]:
    raise ValueError("duplicate_id")
if not re.fullmatch(r"[a-f0-9]{64}", value["recipient_hmac"]):
    raise ValueError("hmac")
for key in ("scope_hex", "provider_template_hex"):
    if not re.fullmatch(r"(?:[a-f0-9]{2})+", value[key]):
        raise ValueError("hex")
if not re.fullmatch(r"lock:email:dispatch:[a-f0-9]{64}", value["lock_key"]):
    raise ValueError("lock")
print("\t".join(str(value[key]) for key in ("operator_id", "template_id", "allowlist_id", "primary_id", "unexpected_id", "recipient_hmac", "scope_hex", "provider_template_hex", "lock_key")))
STRICT_IDENTITY_JSON
)
IFS=$'\t' read -r operator_id template_id allowlist_id primary_id unexpected_id recipient_hmac scope_hex provider_template_hex lock_key <<< "$identity_values"
for value in "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id"; do [[ "$value" =~ ^[1-9][0-9]*$ ]]; done
[[ "$primary_id" != "$unexpected_id" && "$recipient_hmac" =~ ^[a-f0-9]{64}$ && "$scope_hex" =~ ^([a-f0-9]{2})+$ && "$provider_template_hex" =~ ^([a-f0-9]{2})+$ && "$lock_key" =~ ^lock:email:dispatch:[a-f0-9]{64}$ ]]

stage=schema_query
schema_result=$(mysql_scalar 'SELECT CONCAT(version, CHAR(9), IF(dirty,1,0), CHAR(9), (SELECT COUNT(*) FROM schema_migrations)) FROM schema_migrations LIMIT 1;')
IFS=$'\t' read -r schema_version schema_dirty schema_rows <<< "$schema_result"
stage=schema_gate
[[ "$schema_version" == 57 && "$schema_dirty" == 0 && "$schema_rows" == 1 ]]

stage=fixture_query
fixture_result=$(mysql_scalar "SELECT CONCAT(
  (SELECT COUNT(*) FROM email_send_logs WHERE id IN (${primary_id},${unexpected_id})), CHAR(9),
  (SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4)), CHAR(9),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} OR email_hmac='${recipient_hmac}'), CHAR(9),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} OR provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4))
);")
IFS=$'\t' read -r log_count scope_count allowlist_count template_count <<< "$fixture_result"
stage=fixture_absence
[[ "$log_count" == 0 && "$scope_count" == 0 && "$allowlist_count" == 0 && "$template_count" == 0 ]]

stage=redis_ping
[[ "$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" PING)" == PONG ]]
stage=redis_exists
[[ "$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" EXISTS "$lock_key")" == 0 ]]

stage=binary_gate
cleanup_binary=/home/pc/molin/rollback/email-unknown-restart-cleanup.test
expected_binary_sha=__MOLIN_EXPECTED_CLEANUP_BINARY_SHA256__
[[ "$expected_binary_sha" =~ ^[a-f0-9]{64}$ && "$expected_binary_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
binary_metadata=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")
[[ "$binary_metadata" =~ ^$(/usr/bin/id -u):500:[0-9]+:[0-9]+:([1-9][0-9]*)$ ]]
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]

stage=cycle_metadata
expected_cycle_dump_shas=(__MOLIN_EXPECTED_CYCLE_DUMP_SHA256_ONE__ __MOLIN_EXPECTED_CYCLE_DUMP_SHA256_TWO__)
[[ "${expected_cycle_dump_shas[0]}" =~ ^[a-f0-9]{64}$ && "${expected_cycle_dump_shas[1]}" =~ ^[a-f0-9]{64}$ && "${expected_cycle_dump_shas[0]}" != "${expected_cycle_dump_shas[1]}" ]]
mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print | /usr/bin/sort)
(( ${#cycle_markers[@]} == 2 ))
cycle_targets=()
cycle_dirs=()
cycle_evidence_dirs=()
cycle_dumps=()
cycle_dir_identities=()
cycle_evidence_identities=()
cycle_marker_identities=()
cycle_marker_shas=()
cycle_dump_identities=()
cycle_dump_shas=()
for cycle_marker in "${cycle_markers[@]}"; do
  [[ "$cycle_marker" =~ ^(/root/molin-000057-schema57-cycle-run-([a-f0-9]{32}))/evidence/cycle_completed$ ]]
  cycle_dir=${BASH_REMATCH[1]}
  cycle_evidence_dir="${cycle_dir}/evidence"
  cycle_target="molin_restore_57_reverify_${BASH_REMATCH[2]}"
  cycle_dump="${cycle_dir}/evidence/molin_source_schema57.sql"
  [[ "$cycle_target" != "$mysql_database" ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dir" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dir" -mindepth 0 -maxdepth 0 -type d -print)" == "$cycle_dir" ]]
  cycle_dir_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dir")
  [[ "$cycle_dir_identity" =~ ^0:700:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_evidence_dir" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_evidence_dir" -mindepth 0 -maxdepth 0 -type d -print)" == "$cycle_evidence_dir" ]]
  cycle_evidence_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_evidence_dir")
  [[ "$cycle_evidence_identity" =~ ^0:700:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_marker" ]]
  cycle_marker_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_marker")
  [[ "$cycle_marker_identity" =~ ^0:600:[0-9]+:[0-9]+:[0-9]+$ ]]
  cycle_marker_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_marker" | /usr/bin/awk '{print $1}')
  [[ "$cycle_marker_sha" =~ ^[a-f0-9]{64}$ ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_dump" ]]
  cycle_dump_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dump")
  [[ "$cycle_dump_identity" =~ ^0:600:[0-9]+:[0-9]+:([1-9][0-9]*)$ ]]
  cycle_dump_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
  [[ "$cycle_dump_sha" == "${expected_cycle_dump_shas[0]}" || "$cycle_dump_sha" == "${expected_cycle_dump_shas[1]}" ]]
  cycle_targets+=("$cycle_target")
  cycle_dirs+=("$cycle_dir")
  cycle_evidence_dirs+=("$cycle_evidence_dir")
  cycle_dumps+=("$cycle_dump")
  cycle_dir_identities+=("$cycle_dir_identity")
  cycle_evidence_identities+=("$cycle_evidence_identity")
  cycle_marker_identities+=("$cycle_marker_identity")
  cycle_marker_shas+=("$cycle_marker_sha")
  cycle_dump_identities+=("$cycle_dump_identity")
  cycle_dump_shas+=("$cycle_dump_sha")
done
[[ "${cycle_targets[0]}" != "${cycle_targets[1]}" ]]
[[ "${cycle_dump_shas[0]}" != "${cycle_dump_shas[1]}" ]]

stage=cycle_schema
cycle_schema_count=0
for cycle_target in "${cycle_targets[@]}"; do
  [[ "$(cycle_schema_root_scalar "$cycle_target")" == $'57\t0\t69\t1\t1' ]]
  cycle_schema_count=$(( cycle_schema_count + 1 ))
done
[[ "$cycle_schema_count" == 2 ]]

stage=final_artifacts
# 在所有查询完成后再次核对恢复点和冻结二进制，防止只读窗口内发生替换或内容漂移。
for index in 0 1 2; do
  [[ -d "${parent_dirs[$index]}" && ! -L "${parent_dirs[$index]}" ]]
  [[ "$(/usr/bin/stat -c '%u:%a:%d:%i' -- "${parent_dirs[$index]}")" == "${parent_identities[$index]}" ]]
done
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i' -- "$rollback_dir")" == "$rollback_identity" ]]
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")" == "$recovery_metadata" ]]
[[ "$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')" == "$recovery_sha" ]]
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")" == "$binary_metadata" ]]
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
for index in 0 1; do
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dirs[$index]}" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dirs[$index]}" -mindepth 0 -maxdepth 0 -type d -print)" == "${cycle_dirs[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_dirs[$index]}")" == "${cycle_dir_identities[$index]}" ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_evidence_dirs[$index]}" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_evidence_dirs[$index]}" -mindepth 0 -maxdepth 0 -type d -print)" == "${cycle_evidence_dirs[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_evidence_dirs[$index]}")" == "${cycle_evidence_identities[$index]}" ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type f -print)" == "${cycle_markers[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_markers[$index]}")" == "${cycle_marker_identities[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_markers[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_marker_shas[$index]}" ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dumps[$index]}" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dumps[$index]}" -mindepth 0 -maxdepth 0 -type f -print)" == "${cycle_dumps[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_dumps[$index]}")" == "${cycle_dump_identities[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_dumps[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_dump_shas[$index]}" ]]
done

trap - ERR
printf 'status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0\n'

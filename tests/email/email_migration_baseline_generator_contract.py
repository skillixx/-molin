#!/usr/bin/env python3
"""离线验证 migration 基线生成器默认关闭且只操作独立临时 MySQL。"""

from __future__ import annotations

import hashlib
import os
import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "generate-email-migration-baselines.sh"
MIGRATIONS = ROOT / "server" / "migrations"
EXPECTED_SET_SHA = "DE8D942A3C8BBB3E96456C1B85AE0BADAE7542E2A3E6FE0C34FD47C6140D914D"


class ContractError(RuntimeError):
    """表示生成器偏离隔离和失败关闭契约。"""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def bash() -> str:
    candidates = (shutil.which("bash"), r"C:\Program Files\Git\bin\bash.exe")
    result = next((item for item in candidates if item and pathlib.Path(item).is_file()), None)
    require(result is not None, "bash_missing")
    return result


def migration_set_sha() -> str:
    lines = []
    for version in range(1, 57):
        matches = sorted(MIGRATIONS.glob(f"{version:06d}_*.up.sql"))
        require(len(matches) == 1, "migration_set_shape")
        lines.append(f"{hashlib.sha256(matches[0].read_bytes()).hexdigest().upper()}\t{matches[0].name}\n")
    return hashlib.sha256("".join(lines).encode("ascii")).hexdigest().upper()


def errors(text: str) -> list[str]:
    found: list[str] = []
    required = (
        "I_CONFIRM_EMAIL_MIGRATION_BASELINE_GENERATION_ONCE",
        "I_UNDERSTAND_TEMPORARY_NETWORKLESS_MYSQL8_WILL_BE_CREATED",
        f"readonly expected_migration_set_sha={EXPECTED_SET_SHA}",
        "readonly awk_link=/usr/bin/awk",
        "readonly realpath_bin=/usr/bin/realpath",
        'awk_bin=$("$realpath_bin" -e -- "$awk_link")',
        '[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ ]]',
        '[[ -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]]',
        '[[ "$("$stat_bin" -c \'%U:%G\' -- "$awk_bin")" = root:root ]]',
        '[[ ${MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE:-} = "$execute_gate" ]] || blocked',
        '[[ "$image_ref" =~ ^mysql@sha256:[a-f0-9]{64}$ ]]',
        '[[ "$expected_image_id" =~ ^sha256:[a-f0-9]{64}$ ]]',
        "--network none",
        "--read-only",
        "MYSQL_ALLOW_EMPTY_PASSWORD=yes",
        'container_id=$("$docker_bin" run',
        '"$docker_bin" rm --force -- "$container_id"',
        'mysql --no-defaults --version',
        'mysql --no-defaults --default-character-set=utf8mb4 --protocol=socket',
        'mysqldump --no-defaults --default-character-set=utf8mb4 --protocol=socket',
        "^mysql[[:space:]]+Ver[[:space:]]+8\\.[0-9]+\\.[0-9]+[[:space:]]+for[[:space:]]+Linux",
        "MySQL[[:space:]]+Community[[:space:]]+Server",
        "mysql8_runtime_verified=true",
        "CREATE DATABASE molin_baseline",
        "for index in $(seq 1 54)",
        "apply_migration 000055",
        "apply_migration 000056",
        "classification=migration_sql mysql_error_code=%s sqlstate=%s sql_line=%s",
        "check_fingerprints=%s outputs_created=false retained=false",
        "000056:3819:HY000:113",
        "OCTET_LENGTH(clause_compact)",
        "LOWER(SHA2(clause_compact, 256))",
        "ORDER BY FIELD(constraint_name",
        "tc.table_name = 'verification_codes'",
        "tc.constraint_type = 'CHECK'",
        "tc.enforced = 'YES'",
        "chk_verification_code_hash",
        "chk_verification_send_status",
        "chk_verification_target_type",
        "chk_verification_target_shape",
        "chk_verification_email_acceptance",
        "chk_verification_email_idempotency",
        "chk_verification_request_fingerprint",
        "chk_verification_target_hash",
        "^[0-9]+:[a-f0-9]{64}(,[0-9]+:[a-f0-9]{64}){7}$",
        "([0-9]{4})",
        "([A-Z0-9]{5})",
        "([0-9]{1,6})",
        '"$stage" "$mysql_error_code" "$mysql_sqlstate" "$mysql_sql_line"',
        "schema54-empty.sql",
        "schema54-legacy.sql",
        "schema55.sql",
        "schema56.sql",
        "000055-baseline-manifest.tsv",
        "000056-baseline-manifest.tsv",
        "set -o noclobber",
        '$0 == "/*!999999" sprintf("%c", 92) "- enable the sandbox mode */" { next }',
        "@saved_cs_client[[:space:]]*=[[:space:]]*@@character_set_client",
        "character_set_client[[:space:]]*=[[:space:]]*utf8mb4",
        "character_set_client[[:space:]]*=[[:space:]]*@saved_cs_client",
        "\\/\\*!|\\/\\*\\+/ { exit 42 }",
        "if LC_ALL=C \"$grep_bin\" -Eq '/\\*!|/\\*\\+' \"$destination\"; then fail dump_executable_comment; fi",
        "其余可执行注释继续失败关闭",
        'created_outputs+=("$destination")',
        '"$cat_bin" "$temporary_dir/$name" >> "$destination"',
        "outputs=6",
    )
    for item in required:
        if item not in text:
            found.append(f"missing:{item}")
    if text.count('mysql --no-defaults --default-character-set=utf8mb4 --protocol=socket') != 2:
        found.append("mysql_utf8_invocation_count")
    if text.count('mysqldump --no-defaults --default-character-set=utf8mb4 --protocol=socket') != 1:
        found.append("mysqldump_utf8_invocation_count")
    argument_gate = text.find("[[ $# -eq 2 ]] || blocked")
    environment_gate = text.find('[[ ${MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE:-}')
    first_docker = text.find('"$docker_bin" image inspect')
    if not (0 <= argument_gate < environment_gate < first_docker):
        found.append("gate_order")
    forbidden = (
        "docker pull", "--publish", "-p 3306", "--network host", "--database=molin ",
        "DROP DATABASE", "FLUSHDB", "FLUSHALL", "KEYS *", "rm -rf", "MYSQL_ROOT_PASSWORD",
    )
    for item in forbidden:
        if item.lower() in text.lower():
            found.append(f"forbidden:{item}")
    if re.search(r'(?m)^\s*[^#\n]*docker\s+rm\s+--force\s+--\s+"\$container_(?:name|id)\*', text):
        found.append("wildcard_container_cleanup")
    return found


def mutate(text: str) -> dict[str, str]:
    return {
        "remove_argument_gate": text.replace("[[ $# -eq 2 ]] || blocked", ":", 1),
        "remove_environment_gate": text.replace('[[ ${MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE:-} = "$execute_gate" ]] || blocked', ":", 1),
        "bypass_awk_resolution": text.replace('awk_bin=$("$realpath_bin" -e -- "$awk_link")', 'awk_bin="$awk_link"', 1),
        "widen_awk_boundary": text.replace('^/usr/bin/[A-Za-z0-9._+-]+$', '^/.*$', 1),
        "weaken_awk_owner": text.replace('= root:root ]]', '= pc:pc ]]', 1),
        "network_host": text.replace("--network none", "--network host", 1),
        "publish_port": text.replace("--network none", "--network none --publish 3306:3306", 1),
        "image_tag": text.replace('^mysql@sha256:[a-f0-9]{64}$', '^mysql:8$', 1),
        "remove_image_id": text.replace('[[ "$expected_image_id" =~ ^sha256:[a-f0-9]{64}$ ]]', ":", 1),
        "enable_pull": text + "\ndocker pull mysql:8\n",
        "select_main": text + '\nmysql --database=molin --execute="SELECT 1"\n',
        "remove_read_only": text.replace("--network none --read-only", "--network none", 1),
        "remove_noclobber": text.replace("set -o noclobber", ":", 1),
        "widen_sandbox_header": text.replace('$0 == "/*!999999" sprintf("%c", 92) "- enable the sandbox mode */"', '$0 ~ /sandbox/', 1),
        "widen_charset_wrapper": text.replace("utf8mb4 \\*\\/;$", ".* \\*\\/;$", 1),
        "accept_unknown_executable_comment": text.replace("\\/\\*!|\\/\\*\\+/ { exit 42 }", "{ print }", 1),
        "remove_post_strip_scan": text.replace("if LC_ALL=C \"$grep_bin\" -Eq '/\\*!|/\\*\\+' \"$destination\"; then fail dump_executable_comment; fi", ":", 1),
        "remove_output_ownership": text.replace('created_outputs+=("$destination")', ":", 1),
        "noclobber_rewrite": text.replace('"$cat_bin" "$temporary_dir/$name" >> "$destination"', '"$cat_bin" "$temporary_dir/$name" > "$destination"', 1),
        "remove_container_cleanup": text.replace('"$docker_bin" rm --force -- "$container_id"', ":", 1),
        "wildcard_cleanup": text.replace('"$docker_bin" rm --force -- "$container_id"', '"$docker_bin" rm --force -- "$container_name"*', 1),
        "migration_set_drift": text.replace(EXPECTED_SET_SHA, "0" * 64, 1),
        "skip_schema56": text.replace("apply_migration 000056", ":", 1),
        "leak_mysql_error": text.replace(
            '"$stage" "$mysql_error_code" "$mysql_sqlstate" "$mysql_sql_line"',
            '"$stage" "$mysql_error_code" "$mysql_sqlstate" "$mysql_error"',
        ),
        "weaken_mysql_error_shape": text.replace("([A-Z0-9]{5})", "(.*)", 1),
        "remove_fingerprint_scope": text.replace("tc.table_name = 'verification_codes'", "1 = 1", 1),
        "remove_fingerprint_order": text.replace("ORDER BY FIELD(constraint_name", "ORDER BY constraint_name /*", 1),
        "weaken_fingerprint_shape": text.replace("{7}$", "*$", 1),
        "leak_check_clause": text.replace("LOWER(SHA2(clause_compact, 256))", "clause_compact", 1),
        "fake_output_count": text.replace("outputs=6", "outputs=5", 1),
        "remove_mysql8_runtime_probe": text.replace('mysql --no-defaults --version', 'mysql --version', 1),
        "remove_mysql_utf8": text.replace('mysql --no-defaults --default-character-set=utf8mb4', 'mysql --no-defaults', 1),
        "remove_mysqldump_utf8": text.replace('mysqldump --no-defaults --default-character-set=utf8mb4', 'mysqldump --no-defaults', 1),
        "weaken_mysql8_runtime_gate": text.replace("^mysql[[:space:]]+Ver[[:space:]]+8\\.[0-9]+\\.[0-9]+", "Ver", 1),
        "accept_mysql7_runtime": text.replace("Ver[[:space:]]+8\\.", "Ver[[:space:]]+7\\.", 1),
        "fake_mysql8_runtime_summary": text.replace("mysql8_runtime_verified=true", "mysql8_runtime_verified=false", 1),
    }


def validate_dump_sanitizer(text: str) -> None:
    match = re.search(r'  "\$awk_bin" \'(\n.*?\n)  \' "\$dump_raw" > "\$destination"', text, re.DOTALL)
    require(match is not None, "sanitizer_extract")
    program = match.group(1)
    allowed = """/*!999999\\- enable the sandbox mode */
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `sample` (`id` bigint NOT NULL);
/*!40101 SET character_set_client = @saved_cs_client */;
INSERT INTO `sample` VALUES (1);
"""
    expected = "CREATE TABLE `sample` (`id` bigint NOT NULL);\nINSERT INTO `sample` VALUES (1);\n"
    accepted = subprocess.run(
        [bash(), "--noprofile", "--norc", "-c", 'awk "$1"', "_", program],
        input=allowed,
        text=True,
        capture_output=True,
        timeout=10,
    )
    require(accepted.returncode == 0 and accepted.stdout == expected and accepted.stderr == "", "sanitizer_allowed")
    rejected_inputs = (
        "/*!40101 SET SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;\n",
        "/*+ MAX_EXECUTION_TIME(1000) */ SELECT 1;\n",
        "INSERT INTO sample VALUES ('/*!unknown*/');\n",
    )
    for fixture in rejected_inputs:
        rejected = subprocess.run(
            [bash(), "--noprofile", "--norc", "-c", 'awk "$1"', "_", program],
            input=fixture,
            text=True,
            capture_output=True,
            timeout=10,
        )
        require(rejected.returncode == 42 and rejected.stderr == "", "sanitizer_rejection")


def main() -> int:
    raw = SCRIPT.read_bytes()
    require(raw and not raw.startswith(b"\xef\xbb\xbf") and b"\r" not in raw and b"\x00" not in raw, "script_encoding")
    text = raw.decode("utf-8")
    require(migration_set_sha() == EXPECTED_SET_SHA, "migration_set_sha")
    require(not errors(text), "contract:" + ",".join(errors(text)))
    validate_dump_sanitizer(text)
    syntax = subprocess.run([bash(), "--noprofile", "--norc", "-n", str(SCRIPT)], text=True, capture_output=True, timeout=10)
    require(syntax.returncode == 0 and syntax.stderr == "", "bash_syntax")
    selftest = subprocess.run([bash(), "--noprofile", "--norc", str(SCRIPT), "--self-test"], text=True, capture_output=True, timeout=30)
    require(selftest.returncode == 0 and "docker_access=false database_access=false migration_executed=false" in selftest.stdout and selftest.stderr == "", "selftest")
    default = subprocess.run([bash(), "--noprofile", "--norc", str(SCRIPT)], text=True, capture_output=True, timeout=10)
    require(default.returncode == 2 and "docker_access=false database_access=false" in default.stdout and default.stderr == "", "default_closed")
    one_gate_env = dict(os.environ)
    one_gate_env["MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE"] = "WRONG"
    one_gate = subprocess.run([bash(), "--noprofile", "--norc", str(SCRIPT), "--execute", "I_CONFIRM_EMAIL_MIGRATION_BASELINE_GENERATION_ONCE"], text=True, capture_output=True, timeout=10, env=one_gate_env)
    require(one_gate.returncode == 2 and "docker_access=false" in one_gate.stdout and one_gate.stderr == "", "one_gate_closed")
    attacks = mutate(text)
    for name, candidate in attacks.items():
        require(errors(candidate), f"attack_not_rejected:{name}")
    print(f"status=pass mode=email_migration_baseline_generator_contract attack_cases={len(attacks)} migrations=56 bash_syntax=pass default_closed=true docker_access=false database_access=false migration_executed=false outputs_created=false")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

from __future__ import annotations

import hashlib
import os
import re
import shlex
import shutil
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "tests/email/run-000056-container-partial-matrix.sh"
BOUNDARIES = ROOT / "tests/email/000056-partial-boundaries.tsv"
UP = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.up.sql"
DOWN = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.down.sql"
BASIC_RUNNER = ROOT / "tests/email/run-000056-container-isolation-matrix.sh"
UP_SHA = "9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B"
DOWN_SHA = "F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2"
BOUNDARY_SHA = "7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62"
OPTIMIZED_SENTINEL = "optimized_contract=pass|attack_cases=39"


class ContractError(RuntimeError):
    """表示 000056 partial 隔离资产未满足离线契约。"""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def locate_bash() -> str:
    bash = shutil.which("bash")
    if bash is None:
        bash = next((str(path) for path in (Path(r"C:\Program Files\Git\bin\bash.exe"), Path(r"C:\Program Files\Git\usr\bin\bash.exe")) if path.is_file()), None)
    require(bash is not None, "缺少本地 Bash")
    return str(bash)


def run_local(arguments: list[str], environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [locate_bash(), "--noprofile", "--norc", str(RUNNER), *arguments],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )


def verify_boundary_awk_runtime(script: str, boundary_text: str) -> None:
    """执行正式边界清单 awk，防止 Bash 语法检查漏掉 awk action 拼接错误。"""
    match = re.search(
        r'''(?m)^\[\[ \$\(\$AWK_BIN -F '\\t' '([^']*seen\[\$2\]\+\+[^']*)' "\$boundary_manifest"\) = 0 \]\]$''',
        script,
    )
    require(match is not None, "无法提取边界清单 awk")
    result = subprocess.run(
        [locate_bash(), "--noprofile", "--norc", "-c", "awk -F '\\t' " + shlex.quote(match.group(1))],
        check=False,
        input=boundary_text,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    require(result.returncode == 0 and result.stdout.strip() == "0" and result.stderr == "", "边界清单 awk 运行失败")


def parse_boundaries(text: str) -> list[tuple[str, ...]]:
    rows: list[tuple[str, ...]] = []
    for line_number, line in enumerate(text.splitlines(), 1):
        fields = tuple(line.split("\t"))
        require(len(fields) == 12, f"边界清单第{line_number}行列数错误")
        rows.append(fields)
    return rows


def boundary_errors(text: str) -> list[str]:
    errors: list[str] = []
    try:
        rows = parse_boundaries(text)
    except ContractError as exc:
        return [str(exc)]
    up = [row for row in rows if row[0] == "up"]
    down = [row for row in rows if row[0] == "down"]
    if len(up) != 27:
        errors.append("Up断点必须精确为27")
    if len(down) != 14:
        errors.append("Down断点必须精确为14")
    names = [row[1] for row in rows]
    if len(names) != len(set(names)) or any(re.fullmatch(r"(?:up|down)_[a-z0-9_]+", name) is None for name in names):
        errors.append("断点名称必须唯一且格式固定")
    expected_up_names = [
        "up_assertion_table", "up_assert_base_objects", "up_assert_000056_absent",
        "up_assert_verification_columns", "up_assert_verification_indexes", "up_assert_verification_checks",
        "up_assert_ownership_codes", "up_assert_ownership_links", "up_assert_permissions_metadata",
        "up_assert_admin_bindings", "up_assert_scenes_initial", "up_assert_scene_index", "up_assert_admin_role",
        "up_assert_admin_verify_initial", "up_assert_bootstrap_metadata", "up_receipt_table", "up_ownership_table",
        "up_ownership_capture", "up_permission_seed", "up_permission_id", "up_admin_binding",
        "up_admin_binding_id", "up_assert_bootstrap_permission", "up_assert_bootstrap_binding",
        "up_assert_bootstrap_ownership", "up_assert_receipt_empty", "up_assertion_drop",
    ]
    expected_down_names = [
        "down_assertion_table", "down_assert_objects", "down_assert_receipt_empty", "down_assert_admin_role",
        "down_assert_permission_metadata", "down_assert_ownership", "down_assert_unknown_refs",
        "down_delete_admin_binding", "down_delete_permission", "down_assert_binding_deleted",
        "down_assert_permission_deleted", "down_drop_receipt", "down_drop_ownership", "down_drop_assertions",
    ]
    if [row[1] for row in up] != expected_up_names:
        errors.append("Up冻结边界语义名称错误")
    if [row[1] for row in down] != expected_down_names:
        errors.append("Down冻结边界语义名称错误")
    if [row[2] for row in up] != [str(number) for number in range(1, 28)]:
        errors.append("Up必须覆盖全部27条语句边界")
    if [row[2] for row in down] != [str(number) for number in range(1, 15)]:
        errors.append("Down必须覆盖全部14条语句边界")
    try:
        states = [[int(value) for value in row[2:]] for row in rows]
    except ValueError:
        errors.append("边界和状态必须全部为整数")
        return errors
    if any(value < 0 for state in states for value in state):
        errors.append("状态不得为负数")
    if [int(row[4]) for row in up[:15]] != list(range(15)):
        errors.append("Up前置断言行数错误")
    if [int(row[4]) for row in up[22:26]] != [15, 16, 17, 18]:
        errors.append("Up写后断言行数错误")
    if [int(row[4]) for row in down[:11]] != [0, 1, 2, 3, 4, 5, 6, 6, 6, 7, 8]:
        errors.append("Down断言行数错误")
    if [int(row[5]) for row in up] != [0] * 15 + [1] * 12:
        errors.append("Up receipt表边界错误")
    if [int(row[5]) for row in down] != [1] * 11 + [0] * 3:
        errors.append("Down receipt表边界错误")
    if [int(row[6]) for row in up] != [0] * 16 + [1] * 11:
        errors.append("Up ownership表边界错误")
    if [int(row[6]) for row in down] != [1] * 12 + [0] * 2:
        errors.append("Down ownership表边界错误")
    if [int(row[8]) for row in up] != [0] * 18 + [1] * 9 or [int(row[8]) for row in down] != [1] * 8 + [0] * 6:
        errors.append("权限数量边界错误")
    if [int(row[9]) for row in up] != [0] * 20 + [1] * 7 or [int(row[9]) for row in down] != [1] * 7 + [0] * 7:
        errors.append("admin绑定数量边界错误")
    if [int(row[10]) for row in up] != [0] * 19 + [1] * 8 or [int(row[11]) for row in up] != [0] * 21 + [1] * 6:
        errors.append("Up ownership ID回填边界错误")
    return errors


def runner_errors(script: str) -> list[str]:
    errors: list[str] = []

    def contains(fragment: str, label: str) -> None:
        if fragment not in script:
            errors.append(label)

    contains("readonly confirm_phrase=I_CONFIRM_000056_PARTIAL_MATRIX_ONCE", "参数门禁缺失")
    contains("readonly execute_gate=I_UNDERSTAND_000056_PARTIAL_CREATES_43_ISOLATION_DATABASES", "环境门禁缺失")
    contains('[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked', "参数门禁未失败关闭")
    contains('[[ ${MOLIN_000056_PARTIAL_EXECUTE:-} = "$execute_gate" ]] || blocked', "环境门禁未失败关闭")
    contains(f"readonly expected_up_sha={UP_SHA}", "Up SHA未冻结")
    contains(f"readonly expected_down_sha={DOWN_SHA}", "Down SHA未冻结")
    contains(f"readonly expected_boundary_sha={BOUNDARY_SHA}", "边界SHA未冻结")
    for fragment in (
        "readonly expected_schema55_sha=${MOLIN_000056_SCHEMA55_SHA:-}",
        "readonly expected_schema56_sha=${MOLIN_000056_SCHEMA56_SHA:-}",
        "readonly expected_baseline_manifest_sha=${MOLIN_000056_BASELINE_MANIFEST_SHA:-}",
    ):
        contains(fragment, "基线三方外部SHA门禁缺失")
    contains("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "缺少 bind mount 资产 UID 输入")
    contains('[[ "$expected_asset_uid" =~ ^[1-9][0-9]*$ ]]', "资产 UID 格式门禁缺失")
    contains('[[ $($AWK_BIN \'END {print NR+0}\' "$baseline_manifest") -eq 2 ]]', "基线清单行数未由既有 awk 精确核验")
    contains('{seen[$2]++} END', "边界清单唯一性 awk action 缺少独立边界")
    if "WC_BIN" in script or "/usr/bin/wc" in script:
        errors.append("partial runner 不得依赖镜像未承诺的 wc 绝对路径")
    precheck_stages = (
        "environment_identity", "environment_hash_inputs", "environment_tools",
        "asset_directory_identity", "asset_hashes", "baseline_manifest_shape", "boundary_manifest_shape",
    )
    precheck_positions = [script.find(f"stage={stage}") for stage in precheck_stages]
    if min(precheck_positions) < 0 or precheck_positions != sorted(precheck_positions):
        errors.append("环境预检白名单阶段缺失或顺序错误")
    contains('[[ $($STAT_BIN -c %u -- "$asset_dir") = "$expected_asset_uid" ]]', "资产目录属主未绑定")
    contains('[[ $($STAT_BIN -c %u -- "$file") = "$expected_asset_uid" ]]', "资产文件属主未绑定")
    if script.count("trap - ERR\n  set +e") != 4 or script.count("set -e\n  trap on_error ERR") != 4:
        errors.append("显式退出码捕获未临时卸载并恢复 ERR trap")
    contains("local file=$1 mode=${2:-enforced} exit_code", "基线恢复模式门禁缺失")
    contains("SET SESSION FOREIGN_KEY_CHECKS=0;", "基线恢复未临时关闭会话外键检查")
    contains("SET SESSION FOREIGN_KEY_CHECKS=1;", "基线恢复未恢复会话外键检查")
    contains('mysql_file "$baseline_file" baseline_restore', "仅基线恢复路径未显式启用恢复模式")
    contains("readonly target_prefix=molin_56pt_", "partial目标前缀缺失")
    contains('target_db="${target_prefix}${suffix}"', "目标未由运行时UUID生成")
    contains('[[ ! -e "$run_dir" ]]', "运行目录复用未拒绝")
    contains("runtime_unique_targets=43", "运行时目标总数错误")
    contains("partial_up_points=27", "Up点数摘要缺失")
    contains("partial_down_points=14", "Down点数摘要缺失")
    contains("no_injection_baselines=2", "无注入基线摘要缺失")
    contains("base_runner_partial_status_unchanged=not_implemented", "基础runner边界未保留")
    contains("combined_partial_evidence=provided_by_separate_asset", "独立组合证据声明缺失")
    contains("targets_retained=true", "证据保留声明缺失")
    contains("molin_000056_injected_boundary", "显式故障注入缺失")
    contains("assert_partial_state", "断点状态断言缺失")
    if script.count("assert_partial_state") != 2:
        errors.append("断点状态断言必须定义一次且调用一次")
    contains("email_admin_verify_bootstrap_receipts", "receipt状态证据缺失")
    contains("migration_000056_permission_ownership", "ownership状态证据缺失")
    contains("permission_created,':',admin_binding_created", "ownership flags证据缺失")
    contains("permission_id IS NOT NULL", "permission ID证据缺失")
    contains("admin_role_permission_id IS NOT NULL", "binding ID证据缺失")
    contains("information_schema.referential_constraints", "receipt外键证据缺失")
    contains("information_schema.table_constraints", "receipt与断言CHECK证据缺失")
    contains("baseline_without_literals_and_comments", "SQL感知基线扫描缺失")
    contains("if(!closed)exit 44", "未闭合字符串失败关闭缺失")
    contains("'/\\*!|/\\*\\+'", "可执行注释与优化器提示门禁缺失")
    contains('baseline_without_literals_and_comments "$baseline" | LC_ALL=C "$GREP_BIN"', "跨schema检查未使用净化SQL")
    contains("run_baseline up", "Up无注入基线缺失")
    contains("run_baseline down", "Down无注入基线缺失")
    contains("= 27 ]]", "Up语句总数未冻结")
    contains("= 14 ]]", "Down语句总数未冻结")

    guard = script.find("if [[ ${1:-} = --self-test")
    argument_gate = script.find("[[ $# -eq 2 ]] || blocked")
    environment_gate = script.find("[[ ${MOLIN_000056_PARTIAL_EXECUTE:-}")
    first_mysql = script.find('MYSQL_PWD="$MYSQL_ROOT_PASSWORD"')
    if min(guard, argument_gate, environment_gate, first_mysql) < 0 or not guard < argument_gate < environment_gate < first_mysql:
        errors.append("双门禁必须早于全部数据库调用")
    invocations = re.findall(r'(?m)^\s*MYSQL_PWD=.*?"\$MYSQL_BIN"\s+([^\n]+)', script)
    if len(invocations) != 5 or any(not invocation.startswith("--no-defaults --default-character-set=utf8mb4 ") for invocation in invocations):
        errors.append("MySQL调用数量、option隔离或UTF-8字符集错误")
    for pattern in (r"(?im)\bDROP\s+(?:DATABASE|SCHEMA)\b", r"(?im)\bTRUNCATE\b", r"(?im)(?:^|[;&|]\s*)rm\s", r"(?im)\bmysqladmin\b"):
        if re.search(pattern, script):
            errors.append(f"出现危险模式:{pattern}")
    if '--database=molin' in script or re.search(r"(?i)\bmolin\.[a-z_]", script):
        errors.append("可能选择测试主库")
    if re.search(r"(?i)\bmolin_56pt_[a-f0-9]{32}\b", script):
        errors.append("出现硬编码旧partial隔离库")
    if "printf 'target_db=" in script or "printf 'run_dir=" in script:
        errors.append("可能输出完整随机目标")
    if '"$CAT_BIN" "$evidence_dir/mysql.stderr"' in script or "2>/dev/null" in script:
        errors.append("MySQL原始错误可能泄露或丢弃")
    admin_lines = [line.strip() for line in script.splitlines() if re.search(r"\bmysql_admin\b", line) and not re.match(r"\s*mysql_admin\(\)\s*\{", line)]
    expected_admin = [
        '[[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = \'$target_db\';") = 0 ]]',
        'mysql_admin "CREATE DATABASE \\`$target_db\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null',
    ]
    if admin_lines != expected_admin:
        errors.append("mysql_admin只能检查并创建本轮目标")
    if re.search(r'(?i)mysql_(?:admin|query)\s+"[^"\n]*(?:GRANT|REVOKE|CREATE\s+USER|ALTER\s+USER|DROP\s+USER)', script):
        errors.append("不得修改账号或授权")
    basic = BASIC_RUNNER.read_text(encoding="utf-8")
    if "partial_fault_injection=not_implemented" not in basic:
        errors.append("基础runner不再诚实报告partial缺口")
    return errors


def verify_baseline_scanner(script: str) -> None:
    match = re.search(r"(?ms)^baseline_without_literals_and_comments\(\) \{.*?^\}$", script)
    require(match is not None, "无法抽取SQL感知扫描器")
    command = "AWK_BIN=/usr/bin/awk\n" + match.group(0) + "\nbaseline_without_literals_and_comments /dev/stdin\n"

    def scan(fixture: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run([locate_bash(), "--noprofile", "--norc", "-c", command], check=False, input=fixture, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    legal = scan("INSERT INTO users(email) VALUES ('legacy@example.com','legacy\\'alias@example.com',\"double@example.com\");\n")
    require(legal.returncode == 0 and legal.stderr == "" and "example.com" not in legal.stdout, "合法邮箱被跨schema误判")
    disguised = scan("-- hidden_schema.users\n/* hidden_schema.users */ INSERT INTO t VALUES ('fake_schema.users');\n")
    require(disguised.returncode == 0 and disguised.stderr == "" and "hidden_schema.users" not in disguised.stdout and "fake_schema.users" not in disguised.stdout, "注释或字符串未净化")
    for fixture, token in (("SELECT * FROM foreign_schema.users;\n", "foreign_schema.users"), ("SELECT * FROM `foreign_schema`.`users`;\n", "`foreign_schema`.`users`")):
        result = scan(fixture)
        require(result.returncode == 0 and result.stderr == "" and token in result.stdout, "真实跨schema限定名被移除")
    require(scan("/* unterminated\n").returncode == 43, "未闭合注释未失败关闭")
    require(scan("INSERT INTO t VALUES ('unterminated);\n").returncode == 44, "未闭合字符串未失败关闭")


def run_attack_model(script: str, boundary_text: str) -> int:
    script_mutations = {
        "删除参数门禁": script.replace("[[ $# -eq 2 ]] || blocked", ":", 1),
        "删除环境门禁": script.replace('[[ ${MOLIN_000056_PARTIAL_EXECUTE:-} = "$execute_gate" ]] || blocked', ":", 1),
        "删除资产UID绑定": script.replace("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "readonly expected_asset_uid=", 1),
        "删除退出码捕获trap隔离": script.replace("trap - ERR\n  set +e", "set +e", 1),
        "删除基线外键恢复保护": script.replace("SET SESSION FOREIGN_KEY_CHECKS=0;", "SET SESSION FOREIGN_KEY_CHECKS=1;", 1),
        "提前数据库调用": script.replace("stage=initialization", 'stage=initialization\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --execute="SELECT 1"', 1),
        "固定目标": script.replace('target_db="${target_prefix}${suffix}"', "target_db=molin_restore_fixed", 1),
        "硬编码旧目标": script + "\nold=molin_56pt_00000000000040008000000000000000\n",
        "管理连接旧库": script + '\nmysql_admin "SELECT COUNT(*) FROM `molin_restore_fixed`.schema_migrations;"\n',
        "输出完整目标": script + '\nprintf \'target_db=%s\\n\' "$target_db"\n',
        "篡改UpSHA": script.replace(f"readonly expected_up_sha={UP_SHA}", "readonly expected_up_sha=UNKNOWN", 1),
        "篡改DownSHA": script.replace(f"readonly expected_down_sha={DOWN_SHA}", "readonly expected_down_sha=UNKNOWN", 1),
        "篡改边界SHA": script.replace(f"readonly expected_boundary_sha={BOUNDARY_SHA}", "readonly expected_boundary_sha=UNKNOWN", 1),
        "移除schema55SHA": script.replace("readonly expected_schema55_sha=${MOLIN_000056_SCHEMA55_SHA:-}", "readonly expected_schema55_sha=", 1),
        "移除schema56SHA": script.replace("readonly expected_schema56_sha=${MOLIN_000056_SCHEMA56_SHA:-}", "readonly expected_schema56_sha=", 1),
        "移除manifestSHA": script.replace("readonly expected_baseline_manifest_sha=${MOLIN_000056_BASELINE_MANIFEST_SHA:-}", "readonly expected_baseline_manifest_sha=", 1),
        "移除option隔离": script.replace('"$MYSQL_BIN" --no-defaults', '"$MYSQL_BIN"', 1),
        "移除UTF8客户端字符集": script.replace(" --default-character-set=utf8mb4", "", 1),
        "放宽基线清单行数": script.replace("-eq 2 ]]", "-ge 2 ]]", 1),
        "合并资产哈希阶段": script.replace("stage=asset_hashes", "stage=environment_tools", 1),
        "破坏边界清单awk动作边界": script.replace("{seen[$2]++} END", "seen[$2]++ END", 1),
        "移除状态断言": script.replace('assert_partial_state "$direction"', ': "$direction"', 1),
        "移除Up基线": script.replace("run_baseline up", ": # removed", 1),
        "移除Down基线": script.replace("run_baseline down", ": # removed", 1),
        "删除schema": script + "\nDROP DATABASE runtime_target;\n",
        "选择主库": script + '\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --database=molin --execute="SELECT 1"\n',
        "账号授权": script + '\nmysql_query "GRANT SELECT ON *.* TO \'attacker\'@\'%\';"\n',
        "移除可执行注释门禁": script.replace("'/\\*!|/\\*\\+'", "'/never_match/'", 1),
        "绕过SQL净化": script.replace('baseline_without_literals_and_comments "$baseline" | LC_ALL=C "$GREP_BIN"', '"$CAT_BIN" "$baseline" | LC_ALL=C "$GREP_BIN"', 1),
        "移除未闭合门禁": script.replace("if(!closed)exit 44", "", 1),
        "移除receipt证据": script.replace("information_schema.referential_constraints", "information_schema.tables", 1),
        "移除ownership标志": script.replace("permission_created,':',admin_binding_created", "permission_code"),
        "伪报基础runner": script.replace("base_runner_partial_status_unchanged=not_implemented", "base_runner_partial_status_unchanged=implemented", 1),
    }
    boundary_mutations = {
        "减少Up边界": "\n".join(boundary_text.splitlines()[1:]) + "\n",
        "减少Down边界": "\n".join(line for line in boundary_text.splitlines() if not line.startswith("down\tdown_drop_assertions\t")) + "\n",
        "篡改断言行数": boundary_text.replace("up\tup_assert_verification_checks\t6\t1\t5", "up\tup_assert_verification_checks\t6\t1\t9", 1),
        "篡改权限边界": boundary_text.replace("up\tup_permission_seed\t19\t1\t14\t1\t1\t1\t1", "up\tup_permission_seed\t19\t1\t14\t1\t1\t1\t0", 1),
        "篡改Up语义名称": boundary_text.replace("up_assert_000056_absent", "up_assert_verification_absent", 1),
        "篡改Down语义名称": boundary_text.replace("down_assert_unknown_refs", "down_assert_known_refs", 1),
    }
    for name, mutated in script_mutations.items():
        require(mutated != script, f"脚本攻击变异未改变原文本:{name}")
        require(bool(runner_errors(mutated)), f"脚本攻击未被拒绝:{name}")
    for name, mutated in boundary_mutations.items():
        require(mutated != boundary_text, f"边界攻击变异未改变原文本:{name}")
        require(bool(boundary_errors(mutated)), f"边界攻击未被拒绝:{name}")
    return len(script_mutations) + len(boundary_mutations)


def execute_contract() -> int:
    script = RUNNER.read_text(encoding="utf-8")
    boundary_text = BOUNDARIES.read_text(encoding="utf-8")
    require(sha256(UP) == UP_SHA, "000056 Up SHA偏离冻结值")
    require(sha256(DOWN) == DOWN_SHA, "000056 Down SHA偏离冻结值")
    require(sha256(BOUNDARIES) == BOUNDARY_SHA, "000056边界清单SHA偏离冻结值")
    require(RUNNER.stat().st_size < 30_000, "partial runner超过30KB审计上限")
    require(not boundary_errors(boundary_text), "边界清单失败:" + ",".join(boundary_errors(boundary_text)))
    require(not runner_errors(script), "runner契约失败:" + ",".join(runner_errors(script)))
    verify_baseline_scanner(script)
    verify_boundary_awk_runtime(script, boundary_text)
    syntax = subprocess.run([locate_bash(), "--noprofile", "--norc", "-n", str(RUNNER)], check=False, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    require(syntax.returncode == 0 and syntax.stderr == "", "Bash语法检查失败")
    selftest = run_local(["--self-test"])
    require(selftest.returncode == 0 and selftest.stderr == "", "SelfTest失败")
    require(selftest.stdout.strip() == "status=pass mode=selftest cases=43 database_access=false migration_executed=false runtime_target=not_created up_points=27 down_points=14 baselines=2", "SelfTest摘要错误")
    default = run_local([])
    require(default.returncode == 2 and default.stderr == "" and "database_access=false migration_executed=false" in default.stdout, "默认调用未失败关闭")
    environment = dict(os.environ)
    environment["MOLIN_000056_PARTIAL_EXECUTE"] = "WRONG"
    one_gate = run_local(["--execute", "I_CONFIRM_000056_PARTIAL_MATRIX_ONCE"], environment)
    require(one_gate.returncode == 2 and one_gate.stderr == "" and "database_access=false migration_executed=false" in one_gate.stdout, "单门禁未失败关闭")
    cases = run_attack_model(script, boundary_text)
    require(cases == 39, "攻击模型数量错误")
    return cases


def main() -> int:
    try:
        cases = execute_contract()
        if __debug__:
            optimized = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve())], check=False, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            require(optimized.returncode == 0 and optimized.stderr == "", "Python -O子进程失败")
            require(optimized.stdout.strip() == f"optimized_contract=pass|attack_cases={cases}", "Python -O摘要错误")
            print(
                "status=pass checks=boundary_manifest,static,bash,selftest,default_closed,one_gate_closed,sql_scanner "
                f"attack_cases={cases} optimized=true database_access=false migration_executed=false "
                f"up_points=27 down_points=14 baselines=2 runner_sha256={sha256(RUNNER)}"
            )
        else:
            print(f"optimized_contract=pass|attack_cases={cases}")
        return 0
    except (ContractError, OSError, UnicodeError) as exc:
        print(f"status=failed classification=offline_contract detail={exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

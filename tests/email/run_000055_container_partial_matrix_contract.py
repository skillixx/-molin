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
RUNNER = ROOT / "tests/email/run-000055-container-partial-matrix.sh"
BOUNDARIES = ROOT / "tests/email/000055-partial-boundaries.tsv"
UP = ROOT / "server/migrations/000055_add_directmail_email_management.up.sql"
DOWN = ROOT / "server/migrations/000055_add_directmail_email_management.down.sql"
UP_SHA = "7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D"
DOWN_SHA = "217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE"
BOUNDARY_SHA = "4B5E02DC0C72490B168A47637E1DD8E6298DFEBE18AC22CD9DCAF663B8E18585"
OPTIMIZED_SENTINEL = "optimized_contract=pass|attack_cases=34"


class ContractError(RuntimeError):
    """表示 000055 partial 隔离资产未满足离线安全契约。"""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def locate_bash() -> str:
    bash = shutil.which("bash")
    if bash is None:
        bash = next(
            (
                str(path)
                for path in (Path(r"C:\Program Files\Git\bin\bash.exe"), Path(r"C:\Program Files\Git\usr\bin\bash.exe"))
                if path.is_file()
            ),
            None,
        )
    require(bash is not None, "缺少本地 Bash")
    return str(bash)


def run_local(arguments: list[str], environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    """只运行不会越过完整双门禁的本地路径。"""
    return subprocess.run(
        [locate_bash(), "--noprofile", "--norc", str(RUNNER), *arguments],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )


def verify_baseline_scanner(script: str) -> None:
    """用真实 Bash 控制流证明字符串和普通注释不会触发跨 schema 误报。"""
    match = re.search(r"(?ms)^baseline_without_literals_and_comments\(\) \{.*?^\}$", script)
    require(match is not None, "无法抽取基线SQL感知扫描器")
    command = "AWK_BIN=/usr/bin/awk\n" + match.group(0) + "\nbaseline_without_literals_and_comments /dev/stdin\n"

    def scan(fixture: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [locate_bash(), "--noprofile", "--norc", "-c", command],
            check=False,
            input=fixture,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

    email = scan("INSERT INTO users(email) VALUES ('legacy@example.com', 'legacy\\'alias@example.com', \"double@example.com\");\n")
    require(email.returncode == 0 and email.stderr == "" and "example.com" not in email.stdout, "历史邮箱被跨schema扫描误判")
    comment = scan("-- SELECT * FROM line_schema.users;\n/* SELECT * FROM hidden_schema.users; */ INSERT INTO t VALUES ('a.b', 'fake_schema.users');\n")
    require(comment.returncode == 0 and comment.stderr == "" and "hidden_schema.users" not in comment.stdout and "a.b" not in comment.stdout, "普通注释或字符串未被净化")
    qualified = scan("SELECT * FROM foreign_schema.users;\n")
    require(qualified.returncode == 0 and qualified.stderr == "" and "foreign_schema.users" in qualified.stdout, "跨schema限定名被错误移除")
    quoted_qualified = scan("SELECT * FROM `foreign_schema`.`users`;\n")
    require(quoted_qualified.returncode == 0 and quoted_qualified.stderr == "" and "`foreign_schema`.`users`" in quoted_qualified.stdout, "反引号跨schema限定名被错误移除")
    unterminated_comment = scan("/* unterminated\n")
    require(unterminated_comment.returncode == 43 and unterminated_comment.stderr == "", "未闭合普通块注释未失败关闭")
    for fixture in ("INSERT INTO t VALUES ('unterminated);\n", 'INSERT INTO t VALUES ("unterminated);\n'):
        unterminated_string = scan(fixture)
        require(unterminated_string.returncode == 44 and unterminated_string.stderr == "", "未闭合字符串未失败关闭")


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
        require(len(fields) == 14, f"边界清单第 {line_number} 行列数错误")
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
    if len(up) != 16:
        errors.append("Up 断点必须精确为16")
    if len(down) != 15:
        errors.append("Down 断点必须精确为15")
    names = [row[1] for row in rows]
    if len(names) != len(set(names)) or any(re.fullmatch(r"(?:up|down)_[a-z0-9_]+", name) is None for name in names):
        errors.append("断点名称必须唯一且格式固定")
    expected_up_names = [
        "up_table_templates", "up_table_bindings", "up_table_sync_runs", "up_table_allowlist", "up_table_send_logs",
        "up_ownership_table", "up_ownership_row_1", "up_ownership_row_2", "up_ownership_row_3", "up_ownership_row_4",
        "up_permissions_seed", "up_permission_ids", "up_admin_bindings", "up_admin_binding_ids",
        "up_permission_assertion", "up_binding_ownership_assertions",
    ]
    expected_down_names = [
        "down_verification_invalidate", "down_verification_assertion", "down_admin_bindings", "down_permissions",
        "down_cleanup_assertion", "down_ownership_table", "down_table_send_logs", "down_table_allowlist",
        "down_table_sync_runs", "down_table_bindings", "down_table_templates", "down_verification_checks",
        "down_verification_indexes", "down_verification_columns", "down_verification_code",
    ]
    if [row[1] for row in up] != expected_up_names:
        errors.append("Up 冻结边界顺序错误")
    if [row[1] for row in down] != expected_down_names:
        errors.append("Down 冻结边界顺序错误")
    if [row[3] for row in up[:6]] != ["20", "21", "22", "23", "24", "26"]:
        errors.append("Up 表级语句边界错误")
    if [(row[2], row[3]) for row in up[6:10]] != [("ownership", str(number)) for number in range(1, 5)]:
        errors.append("ownership 四行边界错误")
    if [row[3] for row in up[10:]] != ["28", "29", "30", "31", "32", "35"]:
        errors.append("Up seed与断言边界错误")
    if [row[3] for row in down] != ["9", "10", "11", "12", "13", "14", "16", "17", "18", "19", "20", "21", "22", "23", "24"]:
        errors.append("Down 语句边界错误")
    # 状态列必须是非负整数；业务表计数只能按创建/逆序删除单调变化。
    try:
        states = [[int(value) for value in row[4:]] for row in rows]
    except ValueError:
        errors.append("状态断言必须全部为整数")
        return errors
    if any(value < 0 for state in states for value in state):
        errors.append("状态断言不得为负数")
    if [int(row[4]) for row in up[:5]] != [1, 2, 3, 4, 5]:
        errors.append("Up 五业务表计数错误")
    if [int(row[4]) for row in down[6:11]] != [4, 3, 2, 1, 0]:
        errors.append("Down 五业务表逆序计数错误")
    if [int(row[6]) for row in up[6:10]] != [1, 2, 3, 4]:
        errors.append("ownership 行数证据错误")
    if [int(row[11]) for row in down[-4:]] != [8, 8, 0, 0] or [int(row[12]) for row in down[-4:]] != [3, 0, 0, 0] or [int(row[13]) for row in down[-4:]] != [0, 0, 0, 0]:
        errors.append("verification 清理状态错误")
    return errors


def runner_errors(script: str) -> list[str]:
    errors: list[str] = []

    def contains(fragment: str, label: str) -> None:
        if fragment not in script:
            errors.append(label)

    contains("readonly confirm_phrase=I_CONFIRM_000055_PARTIAL_MATRIX_ONCE", "参数确认短语缺失")
    contains("readonly execute_gate=I_UNDERSTAND_000055_PARTIAL_CREATES_33_ISOLATION_DATABASES", "环境门禁缺失")
    contains('[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked', "参数门禁未失败关闭")
    contains('[[ ${MOLIN_000055_PARTIAL_EXECUTE:-} = "$execute_gate" ]] || blocked', "环境门禁未失败关闭")
    contains(f"readonly expected_up_sha={UP_SHA}", "Up SHA未冻结")
    contains(f"readonly expected_down_sha={DOWN_SHA}", "Down SHA未冻结")
    contains(f"readonly expected_boundary_sha={BOUNDARY_SHA}", "边界SHA未冻结")
    contains("readonly expected_schema54_sha=${MOLIN_000055_SCHEMA54_SHA:-}", "schema54基线外部冻结摘要缺失")
    contains("readonly expected_schema55_sha=${MOLIN_000055_SCHEMA55_SHA:-}", "schema55基线外部冻结摘要缺失")
    contains("readonly expected_baseline_manifest_sha=${MOLIN_000055_BASELINE_MANIFEST_SHA:-}", "基线清单外部冻结摘要缺失")
    contains('[[ "$expected_baseline_manifest_sha" =~ ^[A-F0-9]{64}$ ]]', "基线清单摘要格式门禁缺失")
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
    contains("readonly target_prefix=molin_55pt_", "partial隔离库前缀缺失")
    contains('target_db="${target_prefix}${suffix}"', "目标未由运行时UUID派生")
    contains('[[ ! -e "$run_dir" ]]', "运行目录复用未拒绝")
    contains("target_id_sha256=%s", "目标未只输出摘要")
    contains("runtime_unique_targets=33", "运行时目标数错误")
    contains("partial_up_points=16", "Up断点摘要缺失")
    contains("partial_down_points=15", "Down断点摘要缺失")
    contains("no_injection_baselines=2", "无注入基线摘要缺失")
    contains("boundary_state_assertions=true", "状态断言摘要缺失")
    contains("base_runner_partial_status_unchanged=not_implemented", "基础runner边界未保留")
    contains("combined_partial_evidence=provided_by_separate_asset", "组合证据关系缺失")
    contains("targets_retained=true", "证据保留语义缺失")
    contains("emit_ownership_partial", "ownership行级断点执行器缺失")
    contains("ORDER BY spec.code LIMIT \" rows \";", "ownership行级边界未按权限代码确定顺序生成")
    contains("GROUP_CONCAT(permission_code ORDER BY permission_code", "ownership行级精确集合断言缺失")
    contains("permission_created=1 AND admin_binding_created=1", "ownership创建标志精确断言缺失")
    contains("'/\\*!|/\\*\\+'", "基线MySQL可执行注释门禁缺失")
    contains("baseline_without_literals_and_comments", "基线SQL感知净化扫描缺失")
    contains('if (ch == "\\047" || ch == "\\042")', "基线字符串字面量净化缺失")
    contains("if (!closed) exit 44", "基线未闭合字符串失败关闭缺失")
    contains('baseline_without_literals_and_comments "$baseline" | LC_ALL=C "$GREP_BIN"', "跨schema检查未使用净化后SQL")
    contains("`[^`]+`|[A-Za-z_][A-Za-z0-9_]*", "基线跨schema限定名门禁缺失")
    contains("assert_partial_state", "断点后结构状态断言缺失")
    if script.count("assert_partial_state") != 2:
        errors.append("断点状态断言必须定义一次且调用一次")
    contains("molin_000055_injected_boundary", "显式故障注入缺失")
    contains("run_baseline up", "Up无注入基线缺失")
    contains("run_baseline down", "Down无注入基线缺失")
    contains("= 36 ]]", "Up语句总数未冻结")
    contains("= 24 ]]", "Down语句总数未冻结")

    guard = script.find("if [[ ${1:-} = --self-test")
    argument_gate = script.find("[[ $# -eq 2 ]] || blocked")
    environment_gate = script.find("[[ ${MOLIN_000055_PARTIAL_EXECUTE:-}")
    first_mysql = script.find('MYSQL_PWD="$MYSQL_ROOT_PASSWORD"')
    if min(guard, argument_gate, environment_gate, first_mysql) < 0 or not guard < argument_gate < environment_gate < first_mysql:
        errors.append("双门禁必须早于全部数据库调用")

    invocations = re.findall(r'(?m)^\s*MYSQL_PWD=.*?"\$MYSQL_BIN"\s+([^\n]+)', script)
    if len(invocations) != 5 or any(not invocation.startswith("--no-defaults --default-character-set=utf8mb4 ") for invocation in invocations):
        errors.append("MySQL调用数量、option隔离或UTF-8字符集错误")
    dangerous = (
        r"(?im)\bDROP\s+(?:DATABASE|SCHEMA)\b", r"(?im)\bTRUNCATE\b", r"(?im)(?:^|[;&|]\s*)rm\s",
        r"(?im)\bmysqladmin\b", r"(?im)\bFLUSHDB\b|\bKEYS\s+\*",
    )
    for pattern in dangerous:
        if re.search(pattern, script):
            errors.append(f"出现危险模式:{pattern}")
    if '--database=molin' in script or re.search(r"(?i)\bmolin\.[a-z_]", script):
        errors.append("可能选择测试主库")
    if re.search(r"(?i)\bmolin_55pt_[a-f0-9]{32}\b", script):
        errors.append("出现硬编码旧partial隔离库")
    if "printf 'target_db=" in script or "printf 'run_dir=" in script:
        errors.append("完整随机目标可能输出")
    if '"$CAT_BIN" "$evidence_dir/mysql.stderr"' in script or "2>/dev/null" in script:
        errors.append("MySQL原始错误可能泄露或丢弃")
    admin_lines = [
        line.strip() for line in script.splitlines()
        if re.search(r"\bmysql_admin\b", line) and not re.match(r"\s*mysql_admin\(\)\s*\{", line)
    ]
    expected_admin = [
        '[[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = \'$target_db\';") = 0 ]]',
        'mysql_admin "CREATE DATABASE \\`$target_db\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null',
    ]
    if admin_lines != expected_admin:
        errors.append("mysql_admin只能检查并创建本轮目标")
    if re.search(r'(?i)mysql_(?:admin|query)\s+"[^"\n]*(?:GRANT|REVOKE|CREATE\s+USER|ALTER\s+USER|DROP\s+USER)', script):
        errors.append("不得变更账号或授权")
    required_state = (
        "migration_000055_permission_ownership", "permission_id IS NOT NULL", "admin_role_permission_id IS NOT NULL",
        "email_provider_templates", "email_scene_bindings", "email_template_sync_runs", "email_test_recipient_allowlist",
        "email_send_logs", "information_schema.columns", "information_schema.statistics", "information_schema.table_constraints",
    )
    for fragment in required_state:
        contains(fragment, f"缺少断点状态证据:{fragment}")
    return errors


def run_attack_model(script: str, boundary_text: str) -> int:
    script_mutations = {
        "删除参数门禁": script.replace("[[ $# -eq 2 ]] || blocked", ":", 1),
        "删除环境门禁": script.replace('[[ ${MOLIN_000055_PARTIAL_EXECUTE:-} = "$execute_gate" ]] || blocked', ":", 1),
        "删除资产UID绑定": script.replace("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "readonly expected_asset_uid=", 1),
        "删除退出码捕获trap隔离": script.replace("trap - ERR\n  set +e", "set +e", 1),
        "删除基线外键恢复保护": script.replace("SET SESSION FOREIGN_KEY_CHECKS=0;", "SET SESSION FOREIGN_KEY_CHECKS=1;", 1),
        "提前数据库调用": script.replace("stage=initialization", 'stage=initialization\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --execute="SELECT 1"', 1),
        "固定目标": script.replace('target_db="${target_prefix}${suffix}"', "target_db=molin_restore_fixed", 1),
        "硬编码旧目标": script + "\nold=molin_55pt_00000000000040008000000000000000\n",
        "管理连接访问旧库": script + '\nmysql_admin "SELECT COUNT(*) FROM `molin_restore_fixed`.schema_migrations;"\n',
        "输出完整目标": script + "\nprintf 'target_db=%s\\n' \"$target_db\"\n",
        "篡改UpSHA": script.replace(f"readonly expected_up_sha={UP_SHA}", "readonly expected_up_sha=UNKNOWN", 1),
        "篡改DownSHA": script.replace(f"readonly expected_down_sha={DOWN_SHA}", "readonly expected_down_sha=UNKNOWN", 1),
        "篡改边界SHA": script.replace(f"readonly expected_boundary_sha={BOUNDARY_SHA}", "readonly expected_boundary_sha=UNKNOWN", 1),
        "移除option隔离": script.replace('"$MYSQL_BIN" --no-defaults', '"$MYSQL_BIN"', 1),
        "移除UTF8客户端字符集": script.replace(" --default-character-set=utf8mb4", "", 1),
        "放宽基线清单行数": script.replace("-eq 2 ]]", "-ge 2 ]]", 1),
        "合并资产哈希阶段": script.replace("stage=asset_hashes", "stage=environment_tools", 1),
        "破坏边界清单awk动作边界": script.replace("{seen[$2]++} END", "seen[$2]++ END", 1),
        "移除状态断言": script.replace("assert_partial_state \"$direction\"", ": \"$direction\"", 1),
        "移除Up基线": script.replace("run_baseline up", ": # up baseline removed", 1),
        "移除Down基线": script.replace("run_baseline down", ": # down baseline removed", 1),
        "删除schema": script + "\nDROP DATABASE runtime_target;\n",
        "选择主库": script + '\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --database=molin --execute="SELECT 1"\n',
        "账号授权": script + '\nmysql_query "GRANT SELECT ON *.* TO \'attacker\'@\'%\';"\n',
        "伪报基础runner完成": script.replace("base_runner_partial_status_unchanged=not_implemented", "base_runner_partial_status_unchanged=implemented", 1),
        "移除可执行注释门禁": script.replace("'/\\*!|/\\*\\+'", "'/never_match_executable_comment/'", 1),
        "绕过SQL感知净化": script.replace('baseline_without_literals_and_comments "$baseline" | LC_ALL=C "$GREP_BIN"', '"$CAT_BIN" "$baseline" | LC_ALL=C "$GREP_BIN"', 1),
        "移除未闭合字符串门禁": script.replace("if (!closed) exit 44", "", 1),
        "移除字符串净化": script.replace('if (ch == "\\047" || ch == "\\042")', 'if (0)', 1),
    }
    boundary_mutations = {
        "减少Up断点": "\n".join(boundary_text.splitlines()[1:]) + "\n",
        "减少Down断点": "\n".join(line for line in boundary_text.splitlines() if "\tdown_verification_code\t" not in line) + "\n",
        "篡改ownership行数": boundary_text.replace("up_ownership_row_2\townership\t2\t5\t1\t2", "up_ownership_row_2\townership\t2\t5\t1\t4", 1),
        "篡改Down顺序": boundary_text.replace("down\tdown_table_send_logs\tprefix\t16", "down\tdown_table_send_logs\tprefix\t17", 1),
    }

    script_mutations["移除ownership确定顺序"] = script.replace(
        'ORDER BY spec.code LIMIT " rows ";',
        'LIMIT " rows ";',
        1,
    )
    expected = {
        "硬编码旧目标": "出现硬编码旧partial隔离库",
        "管理连接访问旧库": "mysql_admin只能检查并创建本轮目标",
        "账号授权": "不得变更账号或授权",
    }
    for name, mutated in script_mutations.items():
        errors = runner_errors(mutated)
        require(bool(errors), f"脚本攻击未被拒绝:{name}")
        if name in expected:
            require(expected[name] in errors, f"脚本攻击未命中独立规则:{name}")
    for name, mutated in boundary_mutations.items():
        require(bool(boundary_errors(mutated)), f"边界攻击未被拒绝:{name}")
    return len(script_mutations) + len(boundary_mutations)


def execute_contract() -> int:
    script = RUNNER.read_text(encoding="utf-8")
    boundary_text = BOUNDARIES.read_text(encoding="utf-8")
    require(sha256(UP) == UP_SHA, "000055 Up SHA偏离冻结值")
    require(sha256(DOWN) == DOWN_SHA, "000055 Down SHA偏离冻结值")
    require(sha256(BOUNDARIES) == BOUNDARY_SHA, "000055边界清单SHA偏离冻结值")
    require(RUNNER.stat().st_size < 30_000, "partial runner超过30KB审计上限")
    require(not boundary_errors(boundary_text), "边界清单失败:" + ",".join(boundary_errors(boundary_text)))
    require(not runner_errors(script), "runner契约失败:" + ",".join(runner_errors(script)))
    verify_baseline_scanner(script)
    verify_boundary_awk_runtime(script, boundary_text)
    syntax = subprocess.run([locate_bash(), "--noprofile", "--norc", "-n", str(RUNNER)], check=False, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    require(syntax.returncode == 0 and syntax.stderr == "", "Bash语法检查失败")
    selftest = run_local(["--self-test"])
    require(selftest.returncode == 0 and selftest.stderr == "", "SelfTest失败")
    require(selftest.stdout.strip() == "status=pass mode=selftest cases=24 database_access=false migration_executed=false runtime_target=not_created up_points=16 down_points=15 baselines=2", "SelfTest摘要错误")
    default = run_local([])
    require(default.returncode == 2 and default.stderr == "" and "database_access=false migration_executed=false" in default.stdout, "默认调用未失败关闭")
    environment = dict(os.environ)
    environment["MOLIN_000055_PARTIAL_EXECUTE"] = "WRONG"
    one_gate = run_local(["--execute", "I_CONFIRM_000055_PARTIAL_MATRIX_ONCE"], environment)
    require(one_gate.returncode == 2 and one_gate.stderr == "" and "database_access=false migration_executed=false" in one_gate.stdout, "单门禁未失败关闭")
    cases = run_attack_model(script, boundary_text)
    require(cases == 34, "攻击模型数量错误")
    return cases


def main() -> int:
    try:
        cases = execute_contract()
        if __debug__:
            optimized = subprocess.run([sys.executable, "-O", str(Path(__file__).resolve())], check=False, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            require(optimized.returncode == 0 and optimized.stderr == "", "Python -O子进程失败")
            require(optimized.stdout.strip() == OPTIMIZED_SENTINEL, "Python -O摘要错误")
            print(
                "status=pass checks=boundary_manifest,static,bash,selftest,default_closed,one_gate_closed "
                f"attack_cases={cases} optimized=true database_access=false migration_executed=false "
                f"up_points=16 down_points=15 baselines=2 runner_sha256={sha256(RUNNER)}"
            )
        else:
            print(OPTIMIZED_SENTINEL)
        return 0
    except (ContractError, OSError, UnicodeError) as exc:
        print(f"status=failed classification=offline_contract detail={exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

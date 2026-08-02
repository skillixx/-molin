from __future__ import annotations

import ast
import hashlib
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "tests/email/run-000055-container-isolation-matrix.sh"
UP_MIGRATION = ROOT / "server/migrations/000055_add_directmail_email_management.up.sql"
DOWN_MIGRATION = ROOT / "server/migrations/000055_add_directmail_email_management.down.sql"
EXPECTED_UP_SHA = "7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D"
EXPECTED_DOWN_SHA = "217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE"
OPTIMIZED_SENTINEL = "optimized_contract=pass|attack_cases=31"


class ContractError(RuntimeError):
    """表示 000055 隔离执行资产未满足离线安全契约。"""


def require(condition: bool, message: str) -> None:
    """使用显式异常，保证 Python 优化模式不会移除安全断言。"""
    if not condition:
        raise ContractError(message)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def locate_bash() -> str:
    bash = shutil.which("bash")
    if bash is None:
        candidates = (
            Path(r"C:\Program Files\Git\bin\bash.exe"),
            Path(r"C:\Program Files\Git\usr\bin\bash.exe"),
        )
        bash = next((str(path) for path in candidates if path.is_file()), None)
    require(bash is not None, "缺少本地 Bash，无法执行离线语法与默认关闭自检")
    return str(bash)


def run_local(arguments: list[str], environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    """只运行本地 Shell；调用方不得提供完整执行双门禁。"""
    return subprocess.run(
        [locate_bash(), "--noprofile", "--norc", str(RUNNER), *arguments],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )


def contract_errors(script: str) -> list[str]:
    errors: list[str] = []

    def contains(fragment: str, label: str) -> None:
        if fragment not in script:
            errors.append(label)

    contains("readonly confirm_phrase=I_CONFIRM_000055_ISOLATION_MATRIX_ONCE", "缺少参数确认短语")
    contains("readonly execute_gate=I_UNDERSTAND_NEW_ISOLATION_DATABASES_WILL_BE_CREATED", "缺少环境执行门禁")
    contains("[[ $1 = --execute && $2 = \"$confirm_phrase\" ]] || blocked", "参数门禁未失败关闭")
    contains("[[ ${MOLIN_000055_ISOLATION_EXECUTE:-} = \"$execute_gate\" ]] || blocked", "环境门禁未失败关闭")
    contains("database_access=false migration_executed=false", "默认关闭摘要不完整")
    contains("readonly target_prefix=molin_55mx_", "隔离库前缀未固定")
    contains("uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)", "目标未使用系统 UUID")
    contains('target_db="${target_prefix}${suffix}_${case_name}"', "目标未由运行时 UUID 派生")
    contains("[[ ! -e \"$run_dir\" ]]", "运行目录复用未拒绝")
    contains("target_id_sha256=%s", "目标名称未只输出摘要")
    contains("targets_retained=true", "执行证据保留语义缺失")
    contains("partial_fault_injection=not_run", "未覆盖 partial 未明确声明")
    contains("runtime_unique_targets=7", "目标数量摘要不正确")
    contains("empty_schema54_up_down=true", "空基线周期摘要缺失")
    contains("legacy_schema54_up_down=true", "历史基线周期摘要缺失")
    contains("schema55_down=true", "schema55 down 摘要缺失")
    contains("ownership_combinations=4", "ownership 四组合摘要缺失")

    contains(f"readonly expected_up_sha={EXPECTED_UP_SHA}", "控制器未冻结当前 Up SHA")
    contains(f"readonly expected_down_sha={EXPECTED_DOWN_SHA}", "控制器未冻结当前 Down SHA")
    contains("verify_asset \"$up_file\" \"$expected_up_sha\"", "执行前未复核 Up SHA")
    contains("verify_asset \"$down_file\" \"$expected_down_sha\"", "执行前未复核 Down SHA")
    contains("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "缺少 bind mount 资产 UID 输入")
    contains('[[ "$expected_asset_uid" =~ ^[1-9][0-9]*$ ]]', "资产 UID 格式门禁缺失")
    contains('[[ $($STAT_BIN -c %u -- "$asset_dir") = "$expected_asset_uid" ]]', "资产目录属主未绑定")
    contains('[[ $($STAT_BIN -c %u -- "$file") = "$expected_asset_uid" ]]', "资产文件属主未绑定")
    if script.count("trap - ERR\n  set +e") != 3 or script.count("set -e\n  trap on_error ERR") != 3:
        errors.append("显式退出码捕获未临时卸载并恢复 ERR trap")
    contains("local file=$1 mode=${2:-enforced} exit_code", "基线恢复模式门禁缺失")
    contains("SET SESSION FOREIGN_KEY_CHECKS=0;", "基线恢复未临时关闭会话外键检查")
    contains("SET SESSION FOREIGN_KEY_CHECKS=1;", "基线恢复未恢复会话外键检查")
    contains('mysql_file "$baseline" baseline_restore', "仅基线恢复路径未显式启用恢复模式")
    contains('emit_instrumented_down "$instrumented_down"', "Down 未生成同会话阶段标记")
    contains('mysql_file "$instrumented_down"', "Down 未执行冻结后的阶段标记副本")
    contains("statement != 24 || !pending", "Down 语句数量未严格冻结")
    contains('stage="${current_case}_down_statement_${BASH_REMATCH[1]}"', "Down 失败未收敛到固定语句阶段")
    contains("expected >= 1 && expected <= 24", "Down 标记序列未限制范围")
    contains('    fi\n    : > "$evidence_dir/mysql.stdout"\n    report_mysql_failure', "Down 失败输出未在分类前清空")
    contains("error 3819|error 4025|error 1451|error 1452", "MySQL 约束错误未归入固定分类")

    for baseline in ("schema54-empty.sql", "schema54-legacy.sql", "schema55.sql"):
        contains(baseline, f"缺少基线资产 {baseline}")
    contains("readonly manifest_file=\"$asset_dir/baseline-manifest.tsv\"", "缺少基线摘要清单")
    contains("[[ ${#manifest_sha[@]} -eq 3 ]]", "基线摘要清单未限制为三项")
    contains("${manifest_version[schema54-empty.sql]} = 54", "空基线版本未冻结")
    contains("${manifest_kind[schema54-empty.sql]} = empty", "空基线类型未冻结")
    contains("${manifest_version[schema54-legacy.sql]} = 54", "历史基线版本未冻结")
    contains("${manifest_kind[schema54-legacy.sql]} = legacy", "历史基线类型未冻结")
    contains("${manifest_version[schema55.sql]} = 55", "schema55基线版本未冻结")
    contains("${manifest_kind[schema55.sql]} = complete", "schema55基线类型未冻结")
    contains('/\\*![0-9]*', "基线可执行注释控制语句未拒绝")
    contains("CREATE DATABASE \\`$target_db\\`", "未仅创建运行时目标")

    guard_position = script.find("if [[ ${1:-} = --self-test")
    argument_gate_position = script.find("[[ $# -eq 2 ]] || blocked")
    environment_gate_position = script.find("[[ ${MOLIN_000055_ISOLATION_EXECUTE:-}")
    first_mysql_process = script.find('MYSQL_PWD="$MYSQL_ROOT_PASSWORD"')
    if min(guard_position, argument_gate_position, environment_gate_position, first_mysql_process) < 0:
        errors.append("无法证明数据库调用位于双门禁之后")
    elif not guard_position < argument_gate_position < environment_gate_position < first_mysql_process:
        errors.append("默认关闭或双门禁晚于数据库调用")

    mysql_invocations = re.findall(r'(?m)^\s*MYSQL_PWD=.*?"\$MYSQL_BIN"\s+([^\n]+)', script)
    if len(mysql_invocations) != 4:
        errors.append("MySQL 客户端调用数量异常")
    if any(not invocation.startswith("--no-defaults --default-character-set=utf8mb4 ") for invocation in mysql_invocations):
        errors.append("MySQL 未固定 option 隔离和 UTF-8 客户端字符集")

    dangerous_patterns = (
        r"(?im)\bDROP\s+(?:DATABASE|SCHEMA)\b",
        r"(?im)\bTRUNCATE\b",
        r"(?im)\bmysqladmin\b",
        r"(?im)(?:^|[;&|]\s*)rm\s",
        r"(?im)\bFLUSHDB\b|\bKEYS\s+\*",
    )
    for pattern in dangerous_patterns:
        if re.search(pattern, script):
            errors.append(f"出现危险模式:{pattern}")

    if '--database=molin' in script or '--database="$source_db"' in script or re.search(r"(?i)\bmolin\.[a-z_]", script):
        errors.append("可能选择或写入测试主库")
    if re.search(
        r"(?i)\bmolin_55mx_[a-f0-9]{32}_(?:empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)\b",
        script,
    ):
        errors.append("出现硬编码的旧隔离库字面量")

    # 管理连接只能检查本轮随机目标是否不存在并创建该目标；任何新增调用都可能绕过目标库绑定。
    mysql_admin_lines = [
        line.strip()
        for line in script.splitlines()
        if re.search(r"\bmysql_admin\b", line) and not re.match(r"\s*mysql_admin\(\)\s*\{", line)
    ]
    expected_mysql_admin_lines = [
        '[[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = \'$target_db\';") = 0 ]]',
        'mysql_admin "CREATE DATABASE \\`$target_db\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null',
    ]
    if mysql_admin_lines != expected_mysql_admin_lines:
        errors.append("mysql_admin 仅允许检查并创建本轮随机隔离库")
    if re.search(r'(?i)mysql_(?:admin|query)\s+"[^"\n]*(?:GRANT|REVOKE|CREATE\s+USER|DROP\s+USER|ALTER\s+USER)', script):
        errors.append("控制器不得变更账号或授权")
    if "printf 'target_db=" in script or "printf 'run_dir=" in script:
        errors.append("完整随机目标可能输出")
    if '"$CAT_BIN" "$evidence_dir/mysql.stderr"' in script or "2>/dev/null" in script:
        errors.append("MySQL 原始错误可能泄露或被丢弃")

    stages = (
        "stage=empty_baseline",
        "stage=legacy_baseline",
        "stage=schema55_baseline",
        "stage=ownership_matrix",
        "stage=matrix_complete",
    )
    positions = [script.find(stage) for stage in stages]
    if any(position < 0 for position in positions) or positions != sorted(positions):
        errors.append("主矩阵阶段缺失或顺序错误")

    ownership_calls = re.findall(r"(?m)^run_ownership_case\s+(own[a-z]+)\s+", script)
    if ownership_calls != ["ownfresh", "ownperm", "ownall", "ownmixed"]:
        errors.append("ownership 组合不精确")
    if len(re.findall(r"(?m)^new_target\s+(?:empty|legacy|schema55)\s+", script)) != 3:
        errors.append("基础迁移路径数量不精确")

    required_runtime_checks = (
        "assert_schema54",
        "assert_schema55",
        "constraint_type='CHECK'",
        "constraint_type='FOREIGN KEY'",
        "COUNT(DISTINCT CONCAT(table_name, CHAR(31), index_name))",
        "历史邮箱已失效",
        "send_status<>'failed'",
        "target_type='phone'",
        "assert_ownership_flags",
        "assert_preserved_counts",
        "SELECT COUNT(*) FROM INFORMATION_SCHEMA.EVENTS WHERE EVENT_SCHEMA = '$target_db';",
        "SUM(data_type = 'varchar' AND character_maximum_length = $expected_code_length AND is_nullable = '$expected_code_nullable') = 1",
        "assert_schema54 16 NO schema54_baseline",
        "assert_schema54 64 YES schema54_down",
        'stage="${case_name}_baseline_restore"',
        'stage="${case_name}_baseline_version"',
        'stage="${case_name}_baseline_version_cardinality"',
        'stage="${case_name}_database_binding"',
        'stage="${case_name}_engine_policy"',
        'stage="${case_name}_view_policy"',
        'stage="${case_name}_trigger_policy"',
        'stage="${case_name}_routine_policy"',
        'stage="${case_name}_event_policy"',
        "stage=empty_schema54_validate",
        "stage=empty_permissions_absent",
        "stage=empty_verification_empty",
        'stage="${current_case}_${phase}_version"',
        'stage="${current_case}_${phase}_table_absence"',
        'stage="${current_case}_${phase}_code_shape"',
        'stage="${current_case}_${phase}_code_hash_absence"',
    )
    for fragment in required_runtime_checks:
        contains(fragment, f"缺少运行时断言:{fragment}")
    return errors


def run_attack_model(script: str) -> int:
    mutations = {
        "删除参数门禁": script.replace("[[ $# -eq 2 ]] || blocked", ":", 1),
        "删除环境门禁": script.replace('[[ ${MOLIN_000055_ISOLATION_EXECUTE:-} = "$execute_gate" ]] || blocked', ":", 1),
        "删除资产UID绑定": script.replace("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "readonly expected_asset_uid=", 1),
        "删除基线断言阶段": script.replace('stage="${case_name}_baseline_version"', 'stage="${case_name}_baseline_restore"', 1),
        "删除退出码捕获trap隔离": script.replace("trap - ERR\n  set +e", "set +e", 1),
        "删除基线外键恢复保护": script.replace("SET SESSION FOREIGN_KEY_CHECKS=0;", "SET SESSION FOREIGN_KEY_CHECKS=1;", 1),
        "删除Down同会话标记": script.replace('mysql_file "$instrumented_down"', 'mysql_file "$down_file"', 1),
        "放宽Down语句数量": script.replace("statement != 24 || !pending", "statement < 1", 1),
        "放宽Down标记序列": script.replace("expected >= 1 && expected <= 24", "expected >= 1", 1),
        "泄露Down执行输出": script.replace('    fi\n    : > "$evidence_dir/mysql.stdout"\n    report_mysql_failure', '    fi\n    "$CAT_BIN" "$evidence_dir/mysql.stdout"\n    report_mysql_failure', 1),
        "删除约束错误分类": script.replace("error 3819|error 4025|error 1451|error 1452", "never_match_constraint", 1),
        "事件检查未绑定目标库": script.replace("EVENT_SCHEMA = '$target_db'", "EVENT_SCHEMA = DATABASE()", 1),
        "空库后续阶段继续冒充事件检查": script.replace("stage=empty_schema54_validate", 'stage="${case_name}_event_policy"', 1),
        "恢复ONLY_FULL_GROUP_BY非法断言": script.replace("SUM(data_type = 'varchar' AND character_maximum_length = $expected_code_length AND is_nullable = '$expected_code_nullable') = 1", "data_type = 'varchar' AND character_maximum_length = $expected_code_length AND is_nullable = '$expected_code_nullable'", 1),
        "混淆Up前基线与Down后兼容结构": script.replace("assert_schema54 16 NO schema54_baseline", "assert_schema54 64 YES schema54_baseline", 1),
        "数据库调用提前": script.replace("stage=initialization", 'stage=initialization\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --execute="SELECT 1"', 1),
        "固定目标库": script.replace('target_db="${target_prefix}${suffix}_${case_name}"', "target_db=molin_restore_legacy_fixed", 1),
        "硬编码旧隔离库字面量": script + "\nlegacy_target=molin_55mx_00000000000040008000000000000000_empty\n",
        "通过mysql_admin访问旧库": script
        + '\nmysql_admin "SELECT COUNT(*) FROM `molin_55mx_00000000000040008000000000000000_empty`.schema_migrations;"\n',
        "输出完整目标": script + "\nprintf 'target_db=%s\\n' \"$target_db\"\n",
        "删除Up摘要": script.replace(f"readonly expected_up_sha={EXPECTED_UP_SHA}", "readonly expected_up_sha=UNKNOWN", 1),
        "删除Down摘要": script.replace(f"readonly expected_down_sha={EXPECTED_DOWN_SHA}", "readonly expected_down_sha=UNKNOWN", 1),
        "读取客户端配置": script.replace('"$MYSQL_BIN" --no-defaults', '"$MYSQL_BIN"', 1),
        "移除UTF8客户端字符集": script.replace(" --default-character-set=utf8mb4", "", 1),
        "删除基线清单": script.replace("readonly manifest_file=", "readonly manifest_file_removed=", 1),
        "移除空库路径": script.replace("empty_schema54_up_down=true", "empty_schema54_up_down=false", 1),
        "移除历史路径": script.replace("legacy_schema54_up_down=true", "legacy_schema54_up_down=false", 1),
        "移除schema55回滚": script.replace("schema55_down=true", "schema55_down=false", 1),
        "减少ownership组合": script.replace("ownership_combinations=4", "ownership_combinations=3", 1),
        "增加删除隔离库": script + "\nDROP DATABASE runtime_target;\n",
        "选择测试主库": script + '\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --database=molin --execute="SELECT 1"\n',
    }
    expected_rejections = {
        "硬编码旧隔离库字面量": "出现硬编码的旧隔离库字面量",
        "通过mysql_admin访问旧库": "mysql_admin 仅允许检查并创建本轮随机隔离库",
    }
    for name, mutated in mutations.items():
        errors = contract_errors(mutated)
        require(bool(errors), f"攻击模型未被拒绝:{name}")
        if name in expected_rejections:
            require(expected_rejections[name] in errors, f"攻击模型未命中独立拒绝规则:{name}")
    return len(mutations)


def execute_contract() -> int:
    script = RUNNER.read_text(encoding="utf-8")
    require(sha256(UP_MIGRATION) == EXPECTED_UP_SHA, "000055 Up SHA 已偏离冻结值")
    require(sha256(DOWN_MIGRATION) == EXPECTED_DOWN_SHA, "000055 Down SHA 已偏离冻结值")

    syntax = subprocess.run(
        [locate_bash(), "--noprofile", "--norc", "-n", str(RUNNER)],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    require(syntax.returncode == 0 and syntax.stderr == "", "000055 控制器 Bash 语法检查失败")
    require(not contract_errors(script), "000055 控制器离线契约失败:" + ",".join(contract_errors(script)))

    selftest = run_local(["--self-test"])
    require(selftest.returncode == 0, "自检退出码异常")
    require(selftest.stderr == "", "自检产生非预期 stderr")
    require(
        selftest.stdout.strip()
        == "status=pass mode=selftest cases=22 database_access=false migration_executed=false runtime_target=not_created",
        "自检摘要不符合冻结契约",
    )

    default = run_local([])
    require(default.returncode == 2, "默认调用必须失败关闭")
    require(default.stderr == "", "默认关闭产生非预期 stderr")
    require(
        default.stdout.strip()
        == "status=blocked reason=explicit_double_gate_required database_access=false migration_executed=false",
        "默认关闭摘要不符合冻结契约",
    )

    partial_environment = dict(os.environ)
    partial_environment["MOLIN_000055_ISOLATION_EXECUTE"] = "WRONG"
    one_gate = run_local(["--execute", "I_CONFIRM_000055_ISOLATION_MATRIX_ONCE"], partial_environment)
    require(one_gate.returncode == 2, "单门禁不得进入执行路径")
    require(one_gate.stderr == "", "单门禁拒绝产生非预期 stderr")
    require("database_access=false migration_executed=false" in one_gate.stdout, "单门禁拒绝缺少无副作用摘要")
    return run_attack_model(script)


def run_optimized_child() -> None:
    environment = dict(os.environ)
    environment["PYTHONOPTIMIZE"] = "1"
    result = subprocess.run(
        [sys.executable, "-O", str(Path(__file__).resolve()), "--optimized-child"],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )
    require(result.returncode == 0, "Python -O 离线契约失败")
    require(result.stderr == "", "Python -O 离线契约产生非预期 stderr")
    require(result.stdout.strip() == OPTIMIZED_SENTINEL, "Python -O 未执行完整攻击模型")


def main() -> None:
    module = ast.parse(Path(__file__).read_text(encoding="utf-8"))
    require(not any(isinstance(node, ast.Assert) for node in ast.walk(module)), "离线契约禁止使用 assert")
    attack_cases = execute_contract()
    if "--optimized-child" in sys.argv:
        require(sys.flags.optimize >= 1, "优化模式子进程未启用")
        print(f"optimized_contract=pass|attack_cases={attack_cases}")
        return
    run_optimized_child()
    print("000055_container_isolation_matrix_contract=pass")
    print("optimized_contract=pass")
    print("selftest_database_access=false")
    print("migration_executed=false")
    print("runtime_targets=unique_uuid_only")
    print("source_database_selected=false")
    print("matrix=empty54_legacy54_schema55_down_ownership4")
    print("partial_fault_injection=not_implemented")
    print(f"attack_cases={attack_cases}")
    print(f"up_sha256={EXPECTED_UP_SHA}")
    print(f"down_sha256={EXPECTED_DOWN_SHA}")
    print(f"runner_sha256={sha256(RUNNER)}")


if __name__ == "__main__":
    try:
        main()
    except ContractError as exc:
        print(f"FAIL 000055_container_isolation_matrix_contract reason={exc}", file=sys.stderr)
        raise SystemExit(1)

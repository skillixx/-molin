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
CYCLE_SCRIPT = ROOT / "scripts/run-000057-container-backup-restore-cycle.sh"
UP_MIGRATION = ROOT / "server/migrations/000057_fix_email_datetime_utc_seconds.up.sql"
DOWN_MIGRATION = ROOT / "server/migrations/000057_fix_email_datetime_utc_seconds.down.sql"
EXPECTED_UP_SHA = "50DCD97A45D8ADCF2F7CAC316B44D942DDB880D4F922B8872CAA34BA01CFC67C"
EXPECTED_DOWN_SHA = "EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB"
EXPECTED_SCRIPT_SHA = "D3A4B8A318D101640BFC130A482ECE423D61B63F63DC36DF6E89D497A7AF83A6"
OPTIMIZED_SENTINEL = "optimized_contract=pass|fault_injection_cases=12"


class ContractError(RuntimeError):
    """表示隔离周期资产未满足离线安全契约。"""


def require(condition: bool, message: str) -> None:
    """显式抛出异常，确保 Python 优化模式不会移除任何校验。"""
    if not condition:
        raise ContractError(message)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def extract_literal_calls(script: str, function_name: str) -> list[str]:
    pattern = rf'{re.escape(function_name)}\s+"((?:[^"\\]|\\.)*)"'
    return re.findall(pattern, script)


def is_select_only(sql: str) -> bool:
    normalized = re.sub(r"\s+", " ", sql.strip()).upper()
    if not normalized.startswith("SELECT "):
        return False
    write_tokens = r"\b(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD|LOCK|UNLOCK|SET)\b"
    return re.search(write_tokens, normalized) is None


def contract_errors(script: str) -> list[str]:
    errors: list[str] = []

    def contains(fragment: str, name: str) -> None:
        if fragment not in script:
            errors.append(name)

    contains("readonly source_db=molin", "源库固定")
    contains("[[ $(source_version_dirty) = 57:0 ]]", "源库57门禁")
    contains("table_type = 'BASE TABLE';\")\" = 69", "源库表数门禁")
    contains("--single-transaction --quick --skip-lock-tables", "单事务备份")
    contains("readonly asset_dir=/root/molin-000057-schema57-cycle-assets", "只读资产目录")
    contains("target_uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)", "单次随机源")
    contains('target_db="${target_prefix}${target_suffix}"', "随机隔离库")
    contains('run_dir="/root/molin-000057-schema57-cycle-run-${target_suffix}"', "随机运行目录")
    contains('[[ ! -e "$run_dir" ]]', "运行目录不得复用")
    contains("readonly target_db", "目标冻结")
    contains("readonly run_dir evidence_dir dump_file", "运行资产冻结")
    contains("schema57_shape", "schema57结构")
    contains("schema56_shape", "schema56结构")
    contains("receipt_original_hash_from_backup", "毫秒原值摘要")
    contains('[[ $(receipt_full_hash) = "$receipt_original_before" ]]', "Down原值恢复")
    contains('[[ $(receipt_full_hash) = "$receipt_full_before" ]]', "Up秒值恢复")
    contains("backup_receipt_count_before", "备份行数保持")
    contains("assert_data_hashes", "非时间指纹保持")
    contains("source_write_commands_performed=false", "源库未写摘要")
    contains("down_up_down_up_cycle=true", "完整周期摘要")
    contains("report_mysql_failure", "MySQL安全错误摘要")
    contains("mysql_failure_category=%s", "MySQL错误分类")
    contains("mysql_stderr_length=%s", "MySQL错误长度")

    stages = (
        "stage=first_down_mark_dirty",
        "stage=first_down_sql",
        "stage=first_down_finalize",
        "stage=first_down_validate",
        "stage=first_up_mark_dirty",
        "stage=first_up_sql",
        "stage=first_up_finalize",
        "stage=first_up_validate",
        "stage=second_down_mark_dirty",
        "stage=second_down_sql",
        "stage=second_down_finalize",
        "stage=second_down_validate",
        "stage=second_up_mark_dirty",
        "stage=second_up_sql",
        "stage=second_up_finalize",
        "stage=second_up_validate",
    )
    positions = [script.find(stage) for stage in stages]
    if any(position < 0 for position in positions):
        errors.append("周期阶段缺失")
    elif positions != sorted(positions) or len(set(positions)) != len(positions):
        errors.append("Down-Up-Down-Up顺序")

    if len(re.findall(r"(?m)^target_uuid=", script)) != 1:
        errors.append("随机源读取次数")
    if len(re.findall(r"(?m)^target_db=", script)) != 2:
        errors.append("目标赋值次数")
    if len(re.findall(r"(?m)^run_dir=", script)) != 2:
        errors.append("运行目录赋值次数")
    isolation_scan = script.replace("readonly target_prefix=molin_restore_57_reverify_", "", 1)
    isolation_scan = isolation_scan.replace('[[ "$target_db" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]', "", 1)
    if "molin_restore_" in isolation_scan:
        errors.append("出现旧隔离库或额外恢复库前缀")
    if re.search(r"(?i)(?:legacy|old|previous)[_-]?(?:isolation|restore|schema|database|db)", script):
        errors.append("出现旧隔离库语义引用")
    if "printf 'target_db=" in script or "printf 'run_dir=" in script:
        errors.append("随机名称输出")

    dangerous_patterns = (
        r"(?im)\bDROP\s+(?:DATABASE|SCHEMA)\b",
        r"(?im)\bmysqladmin\b",
        r"(?im)(?:^|[;&|]\s*)rm\s",
        r"(?im)\bTRUNCATE\s+TABLE\b",
        r"(?im)\bDELETE\s+FROM\b",
        r"(?im)\bforce\b",
    )
    for pattern in dangerous_patterns:
        if re.search(pattern, script):
            errors.append(f"危险模式:{pattern}")

    mysql_invocations = re.findall(r'(?m)^\s*MYSQL_PWD=.*?"\$(MYSQL(?:DUMP)?_BIN)"\s+([^\n]+)', script)
    if len(mysql_invocations) != 4:
        errors.append("MySQL客户端调用数量异常")
    for binary_name, arguments in mysql_invocations:
        if not arguments.startswith("--no-defaults "):
            errors.append(f"{binary_name}未将--no-defaults作为首参数")
    if re.search(r'(?m)^\s*MYSQL_PWD=.*?"\$MYSQL(?:DUMP)?_BIN"\s+--(?!no-defaults\b)', script):
        errors.append("MySQL客户端可能读取option files")

    source_calls = re.findall(r'mysql_query_db\s+"\$source_db"\s+"((?:[^"\\]|\\.)*)"', script)
    if len(source_calls) < 8 or any(not is_select_only(sql) for sql in source_calls):
        errors.append("源库调用并非全部只读SELECT")
    if '--database="$source_db"' in script:
        errors.append("源库被直接数据库客户端选中")

    admin_calls = extract_literal_calls(script, "mysql_admin_query")
    if len(admin_calls) < 3:
        errors.append("管理查询无法完整审计")
    for sql in admin_calls:
        references_source = "$source_db" in sql or re.search(r"(?i)\bmolin\.", sql) is not None
        if references_source and not is_select_only(sql):
            errors.append("管理查询可能写入源库")
    if re.search(r'mysql_admin_query\s+"\$[^"\n]+"', script):
        errors.append("管理查询使用不可审计动态SQL")

    source_write_pattern = r"(?is)(?:\$source_db|\bmolin\.)[^\n\"]{0,240}\b(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD)\b"
    reverse_source_write_pattern = r"(?is)\b(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD)\b[^\n\"]{0,240}(?:\$source_db|\bmolin\.)"
    if re.search(source_write_pattern, script) or re.search(reverse_source_write_pattern, script):
        errors.append("检测到源库写SQL")

    if '"$CAT_BIN" "$evidence_dir/mysql.stderr"' in script or "2>/dev/null" in script:
        errors.append("MySQL原始错误可能泄露或被丢弃")
    return errors


def run_fault_injection_model(script: str) -> int:
    mutations = {
        "源版本降级": script.replace("[[ $(source_version_dirty) = 57:0 ]]", "[[ $(source_version_dirty) = 56:0 ]]", 1),
        "移除第二次Down": script.replace("stage=second_down_sql", "stage=second_down_removed", 1),
        "移除最终Up": script.replace("stage=second_up_sql", "stage=second_up_removed", 1),
        "固定旧隔离库": script.replace('target_db="${target_prefix}${target_suffix}"', "target_db=molin_restore_56_legacy_deadbeef", 1),
        "增加删除旧库": script + "\nDROP DATABASE archived_schema;\n",
        "允许复用运行目录": script.replace('[[ ! -e "$run_dir" ]]', ":", 1),
        "移除毫秒恢复": script.replace('[[ $(receipt_full_hash) = "$receipt_original_before" ]]', ":"),
        "移除非时间指纹": script.replace("assert_data_hashes", "data_integrity_removed"),
        "移除mysql配置隔离": script.replace('"$MYSQL_BIN" --no-defaults', '"$MYSQL_BIN"', 1),
        "源库直接写入": script + '\nmysql_query_db "$source_db" "UPDATE users SET status = status;"\n',
        "管理通道写源库": script + '\nmysql_admin_query "DELETE FROM molin.users;"\n',
        "泄露原始错误": script + '\n"$CAT_BIN" "$evidence_dir/mysql.stderr"\n',
    }
    for name, mutated in mutations.items():
        require(bool(contract_errors(mutated)), f"故障注入未被离线契约拒绝: {name}")
    return len(mutations)


def locate_bash() -> str:
    bash = shutil.which("bash")
    if bash is None:
        candidates = (Path(r"C:\Program Files\Git\bin\bash.exe"), Path(r"C:\Program Files\Git\usr\bin\bash.exe"))
        bash = next((str(path) for path in candidates if path.is_file()), None)
    require(bash is not None, "缺少可用于bash -n的本地Bash")
    return str(bash)


def execute_contract() -> int:
    script = CYCLE_SCRIPT.read_text(encoding="utf-8")
    require(sha256(UP_MIGRATION) == EXPECTED_UP_SHA, "000057 Up SHA-256不符合冻结值")
    require(sha256(DOWN_MIGRATION) == EXPECTED_DOWN_SHA, "000057 Down SHA-256不符合冻结值")
    require(sha256(CYCLE_SCRIPT) == EXPECTED_SCRIPT_SHA, "000057周期脚本SHA-256不符合冻结值")
    require(f"readonly expected_up_sha={EXPECTED_UP_SHA}" in script, "脚本未冻结Up SHA")
    require(f"readonly expected_down_sha={EXPECTED_DOWN_SHA}" in script, "脚本未冻结Down SHA")

    syntax_result = subprocess.run(
        [locate_bash(), "--noprofile", "--norc", "-n", str(CYCLE_SCRIPT)],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    require(syntax_result.returncode == 0, "隔离周期Shell语法检查失败")
    require(not syntax_result.stderr, "Shell语法检查产生非预期stderr")

    errors = contract_errors(script)
    require(not errors, "隔离执行资产契约失败: " + ", ".join(errors))
    return run_fault_injection_model(script)


def run_optimized_selftest() -> None:
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
    require(result.returncode == 0, "PYTHONOPTIMIZE=1离线契约失败")
    require(result.stderr == "", "PYTHONOPTIMIZE=1离线契约产生非预期stderr")
    require(result.stdout.strip() == OPTIMIZED_SENTINEL, "PYTHONOPTIMIZE=1未真实执行完整校验")


def main() -> None:
    module_tree = ast.parse(Path(__file__).read_text(encoding="utf-8"))
    require(not any(isinstance(node, ast.Assert) for node in ast.walk(module_tree)), "离线契约禁止使用assert语句")
    fault_cases = execute_contract()

    if "--optimized-child" in sys.argv:
        require(sys.flags.optimize >= 1, "优化模式子进程未启用Python优化")
        print(f"optimized_contract=pass|fault_injection_cases={fault_cases}")
        return

    run_optimized_selftest()
    print("000057_container_cycle_contract=pass")
    print("optimized_contract=pass")
    print("source_gate=57:0_read_only")
    print("mysql_option_files=disabled")
    print("mysql_failure_output=safe_category_and_length_only")
    print("cycle=down_up_down_up")
    print("database_access=false")
    print("migration_executed=false")
    print("runtime_target=unique_uuid")
    print("legacy_isolation_touched=false")
    print(f"fault_injection_cases={fault_cases}")
    print(f"up_sha256={EXPECTED_UP_SHA}")
    print(f"down_sha256={EXPECTED_DOWN_SHA}")
    print(f"script_sha256={EXPECTED_SCRIPT_SHA}")


if __name__ == "__main__":
    main()

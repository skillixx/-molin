from __future__ import annotations

import hashlib
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "tests/email/run-000056-container-isolation-matrix.sh"
UP_MIGRATION = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.up.sql"
DOWN_MIGRATION = ROOT / "server/migrations/000056_add_email_admin_verify_bootstrap.down.sql"
EXPECTED_UP_SHA = "9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B"
EXPECTED_DOWN_SHA = "F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2"
OPTIMIZED_SENTINEL = "optimized_contract=pass|attack_cases=24"


class ContractError(RuntimeError):
    """表示 000056 隔离执行资产未满足离线安全契约。"""


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
    require(bash is not None, "缺少本地 Bash，无法执行离线语法与默认关闭验证")
    return str(bash)


def run_local(arguments: list[str], environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    """只运行默认关闭或 SelfTest 路径，调用方不得同时提供完整双门禁。"""
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

    # 默认关闭、双门禁和当前 migration 身份必须全部冻结。
    contains("readonly confirm_phrase=I_CONFIRM_000056_ISOLATION_MATRIX_ONCE", "缺少参数确认短语")
    contains(
        "readonly execute_gate=I_UNDERSTAND_000056_NEW_ISOLATION_DATABASES_WILL_BE_CREATED",
        "缺少环境执行门禁",
    )
    contains('[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked', "参数门禁未失败关闭")
    contains('[[ ${MOLIN_000056_ISOLATION_EXECUTE:-} = "$execute_gate" ]] || blocked', "环境门禁未失败关闭")
    contains("database_access=false migration_executed=false", "默认关闭摘要不完整")
    contains(f"readonly expected_up_sha={EXPECTED_UP_SHA}", "未冻结当前 Up SHA")
    contains(f"readonly expected_down_sha={EXPECTED_DOWN_SHA}", "未冻结当前 Down SHA")
    contains('verify_asset "$up_file" "$expected_up_sha"', "执行前未复核 Up SHA")
    contains('verify_asset "$down_file" "$expected_down_sha"', "执行前未复核 Down SHA")
    contains("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "缺少 bind mount 资产 UID 输入")
    contains('[[ "$expected_asset_uid" =~ ^[1-9][0-9]*$ ]]', "资产 UID 格式门禁缺失")
    contains('[[ $($STAT_BIN -c %u -- "$asset_dir") = "$expected_asset_uid" ]]', "资产目录属主未绑定")
    contains('[[ $($STAT_BIN -c %u -- "$file") = "$expected_asset_uid" ]]', "资产文件属主未绑定")
    if script.count("trap - ERR\n  set +e") != 5 or script.count("set -e\n  trap on_error ERR") != 5:
        errors.append("显式退出码捕获未临时卸载并恢复 ERR trap")
    contains("local file=$1 mode=${2:-enforced} exit_code", "基线恢复模式门禁缺失")
    contains("SET SESSION FOREIGN_KEY_CHECKS=0;", "基线恢复未临时关闭会话外键检查")
    contains("SET SESSION FOREIGN_KEY_CHECKS=1;", "基线恢复未恢复会话外键检查")
    contains('mysql_file "$baseline_file" baseline_restore', "仅基线恢复路径未显式启用恢复模式")

    # 基线只能恢复到运行时 UUID 目标；证据保留且不得输出完整库名。
    contains("readonly asset_dir=/root/molin-000056-isolation-assets", "固定资产目录缺失")
    contains('readonly baseline_file="$asset_dir/schema55.sql"', "schema55 基线缺失")
    contains('readonly baseline56_file="$asset_dir/schema56.sql"', "schema56 基线缺失")
    contains('readonly manifest_file="$asset_dir/baseline-manifest.tsv"', "基线清单缺失")
    contains('[[ ${manifest_version[schema55.sql]} = 55 && ${manifest_kind[schema55.sql]} = complete ]]', "schema55 基线版本或类型未冻结")
    contains('[[ ${manifest_version[schema56.sql]} = 56 && ${manifest_kind[schema56.sql]} = complete ]]', "schema56 基线版本或类型未冻结")
    contains('[[ ${manifest_sha[schema55.sql]} = "$(verify_asset "$baseline_file")" ]]', "schema55 清单摘要未绑定文件")
    contains('[[ ${manifest_sha[schema56.sql]} = "$(verify_asset "$baseline56_file")" ]]', "schema56 清单摘要未绑定文件")
    contains('[[ $($WC_BIN -l < "$manifest_file") -eq 2 ]]', "共用清单未限制为两条记录")
    contains('for baseline in "$baseline_file" "$baseline56_file"', "安全扫描未覆盖两份基线")
    contains("readonly target_prefix=molin_56mx_", "隔离库前缀未冻结")
    contains('uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)', "目标未使用系统 UUID")
    contains('target_db="${target_prefix}${suffix}_${case_name}"', "目标未由运行时 UUID 派生")
    contains('[[ ! -e "$run_dir" ]]', "运行目录复用未拒绝")
    contains("target_id_sha256=%s", "目标名称未只输出摘要")
    contains("targets_retained=true", "证据保留语义缺失")
    contains("runtime_unique_targets=11", "运行时目标数量不正确")

    # 当前范围覆盖 3 个 ownership、3 个 Up 阻断、4 个 Down 阻断和 1 个并发目标。
    contains("ownership_combinations=3", "ownership 三组合摘要缺失")
    contains("admin_cardinality_blocks=2", "管理员基数阻断摘要缺失")
    contains("metadata_conflict_blocked=true", "权限元数据冲突阻断摘要缺失")
    contains("empty_receipt_down=true", "空 receipt 安全 Down 摘要缺失")
    contains("existing_receipt_blocked=true", "既有 receipt 阻断摘要缺失")
    contains("unknown_reference_blocks=3", "未知引用三组合摘要缺失")
    contains("concurrent_scope_unique=true", "并发唯一范围摘要缺失")
    contains("partial_fault_injection=not_implemented", "partial 缺口未显式声明")

    guard = script.find("if [[ ${1:-} = --self-test")
    argument_gate = script.find("[[ $# -eq 2 ]] || blocked")
    environment_gate = script.find("[[ ${MOLIN_000056_ISOLATION_EXECUTE:-}")
    first_mysql = script.find('MYSQL_PWD="$MYSQL_ROOT_PASSWORD"')
    if min(guard, argument_gate, environment_gate, first_mysql) < 0:
        errors.append("无法证明数据库调用位于双门禁之后")
    elif not guard < argument_gate < environment_gate < first_mysql:
        errors.append("默认关闭或双门禁晚于数据库调用")

    mysql_invocations = re.findall(r'(?m)^\s*MYSQL_PWD=.*?"\$MYSQL_BIN"\s+([^\n]+)', script)
    if len(mysql_invocations) != 7:
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
    if '--database=molin' in script or re.search(r"(?i)\bmolin\.[a-z_]", script):
        errors.append("可能选择或写入测试主库")
    if re.search(
        r"(?i)\bmolin_56mx_[a-f0-9]{32}_(?:ownfresh|ownperm|ownall|adminzero|admintwo|metaconf|receipt|refrole|refuser|refgroup|concurrent)\b",
        script,
    ):
        errors.append("出现硬编码旧隔离库字面量")

    # 管理连接只能确认本轮目标不存在并创建它，不能访问历史库或业务表。
    mysql_admin_lines = [
        line.strip()
        for line in script.splitlines()
        if re.search(r"\bmysql_admin\b", line) and not re.match(r"\s*mysql_admin\(\)\s*\{", line)
    ]
    expected_admin_lines = [
        '[[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = \'$target_db\';") = 0 ]]',
        'mysql_admin "CREATE DATABASE \\`$target_db\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null',
    ]
    if mysql_admin_lines != expected_admin_lines:
        errors.append("mysql_admin 仅允许检查并创建本轮随机隔离库")
    if re.search(r'(?i)mysql_(?:admin|query)\s+"[^"\n]*(?:GRANT|REVOKE|CREATE\s+USER|ALTER\s+USER|DROP\s+USER)', script):
        errors.append("控制器不得变更账号或授权")
    if "printf 'target_db=" in script or "printf 'run_dir=" in script:
        errors.append("完整随机目标可能输出")
    if '"$CAT_BIN" "$evidence_dir/mysql.stderr"' in script or "2>/dev/null" in script:
        errors.append("MySQL 原始错误可能泄露或被丢弃")

    stages = ("stage=ownership_matrix", "stage=up_block_matrix", "stage=down_block_matrix", "stage=concurrency_matrix", "stage=matrix_complete")
    positions = [script.find(stage) for stage in stages]
    if any(position < 0 for position in positions) or positions != sorted(positions):
        errors.append("主矩阵阶段缺失或顺序错误")
    ownership = re.findall(r"(?m)^run_ownership_case\s+(own[a-z]+)\s+", script)
    if ownership != ["ownfresh", "ownperm", "ownall"]:
        errors.append("ownership 组合不精确")
    targets = re.findall(r"(?m)^new_target\s+([a-z0-9]+)\s*$", script)
    if targets != ["adminzero", "admintwo", "metaconf", "receipt"]:
        errors.append("固定目标入口不精确")
    if script.count("\nexpect_up_blocked\n") != 3:
        errors.append("Up 阻断调用数量不精确")
    if len(re.findall(r"(?m)^\s*expect_down_blocked\s*$", script)) != 2 or script.count("\ninsert_receipt b\n") != 1:
        errors.append("receipt 或未知引用 Down 阻断调用不精确")
    contains("for reference_case in role user group", "未知引用三组合缺失")
    if script.count("\nrun_concurrent_receipt_case\n") != 1:
        errors.append("并发唯一范围调用不精确")

    required_runtime_checks = (
        "assert_clean_schema55",
        "assert_schema56",
        "permission_created, ':', admin_binding_created",
        "constraint_type='CHECK'",
        "constraint_type='FOREIGN KEY'",
        "COUNT(DISTINCT index_name)",
        "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;",
        "inject_unknown_reference",
        "role_permissions",
        "user_permission_overrides",
        "group_permissions",
        "DO SLEEP(2)",
        "[[ $exit_a -eq 0 && $exit_b -ne 0 ]]",
    )
    for fragment in required_runtime_checks:
        contains(fragment, f"缺少运行时断言:{fragment}")
    return errors


def run_attack_model(script: str) -> int:
    mutations = {
        "删除参数门禁": script.replace("[[ $# -eq 2 ]] || blocked", ":", 1),
        "删除环境门禁": script.replace('[[ ${MOLIN_000056_ISOLATION_EXECUTE:-} = "$execute_gate" ]] || blocked', ":", 1),
        "删除资产UID绑定": script.replace("readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}", "readonly expected_asset_uid=", 1),
        "删除退出码捕获trap隔离": script.replace("trap - ERR\n  set +e", "set +e", 1),
        "删除基线外键恢复保护": script.replace("SET SESSION FOREIGN_KEY_CHECKS=0;", "SET SESSION FOREIGN_KEY_CHECKS=1;", 1),
        "提前数据库调用": script.replace("stage=initialization", 'stage=initialization\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --execute="SELECT 1"', 1),
        "固定目标库": script.replace('target_db="${target_prefix}${suffix}_${case_name}"', "target_db=molin_restore_legacy_fixed", 1),
        "硬编码旧隔离库": script + "\nlegacy_target=molin_56mx_00000000000040008000000000000000_ownfresh\n",
        "管理连接访问旧库": script + '\nmysql_admin "SELECT COUNT(*) FROM `molin_restore_legacy_fixed`.schema_migrations;"\n',
        "输出完整目标": script + "\nprintf 'target_db=%s\\n' \"$target_db\"\n",
        "篡改Up摘要": script.replace(f"readonly expected_up_sha={EXPECTED_UP_SHA}", "readonly expected_up_sha=UNKNOWN", 1),
        "篡改Down摘要": script.replace(f"readonly expected_down_sha={EXPECTED_DOWN_SHA}", "readonly expected_down_sha=UNKNOWN", 1),
        "读取客户端配置": script.replace('"$MYSQL_BIN" --no-defaults', '"$MYSQL_BIN"', 1),
        "移除UTF8客户端字符集": script.replace(" --default-character-set=utf8mb4", "", 1),
        "删除基线清单": script.replace("readonly manifest_file=", "readonly manifest_file_removed=", 1),
        "删除schema56基线": script.replace('readonly baseline56_file="$asset_dir/schema56.sql"\n', "", 1),
        "减少ownership组合": script.replace("run_ownership_case ownall all 0:0 1 1\n", "", 1),
        "移除receipt阻断": script.replace("insert_receipt b\n", "", 1),
        "减少未知引用": script.replace("for reference_case in role user group", "for reference_case in role user", 1),
        "移除并发验证": script.replace("\nrun_concurrent_receipt_case\n", "\n", 1),
        "伪报partial完成": script.replace("partial_fault_injection=not_implemented", "partial_fault_injection=true", 1),
        "增加删除隔离库": script + "\nDROP DATABASE runtime_target;\n",
        "选择测试主库": script + '\nMYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --database=molin --execute="SELECT 1"\n',
        "注入账号授权": script + '\nmysql_query "GRANT SELECT ON *.* TO \'isolation_attacker\'@\'%\';"\n',
    }
    expected_rejections = {
        "硬编码旧隔离库": "出现硬编码旧隔离库字面量",
        "管理连接访问旧库": "mysql_admin 仅允许检查并创建本轮随机隔离库",
        "注入账号授权": "控制器不得变更账号或授权",
    }
    for name, mutated in mutations.items():
        errors = contract_errors(mutated)
        require(bool(errors), f"攻击模型未被拒绝:{name}")
        if name in expected_rejections:
            require(expected_rejections[name] in errors, f"攻击模型未命中独立拒绝规则:{name}")
    return len(mutations)


def execute_contract() -> int:
    script = RUNNER.read_text(encoding="utf-8")
    require(sha256(UP_MIGRATION) == EXPECTED_UP_SHA, "000056 Up SHA 已偏离冻结值")
    require(sha256(DOWN_MIGRATION) == EXPECTED_DOWN_SHA, "000056 Down SHA 已偏离冻结值")
    require(RUNNER.stat().st_size < 30_000, "000056 runner 超过 30KB 审计上限")

    syntax = subprocess.run(
        [locate_bash(), "--noprofile", "--norc", "-n", str(RUNNER)],
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    require(syntax.returncode == 0 and syntax.stderr == "", "000056 runner Bash 语法检查失败")
    errors = contract_errors(script)
    require(not errors, "000056 runner 离线契约失败:" + ",".join(errors))

    selftest = run_local(["--self-test"])
    require(selftest.returncode == 0 and selftest.stderr == "", "SelfTest 执行失败")
    require(
        selftest.stdout.strip()
        == "status=pass mode=selftest cases=20 database_access=false migration_executed=false runtime_target=not_created",
        "SelfTest 摘要不符合冻结契约",
    )
    default = run_local([])
    require(default.returncode == 2 and default.stderr == "", "默认调用未失败关闭")
    require(
        default.stdout.strip()
        == "status=blocked reason=explicit_double_gate_required database_access=false migration_executed=false",
        "默认关闭摘要不符合冻结契约",
    )
    one_gate_env = dict(os.environ)
    one_gate_env["MOLIN_000056_ISOLATION_EXECUTE"] = "WRONG"
    one_gate = run_local(["--execute", "I_CONFIRM_000056_ISOLATION_MATRIX_ONCE"], one_gate_env)
    require(one_gate.returncode == 2 and one_gate.stderr == "", "单门禁不得进入执行路径")
    require("database_access=false migration_executed=false" in one_gate.stdout, "单门禁摘要缺少无外部访问证明")

    attack_cases = run_attack_model(script)
    require(attack_cases == 24, "攻击模型数量偏离冻结值")
    return attack_cases


def main() -> int:
    try:
        attack_cases = execute_contract()
        if __debug__:
            optimized = subprocess.run(
                [sys.executable, "-O", str(Path(__file__).resolve())],
                check=False,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            require(optimized.returncode == 0 and optimized.stderr == "", "Python -O 子进程契约失败")
            require(optimized.stdout.strip() == OPTIMIZED_SENTINEL, "Python -O 子进程摘要异常")
            print(
                "status=pass checks=static,selftest,default_closed,one_gate_closed "
                f"attack_cases={attack_cases} optimized=true database_access=false migration_executed=false "
                f"partial_fault_injection=not_implemented runner_sha256={sha256(RUNNER)}"
            )
        else:
            print(OPTIMIZED_SENTINEL)
        return 0
    except (ContractError, OSError, UnicodeError) as exc:
        print(f"status=failed classification=offline_contract detail={exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

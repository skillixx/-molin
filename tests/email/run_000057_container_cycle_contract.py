from __future__ import annotations

import hashlib
import re
import shutil
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CYCLE_SCRIPT = ROOT / "scripts/run-000057-container-backup-restore-cycle.sh"
UP_MIGRATION = ROOT / "server/migrations/000057_fix_email_datetime_utc_seconds.up.sql"
DOWN_MIGRATION = ROOT / "server/migrations/000057_fix_email_datetime_utc_seconds.down.sql"
EXPECTED_DOWN_SHA = "EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB"
LEGACY_TARGET = "molin_restore_57_reverify_8fb6f25611b25d07a563f15105d0906a"
EXPECTED_WORK_DIR = "/root/molin-000057-container-cycle-3263e5469732436c910dd22f894d647b"


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def require(content: str, fragment: str) -> None:
    assert fragment in content, f"隔离执行脚本缺少冻结片段: {fragment}"


def main() -> None:
    script = CYCLE_SCRIPT.read_text(encoding="utf-8")
    up_sha = sha256(UP_MIGRATION)
    down_sha = sha256(DOWN_MIGRATION)

    # bash -n 只解析语法，不执行数据库、迁移、容器或网络操作。
    bash = shutil.which("bash")
    if bash is None:
        candidates = (
            Path(r"C:\Program Files\Git\bin\bash.exe"),
            Path(r"C:\Program Files\Git\usr\bin\bash.exe"),
        )
        bash = next((str(path) for path in candidates if path.is_file()), None)
    assert bash is not None, "缺少可用于 bash -n 的本地 Bash"
    subprocess.run(
        [bash, "--noprofile", "--norc", "-n", str(CYCLE_SCRIPT)],
        check=True,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )

    # SQL 文件和脚本冻结摘要必须逐字节一致，避免误执行旧 Down。
    assert down_sha == EXPECTED_DOWN_SHA, "000057 Down 文件 SHA-256 不符合冻结值"
    require(script, f"readonly expected_up_sha={up_sha}")
    require(script, f"readonly expected_down_sha={EXPECTED_DOWN_SHA}")

    # 目标名只生成一次、严格校验，并在任何数据库帮助函数定义前冻结。
    generation_fragments = (
        "readonly target_prefix=molin_restore_57_reverify_",
        "target_uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)",
        '[[ "$target_uuid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$ ]]',
        'target_db="${target_prefix}${target_uuid//-/}"',
        '[[ "$target_db" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]',
        '[[ "$target_db" != "$protected_legacy_target_db" ]]',
        "readonly target_db",
        "unset target_uuid",
    )
    for fragment in generation_fragments:
        require(script, fragment)
    assert len(re.findall(r"(?m)^target_db=", script)) == 2, "target_db 只能先置空并在 UUID 生成后赋值一次"
    assert script.index("readonly target_db") < script.index("mysql_query_db()"), "target_db 必须在数据库帮助函数前冻结"

    # work_dir 只能使用远程只读预检确认不存在的新候选，路径格式固定且只读赋值一次。
    work_dir_assignments = re.findall(r"(?m)^(?:readonly )?work_dir=.*$", script)
    assert work_dir_assignments == [f"readonly work_dir={EXPECTED_WORK_DIR}"], "work_dir 必须精确冻结为唯一候选且不可再次覆盖"
    assert re.fullmatch(r"/root/molin-000057-container-cycle-[a-f0-9]{32}", EXPECTED_WORK_DIR), "候选 work_dir 格式不合法"
    assert script.count(EXPECTED_WORK_DIR) == 1, "候选 work_dir 只能在 readonly 定义处出现一次"
    assert re.search(r"(?m)^readonly work_dir=/root/molin-000057-container-cycle$", script) is None, "不得复用已存在的旧 work_dir"
    assert script.index(f"readonly work_dir={EXPECTED_WORK_DIR}") < script.index('readonly evidence_dir="$work_dir/evidence"'), "派生路径必须在 work_dir 冻结后定义"

    # 旧 dirty1 目标名只用于禁止复用门禁，不能进入 SQL、清理命令或输出。
    require(script, f"readonly protected_legacy_target_db={LEGACY_TARGET}")
    assert script.count(LEGACY_TARGET) == 1, "旧 dirty1 目标名不得出现在禁止复用常量以外的位置"
    assert "printf 'target_db=" not in script, "输出不得暴露随机目标 schema 名"

    # 保留源库只读、目标不存在、单事务备份、仅目标写入和 Up→Down→Up 门禁。
    safety_fragments = (
        "readonly source_db=molin",
        "--single-transaction --quick --skip-lock-tables",
        "schema_name = '$target_db';\")\" = 0",
        'mysql_admin_query "CREATE DATABASE \\`$target_db\\` CHARACTER SET $source_charset COLLATE $source_collation;"',
        "mysql_from_file() {",
        '--database="$target_db"',
        "stage=first_up_sql",
        "stage=down_sql",
        "stage=second_up_sql",
        "assert_source_migration_state_unchanged",
        "source_write_commands_performed=false",
        "down_full_snapshot_restored=true",
    )
    for fragment in safety_fragments:
        require(script, fragment)
    assert script.index("stage=first_up_sql") < script.index("stage=down_sql") < script.index("stage=second_up_sql"), "周期顺序必须为 Up→Down→Up"

    # 扫描会删除 schema、清理目录、强制修复版本或写源库的危险命令。
    dangerous_patterns = (
        r"(?im)\bDROP\s+(?:DATABASE|SCHEMA)\b",
        r"(?im)\bmysqladmin\b",
        r"(?im)(?:^|[;&|]\s*)rm\s",
        r"(?im)\bTRUNCATE\s+TABLE\b",
        r"(?im)\bDELETE\s+FROM\b",
        r"(?im)\bforce\b",
    )
    for pattern in dangerous_patterns:
        assert re.search(pattern, script) is None, f"脚本命中危险命令模式: {pattern}"
    source_calls = re.findall(r'mysql_query_db\s+"\$source_db"\s+"([^"]+)"', script)
    assert source_calls, "未找到源库只读检查"
    assert all(sql.lstrip().upper().startswith("SELECT") for sql in source_calls), "源库调用只能执行 SELECT"

    print("000057_container_cycle_contract=pass")
    print("database_access=false")
    print("migration_executed=false")
    print("target_generation=unique_uuid_frozen")
    print("legacy_dirty_target_protected=true")
    print(f"up_sha256={up_sha}")
    print(f"down_sha256={down_sha}")
    print(f"script_sha256={sha256(CYCLE_SCRIPT)}")


if __name__ == "__main__":
    main()

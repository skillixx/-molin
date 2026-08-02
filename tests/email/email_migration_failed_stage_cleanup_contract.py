#!/usr/bin/env python3
"""验证 migration 失败现场清理器只处理可精确证明归属的临时 Stage。"""

from __future__ import annotations

import pathlib
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PREFLIGHT = ROOT / "scripts" / "email-migration-failed-stage-cleanup.payload.sh"
BASELINE = ROOT / "scripts" / "email-migration-baseline-preflight-failure-cleanup.payload.sh"
MYSQL_VERSION = ROOT / "scripts" / "email-migration-mysql-version-failure-cleanup.payload.sh"
DUMP_COMMENT = ROOT / "scripts" / "email-migration-dump-comment-failure-cleanup.payload.sh"
SCHEMA56_SQL = ROOT / "scripts" / "email-migration-schema56-sql-failure-cleanup.payload.sh"

SCHEMA56_FINGERPRINTS = (
    "48:b3f6d38e6965b16c300e0057dc2074afef859b72e28d20f28bc1fde167dccfef,"
    "69:e05e098693ce41e8d9e204e823ea8f50f9fdcb45abdc3a7eae2236359bf04f02,"
    "48:80907c599c935b2bd8b2e9ef6b5a56530203ac83edb6ef05c96250dfbe33dd53,"
    "227:656ef4e1b29c3c481b43aed49b3279f0dfb8f9bb6ed4868e9ef57e55ff660385,"
    "176:ee9f0a7c0344ae5c6220d17b7043e58e8d0735b42fd5cd8caa739d1212e71e06,"
    "232:7345bcbc8d4592a7f62d24a0b3c6e6bbd8ae6fcc86da2533575df162932818d0,"
    "91:02aade59c6a977c09e3595f8e6a5f9b11a2a59ad3a129bb0bc4390edbdbc3ed7,"
    "75:fb3d30c4907cd8ac267ce323b7b2cc6c584d938ffd7fcc638bda58642541dd3d"
)


class ContractError(RuntimeError):
    """表示清理器偏离精确归属或只清理临时 Stage 的边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate_common(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n#") and "\r" not in text, "encoding")
    require("set -Eeuo pipefail" in text and "exec 2>/dev/null" in text, "strict_mode")
    require('[[ $# -eq 3 && $1 = --execute ]]' in text, "argument_gate")
    require('^/home/pc/molin-runtime/email-migration-matrix-' not in text, "computed_path_only")
    require('readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"' in text, "exact_stage")
    require('[[ "$nonce" =~ ^[a-f0-9]{32}$ && "$archive_sha" =~ ^[a-f0-9]{64}$ ]]' in text, "identifier_gate")
    require('"$actual_archive_sha" = "$archive_sha"' in text, "archive_hash")
    require('$($wc_bin -l < "$manifest")" -eq 66' in text, "manifest_count")
    require('"$sha256sum_bin" --check --strict --status source-manifest.sha256' in text, "manifest_hashes")
    require('[[ -z "$($find_bin "$stage" -type l -print -quit)" ]]' in text, "symlink_gate")
    require('"$rm_bin" -rf --one-file-system -- "$stage"' in text, "exact_cleanup")
    require('[[ ! -e "$stage" && ! -L "$stage" ]]' in text, "cleanup_verify")
    forbidden = ("/usr/bin/docker", "docker_bin=", "/usr/bin/mysql", "mysql_bin=", "redis-cli", "scp.exe", "sftp.exe", "FLUSHDB", "FLUSHALL", "KEYS", "SCAN", "UNLINK", '"$stage"*')
    for item in forbidden:
        require(item.lower() not in text.lower(), f"forbidden:{item}")


def validate_preflight(text: str) -> None:
    validate_common(text)
    require("execution_artifact_present" in text, "pre_container_shape")
    require("entries[@]} -eq 3" in text, "pre_container_entries")


def validate_baseline(text: str) -> None:
    validate_common(text)
    require('[[ -z "$($find_bin "$baselines" -mindepth 1 -print -quit)" ]]' in text, "empty_baselines")
    require("output_entries[@]} -eq 2" in text, "capture_shape")
    require("baseline.stderr" in text and "baseline.stdout" in text, "capture_names")
    require("'%U:%s'" in text and "pc:0" in text, "empty_capture_gate")
    require("entries[@]} -eq 5" in text and "unexpected_assets" in text, "baseline_stage_shape")


def validate_fixed_failure(text: str, expected_stage: str, expected_classification: str) -> None:
    validate_common(text)
    require('[[ -z "$($find_bin "$baselines" -mindepth 1 -print -quit)" ]]' in text, "empty_baselines")
    require("output_entries[@]} -eq 2" in text, "capture_shape")
    expected = (
        "status=failed mode=email_migration_baseline_generation "
        f"stage={expected_stage} classification={expected_classification} "
        "outputs_created=false retained=false"
    )
    require(expected in text, "fixed_summary")
    require("baseline.stderr" in text and "pc:0" in text, "empty_stderr")
    require('$($cat_bin "$stdout_file")' in text and '$($wc_bin -l < "$stdout_file")' in text, "stdout_exactness")
    require("entries[@]} -eq 5" in text and "unexpected_assets" in text, "fixed_stage_shape")


def validate_schema56_sql(text: str) -> None:
    """只接受已确认的 3819/HY000/113 与八组固定脱敏指纹。"""
    validate_common(text)
    require('[[ -z "$($find_bin "$baselines" -mindepth 1 -print -quit)" ]]' in text, "empty_baselines")
    require("output_entries[@]} -eq 2" in text, "capture_shape")
    expected = (
        "status=failed mode=email_migration_baseline_generation "
        "stage=schema56_build classification=migration_sql "
        "mysql_error_code=3819 sqlstate=HY000 sql_line=113 "
        f"check_fingerprints={SCHEMA56_FINGERPRINTS} "
        "outputs_created=false retained=false"
    )
    require(expected in text, "schema56_exact_summary")
    require("baseline.stderr" in text and "pc:0" in text, "empty_stderr")
    require('$($cat_bin "$stdout_file")' in text and '$($wc_bin -l < "$stdout_file")' in text, "stdout_exactness")
    require("entries[@]} -eq 5" in text and "unexpected_assets" in text, "schema56_stage_shape")


def mutations(text: str) -> tuple[str, ...]:
    return (
        text.replace('[[ $# -eq 3 && $1 = --execute ]]', ":", 1),
        text.replace('"$nonce" =~ ^[a-f0-9]{32}$', '"$nonce" =~ ^.*$', 1),
        text.replace('"$actual_archive_sha" = "$archive_sha"', "-n \"$actual_archive_sha\"", 1),
        text.replace('$($wc_bin -l < "$manifest")" -eq 66', '$($wc_bin -l < "$manifest")" -gt 0', 1),
        text.replace("--check --strict --status", "--check", 1),
        text.replace('[[ -z "$($find_bin "$stage" -type l -print -quit)" ]]', ":", 1),
        text.replace('"$rm_bin" -rf --one-file-system -- "$stage"', '"$rm_bin" -rf /home/pc/molin-runtime/*', 1),
    )


def schema56_mutations(text: str) -> tuple[str, ...]:
    """证明错误码、行号、指纹数量和单个指纹均不可放宽。"""
    return (
        text.replace("mysql_error_code=3819", "mysql_error_code=0000", 1),
        text.replace("sql_line=113", "sql_line=0", 1),
        text.replace(SCHEMA56_FINGERPRINTS, SCHEMA56_FINGERPRINTS.rsplit(",", 1)[0], 1),
        text.replace(SCHEMA56_FINGERPRINTS[:67], "48:" + "0" * 64, 1),
    )


def main() -> int:
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(bash).is_file(), "bash_missing")
    rejected = 0
    assets = (
        (PREFLIGHT, validate_preflight),
        (BASELINE, validate_baseline),
        (MYSQL_VERSION, lambda text: validate_fixed_failure(text, "mysql_ready", "mysql_version")),
        (DUMP_COMMENT, lambda text: validate_fixed_failure(text, "schema54_build", "dump_executable_comment")),
        (SCHEMA56_SQL, validate_schema56_sql),
    )
    for path, validator in assets:
        raw = path.read_bytes()
        require(raw and not raw.startswith(b"\xef\xbb\xbf") and b"\r" not in raw, "bytes")
        text = raw.decode("utf-8")
        validator(text)
        syntax = subprocess.run([bash, "-n", str(path)], capture_output=True, text=True, timeout=10)
        require(syntax.returncode == 0 and not syntax.stderr, "bash_syntax")
        for candidate in mutations(text):
            try:
                validator(candidate)
            except ContractError:
                rejected += 1
            else:
                raise ContractError("mutation_accepted")
        if path == SCHEMA56_SQL:
            for candidate in schema56_mutations(text):
                try:
                    validator(candidate)
                except ContractError:
                    rejected += 1
                else:
                    raise ContractError("schema56_mutation_accepted")
    print(
        "status=pass mode=email_migration_failed_stage_cleanup_contract "
        f"attack_cases={rejected} cleanup_assets=5 external_access=false database_access=false docker_access=false retries=0"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

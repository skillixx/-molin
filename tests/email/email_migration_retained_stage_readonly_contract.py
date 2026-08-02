#!/usr/bin/env python3
"""验证唯一保留迁移 Stage 的诊断不会泄露标识符或执行写操作。"""

from __future__ import annotations

import pathlib
import shutil
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-retained-stage-readonly.payload.sh"


class ContractError(RuntimeError):
    """表示只读诊断偏离安全边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate(text: str) -> None:
    required = (
        "-regextype posix-extended -mindepth 1 -maxdepth 1 -type d",
        "email-migration-matrix-[a-f0-9]{32}",
        "[[ ${#stages[@]} -eq 1 ]]",
        "source-manifest.sha256",
        "--check --strict --status",
        "target_collision_pre_matrix_retained",
        "matrix55_runner_failure_retained",
        "matrix55_success_summary_contract_mismatch_retained",
        "partial55_runner_failure_retained",
        "matrix55_failure=%s",
        "matrix55_case=%s",
        "matrix55_target_created=%s",
        "matrix55_error=%s",
        "partial55_failure=%s",
        "partial55_case=%s",
        "partial55_target_created=%s",
        "partial55_error=%s",
        "partial55_stderr_class=%s",
        'else if (awk_syntax==NR) print "awk_syntax"',
        "partial55_assets_verified=%s",
        "partial55_environment_precheck_classified_retained",
        "partial55_precheck_stage_classified_retained",
        "environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape",
        "expected_case[1]=\"empty\"; expected_version[1]=\"54\"",
        "expected_case[7]=\"ownmixed\"; expected_version[7]=\"54\"",
        "target_id_sha256=[A-F0-9]+",
        "length(hash_field[2])!=64",
        "seen[hash_field[2]]++",
        "(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)_(target_identity|target_absent|target_create|baseline_restore|baseline_version|baseline_version_cardinality|database_binding|engine_policy|view_policy|trigger_policy|routine_policy|event_policy|schema54_(baseline|down)_(version|table_absence|code_shape|code_hash_absence)|up_(mark_dirty|sql|finalize|validate)|down_(mark_dirty|sql|finalize|validate|statement_[0-9]{2}))",
        "case_field[2]!=expected_case[progress]",
        "terminal==4 && completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==progress+4",
        "terminal==22 && completed==1 && progress==7 && NR==20",
        'print "summary_contract_mismatch|none|false"',
        "000055-partial-boundaries.tsv",
        "boundary_count==31 && expected_count==33",
        'expected_case[boundary_count+1]="up_baseline"',
        'expected_case[boundary_count+2]="down_baseline"',
        "FNR==progress+4",
        "case_known && stage_case_ok",
        'index(stage_value, case_value "_")==1',
        'split($0, output_fields, " ")',
        '/\\/usr\\/bin\\/wc: (No such file or directory|not found)/',
        'if (NR<1 || kinds!=1) print "mixed_or_invalid"',
        'else if (wc_missing==NR) print "wc_missing"',
        "partial55_entries[@]",
        'mapfile -t partial55_entries < <("$find_bin" "$partial55_asset_dir" -mindepth 1 -maxdepth 1 -printf \'%f\\n\' | "$sort_bin")',
        "[[ ${#partial55_entries[@]} -eq 7",
        "000055-partial-boundaries.tsv 000055_add_directmail_email_management.down.sql 000055_add_directmail_email_management.up.sql baseline-manifest.tsv runner.sh schema54-legacy.sql schema55.sql",
        "expected_mode=400; [[ \"$asset_name\" = runner.sh ]] && expected_mode=500",
        "tests/email/run-000055-container-partial-matrix.sh",
        '[[ "$asset_hash" = "$source_hash" ]] || partial55_assets_verified=false',
        "000055-baseline-manifest.tsv",
        '[[ "$actual_manifest_hash" = "$expected_manifest_hash" ]] || partial55_assets_verified=false',
        '"$partial55_assets_verified" = true',
        "category_count==1 && exit_count==1 && length_count==1 && NR==3",
        "source_verified=%s top_count=%s baselines_count=%s assets_count=%s output_count=%s",
        "writes=false database_access=false docker_access=false retries=0",
        "trap 'fail unexpected_failure' ERR",
        'awk_bin=$("$realpath_bin" -e -- "$awk_link")',
        '"$($stat_bin -c \'%U:%G\' -- "$awk_bin")" = root:root',
    )
    for item in required:
        require(item in text, f"missing:{item}")
    require(text.count("(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)_(target_identity|target_absent|target_create|baseline_restore|baseline_version|baseline_version_cardinality|database_binding|engine_policy|view_policy|trigger_policy|routine_policy|event_policy|schema54_(baseline|down)_(version|table_absence|code_shape|code_hash_absence)|up_(mark_dirty|sql|finalize|validate)|down_(mark_dirty|sql|finalize|validate|statement_[0-9]{2}))") == 2, "failure_allowlist_count")
    require(text.count("case_field[2]!=expected_case[progress]") == 2, "case_order_gate_count")
    require(text.count("length(hash_field[2])!=64") == 2 and "seen_hash[hash_field[2]]++" in text, "hash_gate_count")
    require(text.count("category_count==1 && exit_count==1 && length_count==1 && NR==3") == 2, "stderr_gate_count")
    require(text.count('[[ "$asset_hash" = "$source_hash" ]] || partial55_assets_verified=false') == 2, "partial55_source_hash_gate_count")
    require(text.count('"$partial55_assets_verified" = true') == 2, "partial55_asset_classification_gate_count")
    require(text.count("environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape") == 2, "partial55_precheck_stage_allowlist_count")
    forbidden = (
        "/usr/bin/docker", "docker ", "/usr/bin/mysql", "redis-cli", "rm -", "rmdir", "chmod", "chown",
        "scp", "sftp", "cat ", "head ", "tail ", "requestid", "nonce=%", "archive_sha=%",
        "/home/pc/molin-runtime/*",
    )
    for item in forbidden:
        require(item.lower() not in text.lower(), f"forbidden:{item}")


def validate_partial55_runtime(text: str, bash: str) -> int:
    """真实执行 partial55 白名单解析，覆盖顺序、哈希、schema 与 case 绑定。"""
    marker = 'partial55_summary=$("$awk_bin" -F \'\\t\' \'\n'
    start = text.index(marker) + len(marker)
    end = text.index('\n  \' "$partial55_manifest" "$stage/output/partial55.stdout")', start)
    program = text[start:end]
    manifest = ROOT / "tests" / "email" / "000055-partial-boundaries.tsv"
    rows = [line.split("\t") for line in manifest.read_text(encoding="utf-8").splitlines()]
    require(len(rows) == 31, "runtime_manifest_shape")
    first, second = rows[0], rows[1]
    digest_a, digest_b = "A" * 64, "B" * 64
    progress_a = f"case={first[1]} target_id_sha256={digest_a} restored_schema=54"
    progress_b = f"case={second[1]} target_id_sha256={digest_b} restored_schema=54"
    terminal = (
        "partial_matrix_completed=false\n"
        f"failure_stage={second[1]}_validate_state\n"
        f"case={second[1]}\n"
        "target_created=true\n"
    )
    valid = f"{progress_a}\n{progress_b}\n{terminal}"
    attacks = (
        valid.replace(f"case={first[1]}", f"case={second[1]}", 1),
        valid.replace(digest_b, digest_a, 1),
        valid.replace("restored_schema=54", "restored_schema=55", 1),
        valid.replace(f"case={second[1]}\n", "case=up_unknown\n", 1),
        valid.replace(f"failure_stage={second[1]}_validate_state", f"failure_stage={first[1]}_validate_state", 1),
        valid.replace("target_created=true", "target_created="),
        valid.replace("partial_matrix_completed=false\n", "", 1),
    )
    expected = f"{second[1]}_validate_state|{second[1]}|true\n"
    with tempfile.TemporaryDirectory(prefix="molin-partial55-readonly-contract-") as temporary:
        root = pathlib.Path(temporary)
        parser = root / "parser.awk"
        output = root / "partial55.stdout"
        parser.write_text(program, encoding="ascii", newline="\n")

        def run(candidate: str) -> subprocess.CompletedProcess[str]:
            output.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-c", '/usr/bin/awk -F "\\t" -f "$1" "$2" "$3"', "--", str(parser), str(manifest), str(output)],
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(accepted.returncode == 0 and accepted.stdout == expected and accepted.stderr == "", "partial55_runtime_valid")
        for candidate in attacks:
            rejected = run(candidate)
            require(rejected.returncode == 0 and rejected.stdout == "invalid\n" and rejected.stderr == "", "partial55_runtime_attack")
    return 1 + len(attacks)


def main() -> int:
    raw = PAYLOAD.read_bytes()
    require(raw and not raw.startswith(b"\xef\xbb\xbf") and b"\r" not in raw and b"\x00" not in raw, "encoding")
    text = raw.decode("utf-8")
    validate(text)
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(bash).is_file(), "bash_missing")
    runtime_cases = validate_partial55_runtime(text, bash)
    rejected = 0
    for index, candidate in enumerate((
        text.replace("[[ ${#stages[@]} -eq 1 ]]", "[[ ${#stages[@]} -ge 1 ]]", 1),
        text.replace("--check --strict --status", "--check", 1),
        text.replace("target_collision_pre_matrix_retained", "pass", 1),
        text.replace("case_field[2]!=expected_case[progress]", "0", 1),
        text.replace("length(hash_field[2])!=64", "length(hash_field[2])<64", 1),
        text.replace("seen[hash_field[2]]++", "0", 1),
        text.replace("(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)_(target_identity|target_absent|target_create|baseline_restore|baseline_version|baseline_version_cardinality|database_binding|engine_policy|view_policy|trigger_policy|routine_policy|event_policy|schema54_(baseline|down)_(version|table_absence|code_shape|code_hash_absence)|up_(mark_dirty|sql|finalize|validate)|down_(mark_dirty|sql|finalize|validate|statement_[0-9]{2}))", "[a-z0-9_]+"),
        text.replace("terminal==4 && completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==progress+4", "stage_count>=1", 1),
        text.replace("terminal==22 && completed==1 && progress==7 && NR==20", "terminal==22", 1),
        text.replace('print "summary_contract_mismatch|none|false"', 'print "summary_contract_mismatch|none|true"', 1),
        text.replace("boundary_count==31 && expected_count==33", "boundary_count>=1", 1),
        text.replace('expected_case[boundary_count+1]="up_baseline"', 'expected_case[boundary_count+1]="unknown"', 1),
        text.replace("FNR==progress+4", "FNR>=4", 1),
        text.replace("case_known && stage_case_ok", "1", 1),
        text.replace('index(stage_value, case_value "_")==1', "1", 1),
        text.replace('split($0, output_fields, " ")', 'split($1, output_fields, " ")', 1),
        text.replace('if (NR<1 || kinds!=1) print "mixed_or_invalid"', 'if (NR<1) print "mixed_or_invalid"', 1),
        text.replace('else if (awk_syntax==NR) print "awk_syntax"', 'else if (awk_syntax>0) print "awk_syntax"', 1),
        text.replace('else if (wc_missing==NR) print "wc_missing"', 'else if (wc_missing>0) print "wc_missing"', 1),
        text.replace("-mindepth 1 -maxdepth 1 -printf '%f\\n'", "-mindepth 1 -maxdepth 1 -type f -printf '%f\\n'", 1),
        text.replace("${#partial55_entries[@]} -eq 7", "${#partial55_entries[@]} -ge 7", 1),
        text.replace('expected_mode=400; [[ "$asset_name" = runner.sh ]] && expected_mode=500', 'expected_mode=400', 1),
        text.replace('[[ "$asset_hash" = "$source_hash" ]] || partial55_assets_verified=false', 'true', 1),
        text.replace('[[ "$actual_manifest_hash" = "$expected_manifest_hash" ]] || partial55_assets_verified=false', 'true', 1),
        text.replace('"$partial55_assets_verified" = true', '"$partial55_assets_verified" != false', 1),
        text.replace("environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape", "environment_identity|unknown", 1),
        text.replace("category_count==1 && exit_count==1 && length_count==1 && NR==3", "category_count>=1", 1),
        text.replace('awk_bin=$("$realpath_bin" -e -- "$awk_link")', 'awk_bin="$awk_link"', 1),
        text.replace("trap 'fail unexpected_failure' ERR", "true", 1),
        text + "\nrm -rf /home/pc/molin-runtime/*\n",
        text + "\ndocker ps\n",
        text + "\nprintf 'nonce=%s' \"$archive_sha\"\n",
    ), start=1):
        try:
            validate(candidate)
        except ContractError:
            rejected += 1
        else:
            raise ContractError(f"mutation_accepted_{index}")
    syntax = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
    require(syntax.returncode == 0 and not syntax.stderr, "bash_syntax")
    print(
        "status=pass mode=email_migration_retained_stage_readonly_contract "
        f"attack_cases={rejected} partial55_runtime_cases={runtime_cases} external_access=false writes=false database_access=false docker_access=false retries=0"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractError, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_migration_retained_stage_readonly_contract classification=closed")
        raise SystemExit(1)

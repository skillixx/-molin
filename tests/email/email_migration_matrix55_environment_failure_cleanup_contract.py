"""验证 matrix55 环境预检失败 Stage 的精确清理边界。"""

from __future__ import annotations

import hashlib
import pathlib
import shutil
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-matrix55-environment-failure-cleanup.payload.sh"
RUNNER = ROOT / "scripts" / "run-email-migration-matrix55-environment-failure-cleanup.ps1"


class ContractFailure(RuntimeError):
    """表示清理器偏离精确失败现场边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n#") and "\r" not in text and "\x00" not in text, "payload_encoding")
    require("set -Eeuo pipefail" in text and "exec 2>/dev/null" in text, "strict_mode")
    require("I_CONFIRM_EMAIL_MIGRATION_MATRIX55_ENVIRONMENT_FAILURE_CLEANUP_ONCE" in text, "confirmation")
    require("${#stages[@]} -eq 1" in text and "email-migration-matrix-[a-f0-9]{32}" in text, "unique_stage")
    require("${#top_entries[@]} -eq 6" in text and "assets baselines output package.tar.gz source source-manifest.sha256" in text, "stage_shape")
    require("sha256sum_bin\" --check --strict --status source-manifest.sha256" in text, "source_manifest")
    require("${#baseline_entries[@]} -eq 6" in text and "schema54-empty.sql" in text and "schema56.sql" in text, "baseline_shape")
    require("${#output_entries[@]} -eq 5" in text and "${#output_entries[@]} -eq 7" in text, "output_shape")
    require("partial55.stderr partial55.stdout" in text and "output_profile=partial55_failure" in text, "partial55_output_profile")
    require('failure_stage=(environment_precheck|empty_baseline|empty_baseline_restore|empty_event_policy|empty_schema54_validate|empty_schema54_code_shape|schema55_down_sql|schema55_down_statement_05)' in text, "failure_stage")
    require('((stage=="schema55_down_sql" || stage=="schema55_down_statement_05") && case_value=="schema55" && target_value=="true" && progress==3)' in text, "schema55_down_pair")
    require('expected_case[1]="empty"; expected_version[1]="54"' in text and 'expected_case[7]="ownmixed"; expected_version[7]="54"' in text, "progress_order")
    require('target_id_sha256=[A-F0-9]+' in text and 'length(hash_field[2])!=64' in text and 'seen[hash_field[2]]++' in text, "progress_identity")
    require("NR==progress+4" in text, "failure_progress_length")
    require('terminal==22 && completed==1 && progress==7 && NR==20' in text and 'print "summary_contract_mismatch"' in text, "success_summary_pair")
    require('stage=="environment_precheck" && case_value=="none" && target_value=="false" && progress==0' in text, "environment_failure_pair")
    require('(stage=="empty_baseline" || stage=="empty_baseline_restore") && case_value=="empty" && target_value=="true" && progress==0' in text, "empty_baseline_pair")
    require('(stage=="empty_event_policy" || stage=="empty_schema54_validate" || stage=="empty_schema54_code_shape") && case_value=="empty" && target_value=="true" && progress==1' in text, "event_failure_pair")
    require('[[ "$matrix55_stage" = empty_event_policy || "$matrix55_stage" = empty_schema54_validate || "$matrix55_stage" = schema55_down_sql ]]' in text, "event_error_stage_pair")
    require('[[ "$matrix55_error" = other ]] || fail matrix55_error_pair' in text, "event_error_pair")
    require('[[ "$matrix55_stage" = schema55_down_statement_05 ]]' in text and '[[ "$matrix55_error" = constraint ]] || fail matrix55_error_pair' in text, "statement_error_pair")
    require('[[ "$matrix55_error" = none ]] || fail matrix55_error_pair' in text, "non_event_error_pair")
    require('[[ "$matrix55_stage" = summary_contract_mismatch ]]' in text, "summary_error_pair")
    require('[[ "$matrix55_stage" = summary_contract_mismatch && "$matrix55_error" = none ]] || fail partial55_matrix55_pair' in text, "partial55_matrix55_pair")
    require('failure_stage=(environment_precheck|boundary_manifest_shape)' in text and '$0=="case=none"' in text and '$0=="target_created=false"' in text, "partial55_summary")
    require("completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==4" in text, "partial55_summary_shape")
    require('[[ "$partial55_stderr_class" = other ]] || fail partial55_stderr_pair' in text, "partial55_stderr_pair")
    require('"$partial55_stderr_size" -le 4096' in text, "partial55_stderr_size")
    require('[[ "$partial55_stage" = boundary_manifest_shape ]]' in text and 'awk_syntax==NR' in text, "boundary_manifest_stderr_pair")
    require('[[ "$partial55_stderr_class" = awk_syntax ]] || fail partial55_stderr_pair' in text, "boundary_manifest_stderr_class")
    require("category_count==1 && exit_count==1 && length_count==1 && NR==3" in text, "stderr_shape")
    require(text.count("molin-000055-isolation-assets:7") == 1 and text.count("molin-000056-partial-assets:7") == 1, "asset_shape")
    require("name=^/molin-email-matrix-${nonce}$" in text and "label=molin.phase4.matrix=${nonce}" in text, "container_identity")
    require("${#named_containers[@]} -eq 0 && ${#labeled_containers[@]} -eq 0" in text, "container_absence")
    require(text.count('"$rm_bin" -rf --one-file-system -- "$stage"') == 1, "exact_cleanup")
    for forbidden in ("rm -rf /home/pc/molin-runtime/*", "docker rm", "mysql ", "redis-cli", "KEYS", "SCAN", "FLUSHDB", "FLUSHALL"):
        require(forbidden.lower() not in text.lower(), f"forbidden:{forbidden}")


def validate_runner(text: str) -> None:
    require(sha(PAYLOAD) in text, "payload_hash")
    require("Management.Automation.Language.Parser" in text and "controller_single_execution" in text, "ast_gate")
    require(text.count("Invoke-FixedSsh -Ssh $ssh") == 1, "single_ssh")
    require("StrictHostKeyChecking=yes" in text and "ConnectTimeout=10" in text, "ssh_policy")
    require("[Text.UTF8Encoding]::new($false, $true).GetBytes($InputText)" in text, "stdin_encoding")
    require("ErrorLength -ne 0" in text and "remote_output" in text, "output_gate")
    require("scp.exe" not in text and "sftp" not in text.lower(), "no_transfer")


def validate_success_parser_runtime(payload: str, bash: str) -> int:
    """真实执行清理器的成功摘要解析器，防止删除门禁只通过静态检查。"""
    marker = 'matrix55_stage=$("$awk_bin" \'\n'
    start = payload.index(marker) + len(marker)
    end = payload.index('\n\' "$output/matrix55.stdout")', start)
    program = payload[start:end]
    cases = (
        ("empty", "54"), ("legacy", "54"), ("schema55", "55"),
        ("ownfresh", "54"), ("ownperm", "54"), ("ownall", "54"), ("ownmixed", "54"),
    )
    digest_symbols = ("A", "B", "C", "D", "E", "F", "0")
    lines = [
        f"case={name} target_id_sha256={digest_symbols[index] * 64} restored_schema={schema}"
        for index, (name, schema) in enumerate(cases)
    ]
    lines.extend(
        (
            "matrix_completed=true", "database_access=true", "migration_executed=true",
            "source_database_selected=false", "runtime_unique_targets=7", "empty_schema54_up_down=true",
            "legacy_schema54_up_down=true", "schema55_down=true", "ownership_combinations=4",
            "partial_fault_injection=not_run", "targets_retained=true",
            "up_sha256=7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D",
            "down_sha256=217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE",
        )
    )
    valid = "\n".join(lines) + "\n"
    attacks = (
        valid.replace("case=empty", "case=legacy", 1),
        valid.replace("restored_schema=54", "restored_schema=55", 1),
        valid.replace("B" * 64, "A" * 64, 1),
        valid.replace(lines[6] + "\n", "", 1),
        valid.replace("matrix_completed=true", "matrix_completed="),
        valid.replace("schema55_down=true", "schema55_down="),
    )
    with tempfile.TemporaryDirectory(prefix="molin-cleanup-summary-contract-") as temporary:
        root = pathlib.Path(temporary)
        program_file = root / "parser.awk"
        output_file = root / "matrix55.stdout"
        program_file.write_text(program, encoding="ascii", newline="\n")

        def run(candidate: str) -> subprocess.CompletedProcess[str]:
            output_file.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-c", '/usr/bin/awk -f "$1" "$2"', "--", str(program_file), str(output_file)],
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(accepted.returncode == 0 and accepted.stdout == "summary_contract_mismatch\n" and accepted.stderr == "", "success_parser_valid")
        for candidate in attacks:
            require(run(candidate).returncode != 0, "success_parser_attack")
    return 1 + len(attacks)


def validate_partial55_parser_runtime(payload: str, bash: str) -> int:
    """真实执行 partial55 固定四行解析器，防止清理器接受相邻失败现场。"""
    marker = '  partial55_stage=$(\n    "$awk_bin" \'\n'
    start = payload.index(marker) + len(marker)
    end = payload.index('\n    \' "$output/partial55.stdout"', start)
    program = payload[start:end]
    valid = "partial_matrix_completed=false\nfailure_stage=environment_precheck\ncase=none\ntarget_created=false\n"
    boundary_valid = valid.replace("environment_precheck", "boundary_manifest_shape")
    attacks = (
        valid.replace("environment_precheck", "statement_boundary_precheck"),
        valid.replace("case=none", "case=up_baseline"),
        valid.replace("target_created=false", "target_created=true"),
        valid.replace("partial_matrix_completed=false\n", ""),
        valid + "unexpected=true\n",
    )
    with tempfile.TemporaryDirectory(prefix="molin-cleanup-partial55-contract-") as temporary:
        root = pathlib.Path(temporary)
        parser = root / "parser.awk"
        output = root / "partial55.stdout"
        parser.write_text(program, encoding="ascii", newline="\n")

        def run(candidate: str) -> subprocess.CompletedProcess[str]:
            output.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-c", '/usr/bin/awk -f "$1" "$2"', "--", str(parser), str(output)],
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(accepted.returncode == 0 and accepted.stdout == "environment_precheck\n" and accepted.stderr == "", "partial55_parser_valid")
        boundary_accepted = run(boundary_valid)
        require(boundary_accepted.returncode == 0 and boundary_accepted.stdout == "boundary_manifest_shape\n" and boundary_accepted.stderr == "", "partial55_boundary_parser_valid")
        for candidate in attacks:
            require(run(candidate).returncode != 0, "partial55_parser_attack")
    return 2 + len(attacks)


def validate_awk_stderr_runtime(payload: str, bash: str) -> int:
    """真实执行 awk stderr 分类器，确保混合错误或空输入不能取得清理资格。"""
    marker = 'elif [[ "$partial55_stage" = boundary_manifest_shape ]]; then\n'
    branch_start = payload.index(marker) + len(marker)
    parser_marker = '    partial55_stderr_class=$(\n      "$awk_bin" \'\n'
    start = payload.index(parser_marker, branch_start) + len(parser_marker)
    end = payload.index('\n      \' "$output/partial55.stderr"', start)
    program = payload[start:end]
    valid = "awk: cmd. line:1: source\nawk: cmd. line:1: ^ syntax error\n"
    attacks = (
        "",
        "Permission denied\n",
        valid + "Permission denied\n",
        "python: line 1: syntax error\n",
    )
    with tempfile.TemporaryDirectory(prefix="molin-cleanup-awk-stderr-") as temporary:
        root = pathlib.Path(temporary)
        parser = root / "parser.awk"
        stderr_file = root / "partial55.stderr"
        parser.write_text(program, encoding="ascii", newline="\n")

        def run(candidate: str) -> subprocess.CompletedProcess[str]:
            stderr_file.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-c", '/usr/bin/awk -f "$1" "$2"', "--", str(parser), str(stderr_file)],
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(accepted.returncode == 0 and accepted.stdout == "awk_syntax\n" and accepted.stderr == "", "awk_stderr_runtime_valid")
        for candidate in attacks:
            result = run(candidate)
            require(result.returncode == 0 and result.stdout == "mixed_or_invalid\n" and result.stderr == "", "awk_stderr_runtime_attack")
    return 1 + len(attacks)


def main() -> int:
    payload_bytes = PAYLOAD.read_bytes()
    runner_bytes = RUNNER.read_bytes()
    require(not payload_bytes.startswith(b"\xef\xbb\xbf"), "payload_bom")
    payload = payload_bytes.decode("utf-8")
    runner = runner_bytes.decode("utf-8-sig")
    validate_payload(payload)
    validate_runner(runner)
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(bash).is_file(), "bash_missing")
    runtime_cases = (
        validate_success_parser_runtime(payload, bash)
        + validate_partial55_parser_runtime(payload, bash)
        + validate_awk_stderr_runtime(payload, bash)
    )
    mutations = (
        (validate_payload, payload.replace("${#stages[@]} -eq 1", "${#stages[@]} -ge 1", 1)),
        (validate_payload, payload.replace("${#output_entries[@]} -eq 5", "${#output_entries[@]} -ge 5", 1)),
        (validate_payload, payload.replace("${#output_entries[@]} -eq 7", "${#output_entries[@]} -ge 7", 1)),
        (validate_payload, payload.replace('failure_stage=(environment_precheck|boundary_manifest_shape)', 'failure_stage=.*', 1)),
        (validate_payload, payload.replace("completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==4", "NR>=4", 1)),
        (validate_payload, payload.replace('[[ "$partial55_stderr_class" = other ]] || fail partial55_stderr_pair', ":", 1)),
        (validate_payload, payload.replace('[[ "$partial55_stderr_class" = awk_syntax ]] || fail partial55_stderr_pair', ":", 1)),
        (validate_payload, payload.replace('awk_syntax==NR', 'awk_syntax>0', 1)),
        (validate_payload, payload.replace("failure_stage=(environment_precheck|empty_baseline|empty_baseline_restore|empty_event_policy|empty_schema54_validate|empty_schema54_code_shape|schema55_down_sql|schema55_down_statement_05)", "failure_stage=", 1)),
        (validate_payload, payload.replace('((stage=="schema55_down_sql" || stage=="schema55_down_statement_05") && case_value=="schema55" && target_value=="true" && progress==3)', 'stage=="schema55_down_sql"', 1)),
        (validate_payload, payload.replace('expected_case[1]="empty"; expected_version[1]="54"', 'expected_case[1]="legacy"; expected_version[1]="54"', 1)),
        (validate_payload, payload.replace("target_id_sha256=[A-F0-9]+", "target_id_sha256=.*", 1)),
        (validate_payload, payload.replace("length(hash_field[2])!=64", "length(hash_field[2])<64", 1)),
        (validate_payload, payload.replace("seen[hash_field[2]]++", "0", 1)),
        (validate_payload, payload.replace('terminal==22 && completed==1 && progress==7 && NR==20', 'terminal==22', 1)),
        (validate_payload, payload.replace('print "summary_contract_mismatch"', 'print "unknown"', 1)),
        (validate_payload, payload.replace("NR==progress+4", "NR>=4", 1)),
        (validate_payload, payload.replace('(stage=="empty_baseline" || stage=="empty_baseline_restore") && case_value=="empty" && target_value=="true" && progress==0', "0", 1)),
        (validate_payload, payload.replace('(stage=="empty_event_policy" || stage=="empty_schema54_validate" || stage=="empty_schema54_code_shape") && case_value=="empty" && target_value=="true" && progress==1', "0", 1)),
        (validate_payload, payload.replace('[[ "$matrix55_stage" = empty_event_policy || "$matrix55_stage" = empty_schema54_validate || "$matrix55_stage" = schema55_down_sql ]]', '[[ "$matrix55_stage" = empty_event_policy ]]', 1)),
        (validate_payload, payload.replace('[[ "$matrix55_error" = other ]] || fail matrix55_error_pair', ":", 1)),
        (validate_payload, payload.replace('[[ "$matrix55_error" = constraint ]] || fail matrix55_error_pair', ":", 1)),
        (validate_payload, payload.replace('stage=="environment_precheck" && case_value=="none" && target_value=="false"', "0", 1)),
        (validate_payload, payload.replace("${#named_containers[@]} -eq 0", "${#named_containers[@]} -ge 0", 1)),
        (validate_payload, payload.replace('"$rm_bin" -rf --one-file-system -- "$stage"', '"$rm_bin" -rf /home/pc/molin-runtime/*', 1)),
        (validate_runner, runner.replace(sha(PAYLOAD), "0" * 64, 1)),
        (validate_runner, runner.replace("controller_single_execution", "controller_multiple_execution", 1)),
        (validate_runner, runner.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1)),
    )
    rejected = 0
    for index, (validator, candidate) in enumerate(mutations, start=1):
        try:
            validator(candidate)
        except ContractFailure:
            rejected += 1
        else:
            raise ContractFailure(f"mutation_accepted_{index}")
    syntax = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
    require(syntax.returncode == 0 and syntax.stderr == "", "bash_syntax")
    print(f"status=pass mode=email_migration_matrix55_environment_failure_cleanup_contract attack_cases={rejected} success_parser_runtime_cases={runtime_cases} external_access=false database_access=false docker_access=false retries=0")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_migration_matrix55_environment_failure_cleanup_contract classification=closed")
        raise SystemExit(1)

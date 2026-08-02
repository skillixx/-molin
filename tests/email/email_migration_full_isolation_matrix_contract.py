"""验证 000055/000056 全隔离矩阵单次远端编排资产。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import shutil
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-matrix-remote.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-migration-full-isolation-matrix.ps1"
GENERATOR = ROOT / "scripts" / "generate-email-migration-baselines.sh"
EXECUTE_WRAPPER = ROOT / "scripts" / "email-migration-manual-execute.payload.sh"
RECOVERY_CLEANUP = ROOT / "scripts" / "email-migration-matrix55-environment-failure-cleanup.payload.sh"
EXPECTED_MIGRATION_SET = "DE8D942A3C8BBB3E96456C1B85AE0BADAE7542E2A3E6FE0C34FD47C6140D914D"


class ContractFailure(RuntimeError):
    """表示编排资产偏离冻结边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def migration_set_sha() -> str:
    lines: list[str] = []
    migrations = ROOT / "server" / "migrations"
    for version in range(1, 57):
        matches = list(migrations.glob(f"{version:06d}_*.up.sql"))
        require(len(matches) == 1, "migration_set_shape")
        lines.append(f"{sha(matches[0])}\t{matches[0].name}\n")
    return hashlib.sha256("".join(lines).encode("ascii")).hexdigest().upper()


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n#"), "payload_header")
    require("\r" not in text and "\x00" not in text, "payload_encoding")
    require(text.count("set -Eeuo pipefail") == 1 and "exec 2>/dev/null" in text, "strict_mode")
    require(text.count("trap 'fail \"$stage\"' ERR") == 2 and text.count("trap - ERR") >= 2, "classified_errors")
    require("I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE" in text, "confirmation")
    require("email-migration-matrix-${nonce}" in text and "stage_retained=true" in text, "stage_identity")
    require("name=^/molin-mysql$" in text, "main_mysql_identity")
    require("readonly awk_link=/usr/bin/awk" in text and "readonly realpath_bin=/usr/bin/realpath" in text, "awk_fixed_entry")
    require('awk_bin=$("$realpath_bin" -e -- "$awk_link")' in text, "awk_canonical_resolution")
    require('[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ ]]' in text, "awk_canonical_boundary")
    require('[[ -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]]' in text, "awk_resolved_identity")
    require('[[ "$("$stat_bin" -c \'%U:%G\' -- "$awk_bin")" = root:root ]]' in text, "awk_root_owner")
    require("readonly id_bin=/usr/bin/id" in text and 'asset_uid=$("$id_bin" -u pc)' in text, "asset_uid_source")
    require('[[ "$asset_uid" =~ ^[1-9][0-9]*$ ]]' in text, "asset_uid_format")
    require('"$("$stat_bin" -c \'%u\' -- "$stage_root")" = "$asset_uid"' in text, "asset_uid_stage_binding")
    require('"$("$stat_bin" -c \'%u:%a\' -- "$asset_dir")" = "$asset_uid:700"' in text, "asset_uid_directory_binding")
    require('"$("$stat_bin" -c \'%u\' -- "$asset_file")" = "$asset_uid"' in text, "asset_uid_file_binding")
    require('"$sha256sum_bin" "$stat_bin" "$id_bin" "$realpath_bin"' in text and '"$sha256sum_bin" "$awk_link"' not in text, "host_tool_nonlink_policy")
    require("'{{.Config.Image}}'" in text and "image inspect --format '{{.Id}}'" in text, "image_readonly_identity")
    require(text.count("mysql@sha256:[a-f0-9]{64}") == 2 and "image_binding" in text, "image_digest_binding")
    require(text.count("--network none") == 1 and "--read-only" in text, "matrix_container_isolation")
    require("--tmpfs /var/lib/mysql" in text and "--tmpfs /root:rw,nosuid,nodev" in text, "matrix_tmpfs")
    require(text.count('--mount "type=bind,src=') == 4 and text.count(',readonly"') == 4 and text.count("assets|false") == 4, "readonly_asset_mounts")
    require("{{printf \"%s|%s|%s|%t\\n\" .Type .Source .Destination .RW}}" in text and "{{println .Destination" not in text, "mount_inspect_format")
    require("$3 ~ /^\\/root\\/molin-0000(55|56)-(isolation|partial)-assets$/" in text, "asset_mount_filter")
    require(text.count('"bind|$asset') == 4, "asset_mount_source_identity")
    require("asset_mount_identity" in text and "${#asset_mounts[@]} -eq 4" in text, "asset_mount_identity")
    require("mysql8_runtime_verified=true" in text and text.count("^mysql[[:space:]]+Ver[[:space:]]+8\\.[0-9]+\\.[0-9]+") == 1, "mysql8_runtime")
    require("MySQL[[:space:]]+Community[[:space:]]+Server" in text, "mysql8_distribution")
    require("MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE" in text and "baseline_outputs=6" in text, "baseline_generation")
    require("matrix55=true partial55=true matrix56=true partial56=true" in text, "matrix_summary")
    require("runtime_unique_targets=94" in text and "$'7\\t33\\t11\\t43'" in text, "target_count")
    for gate in (
        "MOLIN_000055_ISOLATION_EXECUTE",
        "MOLIN_000055_PARTIAL_EXECUTE",
        "MOLIN_000056_ISOLATION_EXECUTE",
        "MOLIN_000056_PARTIAL_EXECUTE",
    ):
        require(gate in text, f"runner_gate:{gate}")
    require(text.count('"$docker_bin" run --detach') == 1, "matrix_container_count")
    require(text.count('"$docker_bin" rm --force -- "$matrix_container_id"') == 2, "matrix_container_cleanup")
    require("temporary_containers_removed=true" in text and "main_database_modified=false" in text, "safe_summary")
    require("MYSQL_ROOT_PASSWORD=$matrix_password" in text and "matrix_password=" in text, "temporary_password")
    require("validate_stderr" in text and "mysql_stderr_length" in text, "stderr_allowlist")
    require("validate_stdout" in text and 'target_id_sha256=[A-F0-9]+' in text and 'length(hash_field[2])!=64' in text, "stdout_identity")
    require('case_field[2]!=expected_case[NR]' in text and 'schema_field[2]!=expected_schema[NR]' in text, "stdout_order")
    require('seen[hash_field[2]]++' in text and 'progress!=expected_count' in text, "stdout_unique_complete")
    require('"$tail_bin" -n "+$((expected_count + 1))"' in text and '[[ "$terminal" = "$expected" ]]' in text, "stdout_terminal")
    require("classify_runner_failure" in text and '[[ "$name" = partial55 ]] || return 1' in text, "partial55_failure_classifier")
    require('print "partial55_" failure_stage' in text and 'NR != 4' in text, "partial55_failure_output")
    require('family=="dynamic" && case_value==dynamic_case && target_value=="true"' in text, "partial55_dynamic_failure_pair")
    require('family=="matrix" && case_value!="none"' in text, "partial55_matrix_failure_pair")
    require('failure_classification=$(classify_runner_failure "$name" "$stdout_file")' in text, "partial55_failure_transport")
    require('failure_classification="${name}_execution_unclassified"' in text, "runner_failure_fallback")
    require("case_spec55='empty:54,legacy:54,schema55:55,ownfresh:54,ownperm:54,ownall:54,ownmixed:54'" in text, "matrix55_case_spec")
    require("case_spec56='ownfresh:55,ownperm:55,ownall:55,adminzero:55,admintwo:55,metaconf:55,receipt:55,refrole:55,refuser:55,refgroup:55,concurrent:55'" in text, "matrix56_case_spec")
    require(text.count('END {printf "%sup_baseline:') == 2, "partial_case_specs")
    require("trap - ERR\n  set +e\n  \"$docker_bin\" exec" in text and "set -e\n  trap 'fail \"$stage\"' ERR" in text, "matrix_explicit_exit_capture")
    require('-e "MOLIN_MATRIX_ASSET_UID=$asset_uid"' in text, "asset_uid_container_binding")
    for variable in (
        "MOLIN_000055_SCHEMA54_SHA", "MOLIN_000055_SCHEMA55_SHA", "MOLIN_000055_BASELINE_MANIFEST_SHA",
        "MOLIN_000056_SCHEMA55_SHA", "MOLIN_000056_SCHEMA56_SHA", "MOLIN_000056_BASELINE_MANIFEST_SHA",
    ):
        require(f'-e "{variable}=' in text, f"partial_hash_transport:{variable}")
    forbidden = (
        "docker pull", "--network host", "--publish", "-p 3306", "--privileged",
        "--volumes-from", "docker exec \"$main_mysql_id\"", "--database=molin ",
        "FLUSHDB", "FLUSHALL", "KEYS *", "operation_id=", "RequestId",
        '"$docker_bin" cp',
    )
    for item in forbidden:
        require(item.lower() not in text.lower(), f"payload_forbidden:{item}")


def validate_controller(text: str) -> None:
    require("I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE" in text, "controller_confirmation")
    require("I_CONFIRM_EMAIL_MIGRATION_PARTIAL55_BOUNDARY_AWK_STDERR_RECOVERY_ONCE" in text, "recovery_confirmation")
    require("I_CONFIRM_LOCAL_EMAIL_MIGRATION_MANUAL_PACKAGE_ONCE" in text, "prepare_confirmation")
    require(EXPECTED_MIGRATION_SET in text, "migration_set_hash")
    require(sha(PAYLOAD) in text and sha(GENERATOR) in text and sha(EXECUTE_WRAPPER) in text, "payload_generator_hash")
    require("$entries.Count -ne 66" in text and "$version -le 56" in text, "source_count")
    require("function Assert-PackageMembers" in text and "$actual.SetEquals($expected)" in text, "package_exact_members")
    require("$member.Contains('..')" in text and "$member.Contains(':')" in text and "-not $actual.Add($member)" in text, "package_member_attacks")
    require("Get-ExecutionWrapper" in text and "$executeScript=Get-ExecutionWrapper" in text, "single_wrapper_source")
    require("Management.Automation.Language.Parser" in text and "controller_transport_budget" in text, "ast_selftest")
    require("retained_stage_gate" in text and "${#retained_stages[@]} -eq 0" in text and "fail retained_stage_present" in text, "retained_stage_gate")
    require("status=failed mode=email_migration_stage_setup" in text and text.count("trap 'fail \"$phase\"' ERR") == 2, "setup_failure_classification")
    require("[switch]$RecoverKnownFailure" in text and "Get-RecoveryCleanupScript" in text, "recovery_mode")
    require(sha(RECOVERY_CLEANUP) in text and "$setup=$recoveryCleanup+$recoveryStageSetup" in text, "recovery_cleanup_binding")
    require("$setupTail=@('--execute','I_CONFIRM_EMAIL_MIGRATION_MATRIX55_ENVIRONMENT_FAILURE_CLEANUP_ONCE',$nonce)" in text, "recovery_arguments")
    require("baseline_outputs=6 matrix_outputs=2 removed_count=1" in text, "partial55_recovery_output_contract")
    require("verified_matrix55_known_failure_stage_removed" in text and "recovery_retained_stage_gate" in text, "recovery_output_gate")
    require("if($RecoverKnownFailure){\n  if($Confirm-cne$script:RecoveryConfirmPhrase){throw 'recovery_confirmation_required'}" in text, "recovery_confirmation_gate")
    confirmation = text.index("if(-not$Execute){throw 'confirmation_required'}")
    transport = text.index("$ssh=Join-Path $env:WINDIR")
    require(confirmation < transport, "confirmation_before_transport")
    require(text.count("$sshAttempts++") == 2 and text.count("$scpAttempts++") == 1, "transport_budget")
    require("$scpDestination=($script:Remote+':'+$remoteStage+'/package.tar.gz')" in text, "scp_destination_scalar")
    require("'ConnectTimeout=10','--',$package.Archive,$scpDestination" in text, "scp_operand_boundary")
    require("'-O'" in text and text.count("StrictHostKeyChecking=yes") == 2, "transport_policy")
    require("ArgumentList=$Arguments" in text and "[string[]]$Arguments" in text, "argument_array")
    require("& $FilePath @Arguments 1> $stdout 2> $stderr" in text and "Invoke-NativeScp -FilePath $scp" in text, "native_scp_argv")
    require("pc@example:/tmp/package.tar.gz" in text and "native_scp_failed" in text, "native_scp_regression")
    require("[Text.UTF8Encoding]::new($false, $true).GetBytes($normalized)" in text, "stdin_no_bom")
    require("7200s" in text and "TimeoutMilliseconds 7300000" in text, "bounded_timeout")
    require("stage_retained=true ssh_attempts=$sshAttempts" in text, "failure_retention")
    require("stdout_length=$stdoutLength stderr_length=$stderrLength" in text, "failure_lengths")
    require("FailureClassification" in text and "$failureClassification=$match.Groups[1].Value" in text, "failure_classification")
    require("mode=email_migration_matrix55_environment_failure_cleanup classification=([a-z0-9_]+)" in text, "recovery_failure_classification")
    require("classification=$failureClassification exit_code=" in text and "stdout=$output" not in text, "sanitized_failure_output")
    require("operation_id=" not in text and "operation_id=$nonce" not in text, "identifier_output")
    require(text.count("main_database_modified=false") == 2 and text.count("targets=94") == 2, "final_summary")
    require("Remove-LocalStage $package.Root" in text, "local_cleanup")
    require("if($PrepareOnly)" in text and "package_path=$preparedPackage.Archive" in text and "migration_executed=$false" in text, "manual_package_mode")


def validate_execute_wrapper(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n#") and "\r" not in text and "\x00" not in text, "wrapper_encoding")
    require(text.count("set -Eeuo pipefail") == 1 and text.count("trap 'fail \"$phase\"' ERR") == 2, "wrapper_failure_trap")
    require("[[ $# -eq 4 ]] || fail argument_gate" in text, "wrapper_arguments")
    require("stage_entries" in text and "${#stage_entries[@]} -eq 1" in text and '"${stage_entries[0]}" = package.tar.gz' in text, "wrapper_stage_contents")
    require("sha256sum --check --strict --status source-manifest.sha256" in text and '"$(wc -l < "$stage/source-manifest.sha256")" -eq 66' in text, "wrapper_manifest")
    require(text.count('"$payload" --execute I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE "$nonce"') == 1, "wrapper_single_payload_call")
    require("payload_exit=$?" in text and '[[ $payload_exit -eq 0 ]] || exit "$payload_exit"' in text, "wrapper_exit_preserved")
    require("realpath -e -- \"$stage\"" in text and "rm -rf --one-file-system -- \"$stage\"" in text, "wrapper_exact_cleanup")
    require(text.count("find \"$stage\" -type l -print -quit") == 2, "wrapper_symlink_gate")
    require("status=pass stage=remote_stage_removed" in text, "wrapper_success")


def validate_stdout_runtime(payload: str, bash: str) -> int:
    """使用真实 Bash 工具链验证动态 case 前缀和固定终止摘要。"""
    start = payload.index("validate_stdout() {")
    end = payload.index("\n\nrun_matrix() {", start)
    function_source = payload[start:end]
    expected = "matrix_completed=true\ndatabase_access=true"
    harness = (
        "set -Eeuo pipefail\n"
        "awk_bin=$(command -v awk)\n"
        "tail_bin=$(command -v tail)\n"
        f"{function_source}\n"
        "expected=$'matrix_completed=true\\ndatabase_access=true'\n"
        'validate_stdout "$1" "$2" "$expected"\n'
    )
    digest_a = "A" * 64
    digest_b = "B" * 64
    valid = (
        f"case=empty target_id_sha256={digest_a} restored_schema=54\n"
        f"case=schema55 target_id_sha256={digest_b} restored_schema=55\n"
        f"{expected}\n"
    )
    attacks = (
        valid.replace("case=empty", "case=schema55", 1),
        valid.replace("restored_schema=54", "restored_schema=55", 1),
        valid.replace(digest_b, digest_a, 1),
        valid.replace(f"case=schema55 target_id_sha256={digest_b} restored_schema=55\n", "", 1),
        valid.replace("matrix_completed=true", "matrix_completed="),
        valid.replace("database_access=true", "database_access="),
    )
    with tempfile.TemporaryDirectory(prefix="molin-matrix-stdout-contract-") as temporary:
        output = pathlib.Path(temporary) / "stdout.txt"

        def run(candidate: str) -> subprocess.CompletedProcess[str]:
            output.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-s", "--", str(output), "empty:54,schema55:55"],
                input=harness,
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(accepted.returncode == 0 and accepted.stdout == "" and accepted.stderr == "", "stdout_runtime_valid")
        for candidate in attacks:
            require(run(candidate).returncode != 0, "stdout_runtime_attack")
    return 1 + len(attacks)


def validate_failure_classifier_runtime(payload: str, bash: str) -> int:
    """使用真实 Bash 验证 partial55 四行失败摘要只能映射到固定白名单阶段。"""
    start = payload.index("classify_runner_failure() {")
    end = payload.index("\n\nrun_matrix() {", start)
    function_source = payload[start:end]
    harness = (
        "set -Eeuo pipefail\n"
        "awk_bin=$(command -v awk)\n"
        f"{function_source}\n"
        'classify_runner_failure "$1" "$2"\n'
    )
    valid = (
        "partial_matrix_completed=false\n"
        "failure_stage=environment_hash_inputs\n"
        "case=none\n"
        "target_created=false\n"
    )
    dynamic_valid = (
        "partial_matrix_completed=false\n"
        "failure_stage=up_table_templates_validate_state\n"
        "case=up_table_templates\n"
        "target_created=true\n"
    )
    attacks = (
        valid.replace("partial_matrix_completed=false", "partial_matrix_completed=true", 1),
        valid.replace("environment_hash_inputs", "environment_precheck", 1),
        valid.replace("case=none", "case=empty", 1),
        valid.replace("target_created=false", "target_created=true", 1),
        valid + "extra=true\n",
        valid.replace("case=none\n", "", 1),
        dynamic_valid.replace("case=up_table_templates", "case=up_table_bindings", 1),
        dynamic_valid.replace("target_created=true", "target_created=false", 1),
    )
    with tempfile.TemporaryDirectory(prefix="molin-matrix-failure-contract-") as temporary:
        output = pathlib.Path(temporary) / "stdout.txt"

        def run(candidate: str, name: str = "partial55") -> subprocess.CompletedProcess[str]:
            output.write_text(candidate, encoding="ascii", newline="\n")
            return subprocess.run(
                [bash, "--noprofile", "--norc", "-s", "--", name, str(output)],
                input=harness,
                capture_output=True,
                text=True,
                timeout=10,
            )

        accepted = run(valid)
        require(
            accepted.returncode == 0
            and accepted.stdout == "partial55_environment_hash_inputs\n"
            and accepted.stderr == "",
            "failure_classifier_runtime_valid",
        )
        dynamic_accepted = run(dynamic_valid)
        require(
            dynamic_accepted.returncode == 0
            and dynamic_accepted.stdout == "partial55_up_table_templates_validate_state\n"
            and dynamic_accepted.stderr == "",
            "failure_classifier_dynamic_valid",
        )
        for candidate in attacks:
            require(run(candidate).returncode != 0, "failure_classifier_runtime_attack")
        require(run(valid, "partial56").returncode != 0, "failure_classifier_name_attack")
    return 3 + len(attacks)


def mutations(payload: str, controller: str, wrapper: str) -> tuple[tuple[object, str], ...]:
    return (
        (validate_payload, payload.replace("set -Eeuo pipefail", "set -eo pipefail", 1)),
        (validate_payload, payload.replace("exec 2>/dev/null", "true", 1)),
        (validate_payload, payload.replace("trap 'fail \"$stage\"' ERR", "true", 1)),
        (validate_payload, payload.replace("name=^/molin-mysql$", "name=molin-mysql", 1)),
        (validate_payload, payload.replace('awk_bin=$("$realpath_bin" -e -- "$awk_link")', 'awk_bin="$awk_link"', 1)),
        (validate_payload, payload.replace('^/usr/bin/[A-Za-z0-9._+-]+$', '^/.*$', 1)),
        (validate_payload, payload.replace('= root:root ]] || fail host_tool', '= pc:pc ]] || fail host_tool', 1)),
        (validate_payload, payload.replace('asset_uid=$("$id_bin" -u pc)', "asset_uid=0", 1)),
        (validate_payload, payload.replace('-e "MOLIN_MATRIX_ASSET_UID=$asset_uid"', "", 1)),
        (validate_payload, payload.replace('-e "MOLIN_000055_SCHEMA54_SHA=$sha54_legacy"', "", 1)),
        (validate_payload, payload.replace('"$sha256sum_bin" "$stat_bin" "$id_bin" "$realpath_bin"', '"$sha256sum_bin" "$awk_link" "$stat_bin" "$id_bin" "$realpath_bin"', 1)),
        (validate_payload, payload.replace("mysql@sha256:[a-f0-9]{64}", "mysql:8")),
        (validate_payload, payload.replace("--network none", "--network host", 1)),
        (validate_payload, payload.replace("--read-only", "", 1)),
        (validate_payload, payload.replace("--tmpfs /root:rw,nosuid,nodev", "--tmpfs /root:rw", 1)),
        (validate_payload, payload.replace(',readonly"', '"', 1)),
        (validate_payload, payload.replace('{{printf "%s|%s|%s|%t\\n" .Type .Source .Destination .RW}}', '{{println .Destination "|" .RW}}', 1)),
        (validate_payload, payload.replace("$3 ~ /^\\/root\\/molin-0000(55|56)-(isolation|partial)-assets$/", "$3 ~ /assets/", 1)),
        (validate_payload, payload.replace('"bind|$asset55_full|', '"volume|$asset55_full|', 1)),
        (validate_payload, payload.replace("${#asset_mounts[@]} -eq 4", "${#asset_mounts[@]} -gt 0", 1)),
        (validate_payload, payload.replace("^mysql[[:space:]]+Ver[[:space:]]+8\\.[0-9]+\\.[0-9]+", "Ver", 1)),
        (validate_payload, payload.replace("Ver[[:space:]]+8\\.", "Ver[[:space:]]+7\\.", 1)),
        (validate_payload, payload.replace("runtime_unique_targets=94", "runtime_unique_targets=93", 1)),
        (validate_payload, payload.replace("$'7\\t33\\t11\\t43'", "$'7\\t33\\t11\\t42'", 1)),
        (validate_payload, payload.replace("matrix55=true", "matrix55=false", 1)),
        (validate_payload, payload.replace("matrix55=true", "matrix55=", 1)),
        (validate_payload, payload.replace('target_id_sha256=[A-F0-9]+', 'target_id_sha256=.*', 1)),
        (validate_payload, payload.replace('length(hash_field[2])!=64', 'length(hash_field[2])<64', 1)),
        (validate_payload, payload.replace('case_field[2]!=expected_case[NR]', 'case_field[2]==""', 1)),
        (validate_payload, payload.replace('schema_field[2]!=expected_schema[NR]', 'schema_field[2]==""', 1)),
        (validate_payload, payload.replace('seen[hash_field[2]]++', '0', 1)),
        (validate_payload, payload.replace('progress!=expected_count', 'progress>expected_count', 1)),
        (validate_payload, payload.replace('[[ "$terminal" = "$expected" ]]', '[[ -n "$terminal" ]]', 1)),
        (validate_payload, payload.replace('[[ "$name" = partial55 ]] || return 1', '[[ -n "$name" ]] || return 1', 1)),
        (validate_payload, payload.replace('NR != 4', 'NR < 4', 1)),
        (validate_payload, payload.replace('family=="dynamic" && case_value==dynamic_case && target_value=="true"', 'family=="dynamic"', 1)),
        (validate_payload, payload.replace('family=="matrix" && case_value!="none"', 'family=="matrix"', 1)),
        (validate_payload, payload.replace('print "partial55_" failure_stage', 'print failure_stage', 1)),
        (validate_payload, payload.replace('failure_classification="${name}_execution_unclassified"', 'failure_classification="$name"', 1)),
        (validate_payload, payload.replace('empty:54,legacy:54,schema55:55', 'empty:54,legacy:55,schema55:55', 1)),
        (validate_payload, payload.replace('ownfresh:55,ownperm:55,ownall:55', 'ownfresh:56,ownperm:55,ownall:55', 1)),
        (validate_payload, payload.replace('END {printf "%sup_baseline:54,down_baseline:55",sep}', 'END {printf "%s",sep}', 1)),
        (validate_payload, payload.replace("MOLIN_000055_PARTIAL_EXECUTE", "MOLIN_BYPASS", 1)),
        (validate_payload, payload.replace("trap - ERR\n  set +e\n  \"$docker_bin\" exec", "set +e\n  \"$docker_bin\" exec", 1)),
        (validate_payload, payload.replace('"$docker_bin" rm --force -- "$matrix_container_id"', ":", 1)),
        (validate_payload, payload + "\ndocker pull mysql:8\n"),
        (validate_payload, payload + "\ndocker exec \"$main_mysql_id\" mysql --database=molin\n"),
        (validate_payload, payload.replace("main_database_modified=false", "main_database_modified=true", 1)),
        (validate_controller, controller.replace("if(-not$Execute){throw 'confirmation_required'}", ":", 1)),
        (validate_controller, controller.replace("I_CONFIRM_EMAIL_MIGRATION_PARTIAL55_BOUNDARY_AWK_STDERR_RECOVERY_ONCE", "BYPASS_RECOVERY", 1)),
        (validate_controller, controller.replace("baseline_outputs=6 matrix_outputs=2 removed_count=1", "baseline_outputs=6 matrix_outputs=1 removed_count=1", 1)),
        (validate_controller, controller.replace("if($PrepareOnly)", "if($true)", 1)),
        (validate_controller, controller.replace("I_CONFIRM_LOCAL_EMAIL_MIGRATION_MANUAL_PACKAGE_ONCE", "BYPASS", 1)),
        (validate_controller, controller.replace(EXPECTED_MIGRATION_SET, "0" * 64, 1)),
        (validate_controller, controller.replace(sha(PAYLOAD), "0" * 64, 1)),
        (validate_controller, controller.replace(sha(EXECUTE_WRAPPER), "0" * 64, 1)),
        (validate_controller, controller.replace(sha(RECOVERY_CLEANUP), "0" * 64, 1)),
        (validate_controller, controller.replace("$entries.Count -ne 66", "$entries.Count -ne 65", 1)),
        (validate_controller, controller.replace("$actual.SetEquals($expected)", "$actual.IsSupersetOf($expected)", 1)),
        (validate_controller, controller.replace("-not $actual.Add($member)", "$false", 1)),
        (validate_controller, controller.replace("$member.Contains('..')", "$false", 1)),
        (validate_controller, controller.replace("$executeScript=Get-ExecutionWrapper", "$executeScript='inline'", 1)),
        (validate_controller, controller.replace("Management.Automation.Language.Parser", "Management.Automation.Language.Token", 1)),
        (validate_controller, controller.replace("${#retained_stages[@]} -eq 0", "${#retained_stages[@]} -ge 0", 1)),
        (validate_controller, controller.replace("trap 'fail \"$phase\"' ERR", "true")),
        (validate_controller, controller.replace("$sshAttempts++", "# removed", 1)),
        (validate_controller, controller.replace("$scpAttempts++", "# removed", 1)),
        (validate_controller, controller.replace("$scpDestination=($script:Remote+':'+$remoteStage+'/package.tar.gz')", "$scpDestination=$script:Remote+':'+$remoteStage+'/package.tar.gz'", 1)),
        (validate_controller, controller.replace("'ConnectTimeout=10','--',$package.Archive,$scpDestination", "'ConnectTimeout=10',$package.Archive,$scpDestination", 1)),
        (validate_controller, controller.replace("'-O'", "'-p'", 1)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1)),
        (validate_controller, controller.replace("[string[]]$Arguments", "[string]$Arguments")),
        (validate_controller, controller.replace("& $FilePath @Arguments 1> $stdout 2> $stderr", "Start-Process -FilePath $FilePath -ArgumentList $Arguments", 1)),
        (validate_controller, controller.replace("pc@example:/tmp/package.tar.gz", "package.tar.gz")),
        (validate_controller, controller.replace("stage_retained=true ssh_attempts=", "stage_retained=false ssh_attempts=", 1)),
        (validate_controller, controller.replace("stdout_length=$stdoutLength", "stdout=raw", 1)),
        (validate_controller, controller.replace("$failureClassification=$match.Groups[1].Value", "$failureClassification=$output", 1)),
        (validate_controller, controller.replace("mode=email_migration_matrix55_environment_failure_cleanup classification=([a-z0-9_]+)", "mode=email_migration_matrix55_environment_failure_cleanup classification=ignored", 1)),
        (validate_controller, controller.replace("targets=94", "targets=93")),
        (validate_controller, controller.replace("main_database_modified=false", "main_database_modified=true")),
        (validate_execute_wrapper, wrapper.replace("trap 'fail \"$phase\"' ERR", "true", 1)),
        (validate_execute_wrapper, wrapper.replace("${#stage_entries[@]} -eq 1", "${#stage_entries[@]} -ge 1", 1)),
        (validate_execute_wrapper, wrapper.replace('"${stage_entries[0]}" = package.tar.gz', '"${stage_entries[0]}" != package.tar.gz', 1)),
        (validate_execute_wrapper, wrapper.replace("sha256sum --check --strict --status", "sha256sum --check", 1)),
        (validate_execute_wrapper, wrapper.replace('"$payload" --execute I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE "$nonce"', '"$payload" --execute I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE "$nonce"\n"$payload" --execute I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE "$nonce"', 1)),
        (validate_execute_wrapper, wrapper.replace("realpath -e -- \"$stage\"", "printf '%s' \"$stage\"", 1)),
        (validate_execute_wrapper, wrapper.replace("rm -rf --one-file-system -- \"$stage\"", "rm -rf /home/pc/molin-runtime/*", 1)),
        (validate_execute_wrapper, wrapper.replace("find \"$stage\" -type l -print -quit", "true", 1)),
    )


def find_bash() -> str:
    result = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(result).is_file(), "bash_missing")
    return result


def main() -> int:
    payload_bytes = PAYLOAD.read_bytes()
    controller_bytes = CONTROLLER.read_bytes()
    wrapper_bytes = EXECUTE_WRAPPER.read_bytes()
    require(not payload_bytes.startswith(b"\xef\xbb\xbf") and b"\r" not in payload_bytes, "payload_bytes")
    require(controller_bytes.startswith(b"\xef\xbb\xbf"), "controller_bom")
    payload = payload_bytes.decode("utf-8")
    controller = controller_bytes.decode("utf-8-sig")
    require(not wrapper_bytes.startswith(b"\xef\xbb\xbf") and b"\r" not in wrapper_bytes, "wrapper_bytes")
    wrapper = wrapper_bytes.decode("utf-8")
    require(migration_set_sha() == EXPECTED_MIGRATION_SET, "migration_set")
    validate_payload(payload)
    validate_controller(controller)
    validate_execute_wrapper(wrapper)
    bash = find_bash()
    runtime_cases = validate_stdout_runtime(payload, bash)
    failure_runtime_cases = validate_failure_classifier_runtime(payload, bash)
    rejected = 0
    for validator, candidate in mutations(payload, controller, wrapper):
        try:
            validator(candidate)
        except (ContractFailure, ValueError):
            rejected += 1
        else:
            raise ContractFailure("mutation_accepted")
    syntax = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
    require(syntax.returncode == 0 and not syntax.stderr, "bash_syntax")
    wrapper_syntax = subprocess.run([bash, "-n", str(EXECUTE_WRAPPER)], capture_output=True, text=True, timeout=10)
    require(wrapper_syntax.returncode == 0 and not wrapper_syntax.stderr, "wrapper_bash_syntax")
    print(
        "status=pass mode=email_migration_full_isolation_matrix_contract "
        f"attack_cases={rejected} stdout_runtime_cases={runtime_cases} failure_runtime_cases={failure_runtime_cases} sources=66 migrations=56 external_access=false docker_access=false database_access=false migration_executed=false"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_migration_full_isolation_matrix_contract classification=closed")
        raise SystemExit(1)

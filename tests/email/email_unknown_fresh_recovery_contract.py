"""验证 Redis unknown 全新周期失败现场恢复资产的安全边界。"""

from __future__ import annotations

import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-fresh-recovery.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-fresh-recovery.ps1"


class ContractFailure(RuntimeError):
    """表示恢复资产偏离单次授权边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n# DirectMail Phase 4 保留 unknown stage"), "header")
    require(text.count("set -Eeuo pipefail") == 1, "strict_mode")
    require(text.count("exec 2>/dev/null") == 1, "stderr_closed")
    require('^(preflight|uploaded_preflight|cleanup)$' in text, "actions")
    require("${#stage_candidates[@]} -eq 1" in text and "${#stage_links[@]} -eq 0" in text, "stage_unique")
    require("email-unknown-cycle-([a-f0-9]{32})" in text, "stage_identity")
    require(text.count('sha256sum -- "$old_binary"') >= 1, "old_binary_hash")
    require(text.count('sha256sum -- "$old_payload"') >= 1, "old_payload_hash")
    require('sha256sum -- "$recovery_binary"' in text, "recovery_binary_hash")
    require(
        "mismatch_recovery_uploaded_binary_preflight" in text
        and "binary_regular=true binary_symlink=false binary_owner=true"
        in text
        and "binary_mode=%s binary_hash_match=%s retained=true writes=false retries=0" in text,
        "uploaded_binary_diagnostic",
    )
    cleanup_branch = text.split("  cleanup)", 1)[1]
    require(
        '[[ "$(stat -c \'%U\' -- "$recovery_binary")" == pc ]]' in text
        and '[[ "$binary_mode" =~ ^[0-7]{3}$ ]]' in text
        and cleanup_branch.index('chmod 500 -- "$recovery_binary"')
        < cleanup_branch.index('assert_file "$recovery_binary" 500'),
        "uploaded_mode_normalization",
    )
    require(
        0 <= cleanup_branch.index("load_complete_mismatch_state")
        < cleanup_branch.index('chmod 500 -- "$recovery_binary"'),
        "state_before_recovery_write",
    )
    require("object_pairs_hook=strict_object" in text and "duplicate" in text, "strict_json")
    require("os.O_NOFOLLOW" in text and "os.fstat(descriptor)" in text and "info.st_nlink != 1" in text, "state_nofollow")
    require('values[0] not in {"initializing", "phase1_created"}' in text, "state_phase")
    require("values[7] != 0" in text and "values[3] != expected_operator" in text, "state_identity")
    require('python3 -B - "$state_file" "$expected_operator_id" "$operation_id"' in text, "stage_nonce_argument")
    require("values[1] == expected_operation" in text and '"$state_nonce" != "$operation_id"' in text, "stage_nonce_mismatch")
    require("load_complete_mismatch_state" in text and "phase1_created" in text, "complete_only_preflight")
    require("EMAIL_UNKNOWN_RESTART_PHASE=cleanup_phase1" in text, "phase1_cleanup")
    require("EMAIL_ADAPTER=mock" in text, "mock_adapter")
    require("$'57\\t0\\t1\\t1\\t1\\t0\\t1\\t1\\t1\\t1'" in text, "exact_ownership_snapshot")
    require("$'0\\t0\\t0\\t0'" in text, "exact_cleanup_counts")
    require(
        all(
            fragment in text
            for fragment in (
                "email_provider_templates WHERE id=${template_id}",
                "email_test_recipient_allowlist WHERE id=${allowlist_id}",
                "email_send_logs WHERE id=${send_log_id}",
            )
        ),
        "exact_ids",
    )
    require('rm -f -- "$old_binary" "$old_payload" "$recovery_binary"' in text, "exact_file_cleanup")
    require('rmdir -- "$stage"' in text and "rm -rf" not in text, "exact_stage_cleanup")
    require("redis_delete=false" in text and "retries=0" in text, "safe_summary")
    require(
        "FROM email_test_recipient_allowlists" not in text
        and "INTO email_test_recipient_allowlists" not in text
        and "UPDATE email_test_recipient_allowlists" not in text
        and "DELETE FROM email_test_recipient_allowlists" not in text,
        "plural_table_forbidden",
    )
    require("mysql --no-defaults" in text, "mysql_defaults")
    require("assert_fixture_ownership" in text and "fail fixture_query" in text and "fail fixture_ownership" in text, "fixture_ownership")
    require(text.count("if ! counts=$(mysql_exec") == 1 and "fail cleanup_postcheck_query" in text, "cleanup_query_failure")
    require('redis-cli -n "$REDIS_DB" --raw EXISTS "lock:email:dispatch:${lock_digest}"' in text, "redis_exact_exists")
    require(
        "if ! current_run_id=$(redis_run_id); then fail redis_identity; fi" in text
        and "then fail redis_exact_exists; fi" in text,
        "redis_failure_classification",
    )
    require("redis_key_exists=0" in text and "redis_delete=false" in text, "redis_no_delete_summary")
    require("docker restart" not in text and "mysqldump" not in text, "forbidden_side_effect")
    require(re.search(r"\b(?:FLUSHDB|FLUSHALL|KEYS|SCAN)\b", text, re.IGNORECASE) is None, "redis_forbidden")
    require(re.search(r"redis-cli[^\n]*(?:DEL|UNLINK|--scan)", text, re.IGNORECASE) is None, "redis_delete")
    cleanup_branch = text.split("  cleanup)", 1)[1]
    require(
        cleanup_branch.index("assert_fixture_ownership")
        < cleanup_branch.index("assert_redis_ownership")
        < cleanup_branch.index('chmod 500 -- "$recovery_binary"'),
        "readonly_gates_before_chmod",
    )


def validate_controller(text: str) -> None:
    require(
        "I_CONFIRM_EMAIL_UNKNOWN_STAGE_NONCE_MISMATCH_EXACT_RECOVERY_ONCE" in text
        and "I_CONFIRM_EMAIL_UNKNOWN_STAGE_NONCE_MISMATCH_PREFLIGHT_METADATA_ONCE" in text,
        "confirm_phrase",
    )
    require("I_CONFIRM_EMAIL_UNKNOWN_UPLOADED_BINARY_PREFLIGHT_METADATA_ONCE" in text, "uploaded_confirm_phrase")
    require("I_CONFIRM_EMAIL_UNKNOWN_RESUME_UPLOADED_BINARY_EXACT_CLEANUP_ONCE" in text, "resume_confirm_phrase")
    confirmation = text.index("if (-not $Execute)")
    transport = text.index("$ssh = Join-Path $env:WINDIR")
    require(confirmation < transport, "confirm_before_transport")
    require("email_unknown_stage_nonce_mismatch_recovery_selftest external_access=false" in text, "offline_selftest")
    require("$script:PayloadSHA" in text and "throw 'payload_hash'" in text, "payload_hash")
    require(
        "$script:RecoveryBinarySHA = '1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900'" in text
        and "$script:RecoveryBinarySize = 25573597" in text
        and "$recoverySHA -cne $script:RecoveryBinarySHA" in text,
        "recovery_binary_frozen",
    )
    require(
        "Invoke-FixedProcess -FilePath $bash -Arguments @('-c', 'cat')" in text
        and '$transportProbe -cne "transport-probe`n"' in text,
        "transport_selftest",
    )
    require("StrictHostKeyChecking=yes" in text and "BatchMode=yes" in text, "ssh_policy")
    require(text.count("$errorOutput.Length -ne 0") == 1, "stderr_fail_closed")
    require(
        text.index("$process.ExitCode -ne 0") < text.index("if ($errorOutput.Length -ne 0)"),
        "failure_before_stderr",
    )
    require(
        "'stderr_nonempty'" in text and "'exit_nonzero'" in text
        and "StdoutLength" in text and "StderrLength" in text and "ExitCode" in text,
        "stderr_metadata",
    )
    require(
        "$exitCode = [string]$_.Exception.Data['ExitCode']" in text
        and "$stdoutLength = [string]$_.Exception.Data['StdoutLength']" in text
        and "$stderrLength = [string]$_.Exception.Data['StderrLength']" in text,
        "failure_length_propagation",
    )
    require(
        "mode=$mode stage=$currentStage remote_stage=$remoteStage exit_code=$exitCode stdout_length=$stdoutLength stderr_length=$stderrLength retained=true ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0" in text
        and "RemoteStage" in text and "failurePattern" in text
        and "$process.ExitCode -eq 2 -and $output -cmatch $failurePattern" in text,
        "failure_identity",
    )
    require("Write-Output $preflight.Trim()" not in text and "Write-Output $cleanup.Trim()" not in text, "identifier_output")
    require("operation_id=$operation" not in text, "identifier_output")
    require(
        "preflight=true fixture_ownership=true redis_identity=true redis_key_exists=0 cleanup=true retained=false ssh_attempts=2 scp_attempts=1 retries=0" in text,
        "sanitized_success_summary",
    )
    require("email-unknown-phase1-recovery.test" in text, "fixed_recovery_path")
    require("[string[]]$Arguments" in text and "Start-Process @startParameters" in text, "argument_array")
    require("'-q', '-O', '-P', '10003'" in text, "legacy_scp_fixed")
    preflight = text.index("$preflight = Invoke-RemotePayload")
    uploaded_diagnostic_exit = text.index("if ($UploadedBinaryPreflightOnly) {", preflight)
    resume_exit = text.index("if ($ResumeUploadedCleanup) {", preflight)
    diagnostic_exit = text.index("if ($PreflightOnly) {", preflight)
    upload = text.index("$scpArguments = @(")
    resume_cleanup = text.index("$cleanup = Invoke-RemotePayload")
    normal_cleanup = text.rindex("$cleanup = Invoke-RemotePayload")
    require(
        preflight < uploaded_diagnostic_exit < resume_exit < resume_cleanup < diagnostic_exit < upload < normal_cleanup,
        "preflight_before_write",
    )
    require(
        "mode=email_unknown_stage_nonce_mismatch_preflight_metadata preflight=true" in text
        and "retained=true ssh_attempts=1 scp_attempts=0 writes=false retries=0" in text,
        "readonly_preflight_mode",
    )
    require(
        "mode=email_unknown_uploaded_binary_preflight_metadata preflight=true" in text
        and "binary_regular=true binary_symlink=false binary_owner=true binary_mode=$binaryMode binary_hash_match=$binaryHashMatch" in text
        and "$remoteAction = if ($UploadedBinaryPreflightOnly -or $ResumeUploadedCleanup) { 'uploaded_preflight' } else { 'preflight' }" in text,
        "uploaded_readonly_preflight_mode",
    )
    require(
        "mode=email_unknown_resume_uploaded_binary_exact_cleanup preflight=true binary_hash_match=true cleanup=true retained=false ssh_attempts=2 scp_attempts=0 retries=0" in text
        and "$Matches.binary_hash_match -cne 'true'" in text,
        "resume_uploaded_cleanup_mode",
    )
    require(text.count("= Invoke-RemotePayload") == 3 and text.count("Invoke-FixedProcess -FilePath $scp") == 1, "fixed_transport_count")
    require(text.count("$sshAttempts++") == 3 and text.count("$scpAttempts++") == 1, "attempt_count")
    require("Remove-Item -Recurse" not in text and "retries=1" not in text, "no_retry_recursive")


def mutation_cases(payload: str, controller: str) -> int:
    attacks = (
        (validate_payload, payload.replace("${#stage_candidates[@]} -eq 1", "${#stage_candidates[@]} -ge 1", 1)),
        (validate_payload, payload.replace("${#stage_links[@]} -eq 0", "${#stage_links[@]} -ge 0", 1)),
        (validate_payload, payload.replace("exec 2>/dev/null", ":", 1)),
        (validate_payload, payload.replace('sha256sum -- "$old_binary"', 'printf %s "$old_binary_sha"', 1)),
        (validate_payload, payload.replace('sha256sum -- "$old_payload"', 'printf %s "$old_payload_sha"', 1)),
        (validate_payload, payload.replace("object_pairs_hook=strict_object", "", 1)),
        (validate_payload, payload.replace("os.O_NOFOLLOW", "0", 1)),
        (validate_payload, payload.replace("values[7] != 0", "values[7] < 0", 1)),
        (validate_payload, payload.replace("values[1] == expected_operation", "False", 1)),
        (validate_payload, payload.replace('"$state_nonce" != "$operation_id"', '"$state_nonce" == "$operation_id"', 1)),
        (validate_payload, payload.replace("fail fixture_ownership", "fail fixture_presence", 1)),
        (validate_payload, payload.replace('redis-cli -n "$REDIS_DB" --raw EXISTS', 'redis-cli -n "$REDIS_DB" --raw PING', 1)),
        (validate_payload, payload.replace("if ! current_run_id=$(redis_run_id); then fail redis_identity; fi", "current_run_id=$(redis_run_id)", 1)),
        (validate_payload, payload.replace('[[ "$binary_mode" =~ ^[0-7]{3}$ ]]', '[[ -n "$binary_mode" ]]', 1)),
        (
            validate_payload,
            payload.replace(
                '    assert_fixture_ownership\n    assert_redis_ownership\n    chmod 500 -- "$recovery_binary"',
                '    chmod 500 -- "$recovery_binary"\n    assert_fixture_ownership\n    assert_redis_ownership',
                1,
            ),
        ),
        (validate_payload, payload.replace("email_test_recipient_allowlist WHERE", "email_test_recipient_allowlists WHERE", 1)),
        (validate_payload, payload.replace("if ! counts=$(mysql_exec", "counts=$(mysql_exec", 1)),
        (validate_payload, payload.replace("mysql --no-defaults", "mysql", 1)),
        (validate_payload, payload.replace("EMAIL_ADAPTER=mock", "EMAIL_ADAPTER=directmail", 1)),
        (validate_payload, payload + "\ndocker restart molin-redis\n"),
        (validate_payload, payload + "\nredis-cli FLUSHDB\n"),
        (validate_payload, payload + "\nredis-cli --scan\n"),
        (validate_payload, payload.replace('rmdir -- "$stage"', 'rm -rf -- "$stage"', 1)),
        (validate_controller, controller.replace("if (-not $Execute)", "if ($false)", 1)),
        (validate_controller, controller.replace("I_CONFIRM_EMAIL_UNKNOWN_STAGE_NONCE_MISMATCH_PREFLIGHT_METADATA_ONCE", "WRONG_PREFLIGHT_CONFIRM", 1)),
        (validate_controller, controller.replace("I_CONFIRM_EMAIL_UNKNOWN_UPLOADED_BINARY_PREFLIGHT_METADATA_ONCE", "WRONG_UPLOADED_CONFIRM", 1)),
        (validate_controller, controller.replace("I_CONFIRM_EMAIL_UNKNOWN_RESUME_UPLOADED_BINARY_EXACT_CLEANUP_ONCE", "WRONG_RESUME_CONFIRM", 1)),
        (validate_controller, controller.replace("'uploaded_preflight'", "'preflight'", 1)),
        (validate_controller, controller.replace("$Matches.binary_hash_match -cne 'true'", "$false", 1)),
        (validate_controller, controller.replace("if ($PreflightOnly) {\n        Write-Output", "if ($false) {\n        Write-Output", 1)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no")),
        (validate_controller, controller.replace("1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900", "0" * 64, 1)),
        (validate_controller, controller.replace("$script:RecoveryBinarySize = 25573597", "$script:RecoveryBinarySize = 1", 1)),
        (validate_controller, controller.replace("$transportProbe -cne", "$transportProbe -ceq", 1)),
        (validate_controller, controller.replace("$errorOutput.Length -ne 0", "$false", 1)),
        (validate_controller, controller.replace("$process.ExitCode -ne 0", "$false", 1)),
        (validate_controller, controller.replace("'stderr_nonempty'", "'stderr_ignored'", 1)),
        (validate_controller, controller.replace("$stderrLength = [string]$_.Exception.Data['StderrLength']", "$stderrLength = 0", 1)),
        (validate_controller, controller.replace("'-q', '-O', '-P', '10003'", "'-q', '-P', '10003'", 1)),
        (validate_controller, controller.replace("retries=0", "retries=1")),
        (validate_controller, controller.replace("$process.ExitCode -eq 2 -and $output -cmatch $failurePattern", "$true", 1)),
    )
    rejected = 0
    for validator, attacked in attacks:
        try:
            validator(attacked)
        except (ContractFailure, ValueError):
            rejected += 1
        else:
            raise ContractFailure("mutation_accepted")
    return rejected


def find_bash() -> str | None:
    for candidate in (shutil.which("bash"), r"C:\Program Files\Git\bin\bash.exe"):
        if candidate and pathlib.Path(candidate).is_file():
            return candidate
    return None


def main() -> int:
    payload_bytes = PAYLOAD.read_bytes()
    controller_bytes = CONTROLLER.read_bytes()
    require(not payload_bytes.startswith(b"\xef\xbb\xbf"), "payload_bom")
    require(controller_bytes.startswith(b"\xef\xbb\xbf"), "controller_utf8_bom")
    payload = payload_bytes.decode("utf-8", errors="strict")
    controller = controller_bytes.decode("utf-8-sig", errors="strict")
    validate_payload(payload)
    validate_controller(controller)
    attacks = mutation_cases(payload, controller)
    bash = find_bash()
    syntax = "skipped"
    if bash:
        result = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
        require(result.returncode == 0 and not result.stderr, "bash_syntax")
        syntax = "pass"
    print(
        "status=pass mode=email_unknown_fresh_recovery_contract "
        f"attack_cases={attacks} bash_syntax={syntax} external_access=false writes=false cleanup=false"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_fresh_recovery_contract classification=closed")
        raise SystemExit(1)

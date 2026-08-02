"""验证 DirectMail Phase 4 全新 Redis 周期资产的失败关闭边界。"""

from __future__ import annotations

import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-fresh-cycle.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-fresh-cycle.ps1"


class ContractFailure(RuntimeError):
    """表示执行资产偏离冻结的安全契约。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n# DirectMail Phase 4"), "payload_header")
    require("\r" not in text and "\x00" not in text, "payload_encoding")
    require(text.count("set -Eeuo pipefail") == 1 and "exec 2>/dev/null" in text, "payload_strict_mode")
    require('^(preflight|phase1|restart|phase2|cleanup_verified|finalize)$' in text, "payload_actions")
    require(text.count('docker restart "$redis_id"') == 1, "redis_restart_exact_once")
    require("docker restart molin-redis" not in text, "redis_restart_identity_bypass")
    require("molin-redis" in text and "molin-mysql" in text, "container_identity")
    require("schema_migrations" in text and "'57:0'" in text, "schema_gate")
    require("EMAIL_ADAPTER=mock" in text and "APP_ENV=test" in text, "mock_adapter")
    require('export EMAIL_UNKNOWN_RESTART_NONCE="$nonce"' in text, "nonce_binding")
    require("RUN_EMAIL_UNKNOWN_RESTART_CLEANUP=1" in text, "cleanup_gate")
    require("phase2_verified" in text and "unexpected_send_log_id" in text, "verified_state")
    require("--no-defaults --batch" in text, "mysql_defaults_disabled")
    require(
        all(fragment in text for fragment in (
            'flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)',
            "info = os.fstat(fd)",
            "info.st_nlink != 1",
            'with os.fdopen(fd, "r", encoding="utf-8") as handle:',
        )),
        "state_nofollow",
    )
    require(
        all(fragment in text for fragment in (
            "email_send_logs WHERE id=${send_log_id}",
            "email_test_recipient_allowlist WHERE id=${allowlist_id}",
            "email_provider_templates WHERE id=${template_id}",
            "[[ \"$counts\" == $'0\\t0\\t0' ]]",
        )),
        "exact_cleanup_postcheck",
    )
    require("email_test_recipient_allowlists" not in text, "allowlist_table_name")
    require("status=failed stage=%s retained=true retries=0" in text, "failure_retains")
    require("mysqldump" not in text and "mysqladmin" not in text, "main_database_scope")
    require(re.search(r"\b(?:FLUSHDB|FLUSHALL|KEYS|SCAN)\b", text, re.IGNORECASE) is None, "redis_forbidden")
    require(re.search(r"redis-cli[^\n]*(?:DEL|UNLINK)\b", text, re.IGNORECASE) is None, "redis_delete")
    require(re.search(r"(?:rm|rmdir)\s+-[^\n]*\*", text) is None, "wildcard_file_delete")


def validate_controller(text: str) -> None:
    require("I_CONFIRM_DIRECTMAIL_PHASE4_FRESH_CYCLE_LEGACY_SCP_ONCE" in text, "confirmation_phrase")
    require("$script:UploadTransport = 'legacy_scp'" in text, "upload_transport")
    require("29eaa0b18959d9abccdcf10d3793aa6a0c8574b85028714ab7d6eb4e429def54" in text, "payload_hash")
    require("1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900" in text, "binary_hash")
    require("$script:BinarySize = 25573597" in text and "$script:OperatorID = '259'" in text, "binary_identity")
    confirmation = text.index("if (-not $Execute -or $Confirm -cne $script:RequiredPhrase)")
    transport = text.index("$ssh = Join-Path $env:WINDIR")
    require(confirmation < transport, "confirmation_before_transport")
    require("if ($SelfTest)" in text and "external_access=false" in text, "offline_selftest")
    require("hash_transport=pass" in text and "传输探针" in text, "transport_selftest")
    require('.Replace("`r`n", "`n").Replace("`r", "`n")' in text, "stdin_lf_normalization")
    require("[Text.UTF8Encoding]::new($false, $true).GetBytes($normalized)" in text, "stdin_no_bom")
    require("ArgumentList = $Arguments" in text and "[string[]]$Arguments" in text, "argument_array")
    require("$scpBase = @('-q', '-O', '-P', '10003'" in text and "'ConnectTimeout=10', '--')" in text and text.count("Invoke-FixedProcess -FilePath $scp") == 2, "scp_policy")
    require(text.count("$sshAttempts++") == 2 and text.count("$scpAttempts++") == 2, "attempt_budget")
    require(text.count("StrictHostKeyChecking=yes") == 2 and "StrictHostKeyChecking=no" not in text, "host_key_policy")
    require("for action in preflight phase1 restart phase2 cleanup_verified finalize; do" in text, "action_order")
    require("$cycleOutput -cnotmatch $script:CyclePattern" in text, "output_contract")
    require("operation_id=" not in text and "nonce=$nonce" not in text, "identifier_output")
    require("stdout_length=$stdoutLength" in text and "stderr_length=$stderrLength" in text, "failure_lengths")
    require("upload_transport=legacy_scp stage=$currentStage" in text, "failure_transport")
    require("ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0" in text, "failure_counts")
    require("adapter_delta_zero=true cleanup=true" in text, "adapter_delta_summary")
    require("upload_transport=legacy_scp preflight=true" in text, "success_transport")
    require("ssh_attempts=2 scp_attempts=2 retries=0 real_mail=false" in text, "success_counts")
    require('rm -f -- "$payload"' in text and 'rmdir -- "$stage"' in text, "exact_stage_cleanup")
    require("Remove-Item -Recurse" not in text and "rm -rf" not in text, "no_recursive_cleanup")
    require("throw 'fresh_cycle_failed'" in text, "sanitized_failure")


def mutation_cases(payload: str, controller: str) -> int:
    attacks = (
        (validate_payload, payload.replace("set -Eeuo pipefail", "set -eo pipefail", 1)),
        (validate_payload, payload.replace("exec 2>/dev/null", "true", 1)),
        (validate_payload, payload.replace('docker restart "$redis_id"', 'docker restart molin-redis\ndocker restart "$redis_id"', 1)),
        (validate_payload, payload.replace("EMAIL_ADAPTER=mock", "EMAIL_ADAPTER=directmail", 1)),
        (validate_payload, payload.replace('export EMAIL_UNKNOWN_RESTART_NONCE="$nonce"', "unset EMAIL_UNKNOWN_RESTART_NONCE", 1)),
        (validate_payload, payload.replace("'57:0'", "'58:0'", 1)),
        (validate_payload, payload.replace("--no-defaults --batch", "--batch", 1)),
        (validate_payload, payload.replace("O_NOFOLLOW", "O_CLOEXEC", 1)),
        (validate_payload, payload.replace("os.fstat(fd)", "os.lstat(path)", 1)),
        (validate_payload, payload.replace("info.st_nlink != 1", "False", 1)),
        (validate_payload, payload.replace("email_test_recipient_allowlist WHERE", "email_test_recipient_allowlists WHERE", 1)),
        (validate_payload, payload + "\nredis-cli FLUSHDB\n"),
        (validate_payload, payload + "\nredis-cli --scan\n"),
        (validate_payload, payload + "\nredis-cli DEL fixed-key\n"),
        (validate_payload, payload + "\nrm -f -- /tmp/*\n"),
        (validate_payload, payload.replace("retained=true retries=0", "retained=false retries=1", 1)),
        (validate_payload, payload.replace("phase2_verified", "phase1_created")),
        (validate_controller, controller.replace("-not $Execute -or ", "", 1)),
        (validate_controller, controller.replace("29eaa0b18959d9abccdcf10d3793aa6a0c8574b85028714ab7d6eb4e429def54", "0" * 64, 1)),
        (validate_controller, controller.replace("1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900", "0" * 64, 1)),
        (validate_controller, controller.replace("$script:BinarySize = 25573597", "$script:BinarySize = 1", 1)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1)),
        (validate_controller, controller.replace("$scpBase = @('-q', '-O',", "$scpBase = @('-q',", 1)),
        (validate_controller, controller.replace("'ConnectTimeout=10', '--')", "'ConnectTimeout=10')", 1)),
        (validate_controller, controller.replace("$script:UploadTransport = 'legacy_scp'", "$script:UploadTransport = 'sftp'", 1)),
        (validate_controller, controller.replace("[string[]]$Arguments", "[string]$Arguments", 1)),
        (validate_controller, controller.replace("stderr_length=$stderrLength", "stderr_length=unknown", 1)),
        (validate_controller, controller.replace("retries=0", "retries=1")),
        (validate_controller, controller.replace('rm -f -- "$payload"', 'rm -rf -- "$stage"', 1)),
        (validate_controller, controller.replace("for action in preflight phase1 restart phase2 cleanup_verified finalize; do", "for action in phase1 restart; do", 1)),
        (validate_controller, controller.replace("$sshAttempts++", "# removed", 1)),
        (validate_controller, controller.replace("$scpAttempts++", "# removed", 1)),
        (
            validate_controller,
            controller.replace(
                "status=pass mode=email_unknown_fresh_cycle upload_transport=legacy_scp preflight=true",
                "status=pass mode=email_unknown_fresh_cycle upload_transport=legacy_scp operation_id=$nonce preflight=true",
                1,
            ),
        ),
        (validate_controller, controller.replace("adapter_delta_zero=true", "adapter_delta_zero=false", 1)),
        (validate_controller, controller.replace("throw 'fresh_cycle_failed'", "throw $_", 1)),
        (validate_controller, controller.replace('.Replace("`r`n", "`n").Replace("`r", "`n")', "", 1)),
        (validate_controller, controller.replace("[Text.UTF8Encoding]::new($false, $true).GetBytes($normalized)", "[Text.Encoding]::Unicode.GetBytes($normalized)", 1)),
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
    require(controller_bytes.startswith(b"\xef\xbb\xbf"), "controller_bom")
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
    print(f"status=pass mode=email_unknown_fresh_cycle_contract attack_cases={attacks} bash_syntax={syntax} external_access=false")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_fresh_cycle_contract classification=closed")
        raise SystemExit(1)

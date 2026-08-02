"""验证 Redis unknown 保留现场只读诊断资产的单次执行边界。"""

from __future__ import annotations

import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-fresh-readonly-diagnostic.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-fresh-readonly-diagnostic.ps1"


class ContractFailure(RuntimeError):
    """表示诊断资产偏离本次纯只读授权。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n# DirectMail Phase 4"), "header")
    require(text.count("set -Eeuo pipefail") == 1 and "exec 2>/dev/null" in text, "closed_output")
    require("$stage_count -eq 1 && ${#stage_links[@]} -eq 0" in text, "unique_stage")
    require("pc:700" in text and "pc:${mode}" in text, "identity")
    require(text.count("sha256sum --") == 2, "hashes")
    require("object_pairs_hook=strict_object" in text and "unexpected_send_log_id" in text, "strict_state")
    require("stage_nonce_match" in text and "fixture_ownership" in text, "ownership")
    require("email_test_recipient_allowlist'" in text, "singular_table")
    require("email_test_recipient_allowlists'" in text, "plural_table_diagnostic")
    require(text.count("--execute=\"SELECT") == 1, "one_select")
    require(text.count('redis-cli -n "$REDIS_DB" --raw PING') == 1, "redis_ping")
    require(text.count('redis-cli -n "$REDIS_DB" --raw INFO server') == 1, "redis_info")
    require(text.count('redis-cli -n "$REDIS_DB" --raw EXISTS "$exact_key"') == 1, "redis_exists")
    require("writes=false cleanup=false restart=false retries=0" in text, "summary")
    forbidden = (
        "rm -", "unlink ", "docker restart", "docker stop", "docker kill", "docker rm",
        "DELETE ", "UPDATE ", "INSERT ", "REPLACE ", "ALTER ", "DROP ", "TRUNCATE ",
        "FLUSHDB", "FLUSHALL", "redis-cli KEYS", "redis-cli SCAN", "redis-cli DEL",
        "redis-cli UNLINK", "cleanup_phase1", "EMAIL_UNKNOWN_RESTART_PHASE=phase1",
        "SingleSendMail", "curl ", "scp ",
    )
    require(all(token not in text for token in forbidden), "forbidden")


def validate_controller(text: str) -> None:
    require("I_CONFIRM_EMAIL_UNKNOWN_RETAINED_STAGE_READONLY_DIAGNOSTIC_ONCE" in text, "confirm")
    require(
        "__PAYLOAD_SHA256__" not in text
        and len(re.findall(r"(?m)^\$script:PayloadSHA = '[a-f0-9]{64}'$", text)) == 1,
        "frozen_sha",
    )
    require(text.count("Invoke-OneSSH -Payload $payload") == 1, "one_ssh")
    require("StrictHostKeyChecking=yes" in text and "BatchMode=yes" in text, "ssh_policy")
    require("ssh_attempts = 1" in text and "retries = 0" in text, "no_retry")
    require("remote_artifact = $false" in text and "retained = $true" in text, "retained")
    require("cleanup = $false" in text and "restart = $false" in text, "readonly")
    require("$exitCodeValue = $null" in text and "$stdoutTextValue = $null" in text, "capture_guard")
    require("先读取原始属性再单独转换" in text, "capture_exit_code_comment")
    require("$result = $null" in text and "if ($null -eq $result)" in text, "result_guard")
    require("summary_regression" in text and "database_snapshot phase=phase1_created" in text, "summary_regression")
    require("function New-CaptureResult" in text and "System.Management.Automation.PSCustomObject" in text, "capture_factory")
    require(
        "$result = $null" in text
        and "if ($null -eq $result) { throw 'capture_factory_missing' }" in text,
        "capture_factory_guard",
    )
    require(text.count("return $result") == 1, "capture_factory_return")
    require("capture_shape_regression" in text and "New-CaptureResult -ExitCode" in text, "capture_regression")
    require("capture_exit_value_changed" in text and "return $captureResult" in text, "capture_return_guard")
    require("function Invoke-CapturedProcess" in text and text.count("Start-Process") == 1, "captured_process")
    require("ArgumentList = $Arguments" in text and "Start-Process @startParameters" in text, "argument_splat")
    require("captured_process_argument_unsafe" in text and "captured_process_stdin_invalid" in text, "input_guard")
    require("try { $rawExitCode = $process.ExitCode }" in text, "exit_code_read")
    require("process_exit_code_unavailable" in text and "process_exit_code_missing" in text, "exit_code_guard")
    require(not re.search(r"\[int\]\s*\$process\.ExitCode", text), "exit_code_precedence")
    require(not re.search(r"(?m)^\s*#.*\$(?:result|process|rawExitCode)\s*=", text), "comment_swallowed_statement")
    require(text.count("$process.WaitForExit()") >= 2, "exit_code_flush_wait")
    require(
        all(
            marker in text
            for marker in (
                "local_pipeline_exit_regression",
                "local_pipeline_stderr_regression",
                "local_pipeline_stdout_regression",
                "local_pipeline_summary_regression",
                "local_pipeline_cleanup_regression",
            )
        )
        and "-Arguments @('-s', '--')" in text,
        "pipeline_regression",
    )
    require("$fixtureScript = \"printf" in text and "cat >/dev/null" not in text, "pipeline_fixture_executes")
    require("[string[]]$Arguments" in text and "$arguments = @(" in text, "argument_array")
    require("$beforeTemps -ne $afterTemps" in text, "pipeline_cleanup_regression")
    require("New-Object PSObject" not in text and "Add-Member -InputObject $capture" not in text, "empty_psobject_forbidden")
    require("scp.exe" not in text and "Remove-Item" not in text, "no_upload_cleanup_branch")
    confirmation = text.index("if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase)")
    invocation = text.index("$result = Invoke-OneSSH -Payload $payload")
    require(confirmation < invocation, "gate_order")


def mutation_cases(payload: str, controller: str) -> int:
    attacks = (
        (validate_payload, payload.replace("$stage_count -eq 1", "$stage_count -ge 1", 1)),
        (validate_payload, payload.replace("${#stage_links[@]} -eq 0", "${#stage_links[@]} -ge 0", 1)),
        (validate_payload, payload.replace("pc:700", "pc:755", 1)),
        (validate_payload, payload.replace("object_pairs_hook=strict_object", "", 1)),
        (validate_payload, payload.replace("stage=fixture_ownership", "stage=fixture_presence", 1)),
        (validate_payload, payload.replace("email_test_recipient_allowlist'", "email_test_recipient_allowlists'", 1)),
        (validate_payload, payload.replace('redis-cli -n "$REDIS_DB" --raw EXISTS "$exact_key"', 'redis-cli -n "$REDIS_DB" --raw SCAN 0', 1)),
        (validate_payload, payload + "\nredis-cli FLUSHDB\n"),
        (validate_payload, payload + "\ndocker restart molin-redis\n"),
        (validate_payload, payload + "\nrm -f -- /tmp/file\n"),
        (validate_controller, controller.replace("-not $Execute -or ", "", 1)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1)),
        (validate_controller, controller.replace("Invoke-OneSSH -Payload $payload", "Invoke-OneSSH -Payload $payload\nInvoke-OneSSH -Payload $payload", 1)),
        (validate_controller, controller.replace("ssh_attempts = 1", "ssh_attempts = 2", 1)),
        (validate_controller, controller.replace("remote_artifact = $false", "remote_artifact = $true", 1)),
        (validate_controller, controller.replace("cleanup = $false", "cleanup = $true", 1)),
        (validate_controller, controller.replace("$exitCodeValue = $null", "$exitCodeValue = 0", 1)),
        (validate_controller, controller.replace("if ($null -eq $result)", "if ($false)", 1)),
        (validate_controller, controller.replace("return $result", "return $null", 1)),
        (validate_controller, controller.replace("capture_factory_missing", "capture_factory_ignored", 1)),
        (validate_controller, controller.replace("capture_shape_regression", "capture_shape_ignored", 1)),
        (validate_controller, controller.replace("local_pipeline_exit_regression", "local_pipeline_exit_ignored", 1)),
        (validate_controller, controller.replace("$beforeTemps -ne $afterTemps", "$false", 1)),
        (validate_controller, controller.replace("$rawExitCode = $process.ExitCode", "$rawExitCode = 0", 1)),
        (validate_controller, controller.replace("try { $rawExitCode = $process.ExitCode }", "try { # $rawExitCode = $process.ExitCode }", 1)),
        (validate_controller, controller.replace("PayloadSHA = '", "PayloadSHA = '0", 1)),
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
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    syntax = "skipped"
    if pathlib.Path(bash).is_file():
        result = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
        require(result.returncode == 0 and not result.stderr, "bash_syntax")
        syntax = "pass"
    print(
        "status=pass mode=email_unknown_fresh_readonly_diagnostic_contract "
        f"attack_cases={attacks} bash_syntax={syntax} external_access=false writes=false cleanup=false restart=false"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_fresh_readonly_diagnostic_contract classification=closed")
        raise SystemExit(1)

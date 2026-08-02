"""验证 fresh cycle 上传失败现场诊断资产保持纯只读和单次执行边界。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-upload-failure-readonly.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-upload-failure-readonly.ps1"


class ContractFailure(RuntimeError):
    """表示只读诊断资产偏离冻结安全契约。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n# DirectMail Phase 4"), "header")
    require(text.count("set -Eeuo pipefail") == 1 and "exec 2>/dev/null" in text, "closed_stderr")
    require("stage_count -eq 1" in text and text.count("pc:700") == 2, "unique_stage")
    require("stage_file_id" in text and "%U:%a:%d:%i" in text, "stable_stage_identity")
    require('[[ "$parent_writable" == true ]] || fail parent_not_writable' in text, "parent_writable_gate")
    require('[[ "$stage_writable" == true ]] || fail stage_not_writable' in text, "stage_writable_gate")
    require(text.count('/usr/bin/find "$stage" -mindepth 1 -maxdepth 1') == 2, "empty_recheck")
    require("entry_count -eq 0" in text and "entry_count -eq 1" in text, "stage_content_classes")
    require("${#final_entries[@]} -eq $entry_count" in text, "content_recheck")
    require("expected_binary_size=25573597" in text and "expected_binary_sha=" in text, "binary_identity")
    require("binary_size_class=partial" in text and "binary_hash_match=true" in text, "binary_classification")
    require("/usr/bin/df -Pk" in text and "/usr/bin/df -Pi" in text, "capacity_metadata")
    require("-x /usr/bin/scp" in text and "-x /bin/scp" in text, "remote_scp_identity")
    safe_summary = "writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0"
    require(text.count(safe_summary) == 2, "safe_summary")
    forbidden = (
        "rm -", "unlink ", "chmod ", "chown ", "touch ", "mkdir ", "scp -t", "docker ",
        "mysql ", "redis-cli ", "curl ", "wget ", "DELETE ", "UPDATE ", "INSERT ",
        "REPLACE ", "ALTER ", "DROP ", "TRUNCATE ", "FLUSHDB", "FLUSHALL", "KEYS ",
        "SCAN ", "SingleSendMail",
    )
    require(all(token not in text for token in forbidden), "forbidden_operation")


def validate_controller(text: str, payload_sha: str) -> None:
    require("I_CONFIRM_EMAIL_UNKNOWN_UPLOAD_FAILURE_WRITABILITY_READONLY_ONCE" in text, "confirmation")
    require(text.count("-not $Execute -or ") == 1, "confirmation_count")
    require(f"$script:PayloadSHA = '{payload_sha}'" in text, "frozen_payload_hash")
    formal_matches = list(re.finditer(r"(?m)^\$result = Invoke-OneSSH -Payload \$payload$", text))
    require(len(formal_matches) == 1, "one_ssh")
    require("StrictHostKeyChecking=yes" in text and "BatchMode=yes" in text, "ssh_policy")
    require("scp.exe" not in text and "Start-Process" in text, "no_scp_process")
    require("ssh_attempts = 1" in text and "scp_attempts = 0" in text and "retries = 0" in text, "fixed_counts")
    require("database_access = $false" in text and "redis_access = $false" in text, "no_data_services")
    require("cleanup = $false" in text and "restart = $false" in text and "retained = $true" in text, "retention")
    require("$Result.StderrBytes -eq 0" in text, "stderr_gate")
    require("formal_branch_ast_regression" in text and "Language.Parser]::ParseFile" in text, "formal_ast_guard")
    require("stdout_length" in text and "stderr_length" in text, "length_only_transport")
    require("parent_writable = $parentWritable" in text and "stage_writable = $stageWritable" in text, "writability_output")
    require("parent_not_writable|stage_count" in text and "stage_identity|stage_not_writable" in text, "writability_classification")
    require("parent_writable=(?<parent_writable>true) stage_writable=(?<stage_writable>true)" in text, "writability_success_gate")
    require("$Result.Stdout" in text and "ConvertTo-SafeSummary" in text, "structured_output")
    require("Write-Output (ConvertTo-SafeSummary -Result $result)" in text, "safe_output_only")
    require("GetString($stderrBytes)" in text and "Stderr =" not in text, "stderr_not_returned")
    confirmation = text.index("if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase)")
    invocation = formal_matches[0].start()
    require(confirmation < invocation, "gate_order")


def rejected(validator, attacked: str, *args: str) -> None:
    try:
        validator(attacked, *args)
    except (ContractFailure, ValueError):
        return
    raise ContractFailure("mutation_accepted")


def mutation_cases(payload: str, controller: str, payload_sha: str) -> int:
    attacks = (
        (validate_payload, payload.replace("stage_count -eq 1", "stage_count -ge 1", 1), ()),
        (validate_payload, payload.replace("pc:700", "pc:755", 1), ()),
        (validate_payload, payload.replace("%U:%a:%d:%i", "%U:%a", 1), ()),
        (validate_payload, payload.replace('[[ "$parent_writable" == true ]] || fail parent_not_writable', "true", 1), ()),
        (validate_payload, payload.replace('[[ "$stage_writable" == true ]] || fail stage_not_writable', "true", 1), ()),
        (validate_payload, payload.replace("${#final_entries[@]} -eq $entry_count", "${#final_entries[@]} -ge 0", 1), ()),
        (validate_payload, payload.replace("/usr/bin/df -Pk", "/usr/bin/true", 1), ()),
        (validate_payload, payload.replace("expected_binary_size=25573597", "expected_binary_size=1", 1), ()),
        (validate_payload, payload.replace("expected_binary_sha=", "ignored_binary_sha=", 1), ()),
        (validate_payload, payload + "\nrm -f -- /tmp/value\n", ()),
        (validate_payload, payload + "\nredis-cli SCAN 0\n", ()),
        (validate_payload, payload + "\ndocker restart molin-redis\n", ()),
        (validate_payload, payload.replace("database_access=false", "database_access=true", 1), ()),
        (validate_controller, controller.replace("-not $Execute -or ", "", 1), (payload_sha,)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1), (payload_sha,)),
        (validate_controller, controller + "\n$result = Invoke-OneSSH -Payload $payload\n", (payload_sha,)),
        (validate_controller, controller.replace("scp_attempts = 0", "scp_attempts = 1", 1), (payload_sha,)),
        (validate_controller, controller.replace("database_access = $false", "database_access = $true", 1), (payload_sha,)),
        (validate_controller, controller.replace("cleanup = $false", "cleanup = $true", 1), (payload_sha,)),
        (validate_controller, controller.replace("$Result.StderrBytes -eq 0", "$Result.StderrBytes -ge 0", 1), (payload_sha,)),
        (validate_controller, controller.replace("parent_writable = $parentWritable", "parent_writable = $true", 1), (payload_sha,)),
        (validate_controller, controller.replace("stage_writable = $stageWritable", "stage_writable = $true", 1), (payload_sha,)),
        (validate_controller, controller.replace("formal_branch_ast_regression", "formal_branch_ast_ignored", 1), (payload_sha,)),
        (validate_controller, controller.replace(payload_sha, "0" * 64, 1), (payload_sha,)),
        (validate_controller, controller + "\n& scp.exe source destination\n", (payload_sha,)),
    )
    for validator, attacked, args in attacks:
        rejected(validator, attacked, *args)
    return len(attacks)


def main() -> int:
    payload_bytes = PAYLOAD.read_bytes()
    controller_bytes = CONTROLLER.read_bytes()
    require(not payload_bytes.startswith(b"\xef\xbb\xbf") and b"\x00" not in payload_bytes, "payload_encoding")
    require(controller_bytes.startswith(b"\xef\xbb\xbf") and b"\x00" not in controller_bytes, "controller_encoding")
    payload = payload_bytes.decode("utf-8", errors="strict")
    controller = controller_bytes.decode("utf-8-sig", errors="strict")
    payload_sha = hashlib.sha256(payload_bytes).hexdigest()
    validate_payload(payload)
    validate_controller(controller, payload_sha)
    attacks = mutation_cases(payload, controller, payload_sha)
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    syntax = "skipped"
    if pathlib.Path(bash).is_file():
        result = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
        require(result.returncode == 0 and not result.stderr, "bash_syntax")
        syntax = "pass"
    print(
        "status=pass mode=email_unknown_upload_failure_readonly_contract "
        f"attack_cases={attacks} bash_syntax={syntax} external_access=false writes=false "
        "database_access=false redis_access=false cleanup=false restart=false scp=false retries=0"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_upload_failure_readonly_contract classification=closed")
        raise SystemExit(1)

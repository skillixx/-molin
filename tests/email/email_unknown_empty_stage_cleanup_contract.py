"""验证上传失败空 Stage 清理资产只删除唯一、稳定且为空的精确目录。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-empty-stage-cleanup.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-empty-stage-cleanup.ps1"


class ContractFailure(RuntimeError):
    """表示清理资产偏离冻结边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def validate_payload(text: str) -> None:
    require(text.startswith("#!/usr/bin/env bash\n# DirectMail Phase 4"), "header")
    require(text.count("set -Eeuo pipefail") == 1 and "exec 2>/dev/null" in text, "closed_stderr")
    require("stage_count -eq 1" in text and text.count("pc:700") == 2, "unique_stage")
    require("stage_file_id" in text and "%U:%a:%d:%i" in text, "stable_inode")
    require(text.count('/usr/bin/find "$stage" -mindepth 1 -maxdepth 1') == 2, "empty_checks")
    require("entry_count -eq 0" in text and "${#final_entries[@]} -eq 0" in text, "must_be_empty")
    require(text.count('/usr/bin/rmdir -- "$stage"') == 1, "one_exact_rmdir")
    require("! -e \"$stage\" && ! -L \"$stage\"" in text, "removal_postcheck")
    require(text.count("stage_removed=true") == 2, "removal_state")
    require("writes=true database_access=false redis_access=false restart=false scp=false retries=0" in text, "success_summary")
    forbidden = (
        "rm -", "unlink ", "chmod ", "chown ", "touch ", "mkdir ", "docker ", "mysql ",
        "redis-cli ", "curl ", "wget ", "scp ", "DELETE ", "UPDATE ", "INSERT ",
        "REPLACE ", "ALTER ", "DROP ", "TRUNCATE ", "FLUSHDB", "FLUSHALL", "KEYS ",
        "SCAN ", "SingleSendMail",
    )
    require(all(token not in text for token in forbidden), "forbidden_operation")


def validate_controller(text: str, payload_sha: str) -> None:
    require("I_CONFIRM_EMAIL_UNKNOWN_EMPTY_STAGE_CLEANUP_ONCE" in text, "confirmation")
    require(f"$script:PayloadSHA = '{payload_sha}'" in text, "payload_hash")
    formal = list(re.finditer(r"(?m)^\$result = Invoke-OneSSH -Payload \$payload$", text))
    require(len(formal) == 1, "one_ssh")
    require("StrictHostKeyChecking=yes" in text and "BatchMode=yes" in text, "ssh_policy")
    require("scp.exe" not in text and "scp_attempts=0" in text, "no_scp")
    require("database_access=$false;redis_access=$false" in text, "no_data_services")
    require("restart=$false;retries=0" in text, "no_restart_retry")
    require("$Result.StderrBytes -eq 0" in text and "Stderr =" not in text, "stderr_gate")
    require("formal_branch_ast_regression" in text and "Language.Parser]::ParseFile" in text, "ast_guard")
    require("Write-Output (ConvertTo-SafeSummary -Result $result)" in text, "safe_output")
    confirmation = text.index("if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase)")
    require(confirmation < formal[0].start(), "gate_order")


def rejected(validator, attacked: str, *args: str) -> None:
    try:
        validator(attacked, *args)
    except (ContractFailure, ValueError):
        return
    raise ContractFailure("mutation_accepted")


def mutations(payload: str, controller: str, payload_sha: str) -> int:
    attacks = (
        (validate_payload, payload.replace("stage_count -eq 1", "stage_count -ge 1", 1), ()),
        (validate_payload, payload.replace("pc:700", "pc:755", 1), ()),
        (validate_payload, payload.replace("%U:%a:%d:%i", "%U:%a", 1), ()),
        (validate_payload, payload.replace("entry_count -eq 0", "entry_count -ge 0", 1), ()),
        (validate_payload, payload.replace("${#final_entries[@]} -eq 0", "${#final_entries[@]} -ge 0", 1), ()),
        (validate_payload, payload.replace('/usr/bin/rmdir -- "$stage"', '/usr/bin/rmdir -- "$parent"', 1), ()),
        (validate_payload, payload.replace("stage_removed=true", "stage_removed=false", 1), ()),
        (validate_payload, payload + "\nrm -rf -- /tmp/value\n", ()),
        (validate_payload, payload + "\nredis-cli FLUSHDB\n", ()),
        (validate_payload, payload + "\ndocker restart molin-redis\n", ()),
        (validate_controller, controller.replace("-not $Execute -or ", "", 1), (payload_sha,)),
        (validate_controller, controller.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1), (payload_sha,)),
        (validate_controller, controller + "\n$result = Invoke-OneSSH -Payload $payload\n", (payload_sha,)),
        (validate_controller, controller.replace("scp_attempts=0", "scp_attempts=1", 1), (payload_sha,)),
        (validate_controller, controller.replace("database_access=$false", "database_access=$true", 1), (payload_sha,)),
        (validate_controller, controller.replace("restart=$false;retries=0", "restart=$true;retries=1", 1), (payload_sha,)),
        (validate_controller, controller.replace("$Result.StderrBytes -eq 0", "$Result.StderrBytes -ge 0", 1), (payload_sha,)),
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
    attack_count = mutations(payload, controller, payload_sha)
    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    syntax = "skipped"
    if pathlib.Path(bash).is_file():
        result = subprocess.run([bash, "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
        require(result.returncode == 0 and not result.stderr, "bash_syntax")
        syntax = "pass"
    print(
        "status=pass mode=email_unknown_empty_stage_cleanup_contract "
        f"attack_cases={attack_count} bash_syntax={syntax} external_access=false writes=false "
        "database_access=false redis_access=false restart=false scp=false retries=0"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_empty_stage_cleanup_contract classification=closed")
        raise SystemExit(1)

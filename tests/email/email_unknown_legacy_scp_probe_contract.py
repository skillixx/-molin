"""验证 legacy scp 小文件探针保持精确、单次和失败关闭。"""

from __future__ import annotations

import pathlib
import re
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
CONTROLLER = ROOT / "scripts" / "run-email-unknown-legacy-scp-probe.ps1"


class ContractFailure(RuntimeError):
    pass


def require(value: bool, name: str) -> None:
    if not value:
        raise ContractFailure(name)


def validate(text: str) -> None:
    require("I_CONFIRM_EMAIL_UNKNOWN_LEGACY_SCP_PROBE_ONCE" in text, "confirmation")
    require("$script:ProbeSize = 44" in text and "c365cddf1551b4392727480d283fa07a7e1cd944e6ecde64fdf1b87fcca8af69" in text, "probe_identity")
    require("'-q', '-O', '-P', '10003'" in text and text.count("Invoke-CapturedProcess -FilePath $scp") == 1, "legacy_scp_once")
    require(text.count("$sshAttempts++") == 2 and text.count("$scpAttempts++") == 1, "attempt_budget")
    require("parent_writable=true stage_writable=true stage_empty=true" in text, "writable_preflight")
    require('rm -f -- "$probe"' in text and "stage_retained=true" in text, "exact_probe_cleanup")
    require("rm -rf" not in text and "rmdir " not in text and "Remove-Item -Recurse" not in text, "no_stage_cleanup")
    require(all(token not in text for token in ("docker ", "mysql ", "redis-cli ", "FLUSHDB", "FLUSHALL", "KEYS ", "SCAN ", "SingleSendMail")), "no_services")
    require("StrictHostKeyChecking=yes" in text and "StrictHostKeyChecking=no" not in text, "host_key")
    require("$handle = $process.Handle" in text and "process_handle_invalid" in text, "process_handle")
    require("$process.WaitForExit()" in text and "$process.Refresh()" in text and "process_exit_code_regression" in text, "exit_code_refresh")
    require("[AllowEmptyCollection()][byte[]]$InputBytes" in text and "empty_input_process_regression" in text, "empty_input_binding")
    require("retries=0 database_access=false redis_access=false restart=false" in text, "safe_summary")
    require("throw 'legacy_scp_probe_failed'" in text, "sanitized_failure")
    require("if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase)" in text, "default_closed")
    require("operation_id=" not in text and "nonce=$nonce" not in text, "identifier_output")


def attacks(text: str) -> int:
    cases = (
        text.replace("-not $Execute -or ", "", 1),
        text.replace("'-q', '-O', '-P', '10003'", "'-q', '-P', '10003'", 1),
        text.replace("$script:ProbeSize = 44", "$script:ProbeSize = 1", 1),
        text.replace("c365cddf1551b4392727480d283fa07a7e1cd944e6ecde64fdf1b87fcca8af69", "0" * 64, 1),
        text.replace("$sshAttempts++", "# removed", 1),
        text.replace("$scpAttempts++", "# removed", 1),
        text.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1),
        text.replace("$handle = $process.Handle", "$handle = [IntPtr]::Zero", 1),
        text.replace("$process.Refresh()", "# refresh removed", 1),
        text.replace("process_exit_code_regression", "process_exit_code_ignored", 1),
        text.replace("[AllowEmptyCollection()][byte[]]$InputBytes", "[byte[]]$InputBytes", 1),
        text.replace("empty_input_process_regression", "empty_input_process_ignored", 1),
        text.replace('rm -f -- "$probe"', 'rm -rf -- "$stage"', 1),
        text.replace("stage_retained=true", "stage_retained=false"),
        text.replace("retries=0", "retries=1"),
        text.replace("database_access=false", "database_access=true"),
        text.replace("throw 'legacy_scp_probe_failed'", "throw $_", 1),
        text + "\n& redis-cli FLUSHDB\n",
        text + "\nInvoke-CapturedProcess -FilePath $scp -Arguments @() -InputBytes @() -TimeoutMilliseconds 1\n",
    )
    rejected = 0
    for candidate in cases:
        try:
            validate(candidate)
        except ContractFailure:
            rejected += 1
        else:
            raise ContractFailure("mutation_accepted")
    return rejected


def main() -> int:
    raw = CONTROLLER.read_bytes()
    require(raw.startswith(b"\xef\xbb\xbf") and b"\x00" not in raw, "encoding")
    text = raw.decode("utf-8-sig")
    validate(text)
    rejected = attacks(text)
    result = subprocess.run(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(CONTROLLER), "-SelfTest"],
        capture_output=True,
        text=True,
        timeout=30,
    )
    require(result.returncode == 0 and "status=pass" in result.stdout and not result.stderr, "selftest")
    print(f"status=pass mode=email_unknown_legacy_scp_probe_contract attack_cases={rejected} external_access=false writes=false retries=0")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_unknown_legacy_scp_probe_contract classification=closed")
        raise SystemExit(1)

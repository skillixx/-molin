"""验证手工迁移包远端执行入口的单次调用和固定资产边界。"""

from __future__ import annotations

import hashlib
import pathlib
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "run-email-migration-manual-remote.ps1"
WRAPPER = ROOT / "scripts" / "email-migration-manual-execute.payload.sh"
PAYLOAD = ROOT / "scripts" / "email-migration-matrix-remote.payload.sh"
GENERATOR = ROOT / "scripts" / "generate-email-migration-baselines.sh"


class ContractFailure(RuntimeError):
    """表示手工入口偏离冻结契约。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def validate(text: str) -> None:
    require("I_CONFIRM_EMAIL_MIGRATION_MANUAL_REMOTE_ONCE" in text, "confirmation")
    require("if(-not$Execute-or$Confirm-cne$confirmPhrase)" in text, "execute_gate")
    require(all(sha(path) in text for path in (WRAPPER, PAYLOAD, GENERATOR)), "asset_hashes")
    require("Management.Automation.Language.Parser" in text and "controller_ssh_budget" in text, "ast_gate")
    require("Get-WrapperText" in text and "wrapper_encoding" in text, "wrapper_encoding")
    require("Invoke-FixedSsh -Ssh $ssh" in text and text.count("Invoke-FixedSsh -Ssh $ssh") == 1, "single_ssh")
    require("Start-Process -FilePath $Ssh -ArgumentList $Arguments" in text, "argument_array")
    require("$process.ExitCode-ne 0" in text and "exit $LASTEXITCODE" not in text, "fixed_exit_code")
    require("RedirectStandardInput" in text and "RedirectStandardOutput" in text and "RedirectStandardError" in text, "fixed_capture")
    require("status=pass stage=remote_stage_removed" in text and "throw 'remote_output'" in text, "output_gate")
    require("'7200s'" in text and "WaitForExit(7300000)" in text, "timeout")
    require("docker " not in text and "mysql " not in text and "scp.exe" not in text, "scope")


def mutations(text: str) -> tuple[str, ...]:
    return (
        text.replace("if(-not$Execute-or$Confirm-cne$confirmPhrase)", "if($true)", 1),
        text.replace(sha(WRAPPER), "0" * 64, 1),
        text.replace("Management.Automation.Language.Parser", "Management.Automation.Language.Token", 1),
        text.replace("Invoke-FixedSsh -Ssh $ssh", "Invoke-FixedSsh -Ssh $ssh\nInvoke-FixedSsh -Ssh $ssh", 1),
        text.replace("Start-Process -FilePath $Ssh -ArgumentList $Arguments", "Start-Process -FilePath $Ssh -ArgumentList ($Arguments -join ' ')", 1),
        text.replace("$process.ExitCode-ne 0", "$LASTEXITCODE-ne 0", 1),
        text.replace("RedirectStandardError $stderr", "", 1),
        text.replace("status=pass stage=remote_stage_removed", "", 1),
        text + "\nexit $LASTEXITCODE\n",
    )


def main() -> int:
    raw = SCRIPT.read_bytes()
    require(raw.startswith(b"\xef\xbb\xbf"), "controller_bom")
    text = raw.decode("utf-8-sig")
    validate(text)
    rejected = 0
    for candidate in mutations(text):
        try:
            validate(candidate)
        except ContractFailure:
            rejected += 1
        else:
            raise ContractFailure("mutation_accepted")
    result = subprocess.run(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SCRIPT), "-SelfTest"],
        capture_output=True,
        text=True,
        timeout=60,
    )
    require(result.returncode == 0 and "status=pass" in result.stdout and not result.stderr, "selftest")
    print(
        "status=pass mode=email_migration_manual_remote_contract "
        f"attack_cases={rejected} external_access=false docker_access=false database_access=false migration_executed=false"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_migration_manual_remote_contract classification=closed")
        raise SystemExit(1)

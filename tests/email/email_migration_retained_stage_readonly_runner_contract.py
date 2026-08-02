#!/usr/bin/env python3
"""验证保留 Stage 只读 runner 能在失败时保全白名单分类。"""

from __future__ import annotations

import hashlib
import pathlib
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-retained-stage-readonly.payload.sh"
RUNNER = ROOT / "scripts" / "run-email-migration-retained-stage-readonly.ps1"


class ContractError(RuntimeError):
    """表示 runner 偏离单次只读边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate(text: str) -> None:
    payload_sha = hashlib.sha256(PAYLOAD.read_bytes()).hexdigest().upper()
    required = (
        payload_sha, "I_CONFIRM_EMAIL_MIGRATION_RETAINED_STAGE_READONLY_ONCE",
        "Management.Automation.Language.Parser", "controller_single_execution",
        "Invoke-FixedSsh -Ssh $ssh", "$process.ExitCode", "RedirectStandardInput",
        "Write-Output $result.Output.TrimEnd", "classification=[a-z0-9_]+",
        "(?: [a-z0-9_]+=[a-z0-9_]+)*",
        "StrictHostKeyChecking=yes", "NumberOfPasswordPrompts=0", "Get-PayloadText",
    )
    for item in required:
        require(item in text, f"missing:{item}")
    require(text.count("Invoke-FixedSsh -Ssh $ssh") == 1, "ssh_count")
    require("exit $LASTEXITCODE" not in text and "scp.exe" not in text.lower() and "sftp.exe" not in text.lower(), "forbidden")


def main() -> int:
    raw = RUNNER.read_bytes()
    require(raw.startswith(b"\xef\xbb\xbf"), "runner_bom")
    text = raw.decode("utf-8-sig")
    validate(text)
    rejected = 0
    for candidate in (
        text.replace(hashlib.sha256(PAYLOAD.read_bytes()).hexdigest().upper(), "0" * 64, 1),
        text.replace("Management.Automation.Language.Parser", "Management.Automation.Language.Token", 1),
        text.replace("Invoke-FixedSsh -Ssh $ssh", "Invoke-FixedSsh -Ssh $ssh\nInvoke-FixedSsh -Ssh $ssh", 1),
        text.replace("$process.ExitCode", "$LASTEXITCODE", 1),
        text.replace("Write-Output $result.Output.TrimEnd", "$null = $result.Output.TrimEnd", 1),
        text.replace("classification=[a-z0-9_]+", "classification=.+", 1),
        text.replace("(?: [a-z0-9_]+=[a-z0-9_]+)*", "(?: [a-z_]+=[a-z0-9_]+)*", 1),
        text + "\nscp.exe file host:path\n",
        text + "\nexit $LASTEXITCODE\n",
    ):
        try:
            validate(candidate)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("mutation_accepted")
    result = subprocess.run(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(RUNNER), "-SelfTest"],
        capture_output=True,
        text=True,
        timeout=60,
    )
    require(result.returncode == 0 and "status=pass" in result.stdout and not result.stderr, "selftest")
    print(
        "status=pass mode=email_migration_retained_stage_readonly_runner_contract "
        f"attack_cases={rejected} external_access=false writes=false database_access=false docker_access=false retries=0"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractError, OSError, UnicodeError, subprocess.SubprocessError):
        print("status=failed mode=email_migration_retained_stage_readonly_runner_contract classification=closed")
        raise SystemExit(1)

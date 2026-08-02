#!/usr/bin/env python3
"""验证仅清理基线成功且矩阵尚未启动的精确临时 Stage。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-pre-matrix-failure-cleanup.payload.sh"
WRAPPER = ROOT / "scripts" / "run-email-migration-pre-matrix-failure-cleanup.ps1"


class ContractError(RuntimeError):
    """表示清理器偏离精确失败现场。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate_payload(text: str) -> None:
    required = (
        '[[ $# -eq 3 && $1 = --execute',
        '[[ $# -eq 2 && $1 = --execute && $2 = --unique ]]',
        "[[ ${#retained_stages[@]} -eq 1 ]]",
        "-regextype posix-extended -mindepth 1 -maxdepth 1 -type d",
        'readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"',
        '"$actual_archive_sha" = "$archive_sha"',
        '"$sha256sum_bin" --check --strict --status source-manifest.sha256',
        "assets baselines output package.tar.gz source source-manifest.sha256",
        "000055-baseline-manifest.tsv 000056-baseline-manifest.tsv schema54-empty.sql schema54-legacy.sql schema55.sql schema56.sql",
        "baseline.stderr baseline.stdout matrix-container.stderr",
        "BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319",
        'name=^/molin-email-matrix-${nonce}$',
        'label=molin.phase4.matrix=${nonce}',
        '"$rm_bin" -rf --one-file-system -- "$stage"',
        "baseline_outputs=6 matrix_outputs=0 removed_count=1 database_access=false retries=0",
    )
    for item in required:
        require(item in text, f"missing:{item}")
    require(text.count("-assets:") == 4, "asset_count")
    forbidden = (
        '"$docker_bin" run', '"$docker_bin" rm', '"$docker_bin" exec', '"$docker_bin" cp',
        "docker pull", "/usr/bin/mysql", "redis-cli", "FLUSHDB", "FLUSHALL", "KEYS", "SCAN",
        '"$stage"*', "/home/pc/molin-runtime/*",
    )
    for item in forbidden:
        require(item.lower() not in text.lower(), f"forbidden:{item}")
    require(re.search(r"(?m)^\s*(?:command\s+)?mysql\s", text) is None, "mysql_command")


def validate_wrapper(text: str, sha: str) -> None:
    for item in (
        sha, "I_CONFIRM_EMAIL_MIGRATION_PRE_MATRIX_FAILURE_CLEANUP_ONCE", "StrictHostKeyChecking=yes",
        "NumberOfPasswordPrompts=0", "Management.Automation.Language.Parser", "controller_single_execution",
        "[switch]$Unique", "$executeTail = @('--execute', '--unique')", "Invoke-FixedSsh -Ssh $ssh",
        "$process.ExitCode", "RedirectStandardInput", "Get-PayloadText",
    ):
        require(item in text, f"wrapper_missing:{item}")
    require("scp.exe" not in text.lower() and "sftp.exe" not in text.lower(), "transfer_forbidden")
    require(text.count("Invoke-FixedSsh -Ssh $ssh") == 1 and "exit $LASTEXITCODE" not in text, "single_fixed_ssh")


def main() -> int:
    payload_raw = PAYLOAD.read_bytes()
    wrapper_raw = WRAPPER.read_bytes()
    require(payload_raw and wrapper_raw and b"\r" not in payload_raw, "encoding")
    payload = payload_raw.decode("utf-8")
    wrapper = wrapper_raw.decode("utf-8-sig")
    sha = hashlib.sha256(payload_raw).hexdigest().upper()
    validate_payload(payload)
    validate_wrapper(wrapper, sha)

    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(bash).is_file(), "bash_missing")
    syntax = subprocess.run([bash, "--noprofile", "--norc", "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
    require(syntax.returncode == 0 and syntax.stderr == "", "bash_syntax")

    rejected = 0
    for candidate in (
        payload.replace('email-migration-matrix-${nonce}', '*', 1),
        payload.replace("[[ ${#retained_stages[@]} -eq 1 ]]", "[[ ${#retained_stages[@]} -ge 1 ]]", 1),
        payload.replace('"$actual_archive_sha" = "$archive_sha"', '-n "$actual_archive_sha"', 1),
        payload.replace("--check --strict --status", "--check", 1),
        payload.replace("matrix_outputs=0", "matrix_outputs=1", 1),
        payload.replace('"$rm_bin" -rf --one-file-system -- "$stage"', '"$rm_bin" -rf /home/pc/molin-runtime/*', 1),
        payload + "\n\"$docker_bin\" exec container mysql\n",
    ):
        try:
            validate_payload(candidate)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("payload_attack_accepted")
    for candidate in (
        wrapper.replace(sha, "0" * 64, 1),
        wrapper.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1),
        wrapper + "\nscp.exe file host:path\n",
        wrapper.replace("Management.Automation.Language.Parser", "Management.Automation.Language.Token", 1),
        wrapper.replace("Invoke-FixedSsh -Ssh $ssh", "Invoke-FixedSsh -Ssh $ssh\nInvoke-FixedSsh -Ssh $ssh", 1),
        wrapper.replace("$process.ExitCode", "$LASTEXITCODE", 1),
        wrapper + "\nexit $LASTEXITCODE\n",
    ):
        try:
            validate_wrapper(candidate, sha)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("wrapper_attack_accepted")

    print(
        "status=pass mode=email_migration_pre_matrix_failure_cleanup_contract "
        f"attack_cases={rejected} external_access=false database_access=false docker_writes=false retries=0"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

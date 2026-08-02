#!/usr/bin/env python3
"""验证迁移 Stage 形状诊断始终只读、精确且默认关闭。"""

from __future__ import annotations

import hashlib
import pathlib
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-stage-shape-readonly.payload.sh"
WRAPPER = ROOT / "scripts" / "run-email-migration-stage-shape-readonly.ps1"


class ContractError(RuntimeError):
    """表示只读诊断偏离安全边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate_payload(text: str) -> None:
    required = (
        "set -Eeuo pipefail",
        "exec 2>/dev/null",
        '[[ $# -eq 2 && $1 =~ ^[a-f0-9]{32}$ && $2 =~ ^[a-f0-9]{64}$ ]]',
        'readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"',
        '"$actual_archive_sha" = "$archive_sha"',
        "--check --strict --status source-manifest.sha256",
        'readonly symlinks=$($find_bin "$stage" -type l',
        "classification=package_only",
        "classification=source_ready",
        "classification=baseline_stage",
        "classification=matrix_stage",
        "writes=false database_access=false docker_access=false retries=0",
    )
    for item in required:
        require(item in text, f"payload_missing:{item}")
    forbidden = (
        "/usr/bin/docker", "docker ", "/usr/bin/mysql", "mysql ", "redis-cli", "rm -", "rmdir", "chmod", "chown",
        "scp.exe", "sftp.exe", "KEYS", "SCAN", "FLUSHDB", "FLUSHALL", "cat ", "head ", "tail ",
    )
    for item in forbidden:
        require(item.lower() not in text.lower(), f"payload_forbidden:{item}")


def validate_wrapper(text: str, payload_sha: str) -> None:
    required = (
        payload_sha,
        "I_CONFIRM_EMAIL_MIGRATION_STAGE_SHAPE_READONLY_ONCE",
        "StrictHostKeyChecking=yes",
        "NumberOfPasswordPrompts=0",
        "Get-Content -Raw -Encoding UTF8",
        "exit $LASTEXITCODE",
    )
    for item in required:
        require(item in text, f"wrapper_missing:{item}")
    require("scp.exe" not in text.lower() and "sftp.exe" not in text.lower(), "wrapper_transfer")


def main() -> int:
    payload_raw = PAYLOAD.read_bytes()
    wrapper_raw = WRAPPER.read_bytes()
    require(payload_raw and wrapper_raw and b"\r" not in payload_raw, "encoding")
    payload = payload_raw.decode("utf-8")
    wrapper = wrapper_raw.decode("utf-8-sig")
    payload_sha = hashlib.sha256(payload_raw).hexdigest().upper()
    validate_payload(payload)
    validate_wrapper(wrapper, payload_sha)

    bash = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"
    require(pathlib.Path(bash).is_file(), "bash_missing")
    syntax = subprocess.run([bash, "--noprofile", "--norc", "-n", str(PAYLOAD)], capture_output=True, text=True, timeout=10)
    require(syntax.returncode == 0 and syntax.stderr == "", "bash_syntax")

    payload_attacks = (
        payload.replace('email-migration-matrix-${nonce}', '*', 1),
        payload.replace('"$actual_archive_sha" = "$archive_sha"', '-n "$actual_archive_sha"', 1),
        payload.replace("--check --strict --status", "--check", 1),
        payload + "\nrm -rf /home/pc/molin-runtime/*\n",
        payload + "\ndocker ps\n",
    )
    rejected = 0
    for candidate in payload_attacks:
        try:
            validate_payload(candidate)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("payload_attack_accepted")

    wrapper_attacks = (
        wrapper.replace(payload_sha, "0" * 64, 1),
        wrapper.replace("StrictHostKeyChecking=yes", "StrictHostKeyChecking=no", 1),
        wrapper + "\nscp.exe file host:path\n",
    )
    for candidate in wrapper_attacks:
        try:
            validate_wrapper(candidate, payload_sha)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("wrapper_attack_accepted")

    print(
        "status=pass mode=email_migration_stage_shape_readonly_contract "
        f"attack_cases={rejected} external_access=false writes=false database_access=false docker_access=false retries=0"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

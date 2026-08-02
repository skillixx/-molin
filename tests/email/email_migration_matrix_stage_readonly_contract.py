#!/usr/bin/env python3
"""验证矩阵保留 Stage 诊断仅输出固定文件状态与哈希分类。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import shutil
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-migration-matrix-stage-readonly.payload.sh"
WRAPPER = ROOT / "scripts" / "run-email-migration-matrix-stage-readonly.ps1"


class ContractError(RuntimeError):
    """表示矩阵只读诊断越过固定边界。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractError(classification)


def validate_payload(text: str) -> None:
    required = (
        '[[ $# -eq 2 && $1 =~ ^[a-f0-9]{32}$ && $2 =~ ^[a-f0-9]{64}$ ]]',
        'readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"',
        '"$actual_archive_sha" = "$archive_sha"',
        "BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319",
        "2B351B710CBBEA5FD24E7FE0739F0107866ABD30225EC7F8653BDB45139AD3E1",
        "A0EA13852C7C77EBD978F1192EF23DF253287F63EFB46232E75E2929399E2B45",
        "91BFDA21D0A13FFFB1B7F01586D7C5751BA05444699E666707388506B4B7A6A3",
        "6A52CB921A53B4E27DEF000AAEB23C850AFEF65A149DE0E0B34C05A86BD62E9F",
        "|| fail state_probe",
        "|| fail state_shape",
        "writes=false database_access=false docker_access=false retries=0",
    )
    for item in required:
        require(item in text, f"missing:{item}")
    require(text.count("|| fail state_probe") == 11, "state_probe_count")
    require(text.count("|| fail state_shape") == 1, "state_shape_count")
    for item in ("/usr/bin/docker", "/usr/bin/mysql", "redis-cli", "rm -", "rmdir", "chmod", "chown", "cat ", "head ", "tail "):
        require(item.lower() not in text.lower(), f"forbidden:{item}")
    require(re.search(r"(?m)^\s*(?:command\s+)?(?:docker|mysql)\s", text) is None, "forbidden_command")


def validate_wrapper(text: str, sha: str) -> None:
    for item in (sha, "I_CONFIRM_EMAIL_MIGRATION_MATRIX_STAGE_READONLY_ONCE", "StrictHostKeyChecking=yes", "NumberOfPasswordPrompts=0"):
        require(item in text, f"wrapper_missing:{item}")
    require("scp.exe" not in text.lower() and "sftp.exe" not in text.lower(), "transfer_forbidden")


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
        payload.replace('"$actual_archive_sha" = "$archive_sha"', '-n "$actual_archive_sha"', 1),
        payload.replace("BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319", "0" * 64, 1),
        payload.replace(" || fail state_probe", "", 1),
        payload.replace(" || fail state_shape", "", 1),
        payload + "\ndocker ps\n",
        payload + "\nrm -rf /home/pc/molin-runtime/*\n",
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
    ):
        try:
            validate_wrapper(candidate, sha)
        except ContractError:
            rejected += 1
        else:
            raise ContractError("wrapper_attack_accepted")

    print(
        "status=pass mode=email_migration_matrix_stage_readonly_contract "
        f"attack_cases={rejected} external_access=false writes=false database_access=false docker_access=false retries=0"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

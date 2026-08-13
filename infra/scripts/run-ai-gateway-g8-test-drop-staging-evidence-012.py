#!/usr/bin/env python3
"""对 Drop 映射入口执行一次完全只读的 011 暂存低敏取证。"""

from __future__ import annotations

import sys


if not sys.flags.isolated:
    print(
        "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED "
        "reason=isolated_python_required"
    )
    raise SystemExit(2)


import re


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = (
    "/home/pc/molin/"
    ".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
)
EXPECTED_REMOTE_KEYS = frozenset(
    {
        "EVIDENCE_CHANGE_ID",
        "TARGET_CHANGE_ID",
        "LOGIN_USER",
        "DEPLOYMENT_ROOT_REALPATH",
        "DEPLOYMENT_ROOT_CHECK",
        "STAGING_STATE",
        "STAGING_INTEGRITY",
        "STAGING_MISMATCH_REASON",
        "EVIDENCE_RESULT",
    }
)
VALID_STATES = {
    ("ABSENT", "NOT_APPLICABLE", "NONE"),
    ("PRESENT", "PASS", "NONE"),
    *(("PRESENT", "MISMATCH", reason) for reason in (
        "PATH",
        "FILE_SET",
        "FILE_METADATA",
        "FILE_CONTENT",
        "MANIFEST",
        "RECEIPT",
        "READ_ERROR",
    )),
}


class EvidenceError(RuntimeError):
    """表示没有形成完整、低敏且可验证的 012 证据。"""


def build_remote_program() -> str:
    """构造只包含固定目标常量的远端只读程序骨架。"""

    return f"""import grp
import hashlib
import os
import pwd
import stat
evidence_change_id = {CHANGE_ID!r}
target_change_id = {TARGET_CHANGE_ID!r}
deployment_root = {TARGET_DEPLOYMENT_ROOT!r}
staging_path = {STAGING_PATH!r}
"""


def parse_remote_output(stdout: str) -> dict[str, str]:
    """严格解析固定九键和三态组合。"""

    try:
        stdout.encode("ascii")
    except UnicodeEncodeError as error:
        raise EvidenceError("non_ascii_output") from error

    values: dict[str, str] = {}
    for line in stdout.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if match is None or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)

    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")

    fixed = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values[key] != expected for key, expected in fixed.items()):
        raise EvidenceError("remote_contract_mismatch")

    state = (
        values["STAGING_STATE"],
        values["STAGING_INTEGRITY"],
        values["STAGING_MISMATCH_REASON"],
    )
    if state not in VALID_STATES:
        raise EvidenceError("invalid_staging_state")
    return values

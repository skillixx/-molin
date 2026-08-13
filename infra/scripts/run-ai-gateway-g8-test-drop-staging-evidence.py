#!/usr/bin/env python3
"""对 Drop 映射测试入口执行一次完全只读的 003 暂存低敏取证。"""

import sys

# 必须先拒绝非隔离解释器，避免脚本目录或 PYTHONPATH 中的同名模块劫持本地校验。
if not sys.flags.isolated:
    print(
        "G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED "
        "reason=isolated_python_required"
    )
    raise SystemExit(2)

import re


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = (
    "/home/pc/molin/"
    ".g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
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


class EvidenceError(RuntimeError):
    """表示远端输出未形成完整、低敏且可验证的暂存证据。"""


def build_remote_program() -> str:
    """生成不读取物理主机身份的远端只读程序骨架。"""
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
    """解析九键低敏结果，并拒绝额外身份字段或不一致状态组合。"""
    try:
        stdout.encode("ascii")
    except UnicodeEncodeError as error:
        raise EvidenceError("non_ascii_output") from error

    values: dict[str, str] = {}
    lines = stdout.splitlines()
    if len(lines) != len(EXPECTED_REMOTE_KEYS):
        raise EvidenceError("remote_key_count_mismatch")
    for line in lines:
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if match is None or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")

    expected = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values[key] != value for key, value in expected.items()):
        raise EvidenceError("remote_contract_mismatch")

    state = (
        values["STAGING_STATE"],
        values["STAGING_INTEGRITY"],
        values["STAGING_MISMATCH_REASON"],
    )
    valid_states = {
        ("ABSENT", "NOT_APPLICABLE", "NONE"),
        ("PRESENT", "PASS", "NONE"),
        *(("PRESENT", "MISMATCH", reason) for reason in (
            "PATH",
            "FILE_SET",
            "FILE_METADATA",
            "FILE_CONTENT",
            "READ_ERROR",
        )),
    }
    if state not in valid_states:
        raise EvidenceError("invalid_staging_state")
    return values

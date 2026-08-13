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
import textwrap


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = (
    "/home/pc/molin/"
    ".g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
)
FROZEN_FILES = {
    "SHA256SUMS": (
        "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d",
        362,
    ),
    "ai-gateway-reconcile": (
        "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
        13_066_129,
    ),
    "g8-test-readonly-audit": (
        "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
        18_377,
    ),
    "manifest.env": (
        "726174ea41ecfee69f9d8c1aff7411dc9a8c73f3dc60ca0d5e700eb5f962ea66",
        897,
    ),
    "molin-g8-test-readonly-audit.sudoers": (
        "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
        416,
    ),
}
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


def build_remote_program(
    *,
    deployment_root: str = TARGET_DEPLOYMENT_ROOT,
    staging_path: str = STAGING_PATH,
    expected_files: dict[str, tuple[str, int]] | None = None,
    _test_uid: int | None = None,
    _test_gid: int | None = None,
    _test_hook: str = "",
) -> str:
    """生成目录描述符锚定的远端只读程序；测试钩子只接受固定故障枚举。"""
    files = FROZEN_FILES if expected_files is None else expected_files
    hook_before_open = ""
    hook_after_hash = ""
    if _test_hook == "remove_manifest_before_open":
        hook_before_open = """
if name == 'manifest.env':
    os.unlink(name, dir_fd=stage_fd)
"""
    elif _test_hook == "pause_after_manifest_hash":
        hook_after_hash = """
if name == 'manifest.env':
    __import__('time').sleep(0.5)
"""
    elif _test_hook:
        raise EvidenceError("invalid_test_hook")

    identity_setup = (
        "account = pwd.getpwnam('pc')\nuid = account.pw_uid\n"
        "group = grp.getgrnam('pc')\ngid = group.gr_gid\n"
        "if os.getuid() != uid:\n    raise SystemExit(41)"
        if _test_uid is None or _test_gid is None
        else f"uid = {_test_uid!r}\ngid = {_test_gid!r}"
    )
    template = r'''
import grp
import hashlib
import os
import pwd
import stat

evidence_change_id = __CHANGE_ID__
target_change_id = __TARGET_CHANGE_ID__
deployment_root = __DEPLOYMENT_ROOT__
staging_path = __STAGING_PATH__
expected_files = __EXPECTED_FILES__
__IDENTITY_SETUP__

def metadata_identity(value):
    return (
        value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
        value.st_size, value.st_mtime_ns, value.st_ctime_ns,
    )

root_meta = os.lstat(deployment_root)
if os.path.realpath(deployment_root) != deployment_root:
    raise SystemExit(41)
root_fd = os.open(
    deployment_root,
    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
)
try:
    pinned_root = os.fstat(root_fd)
    root_mode = stat.S_IMODE(pinned_root.st_mode)
    if (
        (pinned_root.st_dev, pinned_root.st_ino) != (root_meta.st_dev, root_meta.st_ino)
        or not stat.S_ISDIR(pinned_root.st_mode)
        or pinned_root.st_uid != uid
        or pinned_root.st_gid != gid
        or root_mode != 0o700
        or os.path.dirname(staging_path) != deployment_root
    ):
        raise SystemExit(41)

    stage_name = os.path.basename(staging_path)
    staging_state = 'ABSENT'
    staging_integrity = 'NOT_APPLICABLE'
    staging_mismatch_reason = 'NONE'
    try:
        stage_meta = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        stage_meta = None
    except OSError:
        raise SystemExit(41)

    if stage_meta is not None:
        staging_state = 'PRESENT'
        staging_integrity = 'MISMATCH'
        staging_mismatch_reason = 'PATH'
        try:
            if (
                stat.S_ISDIR(stage_meta.st_mode)
                and stage_meta.st_uid == uid
                and stage_meta.st_gid == gid
                and stat.S_IMODE(stage_meta.st_mode) == 0o700
            ):
                stage_fd = os.open(
                    stage_name,
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                    dir_fd=root_fd,
                )
                try:
                    pinned_stage = os.fstat(stage_fd)
                    if (pinned_stage.st_dev, pinned_stage.st_ino) == (stage_meta.st_dev, stage_meta.st_ino):
                        names = os.listdir(stage_fd)
                        if set(names) != set(expected_files) or len(names) != len(expected_files):
                            staging_mismatch_reason = 'FILE_SET'
                        else:
                            metadata_matches = True
                            entries_stable = True
                            content_matches = True
                            opened_files = {}
                            for name in names:
                                __HOOK_BEFORE_OPEN__
                                file_fd = os.open(
                                    name,
                                    os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                                    dir_fd=stage_fd,
                                )
                                try:
                                    before = os.fstat(file_fd)
                                    expected_sha256, expected_size = expected_files[name]
                                    opened_files[name] = metadata_identity(before)
                                    valid_metadata = (
                                        stat.S_ISREG(before.st_mode)
                                        and before.st_uid == uid
                                        and before.st_gid == gid
                                        and not stat.S_IMODE(before.st_mode) & 0o022
                                        and before.st_size == expected_size
                                    )
                                    if valid_metadata:
                                        digest = hashlib.sha256()
                                        while True:
                                            block = os.read(file_fd, 1024 * 1024)
                                            if not block:
                                                break
                                            digest.update(block)
                                        actual_digest = digest.hexdigest()
                                    else:
                                        metadata_matches = False
                                        actual_digest = None
                                    __HOOK_AFTER_HASH__
                                    if metadata_identity(os.fstat(file_fd)) != opened_files[name]:
                                        entries_stable = False
                                    if actual_digest is not None and actual_digest != expected_sha256:
                                        content_matches = False
                                finally:
                                    os.close(file_fd)

                            final_names = os.listdir(stage_fd)
                            if set(final_names) != set(expected_files) or len(final_names) != len(expected_files):
                                entries_stable = False
                            if entries_stable:
                                for name in final_names:
                                    current = os.stat(name, dir_fd=stage_fd, follow_symlinks=False)
                                    if metadata_identity(current) != opened_files.get(name):
                                        entries_stable = False
                                        break
                            final_stage = os.fstat(stage_fd)
                            current_stage = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
                            if (
                                not entries_stable
                                or (final_stage.st_dev, final_stage.st_ino, final_stage.st_mtime_ns, final_stage.st_ctime_ns)
                                != (pinned_stage.st_dev, pinned_stage.st_ino, pinned_stage.st_mtime_ns, pinned_stage.st_ctime_ns)
                                or (current_stage.st_dev, current_stage.st_ino)
                                != (pinned_stage.st_dev, pinned_stage.st_ino)
                            ):
                                staging_mismatch_reason = 'PATH'
                            elif not metadata_matches:
                                staging_mismatch_reason = 'FILE_METADATA'
                            elif not content_matches:
                                staging_mismatch_reason = 'FILE_CONTENT'
                            else:
                                staging_integrity = 'PASS'
                                staging_mismatch_reason = 'NONE'
                finally:
                    os.close(stage_fd)
        except OSError:
            staging_mismatch_reason = 'READ_ERROR'

    current_root = os.lstat(deployment_root)
    if (
        os.path.realpath(deployment_root) != deployment_root
        or (current_root.st_dev, current_root.st_ino) != (pinned_root.st_dev, pinned_root.st_ino)
    ):
        raise SystemExit(41)
finally:
    os.close(root_fd)

print('EVIDENCE_CHANGE_ID=' + evidence_change_id)
print('TARGET_CHANGE_ID=' + target_change_id)
print('LOGIN_USER=pc')
print('DEPLOYMENT_ROOT_REALPATH=' + deployment_root)
print('DEPLOYMENT_ROOT_CHECK=PASS')
print('STAGING_STATE=' + staging_state)
print('STAGING_INTEGRITY=' + staging_integrity)
print('STAGING_MISMATCH_REASON=' + staging_mismatch_reason)
print('EVIDENCE_RESULT=PASS')
'''
    program = textwrap.dedent(template).replace("__CHANGE_ID__", repr(CHANGE_ID)).replace(
        "__TARGET_CHANGE_ID__", repr(TARGET_CHANGE_ID)
    ).replace("__DEPLOYMENT_ROOT__", repr(deployment_root)).replace(
        "__STAGING_PATH__", repr(staging_path)
    ).replace("__EXPECTED_FILES__", repr(files)).replace(
        "__IDENTITY_SETUP__", identity_setup
    )
    before_code = textwrap.dedent(hook_before_open).strip()
    after_code = textwrap.dedent(hook_after_hash).strip()
    return program.replace(
        "                                __HOOK_BEFORE_OPEN__",
        textwrap.indent(before_code, "                                ") if before_code else "",
    ).replace(
        "                                    __HOOK_AFTER_HASH__",
        textwrap.indent(after_code, "                                    ") if after_code else "",
    )


def parse_remote_output(
    stdout: str,
    *,
    expected_deployment_root: str = TARGET_DEPLOYMENT_ROOT,
) -> dict[str, str]:
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
        "DEPLOYMENT_ROOT_REALPATH": expected_deployment_root,
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

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
import textwrap


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
FROZEN_FILES = {
    "SHA256SUMS": (
        "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
        362,
        0o600,
    ),
    "ai-gateway-reconcile": (
        "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
        13_066_129,
        0o700,
    ),
    "g8-test-readonly-audit": (
        "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
        18_377,
        0o700,
    ),
    "manifest.env": (
        "763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6",
        863,
        0o600,
    ),
    "molin-g8-test-readonly-audit.sudoers": (
        "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
        416,
        0o600,
    ),
}
FROZEN_CHECKSUMS = {
    name: FROZEN_FILES[name][0]
    for name in (
        "ai-gateway-reconcile",
        "g8-test-readonly-audit",
        "manifest.env",
        "molin-g8-test-readonly-audit.sudoers",
    )
}
FROZEN_MANIFEST = {
    "BUNDLE_FORMAT_VERSION": "1",
    "CHANGE_ID": TARGET_CHANGE_ID,
    "SOURCE_COMMIT": "099c38ed62ccd62c3c5a3b6811f1369d7f0d3084",
    "SOURCE_TREE": "c2d1252a05d031d842549345128fa7a1ffe53dc8",
    "GO_VERSION": "go1.26.5",
    "GO_BUILDER_HOST": "windows/amd64",
    "GOOS": "linux",
    "GOARCH": "amd64",
    "CGO_ENABLED": "0",
    "GO_BUILD_FLAGS": "-trimpath,-buildvcs=false",
    "AUDITOR_SHA256": FROZEN_FILES["g8-test-readonly-audit"][0],
    "SUDOERS_SHA256": FROZEN_FILES["molin-g8-test-readonly-audit.sudoers"][0],
    "RECONCILE_SHA256": FROZEN_FILES["ai-gateway-reconcile"][0],
    "RECONCILE_SIZE": "13066129",
    "REPRODUCIBLE_BUILD_COUNT": "2",
    "TARGET_SSH": "pc@8.130.9.163:10003",
    "TARGET_SSH_ED25519_FINGERPRINT": (
        "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
    ),
    "TARGET_TRANSPORT": "DROP_SSH_INTERACTIVE_SUDO",
    "PHYSICAL_HOST_IDENTITY": "NOT_APPLICABLE",
    "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
}


class EvidenceError(RuntimeError):
    """表示没有形成完整、低敏且可验证的 012 证据。"""


def build_remote_program(
    *,
    deployment_root: str = TARGET_DEPLOYMENT_ROOT,
    staging_path: str = STAGING_PATH,
    expected_files: dict[str, tuple[str, int, int]] | None = None,
    expected_manifest: dict[str, str] | None = None,
    expected_checksums: dict[str, str] | None = None,
    _test_uid: int | None = None,
    _test_gid: int | None = None,
) -> str:
    """生成目录描述符锚定且没有写改删能力的远端只读程序。"""

    files = FROZEN_FILES if expected_files is None else expected_files
    manifest = FROZEN_MANIFEST if expected_manifest is None else expected_manifest
    checksums = FROZEN_CHECKSUMS if expected_checksums is None else expected_checksums
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
import re
import stat

evidence_change_id = __CHANGE_ID__
target_change_id = __TARGET_CHANGE_ID__
deployment_root = __DEPLOYMENT_ROOT__
staging_path = __STAGING_PATH__
expected_files = __EXPECTED_FILES__
expected_manifest = __EXPECTED_MANIFEST__
expected_checksums = __EXPECTED_CHECKSUMS__
__IDENTITY_SETUP__

def metadata_identity(value):
    return (
        value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
        value.st_size, value.st_mtime_ns, value.st_ctime_ns,
    )

def parse_manifest(content):
    try:
        text = content.decode('ascii')
    except UnicodeDecodeError:
        return None
    values = {}
    for line in text.splitlines():
        match = re.fullmatch(r'([A-Z0-9_]+)=([^\r\n]+)', line)
        if match is None or match.group(1) in values:
            return None
        values[match.group(1)] = match.group(2)
    return values

def parse_checksums(content):
    try:
        text = content.decode('ascii')
    except UnicodeDecodeError:
        return None
    values = {}
    for line in text.splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9._-]+)', line)
        if match is None or match.group(2) in values:
            return None
        values[match.group(2)] = match.group(1)
    return values

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
        metadata_identity(pinned_root) != metadata_identity(root_meta)
        or not stat.S_ISDIR(pinned_root.st_mode)
        or pinned_root.st_uid != uid
        or pinned_root.st_gid != gid
        or root_mode & 0o700 != 0o700
        or root_mode & 0o022
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
                    if metadata_identity(pinned_stage) == metadata_identity(stage_meta):
                        names = os.listdir(stage_fd)
                        if set(names) != set(expected_files) or len(names) != len(expected_files):
                            staging_mismatch_reason = 'FILE_SET'
                        else:
                            metadata_matches = True
                            entries_stable = True
                            artifact_content_matches = True
                            manifest_matches = True
                            receipt_matches = True
                            opened_files = {}
                            captured = {}
                            for name in names:
                                file_fd = os.open(
                                    name,
                                    os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                                    dir_fd=stage_fd,
                                )
                                try:
                                    before = os.fstat(file_fd)
                                    expected_sha256, expected_size, expected_mode = expected_files[name]
                                    opened_files[name] = metadata_identity(before)
                                    valid_metadata = (
                                        stat.S_ISREG(before.st_mode)
                                        and before.st_uid == uid
                                        and before.st_gid == gid
                                        and stat.S_IMODE(before.st_mode) == expected_mode
                                        and before.st_size == expected_size
                                    )
                                    actual_digest = None
                                    content = None
                                    if valid_metadata:
                                        digest = hashlib.sha256()
                                        kept = bytearray()
                                        while True:
                                            block = os.read(file_fd, 1024 * 1024)
                                            if not block:
                                                break
                                            digest.update(block)
                                            if name in ('manifest.env', 'SHA256SUMS'):
                                                kept.extend(block)
                                        actual_digest = digest.hexdigest()
                                        if name in ('manifest.env', 'SHA256SUMS'):
                                            content = bytes(kept)
                                            captured[name] = content
                                    else:
                                        metadata_matches = False
                                    if metadata_identity(os.fstat(file_fd)) != opened_files[name]:
                                        entries_stable = False
                                    if actual_digest is not None and actual_digest != expected_sha256:
                                        if name == 'manifest.env':
                                            manifest_matches = False
                                        elif name == 'SHA256SUMS':
                                            receipt_matches = False
                                        else:
                                            artifact_content_matches = False
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
                            if metadata_matches:
                                manifest_matches = (
                                    manifest_matches
                                    and parse_manifest(captured.get('manifest.env', b'')) == expected_manifest
                                )
                                receipt_matches = (
                                    receipt_matches
                                    and parse_checksums(captured.get('SHA256SUMS', b'')) == expected_checksums
                                )
                            final_stage = os.fstat(stage_fd)
                            current_stage = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
                            if (
                                not entries_stable
                                or metadata_identity(final_stage) != metadata_identity(pinned_stage)
                                or metadata_identity(current_stage) != metadata_identity(pinned_stage)
                            ):
                                staging_mismatch_reason = 'PATH'
                            elif not metadata_matches:
                                staging_mismatch_reason = 'FILE_METADATA'
                            elif not manifest_matches:
                                staging_mismatch_reason = 'MANIFEST'
                            elif not receipt_matches:
                                staging_mismatch_reason = 'RECEIPT'
                            elif not artifact_content_matches:
                                staging_mismatch_reason = 'FILE_CONTENT'
                            else:
                                staging_integrity = 'PASS'
                                staging_mismatch_reason = 'NONE'
                finally:
                    os.close(stage_fd)
        except OSError:
            staging_mismatch_reason = 'READ_ERROR'

    final_root = os.fstat(root_fd)
    current_root = os.lstat(deployment_root)
    if (
        os.path.realpath(deployment_root) != deployment_root
        or metadata_identity(final_root) != metadata_identity(pinned_root)
        or metadata_identity(current_root) != metadata_identity(pinned_root)
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
    return (
        textwrap.dedent(template)
        .replace("__CHANGE_ID__", repr(CHANGE_ID))
        .replace("__TARGET_CHANGE_ID__", repr(TARGET_CHANGE_ID))
        .replace("__DEPLOYMENT_ROOT__", repr(deployment_root))
        .replace("__STAGING_PATH__", repr(staging_path))
        .replace("__EXPECTED_FILES__", repr(files))
        .replace("__EXPECTED_MANIFEST__", repr(manifest))
        .replace("__EXPECTED_CHECKSUMS__", repr(checksums))
        .replace("__IDENTITY_SETUP__", identity_setup)
    )


def parse_remote_output(
    stdout: str,
    *,
    expected_deployment_root: str = TARGET_DEPLOYMENT_ROOT,
) -> dict[str, str]:
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
        "DEPLOYMENT_ROOT_REALPATH": expected_deployment_root,
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

#!/usr/bin/env python3
"""为 010 Drop 直连候选执行本地门禁，并为后续独立授权保留单次 SSH/SFTP 入口。"""

import sys

# 必须在加载任何可被脚本目录或 PYTHONPATH 替换的模块前拒绝非隔离解释器。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import re
import shutil
import stat
import tempfile
import types
from dataclasses import dataclass
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010"
CHANGE_ID_CONSUMED = False
SOURCE_COMMIT = "75b1fc4ddb7138495547cec03fa948648de337d7"
SOURCE_TREE = "53ba990318bc1a036b442d88ff8133d776a453dc"
TARGET = "pc@8.130.9.163"
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_TRANSPORT = "DROP_SSH_DIRECT"
PHYSICAL_HOST_IDENTITY = "NOT_APPLICABLE"
TARGET_SSH_ED25519_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010"
EXPECTED_BUNDLE_RECEIPT_SHA256 = "3ff8cf3ad7237f866f83305d00ab73f766381b7f3247abee915efee629e41fb0"
FROZEN_AUDITOR_SHA256 = "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256"
FROZEN_SUDOERS_SHA256 = "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f"
FROZEN_RECONCILE_SHA256 = "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1"
FROZEN_RECONCILE_SIZE = 13_066_129
FROZEN_HELPER_SHA256 = "4be88638f2a4a271ebbf23751bd3f7238ea5f78f1f18fcb6889c9e071b953f30"
EXPECTED_FILES = {
    "SHA256SUMS",
    "ai-gateway-reconcile",
    "g8-test-readonly-audit",
    "manifest.env",
    "molin-g8-test-readonly-audit.sudoers",
}
EXPECTED_MANIFEST_KEYS = {
    "BUNDLE_FORMAT_VERSION",
    "CHANGE_ID",
    "SOURCE_COMMIT",
    "SOURCE_TREE",
    "GO_VERSION",
    "GO_BUILDER_HOST",
    "GOOS",
    "GOARCH",
    "CGO_ENABLED",
    "GO_BUILD_FLAGS",
    "AUDITOR_SHA256",
    "SUDOERS_SHA256",
    "RECONCILE_SHA256",
    "RECONCILE_SIZE",
    "REPRODUCIBLE_BUILD_COUNT",
    "TARGET_SSH",
    "TARGET_SSH_ED25519_FINGERPRINT",
    "TARGET_TRANSPORT",
    "PHYSICAL_HOST_IDENTITY",
    "TARGET_DEPLOYMENT_ROOT",
}
EXPECTED_REMOTE_KEYS = {
    "PREFLIGHT_CHANGE_ID",
    "LOGIN_USER",
    "LOGIN_GROUP",
    "DEPLOYMENT_ROOT_REALPATH",
    "DEPLOYMENT_ROOT_META",
    "STAGING_ABSENT",
    "INSTALL_TARGETS_ABSENT",
    "PREFLIGHT_RESULT",
}

# Drop 入口只核验映射端点、登录账户、部署根和目标不存在，不读取物理主机身份。
REMOTE_SCRIPT = f"""set -eu
unset ENV BASH_ENV CDPATH
PATH=/usr/bin:/bin
export PATH
login_user=$(/usr/bin/id -un)
login_group=$(/usr/bin/id -gn)
deployment_root_realpath=$(/usr/bin/realpath -e {TARGET_DEPLOYMENT_ROOT})
deployment_root_meta=$(/usr/bin/stat -c '%U:%G:%a:%F' {TARGET_DEPLOYMENT_ROOT})
staging_path={STAGING_PATH}
if [ -e "$staging_path" ] || [ -L "$staging_path" ]; then exit 41; fi
for target in /usr/local/libexec/molin/g8-test-readonly-audit /usr/local/libexec/molin/ai-gateway-reconcile /etc/sudoers.d/molin-g8-test-readonly-audit; do
  if [ -e "$target" ] || [ -L "$target" ]; then exit 42; fi
done
printf 'PREFLIGHT_CHANGE_ID=%s\n' '{CHANGE_ID}'
printf 'LOGIN_USER=%s\n' "$login_user"
printf 'LOGIN_GROUP=%s\n' "$login_group"
printf 'DEPLOYMENT_ROOT_REALPATH=%s\n' "$deployment_root_realpath"
printf 'DEPLOYMENT_ROOT_META=%s\n' "$deployment_root_meta"
printf 'STAGING_ABSENT=true\n'
printf 'INSTALL_TARGETS_ABSENT=true\n'
printf 'PREFLIGHT_RESULT=PASS\n'
"""


class DirectStageError(RuntimeError):
    """表示直连包装器未形成完整且低敏的可信证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方路径或其他参数。"""

    def error(self, message: str) -> None:
        raise RuntimeError("invalid_arguments")


@dataclass(frozen=True)
class FileEvidence:
    """记录本地文件的稳定身份，不保存或输出文件正文。"""

    resolved_path: str
    device: int
    inode: int
    mode: int
    size: int
    modified_ns: int
    changed_ns: int
    sha256: str


def sha256(path: Path) -> str:
    """流式计算摘要，正文不进入输出或日志。"""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def file_evidence(path: Path) -> FileEvidence:
    """冻结普通非链接文件的路径、身份、大小、时间戳和摘要。"""
    if not path.is_absolute():
        raise RuntimeError("path_not_absolute")
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or path.is_symlink():
        raise RuntimeError("invalid_local_file")
    digest = sha256(path)
    after = path.stat()
    before_identity = (
        before.st_dev,
        before.st_ino,
        before.st_mode,
        before.st_size,
        before.st_mtime_ns,
        before.st_ctime_ns,
    )
    after_identity = (
        after.st_dev,
        after.st_ino,
        after.st_mode,
        after.st_size,
        after.st_mtime_ns,
        after.st_ctime_ns,
    )
    if before_identity != after_identity:
        raise RuntimeError("local_file_drift")
    return FileEvidence(
        str(path.resolve(strict=True)),
        after.st_dev,
        after.st_ino,
        after.st_mode,
        after.st_size,
        after.st_mtime_ns,
        after.st_ctime_ns,
        digest,
    )


def load_frozen_helper():
    """先冻结 009 helper 的普通文件身份和摘要，再加载其中已评审的通用只读函数。"""
    helper_path = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-access-stage-drop.py")
    before = helper_path.lstat()
    if not stat.S_ISREG(before.st_mode) or helper_path.is_symlink():
        raise RuntimeError("helper_invalid")
    with helper_path.open("rb") as handle:
        helper_bytes = handle.read()
    helper_sha256 = hashlib.sha256(helper_bytes).hexdigest()
    after = helper_path.stat()
    if (
        helper_sha256 != FROZEN_HELPER_SHA256
        or (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns)
        != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns)
    ):
        raise RuntimeError("helper_mismatch")
    # 直接执行刚完成摘要核对的同一字节串，避免摘要校验后再次按路径打开产生竞态窗口。
    module_name = "g8_consumed_stage_drop_helper"
    helper = types.ModuleType(module_name)
    helper.__file__ = str(helper_path)
    sys.modules[module_name] = helper
    exec(compile(helper_bytes, str(helper_path), "exec"), helper.__dict__)
    expected_contract = {
        "TARGET": TARGET,
        "TARGET_HOST": TARGET_HOST,
        "TARGET_PORT": TARGET_PORT,
        "TARGET_SSH_ED25519_FINGERPRINT": TARGET_SSH_ED25519_FINGERPRINT,
        "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
    }
    if any(getattr(helper, key, None) != value for key, value in expected_contract.items()):
        raise RuntimeError("helper_contract_mismatch")
    for name in (
        "validate_known_hosts",
        "validate_identity_files",
        "fixed_tool",
        "fixed_ssh_environment",
        "ssh_options",
        "run_bounded_process",
    ):
        if not callable(getattr(helper, name, None)):
            raise RuntimeError("helper_contract_mismatch")
    return helper


def parse_manifest(path: Path) -> dict[str, str]:
    """严格解析低敏清单，拒绝空行、重复键和非 ASCII 内容。"""
    values: dict[str, str] = {}
    for line in path.read_text(encoding="ascii").splitlines():
        if not re.fullmatch(r"[A-Z0-9_]+=[ -~]+", line):
            raise RuntimeError("invalid_manifest")
        key, value = line.split("=", 1)
        if key in values:
            raise RuntimeError("duplicate_manifest_key")
        values[key] = value
    return values


def validate_candidate(candidate_dir: Path) -> None:
    """联网前验证 010 五文件白名单、清单身份、摘要和大小。"""
    if not candidate_dir.is_absolute() or not candidate_dir.is_dir() or candidate_dir.is_symlink():
        raise RuntimeError("invalid_candidate_directory")
    entries = list(candidate_dir.iterdir())
    if {entry.name for entry in entries} != EXPECTED_FILES:
        raise RuntimeError("candidate_file_set_mismatch")
    if any(not entry.is_file() or entry.is_symlink() for entry in entries):
        raise RuntimeError("invalid_candidate_file")
    if sha256(candidate_dir / "SHA256SUMS") != EXPECTED_BUNDLE_RECEIPT_SHA256:
        raise RuntimeError("bundle_receipt_mismatch")
    checksums: dict[str, str] = {}
    for line in (candidate_dir / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match.group(2) in checksums:
            raise RuntimeError("invalid_checksum_manifest")
        checksums[match.group(2)] = match.group(1)
    if set(checksums) != EXPECTED_FILES - {"SHA256SUMS"}:
        raise RuntimeError("checksum_file_set_mismatch")
    if any(sha256(candidate_dir / name) != digest for name, digest in checksums.items()):
        raise RuntimeError("candidate_checksum_mismatch")
    values = parse_manifest(candidate_dir / "manifest.env")
    if set(values) != EXPECTED_MANIFEST_KEYS:
        raise RuntimeError("manifest_key_set_mismatch")
    expected_values = {
        "CHANGE_ID": CHANGE_ID,
        "SOURCE_COMMIT": SOURCE_COMMIT,
        "SOURCE_TREE": SOURCE_TREE,
        "TARGET_TRANSPORT": TARGET_TRANSPORT,
        "PHYSICAL_HOST_IDENTITY": PHYSICAL_HOST_IDENTITY,
        "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
        "TARGET_SSH": f"pc@{TARGET_HOST}:{TARGET_PORT}",
        "TARGET_SSH_ED25519_FINGERPRINT": TARGET_SSH_ED25519_FINGERPRINT,
        "AUDITOR_SHA256": FROZEN_AUDITOR_SHA256,
        "SUDOERS_SHA256": FROZEN_SUDOERS_SHA256,
        "RECONCILE_SHA256": FROZEN_RECONCILE_SHA256,
        "RECONCILE_SIZE": str(FROZEN_RECONCILE_SIZE),
    }
    if any(values.get(key) != value for key, value in expected_values.items()):
        raise RuntimeError("candidate_identity_mismatch")
    if "TARGET_HOSTNAME" in values or "TARGET_MACHINE_ID_SHA256" in values:
        raise RuntimeError("physical_identity_not_allowed")


def validate_local_inputs(candidate_dir: Path, known_hosts: Path, identity_file: Path, identity_public_file: Path, helper):
    """验证候选和现有免密码密钥材料，仅记录摘要与元数据。"""
    validate_candidate(candidate_dir)
    helper.validate_known_hosts(known_hosts)
    helper.validate_identity_files(identity_file, identity_public_file, known_hosts)
    evidence = {
        "known_hosts": file_evidence(known_hosts),
        "identity": file_evidence(identity_file),
        "identity_public": file_evidence(identity_public_file),
    }
    for name in sorted(EXPECTED_FILES):
        evidence[f"candidate:{name}"] = file_evidence(candidate_dir / name)
    return evidence


def assert_local_inputs_unchanged(
    candidate_dir: Path,
    known_hosts: Path,
    identity_file: Path,
    identity_public_file: Path,
    helper,
    expected_evidence,
) -> None:
    """在远端边界前后重算证据，发现任何持久漂移即失败关闭。"""
    if validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file, helper) != expected_evidence:
        raise RuntimeError("local_input_drift")


def create_frozen_candidate_snapshot(candidate_dir: Path, snapshot_dir: Path) -> Path:
    """只复制候选五文件；现有私钥和 known_hosts 始终保留在原路径。"""
    snapshot_dir.mkdir(mode=0o700)
    for name in sorted(EXPECTED_FILES):
        with (candidate_dir / name).open("rb") as source, (snapshot_dir / name).open("xb") as target:
            shutil.copyfileobj(source, target, length=1024 * 1024)
    validate_candidate(snapshot_dir)
    return snapshot_dir


def parse_remote_output(stdout: bytes) -> dict[str, str]:
    """要求预检输出精确键集合，拒绝额外行、重复字段和目标漂移。"""
    try:
        text = stdout.decode("ascii")
    except UnicodeError as error:
        raise DirectStageError("invalid_remote_output") from error
    values: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([ -~]+)", line)
        if not match or match.group(1) in values:
            raise DirectStageError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise DirectStageError("remote_key_set_mismatch")
    expected = {
        "PREFLIGHT_CHANGE_ID": CHANGE_ID,
        "LOGIN_USER": "pc",
        "LOGIN_GROUP": "pc",
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "STAGING_ABSENT": "true",
        "INSTALL_TARGETS_ABSENT": "true",
        "PREFLIGHT_RESULT": "PASS",
    }
    if any(values.get(key) != value for key, value in expected.items()):
        raise DirectStageError("remote_contract_mismatch")
    meta = re.fullmatch(r"pc:pc:([0-7]{3,4}):directory", values["DEPLOYMENT_ROOT_META"])
    if not meta:
        raise DirectStageError("deployment_root_metadata_mismatch")
    mode = int(meta.group(1), 8)
    if mode & 0o700 != 0o700 or mode & 0o022:
        raise DirectStageError("deployment_root_mode_unsafe")
    return values


def run_remote_preflight(helper, ssh_executable: Path, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """只启动一次 SSH，显式引用现有免密码密钥与固定 known_hosts。"""
    command = [
        str(ssh_executable),
        *helper.ssh_options(known_hosts, identity_file),
        "-p",
        TARGET_PORT,
        TARGET,
        "/usr/bin/env",
        "-i",
        "PATH=/usr/bin:/bin",
        "/bin/sh",
        "-s",
    ]
    try:
        returncode, stdout_result, stderr_result = helper.run_bounded_process(
            command,
            helper.fixed_ssh_environment(),
            input_data=REMOTE_SCRIPT.encode("ascii"),
            timeout=20,
        )
    except Exception as error:
        raise DirectStageError("ssh_preflight_failed") from error
    if (
        returncode != 0
        or stderr_result["bytes"] != 0
        or stdout_result["exceeded"]
        or stderr_result["exceeded"]
    ):
        raise DirectStageError("ssh_preflight_failed")
    return parse_remote_output(stdout_result["captured"])


def run_atomic_sftp_upload(
    helper,
    sftp_executable: Path,
    known_hosts: Path,
    identity_file: Path,
    candidate_dir: Path,
) -> None:
    """以一次 SFTP 独占创建 010 暂存目录，已存在即停止且不覆盖。"""
    batch = "\n".join(
        (
            f"mkdir {STAGING_PATH}",
            f"chmod 700 {STAGING_PATH}",
            *(f"put {name} {STAGING_PATH}/{name}" for name in sorted(EXPECTED_FILES)),
            "quit",
        )
    ) + "\n"
    command = [
        str(sftp_executable),
        "-q",
        "-b",
        "-",
        *helper.ssh_options(known_hosts, identity_file),
        "-P",
        TARGET_PORT,
        TARGET,
    ]
    try:
        returncode, stdout_result, stderr_result = helper.run_bounded_process(
            command,
            helper.fixed_ssh_environment(),
            input_data=batch.encode("ascii"),
            timeout=30,
            cwd=candidate_dir,
        )
    except Exception as error:
        raise DirectStageError("sftp_upload_failed") from error
    if (
        returncode != 0
        or stderr_result["bytes"] != 0
        or stdout_result["exceeded"]
        or stderr_result["exceeded"]
    ):
        raise DirectStageError("sftp_upload_failed")


def main() -> int:
    """提供离线工程门禁，并保留必须经未来独立授权才能使用的远端正式入口。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--candidate-dir")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    try:
        arguments = parser.parse_args()
    except RuntimeError:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=invalid_request")
        return 2
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=change_id_consumed")
        return 2
    if arguments.self_test:
        try:
            load_frozen_helper()
            if (
                "hostname" in REMOTE_SCRIPT
                or "machine-id" in REMOTE_SCRIPT
                or REMOTE_SCRIPT.count("PREFLIGHT_RESULT=PASS") != 1
            ):
                raise RuntimeError("self_test_failed")
        except Exception:
            print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=self_test_failed")
            return 2
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT_SELF_TEST=PASS")
        return 0
    if arguments.change_id != CHANGE_ID:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=invalid_request")
        return 2
    try:
        if not all(
            (
                arguments.candidate_dir,
                arguments.known_hosts,
                arguments.identity_file,
                arguments.identity_public_file,
            )
        ):
            raise RuntimeError("missing_argument")
        candidate_dir = Path(arguments.candidate_dir)
        known_hosts = Path(arguments.known_hosts)
        identity_file = Path(arguments.identity_file)
        identity_public_file = Path(arguments.identity_public_file)
        helper = load_frozen_helper()
        evidence = validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file, helper)
        if arguments.local_check:
            print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT_LOCAL_CHECK=PASS")
            return 0
        with tempfile.TemporaryDirectory(prefix="molin-g8-access-stage-drop-direct-") as temporary:
            snapshot_dir = create_frozen_candidate_snapshot(candidate_dir, Path(temporary) / "candidate")
            assert_local_inputs_unchanged(
                candidate_dir, known_hosts, identity_file, identity_public_file, helper, evidence
            )
            values = run_remote_preflight(helper, helper.fixed_tool("ssh"), known_hosts, identity_file)
            assert_local_inputs_unchanged(
                candidate_dir, known_hosts, identity_file, identity_public_file, helper, evidence
            )
            run_atomic_sftp_upload(
                helper,
                helper.fixed_tool("sftp"),
                known_hosts,
                identity_file,
                snapshot_dir,
            )
            assert_local_inputs_unchanged(
                candidate_dir, known_hosts, identity_file, identity_public_file, helper, evidence
            )
            validate_candidate(snapshot_dir)
    except DirectStageError as error:
        reason = "sftp_upload_failed" if str(error) == "sftp_upload_failed" else "remote_preflight_failed"
        print(f"G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason={reason}")
        return 2
    except Exception:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT=PASS")
    print(f"change_id={CHANGE_ID}")
    print(f"target={TARGET}:{TARGET_PORT}")
    print(f"deployment_root_meta={values['DEPLOYMENT_ROOT_META']}")
    print("staging_absent=true")
    print("install_targets_absent=true")
    print("staging_uploaded=true")
    print("business_requests=0 upstream_requests=0 cost_cny=0")
    return 0


if __name__ == "__main__":
    sys.exit(main())

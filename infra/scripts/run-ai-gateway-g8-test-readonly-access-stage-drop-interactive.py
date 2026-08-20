#!/usr/bin/env python3
"""为 011 候选执行离线门禁，并保留未来独立授权的一次 SFTP 入口。"""

import sys

# 必须在加载脚本目录中的依赖前拒绝非隔离解释器。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import stat
import tempfile
import types
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = True
SOURCE_COMMIT = "099c38ed62ccd62c3c5a3b6811f1369d7f0d3084"
SOURCE_TREE = "c2d1252a05d031d842549345128fa7a1ffe53dc8"
TARGET_TRANSPORT = "DROP_SSH_INTERACTIVE_SUDO"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
EXPECTED_BUNDLE_RECEIPT_SHA256 = "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f"
FROZEN_DIRECT_SHA256 = "4fb920e32574c640685ddd9bed919485473dc54873d157a409c1adf987b3ab6a"
DIRECT_NAME = "run-ai-gateway-g8-test-readonly-access-stage-drop-direct.py"


class StageError(RuntimeError):
    """表示只能对外输出固定低敏原因的门禁失败。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 把调用方参数或路径回显到 stderr。"""

    def error(self, message: str) -> None:
        raise StageError("invalid_request")


def sha256_bytes(content: bytes) -> str:
    """计算冻结依赖字节摘要。"""
    return hashlib.sha256(content).hexdigest()


def load_frozen_direct():
    """从同目录读取已消费 010 包装器的冻结字节，只复用其纯校验与 SFTP 原语。"""
    path = Path(__file__).with_name(DIRECT_NAME)
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or path.is_symlink():
        raise StageError("helper_unavailable")
    content = path.read_bytes()
    after = path.stat()
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (
        after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns
    ) or sha256_bytes(content) != FROZEN_DIRECT_SHA256:
        raise StageError("helper_unavailable")
    module = types.ModuleType("g8_consumed_direct_010")
    module.__file__ = str(path)
    sys.modules[module.__name__] = module
    try:
        exec(compile(content, str(path), "exec"), module.__dict__)
    finally:
        sys.modules.pop(module.__name__, None)
    # 所有复用函数通过模块全局读取候选契约，因此在任何材料校验前统一切换到 011。
    module.CHANGE_ID = CHANGE_ID
    module.SOURCE_COMMIT = SOURCE_COMMIT
    module.SOURCE_TREE = SOURCE_TREE
    module.TARGET_TRANSPORT = TARGET_TRANSPORT
    module.STAGING_PATH = STAGING_PATH
    module.EXPECTED_BUNDLE_RECEIPT_SHA256 = EXPECTED_BUNDLE_RECEIPT_SHA256
    return module


def create_candidate_snapshot(direct, candidate_dir: Path, snapshot_root: Path) -> Path:
    """只冻结五文件候选，身份材料始终由固定原路径交给系统 SFTP。"""
    return direct.create_frozen_candidate_snapshot(candidate_dir, snapshot_root / "candidate")


def run_single_sftp(direct, helper, known_hosts: Path, identity_file: Path, snapshot: Path) -> None:
    """唯一远端能力是一次固定 SFTP；本函数没有登录命令或提权能力。"""
    batch = "\n".join(
        (
            f"mkdir {STAGING_PATH}",
            f"chmod 700 {STAGING_PATH}",
            *(f"put {name} {STAGING_PATH}/{name}" for name in sorted(direct.EXPECTED_FILES)),
            f"chmod 600 {STAGING_PATH}/SHA256SUMS",
            f"chmod 700 {STAGING_PATH}/ai-gateway-reconcile",
            f"chmod 700 {STAGING_PATH}/g8-test-readonly-audit",
            f"chmod 600 {STAGING_PATH}/manifest.env",
            f"chmod 600 {STAGING_PATH}/molin-g8-test-readonly-audit.sudoers",
            "quit",
        )
    ) + "\n"
    command = [
        str(helper.fixed_tool("sftp")),
        "-q",
        "-b",
        "-",
        *helper.ssh_options(known_hosts, identity_file),
        "-P",
        direct.TARGET_PORT,
        direct.TARGET,
    ]
    try:
        returncode, stdout_result, stderr_result = helper.run_bounded_process(
            command,
            helper.fixed_ssh_environment(),
            input_data=batch.encode("ascii"),
            timeout=30,
            cwd=snapshot,
        )
    except Exception as error:
        raise StageError("sftp_upload_failed") from error
    if (
        returncode != 0
        or stdout_result["bytes"] != 0
        or stderr_result["bytes"] != 0
        or stdout_result["exceeded"]
        or stderr_result["exceeded"]
    ):
        raise StageError("sftp_upload_failed")


def execute(
    candidate_dir: Path,
    known_hosts: Path,
    identity_file: Path,
    identity_public_file: Path,
    *,
    local_check: bool,
) -> str:
    """验证本地材料；正式模式只允许在完整门禁后执行一次原子暂存。"""
    direct = load_frozen_direct()
    helper = direct.load_frozen_helper()
    evidence = direct.validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file, helper)
    if local_check:
        return "G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE_LOCAL_CHECK=PASS"
    with tempfile.TemporaryDirectory(prefix="molin-g8-stage-011-") as temporary:
        snapshot = create_candidate_snapshot(direct, candidate_dir, Path(temporary))
        direct.assert_local_inputs_unchanged(
            candidate_dir, known_hosts, identity_file, identity_public_file, helper, evidence
        )
        run_single_sftp(direct, helper, known_hosts, identity_file, snapshot)
        direct.assert_local_inputs_unchanged(
            candidate_dir, known_hosts, identity_file, identity_public_file, helper, evidence
        )
    return "G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=PASS"


def main(argv: list[str] | None = None) -> int:
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--candidate-dir")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    try:
        arguments = parser.parse_args(argv)
        if CHANGE_ID_CONSUMED:
            raise StageError("change_id_consumed")
        if arguments.self_test:
            if any((arguments.local_check, arguments.change_id, arguments.candidate_dir, arguments.known_hosts,
                    arguments.identity_file, arguments.identity_public_file)):
                raise StageError("invalid_request")
            load_frozen_direct()
            print("G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE_SELF_TEST=PASS")
            return 0
        if arguments.change_id != CHANGE_ID:
            raise StageError("invalid_request")
        paths = tuple(
            Path(value or "")
            for value in (
                arguments.candidate_dir,
                arguments.known_hosts,
                arguments.identity_file,
                arguments.identity_public_file,
            )
        )
        if any(not path.is_absolute() for path in paths):
            raise StageError("invalid_request")
        print(execute(*paths, local_check=arguments.local_check))
        return 0
    except StageError as error:
        reason = "change_id_consumed" if str(error) == "change_id_consumed" else "invalid_request"
    except Exception:
        reason = "invalid_request"
    print(f"G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=FAILED reason={reason}")
    return 2


if __name__ == "__main__":
    sys.exit(main())

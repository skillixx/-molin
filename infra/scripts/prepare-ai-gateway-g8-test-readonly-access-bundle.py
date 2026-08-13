#!/usr/bin/env python3
"""从冻结提交生成 G8 测试服只读入口候选包，不连接或修改服务器。"""

import sys

# 必须在导入任何可由脚本目录或 PYTHONPATH 替换的模块前拒绝非隔离解释器。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import os
import shutil
import subprocess
import tarfile
import tempfile
from dataclasses import dataclass
from pathlib import Path, PurePosixPath


FROZEN_AUDITOR_SHA256 = "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256"
FROZEN_SUDOERS_SHA256 = "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f"
FROZEN_RECONCILE_SHA256 = "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1"
FROZEN_RECONCILE_SIZE = 13_066_129
TRUSTED_PATH = os.pathsep.join(
    (
        "/usr/local/go/bin",
        "/opt/hostedtoolcache/go/1.26.5/x64/bin",
        "/usr/local/bin",
        "/usr/bin",
        "/bin",
        r"C:\Program Files\Git\cmd",
        r"C:\Program Files\Go\bin",
    )
)
DROP_TRANSPORTS = {
    "DROP_SSH",
    "DROP_SSH_DIRECT",
    "DROP_SSH_INTERACTIVE_SUDO",
}


@dataclass(frozen=True)
class FrozenCandidate:
    """描述一次不可漂移的候选来源与制品事实。"""

    change_id: str
    source_commit: str
    source_tree: str
    target_deployment_root: str | None
    target_transport: str | None = None


CONSUMED_CANDIDATES = {
    "CHG-G8-TEST-READONLY-ACCESS-20260812-001": FrozenCandidate(
        "CHG-G8-TEST-READONLY-ACCESS-20260812-001",
        "c50f092339fcad79ca1262925480219db1755318",
        "2e9701c3f5d8ba12aebc9631b01696b189f1d313",
        None,
    ),
    "CHG-G8-TEST-READONLY-ACCESS-20260812-002": FrozenCandidate(
        "CHG-G8-TEST-READONLY-ACCESS-20260812-002",
        "50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44",
        "73fb652a1f86db84991c8745f8c10e1d2a255f29",
        "/home/pc/molin",
    ),
    "CHG-G8-TEST-READONLY-ACCESS-20260812-003": FrozenCandidate(
        "CHG-G8-TEST-READONLY-ACCESS-20260812-003",
        "8ec878572f62ef2584c38aaadc1bca1cb802b13f",
        "988bdcdc8017322264733ebe68876e4811b01412",
        "/home/pc/molin",
    ),
    "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009": FrozenCandidate(
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009",
        "7f3325e2d6801567fea34a2049a2f3ada114e348",
        "4563feb59850dca87789adfb5eea820f78b1a209",
        "/home/pc/molin",
        "DROP_SSH",
    ),
    "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010": FrozenCandidate(
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010",
        "75b1fc4ddb7138495547cec03fa948648de337d7",
        "53ba990318bc1a036b442d88ff8133d776a453dc",
        "/home/pc/molin",
        "DROP_SSH_DIRECT",
    ),
}
EXPECTED_CONSUMED_RECEIPTS = {
    "win32": {
        "CHG-G8-TEST-READONLY-ACCESS-20260812-001": "2fb6a964cf017997fa07d1df557cc41979b873597445f2543b497744b4fa70c9",
        "CHG-G8-TEST-READONLY-ACCESS-20260812-002": "d6d07f7b4959e48f5ffe0e92ee4116cef55fe56f5318df6ae3f0d9c5350ee567",
        "CHG-G8-TEST-READONLY-ACCESS-20260812-003": "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d",
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009": "840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd",
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010": "3ff8cf3ad7237f866f83305d00ab73f766381b7f3247abee915efee629e41fb0",
    },
    "linux": {
        "CHG-G8-TEST-READONLY-ACCESS-20260812-001": "14b7d8cd832f0b719031fcc93adbbb2208afe76d34383e63d51c44b044772b5a",
        "CHG-G8-TEST-READONLY-ACCESS-20260812-002": "7ae580cc06fb101fe44c9e3a4d7581116fd258ef1e2d09d99bba0bda50151a1f",
        "CHG-G8-TEST-READONLY-ACCESS-20260812-003": "7f4633357bf6883d166b0ee7d9750d7e745cf0a15d23163a547d6519e217efc1",
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009": "5bb49ad531410c8a719008bcd860d143eb9c51d23858a14737d678d5a60fc893",
        "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010": "b3fac1a1530124da9dc604c32d11bd665de3daa5d6799aebb33c38a3d2f174f4",
    },
}

ACTIVE_CANDIDATE = FrozenCandidate(
    "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011",
    "099c38ed62ccd62c3c5a3b6811f1369d7f0d3084",
    "c2d1252a05d031d842549345128fa7a1ffe53dc8",
    "/home/pc/molin",
    "DROP_SSH_INTERACTIVE_SUDO",
)


def sha256(path: Path) -> str:
    """以流式读取计算摘要，避免把二进制整体载入内存。"""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def trusted_environment() -> dict[str, str]:
    """仅向构建子进程传递固定变量，拒绝调用方 Git、Go、Shell 环境注入。"""
    environment = {
        "PATH": TRUSTED_PATH,
        "GOENV": "off",
        "GOWORK": "off",
        "GOTOOLCHAIN": "local",
        "GOFLAGS": "",
        "GOOS": "linux",
        "GOARCH": "amd64",
        "CGO_ENABLED": "0",
        "LANG": "C.UTF-8",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_ATTR_NOSYSTEM": "1",
        # 环境级配置优先于仓库 local 配置，确保不同克隆的换行策略不会改变冻结归档。
        "GIT_CONFIG_COUNT": "2",
        "GIT_CONFIG_KEY_0": "core.autocrlf",
        "GIT_CONFIG_VALUE_0": "false",
        "GIT_CONFIG_KEY_1": "core.eol",
        "GIT_CONFIG_VALUE_1": "lf",
    }
    # Windows 子进程定位系统 DLL、用户模块缓存和临时目录需要这些基础路径；它们不改变 Git 仓库或 Go 构建参数。
    for name in ("SYSTEMROOT", "USERPROFILE", "LOCALAPPDATA", "TEMP", "TMP"):
        if os.environ.get(name):
            environment[name] = os.environ[name]
    return environment


def find_tool(name: str, environment: dict[str, str]) -> str:
    """只从受信 PATH 查找工具并要求返回绝对路径。"""
    tool = shutil.which(name, path=environment["PATH"])
    if not tool or not Path(tool).is_absolute():
        raise RuntimeError("required_tool_unavailable")
    return tool


def run(command: list[str], *, cwd: Path, environment: dict[str, str], stdout=None) -> str:
    """执行固定参数子进程；失败不回显可能含调用方数据的详细异常。"""
    result = subprocess.run(
        command,
        cwd=cwd,
        env=environment,
        stdout=stdout or subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=stdout is None,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError("subprocess_failed")
    return result.stdout.strip() if stdout is None else ""


def safe_extract(archive_path: Path, destination: Path) -> None:
    """只接受普通文件和目录，拒绝绝对路径、穿越、链接及特殊设备。"""
    with tarfile.open(archive_path, "r:") as archive:
        members = archive.getmembers()
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not (member.isfile() or member.isdir()):
                raise RuntimeError("unsafe_archive_member")
        archive.extractall(destination, members=members, filter="data")


def write_manifest(output_dir: Path, values: dict[str, str]) -> None:
    """清单只写固定元数据和摘要，不写凭据或环境变量值。"""
    manifest = output_dir / "manifest.env"
    manifest.write_text("".join(f"{key}={value}\n" for key, value in values.items()), encoding="utf-8")
    manifest.chmod(0o600)
    checksum_paths = (
        "ai-gateway-reconcile",
        "g8-test-readonly-audit",
        "manifest.env",
        "molin-g8-test-readonly-audit.sudoers",
    )
    checksums = output_dir / "SHA256SUMS"
    checksums.write_text(
        "".join(f"{sha256(output_dir / name)}  {name}\n" for name in checksum_paths),
        encoding="ascii",
    )
    checksums.chmod(0o600)


def prepare(candidate: FrozenCandidate, output_dir: Path) -> dict[str, str]:
    """生成全新候选目录；任一失败均清理本次创建的半成品。"""
    if not output_dir.is_absolute() or output_dir.exists():
        raise RuntimeError("invalid_output_directory")

    script_path = Path(__file__).resolve(strict=True)
    repo_root = script_path.parents[2]
    environment = trusted_environment()
    git = find_tool("git", environment)
    go = find_tool("go", environment)
    declared_root = Path(run([git, "rev-parse", "--show-toplevel"], cwd=repo_root, environment=environment)).resolve()
    if declared_root != repo_root:
        raise RuntimeError("repository_mismatch")
    resolved_commit = run(
        [git, "rev-parse", "--verify", f"{candidate.source_commit}^{{commit}}"],
        cwd=repo_root,
        environment=environment,
    )
    source_tree = run([git, "show", "-s", "--format=%T", candidate.source_commit], cwd=repo_root, environment=environment)
    if resolved_commit != candidate.source_commit or source_tree != candidate.source_tree:
        raise RuntimeError("source_mismatch")

    output_created = False
    try:
        with tempfile.TemporaryDirectory(prefix="molin-g8-access-") as temporary:
            temporary_root = Path(temporary)
            build_environment = {
                **environment,
                "GOCACHE": str(temporary_root / "go-build-cache"),
                "GOMODCACHE": str(temporary_root / "go-module-cache"),
            }
            archive_path = temporary_root / "source.tar"
            with archive_path.open("wb") as archive_handle:
                run([git, "archive", candidate.source_commit], cwd=repo_root, environment=environment, stdout=archive_handle)
            source_root = temporary_root / "source"
            source_root.mkdir()
            safe_extract(archive_path, source_root)

            auditor_source = source_root / "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh"
            sudoers_source = source_root / "infra/sudoers/molin-g8-test-readonly-audit"
            server_root = source_root / "server"
            if not auditor_source.is_file() or auditor_source.is_symlink() or not sudoers_source.is_file() or sudoers_source.is_symlink():
                raise RuntimeError("required_asset_missing")
            run([go, "mod", "download"], cwd=server_root, environment=build_environment)
            run([go, "mod", "verify"], cwd=server_root, environment=build_environment)

            build_one = temporary_root / "reconcile-1"
            build_two = temporary_root / "reconcile-2"
            for destination in (build_one, build_two):
                run(
                    [go, "build", "-trimpath", "-buildvcs=false", "-o", str(destination), "./cmd/ai-gateway-reconcile"],
                    cwd=server_root,
                    environment=build_environment,
                )
            reconcile_sha = sha256(build_one)
            if reconcile_sha != sha256(build_two):
                raise RuntimeError("non_reproducible_build")

            output_dir.mkdir(mode=0o700)
            output_created = True
            auditor_target = output_dir / "g8-test-readonly-audit"
            sudoers_target = output_dir / "molin-g8-test-readonly-audit.sudoers"
            reconcile_target = output_dir / "ai-gateway-reconcile"
            shutil.copyfile(auditor_source, auditor_target)
            shutil.copyfile(sudoers_source, sudoers_target)
            shutil.copyfile(build_one, reconcile_target)
            auditor_target.chmod(0o700)
            sudoers_target.chmod(0o600)
            reconcile_target.chmod(0o700)

            auditor_sha = sha256(auditor_target)
            sudoers_sha = sha256(sudoers_target)
            reconcile_size = reconcile_target.stat().st_size
            if (
                auditor_sha != FROZEN_AUDITOR_SHA256
                or sudoers_sha != FROZEN_SUDOERS_SHA256
                or reconcile_sha != FROZEN_RECONCILE_SHA256
                or reconcile_size != FROZEN_RECONCILE_SIZE
            ):
                raise RuntimeError("artifact_mismatch")

            go_version = run([go, "env", "GOVERSION"], cwd=server_root, environment=build_environment)
            if go_version != "go1.26.5":
                raise RuntimeError("go_version_mismatch")
            builder_host = "/".join(
                (
                    run([go, "env", "GOHOSTOS"], cwd=server_root, environment=build_environment),
                    run([go, "env", "GOHOSTARCH"], cwd=server_root, environment=build_environment),
                )
            )
            values = {
                "BUNDLE_FORMAT_VERSION": "1",
                "CHANGE_ID": candidate.change_id,
                "SOURCE_COMMIT": candidate.source_commit,
                "SOURCE_TREE": source_tree,
                "GO_VERSION": go_version,
                "GO_BUILDER_HOST": builder_host,
                "GOOS": "linux",
                "GOARCH": "amd64",
                "CGO_ENABLED": "0",
                "GO_BUILD_FLAGS": "-trimpath,-buildvcs=false",
                "AUDITOR_SHA256": auditor_sha,
                "SUDOERS_SHA256": sudoers_sha,
                "RECONCILE_SHA256": reconcile_sha,
                "RECONCILE_SIZE": str(reconcile_size),
                "REPRODUCIBLE_BUILD_COUNT": "2",
            }
            if candidate.target_transport in DROP_TRANSPORTS:
                # Drop 映射只绑定已批准的 SSH 端点，不把物理主机身份误作固定入口契约。
                values["TARGET_SSH"] = "pc@8.130.9.163:10003"
                values["TARGET_SSH_ED25519_FINGERPRINT"] = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
                values["TARGET_TRANSPORT"] = candidate.target_transport
                values["PHYSICAL_HOST_IDENTITY"] = "NOT_APPLICABLE"
            else:
                # 已消费的 001 至 003 必须维持原始清单字段，确保历史回执可重复核验。
                values["TARGET_SSH"] = "pc@8.130.9.163:10003"
                values["TARGET_HOSTNAME"] = "pc-Z790-UD-AX"
                values["TARGET_MACHINE_ID_SHA256"] = "b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4"
                values["TARGET_SSH_ED25519_FINGERPRINT"] = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
            if candidate.target_deployment_root:
                values["TARGET_DEPLOYMENT_ROOT"] = candidate.target_deployment_root
            write_manifest(output_dir, values)
            values["BUNDLE_RECEIPT_SHA256"] = sha256(output_dir / "SHA256SUMS")
            output_created = False
            return values
    finally:
        if output_created and output_dir.is_dir():
            shutil.rmtree(output_dir)


def main() -> int:
    parser = argparse.ArgumentParser(add_help=True)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--verify-consumed-candidate", action="store_true")
    parser.add_argument("--consumed-change-id")
    parser.add_argument("--change-id")
    parser.add_argument("--source-commit")
    parser.add_argument("--output-dir")
    arguments = parser.parse_args()
    if arguments.self_test:
        print("G8_TEST_READONLY_ACCESS_BUNDLE_SELF_TEST=PASS")
        return 0
    try:
        if arguments.verify_consumed_candidate:
            if arguments.change_id or arguments.source_commit or arguments.output_dir:
                raise RuntimeError("unexpected_argument")
            candidate = CONSUMED_CANDIDATES.get(arguments.consumed_change_id)
            if candidate is None:
                raise RuntimeError("unknown_consumed_candidate")
            # 已消费候选只允许在系统临时目录重建、校验并自动销毁，不再输出可供安装的持久目录。
            with tempfile.TemporaryDirectory(prefix="molin-g8-consumed-verify-") as temporary:
                values = prepare(candidate, Path(temporary) / "bundle")
            platform_receipts = EXPECTED_CONSUMED_RECEIPTS.get(sys.platform)
            if (
                platform_receipts is None
                or values["BUNDLE_RECEIPT_SHA256"] != platform_receipts.get(candidate.change_id)
            ):
                raise RuntimeError("consumed_receipt_mismatch")
            marker = "G8_TEST_READONLY_ACCESS_BUNDLE_VERIFY=PASS"
        else:
            if arguments.consumed_change_id:
                raise RuntimeError("unexpected_argument")
            if (
                ACTIVE_CANDIDATE is None
                or arguments.change_id != ACTIVE_CANDIDATE.change_id
                or arguments.source_commit != ACTIVE_CANDIDATE.source_commit
                or not arguments.output_dir
            ):
                raise RuntimeError("active_candidate_mismatch")
            values = prepare(ACTIVE_CANDIDATE, Path(arguments.output_dir))
            marker = "G8_TEST_READONLY_ACCESS_BUNDLE=PASS"
    except Exception:
        print("G8_TEST_READONLY_ACCESS_BUNDLE=FAILED reason=invalid_request")
        return 2
    print(marker)
    for key in (
        "CHANGE_ID",
        "SOURCE_COMMIT",
        "SOURCE_TREE",
        "AUDITOR_SHA256",
        "SUDOERS_SHA256",
        "RECONCILE_SHA256",
        "RECONCILE_SIZE",
        "BUNDLE_RECEIPT_SHA256",
        "TARGET_TRANSPORT",
    ):
        if key in values:
            print(f"{key.lower()}={values[key]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

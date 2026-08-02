#!/usr/bin/env python3
"""从两个固定前端容器只读导出完整部署树并生成独立 manifest。"""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import re
import signal
import stat
import subprocess
import sys
import tarfile
from dataclasses import dataclass

sys.dont_write_bytecode = True

CONFIRM = "I_CONFIRM_PHASE4_FRONTEND_READONLY_EXPORT"
ROOT = pathlib.Path("/home/pc/molin/phase4-runtime-frontends")
SOURCE = "/usr/share/nginx/html"
PROJECTION_PATH = pathlib.Path("/home/pc/molin-runtime/phase4-ops-linux-0346ff54/tests/email/phase4_runtime_source_projection.py")
PROJECTION_SHA256 = "2bc04f38c2e5073b5fe390c83394f16acc46b0c6b353834a848eec5487f606ab"
PROJECTION_SIZE = 62_893
MAX_FILES = 20_000
MAX_NODES = 25_000
MAX_BYTES = 64 * 1024 * 1024
MAX_DEPTH = 32


class ExportFailure(Exception):
    """失败分类不携带 Docker 或文件系统原始错误。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ExportFailure(classification)


@dataclass(frozen=True)
class TreeIdentity:
    tree_sha256: str
    file_count: int
    byte_count: int


def write_all(descriptor: int, data: bytes) -> None:
    """循环处理短写；零进展立即失败，禁止留下静默裁剪文件。"""
    view = memoryview(data)
    offset = 0
    while offset < len(view):
        count = os.write(descriptor, view[offset:])
        require(count > 0, "short_write")
        offset += count


def descriptor_sha256(descriptor: int) -> str:
    """只从已经绑定身份的只读描述符计算摘要，避免路径二次打开产生替换窗口。"""
    digest = hashlib.sha256()
    while True:
        chunk = os.read(descriptor, 1024 * 1024)
        if not chunk:
            return digest.hexdigest()
        digest.update(chunk)


def _verify_projection_file(path: pathlib.Path, expected_path: pathlib.Path, expected_uid: int) -> pathlib.Path:
    """绑定固定 companion 普通文件；路径、属主、权限、身份或摘要任一异常都关闭。"""
    require(path.is_absolute() and expected_path.is_absolute() and path == expected_path, "projection_path")
    require(not path.is_symlink() and path.resolve(strict=True) == expected_path, "projection_binding")
    before = path.lstat()
    require(
        stat.S_ISREG(before.st_mode) and before.st_uid == expected_uid
        and stat.S_IMODE(before.st_mode) in {0o400, 0o600} and before.st_size == PROJECTION_SIZE,
        "projection_binding",
    )
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        opened = os.fstat(descriptor)
        require(
            stat.S_ISREG(opened.st_mode) and opened.st_uid == expected_uid
            and stat.S_IMODE(opened.st_mode) in {0o400, 0o600}
            and opened.st_size == PROJECTION_SIZE
            and (opened.st_dev, opened.st_ino) == (before.st_dev, before.st_ino),
            "projection_binding",
        )
        require(descriptor_sha256(descriptor) == PROJECTION_SHA256, "projection_binding")
        after = path.lstat()
        require(
            not path.is_symlink() and path.resolve(strict=True) == expected_path
            and (after.st_dev, after.st_ino) == (opened.st_dev, opened.st_ino)
            and after.st_uid == expected_uid and stat.S_IMODE(after.st_mode) in {0o400, 0o600}
            and after.st_size == PROJECTION_SIZE,
            "projection_binding",
        )
    finally:
        os.close(descriptor)
    return path


def verify_projection_file(path: pathlib.Path, expected_path: pathlib.Path, expected_uid: int) -> pathlib.Path:
    """把缺失、权限拒绝和解析异常统一关闭，避免泄露 companion 文件系统细节。"""
    try:
        return _verify_projection_file(path, expected_path, expected_uid)
    except (OSError, RuntimeError, ValueError):
        raise ExportFailure("projection_binding") from None


def verify_projection_path(raw_path: str) -> pathlib.Path:
    """第三参数只能是启动器固化的 stage companion 绝对路径，拒绝任意路径注入。"""
    require(raw_path == str(PROJECTION_PATH), "projection_path")
    return verify_projection_file(pathlib.Path(raw_path), PROJECTION_PATH, os.getuid())


def inspect_container(name: str) -> tuple[str, str]:
    result = subprocess.run(
        ["docker", "inspect", "--format", "{{.Id}}\t{{.Image}}\t{{.State.Running}}\t{{.Name}}", name],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        check=False, timeout=20,
    )
    require(result.returncode == 0, "container_inspect")
    fields = result.stdout.decode("ascii", errors="strict").strip().split("\t")
    require(len(fields) == 4 and fields[2] == "true" and fields[3] == "/" + name, "container_state")
    require(re.fullmatch(r"[a-f0-9]{64}", fields[0]) is not None, "container_id")
    require(re.fullmatch(r"sha256:[a-f0-9]{64}", fields[1]) is not None, "image_digest")
    return fields[0], fields[1]


def verify_container_bindings(
    bindings: tuple[tuple[str, str, str], ...],
    frozen: dict[str, tuple[str, str]],
) -> None:
    """统一复核全部容器，任一 ID、image SHA、运行态或名称漂移都关闭。"""
    for container, _, _ in bindings:
        require(inspect_container(container) == frozen[container], "container_changed")


def normalized_member(name: str) -> pathlib.PurePosixPath | None:
    while name.startswith("./"):
        name = name[2:]
    if name in {"", "."}:
        return None
    path = pathlib.PurePosixPath(name)
    require(not path.is_absolute() and ".." not in path.parts and len(path.parts) <= MAX_DEPTH, "archive_path")
    require(all(part and not part.lower().startswith(".env") for part in path.parts), "archive_path")
    return path


def export_tree(container: str, destination: pathlib.Path) -> None:
    destination.mkdir(mode=0o700)
    process = subprocess.Popen(
        ["docker", "cp", f"{container}:{SOURCE}/.", "-"], stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )
    require(process.stdout is not None, "archive_stream")
    seen: set[str] = set()
    nodes = 0
    files = 0
    total = 0
    try:
        with tarfile.open(fileobj=process.stdout, mode="r|*") as archive:
            for member in archive:
                relative = normalized_member(member.name)
                if relative is None:
                    continue
                key = relative.as_posix()
                require(key not in seen, "archive_duplicate")
                seen.add(key)
                nodes += 1
                require(nodes <= MAX_NODES, "archive_limit")
                target = destination.joinpath(*relative.parts)
                require(not target.exists() and not target.is_symlink(), "stage_collision")
                if member.isdir():
                    target.mkdir(mode=0o700)
                    continue
                require(member.isfile() and not member.issym() and not member.islnk(), "archive_type")
                require(0 <= member.size <= MAX_BYTES, "archive_limit")
                files += 1
                total += member.size
                require(files <= MAX_FILES and total <= MAX_BYTES, "archive_limit")
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                stream = archive.extractfile(member)
                require(stream is not None, "archive_stream")
                descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                remaining = member.size
                try:
                    while remaining:
                        chunk = stream.read(min(1024 * 1024, remaining))
                        require(bool(chunk), "archive_truncated")
                        write_all(descriptor, chunk)
                        remaining -= len(chunk)
                    require(not stream.read(1), "archive_size")
                    os.fsync(descriptor)
                finally:
                    os.close(descriptor)
        require(process.wait(timeout=20) == 0, "docker_copy")
    except BaseException:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=5)
        raise
    require(files >= 3 and total > 0, "frontend_incomplete")


def tree_identity(root: pathlib.Path) -> TreeIdentity:
    """摘要算法逐字节对齐 phase4_runtime_source_projection.py。"""
    digest = hashlib.sha256()
    files = 0
    total = 0
    has_index = False
    has_script = False
    has_style = False
    # 原准备器使用显式 LIFO 栈：先记录本层目录摘要，再以反向发现顺序展开子目录。
    stack: list[pathlib.Path] = [root]
    while stack:
        current_path = stack.pop()
        require(not current_path.is_symlink() and current_path.is_dir(), "symlink")
        children = sorted(current_path.iterdir(), key=lambda value: value.name.encode("utf-8"))
        for target in children:
            require(not target.is_symlink(), "symlink")
            relative = target.relative_to(root).as_posix()
            if target.is_dir():
                digest.update(b"D\0" + relative.encode("utf-8") + b"\0")
                stack.append(target)
                continue
            metadata = target.stat(follow_symlinks=False)
            require(stat.S_ISREG(metadata.st_mode) and not target.is_symlink(), "file_type")
            file_digest = hashlib.sha256(target.read_bytes()).digest()
            files += 1
            total += metadata.st_size
            digest.update(b"F\0" + relative.encode("utf-8") + b"\0" + str(metadata.st_size).encode("ascii") + b"\0" + file_digest)
            has_index = has_index or relative == "index.html"
            suffix = pathlib.PurePosixPath(relative).suffix
            has_script = has_script or (relative.startswith("assets/") and suffix in {".js", ".mjs"})
            has_style = has_style or (relative.startswith("assets/") and suffix == ".css")
    require(has_index and has_script and has_style and files >= 3 and total > 0, "frontend_incomplete")
    return TreeIdentity(digest.hexdigest(), files, total)


def close_tree(root: pathlib.Path) -> None:
    for current, directories, names in os.walk(root, topdown=False, followlinks=False):
        current_path = pathlib.Path(current)
        for name in names:
            os.chmod(current_path / name, 0o444, follow_symlinks=False)
        for directory in directories:
            os.chmod(current_path / directory, 0o555, follow_symlinks=False)
    os.chmod(root, 0o555, follow_symlinks=False)


def write_manifest(path: pathlib.Path, role: str, identity: TreeIdentity, image: str) -> None:
    value = {
        "role": role, "tree_sha256": identity.tree_sha256,
        "file_count": identity.file_count, "byte_count": identity.byte_count,
        "container_or_image_digest": image,
    }
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        write_all(descriptor, json.dumps(value, separators=(",", ":"), sort_keys=True).encode("utf-8"))
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    require(stat.S_IMODE(path.stat(follow_symlinks=False).st_mode) == 0o600, "manifest_mode")


def execute(export_id: str, projection_path: pathlib.Path) -> None:
    require(os.name == "posix" and ROOT.parent.is_dir() and not ROOT.parent.is_symlink(), "platform_gate")
    ROOT.mkdir(mode=0o700, exist_ok=True)
    require(
        ROOT.is_dir() and not ROOT.is_symlink() and ROOT.stat().st_uid == os.getuid()
        and stat.S_IMODE(ROOT.stat().st_mode) == 0o700,
        "platform_gate",
    )
    require(projection_path == PROJECTION_PATH, "projection_binding")
    stage = ROOT / export_id
    stage.mkdir(mode=0o700)
    bindings = (("molin-admin", "admin_frontend", "admin"), ("molin-user", "user_frontend", "user"))
    frozen: dict[str, tuple[str, str]] = {}
    for container, _, _ in bindings:
        frozen[container] = inspect_container(container)
    identities: dict[str, TreeIdentity] = {}
    for container, role, leaf in bindings:
        target = stage / leaf
        export_tree(container, target)
        identities[role] = tree_identity(target)
        close_tree(target)
        require(tree_identity(target) == identities[role], "tree_changed")
        require(inspect_container(container) == frozen[container], "container_changed")
        write_manifest(stage / f"{leaf}.manifest.json", role, identities[role], frozen[container][1])
    # 两棵树全部落盘后再次统一复核，覆盖另一容器在后半程发生漂移的窗口。
    verify_container_bindings(bindings, frozen)
    for _, role, leaf in bindings:
        require(tree_identity(stage / leaf) == identities[role], "tree_changed")
    print(
        "status=pass mode=frontend_export export_id=" + export_id
        + " admin_files=" + str(identities["admin_frontend"].file_count)
        + " user_files=" + str(identities["user_frontend"].file_count)
        + " manifests_mode=600 projection_bound=true container_writes=false"
    )


def main() -> int:
    if len(sys.argv) == 1:
        print("status=disabled mode=frontend_export docker_access=false persistent_writes=false")
        return 0
    try:
        require(len(sys.argv) == 4 and sys.argv[1] == CONFIRM, "confirmation")
        require(re.fullmatch(r"[a-f0-9]{32}", sys.argv[2]) is not None, "export_id")
        signal.signal(signal.SIGALRM, lambda _signum, _frame: (_ for _ in ()).throw(ExportFailure("total_timeout")))
        signal.alarm(90)
        projection_path = verify_projection_path(sys.argv[3])
        execute(sys.argv[2], projection_path)
        signal.alarm(0)
        return 0
    except (ExportFailure, OSError, UnicodeError, subprocess.SubprocessError, tarfile.TarError):
        print("status=failed mode=frontend_export classification=closed container_writes=false")
        return 2
    except Exception:
        print("status=failed mode=frontend_export classification=closed container_writes=false")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())

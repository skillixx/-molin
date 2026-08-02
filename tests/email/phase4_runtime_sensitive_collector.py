#!/usr/bin/env python3
"""从显式只读输入组装 Phase 4 六面封闭证据包。"""

from __future__ import annotations

import contextlib
import datetime as dt
import hashlib
import io
import json
import os
import pathlib
import stat
import sys
from dataclasses import dataclass
from typing import Any

sys.dont_write_bytecode = True

import phase4_runtime_sensitive_scan as scanner


MAX_SOURCE_FILES = 20_000
MAX_SOURCE_NODES = 20_000
MAX_DIRECTORY_ENTRIES = 4_096
MAX_FRONTEND_DEPTH = 64
MAX_OUTPUT_DIRECTORIES = 2_048
OUTPUT_DIRECTORY_NAMES = ("http", "runtime", "frontend")
ENV_PREFIX = ".env"
FAILURE_CLASSIFICATIONS = frozenset({
    "argument_contract", "path_contract", "protected_env", "platform_not_supported",
    "source_contract", "source_not_readonly", "source_identity", "projection_contract",
    "sensitive_source", "log_contract", "telemetry_contract", "output_contract",
    "target_exists", "frontend_contract", "size_limit", "output_identity",
    "cleanup_failed", "close_failed", "unexpected_output", "internal_error", "fault_injected",
})


class CollectFailure(Exception):
    """只携带固定分类，不携带路径、值或系统异常正文。"""

    def __init__(self, classification: str, partial_retained: bool = False) -> None:
        safe_classification = classification if classification in FAILURE_CLASSIFICATIONS else "internal_error"
        super().__init__(safe_classification)
        self.classification = safe_classification
        self.partial_retained = partial_retained


@dataclass(frozen=True)
class CollectContract:
    public_get: str
    admin_get: str
    application_log: str
    audit_projection: str
    database_projection: str
    telemetry: str
    admin_frontend: str
    user_frontend: str
    output: str
    deployment_sha: str


@dataclass
class DirectoryChain:
    descriptors: list[int]
    edges: list[tuple[int, str, os.stat_result]]

    @property
    def leaf(self) -> int:
        return self.descriptors[-1]


@dataclass(frozen=True)
class CreatedNode:
    parent: int
    name: str
    device: int
    inode: int
    node_type: str
    descriptor: int | None


@dataclass
class CreationRegistry:
    nodes: list[CreatedNode]
    children: dict[int, set[str]]
    directory_descriptors: list[int]

    def register(self, node: CreatedNode) -> None:
        if node.node_type == "directory" and node.descriptor is not None:
            require(len(self.directory_descriptors) < MAX_OUTPUT_DIRECTORIES, "size_limit")
        self.nodes.append(node)
        self.children.setdefault(node.parent, set()).add(node.name)
        if node.node_type == "directory" and node.descriptor is not None:
            self.directory_descriptors.append(node.descriptor)
            self.children.setdefault(node.descriptor, set())


@dataclass(frozen=True)
class CollectResult:
    manifest_sha256: str
    bundle_id: str
    files: int
    bytes_written: int

    def line(self) -> str:
        return (
            "status=pass mode=collector surfaces=6 "
            f"files={self.files} bytes={self.bytes_written} "
            f"manifest_sha256={self.manifest_sha256} bundle_id={self.bundle_id} "
            "external_access=false database_access=false redis_access=false env_read=false "
            "partial_retained=false"
        )


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise CollectFailure(classification)


def platform_supported() -> bool:
    return (
        os.name == "posix"
        and os.open in os.supports_dir_fd
        and os.stat in os.supports_dir_fd
        and os.mkdir in os.supports_dir_fd
        and os.scandir in os.supports_fd
    )


def parse_absolute(raw: Any) -> tuple[str, ...]:
    require(isinstance(raw, str) and raw.startswith("/") and "\x00" not in raw, "path_contract")
    require("\\" not in raw and ":" not in raw, "path_contract")
    parsed = pathlib.PurePosixPath(raw)
    require(parsed.is_absolute(), "path_contract")
    parts = parsed.parts[1:]
    require(parts and all(part not in {"", ".", ".."} for part in parts), "path_contract")
    require(all(part == part.rstrip(" .") for part in parts), "path_contract")
    for part in parts:
        reject_env_name(part)
    return parts


def reject_env_name(name: str) -> None:
    require(not name.lower().startswith(ENV_PREFIX), "protected_env")


def identity(metadata: os.stat_result) -> tuple[int, int, int, int, int]:
    return scanner.identity_tuple(metadata)


def node_identity(metadata: os.stat_result) -> tuple[int, int]:
    return metadata.st_dev, metadata.st_ino


def close_descriptors(descriptors: list[int]) -> bool:
    """关闭全部文件描述符；任一关闭失败时返回失败，且不会提前中断后续关闭。"""
    failed = False
    for descriptor in reversed(descriptors):
        try:
            os.close(descriptor)
        except OSError:
            failed = True
    return not failed


def open_directory_chain(parts: tuple[str, ...], require_leaf_readonly: bool) -> DirectoryChain:
    require(platform_supported(), "platform_not_supported")
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    descriptors: list[int] = []
    edges: list[tuple[int, str, os.stat_result]] = []
    try:
        root = os.open("/", flags)
        descriptors.append(root)
        current = root
        for index, part in enumerate(parts):
            before = os.stat(part, dir_fd=current, follow_symlinks=False)
            child = os.open(part, flags, dir_fd=current)
            descriptors.append(child)
            opened = os.fstat(child)
            require(
                identity(before) == identity(opened)
                and stat.S_ISDIR(opened.st_mode)
                and not scanner.is_reparse(opened),
                "source_contract",
            )
            if require_leaf_readonly and index == len(parts) - 1:
                require(opened.st_mode & 0o222 == 0, "source_not_readonly")
            edges.append((current, part, opened))
            current = child
        return DirectoryChain(descriptors, edges)
    except CollectFailure as error:
        if not close_descriptors(descriptors):
            raise CollectFailure("close_failed") from error
        raise
    except OSError as error:
        if not close_descriptors(descriptors):
            raise CollectFailure("close_failed") from error
        raise CollectFailure("source_contract") from error


def verify_chain(chain: DirectoryChain, classification: str, stable_node_only: bool = False) -> None:
    for parent, name, opened in chain.edges:
        try:
            current = os.stat(name, dir_fd=parent, follow_symlinks=False)
        except OSError as error:
            raise CollectFailure(classification) from error
        if stable_node_only:
            require(node_identity(current) == node_identity(opened), classification)
        else:
            require(identity(current) == identity(opened), classification)


def close_chain(chain: DirectoryChain) -> None:
    if not close_descriptors(chain.descriptors):
        raise CollectFailure("close_failed")


def close_descriptor(descriptor: int, partial_retained: bool = False) -> None:
    try:
        os.close(descriptor)
    except OSError as error:
        raise CollectFailure("close_failed", partial_retained=partial_retained) from error


def read_source_file(raw: str, maximum: int = scanner.MAX_FILE_BYTES) -> bytes:
    parts = parse_absolute(raw)
    reject_env_name(parts[-1])
    parent = open_directory_chain(parts[:-1], False)
    descriptor: int | None = None
    try:
        before = os.stat(parts[-1], dir_fd=parent.leaf, follow_symlinks=False)
        flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(parts[-1], flags, dir_fd=parent.leaf)
        opened = os.fstat(descriptor)
        require(
            identity(before) == identity(opened)
            and stat.S_ISREG(opened.st_mode)
            and not scanner.is_reparse(opened)
            and opened.st_mode & 0o222 == 0
            and opened.st_size <= maximum,
            "source_not_readonly" if opened.st_mode & 0o222 else "source_contract",
        )
        chunks: list[bytes] = []
        remaining = opened.st_size
        while remaining:
            chunk = os.read(descriptor, min(1024 * 1024, remaining))
            require(chunk != b"", "source_contract")
            chunks.append(chunk)
            remaining -= len(chunk)
        require(identity(opened) == identity(os.fstat(descriptor)), "source_identity")
        current = os.stat(parts[-1], dir_fd=parent.leaf, follow_symlinks=False)
        require(identity(opened) == identity(current), "source_identity")
        verify_chain(parent, "source_identity")
        return b"".join(chunks)
    except CollectFailure:
        raise
    except OSError as error:
        raise CollectFailure("source_contract") from error
    finally:
        close_failed = False
        if descriptor is not None:
            close_failed = not close_descriptors([descriptor])
        close_failed = not close_descriptors(parent.descriptors) or close_failed
        if close_failed:
            raise CollectFailure("close_failed")


def canonical_projection(data: bytes, surface: str, route_class: str | None = None) -> bytes:
    try:
        value = json.loads(data.decode("utf-8", errors="strict"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise CollectFailure("projection_contract") from error
    require(isinstance(value, dict) and value, "projection_contract")
    try:
        scanner.validate_projection_fields(surface, value)
    except scanner.ScanFailure as error:
        raise CollectFailure("projection_contract") from error
    if route_class is not None:
        require(value.get("route_class") == route_class, "projection_contract")
        require(type(value.get("http_status")) is int, "projection_contract")
    encoded = (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    require(not scanner.contains_sensitive(encoded, surface), "sensitive_source")
    return encoded


def validate_log(data: bytes) -> bytes:
    require(data != b"" and data.endswith(b"\n"), "log_contract")
    try:
        data.decode("utf-8", errors="strict")
    except UnicodeDecodeError as error:
        raise CollectFailure("log_contract") from error
    require(not scanner.contains_sensitive(data, "application_logs"), "sensitive_source")
    return data


def validate_telemetry(data: bytes) -> bytes:
    try:
        scanner.validate_telemetry(data)
    except scanner.ScanFailure as error:
        raise CollectFailure("telemetry_contract") from error
    require(not scanner.contains_sensitive(data, "telemetry"), "sensitive_source")
    return data


def write_file(parent: int, name: str, data: bytes, registry: CreationRegistry) -> os.stat_result:
    reject_env_name(name)
    descriptor: int | None = None
    registered = False
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(name, flags, 0o600, dir_fd=parent)
        created = os.fstat(descriptor)
        require(stat.S_ISREG(created.st_mode), "output_contract")
        registry.register(CreatedNode(parent, name, created.st_dev, created.st_ino, "file", None))
        registered = True
        view = memoryview(data)
        written = 0
        while written < len(view):
            count = os.write(descriptor, view[written:])
            require(count > 0, "output_contract")
            written += count
        os.fsync(descriptor)
        os.fchmod(descriptor, 0o444)
        metadata = os.fstat(descriptor)
        require(stat.S_ISREG(metadata.st_mode) and metadata.st_size == len(data), "output_contract")
        return metadata
    except CollectFailure:
        raise
    except OSError as error:
        if descriptor is not None and not registered and not close_descriptors([descriptor]):
            raise CollectFailure("close_failed", partial_retained=True) from error
        raise CollectFailure("output_contract") from error
    finally:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError as error:
                raise CollectFailure("close_failed", partial_retained=registered) from error


def create_directory(parent: int, name: str, registry: CreationRegistry) -> tuple[int, os.stat_result]:
    reject_env_name(name)
    descriptor: int | None = None
    registered = False
    try:
        os.mkdir(name, 0o700, dir_fd=parent)
        flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(name, flags, dir_fd=parent)
        metadata = os.fstat(descriptor)
        require(stat.S_ISDIR(metadata.st_mode) and not scanner.is_reparse(metadata), "output_contract")
        registry.register(CreatedNode(parent, name, metadata.st_dev, metadata.st_ino, "directory", descriptor))
        registered = True
        return descriptor, metadata
    except CollectFailure:
        if descriptor is not None and not registered:
            try:
                os.close(descriptor)
            except OSError as error:
                raise CollectFailure("close_failed", partial_retained=True) from error
        raise
    except OSError as error:
        raise CollectFailure("output_contract") from error


def close_registry(registry: CreationRegistry) -> bool:
    failed = False
    for descriptor in reversed(registry.directory_descriptors):
        try:
            os.close(descriptor)
        except OSError:
            failed = True
    return not failed


def parse_args(argv: list[str] | None) -> CollectContract:
    values = list(sys.argv[1:] if argv is None else argv)
    flags = (
        "--public-get", "--admin-get", "--application-log", "--audit-projection",
        "--database-projection", "--telemetry", "--admin-frontend", "--user-frontend",
        "--output", "--deployment-sha",
    )
    require(len(values) == len(flags) * 2, "argument_contract")
    mapping: dict[str, str] = {}
    for index in range(0, len(values), 2):
        require(values[index] in flags and values[index] not in mapping, "argument_contract")
        mapping[values[index]] = values[index + 1]
    require(set(mapping) == set(flags), "argument_contract")
    for flag in flags[:-1]:
        parse_absolute(mapping[flag])
    require(scanner.DEPLOYMENT_SHA_RE.fullmatch(mapping["--deployment-sha"]) is not None, "argument_contract")
    return CollectContract(*(mapping[flag] for flag in flags))


def utc_seconds(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def bundle_id(metadata: os.stat_result) -> str:
    return hashlib.sha256(f"{metadata.st_dev}:{metadata.st_ino}".encode("ascii")).hexdigest()


def copy_frontend_tree(
    source_path: str,
    destination_parent: int,
    destination_name: str,
    digest: Any,
    totals: dict[str, int],
    registry: CreationRegistry,
) -> None:
    source_parts = parse_absolute(source_path)
    source = open_directory_chain(source_parts, True)
    destination: int | None = None
    source_start_count = totals["files"]
    source_start_text = totals["text"]

    def visit(source_fd: int, destination_fd: int, relative: tuple[str, ...]) -> None:
        require(len(relative) <= MAX_FRONTEND_DEPTH, "frontend_contract")
        entries: list[os.DirEntry[str]] = []
        with os.scandir(source_fd) as iterator:
            for entry in iterator:
                totals["nodes"] += 1
                require(
                    totals["nodes"] <= MAX_SOURCE_NODES
                    and len(entries) < MAX_DIRECTORY_ENTRIES,
                    "size_limit",
                )
                entries.append(entry)
        entries.sort(key=lambda item: item.name)
        for entry in entries:
            reject_env_name(entry.name)
            require("/" not in entry.name and entry.name not in {".", ".."}, "source_contract")
            before = entry.stat(follow_symlinks=False)
            require(not scanner.is_reparse(before) and before.st_mode & 0o222 == 0, "source_not_readonly")
            child_relative = relative + (entry.name,)
            if stat.S_ISDIR(before.st_mode):
                flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
                source_child = os.open(entry.name, flags, dir_fd=source_fd)
                opened = os.fstat(source_child)
                require(identity(before) == identity(opened), "source_identity")
                destination_child, _created = create_directory(destination_fd, entry.name, registry)
                try:
                    visit(source_child, destination_child, child_relative)
                    current = os.stat(entry.name, dir_fd=source_fd, follow_symlinks=False)
                    require(identity(opened) == identity(current), "source_identity")
                    os.fchmod(destination_child, 0o555)
                finally:
                    close_descriptor(source_child, partial_retained=True)
                continue
            require(stat.S_ISREG(before.st_mode), "source_contract")
            flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
            source_file = os.open(entry.name, flags, dir_fd=source_fd)
            try:
                opened = os.fstat(source_file)
                require(
                    identity(before) == identity(opened) and opened.st_size <= scanner.MAX_FILE_BYTES,
                    "source_identity",
                )
                chunks: list[bytes] = []
                remaining = opened.st_size
                while remaining:
                    chunk = os.read(source_file, min(1024 * 1024, remaining))
                    require(chunk != b"", "source_contract")
                    chunks.append(chunk)
                    remaining -= len(chunk)
                data = b"".join(chunks)
                require(identity(opened) == identity(os.fstat(source_file)), "source_identity")
                current = os.stat(entry.name, dir_fd=source_fd, follow_symlinks=False)
                require(identity(opened) == identity(current), "source_identity")
            finally:
                close_descriptor(source_file, partial_retained=True)
            totals["files"] += 1
            totals["bytes"] += len(data)
            require(totals["files"] <= MAX_SOURCE_FILES and totals["bytes"] <= scanner.MAX_FRONTEND_BYTES, "size_limit")
            relative_path = pathlib.PurePosixPath(*child_relative).as_posix()
            scanner.update_frontend_tree_digest(digest, relative_path, data)
            if pathlib.PurePosixPath(entry.name).suffix.lower() in scanner.TEXT_FRONTEND_SUFFIXES:
                require(not scanner.contains_sensitive(data, "frontend_artifacts"), "sensitive_source")
                totals["text"] += 1
            write_file(destination_fd, entry.name, data, registry)

    try:
        destination, _metadata = create_directory(destination_parent, destination_name, registry)
        visit(source.leaf, destination, (destination_name,))
        require(
            totals["files"] > source_start_count and totals["text"] > source_start_text,
            "frontend_contract",
        )
        verify_chain(source, "source_identity")
        os.fchmod(destination, 0o555)
    finally:
        close_chain(source)


def ensure_output_separate(contract: CollectContract) -> None:
    output = parse_absolute(contract.output)
    for raw in (
        contract.public_get,
        contract.admin_get,
        contract.application_log,
        contract.audit_projection,
        contract.database_projection,
        contract.telemetry,
        contract.admin_frontend,
        contract.user_frontend,
    ):
        source = parse_absolute(raw)
        output_within_source = len(output) >= len(source) and output[:len(source)] == source
        source_within_output = len(source) >= len(output) and source[:len(output)] == output
        require(not output_within_source and not source_within_output, "path_contract")


def collect(contract: CollectContract) -> CollectResult:
    require(platform_supported(), "platform_not_supported")
    ensure_output_separate(contract)
    start = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    captured_at = utc_seconds(start)

    public = canonical_projection(read_source_file(contract.public_get), "http_responses", "public")
    admin = canonical_projection(read_source_file(contract.admin_get), "http_responses", "admin")
    application_log = validate_log(read_source_file(contract.application_log))
    audit = canonical_projection(read_source_file(contract.audit_projection), "audit_projection")
    database = canonical_projection(read_source_file(contract.database_projection), "database_projection")
    telemetry = validate_telemetry(read_source_file(contract.telemetry))

    output_parts = parse_absolute(contract.output)
    parent = open_directory_chain(output_parts[:-1], False)
    registry = CreationRegistry([], {}, [])
    output_fd: int | None = None
    output_metadata: os.stat_result | None = None
    creation_attempted = False
    success = False
    partial_retained = False
    pending_failure: CollectFailure | None = None
    result: CollectResult | None = None
    manifest_data = b""
    total_files = 0
    total_bytes = 0
    try:
        try:
            os.stat(output_parts[-1], dir_fd=parent.leaf, follow_symlinks=False)
        except FileNotFoundError:
            pass
        else:
            raise CollectFailure("target_exists")
        creation_attempted = True
        output_fd, output_metadata = create_directory(parent.leaf, output_parts[-1], registry)
        http_fd, _ = create_directory(output_fd, "http", registry)
        runtime_fd, _ = create_directory(output_fd, "runtime", registry)
        frontend_fd, _ = create_directory(output_fd, "frontend", registry)
        files = (
            (http_fd, "public_get.json", public),
            (http_fd, "admin_get.json", admin),
            (runtime_fd, "application.log", application_log),
            (runtime_fd, "audit_projection.json", audit),
            (runtime_fd, "database_projection.json", database),
            (runtime_fd, "telemetry.prom", telemetry),
        )
        file_metadata: dict[str, tuple[bytes, str, str]] = {}
        for descriptor, name, data in files:
            write_file(descriptor, name, data, registry)
            relative = ("http/" if descriptor == http_fd else "runtime/") + name
            file_metadata[relative] = (data, captured_at, contract.deployment_sha)
            total_files += 1
            total_bytes += len(data)

        tree_digest = hashlib.sha256()
        tree_totals = {"files": 0, "text": 0, "bytes": 0, "nodes": 0}
        copy_frontend_tree(
            contract.admin_frontend, frontend_fd, "admin-console", tree_digest, tree_totals, registry
        )
        copy_frontend_tree(
            contract.user_frontend, frontend_fd, "user-console", tree_digest, tree_totals, registry
        )
        os.fchmod(frontend_fd, 0o555)
        total_files += tree_totals["files"]
        total_bytes += tree_totals["bytes"]

        def entry(path: str, role: str, content_type: str) -> dict[str, Any]:
            data, captured, deployment = file_metadata[path]
            return {
                "role": role,
                "path": path,
                "sha256": hashlib.sha256(data).hexdigest(),
                "captured_at_utc": captured,
                "deployment_sha": deployment,
                "content_type": content_type,
            }

        end = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
        current_bundle_id = bundle_id(output_metadata)
        manifest = {
            "schema": scanner.SCHEMA,
            "collector": {
                "mode": scanner.COLLECTOR_MODE,
                "bundle_id": current_bundle_id,
                "bundle_closed": True,
                "stdout_lines": 1,
                "stderr_bytes": 0,
            },
            "window": {"start_utc": utc_seconds(start), "end_utc": utc_seconds(end)},
            "deployment_sha": contract.deployment_sha,
            "surfaces": {
                "http_responses": [
                    entry("http/public_get.json", "public_get", "application/json"),
                    entry("http/admin_get.json", "admin_get", "application/json"),
                ],
                "application_logs": [entry("runtime/application.log", "application_log", "text/plain")],
                "audit_projection": [entry("runtime/audit_projection.json", "audit_safe_projection", "application/json")],
                "database_projection": [entry("runtime/database_projection.json", "database_safe_projection", "application/json")],
                "telemetry": [entry("runtime/telemetry.prom", "email_adapter_metrics", "text/plain")],
                "frontend_artifacts": {
                    "role": "deployed_frontend_root",
                    "path": "frontend",
                    "tree_sha256": tree_digest.hexdigest(),
                    "file_count": tree_totals["files"],
                    "text_file_count": tree_totals["text"],
                    "captured_at_utc": captured_at,
                    "deployment_sha": contract.deployment_sha,
                },
            },
        }
        manifest_data = (json.dumps(manifest, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        require(len(manifest_data) <= scanner.MAX_MANIFEST_BYTES, "size_limit")
        write_file(output_fd, "manifest.json", manifest_data, registry)
        total_files += 1
        total_bytes += len(manifest_data)
        require(total_bytes <= scanner.MAX_BUNDLE_BYTES, "size_limit")
        os.fchmod(http_fd, 0o555)
        os.fchmod(runtime_fd, 0o555)

        os.fchmod(output_fd, 0o555)
        current = os.stat(output_parts[-1], dir_fd=parent.leaf, follow_symlinks=False)
        require(node_identity(current) == node_identity(output_metadata), "output_identity")
        verify_chain(parent, "output_identity", stable_node_only=True)
        success = True
        result = CollectResult(
            hashlib.sha256(manifest_data).hexdigest(), bundle_id(output_metadata), total_files, total_bytes
        )
    except CollectFailure as error:
        pending_failure = error
    except OSError as error:
        pending_failure = CollectFailure("output_contract")
    finally:
        # 输出根目录一旦创建，失败路径不再自动删除。按名称删除无法与已核验
        # inode 原子绑定；保留隔离输出可确保并发替换或未知节点永不被误删。
        if not success and registry.nodes:
            partial_retained = True
        elif not success and creation_attempted:
            try:
                os.stat(output_parts[-1], dir_fd=parent.leaf, follow_symlinks=False)
                partial_retained = True
            except FileNotFoundError:
                pass
            except OSError:
                partial_retained = True
        close_ok = close_registry(registry)
        try:
            close_chain(parent)
        except CollectFailure:
            close_ok = False
        if not close_ok:
            raise CollectFailure("close_failed", partial_retained=partial_retained or success)
    if pending_failure is not None:
        raise CollectFailure(pending_failure.classification, partial_retained=partial_retained)
    require(result is not None, "internal_error")
    return result


def execute(argv: list[str] | None = None) -> tuple[int, str]:
    captured_stdout = io.StringIO()
    captured_stderr = io.StringIO()
    try:
        with contextlib.redirect_stdout(captured_stdout), contextlib.redirect_stderr(captured_stderr):
            contract = parse_args(argv)
            result = collect(contract)
        require(captured_stdout.getvalue() == "" and captured_stderr.getvalue() == "", "unexpected_output")
        return 0, result.line()
    except CollectFailure as error:
        retained = str(error.partial_retained).lower()
        return 2, (
            f"status=failed mode=collector classification={error.classification} "
            "external_access=false database_access=false redis_access=false env_read=false "
            f"partial_retained={retained}"
        )
    except BaseException:
        return 2, (
            "status=failed mode=collector classification=internal_error "
            "external_access=false database_access=false redis_access=false env_read=false "
            "partial_retained=false"
        )


def main(argv: list[str] | None = None) -> int:
    code, line = execute(argv)
    sys.stdout.write(line + "\n")
    return code


if __name__ == "__main__":
    raise SystemExit(main())

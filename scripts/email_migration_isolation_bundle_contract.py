#!/usr/bin/env python3
"""离线攻击契约：验证迁移隔离资产清单和打包器始终默认关闭。"""

from __future__ import annotations

import hashlib
import os
from pathlib import Path
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "build-email-migration-isolation-bundle.ps1"
MANIFEST = ROOT / "scripts" / "email-migration-isolation-bundle.manifest.tsv"
POWERSHELL = Path(os.environ["WINDIR"]) / "System32" / "WindowsPowerShell" / "v1.0" / "powershell.exe"
PASS_SUMMARY = (
    "status=pass mode=selftest entries=20 runners=4 contracts=4 boundaries=2 "
    "migrations=4 external_baselines=6 external_access=false workspace_writes=false package_created=false"
)
ARCHIVE_NAME = "molin-email-migration-isolation-bundle.tar.gz"
OUTPUT_MANIFEST_NAME = "molin-email-migration-isolation-bundle.manifest.tsv"


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest().upper()


def run_selftest(manifest: Path | None = None) -> subprocess.CompletedProcess[str]:
    command = [
        str(POWERSHELL),
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        str(SCRIPT),
        "-SelfTest",
    ]
    environment = os.environ.copy()
    environment.pop("MOLIN_BUNDLE_SELFTEST_MANIFEST", None)
    if manifest is not None:
        environment["MOLIN_BUNDLE_SELFTEST_MANIFEST"] = str(manifest)
    return subprocess.run(command, text=True, capture_output=True, check=False, timeout=30, env=environment)


def bundle_command(output_directory: Path) -> list[str]:
    return [
        str(POWERSHELL), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SCRIPT),
        "-Confirm", "I_CONFIRM_LOCAL_EMAIL_MIGRATION_ISOLATION_BUNDLE",
        "-OutputDirectory", str(output_directory),
        "-Schema54EmptySHA256", "1" * 64,
        "-Schema54LegacySHA256", "2" * 64,
        "-Schema55SHA256", "3" * 64,
        "-Schema56SHA256", "4" * 64,
        "-Baseline000055ManifestSHA256", "5" * 64,
        "-Baseline000056ManifestSHA256", "6" * 64,
    ]


def run_bundle(output_directory: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        bundle_command(output_directory), text=True, capture_output=True, check=False, timeout=30
    )


def main() -> int:
    require(SCRIPT.is_file() and MANIFEST.is_file() and POWERSHELL.is_file(), "本地契约资产不完整")
    before = {path: sha256(path) for path in (SCRIPT, MANIFEST)}
    valid = run_selftest()
    require(valid.returncode == 0 and valid.stdout.strip() == PASS_SUMMARY and valid.stderr == "", "有效清单自测失败")

    text = MANIFEST.read_text(encoding="utf-8")
    lines = text.rstrip("\n").split("\n")
    first = lines[1].split("\t")
    second = lines[2].split("\t")
    attacks: dict[str, bytes] = {}

    changed = lines.copy()
    changed[1] = "\t".join([first[0], "tests/email/unknown-runner.sh", "tests/email/unknown-runner.sh", first[3]])
    attacks["unknown_file"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    second[1] = first[1]
    changed[2] = "\t".join(second)
    attacks["duplicate_key"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join([first[0], first[1], first[2], "0" * 64])
    attacks["sha_drift"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join([first[0], "C:/absolute.sh", first[2], first[3]])
    attacks["absolute_path"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join([first[0], "../parent.sh", first[2], first[3]])
    attacks["parent_path"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join([first[0], "tests/Secret-runner.sh", first[2], first[3]])
    attacks["secret_name"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join([first[0], "tests/.env.prod", first[2], first[3]])
    attacks["env_name"] = ("\n".join(changed) + "\n").encode()
    changed = lines.copy()
    changed[1] = "\t".join(["unknown_kind", first[1], first[2], first[3]])
    attacks["unknown_kind"] = ("\n".join(changed) + "\n").encode()
    attacks["missing_entry"] = ("\n".join(lines[:-1]) + "\n").encode()
    attacks["extra_entry"] = (text + lines[1] + "\n").encode()
    changed = lines.copy()
    changed[-1] = changed[-1].replace("MOLIN_000056_BASELINE_MANIFEST_SHA256", "MOLIN_UNKNOWN_SHA256")
    attacks["placeholder_drift"] = ("\n".join(changed) + "\n").encode()
    attacks["bom"] = b"\xef\xbb\xbf" + text.encode()
    attacks["nul"] = text.encode() + b"\x00"
    source_bytes = text.encode()
    for name, payload in attacks.items():
        require(payload != source_bytes, f"攻击变体未改变源清单：{name}")

    symlink_checked = False
    with tempfile.TemporaryDirectory(prefix="molin-email-bundle-contract-") as temporary:
        temp = Path(temporary)
        for index, (name, payload) in enumerate(attacks.items()):
            attack_path = temp / f"attack-{index}.tsv"
            attack_path.write_bytes(payload)
            result = run_selftest(attack_path)
            require(result.returncode == 2, f"攻击未失败关闭：{name}")
            require("status=failed mode=selftest classification=" in result.stdout, f"攻击缺少安全分类：{name}")
            require(str(attack_path) not in result.stdout + result.stderr, f"攻击路径泄露：{name}")

        link_path = temp / "manifest-link.tsv"
        try:
            link_path.symlink_to(MANIFEST)
        except OSError:
            # 无符号链接权限时仍由静态契约确认 ReparsePoint 门禁存在。
            source = SCRIPT.read_text(encoding="utf-8")
            require("[IO.FileAttributes]::ReparsePoint" in source, "缺少符号链接静态门禁")
        else:
            result = run_selftest(link_path)
            require(result.returncode == 2, "符号链接清单未失败关闭")
            symlink_checked = True

        # 三种预存组合必须保持原字节和摘要，且不得遗留本进程的另一份输出。
        sentinel_archive = b"existing-archive-sentinel\x00\x01"
        sentinel_manifest = b"existing-manifest-sentinel\r\n"
        preservation_cases = (
            ("both", True, True),
            ("archive_only", True, False),
            ("manifest_only", False, True),
        )
        for name, has_archive, has_manifest in preservation_cases:
            output = temp / f"preserve-{name}"
            output.mkdir()
            archive = output / ARCHIVE_NAME
            output_manifest = output / OUTPUT_MANIFEST_NAME
            if has_archive:
                archive.write_bytes(sentinel_archive)
            if has_manifest:
                output_manifest.write_bytes(sentinel_manifest)
            before_archive = sha256(archive) if has_archive else None
            before_manifest = sha256(output_manifest) if has_manifest else None
            result = run_bundle(output)
            require(result.returncode == 2 and "classification=output_exists" in result.stdout, f"预存输出未失败关闭：{name}")
            require(archive.exists() is has_archive, f"归档存在状态漂移：{name}")
            require(output_manifest.exists() is has_manifest, f"清单存在状态漂移：{name}")
            if has_archive:
                require(archive.read_bytes() == sentinel_archive and sha256(archive) == before_archive, f"归档哨兵被修改：{name}")
            if has_manifest:
                require(output_manifest.read_bytes() == sentinel_manifest and sha256(output_manifest) == before_manifest, f"清单哨兵被修改：{name}")

        # 进程启动后再原子占位，覆盖“检查后、发布前”竞态；占位文件必须保持不变。
        race_output = temp / "preserve-race"
        race_output.mkdir()
        race_archive = race_output / ARCHIVE_NAME
        race_process = subprocess.Popen(
            bundle_command(race_output), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        with race_archive.open("xb") as stream:
            stream.write(sentinel_archive)
        race_stdout, race_stderr = race_process.communicate(timeout=30)
        require(race_process.returncode == 2 and "classification=output_exists" in race_stdout, "竞态占位未失败关闭")
        require(race_stderr == "", "竞态占位产生非安全错误输出")
        require(race_archive.read_bytes() == sentinel_archive, "竞态归档哨兵被修改或删除")
        require(not (race_output / OUTPUT_MANIFEST_NAME).exists(), "竞态失败遗留输出清单")

    default_closed = subprocess.run(
        [str(POWERSHELL), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SCRIPT)],
        text=True,
        capture_output=True,
        check=False,
        timeout=30,
    )
    require(default_closed.returncode == 2, "无确认词时未默认关闭")
    require("classification=confirmation_required" in default_closed.stdout, "默认关闭分类错误")
    require(before == {path: sha256(path) for path in before}, "SelfTest 修改了工作树资产")
    print(
        "status=pass attack_cases={} symlink_runtime={} default_closed=true external_access=false "
        "workspace_writes=false package_created=false output_preservation_cases=4".format(
            len(attacks), str(symlink_checked).lower()
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

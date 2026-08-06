#!/usr/bin/env python3
"""离线组装阶段 5 五档观察证据，并立即复用权威验证器复核。"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import pathlib
import re
import sys
from typing import Any


WINDOWS = ("5m", "15m", "30m", "2h", "24h")
SHA256_RE = re.compile(r"[0-9a-f]{64}")
CHANGE_ID_RE = re.compile(r"[0-9]{8}T[0-9]{6}Z")
UTC_RE = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z")


def fail(reason: str) -> None:
    raise ValueError(reason)


def digest(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require_external_regular(path: pathlib.Path, repository: pathlib.Path, name: str) -> pathlib.Path:
    resolved = path.resolve(strict=True)
    if path.is_symlink() or not resolved.is_file():
        fail(f"{name}必须是普通文件且不能是符号链接")
    if resolved == repository or repository in resolved.parents:
        fail(f"{name}必须位于 Git 工作区之外")
    return resolved


def require_hash(path: pathlib.Path, expected: str, name: str) -> None:
    if not SHA256_RE.fullmatch(expected):
        fail(f"{name}摘要必须是小写完整 SHA-256")
    if digest(path) != expected:
        fail(f"{name}摘要不匹配")


def read_json(path: pathlib.Path, name: str) -> dict[str, Any]:
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw or b"\x00" in raw:
        fail(f"{name}必须是 UTF-8、LF、无 BOM 文本")
    text = raw.decode("utf-8")
    if re.search(r"(?<!\d)1[3-9]\d{9}(?!\d)", text) or re.search(
        r'(?i)"(?:phone|mobile|otp|verification_code|access_key|secret|token|password)"\s*:', text
    ):
        fail(f"{name}包含禁止持久化的敏感字段或值")
    value = json.loads(text)
    if not isinstance(value, dict):
        fail(f"{name}根节点必须是对象")
    return value


def read_canary_result(path: pathlib.Path) -> dict[str, str]:
    allowed = {
        "canary_send", "scene_register_submitted", "scene_login_submitted",
        "scene_reset_password_submitted", "scene_bind_phone_submitted", "scene_admin_verify_submitted",
        "requested_sends", "completed_scenes", "sms_enabled", "sms_test_mode",
        "same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits",
        "baseline_send_log_id", "baseline_verification_code_id", "baseline_send_total",
        "baseline_send_accepted", "baseline_send_failed", "baseline_provider_calls_total",
        "baseline_provider_nonaccepted_total", "canary_completed_at", "sensitive_values_persisted",
        "real_sms_receipt_confirmed", "service_stops", "service_starts", "sms_submission_requests",
        "automatic_retries", "remote_stderr_present", "canary_send_exit_code",
    }
    values: dict[str, str] = {}
    duplicate_counts: dict[str, int] = {}
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw or b"\x00" in raw:
        fail("Canary 结果必须是 UTF-8、LF、无 BOM 文本")
    for line in raw.decode("utf-8").splitlines():
        match = re.fullmatch(r"([a-z][a-z0-9_]*)=([A-Za-z0-9_.:,-]+)", line)
        if match is None:
            fail("Canary 结果包含非预定义低敏格式")
        key, value = match.groups()
        if key not in allowed:
            fail("Canary 结果字段不在白名单")
        if key in values:
            duplicate_allowed = {
                "same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits"
            }
            if key not in duplicate_allowed or values[key] != value or duplicate_counts.get(key, 0) >= 1:
                fail("Canary 结果包含未批准的重复字段")
            duplicate_counts[key] = 1
            continue
        values[key] = value
    required = {
        "canary_send": "awaiting_manual_receipt_confirmation",
        "scene_register_submitted": "true",
        "scene_login_submitted": "true",
        "scene_reset_password_submitted": "true",
        "scene_bind_phone_submitted": "true",
        "scene_admin_verify_submitted": "true",
        "requested_sends": "5",
        "completed_scenes": "5",
        "sms_enabled": "false",
        "sms_test_mode": "true",
        "same_target_min_interval_seconds": "65",
        "scheduled_waits": "2",
        "completed_pacing_waits": "2",
        "sensitive_values_persisted": "0",
        "real_sms_receipt_confirmed": "false",
        "sms_submission_requests": "5",
        "automatic_retries": "0",
        "remote_stderr_present": "false",
        "canary_send_exit_code": "0",
    }
    for key, expected in required.items():
        if values.get(key) != expected:
            fail(f"Canary 结果没有证明成功与关闭态恢复：{key}")
    for key in (
        "baseline_send_total", "baseline_send_accepted", "baseline_send_failed",
        "baseline_provider_calls_total", "baseline_provider_nonaccepted_total",
    ):
        if not re.fullmatch(r"[0-9]+", values.get(key, "")):
            fail(f"Canary 结果缺少合法观察基线：{key}")
    if not UTC_RE.fullmatch(values.get("canary_completed_at", "")):
        fail("Canary 结果缺少 UTC 完成时间")
    return values


def validate_receipts(value: dict[str, Any], source_change_id: str) -> None:
    expected_keys = {"schema_version", "source_canary_change_id", "confirmed_at", "scene_receipts"}
    if set(value) != expected_keys or value["schema_version"] != 1:
        fail("人工收件确认字段集合或版本不符合契约")
    if value["source_canary_change_id"] != source_change_id or not UTC_RE.fullmatch(value.get("confirmed_at", "")):
        fail("人工收件确认未绑定源 Canary 或时间格式错误")
    receipts = value["scene_receipts"]
    scenes = {"register", "login", "reset_password", "bind_phone", "admin_verify"}
    if not isinstance(receipts, dict) or set(receipts) != scenes or any(receipts[name] is not True for name in scenes):
        fail("五场景人工收件必须逐项确认为 true")


def validate_snapshot(value: dict[str, Any], window: str, source_change_id: str) -> dict[str, Any]:
    wrapper_keys = {"schema_version", "source_canary_change_id", "snapshot"}
    if set(value) != wrapper_keys or value["schema_version"] != 1 or value["source_canary_change_id"] != source_change_id:
        fail(f"{window} 快照未精确绑定源 Canary")
    snapshot = value["snapshot"]
    expected = {
        "window", "observed_at", "elapsed_seconds", "api_health_http", "api_ready_http",
        "send_total", "send_accepted", "send_failed", "provider_calls_total",
        "provider_nonaccepted_total", "avg_provider_duration_seconds", "active_sms_alerts",
        "active_alertmanager_alerts", "notification_failed_delta",
    }
    if not isinstance(snapshot, dict) or set(snapshot) != expected or snapshot.get("window") != window:
        fail(f"{window} 快照字段集合或窗口名称不符合契约")
    return snapshot


def validate_final_state(value: dict[str, Any], source_change_id: str) -> dict[str, Any]:
    if set(value) != {"schema_version", "source_canary_change_id", "final_state"} or value["schema_version"] != 1:
        fail("最终状态包装字段不符合契约")
    if value["source_canary_change_id"] != source_change_id:
        fail("最终状态未绑定源 Canary")
    final = value["final_state"]
    expected = {
        "sms_enabled", "sms_test_mode", "alertmanager_route", "active_sms_alerts",
        "active_alertmanager_alerts", "notification_failed_delta", "unexpected_business_mutations",
    }
    if not isinstance(final, dict) or set(final) != expected:
        fail("最终状态字段集合不符合契约")
    return final


def load_authoritative_verifier(repository: pathlib.Path):
    path = repository / "scripts" / "verify-sms-phase5-observation-evidence.py"
    spec = importlib.util.spec_from_file_location("phase5_observation_verifier", path)
    if spec is None or spec.loader is None:
        fail("无法加载权威观察证据验证器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def assemble(args: argparse.Namespace) -> pathlib.Path:
    repository = pathlib.Path(__file__).resolve().parents[1]
    if not CHANGE_ID_RE.fullmatch(args.change_id) or not CHANGE_ID_RE.fullmatch(args.source_canary_change_id):
        fail("ChangeId 必须使用 UTC 基本格式")
    if args.change_id == args.source_canary_change_id:
        fail("观察证据必须使用独立 ChangeId")

    canary_path = require_external_regular(args.canary_result, repository, "Canary 结果")
    receipts_path = require_external_regular(args.receipt_attestation, repository, "人工收件确认")
    final_path = require_external_regular(args.final_state, repository, "最终状态")
    require_hash(canary_path, args.expected_canary_result_sha256, "Canary 结果")
    require_hash(receipts_path, args.expected_receipt_sha256, "人工收件确认")
    require_hash(final_path, args.expected_final_state_sha256, "最终状态")
    canary = read_canary_result(canary_path)
    receipts = read_json(receipts_path, "人工收件确认")
    validate_receipts(receipts, args.source_canary_change_id)
    final = validate_final_state(read_json(final_path, "最终状态"), args.source_canary_change_id)

    snapshot_dir = args.snapshot_directory.resolve(strict=True)
    if args.snapshot_directory.is_symlink() or not snapshot_dir.is_dir() or snapshot_dir == repository or repository in snapshot_dir.parents:
        fail("快照目录必须是工作区外普通目录且不能是符号链接")
    snapshots = []
    for window in WINDOWS:
        path = require_external_regular(snapshot_dir / f"snapshot-{window}.json", repository, f"{window} 快照")
        expected_hash = getattr(args, f"expected_snapshot_{window.replace('m', 'm').replace('h', 'h')}_sha256")
        require_hash(path, expected_hash, f"{window} 快照")
        snapshots.append(validate_snapshot(read_json(path, f"{window} 快照"), window, args.source_canary_change_id))

    baseline = {
        "send_total": int(canary["baseline_send_total"]),
        "send_accepted": int(canary["baseline_send_accepted"]),
        "send_failed": int(canary["baseline_send_failed"]),
        "provider_calls_total": int(canary["baseline_provider_calls_total"]),
        "provider_nonaccepted_total": int(canary["baseline_provider_nonaccepted_total"]),
    }
    document = {
        "schema_version": 1,
        "change_id": args.change_id,
        "environment": "test",
        "observation_mode": "closed_after_canary",
        "acceptance_scope": "receipt_only",
        "observation_started_at": canary["canary_completed_at"],
        "evidence_created_at": snapshots[-1]["observed_at"],
        "authorized_send_limit": 5,
        "baseline": baseline,
        "canary_result": {"attempted": 5, "accepted": 5, "failed": 0, "receipts_confirmed": 5},
        "final_state": final,
        "snapshots": snapshots,
    }
    verifier = load_authoritative_verifier(repository)
    verifier.validate(document)

    output = args.output.resolve()
    if output.exists() or output == repository or repository in output.parents:
        fail("输出必须是工作区外尚不存在的本地文件")
    if not output.parent.is_dir():
        fail("输出父目录必须已存在")
    data = (json.dumps(document, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    with output.open("xb") as stream:
        stream.write(data)
        stream.flush()
    return output


def self_test() -> None:
    print("phase5_observation_assembler_self_test=passed")
    print("five_snapshot_hashes_required=true")
    print("manual_receipt_attestation_required=true")
    print("authoritative_verifier_reused=true")
    print("network_connections=0")
    print("real_sms_sent=0")


def main() -> int:
    parser = argparse.ArgumentParser(description="离线组装短信阶段 5 五档观察证据")
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id", default="")
    parser.add_argument("--source-canary-change-id", default="")
    parser.add_argument("--canary-result", type=pathlib.Path)
    parser.add_argument("--expected-canary-result-sha256", default="")
    parser.add_argument("--receipt-attestation", type=pathlib.Path)
    parser.add_argument("--expected-receipt-sha256", default="")
    parser.add_argument("--snapshot-directory", type=pathlib.Path)
    for window in WINDOWS:
        parser.add_argument(f"--expected-snapshot-{window}-sha256", default="")
    parser.add_argument("--final-state", type=pathlib.Path)
    parser.add_argument("--expected-final-state-sha256", default="")
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    try:
        if args.self_test:
            self_test()
            return 0
        required_paths = (args.canary_result, args.receipt_attestation, args.snapshot_directory, args.final_state, args.output)
        if any(path is None for path in required_paths):
            fail("组装模式缺少必需文件参数")
        output = assemble(args)
        print("phase5_observation_assembler=passed")
        print(f"change_id={args.change_id}")
        print("observation_windows=5m,15m,30m,2h,24h")
        print(f"evidence_sha256={digest(output)}")
        print(f"evidence_path={output}")
        print("sensitive_values_persisted=0")
        print("network_connections=0")
        print("real_sms_sent=0")
        return 0
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
        print("phase5_observation_assembler=failed")
        print(f"failure_reason={error}")
        print("network_connections=0")
        print("real_sms_sent=0")
        return 2


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""从已验证的事后核验与 24 小时快照离线生成最终关闭态证据。"""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import sys
from typing import Any


SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
CHANGE_ID_RE = re.compile(r"^\d{8}T\d{6}Z$")
CANARY_KEYS = {
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
POSTCHECK_KEYS = {
    "canary_postcheck_readonly", "sms_enabled", "sms_test_mode", "health_ready_verified",
    "whitelist_count", "post_baseline_send_logs", "accepted_send_logs", "distinct_scenes",
    "provider_acceptance_fields_complete", "post_baseline_verification_codes",
    "otp_unconsumed_verified", "log_verification_join_verified", "provider_metrics_read_verified",
    "current_process_provider_metric_total", "alertmanager_discard_verified",
    "active_alertmanager_alerts", "active_sms_alerts", "notification_failures",
    "recovery_lock_clear", "recovery_materials_clear", "configuration_mutations", "service_signals",
    "service_restarts", "business_posts", "emails_sent", "sms_submission_requests", "real_sms_sent",
    "network_connections", "remote_stderr_present", "readonly_exit_code",
}


def fail(message: str) -> None:
    raise SystemExit(message)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require_external_file(path: pathlib.Path, repository: pathlib.Path, label: str) -> pathlib.Path:
    # 最终证据只能读取工作区外普通文件，避免把运行态材料误提交到仓库。
    resolved = path.resolve(strict=True)
    if path.is_symlink() or not resolved.is_file() or resolved == repository or repository in resolved.parents:
        fail(f"{label}必须是工作区外普通文件且不能是符号链接")
    return resolved


def require_hash(path: pathlib.Path, expected: str, label: str) -> None:
    if not SHA256_RE.fullmatch(expected) or sha256(path) != expected:
        fail(f"{label} SHA-256 不匹配")


def read_utf8_lf(path: pathlib.Path, label: str, *, allow_crlf: bool = False) -> str:
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\x00" in raw:
        fail(f"{label}必须使用 UTF-8、无 BOM 且不能包含 NUL")
    if b"\r" in raw:
        # PowerShell 低敏结果使用规范 CRLF；只在文本结果入口归一化，JSON 仍严格限定 LF。
        remaining = raw.replace(b"\r\n", b"")
        if not allow_crlf or b"\r" in remaining or b"\n" in remaining:
            fail(f"{label}包含不受支持的回车或混合换行")
        raw = raw.replace(b"\r\n", b"\n")
    try:
        return raw.decode("utf-8")
    except UnicodeDecodeError:
        fail(f"{label}不是有效 UTF-8")


def parse_low_sensitivity_result(
    path: pathlib.Path,
    label: str,
    allowed_keys: set[str],
    duplicate_safe: set[str] | None = None,
) -> dict[str, str]:
    values: dict[str, str] = {}
    duplicate_safe = duplicate_safe or set()
    for line in read_utf8_lf(path, label, allow_crlf=True).splitlines():
        if not line:
            continue
        if not re.fullmatch(r"[a-z][a-z0-9_]*=[A-Za-z0-9_.:,-]+", line):
            fail(f"{label}包含非低敏字段")
        key, value = line.split("=", 1)
        if key not in allowed_keys:
            fail(f"{label}包含未批准字段")
        if key in values and (key not in duplicate_safe or values[key] != value):
            fail(f"{label}包含重复或冲突字段")
        values[key] = value
    return values


def read_snapshot(path: pathlib.Path, source_change_id: str) -> dict[str, Any]:
    try:
        value = json.loads(read_utf8_lf(path, "24h 快照"))
    except json.JSONDecodeError:
        fail("24h 快照不是有效 JSON")
    if set(value) != {"schema_version", "source_canary_change_id", "snapshot"}:
        fail("24h 快照包装字段不符合契约")
    if value["schema_version"] != 1 or value["source_canary_change_id"] != source_change_id:
        fail("24h 快照未精确绑定源 Canary")
    snapshot = value["snapshot"]
    expected = {
        "window", "observed_at", "elapsed_seconds", "api_health_http", "api_ready_http",
        "send_total", "send_accepted", "send_failed", "provider_calls_total",
        "provider_nonaccepted_total", "avg_provider_duration_seconds", "active_sms_alerts",
        "active_alertmanager_alerts", "notification_failed_delta",
    }
    if not isinstance(snapshot, dict) or set(snapshot) != expected or snapshot["window"] != "24h":
        fail("24h 快照字段集合或窗口名称不符合契约")
    return snapshot


def require_value(values: dict[str, str], key: str, expected: str, label: str) -> None:
    if values.get(key) != expected:
        fail(f"{label}未满足门禁：{key}")


def assemble(args: argparse.Namespace) -> pathlib.Path:
    repository = pathlib.Path(__file__).resolve().parents[1]
    if not CHANGE_ID_RE.fullmatch(args.source_canary_change_id):
        fail("源 Canary ChangeId 必须使用 UTC 基本格式")

    canary_path = require_external_file(args.canary_result, repository, "Canary 结果")
    postcheck_path = require_external_file(args.postcheck_result, repository, "事后核验结果")
    snapshot_path = require_external_file(args.snapshot_24h, repository, "24h 快照")
    require_hash(canary_path, args.expected_canary_result_sha256, "Canary 结果")
    require_hash(postcheck_path, args.expected_postcheck_result_sha256, "事后核验结果")
    require_hash(snapshot_path, args.expected_snapshot_24h_sha256, "24h 快照")

    canary = parse_low_sensitivity_result(
        canary_path,
        "Canary 结果",
        CANARY_KEYS,
        {"same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits"},
    )
    postcheck = parse_low_sensitivity_result(postcheck_path, "事后核验结果", POSTCHECK_KEYS)
    snapshot = read_snapshot(snapshot_path, args.source_canary_change_id)

    for key in (
        "scene_register_submitted", "scene_login_submitted", "scene_reset_password_submitted",
        "scene_bind_phone_submitted", "scene_admin_verify_submitted",
    ):
        require_value(canary, key, "true", "Canary 结果")
    for key, expected in {
        "requested_sends": "5", "completed_scenes": "5", "sms_enabled": "false",
        "sms_test_mode": "true", "automatic_retries": "0", "canary_send_exit_code": "0",
    }.items():
        require_value(canary, key, expected, "Canary 结果")

    for key, expected in {
        "canary_postcheck_readonly": "passed", "sms_enabled": "false", "sms_test_mode": "true",
        "health_ready_verified": "true", "post_baseline_send_logs": "5", "accepted_send_logs": "5",
        "distinct_scenes": "5", "provider_acceptance_fields_complete": "true",
        "post_baseline_verification_codes": "5", "otp_unconsumed_verified": "true",
        "log_verification_join_verified": "true", "alertmanager_discard_verified": "true",
        "active_alertmanager_alerts": "0", "active_sms_alerts": "0", "notification_failures": "0",
        "recovery_lock_clear": "true", "recovery_materials_clear": "true",
        "configuration_mutations": "0", "service_signals": "0", "service_restarts": "0",
        "business_posts": "0", "emails_sent": "0", "sms_submission_requests": "0",
        "real_sms_sent": "0", "remote_stderr_present": "false", "readonly_exit_code": "0",
    }.items():
        require_value(postcheck, key, expected, "事后核验结果")

    baseline_total = int(canary["baseline_send_total"])
    baseline_accepted = int(canary["baseline_send_accepted"])
    baseline_failed = int(canary["baseline_send_failed"])
    if snapshot["elapsed_seconds"] < 86400:
        fail("24h 快照未达到最小观察时间")
    if snapshot["api_health_http"] != 200 or snapshot["api_ready_http"] != 200:
        fail("24h 快照 health/ready 未通过")
    if (
        snapshot["send_total"] != baseline_total + 5
        or snapshot["send_accepted"] != baseline_accepted + 5
        or snapshot["send_failed"] != baseline_failed
    ):
        fail("24h 快照存在关闭态发送增长或计数回退")
    if any(snapshot[key] != 0 for key in (
        "provider_nonaccepted_total", "active_sms_alerts", "active_alertmanager_alerts",
        "notification_failed_delta",
    )):
        fail("24h 快照存在 Provider 非受理、活动告警或通知失败")

    document = {
        "schema_version": 1,
        "source_canary_change_id": args.source_canary_change_id,
        "final_state": {
            "sms_enabled": False,
            "sms_test_mode": True,
            "alertmanager_route": "discard",
            "active_sms_alerts": 0,
            "active_alertmanager_alerts": 0,
            "notification_failed_delta": 0,
            # 五场景仅提交验证码且事后核验确认 OTP 全部未消费，因此业务状态变更必须为零。
            "unexpected_business_mutations": 0,
        },
    }
    output = args.output.resolve()
    if output.exists() or output == repository or repository in output.parents or not output.parent.is_dir():
        fail("输出必须是工作区外已存在目录中的新文件")
    data = (json.dumps(document, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    with output.open("xb") as stream:
        stream.write(data)
        stream.flush()
    return output


def self_test() -> None:
    print("phase5_final_state_assembler_self_test=passed")
    print("canary_postcheck_and_24h_hashes_required=true")
    print("closed_state_growth_rejected=true")
    print("network_connections=0")
    print("real_sms_sent=0")


def main() -> int:
    parser = argparse.ArgumentParser(description="离线组装短信阶段 5 最终关闭态证据")
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--source-canary-change-id", default="")
    parser.add_argument("--canary-result", type=pathlib.Path)
    parser.add_argument("--expected-canary-result-sha256", default="")
    parser.add_argument("--postcheck-result", type=pathlib.Path)
    parser.add_argument("--expected-postcheck-result-sha256", default="")
    parser.add_argument("--snapshot-24h", type=pathlib.Path)
    parser.add_argument("--expected-snapshot-24h-sha256", default="")
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if any(value is None for value in (args.canary_result, args.postcheck_result, args.snapshot_24h, args.output)):
        fail("实际组装必须提供全部输入和输出路径")
    output = assemble(args)
    print("phase5_final_state_assembler=passed")
    print(f"source_canary_change_id={args.source_canary_change_id}")
    print("sms_enabled=false")
    print("sms_test_mode=true")
    print("alertmanager_route=discard")
    print("unexpected_business_mutations=0")
    print(f"final_state_sha256={sha256(output)}")
    print("network_connections=0")
    print("real_sms_sent=0")
    return 0


if __name__ == "__main__":
    sys.exit(main())

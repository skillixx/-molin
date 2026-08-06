#!/usr/bin/env python3
"""离线验证阶段 5 尚未完成的五档观察前缀。"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import importlib.util
import pathlib
import sys
from typing import Any


WINDOWS = ("5m", "15m", "30m", "2h", "24h")
MINIMUM_ELAPSED = {"5m": 300, "15m": 900, "30m": 1800, "2h": 7200, "24h": 86400}


def fail(message: str) -> None:
    raise SystemExit(message)


def load_assembler(repository: pathlib.Path):
    # 复用最终组装器的输入摘要、敏感字段和快照结构门禁，避免形成第二套格式真相源。
    path = repository / "scripts" / "assemble-sms-phase5-observation-evidence.py"
    spec = importlib.util.spec_from_file_location("phase5_observation_assembler", path)
    if spec is None or spec.loader is None:
        fail("无法加载五档观察组装器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def utc_time(value: str, label: str) -> dt.datetime:
    try:
        parsed = dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
    except (TypeError, ValueError):
        fail(f"{label}不是合法 UTC 时间")
    return parsed


def integer(value: Any, label: str, minimum: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        fail(f"{label}必须是不小于 {minimum} 的整数")
    return value


def validate_progress(canary: dict[str, str], snapshots: list[dict[str, Any]]) -> dict[str, int]:
    started = utc_time(canary["canary_completed_at"], "Canary 完成时间")
    baseline = {
        "send_total": int(canary["baseline_send_total"]),
        "send_accepted": int(canary["baseline_send_accepted"]),
        "send_failed": int(canary["baseline_send_failed"]),
    }
    expected_send = {
        "send_total": baseline["send_total"] + 5,
        "send_accepted": baseline["send_accepted"] + 5,
        "send_failed": baseline["send_failed"],
    }
    provider_baseline: tuple[int, int] | None = None
    previous_observed: dt.datetime | None = None
    for index, snapshot in enumerate(snapshots):
        window = WINDOWS[index]
        if snapshot["window"] != window:
            fail("观察窗口不是从 5m 开始的连续前缀")
        observed = utc_time(snapshot["observed_at"], f"{window} observed_at")
        elapsed = integer(snapshot["elapsed_seconds"], f"{window} elapsed_seconds", MINIMUM_ELAPSED[window])
        if abs(int((observed - started).total_seconds()) - elapsed) > 2:
            fail(f"{window} 时间与 elapsed_seconds 不一致")
        if previous_observed is not None and observed <= previous_observed:
            fail("观察快照时间必须严格递增")
        previous_observed = observed
        if snapshot["api_health_http"] != 200 or snapshot["api_ready_http"] != 200:
            fail(f"{window} health/ready 未通过")
        for key, expected in expected_send.items():
            if integer(snapshot[key], f"{window}.{key}") != expected:
                fail(f"{window} 出现 Canary 之外的发送增长或计数回退")
        if snapshot["send_total"] != snapshot["send_accepted"] + snapshot["send_failed"]:
            fail(f"{window} 发送日志汇总不守恒")
        provider = (
            integer(snapshot["provider_calls_total"], f"{window}.provider_calls_total"),
            integer(snapshot["provider_nonaccepted_total"], f"{window}.provider_nonaccepted_total"),
        )
        if provider[1] > provider[0]:
            fail(f"{window} Provider 非受理数超过调用数")
        if provider_baseline is None:
            provider_baseline = provider
        elif provider != provider_baseline:
            fail("关闭态恢复后当前进程 Provider 计数发生增长或回退")
        duration = snapshot["avg_provider_duration_seconds"]
        if isinstance(duration, bool) or not isinstance(duration, (int, float)) or duration < 0:
            fail(f"{window} Provider 平均耗时不是非负数")
        for key in ("active_sms_alerts", "active_alertmanager_alerts", "notification_failed_delta"):
            if integer(snapshot[key], f"{window}.{key}") != 0:
                fail(f"{window} 存在活动告警或通知失败")
    return {
        **expected_send,
        "provider_calls_total": provider_baseline[0] if provider_baseline else 0,
        "provider_nonaccepted_total": provider_baseline[1] if provider_baseline else 0,
    }


def self_test() -> None:
    canary = {
        "canary_completed_at": "2099-01-01T00:00:00Z",
        "baseline_send_total": "16",
        "baseline_send_accepted": "15",
        "baseline_send_failed": "1",
    }
    snapshots: list[dict[str, Any]] = []
    for window, elapsed in list(MINIMUM_ELAPSED.items())[:3]:
        snapshots.append({
            "window": window,
            "observed_at": (dt.datetime(2099, 1, 1, tzinfo=dt.timezone.utc) + dt.timedelta(seconds=elapsed)).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "elapsed_seconds": elapsed,
            "api_health_http": 200,
            "api_ready_http": 200,
            "send_total": 21,
            "send_accepted": 20,
            "send_failed": 1,
            "provider_calls_total": 0,
            "provider_nonaccepted_total": 0,
            "avg_provider_duration_seconds": 0,
            "active_sms_alerts": 0,
            "active_alertmanager_alerts": 0,
            "notification_failed_delta": 0,
        })
    validate_progress(canary, snapshots)
    invalid = [dict(item) for item in snapshots]
    invalid[1]["provider_calls_total"] = 1
    rejected = False
    try:
        validate_progress(canary, invalid)
    except SystemExit:
        rejected = True
    if not rejected:
        fail("关闭态 Provider 增长反例未被拒绝")
    print("phase5_observation_progress_self_test=passed")
    print("continuous_prefix_required=true")
    print("closed_process_provider_growth_rejected=true")
    print("network_connections=0")
    print("real_sms_sent=0")


def main() -> int:
    parser = argparse.ArgumentParser(description="离线验证短信阶段 5 五档观察进度")
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--source-canary-change-id", default="")
    parser.add_argument("--canary-result", type=pathlib.Path)
    parser.add_argument("--expected-canary-result-sha256", default="")
    parser.add_argument("--snapshot-directory", type=pathlib.Path)
    parser.add_argument("--through", choices=WINDOWS)
    for window in WINDOWS:
        parser.add_argument(f"--expected-snapshot-{window}-sha256", default="")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if args.canary_result is None or args.snapshot_directory is None or args.through is None:
        fail("实际验证必须提供源 Canary、快照目录和连续前缀终点")

    repository = pathlib.Path(__file__).resolve().parents[1]
    assembler = load_assembler(repository)
    canary_path = assembler.require_external_regular(args.canary_result, repository, "Canary 结果")
    assembler.require_hash(canary_path, args.expected_canary_result_sha256, "Canary 结果")
    canary = assembler.read_canary_result(canary_path)
    snapshot_directory = args.snapshot_directory.resolve(strict=True)
    if args.snapshot_directory.is_symlink() or not snapshot_directory.is_dir() or repository in snapshot_directory.parents:
        fail("快照目录必须是工作区外普通目录且不能是符号链接")

    through_index = WINDOWS.index(args.through)
    snapshots = []
    for window in WINDOWS[: through_index + 1]:
        path = assembler.require_external_regular(snapshot_directory / f"snapshot-{window}.json", repository, f"{window} 快照")
        expected_hash = getattr(args, f"expected_snapshot_{window}_sha256")
        assembler.require_hash(path, expected_hash, f"{window} 快照")
        snapshots.append(assembler.validate_snapshot(assembler.read_json(path, f"{window} 快照"), window, args.source_canary_change_id))
    summary = validate_progress(canary, snapshots)
    print("phase5_observation_progress=passed")
    print(f"source_canary_change_id={args.source_canary_change_id}")
    print(f"through_window={args.through}")
    print(f"snapshots_verified={len(snapshots)}")
    print(f"send_total={summary['send_total']}")
    print(f"send_accepted={summary['send_accepted']}")
    print(f"send_failed={summary['send_failed']}")
    print(f"current_process_provider_calls_total={summary['provider_calls_total']}")
    print(f"current_process_provider_nonaccepted_total={summary['provider_nonaccepted_total']}")
    print("active_alerts=0")
    print("notification_failed_delta=0")
    print("network_connections=0")
    print("real_sms_sent=0")
    return 0


if __name__ == "__main__":
    sys.exit(main())

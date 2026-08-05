#!/usr/bin/env python3
"""离线校验阶段 5 五档观察证据，不连接服务器或供应商。"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import pathlib
import re
import sys
from typing import Any


WINDOWS = {
    "5m": 5 * 60,
    "15m": 15 * 60,
    "30m": 30 * 60,
    "2h": 2 * 60 * 60,
    "24h": 24 * 60 * 60,
}


def fail(reason: str) -> None:
    raise ValueError(reason)


def exact_keys(value: dict[str, Any], expected: set[str], name: str) -> None:
    if set(value) != expected:
        fail(f"{name} 字段集合不符合契约")


def integer(value: Any, name: str, minimum: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        fail(f"{name} 必须是不小于 {minimum} 的整数")
    return value


def utc_time(value: Any, name: str) -> dt.datetime:
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value):
        fail(f"{name} 必须是 UTC 秒级时间")
    return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)


def safe_document(raw: bytes) -> dict[str, Any]:
    if raw.startswith(b"\xef\xbb\xbf") or b"\x00" in raw or b"\r" in raw:
        fail("证据必须是 UTF-8、LF、无 BOM 文本")
    text = raw.decode("utf-8")
    forbidden = (
        r"(?<!\d)1[3-9]\d{9}(?!\d)",
        r"(?i)bearer\s+[a-z0-9._-]+",
        r'(?i)"(?:phone|mobile|otp|verification_code|access_key|secret|token|password)"\s*:',
    )
    if any(re.search(pattern, text) for pattern in forbidden):
        fail("证据包含禁止持久化的敏感字段或值")
    value = json.loads(text)
    if not isinstance(value, dict):
        fail("证据根节点必须是对象")
    return value


def validate(document: dict[str, Any], *, now: dt.datetime | None = None) -> list[str]:
    exact_keys(
        document,
        {
            "schema_version",
            "change_id",
            "environment",
            "observation_mode",
            "acceptance_scope",
            "observation_started_at",
            "evidence_created_at",
            "authorized_send_limit",
            "baseline",
            "canary_result",
            "final_state",
            "snapshots",
        },
        "根节点",
    )
    if document["schema_version"] != 1:
        fail("只支持观察证据 schema_version=1")
    if not isinstance(document["change_id"], str) or not re.fullmatch(
        r"\d{8}T\d{6}Z", document["change_id"]
    ):
        fail("ChangeId 必须使用 UTC 基本格式")
    environment = document["environment"]
    mode = document["observation_mode"]
    if environment not in {"test", "production"}:
        fail("environment 只能是 test 或 production")
    if mode not in {"closed_after_canary", "production_enabled"}:
        fail("observation_mode 不受支持")
    if environment == "test" and mode != "closed_after_canary":
        fail("测试服 Canary 后必须恢复关闭态")
    if mode == "production_enabled" and environment != "production":
        fail("开启态观察只能用于生产环境")
    if document["acceptance_scope"] not in {"receipt_only", "full_business_consume"}:
        fail("acceptance_scope 不受支持")

    started = utc_time(document["observation_started_at"], "observation_started_at")
    created = utc_time(document["evidence_created_at"], "evidence_created_at")
    if created < started + dt.timedelta(hours=24):
        fail("证据创建时间早于完整 24 小时观察窗口")
    current = now or dt.datetime.now(dt.timezone.utc)
    if created > current + dt.timedelta(minutes=5):
        fail("证据创建时间不能位于未来")

    limit = integer(document["authorized_send_limit"], "authorized_send_limit", 5)
    if limit > 10:
        fail("授权发送硬上限不得超过 10")

    baseline = document["baseline"]
    exact_keys(
        baseline,
        {"send_total", "send_accepted", "send_failed", "provider_calls_total", "provider_nonaccepted_total"},
        "baseline",
    )
    baseline_values = {key: integer(value, f"baseline.{key}") for key, value in baseline.items()}
    if baseline_values["send_total"] != baseline_values["send_accepted"] + baseline_values["send_failed"]:
        fail("baseline 发送日志汇总不守恒")
    if baseline_values["provider_nonaccepted_total"] > baseline_values["provider_calls_total"]:
        fail("baseline Provider 非受理数不能超过调用数")

    canary = document["canary_result"]
    exact_keys(canary, {"attempted", "accepted", "failed", "receipts_confirmed"}, "canary_result")
    canary_values = {key: integer(value, f"canary_result.{key}") for key, value in canary.items()}
    if canary_values != {"attempted": 5, "accepted": 5, "failed": 0, "receipts_confirmed": 5}:
        fail("阶段 5 Canary 必须五场景各一次、全部受理并逐条人工确认收件")
    if canary_values["attempted"] > limit:
        fail("Canary 尝试数超过授权硬上限")

    snapshots = document["snapshots"]
    if not isinstance(snapshots, list) or len(snapshots) != len(WINDOWS):
        fail("必须提供五个观察快照")
    previous: dict[str, int] | None = None
    first_expected = {
        "send_total": baseline_values["send_total"] + 5,
        "send_accepted": baseline_values["send_accepted"] + 5,
        "send_failed": baseline_values["send_failed"],
        "provider_calls_total": baseline_values["provider_calls_total"] + 5,
        "provider_nonaccepted_total": baseline_values["provider_nonaccepted_total"],
    }
    for index, (window_name, minimum_elapsed) in enumerate(WINDOWS.items()):
        snapshot = snapshots[index]
        if not isinstance(snapshot, dict):
            fail("观察快照必须是对象")
        exact_keys(
            snapshot,
            {
                "window",
                "observed_at",
                "elapsed_seconds",
                "api_health_http",
                "api_ready_http",
                "send_total",
                "send_accepted",
                "send_failed",
                "provider_calls_total",
                "provider_nonaccepted_total",
                "avg_provider_duration_seconds",
                "active_sms_alerts",
                "active_alertmanager_alerts",
                "notification_failed_delta",
            },
            f"snapshot[{index}]",
        )
        if snapshot["window"] != window_name:
            fail("观察快照顺序或窗口名称不正确")
        observed = utc_time(snapshot["observed_at"], f"snapshot[{index}].observed_at")
        elapsed = integer(snapshot["elapsed_seconds"], f"snapshot[{index}].elapsed_seconds", minimum_elapsed)
        actual_elapsed = int((observed - started).total_seconds())
        if abs(actual_elapsed - elapsed) > 2:
            fail("观察快照时间与 elapsed_seconds 不一致")
        if snapshot["api_health_http"] != 200 or snapshot["api_ready_http"] != 200:
            fail("观察窗口 API health/ready 不健康")
        counters = {
            key: integer(snapshot[key], f"snapshot[{index}].{key}")
            for key in (
                "send_total",
                "send_accepted",
                "send_failed",
                "provider_calls_total",
                "provider_nonaccepted_total",
            )
        }
        if counters["send_total"] != counters["send_accepted"] + counters["send_failed"]:
            fail("观察快照发送日志汇总不守恒")
        if any(counters[key] < first_expected[key] for key in counters):
            fail("观察快照没有包含完整五场景 Canary 增量")
        if previous is not None and any(counters[key] < previous[key] for key in counters):
            fail("观察计数发生回退")
        send_delta = counters["send_total"] - baseline_values["send_total"]
        failed_delta = counters["send_failed"] - baseline_values["send_failed"]
        if counters["provider_calls_total"] - baseline_values["provider_calls_total"] != send_delta:
            fail("Provider 调用增量与发送日志增量不一致")
        if counters["provider_nonaccepted_total"] - baseline_values["provider_nonaccepted_total"] != failed_delta:
            fail("Provider 非受理增量与失败日志增量不一致")
        if mode == "closed_after_canary" and counters != first_expected:
            fail("关闭态观察窗口出现 Canary 之外的新短信增量")
        calls_delta = counters["provider_calls_total"] - baseline_values["provider_calls_total"]
        nonaccepted_delta = counters["provider_nonaccepted_total"] - baseline_values["provider_nonaccepted_total"]
        if calls_delta >= 10 and nonaccepted_delta / calls_delta > 0.20:
            fail("Provider 非受理比例越过自动停止线")
        duration = snapshot["avg_provider_duration_seconds"]
        if isinstance(duration, bool) or not isinstance(duration, (int, float)) or duration < 0:
            fail("Provider 平均耗时必须是非负数")
        if calls_delta >= 10 and duration > 2:
            fail("Provider 平均耗时越过 2 秒停止线")
        if any(
            integer(snapshot[key], f"snapshot[{index}].{key}") != 0
            for key in ("active_sms_alerts", "active_alertmanager_alerts", "notification_failed_delta")
        ):
            fail("观察窗口存在活动告警或通知失败")
        previous = counters

    final_state = document["final_state"]
    exact_keys(
        final_state,
        {
            "sms_enabled",
            "sms_test_mode",
            "alertmanager_route",
            "active_sms_alerts",
            "active_alertmanager_alerts",
            "notification_failed_delta",
            "unexpected_business_mutations",
        },
        "final_state",
    )
    expected_enabled = mode == "production_enabled"
    expected_test_mode = mode != "production_enabled"
    if final_state["sms_enabled"] is not expected_enabled or final_state["sms_test_mode"] is not expected_test_mode:
        fail("最终短信开关状态与观察模式不一致")
    if final_state["alertmanager_route"] not in {"discard", "email"}:
        fail("最终 Alertmanager 路由不受支持")
    if any(
        integer(final_state[key], f"final_state.{key}") != 0
        for key in (
            "active_sms_alerts",
            "active_alertmanager_alerts",
            "notification_failed_delta",
            "unexpected_business_mutations",
        )
    ):
        fail("最终状态存在告警、通知失败或未授权业务变更")

    return [
        "phase5_observation_evidence=passed",
        f"change_id={document['change_id']}",
        f"environment={environment}",
        f"observation_mode={mode}",
        "observation_windows=5m,15m,30m,2h,24h",
        "canary_attempted=5",
        "canary_accepted=5",
        "receipts_confirmed=5",
        "sensitive_values_persisted=0",
        "network_connections=0",
        "real_sms_sent_by_verifier=0",
    ]


def self_test() -> None:
    started = dt.datetime(2099, 1, 1, tzinfo=dt.timezone.utc)
    baseline = {
        "send_total": 13,
        "send_accepted": 13,
        "send_failed": 0,
        "provider_calls_total": 0,
        "provider_nonaccepted_total": 0,
    }
    snapshots = []
    for name, elapsed in WINDOWS.items():
        snapshots.append(
            {
                "window": name,
                "observed_at": (started + dt.timedelta(seconds=elapsed)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "elapsed_seconds": elapsed,
                "api_health_http": 200,
                "api_ready_http": 200,
                "send_total": 18,
                "send_accepted": 18,
                "send_failed": 0,
                "provider_calls_total": 5,
                "provider_nonaccepted_total": 0,
                "avg_provider_duration_seconds": 0.4,
                "active_sms_alerts": 0,
                "active_alertmanager_alerts": 0,
                "notification_failed_delta": 0,
            }
        )
    valid = {
        "schema_version": 1,
        "change_id": "20990101T000000Z",
        "environment": "test",
        "observation_mode": "closed_after_canary",
        "acceptance_scope": "receipt_only",
        "observation_started_at": "2099-01-01T00:00:00Z",
        "evidence_created_at": "2099-01-02T00:01:00Z",
        "authorized_send_limit": 5,
        "baseline": baseline,
        "canary_result": {"attempted": 5, "accepted": 5, "failed": 0, "receipts_confirmed": 5},
        "final_state": {
            "sms_enabled": False,
            "sms_test_mode": True,
            "alertmanager_route": "discard",
            "active_sms_alerts": 0,
            "active_alertmanager_alerts": 0,
            "notification_failed_delta": 0,
            "unexpected_business_mutations": 0,
        },
        "snapshots": snapshots,
    }
    validate(valid, now=started + dt.timedelta(days=1, minutes=2))
    invalid = json.loads(json.dumps(valid))
    invalid["snapshots"][1]["send_total"] = 19
    rejected = False
    try:
        validate(invalid, now=started + dt.timedelta(days=1, minutes=2))
    except ValueError:
        rejected = True
    if not rejected:
        fail("关闭态新增发送反例未被拒绝")

    production = json.loads(json.dumps(valid))
    production["environment"] = "production"
    production["observation_mode"] = "production_enabled"
    production["final_state"]["sms_enabled"] = True
    production["final_state"]["sms_test_mode"] = False
    for snapshot in production["snapshots"]:
        snapshot["send_total"] = 23
        snapshot["send_accepted"] = 23
        snapshot["provider_calls_total"] = 10
        snapshot["avg_provider_duration_seconds"] = 3
    latency_rejected = False
    try:
        validate(production, now=started + dt.timedelta(days=1, minutes=2))
    except ValueError:
        latency_rejected = True
    if not latency_rejected:
        fail("十次调用平均耗时超限反例未被拒绝")

    monotonic = json.loads(json.dumps(production))
    for snapshot in monotonic["snapshots"]:
        snapshot["avg_provider_duration_seconds"] = 1
    monotonic["snapshots"][0]["send_total"] = 24
    monotonic["snapshots"][0]["send_accepted"] = 24
    monotonic["snapshots"][0]["provider_calls_total"] = 11
    monotonic_rejected = False
    try:
        validate(monotonic, now=started + dt.timedelta(days=1, minutes=2))
    except ValueError:
        monotonic_rejected = True
    if not monotonic_rejected:
        fail("计数回退反例未被拒绝")

    sensitive_rejected = False
    try:
        safe_document(b'{"phone":"13800138000"}\n')
    except ValueError:
        sensitive_rejected = True
    if not sensitive_rejected:
        fail("完整手机号反例未被拒绝")
    print("phase5_observation_evidence_self_test=passed")
    print("closed_state_send_growth_rejected=true")
    print("latency_stop_line_rejected=true")
    print("counter_rollback_rejected=true")
    print("sensitive_value_rejected=true")
    print("network_connections=0")
    print("real_sms_sent=0")


def main() -> int:
    parser = argparse.ArgumentParser(description="离线校验短信阶段 5 五档观察证据")
    parser.add_argument("--evidence", type=pathlib.Path)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test == (args.evidence is not None):
        parser.error("必须且只能指定 --self-test 或 --evidence")
    try:
        if args.self_test:
            self_test()
            return 0
        assert args.evidence is not None
        path = args.evidence.resolve(strict=True)
        if args.evidence.is_symlink() or not path.is_file():
            fail("证据必须是普通文件且不能是符号链接")
        repository_root = pathlib.Path(__file__).resolve().parents[1]
        if path == repository_root or repository_root in path.parents:
            fail("观察证据必须位于 Git 工作区之外")
        document = safe_document(path.read_bytes())
        for line in validate(document):
            print(line)
        return 0
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
        print("phase5_observation_evidence=failed")
        print(f"failure_reason={error}")
        print("network_connections=0")
        print("real_sms_sent_by_verifier=0")
        return 2


if __name__ == "__main__":
    sys.exit(main())

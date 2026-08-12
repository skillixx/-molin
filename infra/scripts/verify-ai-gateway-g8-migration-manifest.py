#!/usr/bin/env python3
"""离线校验 G8 测试到生产迁移清单，不连接服务器或读取 Secret。"""

import argparse
import hashlib
import json
import re
import unicodedata
from decimal import Decimal, InvalidOperation
from pathlib import Path, PurePosixPath
from typing import Any


ROOT_KEYS = {
    "schema_version",
    "stage",
    "change_id",
    "source",
    "target",
    "release",
    "scope",
    "traffic",
    "authorization",
    "chain",
    "models",
    "evidence",
}
SECTION_KEYS = {
    "source": {"environment", "host_alias", "deployment_kind", "deployment_root", "database_alias"},
    "target": {
        "environment",
        "host_alias",
        "deployment_kind",
        "deployment_root",
        "database_alias",
        "domain",
    },
    "release": {
        "source_commit",
        "api_artifact_sha256",
        "config_sha256",
        "bifrost_image_digest",
        "migration_version",
        "migration_dirty",
    },
    "scope": {"text_models_only", "excluded_capabilities"},
    "traffic": {"application_gate_enabled", "edge_gate_open", "real_customer_count"},
    "authorization": {
        "production_readonly",
        "production_deploy",
        "paid_upstream",
        "customer_gray",
        "alert_notification",
        "max_requests",
        "max_cost_cny",
    },
    "chain": {
        "previous_stage",
        "previous_manifest_sha256",
        "approval_receipt_sha256",
        "credential_rotation_receipt_sha256",
    },
    "models": {
        "approved_model_count",
        "approved_upstream_count",
        "pricing_approved",
        "minimum_margin_rate",
    },
    "evidence": {
        "backup_readable",
        "rollback_rehearsed",
        "reconciliation_zero",
        "alerts_local_only",
        "credentials_separated",
        "test_credentials_rotated",
    },
}
EXCLUDED_CAPABILITIES = {
    "image",
    "audio",
    "video",
    "multimodal_async",
    "object_storage_lifecycle",
    "gpu",
    "agent",
    "skills",
    "public_self_service_payment",
}
STAGES = {"test_candidate", "production_readonly", "production_closed_deploy", "production_gray"}
CHANGE_ID_PATTERN = re.compile(r"^CHG-G8-[A-Z0-9-]{8,64}$")
COMMIT_PATTERN = re.compile(r"^[0-9a-f]{40}$")
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-f]{64}$")
ALIAS_PATTERN = re.compile(r"^[a-z0-9][a-z0-9._-]{2,63}$")
DOMAIN_PATTERN = re.compile(
    r"^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$"
)
DEPLOYMENT_KINDS = {"host_binary", "docker_compose", "kubernetes"}
STAGE_ORDER = ["test_candidate", "production_readonly", "production_closed_deploy", "production_gray"]


def reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    """拒绝重复 JSON 键，避免后写值覆盖前写值而绕过人工复核。"""
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"JSON 存在重复字段：{key}")
        result[key] = value
    return result


def require_object(value: Any, field: str) -> dict[str, Any]:
    """要求字段为 JSON 对象，避免宽松类型绕过精确字段白名单。"""
    if not isinstance(value, dict):
        raise ValueError(f"{field} 必须为对象")
    return value


def require_exact_keys(value: dict[str, Any], expected: set[str], field: str) -> None:
    """精确核对字段集合，从结构层拒绝密码、Token 等未批准数据。"""
    actual = set(value)
    if actual != expected:
        raise ValueError(f"{field} 字段集合不合法：缺失={sorted(expected - actual)}，越权={sorted(actual - expected)}")


def require_bool(value: Any, field: str) -> bool:
    """拒绝 0/1 冒充布尔值，保持变更单语义唯一。"""
    if type(value) is not bool:
        raise ValueError(f"{field} 必须为布尔值")
    return value


def require_nonnegative_int(value: Any, field: str) -> int:
    """要求计数为非负整数，并拒绝布尔值的整数子类行为。"""
    if type(value) is not int or value < 0:
        raise ValueError(f"{field} 必须为非负整数")
    return value


def require_decimal(value: Any, field: str) -> Decimal:
    """按十进制定点解析费用与毛利，禁止浮点 JSON 引入隐式舍入。"""
    if not isinstance(value, str):
        raise ValueError(f"{field} 必须为十进制字符串")
    try:
        parsed = Decimal(value)
    except InvalidOperation as exc:
        raise ValueError(f"{field} 不是合法十进制字符串") from exc
    if not parsed.is_finite() or parsed < 0:
        raise ValueError(f"{field} 必须为非负有限值")
    return parsed


def require_text(value: Any, field: str, allow_pending: bool = False) -> str:
    """要求非空低敏元数据；生产阶段不得保留占位符。"""
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field} 必须为非空字符串")
    normalized = value.strip()
    if not allow_pending and normalized == "PENDING":
        raise ValueError(f"{field} 不得为 PENDING")
    return normalized


def require_sha_or_none(value: Any, field: str, allow_none: bool) -> str:
    """校验低敏回执摘要；未到对应阶段时只允许显式 NONE。"""
    if allow_none and value == "NONE":
        return value
    if not isinstance(value, str) or not SHA256_PATTERN.fullmatch(value):
        raise ValueError(f"{field} 必须为 64 位 SHA-256")
    return value


def manifest_sha256(manifest: dict[str, Any]) -> str:
    """以稳定 JSON 编码计算清单回执摘要，不读取或输出任何 Secret。"""
    canonical = json.dumps(manifest, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def require_absolute_normalized_path(value: Any, field: str) -> str:
    """只接受无控制字符、反斜杠和路径穿越片段的规范 POSIX 绝对路径。"""
    path = require_text(value, field)
    if any(unicodedata.category(character) in {"Cc", "Cf"} for character in path):
        raise ValueError(f"{field} 不得包含控制或格式字符")
    if path == "/" or "\\" in path or not path.startswith("/") or "//" in path:
        raise ValueError(f"{field} 必须为规范 POSIX 绝对路径")
    if not re.fullmatch(r"/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*", path):
        raise ValueError(f"{field} 只能包含 ASCII 安全路径段")
    parts = PurePosixPath(path).parts
    if any(part in {".", ".."} for part in path.split("/")) or str(PurePosixPath(path)) != path:
        raise ValueError(f"{field} 不得包含路径穿越或非规范片段")
    return path


def validate_common(manifest: dict[str, Any]) -> None:
    """校验所有阶段共享的结构、制品、范围与失败关闭约束。"""
    require_exact_keys(manifest, ROOT_KEYS, "根对象")
    if type(manifest["schema_version"]) is not int or manifest["schema_version"] != 1:
        raise ValueError("schema_version 必须为 1")
    stage = manifest["stage"]
    if not isinstance(stage, str) or stage not in STAGES:
        raise ValueError("stage 不在允许范围")
    if not isinstance(manifest["change_id"], str) or not CHANGE_ID_PATTERN.fullmatch(manifest["change_id"]):
        raise ValueError("change_id 格式不合法")

    sections: dict[str, dict[str, Any]] = {}
    for section, expected_keys in SECTION_KEYS.items():
        sections[section] = require_object(manifest[section], section)
        require_exact_keys(sections[section], expected_keys, section)

    source = sections["source"]
    target = sections["target"]
    if source["environment"] != "test" or target["environment"] != "production":
        raise ValueError("source/target 环境必须固定为 test/production")
    for key in SECTION_KEYS["source"] - {"environment"}:
        require_text(source[key], f"source.{key}")
    allow_pending_target = stage == "test_candidate"
    for key in SECTION_KEYS["target"] - {"environment"}:
        require_text(target[key], f"target.{key}", allow_pending=allow_pending_target)
    if not allow_pending_target:
        if not ALIAS_PATTERN.fullmatch(target["host_alias"]):
            raise ValueError("target.host_alias 格式不合法")
        if target["deployment_kind"] not in DEPLOYMENT_KINDS:
            raise ValueError("target.deployment_kind 不在允许范围")
        require_absolute_normalized_path(target["deployment_root"], "target.deployment_root")
        if not ALIAS_PATTERN.fullmatch(target["database_alias"]):
            raise ValueError("target.database_alias 格式不合法")
        if not DOMAIN_PATTERN.fullmatch(target["domain"]):
            raise ValueError("target.domain 格式不合法")

    release = sections["release"]
    if not isinstance(release["source_commit"], str) or not COMMIT_PATTERN.fullmatch(release["source_commit"]):
        raise ValueError("release.source_commit 必须为完整 40 位提交")
    for key in ("api_artifact_sha256", "config_sha256"):
        if not isinstance(release[key], str) or not SHA256_PATTERN.fullmatch(release[key]):
            raise ValueError(f"release.{key} 必须为 64 位 SHA-256")
    if not isinstance(release["bifrost_image_digest"], str) or not DIGEST_PATTERN.fullmatch(
        release["bifrost_image_digest"]
    ):
        raise ValueError("release.bifrost_image_digest 必须为 sha256 摘要")
    require_nonnegative_int(release["migration_version"], "release.migration_version")
    if require_bool(release["migration_dirty"], "release.migration_dirty"):
        raise ValueError("Migration dirty 状态禁止进入迁移清单")

    scope = sections["scope"]
    if not require_bool(scope["text_models_only"], "scope.text_models_only"):
        raise ValueError("G8 只允许文字模型")
    excluded_capabilities = scope["excluded_capabilities"]
    if not isinstance(excluded_capabilities, list) or not all(
        isinstance(capability, str) for capability in excluded_capabilities
    ):
        raise ValueError("G8 排除能力必须为字符串数组")
    if set(excluded_capabilities) != EXCLUDED_CAPABILITIES:
        raise ValueError("G8 排除能力集合不完整")
    if len(excluded_capabilities) != len(EXCLUDED_CAPABILITIES):
        raise ValueError("G8 排除能力不得重复")

    traffic = sections["traffic"]
    require_bool(traffic["application_gate_enabled"], "traffic.application_gate_enabled")
    require_bool(traffic["edge_gate_open"], "traffic.edge_gate_open")
    require_nonnegative_int(traffic["real_customer_count"], "traffic.real_customer_count")

    authorization = sections["authorization"]
    for key in SECTION_KEYS["authorization"] - {"max_requests", "max_cost_cny"}:
        require_bool(authorization[key], f"authorization.{key}")
    require_nonnegative_int(authorization["max_requests"], "authorization.max_requests")
    require_decimal(authorization["max_cost_cny"], "authorization.max_cost_cny")

    models = sections["models"]
    require_nonnegative_int(models["approved_model_count"], "models.approved_model_count")
    require_nonnegative_int(models["approved_upstream_count"], "models.approved_upstream_count")
    require_bool(models["pricing_approved"], "models.pricing_approved")
    margin = require_decimal(models["minimum_margin_rate"], "models.minimum_margin_rate")
    if margin < Decimal("0.15") or margin >= Decimal("1"):
        raise ValueError("最低毛利率必须在 0.15（含）到 1（不含）之间")

    chain = sections["chain"]
    expected_previous_stage = "none" if stage == "test_candidate" else STAGE_ORDER[STAGE_ORDER.index(stage) - 1]
    if chain["previous_stage"] != expected_previous_stage:
        raise ValueError("前序阶段与当前阶段不连续")
    require_sha_or_none(
        chain["previous_manifest_sha256"],
        "chain.previous_manifest_sha256",
        allow_none=stage == "test_candidate",
    )
    require_sha_or_none(
        chain["approval_receipt_sha256"],
        "chain.approval_receipt_sha256",
        allow_none=stage == "test_candidate",
    )
    require_sha_or_none(
        chain["credential_rotation_receipt_sha256"],
        "chain.credential_rotation_receipt_sha256",
        allow_none=stage in {"test_candidate", "production_readonly"},
    )
    if stage != "test_candidate" and (
        chain["previous_manifest_sha256"] == "NONE" or chain["approval_receipt_sha256"] == "NONE"
    ):
        raise ValueError("生产阶段必须绑定前序清单和审批回执摘要")
    if stage == "test_candidate" and any(value != "NONE" for key, value in chain.items() if key != "previous_stage"):
        raise ValueError("测试候选阶段不得伪造前序、审批或轮换回执")
    if stage == "production_readonly" and chain["credential_rotation_receipt_sha256"] != "NONE":
        raise ValueError("生产只读阶段不得提前声明测试凭据轮换回执")
    if stage in {"production_closed_deploy", "production_gray"} and chain[
        "credential_rotation_receipt_sha256"
    ] == "NONE":
        raise ValueError("生产部署阶段必须绑定测试凭据轮换回执摘要")

    evidence = sections["evidence"]
    for key in SECTION_KEYS["evidence"]:
        require_bool(evidence[key], f"evidence.{key}")


def validate_stage(manifest: dict[str, Any]) -> None:
    """按阶段限制授权、流量、预算与证据，禁止从测试候选直接跳到灰度。"""
    stage = manifest["stage"]
    traffic = manifest["traffic"]
    authorization = manifest["authorization"]
    models = manifest["models"]
    evidence = manifest["evidence"]
    auth_flags = {
        key: authorization[key]
        for key in SECTION_KEYS["authorization"]
        if key not in {"max_requests", "max_cost_cny"}
    }
    max_cost = require_decimal(authorization["max_cost_cny"], "authorization.max_cost_cny")

    if stage == "test_candidate":
        if traffic["application_gate_enabled"] or traffic["edge_gate_open"] or traffic["real_customer_count"] != 0:
            raise ValueError("测试候选阶段必须保持双总闸关闭且客户数为 0")
        if any(auth_flags.values()) or authorization["max_requests"] != 0 or max_cost != 0:
            raise ValueError("测试候选阶段不得声明生产授权、请求额度或费用预算")
        if models["approved_model_count"] != 0 or models["approved_upstream_count"] != 0 or models["pricing_approved"]:
            raise ValueError("测试候选阶段不得伪造生产模型、上游或价格批准")
        return

    if stage == "production_readonly":
        expected = {"production_readonly": True, "production_deploy": False, "paid_upstream": False,
                    "customer_gray": False, "alert_notification": False}
        if auth_flags != expected or authorization["max_requests"] != 0 or max_cost != 0:
            raise ValueError("生产只读阶段授权边界不合法")
        if traffic["application_gate_enabled"] or traffic["edge_gate_open"] or traffic["real_customer_count"] != 0:
            raise ValueError("生产只读阶段必须保持双总闸关闭且客户数为 0")
        return

    required_evidence = (
        evidence["backup_readable"]
        and evidence["rollback_rehearsed"]
        and evidence["reconciliation_zero"]
        and evidence["credentials_separated"]
        and evidence["test_credentials_rotated"]
    )
    if not required_evidence:
        raise ValueError("生产部署前备份、回滚、对账、凭据隔离和测试凭据轮换证据必须完整")

    if stage == "production_closed_deploy":
        expected = {"production_readonly": True, "production_deploy": True, "paid_upstream": False,
                    "customer_gray": False, "alert_notification": False}
        if auth_flags != expected or authorization["max_requests"] != 0 or max_cost != 0:
            raise ValueError("生产关闭态部署授权边界不合法")
        if traffic["application_gate_enabled"] or traffic["edge_gate_open"] or traffic["real_customer_count"] != 0:
            raise ValueError("生产关闭态部署必须保持双总闸关闭且客户数为 0")
        return

    if not all(auth_flags.values()):
        raise ValueError("生产灰度授权必须完整")
    if authorization["max_requests"] <= 0 or max_cost <= 0:
        raise ValueError("生产灰度必须设置正数请求和费用上限")
    if not traffic["application_gate_enabled"] or not traffic["edge_gate_open"] or traffic["real_customer_count"] <= 0:
        raise ValueError("生产灰度必须明确开启双闸并限定至少一个客户")
    if not 5 <= models["approved_model_count"] <= 8:
        raise ValueError("生产灰度必须批准 5 到 8 个文字模型")
    if models["approved_upstream_count"] != 2 or not models["pricing_approved"]:
        raise ValueError("生产灰度必须批准两个上游和价格")
    if evidence["alerts_local_only"]:
        raise ValueError("生产灰度不得继续使用仅本地告警状态")


def validate_manifest(manifest: dict[str, Any]) -> None:
    """执行完整迁移清单验证。"""
    if not isinstance(manifest, dict):
        raise ValueError("根值必须为对象")
    validate_common(manifest)
    validate_stage(manifest)


def validate_chain(manifests: list[dict[str, Any]]) -> None:
    """验证从测试候选开始的连续清单链，禁止只提交自报的生产阶段清单。"""
    if not manifests:
        raise ValueError("清单链不得为空")
    expected_stages = STAGE_ORDER[: STAGE_ORDER.index(manifests[-1].get("stage")) + 1] if manifests[-1].get("stage") in STAGES else []
    actual_stages = [manifest.get("stage") for manifest in manifests]
    if actual_stages != expected_stages:
        raise ValueError("必须从 test_candidate 开始按顺序提供完整清单链")
    change_ids: set[str] = set()
    approval_receipts: set[str] = set()
    for index, manifest in enumerate(manifests):
        validate_manifest(manifest)
        if manifest["change_id"] in change_ids:
            raise ValueError("清单链 ChangeId 不得重复")
        change_ids.add(manifest["change_id"])
        if index > 0:
            approval_receipt = manifest["chain"]["approval_receipt_sha256"]
            if approval_receipt in approval_receipts:
                raise ValueError("每个生产阶段必须使用独立审批回执")
            approval_receipts.add(approval_receipt)
        if index == 0:
            continue
        previous = manifests[index - 1]
        if manifest["chain"]["previous_manifest_sha256"] != manifest_sha256(previous):
            raise ValueError("前序清单摘要不匹配")
        if manifest["release"] != previous["release"]:
            raise ValueError("同一迁移链的发布制品必须保持一致")
        if manifest["source"] != previous["source"]:
            raise ValueError("同一迁移链的测试源身份必须保持一致")
        if index >= 2 and manifest["target"] != previous["target"]:
            raise ValueError("生产目标在只读确认后不得漂移")


def success_summary(manifest: dict[str, Any]) -> str:
    """只输出阶段和布尔聚合，避免泄漏主机、域名、路径或制品细节。"""
    production_authorized = manifest["stage"] != "test_candidate"
    traffic_open = manifest["traffic"]["application_gate_enabled"] or manifest["traffic"]["edge_gate_open"]
    return (
        f"G8_MIGRATION_MANIFEST=PASS stage={manifest['stage']} "
        f"production_authorized={str(production_authorized).lower()} "
        f"traffic_open={str(traffic_open).lower()} receipt_sha256={manifest_sha256(manifest)} secrets_read=false"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="离线校验 G8 测试到生产迁移清单")
    parser.add_argument("--manifest", type=Path, action="append", required=True)
    args = parser.parse_args()
    try:
        manifests = [
            json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=reject_duplicate_keys)
            for path in args.manifest
        ]
        validate_chain(manifests)
    except (OSError, UnicodeError):
        print("G8_MIGRATION_MANIFEST=FAILED reason=manifest_unreadable")
        return 2
    except json.JSONDecodeError:
        print("G8_MIGRATION_MANIFEST=FAILED reason=invalid_json")
        return 2
    except (ValueError, TypeError, KeyError, IndexError):
        print("G8_MIGRATION_MANIFEST=FAILED reason=invalid_manifest")
        return 2
    print(success_summary(manifests[-1]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

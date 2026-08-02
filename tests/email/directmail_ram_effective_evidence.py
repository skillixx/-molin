#!/usr/bin/env python3
"""离线验收 DirectMail RAM 有效权限证据清单，不调用任何云 API。"""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
from datetime import datetime, timezone


SCHEMA = "molin.directmail.ram-effective-evidence/v1"
SHA256_PATTERN = re.compile(r"[0-9a-f]{64}")
DEPLOYMENT_PATTERN = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})")
EXPECTED_ACTIONS = {
    "dm:QueryTemplateByParam": ("allow", {"existing_success"}),
    "dm:DescTemplate": ("allow", {"existing_success"}),
    "dm:SingleSendMail": ("allow", {"historical_accepted"}),
    "dm:CreateTemplate": ("explicit_deny", {"permission_audit", "existing_troubleshoot"}),
    "dm:ModifyTemplate": ("explicit_deny", {"permission_audit", "existing_troubleshoot"}),
    "dm:DeleteTemplate": ("explicit_deny", {"permission_audit", "existing_troubleshoot"}),
}
FORBIDDEN_KEYS = {
    "access_key", "accesskey", "secret", "token", "request_id", "requestid",
    "email", "recipient", "message", "raw", "response", "body", "credential",
}


class EvidenceError(RuntimeError):
    """表示证据结构或结论不足，必须失败关闭。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise EvidenceError(classification)


def strict_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    """拒绝重复 JSON 键，防止后写字段覆盖已审核结论。"""
    result: dict[str, object] = {}
    for key, value in pairs:
        require(key not in result, "duplicate_field")
        result[key] = value
    return result


def parse_utc(value: object, name: str) -> datetime:
    require(isinstance(value, str) and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value) is not None, name)
    return datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def sha256_value(value: object, name: str) -> str:
    require(isinstance(value, str) and SHA256_PATTERN.fullmatch(value) is not None, name)
    return value


def reject_sensitive_keys(value: object) -> None:
    """递归拒绝可能携带凭据、RequestId 或业务原文的字段。"""
    if isinstance(value, dict):
        for key, child in value.items():
            normalized = re.sub(r"[^a-z0-9]", "", key.lower())
            require(normalized not in {re.sub(r"[^a-z0-9]", "", item) for item in FORBIDDEN_KEYS}, "sensitive_field")
            reject_sensitive_keys(child)
    elif isinstance(value, list):
        for child in value:
            reject_sensitive_keys(child)


def validate(document: object) -> None:
    require(isinstance(document, dict), "document_shape")
    require(set(document) == {"schema", "window", "identity", "policy", "trust", "audit", "actions"}, "document_fields")
    require(document.get("schema") == SCHEMA, "schema")
    reject_sensitive_keys(document)

    window = document.get("window")
    require(isinstance(window, dict) and set(window) == {"start_utc", "end_utc", "captured_at_utc"}, "window_fields")
    start = parse_utc(window.get("start_utc"), "window_start")
    end = parse_utc(window.get("end_utc"), "window_end")
    captured = parse_utc(window.get("captured_at_utc"), "captured_at")
    require(start <= end <= captured and (end - start).total_seconds() <= 86400, "window_order")

    identity = document.get("identity")
    require(isinstance(identity, dict) and set(identity) == {"alias_sha256", "principal_type", "policy_version_sha256", "deployment_sha", "same_identity"}, "identity_fields")
    sha256_value(identity.get("alias_sha256"), "identity_alias")
    sha256_value(identity.get("policy_version_sha256"), "policy_version")
    require(identity.get("principal_type") in {"ram_user", "ram_role"}, "principal_type")
    require(isinstance(identity.get("deployment_sha"), str) and DEPLOYMENT_PATTERN.fullmatch(identity["deployment_sha"]) is not None, "deployment_sha")
    require(identity.get("same_identity") is True, "identity_binding")

    policy = document.get("policy")
    require(isinstance(policy, dict) and set(policy) == {"snapshot_sha256", "effective_complete", "attached_sources_complete", "group_sources_complete", "deny_precedence_verified"}, "policy_fields")
    sha256_value(policy.get("snapshot_sha256"), "policy_snapshot")
    for field in ("effective_complete", "attached_sources_complete", "group_sources_complete", "deny_precedence_verified"):
        require(policy.get(field) is True, f"policy_{field}")

    trust = document.get("trust")
    require(isinstance(trust, dict) and set(trust) == {"mode", "chain_complete", "evidence_sha256"}, "trust_fields")
    expected_mode = "not_applicable_direct_user" if identity.get("principal_type") == "ram_user" else "validated_role_chain"
    require(trust.get("mode") == expected_mode and trust.get("chain_complete") is True, "trust_chain")
    sha256_value(trust.get("evidence_sha256"), "trust_evidence")

    audit = document.get("audit")
    require(isinstance(audit, dict) and set(audit) == {"recent_attempts_complete", "evidence_sha256"}, "audit_fields")
    require(audit.get("recent_attempts_complete") is True, "recent_attempts")
    sha256_value(audit.get("evidence_sha256"), "audit_evidence")

    actions = document.get("actions")
    require(isinstance(actions, list) and len(actions) == len(EXPECTED_ACTIONS), "actions_shape")
    indexed: dict[str, dict[str, object]] = {}
    for row in actions:
        require(isinstance(row, dict) and set(row) == {"action", "outcome", "evidence_kind", "evidence_sha256"}, "action_fields")
        action = row.get("action")
        require(isinstance(action, str) and action in EXPECTED_ACTIONS and action not in indexed, "action_identity")
        outcome, evidence_kinds = EXPECTED_ACTIONS[action]
        require(row.get("outcome") == outcome, "action_outcome")
        require(row.get("evidence_kind") in evidence_kinds, "action_evidence_kind")
        sha256_value(row.get("evidence_sha256"), "action_evidence")
        indexed[action] = row
    require(set(indexed) == set(EXPECTED_ACTIONS), "actions_complete")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--manifest-sha256", required=True)
    args = parser.parse_args()

    path = pathlib.Path(args.manifest)
    require(path.is_absolute() and path.is_file() and not path.is_symlink(), "manifest_identity")
    raw = path.read_bytes()
    require(raw and len(raw) <= 1024 * 1024 and not raw.startswith(b"\xef\xbb\xbf") and b"\x00" not in raw, "manifest_encoding")
    expected_hash = args.manifest_sha256.lower()
    require(SHA256_PATTERN.fullmatch(expected_hash) is not None, "manifest_hash_argument")
    require(hashlib.sha256(raw).hexdigest() == expected_hash, "manifest_hash")
    document = json.loads(raw.decode("utf-8"), object_pairs_hook=strict_object)
    validate(document)
    print("status=pass mode=directmail_ram_effective_evidence actions=6 reads_allow=2 send_allow=historical_accepted writes_explicit_deny=3 identity_bound=true trust_complete=true recent_attempts=true external_access=false writes=false")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (EvidenceError, json.JSONDecodeError, UnicodeDecodeError, OSError) as exc:
        classification = str(exc) if isinstance(exc, EvidenceError) else "manifest_parse"
        print(f"status=failed mode=directmail_ram_effective_evidence classification={classification} external_access=false writes=false")
        raise SystemExit(2)

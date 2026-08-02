#!/usr/bin/env python3
"""攻击验证 RAM 脱敏证据验收器始终失败关闭。"""

from __future__ import annotations

import copy
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile


HERE = pathlib.Path(__file__).resolve().parent
VALIDATOR = HERE / "directmail_ram_effective_evidence.py"
HEX = "a" * 64


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def fixture() -> dict[str, object]:
    actions = [
        {"action": "dm:QueryTemplateByParam", "outcome": "allow", "evidence_kind": "existing_success", "evidence_sha256": HEX},
        {"action": "dm:DescTemplate", "outcome": "allow", "evidence_kind": "existing_success", "evidence_sha256": HEX},
        {"action": "dm:SingleSendMail", "outcome": "allow", "evidence_kind": "historical_accepted", "evidence_sha256": HEX},
        {"action": "dm:CreateTemplate", "outcome": "explicit_deny", "evidence_kind": "permission_audit", "evidence_sha256": HEX},
        {"action": "dm:ModifyTemplate", "outcome": "explicit_deny", "evidence_kind": "existing_troubleshoot", "evidence_sha256": HEX},
        {"action": "dm:DeleteTemplate", "outcome": "explicit_deny", "evidence_kind": "permission_audit", "evidence_sha256": HEX},
    ]
    return {
        "schema": "molin.directmail.ram-effective-evidence/v1",
        "window": {"start_utc": "2026-08-01T00:00:00Z", "end_utc": "2026-08-01T01:00:00Z", "captured_at_utc": "2026-08-01T01:05:00Z"},
        "identity": {"alias_sha256": HEX, "principal_type": "ram_user", "policy_version_sha256": HEX, "deployment_sha": "b" * 40, "same_identity": True},
        "policy": {"snapshot_sha256": HEX, "effective_complete": True, "attached_sources_complete": True, "group_sources_complete": True, "deny_precedence_verified": True},
        "trust": {"mode": "not_applicable_direct_user", "chain_complete": True, "evidence_sha256": HEX},
        "audit": {"recent_attempts_complete": True, "evidence_sha256": HEX},
        "actions": actions,
    }


def execute(document: dict[str, object], optimized: bool = False, raw: bytes | None = None) -> subprocess.CompletedProcess[str]:
    with tempfile.TemporaryDirectory() as directory:
        path = pathlib.Path(directory) / "manifest.json"
        payload = raw if raw is not None else (json.dumps(document, ensure_ascii=True, separators=(",", ":")) + "\n").encode()
        path.write_bytes(payload)
        command = [sys.executable]
        if optimized:
            command.append("-O")
        command.extend([str(VALIDATOR), "--manifest", str(path.resolve()), "--manifest-sha256", hashlib.sha256(payload).hexdigest()])
        return subprocess.run(command, text=True, capture_output=True, check=False, timeout=10)


def main() -> int:
    valid = fixture()
    for optimized in (False, True):
        result = execute(valid, optimized)
        require(result.returncode == 0 and result.stdout.startswith("status=pass ") and result.stderr == "", "valid_fixture")

    attacks = []
    for name, mutate in (
        ("identity_unbound", lambda d: d["identity"].__setitem__("same_identity", False)),
        ("policy_incomplete", lambda d: d["policy"].__setitem__("effective_complete", False)),
        ("deny_precedence_missing", lambda d: d["policy"].__setitem__("deny_precedence_verified", False)),
        ("trust_incomplete", lambda d: d["trust"].__setitem__("chain_complete", False)),
        ("attempts_incomplete", lambda d: d["audit"].__setitem__("recent_attempts_complete", False)),
        ("action_removed", lambda d: d["actions"].pop()),
        ("write_allowed", lambda d: d["actions"][3].__setitem__("outcome", "allow")),
        ("send_delivered", lambda d: d["actions"][2].__setitem__("outcome", "delivered")),
        ("unsafe_write_evidence", lambda d: d["actions"][4].__setitem__("evidence_kind", "live_request")),
        ("request_id_field", lambda d: d["actions"][0].__setitem__("request_id", "raw")),
        ("secret_field", lambda d: d.__setitem__("secret", "raw")),
        ("hash_invalid", lambda d: d["audit"].__setitem__("evidence_sha256", "short")),
        ("window_too_long", lambda d: d["window"].__setitem__("end_utc", "2026-08-03T00:00:00Z")),
        ("role_without_chain", lambda d: d["identity"].__setitem__("principal_type", "ram_role")),
    ):
        attacked = copy.deepcopy(valid)
        mutate(attacked)
        attacks.append((name, attacked, None))
    duplicate = json.dumps(valid, ensure_ascii=True, separators=(",", ":")).replace('"schema":', '"schema":"duplicate","schema":', 1).encode()
    attacks.append(("duplicate_json_key", valid, duplicate))

    for name, document, raw in attacks:
        for optimized in (False, True):
            result = execute(document, optimized, raw)
            require(result.returncode == 2 and result.stdout.startswith("status=failed ") and result.stderr == "", f"attack_not_rejected:{name}")

    print(f"status=pass mode=directmail_ram_effective_evidence_contract attack_cases={len(attacks)} optimized=true external_access=false writes=false")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

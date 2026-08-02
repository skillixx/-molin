"""验证 Phase 4 剩余门禁清单不能被局部通过证据误签。"""

from __future__ import annotations

import copy
import hashlib
import json
import pathlib
import re


ROOT = pathlib.Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "tests" / "email" / "phase4_remaining_gates.json"
QA_REPORT = ROOT / "tests" / "email" / "directmail-phase4-qa-report.md"

EXPECTED_CLOSED = {
    "runtime_six_surface_scan",
    "credential_cleanup",
    "redis_lease",
    "redis_history_cleanup",
    "redis_history_postcheck",
    "redis_unknown_restart_cycle",
    "ram_read_actions",
    "ram_effective_permissions",
    "five_scene_real_replay_expiry",
    "migration_000057_cycle",
    "migration_000055_matrix",
    "migration_000056_matrix",
    "frontend_scope_dod",
    "real_role_responsive_matrix",
    "deployed_frontend_error_state",
    "template_send_real_fault_matrix",
    "five_business_flow_e2e",
    "qa_phase4_report",
    "pm_phase4_signoff",
}

EXPECTED_OPEN: dict[str, str] = {}

EXPECTED_AUTHORIZATION = {
    "redis_database_fixture_cycle": "passed_manual_legacy_scp_cycle_no_repeat",
    "migration_matrices": "passed_isolated_mysql8_matrix_no_repeat",
    "external_mail_e2e": "waived_by_project_owner_not_verified",
}

EXPECTED_PROOF = {
    "migration_000055_matrix": ["mysql8_image_identity", "baseline_generation_receipt", "schema54_empty", "schema54_legacy", "schema55", "baseline_manifest", "full_matrix", "partial_matrix", "precise_down"],
    "migration_000056_matrix": ["mysql8_image_identity", "baseline_generation_receipt", "schema55", "schema56", "baseline_manifest", "full_matrix", "partial_matrix", "ownership_matrix"],
}

WAIVED_GATES = {
    "ram_effective_permissions",
    "five_scene_real_replay_expiry",
    "template_send_real_fault_matrix",
    "five_business_flow_e2e",
}

REQUIRED_NO_REPEAT = {
    "runtime_six_surface_scan",
    "redis_lease",
    "redis_history_cleanup",
    "redis_history_postcheck",
    "redis_unknown_restart_cycle",
    "ram_read_actions",
    "ram_effective_permissions",
    "five_scene_real_replay_expiry",
    "migration_000057_cycle",
    "migration_000055_matrix",
    "migration_000056_matrix",
    "frontend_scope_dod",
    "real_role_responsive_matrix",
    "deployed_frontend_error_state",
    "template_send_real_fault_matrix",
    "five_business_flow_e2e",
    "qa_phase4_report",
    "pm_phase4_signoff",
}


class ContractError(RuntimeError):
    """表示门禁清单不满足失败关闭契约。"""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def unique_by_id(rows: object, name: str) -> dict[str, dict[str, object]]:
    require(isinstance(rows, list), f"{name}_shape")
    result: dict[str, dict[str, object]] = {}
    for row in rows:
        require(isinstance(row, dict), f"{name}_row_shape")
        gate_id = row.get("id")
        require(isinstance(gate_id, str) and re.fullmatch(r"[a-z0-9_]+", gate_id) is not None, f"{name}_id")
        require(gate_id not in result, f"{name}_duplicate")
        result[gate_id] = row
    return result


def validate(document: object) -> None:
    require(isinstance(document, dict), "document_shape")
    require(
        set(document) == {
            "schema_version", "phase", "evidence_date", "overall_status", "accepted_semantics",
            "closed_gates", "open_gates", "authorization_state", "qa_pm",
        },
        "document_fields",
    )
    require(document.get("schema_version") == 1, "schema_version")
    require(document.get("phase") == "directmail_email_otp_phase4", "phase")
    require(document.get("evidence_date") == "2026-08-02", "evidence_date")
    require(document.get("overall_status") == "passed_with_project_owner_waivers", "overall_status")
    require(document.get("accepted_semantics") == "provider_acceptance_only", "accepted_semantics")

    closed = unique_by_id(document.get("closed_gates"), "closed")
    opened = unique_by_id(document.get("open_gates"), "open")
    require(set(closed) == EXPECTED_CLOSED, "closed_set")
    require(set(opened) == set(EXPECTED_OPEN), "open_set")
    require(set(closed).isdisjoint(opened), "gate_overlap")

    for gate_id, row in closed.items():
        require(set(row) == {"id", "status", "must_not_repeat"}, f"closed_fields_{gate_id}")
        if gate_id in WAIVED_GATES:
            expected_status = "waived_by_project_owner_not_verified"
        elif gate_id in {"qa_phase4_report", "pm_phase4_signoff"}:
            expected_status = "passed_with_project_owner_waivers"
        else:
            expected_status = "passed"
        require(row.get("status") == expected_status, f"closed_status_{gate_id}")
        require(isinstance(row.get("must_not_repeat"), bool), f"closed_repeat_shape_{gate_id}")
    for gate_id in REQUIRED_NO_REPEAT:
        require(closed[gate_id].get("must_not_repeat") is True, f"closed_repeat_{gate_id}")

    for gate_id, expected_status in EXPECTED_OPEN.items():
        row = opened[gate_id]
        require(set(row) == {"id", "status", "execution_permitted", "proof_required"}, f"open_fields_{gate_id}")
        require(row.get("status") == expected_status, f"open_status_{gate_id}")
        require(row.get("execution_permitted") is False, f"execution_permission_{gate_id}")
        proof = row.get("proof_required")
        require(proof == EXPECTED_PROOF[gate_id], f"proof_value_{gate_id}")

    require(document.get("authorization_state") == EXPECTED_AUTHORIZATION, "authorization_state")
    qa_pm = document.get("qa_pm")
    require(isinstance(qa_pm, dict), "qa_pm_shape")
    require(set(qa_pm) == {"qa_status", "pm_status", "p0_open", "p1_open", "p2_open"}, "qa_pm_fields")
    require(qa_pm.get("qa_status") == "passed_with_project_owner_waivers", "qa_status")
    require(qa_pm.get("pm_status") == "passed_with_project_owner_waivers", "pm_status")
    require([qa_pm.get("p0_open"), qa_pm.get("p1_open"), qa_pm.get("p2_open")] == [0, 0, 0], "defect_counts")


def expect_rejected(document: dict[str, object], mutate, name: str) -> None:
    candidate = copy.deepcopy(document)
    mutate(candidate)
    try:
        validate(candidate)
    except ContractError:
        return
    raise ContractError(f"attack_not_rejected_{name}")


def validate_qa_report(raw: bytes) -> None:
    """确认最终报告保留签署结论、负责人豁免和 Phase 5 边界。"""
    require(raw and not raw.startswith(b"\xef\xbb\xbf") and b"\x00" not in raw, "qa_report_encoding")
    text = raw.decode("utf-8")
    required = [
        "# DirectMail Phase 4 测试验收报告（QA/PM 已附负责人豁免通过）",
        "| 当前结论 | QA/PM 已附负责人豁免关闭 Phase 4 |",
        "| Phase 4 状态 | `passed_with_project_owner_waivers` |",
        "QA 与 PM 均已确认附负责人豁免通过；两项签署都不把四项负责人豁免改写为技术 PASS。",
        "## PM 最终签署记录",
        "PM 同意附负责人豁免关闭 Phase 4，但该结论不代表生产环境验证或批准 Phase 5 上线。",
        "| 产品经理（PM） | 附负责人豁免通过 |",
        "RAM 有效策略、角色信任链和显式 Deny 证据",
        "五场景真实重放与过期矩阵",
        "模板测试发送真实故障矩阵",
        "五业务流真实外发 E2E",
        "waived_by_project_owner_not_verified",
        "P0=0、P1=0、P2=0",
        "Phase 4 已按附负责人豁免关闭；Phase 5 和生产上线仍未批准",
    ]
    for marker in required:
        require(text.count(marker) >= 1, f"qa_report_marker_{hashlib.sha256(marker.encode('utf-8')).hexdigest()[:12]}")
    # 关闭门禁必须在证据索引中逐项出现，防止汇总数字正确但遗漏具体门禁。
    for gate_id in EXPECTED_CLOSED:
        require(text.count(f"| `{gate_id}` |") == 1, f"qa_report_closed_gate_{gate_id}")


def main() -> int:
    raw = MANIFEST.read_bytes()
    require(raw and not raw.startswith(b"\xef\xbb\xbf") and b"\x00" not in raw, "manifest_encoding")
    document = json.loads(raw.decode("utf-8"))
    validate(document)
    report_raw = QA_REPORT.read_bytes()
    validate_qa_report(report_raw)

    attacks = [
        ("overall_plain_passed", lambda d: d.__setitem__("overall_status", "passed")),
        ("accepted_is_delivery", lambda d: d.__setitem__("accepted_semantics", "delivered")),
        ("remove_closed_gate", lambda d: d["closed_gates"].pop()),
        ("add_unknown_open_gate", lambda d: d["open_gates"].append({"id": "unknown", "status": "passed", "execution_permitted": True, "proof_required": []})),
        ("reopen_pm_gate", lambda d: d["open_gates"].append({"id": "pm_phase4_signoff", "status": "signoff_required", "execution_permitted": False, "proof_required": ["qa_phase4_report_passed"]})),
        ("authorization_granted", lambda d: d["authorization_state"].__setitem__("migration_matrices", "granted")),
        ("external_waiver_rewritten", lambda d: d["authorization_state"].__setitem__("external_mail_e2e", "passed")),
        ("qa_signature_downgraded", lambda d: d["qa_pm"].__setitem__("qa_status", "passed")),
        ("pm_signature_downgraded", lambda d: d["qa_pm"].__setitem__("pm_status", "passed")),
        ("repeat_closed_gate", lambda d: d["closed_gates"][0].__setitem__("must_not_repeat", False)),
        ("waiver_promoted_to_passed", lambda d: next(row for row in d["closed_gates"] if row["id"] == "ram_effective_permissions").__setitem__("status", "passed")),
        ("external_waiver_promoted_to_passed", lambda d: next(row for row in d["closed_gates"] if row["id"] == "template_send_real_fault_matrix").__setitem__("status", "passed")),
        ("waiver_repeat_enabled", lambda d: next(row for row in d["closed_gates"] if row["id"] == "five_business_flow_e2e").__setitem__("must_not_repeat", False)),
        ("pm_closed_as_plain_pass", lambda d: next(row for row in d["closed_gates"] if row["id"] == "pm_phase4_signoff").__setitem__("status", "passed")),
        ("duplicate_gate", lambda d: d["closed_gates"].append(copy.deepcopy(d["closed_gates"][0]))),
        ("defect_reopened", lambda d: d["qa_pm"].__setitem__("p1_open", 1)),
        ("unknown_signed_field", lambda d: d.__setitem__("signed", True)),
    ]
    for name, mutate in attacks:
        expect_rejected(document, mutate, name)

    digest = hashlib.sha256(raw).hexdigest().upper()
    report_digest = hashlib.sha256(report_raw).hexdigest().upper()
    print(
        "status=pass mode=phase4_remaining_gates_contract "
        f"closed={len(EXPECTED_CLOSED)} open={len(EXPECTED_OPEN)} attack_cases={len(attacks)} "
        f"overall=passed_with_project_owner_waivers manifest_sha256={digest} report_sha256={report_digest} "
        "external_access=false writes=false"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

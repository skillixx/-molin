"""只读校验非商业视频金样；金额预期独立冻结，不调用业务计算器或外部服务。"""

import argparse
import json
import re
from decimal import Decimal
from pathlib import Path

PURPOSE = "non_commercial_test_fixture"
EVENTS = {"H": "video_billing_held", "S": "video_billing_settled", "R": "video_billing_released", "P": "video_settlement_pending", "A": "video_delivery_available", "J": "video_delivery_rejected", "C": "video_compensation_required"}
CHECKS = set("request quote hold freeze consume unfreeze usage_fact sale_line cost_line adjustment task task_input output_asset task_event provider_callback_event compensation outbox".split())


def money(value):
    return None if value is None else f"{Decimal(value):.8f}"


def expected_rows():
    # 金额及终态来自已批准F01-F12；None是缺少事实，不是金额0。
    specs = [
        ("F01", "5", ".50", ".20", "5", ".50", "0", "9.50", "0", "succeeded", "settled", "available", "none", "", "AHS", "available", 6, "5"),
        ("F02", "5", ".75", ".30", "5", ".75", "0", "9.25", "0", "succeeded", "settled", "available", "none", "", "AHS", "available", 6, "5"),
        ("F03", "0", "0", ".20", "5", "0", ".50", "10", "0", "failed", "released", "rejected", "none", "", "HJR", "quarantined", 1, "5"),
        ("F04", "0", "0", "0", "0", "0", ".50", "10", "0", "failed", "released", "rejected", "none", "", "HJR", "none", 0, None),
        ("F05", None, None, ".20", "5", None, "0", "9.50", ".50", "failed", "settlement_pending", "pending", "pending", "media_unavailable", "CHP", "none", 0, None),
        ("F06", "5", ".50", ".20", "5", ".50", "0", "9.50", "0", "succeeded", "settled", "available", "completed", "settlement_failed", "ACHPS", "available", 6, "5"),
        ("F07", "0", "0", "0", None, "0", ".50", "10", "0", "cancelled", "released", "rejected", "none", "", "HJR", "none", 0, None),
        ("F08", "0", "0", "0", "0", "0", ".50", "10", "0", "cancelled", "released", "rejected", "none", "", "HJR", "none", 0, None),
        ("F09", None, None, None, None, None, "0", "9.50", ".50", "submitted", "held", "pending", "none", "", "H", "none", 0, None),
        ("F10", "5", ".50", ".20", "5", ".50", "0", "9.50", "0", "succeeded", "settled", "available", "none", "", "AHS", "available", 6, "5"),
        ("F11", None, None, None, None, None, "0", "9.50", ".50", "pending_reconcile", "settlement_pending", "pending", "retry", "provider_unknown", "CHP", "none", 0, None),
        ("F12", None, None, ".24", "6", None, "0", "9.50", ".50", "pending_reconcile", "settlement_pending", "pending", "manual_review", "facts_conflict", "CHP", "temporary", 6, "5"),
        ("F06_before_recovery", None, None, ".20", "5", None, "0", "9.50", ".50", "succeeded", "settlement_pending", "pending", "pending", "settlement_failed", "CHP", "temporary", 6, "5"),
        ("F12_before_review", None, None, ".24", "6", None, "0", "9.50", ".50", "pending_reconcile", "settlement_pending", "pending", "pending", "facts_conflict", "CHP", "temporary", 6, "5"),
    ]
    result = {}
    for case, quantity, sale, cost, provider_quantity, settled, released, balance, frozen, execution, billing, delivery, compensation, origin, events, lifecycle, assets, media in specs:
        quote = money(".75" if case == "F02" else ".50")
        source = "" if cost is None else ("gateway" if case == "F07" else "provider_cost")
        facts = {}
        for key, value in (("gateway/usage_fact", quantity), ("gateway/sale_line", sale), ("provider/usage_fact", provider_quantity), (source + "/cost_line", cost)):
            if value is not None:
                facts[key] = 1
        movements = {"freeze": 1}
        terminal = billing in ("settled", "released")
        if terminal:
            movements["unfreeze"] = 1
        if settled is not None and Decimal(settled) > 0:
            movements["consume"] = 1
        result[case] = dict(case_id=case, purpose=PURPOSE, operation="image_to_video" if case == "F02" else "text_to_video", quote_amount=quote, hold_amount=quote, user_usage_quantity=quantity, sale_amount=money(sale), provider_usage_quantity=provider_quantity, recorded_cost_amount=money(cost), cost_source=source, settled_amount=money(settled), net_released_amount=money(released), wallet_balance_before=money("10"), wallet_balance_after=money(balance), frozen_before=money("0"), frozen_after=money(frozen), execution_status=execution, billing_status=billing, delivery_status=delivery, compensation_status=compensation, compensation_origin=origin, asset_lifecycles={} if assets == 0 else {lifecycle: assets}, media_seconds=media, outbox_events=sorted(EVENTS[e] for e in events), reconciliation_passed=terminal, fake_submit_calls=0 if case == "F07" else 1, usage_fact_counts=facts, wallet_transaction_counts=movements, wallet_freeze_amount=quote, wallet_unfreeze_amount=quote if terminal else money("0"), wallet_consume_amount=money(settled or "0"))
    return result


def same(actual, expected):
    # JSON中的true不能冒充计数1；递归核对字段类型及完整集合。
    if type(actual) is not type(expected):
        return False
    if isinstance(expected, dict):
        return actual.keys() == expected.keys() and all(same(actual[k], v) for k, v in expected.items())
    if isinstance(expected, list):
        return len(actual) == len(expected) and all(same(a, b) for a, b in zip(actual, expected))
    return actual == expected


def require(ok, label):
    if not ok:
        raise ValueError(label)


def verify(document):
    require(type(document) is dict and type(document.get("schema_version")) is int and document["schema_version"] == 1, "证据版本错误")
    root_fields = set("schema_version target_goal purpose stage_acceptance evidence_scope observations totals real_provider_requests real_provider_keys real_wallet_writes real_user_funds real_adjustments test_server_writes production_operations external_http_requests provider_cost_cny next_goal_allowed vid_g6_started".split())
    require(set(document) == root_fields, "证据根字段错误")
    require(document["evidence_scope"] == "隔离Fake金额金样，不是商业可用或完整G5验收", "证据范围不明确")
    require(document["provider_cost_cny"] == "0.00000000" and document["next_goal_allowed"] is False and document["vid_g6_started"] is False, "费用或后续阶段边界错误")
    require(document.get("target_goal") == "VID-G5" and document.get("purpose") == PURPOSE and document.get("stage_acceptance") is False, "金样范围标记错误")
    for key in ("real_provider_requests", "real_provider_keys", "real_wallet_writes", "real_user_funds", "real_adjustments", "test_server_writes", "production_operations", "external_http_requests"):
        require(type(document.get(key)) is int and document[key] == 0, "真实操作边界不为0")
    expected = expected_rows()
    rows = document.get("observations")
    require(type(rows) is list and len(rows) == len(expected), "金样或中间快照数量错误")
    indexed = {}
    for row in rows:
        require(type(row) is dict, "金样格式错误")
        case = row.get("case_id")
        require(case in expected and case not in indexed, "金样重复或未知")
        indexed[case] = row
        require(set(row) == set(expected[case]) | {"request_id", "task_id", "quote_id", "reconciliation_differences"}, "金样存在缺失或额外字段")
        for key, value in expected[case].items():
            require(same(row[key], value), f"{case}.{key}不符合批准金样")
        for key, prefix in (("request_id", "vid_req_g5_"), ("task_id", "vid_task_g5_"), ("quote_id", "vid_quote_")):
            require(type(row[key]) is str and re.fullmatch(prefix + r"[A-Za-z0-9_.-]+", row[key]) is not None, "金样关联标识错误")
        differences = row["reconciliation_differences"]
        require(type(differences) is list and all(type(v) is str and v in CHECKS for v in differences) and differences == sorted(set(differences)), "对账差异字段错误")
        require((not differences) == row["reconciliation_passed"], "未闭合与差异标记矛盾")
    finals = [indexed[f"F{i:02d}"] for i in range(1, 13)]
    for field in ("request_id", "task_id", "quote_id"):
        require(len({r[field] for r in finals}) == 12, "独立金样错误复用事实")
        for middle, final in (("F06_before_recovery", "F06"), ("F12_before_review", "F12")):
            require(indexed[middle][field] == indexed[final][field], "中间快照未关联原请求")
    totals = document.get("totals")
    require(type(totals) is list and len(totals) == 3, "汇总数量错误")
    seen = set()
    for total in totals:
        require(type(total) is dict, "汇总格式错误")
        scope = total.get("scope")
        require(scope in ("text_to_video", "image_to_video", "all") and scope not in seen, "汇总范围错误")
        seen.add(scope)
        selected = [r for r in finals if scope == "all" or r["operation"] == scope]
        def summed(key):
            return sum((Decimal(r[key] or "0") for r in selected), Decimal(0))
        quote, sale, released, frozen = (summed(k) for k in ("quote_amount", "sale_amount", "net_released_amount", "frozen_after"))
        calculated = dict(scope=scope, requests=len(selected), quote_hold=money(quote), posted_sale=money(sale), known_cost_subtotal=money(summed("recorded_cost_amount")), unknown_cost_requests=sum(r["recorded_cost_amount"] is None for r in selected), net_released=money(released), wallet_balance_after=money(summed("wallet_balance_after")), frozen_after=money(frozen), conservation_difference=money(quote-sale-released-frozen), all_requests_finally_reconciled=all(r["reconciliation_passed"] for r in selected))
        require(same(total, calculated), "汇总与逐请求事实不一致")
        require(quote == sale + released + frozen, "汇总资金不守恒")
    return {"cases": 12, "intermediate_snapshots": 2, "totals": 3, "stage_acceptance": False}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "JSON重复字段")
        result[key] = value
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    args = parser.parse_args()
    try:
        document = json.loads(args.evidence.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
        result = verify(document)
    except (ValueError, KeyError, TypeError, OSError, ArithmeticError):
        print("VID_G5_GOLDENS=FAIL")
        return 1
    print("VID_G5_GOLDENS=PASS " + json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

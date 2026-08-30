"""以真实隔离运行导出的金样为基准，检验独立校验器对篡改的敏感性。"""

import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("video_g5_goldens", ROOT / "scripts/verify-video-gateway-vid-g5-goldens.py")
VERIFIER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFIER)


class VideoG5GoldenVerificationTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads((ROOT / "docs/evidence/video-gateway-vid-g5-golden-amounts.json").read_text(encoding="utf-8"))

    def row(self, document, case):
        return next(row for row in document["observations"] if row["case_id"] == case)

    def test_real_export_passes(self):
        self.assertEqual(VERIFIER.verify(self.document), {"cases": 12, "intermediate_snapshots": 2, "totals": 3, "stage_acceptance": False})

    def test_mutations_fail_closed(self):
        # 重复计量和零金额消费不改变数值汇总，仍必须被严格数量合同拒绝。
        mutations = {
            "未知成本伪零": lambda d: self.row(d, "F11").update(recorded_cost_amount="0.00000000"),
            "开放请求重复计量": lambda d: self.row(d, "F05")["usage_fact_counts"].update({"provider/usage_fact": 2}),
            "忽略来源计量": lambda d: self.row(d, "F12")["usage_fact_counts"].update({"reconciled/usage_fact": 1}),
            "零金额额外消费": lambda d: self.row(d, "F05")["wallet_transaction_counts"].update(consume=1),
            "错误成本来源": lambda d: self.row(d, "F04").update(cost_source="gateway"),
            "网关成本冒充Provider": lambda d: self.row(d, "F07").update(cost_source="provider_cost"),
            "错误F12终态": lambda d: self.row(d, "F12").update(compensation_status="pending"),
            "补偿前假交付": lambda d: self.row(d, "F06_before_recovery").update(delivery_status="available"),
            "错误冻结金额": lambda d: self.row(d, "F09").update(frozen_after="0.00000000"),
            "错误媒体计量": lambda d: self.row(d, "F12").update(media_seconds="6"),
            "I2V错价": lambda d: self.row(d, "F02").update(sale_amount="0.50000000"),
            "错误中间关联": lambda d: self.row(d, "F06_before_recovery").update(quote_id="vid_quote_other"),
            "少Outbox": lambda d: self.row(d, "F11")["outbox_events"].pop(),
            "敏感字段": lambda d: self.row(d, "F01").update(prompt="forbidden_fixture_field"),
            "布尔冒充计数": lambda d: self.row(d, "F01")["wallet_transaction_counts"].update(freeze=True),
            "未知被汇总忽略": lambda d: d["totals"][2].update(unknown_cost_requests=0),
            "守恒冒充全部闭合": lambda d: d["totals"][2].update(all_requests_finally_reconciled=True),
            "错误成本小计": lambda d: d["totals"][2].update(known_cost_subtotal="0.00000000"),
            "少金样": lambda d: d["observations"].pop(),
            "重复金样": lambda d: self.row(d, "F02").update(case_id="F01"),
            "伪商业验收": lambda d: d.update(stage_acceptance=True),
            "真实调用非零": lambda d: d.update(real_provider_requests=1),
            "证据版本错误": lambda d: d.update(schema_version=2),
            "根敏感字段": lambda d: d.update(prompt="forbidden_fixture_field"),
            "真实费用非零": lambda d: d.update(provider_cost_cny="1.00000000"),
            "额外外部请求": lambda d: d.update(external_http_requests=1),
            "越阶段标记": lambda d: d.update(vid_g6_started=True),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                document = copy.deepcopy(self.document)
                mutate(document)
                with self.assertRaises(ValueError):
                    VERIFIER.verify(document)

    def test_duplicate_json_fields_rejected(self):
        with self.assertRaises(ValueError):
            json.loads('{"case_id":"F01","case_id":"F02"}', object_pairs_hook=VERIFIER.unique_object)


if __name__ == "__main__":
    unittest.main()

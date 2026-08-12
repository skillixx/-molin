#!/usr/bin/env python3
"""验证 G8 测试到生产迁移清单的失败关闭契约。"""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("verify-ai-gateway-g8-migration-manifest.py")


def load_module():
    """按脚本文件路径加载校验器，避免因连字符名称引入额外包结构。"""
    spec = importlib.util.spec_from_file_location("g8_migration_manifest", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载 G8 迁移清单校验器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def valid_manifest(stage: str = "test_candidate") -> dict:
    """生成不含 Secret 的最小有效夹具。"""
    return {
        "schema_version": 1,
        "stage": stage,
        "change_id": "CHG-G8-TEST-20260812-001",
        "source": {
            "environment": "test",
            "host_alias": "molin-test",
            "deployment_kind": "host_binary",
            "deployment_root": "molin-test-root",
            "database_alias": "molin-test-mysql",
        },
        "target": {
            "environment": "production",
            "host_alias": "PENDING",
            "deployment_kind": "PENDING",
            "deployment_root": "PENDING",
            "database_alias": "PENDING",
            "domain": "PENDING",
        },
        "release": {
            "source_commit": "a" * 40,
            "api_artifact_sha256": "b" * 64,
            "config_sha256": "c" * 64,
            "bifrost_image_digest": f"sha256:{'d' * 64}",
            "migration_version": 66,
            "migration_dirty": False,
        },
        "scope": {
            "text_models_only": True,
            "excluded_capabilities": [
                "image",
                "audio",
                "video",
                "multimodal_async",
                "object_storage_lifecycle",
                "gpu",
                "agent",
                "skills",
                "public_self_service_payment",
            ],
        },
        "traffic": {
            "application_gate_enabled": False,
            "edge_gate_open": False,
            "real_customer_count": 0,
        },
        "authorization": {
            "production_readonly": False,
            "production_deploy": False,
            "paid_upstream": False,
            "customer_gray": False,
            "alert_notification": False,
            "max_requests": 0,
            "max_cost_cny": "0",
        },
        "chain": {
            "previous_stage": "none",
            "previous_manifest_sha256": "NONE",
            "approval_receipt_sha256": "NONE",
            "credential_rotation_receipt_sha256": "NONE",
        },
        "models": {
            "approved_model_count": 0,
            "approved_upstream_count": 0,
            "pricing_approved": False,
            "minimum_margin_rate": "0.15",
        },
        "evidence": {
            "backup_readable": False,
            "rollback_rehearsed": True,
            "reconciliation_zero": True,
            "alerts_local_only": True,
            "credentials_separated": False,
            "test_credentials_rotated": False,
        },
    }


def valid_manifest_chain(module, final_stage: str) -> list[dict]:
    """生成摘要相连的四阶段夹具，阶段授权只表达测试数据而非真实批准。"""
    stages = module.STAGE_ORDER[: module.STAGE_ORDER.index(final_stage) + 1]
    manifests = [valid_manifest()]
    for index, stage in enumerate(stages[1:], start=1):
        previous = manifests[-1]
        manifest = deepcopy(previous)
        manifest["stage"] = stage
        manifest["change_id"] = f"CHG-G8-TEST-20260812-00{index + 1}"
        manifest["target"].update(
            {
                "host_alias": "molin-production",
                "deployment_kind": "docker_compose",
                "deployment_root": "/srv/molin",
                "database_alias": "molin-production-mysql",
                "domain": "api.example.invalid",
            }
        )
        manifest["chain"].update(
            {
                "previous_stage": previous["stage"],
                "previous_manifest_sha256": module.manifest_sha256(previous),
                "approval_receipt_sha256": str(index) * 64,
            }
        )
        if stage == "production_readonly":
            manifest["authorization"]["production_readonly"] = True
        elif stage == "production_closed_deploy":
            manifest["authorization"]["production_deploy"] = True
            manifest["chain"]["credential_rotation_receipt_sha256"] = "a" * 64
            manifest["evidence"].update(
                {
                    "backup_readable": True,
                    "credentials_separated": True,
                    "test_credentials_rotated": True,
                }
            )
        else:
            manifest["authorization"].update(
                {
                    "paid_upstream": True,
                    "customer_gray": True,
                    "alert_notification": True,
                    "max_requests": 1,
                    "max_cost_cny": "1",
                }
            )
            manifest["traffic"].update(
                {"application_gate_enabled": True, "edge_gate_open": True, "real_customer_count": 1}
            )
            manifest["models"].update(
                {"approved_model_count": 5, "approved_upstream_count": 2, "pricing_approved": True}
            )
            manifest["evidence"]["alerts_local_only"] = False
        manifests.append(manifest)
    return manifests


class MigrationManifestTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.module = load_module()

    def test_test_candidate_accepts_closed_non_production_state(self) -> None:
        self.module.validate_manifest(valid_manifest())

    def test_rejects_unknown_secret_field(self) -> None:
        manifest = valid_manifest()
        manifest["source"]["password"] = "forbidden"
        with self.assertRaisesRegex(ValueError, "字段集合不合法"):
            self.module.validate_manifest(manifest)

    def test_rejects_open_traffic_in_test_candidate(self) -> None:
        manifest = valid_manifest()
        manifest["traffic"]["edge_gate_open"] = True
        with self.assertRaisesRegex(ValueError, "测试候选阶段必须保持双总闸关闭"):
            self.module.validate_manifest(manifest)

    def test_rejects_non_string_excluded_capability_without_stack_trace(self) -> None:
        manifest = valid_manifest()
        manifest["scope"]["excluded_capabilities"][0] = {"image": True}
        with self.assertRaisesRegex(ValueError, "排除能力必须为字符串数组"):
            self.module.validate_manifest(manifest)

    def test_rejects_boolean_schema_version(self) -> None:
        manifest = valid_manifest()
        manifest["schema_version"] = True
        with self.assertRaisesRegex(ValueError, "schema_version 必须为 1"):
            self.module.validate_manifest(manifest)

    def test_rejects_production_authorization_in_test_candidate(self) -> None:
        manifest = valid_manifest()
        manifest["authorization"]["production_readonly"] = True
        with self.assertRaisesRegex(ValueError, "不得声明生产授权"):
            self.module.validate_manifest(manifest)

    def test_rejects_receipt_claim_in_test_candidate(self) -> None:
        manifest = valid_manifest()
        manifest["chain"]["approval_receipt_sha256"] = "a" * 64
        with self.assertRaisesRegex(ValueError, "测试候选阶段不得伪造"):
            self.module.validate_manifest(manifest)

    def test_production_gray_requires_approved_models_budget_and_evidence(self) -> None:
        manifest = valid_manifest("production_gray")
        manifest["target"].update(
            {
                "host_alias": "molin-production",
                "deployment_kind": "docker_compose",
                "deployment_root": "/srv/molin",
                "database_alias": "molin-production-mysql",
                "domain": "api.example.invalid",
            }
        )
        manifest["evidence"].update(
            {
                "backup_readable": True,
                "credentials_separated": True,
                "test_credentials_rotated": True,
            }
        )
        manifest["chain"].update(
            {
                "previous_stage": "production_closed_deploy",
                "previous_manifest_sha256": "e" * 64,
                "approval_receipt_sha256": "f" * 64,
                "credential_rotation_receipt_sha256": "a" * 64,
            }
        )
        with self.assertRaisesRegex(ValueError, "生产灰度授权必须完整"):
            self.module.validate_manifest(manifest)

    def test_accepts_complete_ordered_chain_for_each_stage(self) -> None:
        for stage in self.module.STAGE_ORDER:
            with self.subTest(stage=stage):
                self.module.validate_chain(valid_manifest_chain(self.module, stage))

    def test_rejects_direct_production_gray_without_predecessor_chain(self) -> None:
        gray = valid_manifest_chain(self.module, "production_gray")[-1]
        with self.assertRaisesRegex(ValueError, "必须从 test_candidate 开始"):
            self.module.validate_chain([gray])

    def test_rejects_predecessor_digest_mismatch(self) -> None:
        manifests = valid_manifest_chain(self.module, "production_closed_deploy")
        manifests[-1]["chain"]["previous_manifest_sha256"] = "f" * 64
        with self.assertRaisesRegex(ValueError, "前序清单摘要不匹配"):
            self.module.validate_chain(manifests)

    def test_rejects_invalid_production_target_and_margin_one(self) -> None:
        manifests = valid_manifest_chain(self.module, "production_gray")
        manifests[-1]["target"]["domain"] = "not a domain"
        with self.assertRaisesRegex(ValueError, "target.domain 格式不合法"):
            self.module.validate_chain(manifests)
        manifests = valid_manifest_chain(self.module, "production_gray")
        manifests[-1]["models"]["minimum_margin_rate"] = "1"
        with self.assertRaisesRegex(ValueError, "1（不含）"):
            self.module.validate_chain(manifests)

    def test_cli_summary_never_contains_manifest_values(self) -> None:
        summary = self.module.success_summary(valid_manifest())
        serialized = json.dumps(valid_manifest(), ensure_ascii=False)
        self.assertNotIn("molin-test", summary)
        self.assertNotIn("api.example", summary)
        self.assertNotIn(serialized, summary)
        self.assertRegex(
            summary,
            r"^G8_MIGRATION_MANIFEST=PASS stage=test_candidate production_authorized=false "
            r"traffic_open=false receipt_sha256=[0-9a-f]{64} secrets_read=false$",
        )

    def test_cli_rejects_duplicate_json_key_without_traceback(self) -> None:
        manifest = json.dumps(valid_manifest(), ensure_ascii=False)
        duplicate_manifest = manifest.replace(
            '"schema_version": 1,',
            '"schema_version": 1, "schema_version": 1,',
            1,
        )
        with tempfile.TemporaryDirectory() as temporary_directory:
            manifest_path = Path(temporary_directory) / "manifest.json"
            manifest_path.write_text(duplicate_manifest, encoding="utf-8")
            result = subprocess.run(
                [sys.executable, str(SCRIPT_PATH), "--manifest", str(manifest_path)],
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={**os.environ, "PYTHONIOENCODING": "utf-8"},
                check=False,
            )
        self.assertEqual(result.returncode, 2)
        self.assertIn("G8_MIGRATION_MANIFEST=FAILED", result.stdout)
        self.assertNotIn("Traceback", result.stdout + result.stderr)
        self.assertNotIn("schema_version", result.stdout + result.stderr)

    def test_cli_failure_does_not_echo_attacker_controlled_field_name(self) -> None:
        manifest = valid_manifest()
        manifest["BEARER_FAKE_SENSITIVE_MARKER"] = True
        with tempfile.TemporaryDirectory() as temporary_directory:
            manifest_path = Path(temporary_directory) / "manifest.json"
            manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
            result = subprocess.run(
                [sys.executable, str(SCRIPT_PATH), "--manifest", str(manifest_path)],
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={**os.environ, "PYTHONIOENCODING": "utf-8"},
                check=False,
            )
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout.strip(), "G8_MIGRATION_MANIFEST=FAILED reason=invalid_manifest")
        self.assertNotIn("BEARER_FAKE_SENSITIVE_MARKER", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

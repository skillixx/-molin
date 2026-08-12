#!/usr/bin/env python3
"""验证 G8 测试到生产迁移清单的失败关闭契约。"""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
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

    def test_production_gray_requires_approved_models_budget_and_evidence(self) -> None:
        manifest = valid_manifest("production_gray")
        manifest["target"].update(
            {
                "host_alias": "molin-production",
                "deployment_kind": "docker_compose",
                "deployment_root": "molin-production-root",
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
        with self.assertRaisesRegex(ValueError, "生产灰度授权必须完整"):
            self.module.validate_manifest(manifest)

    def test_cli_summary_never_contains_manifest_values(self) -> None:
        summary = self.module.success_summary(valid_manifest())
        serialized = json.dumps(valid_manifest(), ensure_ascii=False)
        self.assertNotIn("molin-test", summary)
        self.assertNotIn("api.example", summary)
        self.assertNotIn(serialized, summary)
        self.assertEqual(
            summary,
            "G8_MIGRATION_MANIFEST=PASS stage=test_candidate production_authorized=false "
            "traffic_open=false secrets_read=false",
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


if __name__ == "__main__":
    unittest.main()

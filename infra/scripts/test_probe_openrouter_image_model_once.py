import base64
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest
import urllib.error


SCRIPT_PATH = pathlib.Path(__file__).with_name("probe-openrouter-image-model-once.py")


def load_script():
    """按命令行文件名加载探针模块，测试不建立真实网络连接。"""
    spec = importlib.util.spec_from_file_location("probe_openrouter_image_model_once", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_config():
    """返回不含密钥的固定POC配置。"""
    return {
        "schema_version": 1,
        "base_url": "https://openrouter.ai/api/v1",
        "api_key_env": "OPENROUTER_API_KEY",
        "model": "bytedance-seed/seedream-5-0-lite",
        "provider_tag": "seed",
        "catalog": {
            "models_path": "/images/models",
            "endpoints_path": "/images/models/bytedance-seed/seedream-5-0-lite/endpoints",
            "expected_billable": "output_image",
            "expected_pricing_unit": "image",
            "expected_cost_usd": "0.035",
        },
        "generation": {
            "path": "/images",
            "resolution": "2K",
            "aspect_ratio": "1:1",
            "n": 1,
            "stream": False,
            "allow_fallbacks": False,
            "prompt": "测试专用白色陶瓷杯",
        },
        "limits": {
            "max_response_bytes": 1024 * 1024,
            "max_decoded_image_bytes": 512 * 1024,
            "timeout_seconds": 120,
            "max_retries": 0,
            "max_actual_cost_usd": "0.04",
        },
        "image_validation": {
            "allowed_formats": ["png", "jpeg", "webp"],
            "min_width": 1920,
            "max_width": 2304,
            "min_height": 1920,
            "max_height": 2304,
            "expected_aspect_ratio": "1:1",
            "aspect_ratio_tolerance": "0.01",
            "max_pixels": 5308416,
        },
        "authorization": {
            "change_id": "IMG-OPENROUTER-POC-20260824-001",
            "max_real_requests": 1,
            "status": "pending",
            "consumed_at": None,
        },
        "logging": {
            "print_prompt": False,
            "print_response_body": False,
            "print_image_data": False,
            "print_api_key": False,
        },
    }


def fake_decoder(raw, image_validation):
    """模拟完整图片解码，只向业务层返回低敏格式和尺寸。"""
    if not raw.startswith(b"\x89PNG\r\n\x1a\n"):
        raise ValueError("test_image_invalid")
    return "png", 2048, 2048


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._body = body

    def read(self, limit=-1):
        if limit is None or limit < 0:
            return self._body
        return self._body[:limit]

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, traceback):
        return False


class RecordingOpener:
    def __init__(self, fail_generation=False, omit_media_type=False):
        self.requests = []
        self.fail_generation = fail_generation
        self.omit_media_type = omit_media_type

    def open(self, request, timeout):
        self.requests.append((request, timeout))
        if request.full_url.endswith("/images/models"):
            payload = {
                "data": [
                    {
                        "id": "bytedance-seed/seedream-5-0-lite",
                        "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["image"]},
                        "supported_parameters": {
                            "resolution": {"type": "enum", "values": ["2K", "4K"]},
                            "aspect_ratio": {"type": "enum", "values": ["1:1", "16:9"]},
                            "n": {"type": "range", "min": 1, "max": 4},
                        },
                        "supports_streaming": False,
                    }
                ]
            }
            return FakeResponse(200, json.dumps(payload).encode("utf-8"))
        if request.full_url.endswith("/endpoints"):
            payload = {
                "id": "bytedance-seed/seedream-5-0-lite",
                "endpoints": [
                    {
                        "provider_tag": "seed",
                        "supported_parameters": {
                            "resolution": {"type": "enum", "values": ["2K", "4K"]},
                            "aspect_ratio": {"type": "enum", "values": ["1:1", "16:9"]},
                            "n": {"type": "range", "min": 1, "max": 4},
                        },
                        "supports_streaming": False,
                        "pricing": [{"billable": "output_image", "unit": "image", "cost_usd": 0.035}],
                    }
                ],
            }
            return FakeResponse(200, json.dumps(payload).encode("utf-8"))
        if self.fail_generation:
            raise urllib.error.URLError("test-only-generation-failure")
        image_item = {
            "b64_json": base64.b64encode(b"\x89PNG\r\n\x1a\n" + b"test-only-image-body").decode("ascii")
        }
        if not self.omit_media_type:
            image_item["media_type"] = "image/png"
        payload = {
            "created": 1,
            "data": [image_item],
            "usage": {"prompt_tokens": 0, "completion_tokens": 1, "total_tokens": 1, "cost": 0.035},
        }
        return FakeResponse(200, json.dumps(payload).encode("utf-8"))


class ProbeOpenRouterImageModelOnceTest(unittest.TestCase):
    def setUp(self):
        self.module = load_script()

    def test_repository_poc_config_keeps_consumed_tombstone(self):
        """直接锁定仓库配置的已消费状态，防止CI误放开第二次付费请求。"""
        config = self.module.load_config()
        authorization = config["authorization"]

        self.assertEqual(authorization["change_id"], "IMG-OPENROUTER-POC-20260824-001")
        self.assertEqual(authorization["max_real_requests"], 1)
        self.assertEqual(authorization["status"], "consumed")
        self.assertEqual(authorization["consumed_at"], "2026-08-24T13:36:14.305288Z")
        self.assertNotIn("prompt", config["generation"])
        self.assertEqual(config["generation"]["prompt_env"], "OPENROUTER_IMAGE_POC_PROMPT")

    def test_catalog_check_validates_model_endpoint_and_per_image_pricing(self):
        opener = RecordingOpener()
        result = self.module.catalog_check(test_config(), opener=opener)

        self.assertEqual(len(opener.requests), 2)
        self.assertEqual(result["CATALOG_CHECK"], "PASS")
        self.assertEqual(result["MODEL_AVAILABLE"], "PASS")
        self.assertEqual(result["PARAMETERS_MATCH"], "PASS")
        self.assertEqual(result["PRICING_UNIT"], "image")
        self.assertEqual(result["CATALOG_COST_USD"], "0.035")
        self.assertEqual(result["REAL_REQUEST_ATTEMPTED"], "NO")

    def test_execute_once_requires_all_gates_before_network(self):
        opener = RecordingOpener()
        with tempfile.TemporaryDirectory() as temp_dir:
            receipt = pathlib.Path(temp_dir, "receipt.json")
            output = io.StringIO()
            code = self.module.main(
                ["--execute-once"],
                environ={"OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48},
                output=output,
                opener=opener,
                config=test_config(),
                receipt_path=receipt,
            )

        self.assertEqual(code, 2)
        self.assertEqual(opener.requests, [])
        self.assertIn("EXECUTION_AUTHORIZED=NO", output.getvalue())

    def test_execute_once_sends_one_generation_and_writes_low_sensitivity_receipt(self):
        opener = RecordingOpener()
        env = {
            "OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48,
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            receipt = pathlib.Path(temp_dir, "receipt.json")
            env["IMAGE_GATEWAY_REAL_RECEIPT_PATH"] = str(receipt)
            output = io.StringIO()
            code = self.module.main(
                ["--execute-once"], env, output, opener=opener, config=test_config(), image_decoder=fake_decoder
            )
            receipt_data = json.loads(receipt.read_text(encoding="utf-8"))

        self.assertEqual(code, 0)
        self.assertEqual(len(opener.requests), 3)
        generation_request = opener.requests[-1][0]
        body = json.loads(generation_request.data.decode("utf-8"))
        self.assertEqual(generation_request.full_url, "https://openrouter.ai/api/v1/images")
        self.assertEqual(body["model"], "bytedance-seed/seedream-5-0-lite")
        self.assertEqual(body["n"], 1)
        self.assertEqual(body["resolution"], "2K")
        self.assertEqual(body["aspect_ratio"], "1:1")
        self.assertEqual(body["provider"], {"only": ["seed"], "allow_fallbacks": False})
        self.assertNotIn("sk-or-v1", output.getvalue())
        self.assertEqual(receipt_data["status"], "completed")
        self.assertEqual(receipt_data["real_requests"], 1)
        self.assertEqual(receipt_data["image_signature"], "png")
        self.assertEqual(receipt_data["image_width"], 2048)
        self.assertEqual(receipt_data["image_height"], 2048)
        self.assertEqual(receipt_data["image_aspect_ratio"], "1:1")
        self.assertTrue(receipt_data["image_decode_valid"])
        self.assertEqual(receipt_data["provider_tag"], "seed")
        self.assertEqual(receipt_data["requested_resolution"], "2K")
        self.assertEqual(receipt_data["http_status"], 200)
        self.assertEqual(receipt_data["actual_cost_usd"], "0.035")
        self.assertEqual(receipt_data["catalog_cost_usd"], "0.035")
        self.assertEqual(receipt_data["cost_match"], True)
        self.assertEqual(len(receipt_data["image_sha256"]), 64)

    def test_missing_media_type_is_accepted_after_full_decode(self):
        opener = RecordingOpener(omit_media_type=True)
        env = {
            "OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48,
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            receipt = pathlib.Path(temp_dir, "receipt.json")
            code = self.module.main(
                ["--execute-once"], env, io.StringIO(), opener=opener,
                config=test_config(), receipt_path=receipt, image_decoder=fake_decoder
            )
            receipt_data = json.loads(receipt.read_text(encoding="utf-8"))
        self.assertEqual(code, 0)
        self.assertEqual(receipt_data["status"], "completed")
        self.assertEqual(receipt_data["image_width"], 2048)

    def test_generation_failure_is_zero_retry_and_receipt_blocks_replay(self):
        opener = RecordingOpener(fail_generation=True)
        env = {
            "OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48,
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            receipt = pathlib.Path(temp_dir, "receipt.json")
            first = self.module.main(
                ["--execute-once"], env, io.StringIO(), opener=opener, config=test_config(),
                receipt_path=receipt, image_decoder=fake_decoder
            )
            second_opener = RecordingOpener()
            second = self.module.main(
                ["--execute-once"], env, io.StringIO(), opener=second_opener, config=test_config(),
                receipt_path=receipt, image_decoder=fake_decoder
            )

        self.assertEqual(first, 2)
        self.assertEqual(len(opener.requests), 3)
        self.assertEqual(second, 2)
        self.assertEqual(second_opener.requests, [])

    def test_invalid_api_key_is_rejected_before_network(self):
        opener = RecordingOpener()
        env = {
            "OPENROUTER_API_KEY": "short",
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            code = self.module.main(
                ["--execute-once"], env, io.StringIO(), opener=opener,
                config=test_config(), receipt_path=pathlib.Path(temp_dir, "receipt.json")
            )
        self.assertEqual(code, 2)
        self.assertEqual(opener.requests, [])

    def test_receipt_path_inside_repository_is_rejected_before_network(self):
        opener = RecordingOpener()
        env = {
            "OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48,
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
            "IMAGE_GATEWAY_REAL_RECEIPT_PATH": str(SCRIPT_PATH.parent / "forbidden-receipt.json"),
        }
        code = self.module.main(["--execute-once"], env, io.StringIO(), opener=opener, config=test_config())
        self.assertEqual(code, 2)
        self.assertEqual(opener.requests, [])
        self.assertFalse((SCRIPT_PATH.parent / "forbidden-receipt.json").exists())

    def test_consumed_change_id_is_rejected_before_network(self):
        opener = RecordingOpener()
        config = test_config()
        config["authorization"]["status"] = "consumed"
        config["authorization"]["consumed_at"] = "2026-08-24T13:36:14.305288Z"
        env = {
            "OPENROUTER_API_KEY": "sk-or-v1-" + "a" * 48,
            "IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST": "YES",
            "IMAGE_GATEWAY_REAL_REQUEST_LIMIT": "1",
            "IMAGE_GATEWAY_REAL_CHANGE_ID": "IMG-OPENROUTER-POC-20260824-001",
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            output = io.StringIO()
            code = self.module.main(
                ["--execute-once"], env, output, opener=opener, config=config,
                receipt_path=pathlib.Path(temp_dir, "receipt.json"), image_decoder=fake_decoder
            )
        self.assertEqual(code, 2)
        self.assertEqual(opener.requests, [])
        self.assertIn("ERROR_CLASS=change_id_consumed", output.getvalue())


if __name__ == "__main__":
    unittest.main()

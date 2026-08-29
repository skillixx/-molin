import importlib.util
import hashlib
import io
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock


SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
PROBE_PATH = SCRIPT_DIR / "probe-bifrost-video-contract.py"
FAKE_PATH = SCRIPT_DIR / "video-g0-fake-upstream.py"


def load_probe_module():
    """按命令行文件名加载探针，避免测试依赖Python包安装。"""
    spec = importlib.util.spec_from_file_location("probe_bifrost_video_contract", PROBE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def load_fake_module():
    """加载Fake上游模块，用纯函数验证multipart参考图识别。"""
    spec = importlib.util.spec_from_file_location("video_g0_fake_upstream", FAKE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class ProbeBifrostVideoContractTest(unittest.TestCase):
    def setUp(self):
        self.module = load_probe_module()

    def test_legacy_image_verifiers_apply_video_schema_before_current_head_tests(self):
        """旧阶段脚本必须先完成本阶段断言，再为当前HEAD测试补装000072兼容层。"""
        script_expectations = {
            "verify-ai-gateway-migration-000062.sh": "video_g1_compat_up",
            "verify-image-gateway-migration-000069.sh": "000072_expand_video_gateway_schema.up.sql",
            "verify-image-gateway-migration-000070.sh": "000072_expand_video_gateway_schema.up.sql",
            "verify-image-gateway-migration-000071.sh": "000072_expand_video_gateway_schema.up.sql",
            "verify-image-gateway-img-g6-http.sh": '"${version}" -le 72',
            "verify-image-gateway-img-g7-infrastructure.sh": '"${version}" -le 72',
        }
        for script_name, marker in script_expectations.items():
            content = (SCRIPT_DIR / script_name).read_text(encoding="utf-8")
            self.assertIn(marker, content, script_name)

    def test_locked_image_uses_exact_v2_digest(self):
        """G0-B必须锁定当前复核的镜像摘要，禁止使用浮动标签。"""
        self.assertEqual(self.module.BIFROST_IMAGE_TAG, "maximhq/bifrost:v2.0.0")
        self.assertEqual(
            self.module.BIFROST_IMAGE_DIGEST,
            "maximhq/bifrost@sha256:cf71be9fad4e0749b6e26cbb774c687413dad9a0970b83f4e1dadb6f503ea208",
        )

    def test_source_state_excludes_only_generated_evidence(self):
        """源码快照只排除会递归写入自身的各阶段生成型证据。"""
        self.assertEqual(
            self.module.SOURCE_STATE_EXCLUDES,
            {
                "docs/evidence/video-gateway-vid-g0-bifrost-contract.json",
                "docs/evidence/video-gateway-vid-g0-source-state.json",
                "docs/evidence/video-gateway-vid-g1-mysql-contract.json",
                "docs/evidence/video-gateway-vid-g1-source-state.json",
                "docs/evidence/video-gateway-vid-g2-source-state.json",
                "docs/evidence/video-gateway-vid-g3-source-state.json",
                "docs/evidence/video-gateway-vid-g4-source-state.json",
                "docs/evidence/video-gateway-vid-g4-acceptance.json",
                "docs/evidence/video-gateway-vid-g4-independent-reviews.md",
            },
        )

    def test_remote_url_is_redacted(self):
        """源码清单不能暴露仓库所有者标识或任何凭据。"""
        self.assertEqual(
            self.module.redact_remote_url("https://github.com/example-owner/-molin.git"),
            "github.com/<owner>/-molin.git",
        )

    def test_config_disables_logging_retry_and_fallback(self):
        """隔离探针必须关闭正文日志、重试和fallback，并只指向本地Fake上游。"""
        config = self.module.build_bifrost_config("http://host.docker.internal:19091")
        provider = config["providers"]["openai"]

        self.assertFalse(config["client"]["enable_logging"])
        self.assertEqual(provider["network_config"]["max_retries"], 0)
        self.assertTrue(provider["network_config"]["allow_private_network"])
        self.assertEqual(provider["network_config"]["base_url"], "http://host.docker.internal:19091")
        self.assertEqual(provider["keys"][0]["value"], "env.OPENAI_API_KEY")
        self.assertEqual(provider["keys"][0]["models"], ["sora-2"])
        self.assertNotIn("fallbacks", config)

    def test_contract_validation_accepts_complete_zero_retry_result(self):
        """文生、图生和四类资源操作齐全且故障只提交一次时才允许通过。"""
        result = self.module.example_contract_result()

        self.assertEqual(self.module.validate_contract_result(result), [])

    def test_contract_validation_rejects_hidden_retry(self):
        """仅配置max_retries=0不够，Fake计数出现第二次提交必须失败。"""
        result = self.module.example_contract_result()
        result["fake_counts"]["ack_drop"] = 2

        errors = self.module.validate_contract_result(result)

        self.assertIn("ack_drop_submit_count", errors)

    def test_contract_validation_checks_failure_http_semantics(self):
        """500、超时和ACK丢失必须保持冻结的网关错误语义。"""
        result = self.module.example_contract_result()
        result["failure_http"] = {"upstream_500": 200, "upstream_timeout": 200, "ack_drop": 200}

        errors = self.module.validate_contract_result(result)

        self.assertIn("upstream_500_http", errors)
        self.assertIn("upstream_timeout_http", errors)
        self.assertIn("ack_drop_http", errors)

    def test_contract_validation_checks_authorization_per_scenario(self):
        """每种创建场景都必须证明Fake鉴权已转发，不能使用全局OR替代。"""
        result = self.module.example_contract_result()
        result["authorization_counts"]["image_to_video"] = 0

        errors = self.module.validate_contract_result(result)

        self.assertIn("image_to_video_authorization", errors)

    def test_contract_validation_requires_image_reference(self):
        """图生视频必须确认参考图到达上游，不能只证明文生视频。"""
        result = self.module.example_contract_result()
        result["image_to_video"]["input_reference_forwarded"] = False

        errors = self.module.validate_contract_result(result)

        self.assertIn("image_reference_forwarding", errors)

    def test_contract_validation_requires_exact_image_bytes(self):
        """参考图长度或SHA-256不一致时，图生视频合同必须失败。"""
        result = self.module.example_contract_result()
        result["image_to_video"]["input_reference_size_match"] = False
        result["image_to_video"]["input_reference_sha256_match"] = False

        errors = self.module.validate_contract_result(result)

        self.assertIn("image_reference_size", errors)
        self.assertIn("image_reference_sha256", errors)

    def test_ack_drop_retry_freezes_native_driver(self):
        """ACK丢失重复提交是Bifrost禁用墓碑，不得被误报为合同通过。"""
        result = self.module.example_contract_result()
        result["fake_counts"]["ack_drop"] = 4
        result["authorization_counts"]["ack_drop"] = 4

        decision = self.module.classify_driver_result(result)

        self.assertEqual(decision["contract_status"], "blocked_ack_drop_hidden_retry")
        self.assertEqual(decision["driver"], "native_async")
        self.assertFalse(decision["bifrost_video_enabled"])

    def test_receipt_projection_excludes_sensitive_material(self):
        """仓库回执只保留低敏断言，不保存Prompt、鉴权头或媒体正文。"""
        receipt = self.module.build_receipt(self.module.example_contract_result(), "source-state-test")
        serialized = self.module.json_dumps(receipt)

        self.assertNotIn("Authorization", serialized)
        self.assertNotIn("prompt", serialized.lower())
        self.assertNotIn("base64", serialized.lower())
        self.assertNotIn("fake-key", serialized.lower())
        self.assertNotIn("video-bytes", serialized.lower())
        self.assertEqual(receipt["real_provider_requests"], 0)
        self.assertEqual(receipt["provider_cost"], "CNY 0")
        self.assertEqual(receipt["driver_decision"], "bifrost_candidate")
        self.assertTrue(receipt["bifrost_video_enabled"])
        self.assertEqual(receipt["assertion_total"], len(self.module.contract_checks(self.module.example_contract_result())))

    def test_fake_detects_multipart_image_reference(self):
        """Bifrost把data URL转换为multipart文件后仍应识别为图生视频。"""
        fake = load_fake_module()
        reference = b"\x89PNG\r\n\x1a\ncontract-image"
        body = (
            b'--boundary\r\nContent-Disposition: form-data; name="input_reference"; filename="image.png"\r\n'
            b"Content-Type: image/png\r\n\r\n" + reference + b"\r\n--boundary--\r\n"
        )

        scenario, has_reference, size_bytes, sha256_value = fake.classify_create_body(
            body,
            'multipart/form-data; boundary="boundary"',
        )

        self.assertEqual(scenario, "image_to_video")
        self.assertTrue(has_reference)
        self.assertEqual(size_bytes, len(reference))
        self.assertEqual(sha256_value, hashlib.sha256(reference).hexdigest())

    def test_invalid_json_response_is_rejected(self):
        """上游返回非法JSON时必须失败，不能继续读取缺失字段。"""
        with self.assertRaisesRegex(RuntimeError, "invalid_json_response"):
            self.module._decode_json(b"not-json")

    def test_oversized_http_response_is_rejected(self):
        """HTTP响应超过有界读取上限时必须立即失败。"""
        class OversizedResponse:
            status = 200
            headers = {}

            def read(self, _limit):
                return b"x" * (self.module.MAX_HTTP_BYTES + 1)

            def __enter__(self):
                return self

            def __exit__(self, _exc_type, _exc, _traceback):
                return False

        response = OversizedResponse()
        response.module = self.module
        with mock.patch.object(self.module.urllib.request, "urlopen", return_value=response):
            with self.assertRaisesRegex(RuntimeError, "response_too_large"):
                self.module._http_request("http://127.0.0.1:1/test")

    def test_locked_image_preflight_handles_missing_and_bad_digest(self):
        """镜像缺失、摘要非法和摘要不匹配必须形成不同失败原因。"""
        missing = subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="missing")
        invalid = subprocess.CompletedProcess(args=[], returncode=0, stdout="not-json", stderr="")
        mismatch = subprocess.CompletedProcess(args=[], returncode=0, stdout='["other@sha256:1"]', stderr="")

        with mock.patch.object(self.module.subprocess, "run", return_value=missing):
            self.assertEqual(self.module._inspect_locked_image(), (False, "image_missing"))
        with mock.patch.object(self.module.subprocess, "run", return_value=invalid):
            self.assertEqual(self.module._inspect_locked_image(), (False, "image_digest_invalid"))
        with mock.patch.object(self.module.subprocess, "run", return_value=mismatch):
            self.assertEqual(self.module._inspect_locked_image(), (False, "image_digest_mismatch"))

    def test_cleanup_refuses_unowned_container(self):
        """临时清理只能停止带本探针标签的容器。"""
        with mock.patch.object(self.module, "_container_label", return_value="false"):
            with mock.patch.object(self.module.subprocess, "run") as run:
                self.module._stop_owned_container("shared-container")
        run.assert_not_called()

    def test_cli_execute_requires_receipt_path(self):
        """实际探针没有低敏回执路径时必须在启动容器前拒绝。"""
        output = io.StringIO()
        with mock.patch.object(self.module, "_inspect_locked_image", return_value=(True, "ok")):
            with mock.patch("sys.stdout", output):
                code = self.module.main(["--execute"])

        self.assertEqual(code, 2)
        self.assertIn("receipt_required", output.getvalue())

    def test_receipt_writer_uses_lf_and_round_trips_json(self):
        """生成证据必须使用稳定LF行尾并保持JSON可解析。"""
        with tempfile.TemporaryDirectory() as temp_dir:
            path = pathlib.Path(temp_dir, "receipt.json")
            self.module._write_receipt(path, {"status": "ok"})
            raw = path.read_bytes()

        self.assertNotIn(b"\r\n", raw)
        self.assertEqual(json.loads(raw.decode("utf-8")), {"status": "ok"})

    def test_runware_t2v_and_i2v_share_locked_model_and_spec(self):
        """Runware文生与图生必须复用同一AIR模型和5秒720p规格。"""
        task_uuid = "11111111-1111-4111-8111-111111111111"
        t2v = self.module.build_runware_video_request(task_uuid, "test-prompt")
        i2v = self.module.build_runware_video_request(
            task_uuid,
            "test-prompt",
            input_reference="data:image/png;base64,test-only",
        )

        self.assertEqual(t2v[0]["model"], "runway:1@2")
        self.assertEqual(i2v[0]["model"], "runway:1@2")
        self.assertEqual(t2v[0]["duration"], 5)
        self.assertEqual((t2v[0]["width"], t2v[0]["height"]), (1280, 720))
        self.assertNotIn("inputs", t2v[0])
        self.assertEqual(i2v[0]["inputs"]["frameImages"][0]["frame"], "first")

    def test_runware_ack_recovery_reuses_persisted_task_uuid(self):
        """ACK丢失后的详情和轮询请求必须复用create前持久化的taskUUID。"""
        task_uuid = "22222222-2222-4222-8222-222222222222"
        create = self.module.build_runware_video_request(task_uuid, "test-prompt")
        details = self.module.build_runware_recovery_request(task_uuid, "details")
        poll = self.module.build_runware_recovery_request(task_uuid, "poll")

        self.assertEqual(create[0]["taskUUID"], task_uuid)
        self.assertEqual(details[0], {"taskType": "getTaskDetails", "taskUUID": task_uuid})
        self.assertEqual(poll[0], {"taskType": "getResponse", "taskUUID": task_uuid})

    def test_runware_contract_rejects_invalid_task_uuid_and_mode(self):
        """无效UUID或未知恢复模式必须在发出HTTP请求前拒绝。"""
        with self.assertRaisesRegex(ValueError, "runware_task_uuid_invalid"):
            self.module.build_runware_video_request("not-a-uuid", "test-prompt")
        with self.assertRaisesRegex(ValueError, "runware_recovery_mode_invalid"):
            self.module.build_runware_recovery_request("33333333-3333-4333-8333-333333333333", "retry-create")

    def test_runware_prompt_limit_uses_provider_stricter_intersection(self):
        """Runware Gen-4.5允许1000字符，1001字符必须在Provider前拒绝。"""
        task_uuid = "44444444-4444-4444-8444-444444444444"
        accepted = self.module.build_runware_video_request(task_uuid, "x" * 1000)

        self.assertEqual(len(accepted[0]["positivePrompt"]), 1000)
        with self.assertRaisesRegex(ValueError, "runware_prompt_invalid"):
            self.module.build_runware_video_request(task_uuid, "x" * 1001)
        with self.assertRaisesRegex(ValueError, "runware_prompt_invalid"):
            self.module.build_runware_video_request(task_uuid, "")


if __name__ == "__main__":
    unittest.main()

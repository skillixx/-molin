import importlib.util
import io
import json
import pathlib
import unittest
import urllib.error


SCRIPT_PATH = pathlib.Path(__file__).with_name("probe-openrouter-bifrost-model-once.py")


def load_script():
    """从带连字符的脚本文件加载模块，避免测试依赖真实网络。"""
    spec = importlib.util.spec_from_file_location("probe_openrouter_bifrost_model_once", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._body = body

    def read(self, limit):
        return self._body[:limit]

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, traceback):
        return False


class RecordingOpener:
    def __init__(self):
        self.requests = []

    def open(self, request, timeout):
        self.requests.append((request, timeout))
        if request.full_url.endswith("/health"):
            return FakeResponse(200, b'{"status":"ok"}')
        payload = {
            "id": "test-only-id",
            "choices": [{"message": {"content": "OK"}}],
            "usage": {"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
        }
        return FakeResponse(200, json.dumps(payload).encode("utf-8"))


class HealthFailureOpener:
    """健康检查失败时记录调用，并确保不会继续发送真实 Chat。"""

    def __init__(self):
        self.requests = []

    def open(self, request, timeout):
        self.requests.append((request, timeout))
        raise urllib.error.URLError("test-only-health-failure")


class ChatFailureOpener:
    """健康检查通过后让唯一 Chat 传输失败，用于锁定零重试边界。"""

    def __init__(self):
        self.requests = []

    def open(self, request, timeout):
        self.requests.append((request, timeout))
        if request.full_url.endswith("/health"):
            return FakeResponse(200, b'{"status":"ok"}')
        raise urllib.error.URLError("test-only-chat-failure")


class ProbeOpenRouterBifrostModelOnceTest(unittest.TestCase):
    def setUp(self):
        self.module = load_script()

    def test_probe_sends_exactly_one_chat_after_health(self):
        opener = RecordingOpener()
        result = self.module.probe_once("a" * 32, opener=opener)

        self.assertEqual(len(opener.requests), 2)
        health_request = opener.requests[0][0]
        chat_request = opener.requests[1][0]
        self.assertEqual(health_request.get_method(), "GET")
        self.assertEqual(chat_request.get_method(), "POST")
        self.assertEqual(chat_request.full_url, "http://127.0.0.1:18080/v1/chat/completions")

        body = json.loads(chat_request.data.decode("utf-8"))
        self.assertEqual(body["model"], "openrouter/qwen/qwen3.8-max")
        self.assertEqual(body["max_tokens"], 4)
        self.assertEqual(body["temperature"], 0)
        self.assertFalse(body["stream"])
        self.assertEqual(body["messages"], [{"role": "user", "content": "只回复 OK"}])
        self.assertEqual(result["MODEL_AVAILABLE"], "PASS")

    def test_probe_rejects_invalid_token_before_network(self):
        opener = RecordingOpener()
        with self.assertRaisesRegex(ValueError, "internal_token_invalid"):
            self.module.probe_once("short", opener=opener)
        self.assertEqual(opener.requests, [])

    def test_health_failure_stops_before_chat(self):
        opener = HealthFailureOpener()
        result = self.module.probe_once("a" * 32, opener=opener)

        self.assertEqual(len(opener.requests), 1)
        self.assertEqual(result["CHAT_ATTEMPTED"], "NO")
        self.assertEqual(result["ERROR_CLASS"], "bifrost_health_failed")

    def test_chat_transport_failure_is_not_retried(self):
        opener = ChatFailureOpener()
        result = self.module.probe_once("a" * 32, opener=opener)

        self.assertEqual(len(opener.requests), 2)
        self.assertEqual(result["CHAT_ATTEMPTED"], "YES")
        self.assertEqual(result["ERROR_CLASS"], "chat_transport_failed")
        self.assertEqual(result["ZERO_RETRY"], "YES")

    def test_redirect_handler_rejects_redirects(self):
        handler = self.module.NoRedirectHandler()
        redirected = handler.redirect_request(None, None, 302, "test", {}, "https://example.invalid")
        self.assertIsNone(redirected)

    def test_default_main_does_not_execute(self):
        output = io.StringIO()
        code = self.module.main([], {}, output)
        self.assertEqual(code, 2)
        self.assertIn("EXECUTION_AUTHORIZED=NO", output.getvalue())


if __name__ == "__main__":
    unittest.main()

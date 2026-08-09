#!/usr/bin/env python3
"""G8 隔离文字上游，只返回固定低敏响应，不记录请求正文或鉴权头。"""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        # 隔离验收不记录 URL、请求正文或请求头，避免测试材料形成内容留存。
        return

    def do_GET(self):
        if self.path != "/health":
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"status":"ok"}')

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        payload = {
            "id": "fake-g8-completion",
            "object": "chat.completion",
            "created": 1786200000,
            "model": "fake/g8-text",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "G8 隔离验收通过"}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 8, "completion_tokens": 6, "total_tokens": 14},
        }
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()

#!/usr/bin/env python3
"""VID-G0隔离视频上游，只保存低敏计数和合同断言。"""

import argparse
import base64
import hashlib
import json
import threading
import time
from email import policy
from email.parser import BytesParser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


FAKE_VIDEO_BYTES = b"\x00\x00\x00\x18ftypmp42g0-video-contract"
MAX_REQUEST_BYTES = 1024 * 1024


class FakeVideoState:
    """保存探针计数，不保留Prompt、鉴权头、参考图或媒体正文。"""

    def __init__(self):
        self._lock = threading.Lock()
        self._counts = {
            "text_to_video": 0,
            "image_to_video": 0,
            "upstream_500": 0,
            "timeout": 0,
            "ack_drop": 0,
            "retrieve": 0,
            "content": 0,
            "delete": 0,
            "list": 0,
        }
        self._reference_forwarded = False
        self._reference_size_bytes = 0
        self._reference_sha256 = ""
        self._authorization_counts = {name: 0 for name in self._counts if name not in {"retrieve", "content", "delete", "list"}}
        self._jobs = {}

    def record_create(self, scenario, reference_bytes, has_authorization):
        """记录一次提交并返回确定性任务ID。"""
        with self._lock:
            self._counts[scenario] += 1
            if reference_bytes:
                self._reference_forwarded = True
                self._reference_size_bytes = len(reference_bytes)
                self._reference_sha256 = hashlib.sha256(reference_bytes).hexdigest()
            if has_authorization:
                self._authorization_counts[scenario] += 1
            job_id = f"video_g0_{scenario}_{self._counts[scenario]:03d}"
            if scenario in {"text_to_video", "image_to_video"}:
                self._jobs[job_id] = scenario
            return job_id

    def record_operation(self, operation):
        """记录查询、下载、删除和列表调用次数。"""
        with self._lock:
            self._counts[operation] += 1

    def has_job(self, job_id):
        """判断任务是否由本轮Fake提交创建。"""
        with self._lock:
            return job_id in self._jobs

    def snapshot(self):
        """返回可写入低敏回执的计数快照。"""
        with self._lock:
            return {
                "counts": dict(self._counts),
                "authorization_counts": dict(self._authorization_counts),
                "input_reference_forwarded": self._reference_forwarded,
                "input_reference_size_bytes": self._reference_size_bytes,
                "input_reference_sha256": self._reference_sha256,
                "stored_job_count": len(self._jobs),
            }


STATE = FakeVideoState()


def _json_bytes(payload):
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _video_payload(job_id, status):
    return {
        "id": job_id,
        "object": "video",
        "model": "sora-2",
        "status": status,
        "progress": 100 if status == "completed" else 0,
        "prompt": "g0-contract-redacted",
        "seconds": "4",
        "size": "1280x720",
        "created_at": 1787824800,
        "completed_at": 1787824801 if status == "completed" else None,
        "expires_at": 1787911200,
    }


def _extract_reference_bytes(body, content_type):
    """从JSON或multipart中提取参考图字节，只用于计算低敏指纹。"""
    if content_type.lower().startswith("multipart/form-data"):
        envelope = (
            f"Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n".encode("utf-8") + body
        )
        message = BytesParser(policy=policy.default).parsebytes(envelope)
        if message.is_multipart():
            for part in message.iter_parts():
                name = part.get_param("name", header="content-disposition")
                if name == "input_reference":
                    return part.get_payload(decode=True) or b""
    try:
        payload = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return b""
    reference = payload.get("input_reference") if isinstance(payload, dict) else None
    if isinstance(reference, str) and reference.startswith("data:image/png;base64,"):
        try:
            return base64.b64decode(reference.split(",", 1)[1], validate=True)
        except (ValueError, base64.binascii.Error):
            return b""
    return b""


def classify_create_body(body, content_type=""):
    """仅提取故障标签和参考图存在性，不保存请求正文。"""
    lowered = body.lower()
    reference_bytes = _extract_reference_bytes(body, content_type)
    has_reference = bool(reference_bytes)
    if b"g0-upstream-500" in lowered:
        return "upstream_500", has_reference, len(reference_bytes), hashlib.sha256(reference_bytes).hexdigest() if reference_bytes else ""
    if b"g0-upstream-timeout" in lowered:
        return "timeout", has_reference, len(reference_bytes), hashlib.sha256(reference_bytes).hexdigest() if reference_bytes else ""
    if b"g0-ack-drop" in lowered:
        return "ack_drop", has_reference, len(reference_bytes), hashlib.sha256(reference_bytes).hexdigest() if reference_bytes else ""
    if has_reference:
        return "image_to_video", True, len(reference_bytes), hashlib.sha256(reference_bytes).hexdigest()
    return "text_to_video", False, 0, ""


class Handler(BaseHTTPRequestHandler):
    """实现OpenAI视频上游最小合同和故障注入。"""

    server_version = "MolinVideoG0Fake/1"

    def log_message(self, _format, *_args):
        # 探针禁止把路径、请求正文或鉴权信息写入普通日志。
        return

    def _send_json(self, status, payload):
        body = _json_bytes(payload)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_bounded_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length < 0 or length > MAX_REQUEST_BYTES:
            raise ValueError("request_too_large")
        return self.rfile.read(length)

    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path.rstrip("/") or "/"
        if path == "/health":
            self._send_json(200, {"status": "ok"})
            return
        if path == "/count":
            self._send_json(200, STATE.snapshot())
            return
        if path == "/v1/videos":
            STATE.record_operation("list")
            self._send_json(200, {"object": "list", "data": [], "has_more": False})
            return
        if path.startswith("/v1/videos/") and path.endswith("/content"):
            job_id = path[len("/v1/videos/") : -len("/content")]
            if not STATE.has_job(job_id):
                self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})
                return
            STATE.record_operation("content")
            self.send_response(200)
            self.send_header("Content-Type", "video/mp4")
            self.send_header("Content-Length", str(len(FAKE_VIDEO_BYTES)))
            self.end_headers()
            self.wfile.write(FAKE_VIDEO_BYTES)
            return
        if path.startswith("/v1/videos/"):
            job_id = path[len("/v1/videos/") :]
            if not STATE.has_job(job_id):
                self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})
                return
            STATE.record_operation("retrieve")
            self._send_json(200, _video_payload(job_id, "completed"))
            return
        self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path.rstrip("/") != "/v1/videos":
            self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})
            return
        try:
            body = self._read_bounded_body()
        except ValueError:
            self._send_json(413, {"error": {"code": "request_too_large", "message": "too large"}})
            return

        scenario, has_reference, _, _ = classify_create_body(body, self.headers.get("Content-Type", ""))
        reference_bytes = _extract_reference_bytes(body, self.headers.get("Content-Type", ""))

        job_id = STATE.record_create(
            scenario,
            reference_bytes=reference_bytes if has_reference else b"",
            has_authorization=bool(self.headers.get("Authorization")),
        )
        if scenario == "upstream_500":
            self._send_json(500, {"error": {"code": "g0_fake_500", "message": "fake failure"}})
            return
        if scenario == "timeout":
            time.sleep(3)
            try:
                self._send_json(504, {"error": {"code": "g0_fake_timeout", "message": "fake timeout"}})
            except (BrokenPipeError, ConnectionResetError):
                return
            return
        if scenario == "ack_drop":
            self.close_connection = True
            return
        self._send_json(200, _video_payload(job_id, "queued"))

    def do_DELETE(self):
        parsed = urlparse(self.path)
        path = parsed.path.rstrip("/")
        if not path.startswith("/v1/videos/"):
            self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})
            return
        job_id = path[len("/v1/videos/") :]
        if not STATE.has_job(job_id):
            self._send_json(404, {"error": {"code": "not_found", "message": "not found"}})
            return
        STATE.record_operation("delete")
        self._send_json(200, {"id": job_id, "object": "video.deleted", "deleted": True})


def main():
    parser = argparse.ArgumentParser(description="启动VID-G0隔离视频Fake上游")
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()

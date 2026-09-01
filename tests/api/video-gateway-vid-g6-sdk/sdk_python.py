"""锁定Python SDK的真实HTTP合同；默认只检查依赖，显式授权后才发送请求。"""
import argparse
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit


JOB_FIELDS = {"id", "completed_at", "created_at", "error", "expires_at", "model", "object",
              "progress", "prompt", "remixed_from_video_id", "seconds", "size", "status"}


def require(condition, code):
    if not condition:
        raise AssertionError(code)


def validate_job(job, expected_model=None):
    # 用原始JSON断言必需的null字段，避免SDK模型默认值掩盖服务器漏字段。
    require(set(job) == JOB_FIELDS, "JOB_FIELDS")
    require(re.fullmatch(r"video_[A-Za-z0-9_-]{8,64}", job["id"]) is not None, "PUBLIC_ID")
    require(job["object"] == "video" and job["seconds"] == "5" and job["size"] == "1280x720", "JOB_SPEC")
    require(isinstance(job["model"], str) and 0 < len(job["model"]) <= 128 and
            (expected_model is None or job["model"] == expected_model), "JOB_MODEL")
    require(job["prompt"] is None and job["remixed_from_video_id"] is None, "JOB_NULLS")
    require(job["status"] in {"queued", "in_progress", "completed", "failed"}, "JOB_STATUS")
    require(type(job["created_at"]) is int and type(job["progress"]) in {int, float}, "JOB_TYPES")
    require(0 <= job["progress"] <= 100, "JOB_PROGRESS")
    for key in ("completed_at", "expires_at"):
        require(job[key] is None or type(job[key]) is int, "JOB_TIME")
    if job["error"] is not None:
        require(set(job["error"]) == {"code", "message"}, "JOB_ERROR")
        require(isinstance(job["error"]["code"], str) and 0 < len(job["error"]["code"]) <= 64
                and isinstance(job["error"]["message"], str) and 0 < len(job["error"]["message"]) <= 512, "JOB_ERROR_TYPES")


def validate_page(page, expected_model):
    require(set(page) == {"object", "data", "first_id", "last_id", "has_more"} and page["object"] == "list", "LIST_SHAPE")
    require(type(page["has_more"]) is bool and isinstance(page["data"], list) and len(page["data"]) <= 100, "LIST_TYPES")
    for value in page["data"]:
        validate_job(value, expected_model)
    require(page["first_id"] == (page["data"][0]["id"] if page["data"] else None)
            and page["last_id"] == (page["data"][-1]["id"] if page["data"] else None), "LIST_CURSOR")


def load_fixture(path):
    fixture = json.loads(Path(path).read_text(encoding="utf-8"))
    origin = urlsplit(fixture["origin"])
    require(origin.scheme == "http" and origin.hostname in {"127.0.0.1", "::1"}
            and origin.port is not None and origin.username is None and origin.password is None
            and origin.path == "" and not origin.query and not origin.fragment, "LOOPBACK_ORIGIN_REQUIRED")
    require(fixture["purpose"] == "isolated_synthetic_fixture" and fixture["disposable"] is True, "FIXTURE_REQUIRED")
    require(re.fullmatch(r"[a-z0-9_-]{8,40}", fixture["run_id"]) is not None, "RUN_ID")
    require(re.fullmatch(r"[A-Za-z0-9._/-]{1,128}", fixture["model"]) is not None, "MODEL")
    case = fixture["python"]
    require(re.fullmatch(r"video_[A-Za-z0-9_-]{8,64}", case["completed_video_id"]) is not None, "FIXTURE_VIDEO")
    require(re.fullmatch(r"[A-Za-z0-9_-]{8,128}", case["request_id"]) is not None, "FIXTURE_REQUEST")
    require(type(case["media_size_bytes"]) is int and 16 <= case["media_size_bytes"] <= 8 << 20, "SMALL_MEDIA_REQUIRED")
    require(re.fullmatch(r"[0-9a-f]{64}", case["media_sha256"]) is not None, "MEDIA_HASH")
    require({"request_id", "quote_id", "billing_status", "settled_amount"} <= set(case["billing_facts"]), "BILLING_FACTS_REQUIRED")
    require(case["billing_facts"]["request_id"] == case["request_id"] and case["billing_facts"]["billing_status"] == "settled", "BILLING_FIXTURE")
    image = (Path(path).resolve().parent / fixture["reference_image"]).resolve()
    require(image.is_relative_to(Path(path).resolve().parent) and image.is_file()
            and image.suffix.lower() in {".png", ".jpg", ".jpeg"} and 0 < image.stat().st_size <= 10 << 20, "LOCAL_REFERENCE_REQUIRED")
    return fixture, case, image


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true")
    parser.add_argument("--fixture")
    args = parser.parse_args()
    lock = Path(__file__).with_name("requirements.lock.txt").read_text(encoding="utf-8")
    for line in lock.splitlines():
        if line and not line.startswith("#"):
            package, version = line.split("==", 1)
            require(importlib.metadata.version(package) == version, "SDK_VERSION_MISMATCH")
    os.environ.pop("OPENAI_LOG", None)
    import httpx
    from openai import APIStatusError, OpenAI
    if not args.execute:
        print(json.dumps({"dependency_check": "PASS", "http_contract": "NOT_RUN", "sdk": "openai==2.45.0"}))
        return
    require(os.environ.get("VID_G6_SDK_APPROVED") == "ISOLATED_SYNTHETIC_ONLY" and args.fixture, "EXECUTION_AUTH_REQUIRED")
    fixture, case, image = load_fixture(args.fixture)
    key = os.environ.get("VID_G6_SDK_SK", "")
    require(re.fullmatch(r"sk-molin-g6-fixture-[A-Za-z0-9_-]{16,128}", key) is not None, "SYNTHETIC_SK_REQUIRED")
    origin = fixture["origin"]
    origin_url = httpx.URL(origin)

    class LoopbackTransport(httpx.BaseTransport):
        # 每次真实发送前验证目标，禁止代理、重定向和SDK默认OpenAI地址。
        def __init__(self):
            self.inner = httpx.HTTPTransport(retries=0, trust_env=False)

        def handle_request(self, request):
            require((request.url.scheme, request.url.host, request.url.port) ==
                    (origin_url.scheme, origin_url.host, origin_url.port), "OUTBOUND_BLOCKED")
            require(request.url.path.startswith(("/v1/videos", "/api/token/videos/requests/")), "PATH_BLOCKED")
            return self.inner.handle_request(request)

        def close(self):
            self.inner.close()

    report = {"sdk": "openai==2.45.0", "http_contract": "FAIL", "cases": []}
    current = "preflight"
    try:
        with httpx.Client(transport=LoopbackTransport(), trust_env=False, follow_redirects=False, timeout=15) as http:
            client = OpenAI(api_key=key, base_url=origin + "/v1", http_client=http, max_retries=0)

            def headers(suffix):
                return {"Idempotency-Key": "vid-g6-py-" + fixture["run_id"] + "-" + suffix}

            def passed(name):
                report["cases"].append({"case": name, "status": "PASS"})

            def raw_job(response):
                require(response.status_code == 200, "HTTP_200_REQUIRED")
                require(bool(response.headers.get("x-request-id")), "HTTP_TRACE_REQUIRED")
                value = json.loads(response.text)
                validate_job(value, fixture["model"])
                # parse同样必须成功，真正覆盖SDK模型反序列化，而不是仅发原始HTTP。
                require(response.parse().id == value["id"], "SDK_PARSE")
                return value

            current = "create_t2v_and_replay"
            params = dict(model=fixture["model"], prompt="合成SDK文生视频测试", seconds="5", size="1280x720")
            try:
                client.videos.create(**params)
                raise AssertionError("IDEMPOTENCY_MUST_BE_REQUIRED")
            except APIStatusError as error:
                require(error.status_code == 400, "MISSING_IDEMPOTENCY_400")
            created = client.videos.with_raw_response.create(**params, extra_headers=headers("t2v"))
            job = raw_job(created)
            business_id = created.headers.get("x-molin-request-id")
            require(bool(business_id) and business_id != created.headers.get("x-request-id"), "BUSINESS_TRACE")
            replay = client.videos.with_raw_response.create(**params, extra_headers=headers("t2v"))
            require(raw_job(replay)["id"] == job["id"] and replay.headers.get("x-molin-request-id") == business_id, "REPLAY_IDENTITY")
            try:
                client.videos.create(**dict(params, prompt="另一个合成生成意图"), extra_headers=headers("t2v"))
                raise AssertionError("CONFLICT_MUST_FAIL")
            except APIStatusError as error:
                require(error.status_code == 409, "IDEMPOTENCY_409")
            passed(current)

            current = "create_i2v_multipart"
            with image.open("rb") as stream:
                mime = "image/png" if image.suffix.lower() == ".png" else "image/jpeg"
                value = client.videos.with_raw_response.create(model=fixture["model"], prompt="合成SDK图生视频测试",
                    seconds="5", size="1280x720", input_reference=(image.name, stream, mime), extra_headers=headers("i2v"))
                first_i2v = raw_job(value)
                i2v_business = value.headers.get("x-molin-request-id", "")
                require(re.fullmatch(r"[A-Za-z0-9._:-]{8,128}", i2v_business) is not None
                        and i2v_business != value.headers.get("x-request-id"), "I2V_BUSINESS_TRACE")
                stream.seek(0)
                repeated = client.videos.with_raw_response.create(model=fixture["model"], prompt="合成SDK图生视频测试",
                    seconds="5", size="1280x720", input_reference=(image.name, stream, mime), extra_headers=headers("i2v"))
                require(raw_job(repeated)["id"] == first_i2v["id"] and repeated.headers.get("x-molin-request-id") == value.headers.get("x-molin-request-id"), "I2V_REPLAY")
                require(repeated.headers.get("x-molin-request-id") != repeated.headers.get("x-request-id"), "I2V_REPLAY_TRACE")
            passed(current)
            current = "retrieve_and_list"
            require(raw_job(client.videos.with_raw_response.retrieve(job["id"]))["id"] == job["id"], "RETRIEVE_ID")
            listing = client.videos.with_raw_response.list(limit=100, order="desc")
            require(listing.status_code == 200, "LIST_STATUS")
            page = json.loads(listing.text)
            validate_page(page, fixture["model"])
            require(any(value["id"] == job["id"] for value in page["data"]), "LIST_CREATED")
            require(page["has_more"] is False and any(value["id"] == case["completed_video_id"] for value in page["data"]), "SINGLE_PAGE_DELETE_FIXTURE_REQUIRED")
            listing.parse()
            passed(current)

            current = "completed_content_and_ranges"
            completed = raw_job(client.videos.with_raw_response.retrieve(case["completed_video_id"]))
            require(completed["status"] == "completed", "COMPLETED_FIXTURE_REQUIRED")
            etag = '"' + case["media_sha256"] + '"'
            for range_header, validator, expected_status, expected_size in (
                (None, None, 200, case["media_size_bytes"]),
                ("bytes=0-15", etag, 206, 16),
                ("bytes=0-15", '"old-fixture"', 200, case["media_size_bytes"]),
            ):
                extra = {}
                if range_header:
                    extra.update({"Range": range_header, "If-Range": validator})
                with client.videos.with_streaming_response.download_content(case["completed_video_id"], extra_headers=extra) as response:
                    require(response.status_code == expected_status and response.headers.get("etag") == etag, "CONTENT_STATUS_ETAG")
                    require(response.headers.get("accept-ranges") == "bytes" and response.headers.get("content-type") == "video/mp4", "CONTENT_HEADERS")
                    require(int(response.headers.get("content-length", "-1")) == expected_size, "CONTENT_LENGTH")
                    if expected_status == 206:
                        require(response.headers.get("content-range") == f'bytes 0-15/{case["media_size_bytes"]}', "CONTENT_RANGE")
                    digest, size, prefix = hashlib.sha256(), 0, b""
                    for chunk in response.iter_bytes():
                        size += len(chunk)
                        require(size <= 8 << 20, "MEDIA_BOUND")
                        prefix = (prefix + chunk)[:16]
                        digest.update(chunk)
                    require(size == expected_size and prefix[4:8] == b"ftyp", "MP4_BYTES")
                    if expected_status == 200:
                        require(digest.hexdigest() == case["media_sha256"], "MEDIA_HASH")
            for invalid in ("bytes=-", "bytes=0-1,4-5", f'bytes={case["media_size_bytes"]}-'):
                try:
                    with client.videos.with_streaming_response.download_content(case["completed_video_id"], extra_headers={"Range": invalid}):
                        raise AssertionError("RANGE_MUST_FAIL")
                except APIStatusError as error:
                    require(error.status_code == 416 and error.response.headers.get("content-range") == f'bytes */{case["media_size_bytes"]}', "RANGE_416")
            passed(current)

            current = "delete_and_retained_billing"
            def billing(deleted):
                for suffix in (case["request_id"], "by-video/" + case["completed_video_id"]):
                    response = http.get(origin + "/api/token/videos/requests/" + suffix, headers={"Authorization": "Bearer " + key})
                    require(response.status_code == 200, "BILLING_STATUS")
                    envelope = response.json()
                    require(envelope.get("code") == 0, "BILLING_ENVELOPE")
                    data = envelope["data"]
                    for field, expected in case["billing_facts"].items():
                        require(data.get(field) == expected, "BILLING_FACT_RETAINED")
                    require(data.get("media_deleted") is deleted, "BILLING_MEDIA_MARKER")
            billing(False)
            removed = client.videos.with_raw_response.delete(case["completed_video_id"], extra_headers=headers("delete"))
            require(removed.status_code == 200 and json.loads(removed.text) == {"id": case["completed_video_id"], "object": "video.deleted", "deleted": True}, "DELETE_SHAPE")
            removed.parse()
            for operation in (lambda: client.videos.retrieve(case["completed_video_id"]),
                              lambda: client.videos.download_content(case["completed_video_id"])):
                try:
                    operation()
                    raise AssertionError("DELETED_MUST_BE_404")
                except APIStatusError as error:
                    require(error.status_code == 404, "DELETED_404")
            response = client.videos.with_raw_response.list(limit=100, order="desc")
            require(response.status_code == 200, "AFTER_LIST_STATUS")
            page = json.loads(response.text)
            validate_page(page, fixture["model"])
            require(page["has_more"] is False and all(value["id"] != case["completed_video_id"] for value in page["data"]), "DELETED_LIST_HIDDEN")
            response.parse()
            billing(True)
            passed(current)
            report["http_contract"] = "PASS"
    except Exception as error:
        # 不输出异常正文、HTTP正文、凭据、Prompt或对象位置。
        report["cases"].append({"case": current, "status": "FAIL", "error_class": type(error).__name__,
                                "http_status": getattr(error, "status_code", None)})
    print(json.dumps(report, ensure_ascii=False))
    require(report["http_contract"] == "PASS", "CONTRACT_FAILED")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps({"status": "NOT_RUN_OR_FAILED", "error_class": type(error).__name__}))
        sys.exit(1)

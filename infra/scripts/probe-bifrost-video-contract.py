#!/usr/bin/env python3
"""执行VID-G0-B零费用Bifrost视频合同预探针。"""

import argparse
import base64
import copy
import datetime
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


BIFROST_IMAGE_TAG = "maximhq/bifrost:v2.0.0"
BIFROST_IMAGE_DIGEST = (
    "maximhq/bifrost@sha256:cf71be9fad4e0749b6e26cbb774c687413dad9a0970b83f4e1dadb6f503ea208"
)
FAKE_SCRIPT = pathlib.Path(__file__).with_name("video-g0-fake-upstream.py")
PROBE_LABEL = "molin.video-g0-contract"
FAKE_KEY = "g0-not-a-secret"
FAKE_ENCRYPTION_KEY = "g0-local-contract-encryption-key-32"
MAX_HTTP_BYTES = 2 * 1024 * 1024
SOURCE_STATE_EXCLUDES = {
    "docs/evidence/video-gateway-vid-g0-bifrost-contract.json",
    "docs/evidence/video-gateway-vid-g0-source-state.json",
}
PNG_1X1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nH0AAAAASUVORK5CYII="
REFERENCE_PNG_BYTES = base64.b64decode(PNG_1X1, validate=True)
REFERENCE_PNG_SHA256 = hashlib.sha256(REFERENCE_PNG_BYTES).hexdigest()
RUNWARE_MODEL_ID = "runway:1@2"
RUNWARE_DURATION_SECONDS = 5
RUNWARE_WIDTH = 1280
RUNWARE_HEIGHT = 720
RUNWARE_PROMPT_MAX_CHARS = 1000


def json_dumps(value):
    """使用稳定JSON格式生成低敏证据。"""
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def redact_remote_url(remote_url):
    """只保留远程主机和仓库名，移除用户、所有者和凭据。"""
    value = remote_url.strip()
    if value.startswith("git@") and ":" in value:
        host, path = value[4:].split(":", 1)
        parts = [part for part in path.split("/") if part]
        repository = parts[-1] if parts else "<repo>"
        return f"{host}/<owner>/{repository}"
    if "://" in value:
        parsed = urllib.parse.urlparse(value)
        parts = [part for part in parsed.path.split("/") if part]
        repository = parts[-1] if parts else "<repo>"
        return f"{parsed.hostname or '<host>'}/<owner>/{repository}"
    return "<redacted-remote>"


def _run_git(repo_root, *args, text=True):
    """运行只读Git命令并在失败时终止证据生成。"""
    result = subprocess.run(
        ["git", *args],
        cwd=repo_root,
        capture_output=True,
        text=text,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(f"git_{args[0]}_failed")
    return result.stdout


def capture_source_state(repo_root, origin_provenance):
    """生成排除递归证据文件的可复算源码快照。"""
    root = pathlib.Path(repo_root).resolve()
    head = _run_git(root, "rev-parse", "HEAD").strip()
    origin_main = _run_git(root, "rev-parse", "origin/main").strip()
    remote_url = _run_git(root, "remote", "get-url", "origin").strip()
    patch_bytes = _run_git(root, "diff", "--binary", "HEAD", "--", ".", text=False)
    patch_sha = hashlib.sha256(patch_bytes).hexdigest() if patch_bytes else "EMPTY"
    untracked_output = _run_git(root, "ls-files", "--others", "--exclude-standard")
    manifest = []
    for relative in sorted(line.strip().replace("\\", "/") for line in untracked_output.splitlines() if line.strip()):
        if relative in SOURCE_STATE_EXCLUDES:
            continue
        file_path = root / pathlib.PurePosixPath(relative)
        manifest.append({"path": relative, "sha256": hashlib.sha256(file_path.read_bytes()).hexdigest()})
    manifest_text = "\n".join(f"{item['path']}|{item['sha256']}" for item in manifest)
    manifest_sha = hashlib.sha256(manifest_text.encode("utf-8")).hexdigest() if manifest_text else "EMPTY"
    canonical = "\n".join(
        [
            f"HEAD_COMMIT={head}",
            f"BASE_COMMIT={origin_main}",
            f"ORIGIN_MAIN_COMMIT={origin_main}",
            f"ORIGIN_MAIN_PROVENANCE={origin_provenance}",
            f"TRACKED_PATCH_SHA256={patch_sha}",
            f"UNTRACKED_MANIFEST_SHA256={manifest_sha}",
        ]
    )
    return {
        "schema_version": 1,
        "captured_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "head_commit": head,
        "base_commit": origin_main,
        "origin_main_commit": origin_main,
        "origin_main_remote_url": redact_remote_url(remote_url),
        "origin_main_provenance": origin_provenance,
        "tracked_patch_sha256": patch_sha,
        "untracked_manifest_sha256": manifest_sha,
        "source_state_id": hashlib.sha256(canonical.encode("utf-8")).hexdigest(),
        "excluded_generated_evidence": sorted(SOURCE_STATE_EXCLUDES),
        "untracked_manifest": manifest,
    }


def build_bifrost_config(fake_base_url):
    """生成只允许OpenAI视频Fake模型的隔离配置。"""
    return {
        "$schema": "https://www.getbifrost.ai/schema",
        "encryption_key": "env.BIFROST_ENCRYPTION_KEY",
        "client": {
            "drop_excess_requests": True,
            "enable_logging": False,
        },
        "providers": {
            "openai": {
                "keys": [
                    {
                        "name": "video-g0-fake",
                        "value": "env.OPENAI_API_KEY",
                        "models": ["sora-2"],
                        "weight": 1,
                    }
                ],
                "network_config": {
                    "base_url": fake_base_url,
                    "default_request_timeout_in_seconds": 2,
                    "max_retries": 0,
                    "allow_private_network": True,
                },
            }
        },
        "config_store": {"enabled": False},
    }


def _validate_runware_task_uuid(task_uuid):
    """校验Runware客户端任务ID必须是规范UUIDv4。"""
    try:
        parsed = uuid.UUID(task_uuid)
    except (ValueError, AttributeError, TypeError) as error:
        raise ValueError("runware_task_uuid_invalid") from error
    if parsed.version != 4 or str(parsed) != task_uuid.lower():
        raise ValueError("runware_task_uuid_invalid")


def build_runware_video_request(task_uuid, prompt, input_reference=None):
    """构造Runware视频创建合同，T2V与I2V复用同一模型和taskUUID。"""
    _validate_runware_task_uuid(task_uuid)
    if not isinstance(prompt, str) or not prompt.strip() or len(prompt) > RUNWARE_PROMPT_MAX_CHARS:
        raise ValueError("runware_prompt_invalid")
    request = {
        "taskType": "videoInference",
        "taskUUID": task_uuid,
        "model": RUNWARE_MODEL_ID,
        "positivePrompt": prompt,
        "width": RUNWARE_WIDTH,
        "height": RUNWARE_HEIGHT,
        "duration": RUNWARE_DURATION_SECONDS,
        "numberResults": 1,
        "deliveryMethod": "async",
        "outputType": "URL",
        "ttl": 3600,
        "includeCost": True,
    }
    if input_reference is not None:
        request["inputs"] = {
            "frameImages": [
                {
                    "image": input_reference,
                    "frame": "first",
                }
            ]
        }
    return [request]


def build_runware_recovery_request(task_uuid, mode):
    """构造ACK丢失恢复请求，禁止通过新UUID重新create。"""
    _validate_runware_task_uuid(task_uuid)
    task_types = {"details": "getTaskDetails", "poll": "getResponse"}
    if mode not in task_types:
        raise ValueError("runware_recovery_mode_invalid")
    return [{"taskType": task_types[mode], "taskUUID": task_uuid}]


def example_contract_result():
    """返回单元测试使用的完整通过样例。"""
    operation = {
        "create_http": 200,
        "retrieve_http": 200,
        "retrieve_status": "completed",
        "content_http": 200,
        "content_type": "video/mp4",
        "content_signature": "mp4-ftyp",
        "delete_http": 200,
        "deleted": True,
        "compound_id_used": True,
    }
    return {
        "bifrost_image_digest": BIFROST_IMAGE_DIGEST,
        "bifrost_health_http": 200,
        "list_http": 200,
        "text_to_video": copy.deepcopy(operation),
        "image_to_video": {
            **copy.deepcopy(operation),
            "input_reference_forwarded": True,
            "input_reference_size_bytes": len(REFERENCE_PNG_BYTES),
            "input_reference_sha256": REFERENCE_PNG_SHA256,
            "input_reference_size_match": True,
            "input_reference_sha256_match": True,
        },
        "failure_http": {"upstream_500": 500, "upstream_timeout": 504, "ack_drop": 502},
        "fake_counts": {
            "text_to_video": 1,
            "image_to_video": 1,
            "upstream_500": 1,
            "timeout": 1,
            "ack_drop": 1,
            "retrieve": 2,
            "content": 2,
            "delete": 2,
            "list": 1,
        },
        "authorization_counts": {
            "text_to_video": 1,
            "image_to_video": 1,
            "upstream_500": 1,
            "timeout": 1,
            "ack_drop": 1,
        },
        "authorization_forwarded": True,
        "real_provider_requests": 0,
        "provider_cost": "CNY 0",
    }


def contract_checks(result):
    """生成稳定断言表，输出数量必须来自真实检查项。"""
    checks = {
        "image_digest": result.get("bifrost_image_digest") == BIFROST_IMAGE_DIGEST,
        "bifrost_health": result.get("bifrost_health_http") == 200,
        "list_contract": result.get("list_http") == 200,
    }
    for operation_name in ("text_to_video", "image_to_video"):
        operation = result.get(operation_name, {})
        expected = {
            "create_http": 200,
            "retrieve_http": 200,
            "retrieve_status": "completed",
            "content_http": 200,
            "content_type": "video/mp4",
            "content_signature": "mp4-ftyp",
            "delete_http": 200,
            "deleted": True,
            "compound_id_used": True,
        }
        for key, expected_value in expected.items():
            checks[f"{operation_name}_{key}"] = operation.get(key) == expected_value
    checks["image_reference_forwarding"] = result.get("image_to_video", {}).get("input_reference_forwarded") is True
    checks["image_reference_size"] = result.get("image_to_video", {}).get("input_reference_size_match") is True
    checks["image_reference_sha256"] = result.get("image_to_video", {}).get("input_reference_sha256_match") is True
    fake_counts = result.get("fake_counts", {})
    for scenario in ("text_to_video", "image_to_video", "upstream_500", "timeout", "ack_drop"):
        checks[f"{scenario}_submit_count"] = fake_counts.get(scenario) == 1
    for operation_name, expected_count in (("retrieve", 2), ("content", 2), ("delete", 2), ("list", 1)):
        checks[f"{operation_name}_count"] = fake_counts.get(operation_name) == expected_count
    expected_failure_http = {"upstream_500": 500, "upstream_timeout": 504, "ack_drop": 502}
    failure_http = result.get("failure_http", {})
    for scenario, expected_status in expected_failure_http.items():
        checks[f"{scenario}_http"] = failure_http.get(scenario) == expected_status
    authorization_counts = result.get("authorization_counts", {})
    for scenario in ("text_to_video", "image_to_video", "upstream_500", "timeout", "ack_drop"):
        checks[f"{scenario}_authorization"] = (
            fake_counts.get(scenario, 0) > 0
            and authorization_counts.get(scenario) == fake_counts.get(scenario)
        )
    checks["real_provider_requests"] = result.get("real_provider_requests") == 0
    checks["provider_cost"] = result.get("provider_cost") == "CNY 0"
    return checks


def validate_contract_result(result):
    """验证合同闭环和Fake计数，任何隐藏重试都必须失败。"""
    return sorted(check_id for check_id, passed in contract_checks(result).items() if not passed)


def classify_driver_result(result):
    """把合同结果映射为可执行驱动裁决，不允许把失败伪装成通过。"""
    errors = validate_contract_result(result)
    if not errors:
        return {
            "contract_status": "pass",
            "driver": "bifrost_candidate",
            "bifrost_video_enabled": True,
        }
    if errors == ["ack_drop_submit_count"] and result.get("fake_counts", {}).get("ack_drop", 0) > 1:
        return {
            "contract_status": "blocked_ack_drop_hidden_retry",
            "driver": "native_async",
            "bifrost_video_enabled": False,
        }
    return {
        "contract_status": "failed_unclassified",
        "driver": "undetermined",
        "bifrost_video_enabled": False,
    }


def build_receipt(result, source_state):
    """构造不包含请求正文、媒体正文、URL或鉴权信息的仓库回执。"""
    decision = classify_driver_result(result)
    checks = contract_checks(result)
    return {
        "schema_version": 1,
        "gate": "VID-G0-B",
        "captured_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "tested_source_state": source_state,
        "bifrost_image_tag": BIFROST_IMAGE_TAG,
        "bifrost_image_digest": result["bifrost_image_digest"],
        "bifrost_health_http": result["bifrost_health_http"],
        "list_http": result["list_http"],
        "text_to_video": dict(result["text_to_video"]),
        "image_to_video": dict(result["image_to_video"]),
        "failure_http": dict(result["failure_http"]),
        "fake_counts": dict(result["fake_counts"]),
        "authorization_counts": dict(result["authorization_counts"]),
        "authorization_forwarded": result["authorization_forwarded"],
        "real_provider_requests": result["real_provider_requests"],
        "provider_cost": result["provider_cost"],
        "contract_status": decision["contract_status"],
        "driver_decision": decision["driver"],
        "bifrost_video_enabled": decision["bifrost_video_enabled"],
        "assertion_total": len(checks),
        "assertion_passed": sum(1 for passed in checks.values() if passed),
        "contract_errors": validate_contract_result(result),
    }


def _find_free_port(excluded=None):
    """选择仅用于本轮本机探针的空闲端口。"""
    excluded = excluded or set()
    while True:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        if port not in excluded:
            return port


def _http_request(url, method="GET", payload=None, timeout=10):
    """执行有界HTTP请求并返回状态、响应头和字节。"""
    headers = {}
    data = None
    if payload is not None:
        data = json_dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read(MAX_HTTP_BYTES + 1)
            if len(body) > MAX_HTTP_BYTES:
                raise RuntimeError("response_too_large")
            return response.status, dict(response.headers.items()), body
    except urllib.error.HTTPError as error:
        body = error.read(MAX_HTTP_BYTES + 1)
        return error.code, dict(error.headers.items()), body
    except (urllib.error.URLError, TimeoutError, socket.timeout, ConnectionError):
        return 0, {}, b""


def _wait_for_http(url, timeout_seconds):
    """等待本地服务就绪，不向任何真实Provider发请求。"""
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        status, _, _ = _http_request(url, timeout=1)
        if status == 200:
            return True
        time.sleep(0.25)
    return False


def _decode_json(body):
    """解析探针JSON响应，非法响应直接失败。"""
    try:
        value = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise RuntimeError("invalid_json_response") from error
    if not isinstance(value, dict):
        raise RuntimeError("invalid_json_object")
    return value


def _inspect_locked_image():
    """确认本机镜像标签和摘要与冻结值完全一致。"""
    command = ["docker", "image", "inspect", BIFROST_IMAGE_TAG, "--format", "{{json .RepoDigests}}"]
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        return False, "image_missing"
    try:
        digests = json.loads(result.stdout.strip())
    except json.JSONDecodeError:
        return False, "image_digest_invalid"
    if BIFROST_IMAGE_DIGEST not in digests:
        return False, "image_digest_mismatch"
    return True, "ok"


def _container_label(container_name):
    """读取临时容器标签，清理前用于确认归属。"""
    command = ["docker", "inspect", container_name, "--format", f'{{{{index .Config.Labels "{PROBE_LABEL}"}}}}']
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        return ""
    return result.stdout.strip()


def _stop_owned_container(container_name):
    """只停止带本探针标签的临时容器，禁止误伤其他任务。"""
    if not container_name or _container_label(container_name) != "true":
        return
    subprocess.run(["docker", "stop", container_name], capture_output=True, text=True, check=False)


def _start_fake_server(port):
    """启动本机Fake上游并确认健康。"""
    process = subprocess.Popen(
        [sys.executable, str(FAKE_SCRIPT), "--port", str(port)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    if not _wait_for_http(f"http://127.0.0.1:{port}/health", 10):
        process.terminate()
        process.wait(timeout=5)
        raise RuntimeError("fake_upstream_unhealthy")
    return process


def _start_bifrost_container(app_dir, host_port, fake_port, container_name):
    """使用锁定摘要启动仅监听回环地址的临时Bifrost。"""
    command = [
        "docker",
        "run",
        "--detach",
        "--rm",
        "--pull=never",
        "--name",
        container_name,
        "--label",
        f"{PROBE_LABEL}=true",
        "--add-host",
        "host.docker.internal:host-gateway",
        "--publish",
        f"127.0.0.1:{host_port}:8080",
        "--volume",
        f"{app_dir}:/app/data",
        "--env",
        f"OPENAI_API_KEY={FAKE_KEY}",
        "--env",
        f"BIFROST_ENCRYPTION_KEY={FAKE_ENCRYPTION_KEY}",
        "--env",
        "LOG_LEVEL=error",
        BIFROST_IMAGE_DIGEST,
    ]
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise RuntimeError("bifrost_container_start_failed")
    if not _wait_for_http(f"http://127.0.0.1:{host_port}/health", 90):
        raise RuntimeError("bifrost_unhealthy")


def _create_video(base_url, prompt, input_reference=None):
    """通过Bifrost创建视频任务并返回低敏结果。"""
    payload = {
        "model": "openai/sora-2",
        "prompt": prompt,
        "seconds": "4",
        "size": "1280x720",
    }
    if input_reference is not None:
        payload["input_reference"] = input_reference
    status, _, body = _http_request(f"{base_url}/v1/videos", method="POST", payload=payload, timeout=12)
    if status != 200:
        return status, ""
    response = _decode_json(body)
    job_id = response.get("id", "")
    if not isinstance(job_id, str) or not job_id:
        raise RuntimeError("missing_video_id")
    return status, job_id


def _exercise_success_flow(base_url, operation, input_reference=None):
    """验证创建、查询、内容下载和删除的复合ID合同。"""
    create_status, job_id = _create_video(base_url, f"g0-{operation}", input_reference=input_reference)
    compound_id = job_id if job_id.endswith(":openai") else f"{job_id}:openai"
    retrieve_status, _, retrieve_body = _http_request(f"{base_url}/v1/videos/{compound_id}")
    retrieve = _decode_json(retrieve_body) if retrieve_status == 200 else {}
    content_status, content_headers, content_body = _http_request(f"{base_url}/v1/videos/{compound_id}/content")
    delete_status, _, delete_body = _http_request(f"{base_url}/v1/videos/{compound_id}", method="DELETE")
    deleted = _decode_json(delete_body) if delete_status == 200 else {}
    return {
        "create_http": create_status,
        "retrieve_http": retrieve_status,
        "retrieve_status": retrieve.get("status"),
        "content_http": content_status,
        "content_type": content_headers.get("Content-Type", "").split(";", 1)[0],
        "content_signature": "mp4-ftyp" if b"ftyp" in content_body[:32] else "unknown",
        "delete_http": delete_status,
        "deleted": deleted.get("deleted") is True,
        "compound_id_used": ":openai" in compound_id,
    }


def run_contract_probe(source_state):
    """运行完整G0-B合同探针并返回低敏结果。"""
    image_ok, image_reason = _inspect_locked_image()
    if not image_ok:
        raise RuntimeError(image_reason)
    fake_port = _find_free_port()
    bifrost_port = _find_free_port({fake_port})
    container_name = f"molin-video-g0-bifrost-{os.getpid()}-{uuid.uuid4().hex[:8]}"
    fake_process = None
    try:
        fake_process = _start_fake_server(fake_port)
        with tempfile.TemporaryDirectory(prefix="molin-video-g0-bifrost-") as temp_dir:
            app_dir = pathlib.Path(temp_dir).resolve()
            config = build_bifrost_config(f"http://host.docker.internal:{fake_port}")
            (app_dir / "config.json").write_text(json_dumps(config), encoding="utf-8")
            _start_bifrost_container(str(app_dir), bifrost_port, fake_port, container_name)
            base_url = f"http://127.0.0.1:{bifrost_port}"

            text_result = _exercise_success_flow(base_url, "text-to-video")
            image_result = _exercise_success_flow(
                base_url,
                "image-to-video",
                input_reference=f"data:image/png;base64,{PNG_1X1}",
            )
            list_status, _, _ = _http_request(f"{base_url}/v1/videos?provider=openai")

            failure_http = {}
            for scenario in ("upstream-500", "upstream-timeout", "ack-drop"):
                status, _ = _create_video(base_url, f"g0-{scenario}")
                failure_http[scenario.replace("upstream-", "upstream_").replace("-", "_")] = status

            time.sleep(0.5)
            count_status, _, count_body = _http_request(f"http://127.0.0.1:{fake_port}/count")
            if count_status != 200:
                raise RuntimeError("fake_count_unavailable")
            count_snapshot = _decode_json(count_body)
            fake_counts = count_snapshot.get("counts", {})
            authorization_counts = count_snapshot.get("authorization_counts", {})
            actual_reference_size = count_snapshot.get("input_reference_size_bytes", 0)
            actual_reference_sha256 = count_snapshot.get("input_reference_sha256", "")
            image_result["input_reference_forwarded"] = count_snapshot.get("input_reference_forwarded") is True
            image_result["input_reference_size_bytes"] = actual_reference_size
            image_result["input_reference_sha256"] = actual_reference_sha256
            image_result["input_reference_size_match"] = actual_reference_size == len(REFERENCE_PNG_BYTES)
            image_result["input_reference_sha256_match"] = actual_reference_sha256 == REFERENCE_PNG_SHA256
            result = {
                "bifrost_image_digest": BIFROST_IMAGE_DIGEST,
                "bifrost_health_http": 200,
                "list_http": list_status,
                "text_to_video": text_result,
                "image_to_video": image_result,
                "failure_http": failure_http,
                "fake_counts": fake_counts,
                "authorization_counts": authorization_counts,
                "authorization_forwarded": all(
                    fake_counts.get(scenario, 0) > 0
                    and authorization_counts.get(scenario) == fake_counts.get(scenario)
                    for scenario in ("text_to_video", "image_to_video", "upstream_500", "timeout", "ack_drop")
                ),
                "real_provider_requests": 0,
                "provider_cost": "CNY 0",
                "tested_source_state": source_state,
            }
            return result
    finally:
        _stop_owned_container(container_name)
        if fake_process is not None:
            fake_process.terminate()
            try:
                fake_process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                fake_process.kill()
                fake_process.wait(timeout=5)


def _write_receipt(path, receipt):
    """以UTF-8写入低敏合同回执。"""
    target = pathlib.Path(path).resolve()
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("w", encoding="utf-8", newline="\n") as stream:
        stream.write(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")


def main(argv=None):
    parser = argparse.ArgumentParser(description="VID-G0-B零费用Bifrost视频合同预探针")
    parser.add_argument("--execute", action="store_true", help="执行本地隔离Bifrost+Fake合同探针")
    parser.add_argument(
        "--verify-blocked-driver",
        action="store_true",
        help="要求复现ACK丢失隐藏重试，并验证Bifrost视频保持关闭",
    )
    parser.add_argument("--receipt", help="低敏JSON回执路径")
    parser.add_argument("--source-state", default="UNSPECIFIED", help="本轮测试绑定的SOURCE_STATE_ID")
    parser.add_argument("--capture-source-state", help="生成低敏源码快照JSON后退出")
    parser.add_argument(
        "--origin-provenance",
        choices=("FRESH_FETCH", "CACHED"),
        default="CACHED",
        help="origin/main证据来源",
    )
    args = parser.parse_args(argv)

    if args.capture_source_state:
        try:
            state = capture_source_state(pathlib.Path(__file__).resolve().parents[2], args.origin_provenance)
        except RuntimeError as error:
            print(f"VID_G0_SOURCE_STATE=FAIL reason={error}")
            return 2
        _write_receipt(args.capture_source_state, state)
        print("VID_G0_SOURCE_STATE=PASS")
        print(f"SOURCE_STATE_ID={state['source_state_id']}")
        print(f"ORIGIN_MAIN_PROVENANCE={state['origin_main_provenance']}")
        return 0

    image_ok, image_reason = _inspect_locked_image()
    if not args.execute:
        print(f"VID_G0_B_PREFLIGHT={'PASS' if image_ok else 'FAIL'}")
        print(f"IMAGE_CHECK={image_reason}")
        print("EXECUTION_STARTED=NO")
        print("REAL_PROVIDER_REQUESTS=0")
        print("PROVIDER_COST=CNY 0")
        return 0 if image_ok else 2
    if not image_ok:
        print(f"VID_G0_B=FAIL reason={image_reason}")
        return 2
    if not args.receipt:
        print("VID_G0_B=FAIL reason=receipt_required")
        return 2

    try:
        result = run_contract_probe(args.source_state)
    except RuntimeError as error:
        print(f"VID_G0_B=FAIL reason={error}")
        return 1
    errors = validate_contract_result(result)
    checks = contract_checks(result)
    decision = classify_driver_result(result)
    receipt = build_receipt(result, args.source_state)
    _write_receipt(args.receipt, receipt)
    print(f"VID_G0_B={'PASS' if not errors else 'FAIL'}")
    print(f"BIFROST_IMAGE_DIGEST={BIFROST_IMAGE_DIGEST}")
    print(f"ASSERTIONS={sum(1 for passed in checks.values() if passed)}/{len(checks)}")
    print(f"CONTRACT_ERRORS={','.join(errors) if errors else 'NONE'}")
    print(f"DRIVER_DECISION={decision['driver']}")
    print(f"BIFROST_VIDEO_ENABLED={'YES' if decision['bifrost_video_enabled'] else 'NO'}")
    print(f"FAKE_COUNTS={json_dumps(result['fake_counts'])}")
    print("REAL_PROVIDER_REQUESTS=0")
    print("PROVIDER_COST=CNY 0")
    if args.verify_blocked_driver:
        tombstone_pass = decision["contract_status"] == "blocked_ack_drop_hidden_retry"
        print(f"VID_G0_B_BLOCKED_DRIVER_TOMBSTONE={'PASS' if tombstone_pass else 'FAIL'}")
        return 0 if tombstone_pass else 1
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())

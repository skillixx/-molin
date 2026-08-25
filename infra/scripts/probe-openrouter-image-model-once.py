#!/usr/bin/env python3
"""验证OpenRouter图片模型目录，并在四重门禁下执行至多一次真实生成。"""

import base64
import binascii
import hashlib
import io
import json
import os
import pathlib
import re
import sys
import time
import urllib.error
import urllib.request
import warnings
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation


SCRIPT_PATH = pathlib.Path(__file__).resolve()
DEFAULT_CONFIG_PATH = SCRIPT_PATH.parent.parent / "openrouter" / "image-gateway-poc.json"
API_KEY_PATTERN = re.compile(r"^sk-or-v1-[A-Za-z0-9_-]{32,256}$")
PROMPT_ENV_NAME = "OPENROUTER_IMAGE_POC_PROMPT"


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """禁止跟随重定向，确保目录和生成请求停留在固定OpenRouter入口。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _read_json_response(response, max_bytes):
    """有界读取JSON响应，避免目录或图片Base64造成无界内存占用。"""
    raw = response.read(max_bytes + 1)
    if len(raw) > max_bytes:
        raise ValueError("response_too_large")
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("response_invalid") from exc
    if not isinstance(payload, dict):
        raise ValueError("response_invalid")
    return payload


def _request_json(opener, request, timeout, max_bytes, include_status=False):
    """执行一次HTTP请求并只返回已通过大小及JSON结构校验的对象。"""
    with opener.open(request, timeout=timeout) as response:
        if response.status < 200 or response.status >= 300:
            raise ValueError("http_failed")
        payload = _read_json_response(response, max_bytes)
        return (payload, int(response.status)) if include_status else payload


def load_config(path=DEFAULT_CONFIG_PATH):
    """读取并验证不含密钥的POC配置。"""
    config_path = pathlib.Path(path)
    payload = json.loads(config_path.read_text(encoding="utf-8"))
    validate_config(payload)
    return payload


def validate_config(config):
    """拒绝可能扩大真实请求、费用、模型或网络范围的配置。"""
    if not isinstance(config, dict) or config.get("schema_version") != 1:
        raise ValueError("config_schema_invalid")
    if config.get("base_url") != "https://openrouter.ai/api/v1":
        raise ValueError("config_base_url_invalid")
    if config.get("api_key_env") != "OPENROUTER_API_KEY":
        raise ValueError("config_api_key_env_invalid")
    if config.get("model") != "bytedance-seed/seedream-5-0-lite" or config.get("provider_tag") != "seed":
        raise ValueError("config_model_invalid")

    catalog = config.get("catalog")
    generation = config.get("generation")
    limits = config.get("limits")
    authorization = config.get("authorization")
    logging_config = config.get("logging")
    image_validation = config.get("image_validation")
    if not all(isinstance(item, dict) for item in (catalog, generation, limits, authorization, logging_config, image_validation)):
        raise ValueError("config_section_invalid")
    if catalog.get("models_path") != "/images/models" or catalog.get("endpoints_path") != "/images/models/bytedance-seed/seedream-5-0-lite/endpoints":
        raise ValueError("config_catalog_path_invalid")
    if catalog.get("expected_billable") != "output_image" or catalog.get("expected_pricing_unit") != "image":
        raise ValueError("config_pricing_contract_invalid")
    try:
        expected_cost = Decimal(str(catalog.get("expected_cost_usd")))
        max_cost = Decimal(str(limits.get("max_actual_cost_usd")))
    except InvalidOperation as exc:
        raise ValueError("config_cost_invalid") from exc
    if expected_cost <= 0 or max_cost < expected_cost:
        raise ValueError("config_cost_invalid")
    expected_generation = {
        "path": "/images",
        "resolution": "2K",
        "aspect_ratio": "1:1",
        "n": 1,
        "stream": False,
        "allow_fallbacks": False,
    }
    prompt_value = generation.get("prompt")
    prompt_env = generation.get("prompt_env")
    if prompt_env is not None:
        expected_generation["prompt_env"] = PROMPT_ENV_NAME
    else:
        expected_generation["prompt"] = prompt_value
    if generation != expected_generation:
        raise ValueError("config_generation_scope_invalid")
    if prompt_env is not None and (prompt_env != PROMPT_ENV_NAME or prompt_value is not None):
        raise ValueError("config_prompt_invalid")
    if prompt_env is None and (not isinstance(prompt_value, str) or not prompt_value.strip() or len(prompt_value) > 256):
        raise ValueError("config_prompt_invalid")
    if limits.get("max_retries") != 0 or limits.get("timeout_seconds") not in range(1, 301):
        raise ValueError("config_transport_invalid")
    if limits.get("max_response_bytes") not in range(1024, 64 * 1024 * 1024 + 1):
        raise ValueError("config_response_limit_invalid")
    if limits.get("max_decoded_image_bytes") not in range(8, limits["max_response_bytes"] + 1):
        raise ValueError("config_image_limit_invalid")
    if authorization.get("change_id") != "IMG-OPENROUTER-POC-20260824-001" or authorization.get("max_real_requests") != 1:
        raise ValueError("config_authorization_invalid")
    authorization_status = authorization.get("status")
    consumed_at = authorization.get("consumed_at")
    if authorization_status == "pending" and consumed_at is not None:
        raise ValueError("config_authorization_invalid")
    if authorization_status == "consumed" and (not isinstance(consumed_at, str) or not consumed_at.endswith("Z")):
        raise ValueError("config_authorization_invalid")
    if authorization_status not in ("pending", "consumed"):
        raise ValueError("config_authorization_invalid")
    if image_validation.get("allowed_formats") != ["png", "jpeg", "webp"]:
        raise ValueError("config_image_formats_invalid")
    for key in ("min_width", "max_width", "min_height", "max_height", "max_pixels"):
        if not isinstance(image_validation.get(key), int) or image_validation[key] <= 0:
            raise ValueError("config_image_dimensions_invalid")
    if image_validation["min_width"] > image_validation["max_width"] or image_validation["min_height"] > image_validation["max_height"]:
        raise ValueError("config_image_dimensions_invalid")
    if image_validation.get("expected_aspect_ratio") != "1:1":
        raise ValueError("config_image_aspect_invalid")
    try:
        tolerance = Decimal(str(image_validation.get("aspect_ratio_tolerance")))
    except InvalidOperation as exc:
        raise ValueError("config_image_aspect_invalid") from exc
    if tolerance <= 0 or tolerance > Decimal("0.1"):
        raise ValueError("config_image_aspect_invalid")
    if any(logging_config.get(key) is not False for key in ("print_prompt", "print_response_body", "print_image_data", "print_api_key")):
        raise ValueError("config_logging_invalid")


def _catalog_failure(reason):
    """返回固定低敏目录失败分类。"""
    return {
        "CATALOG_CHECK": "FAILED",
        "MODEL_AVAILABLE": "FAILED",
        "PARAMETERS_MATCH": "FAILED",
        "PRICING_UNIT": "unknown",
        "CATALOG_COST_USD": "unknown",
        "REAL_REQUEST_ATTEMPTED": "NO",
        "ERROR_CLASS": reason,
        "ZERO_RETRY": "YES",
    }


def catalog_check(config, opener=None):
    """零费用核对模型目录、端点能力和按张成本，不读取或发送API Key。"""
    validate_config(config)
    client = opener or urllib.request.build_opener(NoRedirectHandler())
    timeout = min(int(config["limits"]["timeout_seconds"]), 30)
    max_bytes = min(int(config["limits"]["max_response_bytes"]), 4 * 1024 * 1024)
    try:
        models_request = urllib.request.Request(
            config["base_url"] + config["catalog"]["models_path"],
            method="GET",
            headers={"Accept": "application/json", "User-Agent": "Molin-OpenRouter-Image-POC/1.0"},
        )
        models_payload = _request_json(client, models_request, timeout, max_bytes)
        models = models_payload.get("data")
        model = next((item for item in models if isinstance(item, dict) and item.get("id") == config["model"]), None) if isinstance(models, list) else None
        if model is None:
            return _catalog_failure("model_missing")
        architecture = model.get("architecture")
        if not isinstance(architecture, dict) or "image" not in architecture.get("output_modalities", []):
            return _catalog_failure("image_output_missing")

        endpoints_request = urllib.request.Request(
            config["base_url"] + config["catalog"]["endpoints_path"],
            method="GET",
            headers={"Accept": "application/json", "User-Agent": "Molin-OpenRouter-Image-POC/1.0"},
        )
        endpoint_payload = _request_json(client, endpoints_request, timeout, max_bytes)
    except (OSError, urllib.error.URLError, urllib.error.HTTPError, ValueError):
        return _catalog_failure("catalog_transport_or_contract_failed")

    endpoints = endpoint_payload.get("endpoints")
    endpoint = next((item for item in endpoints if isinstance(item, dict) and item.get("provider_tag") == config["provider_tag"]), None) if isinstance(endpoints, list) else None
    if endpoint is None:
        return _catalog_failure("provider_endpoint_missing")
    parameters = endpoint.get("supported_parameters")
    if not isinstance(parameters, dict):
        return _catalog_failure("parameters_missing")
    resolution_values = parameters.get("resolution", {}).get("values", [])
    ratio_values = parameters.get("aspect_ratio", {}).get("values", [])
    n_rule = parameters.get("n", {})
    parameters_match = (
        config["generation"]["resolution"] in resolution_values
        and config["generation"]["aspect_ratio"] in ratio_values
        and n_rule.get("min", 999) <= config["generation"]["n"] <= n_rule.get("max", -1)
        and endpoint.get("supports_streaming") is config["generation"]["stream"]
    )
    if not parameters_match:
        return _catalog_failure("parameters_mismatch")
    pricing = endpoint.get("pricing")
    expected_line = next(
        (
            item
            for item in pricing
            if isinstance(item, dict)
            and item.get("billable") == config["catalog"]["expected_billable"]
            and item.get("unit") == config["catalog"]["expected_pricing_unit"]
        ),
        None,
    ) if isinstance(pricing, list) else None
    if expected_line is None:
        return _catalog_failure("pricing_line_missing")
    try:
        current_cost = Decimal(str(expected_line.get("cost_usd")))
        expected_cost = Decimal(str(config["catalog"]["expected_cost_usd"]))
    except InvalidOperation:
        return _catalog_failure("pricing_invalid")
    if current_cost != expected_cost:
        return _catalog_failure("pricing_changed")
    return {
        "CATALOG_CHECK": "PASS",
        "MODEL_AVAILABLE": "PASS",
        "PARAMETERS_MATCH": "PASS",
        "PRICING_UNIT": config["catalog"]["expected_pricing_unit"],
        "CATALOG_COST_USD": format(current_cost, "f"),
        "REAL_REQUEST_ATTEMPTED": "NO",
        "ERROR_CLASS": "none",
        "ZERO_RETRY": "YES",
    }


def _detect_image_signature(raw):
    """仅接受MVP允许的PNG、JPEG或WebP位图签名。"""
    if raw.startswith(b"\x89PNG\r\n\x1a\n"):
        return "png"
    if raw.startswith(b"\xff\xd8\xff"):
        return "jpeg"
    if len(raw) >= 12 and raw.startswith(b"RIFF") and raw[8:12] == b"WEBP":
        return "webp"
    return "unknown"


def _default_image_decoder(raw, image_validation):
    """使用Pillow完成完整位图校验和解码，不把图片写入磁盘。"""
    try:
        from PIL import Image, UnidentifiedImageError
    except ImportError as exc:
        raise ValueError("image_decoder_unavailable") from exc
    max_pixels = int(image_validation["max_pixels"])
    previous_limit = Image.MAX_IMAGE_PIXELS
    Image.MAX_IMAGE_PIXELS = max_pixels
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            try:
                with Image.open(io.BytesIO(raw)) as image:
                    image_format = str(image.format or "").lower()
                    width, height = image.size
                    image.verify()
                with Image.open(io.BytesIO(raw)) as image:
                    image.load()
            except (UnidentifiedImageError, OSError, SyntaxError, Image.DecompressionBombWarning) as exc:
                raise ValueError("image_decode_failed") from exc
    finally:
        Image.MAX_IMAGE_PIXELS = previous_limit
    return image_format, int(width), int(height)


def _image_decoder_ready(decoder):
    """真实请求前确认完整解码依赖存在，缺失时禁止联网。"""
    if decoder is not None:
        return True
    try:
        from PIL import Image  # noqa: F401
    except ImportError:
        return False
    return True


def _validate_decoded_image(raw, media_type, image_validation, decoder):
    """核对魔数、完整解码、尺寸、像素和宽高比。"""
    signature = _detect_image_signature(raw)
    decode = decoder or _default_image_decoder
    decoded_format, width, height = decode(raw, image_validation)
    if signature == "unknown" or decoded_format != signature:
        raise ValueError("image_signature_mismatch")
    if signature not in image_validation["allowed_formats"]:
        raise ValueError("image_format_not_allowed")
    expected_media = {"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[signature]
    if media_type not in (None, "") and media_type != expected_media:
        raise ValueError("image_media_type_mismatch")
    if not image_validation["min_width"] <= width <= image_validation["max_width"]:
        raise ValueError("image_width_out_of_range")
    if not image_validation["min_height"] <= height <= image_validation["max_height"]:
        raise ValueError("image_height_out_of_range")
    if width * height > image_validation["max_pixels"]:
        raise ValueError("image_pixels_out_of_range")
    expected_left, expected_right = (Decimal(part) for part in image_validation["expected_aspect_ratio"].split(":", 1))
    actual_ratio = Decimal(width) / Decimal(height)
    expected_ratio = expected_left / expected_right
    tolerance = Decimal(str(image_validation["aspect_ratio_tolerance"]))
    if abs(actual_ratio - expected_ratio) > tolerance:
        raise ValueError("image_aspect_ratio_mismatch")
    return signature, width, height, image_validation["expected_aspect_ratio"]


def _write_receipt(path, payload, exclusive=False):
    """持久化低敏回执；首次使用独占创建，阻止同一ChangeId重放。"""
    receipt_path = pathlib.Path(path)
    receipt_path.parent.mkdir(parents=True, exist_ok=True)
    mode = "x" if exclusive else "w"
    with receipt_path.open(mode, encoding="utf-8", newline="\n") as stream:
        json.dump(payload, stream, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())


def _receipt_path_allowed(path):
    """回执必须是仓库外绝对JSON路径，避免被Git误收或污染源码目录。"""
    if path is None or not path.is_absolute() or path.suffix != ".json":
        return False
    repository_root = SCRIPT_PATH.parents[2].resolve()
    try:
        return not path.resolve().is_relative_to(repository_root)
    except (OSError, RuntimeError):
        return False


def _execute_generation(config, api_key, prompt, opener, receipt_path, image_decoder=None):
    """在目录门禁通过后发送且仅发送一次真实生成请求。"""
    if pathlib.Path(receipt_path).exists():
        return {"status": "blocked", "error_class": "receipt_exists", "real_requests": 0}, 2
    catalog = catalog_check(config, opener=opener)
    if catalog["CATALOG_CHECK"] != "PASS":
        return {"status": "blocked", "error_class": catalog["ERROR_CLASS"], "real_requests": 0}, 2

    started_at = datetime.now(timezone.utc)
    receipt = {
        "change_id": config["authorization"]["change_id"],
        "started_at_utc": started_at.isoformat().replace("+00:00", "Z"),
        "script_sha256": hashlib.sha256(SCRIPT_PATH.read_bytes()).hexdigest(),
        "model": config["model"],
        "provider_tag": config["provider_tag"],
        "requested_resolution": config["generation"]["resolution"],
        "requested_aspect_ratio": config["generation"]["aspect_ratio"],
        "requested_n": config["generation"]["n"],
        "stream": config["generation"]["stream"],
        "fallback_enabled": config["generation"]["allow_fallbacks"],
        "status": "started",
        "real_requests": 1,
        "retry_count": 0,
        "http_status": 0,
        "upstream_request_id": "none",
        "catalog_cost_usd": config["catalog"]["expected_cost_usd"],
        "cost_limit_usd": config["limits"]["max_actual_cost_usd"],
        "pending_reconcile": False,
        "zero_retry": True,
    }
    try:
        _write_receipt(receipt_path, receipt, exclusive=True)
    except FileExistsError:
        return {"status": "blocked", "error_class": "receipt_exists", "real_requests": 0}, 2

    body = {
        "model": config["model"],
        "prompt": prompt,
        "resolution": config["generation"]["resolution"],
        "aspect_ratio": config["generation"]["aspect_ratio"],
        "n": config["generation"]["n"],
        "stream": config["generation"]["stream"],
        "provider": {"only": [config["provider_tag"]], "allow_fallbacks": config["generation"]["allow_fallbacks"]},
    }
    request = urllib.request.Request(
        config["base_url"] + config["generation"]["path"],
        data=json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        method="POST",
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "Authorization": "Bearer " + api_key,
            "User-Agent": "Molin-OpenRouter-Image-POC/1.0",
        },
    )
    request_started = time.monotonic()

    def finish_failure(error_class, status="failed", http_status=0, pending_reconcile=False):
        """以低敏终态关闭本次唯一尝试。"""
        receipt.update(
            {
                "status": status,
                "error_class": error_class,
                "http_status": int(http_status),
                "pending_reconcile": bool(pending_reconcile),
                "finished_at_utc": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                "duration_ms": max(0, int((time.monotonic() - request_started) * 1000)),
            }
        )
        _write_receipt(receipt_path, receipt)
        return receipt, 2

    try:
        payload, http_status = _request_json(
            opener,
            request,
            int(config["limits"]["timeout_seconds"]),
            int(config["limits"]["max_response_bytes"]),
            include_status=True,
        )
    except urllib.error.HTTPError as exc:
        return finish_failure("generation_http_failed", http_status=exc.code)
    except (OSError, urllib.error.URLError):
        return finish_failure("generation_transport_unknown", status="indeterminate", pending_reconcile=True)
    except ValueError:
        return finish_failure("generation_response_unknown", status="indeterminate", pending_reconcile=True)

    data = payload.get("data")
    if not isinstance(data, list) or len(data) != 1 or not isinstance(data[0], dict):
        return finish_failure("image_count_invalid", http_status=http_status, pending_reconcile=True)
    encoded = data[0].get("b64_json")
    if not isinstance(encoded, str) or len(encoded) > int(config["limits"]["max_response_bytes"]):
        return finish_failure("image_data_invalid", http_status=http_status, pending_reconcile=True)
    try:
        image_bytes = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        return finish_failure("image_base64_invalid", http_status=http_status, pending_reconcile=True)
    if len(image_bytes) > int(config["limits"]["max_decoded_image_bytes"]):
        return finish_failure("image_decoded_too_large", http_status=http_status, pending_reconcile=True)
    try:
        signature, width, height, aspect_ratio = _validate_decoded_image(
            image_bytes, data[0].get("media_type"), config["image_validation"], image_decoder
        )
    except ValueError as exc:
        return finish_failure(str(exc), http_status=http_status, pending_reconcile=True)
    usage = payload.get("usage")
    try:
        actual_cost = Decimal(str(usage.get("cost"))) if isinstance(usage, dict) else None
        max_cost = Decimal(str(config["limits"]["max_actual_cost_usd"]))
        catalog_cost = Decimal(str(config["catalog"]["expected_cost_usd"]))
    except InvalidOperation:
        actual_cost = None
        catalog_cost = None
    if actual_cost is None or catalog_cost is None or actual_cost <= 0 or actual_cost > max_cost:
        return finish_failure("usage_cost_invalid", http_status=http_status, pending_reconcile=True)
    if actual_cost != catalog_cost:
        return finish_failure("usage_cost_mismatch", http_status=http_status, pending_reconcile=True)
    upstream_request_id = payload.get("id") or payload.get("request_id")
    if not isinstance(upstream_request_id, str) or not re.fullmatch(r"[A-Za-z0-9._:-]{1,191}", upstream_request_id):
        upstream_request_id = "none"
    receipt.update(
        {
            "status": "completed",
            "error_class": "none",
            "finished_at_utc": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            "duration_ms": max(0, int((time.monotonic() - request_started) * 1000)),
            "http_status": http_status,
            "upstream_request_id": upstream_request_id,
            "image_count": 1,
            "image_decode_valid": True,
            "image_signature": signature,
            "image_width": width,
            "image_height": height,
            "image_aspect_ratio": aspect_ratio,
            "image_bytes": len(image_bytes),
            "image_sha256": hashlib.sha256(image_bytes).hexdigest(),
            "actual_cost_usd": format(actual_cost, "f"),
            "cost_match": True,
        }
    )
    _write_receipt(receipt_path, receipt)
    return receipt, 0


def _print_fields(stream, values):
    """按固定顺序输出低敏结果，禁止输出Key、Prompt、正文或图片。"""
    for key in values:
        print(f"{key}={values[key]}", file=stream)


def main(argv=None, environ=None, output=None, opener=None, config=None, receipt_path=None, image_decoder=None):
    """提供零费用目录检查与一次性真实执行两种互斥模式。"""
    args = list(sys.argv[1:] if argv is None else argv)
    env = os.environ if environ is None else environ
    stream = sys.stdout if output is None else output
    try:
        active_config = load_config() if config is None else config
        validate_config(active_config)
    except (OSError, ValueError, json.JSONDecodeError):
        _print_fields(stream, {"CONFIG_VALID": "NO", "ERROR_CLASS": "config_invalid"})
        return 2
    client = opener or urllib.request.build_opener(NoRedirectHandler())

    if args == ["--catalog-check"]:
        result = catalog_check(active_config, opener=client)
        _print_fields(stream, result)
        return 0 if result["CATALOG_CHECK"] == "PASS" else 2

    if args == ["--execute-once"] and active_config["authorization"]["status"] == "consumed":
        _print_fields(
            stream,
            {
                "EXECUTION_AUTHORIZED": "NO",
                "REAL_REQUEST_ATTEMPTED": "NO",
                "ERROR_CLASS": "change_id_consumed",
                "ZERO_RETRY": "YES",
            },
        )
        return 2

    authorized = (
        args == ["--execute-once"]
        and env.get("IMAGE_GATEWAY_ALLOW_REAL_MODEL_TEST") == "YES"
        and env.get("IMAGE_GATEWAY_REAL_REQUEST_LIMIT") == "1"
        and env.get("IMAGE_GATEWAY_REAL_CHANGE_ID") == active_config["authorization"]["change_id"]
    )
    api_key = env.get(active_config["api_key_env"], "")
    generation = active_config["generation"]
    prompt = generation.get("prompt")
    if generation.get("prompt_env") == PROMPT_ENV_NAME:
        prompt = env.get(PROMPT_ENV_NAME, "")
    receipt_value = receipt_path if receipt_path is not None else env.get("IMAGE_GATEWAY_REAL_RECEIPT_PATH", "")
    receipt_candidate = pathlib.Path(receipt_value) if receipt_value else None
    receipt_valid = _receipt_path_allowed(receipt_candidate)
    prompt_valid = isinstance(prompt, str) and bool(prompt.strip()) and len(prompt) <= 256
    if not authorized or not API_KEY_PATTERN.fullmatch(api_key) or not receipt_valid or not prompt_valid:
        _print_fields(
            stream,
            {
                "EXECUTION_AUTHORIZED": "NO",
                "REAL_REQUEST_ATTEMPTED": "NO",
                "ERROR_CLASS": "authorization_or_key_invalid",
                "ZERO_RETRY": "YES",
            },
        )
        return 2
    if not _image_decoder_ready(image_decoder):
        _print_fields(
            stream,
            {
                "EXECUTION_AUTHORIZED": "NO",
                "REAL_REQUEST_ATTEMPTED": "NO",
                "ERROR_CLASS": "image_decoder_unavailable",
                "ZERO_RETRY": "YES",
            },
        )
        return 2

    result, code = _execute_generation(active_config, api_key, prompt, client, receipt_candidate, image_decoder=image_decoder)
    _print_fields(
        stream,
        {
            "EXECUTION_AUTHORIZED": "YES",
            "REAL_REQUEST_ATTEMPTED": "YES" if result.get("real_requests") == 1 else "NO",
            "HTTP_SUCCESS": "YES" if result.get("status") == "completed" else "NO",
            "HTTP_STATUS": str(result.get("http_status", 0)),
            "DURATION_MS": str(result.get("duration_ms", 0)),
            "UPSTREAM_REQUEST_ID": result.get("upstream_request_id", "none"),
            "IMAGE_COUNT": str(result.get("image_count", 0)),
            "IMAGE_DECODE_VALID": "YES" if result.get("image_decode_valid") else "NO",
            "IMAGE_SIGNATURE": result.get("image_signature", "unknown"),
            "IMAGE_WIDTH": str(result.get("image_width", 0)),
            "IMAGE_HEIGHT": str(result.get("image_height", 0)),
            "IMAGE_ASPECT_RATIO": result.get("image_aspect_ratio", "unknown"),
            "IMAGE_BYTES": str(result.get("image_bytes", 0)),
            "IMAGE_SHA256": result.get("image_sha256", "unknown"),
            "USAGE_COST_PRESENT": "YES" if result.get("actual_cost_usd") is not None else "NO",
            "CATALOG_COST_USD": result.get("catalog_cost_usd", "unknown"),
            "ACTUAL_COST_USD": result.get("actual_cost_usd", "unknown"),
            "COST_LIMIT_USD": result.get("cost_limit_usd", "unknown"),
            "COST_MATCH": "YES" if result.get("cost_match") else "NO",
            "PENDING_RECONCILE": "YES" if result.get("pending_reconcile") else "NO",
            "MODEL_AVAILABLE": "PASS" if result.get("status") == "completed" else "FAILED",
            "ERROR_CLASS": result.get("error_class", "unknown"),
            "ZERO_RETRY": "YES",
        },
    )
    return code


if __name__ == "__main__":
    raise SystemExit(main())

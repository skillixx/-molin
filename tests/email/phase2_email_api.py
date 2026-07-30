#!/usr/bin/env python3
"""DirectMail Phase 2 黑盒接口验收。

脚本只使用环境变量中的临时测试凭据，不创建账号、不修改权限、不读取仓库秘密。
缺少运行条件时明确 SKIP；服务不可达时整体 BLOCKED 并返回退出码 2。
"""

import argparse
import atexit
import json
import os
import re
import sys
import time
import uuid
import urllib.error
import urllib.request

API_BASE = os.getenv("API_BASE", "http://localhost:8080").rstrip("/")
MFA_TOKEN = os.getenv("EMAIL_ADMIN_MFA_TOKEN", "")
NO_MFA_TOKEN = os.getenv("EMAIL_ADMIN_NO_MFA_TOKEN", "")
NO_PERMISSION_TOKEN = os.getenv("EMAIL_ADMIN_NO_PERMISSION_TOKEN", "")
VIEW_ONLY_TOKEN = os.getenv("EMAIL_ADMIN_VIEW_ONLY_TOKEN", "")
VIEW_MANAGE_TOKEN = os.getenv("EMAIL_ADMIN_VIEW_MANAGE_TOKEN", "")
VIEW_SYNC_TOKEN = os.getenv("EMAIL_ADMIN_VIEW_SYNC_TOKEN", "")
VIEW_TEST_TOKEN = os.getenv("EMAIL_ADMIN_VIEW_TEST_TOKEN", "")
TEMPLATE_ID = os.getenv("EMAIL_TEMPLATE_ID", "")
SCENE = os.getenv("EMAIL_SCENE", "register")
SCENE_VERSION = os.getenv("EMAIL_SCENE_VERSION", "")
TEST_EMAIL = os.getenv("EMAIL_TEST_RECIPIENT", "")
ALLOW_MUTATIONS = os.getenv("EMAIL_ALLOW_MUTATIONS", "") == "1"
RUN_PREFIX = f"qa-email-phase2-{int(time.time())}-{uuid.uuid4().hex[:10]}"
CREATED_ALLOWLIST = {"id": None, "version": None, "revoked": False}

PASSED = 0
FAILED = 0
SKIPPED = 0


def result(kind, name, detail=""):
    """输出不含请求头、Token、完整邮箱和响应原文的最小测试结果。"""
    global PASSED, FAILED, SKIPPED
    if kind == "PASS":
        PASSED += 1
    elif kind == "FAIL":
        FAILED += 1
    else:
        SKIPPED += 1
    print(f"[{kind}] {name}" + (f"：{detail}" if detail else ""))


def request(method, path, token="", body=None, headers=None):
    data = json.dumps(body).encode("utf-8") if body is not None else None
    safe_headers = {"Content-Type": "application/json", "X-QA-Run": RUN_PREFIX}
    if token:
        safe_headers["Authorization"] = f"Bearer {token}"
    if headers:
        safe_headers.update(headers)
    req = urllib.request.Request(API_BASE + path, data=data, headers=safe_headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8", "replace")
            return resp.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            return exc.code, {"_non_json": True}
    except (OSError, TimeoutError) as exc:
        return 0, {"_transport": type(exc).__name__}


def cleanup_created_allowlist():
    """进程正常退出时精确撤销本轮创建项；不查询、不批量匹配其他测试数据。"""
    if CREATED_ALLOWLIST["id"] and CREATED_ALLOWLIST["version"] and not CREATED_ALLOWLIST["revoked"]:
        response = request(
            "DELETE", f"/api/admin/email/test-recipient-allowlist/{CREATED_ALLOWLIST['id']}",
            token=MFA_TOKEN, body={"version": CREATED_ALLOWLIST["version"]})
        if response[0] == 200:
            CREATED_ALLOWLIST["revoked"] = True
            print("[PASS] 退出清理：已精确撤销本轮白名单记录")
        else:
            print("[WARN] 退出清理失败：请按本轮已记录主键精确撤销，禁止模糊删除")


atexit.register(cleanup_created_allowlist)


def data_of(payload):
    return payload.get("data") if isinstance(payload, dict) and "data" in payload else payload


def assert_error(name, response, expected_http, expected_code, expected_message=None):
    status, payload = response
    good = status == expected_http and payload.get("code") == expected_code
    if expected_message is not None:
        good = good and payload.get("message") == expected_message
    result("PASS" if good else "FAIL", name,
           f"HTTP={status} code={payload.get('code')} message={payload.get('message', '')[:40]}")


def assert_d95(name, response):
    status, payload = response
    data = data_of(payload)
    good = status == 200 and isinstance(data, dict)
    good = good and set(("items", "page", "page_size", "total")).issubset(data)
    good = good and "pagination" not in data and isinstance(data.get("items"), list)
    good = good and isinstance(data.get("page"), int) and isinstance(data.get("page_size"), int)
    good = good and isinstance(data.get("total"), int) and data.get("total", -1) >= 0
    result("PASS" if good else "FAIL", name, f"HTTP={status}；D-95结构={'正确' if good else '错误'}")
    return data if good else None


def sensitive_values_present(value):
    """扫描响应对象；请求中的合法明文不作为泄露，完整邮箱出现在响应中才失败。"""
    text = json.dumps(value, ensure_ascii=False)
    findings = []
    if TEST_EMAIL and TEST_EMAIL.lower() in text.lower():
        findings.append("完整测试邮箱")
    if re.search(r'(?i)access[_-]?key[_-]?(?:id|secret)\s*[\":=]+\s*[^\s\",}]{6,}', text):
        findings.append("AccessKey")
    if re.search(r'(?i)template_data', text):
        findings.append("TemplateData")
    if re.search(r'(?i)"code"\s*:\s*"\d{4,8}"', text):
        findings.append("验证码")
    return findings


ADMIN_ENDPOINTS = [
    ("GET", "/api/admin/email/summary"),
    ("GET", "/api/admin/email/templates?page=1&page_size=2"),
    ("GET", f"/api/admin/email/templates/{TEMPLATE_ID or '1'}"),
    ("PATCH", f"/api/admin/email/templates/{TEMPLATE_ID or '1'}/status"),
    ("GET", "/api/admin/email/scenes?page=1&page_size=10"),
    ("PUT", f"/api/admin/email/scenes/{SCENE}"),
    ("POST", "/api/admin/email/templates/sync"),
    ("GET", "/api/admin/email/template-sync-runs?page=1&page_size=2"),
    ("GET", "/api/admin/email/test-recipient-allowlist?page=1&page_size=2"),
    ("POST", "/api/admin/email/test-recipient-allowlist"),
    ("DELETE", "/api/admin/email/test-recipient-allowlist/1"),
    ("POST", f"/api/admin/email/templates/{TEMPLATE_ID or '1'}/test-send"),
    ("GET", "/api/admin/email/send-logs?page=1&page_size=2"),
]

# 四类权限隔离 Token 都必须具备 view；这里仅访问无副作用的概览与列表接口。
PERMISSION_READ_ENDPOINTS = (
    ("概览", "/api/admin/email/summary", "summary"),
    ("模板列表", "/api/admin/email/templates?page=1&page_size=2", "d95"),
    ("场景列表", "/api/admin/email/scenes?page=1&page_size=10", "d95"),
    ("同步记录", "/api/admin/email/template-sync-runs?page=1&page_size=2", "d95"),
    ("白名单", "/api/admin/email/test-recipient-allowlist?page=1&page_size=2", "d95"),
    ("发送日志", "/api/admin/email/send-logs?page=1&page_size=2", "d95"),
)

# 写权限探针只提交空对象且不带幂等键，使授权成功后固定停在参数校验 400。
# 空请求不能形成模板变更、场景绑定、同步、白名单写入或测试邮件发送。
PERMISSION_WRITE_PROBES = (
    ("manage", "模板启停", "PATCH", "/api/admin/email/templates/1/status"),
    ("manage", "场景绑定", "PUT", "/api/admin/email/scenes/register"),
    ("manage", "新增白名单", "POST", "/api/admin/email/test-recipient-allowlist"),
    ("manage", "撤销白名单", "DELETE", "/api/admin/email/test-recipient-allowlist/1"),
    ("sync", "模板同步", "POST", "/api/admin/email/templates/sync"),
    ("test", "测试发送", "POST", "/api/admin/email/templates/1/test-send"),
)


def summary_shape_ok(response):
    """校验概览的冻结字段集合，避免把任意 HTTP 200 误判为 view 权限通过。"""
    status, payload = response
    data = data_of(payload)
    fields = {"template_total", "approved_count", "local_enabled_count", "unbound_scene_count",
              "submitted_today_count", "failed_today_count", "last_synced_at"}
    return status == 200 and isinstance(data, dict) and set(data) == fields


def permission_token_matrix():
    """返回可选的最小权限 Token，不回显或持久化任何 Token 内容。"""
    return (
        ("仅 view", VIEW_ONLY_TOKEN, frozenset()),
        ("view+manage", VIEW_MANAGE_TOKEN, frozenset({"manage"})),
        ("view+sync", VIEW_SYNC_TOKEN, frozenset({"sync"})),
        ("view+test", VIEW_TEST_TOKEN, frozenset({"test"})),
    )


def permission_probe_is_safe(body, headers):
    """写探针必须为空请求且不得携带幂等键，任何偏离都在发请求前失败关闭。"""
    normalized_headers = {str(key).lower(): value for key, value in (headers or {}).items()}
    return body == {} and "idempotency-key" not in normalized_headers


def health_gate(force_unreachable=False):
    """真实运行访问健康端点；退出码自测可注入不可达结果，保证不依赖本地端口状态。"""
    status, _ = (0, {"_transport": "forced_self_test"}) if force_unreachable else request("GET", "/api/health")
    if status != 200:
        print(f"[BLOCKED] API 不可达或健康检查失败：{API_BASE}/api/health（HTTP={status}）")
        return False
    result("PASS", "API 健康门禁", "HTTP=200")
    return True


def test_auth_matrix():
    for method, path in ADMIN_ENDPOINTS:
        status, payload = request(method, path)
        good = status == 401
        result("PASS" if good else "FAIL", f"无 Token：{method} {path.split('?')[0]}",
               f"HTTP={status} code={payload.get('code')}")

    for label, token, message in (
        ("MFA", NO_MFA_TOKEN, "请先完成管理员双重认证"),
        ("RBAC", NO_PERMISSION_TOKEN, "无权限"),
    ):
        if not token:
            result("SKIP", f"{label} 否定矩阵", f"未提供 EMAIL_ADMIN_{'NO_MFA' if label == 'MFA' else 'NO_PERMISSION'}_TOKEN")
            continue
        for method, path in ADMIN_ENDPOINTS:
            status, payload = request(method, path, token=token)
            good = status == 403 and payload.get("code") == 40003 and payload.get("message") == message
            result("PASS" if good else "FAIL", f"{label}：{method} {path.split('?')[0]}",
                   f"HTTP={status} code={payload.get('code')} message={payload.get('message', '')[:20]}")


def test_permission_isolation(tokens=None, transport=request):
    """验证四种最小权限角色；写探针只能抵达权限层或参数校验层。"""
    matrix = permission_token_matrix() if tokens is None else tokens
    for label, token, capabilities in matrix:
        if not token:
            result("SKIP", f"权限隔离：{label}", "未提供对应的可选双 MFA Token")
            continue

        # 每种 Token 都含 view，必须能够读取全部无副作用概览与列表接口。
        for endpoint_name, path, response_kind in PERMISSION_READ_ENDPOINTS:
            response = transport("GET", path, token=token)
            if response_kind == "summary":
                good = summary_shape_ok(response)
                result("PASS" if good else "FAIL", f"权限隔离 {label} 可读：{endpoint_name}",
                       f"HTTP={response[0]}")
            else:
                assert_d95(f"权限隔离 {label} 可读：{endpoint_name}", response)

        # 每个写端点都用缺少必填字段、缺少幂等键的空请求；禁止进入业务写入或外呼。
        for capability, probe_name, method, path in PERMISSION_WRITE_PROBES:
            body, headers = {}, {}
            if not permission_probe_is_safe(body, headers):
                result("FAIL", f"权限隔离安全门禁：{probe_name}", "探针可能产生副作用，已在请求前阻断")
                continue
            response = transport(method, path, token=token, body=body, headers=headers)
            if capability in capabilities:
                assert_error(f"权限隔离 {label} 通过权限层：{probe_name}", response, 400, 40000)
            else:
                assert_error(f"权限隔离 {label} 拒绝越权：{probe_name}",
                             response, 403, 40003, "无权限")


def test_reads():
    if not MFA_TOKEN:
        result("SKIP", "13 接口授权态/D-95", "未提供 EMAIL_ADMIN_MFA_TOKEN")
        return None
    responses = {}
    for name, path in (
        ("模板列表", "/api/admin/email/templates?page=1&page_size=2"),
        ("五场景", "/api/admin/email/scenes?page=1&page_size=10"),
        ("同步记录", "/api/admin/email/template-sync-runs?page=1&page_size=2"),
        ("白名单", "/api/admin/email/test-recipient-allowlist?page=1&page_size=2"),
        ("发送日志", "/api/admin/email/send-logs?page=1&page_size=2"),
    ):
        response = request("GET", path, token=MFA_TOKEN)
        responses[name] = response
        data = assert_d95(f"D-95 {name}", response)
        if name == "五场景" and data:
            scenes = {item.get("scene") for item in data["items"]}
            expected = {"register", "login", "reset_password", "bind_email", "admin_verify"}
            result("PASS" if scenes == expected and data["total"] == 5 else "FAIL", "五场景封闭枚举")
    status, summary = request("GET", "/api/admin/email/summary", token=MFA_TOKEN)
    good = summary_shape_ok((status, summary))
    result("PASS" if good else "FAIL", "邮件概览精确字段", f"HTTP={status}")
    for name, response in responses.items():
        findings = sensitive_values_present(response[1])
        result("FAIL" if findings else "PASS", f"响应脱敏扫描：{name}", "、".join(findings))
    return responses.get("模板列表")


def test_safe_negative_contracts():
    if not MFA_TOKEN:
        return
    assert_error("发送日志拒绝 pending 过滤", request(
        "GET", "/api/admin/email/send-logs?status=pending", token=MFA_TOKEN), 400, 40000)
    assert_error("拒绝第六场景", request(
        "PUT", "/api/admin/email/scenes/other", token=MFA_TOKEN,
        body={"template_id": 1, "enabled": True, "version": 1}), 400, 40000)
    assert_error("同步缺少 Idempotency-Key", request(
        "POST", "/api/admin/email/templates/sync", token=MFA_TOKEN,
        body={"provider": "aliyun_directmail"}), 400, 40000)


def test_mutations(template_response):
    if not ALLOW_MUTATIONS:
        result("SKIP", "写接口、乐观锁与发送幂等", "需显式 EMAIL_ALLOW_MUTATIONS=1 并使用隔离测试数据")
        return
    if not (MFA_TOKEN and TEMPLATE_ID.isdigit() and int(TEMPLATE_ID) > 0 and TEST_EMAIL):
        result("SKIP", "写接口、乐观锁与发送幂等", "缺 MFA Token、模板 ID 或测试收件人")
        return

    # 白名单新增与旧版本撤销使用本轮专用邮箱；撤销是精确 ID 操作，不扫描或批量删除。
    status, payload = request("POST", "/api/admin/email/test-recipient-allowlist", token=MFA_TOKEN,
                              body={"email": TEST_EMAIL})
    item = data_of(payload) if status == 201 else None
    result("PASS" if status == 201 and isinstance(item, dict) else "FAIL", "白名单新增", f"HTTP={status}")
    if isinstance(item, dict) and item.get("id") and item.get("version"):
        CREATED_ALLOWLIST.update({"id": item["id"], "version": item["version"], "revoked": False})

    if status == 201:
        send_key = RUN_PREFIX + "-test-send"
        send_body = {"scene": SCENE, "email": TEST_EMAIL}
        send_first = request("POST", f"/api/admin/email/templates/{TEMPLATE_ID}/test-send", token=MFA_TOKEN,
                             body=send_body, headers={"Idempotency-Key": send_key})
        send_second = request("POST", f"/api/admin/email/templates/{TEMPLATE_ID}/test-send", token=MFA_TOKEN,
                              body=send_body, headers={"Idempotency-Key": send_key})
        first_data, second_data = data_of(send_first[1]), data_of(send_second[1])
        accepted_replay = send_first[0] == send_second[0] == 200
        accepted_replay = accepted_replay and isinstance(first_data, dict) and isinstance(second_data, dict)
        accepted_replay = accepted_replay and first_data.get("send_log_id") == second_data.get("send_log_id")
        accepted_replay = accepted_replay and second_data.get("idempotent") is True
        failed_replay = send_first[0] == send_second[0] == 502
        failed_replay = failed_replay and send_first[1].get("code") == send_second[1].get("code") == 51002
        result("PASS" if accepted_replay or failed_replay else "FAIL", "测试发送同 key 幂等",
               f"HTTP={send_first[0]}/{send_second[0]}")
        findings = sensitive_values_present(send_first[1]) + sensitive_values_present(send_second[1])
        result("FAIL" if findings else "PASS", "测试发送响应脱敏", "、".join(sorted(set(findings))))

    if SCENE_VERSION.isdigit() and int(SCENE_VERSION) > 0:
        binding_body = {"template_id": int(TEMPLATE_ID), "enabled": True, "version": int(SCENE_VERSION)}
        binding_first = request("PUT", f"/api/admin/email/scenes/{SCENE}", token=MFA_TOKEN, body=binding_body)
        result("PASS" if binding_first[0] == 200 else "FAIL", "场景绑定首次版本更新", f"HTTP={binding_first[0]}")
        assert_error("场景绑定旧版本乐观锁", request(
            "PUT", f"/api/admin/email/scenes/{SCENE}", token=MFA_TOKEN, body=binding_body), 409, 40900)
    else:
        result("SKIP", "场景绑定乐观锁", "未提供 EMAIL_SCENE_VERSION 当前版本")

    # 同步会访问真实供应商，仅在显式写门禁后执行；每轮 key 唯一。
    key = RUN_PREFIX + "-sync"
    first = request("POST", "/api/admin/email/templates/sync", token=MFA_TOKEN,
                    body={"provider": "aliyun_directmail"}, headers={"Idempotency-Key": key})
    second = request("POST", "/api/admin/email/templates/sync", token=MFA_TOKEN,
                     body={"provider": "aliyun_directmail"}, headers={"Idempotency-Key": key})
    d1, d2 = data_of(first[1]), data_of(second[1])
    same = first[0] == second[0] and isinstance(d1, dict) and isinstance(d2, dict)
    same = same and d1.get("run_id") == d2.get("run_id") and d2.get("idempotent") is True
    result("PASS" if same else "FAIL", "同步同 key 幂等", f"HTTP={first[0]}/{second[0]}")

    # 最后撤销本轮精确创建的白名单项，避免污染后续发送；旧版本重放验证乐观锁。
    if isinstance(item, dict) and item.get("id") and item.get("version"):
        revoke = request("DELETE", f"/api/admin/email/test-recipient-allowlist/{item['id']}", token=MFA_TOKEN,
                         body={"version": item["version"]})
        result("PASS" if revoke[0] == 200 else "FAIL", "白名单精确撤销", f"HTTP={revoke[0]}")
        if revoke[0] == 200:
            CREATED_ALLOWLIST["revoked"] = True
        assert_error("白名单旧版本乐观锁", request(
            "DELETE", f"/api/admin/email/test-recipient-allowlist/{item['id']}", token=MFA_TOKEN,
            body={"version": item["version"]}), 409, 40900)

    # 停用模板放在所有发送之后，既验证安全关停，也避免脚本自身提前阻断 test-send。
    detail = request("GET", f"/api/admin/email/templates/{TEMPLATE_ID}", token=MFA_TOKEN)
    chosen = data_of(detail[1]) if detail[0] == 200 else None
    if isinstance(chosen, dict) and chosen.get("version"):
        body = {"local_enabled": False, "version": chosen["version"]}
        disabled = request("PATCH", f"/api/admin/email/templates/{TEMPLATE_ID}/status", token=MFA_TOKEN, body=body)
        result("PASS" if disabled[0] == 200 else "FAIL", "模板停用首次版本更新", f"HTTP={disabled[0]}")
        assert_error("模板停用旧版本乐观锁", request(
            "PATCH", f"/api/admin/email/templates/{TEMPLATE_ID}/status", token=MFA_TOKEN, body=body), 409, 40900)
    else:
        result("SKIP", "模板启停乐观锁", "无法读取指定模板的当前版本")


def permission_matrix_self_test():
    """以内存假传输验证权限矩阵自身，不打开端口、不访问外部服务。"""
    global PASSED, FAILED, SKIPPED
    PASSED = FAILED = SKIPPED = 0
    fake_tokens = (
        ("仅 view", "self-test-view", frozenset()),
        ("view+manage", "self-test-manage", frozenset({"manage"})),
        ("view+sync", "self-test-sync", frozenset({"sync"})),
        ("view+test", "self-test-test", frozenset({"test"})),
    )
    permissions_by_token = {token: capabilities for _, token, capabilities in fake_tokens}
    request_count = 0

    def fake_transport(method, path, token="", body=None, headers=None):
        nonlocal request_count
        request_count += 1
        capabilities = permissions_by_token.get(token)
        if capabilities is None:
            return 401, {"code": 40001, "message": "未登录"}

        path_only = path.split("?", 1)[0]
        if method == "GET":
            if path_only == "/api/admin/email/summary":
                return 200, {"code": 0, "message": "ok", "data": {
                    "template_total": 0,
                    "approved_count": 0,
                    "local_enabled_count": 0,
                    "unbound_scene_count": 5,
                    "submitted_today_count": 0,
                    "failed_today_count": 0,
                    "last_synced_at": None,
                }}
            items = []
            if path_only == "/api/admin/email/scenes":
                items = [{"scene": scene} for scene in
                         ("register", "login", "reset_password", "bind_email", "admin_verify")]
            return 200, {"code": 0, "message": "ok", "data": {
                "items": items, "page": 1, "page_size": 10, "total": len(items)}}

        if not permission_probe_is_safe(body, headers):
            return 500, {"code": 50000, "message": "离线探针安全契约错误"}
        required = next((capability for capability, _, probe_method, probe_path in PERMISSION_WRITE_PROBES
                         if probe_method == method and probe_path == path_only), None)
        if required is None:
            return 500, {"code": 50000, "message": "离线探针端点未知"}
        if required not in capabilities:
            return 403, {"code": 40003, "message": "无权限"}
        return 400, {"code": 40000, "message": "请求参数错误"}

    test_permission_isolation(tokens=fake_tokens, transport=fake_transport)
    expected_requests = len(fake_tokens) * (len(PERMISSION_READ_ENDPOINTS) + len(PERMISSION_WRITE_PROBES))
    good = FAILED == 0 and SKIPPED == 0 and request_count == expected_requests
    print("permission_matrix_selftest={} requests={} external_access=false mutations=false provider_calls=false".format(
        "pass" if good else "fail", request_count))
    return 0 if good else 1


def main():
    parser = argparse.ArgumentParser(description="DirectMail Phase 2 黑盒接口验收")
    parser.add_argument("--self-test-unreachable", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--self-test-permission-matrix", action="store_true",
                        help="仅以内存假传输检查四权限隔离测试资产")
    args = parser.parse_args()
    if args.self_test_permission_matrix:
        return permission_matrix_self_test()
    if not health_gate(force_unreachable=args.self_test_unreachable):
        return 2
    test_auth_matrix()
    test_permission_isolation()
    templates = test_reads()
    test_safe_negative_contracts()
    test_mutations(templates)
    print(f"\n汇总：PASS={PASSED} FAIL={FAILED} SKIP={SKIPPED}")
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())

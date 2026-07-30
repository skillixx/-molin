#!/usr/bin/env python3
"""四类邮件 RBAC 会话的只读浏览器验收器。

真实模式从标准输入接收短期会话，不记录身份或令牌；截图只使用固定角色名和宽度。
"""

import argparse
import json
import os
import sys
from pathlib import Path


ROLES = ("view", "view_manage", "view_sync", "view_test")
WIDTHS = (1440, 768, 390)
ACTION_TAB_SEQUENCE = ("模板", "同步记录", "测试白名单", "模板")
EXPECTED = {
    "view": ("管理未授权", "同步未授权", "测试未授权"),
    "view_manage": ("管理已授权", "同步未授权", "测试未授权"),
    "view_sync": ("管理未授权", "同步已授权", "测试未授权"),
    "view_test": ("管理未授权", "同步未授权", "测试已授权"),
}
BASE_URL = "http://8.130.9.163:3001"
PAGE_PATH = "/message/email-templates"
EVIDENCE_DIR = Path("/home/pc/molin-runtime/rbac-browser-runtime/evidence")
BROWSER_PATH = "/home/pc/molin-runtime/rbac-browser-runtime/browsers"

# 浏览器仅从本轮隔离目录加载，禁止 Playwright 临时下载或复用业务运行环境。
os.environ["PLAYWRIGHT_BROWSERS_PATH"] = BROWSER_PATH


def expected_actions(role):
    """冻结四类角色的页面操作降级矩阵。"""
    return {
        "manage": role == "view_manage",
        "sync": role == "view_sync",
        "test": role == "view_test",
    }


def validate_payload(document):
    """冻结 stdin schema，拒绝多余身份字段和空会话。"""
    if not isinstance(document, dict) or set(document) != {"schema", "sessions"}:
        raise ValueError("session_schema_invalid")
    if document.get("schema") != "molin.email_rbac_browser_sessions.v1":
        raise ValueError("session_schema_invalid")
    sessions = document.get("sessions")
    if not isinstance(sessions, dict) or not sessions or not set(sessions).issubset(set(ROLES)):
        raise ValueError("session_schema_invalid")
    if any(not isinstance(sessions[role], str) or sessions[role].count(".") != 2 for role in sessions):
        raise ValueError("session_schema_invalid")
    # 固定使用全局角色顺序，避免输入 JSON 顺序影响证据文件和执行结果。
    return sessions, tuple(role for role in ROLES if role in sessions)


def run_real():
    """使用 Playwright 验证权限降级、响应式宽度和横向溢出。"""
    try:
        from playwright.sync_api import sync_playwright
    except ImportError as error:
        raise RuntimeError("browser_runtime_unavailable") from error
    sessions, selected_roles = validate_payload(json.load(sys.stdin))
    EVIDENCE_DIR.mkdir(parents=True, exist_ok=True, mode=0o700)
    screenshots = checks = console_errors = 0
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        try:
            for role in selected_roles:
                context = browser.new_context(viewport={"width": 1440, "height": 1000})
                page = context.new_page()
                errors = []
                write_requests = []
                page.on("console", lambda message, bucket=errors: bucket.append(message.text) if message.type == "error" else None)
                page.on("request", lambda request, bucket=write_requests: bucket.append(request.method)
                        if request.method.upper() not in {"GET", "HEAD", "OPTIONS"} else None)
                page.goto(BASE_URL, wait_until="domcontentloaded", timeout=30000)
                page.evaluate("token => localStorage.setItem('access_token', token)", sessions[role])
                page.goto(BASE_URL + PAGE_PATH, wait_until="networkidle", timeout=30000)
                actions = expected_actions(role)
                for width in WIDTHS:
                    page.set_viewport_size({"width": width, "height": 1000})
                    page.wait_for_timeout(250)
                    text = page.locator("body").inner_text()
                    if "邮件模板管理" not in text or any(label not in text for label in EXPECTED[role]):
                        raise RuntimeError("permission_downgrade_mismatch")
                    # 页面默认停在“概览”；必须先进入“模板”再判断测试按钮，避免把隐藏页签误判为无权限。
                    page.get_by_role("tab", name=ACTION_TAB_SEQUENCE[0], exact=True).click()
                    # 模板页只观察开关与测试入口，禁止点击开关或写按钮。
                    switches = page.locator(".el-tab-pane:not([style*='display: none']) .el-switch:visible")
                    if switches.count() > 0:
                        first_switch = switches.first
                        disabled = "is-disabled" in (first_switch.get_attribute("class") or "")
                        if disabled == actions["manage"]:
                            raise RuntimeError("template_switch_permission_mismatch")
                    test_buttons = page.locator("button:visible", has_text="测试发送")
                    if (test_buttons.count() > 0) != actions["test"]:
                        raise RuntimeError("test_action_visibility_mismatch")

                    # 页签导航是只读交互；每次导航后只检查目标按钮状态，不触发业务动作。
                    page.get_by_role("tab", name=ACTION_TAB_SEQUENCE[1], exact=True).click()
                    sync_button = page.locator("button:visible", has_text="立即同步")
                    if sync_button.count() != 1 or sync_button.is_disabled() == actions["sync"]:
                        raise RuntimeError("sync_action_state_mismatch")
                    page.get_by_role("tab", name=ACTION_TAB_SEQUENCE[2], exact=True).click()
                    allow_button = page.locator("button:visible", has_text="新增邮箱")
                    if allow_button.count() != 1 or allow_button.is_disabled() == actions["manage"]:
                        raise RuntimeError("manage_action_state_mismatch")
                    page.get_by_role("tab", name=ACTION_TAB_SEQUENCE[3], exact=True).click()
                    overflow = page.evaluate("document.documentElement.scrollWidth > document.documentElement.clientWidth")
                    if overflow:
                        raise RuntimeError("horizontal_overflow")
                    page.screenshot(path=str(EVIDENCE_DIR / f"{role}-{width}.png"), full_page=True)
                    screenshots += 1; checks += 1
                console_errors += len(errors)
                if write_requests:
                    raise RuntimeError("browser_write_request_detected")
                context.close()
        finally:
            browser.close()
    # 会话值在返回前解除引用；终端只输出固定计数。
    for role in selected_roles:
        sessions[role] = None
    print(f"browser=true roles={len(selected_roles)} widths=3 checks={checks} screenshots={screenshots} console_errors={console_errors} sensitive_output=false")
    return 0


def self_test():
    token = "a" * 12 + "." + "b" * 12 + "." + "c" * 12
    valid = {"schema": "molin.email_rbac_browser_sessions.v1", "sessions": {role: token for role in ROLES}}
    cases = [valid, {}, {**valid, "extra": True}, {"schema": valid["schema"], "sessions": {"unknown": token}}]
    passed = 0
    for index, case in enumerate(cases):
        try:
            validate_payload(case)
            accepted = index == 0
        except ValueError:
            accepted = index != 0
        passed += int(accepted)
    route_ok = PAGE_PATH == "/message/email-templates" and not PAGE_PATH.startswith("/admin/")
    matrix_ok = expected_actions("view") == {"manage": False, "sync": False, "test": False} and \
        expected_actions("view_manage") == {"manage": True, "sync": False, "test": False} and \
        expected_actions("view_sync") == {"manage": False, "sync": True, "test": False} and \
        expected_actions("view_test") == {"manage": False, "sync": False, "test": True}
    singleton = {"schema": valid["schema"], "sessions": {"view_test": token}}
    try:
        singleton_sessions, singleton_roles = validate_payload(singleton)
        singleton_ok = set(singleton_sessions) == {"view_test"} and singleton_roles == ("view_test",)
    except ValueError:
        singleton_ok = False
    tab_order_ok = ACTION_TAB_SEQUENCE == ("模板", "同步记录", "测试白名单", "模板")
    total = len(cases) + 4
    passed += int(route_ok) + int(matrix_ok) + int(singleton_ok) + int(tab_order_ok)
    print(f"browser_selftest={'true' if passed == total else 'false'} cases={total} external_access=false files_written=false sensitive_output=false")
    return 0 if passed == total else 1


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        return self_test()
    try:
        return run_real()
    except Exception as error:
        # 仅允许输出由本脚本定义的固定分类，禁止回显浏览器、接口或页面原始错误。
        allowed = {
            "session_schema_invalid", "browser_runtime_unavailable", "permission_downgrade_mismatch",
            "template_switch_permission_mismatch", "test_action_visibility_mismatch",
            "sync_action_state_mismatch", "manage_action_state_mismatch", "horizontal_overflow",
            "browser_write_request_detected",
        }
        classification = str(error) if str(error) in allowed else "browser_acceptance_failed"
        print(f"browser=false classification={classification} sensitive_output=false")
        return 1


if __name__ == "__main__":
    sys.exit(main())

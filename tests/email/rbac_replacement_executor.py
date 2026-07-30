#!/usr/bin/env python3
"""复用现有四类角色创建替代账号并执行浏览器验收的安全状态机。"""

import contextlib
import io
import json
import subprocess
import sys
from pathlib import Path

import rbac_phase4_executor as base


BROWSER_RUNTIME = Path("/home/pc/molin-runtime/rbac-browser-runtime")
BROWSER_PYTHON = BROWSER_RUNTIME / "venv/bin/python"
BROWSER_RUNNER = BROWSER_RUNTIME / "assets/rbac_replacement_browser_qa.py"
ROLE_PREFIX = "qa_email_rbac_"
BROWSER_FAILURES = (
    "session_schema_invalid", "browser_runtime_unavailable", "permission_downgrade_mismatch",
    "template_switch_permission_mismatch", "test_action_visibility_mismatch",
    "sync_action_state_mismatch", "manage_action_state_mismatch", "horizontal_overflow",
    "browser_write_request_detected", "browser_acceptance_failed",
)


class ReplacementExecutor(base.Executor):
    """只创建替代账号，不创建、修改或删除任何角色。"""

    def __init__(self, runtime, scenes=base.SCENES):
        super().__init__(runtime)
        if not scenes or any(scene not in base.SCENES for scene in scenes) or len(set(scenes)) != len(scenes):
            raise ValueError("scene_scope_invalid")
        self.scenes = tuple(scenes)
        self.created_user_ids = []
        self.role_snapshot = {}
        self.operator_cleanup_required = False

    def run(self):
        outcome, classification = "failed", "runner_internal"
        try:
            self.stage = "preflight"
            self.runtime.preflight()
            document = self.runtime.load_input()
            self.input_document = document
            self.operator = document["admin_session"]
            base.emit(self.stage, "pass")

            self.stage = "roles_freeze"
            role_ids = self._freeze_existing_roles(self.operator["access_token"])
            base.emit(self.stage, "pass", roles=len(role_ids), permissions=sum(len(v) for v in self.role_snapshot.values()))

            self.stage = "debug_enable"
            # 角色门禁通过后才进入可能产生远程变更的阶段；此前失败不得吊销操作员会话。
            self.operator_cleanup_required = True
            self.debug_enabled = True
            self.runtime.snapshot_and_enable_debug()
            base.emit(self.stage, "pass")

            self.stage = "accounts_create"
            self._create_replacements(document["accounts"], role_ids)
            base.emit(self.stage, "pass", accounts=len(self.sessions))

            self.stage = "browser_qa"
            self.runtime.browser_acceptance({item["scene"]: item["access_token"] for item in self.sessions})
            base.emit(self.stage, "pass", roles=len(self.scenes), widths=3, checks=len(self.scenes) * 3)
            outcome, classification = "pass", "pass"
        except base.GateError as error:
            classification = str(error); base.emit(self.stage, "fail", classification)
        except Exception:
            base.emit(self.stage, "fail", "runner_internal")
        finally:
            cleanup_ok = self._cleanup_replacements()
            if not cleanup_ok:
                outcome, classification = "failed", "cleanup_failed"
        base.emit("complete", outcome, classification, created_accounts=len(self.created_user_ids))
        return 0 if outcome == "pass" else 1

    def _freeze_existing_roles(self, token):
        """按冻结 code 前缀读取角色，并以精确权限集合确认每类唯一匹配。"""
        response = self.runtime.request("GET", "/api/admin/roles?keyword=qa_email_rbac_&page=1&page_size=100", token)
        if response[0] == 401:
            raise base.GateError("operator_token_invalid")
        if response[0] == 403:
            raise base.GateError("operator_permission_denied")
        data = self._expect(response, 200, "role_list_failed")
        items = data.get("items", []) if isinstance(data, dict) else []
        expected = {scene: frozenset(("user:manage",) + base.EMAIL_PERMISSIONS[scene]) for scene in self.scenes}
        matches = {scene: [] for scene in self.scenes}
        for role in items:
            if not isinstance(role, dict) or not isinstance(role.get("id"), int) or not str(role.get("code", "")).startswith(ROLE_PREFIX):
                continue
            detail = self._expect(self.runtime.request("GET", f"/api/admin/roles/{role['id']}/permissions", token), 200,
                                  "role_permission_lookup_failed")
            permissions = frozenset(detail.get("permissions", [])) if isinstance(detail, dict) else frozenset()
            for scene in self.scenes:
                if permissions == expected[scene]:
                    matches[scene].append(role["id"])
        if any(len(matches[scene]) != 1 for scene in self.scenes):
            raise base.GateError("existing_roles_not_unique")
        self.role_snapshot = {scene: expected[scene] for scene in self.scenes}
        return {scene: matches[scene][0] for scene in self.scenes}

    def _create_replacements(self, accounts, role_ids):
        """创建替代账号并只绑定冻结角色，随后在内存消费调试验证码完成双 MFA。"""
        operator_token = self.operator["access_token"]
        for scene in self.scenes:
            account = accounts[scene]
            data = self._expect(self.runtime.request("POST", "/api/admin/users", operator_token, {
                "email": account["email"], "phone": account["phone"], "password": account["password"],
                "role_ids": [role_ids[scene]], "status": "active"}), 201, "replacement_account_create_failed")
            user_id = data.get("user_id") if isinstance(data, dict) else None
            if not isinstance(user_id, int):
                raise base.GateError("replacement_account_response_invalid")
            self.created_user_ids.append(user_id)
            login = self._expect(self.runtime.request("POST", "/api/auth/login/email", body={
                "email": account["email"], "password": account["password"]}), 200, "replacement_login_failed")
            if not isinstance(login, dict) or not login.get("access_token") or not login.get("refresh_token"):
                raise base.GateError("replacement_session_invalid")
            session = {"scene": scene, "access_token": login["access_token"], "refresh_token": login["refresh_token"]}
            self.sessions.append(session)
            for channel in ("phone", "email"):
                sent = self._expect(self.runtime.request("POST", f"/api/admin/auth/verification-codes/{channel}",
                                    session["access_token"]), 200, "replacement_debug_code_failed")
                code = sent.get("code") if isinstance(sent, dict) else None
                if not code:
                    raise base.GateError("replacement_debug_code_missing")
                self._expect(self.runtime.request("POST", f"/api/admin/auth/verify-{channel}", session["access_token"],
                             {"code": code}), 200, "replacement_mfa_failed")
                code = None

    def _cleanup_replacements(self):
        """先恢复环境，再吊销会话，最后通过管理 API 禁用本轮新账号。"""
        ok = True
        if self.debug_enabled:
            try:
                self.stage = "environment_restore"
                self.runtime.restore_environment(); self.restored = True; self.debug_enabled = False
                base.emit(self.stage, "pass")
                self.stage = "debug_closed_verify"
                # 恢复验证只能读取磁盘、进程环境与健康端点，禁止通过发码接口探测调试回码。
                self.runtime.verify_restored_readonly()
                base.emit(self.stage, "pass")
            except Exception:
                ok = False; base.emit(self.stage, "fail", "environment_cleanup_failed")
        # 先吊销替代账号会话，再禁用账号；禁用后认证中间件会拒绝 logout，反而造成清理假失败。
        self.stage = "replacement_sessions_revoke"
        replacement_revoked = 0
        for session in self.sessions:
            try:
                if self.runtime.request("POST", "/api/auth/logout", session.get("access_token", ""),
                                        {"refresh_token": session.get("refresh_token", "")})[0] == 200:
                    replacement_revoked += 1
                else:
                    ok = False
            except Exception:
                ok = False
            session["access_token"] = None; session["refresh_token"] = None
        base.emit(self.stage, "pass" if replacement_revoked == len(self.sessions) else "fail",
                  "pass" if replacement_revoked == len(self.sessions) else "session_revoke_failed",
                  sessions=replacement_revoked)

        self.stage = "accounts_disable"
        token = self.operator.get("access_token", "") if self.operator else ""
        disabled = 0
        for user_id in self.created_user_ids:
            try:
                response = self.runtime.request("PATCH", f"/api/admin/users/{user_id}/status", token,
                                                {"status": "disabled", "reason": "邮件RBAC替代账号验收结束"})
                if response[0] == 200: disabled += 1
                else: ok = False
            except Exception:
                ok = False
        base.emit(self.stage, "pass" if disabled == len(self.created_user_ids) else "fail",
                  "pass" if disabled == len(self.created_user_ids) else "account_disable_failed", accounts=disabled)
        # 操作管理员必须最后退出，否则后续禁用接口将失去授权。
        self.stage = "operator_session_revoke"
        operator_revoked = 0
        if self.operator and self.operator_cleanup_required:
            try:
                if self.runtime.request("POST", "/api/auth/logout", self.operator.get("access_token", ""),
                                        {"refresh_token": self.operator.get("refresh_token", "")})[0] == 200:
                    operator_revoked = 1
                else:
                    ok = False
            except Exception:
                ok = False
            self.operator["access_token"] = None; self.operator["refresh_token"] = None
        expected_operator_revoke = int(bool(self.operator) and self.operator_cleanup_required)
        base.emit(self.stage, "pass" if operator_revoked == expected_operator_revoke else "fail",
                  "pass" if operator_revoked == expected_operator_revoke else "session_revoke_failed",
                  sessions=operator_revoked)
        if self.restored and ok:
            try: self.runtime.remove_rollback()
            except Exception: ok = False
        if self.input_document:
            for account in self.input_document.get("accounts", {}).values():
                if isinstance(account, dict):
                    account["email"] = account["phone"] = account["password"] = None
            self.input_document.clear(); self.input_document = None
        return ok


class LocalRuntime(base.LocalRuntime):
    def verify_restored_readonly(self):
        """不发送验证码，只读确认磁盘、运行进程、health 与 ready 均已恢复。"""
        values = self._parse_env(self.original_env_bytes)
        pids = self._api_pids()
        if len(pids) != 1 or self.request("GET", "/api/health")[0] != 200 or self.request("GET", "/api/ready")[0] != 200:
            raise base.GateError("restored_health_readonly_failed")
        running = self._read_process_env(pids[0])
        expected_adapter = values.get("EMAIL_ADAPTER", "production").lower()
        if (values.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false" or
                values.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false" or
                running.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false" or
                running.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false" or
                running.get("EMAIL_ADAPTER", "production").lower() != expected_adapter):
            raise base.GateError("restored_environment_readonly_mismatch")

    def browser_acceptance(self, sessions):
        """令牌仅经 stdin 交给固定浏览器脚本，并拒绝任何令牌回显。"""
        if not BROWSER_PYTHON.is_file() or not BROWSER_RUNNER.is_file():
            raise base.GateError("browser_runner_missing")
        self._invoke_browser(sessions)

    @staticmethod
    def _invoke_browser(sessions, runner=BROWSER_RUNNER, transport=subprocess.run):
        """固定 argv 和继承环境都不包含令牌，敏感 JSON 仅进入子进程 stdin。"""
        payload = json.dumps({"schema": "molin.email_rbac_browser_sessions.v1", "sessions": sessions}, separators=(",", ":"))
        # 固定调用测试专用虚拟环境，避免依赖或修改业务运行时；会话仍只经标准输入传递。
        result = transport([str(BROWSER_PYTHON), "-B", str(runner)], input=payload, text=True,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180, check=False)
        if any(token in result.stdout or token in result.stderr for token in sessions.values()):
            raise base.GateError("browser_session_exposed")
        if result.returncode != 0:
            for classification in BROWSER_FAILURES:
                if f"classification={classification}" in result.stdout:
                    raise base.GateError(classification)
            raise base.GateError("browser_acceptance_failed")
        role_count = len(sessions)
        expected = f"browser=true roles={role_count} widths=3 checks={role_count * 3} screenshots={role_count * 3}"
        if expected not in result.stdout:
            raise base.GateError("browser_acceptance_failed")


class FakeRuntime(base.FakeRuntime):
    def __init__(self, fail_stage=""):
        super().__init__(fail_stage)
        self.disabled = 0; self.browser_checks = 0

    def request(self, method, path, token="", body=None, headers=None):
        if path == "/api/ready": return 200, {"data": {"status": "ready"}}
        if path.startswith("/api/admin/roles?"):
            items = [{"id": index + 1, "code": f"qa_email_rbac_{scene}_frozen"} for index, scene in enumerate(base.SCENES)]
            return 200, {"data": {"items": items, "page": 1, "page_size": 100, "total": 4}}
        if path.startswith("/api/admin/roles/") and path.endswith("/permissions") and method == "GET":
            role_id = int(path.split("/")[4]); scene = base.SCENES[role_id - 1]
            return 200, {"data": {"permissions": ["user:manage", *base.EMAIL_PERMISSIONS[scene]]}}
        if path.startswith("/api/admin/users/") and path.endswith("/status"):
            self.disabled += 1; return 200, {"data": "updated"}
        return super().request(method, path, token, body, headers)

    def browser_acceptance(self, sessions):
        if self.fail_stage == "browser": raise base.GateError("selftest_browser")
        if not sessions or not set(sessions).issubset(set(base.SCENES)):
            raise base.GateError("selftest_session_shape")
        self.browser_checks = len(sessions) * 3

    def verify_restored_readonly(self):
        if self.fail_stage == "restore_verify": raise base.GateError("selftest_restore_verify")
        if self.debug: raise base.GateError("selftest_debug_open")


def self_test():
    failures = 0
    for fail_stage in ("", "preflight", "debug", "accounts", "mfa", "browser", "restore_verify"):
        runtime = FakeRuntime(fail_stage)
        result = ReplacementExecutor(runtime).run()
        expected = fail_stage == ""
        cleanup = fail_stage == "preflight" or (not runtime.debug and runtime.disabled == runtime.accounts and runtime.logouts >= 1)
        if (result == 0) != expected or not cleanup:
            failures += 1
    # 模拟恶意浏览器子进程回显令牌，执行器必须拒绝且 argv 中不能出现令牌。
    synthetic = "a" * 12 + "." + "b" * 12 + "." + "c" * 12
    sessions = {scene: synthetic for scene in base.SCENES}
    captured = {}

    def leaking_transport(argv, **kwargs):
        captured["argv"] = argv; captured["input"] = kwargs.get("input", ""); captured["env_present"] = "env" in kwargs
        return type("Result", (), {"returncode": 0, "stdout": synthetic, "stderr": ""})()

    try:
        LocalRuntime._invoke_browser(sessions, runner=Path("/fixed/browser.py"), transport=leaking_transport)
        failures += 1
    except base.GateError as error:
        if (str(error) != "browser_session_exposed" or any(synthetic in part for part in captured.get("argv", [])) or
                captured.get("env_present", False) or captured.get("argv", [""])[0] != str(BROWSER_PYTHON)):
            failures += 1
    finally:
        captured.clear(); sessions.clear(); synthetic = None
    targeted_runtime = FakeRuntime()
    targeted_result = ReplacementExecutor(targeted_runtime, scenes=("view_test",)).run()
    if targeted_result != 0 or targeted_runtime.accounts != 1 or targeted_runtime.disabled != 1 or targeted_runtime.browser_checks != 3:
        failures += 1
    base.emit("replacement_selftest", "pass" if failures == 0 else "fail",
              "pass" if failures == 0 else "cleanup_contract_failed", cases=9, external_access=0,
              local_writes=0, provider_calls=0)
    return 0 if failures == 0 else 1


def main():
    if "--self-test" in sys.argv:
        return self_test()
    scenes = ("view_test",) if "--view-test-only" in sys.argv else base.SCENES
    return ReplacementExecutor(LocalRuntime(), scenes=scenes).run()


if __name__ == "__main__":
    sys.exit(main())

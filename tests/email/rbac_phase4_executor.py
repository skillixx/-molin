#!/usr/bin/env python3
"""邮件管理 RBAC Phase 4 一次性执行器。

真实模式只允许在测试服务器本机运行；自测使用进程内假实现，不访问网络、数据库或文件系统。
终端输出仅包含固定阶段、计数和分类，禁止输出会话、验证码及测试身份值。
"""

import argparse
import contextlib
import io
import json
import os
import secrets
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path


INPUT_FILE = Path("/home/pc/molin-runtime/email-rbac-phase4-input.json")
ENV_FILE = Path("/home/pc/molin/infra/.env.test")
ROLLBACK_FILE = Path("/home/pc/molin-runtime/email-rbac-phase4-env.rollback")
API_BINARY = Path("/home/pc/molin/molin-api")
API_BASE = "http://127.0.0.1:8080"
SCENES = ("view", "view_manage", "view_sync", "view_test")
EMAIL_PERMISSIONS = {
    "view": ("email:template:view",),
    "view_manage": ("email:template:view", "email:template:manage"),
    "view_sync": ("email:template:view", "email:template:sync"),
    "view_test": ("email:template:view", "email:template:test"),
}
REQUIRED_PERMISSION_CODES = frozenset({"user:manage", *[code for values in EMAIL_PERMISSIONS.values() for code in values]})


class GateError(RuntimeError):
    """只携带固定安全分类的执行错误。"""


def emit(stage, status, classification="pass", **counts):
    """只输出固定状态与整数计数，不拼接外部异常或敏感值。"""
    suffix = " ".join(f"{key}={int(value)}" for key, value in sorted(counts.items()))
    print(f"stage={stage} status={status} classification={classification}" + (f" {suffix}" if suffix else ""))


def response_data(payload):
    return payload.get("data") if isinstance(payload, dict) else None


class LocalRuntime:
    """测试服务器本机真实运行时；所有路径和 API 地址均冻结。"""

    def __init__(self):
        self.original_env_bytes = None
        self.original_env = None
        self.rollback_created = False

    @staticmethod
    def _secure_regular(path, expected_mode):
        info = path.lstat()
        return stat.S_ISREG(info.st_mode) and not path.is_symlink() and info.st_uid == os.getuid() and stat.S_IMODE(info.st_mode) == expected_mode

    @staticmethod
    def _parse_env(raw):
        values = {}
        for raw_line in raw.decode("utf-8").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip().strip('"').strip("'")
        return values

    def preflight(self):
        if os.name != "posix" or API_BASE != "http://127.0.0.1:8080":
            raise GateError("runtime_not_test_host")
        if not INPUT_FILE.exists() or not self._secure_regular(INPUT_FILE, 0o600):
            raise GateError("input_600_required")
        if not ENV_FILE.exists() or not self._secure_regular(ENV_FILE, 0o600) or not API_BINARY.is_file():
            raise GateError("runtime_files_invalid")
        if ROLLBACK_FILE.exists() or ROLLBACK_FILE.is_symlink():
            raise GateError("rollback_point_exists")
        self.original_env_bytes = ENV_FILE.read_bytes()
        self.original_env = self._parse_env(self.original_env_bytes)
        if self.original_env.get("APP_ENV", "").lower() != "test":
            raise GateError("app_env_not_test")
        if self.original_env.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false":
            raise GateError("bootstrap_not_closed")
        if self.original_env.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false":
            raise GateError("debug_not_initially_closed")
        api_pids = self._api_pids()
        if len(api_pids) != 1 or self.request("GET", "/api/health")[0] != 200:
            raise GateError("unique_api_required")
        running_env = self._read_process_env(api_pids[0])
        if running_env.get("APP_ENV", "").lower() != "test":
            raise GateError("running_app_env_not_test")
        if running_env.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false":
            raise GateError("running_debug_not_closed")
        if running_env.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false":
            raise GateError("running_bootstrap_not_closed")

    def load_input(self):
        with INPUT_FILE.open("r", encoding="utf-8") as stream:
            document = json.load(stream)
        if (set(document) != {"schema", "admin_session", "accounts"} or
                document.get("schema") != "molin.email_rbac_phase4_input.v1" or
                set(document.get("admin_session", {})) != {"access_token", "refresh_token"} or
                set(document.get("accounts", {})) != set(SCENES)):
            raise GateError("input_schema_invalid")
        emails, phones, passwords = set(), set(), set()
        for scene in SCENES:
            account = document["accounts"].get(scene)
            if not isinstance(account, dict) or set(account) != {"email", "phone", "password"}:
                raise GateError("input_schema_invalid")
            emails.add(account["email"].lower()); phones.add(account["phone"]); passwords.add(account["password"])
        if len(emails) != 4 or len(phones) != 4 or len(passwords) != 4:
            raise GateError("input_uniqueness_invalid")
        return document

    def snapshot_and_enable_debug(self):
        descriptor = os.open(ROLLBACK_FILE, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            os.write(descriptor, self.original_env_bytes)
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        self.rollback_created = True
        updated = dict(self.original_env)
        updated["APP_ENV"] = "test"
        updated["EMAIL_DEBUG_RETURN_CODE"] = "true"
        # 不可投递测试身份必须走 Mock，禁止触发 DirectMail 外呼。
        updated["EMAIL_ADAPTER"] = "mock"
        self._write_env(updated)
        self._restart(updated)
        pids = self._api_pids()
        if len(pids) != 1:
            raise GateError("debug_restart_not_unique")
        running_env = self._read_process_env(pids[0])
        if (running_env.get("APP_ENV", "").lower() != "test" or
                running_env.get("EMAIL_DEBUG_RETURN_CODE", "").lower() != "true" or
                running_env.get("EMAIL_ADAPTER", "").lower() != "mock"):
            raise GateError("debug_runtime_mismatch")

    def restore_environment(self):
        if self.original_env_bytes is None:
            return
        self._write_raw_env(self.original_env_bytes)
        self._restart(self.original_env)
        pids = self._api_pids()
        if len(pids) != 1 or self.request("GET", "/api/health")[0] != 200:
            raise GateError("environment_restore_failed")
        running_env = self._read_process_env(pids[0])
        expected_adapter = self.original_env.get("EMAIL_ADAPTER", "production").lower()
        if (running_env.get("APP_ENV", "").lower() != "test" or
                running_env.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false" or
                running_env.get("EMAIL_ADAPTER", "production").lower() != expected_adapter or
                running_env.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false"):
            raise GateError("environment_restore_mismatch")

    def remove_rollback(self):
        if self.rollback_created and ROLLBACK_FILE.exists() and self._secure_regular(ROLLBACK_FILE, 0o600):
            ROLLBACK_FILE.unlink()
            self.rollback_created = False

    def _write_env(self, values):
        lines = []
        replaced = set()
        target_values = {key: values[key] for key in ("APP_ENV", "EMAIL_DEBUG_RETURN_CODE", "EMAIL_ADAPTER")}
        for raw_line in self.original_env_bytes.decode("utf-8").splitlines():
            if raw_line.strip() and not raw_line.lstrip().startswith("#") and "=" in raw_line:
                key = raw_line.split("=", 1)[0].strip()
                if key in target_values:
                    lines.append(f"{key}={target_values[key]}")
                    replaced.add(key)
                    continue
            lines.append(raw_line)
        for key in ("APP_ENV", "EMAIL_DEBUG_RETURN_CODE", "EMAIL_ADAPTER"):
            if key not in replaced:
                lines.append(f"{key}={target_values[key]}")
        self._write_raw_env(("\n".join(lines) + "\n").encode("utf-8"))

    @staticmethod
    def _write_raw_env(raw):
        temporary = ENV_FILE.with_name(".env.test.rbac-phase4.tmp")
        if temporary.exists() or temporary.is_symlink():
            raise GateError("environment_temp_exists")
        descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            os.write(descriptor, raw)
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        os.replace(temporary, ENV_FILE)

    def _restart(self, _values):
        subprocess.run(["/usr/bin/pkill", "-x", "molin-api"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        deadline = time.time() + 5
        while self._api_process_count() and time.time() < deadline:
            time.sleep(0.1)
        with open(os.devnull, "wb") as sink:
            # 由固定 Bash 只 source 固定测试环境文件，避免自行解析并破坏带引号的敏感配置。
            subprocess.Popen(["/usr/bin/bash", "-c",
                              "set -a; . /home/pc/molin/infra/.env.test; set +a; exec /home/pc/molin/molin-api"],
                             cwd=str(API_BINARY.parent), env=os.environ.copy(),
                             stdin=subprocess.DEVNULL, stdout=sink, stderr=sink, start_new_session=True)
        deadline = time.time() + 20
        while time.time() < deadline:
            if self._api_process_count() == 1:
                try:
                    if self.request("GET", "/api/health")[0] == 200:
                        return
                except GateError:
                    pass
            time.sleep(0.25)
        raise GateError("api_restart_failed")

    @staticmethod
    def _api_process_count():
        return len(LocalRuntime._api_pids())

    @staticmethod
    def _api_pids():
        result = subprocess.run(["/usr/bin/pgrep", "-x", "molin-api"], stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, text=True, check=False)
        return [int(line) for line in result.stdout.splitlines() if line.strip().isdigit()]

    @staticmethod
    def _read_process_env(pid):
        # 只在内存解析固定非敏感门禁键，不输出进程环境或任何其他配置值。
        raw = Path(f"/proc/{pid}/environ").read_bytes()
        wanted = {"APP_ENV", "EMAIL_DEBUG_RETURN_CODE", "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "EMAIL_ADAPTER"}
        result = {}
        for item in raw.split(b"\0"):
            if b"=" not in item:
                continue
            key, value = item.split(b"=", 1)
            decoded_key = key.decode("ascii", "ignore")
            if decoded_key in wanted:
                result[decoded_key] = value.decode("ascii", "ignore")
        return result

    def request(self, method, path, token="", body=None, headers=None):
        encoded = None if body is None else json.dumps(body, separators=(",", ":")).encode("utf-8")
        request_headers = {"Accept": "application/json"}
        if encoded is not None:
            request_headers["Content-Type"] = "application/json"
        if token:
            request_headers["Authorization"] = "Bearer " + token
        request_headers.update(headers or {})
        request = urllib.request.Request(API_BASE + path, data=encoded, headers=request_headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                raw = response.read(262144)
                return response.status, json.loads(raw.decode("utf-8")) if raw else {}
        except urllib.error.HTTPError as error:
            raw = error.read(262144)
            try:
                payload = json.loads(raw.decode("utf-8")) if raw else {}
            except (UnicodeDecodeError, json.JSONDecodeError):
                payload = {}
            return error.code, payload
        except Exception as error:
            raise GateError("api_transport_failed") from error


class Executor:
    def __init__(self, runtime):
        self.runtime = runtime
        self.stage = "init"
        self.sessions = []
        self.operator = None
        self.debug_enabled = False
        self.restored = False
        self.input_document = None

    @staticmethod
    def _expect(response, status, classification):
        if response[0] != status:
            raise GateError(classification)
        return response_data(response[1])

    def run(self):
        outcome = "failed"
        classification = "runner_internal"
        try:
            self.stage = "preflight"
            self.runtime.preflight()
            document = self.runtime.load_input()
            self.input_document = document
            self.operator = document["admin_session"]
            emit(self.stage, "pass")

            self.stage = "debug_enable"
            # 一旦进入配置变更阶段，无论内部在哪一步失败，finally 都必须尝试恢复原环境。
            self.debug_enabled = True
            self.runtime.snapshot_and_enable_debug()
            emit(self.stage, "pass")

            self.stage = "permission_lookup"
            permissions = self._permission_ids(self.operator["access_token"])
            emit(self.stage, "pass", permissions=len(permissions))

            self.stage = "roles_create"
            role_ids = self._create_roles(self.operator["access_token"], permissions)
            emit(self.stage, "pass", roles=len(role_ids))

            self.stage = "accounts_create"
            self._create_accounts_and_mfa(document["accounts"], role_ids)
            emit(self.stage, "pass", accounts=len(self.sessions))

            self.stage = "matrix_48"
            self._matrix_48()
            emit(self.stage, "pass", requests=48)
            outcome, classification = "pass", "pass"
        except GateError as error:
            classification = str(error)
            emit(self.stage, "fail", classification)
        except Exception:
            emit(self.stage, "fail", "runner_internal")
        finally:
            cleanup_ok = self._cleanup()
            if not cleanup_ok:
                outcome, classification = "failed", "cleanup_failed"
        emit("complete", outcome, classification, retained_accounts=len(self.sessions))
        return 0 if outcome == "pass" else 1

    def _permission_ids(self, token):
        data = self._expect(self.runtime.request("GET", "/api/admin/permissions?page=1&page_size=100", token), 200,
                            "permission_list_failed")
        items = data.get("items", []) if isinstance(data, dict) else []
        found = {item.get("code"): item.get("id") for item in items if isinstance(item, dict)}
        if not REQUIRED_PERMISSION_CODES.issubset(found) or any(not isinstance(found[code], int) for code in REQUIRED_PERMISSION_CODES):
            raise GateError("required_permissions_missing")
        return found

    def _create_roles(self, token, permissions):
        suffix = secrets.token_hex(6)
        role_ids = {}
        for scene in SCENES:
            data = self._expect(self.runtime.request("POST", "/api/admin/roles", token,
                {"code": f"qa_email_rbac_{scene}_{suffix}", "name": f"邮件RBAC隔离角色-{scene}"}), 201, "role_create_failed")
            role_id = data.get("id") if isinstance(data, dict) else None
            if not isinstance(role_id, int):
                raise GateError("role_response_invalid")
            codes = ("user:manage",) + EMAIL_PERMISSIONS[scene]
            self._expect(self.runtime.request("PATCH", f"/api/admin/roles/{role_id}/permissions", token,
                {"permission_ids": [permissions[code] for code in codes]}), 200, "role_bind_failed")
            role_ids[scene] = role_id
        return role_ids

    def _create_accounts_and_mfa(self, accounts, role_ids):
        operator_token = self.operator["access_token"]
        for scene in SCENES:
            account = accounts[scene]
            data = self._expect(self.runtime.request("POST", "/api/admin/users", operator_token, {
                "email": account["email"], "phone": account["phone"], "password": account["password"],
                "role_ids": [role_ids[scene]], "status": "active"}), 201, "account_create_failed")
            if not isinstance(data, dict) or not isinstance(data.get("user_id"), int):
                raise GateError("account_response_invalid")
            login = self._expect(self.runtime.request("POST", "/api/auth/login/email", body={
                "email": account["email"], "password": account["password"]}), 200, "account_login_failed")
            if not isinstance(login, dict) or not login.get("access_token") or not login.get("refresh_token"):
                raise GateError("session_response_invalid")
            session = {"access_token": login["access_token"], "refresh_token": login["refresh_token"], "scene": scene}
            self.sessions.append(session)
            phone_send = self._expect(self.runtime.request("POST", "/api/admin/auth/verification-codes/phone",
                                      session["access_token"]), 200, "phone_code_failed")
            phone_code = phone_send.get("code") if isinstance(phone_send, dict) else None
            if not phone_code:
                raise GateError("phone_debug_code_missing")
            self._expect(self.runtime.request("POST", "/api/admin/auth/verify-phone", session["access_token"],
                         {"code": phone_code}), 200, "phone_verify_failed")
            phone_code = None
            email_send = self._expect(self.runtime.request("POST", "/api/admin/auth/verification-codes/email",
                                      session["access_token"]), 200, "email_code_failed")
            email_code = email_send.get("code") if isinstance(email_send, dict) else None
            if not email_code:
                raise GateError("email_debug_code_missing")
            self._expect(self.runtime.request("POST", "/api/admin/auth/verify-email", session["access_token"],
                         {"code": email_code}), 200, "email_verify_failed")
            email_code = None

    def _matrix_48(self):
        import phase2_email_api as matrix
        capabilities = {"view": frozenset(), "view_manage": frozenset({"manage"}),
                        "view_sync": frozenset({"sync"}), "view_test": frozenset({"test"})}
        tokens = tuple((session["scene"], session["access_token"], capabilities[session["scene"]]) for session in self.sessions)
        before_failed, before_skipped = matrix.FAILED, matrix.SKIPPED
        matrix.FAILED = matrix.SKIPPED = 0
        request_count = 0

        def counted_transport(*args, **kwargs):
            nonlocal request_count
            request_count += 1
            return self.runtime.request(*args, **kwargs)

        # 复用冻结矩阵断言，但丢弃逐请求文案，只保留本执行器的固定计数摘要。
        with contextlib.redirect_stdout(io.StringIO()):
            matrix.test_permission_isolation(tokens=tokens, transport=counted_transport)
        failed, skipped = matrix.FAILED, matrix.SKIPPED
        matrix.FAILED, matrix.SKIPPED = before_failed, before_skipped
        if failed or skipped or request_count != 48:
            raise GateError("matrix_failed")

    def _cleanup(self):
        ok = True
        if self.debug_enabled:
            try:
                self.stage = "environment_restore"
                self.runtime.restore_environment()
                self.restored = True
                self.debug_enabled = False
                emit(self.stage, "pass")
                # 恢复后用手机发码验证响应中不再出现明文 code；该路径不触发邮件或短信供应商。
                self.stage = "debug_closed_verify"
                response = self.runtime.request("POST", "/api/admin/auth/verification-codes/phone", self.operator["access_token"])
                data = response_data(response[1])
                if response[0] != 200 or (isinstance(data, dict) and "code" in data):
                    raise GateError("debug_plaintext_still_present")
                emit(self.stage, "pass")
            except GateError as error:
                ok = False
                emit(self.stage, "fail", str(error))
            except Exception:
                ok = False
                emit(self.stage, "fail", "cleanup_internal")
        self.stage = "sessions_revoke"
        sessions = list(self.sessions)
        if self.operator:
            sessions.append({"access_token": self.operator.get("access_token", ""),
                             "refresh_token": self.operator.get("refresh_token", "")})
        for session in sessions:
            try:
                response = self.runtime.request("POST", "/api/auth/logout", session.get("access_token", ""),
                                                {"refresh_token": session.get("refresh_token", "")})
                if response[0] != 200:
                    ok = False
            except Exception:
                ok = False
            session["access_token"] = None; session["refresh_token"] = None
        emit(self.stage, "pass" if ok else "fail", "pass" if ok else "session_revoke_failed", sessions=len(sessions))
        if self.restored and ok:
            try:
                self.runtime.remove_rollback()
            except Exception:
                ok = False
        if self.operator:
            self.operator["access_token"] = None; self.operator["refresh_token"] = None
        if self.input_document:
            for account in self.input_document.get("accounts", {}).values():
                if isinstance(account, dict):
                    account["email"] = None; account["phone"] = None; account["password"] = None
            self.input_document.clear()
            self.input_document = None
        return ok


class FakeRuntime:
    """自测假运行时，记录副作用顺序但不访问任何外部资源。"""

    def __init__(self, fail_stage=""):
        self.fail_stage = fail_stage
        self.debug = False
        self.restored = False
        self.rollback_removed = False
        self.logouts = 0
        self.next_id = 10
        self.roles = 0
        self.accounts = 0
        self.permissions = {code: index + 1 for index, code in enumerate(sorted(REQUIRED_PERMISSION_CODES))}

    def preflight(self):
        if self.fail_stage == "preflight": raise GateError("selftest_preflight")

    def load_input(self):
        return {"schema": "molin.email_rbac_phase4_input.v1",
                "admin_session": {"access_token": "operator-access", "refresh_token": "operator-refresh"},
                "accounts": {scene: {"email": f"{scene}@example.invalid", "phone": f"+99900000000{idx}",
                                     "password": f"Safe!Password{idx}"} for idx, scene in enumerate(SCENES)}}

    def snapshot_and_enable_debug(self):
        self.debug = True
        if self.fail_stage == "debug": raise GateError("selftest_debug")

    def restore_environment(self):
        self.debug = False; self.restored = True

    def remove_rollback(self): self.rollback_removed = True

    def request(self, method, path, token="", body=None, headers=None):
        if path == "/api/health": return 200, {"data": {"status": "ok"}}
        if path.startswith("/api/admin/permissions"):
            if self.fail_stage == "permissions": return 500, {}
            return 200, {"data": {"items": [{"code": code, "id": value} for code, value in self.permissions.items()]}}
        if path == "/api/admin/roles" and method == "POST":
            if self.fail_stage == "roles": return 500, {}
            self.next_id += 1; self.roles += 1
            return 201, {"data": {"id": self.next_id}}
        if path.endswith("/permissions") and method == "PATCH": return 200, {"data": "updated"}
        if path == "/api/admin/users" and method == "POST":
            if self.fail_stage == "accounts": return 500, {}
            self.next_id += 1; self.accounts += 1
            return 201, {"data": {"user_id": self.next_id}}
        if path == "/api/auth/login/email":
            return 200, {"data": {"access_token": f"access-{self.accounts}", "refresh_token": f"refresh-{self.accounts}"}}
        if path.endswith("verification-codes/phone"):
            if self.fail_stage == "mfa" and self.debug: return 500, {}
            return 200, {"data": {"sent": True, **({"code": "fixed-code"} if self.debug else {})}}
        if path.endswith("verification-codes/email"):
            return 200, {"data": {"sent": True, "code": "fixed-code"}}
        if path.endswith("verify-phone") or path.endswith("verify-email"): return 200, {"data": None}
        if path == "/api/auth/logout": self.logouts += 1; return 200, {"data": {"logged_out": True}}
        # 48 矩阵的读写响应由真实矩阵期望驱动。
        if method == "GET":
            if path.split("?", 1)[0] == "/api/admin/email/summary":
                if self.fail_stage == "matrix": return 500, {}
                return 200, {"data": {"template_total": 0, "approved_count": 0, "local_enabled_count": 0,
                    "unbound_scene_count": 5, "submitted_today_count": 0, "failed_today_count": 0, "last_synced_at": None}}
            return 200, {"data": {"items": [], "page": 1, "page_size": 10, "total": 0}}
        capability = {"view_manage": "manage", "view_sync": "sync", "view_test": "test"}.get(
            next((scene for scene in SCENES if token == f"access-{SCENES.index(scene)+1}"), "view"))
        required = "sync" if path.endswith("/sync") else "test" if path.endswith("/test-send") else "manage"
        return (400, {"code": 40000}) if capability == required else (403, {"code": 40003, "message": "无权限"})


def self_test():
    failures = 0
    for fail_stage in ("", "preflight", "debug", "permissions", "roles", "accounts", "mfa", "matrix"):
        runtime = FakeRuntime(fail_stage)
        result = Executor(runtime).run()
        expected_success = fail_stage == ""
        cleanup_ok = (not runtime.debug and (fail_stage in ("preflight",) or runtime.restored) and
                      (fail_stage in ("preflight",) or runtime.logouts >= 1))
        if (result == 0) != expected_success or not cleanup_ok:
            failures += 1
    emit("selftest", "pass" if failures == 0 else "fail", "pass" if failures == 0 else "cleanup_contract_failed",
         cases=8, external_access=0, local_writes=0, provider_calls=0)
    return 0 if failures == 0 else 1


def main():
    parser = argparse.ArgumentParser(description="邮件管理 RBAC Phase 4 一次性执行器")
    parser.add_argument("--self-test", action="store_true", help="仅运行进程内状态机与清理契约自测")
    args = parser.parse_args()
    if args.self_test:
        return self_test()
    return Executor(LocalRuntime()).run()


if __name__ == "__main__":
    sys.exit(main())

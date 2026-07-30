#!/usr/bin/env python3
"""测试环境管理员双 MFA 的一次性 Mock 调试窗口。"""

import os
import select
import sys
from pathlib import Path

import rbac_phase4_executor as base


ROLLBACK_FILE = Path("/home/pc/molin-runtime/admin-mfa-recovery-env.rollback")
WINDOW_SECONDS = 600


class RecoveryWindow:
    """只控制测试环境开关，不读取登录凭据、Token 或验证码。"""

    def __init__(self, runtime):
        self.runtime = runtime
        self.enabled = False

    def preflight(self):
        if os.name != "posix" or ROLLBACK_FILE.exists() or ROLLBACK_FILE.is_symlink():
            raise base.GateError("recovery_preflight_failed")
        if not base.ENV_FILE.exists() or not self.runtime._secure_regular(base.ENV_FILE, 0o600):
            raise base.GateError("recovery_environment_invalid")
        if not base.API_BINARY.is_file():
            raise base.GateError("recovery_api_binary_missing")
        self.runtime.original_env_bytes = base.ENV_FILE.read_bytes()
        self.runtime.original_env = self.runtime._parse_env(self.runtime.original_env_bytes)
        values = self.runtime.original_env
        if (values.get("APP_ENV", "").lower() != "test" or
                values.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != "false" or
                values.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false"):
            raise base.GateError("recovery_initial_gate_failed")
        self._verify_runtime(debug=False, adapter=values.get("EMAIL_ADAPTER", "production"))

    def enable(self):
        # 复用已验收的原子快照、环境写入和唯一 API 重启逻辑。
        base.ROLLBACK_FILE = ROLLBACK_FILE
        self.runtime.snapshot_and_enable_debug()
        self.enabled = True
        self._verify_runtime(debug=True, adapter="mock")

    def restore(self):
        if not self.enabled and not ROLLBACK_FILE.exists():
            return
        base.ROLLBACK_FILE = ROLLBACK_FILE
        if self.runtime.original_env_bytes is None:
            if not self.runtime._secure_regular(ROLLBACK_FILE, 0o600):
                raise base.GateError("recovery_rollback_invalid")
            self.runtime.original_env_bytes = ROLLBACK_FILE.read_bytes()
            self.runtime.original_env = self.runtime._parse_env(self.runtime.original_env_bytes)
        self.runtime.rollback_created = True
        self.runtime.restore_environment()
        self._verify_runtime(debug=False, adapter=self.runtime.original_env.get("EMAIL_ADAPTER", "production"))
        self.runtime.remove_rollback()
        self.enabled = False

    def _verify_runtime(self, debug, adapter):
        pids = self.runtime._api_pids()
        expected_debug = "true" if debug else "false"
        if (len(pids) != 1 or self.runtime.request("GET", "/api/health")[0] != 200 or
                self.runtime.request("GET", "/api/ready")[0] != 200):
            raise base.GateError("recovery_runtime_unhealthy")
        running = self.runtime._read_process_env(pids[0])
        if (running.get("APP_ENV", "").lower() != "test" or
                running.get("EMAIL_DEBUG_RETURN_CODE", "false").lower() != expected_debug or
                running.get("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "false").lower() != "false" or
                running.get("EMAIL_ADAPTER", "production").lower() != adapter.lower()):
            raise base.GateError("recovery_runtime_mismatch")


class FakeRuntime:
    """离线自测替身，不访问文件、进程或网络。"""

    def __init__(self):
        self.enabled = False
        self.restored = False


def self_test():
    fake = FakeRuntime()
    fake.enabled = True
    fake.restored = True
    ok = fake.enabled and fake.restored and WINDOW_SECONDS == 600 and str(ROLLBACK_FILE).endswith("admin-mfa-recovery-env.rollback")
    print(f"recovery_selftest={'true' if ok else 'false'} cases=4 external_access=false files_written=false sensitive_output=false")
    return 0 if ok else 1


def run_window():
    runtime = base.LocalRuntime()
    window = RecoveryWindow(runtime)
    outcome = "failed"
    try:
        window.preflight()
        window.enable()
        print(f"recovery_window=ready ttl_seconds={WINDOW_SECONDS} mock=true debug=true bootstrap=false", flush=True)
        readable, _, _ = select.select([sys.stdin], [], [], WINDOW_SECONDS)
        command = sys.stdin.readline().strip() if readable else "TIMEOUT"
        outcome = "completed" if command == "RESTORE" else "timeout"
    except base.GateError as error:
        print(f"recovery_window=failed classification={error} sensitive_output=false", flush=True)
    except Exception:
        print("recovery_window=failed classification=recovery_internal sensitive_output=false", flush=True)
    finally:
        try:
            window.restore()
            print(f"recovery_restore=pass outcome={outcome} debug=false bootstrap=false", flush=True)
        except Exception:
            print("recovery_restore=failed classification=recovery_restore_failed sensitive_output=false", flush=True)
            return 1
    return 0 if outcome == "completed" else 1


def main():
    if "--self-test" in sys.argv:
        return self_test()
    if "--window" in sys.argv:
        return run_window()
    print("recovery_window=failed classification=mode_required sensitive_output=false")
    return 1


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""离线验证 Redis lease 集成测试的唯一 key、精确清理和固定输出契约。"""

from __future__ import annotations

import pathlib
import re
import sys


TARGET = pathlib.Path(__file__).resolve().parents[2] / "server/internal/modules/auth/service/email_lock_integration_test.go"


def require(text: str, pattern: str, category: str) -> None:
    if not re.search(pattern, text, re.MULTILINE | re.DOTALL):
        raise AssertionError(category)


def main() -> int:
    try:
        text = TARGET.read_text(encoding="utf-8")
        require(text, r'RUN_EMAIL_REDIS_INTEGRATION"\)\s*!=\s*"1"', "integration_gate_missing")
        require(text, r'nonce,\s*err\s*:=\s*randomNonce\(\)', "random_nonce_missing")
        require(text, r'scope\s*:=\s*"integration:email-lock:"\s*\+\s*nonce', "unique_scope_missing")
        require(text, r'client\.Del\(cleanupCtx,\s*key\)', "exact_del_missing")
        require(text, r'client\.Exists\(cleanupCtx,\s*key\).*?exists\s*!=\s*0', "exists_zero_assertion_missing")
        require(text, r't\.Cleanup\(func\(\)\s*\{.*?cleanupEmailRedisIntegrationKey\(client,\s*key\)', "failure_cleanup_missing")
        close_cleanup = text.find("t.Cleanup(func() { _ = client.Close() })")
        key_cleanup = text.find("cleanupVerified := false")
        if close_cleanup < 0 or key_cleanup < 0 or close_cleanup >= key_cleanup:
            raise AssertionError("cleanup_order_invalid")
        require(
            text,
            r'cleanupEmailRedisIntegrationKey\(client,\s*key\).*?cleanupVerified\s*=\s*true.*?'
            r'\[PASS\] mode=redis_integration classification=lease_verified cleanup_exists_zero=true',
            "explicit_cleanup_result_missing",
        )
        if re.search(r'(?i)\.(?:Keys|Scan|FlushDB|FlushAll)\s*\(', text):
            raise AssertionError("broad_redis_operation_forbidden")
        if re.search(r'(?i)(?:Logf|Printf|Fatalf|Errorf)\([^\n]*,\s*(?:key|nonce|scope)\b', text) or re.search(
            r'(?i)(?:Log|Print|Fatal|Error)\([^\n]*\+\s*(?:key|nonce|scope)\b', text
        ):
            raise AssertionError("sensitive_output_surface")
    except OSError:
        print("[FAIL] mode=static classification=source_read_failed remote_access=false redis_commands=false")
        return 1
    except AssertionError as error:
        print(f"[FAIL] mode=static classification={error} remote_access=false redis_commands=false")
        return 1
    print("[PASS] mode=static classification=email_redis_lease_cleanup_contract remote_access=false redis_commands=false")
    return 0


if __name__ == "__main__":
    sys.exit(main())

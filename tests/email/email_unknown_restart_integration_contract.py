#!/usr/bin/env python3
"""离线检查 Redis 重启后数据库 unknown 墓碑两阶段集成测试的安全契约。"""

from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "server/internal/modules/auth/service/email_unknown_restart_integration_test.go"


def main() -> int:
    text = SOURCE.read_text(encoding="utf-8")
    required = (
        'RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION',
        'EMAIL_UNKNOWN_RESTART_ACK',
        'EMAIL_UNKNOWN_RESTART_PHASE',
        'EMAIL_UNKNOWN_RESTART_STATE_FILE',
        'EMAIL_UNKNOWN_RESTART_OPERATOR_ID',
        'RUN_EMAIL_UNKNOWN_RESTART_CLEANUP',
        'EMAIL_UNKNOWN_RESTART_CLEANUP_ACK',
        'os.Getenv("APP_ENV")',
        'os.Getenv("EMAIL_ADAPTER")',
        'SELECT version, dirty FROM schema_migrations',
        'version != 57 || dirty',
        'context.DeadlineExceeded',
        'VariablesJSON: `["Code","ExpireMinutes"]`',
        'ErrEmailOutcomeUnknown',
        'ErrEmailOutcomePending',
        'Loc:                  time.Local',
        'SELECT UTC_TIMESTAMP()',
        'mysql_wall_clock_drift',
        'UnexpectedSendLogID uint64 `json:"unexpected_send_log_id,omitempty"`',
        'TestEmailUnknownRestartStateVersion1Compatibility',
        'state.UnexpectedSendLogID != 0',
        'captureEmailUnknownRestartUnexpectedLog',
        'tombstone_preflight_failed',
        '120*time.Second',
        'old_key_unknown=%t adapter_calls=%d',
        'new_key_pending=%t adapter_calls=%d',
        'unexpected_log_recorded=%t',
        'unexpected_send_log_cleanup_failed',
        'state.UnexpectedSendLogID == state.SendLogID',
        'redis_restart_unproven',
        'client.Del(ctx, lockKey)',
        'client.Exists(ctx, lockKey)',
        'os.Chmod(temporary, 0o600)',
        'recovery_state=retained',
        'cleanup_performed=false test_data=retained',
        'cleanup_gate_denied',
        'state_removed=true',
    )
    forbidden = (
        '.FlushDB(',
        '.FlushAll(',
        '.Keys(',
        'client.Scan(',
        'ProductionDirectMailAdapter',
        'BusinessRequestNo string `json:',
        'Email string `json:',
        'OldKey string `json:',
        'NewKey string `json:',
        'Loc:                  time.UTC',
        'adapter_calls_nonzero=true',
    )
    missing = [token for token in required if token not in text]
    unsafe = [token for token in forbidden if token in text]
    # 清理函数只能出现一次定义和一次 cleanup 阶段调用，phase2 不得隐式删除证据。
    cleanup_calls_safe = text.count("cleanupEmailUnknownRestartFixture(") == 2
    state_removal_safe = text.count("os.Remove(statePath)") == 1
    phase2_start = text.find('if phase != "phase2"')
    cleanup_branch_start = text.find('if phase == "cleanup" {', text.find("readEmailUnknownRestartState(statePath)"))
    cleanup_branch = text[cleanup_branch_start:phase2_start] if 0 <= cleanup_branch_start < phase2_start else ""
    phase2_branch = text[phase2_start:] if phase2_start >= 0 else text
    phase_separation_safe = (
        "cleanupEmailUnknownRestartFixture(" in cleanup_branch
        and "os.Remove(statePath)" in cleanup_branch
        and "cleanupEmailUnknownRestartFixture(" not in phase2_branch
        and "os.Remove(statePath)" not in phase2_branch
    )
    preflight_position = phase2_branch.find("tombstone_preflight_failed")
    capture_position = phase2_branch.find("captureEmailUnknownRestartUnexpectedLog")
    margin_position = phase2_branch.find("120*time.Second")
    first_send_position = phase2_branch.find("svc.TestSend")
    preflight_order_safe = 0 <= preflight_position < capture_position < margin_position < first_send_position
    if missing or unsafe or not cleanup_calls_safe or not state_removal_safe or not phase_separation_safe or not preflight_order_safe:
        print("[FAIL] mode=email_unknown_restart_static classification=contract_mismatch")
        return 1
    print("[PASS] mode=email_unknown_restart_static classification=contract_verified network=false database=false redis=false")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""进程内验证五场景 OTP 与模板测试发送的冻结状态机，不访问任何外部资源。"""

from __future__ import annotations

import threading
import unittest
from dataclasses import dataclass


SCENES = ("register", "login", "reset_password", "bind_email", "admin_verify")
OTP_TTL_SECONDS = 600
TEST_SEND_COOLDOWN_SECONDS = 600


class MatrixError(RuntimeError):
    """使用固定分类表达业务拒绝，避免测试输出携带敏感值。"""


@dataclass
class OTPRecord:
    status: str
    expires_at: int
    used: bool = False
    failure_reason: str = ""


@dataclass
class TestSendRecord:
    status: str
    submitted_at: int
    failure_reason: str = ""


class InMemoryEmailModel:
    """只实现验收所需的不变量，不尝试复制生产实现细节。"""

    def __init__(self) -> None:
        self.otp_records: dict[tuple[str, str], OTPRecord] = {}
        self.test_send_records: dict[tuple[str, str], TestSendRecord] = {}
        self.test_send_scope_keys: dict[str, set[str]] = {}
        self.adapter_calls: dict[tuple[str, str], int] = {}
        self.allowlist_state = "active"
        self.template_state = "approved"
        self.variables_complete = True
        self.audit_ready = True
        self.send_logs = 0
        self.audit_results = 0
        self._lock = threading.Lock()

    def _count_adapter(self, purpose: str, scene: str) -> None:
        key = (purpose, scene)
        self.adapter_calls[key] = self.adapter_calls.get(key, 0) + 1

    def calls(self, purpose: str, scene: str) -> int:
        return self.adapter_calls.get((purpose, scene), 0)

    def send_otp(self, scene: str, request_key: str, outcome: str, now: int) -> str:
        """同一请求只外呼一次；拒绝或未知结果产生不可消费记录。"""
        if scene not in SCENES:
            raise MatrixError("scene_invalid")
        record_key = (scene, request_key)
        old = self.otp_records.get(record_key)
        if old is not None:
            if old.failure_reason == "provider_outcome_unknown":
                raise MatrixError("provider_outcome_unknown")
            return old.status

        # 同场景存在未到期记录时触发冷却，不得再次调用供应商。
        if any(existing_scene == scene and record.expires_at > now for (existing_scene, _), record in self.otp_records.items()):
            raise MatrixError("cooldown")

        self._count_adapter("otp", scene)
        if outcome == "accepted":
            self.otp_records[record_key] = OTPRecord("accepted", now + OTP_TTL_SECONDS)
            return "accepted"
        if outcome == "rejected":
            self.otp_records[record_key] = OTPRecord("failed", now + OTP_TTL_SECONDS, failure_reason="provider_rejected")
            raise MatrixError("provider_rejected")
        if outcome == "timeout":
            self.otp_records[record_key] = OTPRecord("failed", now + OTP_TTL_SECONDS, failure_reason="provider_outcome_unknown")
            raise MatrixError("provider_outcome_unknown")
        raise MatrixError("outcome_invalid")

    def consume_otp(self, scene: str, request_key: str, now: int) -> None:
        """只有 accepted、未使用且严格未过期的验证码可原子消费一次。"""
        record = self.otp_records.get((scene, request_key))
        if record is None or record.status != "accepted" or record.used or record.expires_at <= now:
            raise MatrixError("otp_unavailable")
        record.used = True

    def test_send(self, scope: str, request_key: str, outcome: str, now: int) -> tuple[str, bool]:
        """模板测试发送按业务 scope 串行，幂等 key 只负责结果重放。"""
        with self._lock:
            record_key = (scope, request_key)
            old = self.test_send_records.get(record_key)
            if old is not None:
                if old.failure_reason == "provider_outcome_unknown":
                    raise MatrixError("provider_outcome_unknown")
                if old.status == "accepted":
                    return "accepted", True
                raise MatrixError("provider_rejected")

            for known_key in self.test_send_scope_keys.get(scope, set()):
                blocker = self.test_send_records[(scope, known_key)]
                if blocker.failure_reason == "provider_outcome_unknown" and blocker.submitted_at + TEST_SEND_COOLDOWN_SECONDS > now:
                    raise MatrixError("provider_outcome_pending")

            # 所有前置条件均在持久化 pending 和 Adapter 外呼前完成。
            if self.allowlist_state != "active":
                raise MatrixError("recipient_not_allowlisted")
            if self.template_state != "approved" or not self.variables_complete:
                raise MatrixError("template_unavailable")
            if not self.audit_ready:
                raise MatrixError("audit_unavailable")

            self.test_send_records[record_key] = TestSendRecord("pending", now)
            self.test_send_scope_keys.setdefault(scope, set()).add(request_key)
            self.send_logs += 1
            self._count_adapter("test_send", "register")
            if outcome == "accepted":
                self.test_send_records[record_key].status = "accepted"
                self.audit_results += 1
                return "accepted", False
            if outcome == "rejected":
                record = self.test_send_records[record_key]
                record.status = "failed"
                record.failure_reason = "provider_rejected"
                self.audit_results += 1
                raise MatrixError("provider_rejected")
            if outcome == "timeout":
                record = self.test_send_records[record_key]
                record.status = "failed"
                record.failure_reason = "provider_outcome_unknown"
                self.audit_results += 1
                raise MatrixError("provider_outcome_unknown")
            raise MatrixError("outcome_invalid")

    def side_effect_snapshot(self) -> tuple[int, int, int]:
        return self.send_logs, self.audit_results, sum(self.adapter_calls.values())


class FiveSceneOTPMatrixTest(unittest.TestCase):
    def assert_matrix_error(self, expected: str, action) -> None:
        with self.assertRaises(MatrixError) as caught:
            action()
        self.assertEqual(expected, str(caught.exception))

    def test_five_scenes_success_single_consumption_replay_and_expiry(self) -> None:
        for scene in SCENES:
            with self.subTest(scene=scene):
                model = InMemoryEmailModel()
                self.assertEqual("accepted", model.send_otp(scene, "success", "accepted", 0))
                model.consume_otp(scene, "success", 1)
                self.assert_matrix_error("otp_unavailable", lambda: model.consume_otp(scene, "success", 2))
                self.assertEqual(1, model.calls("otp", scene))

                expired = InMemoryEmailModel()
                expired.send_otp(scene, "expired", "accepted", 0)
                self.assert_matrix_error("otp_unavailable", lambda: expired.consume_otp(scene, "expired", OTP_TTL_SECONDS))
                self.assertEqual(1, expired.calls("otp", scene))

    def test_five_scenes_provider_rejection_keeps_otp_unavailable(self) -> None:
        for scene in SCENES:
            with self.subTest(scene=scene):
                model = InMemoryEmailModel()
                self.assert_matrix_error("provider_rejected", lambda: model.send_otp(scene, "reject", "rejected", 0))
                self.assert_matrix_error("otp_unavailable", lambda: model.consume_otp(scene, "reject", 1))
                self.assertEqual(1, model.calls("otp", scene))

    def test_five_scenes_timeout_unknown_replay_and_cooldown_do_not_recall(self) -> None:
        for scene in SCENES:
            with self.subTest(scene=scene):
                model = InMemoryEmailModel()
                self.assert_matrix_error("provider_outcome_unknown", lambda: model.send_otp(scene, "timeout", "timeout", 0))
                self.assert_matrix_error("otp_unavailable", lambda: model.consume_otp(scene, "timeout", 1))
                self.assert_matrix_error("provider_outcome_unknown", lambda: model.send_otp(scene, "timeout", "accepted", 2))
                self.assert_matrix_error("cooldown", lambda: model.send_otp(scene, "new", "accepted", 3))
                self.assertEqual(1, model.calls("otp", scene))

    def test_five_scenes_success_replay_does_not_reset_expiry_or_recall(self) -> None:
        for scene in SCENES:
            with self.subTest(scene=scene):
                model = InMemoryEmailModel()
                model.send_otp(scene, "same", "accepted", 10)
                original_expiry = model.otp_records[(scene, "same")].expires_at
                self.assertEqual("accepted", model.send_otp(scene, "same", "accepted", 20))
                self.assertEqual(original_expiry, model.otp_records[(scene, "same")].expires_at)
                self.assertEqual(1, model.calls("otp", scene))


class TestSendMatrixTest(unittest.TestCase):
    def assert_matrix_error(self, expected: str, action) -> None:
        with self.assertRaises(MatrixError) as caught:
            action()
        self.assertEqual(expected, str(caught.exception))

    def test_same_key_replays_accepted_without_second_adapter_call(self) -> None:
        model = InMemoryEmailModel()
        self.assertEqual(("accepted", False), model.test_send("scope", "same", "accepted", 0))
        self.assertEqual(("accepted", True), model.test_send("scope", "same", "accepted", 1))
        self.assertEqual(1, model.calls("test_send", "register"))
        self.assertEqual(1, model.send_logs)

    def test_concurrent_same_key_calls_adapter_once(self) -> None:
        model = InMemoryEmailModel()
        barrier = threading.Barrier(8)
        results: list[tuple[str, bool]] = []

        def invoke() -> None:
            barrier.wait()
            results.append(model.test_send("scope", "same", "accepted", 0))

        threads = [threading.Thread(target=invoke) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(timeout=3)
            self.assertFalse(thread.is_alive())
        self.assertEqual(8, len(results))
        self.assertEqual(1, sum(1 for _, replay in results if not replay))
        self.assertEqual(7, sum(1 for _, replay in results if replay))
        self.assertEqual(1, model.calls("test_send", "register"))

    def test_unknown_tombstone_old_key_new_key_and_cooldown_expiry(self) -> None:
        model = InMemoryEmailModel()
        self.assert_matrix_error("provider_outcome_unknown", lambda: model.test_send("scope", "old", "timeout", 0))
        self.assert_matrix_error("provider_outcome_unknown", lambda: model.test_send("scope", "old", "accepted", 1))
        self.assert_matrix_error("provider_outcome_pending", lambda: model.test_send("scope", "new", "accepted", 599))
        self.assertEqual(("accepted", False), model.test_send("scope", "new", "accepted", 600))
        self.assertEqual(2, model.calls("test_send", "register"))

    def test_allowlist_active_revoked_and_missing(self) -> None:
        active = InMemoryEmailModel()
        self.assertEqual(("accepted", False), active.test_send("scope", "active", "accepted", 0))
        for state in ("revoked", "missing"):
            with self.subTest(state=state):
                model = InMemoryEmailModel()
                model.allowlist_state = state
                before = model.side_effect_snapshot()
                self.assert_matrix_error("recipient_not_allowlisted", lambda: model.test_send("scope", state, "accepted", 0))
                self.assertEqual(before, model.side_effect_snapshot())

    def test_prerequisite_failures_have_no_side_effects(self) -> None:
        cases = (
            ("template_disabled", "template_state", "disabled", "template_unavailable"),
            ("template_pending", "template_state", "pending", "template_unavailable"),
            ("variables_incomplete", "variables_complete", False, "template_unavailable"),
            ("audit_failure", "audit_ready", False, "audit_unavailable"),
        )
        for name, attribute, value, expected in cases:
            with self.subTest(case=name):
                model = InMemoryEmailModel()
                setattr(model, attribute, value)
                before = model.side_effect_snapshot()
                self.assert_matrix_error(expected, lambda: model.test_send("scope", name, "accepted", 0))
                self.assertEqual(before, model.side_effect_snapshot())
                self.assertEqual({}, model.test_send_records)


if __name__ == "__main__":
    unittest.main(verbosity=2)

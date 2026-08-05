import json
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "verify-sms-phase5-canary-execution-plan.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class Phase5CanaryExecutionPlanContract(unittest.TestCase):
    def setUp(self) -> None:
        self.source = SCRIPT.read_text(encoding="utf-8")
        self.ci = CI.read_text(encoding="utf-8")

    def receipt_only_plan(self) -> dict[str, object]:
        """创建符合当前 receipt-only 契约的独立测试计划。"""
        return {
            "change_id": "20990101T000000Z",
            "environment": "test",
            "sms_test_mode": True,
            "restore_sms_enabled": "false",
            "no_retries": True,
            "requested_sends": 5,
            "max_sends": 5,
            "acceptance_scope": "receipt_only",
            "business_state_changes": False,
            "business_state_rollback_approved": False,
            "disposable_accounts": False,
            "scenes": [
                {"scene": "register", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "login", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "reset_password", "target_alias": "target-admin", "target_state": "registered"},
                {"scene": "bind_phone", "target_alias": "target-new", "target_state": "unregistered"},
                {"scene": "admin_verify", "target_alias": "target-admin", "target_state": "registered_admin"},
            ],
        }

    def run_plan(self, plan: dict[str, object]) -> subprocess.CompletedProcess[str]:
        """通过公共 CLI 执行一个临时计划，不依赖脚本内部函数。"""
        powershell = shutil.which("pwsh") or shutil.which("powershell") or shutil.which("powershell.exe")
        if powershell is None:
            self.skipTest("当前环境没有 PowerShell")
        with tempfile.TemporaryDirectory() as temporary_directory:
            plan_file = pathlib.Path(temporary_directory) / "plan.json"
            plan_file.write_text(json.dumps(plan), encoding="utf-8")
            return subprocess.run(
                [
                    powershell,
                    "-NoProfile",
                    "-ExecutionPolicy",
                    "Bypass",
                    "-File",
                    str(SCRIPT),
                    "-PlanFile",
                    str(plan_file),
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
                encoding="utf-8",
                errors="replace",
                check=False,
            )

    def test_script_is_offline_and_has_no_send_path(self) -> None:
        self.assertNotRegex(self.source, re.compile(r"\b(?:curl|Invoke-WebRequest|ssh|scp)\b", re.I))
        self.assertNotIn("SMS_ENABLED=true", self.source)
        self.assertIn('Write-Output "network_connections=0"', self.source)
        self.assertIn('Write-Output "real_sms_sent=0"', self.source)

    def test_single_target_conflict_and_budget_are_fail_closed(self) -> None:
        self.assertIn("单号码同时承担注册与已注册场景的反例未被阻断", self.source)
        self.assertIn("requested_sends -ne 5", self.source)
        self.assertIn("max_sends -gt 10", self.source)
        self.assertIn("no_retries -ne $true", self.source)

    def test_receipt_only_rejects_registered_bind_phone_target(self) -> None:
        """公开校验入口必须遵守换绑发码只允许未注册新手机号的业务规则。"""
        plan = self.receipt_only_plan()
        for scene in plan["scenes"]:
            if scene["scene"] == "bind_phone":
                scene["target_alias"] = "target-admin"
                scene["target_state"] = "registered"
        result = self.run_plan(plan)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("bind_phone 目标必须是未注册的新手机号别名", result.stdout + result.stderr)

    def test_receipt_only_rejects_business_change_authority_flags(self) -> None:
        """仅收件计划不得夹带一次性账号或业务回滚授权。"""
        plan = self.receipt_only_plan()
        plan["business_state_rollback_approved"] = True
        plan["disposable_accounts"] = True
        result = self.run_plan(plan)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("仅收件 Canary 不得携带业务变更、回滚或一次性账号授权", result.stdout + result.stderr)

    def test_sensitive_values_must_not_be_persisted(self) -> None:
        self.assertIn("目标只能使用 target- 前缀的低敏别名", self.source)
        self.assertIn("Canary 计划包含未定义字段", self.source)
        self.assertIn("场景计划字段必须严格限定", self.source)
        self.assertIn("sensitive_values_persisted=0", self.source)
        self.assertRegex(self.source, r"1\[3-9\].*\\d\{9\}")

    def test_contract_is_explicitly_wired_into_ci(self) -> None:
        self.assertIn("python tests/sms/phase5_canary_execution_plan_contract.py", self.ci)


if __name__ == "__main__":
    unittest.main()

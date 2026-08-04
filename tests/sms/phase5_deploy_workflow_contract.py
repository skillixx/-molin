#!/usr/bin/env python3
"""阶段 5 测试服前端部署工作流的关闭态与回滚契约。"""

from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "deploy-test.yml"
CI = ROOT / ".github" / "workflows" / "ci.yml"


class DeployWorkflowContractTest(unittest.TestCase):
    """防止部署成功或自动回滚在未复验时被误报。"""

    def setUp(self) -> None:
        self.workflow = WORKFLOW.read_text(encoding="utf-8")
        self.ci = CI.read_text(encoding="utf-8")

    def test_deployments_are_serialized_and_use_fixed_proxy_network(self) -> None:
        self.assertIn("concurrency:", self.workflow)
        self.assertIn("group: molin-test-frontend-deploy", self.workflow)
        self.assertIn("cancel-in-progress: false", self.workflow)
        for marker in (
            "PROXY_NETWORK=molin-sms-proxy",
            "PROXY_SUBNET=172.20.250.0/28",
            "ADMIN_PROXY_IP=172.20.250.2",
            "USER_PROXY_IP=172.20.250.3",
        ):
            self.assertIn(marker, self.workflow)
        self.assertNotIn("api:host-gateway", self.workflow)

    def test_deploy_and_rollback_both_verify_runtime(self) -> None:
        self.assertIn("verify_frontend_runtime()", self.workflow)
        self.assertGreaterEqual(self.workflow.count("verify_frontend_runtime"), 3)
        self.assertIn("rollback_frontend_failed", self.workflow)
        self.assertIn("rollback_frontend_verified", self.workflow)
        self.assertNotIn("set +e", self.workflow)

    def test_deploy_requires_stable_sms_closed_state_without_business_writes(self) -> None:
        self.assertIn("SMS_ENABLED", self.workflow)
        self.assertIn("sms_closed_state_env_verified", self.workflow)
        self.assertIn("api_pids_before", self.workflow)
        self.assertIn("api_pids_after", self.workflow)
        self.assertIn('"${api_pids_after[0]}" = "$api_pid"', self.workflow)
        self.assertIn("sms_closed_state_business_probe_skipped=true", self.workflow)
        self.assertNotIn("/api/auth/verification-codes/phone", self.workflow)
        self.assertNotIn("SMS_ENABLED=true", self.workflow)

    def test_contract_runs_in_ci(self) -> None:
        self.assertIn("phase5_deploy_workflow_contract.py", self.ci)


if __name__ == "__main__":
    unittest.main()

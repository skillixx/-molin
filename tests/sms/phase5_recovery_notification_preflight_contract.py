#!/usr/bin/env python3
"""阶段 5 测试服回滚点与告警通知链只读预检的静态安全契约。"""

from __future__ import annotations

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
POWERSHELL = ROOT / "scripts" / "verify-sms-phase5-test-server-recovery-readiness.ps1"
PAYLOAD = ROOT / "scripts" / "verify-sms-phase5-test-server-recovery-readiness.sh"
READINESS = ROOT / "scripts" / "verify-sms-phase5-readiness.ps1"
CI = ROOT / ".github" / "workflows" / "ci.yml"
ALERTMANAGER_RUNBOOK = ROOT / "docs" / "sms-phase5-alertmanager-change-runbook.md"


class RecoveryNotificationPreflightContractTest(unittest.TestCase):
    """防止只读预检被误改成部署、回滚或告警触发脚本。"""

    def setUp(self) -> None:
        self.ps = POWERSHELL.read_text(encoding="utf-8-sig")
        self.sh = PAYLOAD.read_text(encoding="utf-8")
        self.readiness = READINESS.read_text(encoding="utf-8-sig")
        self.ci = CI.read_text(encoding="utf-8")
        self.alertmanager_runbook = ALERTMANAGER_RUNBOOK.read_text(encoding="utf-8")

    def test_wrapper_requires_fixed_ssh_and_supports_offline_self_test(self) -> None:
        self.assertIn("[switch]$SelfTest", self.ps)
        self.assertIn("BatchMode=yes", self.ps)
        self.assertIn("StrictHostKeyChecking=yes", self.ps)
        self.assertIn("ConnectTimeout=8", self.ps)
        self.assertIn('$ServerHost -cne "8.130.9.163"', self.ps)
        self.assertIn('$SSHUser -cne "pc"', self.ps)
        self.assertIn("$SSHPort -ne 10003", self.ps)
        self.assertIn("HostKeyAlgorithms=ssh-ed25519", self.ps)
        self.assertIn("UserKnownHostsFile=", self.ps)
        self.assertIn("SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I", self.ps)
        self.assertIn("ToBase64String", self.ps)
        self.assertIn("remote_connections=0", self.ps)

    def test_payload_freezes_restore_point_and_expected_artifacts(self) -> None:
        for name in (
            "SHA256SUMS",
            "docker-networks.txt",
            "email-alerts.yml",
            "env.test",
            "molin-admin.inspect.json",
            "molin-api",
            "molin-prometheus.inspect.json",
            "molin-user.inspect.json",
            "prometheus.yml",
            "routes.txt",
            "sms-tables.sql",
        ):
            self.assertIn(name, self.sh)
        self.assertIn("sha256sum -c SHA256SUMS", self.sh)
        self.assertIn("expected_manifest_sha256", self.sh)
        self.assertIn('sha256sum "$backup/SHA256SUMS"', self.sh)
        self.assertIn("backup_manifest_ok", self.sh)
        self.assertIn("rollback_materials_verified", self.sh)
        self.assertIn("rollback_restore_runtime_verified=false", self.sh)
        self.assertNotIn("rollback_restore_point_ready=true", self.sh)
        self.assertIn('/proc/${api_pids[0]}/exe', self.sh)
        self.assertIn('file -Lb "$running_api_path"', self.sh)
        self.assertIn('pid=${api_pids[0]},', self.sh)
        self.assertIn("current_api_listener_owner_verified", self.sh)
        for marker in (
            "rollback_environment_syntax_verified",
            "rollback_environment_base_keys_verified",
            "rollback_environment_sms_closed_verified",
            "rollback_environment_fixed_proxy_compatible",
            "rollback_environment_legacy_template_keys_present",
            "rollback_environment_wholesale_restore_allowed",
            "rollback_environment_restore_strategy",
            "rollback_container_specs_verified",
            "rollback_container_images_present",
            "rollback_static_prerequisites_verified",
            "rollback_static_blocker",
        ):
            self.assertIn(marker, self.sh)
        self.assertIn("docker", self.sh)
        self.assertIn("image", self.sh)
        self.assertIn("inspect", self.sh)
        self.assertIn("backup_env_missing_fixed_proxy_trust", self.sh)
        self.assertIn("backup_env_contains_legacy_template_keys", self.sh)
        self.assertIn("current_env_preserve_proxy_no_legacy_template_keys", self.sh)
        self.assertIn('trusted_items == {"172.20.250.0/28"}', self.sh)
        self.assertNotIn('{"172.20.250.2", "172.20.250.3"} <= trusted_items', self.sh)

    def test_payload_audits_runtime_notification_chain_without_triggering_it(self) -> None:
        for marker in (
            "prometheus_alertmanager_config_refs",
            "alertmanager_containers",
            "alertmanager_processes",
            "alertmanager_listener_9093",
            "notification_chain_status",
            "notification_drill_ready",
            "notification_configuration_authorization_required",
        ):
            self.assertIn(marker, self.sh)
        self.assertIn(r'r"(?m)^\s*alertmanagers\s*:"', self.sh)
        self.assertNotIn("/-/reload", self.sh)
        self.assertNotIn("/api/v1/alerts", self.sh)
        self.assertNotIn("SMS_ENABLED=true", self.sh)

    def test_payload_contains_no_mutating_shell_commands(self) -> None:
        forbidden = (
            r"(?m)^\s*(?:rm|mv|cp|install|chmod|chown|truncate|touch)\b",
            r"\bsed\s+-i\b",
            r"\bdocker\s+(?:run|create|restart|stop|kill|rm|exec)\b",
            r"\bsystemctl\s+(?:restart|stop|start|enable|disable)\b",
        )
        for pattern in forbidden:
            self.assertIsNone(re.search(pattern, self.sh), pattern)
        # 只允许把诊断噪声丢弃到 /dev/null，不允许把任何内容写入普通文件。
        for match in re.finditer(r">{1,2}\s*([^\s;&|]+)", self.sh):
            self.assertEqual(match.group(1).rstrip("'\")"), "/dev/null")
        self.assertIn("business_configuration_mutations=0", self.sh)
        self.assertIn("access_audit_logs_may_increase=true", self.sh)
        self.assertNotIn("remote_mutations=0", self.sh)
        self.assertIn("real_sms_sent=0", self.sh)

    def test_offline_contract_is_part_of_readiness_and_ci(self) -> None:
        self.assertIn(POWERSHELL.name, self.readiness)
        self.assertIn(PAYLOAD.name, self.readiness)
        self.assertIn("phase5_recovery_notification_preflight_contract.py", self.ci)
        self.assertIn(f"./scripts/{POWERSHELL.name} -SelfTest", self.ci)
        self.assertIn(f"bash -n scripts/{PAYLOAD.name}", self.ci)

    def test_alertmanager_runbook_keeps_configuration_and_drill_as_separate_gates(self) -> None:
        for marker in (
            "接收渠道决策门禁",
            "离线配置校验门禁",
            "关闭态部署门禁",
            "合成告警演练门禁",
            "真实短信发送数必须为 0",
            "amtool check-config",
            "utf8-strict-mode",
        ):
            self.assertIn(marker, self.alertmanager_runbook)
        self.assertNotIn("SMS_ENABLED=true", self.alertmanager_runbook)
        self.assertNotRegex(self.alertmanager_runbook, r"(?i)(access[_-]?key|secret|token)\s*[:=]\s*[^<\s`]+")


if __name__ == "__main__":
    unittest.main()

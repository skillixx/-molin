#!/usr/bin/env python3
"""验证 024 最小 SSH 连接诊断的固定能力边界。"""

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-ssh-diagnostic-024.py")
CHANGE_ID = "CHG-G8-TEST-READONLY-SSH-DIAGNOSTIC-20260816-024"


def load_module():
    """从固定文件加载候选模块，测试不修改真实 SSH 配置。"""
    specification = importlib.util.spec_from_file_location("g8_ssh_diagnostic_024", SCRIPT)
    if specification is None or specification.loader is None:
        raise RuntimeError("module_unavailable")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


class G8ReadonlySshDiagnostic024Tests(unittest.TestCase):
    """从 CLI、参数和一次性子进程三个公开边界验证候选。"""

    def test_default_and_incomplete_authorization_fail_before_ssh(self):
        """普通入口和缺失冻结绑定都必须在子进程前失败关闭。"""
        for arguments in ([], ["--execute-authorized", "--change-id", CHANGE_ID]):
            completed = subprocess.run(
                [sys.executable, "-I", str(SCRIPT), *arguments],
                capture_output=True,
                text=True,
                encoding="utf-8",
                timeout=15,
            )
            self.assertEqual(2, completed.returncode)
            self.assertIn("reason=remote_not_authorized", completed.stdout)
            self.assertNotIn("SSH_ATTEMPTED=YES", completed.stdout)
            self.assertEqual("", completed.stderr)

    def test_self_test_is_offline_and_feature_head_is_not_an_authorized_merge(self):
        """自检不启动 SSH，普通单父提交不能冒充工程合并对象。"""
        completed = subprocess.run(
            [sys.executable, "-I", str(SCRIPT), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=15,
        )
        self.assertEqual(0, completed.returncode)
        self.assertIn("SELF_TEST=PASS", completed.stdout)
        self.assertNotIn("SSH_ATTEMPTED=YES", completed.stdout)
        self.assertEqual("", completed.stderr)

        module = load_module()
        repository = Path(__file__).resolve().parents[2]
        try:
            feature_head = subprocess.run(
                ["git", "rev-parse", "HEAD"],
                cwd=repository,
                capture_output=True,
                text=True,
                encoding="ascii",
                check=True,
                timeout=15,
            ).stdout.strip()
        except subprocess.CalledProcessError:
            self.skipTest("linked-worktree 的外部 Git 对象库未挂载；普通 CI checkout 必须实算")
        with self.assertRaises(module.RunnerFailure) as raised:
            module.verify_engineering_merge(repository, feature_head)
        self.assertEqual("engineering_material_not_merge", raised.exception.reason)

    def test_fixed_arguments_use_existing_authentication_chain_once(self):
        """固定参数保留默认身份与代理，但隔离用户配置、密码、转发和重试。"""
        module = load_module()
        arguments = module.build_ssh_arguments(Path(r"C:\fixed\known_hosts"))
        rendered = " ".join(arguments)
        for expected in (
            "-F none",
            "-p 10003",
            "BatchMode=yes",
            "ConnectionAttempts=1",
            "ConnectTimeout=15",
            "PasswordAuthentication=no",
            "KbdInteractiveAuthentication=no",
            "StrictHostKeyChecking=yes",
            "GlobalKnownHostsFile=none",
            "KnownHostsCommand=none",
            "VerifyHostKeyDNS=no",
            "HostKeyAlgorithms=ssh-ed25519",
            "ClearAllForwardings=yes",
            "ControlMaster=no",
            "ControlPath=none",
            "ControlPersist=no",
            "RequestTTY=no",
            "UserKnownHostsFile=C:\\fixed\\known_hosts",
            "pc@8.130.9.163",
            "printf 'G8_TEST_READONLY_SSH_DIAGNOSTIC_024_REMOTE=PASS\\n'",
        ):
            self.assertIn(expected, rendered)
        for forbidden in ("-i ", "IdentitiesOnly", "docker", "sudo", "curl", "mysql", "migration"):
            self.assertNotIn(forbidden, rendered)

    def test_user_ssh_config_capabilities_are_ignored(self):
        """恶意用户配置不能增加本地命令、额外信任源、环境发送或连接复用。"""
        module = load_module()
        malicious_config = "\n".join(
            (
                "Match exec \"local-command\"",
                "KnownHostsCommand local-command",
                "SendEnv SECRET_VALUE",
                "SetEnv SECRET_VALUE=hidden",
                "ControlMaster auto",
                "ControlPersist yes",
                "VerifyHostKeyDNS yes",
            )
        )
        self.assertIn("KnownHostsCommand", malicious_config)
        rendered = " ".join(module.build_ssh_arguments(Path(r"C:\fixed\known_hosts")))
        for expected in (
            "-F none",
            "KnownHostsCommand=none",
            "GlobalKnownHostsFile=none",
            "VerifyHostKeyDNS=no",
            "ControlMaster=no",
            "ControlPersist=no",
        ):
            self.assertIn(expected, rendered)

    def test_fake_ssh_is_called_once_and_raw_stderr_is_not_forwarded(self):
        """假 SSH 精确调用一次，失败只返回固定分类而不回显原始错误。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-024-fake-ssh-") as temporary:
            root = Path(temporary)
            marker = root / "calls.txt"
            fake = root / "fake_ssh.py"
            fake.write_text(
                "import json, os, pathlib, sys\n"
                "path = pathlib.Path(os.environ['G8_FAKE_SSH_MARKER'])\n"
                "path.write_text(json.dumps(sys.argv[1:]), encoding='utf-8')\n"
                "sys.stderr.write('Permission denied (publickey). SECRET_PATH=C:/hidden/key')\n"
                "raise SystemExit(255)\n",
                encoding="utf-8",
                newline="\n",
            )
            environment = dict(os.environ)
            environment["G8_FAKE_SSH_MARKER"] = str(marker)
            attempted = []
            result = module.run_ssh_probe(
                [sys.executable, "-I", str(fake)],
                Path(r"C:\fixed\known_hosts"),
                environment=environment,
                timeout=10,
                on_started=lambda: attempted.append("started"),
            )
            self.assertEqual("authentication_failed", result.reason)
            self.assertEqual(255, result.exit_code)
            captured = marker.read_text(encoding="utf-8")
            self.assertIn('"-F", "none"', captured)
            self.assertIn('"GlobalKnownHostsFile=none"', captured)
            self.assertIn('"KnownHostsCommand=none"', captured)
            self.assertIn('"pc@8.130.9.163"', captured)
            self.assertEqual(["started"], attempted)
            self.assertNotIn("SECRET_PATH", result.reason)

    def test_failed_process_creation_does_not_mark_ssh_attempted(self):
        """SSH 客户端进程未创建时不得形成 SSH_ATTEMPTED 标志。"""
        module = load_module()
        attempted = []
        with self.assertRaises(module.RunnerFailure) as raised:
            module.run_ssh_probe(
                [str(Path(tempfile.gettempdir()) / "g8-024-missing-ssh-client.exe")],
                Path(r"C:\fixed\known_hosts"),
                environment=dict(os.environ),
                timeout=1,
                on_started=lambda: attempted.append("started"),
            )
        self.assertEqual("ssh_client_unavailable", raised.exception.reason)
        self.assertEqual([], attempted)

    def test_result_classification_distinguishes_transport_and_remote_probe(self):
        """退出状态和低敏 stderr 模式必须区分主要 SSH 失败阶段。"""
        module = load_module()
        cases = (
            (255, b"", b"Host key verification failed.", "host_key_failed"),
            (255, b"", b"Connection timed out", "connect_timeout"),
            (255, b"", b"Connection refused", "connect_refused"),
            (255, b"", b"No route to host", "network_unreachable"),
            (255, b"", b"unexpected fixed client failure", "transport_failed"),
            (7, b"", b"", "remote_probe_failed"),
            (0, module.REMOTE_MARKER, b"", "pass"),
            (0, b"unexpected", b"", "remote_marker_failed"),
        )
        for exit_code, stdout, stderr, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(expected, module.classify_result(exit_code, stdout, stderr).reason)

    def test_timeout_and_output_limit_fail_closed_without_retry(self):
        """超时与输出泛洪必须终止唯一子进程并返回固定低敏原因。"""
        module = load_module()
        with tempfile.TemporaryDirectory(prefix="g8-024-bounds-") as temporary:
            root = Path(temporary)
            slow = root / "slow.py"
            slow.write_text("import time\ntime.sleep(5)\n", encoding="utf-8", newline="\n")
            timeout_result = module.run_ssh_probe(
                [sys.executable, "-I", str(slow)],
                Path(r"C:\fixed\known_hosts"),
                environment=dict(os.environ),
                timeout=1,
            )
            self.assertEqual("connect_timeout", timeout_result.reason)

            noisy = root / "noisy.py"
            noisy.write_text(
                "import sys\nsys.stderr.buffer.write(b'x' * (64 * 1024 + 1))\nraise SystemExit(255)\n",
                encoding="utf-8",
                newline="\n",
            )
            limit_result = module.run_ssh_probe(
                [sys.executable, "-I", str(noisy)],
                Path(r"C:\fixed\known_hosts"),
                environment=dict(os.environ),
                timeout=10,
            )
            self.assertEqual("output_limit_exceeded", limit_result.reason)


if __name__ == "__main__":
    unittest.main()

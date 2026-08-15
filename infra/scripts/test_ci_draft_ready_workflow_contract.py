import re
import unittest
from pathlib import Path


WORKFLOW_PATH = Path(__file__).parents[2] / ".github/workflows/ci.yml"


class CIDraftReadyWorkflowContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = WORKFLOW_PATH.read_text(encoding="utf-8")

    def job_block(self, job_id: str) -> str:
        """提取固定 job 块，避免只在其他作业中命中同名命令。"""

        match = re.search(
            rf"(?ms)^  {re.escape(job_id)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
            self.workflow,
        )
        self.assertIsNotNone(match, f"缺少 CI job：{job_id}")
        return match.group("body")

    def test_pull_request_events_and_concurrency_are_fixed(self):
        self.assertIn(
            "types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]",
            self.workflow,
        )
        self.assertIn(
            "group: ci-pr-${{ github.event.pull_request.number }}",
            self.workflow,
        )
        self.assertIn("cancel-in-progress: true", self.workflow)

    def test_change_scope_exports_mode_and_draft_targets(self):
        block = self.job_block("change-scope")
        for output_name in (
            "pr_mode",
            "draft_docs",
            "draft_python",
            "draft_backend",
            "draft_frontend_admin",
            "draft_frontend_user",
            "python_tests_json",
            "python_compile_json",
            "go_packages_json",
        ):
            self.assertRegex(block, rf"(?m)^      {output_name}:")
        self.assertIn("--pr-mode=", block)
        self.assertIn("select-ci-draft-tests.py", block)
        self.assertIn("安装 Go 以选择 Draft package", block)
        self.assertIn("steps.scope.outputs.draft_backend == 'true'", block)
        for test_path in (
            "test_classify_ci_change_scope.py",
            "test_select_ci_draft_tests.py",
            "test_run_ci_draft_targets.py",
            "test_ci_draft_ready_workflow_contract.py",
        ):
            self.assertIn(test_path, block)

    def test_draft_jobs_are_draft_only_and_ready_jobs_are_ready_only(self):
        for job_id in (
            "draft-quality",
            "draft-python",
            "draft-backend",
            "draft-frontend-admin",
            "draft-frontend-user",
            "ci-draft-gate",
        ):
            with self.subTest(job_id=job_id):
                self.assertIn(
                    "github.event.pull_request.draft == true",
                    self.job_block(job_id),
                )
        for job_id in (
            "docs-lightweight",
            "backend",
            "phase5-release-safety",
            "gateway-g3",
            "gateway-g4",
            "gateway-g7",
            "gateway-g8",
            "gateway-g8-windows",
            "gateway-g8-real-e2e",
            "frontend-admin",
            "frontend-user",
            "ci-required-gate",
        ):
            with self.subTest(job_id=job_id):
                self.assertIn(
                    "github.event.pull_request.draft == false",
                    self.job_block(job_id),
                )

    def test_draft_frontend_jobs_do_not_build_or_run_browser_e2e(self):
        for job_id in ("draft-frontend-admin", "draft-frontend-user"):
            with self.subTest(job_id=job_id):
                block = self.job_block(job_id)
                self.assertIn("run: npm ci", block)
                self.assertIn("npm run type-check", block)
                self.assertNotIn("npm run build", block)
                self.assertNotIn("playwright", block.lower())

    def test_summary_names_are_distinct_and_fixed(self):
        self.assertIn("name: CI Draft 快速门禁汇总", self.job_block("ci-draft-gate"))
        self.assertIn("name: CI 必选门禁汇总", self.job_block("ci-required-gate"))

    def test_draft_summary_checks_every_selected_job(self):
        block = self.job_block("ci-draft-gate")
        for job_id in (
            "draft-quality",
            "draft-python",
            "draft-backend",
            "draft-frontend-admin",
            "draft-frontend-user",
        ):
            self.assertIn(job_id, block)
        self.assertIn("check_required_gate", block)
        self.assertIn("success", block)
        self.assertIn("DRAFT_DOCS_REQUIRED", block)
        self.assertIn("selected_gate", block)

    def test_ready_summary_still_checks_all_existing_jobs(self):
        block = self.job_block("ci-required-gate")
        for job_id in (
            "docs-lightweight",
            "backend",
            "phase5-release-safety",
            "gateway-g3",
            "gateway-g4",
            "gateway-g7",
            "gateway-g8",
            "gateway-g8-windows",
            "gateway-g8-real-e2e",
            "frontend-admin",
            "frontend-user",
        ):
            self.assertIn(job_id, block)
        self.assertNotIn("ci-draft-gate", block)

    def test_g8_windows_job_covers_015_through_023_tombstones(self):
        """原生 Windows 门禁必须覆盖 015 至 023 墓碑。"""

        block = self.job_block("gateway-g8-windows")
        self.assertIn("runs-on: windows-latest", block)
        self.assertIn("timeout-minutes: 15", block)
        windows_step = block[block.index("- name: 验证 Windows 可信系统目录与失效入口失败关闭") :]
        self.assertIn("timeout-minutes: 12", windows_step.split("run: |", 1)[0])
        self.assertIn("test_diagnose_ai_gateway_g8_local_ssh_materials.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence_013.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence_014.py", block)
        self.assertIn("test_g8_test_readonly_access_install_015.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_015_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_install_015_authorization_contract.py", block)
        self.assertIn("test_g8_test_readonly_access_install_016.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_016_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_install_016_authorization_contract.py", block)
        self.assertIn("test_g8_test_readonly_access_install_017.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_017_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_install_017_authorization_contract.py", block)
        self.assertIn("test_g8_test_readonly_access_install_018.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_018_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_install_018_authorization_contract.py", block)
        self.assertIn("test_g8_test_readonly_access_install_019.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_019_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_install_019_authorization_contract.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_020_authorization_contract.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_021_authorization_contract.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_022_authorization_contract.py", block)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py", block)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_023_authorization_contract.py", block)
        self.assertNotIn("test_g8_test_readonly_access_install_020.py", block)
        self.assertNotIn("test_prepare_ai_gateway_g8_test_readonly_access_020_command.py", block)
        self.assertIn("fetch-depth: 0", block)
        self.assertEqual(block.count("$PSNativeCommandUseErrorActionPreference = $false"), 6)
        self.assertGreater(
            block.index("$PSNativeCommandUseErrorActionPreference = $false"),
            block.index("diagnose-ai-gateway-g8-local-ssh-materials.py --self-test"),
        )
        self.assertIn("$g8ConsumedExit = $LASTEXITCODE", block)
        self.assertIn("$g8Consumed014Exit = $LASTEXITCODE", block)
        self.assertIn("$g8Consumed015Exit = $LASTEXITCODE", block)
        self.assertIn("$g8Consumed016Exit = $LASTEXITCODE", block)
        self.assertIn("$g8Consumed017Exit = $LASTEXITCODE", block)
        self.assertIn("$g8Consumed018Exit = $LASTEXITCODE", block)
        self.assertEqual(block.count("$PSNativeCommandUseErrorActionPreference = $g8PreviousNativeErrorPreference"), 6)
        for failure_message in (
            "Windows 本地材料诊断单测失败",
            "013 墓碑单测失败",
            "014 墓碑单测失败",
            "015 安装器单测失败",
            "015 命令生成器单测失败",
            "015 授权清单契约测试失败",
            "016 安装器单测失败",
            "016 命令生成器单测失败",
            "016 授权清单契约测试失败",
            "017 安装器单测失败",
            "017 命令生成器单测失败",
            "017 授权清单契约测试失败",
            "018 安装器单测失败",
            "018 命令生成器单测失败",
            "018 授权清单契约测试失败",
            "019 安装器单测失败",
            "019 命令生成器单测失败",
            "019 授权清单契约测试失败",
            "020 墓碑生成器单测失败",
            "020 消费授权契约失败",
            "022 命令生成器单测失败",
            "022 固定启动器单测失败",
            "022 授权清单契约失败",
            "023 命令生成器单测失败",
            "023 固定启动器单测失败",
            "023 授权清单契约失败",
            "Windows G8 Python 编译检查失败",
            "Windows 本地材料诊断自检失败",
        ):
            self.assertIn(failure_message, block)
        self.assertIn("reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_015_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_017_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_018_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("exit 0", block)
        self.assertIn("needs.change-scope.outputs.gateway_g8 == 'true'", block)

    def test_g8_ready_job_runs_015_through_023_tombstones_in_host_and_network_none(self):
        """Linux 门禁必须覆盖 015 至 023 墓碑断网回归。"""

        block = self.job_block("gateway-g8")
        self.assertIn("bash -n infra/scripts/g8-test-readonly-access-install-015.sh", block)
        self.assertGreaterEqual(block.count("test_g8_test_readonly_access_install_015.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_access_015_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_install_015_authorization_contract.py"), 2)
        self.assertIn("bash -n infra/scripts/g8-test-readonly-access-install-016.sh", block)
        self.assertGreaterEqual(block.count("test_g8_test_readonly_access_install_016.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_access_016_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_install_016_authorization_contract.py"), 2)
        self.assertIn("bash -n infra/scripts/g8-test-readonly-access-install-017.sh", block)
        self.assertIn("test_g8_test_readonly_access_install_017.py", block)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_access_017_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_install_017_authorization_contract.py"), 2)
        self.assertIn("g8_consumed_017_exit", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_017_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("bash -n infra/scripts/g8-test-readonly-access-install-018.sh", block)
        self.assertGreaterEqual(block.count("test_g8_test_readonly_access_install_018.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_access_018_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_install_018_authorization_contract.py"), 2)
        self.assertIn("g8_consumed_018_exit", block)
        self.assertIn("G8_TEST_READONLY_ACCESS_018_COMMAND=FAILED reason=change_id_consumed", block)
        self.assertIn("bash -n infra/scripts/g8-test-readonly-access-install-019.sh", block)
        self.assertGreaterEqual(block.count("test_g8_test_readonly_access_install_019.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_access_019_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_install_019_authorization_contract.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_runtime_audit_020_authorization_contract.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py"), 2)
        self.assertGreaterEqual(block.count("test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_runtime_audit_021_authorization_contract.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py"), 2)
        self.assertGreaterEqual(block.count("test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_runtime_audit_022_authorization_contract.py"), 2)
        self.assertGreaterEqual(block.count("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py"), 2)
        self.assertGreaterEqual(block.count("test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py"), 2)
        self.assertGreaterEqual(block.count("test_ai_gateway_g8_readonly_runtime_audit_023_authorization_contract.py"), 2)
        self.assertNotIn("g8-test-readonly-access-install-020.sh", block)
        self.assertNotIn("test_g8_test_readonly_access_install_020.py", block)
        self.assertIn("验证 G8 015/016/017/018/019/020/021/022/023 墓碑离线门禁", block)
        digest = "python@sha256:62eafe52c91cad83c2c74e630bfde917da8c253673e695665d454def84fc9a13"
        self.assertIn(f"g8_bookworm_image='{digest}'", block)
        self.assertEqual(block.count('docker pull "$g8_bookworm_image"'), 1)
        self.assertIn("docker run --rm --pull=never --network none", block)
        bookworm_network_none = block.split("docker run --rm --pull=never --network none", 1)[1].split(
            "docker run --rm --pull=never --network none", 1
        )[0]
        self.assertIn('"$g8_bookworm_image"', bookworm_network_none)
        self.assertIn("test_g8_test_readonly_access_install_015.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_015_command.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_install_015_authorization_contract.py", bookworm_network_none)
        self.assertIn("test_g8_test_readonly_access_install_016.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_016_command.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_install_016_authorization_contract.py", bookworm_network_none)
        self.assertIn("test_g8_test_readonly_access_install_017.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_017_command.py", bookworm_network_none)
        self.assertIn("test_g8_test_readonly_access_install_018.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_018_command.py", bookworm_network_none)
        self.assertIn("test_g8_test_readonly_access_install_019.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_access_019_command.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_020_authorization_contract.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py", bookworm_network_none)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_021_authorization_contract.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py", bookworm_network_none)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_022_authorization_contract.py", bookworm_network_none)
        self.assertIn("test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py", bookworm_network_none)
        self.assertIn("test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py", bookworm_network_none)
        self.assertIn("test_ai_gateway_g8_readonly_runtime_audit_023_authorization_contract.py", bookworm_network_none)

    def test_ready_heavy_command_sentinels_are_not_removed(self):
        for sentinel in (
            "go test -v -race -count=1 ./...",
            "go vet ./...",
            "go build -o /dev/null ./cmd/api",
            "verify-ai-gateway-g6-customer.sh",
            "verify-ai-gateway-migration-000062.sh",
            "verify-ai-gateway-g4-governance.sh",
            "verify-ai-gateway-g7-reliability.sh",
            "verify-ai-gateway-g8-real-backend-e2e.sh",
            "verify-sms-phase5-sensitive-data.py",
            "npm run lint",
            "npm run build",
            "npm run test:g6-e2e",
            "npx playwright install --with-deps chrome",
        ):
            with self.subTest(sentinel=sentinel):
                self.assertIn(sentinel, self.workflow)


if __name__ == "__main__":
    unittest.main()

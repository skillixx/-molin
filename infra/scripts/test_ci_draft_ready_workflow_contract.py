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

    def test_g8_windows_job_covers_trusted_paths_and_consumed_entry(self):
        """原生 Windows 门禁必须覆盖可信系统路径和旧 ChangeId 失败关闭。"""

        block = self.job_block("gateway-g8-windows")
        self.assertIn("runs-on: windows-latest", block)
        self.assertIn("test_diagnose_ai_gateway_g8_local_ssh_materials.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence_013.py", block)
        self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence_014.py", block)
        self.assertIn("$PSNativeCommandUseErrorActionPreference = $false", block)
        self.assertIn("reason=change_id_consumed", block)
        self.assertIn("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed", block)
        self.assertIn("needs.change-scope.outputs.gateway_g8 == 'true'", block)

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

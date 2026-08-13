import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("classify-ci-change-scope.py")


def load_classifier():
    """从固定脚本路径加载分类器，测试其公开的路径分类接口。"""

    spec = importlib.util.spec_from_file_location("classify_ci_change_scope", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载 CI 变更范围分类器")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ClassifyCIChangeScopeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.classifier = load_classifier()

    def assert_enabled(self, paths, *expected):
        """断言给定路径只开启预期门禁，防止分类规则静默扩大或缩小。"""

        result = self.classifier.classify_paths(paths)
        enabled = {name for name, value in result.items() if value}
        self.assertEqual(set(expected), enabled)

    def assert_draft_enabled(self, paths, *expected):
        """断言 Draft 只启用与变更直接相关的定向门禁。"""

        result = self.classifier.classify_draft_paths(paths)
        enabled = {name for name, value in result.items() if value}
        self.assertEqual(set(expected), enabled)

    def test_draft_pure_docs_only_selects_docs(self):
        self.assert_draft_enabled(
            ["README.md", "docs/ci-guide.md"],
            "draft_docs",
        )

    def test_draft_python_and_workflow_paths_select_python(self):
        for path in (
            "infra/scripts/check.py",
            "scripts/check.py",
            "tests/test_contract.py",
            ".github/workflows/ci.yml",
        ):
            with self.subTest(path=path):
                self.assert_draft_enabled([path], "draft_python")

    def test_draft_backend_selects_backend(self):
        self.assert_draft_enabled(
            ["server/internal/modules/content/service/article.go"],
            "draft_backend",
        )

    def test_draft_shared_frontend_selects_both_consoles(self):
        self.assert_draft_enabled(
            ["web/shared/src/http/client.ts"],
            "draft_frontend_admin",
            "draft_frontend_user",
        )

    def test_draft_unknown_path_fails_closed_to_all_targeted_gates(self):
        result = self.classifier.classify_draft_paths(["new-build-system.toml"])
        self.assertTrue(all(result.values()))

    def test_ready_outputs_remain_unchanged_when_draft_contract_is_added(self):
        result = self.classifier.classify_paths(
            ["server/internal/modules/content/service/article.go"]
        )
        enabled = {name for name, value in result.items() if value}
        self.assertEqual({"backend"}, enabled)

    def test_github_output_contains_mode_draft_and_ready_contracts(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = Path(temp_dir) / "github-output.txt"
            ready_result = self.classifier.classify_paths(["docs/ci-guide.md"])
            draft_result = self.classifier.classify_draft_paths(["docs/ci-guide.md"])

            self.classifier.write_github_outputs(
                output_path,
                ready_result,
                "draft",
                draft_result,
            )

            values = dict(
                line.split("=", 1)
                for line in output_path.read_text(encoding="utf-8").splitlines()
            )
            self.assertEqual("draft", values["pr_mode"])
            self.assertEqual("true", values["draft_docs"])
            self.assertEqual("false", values["draft_backend"])
            self.assertEqual("true", values["docs_lightweight"])

    def test_cli_rejects_unknown_pr_mode(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            completed = subprocess.run(
                [
                    "python",
                    str(SCRIPT_PATH),
                    "--base-sha",
                    "0" * 40,
                    "--head-sha",
                    "1" * 40,
                    "--github-output",
                    str(Path(temp_dir) / "github-output.txt"),
                    "--pr-mode",
                    "invalid",
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(2, completed.returncode)

    def test_pure_docs_only_runs_lightweight_gate(self):
        self.assert_enabled(
            ["README.md", "docs/ai-gateway-g8-development.md"],
            "docs_lightweight",
        )

    def test_admin_change_only_runs_admin_gate(self):
        self.assert_enabled(
            ["web/admin-console/src/views/DashboardView.vue"],
            "frontend_admin",
        )

    def test_shared_frontend_change_runs_both_frontend_gates(self):
        self.assert_enabled(
            ["web/shared/src/http/client.ts"],
            "frontend_admin",
            "frontend_user",
            "phase5",
            "gateway_g8_real_e2e",
        )

    def test_general_backend_change_only_runs_backend_gate(self):
        self.assert_enabled(
            ["server/internal/modules/content/service/article_service.go"],
            "backend",
        )

    def test_sms_change_runs_backend_and_phase5_gates(self):
        result = self.classifier.classify_paths(
            ["server/internal/modules/auth/service/sms_service.go"]
        )
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_sms_module_change_runs_backend_and_phase5_gates(self):
        self.assert_enabled(
            ["server/internal/modules/sms/service/dispatcher.go"],
            "backend",
            "phase5",
        )

    def test_gateway_change_runs_backend_and_all_gateway_gates(self):
        self.assert_enabled(
            ["server/internal/modules/token_gateway/service/forward_service.go"],
            "backend",
            "gateway_g3",
            "gateway_g4",
            "gateway_g7",
            "gateway_g8",
            "gateway_g8_real_e2e",
        )

    def test_billing_change_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(
            ["server/internal/modules/billing/service/wallet_service.go"]
        )
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_migration_change_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(
            ["server/migrations/000099_sensitive.up.sql"]
        )
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_audit_change_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(
            ["server/internal/modules/audit/service/audit_service.go"]
        )
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_security_submodule_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(
            ["server/internal/modules/workbench/security/policy.go"]
        )
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_seed_admin_command_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(["server/cmd/seed-admin/main.go"])
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_shared_security_package_fails_closed_to_full_ci(self):
        for path in (
            "server/pkg/crypto/encrypt.go",
            "server/pkg/jwt/jwt.go",
            "server/pkg/httputil/ip.go",
        ):
            with self.subTest(path=path):
                result = self.classifier.classify_paths([path])
                self.assertTrue(result["full"])
                self.assertTrue(all(result.values()))

    def test_shared_server_package_fails_closed_to_full_ci(self):
        for path in (
            "server/pkg/db/db.go",
            "server/pkg/cache/redis.go",
            "server/pkg/response/response.go",
        ):
            with self.subTest(path=path):
                result = self.classifier.classify_paths([path])
                self.assertTrue(result["full"])
                self.assertTrue(all(result.values()))

    def test_global_http_runtime_fails_closed_to_full_ci(self):
        for path in (
            "server/internal/httpserver/server.go",
            "server/internal/router/router.go",
        ):
            with self.subTest(path=path):
                result = self.classifier.classify_paths([path])
                self.assertTrue(result["full"])
                self.assertTrue(all(result.values()))

    def test_gateway_reconcile_command_runs_all_gateway_gates(self):
        self.assert_enabled(
            ["server/cmd/ai-gateway-reconcile/main.go"],
            "backend",
            "gateway_g3",
            "gateway_g4",
            "gateway_g7",
            "gateway_g8",
            "gateway_g8_real_e2e",
        )

    def test_gateway_asset_and_provision_changes_run_all_gateway_gates(self):
        for path in (
            "server/internal/modules/asset/service/entitlement_reserve.go",
            "server/internal/modules/provision/handler/token_provisioner.go",
            "server/internal/modules/workbench/service/chat_service.go",
            "server/internal/modules/conversation/service/conversation_service.go",
        ):
            with self.subTest(path=path):
                self.assert_enabled(
                    [path],
                    "backend",
                    "gateway_g3",
                    "gateway_g4",
                    "gateway_g7",
                    "gateway_g8",
                    "gateway_g8_real_e2e",
                )

    def test_gateway_frontend_change_runs_frontend_and_real_e2e(self):
        self.assert_enabled(
            ["web/user-console/src/views/ai/AIModelsView.vue"],
            "frontend_user",
            "gateway_g8_real_e2e",
        )

    def test_shared_text_execution_frontend_runs_gateway_e2e(self):
        for path in (
            "web/user-console/src/views/agent/AgentChatView.vue",
            "web/user-console/src/api/conversation.ts",
            "web/user-console/src/types/conversation.ts",
        ):
            with self.subTest(path=path):
                self.assert_enabled(
                    [path],
                    "frontend_user",
                    "gateway_g8_real_e2e",
                )

    def test_gateway_playwright_config_runs_frontend_and_real_e2e(self):
        for console in ("admin-console", "user-console"):
            with self.subTest(console=console):
                result = self.classifier.classify_paths(
                    [f"web/{console}/playwright.g8-real.config.ts"]
                )
                self.assertTrue(result["gateway_g8_real_e2e"])
                self.assertTrue(result["frontend_admin" if console == "admin-console" else "frontend_user"])

    def test_frontend_router_change_runs_cross_domain_browser_gates(self):
        self.assert_enabled(
            ["web/user-console/src/router/index.ts"],
            "frontend_user",
            "phase5",
            "gateway_g8_real_e2e",
        )

    def test_frontend_http_and_auth_foundation_runs_cross_domain_gates(self):
        for path, frontend_gate in (
            ("web/admin-console/src/api/http.ts", "frontend_admin"),
            ("web/admin-console/src/stores/token-storage.ts", "frontend_admin"),
            ("web/user-console/src/api/http.ts", "frontend_user"),
            ("web/user-console/src/stores/auth.ts", "frontend_user"),
        ):
            with self.subTest(path=path):
                self.assert_enabled(
                    [path],
                    frontend_gate,
                    "phase5",
                    "gateway_g8_real_e2e",
                )

    def test_sms_frontend_change_runs_phase5_gate(self):
        self.assert_enabled(
            ["web/admin-console/src/views/sms/SmsManagementView.vue"],
            "frontend_admin",
            "phase5",
        )

    def test_admin_mfa_change_runs_phase5_gate(self):
        self.assert_enabled(
            ["web/admin-console/src/views/auth/AdminVerifyView.vue"],
            "frontend_admin",
            "phase5",
        )

    def test_infrastructure_change_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(["infra/nginx/api.conf"])
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_workflow_change_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths([".github/workflows/ci.yml"])
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_unknown_root_path_fails_closed_to_full_ci(self):
        result = self.classifier.classify_paths(["new-build-system.toml"])
        self.assertTrue(result["full"])
        self.assertTrue(all(result.values()))

    def test_mixed_docs_and_code_is_not_treated_as_pure_docs(self):
        self.assert_enabled(
            ["docs/guide.md", "server/internal/modules/content/service/article_service.go"],
            "backend",
        )

    def test_empty_path_list_is_rejected(self):
        with self.assertRaises(self.classifier.ClassificationError):
            self.classifier.classify_paths([])

    def test_changed_paths_includes_deleted_files(self):
        """删除关键文件也必须进入分类，不能因文件已不存在而漏掉回归。"""

        with tempfile.TemporaryDirectory() as temp_dir:
            repo = Path(temp_dir)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.email", "ci-test@example.invalid"],
                check=True,
            )
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.name", "CI Test"],
                check=True,
            )
            target = repo / "server/internal/modules/token_gateway/deleted.go"
            target.parent.mkdir(parents=True)
            target.write_text("package token_gateway\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "base"], check=True)
            base_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()
            target.unlink()
            subprocess.run(["git", "-C", str(repo), "add", "-u"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "delete"], check=True)
            head_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()

            self.assertEqual(
                ["server/internal/modules/token_gateway/deleted.go"],
                self.classifier.changed_paths(repo, base_sha, head_sha),
            )

    def test_changed_paths_includes_both_sides_of_rename(self):
        """重命名必须同时分类旧路径和新路径，防止移入 docs 后绕过原门禁。"""

        with tempfile.TemporaryDirectory() as temp_dir:
            repo = Path(temp_dir)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.email", "ci-test@example.invalid"],
                check=True,
            )
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.name", "CI Test"],
                check=True,
            )
            source = repo / ".github/workflows/critical.yml"
            source.parent.mkdir(parents=True)
            source.write_text("name: critical\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "base"], check=True)
            base_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()
            destination = repo / "docs/critical.md"
            destination.parent.mkdir()
            source.rename(destination)
            subprocess.run(["git", "-C", str(repo), "add", "-A"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "rename"], check=True)
            head_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()

            paths = self.classifier.changed_paths(repo, base_sha, head_sha)
            self.assertEqual(
                [".github/workflows/critical.yml", "docs/critical.md"],
                paths,
            )
            result = self.classifier.classify_paths(paths)
            self.assertTrue(result["full"])
            self.assertTrue(all(result.values()))

    def test_changed_paths_includes_both_sides_of_copy(self):
        """复制必须同时分类源路径和目标路径，保留原高风险来源门禁。"""

        with tempfile.TemporaryDirectory() as temp_dir:
            repo = Path(temp_dir)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.email", "ci-test@example.invalid"],
                check=True,
            )
            subprocess.run(
                ["git", "-C", str(repo), "config", "user.name", "CI Test"],
                check=True,
            )
            source = repo / ".github/workflows/critical.yml"
            source.parent.mkdir(parents=True)
            source.write_text("name: critical\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "base"], check=True)
            base_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()
            destination = repo / "docs/critical.md"
            destination.parent.mkdir()
            destination.write_text(source.read_text(encoding="utf-8"), encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "copy"], check=True)
            head_sha = subprocess.check_output(
                ["git", "-C", str(repo), "rev-parse", "HEAD"], text=True
            ).strip()

            self.assertEqual(
                [".github/workflows/critical.yml", "docs/critical.md"],
                self.classifier.changed_paths(repo, base_sha, head_sha),
            )


if __name__ == "__main__":
    unittest.main()

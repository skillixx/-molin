#!/usr/bin/env python3
"""验证扫描分类和输出脱敏，不访问仓库外部资源。"""

from __future__ import annotations

import contextlib
import io
import pathlib
import tempfile
import unittest

import sensitive_scan


class SensitiveScanSelfTest(unittest.TestCase):
    def run_scan(self, files: dict[str, str]) -> tuple[int, str]:
        with tempfile.TemporaryDirectory() as raw_dir:
            root = pathlib.Path(raw_dir)
            for name, content in files.items():
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content, encoding="utf-8")
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                code = sensitive_scan.main([str(root), "--repo-root", str(root)])
            return code, output.getvalue()

    def test_real_values_fail_without_echoing_values(self) -> None:
        email = "security" + "@" + "corp.example.cn"
        token = "eyJ" + "a" * 12 + "." + "b" * 12 + "." + "c" * 12
        access_key = "LTAI" + "7" * 16
        refresh_token = "refresh" + "8" * 24
        private_key = "-----BEGIN " + "PRIVATE KEY-----"
        artifact = f"email={email}\ntoken={token}\naccess_key_id={access_key}\nrefresh_token={refresh_token}\n{private_key}\n"
        code, output = self.run_scan({"artifact.log": artifact})
        self.assertEqual(1, code)
        self.assertIn("category=unmasked_email_artifact", output)
        self.assertIn("category=jwt_value", output)
        self.assertIn("category=access_key_or_secret_value", output)
        self.assertIn("category=refresh_token_value", output)
        self.assertIn("category=private_key", output)
        self.assertNotIn(email, output)
        self.assertNotIn(token, output)
        self.assertNotIn(access_key, output)
        self.assertNotIn(refresh_token, output)
        self.assertNotIn(private_key, output)

    def test_document_terms_and_placeholders_are_info(self) -> None:
        placeholder = "qa" + "@" + "example.invalid"
        code, output = self.run_scan({"README.md": f"TemplateData 仅用于说明。\n{placeholder}\n"})
        self.assertEqual(0, code)
        self.assertIn("category=document_literal", output)
        self.assertIn("category=placeholder_email", output)
        self.assertNotIn(placeholder, output)

    def test_debug_response_and_provider_body_log_fail(self) -> None:
        code, output = self.run_scan({"service.go": '// verification debug response\npayload := map[string]any{"code": code}\nlog.Printf("provider body=%s", raw_body)\n'})
        self.assertEqual(0, code)
        self.assertIn("category=debug_code_response_surface", output)
        self.assertIn("category=provider_raw_or_body_log", output)
        self.assertNotIn("payload :=", output)
        self.assertNotIn("raw_body", output)

    def test_protected_environment_file_is_not_read(self) -> None:
        secret = "secret" + "@" + "corp.example.cn"
        code, output = self.run_scan({"infra/.env.test": secret})
        self.assertEqual(0, code)
        self.assertIn("category=protected_env", output)
        self.assertNotIn(secret, output)


if __name__ == "__main__":
    unittest.main(verbosity=2)

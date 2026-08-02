#!/usr/bin/env python3
"""按固定阶段定位 Phase 4 扫描契约在 Linux 上的离线失败位置。"""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import re
import subprocess
import sys
import tempfile
from collections.abc import Callable

sys.dont_write_bytecode = True

import phase4_runtime_sensitive_scan_contract as contract


STAGE_NAMES = (
    "static_contract",
    "fixture_pass_normal",
    "fixture_pass_optimized",
    "attack_cases",
    "internal_output_gate",
    "frontend_dist_compatibility",
    "platform_root_exchange",
    "platform_frontend_before_symlink",
    "platform_frontend_after_alternative",
    "platform_assets_before_symlink",
    "platform_assets_after_alternative",
)
FAILURE_CLASSIFICATIONS = {
    "contract_failure",
    "os_error",
    "subprocess_error",
    "unexpected_output",
    "internal_error",
    "platform_not_supported",
    "argument_contract",
}
SUMMARY_RE = re.compile(
    r"\Astatus=(?:pass|failed) mode=(?:linux_diagnostic|selftest) "
    r"(?:stages=11 stage=complete classification=none|"
    r"stage=[a-z_]+ classification=[a-z_]+|cases=4 classification=none) "
    r"external_access=false persistent_writes=false\Z"
)


def fixture_pass(optimized: bool) -> None:
    """在自动回收的临时目录中验证一个基础证据包。"""
    with tempfile.TemporaryDirectory() as raw:
        manifest = contract.build_bundle(pathlib.Path(raw))
        contract.require_fixed_result(contract.run_scanner(manifest, optimized=optimized), True)


def classify_failure(error: BaseException) -> str:
    """只按异常类型映射固定分类，绝不输出异常正文。"""
    if isinstance(error, contract.ContractFailure):
        return "contract_failure"
    if isinstance(error, OSError):
        return "os_error"
    if isinstance(error, subprocess.SubprocessError):
        return "subprocess_error"
    return "internal_error"


def emit_failure(stage: str, classification: str) -> int:
    """输出单行白名单失败摘要。"""
    safe_stage = stage if stage in {*STAGE_NAMES, "argument_gate", "platform_gate"} else "argument_gate"
    safe_classification = classification if classification in FAILURE_CLASSIFICATIONS else "internal_error"
    sys.stdout.write(
        f"status=failed mode=linux_diagnostic stage={safe_stage} "
        f"classification={safe_classification} external_access=false persistent_writes=false\n"
    )
    return 1


def run_diagnostic() -> int:
    """依次执行固定阶段，并封闭所有阶段内部输出。"""
    if os.name != "posix":
        return emit_failure("platform_gate", "platform_not_supported")
    stages: tuple[tuple[str, Callable[[], object]], ...] = (
        ("static_contract", contract.static_contract),
        ("fixture_pass_normal", lambda: fixture_pass(False)),
        ("fixture_pass_optimized", lambda: fixture_pass(True)),
        ("attack_cases", contract.run_attack_cases),
        ("internal_output_gate", contract.test_internal_output_gate),
        ("frontend_dist_compatibility", contract.test_frontend_dist_compatibility),
        ("platform_root_exchange", contract.test_platform_root_exchange),
        (
            "platform_frontend_before_symlink",
            lambda: contract.test_platform_intermediate_exchange("frontend", "before", "symlink"),
        ),
        (
            "platform_frontend_after_alternative",
            lambda: contract.test_platform_intermediate_exchange("frontend", "after", "alternative"),
        ),
        (
            "platform_assets_before_symlink",
            lambda: contract.test_platform_intermediate_exchange("assets", "before", "symlink"),
        ),
        (
            "platform_assets_after_alternative",
            lambda: contract.test_platform_intermediate_exchange("assets", "after", "alternative"),
        ),
    )
    contract.require(tuple(name for name, _callback in stages) == STAGE_NAMES, "diagnostic_stage_contract")
    for stage, callback in stages:
        captured_stdout = io.StringIO()
        captured_stderr = io.StringIO()
        try:
            with contextlib.redirect_stdout(captured_stdout), contextlib.redirect_stderr(captured_stderr):
                callback()
        except BaseException as error:
            return emit_failure(stage, classify_failure(error))
        if captured_stdout.getvalue() or captured_stderr.getvalue():
            return emit_failure(stage, "unexpected_output")
    sys.stdout.write(
        "status=pass mode=linux_diagnostic stages=11 stage=complete classification=none "
        "external_access=false persistent_writes=false\n"
    )
    return 0


def self_test() -> int:
    """仅验证固定摘要集合，不执行任何契约阶段。"""
    samples = (
        "status=pass mode=linux_diagnostic stages=11 stage=complete classification=none external_access=false persistent_writes=false",
        "status=failed mode=linux_diagnostic stage=platform_root_exchange classification=contract_failure external_access=false persistent_writes=false",
        "status=failed mode=linux_diagnostic stage=platform_gate classification=platform_not_supported external_access=false persistent_writes=false",
        "status=pass mode=selftest cases=4 classification=none external_access=false persistent_writes=false",
    )
    if not all(SUMMARY_RE.fullmatch(sample) is not None for sample in samples):
        return emit_failure("argument_gate", "internal_error")
    sys.stdout.write(
        "status=pass mode=selftest cases=4 classification=none "
        "external_access=false persistent_writes=false\n"
    )
    return 0


def main(argv: list[str] | None = None) -> int:
    """只接受无参数诊断或显式自检。"""
    values = list(sys.argv[1:] if argv is None else argv)
    if values == ["--self-test"]:
        return self_test()
    if values:
        return emit_failure("argument_gate", "argument_contract")
    return run_diagnostic()


if __name__ == "__main__":
    raise SystemExit(main())

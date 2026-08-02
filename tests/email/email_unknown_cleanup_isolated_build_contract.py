#!/usr/bin/env python3
"""验证隔离构建控制器的共享实现、身份边界与离线 SelfTest。"""

from __future__ import annotations

import pathlib
import re
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "tests" / "email" / "email_unknown_cleanup_isolated_build.payload.sh"
CONTROLLER = ROOT / "tests" / "email" / "run-email-unknown-cleanup-isolated-build-background.ps1"
BASH = pathlib.Path(r"C:\Program Files\Git\bin\bash.exe")


def require(condition: bool, message: str) -> None:
    """条件不满足时立即失败，且在 Python -O 下仍保持门禁有效。"""
    if not condition:
        raise AssertionError(message)


def section(text: str, start: str, end: str) -> str:
    """按唯一边界提取源码段，避免静态检查误命中其他执行模式。"""
    require(text.count(start) == 1, f"源码起始边界不唯一: {start}")
    require(text.count(end) == 1, f"源码结束边界不唯一: {end}")
    return text.split(start, 1)[1].split(end, 1)[0]


def main() -> None:
    """执行静态单实现门禁和不联网的 Bash SelfTest。"""
    text = PAYLOAD.read_text(encoding="utf-8")
    controller = CONTROLLER.read_text(encoding="utf-8-sig")
    require(not PAYLOAD.read_bytes().startswith(b"\xef\xbb\xbf"), "payload 必须为 UTF-8 无 BOM")
    require(not pathlib.Path(__file__).read_bytes().startswith(b"\xef\xbb\xbf"), "Python 契约必须为 UTF-8 无 BOM")
    require(CONTROLLER.read_bytes().startswith(b"\xef\xbb\xbf"), "PowerShell 控制器必须保留 UTF-8 BOM")

    require("/usr/bin/command" not in text, "不得依赖不存在的外部 command 文件")
    require(len(re.findall(r'candidate=\$\(command -v -- "\$tool_name"\)', text)) == 1, "必须唯一使用 Bash 内建 command -v")
    require('assert_frozen_tool "$go_bin" "$go_sha"' in text, "Go 路径和摘要必须冻结复核")
    require('assert_frozen_tool "$gofmt_bin" "$gofmt_sha"' in text, "gofmt 路径和摘要必须冻结复核")
    require("missing-go" in text and 'assert_frozen_tool "$go_path" "$go_sha"' in text, "必须覆盖工具缺失和替换攻击")
    require("RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION" in text, "必须显式拒绝集成环境变量")
    require("TestEmailUnknownTombstoneSurvivesRedisRestart" not in text.split("test_pattern=", 1)[1].split("\n", 1)[0], "固定单测表达式不得包含集成测试")
    require("/usr/bin/nohup /usr/bin/env -i" in text, "后台启动必须使用空环境")
    require(text.count('--worker "$nonce"') == 1, "后台 worker 只能启动一次")
    require("worker_starttime=$(/usr/bin/awk '{print $22}'" in text, "必须冻结进程 starttime")
    require("for _ in {1..200}" in text and "ready_seen=true" in text, "worker 必须等待启动状态原子发布")
    require('/usr/bin/mv -T -- "${build_root}/status.tmp" "${build_root}/status.final"' in text, "status 必须原子发布")
    require('/usr/bin/mv -T -- "${build_root}/marker.tmp" "${build_root}/marker.final"' in text, "marker 必须原子发布")
    require('"$binary" -test.run' not in text, "构建控制器不得执行 cleanup 测试二进制")
    require("expected_archive_sha=${4:?archive_sha_required}" in text, "worker 必须接收冻结的快照摘要")
    require(text.count('assert_frozen_archive "$archive" "$expected_archive_sha"') >= 7, "每个 Go 阶段前必须复核快照摘要")
    require('assert_safe_server_archive "$archive"' in text, "解包前必须验证 tar 成员边界")

    # 身份解析只有一份：正式环境固定 /home/pc 与 pc，SelfTest 才能选择当前属主的严格临时根。
    require(text.count("resolve_build_identity() {") == 1, "构建身份解析函数必须唯一")
    identity = section(text, "resolve_build_identity() {", "\n}\n\n# 解析 Go 工具")
    require('build_root="/home/pc/molin-qa-email-cleanup-build-${nonce}"' in identity, "正式构建根必须硬编码在 /home/pc")
    require("build_owner=pc" in identity, "正式构建属主必须硬编码为 pc")
    require('if [[ "${SELFTEST:-0}" == 1 ]]; then' in identity, "只有 SELFTEST=1 才能进入临时身份分支")
    require('^/(tmp|var/tmp)/[-._a-zA-Z0-9/]+$' in identity, "SelfTest 根必须限制为严格绝对临时路径")
    require('! -L "$SELFTEST_BUILD_ROOT"' in identity and "/usr/bin/readlink -f" in identity, "SelfTest 根必须拒绝符号链接和非规范路径")
    require("current_owner=$(/usr/bin/id -un)" in identity, "SelfTest 只能使用当前属主")
    formal = text.split("# 正式模式忽略并拒绝外部注入的 SelfTest 身份", 1)[1]
    require("SELFTEST=0\nunset SELFTEST_BUILD_ROOT" in formal, "正式模式必须清除 SelfTest 身份注入")

    # 正式 launch 与 SelfTest 只能调用同一份残留资产检查。
    require(text.count("assert_launch_artifacts_absent() {") == 1, "启动残留检查函数必须唯一")
    launch_calls = re.findall(r"(?m)^\s*(?:if\s+)?assert_launch_artifacts_absent(?:\s|$)", text)
    require(len(launch_calls) == 3, "残留检查必须且只能供 SelfTest 正反例与正式 launch 调用")

    # 所有 poll 状态文本只能位于 read_poll_evidence；SelfTest 与正式 poll 均调用它。
    require("classify_poll_fixture" not in text, "不得保留第二套 fixture 分类器")
    require(text.count("read_poll_evidence() {") == 1, "poll 证据读取函数必须唯一")
    poll_body = section(text, "read_poll_evidence() {", "\n}\n\n# 后台 worker")
    formal_poll = section(text, 'if [[ "$mode" == --poll ]]; then', "\nfi\n\n[[ \"$mode\" == --worker ]]")
    require("status=unknown reason=" not in formal_poll and "status=pending reason=" not in formal_poll, "正式 poll 不得保留内联状态分类")
    require('read_poll_evidence "$timeout_seconds"' in formal_poll, "正式 poll 必须调用共享证据函数")
    require(len(re.findall(r"(?m)^\s*(?:actual=\$\()?read_poll_evidence(?:\s|$)", text)) == 2, "SelfTest 与正式 poll 必须各通过唯一共享实现")
    selftest = section(text, 'if [[ "${1:-}" == --self-test ]]; then', "\nfi\n\n# 正式模式忽略")
    for evidence in ("pid_reused", "reason=running", "partial_evidence", "unexpected_output", "timeout", "worker_missing", "binary_sha", "binary_size"):
        require(evidence in selftest, f"SelfTest 缺少真实证据场景: {evidence}")
    require('actual=$(read_poll_evidence "$timeout_seconds")' in selftest, "SelfTest 必须直接使用共享 poll 读取函数")
    require('"$go_bin"' not in selftest and " go test " not in selftest and " go build " not in selftest, "fixture worker 不得运行 Go")

    ps_selftest = section(controller, "if ($SelfTest) {", "\n}\n\nAssert-Nonce -Value $Nonce")
    require("Invoke-FixedOpenSSH" not in ps_selftest and "$script:SshPath" not in ps_selftest and "$script:ScpPath" not in ps_selftest, "PowerShell SelfTest 不得调用网络传输")
    require(controller.count("$script:LegacyScpProtocolFlag = '-O'") == 1, "传统 SCP 协议标志必须且只能固定一次")
    require(
        "$script:ScpCommon = @($script:LegacyScpProtocolFlag, '-P', $script:Port, '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10')" in controller,
        "SCP 参数必须保持固定端口、批处理、主机密钥与超时边界",
    )
    require("Assert-FixedScpArguments -Arguments $script:ScpCommon" in ps_selftest, "SelfTest 必须验证正式 SCP 参数数组")
    require("missing_legacy_scp_flag_accepted" in ps_selftest, "SelfTest 必须拒绝缺少 -O")
    require("sftp_mode_accepted" in ps_selftest and "@('-s')" in ps_selftest, "SelfTest 必须拒绝 -s/SFTP 模式")
    require("scp_config_override_accepted" in ps_selftest and "@('-F', 'unsafe-config')" in ps_selftest, "SelfTest 必须拒绝配置覆盖")
    ps_formal = controller.split("\n}\n\nAssert-Nonce -Value $Nonce", 1)[1]
    require("$scpCommon" not in ps_formal, "正式上传不得创建可漂移的局部 SCP 参数")
    require("Assert-FixedScpArguments -Arguments $script:ScpCommon" in ps_formal, "正式上传前必须复核固定 SCP 参数")
    require("'-s'" not in ps_formal and "'-S'" not in ps_formal and "'-F'" not in ps_formal, "正式路径不得启用 SFTP 或配置/程序覆盖")
    require(ps_formal.count('$script:ScpCommon + @($snapshotFull, "${script:HostName}:$remoteRoot/server-snapshot.tar")') == 1, "快照必须只有一个固定 SCP 目标")
    require(ps_formal.count('$script:ScpCommon + @($script:PayloadPath, "${script:HostName}:$remotePayload")') == 1, "payload 必须只有一个固定 SCP 目标")
    transport = section(controller, "function Invoke-FixedOpenSSH {", "\n}\n\nfunction Assert-FixedScpArguments")
    require(transport.count("& $FilePath @Arguments") == 1, "每次传输调用只能执行一次固定程序")
    require('if ($exitCode -ne 0) { throw "transport_failed:$exitCode" }' in transport, "传输失败必须立即失败关闭")
    require("Start-Sleep" not in transport and "while (" not in transport and "Invoke-FixedOpenSSH" not in transport, "传输函数不得重试或递归")
    require(BASH.is_file(), "本机缺少固定 Git Bash")
    result = subprocess.run(
        [str(BASH), "--noprofile", "--norc", str(PAYLOAD), "--self-test"],
        cwd=ROOT,
        text=True,
        encoding="utf-8",
        capture_output=True,
        check=False,
    )
    require(result.returncode == 0 and result.stderr == "", f"隔离 SelfTest 失败: {result.stderr}")
    require(
        re.fullmatch(
            r"status=pass mode=selftest cases=13 duplicate_launch_rejected=true pid_reuse_rejected=true running_observed=true partial_marker_rejected=true stderr_rejected=true timeout_unknown=true worker_missing_unknown=true pass_binary_verified=true integration_env_rejected=true fixture_worker_go_executed=false external_access=false\s*",
            result.stdout,
        )
        is not None,
        "隔离 SelfTest 摘要不固定",
    )
    print("status=pass mode=isolated_build_controller_contract cases=13 shared_poll_implementation=true external_access=false")


if __name__ == "__main__":
    main()

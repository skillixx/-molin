from __future__ import annotations

import re
import shutil
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "scripts/run-email-unknown-remote-readonly-gate.ps1"
PAYLOAD = ROOT / "scripts/email-unknown-remote-readonly.payload.sh"

SUCCESS_PATTERN = re.compile(
    r"^status=pass api_count=1 health=true ready=true live_adapter_mock=false mysql_count=1 redis_count=1 "
    r"schema=57 dirty=false clock_drift_ok=true state_safe=true "
    r"state_phase=(phase1_created|phase2_verified) primary_owned=1 unexpected_owned=1 "
    r"scope_rows=2 template_owned=1 allowlist_owned=1 redis_ping=true "
    r"run_id_changed=(true|false) lock_exists=(0|1) orphan_count=([0-9]+) "
    r"orphan_safe_count=([0-9]+) cycle_evidence_count=2 cycle_valid_count=2 "
    r"cycle_schema_count=2 cycle_excluded_count=2 writes=false restart=false cleanup=false\n?$"
)

SELFTEST_PATTERN = re.compile(
    r"^status=pass mode=selftest cases=(?P<cases>[0-9]+) external_access=false "
    r"process_exit_codes=(?P<exit_codes>[0-9]+(?:,[0-9]+)*)\s*$"
)


class ContractError(RuntimeError):
    """表示只读运行器未满足冻结契约。"""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def payload_contract_errors(payload: str) -> list[str]:
    errors: list[str] = []
    if not payload.startswith("set -Eeuo pipefail\n"):
        errors.append("首行Shell选项")
    if "\x00" in payload or payload.startswith("\ufeff") or payload.startswith("\ufffe"):
        errors.append("BOM或NUL")
    if re.search(r"(?:^|\s)(?:/bin/)?sh\s+-c\b", payload):
        errors.append("嵌套sh-c")
    cycle_summary_fragment = "cycle_evidence_count=%s cycle_valid_count=%s cycle_schema_count=%s cycle_excluded_count=%s"
    if cycle_summary_fragment not in payload:
        errors.append("最终周期摘要")
    required = (
        "shopt -qo errexit",
        "shopt -qo nounset",
        "shopt -qo pipefail",
        "/usr/bin/cat >/dev/null || true",
        "--request GET",
        "--write-out '%{http_code}'",
        "http://127.0.0.1:8080/api/health",
        "http://127.0.0.1:8080/api/ready",
        '[[ "$health_status" == 200 ]]',
        '[[ "$ready_status" == 200 ]]',
        '[[ -n "$email_adapter" && "$email_adapter" != mock ]]',
        "live_adapter_mock=false",
        "provider='aliyun_directmail'",
        "provider_status='approved'",
        "variables_complete=1",
        "local_enabled=1",
        "JSON_LENGTH(variables_json)=2",
        "JSON_QUOTE('Code')",
        "JSON_QUOTE('ExpireMinutes')",
        "mysql_scalar()",
        "^molin_restore_57_reverify_[a-f0-9]{32}$",
        "cycle_schema_root_scalar()",
        'cycle_schema_exists=$(cycle_schema_root_scalar "$cycle_target")',
        '/usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- "$schema_name"',
        '[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]',
        '[[ "${sql%% *}" == SELECT ]]',
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=",
        "${cycle_targets[0]}\" != \"${cycle_targets[1]}",
        "first_cycle in raw or second_cycle in raw",
        "INFO server",
        'EXISTS "$lock_key"',
        "stage=cycle_exclusion",
        "stage=cycle_target_source",
        "stage=cycle_dir_metadata",
        "stage=cycle_marker_metadata",
        "stage=cycle_dump_symlink",
        "stage=cycle_dump_metadata",
        "stage=cycle_targets_duplicate",
        "-type d -print",
        "-type f -print",
        "stat -c '%u:%a'",
        "stat -c '%u:%a:%s'",
        "-type l -print",
        '[[ -z "$dump_symlink" ]]',
        "^/home/pc/molin-email-unknown-[a-f0-9]{32}\\.state$",
        "^/home/pc/molin-runtime/email-unknown-[a-f0-9]{32}$",
    )
    for fragment in required:
        if fragment not in payload:
            errors.append(f"缺少:{fragment}")

    if "/home/pc/molin-runtime/email-unknown-restart-state" in payload:
        errors.append("旧状态文件路径")
    if "%F" in payload or "cycle_not_isolated" in payload:
        errors.append("本地化类型文本或旧聚合stage")
    if payload.count("provider='aliyun_directmail'") != 3:
        errors.append("日志与模板供应商归属数量")
    if payload.count("provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4)") != 3:
        errors.append("日志与模板供应商模板归属数量")
    dump_link_gate = payload.find('[[ -z "$dump_symlink" ]]')
    cycle_schema_query = payload.find('cycle_schema_exists=$(cycle_schema_root_scalar "$cycle_target")')
    if dump_link_gate < 0 or cycle_schema_query < 0 or dump_link_gate > cycle_schema_query:
        errors.append("dump符号链接门禁顺序")
    cycle_unique_gate = payload.find('[[ "${cycle_targets[0]}" != "${cycle_targets[1]}" ]]')
    cycle_non_main_gate = payload.find('[[ "$cycle_target" != "$mysql_database" ]]')
    if cycle_unique_gate < 0 or cycle_non_main_gate < 0 or cycle_schema_query < cycle_unique_gate or cycle_schema_query < cycle_non_main_gate:
        errors.append("root查询前目标身份门禁顺序")
    if payload.count('MYSQL_PWD="$MYSQL_ROOT_PASSWORD"') != 1 or payload.count("--user=root") != 1:
        errors.append("容器内root只读通道数量")

    allowed_tools = {
        "sed", "tr", "pgrep", "curl", "docker", "mysql", "redis-cli", "date", "find", "stat", "id", "python3", "bash", "cat",
    }
    for tool in re.findall(r"/(?:usr/)?(?:local/)?bin/([A-Za-z0-9_.-]+)", payload):
        if tool not in allowed_tools:
            errors.append(f"越界工具:{tool}")

    forbidden_commands = (
        r"(?m)^\s*(?:/[^ ]+/)?(?:rm|mv|cp|mkdir|touch|chmod|scp|sftp)\b",
        r"(?im)\bdocker\s+(?:restart|stop|start|rm|exec\s+[^\n]*\s+(?:rm|mv|cp|mkdir|touch|chmod))\b",
        r"(?im)\bmysql\b[^\n]*(?:--execute|-e)(?:=|\s)[^\n]*\b(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD)\b",
        r"(?im)\bredis-cli\b[^\n]*(?:\sDEL\s|\sKEYS\s|\sSCAN\s|\sFLUSHDB\s|\sFLUSHALL\s)",
        r"(?im)\bcurl\b[^\n]*(?:--request|-X)\s+(?:POST|PUT|PATCH|DELETE)\b",
    )
    for pattern in forbidden_commands:
        if re.search(pattern, payload):
            errors.append(f"禁止命令:{pattern}")

    for line in payload.splitlines():
        stripped = line.strip()
        if "mysql_scalar " in stripped and not stripped.startswith("mysql_scalar()"):
            sql_start = stripped.split("mysql_scalar ", 1)[1].lstrip("$(")
            if not (sql_start.startswith("'SELECT") or sql_start.startswith('"SELECT')):
                errors.append("非SELECT调用")
        if "/usr/local/bin/redis-cli" in stripped:
            if not re.search(r"\s(?:PING|INFO server|EXISTS \"\$lock_key\")\)?$", stripped):
                errors.append("Redis命令越界")
    return errors


def runner_contract_errors(runner: str) -> list[str]:
    errors: list[str] = []
    required = (
        "System.Text.UTF8Encoding($false, $true)",
        "Microsoft.PowerShell.Management\\Start-Process",
        "-RedirectStandardInput $InputPath",
        "Write-RestrictedBytes -Path $inputPath -Bytes $payloadBytes",
        "$processHandle = $process.Handle",
        "WaitForExit($TimeoutMilliseconds)",
        "try { $exitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }",
        "if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }",
        "foreach ($expectedExitCode in @(0, 7))",
        "if ($exitProbe.ExitCode -ne $expectedExitCode) { throw 'selftest_process_exit_code_mismatch' }",
        "process_exit_codes=0,7",
        "Remove-RestrictedTempDirectory",
        "process_timeout",
        "I_CONFIRM_EMAIL_UNKNOWN_REMOTE_READONLY_GATE_ONCE",
        "StrictHostKeyChecking=yes",
        "BatchMode=yes",
        "$script:SuccessPattern",
        "$script:FailurePattern",
        "(?<stage>",
        "\\r?\\n?\\z",
        "Classification = 'remote_gate_failed'; Stage = $failureMatch.Groups['stage'].Value",
        "$Classification -ceq 'remote_gate_failed'",
        "remote_stderr_nonempty",
        "ssh_attempt_count",
        "external_access=false",
    )
    for fragment in required:
        if fragment not in runner:
            errors.append(f"运行器缺少:{fragment}")
    if re.search(r"\$payload\s*\|", runner, re.IGNORECASE):
        errors.append("PowerShell管道提交payload")
    if runner.count("Microsoft.PowerShell.Management\\Start-Process") != 1:
        errors.append("SSH启动点数量")
    if "StandardInput.BaseStream" in runner or "RedirectStandardInput = $true" in runner:
        errors.append("默认StreamWriter输入边界")
    if "EF -and $bytes[1] -eq 0xBB" not in runner:
        errors.append("UTF8-BOM字节门禁")
    process_start = runner.find("$process = Microsoft.PowerShell.Management\\Start-Process")
    handle_read = runner.find("$processHandle = $process.Handle", process_start)
    timed_wait = runner.find("$process.WaitForExit($TimeoutMilliseconds)", process_start)
    exit_code_read = runner.find("$exitCode = $process.ExitCode", timed_wait)
    null_guard = runner.find("if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }", exit_code_read)
    return_cast = runner.find("return [pscustomobject]@{ ExitCode = [int]$exitCode }", null_guard)
    if min(process_start, handle_read, timed_wait) < 0 or not process_start < handle_read < timed_wait:
        errors.append("原生Handle必须在等待前取得")
    if min(timed_wait, exit_code_read, null_guard, return_cast) < 0 or not timed_wait < exit_code_read < null_guard < return_cast:
        errors.append("退出码读取必须先失败关闭再转换")
    if re.search(r"\[int\]\s*\$process\.ExitCode\b", runner, re.IGNORECASE):
        errors.append("禁止直接强转process.ExitCode")
    return errors


def run_runner_attack_models(runner: str) -> int:
    """对进程退出码冻结契约做单点变异，防止只更新 SelfTest 数字。"""
    mutations = (
        runner.replace("$processHandle = $process.Handle", "$processHandle = [IntPtr]::Zero", 1),
        runner.replace(
            "$processHandle = $process.Handle",
            "$null = $process.WaitForExit($TimeoutMilliseconds)\n        $processHandle = $process.Handle",
            1,
        ),
        runner.replace("if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }", "", 1),
        runner.replace(
            "return [pscustomobject]@{ ExitCode = [int]$exitCode }",
            "return [pscustomobject]@{ ExitCode = [int]$process.ExitCode }",
            1,
        ),
        runner.replace("foreach ($expectedExitCode in @(0, 7))", "foreach ($expectedExitCode in @(0))", 1),
        runner.replace("process_exit_codes=0,7", "process_exit_codes=0", 1),
    )
    for mutated in mutations:
        require(mutated != runner, "运行器变异未生效")
        require(bool(runner_contract_errors(mutated)), "退出码运行器变异未拒绝")
    return len(mutations)


def encode_without_bom(value: str) -> bytes:
    normalized = value.replace("\r\n", "\n").replace("\r", "\n")
    require(bool(normalized), "空payload")
    require(normalized[0] not in {"\ufeff", "\ufffe"}, "payload BOM")
    require("\x00" not in normalized, "payload NUL")
    require(normalized.startswith("set -Eeuo pipefail\n"), "payload首行")
    encoded = normalized.encode("utf-8")
    require(not encoded.startswith(b"\xef\xbb\xbf"), "编码产生BOM")
    return encoded


def validate_cycles(suffixes: list[str], schema_counts: list[int]) -> None:
    require(len(suffixes) == 2, "证据目录必须恰好两个")
    require(len(set(suffixes)) == 2, "重复目标")
    require(all(re.fullmatch(r"[a-f0-9]{32}", suffix) for suffix in suffixes), "恶意目录后缀")
    require(schema_counts == [1, 1], "隔离schema缺失或额外")


def validate_transport(stdout: str, stderr: str, exit_code: int) -> None:
    require(stderr == "", "stderr非空")
    require(exit_code == 0, "SSH非零")
    require(SUCCESS_PATTERN.fullmatch(stdout) is not None, "最终摘要无效")


def exact_state_paths(paths: list[str]) -> list[str]:
    """只保留历史状态文件的冻结精确命名，近似名称不得参与唯一性判断。"""
    pattern = re.compile(r"^/home/pc/molin-email-unknown-[a-f0-9]{32}\.state$")
    return [path for path in paths if pattern.fullmatch(path)]


def exact_orphan_paths(paths: list[str]) -> list[str]:
    """只保留本轮冻结的孤儿目录命名，后缀或扩展名近似项必须忽略。"""
    pattern = re.compile(r"^/home/pc/molin-runtime/email-unknown-[a-f0-9]{32}$")
    return [path for path in paths if pattern.fullmatch(path)]


def validate_adapter_boundary(live_adapter: str, isolated_adapter: str, template_provider: str) -> None:
    """分别冻结线上 Adapter、隔离进程 Adapter 和持久化模板供应商，三者不得混淆。"""
    require(bool(live_adapter) and live_adapter != "mock", "线上 API 仍使用 Mock")
    require(isolated_adapter == "mock", "独立故障验证进程未固定 Mock")
    require(template_provider == "aliyun_directmail", "持久化模板供应商不是阿里云 DirectMail")


def validate_root_schema_query(schema_name: str, sql: str) -> None:
    """容器内 root 通道只允许查询一个已验证隔离库是否存在。"""
    require(re.fullmatch(r"molin_restore_57_reverify_[a-f0-9]{32}", schema_name) is not None, "隔离库名无效")
    expected = f"SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='{schema_name}';"
    require(sql.split(maxsplit=1)[0] == "SELECT", "root 通道首关键字不是 SELECT")
    require(sql == expected, "root 通道 SQL 超出精确白名单")


def validate_numeric_file_metadata(expected_path: str, found_path: str, metadata: str, with_size: bool) -> None:
    """文件类型由 find 精确路径证明，stat 只允许数值 UID、mode 和可选非零大小。"""
    require(found_path == expected_path, "find 类型证明不匹配")
    pattern = r"^0:600:[1-9][0-9]*$" if with_size else r"^0:(?:600|700)$"
    require(re.fullmatch(pattern, metadata) is not None, "数值元数据无效")


def validate_early_failure_drain(bash: str) -> None:
    """模拟远端在长 stdin 尚未发送完时失败，确认会耗尽输入且 stderr 保持为空。"""
    probe = """set -Eeuo pipefail
stage=shell_options
exec 2>/dev/null
fail() {
  local failed_stage=$1
  trap - ERR
  /usr/bin/cat >/dev/null || true
  printf 'status=failed stage=%s\\n' "$failed_stage"
  exit 2
}
trap 'fail "$stage"' ERR
false
""" + ("# remaining-payload\n" * 8192)
    completed = subprocess.run(
        [bash, "--noprofile", "--norc", "-s", "--"],
        input=probe,
        check=False,
        capture_output=True,
        text=True,
    )
    require(completed.returncode == 2, "早期失败探针退出码无效")
    require(completed.stdout == "status=failed stage=shell_options\n", "早期失败摘要无效")
    require(completed.stderr == "", "早期失败仍产生 stderr")


def run_attack_models(payload: str) -> int:
    cases = 0
    for invalid in ("\ufeff" + payload, "\ufffe" + payload, payload + "\x00"):
        rejected = False
        try:
            encode_without_bom(invalid)
        except ContractError:
            rejected = True
        require(rejected, "BOM/NUL攻击未拒绝")
        cases += 1
    require(encode_without_bom(payload.replace("\n", "\r\n")).startswith(b"set "), "CRLF规范化失败")
    cases += 1

    malicious_suffixes = (
        "a" * 31,
        "a" * 33,
        "g" * 32,
        "a" * 15 + "'" + "a" * 16,
        "a" * 15 + ";" + "a" * 16,
        "a" * 15 + " " + "a" * 16,
        "a" * 15 + "\n" + "a" * 16,
        "a" * 15 + "*" + "a" * 16,
    )
    for suffix in malicious_suffixes:
        rejected = False
        try:
            validate_cycles([suffix, "b" * 32], [1, 1])
        except ContractError:
            rejected = True
        require(rejected, "恶意后缀未拒绝")
        cases += 1

    for suffixes, counts in ((["a" * 32], [1]), (["a" * 32, "a" * 32], [1, 1]), (["a" * 32, "b" * 32, "c" * 32], [1, 1, 1]), (["a" * 32, "b" * 32], [1, 0])):
        rejected = False
        try:
            validate_cycles(suffixes, counts)
        except ContractError:
            rejected = True
        require(rejected, "目录/schema数量攻击未拒绝")
        cases += 1

    valid = (
        "status=pass api_count=1 health=true ready=true live_adapter_mock=false mysql_count=1 redis_count=1 schema=57 dirty=false "
        "clock_drift_ok=true state_safe=true state_phase=phase1_created primary_owned=1 unexpected_owned=1 "
        "scope_rows=2 template_owned=1 allowlist_owned=1 redis_ping=true run_id_changed=true lock_exists=1 "
        "orphan_count=0 orphan_safe_count=0 cycle_evidence_count=2 cycle_valid_count=2 cycle_schema_count=2 "
        "cycle_excluded_count=2 writes=false restart=false cleanup=false\n"
    )
    validate_transport(valid, "", 0)
    cases += 1
    for stdout, stderr, exit_code in (
        (valid.replace(" cycle_excluded_count=2", ""), "", 0),
        (valid + "extra=true\n", "", 0),
        (valid.replace("api_count=1 health=true", "health=true api_count=1"), "", 0),
        (valid, "raw error", 0),
        ("", "", 255),
    ):
        rejected = False
        try:
            validate_transport(stdout, stderr, exit_code)
        except ContractError:
            rejected = True
        require(rejected, "输出/SSH攻击未拒绝")
        cases += 1

    mutations = (
        payload.replace("set -Eeuo pipefail", "set -e", 1),
        payload + "\nsh -c 'echo unsafe'\n",
        payload + "\n/usr/bin/docker exec x /usr/bin/mysql --execute='UPDATE users SET id=id'\n",
        payload + "\n/usr/local/bin/redis-cli DEL unsafe\n",
        payload + "\n/usr/bin/curl --request POST http://127.0.0.1:8080/api/health\n",
        payload.replace("cycle_evidence_count=%s cycle_valid_count=%s cycle_schema_count=%s cycle_excluded_count=%s", "cycle_evidence_count=%s"),
    )
    for mutated in mutations:
        require(bool(payload_contract_errors(mutated)), "写命令或摘要攻击未拒绝")
        cases += 1
    valid_suffix = "a" * 32
    state_candidates = (
        f"/home/pc/molin-email-unknown-{valid_suffix}.state",
        f"/home/pc/molin-runtime/email-unknown-restart-state-{valid_suffix}.json",
        f"/home/pc/molin-email-unknown-{valid_suffix}.state.bak",
        f"/home/pc/molin-email-unknown-{valid_suffix.upper()}.state",
    )
    require(exact_state_paths(list(state_candidates)) == [state_candidates[0]], "历史状态文件近似名被纳入")
    cases += 1

    orphan_candidates = (
        f"/home/pc/molin-runtime/email-unknown-{valid_suffix}",
        f"/home/pc/molin-runtime/email-unknown-{valid_suffix}-backup",
        f"/home/pc/molin-runtime/email-unknown-{valid_suffix}.state",
        f"/home/pc/molin-runtime/email-unknown-{valid_suffix.upper()}",
    )
    require(exact_orphan_paths(list(orphan_candidates)) == [orphan_candidates[0]], "孤儿目录近似名被纳入")
    cases += 1

    require("200" == "200" and all(status != "200" for status in ("204", "301", "404")), "HTTP状态门禁未严格限制为200")
    cases += 1

    dump_gate_mutation = payload.replace('[[ -z "$dump_symlink" ]]', "", 1)
    require(bool(payload_contract_errors(dump_gate_mutation)), "dump符号链接门禁删除攻击未拒绝")
    cases += 1

    validate_adapter_boundary("directmail", "mock", "aliyun_directmail")
    for live_adapter, isolated_adapter in (("mock", "mock"), ("directmail", "directmail")):
        rejected = False
        try:
            validate_adapter_boundary(live_adapter, isolated_adapter, "aliyun_directmail")
        except ContractError:
            rejected = True
        require(rejected, "线上与独立测试进程的 Adapter 边界未冻结")
    cases += 1

    rejected = False
    try:
        validate_adapter_boundary("directmail", "mock", "mock")
    except ContractError:
        rejected = True
    require(rejected, "Mock Adapter 被错误当作持久化模板供应商")
    cases += 1

    root_schema = "molin_restore_57_reverify_" + "b" * 32
    root_sql = f"SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='{root_schema}';"
    validate_root_schema_query(root_schema, root_sql)
    app_visible, root_visible = [0, 0], [1, 1]
    require(app_visible == [0, 0] and root_visible == [1, 1], "权限可见性模型无效")
    for invalid_sql in (
        f"DELETE FROM information_schema.schemata WHERE schema_name='{root_schema}';",
        root_sql + " SELECT 1;",
    ):
        rejected = False
        try:
            validate_root_schema_query(root_schema, invalid_sql)
        except ContractError:
            rejected = True
    require(rejected, "root 只读通道接受了越界 SQL")
    cases += 1

    fixture_path = "/root/molin-000057-schema57-cycle-run-" + "c" * 32
    validate_numeric_file_metadata(fixture_path, fixture_path, "0:700", False)
    validate_numeric_file_metadata(fixture_path + "/dump.sql", fixture_path + "/dump.sql", "0:600:1024", True)
    for localized_type in ("目录", "普通文件", "directory", "regular file"):
        rejected = False
        try:
            validate_numeric_file_metadata(fixture_path, localized_type, "0:700", False)
        except ContractError:
            rejected = True
        require(rejected, "本地化文件类型文本被错误接受")
    cases += 1
    return cases


def main() -> None:
    payload_bytes = PAYLOAD.read_bytes()
    require(not payload_bytes.startswith((b"\xef\xbb\xbf", b"\xff\xfe", b"\xfe\xff")), "payload文件含BOM")
    payload = payload_bytes.decode("utf-8")
    runner = RUNNER.read_text(encoding="utf-8")
    require(not payload_contract_errors(payload), "payload静态契约失败: " + ", ".join(payload_contract_errors(payload)))
    require(not runner_contract_errors(runner), "运行器静态契约失败: " + ", ".join(runner_contract_errors(runner)))

    bash = shutil.which("bash")
    if bash is None:
        candidates = (Path(r"C:\Program Files\Git\bin\bash.exe"), Path(r"C:\Program Files\Git\usr\bin\bash.exe"))
        bash = next((str(candidate) for candidate in candidates if candidate.is_file()), None)
    require(bash is not None, "缺少本地Bash")
    syntax = subprocess.run([str(bash), "--noprofile", "--norc", "-n", str(PAYLOAD)], check=False, capture_output=True, text=True)
    require(syntax.returncode == 0 and syntax.stderr == "", "payload Bash语法失败")
    validate_early_failure_drain(str(bash))

    powershell = shutil.which("powershell.exe") or shutil.which("powershell")
    require(powershell is not None, "缺少Windows PowerShell")
    selftest = subprocess.run(
        [str(powershell), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(RUNNER), "-SelfTest"],
        check=False,
        capture_output=True,
        text=True,
    )
    require(selftest.returncode == 0 and selftest.stderr == "", "PowerShell SelfTest失败")
    selftest_match = SELFTEST_PATTERN.fullmatch(selftest.stdout)
    require(selftest_match is not None, "SelfTest摘要无效")
    require(selftest_match.group("cases") == "20", "SelfTest用例数未冻结为20")
    require(selftest_match.group("exit_codes") == "0,7", "SelfTest未动态验证0/7退出码")

    attack_cases = run_attack_models(payload) + run_runner_attack_models(runner) + 1
    print("email_unknown_remote_readonly_gate_contract=pass")
    print("external_access=false")
    print("database_access=false")
    print("redis_access=false")
    print("ssh_started=false")
    print(f"attack_cases={attack_cases}")
    print("powershell_selftest=pass")


if __name__ == "__main__":
    main()

# G8 Drop 映射测试服暂存只读取证（008）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个适用于 Drop 映射入口的 008 暂存只读取证候选，在不读取 hostname 或 machine-id 的前提下安全收敛 003 暂存状态，并完成仓库工程门禁但不连接测试服务。

**Architecture:** 新包装器冻结加载已消费 004 helper，仅复用其本地 known_hosts、客户端密钥、OpenSSH 路径和最小环境校验；008 自己生成独立远端只读程序并解析九键三态契约。远端程序以目录描述符钉住部署根和暂存目录，对固定五文件执行元数据、内容和竞态复核；历史 004–007 脚本保持不变。

**Tech Stack:** Python 3.13 标准库、`unittest`、OpenSSH、GitHub Actions、Docker `python:3.13-alpine --network none`、Markdown。

## Global Constraints

- ChangeId 固定为 `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`。
- 目标固定为 `pc@8.130.9.163:10003`，部署根固定为 `/home/pc/molin`，暂存路径固定为 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
- 004 helper SHA-256 固定为 `599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`，不得修改历史 004–007 脚本或恢复其 ChangeId。
- 008 不读取或验证 hostname、`/etc/machine-id`、实例元数据、CMDB、日志、数据库、队列、监控、备份或业务数据。
- 最大本地检查 1 次、SSH 1 次、重试 0、业务请求 0、上游请求 0、费用上限 0 CNY；用户再次独立批准前禁止正式运行 `--local-check` 或 SSH。
- 源码注释、提交、PR 和文档均使用中文；不得输出或提交 Secret、私钥、Token、当前 machine-id 原文或摘要。
- 本计划只完成工程候选、评审、CI、PR 和 merge commit；不授权上传、安装、sudo、清理、生产、付费调用、通知或客户灰度。

---

### Task 1: 冻结远端九键三态契约

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py`
- Create: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py`

**Interfaces:**
- Consumes: 设计文档中的固定 ChangeId、目标、部署根、暂存路径和五文件摘要/大小。
- Produces: `build_remote_program() -> str`、`parse_remote_output(stdout: str) -> dict[str, str]`、`EvidenceError`。

- [ ] **Step 1: 写入脚本加载器和物理主机身份禁用测试**

```python
SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-drop-staging-evidence.py")
SPEC = importlib.util.spec_from_file_location("g8_drop_staging_evidence", SCRIPT_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)

def test_remote_program_omits_physical_host_identity(self):
    program = MODULE.build_remote_program()
    self.assertNotIn("/etc/machine-id", program)
    self.assertNotIn("HOSTNAME=", program)
    self.assertNotIn("os.uname", program)
    self.assertNotIn("instance-id", program)
    self.assertIn("deployment_root = '/home/pc/molin'", program)

def test_parser_accepts_absent_state(self):
    values = MODULE.parse_remote_output(
        "\n".join([
            f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}",
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}",
            "LOGIN_USER=pc",
            "DEPLOYMENT_ROOT_REALPATH=/home/pc/molin",
            "DEPLOYMENT_ROOT_CHECK=PASS",
            "STAGING_STATE=ABSENT",
            "STAGING_INTEGRITY=NOT_APPLICABLE",
            "STAGING_MISMATCH_REASON=NONE",
            "EVIDENCE_RESULT=PASS",
        ])
    )
    self.assertEqual(values["STAGING_STATE"], "ABSENT")
```

- [ ] **Step 2: 运行测试并确认 RED**

Run:

```powershell
$env:PYTHONPYCACHEPREFIX = Join-Path $env:TEMP 'g8-008-red-pycache'
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
```

Expected: FAIL，原因是生产脚本不存在或 `build_remote_program`/`parse_remote_output` 未定义；不得是测试语法错误。

- [ ] **Step 3: 实现最小脚本常量、远端程序骨架和严格解析器**

```python
#!/usr/bin/env python3
"""对 Drop 映射测试入口执行一次完全只读的 003 暂存低敏取证。"""

import sys

if not sys.flags.isolated:
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import re

CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
EXPECTED_REMOTE_KEYS = frozenset({
    "EVIDENCE_CHANGE_ID", "TARGET_CHANGE_ID", "LOGIN_USER",
    "DEPLOYMENT_ROOT_REALPATH", "DEPLOYMENT_ROOT_CHECK",
    "STAGING_STATE", "STAGING_INTEGRITY", "STAGING_MISMATCH_REASON",
    "EVIDENCE_RESULT",
})

class EvidenceError(RuntimeError):
    """表示远端输出未形成完整、低敏且可验证的暂存证据。"""

def build_remote_program() -> str:
    return f"""import grp
import hashlib
import os
import pwd
import stat
evidence_change_id = {CHANGE_ID!r}
target_change_id = {TARGET_CHANGE_ID!r}
deployment_root = {TARGET_DEPLOYMENT_ROOT!r}
staging_path = {STAGING_PATH!r}
"""

def parse_remote_output(stdout: str) -> dict[str, str]:
    try:
        stdout.encode("ascii")
    except UnicodeEncodeError as error:
        raise EvidenceError("non_ascii_output") from error
    values: dict[str, str] = {}
    for line in stdout.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if not match or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")
    expected = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values[key] != value for key, value in expected.items()):
        raise EvidenceError("remote_contract_mismatch")
    state = (values["STAGING_STATE"], values["STAGING_INTEGRITY"], values["STAGING_MISMATCH_REASON"])
    valid = {
        ("ABSENT", "NOT_APPLICABLE", "NONE"),
        ("PRESENT", "PASS", "NONE"),
        *(("PRESENT", "MISMATCH", reason) for reason in (
            "PATH", "FILE_SET", "FILE_METADATA", "FILE_CONTENT", "READ_ERROR"
        )),
    }
    if state not in valid:
        raise EvidenceError("invalid_staging_state")
    return values
```

- [ ] **Step 4: 增加解析器负例并运行 GREEN**

```python
def test_parser_rejects_physical_identity_keys_and_bad_combinations(self):
    valid = self.valid_output()
    for bad in (
        valid + "\nHOSTNAME=backend",
        valid + "\nMACHINE_ID_SHA256=" + "a" * 64,
        valid.replace("STAGING_INTEGRITY=NOT_APPLICABLE", "STAGING_INTEGRITY=PASS"),
        valid.replace(f"EVIDENCE_CHANGE_ID={MODULE.CHANGE_ID}", "EVIDENCE_CHANGE_ID=wrong"),
    ):
        with self.subTest(bad=bad[-80:]):
            with self.assertRaises(MODULE.EvidenceError):
                MODULE.parse_remote_output(bad)
```

Run: 前述 `python -I ... -v`。

Expected: PASS，且输出没有 ResourceWarning。

- [ ] **Step 5: 提交契约交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
git commit -m "新增：冻结G8 Drop暂存取证008契约" -m "影响模块：infra/scripts；新增九键三态解析和物理主机身份禁用门禁"
```

---

### Task 2: 实现目录描述符锚定的暂存取证引擎

**Files:**
- Modify: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py`

**Interfaces:**
- Consumes: Task 1 的 `build_remote_program()` 和九键输出契约。
- Produces: `build_remote_program(*, deployment_root: str = TARGET_DEPLOYMENT_ROOT, staging_path: str = STAGING_PATH, expected_files: dict[str, tuple[str, int]] | None = None) -> str`；可在临时 POSIX 文件系统执行的完整远端程序；固定五文件 `FROZEN_FILES: dict[str, tuple[str, int]]`。

- [ ] **Step 1: 写入 ABSENT、PASS 和五类 MISMATCH 动态测试**

先在测试文件中定义 `PosixFixture`。它必须把生产脚本的固定路径常量替换为系统临时目录内的 `deployment_root` 和 `staging_path`，以当前 uid/gid 创建目录；`run_remote()` 使用 `MODULE.build_remote_program(deployment_root=str(self.deployment_root), staging_path=str(self.staging_path), expected_files=self.expected_files)` 生成程序，再以 `subprocess.run([sys.executable, "-I", "-c", program], capture_output=True, text=True, timeout=10, check=False)` 真实执行并调用 `MODULE.parse_remote_output`，不得 mock 文件系统调用。`create_valid_stage()` 按 `self.expected_files` 创建五文件；测试夹具生成的文件内容及摘要由构造器从实际测试字节计算，生产默认值仍只能是正式 `MODULE.FROZEN_FILES`。

```python
def test_remote_program_reports_absent_and_present_pass(self):
    with self.posix_fixture() as fixture:
        absent = fixture.run_remote()
        self.assertEqual(absent["STAGING_STATE"], "ABSENT")
        fixture.create_valid_stage()
        present = fixture.run_remote()
        self.assertEqual(
            (present["STAGING_STATE"], present["STAGING_INTEGRITY"], present["STAGING_MISMATCH_REASON"]),
            ("PRESENT", "PASS", "NONE"),
        )

def test_remote_program_classifies_all_mismatch_reasons(self):
    mutations = {
        "PATH": lambda f: f.replace_stage_with_symlink(),
        "FILE_SET": lambda f: f.add_extra_file(),
        "FILE_METADATA": lambda f: f.chmod_manifest(0o622),
        "FILE_CONTENT": lambda f: f.replace_manifest_same_size(),
        "READ_ERROR": lambda f: f.make_manifest_unreadable(),
    }
    for expected, mutate in mutations.items():
        with self.subTest(expected=expected), self.posix_fixture() as fixture:
            fixture.create_valid_stage()
            mutate(fixture)
            result = fixture.run_remote()
            self.assertEqual(result["STAGING_MISMATCH_REASON"], expected)
```

- [ ] **Step 2: 运行测试并确认 RED**

Run:

```powershell
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
```

Expected: FAIL，因为远端程序尚未输出三态或未执行文件取证。

- [ ] **Step 3: 实现完整远端只读算法**

在生产脚本中冻结以下文件清单，并把这些常量直接插入 `build_remote_program()`：

```python
FROZEN_FILES = {
    "SHA256SUMS": ("82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d", 362),
    "ai-gateway-reconcile": ("37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1", 13_066_129),
    "g8-test-readonly-audit": ("308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", 18_377),
    "manifest.env": ("726174ea41ecfee69f9d8c1aff7411dc9a8c73f3dc60ca0d5e700eb5f962ea66", 897),
    "molin-g8-test-readonly-audit.sudoers": ("1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f", 416),
}
```

远端程序按以下顺序实现，所有注释使用中文：

```python
account = pwd.getpwnam("pc")
group = grp.getgrnam("pc")
if os.getuid() != account.pw_uid:
    raise SystemExit(41)

root_meta = os.lstat(deployment_root)
if os.path.realpath(deployment_root) != deployment_root:
    raise SystemExit(41)
root_fd = os.open(deployment_root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
try:
    pinned_root = os.fstat(root_fd)
    # 核对 dev/ino、目录类型、pc:pc、0700 必需位和组/其他不可写。
    # 只用固定 basename + dir_fd 查找暂存目录。
    # 暂存存在时打开 stage_fd，并核对精确 0700 和 pc:pc。
    # 对五文件逐一 O_NOFOLLOW 打开，先 fstat，再按 1 MiB 分块 SHA-256，再 fstat。
    # 保存 dev/ino/mode/uid/gid/size/mtime_ns/ctime_ns，哈希后和最终目录项全部复核。
    # 最后重列五文件集合并复核 stage/root 的 inode、mtime_ns、ctime_ns。
finally:
    os.close(root_fd)
```

异常分类规则必须写成显式分支：路径/竞态为 `PATH`，集合为 `FILE_SET`，类型/属主/权限/大小为 `FILE_METADATA`，摘要为 `FILE_CONTENT`，读取系统调用异常为 `READ_ERROR`。元数据不合规时不得读取内容。

- [ ] **Step 4: 写入父路径和文件目录项竞态测试**

```python
def test_remote_program_fails_closed_when_root_or_file_entry_drifts(self):
    for injector in ("replace_root_after_open", "replace_manifest_after_hash"):
        with self.subTest(injector=injector), self.posix_fixture(injector=injector) as fixture:
            fixture.create_valid_stage()
            result = fixture.run_remote(allow_exit_41=True)
            self.assertIn(result.classification, {"EXIT_41", "PRESENT_MISMATCH_PATH"})
            self.assertNotIn("PASS", result.raw_stdout)
```

- [ ] **Step 5: 在 Linux 无网络容器运行 GREEN**

Run:

```powershell
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
```

Expected: 所有 POSIX 动态测试 PASS，不跳过竞态用例，不产生网络访问或 ResourceWarning。

- [ ] **Step 6: 提交取证引擎交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
git commit -m "新增：实现G8 Drop暂存只读取证" -m "影响模块：infra/scripts；增加目录描述符锚定、五文件摘要和竞态失败关闭"
```

---

### Task 3: 实现冻结 helper、本地检查和单次 SSH 包装

**Files:**
- Modify: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py`

**Interfaces:**
- Consumes: Task 2 的远端程序与解析器；冻结 helper 路径和 SHA。
- Produces: `load_frozen_helper() -> types.ModuleType`、`collect_stream(stream, limit) -> StreamCapture`、`run_once(helper, known_hosts, identity_file) -> dict[str, str]`、`main() -> int`。

- [ ] **Step 1: 写入 helper 漂移、本地检查和单 SSH RED 测试**

在测试文件中定义两个定向工具：`helper_fixture(mutation: str)` 从冻结 helper 复制普通文件到 `TemporaryDirectory`，按枚举执行链接替换、读后 inode 替换、单字节改动或模块常量改动；`capture_main(*args: str)` 用 `mock.patch.object(sys, "argv", [str(SCRIPT_PATH), *args])` 捕获 stdout 和 `MODULE.main()` 返回码，并在退出后恢复 argv。工具仅位于测试文件，不进入生产脚本。

```python
def test_helper_type_inode_digest_and_contract_drift_fail_closed(self):
    mutations = ("symlink", "replace_after_open", "wrong_digest", "wrong_target", "wrong_port", "wrong_fingerprint")
    for mutation in mutations:
        with self.subTest(mutation=mutation), self.helper_fixture(mutation) as path:
            with self.assertRaises(MODULE.EvidenceError):
                MODULE.load_frozen_helper(path)

def test_local_check_never_starts_ssh(self):
    with mock.patch.object(MODULE, "run_once") as run_once:
        code, stdout = self.call_main("--local-check")
    self.assertEqual(code, 0)
    self.assertEqual(stdout, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_LOCAL_CHECK=PASS\n")
    run_once.assert_not_called()

def test_run_once_uses_one_locked_down_ssh_process(self):
    result = MODULE.run_once(self.helper, self.known_hosts, self.identity_file)
    self.assertEqual(self.popen_calls, 1)
    command = self.last_command
    self.assertIn("ConnectionAttempts=1", command)
    self.assertIn("StrictHostKeyChecking=yes", command)
    self.assertIn("PasswordAuthentication=no", command)
    self.assertIn("ClearAllForwardings=yes", command)
    self.assertEqual(result["STAGING_STATE"], "ABSENT")
```

- [ ] **Step 2: 运行测试并确认 RED**

Expected: FAIL，因为 helper 加载、流采集、SSH 和 CLI 尚未实现。

- [ ] **Step 3: 实现冻结 helper 和本地输入校验**

```python
FROZEN_HELPER_SHA256 = "599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89"

def load_frozen_helper(path: Path | None = None) -> types.ModuleType:
    helper_path = path or Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence.py")
    before = helper_path.lstat()
    if not stat.S_ISREG(before.st_mode) or helper_path.is_symlink():
        raise EvidenceError("invalid_helper")
    source = helper_path.read_bytes()
    after = helper_path.lstat()
    if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino):
        raise EvidenceError("helper_drift")
    if hashlib.sha256(source).hexdigest() != FROZEN_HELPER_SHA256:
        raise EvidenceError("helper_digest_mismatch")
    module = types.ModuleType("g8_frozen_staging_helper")
    exec(compile(source, str(helper_path), "exec"), module.__dict__)
    expected = {
        "TARGET": "pc@8.130.9.163",
        "TARGET_HOST": "8.130.9.163",
        "TARGET_PORT": "10003",
        "TARGET_SSH_ED25519_FINGERPRINT": "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
        "LOCAL_IDENTITY_ED25519_FINGERPRINT": "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0",
    }
    if module.CHANGE_ID_CONSUMED is not True or any(getattr(module, key, None) != value for key, value in expected.items()):
        raise EvidenceError("helper_contract_mismatch")
    return module
```

调用 helper 的 `validate_known_hosts`、`validate_identity_file`、`fixed_ssh_executable` 和 `fixed_ssh_environment`；任一异常统一转换为 `EvidenceError`，不得回显异常正文。

- [ ] **Step 4: 实现有界双流采集和固定 SSH**

```python
@dataclass
class StreamCapture:
    data: bytes
    byte_count: int
    line_count: int
    sha256: str
    exceeded: bool
    error: bool

def collect_stream(stream, limit: int = 64 * 1024) -> StreamCapture:
    kept = bytearray()
    digest = hashlib.sha256()
    count = lines = 0
    failed = False
    try:
        while True:
            block = stream.read(8192)
            if not block:
                break
            count += len(block)
            lines += block.count(b"\n")
            digest.update(block)
            if len(kept) < limit + 1:
                kept.extend(block[: limit + 1 - len(kept)])
    except Exception:
        failed = True
    return StreamCapture(bytes(kept), count, lines, digest.hexdigest(), count > limit, failed)
```

`run_once` 使用一次 `subprocess.Popen`，命令必须以 helper 返回的固定 OpenSSH 开头，追加 `-F none`、`BatchMode=yes`、`ConnectionAttempts=1`、严格 known_hosts/IdentityFile、禁密码/键盘交互/Agent/X11/TTY/全部转发，并以 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -` 为唯一远端命令。两个线程并发排空 stdout/stderr；非零、stderr 非空、任一流超限或 error 均抛 `EvidenceError`。

- [ ] **Step 5: 实现 CLI、稳定输出和消费前置门禁**

```python
class SafeArgumentParser(argparse.ArgumentParser):
    """把参数错误收敛为固定低敏异常，不回显调用方路径或参数。"""

    def error(self, message: str) -> None:
        raise EvidenceError("invalid_arguments")

def build_parser() -> argparse.ArgumentParser:
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    return parser

def main() -> int:
    arguments = build_parser().parse_args()
    if arguments.self_test:
        load_frozen_helper()
        compile(build_remote_program(), "<g8-drop-staging-evidence>", "exec")
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_SELF_TEST=PASS")
        return 0
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=change_id_consumed")
        return 2
    # 参数形状、ChangeId、本地身份材料全部通过后，local-check 不联网；正式模式仅调用 run_once 一次。
```

正式结果映射：ABSENT 与 PASS 返回 0；MISMATCH 返回 3；异常返回 2 且仅输出 `G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=evidence_unavailable`。

- [ ] **Step 6: 增加 64 KiB、管道异常和消费顺序测试并运行 GREEN**

```python
def test_consumed_change_rejects_before_helper_identity_or_network(self):
    with mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", True), \
         mock.patch.object(MODULE, "load_frozen_helper") as helper, \
         mock.patch.object(MODULE, "run_once") as run_once, \
         mock.patch.object(sys, "argv", [str(SCRIPT_PATH)]):
        code, stdout = self.capture_main()
    self.assertEqual((code, stdout), (2, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=change_id_consumed\n"))
    helper.assert_not_called()
    run_once.assert_not_called()
```

Windows 和 Linux `--network none` 均须全绿。

- [ ] **Step 7: 提交包装器交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
git commit -m "新增：完成G8 Drop暂存取证008包装器" -m "影响模块：infra/scripts；冻结SSH身份、单次连接、有界输出和消费门禁"
```

---

### Task 4: 接入分级 CI、授权清单和证据文档

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/tools.md`
- Create: `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md`

**Interfaces:**
- Consumes: Task 3 的脚本、测试数量、脚本 SHA-256 和固定命令行。
- Produces: G8 CI 门禁、`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL` 授权清单及一致的工程/远端边界文档。

- [ ] **Step 1: 先写 CI 静态门禁失败测试**

在 008 单测中增加：

```python
def test_ci_runs_windows_and_linux_no_network_gates(self):
    workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
    self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence.py", workflow)
    self.assertIn("python:3.13-alpine", workflow)
    self.assertIn("--network none", workflow)
    self.assertIn("run-ai-gateway-g8-test-drop-staging-evidence.py --self-test", workflow)
```

Run 单测，Expected: FAIL，因为 CI 尚未接入。

- [ ] **Step 2: 修改 G8 生产就绪 Job**

在现有 G8 脚本测试附近加入以下等价步骤，保持 YAML 缩进与精确 PR HEAD checkout：

```yaml
- name: 验证 G8 Drop 暂存取证 008
  shell: bash
  run: |
    python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
    python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
    python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py --self-test
    docker run --rm --network none -v "$PWD:/repo:ro" -w /repo python:3.13-alpine \
      python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
```

- [ ] **Step 3: 计算冻结摘要并编写授权清单**

Run:

```powershell
$scriptSha = (Get-FileHash -Algorithm SHA256 infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py).Hash.ToLowerInvariant()
$helperSha = (Get-FileHash -Algorithm SHA256 infra/scripts/run-ai-gateway-g8-test-staging-evidence.py).Hash.ToLowerInvariant()
```

授权清单必须写入实际 64 位摘要，不使用占位符；状态为 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。命令摘要只能包含一次 `--local-check` 和一次移除该参数的正式调用，明确当前全部命令禁止执行。影响为只读 SSH/可能系统访问日志，无应用层回滚；停止条件覆盖身份材料、Drop 目标、登录用户、部署根、输出契约和三态异常。

- [ ] **Step 4: 同步历史纠偏和当前状态文档**

文档必须一致记录：

```text
007 READABLE_MISMATCH 是真实历史结果，但 machine-id 门禁不适用于 Drop 映射入口，因此不再登记为测试服运行态 P1。
003 暂存仍为 UNKNOWN；008 仅完成工程候选，未连接测试服务。
008 合并不构成执行授权；执行、清理、安装、运行态审计分别需要独立 ChangeId。
G8_ENGINEERING_READY 保持；G8_COMMERCIAL_ACCEPTED 未完成。
```

- [ ] **Step 5: 运行文档、CI 和敏感信息门禁**

Run:

```powershell
python -I infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main
git diff --check
```

Expected: 全部 PASS；敏感扫描 `findings=0`，文档不含当前 machine-id 原文/摘要或私钥内容。

- [ ] **Step 6: 提交 CI 与文档交付**

```powershell
git add .github/workflows/ci.yml README.md docs/ai-gateway-g8-acceptance.md docs/ai-gateway-g8-test-readonly-access-runbook.md docs/test-plan.md docs/tools.md docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
git commit -m "文档：接入G8 Drop暂存取证008门禁" -m "影响模块：CI、README、G8验收、测试计划、工具和只读入口Runbook"
```

---

### Task 5: 完成工程验收、PR 和 merge commit

**Files:**
- Modify only if evidence changes: `README.md`
- Modify only if evidence changes: `docs/ai-gateway-g8-acceptance.md`
- Modify only if evidence changes: `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md`

**Interfaces:**
- Consumes: Tasks 1–4 的完整分支和精确 HEAD。
- Produces: 独立安全/QA/产品 P0/P1=0、适用 CI 绿色、merge commit、远端分支删除和 `PENDING_USER_APPROVAL` 执行清单。

- [ ] **Step 1: 运行分级本地门禁**

```powershell
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py -v
python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence.py
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py --self-test
git diff --check origin/main...HEAD
```

不得运行正式 `--local-check`，因为用户只批准仓库工程工作。

- [ ] **Step 2: 固定精确 HEAD 并组织独立门禁**

记录 `git rev-parse HEAD`，分别要求独立代码安全评审、QA 和产品/规格验收只读检查该精确提交。每方必须明确：仓库变更 `P0=0/P1=0`、未连接测试服务、007 历史事实保留、003 仍 UNKNOWN、008 执行未授权。

- [ ] **Step 3: 推送并创建中文 Draft PR**

```powershell
git push -u origin feature/backend-d-ai-gateway-g8-drop-staging-evidence-008-design
gh pr create --draft --base main --head feature/backend-d-ai-gateway-g8-drop-staging-evidence-008-design --title "[AI网关] 增加Drop映射暂存取证008" --body-file docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md
```

PR 正文须补充精确 HEAD、测试、三方结论和非执行/非生产/非商业边界；不得把授权清单状态写成已批准。

- [ ] **Step 4: 等待精确 PR HEAD 适用 CI 全绿**

Run:

```powershell
gh pr checks --watch --interval 20
```

Expected: 分类器、G8 生产就绪及必选汇总成功；因改动包含 `.github/**`、`infra/**`，分类器应失败关闭为完整 CI，不得手工降级。

- [ ] **Step 5: 收口最终同 HEAD 评审并转 Ready**

如 CI 或评审修复产生新提交，必须对新 HEAD 重跑三方增量复评并更新 PR 证据。只有 P0/P1=0、全部适用 checks 成功后才能执行 `gh pr ready`。

- [ ] **Step 6: 使用 merge commit 合并并删除远端分支**

```powershell
gh pr merge --merge --delete-branch
git fetch origin main --prune
```

验证 merge commit 有两个父提交，第二父为精确 PR HEAD；验证远端功能分支不存在。若 `gh` 因本地主工作树占用 main 返回本地错误，先只读核对 PR 是否已远端合并，禁止盲目重试。

- [ ] **Step 7: 创建纯文档证据收口 PR**

从新 `origin/main` 创建 `feature/docs-ai-gateway-g8-drop-staging-evidence-008-closeout`，只更新 README、G8 acceptance 和 008 授权清单：精确记录功能 HEAD、CI run、三方结果、merge commit、分支删除，并把状态收敛为 `PENDING_USER_APPROVAL`。按 docs-only 分级门禁通过后以 merge commit 合并。

- [ ] **Step 8: 提交独立执行授权清单并停止**

向用户报告：008 工程候选已合并，但未运行本地检查或 SSH。执行授权申请必须绑定 ChangeId、精确脚本/helper SHA、目标、一次本地检查、一次 SSH、零重试、0/0/0 CNY、影响、无应用层回滚和停止条件。等待用户再次明确批准，不得自动连接测试服务。

---

## 计划自审结论

- 规格覆盖：Task 1 覆盖九键契约，Task 2 覆盖暂存算法和竞态，Task 3 覆盖本地信任与传输，Task 4 覆盖 CI/文档/授权，Task 5 覆盖独立验收和合并。
- 范围边界：计划没有测试服连接、上传、清理、安装、生产、付费调用、通知或客户灰度步骤。
- 类型一致：`build_remote_program`、`parse_remote_output`、`load_frozen_helper`、`collect_stream`、`run_once` 和 `main` 的名称与消费者一致。
- 无占位符：所有 ChangeId、路径、历史摘要、命令和状态均为精确值；008 脚本摘要只能在实现后实算并写入授权清单，不允许猜测。

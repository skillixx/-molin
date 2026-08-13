# G8 Drop 暂存只读取证 012 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建新的 012 只读取证候选，在不连接测试服的工程阶段完成代码、测试、CI、独立评审和 Draft PR，为以后一次授权 SSH 精确收敛 011 暂存 `UNKNOWN` 做准备。

**Architecture:** 新包装器独立冻结 Drop 端点、本地 ED25519 信任材料、011 五文件及 manifest/回执契约；正式模式只允许一个固定 OpenSSH 子进程，把独立远端 Python 只读程序经 stdin 发送到固定目标。远端程序以目录描述符锚定部署根、暂存目录和每个文件，输出严格九键三态；001 至 011 和 008 历史入口保持消费态且不被调用。

**Tech Stack:** Python 3.13 标准库、`unittest`、OpenSSH、PowerShell、GitHub Actions、Docker `python:3.13-alpine --network none`、Markdown。

## Global Constraints

- ChangeId 固定为 `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012`。
- 目标 ChangeId 固定为 `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- Drop 目标固定为 `pc@8.130.9.163:10003`，部署根固定为 `/home/pc/molin`，暂存固定为 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 物理主机身份固定为 `NOT_APPLICABLE`；禁止读取 hostname、machine-id、实例元数据或 CMDB。
- 最大本地检查 1 次、SSH 1 次、重试 0、业务请求 0、上游请求 0、费用上限 0 CNY；当前用户授权不允许运行正式 `--local-check` 或 SSH。
- 远端禁止 SFTP、上传、下载、创建、修改、删除、sudo、Docker、数据库、队列、日志、监控、备份和业务 HTTP。
- 源码注释、提交、PR 和文档使用中文；不得输出或提交密码、私钥、Token、环境变量值或远端实际摘要。
- 当前授权止于实现、测试、评审、CI 和 Draft PR；禁止自动转 Ready、合并、删除远端分支或连接测试服。

---

### Task 1: 冻结 012 常量、九键三态和严格解析器

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py`
- Create: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py`

**Interfaces:**
- Consumes: 012 设计文档中的 ChangeId、固定路径和输出枚举。
- Produces: `EvidenceError`、`build_remote_program() -> str`、`parse_remote_output(stdout: str) -> dict[str, str]`。

- [ ] **Step 1: 写入普通 Python 拒绝、物理身份禁用和 ABSENT 解析 RED 测试**

```python
SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-drop-staging-evidence-012.py")

def test_remote_program_omits_physical_host_identity(self):
    program = MODULE.build_remote_program()
    self.assertNotIn("/etc/machine-id", program)
    self.assertNotIn("HOSTNAME=", program)
    self.assertNotIn("os.uname", program)
    self.assertNotIn("instance-id", program)
    self.assertIn("/home/pc/molin", program)

def test_parser_accepts_only_exact_absent_state(self):
    values = MODULE.parse_remote_output(self.remote_output("ABSENT", "NOT_APPLICABLE", "NONE"))
    self.assertEqual(values["TARGET_CHANGE_ID"], MODULE.TARGET_CHANGE_ID)
    self.assertEqual(values["STAGING_STATE"], "ABSENT")
```

- [ ] **Step 2: 运行测试并确认 RED**

Run:

```powershell
$env:PYTHONPYCACHEPREFIX = Join-Path $env:TEMP 'g8-012-red-pycache'
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
```

Expected: FAIL，原因是生产脚本或接口不存在，不得是测试语法错误。

- [ ] **Step 3: 实现最小常量、远端骨架和严格解析器**

```python
#!/usr/bin/env python3
"""对 Drop 映射入口执行一次完全只读的 011 暂存低敏取证。"""

import sys

if not sys.flags.isolated:
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import re

CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
EXPECTED_REMOTE_KEYS = frozenset({
    "EVIDENCE_CHANGE_ID", "TARGET_CHANGE_ID", "LOGIN_USER",
    "DEPLOYMENT_ROOT_REALPATH", "DEPLOYMENT_ROOT_CHECK",
    "STAGING_STATE", "STAGING_INTEGRITY", "STAGING_MISMATCH_REASON",
    "EVIDENCE_RESULT",
})

class EvidenceError(RuntimeError):
    """表示没有形成完整、低敏且可验证的 012 证据。"""

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
```

`parse_remote_output` 必须要求 ASCII、精确九键、无重复、无额外键，并只接受以下组合：

```python
VALID_STATES = {
    ("ABSENT", "NOT_APPLICABLE", "NONE"),
    ("PRESENT", "PASS", "NONE"),
    *(("PRESENT", "MISMATCH", reason) for reason in (
        "PATH", "FILE_SET", "FILE_METADATA", "FILE_CONTENT",
        "MANIFEST", "RECEIPT", "READ_ERROR",
    )),
}
```

- [ ] **Step 4: 增加缺键、额外键、重复键、非 ASCII、错误 ChangeId 和非法组合测试**

每个负例必须断言抛出 `EvidenceError`，不得把未知值归入其他枚举。

- [ ] **Step 5: 运行测试并确认 GREEN**

Expected: Task 1 的契约测试全部 PASS，无 ResourceWarning。

- [ ] **Step 6: 提交契约交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
git commit -m "新增：冻结G8暂存取证012契约"
```

---

### Task 2: 实现目录描述符锚定的五文件、manifest 和回执取证

**Files:**
- Modify: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py`

**Interfaces:**
- Consumes: Task 1 的 `build_remote_program` 与输出契约。
- Produces: `FROZEN_FILES`、`FROZEN_MANIFEST` 和可在临时 POSIX 文件系统执行的完整远端程序。

- [ ] **Step 1: 写入 ABSENT、PRESENT/PASS 和八类 MISMATCH 动态 RED 测试**

测试夹具在系统临时目录创建部署根和暂存，使用当前 uid/gid，不得 mock `lstat/open/fstat/read`：

```python
def test_remote_program_reports_absent_and_present_pass(self):
    with self.posix_fixture() as fixture:
        self.assertEqual(fixture.run_remote()["STAGING_STATE"], "ABSENT")
        fixture.create_valid_stage()
        values = fixture.run_remote()
        self.assertEqual(
            (values["STAGING_STATE"], values["STAGING_INTEGRITY"], values["STAGING_MISMATCH_REASON"]),
            ("PRESENT", "PASS", "NONE"),
        )

def test_remote_program_classifies_all_mismatch_reasons(self):
    for reason, mutation in self.mismatch_mutations().items():
        with self.subTest(reason=reason), self.posix_fixture() as fixture:
            fixture.create_valid_stage()
            mutation(fixture)
            self.assertEqual(fixture.run_remote()["STAGING_MISMATCH_REASON"], reason)
```

- [ ] **Step 2: 运行测试并确认 RED**

Expected: FAIL，因为远端程序尚未检查真实文件系统。

- [ ] **Step 3: 冻结 011 五文件和 manifest**

```python
FROZEN_FILES = {
    "SHA256SUMS": ("15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f", 362, 0o600),
    "ai-gateway-reconcile": ("37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1", 13_066_129, 0o700),
    "g8-test-readonly-audit": ("308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", 18_377, 0o700),
    "manifest.env": ("763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6", 863, 0o600),
    "molin-g8-test-readonly-audit.sudoers": ("1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f", 416, 0o600),
}

FROZEN_MANIFEST = {
    "BUNDLE_FORMAT_VERSION": "1",
    "CHANGE_ID": TARGET_CHANGE_ID,
    "SOURCE_COMMIT": "099c38ed62ccd62c3c5a3b6811f1369d7f0d3084",
    "SOURCE_TREE": "c2d1252a05d031d842549345128fa7a1ffe53dc8",
    "GO_VERSION": "go1.26.5",
    "GO_BUILDER_HOST": "windows/amd64",
    "GOOS": "linux",
    "GOARCH": "amd64",
    "CGO_ENABLED": "0",
    "GO_BUILD_FLAGS": "-trimpath,-buildvcs=false",
    "AUDITOR_SHA256": "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
    "SUDOERS_SHA256": "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
    "RECONCILE_SHA256": "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
    "TARGET_SSH": "pc@8.130.9.163:10003",
    "TARGET_SSH_ED25519_FINGERPRINT": "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
    "TARGET_TRANSPORT": "DROP_SSH_INTERACTIVE_SUDO",
    "PHYSICAL_HOST_IDENTITY": "NOT_APPLICABLE",
    "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
    "RECONCILE_SIZE": "13066129",
    "REPRODUCIBLE_BUILD_COUNT": "2",
}
```

实现时必须要求 manifest 的键集合与上述字典完全相等，不得接受缺键或额外键。

- [ ] **Step 4: 实现远端只读算法**

远端程序按以下顺序执行：

```python
root_meta = os.lstat(deployment_root)
if os.path.realpath(deployment_root) != deployment_root:
    raise SystemExit(41)
root_fd = os.open(deployment_root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
try:
    pinned_root = os.fstat(root_fd)
    # 核验目录类型、dev/ino、pc:pc、组和其他不可写。
    # 只以固定 basename 和 root_fd 查询暂存。
    # 暂存存在时固定 stage_fd，并核验 pc:pc:0700。
    # 五文件逐项 O_NOFOLLOW 打开，哈希前后复核完整元数据。
    # 精确解析 SHA256SUMS 和 manifest.env。
    # 重新列出文件集合，并复核文件、stage 和 root 的最终目录项。
finally:
    os.close(root_fd)
```

元数据不合规时不得读取内容。路径或竞态归 `PATH`，集合归 `FILE_SET`，类型/属主/权限/大小归 `FILE_METADATA`，制品摘要归 `FILE_CONTENT`，manifest 归 `MANIFEST`，SHA256SUMS 自身或四项映射归 `RECEIPT`，系统读取异常归 `READ_ERROR`。

- [ ] **Step 5: 增加部署根、暂存目录和文件同名替换竞态测试**

使用测试注入点在打开后或哈希后执行真实 `rename/chmod/replace`；结果必须是退出 41 或 `PRESENT/MISMATCH/PATH`，不得输出 PASS。

- [ ] **Step 6: 在 Linux 无网络容器运行 GREEN**

```powershell
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
```

Expected: 所有 POSIX 动态测试执行且 PASS，无 skip、网络访问或 ResourceWarning。

- [ ] **Step 7: 提交只读引擎交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
git commit -m "新增：实现G8暂存取证012只读引擎"
```

---

### Task 3: 实现本地信任冻结、有界输出和单次 SSH

**Files:**
- Modify: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py`

**Interfaces:**
- Consumes: Task 2 的远端程序与解析器。
- Produces: `FileEvidence`、`StreamCapture`、`freeze_local_inputs(...)`、`collect_stream(...)`、`run_once(...)`、`main() -> int`。

- [ ] **Step 1: 写入本地材料、known_hosts 唯一性和 local-check RED 测试**

```python
def test_known_hosts_rejects_plain_hashed_and_other_algorithm_duplicates(self):
    for mutation in ("extra_ed25519", "hashed_ed25519", "rsa", "ecdsa", "wrong_endpoint"):
        with self.subTest(mutation=mutation), self.identity_fixture(mutation) as fixture:
            with self.assertRaises(MODULE.EvidenceError):
                MODULE.freeze_local_inputs(**fixture.paths)

def test_local_check_never_starts_ssh(self):
    with mock.patch.object(MODULE, "run_once") as run_once:
        code, stdout, stderr = self.call_main("--local-check", *self.valid_arguments())
    self.assertEqual((code, stderr), (0, ""))
    self.assertEqual(stdout, "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_LOCAL_CHECK=PASS\n")
    run_once.assert_not_called()
```

- [ ] **Step 2: 运行测试并确认 RED**

Expected: FAIL，因为本地信任接口尚未实现。

- [ ] **Step 3: 实现本地文件证据与 known_hosts/密钥语义校验**

```python
@dataclass(frozen=True)
class FileEvidence:
    path: Path
    device: int
    inode: int
    mode: int
    size: int
    mtime_ns: int
    ctime_ns: int
    sha256: str

TARGET_HOST_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
```

`freeze_local_inputs` 必须先固定系统 `ssh.exe/ssh`、`ssh-keygen.exe/ssh-keygen`、known_hosts、私钥、公钥的普通文件类型、非 reparse/非链接、绝对路径、dev/ino、mode、size、mtime/ctime 和 SHA，再执行语义校验。`ssh-keygen -F '[8.130.9.163]:10003' -f` 的非注释命中必须恰好一条 ED25519 且指纹匹配；SSH 只消费由该行派生的临时单行 known_hosts。客户端公钥指纹和公私钥匹配必须精确，私钥不得复制或 chmod。

- [ ] **Step 4: 写入 SSH 后本地材料漂移和有界采集 RED 测试**

```python
def test_post_ssh_local_material_drift_is_rejected(self):
    def mutate_after_process(*args, **kwargs):
        self.known_hosts.write_text(self.changed_known_hosts, encoding="ascii")
        return self.successful_process()
    with mock.patch.object(MODULE.subprocess, "Popen", side_effect=mutate_after_process):
        with self.assertRaises(MODULE.EvidenceError):
            MODULE.run_once(self.inputs)

def test_stream_capture_keeps_limit_plus_one_but_hashes_full_stream(self):
    payload = b"x" * (192 * 1024)
    capture = MODULE.collect_stream(io.BytesIO(payload), 64 * 1024)
    self.assertEqual(len(capture.data), 64 * 1024 + 1)
    self.assertEqual(capture.byte_count, len(payload))
    self.assertEqual(capture.sha256, hashlib.sha256(payload).hexdigest())
    self.assertTrue(capture.exceeded)
```

- [ ] **Step 5: 实现有界双流和唯一 OpenSSH**

```python
@dataclass(frozen=True)
class StreamCapture:
    data: bytes
    byte_count: int
    line_count: int
    sha256: str
    exceeded: bool
    error: bool
```

`collect_stream` 每次读取 8192 字节，保留上限加一字节，同时累计完整长度、逻辑行数和 SHA；读取异常只设置内部 `error`。`run_once` 只调用一次 `subprocess.Popen`，双线程并发排空 stdout/stderr，并固定：

```text
-F none
-p 10003
BatchMode=yes
IdentitiesOnly=yes
ConnectionAttempts=1
StrictHostKeyChecking=yes
HostKeyAlgorithms=ssh-ed25519
PasswordAuthentication=no
KbdInteractiveAuthentication=no
ForwardAgent=no
ClearAllForwardings=yes
RequestTTY=no
```

远端命令固定为 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`。SSH 非零、stderr 非空、超限、流异常、本地材料漂移或输出契约异常统一抛 `EvidenceError`，不回显正文。

- [ ] **Step 6: 实现 CLI、稳定状态码和消费门禁**

未消费时：self-test 仅编译代码和验证固定常量；local-check 只做离线材料校验；正式入口只调用 `run_once` 一次。PASS=0、MISMATCH=3、FAILED=2。

消费后，`main` 必须在构造 argparse 前执行：

```python
if CHANGE_ID_CONSUMED:
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=change_id_consumed")
    return 2
```

测试覆盖空参数、`--help`、未知参数、缺值、含敏感哨兵、self-test、local-check 和正式入口，精确断言 stdout 单行、stderr 空、哨兵不回显、没有材料或网络调用。

- [ ] **Step 7: 运行 Windows 与 Linux GREEN**

```powershell
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py --self-test
```

- [ ] **Step 8: 提交包装器交付**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
git commit -m "新增：完成G8暂存取证012包装器"
```

---

### Task 4: 接入分级 CI、中文文档和独立授权清单

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/tools.md`
- Create: `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-012.md`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py`

**Interfaces:**
- Consumes: Task 3 的脚本、测试和实际脚本 SHA。
- Produces: G8 CI 门禁、`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL` 授权清单和一致状态文档。

- [ ] **Step 1: 写入 CI 静态契约 RED 测试**

```python
def test_ci_runs_012_windows_and_linux_no_network_gates(self):
    workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
    self.assertIn("test_run_ai_gateway_g8_test_drop_staging_evidence_012.py", workflow)
    self.assertIn("run-ai-gateway-g8-test-drop-staging-evidence-012.py --self-test", workflow)
    self.assertIn("python:3.13-alpine", workflow)
    self.assertIn("--network none", workflow)
```

Run 单测，Expected: FAIL，因为 CI 尚未包含 012。

- [ ] **Step 2: 在 G8 生产就绪 Job 加入 012 门禁**

```yaml
- name: 验证 G8 Drop 暂存取证 012
  run: |
    python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
    python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
    python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py --self-test
    docker run --rm --network none \
      -v "${{ github.workspace }}:/repo:ro" -w /repo python:3.13-alpine \
      python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
```

不得在 CI 传入正式 ChangeId、known_hosts 或密钥路径，不得真实 SSH。

- [ ] **Step 3: 实算脚本 SHA 并编写 012 授权清单**

```powershell
$scriptSha = (Get-FileHash -Algorithm SHA256 infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py).Hash.ToLowerInvariant()
```

授权清单冻结精确 ChangeId、目标、端点/客户端指纹、部署根、暂存路径、五文件摘要/大小/权限、完整 manifest、回执、脚本 SHA、一次 local-check、一次 SSH、零重试、0/0/0 CNY、系统日志/atime 影响、无应用层回滚及全部停止条件。精确最终 PR HEAD 在提交产生后写入 Draft PR 正文或固定评论，避免仓内文档自引用；HEAD 漂移时必须重签。当前状态只能为 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`，并明确所有命令当前禁止执行。

- [ ] **Step 4: 同步 README、G8 验收、Runbook、测试计划和工具文档**

文档统一记录：011 暂存仍是 `UNKNOWN`；012 只是未执行工程候选；001 至 011 均消费；012 结果不自动授权清理、安装或运行态审计；API、schema、数据库、Bifrost、监控、备份和账务门禁不因暂存证据关闭；`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 未完成。

- [ ] **Step 5: 运行 CI、文档和敏感信息本地门禁**

```powershell
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main
git diff --check origin/main...HEAD
```

Expected: 测试全绿，敏感扫描 `findings=0`，未执行真实 `--local-check` 或 SSH。

- [ ] **Step 6: 提交 CI 与文档交付**

```powershell
git add .github/workflows/ci.yml README.md docs/ai-gateway-g8-acceptance.md docs/ai-gateway-g8-test-readonly-access-runbook.md docs/test-plan.md docs/tools.md docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-012.md infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
git commit -m "文档：接入G8暂存取证012门禁"
```

---

### Task 5: 完成本地门禁、独立评审、CI 和 Draft PR

**Files:**
- Modify only for verified evidence corrections: `README.md`
- Modify only for verified evidence corrections: `docs/ai-gateway-g8-acceptance.md`
- Modify only for verified evidence corrections: `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-012.md`

**Interfaces:**
- Consumes: Tasks 1–4 的完整分支和精确 HEAD。
- Produces: 本地门禁证据、独立安全/QA/产品结论、精确 HEAD CI 与 Draft PR；不合并。

- [ ] **Step 1: 运行最终本地门禁**

```powershell
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py -v
python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_012.py
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py --self-test
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main
git diff --check origin/main...HEAD
```

不得运行 `--local-check` 或正式模式。

- [ ] **Step 2: 固定精确 HEAD 并组织独立三方门禁**

记录 `git rev-parse HEAD`。代码安全评审、QA 和产品/规格验收必须绑定同一 HEAD，明确仓库 P0/P1/P2、未连接测试服、011 仍 UNKNOWN、012 未执行及非生产/非商业边界。任一 P0/P1 必须先修复，并对新 HEAD 全部复签。

- [ ] **Step 3: 推送并创建中文 Draft PR**

```powershell
git push -u origin feature/backend-d-ai-gateway-g8-staging-evidence-012-design
gh pr create --draft --base main --head feature/backend-d-ai-gateway-g8-staging-evidence-012-design --title "[AI网关] 增加Drop暂存只读取证012" --body-file docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-012.md
```

PR 正文补充精确 HEAD、本地测试、脚本 SHA、独立评审和非执行边界，不得声称已获测试服授权。

- [ ] **Step 4: 等待精确 PR HEAD 适用 CI 全绿**

```powershell
gh pr checks --watch --interval 20
```

Expected: 因包含 `.github/**` 和 `infra/**`，分类器应触发完整适用门禁；G8 生产就绪和 required 汇总必须成功。CI 平台外部失败必须单列，不得当作测试通过。

- [ ] **Step 5: 收口同 HEAD 评审并停止在合并授权门禁**

若 CI 或评审修复产生新提交，重跑本地门禁和三方增量复评。全部 P0/P1/P2=0 且 CI 全绿后，向用户提交精确 HEAD、CI run、脚本 SHA、测试、影响、回滚和停止条件，请求 Draft→Ready 与 merge commit 授权。

当前授权不允许执行 `gh pr ready`、`gh pr merge`、删除远端分支、连接测试服、清理暂存或安装。

---

## 计划自审结论

- 规格覆盖：Task 1 覆盖九键三态，Task 2 覆盖五文件、manifest、回执和竞态，Task 3 覆盖本地信任、单 SSH、有界输出与消费门禁，Task 4 覆盖 CI/文档/授权，Task 5 覆盖独立验收和 Draft PR。
- 类型一致：`build_remote_program`、`parse_remote_output`、`freeze_local_inputs`、`collect_stream`、`run_once` 和 `main` 的生产者与消费者一致。
- 范围一致：计划没有真实 local-check、测试服连接、上传、下载、清理、安装、sudo、生产、付费调用、通知或客户灰度步骤。
- 授权一致：计划在 Draft PR、精确 HEAD CI 和独立评审后停止，等待用户另行授权合并。
- 无占位符：所有 ChangeId、路径、摘要、大小、状态、命令和输出枚举均为精确值；脚本 SHA 只能实现后实算写入，不允许猜测。

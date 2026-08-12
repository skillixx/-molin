# G8 测试服主机身份低敏诊断 007 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现一个在独立授权后最多执行一次只读 SSH 的 007 诊断器，仅返回 `READABLE_MATCH`、`READABLE_MISMATCH` 或 `UNREADABLE`，且绝不输出当前 machine-id 原文或摘要。

**Architecture:** 新脚本复用并冻结已合并的 004 helper 来完成 known_hosts、密钥、SSH 目标与最小环境校验；远端隔离 Python 只读取固定 `/etc/machine-id`，在内存比较仓库既有批准摘要并输出精确三键协议。本地包装器采用有界双流排空、精确键集解析、一次性消费门禁和固定低敏错误，工程合并不构成远端执行授权。

**Tech Stack:** Python 3 标准库、`unittest`、OpenSSH、GitHub Actions、Markdown。

## Global Constraints

- ChangeId 固定为 `CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007`。
- 目标固定为 `pc@8.130.9.163:10003`，部署根保持 `/home/pc/molin`，但 007 不读取部署根或暂存目录。
- 本地检查最多 1 次、只读 SSH 最多 1 次、`ConnectionAttempts=1`、重试 0 次。
- 业务请求 0、上游请求 0、费用上限 0 CNY。
- 任何输出、日志、提交、PR、CI 和文档均不得出现当前 machine-id 原文或摘要。
- 只允许 `READABLE_MATCH`、`READABLE_MISMATCH`、`UNREADABLE` 三种远端状态；协议异常统一低敏失败关闭。
- 工程合并后授权清单只能处于 `PENDING_USER_APPROVAL`；用户再次明确批准前禁止连接测试服务器。
- 不连接生产服务器，不执行真实付费上游、真实通知、客户灰度或商业观察。

---

### Task 1: 用失败测试锁定 007 协议与消费边界

**Files:**
- Create: `infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py`
- Create: `infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py`

**Interfaces:**
- Consumes: 004 helper 的 `validate_known_hosts`、`validate_identity_file`、`validate_identity_pair`、`fixed_ssh_executable`、`fixed_ssh_environment`。
- Produces: `build_remote_program(machine_id_path: str = "/etc/machine-id") -> str`、`parse_remote_output(stdout: bytes) -> str`、`run_once(...) -> tuple[int, dict[str, object], dict[str, object]]`、`main() -> int`。

- [ ] **Step 1: 写远端三态与不泄漏测试**

```python
def test_remote_program_returns_three_states_without_identifier_or_digest(self) -> None:
    cases = ((b"approved\n", "READABLE_MATCH"), (b"changed\n", "READABLE_MISMATCH"), (None, "UNREADABLE"))
    for content, expected in cases:
        result = run_remote_fixture(content)
        self.assertEqual(result["MACHINE_ID_STATE"], expected)
        self.assertNotIn("approved", result["stdout"])
        self.assertNotRegex(result["stdout"], r"[0-9a-f]{64}")
```

- [ ] **Step 2: 运行单测并确认因 007 脚本不存在而失败**

Run: `python -I -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v`

Expected: FAIL，原因是目标脚本或接口尚不存在，而非测试语法错误。

- [ ] **Step 3: 写精确协议、一次 SSH、有界输出和消费负例**

```python
def test_parser_rejects_wrong_or_extra_keys(self) -> None:
    invalid = b"DIAGNOSTIC_CHANGE_ID=WRONG\nTARGET_CHANGE_ID=WRONG\nMACHINE_ID_STATE=READABLE_MATCH\nEXTRA=1\n"
    with self.assertRaises(MODULE.DiagnosticError):
        MODULE.parse_remote_output(invalid)

def test_consumed_change_rejects_before_helper_identity_or_network(self) -> None:
    with mock.patch.object(sys, "argv", [str(SCRIPT_PATH)]), mock.patch.object(MODULE, "load_staging_helper") as helper, mock.patch.object(MODULE, "run_once") as remote:
        self.assertEqual(MODULE.main(), 2)
    helper.assert_not_called()
    remote.assert_not_called()
```

- [ ] **Step 4: 再次运行并确认新增测试仍为预期 RED**

Run: `python -I -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v`

Expected: FAIL，缺少生产实现。

- [ ] **Step 5: 提交测试骨架**

```powershell
git add infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py
git commit -m "测试：锁定G8主机身份诊断007契约" -m "影响模块：infra/scripts"
```

### Task 2: 最小实现远端三态和本地失败关闭包装器

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py`

**Interfaces:**
- Consumes: Task 1 的行为测试和冻结 004 helper SHA-256。
- Produces: 可离线 `--self-test`、`--local-check` 以及需后续授权的单次正式调用入口。

- [ ] **Step 1: 写最小远端程序生成器**

```python
def build_remote_program(machine_id_path: str = "/etc/machine-id") -> str:
    return REMOTE_PROGRAM_TEMPLATE.replace("__MACHINE_ID_PATH__", repr(machine_id_path))
```

远端程序必须以二进制方式最多读取 4097 字节；空、超限、打开/读取/关闭/摘要异常归类 `UNREADABLE`；正常内容只在内存比较冻结摘要并输出精确三键。

- [ ] **Step 2: 实现精确输出解析器**

```python
def parse_remote_output(stdout: bytes) -> str:
    values = parse_ascii_key_values(stdout)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise DiagnosticError("remote_contract_mismatch")
    if values["DIAGNOSTIC_CHANGE_ID"] != CHANGE_ID or values["TARGET_CHANGE_ID"] != TARGET_CHANGE_ID:
        raise DiagnosticError("remote_contract_mismatch")
    state = values["MACHINE_ID_STATE"]
    if state not in MACHINE_ID_STATES:
        raise DiagnosticError("remote_contract_mismatch")
    return state
```

- [ ] **Step 3: 实现固定一次 SSH 和有界双流采集**

复用 005 已验证的 `collect_stream`/`run_bounded_process` 结构；命令必须包含 `-F none`、`ConnectionAttempts=1`、禁密码/交互/代理/X11/转发/TTY，并以 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -` 执行 stdin 程序。

- [ ] **Step 4: 实现低敏主入口**

```python
if CHANGE_ID_CONSUMED:
    print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=change_id_consumed")
    return 2
```

`--local-check` 只完成本地身份门禁；正式路径只允许一次 `run_once`。`READABLE_MATCH` 返回 0；另两态返回 3；传输或协议异常仅输出 `FAILED reason=diagnostic_unavailable` 并返回 2。

- [ ] **Step 5: 运行单测确认 GREEN**

Run: `python -I -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v`

Expected: 全部 PASS，无 traceback、ResourceWarning 或敏感正文。

- [ ] **Step 6: 执行 mutation 检查**

临时改变状态白名单、删除 ChangeId 校验、允许第二次 `run_once`、输出摘要中的任一项时，至少一个测试必须失败；恢复后重跑全绿。

- [ ] **Step 7: 提交最小实现**

```powershell
git add infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py
git commit -m "新增：实现G8主机身份低敏诊断007" -m "影响模块：infra/scripts"
```

### Task 3: 接入分级 CI 与 Linux 隔离验证

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py`

**Interfaces:**
- Consumes: Task 2 的脚本与测试。
- Produces: G8 生产就绪适用门禁中的 Windows/Linux 等价证据，不触发真实 SSH。

- [ ] **Step 1: 增加 Linux-only 临时只读文件三态测试**

测试必须在临时目录中构造匹配、非匹配、缺失、空、4097 字节和读取异常夹具，执行真实远端程序；CI 使用 `--network none` 或等价无网络环境。

- [ ] **Step 2: 运行测试确认新增 Linux 用例在 Windows 跳过、在 Linux 全执行**

Run: `python -I -W error::ResourceWarning -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v`

Expected: Windows 仅 Linux 动态用例明确 SKIP，其余 PASS。

- [ ] **Step 3: 将测试、自检和禁止联网断言加入 G8 CI**

```yaml
- name: 验证 G8 主机身份低敏诊断 007
  run: |
    python -I -W error::ResourceWarning -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v
    python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py
    python -I infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py --self-test
```

CI 只能运行 `--self-test` 和测试，不得传正式 ChangeId、known_hosts 或身份文件，不得连接测试服。

- [ ] **Step 4: 校验 YAML、差异和脚本自检**

Run: `python -I infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py --self-test`

Run: `git diff --check`

Expected: 均为 exit 0。

- [ ] **Step 5: 提交 CI 门禁**

```powershell
git add .github/workflows/ci.yml infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py
git commit -m "测试：接入G8主机身份诊断007门禁" -m "影响模块：CI、infra/scripts"
```

### Task 4: 同步授权清单、Runbook 和验收边界

**Files:**
- Create: `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-authorization-20260812-007.md`
- Modify: `README.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/tools.md`

**Interfaces:**
- Consumes: 精确脚本 SHA-256、候选 HEAD、测试数量和 CI 证据。
- Produces: 状态为 `PENDING_USER_APPROVAL` 的独立执行授权清单。

- [ ] **Step 1: 编写 007 授权清单**

清单必须绑定：ChangeId、`pc@8.130.9.163:10003`、精确脚本/helper SHA、known_hosts 与本地公钥指纹、命令摘要、最多一次 local-check/SSH、0/0/0 CNY、三态停止条件，以及禁止暂存读取、上传、sudo、生产和商业动作。

- [ ] **Step 2: 更新状态文档**

文档明确 006 已消费且结果仅为 `BLOCKED/MACHINE_ID`；007 只是工程候选，未获单次 SSH 授权；`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 未完成。

- [ ] **Step 3: 计算并写入精确摘要**

Run: `Get-FileHash -Algorithm SHA256 infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py`

Expected: 64 位小写十六进制摘要只作为候选绑定证据，不包含当前 machine-id 摘要。

- [ ] **Step 4: 执行文档和敏感信息检查**

Run: `git diff --check`

Run: `rg -n "BEGIN (RSA |OPENSSH )?PRIVATE KEY|Bearer [A-Za-z0-9._-]+|AKIA[0-9A-Z]{16}" README.md docs infra/scripts .github/workflows/ci.yml`

Expected: diff check exit 0；无真实敏感信息命中。

- [ ] **Step 5: 提交中文文档**

```powershell
git add README.md docs .github/workflows/ci.yml infra/scripts
git commit -m "文档：准备G8主机身份诊断007授权清单" -m "影响模块：README、G8文档、测试计划、工具文档"
```

### Task 5: 完整验证、独立门禁与合并

**Files:**
- Verify: all files changed since `origin/main`

**Interfaces:**
- Consumes: Tasks 1-4 的精确 HEAD。
- Produces: P0/P1=0 的独立评审、QA、产品结论，PR merge commit 和远端分支删除证据。

- [ ] **Step 1: 执行适用的本地分级门禁**

Run: `python -I -W error::ResourceWarning -m unittest infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py -v`

Run: `python -I -m py_compile infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py infra/scripts/test_run_ai_gateway_g8_test_host_identity_diagnostic.py`

Run: `python -I infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py --self-test`

Run: `git diff --check origin/main...HEAD`

Expected: 全部 exit 0；不执行正式 SSH。

- [ ] **Step 2: 独立代码安全、QA、产品验收**

三方必须绑定同一精确 HEAD，并分别确认 P0=0、P1=0；任何 P0/P1 必须修复并对新 HEAD 复评。

- [ ] **Step 3: 推送并创建中文 Draft PR**

```powershell
git push -u origin feature/backend-d-ai-gateway-g8-host-identity-diagnostic-007-design
gh pr create --draft --base main --head feature/backend-d-ai-gateway-g8-host-identity-diagnostic-007-design --title "新增：G8测试服主机身份低敏诊断007" --body-file <已生成的中文PR说明文件>
```

- [ ] **Step 4: 等待精确 PR HEAD 的适用 CI 与必选汇总成功**

CI 必须按变更范围执行分类后的适用门禁；不得以旧 HEAD 的成功结果替代最新 HEAD。

- [ ] **Step 5: 转 Ready 并按 merge commit 合并**

```powershell
gh pr ready <PR编号>
gh pr merge <PR编号> --merge --delete-branch
```

禁止 squash；合并后核对 merge commit 为双父提交、`origin/main` 已包含功能 HEAD、远端功能分支已删除。

- [ ] **Step 6: 提交独立 007 SSH 授权清单**

只报告候选脚本 SHA、合并提交、CI、精确命令摘要、影响、回滚和停止条件；状态保持 `PENDING_USER_APPROVAL`。用户再次批准前不得运行 local-check 或连接测试服务器。

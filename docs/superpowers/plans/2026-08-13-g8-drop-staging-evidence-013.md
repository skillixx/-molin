# G8 Drop 暂存只读取证 013 实施计划

> **执行要求：** 严格按 TDD 完成每个任务：先写公开行为测试并确认 RED，再写最小实现确认 GREEN，最后重构和提交。整个计划禁止连接测试服。

**目标：** 交付无 ChangeId、可重复且无远端能力的本地身份材料诊断器，以及未来独立授权后最多执行一次只读 SSH 的 013 暂存取证器；同时形成后续清理、安装、部署和运行态验收状态机。

**架构：** 本地诊断器独立冻结和核验 OpenSSH 身份材料，只调用本地 `ssh-keygen`。013 包装器按摘要加载该诊断模块，复用其本地材料证据，但独立实现单次 SSH、远端 fd 锚定只读算法、九键解析和消费门禁。两者不导入或调用已消费的 012。

**技术栈：** Python 3.13、`unittest`、Windows OpenSSH、POSIX 文件描述符、GitHub Actions、Docker `--network none`。

---

## Task 1：冻结本地诊断器契约

**Files:**
- Create: `infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
- Create: `infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py`

### Step 1：写 CLI、无 ChangeId 和无远端能力 RED 测试

测试要求：

- `--self-test` 在隔离 Python 中成功。
- 正式调用只接受 known_hosts、私钥、公钥三个绝对路径。
- 重复调用不改变任何 ChangeId 或文件。
- 源码 AST/文本不得出现 SSH/SFTP/SCP 执行、socket、远端命令或目标登录能力。
- 空参数、help、未知参数、畸形参数和敏感哨兵均只输出固定低敏失败，stderr 为空。

运行：

```powershell
python -I -W error::ResourceWarning infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py -v
```

Expected: RED，模块尚不存在。

### Step 2：实现最小 CLI 和固定输出

实现 `main() -> int`、安全参数解析、固定枚举和 `--self-test`。源码注释使用中文。失败不得回显参数、路径或异常正文。

运行同一测试，Expected: CLI 相关用例 GREEN，其余材料用例尚未实现。

### Step 3：提交契约骨架

```powershell
git add infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py
git commit -m "新增：定义G8本地身份材料诊断契约"
```

---

## Task 2：以 fd 冻结本地材料并完成语义校验

**Files:**
- Modify: `infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
- Modify: `infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py`

### Step 1：写绝对路径和 TOCTOU RED 测试

覆盖：

- 相对路径、链接、目录、设备文件、Windows reparse point 拒绝。
- `lstat -> open` 同名替换、读取中替换、读取后恢复、mtime/ctime/size/inode 漂移拒绝。
- 读取失败、close 失败和摘要异常统一低敏失败。
- 不修改 ACL、权限或源文件。

Expected: RED。

### Step 2：实现 `FileEvidence` 与 `freeze_file(...)`

接口：

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
    data: bytes
```

使用 `os.open`、`O_NOFOLLOW`、同一 fd 分块读取、前后 `fstat` 与最终 `lstat`。Windows 补充 reparse 检查。只在内存保存数据，不写磁盘。

### Step 3：写 known_hosts 和密钥语义 RED 测试

临时生成 ED25519 测试密钥，覆盖：

- 固定端点恰一条批准 ED25519。
- 明文和哈希重复 ED25519 拒绝。
- 错误 ED25519 指纹拒绝。
- RSA/ECDSA 与批准 ED25519 共存时通过，但派生材料只保留批准 ED25519。
- 错误端点、未知格式、非 ASCII 拒绝。
- 公私钥匹配、错配、批准客户端指纹不符。
- `ssh-keygen` 非零、stderr、超限、超时和输出异常。

Expected: RED。

### Step 4：实现本地 `ssh-keygen` 有界调用和语义校验

仅调用冻结的系统 `ssh-keygen`，参数数组执行、无 shell、最小环境。实现：

- `find_approved_host_key(...)`
- `validate_identity_pair(...)`
- `diagnose_materials(...)`
- `assert_materials_unchanged(...)`

不输出实际指纹或文件摘要。

### Step 5：Windows GREEN 与重复执行

```powershell
python -I -W error::ResourceWarning infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py -v
python -I -m py_compile infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py
python -I infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py --self-test
```

### Step 6：提交本地诊断器

```powershell
git add infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py
git commit -m "新增：实现无ChangeId本地身份材料诊断"
```

---

## Task 3：实现 013 远端三态程序和解析器

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`
- Create: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py`

### Step 1：写固定常量、九键和三态 RED 测试

锁定：

- 013 ChangeId、011 目标、Drop 端点、部署根和五文件清单。
- 精确九键 ASCII 输出，无缺失、重复、额外键。
- `ABSENT/NOT_APPLICABLE/NONE`、`PRESENT/PASS/NONE`、`PRESENT/MISMATCH/固定原因`。
- 六行本地低敏输出和退出码 0/3。
- 错误 ChangeId、路径、用户、枚举和状态组合拒绝。

Expected: RED。

### Step 2：实现独立远端程序构造和解析器

实现：

- `build_remote_program() -> str`
- `parse_remote_output(data: bytes) -> EvidenceResult`
- `render_result(result) -> tuple[int, str]`

不导入或运行 012。

### Step 3：写文件、manifest、回执动态 RED 测试

在临时 POSIX 目录创建完整五文件夹具，覆盖：

- ABSENT 与完整 PRESENT/PASS。
- PATH、FILE_SET、FILE_METADATA、FILE_CONTENT、MANIFEST、RECEIPT、READ_ERROR。
- stage symlink -> PATH；文件 symlink -> FILE_METADATA。
- 类型/属主/权限/大小不合规时不读取内容。

Expected: RED。

### Step 4：实现 fd 锚定只读算法

固定部署根和 stage fd；每文件 `lstat/open/fstat/hash/fstat/lstat`；最终重列集合并复核 root/stage/file 完整元数据。所有 fd 使用 `finally` 关闭。远端程序无写入、子进程、socket、sudo 或 shell 能力。

### Step 5：写竞态 RED 测试并关闭

动态注入：

- 部署根同名替换或 chmod/chown 漂移。
- stage 在 stat/open 之间替换或权限漂移。
- 文件哈希后 rename + 同名替换 + 额外 `.old`。
- 文件读取中 chmod、truncate 或目录项恢复。

Expected: 不得输出 PASS；归 PATH 或无证据退出。

### Step 6：Linux 断网 GREEN

```powershell
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py -v
```

### Step 7：提交远端只读引擎

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py
git commit -m "新增：实现G8暂存取证013只读引擎"
```

---

## Task 4：实现 helper 冻结、单 SSH 和消费门禁

**Files:**
- Modify: `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py`

### Step 1：写 helper 冻结 RED 测试

覆盖本地诊断器路径非普通文件、inode 替换、摘要漂移、常量/函数契约漂移，全部在身份材料和网络前失败。

### Step 2：实现 `load_frozen_local_diagnostic(...)`

按实现后实算 SHA 冻结 helper；使用 fd 读取、前后元数据和最终目录项复核；对已验证字节 `compile/exec`，断言固定常量和函数集合。

### Step 3：写有界双流和单 SSH RED 测试

覆盖：

- 精确 64 KiB、64 KiB+1 和 192 KiB。
- 完整字节数、逻辑行数和 SHA 累计。
- stdout/stderr 管道读取异常。
- 非零、任意 stderr、超时、超限、输出不匹配。
- 正式路径恰好一个 `Popen`，无重试，固定 OpenSSH 参数和最小环境。
- SSH 后本地材料漂移失败关闭。

### Step 4：实现 `StreamCapture`、`collect_stream` 和 `run_once`

双线程并发排空；每流只保留上限加一，异常只设置内部标志。正式 SSH 只使用批准 ED25519 派生的临时单行 known_hosts 和显式私钥原路径。

### Step 5：写 CLI 与消费转换 RED 测试

初始状态：

- `--self-test` 仅离线编译和冻结契约。
- 不接受 `--local-check`。
- 正式请求必须是精确 013 ChangeId 和三个绝对身份路径。

消费转换夹具必须证明 `CHANGE_ID_CONSUMED=True` 时，空参数、help、未知、畸形、敏感哨兵、self-test 和正式入口都在 argparse/helper/材料/网络前固定拒绝。

### Step 6：实现 CLI 并完成 GREEN

失败统一为：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=FAILED reason=evidence_unavailable
```

本轮测试不得运行正式请求或真实身份材料。

### Step 7：提交包装器

```powershell
git add infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py
git commit -m "新增：完成G8暂存取证013单次包装器"
```

---

## Task 5：接入 CI、授权清单与验收状态机

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/tools.md`
- Create: `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-013.md`
- Modify: relevant CI contract tests under `infra/scripts/`

### Step 1：写 CI 契约 RED 测试

要求 Draft 选择器能发现两个新测试；Ready G8 重型门禁包含：

- Windows 本地诊断测试、013 测试、py_compile 和两个 self-test。
- Linux `--network none` 两套测试。
- 不传真实 known_hosts、私钥、公钥或正式 013 ChangeId。
- 不运行 SSH/SFTP。

Expected: RED。

### Step 2：最小修改 CI 达成 GREEN

保留 PR #373 的 Draft/Ready 互斥和 concurrency；不删除现有测试。Draft 只执行分级定向门禁，Ready 才执行完整 G8 重型门禁。

### Step 3：实算 SHA 并编写 013 授权清单

实算本地诊断器和 013 包装器 SHA/大小。授权清单状态为 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`，冻结：

- ChangeId、目标 011、端点、部署根和暂存路径。
- 五文件摘要/大小/权限、manifest 和回执。
- 两脚本 SHA/大小、固定命令和最大 SSH 次数 1。
- 业务/上游/费用 0/0/0 CNY。
- 影响、日志/atime 边界、停止条件和无应用层回滚。
- 未获用户再次明确批准前禁止运行本地真实诊断和正式 013。

### Step 4：同步中文文档

统一记录：001–012 consumed、011 staging UNKNOWN、013 是未执行候选；本地诊断无 ChangeId且不联网；后续清理/安装/部署/运行态审计分别独立授权；商业验收未完成。

### Step 5：提交 CI 和文档

```powershell
git add .github/workflows/ci.yml README.md docs infra/scripts
git commit -m "文档：接入G8暂存取证013工程门禁"
```

---

## Task 6：完整本地验证与独立评审

### Step 1：Windows 验证

```powershell
python -I -W error::ResourceWarning infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py -v
python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py -v
python -I -m py_compile infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py
python -I infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py --self-test
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py --self-test
```

### Step 2：Linux 断网验证

```powershell
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_diagnose_ai_gateway_g8_local_ssh_materials.py -v
docker run --rm --network none -v "${PWD}:/repo:ro" -w /repo python:3.13-alpine python -I -W error::ResourceWarning infra/scripts/test_run_ai_gateway_g8_test_drop_staging_evidence_013.py -v
```

### Step 3：仓库质量门禁

```powershell
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main
git diff --check origin/main...HEAD
```

不得执行真实诊断参数、013 正式入口、SSH 或 SFTP。

### Step 4：独立三方评审

绑定同一精确 HEAD：

- Standards/安全：本地诊断零远端能力、helper 信任、单 SSH、TOCTOU、低敏输出和消费门禁。
- QA：Windows/Linux 断网公开行为、CI 分类、文档与候选状态。
- 产品/规格：完整范围、状态机、验收矩阵、授权和 G8 工程/商业边界。

任一 P0/P1 必须修复并对新 HEAD 全量复签；P2 必须修复或得到明确处置。

---

## Task 7：推送并创建 Draft PR

### Step 1：最终核验工作树和提交

```powershell
git status --short
git log --oneline origin/main..HEAD
git diff --check origin/main...HEAD
```

### Step 2：推送已授权分支

```powershell
git push -u origin feature/backend-d-ai-gateway-g8-staging-evidence-013
```

### Step 3：创建中文 Draft PR

```powershell
gh pr create --draft --base main --head feature/backend-d-ai-gateway-g8-staging-evidence-013 --title "[AI网关] 增加Drop暂存只读取证013" --body-file docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-013.md
```

PR 正文或固定评论必须绑定 exact base、HEAD、两脚本 SHA/大小、测试结果和“HEAD 漂移即失效”。

### Step 4：观察 Draft 分级 CI

只观察 Draft 轻量和定向门禁。不得转 Ready、合并、修改仓库设置、连接测试服或运行正式 013。

---

## 计划自审

- 设计覆盖：无 ChangeId 本地诊断、013 一次性远端取证、三态、消费门禁、后续状态机和验收矩阵均有实现或文档任务。
- TDD 覆盖：每个生产能力都先有 RED 测试，再有最小实现和 GREEN 命令。
- 权限边界：计划不运行真实本地材料诊断、不执行 SSH/SFTP、不连接测试服、不转 Ready/merge。
- 证据边界：Draft CI、完整本地断网验证、三方评审和未来 Ready 全量 CI 分开记录。
- Goal 边界：只推进 G8 测试运行态准备，不完成生产、客户灰度、四周观察或 `G8_COMMERCIAL_ACCEPTED`。

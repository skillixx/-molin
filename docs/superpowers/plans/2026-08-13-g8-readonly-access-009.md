# G8 Drop 只读入口安装候选 009 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不连接测试服的前提下，生成并冻结一个适配 Drop SSH 入口的新 009 只读审计安装候选，完成本地/CI/独立评审与安装授权清单，随后等待用户单独批准远端预检、暂存和安装。

**Architecture:** 复用已审计的 auditor、sudoers 和 Go 对账器源码，但让候选清单显式声明 `DROP_SSH`，不再绑定物理 hostname 或 machine-id。新增 009 专用 stage 包装器：本地先验证五文件、回执、known_hosts 和显式 ED25519 密钥；未来获批后才允许一次只读 SSH 预检和一次独占 SFTP 暂存，远端预检只绑定登录用户、部署根、暂存路径和 live 目标，不读取物理主机身份。

**Tech Stack:** Python 3.13 isolated mode、Go 1.26.5、OpenSSH/SFTP、GitHub Actions、Markdown。

## Global Constraints

- 当前只允许仓库代码、文档、测试、构建、独立评审、CI 和 PR；禁止连接测试服、上传、安装或修改 sudoers。
- 新 ChangeId 固定为 `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`，旧 001–008 全部禁止重放。
- 候选来源固定为 `7f3325e2d6801567fea34a2049a2f3ada114e348`，源码树固定为 `4563feb59850dca87789adfb5eea820f78b1a209`。
- Drop SSH 入口固定为 `pc@8.130.9.163:10003`，部署根固定为 `/home/pc/molin`；物理 hostname、machine-id、实例元数据和 CMDB 均不是门禁。
- 业务请求、上游请求和费用上限均为 `0 / 0 / 0 CNY`。
- 任一类型、路径、摘要、文件集、权限、stderr、超时或输出契约不符必须失败关闭；不得输出 Secret、私钥、环境变量值或业务数据。
- `G8_ENGINEERING_READY` 保持；本计划不授权生产、真实付费、通知、客户灰度或 `G8_COMMERCIAL_ACCEPTED`。

---

### Task 1: 冻结 Drop 009 候选生成契约

**Files:**
- Modify: `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py`
- Modify: `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_bundle.py`

**Interfaces:**
- Consumes: `FrozenCandidate`、`prepare(candidate, output_dir)` 与历史 `CONSUMED_CANDIDATES`。
- Produces: `ACTIVE_CANDIDATE`；普通 CLI 仅接受 009 精确 ChangeId、来源提交和全新绝对输出目录。

- [ ] **Step 1: 写失败测试**：断言普通 CLI 对 009 生成五文件，manifest 包含 `TARGET_TRANSPORT=DROP_SSH` 和 `PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE`，且不含 `TARGET_HOSTNAME`、`TARGET_MACHINE_ID_SHA256`；错误 ChangeId、来源或既存目录退出 2。
- [ ] **Step 2: 验证红灯**：运行 `python -I -m unittest infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_bundle.py -v`，预期因没有活动候选失败。
- [ ] **Step 3: 最小实现**：扩展 `FrozenCandidate` 的 Drop 契约字段，保持 001–003 历史 manifest 不漂移；增加 009 `ACTIVE_CANDIDATE`，普通入口只接受精确 009 参数。
- [ ] **Step 4: 验证绿灯与历史回执**：同一测试套件通过；分别运行 001–003 `--verify-consumed-candidate`，确认历史来源、三摘要、大小和回执保持不变。
- [ ] **Step 5: 生成本地候选**：在工作树外 `D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` 生成五文件，逐项重算 `SHA256SUMS`、bundle receipt、对账器大小与 manifest 键集合。
- [ ] **Step 6: 提交**：中文提交说明必须写明生成器、Drop manifest 与历史候选兼容边界。

### Task 2: 新增 009 专用 Drop 暂存包装器

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-readonly-access-stage-drop.py`
- Create: `infra/scripts/test_run_ai_gateway_g8_test_readonly_access_stage_drop.py`

**Interfaces:**
- Consumes: 009 五文件候选目录、known_hosts、`id_ed25519`、`id_ed25519.pub`。
- Produces: `--self-test`、`--local-check` 与未来授权后的单次预检/单次 SFTP 入口；当前 `CHANGE_ID_CONSUMED=False`。

- [ ] **Step 1: 写失败测试**：覆盖五文件/receipt/manifest、Drop 字段、物理身份键拒绝、known_hosts/密钥指纹、本地检查不启动 SSH、一次 SSH、一次 SFTP、无重试、预检与上传失败分类、stderr 失败关闭。
- [ ] **Step 2: 验证红灯**：运行新测试文件，预期因脚本不存在失败。
- [ ] **Step 3: 最小实现**：使用 isolated Python、固定 OpenSSH 绝对路径和最小环境；远端脚本只读取登录用户、部署根真实路径/元数据以及 staging/live 目标是否不存在，不读取 hostname 或 machine-id。
- [ ] **Step 4: 验证绿灯**：Windows 定向测试、Linux `python:3.13-alpine --network none`、`py_compile` 和 `--self-test` 全部通过；测试不得执行真实 SSH/SFTP。
- [ ] **Step 5: 提交**：中文提交说明写明 Drop 端点、单次调用和失败分类。

### Task 3: 冻结 009 安装授权与回滚清单

**Files:**
- Create: `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260813-009.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `README.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/tools.md`

**Interfaces:**
- Consumes: 009 五文件摘要、bundle receipt、包装器 SHA、目标路径与 sudoers 固定命令。
- Produces: `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL` 安装清单，不产生远端授权。

- [ ] **Step 1: 写精确授权清单**：列出一次 local-check、一次只读 SSH、一次独占 SFTP、一次 root 控制台安装和一次非特权 `sudo -n ... --self-test` 的精确顺序；所有命令在用户另行批准前作废。
- [ ] **Step 1a: 冻结包装器与完整创建面**：记录 stage 包装器 SHA，并覆盖 SFTP 暂存、root-only 临时目录、可选新建父目录及三个 live 目标的逐项创建、保留、清理和部分失败回滚。
- [ ] **Step 2: 固定 root-only TOCTOU 边界**：root 先把五文件复制到全新 `root:root:0700` 目录，再从该副本核五文件、SHA、size 和 `visudo -cf`；逐级检查 `/usr` 与 `/etc/sudoers.d` 父链；安装后复核 live 文件 owner/mode/SHA/size。
- [ ] **Step 3: 固定部分失败回滚**：逐项记录本次新建目标，仅逆序删除本次创建项；先删除 sudoers 并重新 `visudo`，绝不删除预存目标；SFTP 部分失败只停止并另立清理 ChangeId。
- [ ] **Step 4: 同步状态文档**：明确 003 staging 已为 `ABSENT`、009 只是本地候选、测试服 live 入口未安装、运行态 P1/UNKNOWN 未关闭。
- [ ] **Step 5: 文档自检**：运行 `git diff --check` 和敏感扫描，确认无真实凭据、无“已安装/已上线”夸大。

### Task 4: CI、独立验收与合并

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_bundle.py`
- Modify: `infra/scripts/test_run_ai_gateway_g8_test_readonly_access_stage_drop.py`

**Interfaces:**
- Consumes: Task 1–3 的生成器、包装器、候选摘要和文档。
- Produces: 精确 PR HEAD 的分级 CI、三方 P0/P1/P2=0 与 merge commit 证据。

- [ ] **Step 1: 写失败 CI 契约测试**：要求 CI 在临时目录真实生成 009、校验五文件/manifest/receipt，并在 Linux `--network none` 运行 stage 测试；不得持久化或上传候选 artifact。
- [ ] **Step 2: 修 CI 并验证**：运行 actionlint 等价门禁、Windows/Linux 定向测试、双构建、历史 001–003 复现、敏感扫描和 `git diff --check`。
- [ ] **Step 3: 独立评审**：同一精确 HEAD 取得代码安全/Standards、QA、产品/规格 P0=0、P1=0；P2 必须关闭或明确为非阻断且不影响授权契约。
- [ ] **Step 4: PR 与合并**：创建 Draft PR，等待 required checks 全绿，转 Ready，仅使用 merge commit 合并并删除远端分支。
- [ ] **Step 5: 收口证据**：纯文档提交记录 final HEAD、CI run、三方结论和 merge commit；保持 `PENDING_USER_APPROVAL`，等待用户对 009 安装另行明确批准。

## Self-Review

- 规格覆盖：候选生成、Drop stage、root-only 安装、部分回滚、CI、三方验收、PR/merge 和商业边界均有对应任务。
- 无占位符：所有 ChangeId、来源提交、源码树、目标、执行次数和禁止项均已固定。
- 类型一致：`ACTIVE_CANDIDATE` 只服务 009；`CONSUMED_CANDIDATES` 继续服务 001–003 历史复现；stage 包装器只消费 009 manifest。

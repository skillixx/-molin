# G8 Drop 只读入口 011 交互 sudo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 以 TDD 实现绑定 `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011` 的五文件候选、独立 SFTP 暂存包装器、单一交互 sudo 安装命令与失败关闭 root 安装脚本，并形成仍待用户再次授权的安装清单。

**Architecture:** 生成器只负责可复现五文件候选；暂存包装器只负责离线校验和未来的一次 SFTP；交互命令生成器只生成供人工复制的单会话命令；root 安装脚本只从固定 011 暂存复制到 root-only 目录并以 no-clobber 安装。密码仅由 `sudo -k -v` 从交互 TTY 读取，任何仓库资产均不得接收、保存或输出密码。

**Tech Stack:** Python 3.13、Bash、OpenSSH/SFTP、Go 1.26.5、GitHub Actions、unittest。

## Global Constraints

- 本实施阶段禁止运行 011 `--local-check` 正式材料检查、SSH、SFTP、sudo、root 安装和远端 self-test。
- 010 必须保持 consumed，011 是唯一 active candidate；不得读取、复用或删除 010 暂存。
- 所有新增代码注释与提交/PR 文本使用中文。
- 测试中的 root、sudo、SFTP 和 no-clobber 行为只能在临时目录、mock 或 `--network none` 容器内验证。
- 每个任务严格遵循 RED → GREEN → REFACTOR；未看到预期失败不得修改生产代码。
- `G8_ENGINEERING_READY` 与 `G8_COMMERCIAL_ACCEPTED` 分离；本计划不包含生产、付费、通知、客户灰度或商业观察。

---

## Task 1: 激活并冻结 011 五文件候选

**Files:**
- Modify: `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_bundle.py`
- Modify: `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py`
- Modify: `.github/workflows/ci.yml`

- [ ] 在生成器测试中先新增失败断言：010 仍位于 `CONSUMED_CANDIDATES`，011 为唯一 `ACTIVE_CANDIDATE`，并精确绑定：
  - ChangeId `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
  - source commit `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`
  - source tree `c2d1252a05d031d842549345128fa7a1ffe53dc8`
  - deployment root `/home/pc/molin`
  - transport `DROP_SSH_INTERACTIVE_SUDO`
- [ ] 运行：
  `python -I -W error::ResourceWarning -m unittest infra.scripts.test_prepare_ai_gateway_g8_test_readonly_access_bundle`
  并确认因 011 尚未激活而失败。
- [ ] 最小修改生成器：保留 001/002/003/009/010 历史回执和 manifest 字节顺序，新增唯一 011 active candidate，并把新 transport 纳入 Drop manifest 字段集合。
- [ ] 重跑生成器测试，确认通过。
- [ ] 使用绝对、全新且工作树外的目录生成 Windows 011 候选；核对恰好五文件、`SHA256SUMS` 四项、manifest、候选回执、对账器大小和双构建摘要。
- [ ] 在 CI 中加入 Linux 临时生成、五文件白名单、manifest/source/tree/transport、四摘要、大小和 Linux 回执精确断言；CI 不持久化候选，不上传 artifact，不执行网络命令。
- [ ] 提交：`新增：冻结G8只读入口011候选`

## Task 2: TDD 实现失败关闭 root 安装脚本

**Files:**
- Create: `infra/scripts/g8-test-readonly-access-install-011.sh`
- Create: `infra/scripts/test_g8_test_readonly_access_install_011.py`

- [ ] 先写失败测试，锁定脚本为固定 ChangeId、无参数、EUID=0、可信 PATH、`umask 077`，并清除 Bash/Python 注入环境。
- [ ] 写失败测试，锁定固定 011 暂存、root-only 目录、五文件集合、Windows 候选回执、四项摘要、对账器大小及 manifest 字段。
- [ ] 写失败测试，覆盖 candidate sudoers 与 live sudoers 两次精确 `visudo -cf`、`sudo -n -l -U pc` 精确范围和 `pc` 不属于 Docker 组。
- [ ] 写 Linux 动态失败测试：预存任一 live 目标时整体非零且内容不变；创建后复制/权限步骤失败时仅删除本次创建目标；预存父目录不删除。
- [ ] 写 Linux 动态失败测试：仅本次创建且回滚后为空的 `/usr/local/libexec/molin` 才可 `rmdir`；sudoers 回滚优先并重新校验 `/etc/sudoers`。
- [ ] 运行新测试并确认缺少脚本导致 RED。
- [ ] 实现单一 Bash 脚本：固定参数、root-only 再复制、完整父链验证、同一 FD no-clobber、逐项 created 登记、EXIT trap 逆序回滚、固定低敏输出。
- [ ] 运行 `bash -n infra/scripts/g8-test-readonly-access-install-011.sh`、Windows unittest 与 Linux `--network none` 动态测试，确认 GREEN。
- [ ] 提交：`新增：实现G8只读入口011失败关闭安装器`

## Task 3: TDD 实现人工复制的单会话命令生成器

**Files:**
- Create: `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-011-command.py`
- Create: `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_011_command.py`

- [ ] 先写失败测试，锁定输入只允许固定 011 ChangeId、固定 root 安装脚本和全新绝对输出文件；错误参数或既存输出必须失败关闭。
- [ ] 写失败测试，断言生成文本恰有一次 `sudo -k -v`，后续仅使用 `sudo -n /bin/bash -ceu` 和固定 `sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test`。
- [ ] 写失败测试，禁止 `sudo -S`、askpass、密码变量、密码 stdin、命令替换、未引用 heredoc、模板占位符和 010 路径。
- [ ] 写失败测试，锁定系统 OpenSSH、`-F none`、`-tt`、固定端点、known_hosts、原始 ED25519 路径、`IdentitiesOnly=yes`、`ConnectionAttempts=1`，并禁止 Agent/X11/转发/本地命令。
- [ ] 写失败测试，锁定 Base64 解码后的脚本字节与仓库 root 安装脚本完全一致；bootstrap 以 root-only no-clobber 写入并复核精确 size/SHA 后才执行。
- [ ] 写失败测试，断言命令不包含密码、私钥正文、Token 或环境变量值，只输出低敏路径和摘要。
- [ ] 运行测试确认 RED，随后实现生成器并重跑至 GREEN。
- [ ] 生成工作树外的正式人工复制命令资产，冻结其 SHA-256、root 安装脚本 SHA-256 与大小；不得执行生成内容。
- [ ] 提交：`新增：生成G8只读入口011交互安装命令`

## Task 4: TDD 实现独立 SFTP 暂存包装器

**Files:**
- Create: `infra/scripts/run-ai-gateway-g8-test-readonly-access-stage-drop-interactive.py`
- Create: `infra/scripts/test_run_ai_gateway_g8_test_readonly_access_stage_drop_interactive.py`

- [ ] 先写失败测试，锁定 `--local-check` 只读离线，正式模式只调用一次 SFTP，生产脚本不得包含 SSH、sudo 或 root 安装调用。
- [ ] 写失败测试，覆盖固定五文件/manifest/回执、known_hosts、密钥对、目标端点、原始身份路径与 helper 摘要/契约。
- [ ] 写失败测试，覆盖 SFTP batch 首条独占 `mkdir`、固定 011 路径、仅五个 `put`，并静态/动态拒绝任何 010 路径。
- [ ] 写失败测试，覆盖候选、known_hosts、密钥对在 SFTP 前后漂移失败关闭；非零、stderr、超时、超限或输出漂移均固定低敏失败且零重试。
- [ ] 写失败测试，锁定 consumed 门禁发生在 helper、候选、身份材料和网络读取前，并覆盖 local-check/self-test/正式入口。
- [ ] 运行测试确认 RED，随后实现包装器并重跑至 GREEN。
- [ ] 运行 Windows 单测、Linux `--network none` 单测、`py_compile` 与离线 `--self-test`；不得运行正式参数。
- [ ] 冻结包装器 SHA-256 并提交：`新增：实现G8只读入口011原子暂存包装器`

## Task 5: 同步 CI、安装授权清单和工程文档

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260813-011.md`
- Modify: `README.md`
- Modify: `docs/ai-gateway-g8-acceptance.md`
- Modify: `docs/ai-gateway-g8-test-readonly-access-runbook.md`
- Modify: `docs/tools.md`
- Modify: `docs/test-plan.md`

- [ ] 在 CI 中执行四组 011 unittest、`py_compile`、`bash -n`、离线 self-test、Windows runner 测试和 Linux `--network none` 动态测试。
- [ ] CI 只生成临时候选和命令资产并核对摘要；禁止 SSH/SFTP/sudo/远端地址访问和 artifact 上传。
- [ ] 编写独立 011 安装授权清单，精确冻结 source commit/tree、五文件、四摘要、候选回执、对账器大小、暂存包装器/root 安装脚本/命令生成器/命令资产/helper SHA-256。
- [ ] 清单明确：状态仅 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`；本轮没有运行 local-check、SFTP、SSH、sudo 或安装；未来执行必须再次获得用户授权。
- [ ] 清单列出单次 SFTP、单次交互 SSH、一次 `sudo -k -v`、root-only/no-clobber、影响面、精确回滚与停止条件，且不包含密码或任何环境变量值。
- [ ] 同步 README、G8 acceptance、Runbook、tools 和 test-plan，保持 `G8_ENGINEERING_READY` 与 `G8_COMMERCIAL_ACCEPTED` 边界。
- [ ] 运行文档/敏感信息/diff 门禁并提交：`文档：冻结G8只读入口011安装授权清单`

## Task 6: 完整验证、独立评审、PR 与交付

**Files:**
- Verify all files changed since `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`

- [ ] 运行全部 011 定向测试、历史候选回执复现、Windows 测试与 Linux `--network none` 测试。
- [ ] 运行 `py_compile`、`bash -n`、Actionlint、敏感扫描、`git diff --check`，确认工作树无未提交漂移。
- [ ] 对精确 HEAD 执行代码安全/Standards、独立 QA、产品/规格三轴评审；P0/P1/P2 必须全部为 0。
- [ ] 创建中文 Draft PR，等待分级 CI；只在精确 HEAD 全部适用门禁成功后转 Ready。
- [ ] 使用 merge commit 合并，禁止 squash，并删除远端功能分支。
- [ ] 另开纯文档证据收口 PR，将状态收敛为 `PENDING_USER_APPROVAL`；不得把合并表述为测试服执行授权。
- [ ] 向用户提交中文结果：候选/摘要、测试/CI/评审、PR/merge，以及明确“未连接测试服、未执行安装、仍需再次批准”。


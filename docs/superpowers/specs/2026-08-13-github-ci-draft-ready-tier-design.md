# GitHub CI Draft/Ready 分级门禁设计

> 状态：`IMPLEMENTED_LOCAL_GATES_PENDING_DRAFT_PR`。实施计划见 `docs/superpowers/plans/2026-08-13-github-ci-draft-ready-tier.md`。本设计只优化仓库 CI、测试与文档，不安装 self-hosted runner，不连接测试服，也不改变生产、真实付费、通知或客户灰度授权边界。

## 1. 背景与当前证据

仓库 `skillixx/-molin` 当前为 Private，`.github/workflows/ci.yml` 的所有作业均使用 `ubuntu-latest`。GitHub-hosted runner 对每个 job 分别按分钟向上取整；当前完整 G8 run `31700050048` 的 12 个成功 job 约占 37 个取整分钟，而纯文档 run `31701733345` 的 3 个成功 job 约占 3 个取整分钟。

现有路径分类已经能让纯文档 PR 跳过重型任务，但所有非文档 PR 在 Draft 阶段每次 `synchronize` 都会执行全部适用重型门禁。反复修复和评审期间，旧 HEAD 的 Docker、race、浏览器和完整构建仍会继续运行，消耗大量分钟。

本设计把 CI 分成两层：Draft 只提供快速反馈，Ready 才提供最终可合并证据。所有现有重型测试继续保留在 Ready 层，不能以节省分钟为理由删除、降级或用窄测试替代最终门禁。

只读核验还发现：`main` 当前没有 classic branch protection，仓库也没有 ruleset。因此工作流可以生成独立的最终检查，但仅修改仓库文件不能从 GitHub 平台层阻止管理员或有合并权限者绕过检查。分支保护或 ruleset 必须作为后续单独授权的仓库设置动作；在此之前，产品合并流程仍须人工核对精确 HEAD 的 `CI 必选门禁汇总`。

## 2. 目标与非目标

### 2.1 目标

- Draft PR 只运行轻量质量检查和与变更范围直接相关的定向测试。
- Ready PR 自动运行当前全部适用重型门禁，保留最终精确 HEAD 的完整证据。
- 同一 PR 的旧运行在新事件或新提交到来时自动取消。
- Draft 和 Ready 使用不同的汇总检查名称，Draft PASS 不能被误当成最终完整门禁。
- Ready 后的新提交再次触发完整适用门禁；任何失败、取消或缺失都不能产生成功汇总。
- 路径分类失败、目标选择失败或未知路径必须失败关闭，不得静默漏测。
- 不删除任何现有测试，不安装 self-hosted runner，不迁移 GitLab CI。

### 2.2 非目标

- 本次不修改 GitHub 计费预算、组织设置、branch protection、ruleset 或 Actions 权限。
- 本次不改变 PR 审查人数、merge commit、禁止 squash 或产品验收规则。
- 本次不连接测试服、生产、数据库、Redis、RabbitMQ、Bifrost、监控或真实上游。
- 本次不把 Draft 快速门禁表述为完整 QA、生产就绪或商业验收。

## 3. 事件与并发模型

工作流事件固定为：

```yaml
on:
  pull_request:
    branches: [main]
    types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]

concurrency:
  group: ci-pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true
```

语义：

- `opened`、`reopened`、`synchronize` 根据事件载荷中的 `pull_request.draft` 选择 Draft 或 Ready 层。
- `ready_for_review` 立即启动 Ready 完整层。
- `converted_to_draft` 立即取消同 PR 仍在运行的 Ready run，并启动 Draft 快速层。
- 新提交触发的 `synchronize` 取消同 PR 旧 HEAD 的任何运行。
- 并发组只按 PR number 隔离，不允许不同 PR 互相取消。

所有 checkout、分类和汇总继续绑定 `github.event.pull_request.head.sha` 与 `base.sha`，不能使用漂移的分支引用。

## 4. 统一分类与模式输出

现有 `infra/scripts/classify-ci-change-scope.py` 继续负责 Ready 重型门禁集合。新增 PR 模式输出：

```text
pr_mode=draft|ready
draft_docs=true|false
draft_python=true|false
draft_backend=true|false
draft_frontend_admin=true|false
draft_frontend_user=true|false
```

模式来源只能是 GitHub PR 事件中的布尔 `draft`，不能从标题、标签、分支名或提交信息推断。分类器接受显式 `--pr-mode`，只允许 `draft` 或 `ready`。

Draft 目标规则：

- 任意 PR 均执行统一轻量质量门禁。
- 纯 Markdown 文档：只开 `draft_docs`。
- `.py`、`infra/scripts/`、`scripts/`、`tests/` 或 `.github/`：开 `draft_python`；工作流变更还必须运行 actionlint。
- `server/`：开 `draft_backend`。
- `web/admin-console/` 或 `web/shared/`：开 `draft_frontend_admin`。
- `web/user-console/` 或 `web/shared/`：开 `draft_frontend_user`。
- 未知根路径、分类异常、重命名或复制源路径无法安全映射时，Draft 层失败关闭到所有定向门禁；Ready 层继续使用现有 `full=true` 完整回归。

分类器必须同时输出现有 Ready 布尔值，确保 Ready 行为不因本设计缩窄。测试覆盖 Draft/Ready 的相同路径矩阵、删除、重命名、复制、未知路径和非法模式。

## 5. Draft 快速门禁

### 5.1 统一轻量质量门禁

所有 Draft PR 必须运行一个 `ubuntu-latest` job，包含：

- 精确 base/head 的 `git diff --check`；
- 差异敏感信息扫描；
- `python -m unittest infra/scripts/test_classify_ci_change_scope.py`；
- 分类器与新增 Draft 目标选择器的 `py_compile`；
- actionlint（仅当 `.github/workflows/` 或 Actions 相关测试发生变化时必须执行；无法确定时执行）。

该 job 不启动 Docker、MySQL、Redis、Go race、浏览器安装或构建。

### 5.2 Python/G8 定向门禁

新增 `infra/scripts/select-ci-draft-tests.py`，只根据精确 Git 差异输出 NUL 安全的仓库相对测试路径。规则：

- 修改 `infra/scripts/foo.py` 时选择同目录 `test_foo.py`；不存在对应测试时失败关闭到 `infra/scripts/test_*.py` 全部本机单测。
- 修改 `infra/scripts/test_foo.py` 时选择该测试本身。
- 修改分类器、目标选择器或 `.github/workflows/ci.yml` 时至少选择两者的契约测试和工作流契约测试。
- 修改 `scripts/` 或 `tests/` 的未知 Python 文件时运行对应目录全部 Python 测试；不得返回空测试集。
- 选中的生产脚本和测试脚本全部执行 `py_compile`。
- Draft 层不运行 Docker；需要 POSIX 动态语义的测试在本机按既有平台条件 skip，最终由 Ready 的 `--network none` 测试覆盖。

选择器输出只包含仓库路径，不允许 shell 片段、空路径、绝对路径、反斜杠或 `..`。

### 5.3 Go 定向门禁

对 `server/` 变更：

- 使用 `go list` 将发生变化的 `.go` 文件映射到 package；删除文件时同时依据父目录映射。
- 对唯一 package 集执行 `go test -count=1`，不启用 `-race`，不启动 MySQL/Redis，不执行迁移演练。
- `go.mod`、`go.sum`、共享 `server/pkg/`、bootstrap、config、migration 或无法映射路径失败关闭到 `go test -count=1 ./...`，但仍不启动服务容器。
- 运行 `go vet` 仅限选中 package；未知依赖关系在 Ready 完整层再次覆盖。

### 5.4 前端定向门禁

受影响控制台分别运行：

- `npm ci`；
- 已有单元/契约测试；
- `npm run type-check`。

Draft 不运行 `npm run build`、Playwright、Chrome 安装或真实后端浏览器 E2E。`web/shared/` 同时选择两个控制台。

## 6. Ready 完整门禁

当 `pull_request.draft == false` 时：

- 继续运行现有 `docs-lightweight`、`backend`、`phase5-release-safety`、`gateway-g3`、`gateway-g4`、`gateway-g7`、`gateway-g8`、`gateway-g8-real-e2e`、`frontend-admin`、`frontend-user` 中所有适用 job。
- 保留当前每个 job 的测试、Docker、race、浏览器、构建、敏感扫描、promtool/amtool 与隔离演练，不删除、不替换、不降级。
- 路径分类 `full=true` 时仍要求现有全部适用门禁为 true。
- Ready 层不得依赖同 SHA 的 Draft job 结果；它必须在当前 Ready run 内重新 checkout、分类和执行。
- Ready 后任何新 commit 都触发新的 Ready 完整 run，旧 run 自动取消。

重型 job 的 `if` 必须同时要求 Ready 模式和原有 scope 输出。例如：

```yaml
if: github.event.pull_request.draft == false && needs.change-scope.outputs.gateway_g8 == 'true'
```

## 7. 双汇总检查与失败关闭

新增两个互斥汇总 job：

```text
CI Draft 快速门禁汇总
CI 必选门禁汇总
```

### 7.1 Draft 汇总

- 仅在 `pull_request.draft == true` 时运行。
- 必须验证分类成功、统一轻量门禁成功以及所有被选中的 Draft 定向门禁成功。
- 被选择 job 的 `failure`、`cancelled` 或缺失均使汇总失败。
- 未选择 job 只能为 `skipped`。
- Draft 汇总明确输出“不可作为最终合并门禁”，但不得使用动态检查名。

### 7.2 Ready 汇总

- 仅在 `pull_request.draft == false` 时运行。
- 沿用当前 `CI 必选门禁汇总` 名称和全部失败关闭校验。
- 必须验证当前 run 的分类结果、`full` 一致性和所有适用重型 job 成功。
- 不读取 Draft 汇总、历史 run 或其他 HEAD 的结论。

两个汇总固定名称不同，避免 Draft PASS 在 UI、API、PR 评论或人工流程中被误认成最终完整门禁。

## 8. 分支保护与合并控制

当前权威只读结果：`main` 未启用 classic branch protection，仓库 rulesets 为空。因此本次仓库变更完成后：

- CI 能准确提供 `CI 必选门禁汇总`，但 GitHub 仍不会自动阻止有权限者绕过 CI 合并。
- 在获得仓库设置变更的独立授权前，产品经理必须使用 PR 精确 `headRefOid`、同 HEAD CI run 和三方评审证据人工门禁合并。
- 后续建议单独批准并配置 main ruleset：要求 PR、禁止 force push、要求 `CI 必选门禁汇总`、要求分支最新、至少一名其他开发者评审，并保持 merge commit、禁止 squash。
- 本次不得通过 API 创建或修改 branch protection/ruleset。

文档不得把“工作流生成了检查”描述成“平台已强制 required”。

## 9. 测试策略

### 9.1 公共 seam

- 分类 seam：路径集合 + `pr_mode` → Ready 与 Draft 布尔输出。
- 目标选择 seam：精确变更路径 → 安全、非空、去重的定向测试/package 集。
- 工作流 seam：PR event payload → 互斥的 Draft/Ready job 集与汇总名。
- 汇总 seam：required 布尔值 + job result → 成功或失败。

### 9.2 必测行为

- opened/reopened/synchronize 的 Draft 与 Ready 分流。
- ready_for_review 启动完整层；converted_to_draft 只启动快速层。
- 同 PR concurrency key 相同，不同 PR key 不同，`cancel-in-progress=true`。
- Draft 下所有重型 job 为 skipped，Ready 下所有 Draft 定向 job 为 skipped。
- 工作流变更在 Draft 运行 actionlint/契约测试，在 Ready 保持 full CI。
- 纯文档 Draft 只运行分类、轻量、Draft 汇总。
- G8 Python 脚本 Draft 运行对应定向测试，但不运行 Docker/Playwright；Ready 仍运行全部适用 G8 测试。
- backend、共享高风险包、migration、双前端及未知路径的失败关闭映射。
- Draft/Ready 汇总分别拒绝 required job 的 failure、cancelled、缺失与异常字符串。
- Draft 汇总无法替代 Ready 汇总，检查名称与 `if` 条件由契约测试精确锁定。
- 现有全部测试命令仍能在 Ready job 中被静态契约测试找到，防止后续以优化名义删除。

### 9.3 计费回归指标

实现后以 GitHub run API 记录：

- 典型 G8 Draft 的成功 job 数、总墙钟时间和按 job 向上取整的近似分钟；目标为 `<= 8` 分钟。
- 同一变更转 Ready 后的完整 job 集必须与优化前适用集合一致；不把 Ready 分钟下降作为验收要求。
- 纯文档 Draft 目标保持约 3 个 job；不得为节省一分钟合并分类与汇总而削弱失败关闭。

计费指标是优化证据，不是测试通过的替代品。

## 10. 文档与交付

实现阶段修改：

- `.github/workflows/ci.yml`：事件、concurrency、Draft job、Ready 条件和双汇总。
- `infra/scripts/classify-ci-change-scope.py` 与测试：Draft/Ready 模式和输出。
- 新增 Draft 测试/package 目标选择器及测试。
- 新增工作流契约测试，锁定全部现有 Ready 命令仍存在。
- `docs/tools.md`、`docs/git-workflow.md`、`docs/test-plan.md`：解释两层门禁、人工合并门禁和当前无平台保护事实。
- README 只记录 CI 工程优化，不描述为生产或商业完成。

交付顺序：书面设计复核 → 实施计划 → TDD → 本地验证 → 独立安全/QA/产品评审 → Draft PR → Draft 快速 CI → 转 Ready → Ready 完整 CI → 用户独立批准 merge commit。

本次设计与后续 PR 不授权测试服、生产、真实上游、真实资金、通知或客户灰度，也不改变 `G8_ENGINEERING_READY` 与 `G8_COMMERCIAL_ACCEPTED` 的边界。

## 11. 验收条件

- Draft 与 Ready 事件、job 集和汇总名称严格互斥。
- 同 PR 旧运行自动取消，不同 PR 不互相影响。
- Draft 提供变更范围直接相关的可操作反馈，未知范围失败关闭而非漏测。
- Ready 精确 HEAD 的现有全部适用测试一项不少，并由 `CI 必选门禁汇总` 汇总。
- 所有新增分类、目标选择和工作流契约测试通过；actionlint、diff-check 与敏感扫描通过。
- 典型 G8 Draft 近似取整分钟不高于 8；最终 Ready 完整门禁证据不缩水。
- 文档诚实说明 main 当前无 branch protection/ruleset；未经独立授权不修改仓库设置。
- 未安装 self-hosted runner，未连接测试服或生产，未执行真实付费、通知或客户灰度。

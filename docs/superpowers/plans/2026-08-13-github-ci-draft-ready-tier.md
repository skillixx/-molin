# GitHub CI Draft/Ready 分级门禁实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不删除任何现有测试的前提下，把 GitHub Actions 调整为 Draft PR 只运行轻量与定向门禁、Ready PR 运行全部适用重型门禁，并自动取消同一 PR 的旧运行。

**Architecture:** 保留现有 Ready 路径分类与全部重型作业，扩展分类器输出 Draft 模式及目标范围；新增安全的定向目标选择器和执行器；工作流使用 PR Draft 状态互斥选择两层作业，并用不同名称的汇总作业失败关闭。仓库内工作流只生成检查结果，不声称 GitHub 平台已经启用 branch protection 或 ruleset。

**Tech Stack:** GitHub Actions YAML、Python 3.13 标准库、Go、npm、unittest、actionlint、PowerShell/Git。

## Global Constraints

- 只修改仓库 CI、测试和中文文档；不安装 self-hosted runner，不连接测试服或生产。
- 所有新增源码注释、提交说明、PR 说明和评审说明使用中文。
- 现有 Ready 作业中的测试、Docker、race、浏览器、构建、敏感扫描和隔离演练命令不得删除或降级。
- Draft 汇总只能叫 `CI Draft 快速门禁汇总`，Ready 汇总继续叫 `CI 必选门禁汇总`，两者不能互相替代。
- 同一 PR 使用固定 concurrency key 并自动取消旧运行；不同 PR 不互相取消。
- 未知路径、目标选择异常、空目标或非法路径必须失败关闭，不能静默漏测。
- `main` 当前无平台 branch protection/ruleset 的事实必须如实写入文档；本计划不修改仓库设置。
- 每项实现严格遵守 RED → GREEN → REFACTOR；先观察失败，再写最小实现。

---

## Task 1：扩展变更范围分类器的 Draft/Ready 契约

**Files:**

- Modify: `infra/scripts/classify-ci-change-scope.py`
- Modify: `infra/scripts/test_classify_ci_change_scope.py`

- [ ] **Step 1：先增加 Draft 模式失败测试**

  在 `test_classify_ci_change_scope.py` 增加公开行为测试，至少覆盖：

  - `draft` 纯 Markdown 只选 `draft_docs`；
  - `infra/scripts/*.py`、`scripts/`、`tests/`、`.github/` 选择 `draft_python`；
  - `server/` 选择 `draft_backend`；
  - `web/shared/` 同时选择两个前端；
  - 未知根路径选择全部 Draft 定向门禁；
  - `ready` 保持既有 Ready 输出逐项不变；
  - 非法 `--pr-mode` 被 argparse 拒绝；
  - GitHub output 同时包含 `pr_mode`、Draft 输出和既有 Ready 输出。

- [ ] **Step 2：运行测试并确认 RED**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_classify_ci_change_scope.py -v
  ```

  预期：新增测试因 `--pr-mode`、Draft 输出或公开函数尚不存在而失败。

- [ ] **Step 3：实现最小分类契约**

  在分类器中增加：

  ```python
  PR_MODES = ("draft", "ready")
  DRAFT_OUTPUT_NAMES = (
      "draft_docs",
      "draft_python",
      "draft_backend",
      "draft_frontend_admin",
      "draft_frontend_user",
  )

  def classify_draft_paths(paths: Sequence[str]) -> dict[str, bool]:
      """根据精确变更路径选择 Draft 定向门禁，未知范围按全部执行失败关闭。"""
  ```

  CLI 新增 `--pr-mode`，只接受 `draft|ready`；输出同时保留现有 Ready 布尔值，避免 Ready 行为收窄。

- [ ] **Step 4：运行测试并确认 GREEN**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_classify_ci_change_scope.py -v
  python -m py_compile infra/scripts/classify-ci-change-scope.py infra/scripts/test_classify_ci_change_scope.py
  ```

- [ ] **Step 5：检查兼容性**

  对至少一组纯文档、一组 G8 Python、一组 backend 路径同时比较改动前后的 Ready 输出，确认既有 `docs_lightweight/backend/gateway_*/frontend_*/full` 契约未漂移。

## Task 2：TDD 新增安全的 Draft 定向目标选择器与执行器

**Files:**

- Create: `infra/scripts/select-ci-draft-tests.py`
- Create: `infra/scripts/run-ci-draft-targets.py`
- Create: `infra/scripts/test_select_ci_draft_tests.py`
- Create: `infra/scripts/test_run_ci_draft_targets.py`

- [ ] **Step 1：先写路径和目标选择失败测试**

  测试公开 seam：

  ```python
  select_python_targets(paths, repo_root)
  select_go_packages(paths, repo_root)
  validate_repository_path(value)
  ```

  至少覆盖：

  - 修改 `infra/scripts/foo.py` 选择 `test_foo.py` 和生产脚本的 `py_compile`；
  - 对应测试不存在时回退到 `infra/scripts/test_*.py`，不得返回空集；
  - 修改测试文件选择测试本身；
  - CI 工作流变更强制选择分类器、选择器、执行器和工作流契约测试；
  - `scripts/`、`tests/` 的未知 Python 变更回退到对应目录测试；
  - `server/` 普通文件映射父 package；`go.mod`、`go.sum`、`server/pkg/`、bootstrap、config、migration 映射 `./...`；
  - 去重、排序和删除文件处理；
  - 拒绝绝对路径、空路径、反斜杠、`..`、控制字符和 shell 片段；
  - JSON 输出只包含字符串数组。

- [ ] **Step 2：先写执行器失败测试**

  使用 mock 的 `subprocess.run` 验证：

  - Python 测试逐项以参数列表执行，不拼 shell；
  - 所有选中 Python 文件执行 `py_compile`；
  - Go 仅运行选中 package 的 `go test -count=1` 和 `go vet`；
  - 空数组、非法 JSON、非法路径、子进程失败均返回非零；
  - 不允许 `shell=True`，不启动 Docker、数据库或远端命令。

- [ ] **Step 3：运行测试并确认 RED**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_select_ci_draft_tests.py -v
  python -I -W error::ResourceWarning infra/scripts/test_run_ci_draft_targets.py -v
  ```

- [ ] **Step 4：实现最小选择器**

  选择器 CLI 固定接受 `--base`、`--head`、`--repo-root`、`--github-output`，输出单行 JSON：

  ```text
  python_tests_json=[...]
  python_compile_json=[...]
  go_packages_json=[...]
  ```

  使用 `git diff --name-status -z` 或复用分类器的 NUL 安全差异读取，不接受用户提供的 shell 片段。

- [ ] **Step 5：实现最小执行器**

  执行器固定三种模式：`python-tests`、`python-compile`、`go`；JSON 解码后再次调用路径验证，全部通过 `subprocess.run([...], check=True)` 执行。

- [ ] **Step 6：运行测试并确认 GREEN**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_select_ci_draft_tests.py -v
  python -I -W error::ResourceWarning infra/scripts/test_run_ci_draft_targets.py -v
  python -m py_compile infra/scripts/select-ci-draft-tests.py infra/scripts/run-ci-draft-targets.py infra/scripts/test_select_ci_draft_tests.py infra/scripts/test_run_ci_draft_targets.py
  ```

## Task 3：TDD 锁定工作流事件、互斥作业和 Ready 命令完整性

**Files:**

- Create: `infra/scripts/test_ci_draft_ready_workflow_contract.py`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1：先写工作流契约失败测试**

  直接读取 YAML 文本并锁定：

  - PR 事件类型为 `opened/synchronize/reopened/ready_for_review/converted_to_draft`；
  - concurrency group 为 `ci-pr-${{ github.event.pull_request.number }}` 且 `cancel-in-progress: true`；
  - `change-scope` 输出 `pr_mode` 和全部 Draft 布尔值/目标 JSON；
  - Draft 作业只在 `draft == true`；现有重型作业只在 `draft == false`；
  - 两个汇总名称和互斥条件准确；
  - Draft 汇总验证所有被选中的 Draft 作业，不接受 failure/cancelled/缺失；
  - Ready 汇总仍验证全部现有适用重型作业；
  - 基线 Ready 作业中的关键命令哨兵全部仍存在，包括 Go race、G3/G4/G7/G8、真实后端浏览器、双前端 lint/type-check/build/E2E、敏感扫描及当前隔离测试。

- [ ] **Step 2：运行契约测试并确认 RED**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_ci_draft_ready_workflow_contract.py -v
  ```

- [ ] **Step 3：修改触发器与并发控制**

  ```yaml
  on:
    pull_request:
      branches: [main]
      types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]

  concurrency:
    group: ci-pr-${{ github.event.pull_request.number }}
    cancel-in-progress: true
  ```

- [ ] **Step 4：扩展 `change-scope` 作业**

  精确 checkout `pull_request.head.sha`，以事件布尔值传入 `--pr-mode`；Draft 时运行选择器并把 JSON 输出暴露给后续作业。任何分类或选择失败直接使作业失败。

- [ ] **Step 5：新增 Draft 作业**

  新增：

  - `draft-quality`：diff-check、敏感扫描、分类/选择/工作流契约测试和必要 actionlint；
  - `draft-python`：只运行选中的 Python 测试和 `py_compile`；
  - `draft-backend`：只运行选中 Go package 的 test/vet，不启用 race 或服务容器；
  - `draft-frontend-admin`、`draft-frontend-user`：`npm ci`、已有单测、`type-check`，不 build/Playwright；
  - `ci-draft-gate`：显示名固定为 `CI Draft 快速门禁汇总`。

- [ ] **Step 6：把全部既有重型作业限制为 Ready**

  每个既有 job 的 scope 条件前增加：

  ```yaml
  github.event.pull_request.draft == false
  ```

  不改动现有 job 内的测试命令。

- [ ] **Step 7：保留并加强 Ready 汇总**

  `ci-required-gate` 名称继续为 `CI 必选门禁汇总`，只在 Ready 执行，按当前 run 的分类结果逐项要求适用 job 为 `success`；不得读取 Draft 汇总或历史 run。

- [ ] **Step 8：运行契约测试并确认 GREEN**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_ci_draft_ready_workflow_contract.py -v
  ```

- [ ] **Step 9：运行 actionlint**

  优先使用仓库已固定版本；如本机无 actionlint，使用仓库当前 CI 中的固定安装方式，仅做本地 YAML 静态验证，不改系统配置。

## Task 4：更新中文 CI、Git 和测试文档

**Files:**

- Modify: `README.md`
- Modify: `docs/tools.md`
- Modify: `docs/git-workflow.md`
- Modify: `docs/test-plan.md`
- Modify: `docs/superpowers/specs/2026-08-13-github-ci-draft-ready-tier-design.md`

- [ ] **Step 1：更新设计状态**

  把设计状态从待书面复核改为已批准实施，并记录实施计划路径；不得改写已批准的边界。

- [ ] **Step 2：更新 README 与工具文档**

  说明 Draft/Ready 两层、自动取消、双汇总、预计 Draft 分钟数目标；删除“只有三个并行 job”等过时描述。

- [ ] **Step 3：更新 Git 工作流文档**

  说明 Draft PASS 不是合并证据，只有当前 Ready 精确 HEAD 的 `CI 必选门禁汇总` 可进入人工合并门禁。

- [ ] **Step 4：诚实记录平台保护现状**

  明确当前仓库没有 branch protection/ruleset；“建议设为 required”与“平台已强制 required”分开表述。本任务不调用 GitHub API 修改设置。

- [ ] **Step 5：更新测试计划**

  补齐 Draft 快速反馈、Ready 完整回归、旧运行取消、未知范围失败关闭和计费回归验收；重申未删除任何测试。

## Task 5：全量本地验证和差异审查

**Files:**

- Verify all changed files above.

- [ ] **Step 1：运行全部新增/受影响 Python 测试**

  ```powershell
  python -I -W error::ResourceWarning infra/scripts/test_classify_ci_change_scope.py -v
  python -I -W error::ResourceWarning infra/scripts/test_select_ci_draft_tests.py -v
  python -I -W error::ResourceWarning infra/scripts/test_run_ci_draft_targets.py -v
  python -I -W error::ResourceWarning infra/scripts/test_ci_draft_ready_workflow_contract.py -v
  ```

- [ ] **Step 2：运行语法、格式和敏感检查**

  ```powershell
  python -m py_compile infra/scripts/classify-ci-change-scope.py infra/scripts/select-ci-draft-tests.py infra/scripts/run-ci-draft-targets.py infra/scripts/test_classify_ci_change_scope.py infra/scripts/test_select_ci_draft_tests.py infra/scripts/test_run_ci_draft_targets.py infra/scripts/test_ci_draft_ready_workflow_contract.py
  git diff --check origin/main...HEAD
  ```

  同时复用仓库既有敏感扫描命令；输出不得含密码、私钥、Token 或环境变量值。

- [ ] **Step 3：验证 Ready 命令零删除**

  由契约测试和人工差异审查共同确认：改动前 `.github/workflows/ci.yml` 中各 Ready 重型 job 的关键测试命令在改动后仍存在，只有触发条件和汇总结构变化。

- [ ] **Step 4：验证 Draft 目标矩阵**

  使用临时提交范围或公开函数模拟纯文档、G8 Python、backend、admin、user、shared、workflow、未知路径，确认输出和计划一致；不发起真实 GitHub run。

- [ ] **Step 5：检查工作树边界**

  ```powershell
  git status --short --branch
  git diff --stat origin/main...HEAD
  ```

  确认只包含 CI、测试和文档；没有 self-hosted 配置、测试服地址调用或远端执行代码。

## Task 6：本地提交、独立门禁与 Draft PR 交接

**Files:**

- Commit all approved CI/test/docs changes.

- [ ] **Step 1：创建中文本地提交**

  ```powershell
  git add .github/workflows/ci.yml infra/scripts README.md docs
  git commit -m "优化：实现GitHub CI Draft Ready分级门禁"
  ```

- [ ] **Step 2：固定提交证据**

  记录精确 HEAD、base、tree、测试结果和差异文件；任何后续 HEAD 漂移使当前证据失效。

- [ ] **Step 3：执行独立代码安全、QA 和产品规格评审**

  三方均要求 P0=0、P1=0；P2 必须有明确处置。评审只读，不连接测试服。

- [ ] **Step 4：在获得 Git 推送/PR 授权后创建 Draft PR**

  PR 标题和正文使用中文，精确绑定 HEAD，明确：

  - Draft 只验证快速门禁；
  - 转 Ready 后才运行全部适用重型门禁；
  - 当前 GitHub 平台未配置 required ruleset；
  - 本 PR 不授权测试服、生产、付费、通知或客户灰度。

- [ ] **Step 5：分两阶段验收远端 CI**

  先核验 Draft run 只出现轻量/定向作业及 `CI Draft 快速门禁汇总`；然后必须由用户单独批准转 Ready，核验同一精确 HEAD 的全部适用重型作业和 `CI 必选门禁汇总`。不得用 Draft PASS 代替 Ready PASS。

- [ ] **Step 6：停止于合并授权门禁**

  即使 Ready CI 全绿，也只提交证据并等待用户明确批准 merge commit；禁止 squash，禁止擅自修改 GitHub 仓库设置。

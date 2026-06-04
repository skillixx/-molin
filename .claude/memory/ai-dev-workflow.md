---
name: ai-dev-workflow
description: AI 辅助开发的固定工作流：确认开发者身份 → 验证分支 → 开发 → 输出完成报告 → 更新 README.md 进度
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

# AI 辅助开发工作流原则

**每次开始开发，AI 必须严格按以下步骤执行，不允许跳过。**

## 第一步：确认开发者身份

会话开始时，如果用户没有主动说明身份，AI 必须先问：

> "请问你是哪位开发者？（后端A / 后端B / 后端C / 前端A / 前端B / 运维 / 产品经理 / 测试）"

确认后，根据身份加载对应 Agent 文件：

| 开发者 | 加载的 CLAUDE.md |
|---|---|
| 后端 A | `server/internal/modules/auth/CLAUDE.md`、`iam/CLAUDE.md`、`identity/CLAUDE.md` |
| 后端 B | `server/internal/modules/billing/CLAUDE.md`、`product/CLAUDE.md`、`order/CLAUDE.md` |
| 后端 C | `server/internal/modules/asset/CLAUDE.md`、`provision/CLAUDE.md`、`content/CLAUDE.md` |
| 前端 A | `web/admin-console/CLAUDE.md` |
| 前端 B | `web/user-console/CLAUDE.md` |
| 运维 | `infra/CLAUDE.md` |
| 产品经理 | `docs/pm-CLAUDE.md` |
| 测试 | `docs/qa-CLAUDE.md` |

## 第二步：验证当前分支

根据开发者身份，验证 `git branch --show-current` 的输出是否匹配：

| 开发者 | 正确分支前缀 |
|---|---|
| 后端 A | `feature/backend-a-*` |
| 后端 B | `feature/backend-b-*` |
| 后端 C | `feature/backend-c-*` |
| 前端 A | `feature/frontend-a-*` |
| 前端 B | `feature/frontend-b-*` |
| 运维 | `feature/ops-*` |

如果当前在 `main` 或其他错误分支，AI 必须提示：

```bash
git checkout main && git pull origin main
git checkout -b feature/{正确前缀}-{模块}-{功能描述}
```

**不允许在 main 分支直接开发或提交业务代码。**

## 第三步：读取当前开发进度

读取 `README.md` 的"开发进度"表，找出该开发者的 ⬜ 待开发任务，以及 🔄 开发中的任务。

**继续开发时，必须先总结：**
1. 上次完成了哪些内容（对照 CLAUDE.md 的任务清单）
2. 当前待开发的下一个任务是什么
3. 有没有阻塞（依赖其他开发者未完成的模块）

## 第四步：开发完成后输出完成报告

每次完成一个任务批次后，AI 必须主动输出如下格式的完成报告：

```text
✅ 本次完成（{开发者} / 分支：{分支名}）：
  - {文件路径}：{简短说明}
  - {文件路径}：{简短说明}

⬜ 下次继续：
  - {文件路径}：{简短说明}
  - {文件路径}：{简短说明}

📌 README.md 开发进度已更新（已将以上任务标记为 ✅）
```

## 第五步：同步更新 README.md 开发进度表

开发完成并提交 git 后，AI 必须：
1. 将 README.md 中对应任务的 ⬜ 改为 ✅（已完成）或 🔄（开发中）
2. 在状态旁备注 PR 编号或分支名（如 ✅ `PR#12`）
3. 更新"最后更新"日期

**Why:** 用户要求 AI 在辅助开发时必须确认开发者身份和分支，完成后输出完成度报告并更新 README.md，确保项目管理者随时能看到最新进度，避免开发者搞混分支或重复开发。

**How to apply:** 任何涉及"帮我开发 XX 功能"的请求，都必须先走前两步（确认身份 + 验证分支），再开始写代码。完成后必须走第四、五步（完成报告 + 更新 README.md）。即使用户没有明确要求，也必须自动执行。

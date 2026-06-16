# Memory Index

- [Project Overview](project-overview.md) — Molin 云管理平台，Vue3 + Go + MySQL，三阶段交付
- [Design Decisions](design-decisions.md) — 关键设计决策和安全约定（HMAC、user_sessions、支付回调等）
- [Developer Assignments](developer-assignments.md) — 开发者分工（含运维/PM/测试）、代码路径、Agent 文件位置；第一阶段已验收通过（2026-06-07）
- [AI Dev Workflow](ai-dev-workflow.md) — AI 辅助开发必须遵守的工作流：确认身份→验证分支→开发→完成报告→更新 README.md 进度
- [Tool Documentation Principle](tool-documentation-principle.md) — 项目原则：使用任何工具时简要记录其作用和常用命令
- [Stage Gate Principle](stage-gate-principle.md) — 开发准则：每阶段完成必须经测试验收 + 产品经理确认，才可进入下一阶段
- [Git Workflow Feedback](feedback-git-workflow.md) — 开发必须在 feature 分支上进行，禁止直接 push main，需开 PR 走审查流程
- [Permission Code Seeding Feedback](feedback-permission-code-seeding.md) — RequirePerm 声明权限码时必须同时建 seed migration，已三次重复出现同根因 P1
- [Design Decisions](design-decisions.md) 已更新：注册接口统一为双OTP单入口（2026-06-09）；权限码 seed 缺失为反复出现的 P1 根因；账号唯一性必须做规范化 + DB 唯一键兜底（2026-06-10）
- [Test Server Reference](reference-test-server.md) — 测试服务器 SSH（8.130.9.163:10003），服务端口、API 部署、Python 测试执行方式
- [Design Decisions](design-decisions.md) 已更新：后端接口字段变更未同步前端为反复出现根因（PR3 list/items、PR4 phone登录改密码），2026-06-12
- [Pending Admin Console Tasks](project-pending-admin-console-tasks.md) — admin-console 缺审计日志页(0.5-1人日)和用户分组管理页(3-5人日)
- [Confirm Before Merge Feedback](feedback-confirm-before-merge.md) — 派 产品经理 merge PR 到 main 前必须先和用户确认，每个 PR 单独确认（2026-06-12）
- [Round 7 Audit Status](project-round7-audit-status.md) — D-93~D-96 闭环；后端乙重设计 R1~R6(PR#106~#112) + base-roles seed(000024)/admin bootstrap CLI(#113/#115) + 缺陷修复 F1~F6/P3(#120~#126,两轮QA回归全闭环,migrate→000025) 全部合并 main；上线检查单 docs/backend-b-go-live-checklist.md；并发多actor用 worktree 隔离（2026-06-15）
- [Backend-Only Scope Feedback](feedback-backend-only-scope.md) — 原则：Claude 只负责后端+对接文档，不写前端页面代码，每次输出前先确认是否属后端范围（2026-06-15）
- [Codex Frontend Scope Feedback](feedback-codex-frontend-scope.md) — 原则：Codex 只负责前端页面，不写后端业务代码（与 Claude 后端分工互斥）；两条已写入仓库 CLAUDE.md/AGENTS.md（2026-06-15）

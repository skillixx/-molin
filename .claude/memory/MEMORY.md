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

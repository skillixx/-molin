# Molin 文档索引

> 本目录为设计文档与对接文档（所有人只读）。本文是 `docs/` 的总索引，新增文档请在此登记。

---

## ⭐ 业务功能与计费文档体系（商品 · 会员 · 应用 · 扣费）

> 面向运营/产品/测试/前端对接的功能说明与集成设计，含操作流程与逐案例教程。
> **入口先读** → [业务与计费总览（文档导航）](./business-billing-overview.md)

| 文档 | 主题 | 定位 |
|---|---|---|
| [business-billing-overview.md](./business-billing-overview.md) | 总览导航 | 串起下列四份，建立全局认知（**先读**） |
| [product-and-billing-guide.md](./product-and-billing-guide.md) | 商品与商品计费 | 怎么卖、怎么定价、怎么扣钱（底座） |
| [membership-management-guide.md](./membership-management-guide.md) | 会员管理 | 会员等级/权益，决定是否享会员价 |
| [app-management-guide.md](./app-management-guide.md) | 应用管理 | 应用元数据（图标/描述/适配器） |
| [app/billing-integration-design.md](./app/billing-integration-design.md) | 应用×财务商品 扣费集成 | 把应用挂成商品，购买扣费+使用扣费（总装） |
| [app/billing-integration-spec.md](./app/billing-integration-spec.md) | 应用接入计费 开发对接规范 | 开发者视角：接口字段级契约、上报/扣减流程、功能分工 |
| [app/developer-requirements.md](./app/developer-requirements.md) | 应用开发需求与注意事项 | 硬性需求、单价设计、会员设计、易错坑、开发规范与案例 |
| [app/](./app/README.md) | **应用接入文档包** | 可整目录下发给应用开发者；含下列 6 份 |
| [app/platform-integration-tasks.md](./app/platform-integration-tasks.md) | 平台方应用接入任务清单 | 平台方视角：准入准备、配置、交付物、验收（派任务用） |
| [app/platform-resource-auth-checklist.md](./app/platform-resource-auth-checklist.md) | 平台资源与认证配置清单 | 平台方填空（ID/密钥/计费/身份/测试账号）→ 下发给开发者 |
| [app/developer-integration-guide.md](./app/developer-integration-guide.md) | 应用开发者对接指南 | 开发者视角：需了解什么、要实现哪三件事、怎么对接 |
| [backend-token-billing-contract.md](./backend-token-billing-contract.md) | 计费深度契约 | 按量/按次/套餐预付的对接契约 |

阅读顺序：总览 → 商品底座 → 会员/应用 → 扣费集成。

---

## 接口契约与规范

| 文档 | 说明 |
|---|---|
| [full-api-design.md](./full-api-design.md) | 全量 API 设计（接口/字段/错误码权威） |
| [frontend-api-reference.md](./frontend-api-reference.md) | 前端对接 API 参考 |
| [api-pagination-standard.md](./api-pagination-standard.md) | 扁平分页规范（D-95） |
| [api-issues.md](./api-issues.md) | 接口问题记录 |
| [database-schema-design.md](./database-schema-design.md) | 数据库表结构设计 |
| [data-scope-design.md](./data-scope-design.md) | 数据权限范围设计 |
| [data-scale-sharding-plan.md](./data-scale-sharding-plan.md) | 数据规模与分片规划 |

## 架构与基础设施

| 文档 | 说明 |
|---|---|
| [base-architecture-environment.md](./base-architecture-environment.md) | 基础架构与环境 |
| [backend-stage2-architecture-roadmap.md](./backend-stage2-architecture-roadmap.md) | 第二阶段架构路线图 |
| [backend-token-gateway-design.md](./backend-token-gateway-design.md) | Token 网关设计 |
| [backend-token-gateway-integration.md](./backend-token-gateway-integration.md) | Token 网关集成 |
| [token-gateway-openai-compat.md](./token-gateway-openai-compat.md) | OpenAI 兼容对话 |
| [backend-sk-auth-contract.md](./backend-sk-auth-contract.md) | SK 鉴权契约 |
| [backend-chat-workbench-contract.md](./backend-chat-workbench-contract.md) | 聊天工作台契约 |
| [cloud-resource-app-marketplace-mvp.md](./cloud-resource-app-marketplace-mvp.md) | 云资源应用市场 MVP |

## 后端模块开发计划

| 文档 | 说明 |
|---|---|
| [backend-dev-plan-backend-b.md](./backend-dev-plan-backend-b.md) | 后端乙（product/order/billing） |
| [backend-dev-plan-backend-c.md](./backend-dev-plan-backend-c.md) | 后端丙（asset/membership/app/provision/content） |
| [backend-a-group-roles-design.md](./backend-a-group-roles-design.md) | 后端甲 分组角色设计 |
| [backend-b-fix-plan.md](./backend-b-fix-plan.md) | 后端乙 修复计划 |
| [backend-b-go-live-checklist.md](./backend-b-go-live-checklist.md) | 后端乙 上线检查单 |
| [backend-stage2-go-live-checklist.md](./backend-stage2-go-live-checklist.md) | 第二阶段上线检查单 |
| [backend-stage2-master-tracking.md](./backend-stage2-master-tracking.md) | 第二阶段主跟踪 |
| [backend-stage2-task-schedule.md](./backend-stage2-task-schedule.md) | 第二阶段任务排期 |
| [backend-stage2-risk-review.md](./backend-stage2-risk-review.md) | 第二阶段风险评审 |

## 第三阶段（Agent / MCP）契约

| 文档 | 说明 |
|---|---|
| [backend-stage3-agent-category-contract.md](./backend-stage3-agent-category-contract.md) | Agent 分类契约 |
| [backend-stage3-agent-visibility-contract.md](./backend-stage3-agent-visibility-contract.md) | Agent 可见性契约 |
| [backend-stage3-mcp-integration-contract.md](./backend-stage3-mcp-integration-contract.md) | MCP 集成契约 |

## 前端对接任务与计划

| 文档 | 说明 |
|---|---|
| [frontend-dev-plan-backend-a.md](./frontend-dev-plan-backend-a.md) / [b](./frontend-dev-plan-backend-b.md) / [c](./frontend-dev-plan-backend-c.md) | 前端对接各后端开发计划 |
| [frontend-task-admin-console.md](./frontend-task-admin-console.md) | 管理后台任务 |
| [frontend-task-user-console.md](./frontend-task-user-console.md) | 用户控制台任务 |
| [frontend-definition-of-done.md](./frontend-definition-of-done.md) | 前端完成定义（五道关卡） |
| [frontend-conversation-persistence.md](./frontend-conversation-persistence.md) | 会话持久化对接 |
| [frontend-task-product-access-prices.md](./frontend-task-product-access-prices.md) | 商品访问/价格前端任务 |

> 前端任务文档较多（`frontend-task-*.md`），按模块/阶段命名，按需检索。

## 测试与验收

| 文档 | 说明 |
|---|---|
| [test-plan.md](./test-plan.md) | 测试计划 |
| [api-test-guide-backend-a.md](./api-test-guide-backend-a.md) / [b](./api-test-guide-backend-b.md) | 后端接口测试指南 |
| [apipost-test-guide-backend-b.md](./apipost-test-guide-backend-b.md) / [c](./apipost-test-guide-backend-c.md) | ApiPost 测试指南 |
| [m1](./m1-manual-test-apipost.md) / [m2](./m2-manual-test-apipost.md) / [m3](./m3-manual-test-apipost.md) / [m4](./m4-manual-test-apipost.md) / [stage3](./stage3-manual-test-apipost.md) | 各里程碑手动测试用例 |
| [backend-stage2-m2-test-report.md](./backend-stage2-m2-test-report.md) / [m3](./backend-stage2-m3-test-report.md) / [m4](./backend-stage2-m4-test-report.md) | 第二阶段测试报告 |
| [backend-stage3-test-report.md](./backend-stage3-test-report.md) | 第三阶段测试报告 |
| [regression-backend-b-fixes.md](./regression-backend-b-fixes.md) / [final-regression-f3-p3.md](./final-regression-f3-p3.md) | 回归测试 |

## 流程与协作规范

| 文档 | 说明 |
|---|---|
| [git-workflow.md](./git-workflow.md) | Git 工作流 |
| [development-execution-plan.md](./development-execution-plan.md) | 开发执行计划 |
| [team-task-assignment.md](./team-task-assignment.md) / [developer-task-board.md](./developer-task-board.md) | 团队分工与任务板 |
| [interface-requirements-and-project-management.md](./interface-requirements-and-project-management.md) | 接口需求与项目管理 |
| [tools.md](./tools.md) | 工具说明 |
| [pm-CLAUDE.md](./pm-CLAUDE.md) / [qa-CLAUDE.md](./qa-CLAUDE.md) | 产品经理 / 测试 角色规范 |

---

> 维护约定：新增文档后在本索引对应分类下补一行；业务功能类文档优先归入顶部「业务功能与计费文档体系」。
</content>

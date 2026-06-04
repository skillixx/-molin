---
name: project-overview
description: Molin 云管理平台项目概述，技术栈和三阶段交付计划
metadata: 
  node_type: memory
  type: project
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

Molin 是一个云资源与应用售卖平台，支持商品售卖、计费、用户资产、实名认证、应用管理、会员体系，以及 GPU 租赁、Agent/Skills 市场、Token 网关。

**技术栈：** Vue3 + Vite + TypeScript + Element Plus / Go + Gin + GORM / MySQL 8 + Redis 7 + RabbitMQ + MinIO

**三阶段交付：**
- 第一阶段（Week 1–4）：平台底座 + 应用售卖（auth、iam、product、billing、asset、app、membership、content）
- 第二阶段（Week 5–7）：GPU 租赁
- 第三阶段（Week 8–12）：Agent / Skills / Token 网关

**设计文档位置：**
- `docs/cloud-resource-app-marketplace-mvp.md` — 主设计文档（已重新设计）
- `docs/database-schema-design.md` — 数据库表设计（已更新安全约定）
- `docs/full-api-design.md` — 完整 API 设计（已补充限流、支付回调、Token 路由）
- `docs/development-execution-plan.md` — 开发执行计划（已与三阶段对齐）
- `docs/data-scale-sharding-plan.md` — 数据量和分库分表规划

**Why:** 用户希望设计被记录在文档中，并根据评审意见进行了重新设计。
**How to apply:** 参考 docs/ 下文档，主设计文档是 cloud-resource-app-marketplace-mvp.md。

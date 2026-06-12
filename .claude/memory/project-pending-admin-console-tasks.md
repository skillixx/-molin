---
name: project-pending-admin-console-tasks
description: admin-console 待开发页面缺口（审计日志、用户分组管理），2026-06-12 调研记录
metadata: 
  node_type: memory
  type: project
  originSessionId: 702abc53-b89e-464e-bf60-d1ca4200e2ec
---

# admin-console 待办（2026-06-12 前端工程师甲调研）

调研结论：以下两个后端接口已上线（main 分支），但 admin-console 完全没有对应前端页面/路由/菜单：

1. **审计日志**：`GET /api/admin/audit-logs`（[[design-decisions]] 中 iam 模块，需 `role:manage` 权限）
   - 工作量：约 0.5-1 人日（小）
   - 需要：`src/api/audit.ts`、`src/types/audit.ts`、`src/views/iam/AuditLogListView.vue`、路由+侧边栏菜单（建议放"权限管理"子菜单下）
   - 顺带 TODO：`src/router/index.ts` 中 `meta.permission` 校验目前是占位（"待后续扩展"），做审计日志页面时大概率需要补上

2. **用户分组管理**：`/api/admin/user-groups` 等 16 个接口（Phase 0-3 已上线，需 `group:manage` + 管理员双重认证）
   - 工作量：约 3-5 人日（大），建议拆 2-3 个子任务
   - 需要：分组 CRUD、成员管理、分组权限、邀请码管理 4 块页面/Tab，参考 `server/internal/modules/iam/dto/group_dto.go`
   - 建议独立分支 `feature/frontend-a-admin-user-groups`，未在前端 A 现有 7 个分支规划中

**Why:** `docs/frontend-api-reference.md` 在 PR#10 中已补全这些接口的文档，但前端实现仍是空白——这是文档先于实现的情况（与通常"实现先于文档"相反）。
**How to apply:** 用户问"前端 A 接下来做什么"或规划下一阶段任务时，优先提及这两项；如需要立项，按上述分支名创建。

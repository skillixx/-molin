# 前端验收缺陷回报 — 后端丙对接（给前端团队 / Codex）

> 来源：2026-06-19 前端验收（L1 构建 + L2 前端 API 层↔后端契约一致性核验）。
> 验收总结论：**通过**——两端 `type-check`/`lint`/`build` 全绿；L2 全部受测端点路径/方法/字段/分页结构与后端一致，**无 P0/P1/P2**。
> 本文仅列 **3 项 P3**（均无运行时阻断），需 **前端（Codex）** 修复；后端契约本身正确、无需改动。每项已对后端源码逐一核实。
> 字段 SSOT：`docs/frontend-api-reference.md`；分页区分见 `docs/frontend-dev-plan-backend-c.md` §1。

---

## BUG-1 [P3] 适配器列表（AP6）按不分页处理，但后端是分页接口 ⚠️ 影响最实在

- **现象**：超过一页（>20 条）的适配器只显示首页 20 条，且无分页控件。
- **前端**：`web/admin-console/src/api/app-admin.ts:40-42`
  ```ts
  export function listAdminAppAdapters() {
    // 应用适配器当前是不分页列表，页面直接渲染 items。
    return http.get<unknown, ItemsResult<AdminAppAdapter>>('/admin/app-adapters')
  }
  ```
- **后端实际**：`GET /api/admin/app-adapters` 返回**扁平分页** `{ items, page, page_size, total }`，并支持 `?page=&page_size=&status=` 查询参数（`server/internal/modules/app/handler/app_handler.go:166-184`，`page_size` 默认 20、上限 100）。
- **期望**：改为分页接口处理——
  ```ts
  export function listAdminAppAdapters(params?: { page?: number; page_size?: number; status?: string }) {
    return http.get<unknown, PageResult<AdminAppAdapter>>('/admin/app-adapters', { params })
  }
  ```
  页面读取 `total` 接分页控件（与 `listAdminApps` 一致），并可选支持 `status` 过滤。
- **备注（文档侧待办）**：现有分页区分文档（`frontend-dev-plan-backend-c.md` §1、`frontend-api-reference.md` §13）的「分页清单」**遗漏了 AP6 `/admin/app-adapters`**，应将其补入「分页」一类（与 AS4/AP2/M9/C5/C9 同级）。此项属后端对接文档修订，将另行处理。

## BUG-2 [P3] grantUserMembership 返回类型声明与实际不符

- **前端**：`web/admin-console/src/api/membership-admin.ts:60-66`
  ```ts
  return http.post<unknown, AdminUserMembership>('/admin/user-memberships', data)
  ```
  声明返回 `AdminUserMembership` 对象。
- **后端实际**：`POST /api/admin/user-memberships` 成功返回 `{ "message": "开通成功" }`（`server/internal/modules/membership/handler/membership_handler.go:264`），**不含**会员对象。
- **期望**：返回类型改为 `{ message: string }`（与同模块其它写操作如 `updateUserMembership`/`updateMembershipLevel` 一致）。
- **影响**：无运行时影响（页面开通后是重新拉取 M9 列表，未消费该返回对象）；仅类型不准。

## BUG-3 [P3] 适配器 service_name 必填性与后端不一致

- **前端**：`web/admin-console/src/api/app-admin.ts:50,64` 与 `web/admin-console/src/types/app-admin.ts:24` 均为 `service_name: string`（必填、非空）。
- **后端实际**：`CreateAdapterReq.ServiceName *string` / `UpdateAdapterReq.ServiceName *string`（可选/可空，`server/internal/modules/app/dto/app_dto.go:73,84`）。
- **期望**：二选一——
  - 若产品上 `service_name` 确为必填：保留前端约束（前端更严、不会触发后端报错），仅需在文档注明口径；
  - 若允许为空：类型放宽为 `service_name?: string | null`，表单不强制校验。
- **影响**：前端更严，不会造成后端错误；仅契约口径一致性。建议与后端/产品确认 `service_name` 是否必填后统一。

---

## 处理建议

- **BUG-1** 建议优先修（唯一有实际体验影响：适配器多于一页会漏显）。
- **BUG-2 / BUG-3** 为类型/口径对齐，可随手版本修正，不阻塞上线。
- 三项均 P3，不构成第一阶段前端验收的放行阻断；本次验收结论为「通过（附 3 项 P3 待办）」。

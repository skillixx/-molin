# 后端丙接口回归测试报告（asset / membership / app / provision / content）

> 测试执行：测试工程师　|　报告日期：2026-06-18
> 测试类型：接口/功能回归 + 关键缺陷修复验证（C-FIX 系列）
> 结论：**部分通过，建议允许合并上线**（P0/P1/P2 = 0，唯一 P3 不阻断）

---

## 1. 测试环境与部署

| 项 | 值 |
|---|---|
| 被测分支 | `origin/feature/backend-c-p3-cleanup`（后端丙全部接口累积分支） |
| 被测 commit | `6cfc1e08c9ba929f20211099093f7d1c44aa2351`（后端丙：清理 P3 小缺陷） |
| 编译 | 隔离 git worktree 内基于该 commit `GOOS=linux GOARCH=amd64 go build`，零错误 |
| 部署 | linux/amd64 二进制部署至测试服 `~/molin/molin-api` 并重启 |
| API | `8.130.9.163:8080`（APP_ENV=test） |
| DB | MySQL `:13306`，migration 已到 `000026`（含 C-FIX-1 续期定位索引，已确认存在） |
| 启动日志 | `api.log` 无启动/migration 错误；`ExpireMembershipsJob`（C-FIX-5）确认正在运行 |
| 测试脚本 | `tests/test_backend_c_regression.py`（测试服执行副本 `~/molin/test_backend_c_regression.py`） |

> 测试过程未触碰 `main`，未做任何 commit/push/merge；临时编译 worktree 已清理。

---

## 2. 总体统计

| 指标 | 数值 |
|---|---|
| 总用例 | 75 |
| 通过 | 74 |
| 失败 | 1 |
| 通过率 | **98.7%** |
| 缺陷分布 | P0=0　P1=0　P2=0　**P3=1** |

---

## 3. 重点修复项逐项验证（C-FIX 系列）

### C-FIX-1　会员续期（原 P1）— ✅ 通过
- 同一用户对同一 level 重复开通：`expires_at` 叠加延长，**不产生多条 active**。
- 并发 5 次开通：终态仍为**单条 active**（应用层行锁 + `(user_id, level_id)` 复合索引防双插）。
- `FindActive` 已带 `ORDER BY expires_at DESC`，命中语义明确。
- 验证方式：HTTP 触发 + DB 终态独立核验（直查 `user_memberships`）。

### C-FIX-2a　资产 cancel（P2）— ✅ 通过
- `PATCH /api/admin/assets/{id}` `action=cancel`（带 `reason`）：`active → cancelled` 成功。
- 关联 `entitlement` 同步置 `cancelled`。
- 写入 `asset_events`（`event_type=cancelled`，`before/after/remark` 字段正确）。
- 重复 cancel 被正确拒绝；非法 action 的错误提示中包含 `cancel` 枚举。

### C-FIX-4　分页信封补 page_size（P2，D-95 红线）— ✅ 通过
- asset / membership / content / app / help / adapter 共 **6 个 admin 列表**返回均含 `{items, page, page_size, total}` 四字段，历史缺失的 `page_size` 已补齐。

### C-FIX-5　会员过期定时任务（P2）— ✅ 通过（运行确认）
- 启动日志确认 `ExpireMembershipsJob` 正在运行；流转 SQL 经源码审查正确。
- 注：整点实际批量流转周期为 1 小时，超出回归窗口，采用「日志确认运行 + 源码审查 + 造过期记录」组合验证（见 §5 未覆盖说明）。

### C-FIX-6　公告分页 + SQL 预过滤 + 可见范围（P2）— ✅ 通过
- `/api/announcements` 支持 `page/page_size`，SQL 层预过滤。
- `visible_scope` 过滤正确：`admins`、`members`、未命中的 `roles` 对普通用户 **fail-closed 不泄露**；为用户加上目标角色后，对应 `roles` 公告可见。

### 接口缺口补齐 — ✅ 通过
- asset_summary、会员管理端写接口、provision 生命周期编排均验证可用。

---

## 4. 通用鉴权 / 安全用例

| 用例 | 结果 |
|---|---|
| 权限码 seed：asset/membership/content/app 四模块无权限访问返回 **403（非 500）** | ✅ 历史 P1 根因未复现 |
| `/api/my/*` 越权访问他人资源返回 **403** | ✅ |
| 公开接口（memberships、help/categories、help/articles）免登录可访问 | ✅ |
| admin 接口无权限返回 **403** | ✅ |
| `GET /api/marketplace/apps/{id}` 无 token 返回 **401** | ✅ |

---

## 5. 缺陷清单

### BUG-C1　`GET /api/my/membership` 响应结构不对称　【membership】【P3，非阻断】

- **现象**：
  - 无会员时：`data = {"membership": null}`
  - 有会员时：会员字段**平铺在 `data` 顶层**，而非 `data.membership = {...}`
- **期望**：按 `docs/backend-dev-plan-backend-c.md` §4.2 语义，两种情形统一为 `data.membership`。当前不一致导致前端需分支判断。
- **代码位置**：`server/internal/modules/membership/handler/membership_handler.go` 的 `GetMyMembership`（nil 分支与有会员分支结构不一致）。
- **影响**：不影响数据正确性与核心功能，属契约一致性问题。
- **建议**：合并后小修，统一为 `data.membership`，并同步 `docs/frontend-api-reference.md` 与用户端控制台（避免「字段变更未同步前端」历史根因复发）。

---

## 6. 未覆盖项（已给替代验证，非静默跳过）

| 未覆盖项 | 原因 | 替代验证 |
|---|---|---|
| provision 真实开通链路 | 需后端乙 product/order/billing 下单触发，跨模块依赖 | 经管理端 cancel 间接验证 `provision.Cancel→asset.CancelAsset`；经管理端 grant 等价验证 `CreateOrRenewMembership` |
| ExpireMembershipsJob 整点实际流转 | 周期 1 小时 > 回归窗口 | 日志确认运行 + 源码审查流转 SQL + 造过期记录 |
| ConsumeEntitlement 买断配额消耗 | 设计判定为 LATER（无买断商品需求） | 不立项不测 |
| C-FIX-2b 退款 → 资产自动取消联动 | 依赖后端乙 `paid→refunded` 落地（第一阶段不实现） | 不立项不测 |
| provision Renew / Suspend / Resume | 设计为占位实现，待业务场景立项 | 不立项不测 |

---

## 7. 结论与建议

1. **结论**：后端丙接口回归 **部分通过（98.7%）**，无 P0/P1/P2 缺陷，唯一 P3（BUG-C1）不阻断核心功能。
2. **合并建议**：**建议允许合并上线**。BUG-C1 可在合并前由后端丙顺手修正，或合并后单独小修并同步前端契约文档。
3. **后续**：
   - 合并到 main 后，建议在测试服重新部署 main 二进制，避免长期运行 feature 分支二进制。
   - LATER 三项（买断配额 / 退款联动 / Renew·Suspend·Resume）触发条件满足时再单独立项测试。

---

> 测试脚本：`tests/test_backend_c_regression.py`

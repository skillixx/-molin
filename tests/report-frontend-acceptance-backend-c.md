# 前端验收签字报告 — admin-console / user-console

- **日期**：2026-06-19
- **仓库**：`/home/pc-w1/molin`（main，HEAD 验收时为 `3269a2c`）
- **后端基线**：测试服 `8.130.9.163:8080`，molin-api md5=`371303d`（最新 main，含 #174 AP1 白名单）
- **验收脚本**：`tests/api/l2_contract_verify.py`
- **达到层级**：**L1 + L2**（L3 真浏览器 E2E 未执行，原因见下）

## 验收层级说明

| 层 | 内容 | 是否执行 |
|---|---|---|
| L1 | 构建与静态校验（type-check / lint / build） | ✅ 执行 |
| L2 | 前端 API 层 ↔ 后端真实契约一致性核验（测试服 8080 实 curl 对照） | ✅ 执行 |
| L3 | 真浏览器 E2E（页面渲染 + 交互流程） | ❌ 未执行 |

> L3 未执行原因：环境未安装浏览器自动化驱动（playwright/puppeteer，已探测确认无）；sandbox 内 chrome headless 对 vite SPA 无法稳定产出 DOM。按纪律「无浏览器环境则明确写明、不伪造结果」，以 **L1+L2 为验收结论**——核心契约风险已由 L2 真 API 全量覆盖。如需补 L3：可联网环境 `npm i -D playwright && npx playwright install chromium`，vite dev 的 `/api` 代理已指向测试服 8080，具备直接跑 E2E 条件。

## L1：构建与静态校验（全绿）

| 工程 | install | type-check (vue-tsc) | lint | build (vite) |
|---|---|---|---|---|
| admin-console | ✅ | ✅ 无错误 | ✅（仅 ESM 提示警告） | ✅ `built in 2.81s` |
| user-console | ✅ | ✅ 无错误 | ✅ | ✅ `built in 2.76s` |

> 唯一非阻断提示：主 chunk ~1.24MB（>500KB 警告），属构建优化建议。

## L2：前端 API 层 vs 后端真实契约（27 条断言）

方法：逐一比对 `src/api/*.ts` + `src/types/*` 与后端 `route.go`/`dto`/`handler`，并用测试服 8080 真实 curl 对照（新建 admin/user token、精确主键造数与清理）。

- **后端丙管理端**（FA-06/07/09/10）：资产列表分页/指定用户资产不分页/cancel 用 remark、公告分页/帮助分类不分页/文章分页、会员等级与权益不分页/用户会员分页且内联 level_name/asset_id 恒在/M10 永久 duration_days=null/M11 取消改期、应用分页/适配器、三个 JSON 字符串字段 —— 一致 ✅
- **后端丙用户端**（FB-07/08/09）：我的资产/权益不分页、公开等级/公开权益端点、my/membership 对称且内联 level_name、公告完整分页、帮助分类文章不分页、文章详情 data 直接为对象 —— 一致 ✅
- **后端乙抽查**：钱包 `wallet_id`、流水分页、充值响应字段、消费记录 —— 一致 ✅

**结论**：所有受测端点的路径/方法/请求字段/关键响应字段/分页结构与前端期望**一致**；项目历史高频根因（后端字段变更未同步前端）本轮**未发现实质偏差**；无 P0/P1/P2。

### 缺陷清单（3 项 P3，均无运行时阻断，属前端代码/类型修复）

| # | 前端位置 | 问题 | 后端事实 |
|---|---|---|---|
| BUG-1 | `app-admin.ts:40-42` | 适配器列表按不分页 `{items}` 处理 | `GET /admin/app-adapters` 实为分页 `{items,page,page_size,total}`（`app_handler.go:180`，支持 page/page_size/status）→ >20 条会漏显，**建议优先修** |
| BUG-2 | `membership-admin.ts:66` | `grantUserMembership` 返回类型声明为会员对象 | 后端返 `{message}`（`membership_handler.go:264`）；类型应为 `{message:string}` |
| BUG-3 | `app-admin.ts:50` / `types/app-admin.ts:24` | `service_name` 必填 | 后端 `*string` 可空（`app_dto.go:73,84`）；口径待统一 |

> 详见 `docs/frontend-acceptance-defects-backend-c.md`（已合并 main，PR #178）。BUG-1 暴露的「对接文档分页清单遗漏 AP6」已在文档侧修订补入分页一类。

## 数据清理核对

- L2 造数全部按精确主键删除（users/roles/sessions/wallets/transactions/memberships/levels/benefits/announcements/help_*/applications/adapters/verification_codes），脚本内造数用户残留计数 = 0。
- 二次只读核对 `users WHERE email LIKE 'l2_%@testmail.io'` = 0（LIKE 仅用于只读计数，未用于 DELETE）。
- 未触碰既有业务数据，未改前端代码/.env，未重启后端；远端临时脚本已删。

## 签字结论：**通过（附 3 项 P3 待办）**

- L1 两端 type-check/lint/build 全绿；L2 所有受测端点契约一致，无 P0/P1/P2；
- 3 项 P3 均无运行时阻断，属前端代码/类型修正（已回报前端团队，见缺陷文档），不阻塞上线；
- L3 未执行不影响结论（核心契约风险已由 L2 真 API 覆盖）。

> 测试工程师（QA）签字，2026-06-19。建议：BUG-1 优先修（适配器超一页漏显）；如要求 L3 通过方放行，请在可联网环境安装 playwright 后补跑关键页加载与一次写操作冒烟。

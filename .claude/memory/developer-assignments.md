---
name: developer-assignments
description: 开发者分工、负责模块、代码路径、Agent 文件位置和周任务清单
metadata: 
  node_type: memory
  type: project
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

# 开发者分工

## 当前项目状态（截至 2026-06-09）

**第一阶段（Week 1-4：平台底座 + 应用售卖闭环）已于 2026-06-07 正式验收通过。**

- 三位后端工程师全部模块代码均已实现并部署到测试服务器
- 端到端全链路测试 37/37 全部通过，3 个 P1 缺陷（钱包懒创建/会员购买门槛/封禁接口+权限码）均已修复闭环
- 全量 API 地毯式测试（111 项断言，105 通过），发现并修复 3 个新 P1（product:view/order:list 权限码缺失、充值方式校验缺失），migration 000013 已部署生效
- 注册接口于 2026-06-09 统一为双 OTP 单入口（见 design-decisions.md），旧接口已下线
- 2026-06-10 修复后端 A 注册唯一性问题：邮箱/手机号/用户名写入前规范化，并用 MySQL 唯一键冲突兜底；已用临时 `/tmp/go` 执行 `gofmt` 和后端 `go test ./...` 通过

已产出的测试报告均在 `tests/` 目录，人工测试指导文档：`tests/manual-test-guide-backend-apis.md`（2230行，ApiPost 教程式）。

## 后端 A — 账号 + 权限 + 实名（Week 1 最高优先级）

负责模块：
- `server/internal/modules/auth/` — 注册、登录、会话、验证码、JWT
- `server/internal/modules/iam/` — 角色、权限、动态授权、Redis 缓存
- `server/internal/modules/identity/` — 实名认证（HMAC-SHA256）
- `server/internal/middleware/` — auth.go（RequireAuth）、permission.go（RequirePerm）
- `server/pkg/db/`、`server/pkg/cache/`、`server/pkg/crypto/`、`server/pkg/jwt/` — 公共基础（其他人依赖）

Agent 文件：
- `server/internal/modules/auth/CLAUDE.md` — 含完整代码模板
- `server/internal/modules/iam/CLAUDE.md`
- `server/internal/modules/identity/CLAUDE.md`
- `server/internal/bootstrap/CLAUDE.md` — bootstrap 接入说明

**Why:** 其他所有模块依赖 pkg/db、pkg/jwt、RequireAuth 中间件，必须最先完成。
**How to apply:** 后端 A 未完成时，其他开发者无法集成鉴权，应优先 unblock 他。

## 后端 B — 商品 + 订单 + 钱包 + 计费（Week 2-3）

负责模块：
- `server/internal/modules/product/` — 商品/套餐/价格/购买入口
- `server/internal/modules/order/` — 订单状态机
- `server/internal/modules/billing/` — 钱包乐观锁扣费、支付回调幂等
- `server/internal/modules/finance_consumer/` — 消费事件幂等处理

Agent 文件：
- `server/internal/modules/billing/CLAUDE.md` — 含扣费事务模板和回调幂等模板
- `server/internal/modules/product/CLAUDE.md` — 含价格优先级和购买链路
- `server/internal/modules/order/CLAUDE.md` — 含订单状态机和订单号格式
- `server/internal/modules/finance_consumer/CLAUDE.md`

**Why:** 钱包扣费和支付回调是资金安全核心，必须人工审查。
**How to apply:** 这两个方法的代码不接受 AI 直接输出，需开发者手写并经产品经理 Review。

## 后端 C — 资产 + 会员 + 应用 + 内容 + 开通（Week 3-4）

负责模块：
- `server/internal/modules/asset/` — 用户资产、权益额度（含并发消耗）
- `server/internal/modules/provision/` — ProvisionHandler 接口 + AppProvisioner
- `server/internal/modules/membership/` — 会员等级、权益
- `server/internal/modules/app/` — 应用商品业务详情
- `server/internal/modules/content/` — 公告、帮助文档
- `server/internal/jobs/` — 资产到期定时任务

Agent 文件：
- `server/internal/modules/asset/CLAUDE.md` — 含并发权益消耗模板
- `server/internal/modules/provision/CLAUDE.md` — ProvisionHandler 接口定义

## 后端 D — Token 网关 + Agent + Skills（第二阶段，Week 5–9，2026-06-19 新设）

负责模块（第二阶段 AI 业务，目录待建）：
- `server/internal/modules/token_gateway/` — Token 上游聚合网关、模型路由、OpenAI 兼容 chat、用量计费（优先）
- `server/internal/modules/agent/` — Agent 定制市场
- `server/internal/modules/skill/` — Skills 技能市场

Agent 文件：`.claude/agents/后端工程师丁.md`（标识 backend-d，分支前缀 `feature/backend-d-*`）
权威设计：`docs/backend-token-gateway-design.md`（落地方案）。migration 从 000030 起。

**Why:** 阶段规划调整（PR #188）后 Token 网关提前到第二阶段，用户决定为其新设专职负责人后端丁（而非后端乙兼）。
**How to apply:** token_gateway/agent/skill 任务派给后端工程师丁；计费走 finance_consumer（乙）、额度走 asset/provision（丙）、鉴权权限码挂接走 iam（甲），不跨改他人模块。

## 前端 A — 管理后台（web/admin-console）

Agent 文件：`web/admin-console/CLAUDE.md`（含完整代码模板）

Week 1 核心：Axios 实例、Auth Store、路由守卫、登录页、AdminLayout、用户列表、角色管理

## 前端 B — 用户控制台（web/user-console）

Agent 文件：`web/user-console/CLAUDE.md`（含 Token 自动刷新拦截器和购买幂等模板）

Week 1 核心：Token 自动刷新拦截器、注册页、登录页、实名认证页、商品市场

## 运维 — 环境与部署

Agent 文件：`infra/CLAUDE.md`

## 产品经理 — 代码合并与评审

Agent 文件：`docs/pm-CLAUDE.md`，规范：`docs/git-workflow.md`

## 测试 — 功能验收

Agent 文件：`docs/qa-CLAUDE.md`，计划：`docs/test-plan.md`

## 主要文档

- `docs/developer-task-board.md` — 按人分组的完整 Week 1–4 文件清单（最新）
- `docs/team-task-assignment.md` — 详细模块规划和代码路径
- `server/internal/bootstrap/CLAUDE.md` — 所有模块的接入顺序和 bootstrap 模板

**Why:** 用户要求按开发者分工规划代码位置，并创建 Agent 文件辅助开发，于 2026-06-04 完成第二次更新（加入具体代码模板和周任务清单）。
**How to apply:** 开发者提问时，根据其负责模块推荐对应 CLAUDE.md，直接给出代码模板而非描述。

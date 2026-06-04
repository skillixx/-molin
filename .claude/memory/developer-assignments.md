---
name: developer-assignments
description: 开发者分工、负责模块、代码路径、Agent 文件位置和周任务清单
metadata: 
  node_type: memory
  type: project
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

# 开发者分工

## 当前项目状态

骨架已存在，所有模块目录只有 CLAUDE.md，没有业务代码。
Week 1 开发尚未开始，后端 A 应最先启动。

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

---
name: feedback-git-workflow
description: 开发必须在 feature 分支上进行，禁止直接 push main；每个开发者有预定义的分支命名规范和分支规划表
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

开发前必须切出 feature 分支，不能直接提交或推送到 main。

**Why:** 项目有严格的 PR 审查流程（CI + Code Review + 产品经理验收），直接推 main 绕过了所有审核环节。上一轮开发（Week 1 后端甲）曾直接 push main，已确认不再重复。

**How to apply:**
1. 确认当前开发者身份（如"后端 A"对应标识 `backend-a`）
2. 从最新 main 切出分支：`git checkout main && git pull && git checkout -b feature/{前缀}-{功能}`
3. 开发完成后 push 并提 PR，等待 CI + Code Review + 产品经理验收后合并
4. 分支合并后由产品经理通过 `gh pr merge --delete-branch` 删除远端分支

---

## 分支前缀对照表

| 开发者 | 前缀 |
|---|---|
| 后端工程师甲 | `feature/backend-a-` |
| 后端工程师乙 | `feature/backend-b-` |
| 后端工程师丙 | `feature/backend-c-` |
| 前端工程师甲 | `feature/frontend-a-` |
| 前端工程师乙 | `feature/frontend-b-` |
| 运维工程师 | `feature/ops-` |
| 测试工程师 | `feature/test-` |
| 产品经理 | 只合并 PR，不提分支 |

---

## 各开发者分支规划（含阶段）

### 后端工程师甲（backend-a）

| 阶段 | 分支名 |
|---|---|
| Week 1 | `feature/backend-a-auth-register-login` |
| Week 1 | `feature/backend-a-iam-role-permission` |
| Week 1 | `feature/backend-a-identity-realname` |
| Week 1 | `feature/backend-a-audit-log` |
| Week 2 | `feature/backend-a-auth-ban-unlock` |
| Week 2 | `feature/backend-a-iam-admin-api` |

### 后端工程师乙（backend-b）

| 阶段 | 分支名 |
|---|---|
| Week 2 | `feature/backend-b-product-catalog` |
| Week 2 | `feature/backend-b-product-plans-prices` |
| Week 3 | `feature/backend-b-billing-wallet` |
| Week 3 | `feature/backend-b-payment-callback` |
| Week 3 | `feature/backend-b-order-purchase` |
| Week 3 | `feature/backend-b-finance-consumer` |

### 后端工程师丙（backend-c）

| 阶段 | 分支名 |
|---|---|
| Week 2 | `feature/backend-c-app-market` |
| Week 2 | `feature/backend-c-provision-handler` |
| Week 2-3 | `feature/backend-c-membership-levels` |
| Week 3 | `feature/backend-c-asset-management` |
| Week 4 | `feature/backend-c-content-cms` |

### 前端工程师甲（frontend-a，admin-console）

| 阶段 | 分支名 |
|---|---|
| Week 1 | `feature/frontend-a-admin-login-layout` |
| Week 1 | `feature/frontend-a-admin-user-management` |
| Week 1-2 | `feature/frontend-a-admin-role-permission` |
| Week 2-3 | `feature/frontend-a-admin-product-manage` |
| Week 3 | `feature/frontend-a-admin-order-wallet` |
| Week 3-4 | `feature/frontend-a-admin-asset-identity` |
| Week 4 | `feature/frontend-a-admin-content-cms` |

### 前端工程师乙（frontend-b，user-console）

| 阶段 | 分支名 |
|---|---|
| Week 1 | `feature/frontend-b-user-register-login` |
| Week 1 | `feature/frontend-b-identity-certification` |
| Week 1 | `feature/frontend-b-user-layout` |
| Week 2 | `feature/frontend-b-marketplace-browse` |
| Week 3 | `feature/frontend-b-purchase-flow` |
| Week 3 | `feature/frontend-b-wallet-recharge` |
| Week 3 | `feature/frontend-b-asset-management` |
| Week 4 | `feature/frontend-b-membership-content` |

### 运维工程师（ops）

| 阶段 | 分支名 |
|---|---|
| Week 1 | `feature/ops-local-docker-compose` |
| Week 1 | `feature/ops-ci-pipeline` |
| Week 2 | `feature/ops-prod-dockerfile` |
| Week 2 | `feature/ops-nginx-config` |
| Week 3 | `feature/ops-deploy-script` |
| Week 3 | `feature/ops-deploy-test-workflow` |

### 测试工程师（qa）

| 阶段 | 分支名 |
|---|---|
| Week 1 | `feature/test-seed-data-core` |
| Week 2 | `feature/test-auth-iam-cases` |
| Week 3 | `feature/test-purchase-flow-cases` |
| Week 3 | `feature/test-concurrent-load` |
| Week 3 | `feature/test-payment-callback-cases` |
| Week 4 | `feature/test-membership-content-cases` |

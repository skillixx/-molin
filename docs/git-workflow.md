# Git 工作流与代码评审规范

## 1. 分支策略

```text
main
  生产分支，只接受来自 feature/* 和 hotfix/* 的 PR。
  禁止直接 push。

feature/{模块}-{功能描述}
  功能开发分支，从 main 切出，完成后合并回 main。
  命名示例：
    feature/auth-register-login
    feature/product-unified-product-model
    feature/billing-wallet-deduct
    feature/asset-user-asset
    feature/frontend-admin-user-management

hotfix/{描述}
  线上紧急修复，从 main 切出，修复后合并回 main。
  命名示例：hotfix/wallet-deduct-concurrency-bug
```

## 2. 提交规范

**Commit message 必须使用中文，格式：**

```text
{类型}：{说明}

{详细描述（可选，多行）}

影响模块：{模块名}
```

类型说明：

| 类型 | 用途 |
|---|---|
| 新增 | 新功能 |
| 修复 | Bug 修复 |
| 重构 | 代码重构（不影响功能） |
| 优化 | 性能优化 |
| 文档 | 文档更新 |
| 测试 | 测试用例 |
| 配置 | 配置和构建变更 |

**示例：**

```text
新增：邮箱注册和手机号注册接口

实现邮箱和手机号注册流程，包含：
- 验证码校验
- 密码 bcrypt hash
- 创建用户记录
- 初始化钱包
- 生成 JWT 和 Refresh Token

影响模块：auth
```

## 3. PR 规范

### 3.1 PR 标题格式

```text
[模块] 功能描述
```

示例：
- `[auth] 邮箱注册和手机号注册`
- `[billing] 钱包乐观锁扣费和支付回调`
- `[前端-管理后台] 用户管理和角色管理页面`

### 3.2 PR 描述模板

每个 PR 必须填写以下内容（在 PR 描述中）：

```markdown
## 功能说明

本次 PR 实现了 [功能描述]。

## 变更内容

- 新增 `modules/auth/service/auth_service.go`：注册和登录业务逻辑
- 新增 `modules/auth/handler/auth_handler.go`：HTTP Handler
- 新增 `server/migrations/000001_create_auth_tables.up.sql`：建表脚本

## 测试情况

- [ ] 单元测试已通过
- [ ] 接口手动测试已通过
- [ ] 覆盖了边界情况（如重复邮箱注册、错误密码等）

## 数据库变更

本次 PR 包含以下数据库变更：
- 新增表：users、user_sessions、verification_codes

执行方式：
```bash
./scripts/migrate.sh
```

## 注意事项

[如有需要审查者特别注意的地方，在此说明]
```

### 3.3 PR 规模

- 单个 PR 原则上不超过 500 行代码变更。
- Migration 文件、生成代码除外。
- 如果 PR 太大，说明任务拆分不够细。

## 4. 代码评审规范（产品经理 + 开发者共同执行）

### 4.1 产品经理审查重点

产品经理在 PR 中重点检查：

```text
□ 接口响应的字段和文档是否一致
□ 错误提示文案是否用户友好（中文、清晰）
□ 实名认证、权限、会员等业务规则是否正确实现
□ 价格计算逻辑是否符合需求（角色价 > 会员价 > 默认价 优先级）
□ 订单状态流转是否正确
□ 用户购买后是否正确生成资产
□ 余额不足、未实名等错误码是否正确返回
```

### 4.2 开发者互审重点

```text
□ 钱包扣费是否使用了事务和乐观锁
□ 幂等处理是否正确（特别是购买接口和消费事件）
□ 敏感数据是否未明文存储（密码、身份证号、Token）
□ 权限校验中间件是否被正确应用
□ 错误处理是否统一（统一错误码，不暴露内部错误）
□ 日志中是否无敏感信息
□ 数据库操作是否有合适索引
□ 是否覆盖了并发场景
```

### 4.3 必须拒绝的代码

以下情况必须在代码评审中拒绝，要求修改后再合并：

```text
✗ 身份证号明文或 SHA256 直接 hash 存储
✗ Refresh Token 明文存储
✗ JWT 密钥硬编码在代码中
✗ 钱包扣费没有事务保护
✗ 购买接口没有幂等处理
✗ 支付回调没有签名校验
✗ 权限校验被注释或跳过
✗ SQL 注入风险（字符串拼接 SQL）
✗ 日志中打印密码、Token、身份证号
✗ 英文提交说明或英文代码注释
```

## 5. 合并流程

```text
开发者创建 feature 分支
  -> 开发完成，本地自测通过
  -> 推送分支，创建 PR
  -> CI 自动运行（构建 + 测试 + lint）
  -> 至少 1 名其他开发者 Code Review
  -> 产品经理审查业务逻辑
  -> 所有 Review 意见已解决
  -> CI 全部通过
  -> 产品经理或项目负责人合并到 main
  -> 删除 feature 分支
  -> 通知运维部署到测试环境
```

## 6. 禁止事项

```text
✗ 禁止直接 push 到 main 分支
✗ 禁止 force push（--force）到 main 分支
✗ 禁止跳过 CI 合并（--no-verify）
✗ 禁止在代码中硬编码密钥或密码
✗ 禁止提交 .env.local、.env.prod 文件
✗ 禁止未经 Review 自行合并自己的 PR
```

## 7. 发布节奏

```text
每周五：
  -> 代码冻结（不合并新 feature）
  -> 回归测试
  -> 测试通过后由运维部署

紧急 hotfix：
  -> hotfix 分支立即修复
  -> 只需 1 名开发者 Review
  -> 测试通过后立即部署
```

## 8. 开发者分支对应表

每位开发者只在自己负责的模块分支上工作。**开始开发前必须确认身份和当前分支。**

### 8.1 分支命名规则

```text
feature/{开发者标识}-{模块}-{功能描述}

开发者标识：
  backend-a   — 后端 A
  backend-b   — 后端 B
  backend-c   — 后端 C
  frontend-a  — 前端 A
  frontend-b  — 前端 B
  ops         — 运维
  docs        — 纯文档（设计/对接文档，不含代码改动，见 §8.2b）
```

### 8.2 各开发者分支示例

| 开发者 | 负责模块 | 分支示例 |
|---|---|---|
| 后端 A | auth / iam / identity | `feature/backend-a-auth-register-login` |
| 后端 A | auth / iam / identity | `feature/backend-a-iam-rbac-permission` |
| 后端 A | auth / iam / identity | `feature/backend-a-identity-realname` |
| 后端 B | product / order / billing | `feature/backend-b-product-model` |
| 后端 B | product / order / billing | `feature/backend-b-billing-wallet-deduct` |
| 后端 B | product / order / billing | `feature/backend-b-order-state-machine` |
| 后端 C | asset / membership / app / content | `feature/backend-c-asset-management` |
| 后端 C | asset / membership / app / content | `feature/backend-c-provision-handler` |
| 后端 C | asset / membership / app / content | `feature/backend-c-content-cms` |
| 前端 A | web/admin-console | `feature/frontend-a-admin-login-layout` |
| 前端 A | web/admin-console | `feature/frontend-a-admin-user-management` |
| 前端 B | web/user-console | `feature/frontend-b-user-register-login` |
| 前端 B | web/user-console | `feature/frontend-b-marketplace-purchase` |
| 运维 | infra / CI/CD | `feature/ops-ci-pipeline` |

### 8.2b 文档类 PR 分支约定

**只改 `docs/` 下 markdown、不含任何代码改动的 PR，统一用 `feature/docs-{描述}` 前缀**（不带开发者标识），便于一眼区分文档 PR 与代码 PR。

```text
feature/docs-{描述}

示例：
  feature/docs-business-billing-system
  feature/docs-app-developer-billing-spec
  feature/docs-entitlement-quota-tips
```

约定细则：
- 仅当改动**全部落在 `docs/`**（设计文档、对接文档、规范）时适用；一旦同 PR 含代码改动，回退到 §8.1 的 `feature/{开发者标识}-{模块}-{描述}`。
- 文档 PR 同样走 PR 审查（由产品经理或对应模块负责人 review），合并前需用户确认。
- 文档 PR 不改代码、不影响构建产物；CI 仍会对其跑完整流水线。若因账户/CI 基础设施问题（非内容失败）阻塞，可经用户确认后由具备权限者合并——**这是第 5/6 节「CI 全部通过」门槛与「禁止跳过 CI 合并」的唯一例外，且仅适用纯文档 PR**；任何含代码改动的 PR 不享此例外。

### 8.3 开发前检查步骤（必须执行）

```bash
# 1. 确认当前在正确分支
git branch --show-current

# 2. 如果分支不对，从 main 切出新分支
git checkout main
git pull origin main
git checkout -b feature/{开发者标识}-{模块}-{功能描述}

# 3. 确认分支干净，无未提交的改动
git status
```

### 8.4 开发完成后（提交 PR 前）

```bash
# 1. 推送分支
git push -u origin feature/{分支名}

# 2. 在 GitHub 创建 PR，标题格式：[模块] 功能描述
# 3. 更新 README.md 的开发进度表（见下方要求）
# 4. 通知产品经理进行 Review
```

## 9. 产品经理每周工作

```text
周一：
  - 确认本周功能范围
  - 向开发者确认需求细节

周三：
  - 对已完成的 PR 进行业务逻辑评审

周五：
  - 主持当周验收
  - 确认合并到 main 的 PR 范围
  - 通知运维执行部署

持续：
  - 维护 docs/ 下的需求和规则文档
  - 整理角色清单、权限清单、状态枚举
  - 跟踪 Bug 和功能缺陷
```

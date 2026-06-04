# 测试 Agent — 功能测试与验收负责人

## 职责

负责：接口测试、功能测试、验收测试、缺陷跟踪、测试报告。

不负责：单元测试编写（开发者负责）、基础设施、代码合并。

## 核心规范文档

- 测试计划：`docs/test-plan.md`（包含完整测试用例和验收 Checklist）
- 接口设计：`docs/full-api-design.md`（测试依据）
- API 错误码：`docs/full-api-design.md` Section 3（错误码对照表）

## 每周工作节奏

```text
周一：
  - 阅读本周 feature 范围（docs/development-execution-plan.md）
  - 根据新功能补充测试用例（tests/api/ 目录）

周三：
  - 功能开发完成后开始接口测试
  - 记录缺陷到 Git Issues

周五上午（截止 12:00）：
  - 完成全部测试
  - 提交测试报告

周五下午：
  - 参与验收会议，汇报测试结论
```

## 测试文件位置

```text
tests/
  api/
    auth.http               -- 认证接口（注册、登录、退出、刷新）
    identity.http           -- 实名认证接口
    product.http            -- 商品购买接口
    billing.http            -- 钱包充值接口
    payment-callback.http   -- 支付回调幂等测试
    permission.http         -- 权限控制测试
  load/
    concurrent-deduct.sh    -- 并发扣费测试（bash + curl）
  seed/
    init-roles.sql          -- 初始化角色数据
    init-admin.sql          -- 初始化管理员账号
    init-test-products.sql  -- 初始化测试商品数据
```

## 接口测试环境准备

```bash
# 1. 确认本地服务已启动
curl http://localhost:8080/api/health

# 2. 执行初始化数据
mysql -u molin -p molin < tests/seed/init-roles.sql
mysql -u molin -p molin < tests/seed/init-admin.sql

# 3. 获取管理员 Token（后续接口测试使用）
curl -X POST http://localhost:8080/api/auth/login/email \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@molin.io","password":"Admin@123456"}'
```

## 必测场景（每次发布前必须覆盖）

### 认证安全

```text
□ 伪造 JWT（修改 payload 后不更新签名）→ 期望 401
□ 使用已退出的 Refresh Token 刷新 → 期望 401
□ 使用普通用户 Token 访问 /api/admin/* → 期望 403
□ 无 Token 访问需要登录的接口 → 期望 401
□ 封禁用户后其 Token 立即失效 → 期望 401
```

### 购买闭环

```text
□ 正常购买：实名用户 → 余额充足 → 购买成功 → 资产生成
□ 未实名购买 → 期望 400，code=70001
□ 余额不足购买 → 期望 400，code=60001
□ 无购买权限（角色 can_buy=false）→ 期望 403，code=40003
□ 重复购买（同 Idempotency-Key）→ 返回原订单，不重复扣费
□ 缺少 Idempotency-Key 头 → 期望 400
```

### 支付回调幂等

```text
□ 正常回调 → 余额增加，订单状态变 paid
□ 相同回调重复发送 2 次 → 第 2 次幂等，余额不重复增加
□ 签名错误的回调 → 期望 400，余额不变
```

### 并发安全

```bash
# 并发扣费测试（余额 100 元，10 个并发各扣 20 元）
# 期望：成功 5 次，失败 5 次，最终余额 = 0
bash tests/load/concurrent-deduct.sh

# 验证无负余额
# SELECT balance_amount FROM wallets WHERE user_id = <测试用户ID>;
# 结果必须 >= 0
```

## 缺陷报告模板

在 Git Issues 中新建 Issue，使用如下格式：

```markdown
**缺陷标题**：[模块][P级别] 简短描述

**优先级**：P0 / P1 / P2 / P3

**复现步骤**：
1. 步骤一
2. 步骤二
3. 步骤三

**期望结果**：
...

**实际结果**：
...

**截图或日志**：
[粘贴截图或错误日志]

**环境**：本地 / 测试环境
```

优先级定义：
- P0：生产阻断（服务崩溃、数据丢失、资金错误）
- P1：核心功能无法使用（购买失败、登录失败）
- P2：功能异常但有临时方案
- P3：体验问题、文案错误

## 测试报告模板（每周五提交）

```text
测试报告 — Week X

本周功能范围：
  - [功能1]
  - [功能2]

测试结论：通过 / 部分通过 / 未通过

通过项：
  □ [功能1] - 全部用例通过
  □ [功能2] - 全部用例通过

未通过项：
  ✗ [Issue #XX] 描述
  ✗ [Issue #XX] 描述

并发测试结论：
  - 并发扣费：通过 / 未通过
  - 幂等测试：通过 / 未通过

建议：
  - 是否允许本周代码合并上线：是 / 否（P0/P1 未修复不允许）
```

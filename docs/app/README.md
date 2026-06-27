# 应用接入文档包（docs/app）

> 本目录是「应用接入本平台」的完整文档包。平台方可整目录下发给应用开发者。
> 上层体系总览见 [`../business-billing-overview.md`](../business-billing-overview.md)。

---

## 怎么用这个包

### 你是平台方（派任务、配资源）
1. 先读 [platform-integration-tasks.md](./platform-integration-tasks.md) —— 你要做哪些事、交付什么。
2. 按 [platform-resource-auth-checklist.md](./platform-resource-auth-checklist.md) 把实际值（ID/密钥/计费约定/身份方案/测试账号）逐项填好。
3. 把**填好的清单 + 本目录**一起下发给开发者。

### 你是应用开发者（拿到包开始干）
1. 先读 [developer-integration-guide.md](./developer-integration-guide.md) —— 你要了解什么、实现哪三件事。
2. 对照平台方给的 [资源认证配置清单](./platform-resource-auth-checklist.md) 拿全参数。
3. 写代码时查 [billing-integration-spec.md](./billing-integration-spec.md)（字段级契约）、[developer-requirements.md](./developer-requirements.md)（单价/会员/避坑/案例）。

---

## 文档清单

| 文档 | 读者 | 内容 |
|---|---|---|
| [platform-integration-tasks.md](./platform-integration-tasks.md) | 平台方 | 准入准备、配置步骤、交付物、联调验收、安全红线、派任务模板 |
| [platform-resource-auth-checklist.md](./platform-resource-auth-checklist.md) | 平台方填→开发者用 | 环境/标识/计费/认证/身份/接口/测试账号 的填空清单 |
| [developer-integration-guide.md](./developer-integration-guide.md) | 开发者 | 边界认知、核心概念、要实现的三件事、接口速查、开发步骤 |
| [billing-integration-spec.md](./billing-integration-spec.md) | 开发者 | 字段级接口契约：上报用量/额度扣减/查询，鉴权与幂等 |
| [developer-requirements.md](./developer-requirements.md) | 开发者 | 硬性需求、商品单价设计、会员设计、易错坑、4 个开发案例、自检清单 |
| [billing-integration-design.md](./billing-integration-design.md) | 平台方/开发者 | 应用如何挂成商品、购买扣费 + 使用扣费的端到端设计 |

---

## 一句话

> 平台给「卖 + 计费 + 凭证」，应用给「功能」。
> 平台方填清单 → 开发者接 JWT 认人、用前校验资产/额度、用时调计费接口（带 token+幂等键）。
</content>

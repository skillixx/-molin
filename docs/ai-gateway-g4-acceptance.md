# AI 网关 G4 最终验收记录

> 最终状态：**测试环境验收通过，允许进入 G5 开发**。
>
> 本结论覆盖 G4 主线合并、Migration `000060` 至 `000063`、Molin API 到 Bifrost 的双上游真实 E2E、商业计量、安全拒绝、回滚准备和测试凭据回收。它不代表生产部署、真实客户流量、管理后台 UI 或多模态能力已经完成。

## 1. 验收对象

| 项目 | 最终事实 |
|---|---|
| G4 功能 PR | [#316](https://github.com/skillixx/-molin/pull/316) 已合并 |
| G4 主线提交 | `19a353bffe4520c17b6703296d404566561bc06f` |
| E2E 热修复 PR | [#318](https://github.com/skillixx/-molin/pull/318) 已合并 |
| 最终主线提交 | `1b378499aff30e13a9ee85ddc35943fac67450ea` |
| 最终主线树 | `9822a9d4106258a3150abd4e62a1b6dbcf0115fd` |
| 测试环境 | `pc@8.130.9.163:10003` |
| 数据库版本 | `63:0`，即 version=63、dirty=0 |
| API 执行驱动 | `bifrost` |
| Bifrost 入口 | 测试机本地统一入口；内部 Token 和上游 SK 未写入本文 |
| 短信开关 | `SMS_ENABLED=false` |

## 2. 主线与评审

PR #316 合并前完成独立代码、测试和产品复核：

- 代码评审：P0=0、P1=0、P2=0、P3=0。
- 测试复核：P0=0、P1=0、P2=3、P3=0，允许合并。
- 产品复核：P0=0、P1=0、P2=1、P3=0，允许合并。
- CI 五项全部通过：后端、G3、G4、管理后台、用户控制台。

真实 E2E 随后发现一个此前 Mock 未覆盖的 P1：GORM 将授权快照中的嵌套 `TokenModel` 误判为未声明关联，导致合法 Project SK 返回 403。PR #318 将授权 SQL 原始行改为扁平结构，再单独加载模型实体，并增加回归测试。PR #318 的五项 CI 全绿，候选 Linux 二进制通过真实 MySQL 和双上游复验后合并。

## 3. Migration 验收

部署前确认测试环境为 `59:0`，且不存在 `000060` 至 `000063` 的异常部分结构。完成数据库压缩备份、API 二进制备份、环境配置备份、哈希清单和回滚路径后，按顺序执行：

1. `000060`：AI 请求、Usage、执行尝试和 Project 账本。
2. `000061`：Project SK、模型 allowlist 和归属约束。
3. `000062`：价格版本、钱包关联和事务 Outbox。
4. `000063`：内容安全、资源策略、预算和补偿任务。

每一步执行后均确认版本和 dirty 状态。最终结果：

```text
MIGRATIONS=PASS
schema_migrations=63:0
critical_counts_unchanged=true
permissions=5
sms_enabled=false
```

短信、邮件、钱包、订单和旧 `token_usage_logs` 的关键行数在迁移前后保持一致。Migration 未自动创建活动安全策略，符合 G4 的 fail-closed 设计。

## 4. 部署验收

从合并后的主线构建 Linux `amd64` 二进制。候选端口通过 `/api/health`、`/api/ready` 和 `/api/version` 后，再切换正式测试端口。

最终热修复二进制：

```text
sha256=2475408aacb9f4b3b8096bae4f136c19dacf16b706cf92926fc44d789bdfe78e
formal_instances=1
listener_8080=1
candidate_listener_18081=0
rollback=ready
```

部署后确认：

- `TOKEN_EXECUTION_DRIVER=bifrost`。
- Bifrost、MySQL、Redis、RabbitMQ 和 MinIO 健康。
- 正式端口只有一个 Molin API 实例。
- 原二进制和部署前配置保留在测试机私有回滚目录。
- API 回滚不删除请求账本、价格快照、钱包流水或安全事件。

## 5. 双上游真实 E2E

测试使用专用已实名用户、Project、allowlist 平台 SK、人民币钱包、活动价格版本和完整七类安全策略。只执行最低成本样本，不进行真实上游压力测试。

| 上游 | 逻辑模型 | 请求结果 | 执行驱动 | 账单结果 | 销售金额 |
|---|---|---|---|---|---:|
| 阿里云百炼 | `molin/qwen-turbo` | HTTP 200、Usage 完整 | Bifrost / Bailian | `succeeded / settled / passed` | ¥0.00000100 |
| OpenRouter | `molin/deepseek-v4-flash` | HTTP 200、Usage 完整 | Bifrost / OpenRouter | `succeeded / settled / passed` | ¥0.00000100 |

最终请求账本标识：

```text
Bailian:    req_acbfdd4bd7035e1e
OpenRouter: req_fae277f43aae8fe8
```

两个请求均满足：

- 用户、Project、API Key 归属一致。
- `execution_driver=bifrost`，供应商分别归因为 `bailian` 和 `openrouter`。
- 原始 Usage、计价 Usage、价格版本和价格快照齐全。
- 每个请求只有一个钱包关联、一个 hold、一个冻结流水、一个消费流水和一个解冻流水。
- hold、结算和释放后余额一致，`frozen_amount=0`。
- 两个请求的总钱包减少金额为 ¥0.00000200，无重复扣费。
- 使用同一幂等键重放返回 HTTP 202 和原 request_id；请求、attempt、钱包关联仍各为 1。
- `/v1/models` 只展示该 SK 获准模型；`/v1/requests/{request_id}` 返回本人已结算状态。
- 公开响应保留 OpenAI 兼容字段，但不暴露 `extra_fields`、`routing_info`、上游响应头或密钥。

按不可变价格快照和实际 Usage 重算的平台成本：

| 上游 | 销售金额 | 平台成本 | 毛利金额 |
|---|---:|---:|---:|
| Bailian | ¥0.00000100 | ¥0.00000003 | ¥0.00000097 |
| OpenRouter | ¥0.00000100 | ¥0.00000013 | ¥0.00000087 |

正常成功请求的上游成本事实由 `price_snapshot_json` 和原始 Usage 确定性重算；`provider_cost` 独立 Usage 行保留给“输出被平台拦截、用户免单但平台承担成本”的受控场景。

## 6. 安全与失败场景

| 场景 | 结果 | 上游调用 | 钱包影响 |
|---|---|---:|---:|
| 非法平台 SK | HTTP 401 | 0 | 0 |
| 命中输入安全规则 | HTTP 403 | 0 | 0 |
| 安全规则缺失 | fail-closed | 0 | 0 |
| Redis 不可用 | 隔离测试稳定拒绝 | 0 | 0 |
| `request_not_sent` | 请求失败并释放 hold | 0 | 释放 |
| 相同幂等键重放 | 返回原请求 | 不重复 | 不重复 |

安全命中时返回统一文案：

```text
请求内容违反中国大陆相关法律法规或平台安全规范，无法继续处理。
```

安全事件只保存分类、规则编号、内容摘要和主体标识；未保存测试提示词正文。测试日志扫描未发现平台 SK 明文。

## 7. 测试凭据回收

最终验收完成后：

- 专用平台 SK 状态改为 `revoked`。
- 专用 Project 状态改为 `archived`。
- 专用测试用户状态改为 `disabled`。
- 保存明文平台 SK 的临时文件已删除。
- 吊销后再次访问 `/v1/models` 返回 HTTP 401。
- 请求账本、Usage、价格快照、钱包流水和安全事件作为不可变验收证据保留。

测试环境保留活动安全策略，避免 API 在无策略时进入 fail-closed；该合成测试规则不得直接视为生产合规词库，生产发布前必须经过合规负责人审批和替换。

## 8. 最终 QA 与产品结论

| 角色 | P0 | P1 | P2 | P3 | 结论 |
|---|---:|---:|---:|---:|---|
| 测试工程师最终验收 | 0 | 0 | 2 | 0 | PASS，允许进入 G5 |
| 产品经理最终确认 | 0 | 0 | 2 | 0 | PASS，允许进入 G5 |

两项开放 P2：

1. 管理端治理 JSON 当前拒绝尾随文档，但未显式拒绝重复对象键；G5 应统一使用可检测重复键的严格解码器。
2. OpenAI 兼容响应的顶层敏感扩展已过滤，但 message/delta 内仍允许部分未知兼容扩展；G5 应冻结嵌套字段白名单和兼容策略。

两项均不造成当前 G4 的鉴权绕过、重复扣费、敏感密钥泄露或内容安全失效，不阻断进入 G5；进入生产前必须完成复核。

## 9. 阶段结论

G4 的主线合并、Migration、测试环境部署、真实 Bifrost 双上游调用、平台 SK 鉴权、模型权限、安全拒绝、人民币计量、钱包结算、幂等、脱敏和回滚准备均已完成。

**G4 测试环境门禁通过，允许开始 G5“管理后台模型发布、价格、路由和安全工作台”开发。**

以下事项不属于本结论：

- 生产环境 Migration 或生产流量。
- 管理后台和用户控制台 G5/G6 页面。
- 图片、视频、音频、Embedding 和对象存储生命周期。
- 生产内容词库、分类器、算法备案和法务批准。

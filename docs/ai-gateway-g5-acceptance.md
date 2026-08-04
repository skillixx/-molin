# AI 网关 G5 最终验收记录

> 最终状态：**测试环境验收通过，允许进入 G6 用户端模型市场与完整客户旅程开发。**
>
> 本结论覆盖 G5 主线合并、Migration `000064`、管理后台工作台、真实 Bifrost/Bailian 文字请求、人民币计量、凭据回收和 QA/产品双签。它不代表生产部署、真实客户流量、多模态执行链路或生产内容合规词库已经开放。

## 1. 验收对象

| 项目 | 结果 |
|---|---|
| 实现 PR | [#321](https://github.com/skillixx/-molin/pull/321)，已 Squash 合并 |
| 实现基线 | `30c3e4376d3b35123897454c881ed09298972888` |
| Migration | `000064_create_ai_gateway_g5_admin_workbench` |
| 测试环境 schema | `64:0` |
| Bifrost | `maximhq/bifrost:v1.6.6`，双节点健康，统一入口健康 |
| 执行驱动 | `TOKEN_EXECUTION_DRIVER=bifrost` |
| 短信保护 | `SMS_ENABLED=false` |

## 2. 代码与 CI

| 门禁 | 结果 | 证据摘要 |
|---|---|---|
| Git 与评审 | PASS | PR #321 已合并；最终独立后端评审 P0=0、P1=0 |
| GitHub Actions | PASS | 后端、G3、G4、管理端构建、用户端构建共 5 项全绿 |
| Go | PASS | `go test -count=1 ./...`、远程 Linux `go test -race`、`go vet ./...`、`go mod verify` |
| 历史回归 | PASS | G3 MySQL/RabbitMQ、G4 MySQL/Redis/RabbitMQ 与并发场景 |
| 前端 | PASS | 管理端和用户端 `type-check`、`lint`、`build` |
| Playwright | PASS | 关键交互 1 项及 1440/768/375 三宽度，共 4/4 |
| 响应治理 | PASS | 重复键、畸形类型、顶层及 `choices/message/delta/logprobs/usage` 递归失败关闭 |
| 敏感信息 | PASS | 源码、文档、响应和验收输出未包含完整 SK、JWT、数据库密码或上游密钥 |

## 3. Migration 与部署

测试环境在执行前完成数据库、API 二进制、进程环境和前端容器备份。Migration 从 `63:0` 执行到 `64:0`，四张新表和 15 个命名约束均存在，`dirty=0`。隔离 MySQL 8 另完成 `63:0 -> 64:0 -> 63:0 -> 64:0`、重复 up 和真实仓储并发验证。

部署后的只读检查全部通过：

- `/api/health`、`/api/ready`、`/api/version` 均为 HTTP 200。
- 管理端 `3001` 和用户端 `3000` 的 API 反代均为 HTTP 200。
- Bifrost 统一入口及两个节点健康。
- 管理端与用户端镜像均来自实现基线 `30c3e4376d3b`。
- `APP_ENV=test`、`TOKEN_EXECUTION_DRIVER=bifrost`、`SMS_ENABLED=false`。

## 4. 真实 Bifrost E2E

测试使用专用实名用户、Project、临时 allowlist SK、人民币钱包、活动价格和最低成本 `molin/qwen-turbo`。管理员先完成渠道无密钥健康检查、模型静态文档配置、Bifrost 路由创建和模型 `v1` 发布。

| 核验项 | 结果 |
|---|---|
| 路由 | `route:1 -> bifrost / bailian / bailian/qwen-turbo` |
| 执行 | `succeeded / settled / passed`，`result_unknown=0` |
| 原始 Usage | `provider / sequence_no=0`，3 行 |
| 销售计量 | `provider / sequence_no=1`，4 行 |
| 上游成本 | `provider / sequence_no=2`，4 行 |
| 单次销售结算 | `¥0.00000100` |
| 单次平台成本 | `¥0.00000003` |
| 钱包 | 每个请求各一组 `freeze -> unfreeze -> consume`，consume 仅一次 |
| 响应脱敏 | 未发现 `extra_fields`、`routing_info`、Provider Header、密钥名或鉴权字段 |
| 审计 | 渠道健康、模型更新、路由创建和模型发布均有管理员审计事实 |

主验收请求为 `req_f3e03136be111bef`。验收脚本随后误发了第二个不带幂等键的低成本请求 `req_7251e4b1503cff6c`；第二个请求也是独立、可追溯且仅结算一次，额外消费 `¥0.00000100`。该问题属于验收脚本 P3，不是网关重复扣费。后续验收不得再使用非幂等请求做重复确认。

验收结束后，临时 SK 已吊销、Project 已归档、测试用户已禁用，明文 SK 未落盘或进入报告。两个请求的账本、Usage、成本和钱包流水作为不可变测试证据保留。

## 5. UI 验收

测试环境实页 `Token 网关 -> AI 网关工作台` 已读取真实数据，能够展示请求量、成功率、Token、销售额、成本、毛利、模型、价格、渠道和路由状态。`molin/qwen-turbo` 显示 `v1`、操作文档和下架入口，Bifrost 路由显示为生效。

- 仓库 Playwright：桌面、平板和手机宽度均无横向溢出，关键按钮均产生真实前端交互。
- 部署实页：页面标题正确、控制台数据加载成功、桌面 `overflow=0`，未发现文字遮挡或控件重叠。
- 页面写操作自动化仍使用 Mock API；真实后端写入由本次测试环境管理员 API、审计事实和数据库结果补充验证。

## 6. QA 与产品双签

| 角色 | P0 | P1 | P2 | P3 | 结论 |
|---|---:|---:|---:|---:|---|
| 测试工程师 | 0 | 0 | 1 | 3 | PASS，允许 G5 测试门禁通过 |
| 产品经理 | 0 | 0 | 2 | 0 | PASS，允许进入 G6 |

两位验收人均确认：额外的第二个低成本请求独立对账、没有重复扣费且测试凭据已回收，因此不阻断 G5。

上述数量是独立验收时的原始发现。本次收尾同步修正了“验收状态陈旧”和“正常成本序列错误”两项 QA P3，并修正了产品经理指出的验收状态 P2；合并本记录后，未关闭项为测试工程师 P2=1/P3=1、产品经理 P2=1/P3=0。

## 7. 残余风险

1. **P2：渠道健康检查存在受限的盲 SSRF 风险。** 当前会拒绝明显危险的字面地址并固定访问 `/health`、禁止重定向、不带密钥、不返回正文，但尚未对域名解析后的 loopback/RFC1918 地址执行统一策略。由于测试环境 Bifrost 合法使用本机入口，生产开放前应增加显式内部渠道白名单或解析后地址校验。
2. **P2：前端 Playwright 不是真实后端写入 E2E。** 测试会 Mock `/api/**`；测试环境实页、管理员 API、审计和数据库事实已经覆盖本阶段，但后续应补充真实测试环境浏览器写入冒烟，并为安全策略 payload 增加独立断言。
3. **P3：验收脚本多发一次非幂等请求。** 已完成逐笔对账和凭据回收；脚本后续必须强制唯一幂等键并禁止额外付费复查。
4. Migration `000064` 由多项在线 DDL 组成，测试库规模很小；生产部署前仍须重新评估表规模、DDL 实际耗时和维护窗口。

## 8. 回滚位置

- 数据库备份、旧 API 二进制和原进程环境：`/home/pc/molin/backups/g5-20260804T092035Z-30c3e4376d3b`。
- 旧管理端容器：`molin-admin-g5-prev-20260804T092909Z`。
- 旧用户端容器：`molin-user-g5-prev-20260804T092909Z`。
- Migration 64 的 down 为保留审计事实的 no-op；应用回滚优先恢复旧二进制和旧前端容器，不手工删除新表、字段或账本事实。

## 9. 阶段结论

G5 的代码、CI、Migration、测试环境部署、真实 Bifrost 路由、模型发布、人民币计量、钱包结算、响应脱敏、管理后台 UI、凭据回收和 QA/产品双签均已完成。

**G5 测试环境门禁通过，允许开始 G6。生产部署、多模态执行、对象存储生命周期和生产内容合规仍须使用独立 Goal 与验收门禁。**

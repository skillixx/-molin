# AI 网关 G8 生产就绪检查

> 本清单是生产授权前的只读准备材料。完成清单不等于生产部署、真实上游受理、客户调用或真实扣费。

## 1. 目标与拓扑

- 固定 ChangeId、生产主机/集群、域名、TLS 证书摘要、维护窗口、责任人和回滚点。
- 核对 Nginx SSE buffering、连接/读取超时、请求体限制、连接数和来源头边界。
- 核对 API、MySQL 8、Redis 7、RabbitMQ、Bifrost 双节点、Prometheus、Alertmanager、Grafana 和日志系统的实际运行形态。
- 指标端点只能由监控网络以强 Token+来源 IP 访问；Prometheus、Grafana 和 Alertmanager 管理面不得直接暴露公网。

## 2. 配置失败关闭

- `APP_ENV=production`，`AI_GATEWAY_TRAFFIC_ENABLED=false` 完成关闭态部署。
- 密钥仅通过受控环境变量或 Secret Manager 注入；源码、镜像层、build args、日志、PR 和证据文档均不得出现真实值。
- 开启总闸时必须具备 `TOKEN_PROVIDER_KEY`、`API_KEY_HMAC_SECRET`、`INTERNAL_API_TOKEN`、`INTERNAL_ALLOWED_IPS`、RabbitMQ/Outbox 和安全预占配置。
- Bifrost 驱动必须具备受控基址与不少于 32 字节的内部 Token。
- DB 门禁要求 5～8 个发布文字模型、两个健康渠道、逐模型有效价格/路由、唯一内容安全策略、成本未过期且毛利达标。运行链只允许对明确未发出的失败在同一路由安全重试；结果未知、超时、流中断和已收到上游响应均不得自动重试或跨上游切换。

## 3. 制品与依赖

记录源码提交、API/前端二进制或镜像 digest、SHA-256、Go/Node 版本、依赖锁文件摘要、Bifrost 镜像摘要、Prometheus/Alertmanager/Grafana 镜像摘要和脱敏配置摘要。禁止使用 `latest`。

## 4. Migration 生产评估

G8 当前无新增 Migration。生产前仍需对 `000060`～`000066` 逐项记录目标表行数、数据/索引大小、DDL 算法与锁级别、预估时长、超时/终止条件、备份与恢复步骤。不得仅凭 `information_schema` 权限或测试空库耗时认定生产安全。

静态复核结论如下；表中“生产动作”均未执行，实际算法必须在取得只读生产授权后使用目标 MySQL 版本的 `EXPLAIN ALTER` 或等价能力确认：

| Migration | 主要存量风险 | 生产门禁 |
|---|---|---|
| `000060` | `api_keys` 增加复合唯一索引，其余以新建账本表为主 | 先核对重复键、表大小和索引构建临时空间；唯一索引未确认前不得执行 |
| `000061` | `api_keys` 多次加列、索引、外键和全表 `UPDATE`；`ai_requests` 增加复合外键 | 风险最高；要求拆分窗口、核对孤儿/重复数据、在线 DDL 能力、元数据锁等待和中止阈值 |
| `000062` | `wallets`、`wallet_transactions`、`wallet_holds` 加列或约束，涉及财务事实表 | 先做负值/越界基线与备份恢复；失败时保留事实，禁止用 down 删除账务记录 |
| `000063` | 新建治理表，并替换 `ai_usage_items.source` CHECK | 核对历史 source 分布；CHECK 替换期间不得放行未知枚举 |
| `000064` | `token_models`、`token_channels` 连续加列/外键/约束 | 逐条确认 INSTANT/INPLACE/COPY 实际算法，避免连续 ALTER 累积锁表窗口 |
| `000065` | `token_models` 三次加列与三次全表初始化，`ai_requests` 新增索引 | 先按生产行数演算扫描与索引时间；初始化必须限窗且可观测元数据锁 |
| `000066` | `ai_requests` 复合唯一索引及申诉归属外键 | 先核对重复 request/user 和跨用户申诉为 0，再建立唯一索引与外键 |

隔离 MySQL 8 已从空库应用当前全部 Migration，并通过 G7 1000 请求可靠性回归；该结果只证明迁移顺序和新库兼容，不替代生产数据量、锁等待、备份可读和恢复时间验证。

## 5. 放量停止条件

任一 P0/P1、非零账务差额、重复扣费、负余额、未释放 hold、备份失败、回滚失败、监控不可用、审核失败开放、结果未知自动重试或密钥泄漏信号，立即停止放量并按已批准回滚手册执行。

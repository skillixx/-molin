# AI 网关 G4 验收记录

## 1. 验收范围

验收对象为 `feature/bifrost-ai-gateway-g4` 当前候选代码，包括内容安全、四层资源限制、Project/SK 预算、补偿任务、管理接口、migration 和文档。结论不代表已合并 main、已部署测试环境、已接入真实用户或已进入 G5。

## 2. 自动化证据

| 门禁 | 结果 | 覆盖 |
|---|---|---|
| Token 网关模块测试 | 通过 | 输入/输出审核、资源、预算、SSE、错误契约、路由 |
| G4 Linux 隔离脚本 | 通过 | migration 重入/保留、临时 MySQL/Redis/RabbitMQ |
| MySQL 100 并发 | 通过 | 两个 SK 共享 Project hard 预算，10 准入、90 拒绝、无超卖 |
| Redis 8 节点模拟 | 通过 | 100 请求、并发上限 20、租约过期、TPM 核销 |
| Redis 停止/恢复 | 通过 | 停止时失败关闭，恢复后无幽灵租约 |
| RabbitMQ 停止/恢复 | 通过 | 基础设施恢复；G3 Outbox 由 G3 回归继续验证 |
| 安全输出 | 通过 | JSON 拦截；SSE 违规片段不外泄且 Usage 持久化 |
| 补偿任务 | 通过 | 八次失败进入 dead，乐观锁和 manual_review |

隔离脚本成功摘要：

```text
G4_VERIFY=PASS isolated=true migrations=up_repeated_down_reup_preserved
redis_nodes=8 concurrency=100/20 redis_down_fail_closed=true
redis_recovered=true budget_multi_sk=true thresholds=80_90_100
rabbit_recovered=true project_database=false
```

最终复核候选使用独立 Git index 生成，不包含忽略文件或本地密钥：

```text
candidate_tree=43b139731cd765cd25f632fc9d91016788753b57
archive_sha256=1ca8110091bfdafb308b4cae4ce3bfa564a6633163f844e88b7b526eef586a40
remote_linux_race=PASS
g3_mysql_rabbitmq=PASS
g4_isolated_governance=PASS
sensitive_scan_hits=0
```

本轮补充了内容安全免单请求的正式受控 Usage 补录入口，并在隔离 MySQL 中验证 `settlement_pending`、超期 `billing_exception`、相同 Usage 幂等、冲突 Usage 拒绝、用户消费为 0、钱包 hold 释放、平台成本入账和唯一 Outbox。测试不再以直接插入 Usage 表作为业务收敛路径。

## 3. 人工验收

最终提交前由独立测试工程师和产品经理基于同一候选工作树复核。2026-08-03 最终定向复核结论如下：

| 角色 | P0 | P1 | P2 | 结论 |
|---|---:|---:|---:|---|
| 独立测试工程师 | 0 | 0 | 0 | PASS |
| 产品经理 | 0 | 0 | 0 | PASS |

两位复核人确认：超期内容安全请求保留 `output_moderation_blocked`，`billing_exception` 可通过专用入口补录；完整规范化 JSON/SSE 公共载荷均参与审核；G4 脚本实际执行全部“输出审核”子用例。commit SHA 在提交后以 Git 历史为准，不把未提交候选误写为提交完成。

## 4. 残余边界

- 主线合并和测试环境部署依赖短信阶段2 Migration `000059`（PR #315）先完成合并；网关 Migration 已调整为 `000060` 至 `000063`，不得跳过该依赖直接部署。
- 默认关键词规则只是工程初始防线，生产词库、分类器、误判指标和留存策略仍需合规审批。
- 管理后台和用户控制台 UI 不在本阶段实现范围，接口已冻结供后续前端任务使用。
- 图片、视频、音频、embedding 和对象存储生命周期属于 G5。
- 本阶段未执行生产 migration、真实上游付费调用或真实用户流量。

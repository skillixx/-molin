# AI 网关 G4 验收记录

## 0. PR 独立评审整改候选（2026-08-04）

PR #316 在合并前独立评审发现的阻断项已经形成代码和测试候选，但在新 commit 的 G3/G4 隔离 CI、QA 和产品复验完成前，本节不把候选误写为最终通过：

- SSE 使用“全公开字段段审核 + 增量生成字段连续视图”，公开 choice 仅允许对象并保留兼容字段；新增跨段、300 组斜杠/零宽字符、U+034F、普通组合附加符和变体选择符绕过测试，违规后段不外泄。
- 管理端创建、发布和回滚安全策略都要求完整覆盖 illegal、sexual、gambling、drugs、terror、hate、self_harm，关键词规范化长度不超过 256。
- 预算 held/settled 统一按预留时固化的日/月周期归集，新增跨午夜隔离 MySQL 用例。
- 预算释放失败立即登记 `budget_release_failed` 补偿，下一轮在无 G3 请求事实时直接释放并把任务推进 completed；补偿任务也无法落库时保留 held 到自然过期并记录错误，不使用存在并发竞争的固定时间兜底。
- pending -> running 启动失败新增原子 `request_not_sent` 终结，释放钱包 hold，并在 G3 隔离 MySQL 中核对请求与 hold 终态。
- Outbox dead 新增受 `ai_gateway:reconcile_manage`、管理员二次认证、非空原因和前置审计保护的重试入口；只按原 event_id 重排。
- 日/月限额逐项要求正数，钱包 GORM 金额精度同步为 `DECIMAL(20,8)`，治理 JSON 拒绝尾随文档。

本地已完成 `go test -count=1 ./...`、`go vet ./...`、`go mod verify`、管理端和用户端 type-check/lint/契约测试/build。当前 Windows 环境无 Docker CLI，新增隔离 MySQL 用例必须以推送后 `gateway-g3`、`gateway-g4` CI 结果为准；CI 未绿、独立复评 P0/P1 未清零前禁止合并和部署。

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
| 补偿任务 | 待当前 Head CI 复验 | 有事实的释放失败立即收敛、自然过期、completed、八次失败进入 dead、乐观锁和 manual_review |

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

以下表格仅记录 2026-08-03 旧候选树的历史定向复核，不适用于 2026-08-04 PR 整改提交，也不能作为当前 Head 的合并批准。当前 Head 必须重新取得独立测试工程师和产品经理 P0=0、P1=0 结论：

| 角色 | P0 | P1 | P2 | 结论 |
|---|---:|---:|---:|---|
| 独立测试工程师（旧候选） | 0 | 0 | 0 | 历史 PASS，当前无效 |
| 产品经理（旧候选） | 0 | 0 | 0 | 历史 PASS，当前无效 |

两位复核人确认：超期内容安全请求保留 `output_moderation_blocked`，`billing_exception` 可通过专用入口补录；完整规范化 JSON/SSE 公共载荷均参与审核；G4 脚本实际执行全部“输出审核”子用例。commit SHA 在提交后以 Git 历史为准，不把未提交候选误写为提交完成。

## 4. 残余边界

- 主线合并和测试环境部署依赖短信阶段2 Migration `000059`（PR #315）先完成合并；网关 Migration 已调整为 `000060` 至 `000063`，不得跳过该依赖直接部署。
- 默认关键词规则只是工程初始防线，生产词库、分类器、误判指标和留存策略仍需合规审批。
- 管理后台和用户控制台 UI 不在本阶段实现范围，接口已冻结供后续前端任务使用。
- 图片、视频、音频、embedding 和对象存储生命周期属于 G5。
- 本阶段未执行生产 migration、真实上游付费调用或真实用户流量。

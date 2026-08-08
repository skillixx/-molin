# AI 网关 G7 测试环境运维与回滚手册

> 本手册只适用于墨灵测试环境。禁止据此操作生产、导入真实客户数据或执行图片/视频模型压力测试。

## 1. 变更范围与影响

- API：增加 AI 网关指标采集和只读对账命令，不新增公开客户 API。
- 监控：Prometheus 增加 AI 告警规则；新增 Grafana SLO 看板。
- 数据库：G7 无新 Migration；只读取现有 `ai_requests`、`ai_usage_items`、钱包、Outbox 和补偿事实。
- 测试：Fake 上游和临时 MySQL/Redis 容器，不产生付费上游费用。

## 2. 上线前只读检查

必须记录但不得把敏感值写入文档或命令输出：

1. 目标主机、当前用户、仓库绝对路径、当前提交和工作树状态。
2. API、MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana 的实际进程/容器和监听端口。
3. API 实际加载的环境文件；确认 `APP_ENV=test`、内部指标 Token 已配置且长度不少于 32 字节。
4. `schema_migrations` 仍为 G6 已验收版本 `66:0`；G7 不推进 schema。
5. 当前 `/api/health`、`/api/ready`、Bifrost health 和现有 Prometheus 抓取状态。
6. 当前是否有其他部署、Migration、真实请求或混沌演练正在执行；冲突时停止本次变更。

## 3. 隔离预验收

```bash
AI_GATEWAY_G7_ISOLATED_APPROVED=YES G7_DOCKER_PULL_POLICY=missing \
  bash infra/scripts/verify-ai-gateway-g7-reliability.sh
```

只接受末行 `G7_VERIFY=PASS`，并确认 `project_database=false`、`paid_upstream=false`、三项 difference 为 0、四类 anomaly 为 0。脚本退出后不得残留 `molin-g7-*` 临时容器或网络。

## 4. 测试环境部署

1. 为当前提交构建 API 二进制或镜像并计算 SHA256。
2. 创建带 ChangeId 的回滚目录，保存旧 API、Compose/Prometheus/Grafana 配置和实际环境文件的权限/校验值；环境文件本体不得进入 Git。
3. 原子替换 API 和监控配置，先运行 `promtool`、`docker compose ... config --quiet`，再受控重启 API、Prometheus 和 Grafana。
4. 不执行 Migration；若远端 schema 不是 `66:0`，停止部署并按 G6 手册处理，禁止用 G7 顺带升级。
5. 验证 `/api/health`、`/api/ready`、Prometheus target、Grafana `/api/health` 和 UID `molin-ai-gateway-g7`。

Grafana 默认只绑定 `127.0.0.1:13000`。远程查看示例：

```bash
ssh -L 13000:127.0.0.1:13000 <test-user>@<test-host>
```

浏览器打开 `http://127.0.0.1:13000/d/molin-ai-gateway-g7`。不得为了方便把端口改为 `0.0.0.0`。

## 5. 测试环境 E2E

1. 使用专用测试管理员/用户和 Fake 上游执行唯一 ChangeId 的文字请求。
2. 验证 JSON、SSE、客户端断连、相同幂等键重放；断连后请求仍形成可信 Usage 和财务终态。
3. 在受控窗口停止 Fake 上游，确认未发送请求释放预占；恢复后重新请求成功。
4. 在受控窗口停止 Redis，确认治理失败关闭；恢复后确认租约可重新准入且无幽灵租约。
5. 运行只读核对：

```bash
APP_ENV=test AI_GATEWAY_RECONCILE_READ_ONLY=YES \
  ./ai-gateway-reconcile --format json
```

6. 验证报告 `status=PASS`、`has_mismatch=false`、三项差额 `0.00000000`、四类异常 0、未释放预占 0。
7. 验证指标端点：无 Token、错误 Token、非 allowlist 来源均为 403；正确内部凭据返回 Prometheus 文本且不含用户、Project、SK、正文或请求明细。
8. 验收后回收专用会话/SK；保留脱敏请求和账务事实，不删除或篡改历史账本。

## 6. 告警处置

- 可用性、P95 或 TTFT：先区分治理拒绝、上游失败和网关自身延迟，禁止盲目重试结果未知请求。
- Usage 缺失/待结算：保持 hold，使用受控人工核定入口；禁止直接更新钱包。
- 任一账单差额/异常：停止扩大测试流量，保存只读报告和 request_id 列表，交由财务对账流程处理。
- Outbox/补偿积压：先检查 RabbitMQ/Worker 和任务状态；禁止直接把状态批量改为成功。
- 心跳/幽灵租约：检查 Redis 时钟、连接和节点生命周期，恢复后重跑资源验收。

## 7. 回滚

1. 停止当前 ChangeId 的验证流量和混沌动作，确保 Fake 上游与 Redis 已恢复。
2. 恢复旧 API 二进制和旧 Prometheus/Grafana 配置，受控重启对应服务。
3. G7 无数据库 down；不得删除请求、Usage、钱包流水、Outbox 或补偿事实。
4. 验证健康、旧指标、钱包不变量和 `schema_migrations=66:0`。
5. 保存回滚 ChangeId、执行人、时间、校验值、失败原因和只读对账报告。

生产部署、生产流量、真实客户灰度或自动修账必须使用独立授权和独立验收门禁。

# 视频网关监控与告警

## 适用范围

本文只描述VID-G7关闭态基础设施监控。它不代表测试服或生产监控已经安装，也不授权真实Provider、真实钱包或生产操作。

## 指标

视频指标复用`GET /api/internal/metrics`的既有鉴权边界，不新增公开端点。指标只使用封闭低基数标签：

- `operation`：`text_to_video`、`image_to_video`。
- `status`：冻结任务、观察或补偿状态。
- `stage`：`submit`、`poll`、`fetch`。
- `kind`：`work`、`delay`、`dead`。
- `phase`：`queued`、`promoting`、`running`。
- `direction`：`db_missing_object`、`storage_unreferenced_object`。

禁止加入用户ID、Project、API Key、request_id、task_id、Prompt、对象键、URL、错误原文或Provider正文。

当前指标族包括：

- `molin_ai_gateway_video_tasks`与`molin_ai_gateway_video_task_oldest_age_seconds`。
- `molin_ai_gateway_video_queue_depth`。
- `molin_ai_gateway_video_capacity_leases`。
- `molin_ai_gateway_video_unsettled_holds`、金额与最老年龄。
- `molin_ai_gateway_video_object_observations`。
- `molin_ai_gateway_video_object_compensations`。
- `molin_ai_gateway_video_component_up`、`molin_ai_gateway_video_component_failures_total`与最近成功年龄。
- `molin_ai_gateway_video_object_bytes`与`molin_ai_gateway_video_cleanup_failures`。

容量指标由Redis单次Lua读取：同时验证无TTL、run_id、epoch、policy、ready状态和全部记录形状，不能用单实例内存计数替代。MySQL、Redis、RabbitMQ分别采集；其中一个依赖失败时，其他指标仍可抓取，并由`component_up=0`明确暴露失败。

## 告警与处置

规则文件为`infra/prometheus/video-gateway-alerts.yml`，Grafana面板为`infra/grafana/dashboards/video-gateway-g7.json`。

发生Provider hard cap超限、长期pending_reconcile、队列年龄/积压、DLQ、长期Hold、组件不可用、清理失败、confirmed对象异常或dead/manual_review对象补偿时：

1. 将`VIDEO_GATEWAY_TRAFFIC_ENABLED=false`，停止新报价和提交。
2. 保持`VIDEO_GATEWAY_ENABLED=true`，保留回调、轮询、抓取、结算和补偿Worker。
3. 只读核对MySQL恢复epoch、Redis run_id、RabbitMQ队列和财务事实。
4. 不清空Redis、RabbitMQ、MinIO或数据库事实。
5. 需要测试服重启、migration、回滚或凭据轮换时，先提交精确授权包。

本地`promtool`已验证10条规则，Grafana JSON包含8个面板。共享环境告警送达和通知链尚未执行。

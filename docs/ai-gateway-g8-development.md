# AI 网关 G8 开发说明

> 基线：`origin/main@6e1f67ad4c1a10bb1ad79b3aeac6b16211ccfac1`；功能分支 `feature/backend-d-ai-gateway-g8-commercial-gray`。

## 1. 代码结构

- `server/internal/modules/token_gateway/service/channel_service.go`：渠道健康探测 SSRF 防护、DNS 解析结果校验、固定已校验 IP 拨号和重定向拒绝。
- `server/internal/modules/token_gateway/service/production_readiness.go`：生产流量开启前只读发布事实门禁。
- `server/internal/config/config.go`：生产流量总闸和生产配置失败关闭校验。
- `server/internal/modules/token_gateway/handler/chat_handler.go`：总闸关闭时返回稳定 50330，不创建付费请求。
- `infra/prometheus`、`infra/grafana`：沿用 G7 22 条告警和 16 面板，迁移到生产隔离网络与强鉴权入口。

## 2. 安全与数据流

渠道健康检查不携带上游密钥。URL 先校验协议和字面 IP，实际连接时重新解析全部地址；任一解析结果为本机、私网、链路本地、未指定或组播即拒绝。Transport 直接拨号到已校验 IP，避免默认 Transport 二次解析形成 DNS 重绑定窗口。HTTP 只允许精确登记的测试内网目标。

生产启动顺序：

```text
读取环境变量
  -> 生产配置失败关闭
  -> 连接 MySQL/Redis
  -> 装配 AI 网关
  -> 只读发布事实门禁
  -> 注册流量入口
```

关闭态仍允许部署、健康检查、只读监控和后台准备，但 `POST /v1/chat/completions` 与别名入口统一返回 50330。

## 3. 数据库与 Migration

当前 G8 无新增 Migration。生产门禁只读现有 `token_models`、`token_channels`、`ai_model_routes`、`ai_price_versions`、`ai_price_skus` 和 `ai_safety_policy_versions`。如后续必须新增 Migration，应另行采用 expand-contract、可重复 up、旧版本兼容、在线 DDL 评估和事实保留型回滚，并同步数据库设计。

## 4. 测试入口

```bash
cd server
go test ./...
go vet ./...
go mod verify
```

专项覆盖：私网/回环/IPv6、DNS 指向私网、精确内网白名单、重定向、生产配置缺项、模型数量、渠道数量、价格/路由/审核缺项、低毛利，以及结果未知请求只调用一次的既有回归测试。

## 5. 独立评审修复

- 商业流量总闸下沉至共享 `ForwardService`，公开文字接口、工作台和会话摘要复用入口都必须在读模型、写账本和外呼前失败关闭。
- 生产发布 SQL 与运行时价格规则统一：人民币汇率固定为 1、每版本只能有四条且四类唯一 SKU、按已批准 `min_margin_rate` 校验、排除熔断中或健康结果超过五分钟的路由。
- Bifrost 内部重试固定为 0；结果未知请求只能由 Go 账本根据“明确未发送且结果不未知”的证据决定是否重试。
- 生产 Compose 从仓库外受控环境文件注入 Bifrost 加密密钥、两个上游密钥和内部 Token；节点只有受控公网出站，不发布公网入站端口。
- 隔离生产形态预演实际注册 Token 模块并使用临时 JWT 请求文字接口，关闭态必须返回 HTTP 503 与业务码 50330；双 Fake Bifrost 节点同时验证 LB 未授权拒绝和授权转发，避免以匿名 404 代替总闸证据。

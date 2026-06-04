# Molin 云管理平台 — 全局开发规则

## 语言规范

- 代码注释使用中文
- Git commit message、PR 标题和描述、代码评审意见使用中文
- Commit message 格式：`模块：描述`，例如 `auth：添加注册接口`、`运维：调整端口配置`

## 安全约定（所有开发者必须遵守）

- **身份证号**：禁止明文存储；必须使用 `HMAC-SHA256(id_card_no, ID_CARD_HMAC_SECRET)`，禁止用 SHA-256/MD5 直接 hash；同时保存 masked 值（前6后4，中间替换为 `*`）
- **Refresh Token**：DB 只存 `HMAC-SHA256(token, REFRESH_TOKEN_SECRET)`，不存明文；必须写入 `user_sessions` 表，以支持退出登录和封禁吊销
- **Token 供应商 API Key**：AES-256-GCM 加密存储，密钥通过 `TOKEN_PROVIDER_KEY` 环境变量注入；API 响应禁止返回该字段
- **支付回调报文**：建议 AES-256-GCM 加密后存入 `payment_callbacks.notify_body`
- **禁止**将 `.env.local`、`.env.prod` 或含真实密钥的文件提交到代码仓库

## 目录结构

```
server/              后端 Go API（后端 A/B/C 负责）
web/admin-console/   管理后台（前端 A 负责）
web/user-console/    用户控制台（前端 B 负责）
infra/               运维配置（运维负责）
docs/                设计文档（所有人只读）
scripts/             运维脚本
```

## 本地开发启动

```bash
# 启动基础服务（MySQL 13306 / Redis 16379 / RabbitMQ 5673 / MinIO 19000）
docker compose -f infra/docker-compose.yml up -d

# 后端 API
cd server && go run ./cmd/api

# 管理后台
cd web/admin-console && npm install && npm run dev

# 用户控制台
cd web/user-console && npm install && npm run dev
```

## 环境变量

参考 `infra/.env.example`（包含完整变量说明），复制为 `infra/.env.local` 后填写实际值。

## 每个模块的规范

每个模块目录下有 `CLAUDE.md`，包含该模块的任务清单、接口规范和代码模板，**开发前必读**。

# Molin 云管理平台 — 全局开发规则

## AI 协作分工原则（Claude 后端 / Codex 前端）

人机分工明确、职责互斥；每次输出前先确认本次内容属于自己的范围。

### Claude 只负责后端开发与对接文档，不编写前端页面代码

1. 负责：接口设计、数据库设计、权限认证、业务逻辑、后端目录结构、API 文档、**前端对接文档与设计逻辑**。
2. 可提供接口返回格式（字段、结构、错误码），方便前端调用。
3. **不写 React / Vue / HTML / CSS 页面代码。**
4. 涉及前端需求时，只说明「接口如何配合」，不实现页面。
5. 后端代码必须考虑：安全性、异常处理、参数校验、日志、权限控制。
6. **每次输出前先确认：本次内容是否属于后端范围。**

### Codex 只负责前端页面开发，不编写后端业务代码

1. 负责：页面结构、组件、路由、表单、样式、交互逻辑、接口调用封装。
2. 后端接口只按已有 API 文档（`docs/full-api-design.md`、`docs/frontend-api-reference.md`）调用，**不自行设计后端逻辑**。
3. **不写数据库、后端控制器、服务层、鉴权中间件等代码。**
4. 发现接口缺失时，只列出需要后端补充的接口，不自己实现后端。
5. 前端代码要注意：组件复用、页面美观、响应式布局、错误提示、加载状态。
6. **每次输出前先确认：本次内容是否属于前端范围。**
7. **报告「前端开发完成」前，必须过 `docs/frontend-definition-of-done.md` 的五道关卡，并以最新 main 对账**（含关卡 0 契约对账，防止「已合并的后端 delta 未对接」被当成完成）。未满足时只能说「截至 commit X 的范围完成」。

> 前端页面由 Codex/前端团队实现；Claude 对前端的产出聚焦在接口契约与对接说明文档（如 `docs/frontend-api-reference.md`、`docs/frontend-task-*.md`）。

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

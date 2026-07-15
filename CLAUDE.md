# Molin 云管理平台 — 全局开发规则

## AI 协作与角色分配原则（Codex 可替代 Claude）

Codex、Claude 等 AI 工具不与前端或后端角色固定绑定。Codex 可以替代 Claude 承担后端开发，并可按用户要求承担前端、测试、运维和文档工作；Claude 也可以在明确授权后承担相同角色。

### 执行要求

1. 每次任务开始前先声明当前承担的系统角色和负责模块。
2. 角色规范以 `docs/agents/` 为准，模块级 `CLAUDE.md` 是细化规范，文件名不代表工具专属。
3. 用户明确要求全栈或跨角色开发时，按后端契约、后端实现、前端对接、测试验收的阶段顺序执行，避免无契约并行修改。
4. 后端开发必须覆盖安全性、异常处理、参数校验、日志、权限、事务、幂等、migration 和接口文档。
5. 前端开发必须覆盖组件复用、响应式布局、加载与错误状态、权限降级和接口契约一致性。
6. 报告「前端开发完成」前，必须通过 `docs/frontend-definition-of-done.md` 的五道关卡并以最新 main 对账。
7. 测试与运维工作分别遵守 `docs/agents/qa.md`、`docs/agents/devops.md`；开发者自测不能替代独立 QA 和产品经理验收。
8. 钱包、订单、实名、权限、资产、计费、密钥和生产部署等高风险内容必须保留人工审查。

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
docs/                协作规则与设计文档（按角色维护）
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

# 第二阶段上线检查单（Token 售卖 + 套餐预付 + 聊天工作台）

> 适用范围：第二阶段交付内容 — M1 Token 售卖、M2 套餐预付、M3 多模型聊天工作台（tool-use 编排）。
> 涉及模块：`token_gateway`（门面/渠道/模型目录）、`auth.api_keys`（平台 sk）、`asset.entitlement`（套餐权益）、`workbench`（Agent/Skill/Plugin + 编排 chat）、`billing.wallet_holds`（预扣保证金）、`finance_consumer`（按量上报）。
> 代码基线：第二阶段全部合并 main，最新 commit `6219fdc`（#238）。
> 制定：2026-06-22（S2-运3）
>
> 使用说明：每个 `[ ]` 为可勾选项；上线前逐项核对，全部勾选并签字后方可放行。所有 32 字节密钥示例均用占位符，禁止把真实密钥写入仓库或本文档。

---

## 0. 上线前提（代码与回归状态）

- [ ] 第二阶段 M1/M2/M3 已全部合并 main，构建分支基于 commit `6219fdc` 或更新。
- [ ] 后端 `go build ./cmd/api` 通过、`go vet ./...` 无异常、`go test ./...` 通过（CI ci.yml 在 PR 阶段已校验）。
- [ ] 测试环境回归通过（M1/M2/M3 主链路 + 鉴权 + 计费）。

---

## 1. 数据库迁移

第二阶段新增迁移序列 **000030 ~ 000044**（连续 15 条），承接第一阶段截止的 000025（M0/第一阶段补丁），区间 000026~000029 为第一/二阶段过渡迁移。本阶段重点：

| 序号 | 迁移文件 | 内容 | 关键性 |
|---|---|---|---|
| 000030 | create_token_facade_tables | token 门面基础表（models / 用量等） | 必须 |
| 000031 | create_token_channels_and_routing | **token_channels**（上游渠道，含 AES 加密 api_key 列）+ token_models 增列 channel_id / upstream_model | 必须 |
| 000032 | seed_token_manage_permission | 权限码 `token:manage` seed | 必须 |
| 000033 | seed_token_product_and_billing_rules | token 商品 + 计费规则 seed | 必须 |
| 000034 | create_api_keys | **api_keys**（平台 sk，DB 存 HMAC，不存明文） | 必须 |
| 000035 | create_wallet_holds | **wallet_holds**（postpaid 预扣保证金冻结/解冻） | 必须 |
| 000036 | seed_token_call_billing_rule | token 按 call 计费规则 seed | 必须 |
| 000037 | seed_token_package_plan | token **套餐 plan** seed（M2 预付） | 必须 |
| 000038 | create_agents | 工作台 **agents** 建表 | 必须 |
| 000039 | create_skills | 工作台 **skills** 建表 | 必须 |
| 000040 | create_plugins | 工作台 **plugins** 建表（含 auth_config_encrypted） | 必须 |
| 000041 | create_entitlement_consume_logs | **entitlement_consume_logs**（套餐额度消费幂等日志） | 必须 |
| 000042 | create_entitlement_holds | **entitlement_holds**（prepaid 预占额度，方案 B reserve/settle/release） | 必须 |
| 000043 | seed_workbench_manage_permissions | 权限码 `agent:manage` / `skill:manage` / `plugin:manage` seed + 绑定 admin 角色 | 必须 |
| 000044 | create_plugin_daily_call_logs | **plugin_daily_call_logs**（付费插件成本平台担，按 daily_limit 限量统计） | 必须 |

### 迁移红线（golang-migrate）

- [ ] 序号 **连续递增、按合并顺序、无空号无重号**（golang-migrate 不支持 out-of-order；预留空号会导致后续迁移无法应用）。
- [ ] 先迁移、后上线新代码：seed 权限码（000032 / 000043）、新列（000031 channel_id）、新表必须先在库，否则新代码路由/鉴权报错。

### 执行方式

```bash
# 生产环境用真实连接参数（密钥/口令通过环境注入，勿写入命令历史）
export MYSQL_HOST=<prod-host> MYSQL_PORT=<prod-port> \
       MYSQL_DATABASE=molin MYSQL_USER=<user> MYSQL_PASSWORD=<pass>

./scripts/migrate.sh version     # 上线前确认当前版本
./scripts/migrate.sh up          # 应用全部未执行迁移
./scripts/migrate.sh version     # 上线后复核
```

- [ ] **迁移前完整备份生产库**（mysqldump --single-transaction --routines --triggers），备份文件离线留存、禁止入库。
- [ ] 执行 `./scripts/migrate.sh up` 至最新。
- [ ] 上线前确认 `schema_migrations` 表 **version = 44 且 dirty = 0**（dirty=1 表示上一次迁移中断，需先 `./scripts/migrate.sh force <version>` 修复后重跑）。

```sql
SELECT version, dirty FROM schema_migrations;   -- 期望: 44 / 0
```

### 回滚（down）

- [ ] 每条迁移均有对应 `.down.sql`，可按序逐条回滚：`./scripts/migrate.sh down 1`（一次回退一条，从 000044 往回）。
- [ ] 回滚 seed（000032/000033/000036/000037/000043）会删除权限码/商品/套餐数据；建表迁移 down 为 DROP TABLE，**回滚前确认无业务数据依赖**，资金类（wallet_holds / entitlement_holds / entitlement_consume_logs）回滚会丢失冻结/消费记录，生产慎用，优先靠备份恢复。

---

## 2. 环境变量 / 配置项

> 以下逐项核对自 `server/internal/config/config.go`（Load 函数）实际读取的 key，并对照 `server/internal/bootstrap/app.go` 的装配条件。
> 32 字节密钥生成建议：`openssl rand -base64 24`（24 字节 base64 → 32 字符）或 `head -c 32 /dev/urandom | base64`，按各密钥所需「32 字节」语义生成，禁止入库（`.env.local` / `.env.prod` 不提交）。

### 2.1 第二阶段新增 / 关键配置

| 环境变量 | 用途 | 是否必填 | 默认值 | 未配后果 |
|---|---|---|---|---|
| `TOKEN_PROVIDER_KEY` | 上游渠道 `token_channels.api_key_encrypted` 的 AES-256-GCM 加解密密钥（**32 字节**） | **必填** | 空 | token 网关门面**整体不装配**，管理端渠道/模型目录 + 用户端 chat 转发全部不可用；编排 chat 端点连带不注册（依赖上游转发） |
| `API_KEY_HMAC_SECRET` | 平台 sk（api_keys）HMAC 存储密钥，DB 只存 HMAC、明文仅签发时返回一次 | 必填（如需 sk 能力） | 空 | sk 系统**不装配**：`/api/keys` 路由不注册，门面 sk 鉴权退化为纯 JWT；prepaid/postpaid 计费分流降级为「一律 postpaid」 |
| `PLUGIN_SECRET_KEY` | 插件凭证 `plugins.auth_config_encrypted` 加密密钥（**32 字节**） | 必填（如需工作台） | **回退复用 `TOKEN_PROVIDER_KEY`** | 若 PLUGIN_SECRET_KEY 与 TOKEN_PROVIDER_KEY **都为空**，则 workbench 模块整体不装配（Agent/Skill/Plugin 管理端、用户端、编排 chat 全部不可用） |
| `MAX_ROUNDS` | tool-use 编排最大轮数（防无限循环/失控成本） | 否 | **5** | 不配按 5；配 0 或非法值按默认 5 |
| `PLUGIN_DOMAIN_WHITELIST` | 插件转发 / skill 联网外呼域名白名单（逗号分隔，如 `api.weather.com,docs.example.com`） | 否 | 空 | 空=不启用域名白名单，仅按 SSRF 网段规则拦内网/回环；**生产强烈建议显式配置**收紧外呼面 |
| `INTERNAL_API_TOKEN` | 内部接口共享密钥；门面 prepaid 结算调 asset `entitlement-consume/reserve/settle/release` 须带 `X-Internal-Token` | **必填（M2 prepaid 必需）** | 空 | prepaid 内部调用 **fail-closed**：reserve 返回 ErrInternalAuth → 门面 fail-safe 拒绝转发（绝不放行白嫖），即套餐用户 chat 全部 503 |
| `ASSET_INTERNAL_BASE_URL` | asset 模块内部接口基址（门面 prepaid 结算目标） | 否（同进程默认即可） | `http://127.0.0.1:8080` | 同进程部署用默认即可；分进程部署须指向 asset 实例内网地址 |
| `TOKEN_HOLD_UNIT_PRICE` | postpaid 预扣保证金兜底单价（CNY/token），冻结额 = max_tokens × 单价 | 否 | `0.00002` | 非法值时退化为不冻结（仅按 product-usage-events 实扣，不阻断调用，但失去并发透支前置防护） |
| `TOKEN_HOLD_DEFAULT_MAX_TOKENS` | 请求未带 max_tokens 时的兜底冻结上限 | 否 | `4096` | 不配按 4096 |

### 2.2 第一阶段沿用、第二阶段仍需确认的配置

| 环境变量 | 与第二阶段的关系 |
|---|---|
| `NOTIFY_BODY_KEY` | 套餐/token 购买走支付回调时仍需（32 字节，回调报文 AES-256-GCM） |
| `JWT_SECRET` / `REFRESH_TOKEN_SECRET` / `ID_CARD_HMAC_SECRET` | 全局鉴权/安全基线，必须延续第一阶段生产值 |
| `MYSQL_*` / `REDIS_*` | 生产连接参数 |

### 2.3 勾选项

- [ ] `TOKEN_PROVIDER_KEY` 已配置且为 32 字节（否则整个 token 网关 + 工作台不可用）。
- [ ] `API_KEY_HMAC_SECRET` 已配置（如本期对外开放平台 sk）。
- [ ] `PLUGIN_SECRET_KEY` 已配置（或确认回退复用 TOKEN_PROVIDER_KEY 符合预期）。
- [ ] `INTERNAL_API_TOKEN` 已配置，且 asset 校验侧与门面侧 **同值同源**（M2 prepaid 依赖）。
- [ ] `PLUGIN_DOMAIN_WHITELIST` 已按真实外呼依赖配置（生产建议非空）。
- [ ] `MAX_ROUNDS` / `TOKEN_HOLD_UNIT_PRICE` / `TOKEN_HOLD_DEFAULT_MAX_TOKENS` 已确认（采用默认或显式覆盖）。
- [ ] `NOTIFY_BODY_KEY` 等第一阶段密钥延续生产值。
- [ ] 启动日志复核：无 `[security] API_KEY_HMAC_SECRET 未配置`、无 `[token_gateway] ... 未启用`、无 `[workbench] ... 未启用` 等非预期降级提示。

---

## 3. 渠道 / 模型目录配置（数据，非环境变量）

迁移 seed 只建商品/计费规则/权限码，**不含上游渠道凭证与模型路由**。上线前必须由运营/管理端配置以下数据，否则 chat / 编排不可用：

- [ ] 配置 **token_channels**：每个上游渠道的 `base_url`、加密 `api_key`（经 TOKEN_PROVIDER_KEY 加密写入）、`status = active`。
- [ ] 配置 **token_models**：逻辑模型 → `channel_id`（绑定渠道）+ `upstream_model`（上游真实模型名）+ `status = active`。
- [ ] 验证：管理端模型目录能列出至少一个 active 模型，且其绑定渠道 active。

### D6 已知单点（验收登记）

- [ ] 知悉：本期 token_models **绑定单一渠道**，多渠道故障转移（同逻辑模型择优/降级）延后第三阶段。单渠道故障即对应模型不可用，需运营手动切换渠道绑定。

---

## 4. 部署步骤 + 冒烟验证

### 4.1 构建与部署（参考 `server/CLAUDE.md` 测试服流程，生产改用生产主机/凭证）

```bash
# 1) 交叉编译 Linux 二进制
cd server
GOOS=linux GOARCH=amd64 go build -o ../molin-api ./cmd/api

# 2) 上传二进制到目标主机（生产用生产连接信息）
scp ../molin-api <user>@<host>:~/molin/molin-api

# 3) 加载生产环境变量并重启（env 文件由服务器手动管理，不入库）
ssh <user>@<host> "
  pkill molin-api 2>/dev/null; sleep 1
  export \$(grep -v '^#' ~/molin/infra/.env.prod | xargs)
  nohup ~/molin/molin-api > ~/molin/api.log 2>&1 &
"
```

- [ ] 先完成第 1 节迁移，再部署新二进制（先迁移后上线代码）。
- [ ] 重启后确认进程加载的是新二进制、新 env。

### 4.2 冒烟验证

- [ ] 健康检查：`curl http://<host>:8080/api/health` 返回 `{"status":"ok"}`。
- [ ] 鉴权探测（关键路由未带凭证应 401，证明路由已注册且鉴权生效）：
  - [ ] `GET /api/keys`（平台 sk，需 API_KEY_HMAC_SECRET 已装配）→ 401。
  - [ ] token 网关管理端渠道/模型目录路由 → 401（未带管理员凭证）。
  - [ ] 工作台 `agent/skill/plugin:manage` 管理端路由 → 401。
  - [ ] 编排 `POST /api/agents/{id}/chat`（仅登录态）→ 401。
- [ ] 一次真实 chat 调用：用有效登录态/有效 sk，对一个 active 模型发起 OpenAI 兼容 chat，确认上游转发成功、用量上报、计费扣减（postpaid 钱包流水或 prepaid 套餐额度消费）正确。
- [ ] 日志复核：`tail -n 100 ~/molin/api.log` 无 panic、无非预期降级、无敏感字段（密码/Token/身份证号）泄漏。

---

## 5. 回滚预案

- [ ] **二进制回退**：保留上一版 `molin-api`，回滚时 `pkill` 后用旧二进制 `nohup` 重启。
- [ ] **迁移回滚**：`./scripts/migrate.sh down N` 逐条回退；资金/额度类表（wallet_holds / entitlement_holds / entitlement_consume_logs）优先用迁移前备份恢复，避免 DROP 丢失冻结/消费记录。
- [ ] **配置回退**：保留上一版 `.env.prod`；新增密钥（TOKEN_PROVIDER_KEY / API_KEY_HMAC_SECRET / PLUGIN_SECRET_KEY / INTERNAL_API_TOKEN）一旦使用过，回滚时若清空会导致已加密数据无法解密——回退前评估，优先回退二进制而非清空密钥。
- [ ] **降级回退**：紧急时可清空 `TOKEN_PROVIDER_KEY` 使 token 网关 + 工作台整体下线（其余模块照常），作为隔离故障域的临时手段。

---

## 6. 已知风险 / 限制登记

- [ ] **D6 渠道单点**：token_models 绑单一渠道，无多渠道故障转移（延后第三阶段）；单渠道故障即模型不可用。
- [ ] **对话历史前端自持、后端不落库**：编排 chat 的多轮上下文由前端维护并随请求回传，后端不持久化会话历史。前端丢失即历史丢失；切端/换设备不同步。
- [ ] **付费插件成本平台担**：付费插件调用成本由平台承担，按 `plugin_daily_call_logs` + daily_limit 限量兜底，超额当日不再放行。需运营监控每日成本，必要时下调 daily_limit。
- [ ] **postpaid 预扣保证金为估算冻结**：hold = max_tokens × TOKEN_HOLD_UNIT_PRICE 仅用于并发前置占额，实扣以 product-usage-events 为准，结算多退少补；兜底单价取保守值（宁高勿低，高估只多冻结、结算退回，绝不透支）。
- [ ] **sk 未装配时计费降级**：若 API_KEY_HMAC_SECRET 未配，sk 调用退化为纯 JWT 且计费分流退化为「一律 postpaid」，prepaid 套餐用户无法走 sk 路径。

---

## 7. 放行签字

- [ ] 运维确认（迁移 / 配置 / 部署 / 冒烟）：________
- [ ] 后端确认（代码基线 / 回归）：________
- [ ] 测试确认（回归通过）：________
- [ ] 产品经理确认放行：________

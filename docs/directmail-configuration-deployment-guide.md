# DirectMail 配置与部署手册

## 1. 文档范围

本文说明墨灵 DirectMail 邮件模板与邮箱验证码功能的配置、测试环境部署、生产发布门禁、初始化和回滚方式。它是操作手册，不是生产环境上线批准，也不代表真实邮件已经送达。

当前实现固定支持五个场景：

| 场景 | 模板名称 | 业务入口 |
|---|---|---|
| 注册 | `molin_register_code_v1` | 公开邮箱注册 |
| 登录 | `molin_login_code_v1` | 邮箱验证码登录 |
| 找回密码 | `molin_reset_password_code_v1` | 邮箱找回密码 |
| 换绑邮箱 | `molin_bind_email_code_v1` | 登录后换绑邮箱 |
| 管理员验证 | `molin_admin_verify_code_v1` | 管理员邮箱双重认证 |

模板必须在阿里云 DirectMail 控制台维护并审核通过。平台只调用 `QueryTemplateByParam`、`DescTemplate` 和 `SingleSendMail`，不创建、修改或删除供应商模板。模板变量固定为大小写精确的 `Code` 与 `ExpireMinutes`。

## 2. 部署拓扑

### 2.1 当前测试环境

- Go API 是宿主机上的 `molin-api` 二进制，不在 Compose 中。
- 管理后台和用户控制台使用容器部署，前端 `/api` 反向代理到宿主机 API。
- MySQL、Redis、RabbitMQ 和 MinIO 使用 `infra/docker-compose.yml`。
- 合并到 `main` 不会自动部署；前端由 GitHub Actions 手动触发，后端需要单独发布。

### 2.2 生产 Compose

`infra/docker-compose.prod.yml` 从仓库根目录的 `.env.prod` 读取 API 配置。`.env.prod` 已加入 Git 忽略规则，仍应由服务器 Secret 管理流程创建和限制文件权限，不得通过聊天、提交、镜像层、构建参数或工单传递真实值。

### 2.3 MySQL 端口规则

| 调用位置 | MySQL 地址示例 | 说明 |
|---|---|---|
| Windows 或测试服务器宿主机 | `127.0.0.1:13306` | `infra/docker-compose.yml` 把宿主机 `13306` 映射到容器 `3306` |
| `molin-mysql` 容器内部 | `127.0.0.1:3306` | 容器内部监听端口 |
| 同一 Compose 网络中的 API | `mysql:3306` | 使用服务名和容器端口，不使用 `13306` |

`MYSQL_PORT` 必须按 API 或 migration 执行器所在位置填写。看到容器内 `3306` 不代表宿主机也应配置为 `3306`。

## 3. 阿里云前置条件

部署前由云账号管理员完成并留存脱敏证据：

1. DirectMail 服务已开通，地域为 `cn-hangzhou`。
2. 发信域名和发信地址已验证，发信地址属于当前 DirectMail 账号。
3. 五个模板名称与上表完全一致，状态为审核通过。
4. 每个模板正文只使用 `Code` 和 `ExpireMinutes` 两个变量，变量大小写正确。
5. RAM 身份至少允许 `dm:SingleSendMail`、`dm:QueryTemplateByParam`、`dm:DescTemplate`。
6. RAM 身份不应拥有 `CreateTemplate`、`ModifyTemplate`、`DeleteTemplate` 等非本功能所需权限。
7. API 主机可以通过 HTTPS 访问 `dm.aliyuncs.com`，系统时间同步正常。

不得把 AccessKey、完整发信地址、验证码或供应商原始响应写入验收报告。

## 4. 环境变量

以 `infra/.env.example` 为字段权威模板。以下表格只展示键名和规则，尖括号内容不是可用配置值。

| 配置键 | 测试 | 生产 | 规则 |
|---|---|---|---|
| `APP_ENV` | `test` | `production` | 必须显式配置；生产环境禁止使用测试值 |
| `EMAIL_ADAPTER` | `production` 或隔离测试时 `mock` | `production` | Mock 只允许显式安全的非生产环境 |
| `DIRECTMAIL_ACCESS_KEY_ID` | 必填 | 必填 | 仅通过 Secret 渠道注入 |
| `DIRECTMAIL_ACCESS_KEY_SECRET` | 必填 | 必填 | 不得打印、提交或复用 |
| `DIRECTMAIL_REGION` | `cn-hangzhou` | `cn-hangzhou` | 当前发布基线固定为杭州 |
| `DIRECTMAIL_ACCOUNT_NAME` | 必填 | 必填 | 阿里云已验证的单个发信地址 |
| `DIRECTMAIL_FROM_ALIAS` | 必填 | 必填 | 非空发件人别名 |
| `DIRECTMAIL_ENDPOINT` | `https://dm.aliyuncs.com/` | 同左 | 必须是代码冻结的官方 HTTPS 地址 |
| `EMAIL_ADDRESS_HMAC_SECRET` | 必填 | 必填 | 至少 32 字节的独立随机值 |
| `EMAIL_IDEMPOTENCY_SECRET` | 必填 | 必填 | 至少 32 字节，且与地址 HMAC 不同 |
| `EMAIL_DEBUG_RETURN_CODE` | 默认 `false` | 必须 `false` | 生产和未知环境不会返回明文验证码 |
| `REDIS_ADDR`、`REDIS_PASSWORD`、`REDIS_DB` | 必填 | 必填 | Redis 是邮件锁和限流的发布依赖 |

生产环境示意结构：

```dotenv
APP_ENV=production
EMAIL_ADAPTER=production
DIRECTMAIL_ACCESS_KEY_ID=<通过 Secret 管理器注入>
DIRECTMAIL_ACCESS_KEY_SECRET=<通过 Secret 管理器注入>
DIRECTMAIL_REGION=cn-hangzhou
DIRECTMAIL_ACCOUNT_NAME=<已验证发信地址>
DIRECTMAIL_FROM_ALIAS=<发件人别名>
DIRECTMAIL_ENDPOINT=https://dm.aliyuncs.com/
EMAIL_ADDRESS_HMAC_SECRET=<独立强随机值>
EMAIL_IDEMPOTENCY_SECRET=<另一独立强随机值>
EMAIL_DEBUG_RETURN_CODE=false
EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED=false
```

不要直接把上述占位文本用于启动。部署记录只能确认键是否存在、长度是否合规和状态是否正确，不能输出值。

### 4.1 测试环境安全向导

在 Windows 仓库根目录执行：

```powershell
Copy-Item .\infra\.env.example .\infra\.env.test
git check-ignore --quiet .\infra\.env.test
if ($LASTEXITCODE -ne 0) { throw 'infra/.env.test 未被 Git 忽略' }

.\scripts\configure-directmail-test.ps1
```

向导会安全读取 DirectMail 凭据，固定生产 Adapter 和官方 Endpoint，并生成两枚独立邮件 HMAC 密钥。每次运行还会轮换 `INTERNAL_API_TOKEN`，所以已经存在测试数据、Prometheus 或运行中 API 时，必须先评估 HMAC 命中失效和监控中断影响，不能把它当成普通重复执行命令。

宿主机二进制不会自动读取 `.env.test`。API 启动器必须通过受控的环境文件加载机制注入这些键；不要把 Secret 直接拼进 `nohup`、PowerShell 历史或命令行参数。

### 4.2 `admin_verify` 一次性 Bootstrap

以下配置默认保持关闭：

```dotenv
EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED=false
EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN=
EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS=
EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS=
```

只有首次配置 `admin_verify` 且经过单独维护窗口批准时才短时启用。完成一次性配置后必须立即恢复 `false`、移除 Token 并重启 API。该入口没有前端页面，不得向公网转发。完整约束见 `infra/README.md` 的“admin_verify 一次性 bootstrap 运维边界”。

## 5. 部署前检查

### 5.1 代码和构建

```powershell
Set-Location .\server
go test ./... -count=1
go vet ./...
go build ./...

Set-Location ..\web\admin-console
npm.cmd run type-check
npm.cmd run lint
npm.cmd run test:email-management
npm.cmd run build

Set-Location ..\user-console
npm.cmd run type-check
npm.cmd run lint
npm.cmd run test:email-otp
npm.cmd run build
```

至少再执行以下邮件发布契约：

```powershell
Set-Location ..\..
python -B .\tests\email\phase4_remaining_gates_contract.py
python -O .\tests\email\phase4_remaining_gates_contract.py
python -B .\tests\email\sensitive_scan.py server web tests docs infra scripts examples `
  --repo-root . --allow-domain example.invalid --show-level FAIL --show-level REVIEW --show-counts
git diff --check
```

### 5.2 数据与依赖门禁

1. 确认目标环境、数据库名和 API 进程身份，禁止把测试步骤指向生产库。
2. 停止注册、登录、发码和验证码校验流量，等待至少 10 分钟，再停止全部旧 API 实例。
3. 创建数据库备份并在隔离库验证可恢复。
4. 只读确认 migration 当前版本和 `dirty=0`。
5. 确认 MySQL 8、Redis 7 可用，Redis 不是无保护公网实例。
6. 确认新旧 API 不会同时访问 000055 之后的 schema；本功能禁止滚动混部。

数据库 migration、停止远程 API、真实邮件发送和生产发布均属于独立授权动作。本文中的命令不构成授权。

## 6. Migration

DirectMail 发布至少包含：

- `000055_add_directmail_email_management`
- `000056_add_email_admin_verify_bootstrap`
- `000057_fix_email_datetime_utc_seconds`

`scripts/migrate.sh` 使用执行器所在位置的 `MYSQL_HOST` 和 `MYSQL_PORT`。测试服务器宿主机连接当前 Compose MySQL 时使用 `127.0.0.1:13306`；容器内部使用 `3306`。

经批准后执行：

```bash
export MYSQL_HOST='<目标地址>'
export MYSQL_PORT='<目标端口>'
export MYSQL_DATABASE='<目标库名>'
export MYSQL_USER='<迁移账号>'
# MYSQL_PASSWORD 由安全环境注入，不写入命令历史。

./scripts/migrate.sh version
./scripts/migrate.sh up
./scripts/migrate.sh version
```

成功标准：当前仓库最新版本、`dirty=0`，且 000055/000056/000057 的结构和 seed 对账通过。`force` 只修改 migration 元数据，不执行 SQL；禁止用 `force` 掩盖 partial migration。

## 7. 测试环境部署

### 7.1 后端 API

在本地构建 Linux 二进制：

```powershell
Set-Location .\server
$env:GOOS='linux'
$env:GOARCH='amd64'
go build -trimpath -o ..\molin-api .\cmd\api
Get-FileHash -Algorithm SHA256 ..\molin-api
```

部署时必须记录本地和服务器 SHA-256，先上传到唯一临时文件，校验一致后再由受控发布器原子替换。启动器加载服务器侧 Secret 环境文件，且只能停止和启动本次确认的唯一 `molin-api`。不要使用通配 `pkill`、不要输出进程环境、不要把旧二进制直接覆盖后再验证。

启动后检查：

```bash
curl --fail --silent http://127.0.0.1:8080/api/health
curl --fail --silent http://127.0.0.1:8080/api/ready
curl --fail --silent http://127.0.0.1:8080/api/version
```

`health`、`ready` 和 `version` 只证明 API 进程可访问，不能证明 DirectMail 凭据、模板、Redis lease、供应商受理或最终送达正常。

### 7.2 前端

GitHub 仓库中手动运行“部署测试环境（前端）”工作流，分支选择 `main`。该工作流只构建和部署管理后台、用户控制台，不发布宿主机 API，也不执行 migration。

部署后检查管理后台和用户控制台页面、静态资源、`/api/health` 代理以及移动端布局。邮件管理入口还需要管理员 JWT、手机和邮箱双重认证及对应 `email:template:*` 权限。

## 8. 生产 Compose 部署

1. 在服务器仓库根目录通过 Secret 管理流程创建 `.env.prod`，权限限制为部署账号可读。
2. 使用 `git check-ignore --quiet .env.prod` 确认文件不会入库。
3. 不执行会展开完整环境变量的 `docker compose config` 并把输出保存到日志；可使用 `config --services` 检查服务集合。
4. 完成第 5、6 节全部门禁后构建镜像。
5. 停止旧 API，执行已批准 migration，再启动新 API；禁止新旧版本并存。

```bash
git check-ignore --quiet .env.prod
docker compose --env-file .env.prod -f infra/docker-compose.prod.yml config --services
docker compose --env-file .env.prod -f infra/docker-compose.prod.yml build api admin-console user-console

# 停止流量、备份与 migration 必须先按维护窗口完成。
docker compose --env-file .env.prod -f infra/docker-compose.prod.yml up -d api admin-console user-console
docker compose --env-file .env.prod -f infra/docker-compose.prod.yml ps
```

Compose 内 API 的数据库地址应为 `mysql:3306`，Redis 地址应为 `redis:6379`。不要把宿主机映射端口写入容器间连接配置。

## 9. 模板初始化

Migration 完成后五个场景默认未绑定且关闭。初始化顺序固定为：

1. 保持五个场景关闭。
2. 管理员完成双重认证并进入邮件管理。
3. 执行一次模板同步；确认五个模板均为 `approved`，变量完整且 `missing=false`。
4. 逐个核对供应商模板 ID、名称、主题和正文摘要，不把供应商 ID 写成代码常量。
5. 逐个启用本地模板。
6. 按当前平台模板 ID 和当前 binding version 绑定五个场景。
7. 先使用获批测试邮箱白名单执行测试发送，再按授权范围开启正式场景。

禁止直接向邮件业务表批量插入模板或绑定。测试发送和真实 OTP 都可能产生外部邮件，必须单独授权。

## 10. 发布后验证

| 层级 | 验证 | 可得结论 |
|---|---|---|
| 进程 | `health/ready/version` | API 可访问 |
| 配置 | 邮件概览、Redis 连接、Adapter 非 `not_ready` | 配置和基础依赖可用 |
| 模板 | 同步成功、五模板审核与变量检查通过 | 模板镜像可用于绑定 |
| 供应商 | 发送日志为 `accepted` 且有脱敏受理凭据 | DirectMail 已受理请求 |
| 业务 | 收件、验证码消费、重放和过期检查 | 对应业务链路在本环境通过 |

`accepted` 不等于最终送达。未执行真实外发时，必须写“未技术验证”或“负责人豁免”，不能写成发送成功。

发布后还应检查低基数指标和告警：`send_mail`、`query_templates`、`describe_template` 的成功、拒绝、超时和 unknown 分类。日志不得包含完整邮箱、验证码、AccessKey、模板正文、幂等键或供应商原始响应。

## 11. 回滚

### 11.1 配置回滚

- 配置错误时先停止邮件流量，再恢复受控备份并重启所有读取过旧配置的进程。
- 不要随意轮换 `EMAIL_ADDRESS_HMAC_SECRET` 或 `EMAIL_IDEMPOTENCY_SECRET`。轮换会改变邮箱匹配、白名单、幂等和锁作用域，必须有数据兼容方案。
- AccessKey 泄露时先在阿里云侧禁用或轮换，再更新服务器 Secret；不得在报告中粘贴旧值。

### 11.2 应用和 schema 回滚

- 先停止流量并停止全部新 API 实例，备份并验证恢复点。
- 000056 从未执行时，按 000055 的回滚门禁处理。
- 000056 已执行且没有成功 bootstrap receipt 时，先核验并 down 000056，再核验并 down 000055。
- 已存在成功 bootstrap receipt 时，默认保留 schema 55/56/57 和业务数据，只回滚应用兼容问题；不得自动执行 down。
- 任何 partial migration、未知引用或 `dirty=1` 都必须保留现场并专项诊断，禁止盲目重跑或 `force`。

不要使用 `docker compose down -v` 作为应用回滚，它会删除持久化卷。

## 12. 常见问题

### 邮件接口返回“邮件发送服务未就绪”

依次检查：两个 HMAC 密钥是否均至少 32 字节且不同、Redis 是否可用、Adapter 值是否合法、生产 Adapter 的 AccessKey/Region/发信地址/别名是否完整、Endpoint 是否为冻结的官方 HTTPS 地址。

### `EMAIL_ADAPTER=mock` 没有效果

Mock 只允许显式安全非生产环境。生产、未知或未正确声明的环境会失败关闭，不能通过 Mock 绕过真实配置。

### 测试环境没有返回明文验证码

只有显式安全非生产环境且 `EMAIL_DEBUG_RETURN_CODE=true` 时才可能回码。生产和未知环境始终强制关闭。正式 DirectMail 验收不应依赖响应回码。

### DirectMail 返回 `accepted` 但用户没有收到

`accepted` 只表示供应商受理。继续检查退信、垃圾箱、域名信誉、供应商投递日志和收件系统策略，不要自动重复发送同一业务请求。

### 出现 `provider_outcome_unknown`

请求可能已经被供应商处理。系统会持久化 unknown 并阻断自动重发；等待验证码过期后按授权流程处理，禁止立即重复调用。

### MySQL 应该填 `3306` 还是 `13306`

宿主机连接 `infra/docker-compose.yml` 的 MySQL 使用 `13306`；容器内部或 Compose 服务间连接使用 `3306`。最终以执行器所在网络命名空间和已核验的端口映射为准。

## 13. 相关文档

- 环境变量模板：`infra/.env.example`
- 基础设施与监控：`infra/README.md`
- 认证模块实现：`server/internal/modules/auth/README.md`
- 功能验收：`docs/aliyun-directmail-email-template-feature-acceptance.md`
- QA 报告：`tests/email/directmail-phase4-qa-report.md`
- API 契约：`docs/full-api-design.md`、`docs/frontend-api-reference.md`

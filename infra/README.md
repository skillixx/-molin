# 基础设施

这个目录用于存放本地开发基础设施。

## 服务

- MySQL 8：业务数据库。
- Redis 7：验证码、权限缓存、限流。
- RabbitMQ：订单支付、资产开通、按量计费等异步事件。
- MinIO：文件、图标、实名附件、帮助文档媒体资源。

## 启动

```bash
docker compose -f infra/docker-compose.yml up -d
```

## 测试环境部署

测试环境部署为**手动触发**，合并到 main 不会自动部署。

- 操作路径：GitHub 仓库 → Actions → 选择「部署测试环境（前端）」工作流 → Run workflow（分支选 main）。
- 对应工作流文件：`.github/workflows/deploy-test.yml`（`workflow_dispatch`）。
- **范围：仅前端**。在 runner 上构建 `molin-admin` / `molin-user` 镜像 → 传到测试服务器 → 重建容器（保留 `--add-host api:host-gateway`，nginx 把 `/api` 代理到宿主机上的 `molin-api`）。
- **需配置 Secrets**（Settings → Secrets and variables → Actions）：`TEST_SERVER_HOST`、`TEST_SERVER_USER`、`TEST_SERVER_PASSWORD`（本服务器为密码认证，SSH 端口 10003）。
- **后端不在本工作流内**：测试服 `molin-api` 是宿主机二进制（非容器），构建/重启见 `infra/CLAUDE.md` 的「测试服务器」一节，需单独执行。
- DirectMail 完整配置、端口选择、Migration、模板初始化、生产 Compose 和回滚流程见 `docs/directmail-configuration-deployment-guide.md`。

## 邮件 Phase 4 测试环境监控

本节只描述测试环境运行方式，不授权执行数据库 migration、远程部署或真实邮件发送。Prometheus 直接抓取宿主机 API 的
`GET /api/internal/metrics`，请求仍必须同时通过 `X-Internal-Token` 与来源 IP 两道应用层门禁。监控容器使用固定低基数标签，
不采集完整邮箱、验证码、AccessKey、TemplateData、锁 Token 或供应商原始响应。

### 配置键清单

测试环境 `infra/.env.test` 必须保持被 Git 忽略，并至少确认以下键：

- 应用与依赖：`APP_ENV=test`、`API_HOST`、`API_PORT`、全部 `MYSQL_*`、`REDIS_ADDR`、`REDIS_PASSWORD`、`REDIS_DB`。
- 应用安全：`JWT_SECRET`、`REFRESH_TOKEN_SECRET`、`ID_CARD_HMAC_SECRET`、`ADMIN_VERIFY_EXPIRE_HOURS`。
- DirectMail：`DIRECTMAIL_ACCESS_KEY_ID`、`DIRECTMAIL_ACCESS_KEY_SECRET`、`DIRECTMAIL_REGION`、`DIRECTMAIL_ACCOUNT_NAME`、
  `DIRECTMAIL_FROM_ALIAS`、`DIRECTMAIL_ENDPOINT`、`EMAIL_ADAPTER=production`。
- 邮件安全：`EMAIL_ADDRESS_HMAC_SECRET` 与 `EMAIL_IDEMPOTENCY_SECRET` 均至少 32 字节且互不相同，
  `EMAIL_DEBUG_RETURN_CODE=false`。
- 来源边界：`TRUSTED_PROXY_IPS`、`INTERNAL_ALLOWED_IPS`、`INTERNAL_TRUSTED_PROXY_IPS`、`INTERNAL_API_TOKEN`。
- Prometheus：`PROMETHEUS_CONTAINER_IP`、`PROMETHEUS_SUBNET`、`PROMETHEUS_PORT`。

`INTERNAL_ALLOWED_IPS` 必须精确包含 `PROMETHEUS_CONTAINER_IP`。Prometheus 是直接抓取客户端，不是可信代理，禁止把它加入
`INTERNAL_TRUSTED_PROXY_IPS`；后者只填写真实连接 API 的监控反向代理地址或网段。`TRUSTED_PROXY_IPS` 用于公开邮件/手机验证码发码及密码重置来源，
必须与内部 metrics 配置分离。监控网段还必须先与服务器现有 Docker/VPC 网段核对，发生冲突时先调整两项 Prometheus 地址配置。

### 安全配置向导

在仓库根目录使用 PowerShell 执行：

```powershell
./scripts/configure-directmail-test.ps1

# 配置同一 Git 仓库中已注册主 worktree 的测试环境文件；任意其他路径会失败关闭。
./scripts/configure-directmail-test.ps1 `
  -EnvironmentFile "D:\molingproject\molin\infra\.env.test"
```

向导具有以下安全边界：

- 默认更新当前 worktree 的 `infra/.env.test`；通过 `-EnvironmentFile` 选择其他目标时，只允许同一 Git 仓库
  `git worktree list --porcelain` 已注册且当前主机实际存在的 worktree 下精确 `infra/.env.test`，任意其他路径失败关闭。
- 目标 worktree 内的目标文件、同目录备份和临时文件必须分别通过 `git check-ignore --quiet`，否则在读取凭据前拒绝继续。
- AccessKey ID、AccessKey Secret 和发信地址使用 `SecureString` 输入，不回显值。
- 自动生成两枚互不相同的 32 字节随机邮件 HMAC 密钥及一枚 32 字节随机内部 Token。
- 固定写入官方 HTTPS Endpoint、`production` Adapter 与关闭调试回码，不写模板 ID 或模板正文。
- 原子替换前在同目录创建带时间戳的 `.env.test.backup.*`。备份同样含敏感信息，只能短期用于测试环境回退，
  完成验证后应按组织密钥留存策略安全删除，不得上传或提交。
- 每次运行都会轮换邮件 HMAC 与内部 Token；已有测试数据或运行实例存在时，必须先评估 HMAC 命中失效和监控抓取中断影响。

脚本输出只显示键名和成功状态，不显示任何值。PowerShell/.NET 无法保证已经形成的托管字符串立即从进程内存清除，
因此只能在受控测试主机运行，结束后关闭该 PowerShell 会话。

### 静态检查

先执行仓库内不需要凭据的 Nginx 来源头检查：

```powershell
python infra/nginx/verify_forwarded_headers.py
```

该脚本除来源头外，还会分别检查 `admin.conf` 与 `user.conf` 的 bootstrap 关闭边界：目标路径必须有专用 404
`location`，server 层必须把 Nginx 核心对 TRACE 的 405 转入命名 location；命名 location 只能对精确目标 URI 返回
404，其他 URI 必须继续返回 405。删除任一 TRACE 归一化指令都会使静态检查失败，不能仅凭 GET/POST 404 判定通过。

测试服真实前端入口当前为用户控制台 `3000` 和管理后台 `3001`；宿主机 `80` 未发布、无监听，因此访问公网 `:80`
得到 HTTP `000` 表示请求没有到达 Nginx 或 API，不能与 3000/3001 的应用层 404 混为一谈。热加载配置后，在服务器本机
对两个真实入口分别验证健康状态和八种方法：

```bash
for port in 3000 3001; do
  curl --fail --silent --output /dev/null "http://localhost:${port}/api/health"
  for method in GET HEAD POST PUT PATCH DELETE OPTIONS TRACE; do
    if [ "$method" = HEAD ]; then
      code=$(curl --silent --head --output /dev/null --write-out '%{http_code}' \
        "http://localhost:${port}/api/internal/email/bootstrap/admin-verify")
    else
      code=$(curl --silent --request "$method" --output /dev/null --write-out '%{http_code}' \
        "http://localhost:${port}/api/internal/email/bootstrap/admin-verify")
    fi
    [ "$code" = 404 ] || { echo "端口 ${port} 的 ${method} 未返回 404"; exit 1; }
  done
done
```

健康接口 200 证明通用 `/api/` 反代仍可用；关闭路径八方法全部 404 证明请求在控制台 Nginx 被统一隐藏，其中 TRACE
404 依赖上述 405 精确归一化规则。该检查只发送无请求体、无 Token 的关闭路径请求，不代表 bootstrap 被启用或执行。

具备 Docker 后，再使用固定版本镜像执行真实语法检查：

```powershell
docker run --rm --add-host api:127.0.0.1 `
  -v "${PWD}/infra/nginx:/etc/nginx/conf.d:ro" `
  nginx:1.27-alpine nginx -t

docker run --rm --entrypoint /bin/promtool `
  -v "${PWD}/infra/prometheus:/etc/prometheus:ro" `
  prom/prometheus:v3.12.0-distroless `
  check rules /etc/prometheus/email-alerts.yml

# 完整配置引用内部 Token 文件，必须通过 Compose secret 挂载后检查，禁止制作仓库内假 Token 文件。
docker compose --env-file infra/.env.test -f infra/docker-compose.yml `
  --profile monitoring run --rm --entrypoint /bin/promtool prometheus `
  check config /etc/prometheus/prometheus.yml
```

### 启动与验证

启动前必须先确认 API 已使用测试环境配置运行，且 `/api/health`、`/api/ready`、`/api/version` 可访问。当前 `/api/ready`
只代表进程已完成启动，不能替代邮件 Adapter、Redis 锁或 metrics 双门禁验证。不得由监控启动流程自动执行 migration。

```powershell
docker compose --env-file infra/.env.test -f infra/docker-compose.yml `
  --profile monitoring up -d prometheus

curl.exe --fail http://127.0.0.1:19090/-/ready
```

Compose 从运行时 `INTERNAL_API_TOKEN` 创建 `/run/secrets/internal_api_token`，Prometheus 再通过 `http_headers.files` 读取并发送
`X-Internal-Token`。Token 不进入镜像环境变量、Prometheus YAML 或版本库。Prometheus 管理端口固定绑定宿主机回环，远程查看须使用
受控 SSH 隧道，不得改成公网监听。

启动后在 Prometheus Targets 页面确认 `molin-email-adapter` 为 UP，并检查以下告警规则已加载：

- `MolinEmailMetricsTargetDown`：连续两分钟抓取失败。
- `MolinEmailAdapterFailureRateHigh`：单场景五分钟内至少 10 次调用，失败及超时比例持续超过 20%。
- `MolinEmailAdapterOutcomeUnknown`：单场景五分钟内出现任一 `timeout`。冻结指标把供应商超时或响应未知统一归入该结果，
  不新增错误原文等高基数标签。

当前指标不暴露 Redis 锁所有权异常计数，因此本配置不能替代该项结构化日志/告警验收；补充专用低基数指标前，只能在运行日志中核验。

### 回滚

监控异常时先停止 Prometheus，不停止 API、不修改数据库、不删除持久化数据：

```powershell
docker compose --env-file infra/.env.test -f infra/docker-compose.yml `
  --profile monitoring stop prometheus
```

禁止把 `docker compose down -v` 作为常规回滚命令，因为它会删除监控及其他 Compose 数据卷。若需要恢复配置，先停止相关测试流量，
再由授权人员使用向导生成的同目录备份恢复 `infra/.env.test`，并重启全部读取过旧 Token/HMAC 的测试进程。

### 远程 Redis 仅测试安全边界

邮件分布式锁把 Redis 作为发布必需依赖。测试 Redis 即使 `PING` 成功，也不代表具备安全发布条件。无密码、无 TLS 的公网 Redis
可能被读取、删除或伪造锁键，并可能遭受清库和拒绝服务攻击，必须遵守：

- 仅限测试 API 主机或专用私网访问；安全组和主机防火墙默认拒绝其他来源。
- 使用独立测试实例，禁止连接生产 Redis，禁止存放生产数据或生产密钥；Redis DB 编号不是安全隔离边界。
- 优先启用 ACL/强密码和 TLS，或通过受控私网/加密隧道访问；应用当前未配置 TLS 时，不得把明文连接扩展到公网。
- Redis 锁契约测试会写入并清理测试键，必须在取得测试执行授权、确认键前缀和清理范围后运行，不能把只读 `PING` 当作锁契约通过。
- Redis 重启或锁键丢失后不得自动重发邮件，仍由数据库中的 pending/unknown 持久化记录阻断重复发送。

### `admin_verify` 一次性 bootstrap 运维边界

四个配置键固定为 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`。enabled 只有配置键缺失时默认 false；字面 true/false（大小写不敏感）有效，显式空字符串或其他值必须使应用启动失败；显式 false 时路由不注册且全方法404。enabled=true 时其他三项任一缺失、空值、弱占位或非法均必须使应用启动失败，不允许只降级 ready。Bootstrap Token 按原始 UTF-8 字节校验，必须至少 32 字节、至少包含 8 种不同原始字节、无首尾空白，并以大小写不敏感方式拒绝 `REPLACE_WITH_EMAIL_BOOTSTRAP_TOKEN`、`REPLACE_WITH_INTERNAL_API_TOKEN`、`CHANGE_ME`、`CHANGEME`、`DEFAULT`、`SECRET`、`TEST` 等冻结弱占位；若与已配置的 `INTERNAL_API_TOKEN` 原始值完全相等也必须启动失败，比较过程不得记录任一值。只有经批准的首次配置维护窗口才通过安全 Secret 注入；不得复用其他内部配置，也不得把值写入 Compose YAML、镜像、命令历史、日志、工单或报告。

两项 Bootstrap CIDR 必须通过各自专用键独立显式配置，禁止从 `INTERNAL_ALLOWED_IPS`、`INTERNAL_TRUSTED_PROXY_IPS`、`TRUSTED_PROXY_IPS` 等平台列表读取、合并或回退。Bootstrap allowed 与 bootstrap trusted-proxy 两份列表之间若包含相同的规范化 CIDR 条目必须启动失败，避免同一网段同时承担调用来源与可信代理角色；不同前缀的部分重叠允许。两份 Bootstrap 列表独立显式配置后，分别允许与现有平台 CIDR 列表使用相同条目或部分重叠，同一 Nginx 代理网段可按不同端点需要重复配置。任一 Bootstrap 列表包含解析后前缀长度为 0 的 IPv4/IPv6 全网网段，包括 `0.0.0.0/0`、`::/0` 及其等价写法，均必须启动失败；各列表分别规范化后，多个非零前缀的并集若覆盖完整 IPv4 或 IPv6 地址族也必须启动失败，例如 IPv4 的 `0.0.0.0/1,128.0.0.0/1` 两半或 IPv6 的 `::/1,8000::/1` 两半，不能通过拆分全网规则绕过失败关闭校验。

反向代理不得向公网、管理后台或用户控制台转发该路径，只允许批准的运维网段访问，并覆盖单值 `X-Real-IP`、删除 XFF/Forwarded。调用还必须携带正常管理员 JWT、有效手机 MFA、专用 `email:template:bootstrap` 权限、独立 Header Token 和 Idempotency-Key。前端没有入口，内部 Token 也不能代替管理员身份。

维护窗口固定流程为：执行并完整核验 000055 → 执行并完整核验 000056 与备份恢复点 → 核对一个 TemplateId → 短时启用独立配置并重启 → 以严格 POST/Header/Content-Type/Body 调用一次 → 核对 `ProviderTemplate.Name=molin_admin_verify_code_v1`（大小写精确）、approved、Code/ExpireMinutes → 只读核对 receipt/绑定/事务审计 → 立即移除 enabled/token 并重启 → 从运维网络与非运维网络分别确认路径 404 → 再走正常邮箱 MFA。请求体中的 `provider_template_id` 必须是 1-64 字节的 ASCII 十进制正整数：只允许 ASCII 数字且数值不能全零；空值、全零、65 字节及以上、非数字、正负号、小数、指数或任何空白均在前置校验返回 400，attempt 审计、Adapter 调用和数据库写入增量必须全部为 0。该规则沿用现有 64 字节列，不扩容数据库字段。模板资格只以上述三个客观条件判定；任何报告只记录配置键名、状态、内部记录 ID 和脱敏摘要。

不同 Idempotency-Key 可并发完成只读 Describe；bootstrap 并发控制仅在数据库写阶段执行，由事务 `SELECT ... FOR UPDATE` 锁定 admin_verify、receipt 复查、初始态 CAS 与 scope 唯一约束决定唯一胜者。`admin_id` 同时纳入既有 `EMAIL_IDEMPOTENCY_SECRET` 的 key HMAC 作用域和 request fingerprint，receipt 重放还必须匹配当前管理员的 `completed_by`；同一管理员、同一 key、同一 fingerprint 的并发首次请求即使均已 Describe，后取得行锁者仍返回原成功且 `idempotent=true`。跨管理员复用同 key 固定返回 `409/40900「管理员邮箱认证场景已完成首次配置」`，不得泄露原操作者。每次真实 Describe 复用 `operation=describe_template,scene=template_sync` 指标一次，不新增序列。attempt 审计成功后才能外呼，result 审计必须与镜像/绑定/receipt 同事务，并固定使用 `target_type=email_admin_verify_bootstrap_receipt`、`target_id=receipt` 的内部十进制 ID；不得用供应商 TemplateId、管理员 ID 或 scene 代替，失败必须全回滚。

Bootstrap Token 缺失/空/重复/逗号多值/错误统一403；Authorization 按标准401；Idempotency-Key异常400。`ADMIN_VERIFY_EXPIRE_HOURS<0` 无论 bootstrap 是否启用均必须启动失败；`=0` 只表示不因历史时间过期，手机 MFA 时间戳缺失、恰到过期边界或晚于当前数据库 UTC 时间仍失败关闭，且无 attempt 审计、Adapter 或数据库副作用。仅动态获权但没有 admin 角色的普通用户仍403。供应商 `DescTemplate` 详情只采用真实且未废弃字段 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText`；`QueryTemplateByParam` 列表字段 `TemplateId/TemplateName/TemplateStatus/CreateTime` 另行处理，不得混用。字段以 [阿里云 DirectMail DescTemplate](https://help.aliyun.com/en/direct-mail/api-dm-2015-11-23-desctemplate) 为准，JSON `TemplateName` 精确映射 Adapter `ProviderTemplate.Name`。

000056 使用独立 `migration_000056_permission_ownership`，要求唯一 admin 角色，精确记录专用权限和 admin 绑定的预存/新增状态；partial-up/down 或未知引用失败关闭。回滚矩阵固定为：A）000056 未执行，走原 000055 down；B）000056 已执行且无成功 receipt，先 down 000056、核验后再 down 000055；C）存在成功 receipt，应用回滚保留 schema 55+56、receipt、模板镜像和绑定，不执行任一 down。C 类确需回到 55 前必须另立高风险变更，先完成备份恢复验证、不可变审计留证和 QA/产品经理/运维联合批准，解除全部引用后依次 down 000056、down 000055，禁止 force。该流程不授权 DirectMail 全量同步、测试发送、真实 OTP、数据库手工绑定、手工写 MFA 时间戳或停止当前 API，以上动作仍需各自独立授权。

静态边界使用 `python infra/nginx/verify_forwarded_headers.py` 验证，脚本会同时确认 admin/user/api 三类公开入口显式返回 404 且没有反代该路径。具备独立测试数据库和可启动 API 二进制后，Windows 使用 `powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/verify-admin-verify-bootstrap.ps1 -ApiExecutable <测试二进制绝对路径>` 做本机隔离验证；脚本依次检查“四键全部缺失时 404 → enabled=true 且其余三键缺失时启动失败 → 显式 false 后 404”。脚本使用随机空闲回环端口，只终止自己创建的子进程，不访问远程服务、不执行 migration、不调用 DirectMail，也不打印 Token、CIDR、子进程日志或环境变量值。若基础依赖未就绪导致默认关闭实例无法通过 health，应先修复隔离测试环境，不能把依赖失败当作 bootstrap 验证通过。

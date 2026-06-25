# presenton 接入 — 运维部署交接清单

> 对象：运维。后端（墨灵网关 D1/D2/C1，PR #258）与 fork 二开（F-A/B/C，已合 fork main）均已就绪，
> 上线只差运维侧：① 内网部署 presenton fork ② 两端环境变量 ③ 反代 + CSP。
> 安全约定：`MOLIN_TRUST_SECRET` 等密钥**禁止提交仓库**，用 `infra/.env.*`（已 gitignore）注入。

---

## 0. 拓扑

```
浏览器(user-console)
  │ iframe src = https://<墨灵公网域名>/app/presenton/launch?ticket=...
  ▼
墨灵 API(server，已含 D2 反代)  ← 公网，复用现有部署
  │ 内网转发 + 注入 X-Molin-* 头
  ▼
presenton fork(FastAPI+Next)   ← 仅内网，不暴露公网
  │ 内部 LLM 调用
  ▼
token_gateway(墨灵 OpenAI 兼容入口)  ← 已有
```

---

## 1. 内网部署 presenton fork

- 源码：墨灵主仓子模块 `services/presenton`（已 pin 到含 F-A/B/C 的 fork main `fb78e001`）。
  - 拉取：`git submodule update --init --depth 1 services/presenton`
- 用 fork 自带 production docker-compose 起服务，**只绑内网**（不映射公网端口；仅墨灵 API 所在网络可达）。
- 记录 FastAPI 的内网访问地址（host:port），供下方 `PRESENTON_INTERNAL_BASE_URL`。
- **数据库**：`DATABASE_URL` 指向 MySQL/Postgres（**禁用默认 SQLite**），并跑 fork 的 Alembic 迁移（含 F-B 的 `user_id` 加列 `b7c1a9d2e3f4`）。
- 关闭/不映射 Next.js 对外端口，统一经墨灵 D2 反代访问。

## 2. presenton fork 端环境变量

| 变量 | 值 | 说明 |
|---|---|---|
| `CUSTOM_LLM_URL` | token_gateway 的 OpenAI 兼容入口 | F-A：内部 LLM 默认 base_url |
| `CUSTOM_LLM_API_KEY` | 占位值（如 `placeholder`） | 实际 key 由墨灵按请求注入，env 仅兜底 |
| `DATABASE_URL` | MySQL/Postgres DSN | 禁用 SQLite |
| `MOLIN_TRUST_SECRET` | **强随机串（与墨灵侧一致）** | F-C 防伪造，**必须配**，否则网络可达时可冒充任意用户 |

> `MOLIN_TRUST_SECRET` 用 `openssl rand -hex 32` 生成一次，两端填同一个值。

## 3. 墨灵 API 端环境变量（`PRESENTON_*`）

加到墨灵 API 的 `infra/.env.*`：

| 变量 | 示例 | 说明 |
|---|---|---|
| `PRESENTON_INTERNAL_BASE_URL` | `http://presenton-internal:8000` | **必配，否则 D2 反代不注册**（内网 presenton 入口） |
| `PRESENTON_GATEWAY_BASE_URL` | `https://<墨灵公网域名>` | 拼前端嵌入 URL 的域名 |
| `PRESENTON_PATH_PREFIX` | `/app/presenton` | 反代公开前缀（默认值，一般不改） |
| `MOLIN_TRUST_SECRET` | **同 fork 端那一个** | 注入 `X-Molin-Auth-Secret` |
| `PRESENTON_LLM_BASE_URL` | （留空） | 可选；留空则 presenton 用其 `CUSTOM_LLM_URL` |
| `PRESENTON_APP_CODE` | `presenton-ppt` | 默认值，与 seed 000052 一致 |
| `PRESENTON_TICKET_TTL_SECONDS` | `300` | 票据有效期 |
| `PRESENTON_SESSION_TTL_SECONDS` | `7200` | 反代会话有效期 |

> 另确认墨灵已配 `API_KEY_HMAC_SECRET`（D1 签发用户 key 依赖它，否则打开入口不注册）。

## 4. 反向代理 + CSP

- 墨灵公网 nginx：把 `/app/presenton/`（含 `/app/presenton/launch`）路由到**墨灵 API**（D2 反代在墨灵 API 内，不是直连 presenton）。其余 `/api/*` 保持现状。
- presenton **不开公网**；仅墨灵 API 内网可达（D2 注入的可信头才有意义）。
- **CSP（允许 iframe 内嵌）**：presenton 响应（或墨灵反代回写）需带
  `Content-Security-Policy: frame-ancestors 'self' https://<user-console 域名>;`
  且**不要**设 `X-Frame-Options: DENY/SAMEORIGIN`（会盖过 frame-ancestors 导致 iframe 被拦）。
- 跨源场景：D2 会话 cookie 在 https 下为 `SameSite=None; Secure`，nginx 不要剥 Set-Cookie。

## 5. 部署后验证

1. 墨灵 API 启动日志：应看到 D1/D2 已注册（无 "PRESENTON_INTERNAL_BASE_URL 未配置" 警告）。
2. 迁移：墨灵 `000052` 已应用（市场出现「PPT 生成器」）；presenton Alembic `user_id` 列已加。
3. 端到端：用一个**已开通**的测试用户，前端点「打开」→ iframe 应加载 presenton 编辑器。
4. 计费归属：在编辑器内触发 AI 生成 → token_gateway 应记到**该用户**名下（验 F-A 生效）。
5. 隔离：换另一用户，确认看不到前一个用户的文稿（验 F-B）。
6. 防伪造：直接向内网 presenton 发不带/错 `X-Molin-Auth-Secret` 的请求 → 应被当普通请求（无身份）。

## 6. 完成后

- 验证通过后，回报后端：可逐个确认合并 **#257（子模块，已 bump 无隐患）** 与 **#258（墨灵网关，含迁移 000052）**。
- 合并 #258 后按 `server/CLAUDE.md` 备份本地库并同步测试库。

---

## 7. 手动部署详细命令（测试服务器 `~/molin`）

> 全流程：①presenton 内网起来(:5001) → ②墨灵 API 从 feature 分支重建+迁移 → ③nginx 路由+CSP → ④验证。
> 基于 fork 真实 `docker-compose.yml` 核实，已避开两个坑（见下方 🔴）。

### ① 部署 presenton 内网

fork `docker-compose.yml` 实情：`production` 服务端口 `${PRESENTON_HTTP_HOST_PORT:-5001}:80`（容器内 nginx 在 80），`MIGRATE_DATABASE_ON_STARTUP=true`（启动自动跑 Alembic，含 F-B 的 user_id 加列，**无需手动 alembic**）。

🔴 **坑1**：compose **未透传 `MOLIN_TRUST_SECRET`** → 不补则 F-C 防伪造失效（任何带 X-Molin-User-Id 的请求都被信任）。
🔴 **坑2**：默认端口绑 `0.0.0.0`（公网可达）→ 要"仅内网"必须改 `127.0.0.1`。

```bash
cd ~/molin
git submodule update --init --depth 1 services/presenton
cd services/presenton
```

改 `docker-compose.yml` 的 `production` 服务两处：
```yaml
  production:
    ports:
      - "127.0.0.1:${PRESENTON_HTTP_HOST_PORT:-5001}:80"   # ← 加 127.0.0.1，仅回环
      # "1455:1455" 不用 Codex 可删
    environment:
      - MIGRATE_DATABASE_ON_STARTUP=true
      - MOLIN_TRUST_SECRET=${MOLIN_TRUST_SECRET:-}          # ← 新增，透传 F-C 密钥
      # 其余保持原样
```

建 presenton 的 `.env`（compose 自动读同目录 `.env`）：
```bash
cat > .env <<EOF
PRESENTON_HTTP_HOST_PORT=5001
LLM=custom
CUSTOM_LLM_URL=<墨灵 token_gateway 的 OpenAI 兼容入口，如 http://127.0.0.1:8080/v1>
CUSTOM_LLM_API_KEY=placeholder
DATABASE_URL=<异步驱动，见下方注意>
MOLIN_TRUST_SECRET=<从墨灵 infra/.env.test 复制【同一个】值，别新生成>
EOF
chmod 600 .env

docker compose up -d production        # 镜像构建较久
docker compose logs -f production      # 看 Alembic 迁移 + 启动
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:5001/
ss -tlnp | grep 5001                   # 确认只绑 127.0.0.1
```

### ② 墨灵 API 重建（含 D1/D2/C1）+ 测试库迁移

```bash
cd ~/molin
git fetch origin
git checkout feature/backend-presenton-open-gateway
git pull origin feature/backend-presenton-open-gateway
export $(grep -v '^#' infra/.env.test | xargs)

bash scripts/migrate.sh up             # 末尾应见 52/u seed_presenton_app，无 dirty
cd server && go build -o ../molin-api ./cmd/api && cd ..
pkill molin-api 2>/dev/null; sleep 1
nohup ./molin-api > api.log 2>&1 &
sleep 2 && grep -iE "presenton" api.log
#   ✅ 无 "PRESENTON_INTERNAL_BASE_URL 未配置"、无 "API_KEY_HMAC_SECRET 未配置...D1 未启用"
```

### ③ nginx 路由 + CSP

墨灵公网 nginx server 块加（presenton 不单独开公网，只经墨灵 API 反代）：
```nginx
location /app/presenton/ {
    proxy_pass http://127.0.0.1:8080;     # 墨灵 API
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    add_header Content-Security-Policy "frame-ancestors 'self' https://<控制台域名>" always;
    proxy_pass_header Set-Cookie;         # 别剥 SameSite=None;Secure 会话 cookie
    # 注意：本 location 不要设 X-Frame-Options（会盖过 frame-ancestors）
}
```
```bash
nginx -t && nginx -s reload
```
> 控制台与网关同源时 CSP 那行可省；跨源才必须放行。

### ④ 端到端验证（6 步）

```bash
# 1. presenton 活着且仅绑回环
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:5001/ ; ss -tlnp | grep 5001
# 2. 迁移已应用（市场有 presenton）
mysql -h127.0.0.1 -P13306 -umolin -p"$MYSQL_PASSWORD" molin \
  -e "SELECT code,status FROM applications WHERE code='presenton-ppt';"
# 3. D1 打开入口（需已开通用户的 JWT）
curl -s -H "Authorization: Bearer <测试用户JWT>" http://127.0.0.1:8080/api/app/presenton/open
#   ✅ {code:0,data:{embed_url,expires_in_seconds}}；未开通→40300（先给该用户开通 presenton-ppt）
# 4. 计费归属(F-A)：走 embed_url 进编辑器触发 AI 生成，查 token_gateway 用量记到该用户
# 5. 隔离(F-B)：换另一用户，确认看不到前一用户文稿
# 6. 防伪造(F-C)：直接打内网 presenton 带伪造头但无正确 secret，应被当普通请求
curl -s -H "X-Molin-User-Id: 999" http://127.0.0.1:5001/api/v1/ppt/presentation/all
#   ✅ 不返回 999 的数据
```

### 最易卡的两点（提前确认）
1. **`CUSTOM_LLM_URL`** 指向的 token_gateway OpenAI 兼容入口，测试环境是否就绪（不在则 AI 生成不可用）。
2. **`DATABASE_URL` 异步驱动**：MySQL 用 `mysql+aiomysql://...`、Postgres 用 `postgresql+asyncpg://...`；确认 presenton 镜像自带对应驱动，否则启动连不上库。

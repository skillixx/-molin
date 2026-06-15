# 后端 Go API — 开发规范

## 职责边界

负责 `server/` 下所有 Go 业务代码，包含：
- `server/internal/modules/` — 各业务模块（auth、iam、identity、product、order、billing 等）
- `server/internal/middleware/` — 认证、权限、限流中间件
- `server/pkg/` — 公共基础库（db、cache、crypto、jwt 等）
- `server/cmd/api/` — 应用入口

不负责：`infra/`、`scripts/`、`.github/workflows/` 等运维配置。

## 开发完成后必须执行：数据库备份 & 同步测试环境

**每个功能模块开发完成、通过本地自测后，必须按以下流程备份数据库并同步到测试服务器。**

### 第一步：备份本地开发库

```bash
# 导出本地开发库（端口 13306）
mysqldump -h 127.0.0.1 -P 13306 -u molin -p$TEST_MYSQL_PASS \
  --single-transaction --routines --triggers \
  molin > scripts/backup/dev-$(date +%Y%m%d-%H%M%S).sql

# 同时保留一份固定名称的最新备份，供测试环境使用
mysqldump -h 127.0.0.1 -P 13306 -u molin -p$TEST_MYSQL_PASS \
  --single-transaction --routines --triggers \
  molin > scripts/backup/latest.sql
```

备份文件放在 `scripts/backup/`，`latest.sql` 是测试环境始终使用的最新版本。

### 第二步：上传备份到测试服务器并还原

```bash
# 上传 latest.sql 到测试服务器
sshpass -p '$TEST_SSH_PASS' scp -P 10003 \
  scripts/backup/latest.sql \
  pc@8.130.9.163:~/molin/backup/latest.sql

# 在测试服务器上还原（会覆盖测试库，先清空再导入）
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163 "
  mysql -h 127.0.0.1 -P 13306 -u molin -p$TEST_MYSQL_PASS \
    -e 'DROP DATABASE IF EXISTS molin; CREATE DATABASE molin CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
  mysql -h 127.0.0.1 -P 13306 -u molin -p$TEST_MYSQL_PASS molin < ~/molin/backup/latest.sql
  echo '数据库还原完成'
"
```

### 第三步：确认测试服务器 API 使用最新数据

```bash
# 如果部署了新的 API 二进制，重启 API
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163 "
  pkill molin-api 2>/dev/null; sleep 1
  export \$(grep -v '^#' ~/molin/infra/.env.test | xargs)
  nohup ~/molin/molin-api > ~/molin/api.log 2>&1 &
  echo 'API 已重启'
"

# 验证测试服务器 API 健康
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163 \
  'curl -s http://localhost:8080/api/health'
```

### 注意事项

- `scripts/backup/*.sql` 已在 `.gitignore` 中排除，禁止入库（可能含测试数据）
- 备份文件按日期命名（`dev-YYYYMMDD-HHMMSS.sql`），本地保留最近 5 份即可，旧的手动清理
- 测试服务器的数据库会被**完全覆盖**，测试工程师在测试期间不要手动修改测试库

## 测试服务器信息

| 项目 | 值 |
|---|---|
| 公网 IP | `8.130.9.163` |
| SSH 端口 | `10003` |
| SSH 用户 | `pc` |
| 项目目录 | `~/molin/` |
| MySQL 端口 | `13306` |
| API 端口 | `8080` |

## 分支和提交规范

- 每个功能必须在 `feature/backend-{模块}-{描述}` 分支上开发，禁止直接 push `main`
- Commit message 格式：`模块：描述`，例如 `auth：添加注册接口`

## 代码规范

- 所有代码注释使用中文
- 禁止在代码中硬编码密钥、密码、Token
- 身份证号必须 HMAC-SHA256 存储（详见全局 `CLAUDE.md` 安全约定）
- Refresh Token DB 只存 HMAC-SHA256 哈希值

## 各后端模块架构权威文档

| 负责人 | 模块 | 架构权威文档 |
|---|---|---|
| 后端甲 | auth / iam / identity / audit | `docs/api-test-guide-backend-a.md`、各模块 `CLAUDE.md` |
| 后端乙 | product / order / billing / finance_consumer | **`docs/backend-dev-plan-backend-b.md`**、各模块 `CLAUDE.md` |
| 后端丙 | asset / membership / app / provision / content | 各模块 `CLAUDE.md` |

## Round 7 全后端红线（列表/契约一致性）

- **D-95 扁平分页**：所有列表接口返回 `{items,page,page_size,total}`（`data` 顶层），禁止嵌套 `{list,pagination}`；参见 `docs/api-pagination-standard.md`。
- **批量写入 body 统一 `items` 键**（如商品 access/prices 覆盖写）。
- **接口字段/错误码必须与 `docs/full-api-design.md` 一致**；契约变更后必须同步 `docs/frontend-api-reference.md` 与两端控制台（字段变更未同步前端为反复出现根因）。
- **新增/变更权限码必须同时建 seed migration**（历史多次因缺 seed 导致上线 P1）。

# Molin 云管理平台

Molin 云管理平台是一个基于 Vue3 + Go + MySQL 的管理平台，用于商品售卖、计费、用户资产、实名制认证、应用管理、会员体系，以及后续 GPU、Agent、Skills、Token 网关等模块。

## 项目目录

```text
server
  Go API 服务。

web/admin-console
  Vue3 管理后台。

web/user-console
  Vue3 用户控制台。

web/shared
  前端共享代码。

infra
  本地开发基础设施。

docs
  规划、接口、任务分配和架构文档。

skills
  项目专用 Codex skills。
```

## 本地基础环境

```bash
docker compose -f infra/docker-compose.yml up -d
```

## 创建数据库表

```bash
chmod +x scripts/create_mysql_tables.sh
./scripts/create_mysql_tables.sh
```

## 启动 API

```bash
cd server
go run ./cmd/api
```

健康检查接口：

```text
GET /api/health
GET /api/ready
GET /api/version
```

## 开发设计文档

- [完整接口设计](docs/full-api-design.md)
- [数据库表设计](docs/database-schema-design.md)
- [数据量和分库分表规划](docs/data-scale-sharding-plan.md)
- [基础架构环境说明](docs/base-architecture-environment.md)

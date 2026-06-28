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

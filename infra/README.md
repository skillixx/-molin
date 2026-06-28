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

- 操作路径：GitHub 仓库 → Actions → 选择「部署测试环境」工作流 → Run workflow（分支选 main）。
- 对应工作流文件：`.github/workflows/deploy-test.yml`（`workflow_dispatch`）。

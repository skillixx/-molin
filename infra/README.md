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

# Infrastructure

This directory contains local development infrastructure.

## Services

- MySQL 8: business database.
- Redis 7: verification codes, permission cache, rate limits.
- RabbitMQ: async events for order payment, asset provisioning, and usage billing.
- MinIO: files, icons, identity attachments, and help-doc media.

## Start

```bash
docker compose -f infra/docker-compose.yml up -d
```

# Molin Cloud Platform

Molin Cloud Platform is a Vue3 + Go + MySQL management platform for product sales, billing, user assets, identity verification, applications, membership, and future GPU / Agent / Skills / Token gateway modules.

## Project Layout

```text
server
  Go API service.

web/admin-console
  Vue3 admin console.

web/user-console
  Vue3 user console.

web/shared
  Shared frontend packages.

infra
  Local development infrastructure.

docs
  Planning, interface, assignment, and architecture documents.

skills
  Project-specific Codex skills.
```

## Local Infrastructure

```bash
docker compose -f infra/docker-compose.yml up -d
```

## Run API

```bash
cd server
go run ./cmd/api
```

Health checks:

```text
GET /api/health
GET /api/ready
GET /api/version
```

# AGENTS.md

## Project Memory

This repository is for developing a cloud resource and application sales management platform similar in direction to a lightweight cloud console.

The platform must support:

- Email and phone registration.
- Email and phone login.
- Real-name identity verification after registration.
- Dynamic user roles and permissions.
- Unified product sales.
- Wallet, recharge, consumption, and financial transaction records.
- User assets and entitlement quotas.
- Membership-based product sales.
- Application marketplace.
- Future GPU bare-metal rental.
- Future Agent customization marketplace.
- Future Skills marketplace.
- Future Token upstream aggregation gateway.
- System announcements.
- Help document management.

## Current Tech Stack

- Frontend: Vue3 + Vite + TypeScript.
- Frontend UI: Element Plus.
- Backend: Go.
- Database: MySQL 8.
- Cache: Redis 7.
- Queue: RabbitMQ.
- Object storage: MinIO.
- Local environment: Docker Compose.

## Important Documents

- `README.md`: project overview and quick start.
- `docs/cloud-resource-app-marketplace-mvp.md`: product and MVP planning.
- `docs/development-execution-plan.md`: development execution plan.
- `docs/team-task-assignment.md`: team role and task assignment.
- `docs/interface-requirements-and-project-management.md`: interface requirements, database design, management, demo, and testing plan.
- `docs/base-architecture-environment.md`: base architecture environment and code file descriptions.
- `skills/README.md`: project-specific Codex skills.

## Repository Layout

```text
server
  Go API service.

web/admin-console
  Vue3 admin console.

web/user-console
  Vue3 user console.

web/shared
  Shared frontend code.

infra
  Local Docker Compose infrastructure.

docs
  Planning and project management documents.

skills
  Project-specific Codex skills.
```

## Backend Module Ownership

Backend 1 owns:

- `server/internal/modules/auth`
- `server/internal/modules/identity`
- `server/internal/modules/iam`
- `server/internal/modules/audit`

Responsibilities:

- Email registration.
- Phone registration.
- Email login.
- Phone login.
- Verification codes.
- JWT and refresh tokens.
- Real-name identity verification.
- Users, roles, permissions.
- Dynamic permission overrides.
- Permission cache invalidation.
- Audit logs.

Backend 2 owns:

- `server/internal/modules/product`
- `server/internal/modules/order`
- `server/internal/modules/billing`
- `server/internal/modules/finance_consumer`

Responsibilities:

- Unified products.
- Product plans.
- Product prices.
- Product role access.
- Membership product rules.
- Orders.
- Wallets.
- Wallet transactions.
- Recharge.
- Consumption.
- Usage-based billing.
- Idempotent payment and consumption events.

Backend 3 owns:

- `server/internal/modules/asset`
- `server/internal/modules/membership`
- `server/internal/modules/application_adapter`
- `server/internal/modules/app`
- `server/internal/modules/content`

Responsibilities:

- User assets.
- User entitlements.
- Asset events.
- Membership levels.
- Membership benefits.
- Application adapters.
- Application sales integration.
- System announcements.
- Help documents.

## Frontend Ownership

Frontend 1 owns:

- `web/admin-console`

Responsibilities:

- Admin login.
- Dashboard.
- User management.
- Role management.
- Permission management.
- Real-name verification review.
- Product management.
- Plan management.
- Price configuration.
- Order management.
- Wallet transactions.
- User assets.
- Membership management.
- Announcements.
- Help documents.

Frontend 2 owns:

- `web/user-console`

Responsibilities:

- Email registration.
- Phone registration.
- Email login.
- Phone login.
- Real-name verification page.
- Product marketplace.
- Product detail.
- Purchase confirmation.
- My assets.
- My entitlements.
- Wallet balance.
- Billing records.
- Membership center.
- Announcements.
- Help center.

## Development Priority

Do not start with GPU, Agent, Skills, or Token gateway.

First build this core flow:

```text
Register/login
  -> Real-name verification
  -> Role and permission control
  -> Product configuration
  -> Wallet recharge
  -> Product purchase
  -> Wallet deduction
  -> Order creation
  -> Wallet transaction creation
  -> User asset creation
  -> User can access purchased product
  -> Admin can query order, wallet transaction, and asset
```

Only after this flow works should the team expand to:

- GPU rental.
- Agent customization.
- Skills marketplace.
- Token aggregation gateway.

## Real-Name Verification Rule

After registration, users default to:

```text
real_name_status = unverified
```

Unverified users must not be allowed to:

- Purchase products.
- Rent GPU resources.
- Call Token services.
- Open user assets.

Identity information must be encrypted or masked. Do not store full ID card numbers in plaintext. Store hash and masked values only.

## Billing and Asset Rules

Wallet and billing are high-risk modules.

Required rules:

- Every wallet balance change must create a wallet transaction.
- Wallet deduction must use database transactions.
- Payment and usage billing must be idempotent.
- Orders must be traceable to wallet transactions.
- Paid products must generate user assets.
- Assets with quotas must generate user entitlements.
- Asset events must be recorded.

## Permission Rules

Permission checks must support:

- Role permissions.
- Dynamic user permission overrides.
- Product access rules.
- Membership rules.
- Real-name verification status.
- Asset status.

Suggested permission decision priority:

```text
explicit user deny
  -> explicit user allow
  -> role permissions
  -> product access rules
  -> membership rules
  -> asset and entitlement checks
```

Permission changes must invalidate permission cache.

## AI Development Rules

Use AI for:

- CRUD generation.
- Migration drafts.
- DTO generation.
- API client generation.
- Frontend forms.
- Frontend tables.
- Test case generation.
- Documentation.
- Security review.

Human review is required for:

- Wallet deduction.
- Order state transitions.
- Real-name verification privacy.
- Permission logic.
- User asset generation.
- Usage billing.
- Idempotency.
- Security-sensitive code.

## Project Skills

Project-specific skills live in `skills/`.

Relevant skills:

- `define-goal`
- `openai-docs`
- `playwright`
- `playwright-interactive`
- `screenshot`
- `security-best-practices`
- `security-threat-model`
- `security-ownership-map`
- `sentry`

## Current Base Environment

The current scaffold includes:

- Go API entrypoint.
- Basic `/api/health`, `/api/ready`, `/api/version` routes.
- Request ID middleware.
- Logger middleware.
- Recovery middleware.
- Response helper.
- Vue3 admin-console skeleton.
- Vue3 user-console skeleton.
- Docker Compose for MySQL, Redis, RabbitMQ, and MinIO.

The current execution environment did not have Go installed, so `gofmt` and `go run` were not executed during scaffold creation.

## Git Notes

Current remote:

```text
http://8.130.9.163:6888/aisiqing/molin.git
```

Use feature branches for implementation work. Do not commit directly to `main` except for planning/scaffold updates requested by the project owner.

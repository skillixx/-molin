# Membership 模块 — 后端 C 负责

## 职责边界

只负责：会员等级、会员权益配置、用户会员状态查询。

不负责：会员购买订单（product / order 模块）、会员资产生成（asset 模块，会员也是一种资产）。

## 需要创建的文件

```text
model/
  membership.go         -- membership_levels, membership_benefits, user_memberships

repository/
  level_repo.go
  benefit_repo.go
  user_membership_repo.go

service/
  membership_service.go     -- 查询用户当前有效会员、获取会员权益

handler/
  membership_handler.go     -- 用户端 + 管理端

dto/
  membership_dto.go

route.go
```

## 关键业务逻辑

```go
// 获取用户当前有效会员（用于 pricing_service 计算会员价）
func (s *MembershipService) GetActiveMembership(ctx context.Context, userID uint64) (*UserMembership, error) {
    return s.repo.FindActive(userID)
    // 查询条件：status = active AND (expires_at IS NULL OR expires_at > NOW())
}

// 检查某商品对当前用户的会员规则
func (s *MembershipService) GetProductRule(ctx context.Context, productID uint64, membershipLevelID uint64) (*ProductMembershipRule, error) {
    return s.ruleRepo.FindByProductAndLevel(productID, membershipLevelID)
}
```

## 接口清单

> 已按 `docs/backend-dev-plan-backend-c.md` 架构审查优化：
> - **移除 `POST /api/memberships/:id/purchase`**（C-OPT-2）：购买入口单一，会员作为 `product_type=membership` 商品走 product→order→provision→`CreateOrRenewMembership`。
> - **移除 `/api/admin/product-membership-rules`（×3）**（C-OPT-1）：会员价由 `product_prices`（会员档）唯一承载，不引入第二套定价来源。

```text
GET  /api/memberships
GET  /api/my/membership
GET  /api/admin/membership-levels
POST /api/admin/membership-levels
PATCH /api/admin/membership-levels/:id
GET  /api/admin/membership-benefits
POST /api/admin/membership-benefits
PATCH /api/admin/membership-benefits/:id
GET  /api/admin/user-memberships
```

> **续期（C-FIX-1）**：内部方法应为 `CreateOrRenewMembership(userID, levelID, assetID, duration)`——
> 同一 `(user_id, level_id)` 存在 active 记录则叠加延长 `expires_at`，否则新建；禁止重复 INSERT 产生多条 active。
> 另需 `ExpireMembershipsJob` 将过期 active 流转为 expired（C-FIX-5）。

## 依赖关系

- 被 `modules/product/service/pricing_service` 依赖 — 查询用户会员级别
- 被 `modules/provision/service` 依赖 — 开通会员后写 user_memberships
- 不依赖 billing 或 order（不处理支付，只提供查询）

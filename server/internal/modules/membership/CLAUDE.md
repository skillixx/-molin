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

```text
GET  /api/memberships
GET  /api/my/membership
POST /api/memberships/:id/purchase          -- 购买会员（转给 product 模块处理）
GET  /api/admin/membership-levels
POST /api/admin/membership-levels
PATCH /api/admin/membership-levels/:id
GET  /api/admin/membership-benefits
POST /api/admin/membership-benefits
PATCH /api/admin/membership-benefits/:id
GET  /api/admin/product-membership-rules
POST /api/admin/product-membership-rules
PATCH /api/admin/product-membership-rules/:id
GET  /api/admin/user-memberships
```

## 依赖关系

- 被 `modules/product/service/pricing_service` 依赖 — 查询用户会员级别
- 被 `modules/provision/service` 依赖 — 开通会员后写 user_memberships
- 不依赖 billing 或 order（不处理支付，只提供查询）

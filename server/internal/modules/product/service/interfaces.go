package service

import (
	"context"

	"github.com/shopspring/decimal"
)

// IAMService 定义 product/service 包所需的 IAM 接口。
type IAMService interface {
	GetUserRoleIDs(ctx context.Context, userID uint64) ([]uint64, error)
	CheckPermission(ctx context.Context, userID uint64, permCode string) bool
}

// ProvisionService 定义商品开通接口（Week 2 由 stub 实现）。
type ProvisionService interface {
	Provision(ctx context.Context, orderID, productID, planID, userID uint64) error
}

// MembershipInfo 活跃会员信息。
type MembershipInfo struct {
	LevelID uint64
}

// MembershipService 定义会员查询接口（Week 2 由 stub 实现）。
type MembershipService interface {
	GetActive(ctx context.Context, userID uint64) (*MembershipInfo, error)
}

// BillingService 定义扣费接口，由 billing.WalletService 实现。
type BillingService interface {
	Deduct(ctx context.Context, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error
}

// UserRepository 定义实名状态查询接口。
type UserRepository interface {
	GetRealNameStatus(ctx context.Context, userID uint64) (string, error)
}

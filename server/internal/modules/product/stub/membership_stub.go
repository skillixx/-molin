package stub

import (
	"context"

	"molin/server/internal/modules/product/service"
)

// MembershipStub 是 service.MembershipService 的空操作实现，用于 Week 2 阶段占位。
// 后端丙完成 membership 模块后，在 bootstrap/app.go 中替换为真实实现。
type MembershipStub struct{}

// NewMembershipStub 创建 stub 实例。
func NewMembershipStub() *MembershipStub {
	return &MembershipStub{}
}

// GetActive 始终返回 nil，表示用户无活跃会员等级，定价时跳过会员价格优先级。
func (s *MembershipStub) GetActive(ctx context.Context, userID uint64) (*service.MembershipInfo, error) {
	// TODO: Week 3/4 由后端丙接入 membership 模块实现真实会员查询
	return nil, nil
}

// HasActiveLevelIn 始终返回 false，表示用户无任何有效会员资格。
// 占位阶段：若商品配置了会员专属价，会员专属门槛校验将拦截所有用户购买（与"无会员"语义一致）。
func (s *MembershipStub) HasActiveLevelIn(ctx context.Context, userID uint64, levelIDs []uint64) (bool, error) {
	// TODO: Week 3/4 由后端丙接入 membership 模块实现真实会员资格校验
	return false, nil
}

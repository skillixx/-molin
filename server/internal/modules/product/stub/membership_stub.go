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

package stub

import "context"

// ProvisionStub 是 service.ProvisionService 的空操作实现，用于 Week 2 阶段占位。
// 后端丙完成 provision 模块后，在 bootstrap/app.go 中替换为真实实现。
type ProvisionStub struct{}

// NewProvisionStub 创建 stub 实例。
func NewProvisionStub() *ProvisionStub {
	return &ProvisionStub{}
}

// Provision 空操作：不执行实际开通流程。
func (s *ProvisionStub) Provision(ctx context.Context, orderID, productID, planID, userID uint64) error {
	// TODO: Week 3 由后端丙接入 provision 模块实现真实开通逻辑
	return nil
}

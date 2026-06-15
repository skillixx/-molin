package service

import "context"

// provisionStub 是 ProvisionService 的空操作实现，用于 provision 模块未注入时降级。
// 与 product 模块的 stub 语义一致：支付成功后不执行实际开通，仅占位避免 nil 调用。
type provisionStub struct{}

// NewProvisionStub 创建空操作开通 stub。
func NewProvisionStub() ProvisionService {
	return &provisionStub{}
}

// Provision 空操作：不执行实际开通流程。
func (s *provisionStub) Provision(ctx context.Context, orderID, productID, planID, userID uint64) error {
	return nil
}

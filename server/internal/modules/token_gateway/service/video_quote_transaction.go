package service

import (
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
)

// 同时绑定报价、价格和可信输入读取，不能持有外层锁后再从主池借第二连接。
func (s *VideoQuoteService) withTransaction(tx *gorm.DB) *VideoQuoteService {
	copy := *s
	copy.store = repository.NewVideoQuoteRepository(tx)
	if s.pricing != nil {
		if _, ok := s.pricing.repo.(*repository.G3PricingRepository); ok {
			pricing := *s.pricing
			pricing.repo = repository.NewG3PricingRepository(tx)
			copy.pricing = &pricing
		}
	}
	if inputs, ok := s.inputs.(*GORMVideoInputSnapshotResolver); ok {
		bound := *inputs
		bound.db = tx
		copy.inputs = &bound
	}
	return &copy
}

package service

import "molin/server/internal/modules/token_gateway/model"

// 解析器下沉到领域模型，编辑、发布、回滚和运行时必须遵守同一份七键合同。
type VideoModelContract = model.VideoModelContract

// 保留原服务错误分类，避免共享解析器改变既有HTTP准入的失败语义。
func ParseVideoModelContract(raw []byte, productID *uint64) (VideoModelContract, error) {
	result, err := model.ParseVideoModelContract(raw, productID)
	if err != nil {
		return result, ErrVideoAccessUnavailable
	}
	return result, nil
}

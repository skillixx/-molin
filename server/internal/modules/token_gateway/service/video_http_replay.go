package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 只为安全终态、已释放租约且引用原InputAsset的生成重放读取冻结快照。
// 此处不是重放授权：随后G5仍核对完整生成指纹、当前权限、权利及原声明；绝不创建或重绑输入。
func (s *VideoHTTPService) frozenVideoReplayInput(ctx context.Context, c VideoCommand, owner repository.VideoOwner) (*VideoQuoteInputBinding, bool, error) {
	var request model.VideoBillingRequest
	err := s.db.WithContext(ctx).Where("user_id=? AND project_id=? AND command_kind='create_video' AND intent_key_hash=?", owner.UserID, owner.ProjectID, videoBillingDigest("create_video\x00"+c.IdempotencyKey)).Take(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrVideoAccessUnavailable
	}
	if !sameVideoRightsKey(request.APIKeyID, owner.APIKeyID) {
		return nil, false, ErrVideoBillingAccess
	}
	if request.Operation == nil || *request.Operation != model.AIVideoOperationImageToVideo {
		return nil, false, ErrVideoBillingConflict
	}
	var identity struct{ PublicID string }
	if err := s.db.WithContext(ctx).Table("ai_gateway_tasks").Select("public_id").Where("request_id=? AND user_id=? AND project_id=? AND capability=?", request.RequestID, owner.UserID, owner.ProjectID, model.AIVideoCapability).Take(&identity).Error; err != nil {
		return nil, false, ErrVideoBillingState
	}
	task, err := repository.NewVideoTaskRepository(s.db).FindForOwner(ctx, identity.PublicID, owner)
	if err != nil {
		return nil, false, err
	}
	safe := task.Status == model.AIImageTaskSucceeded && task.BillingStatus == model.AIBillingSettled
	safe = safe || ((task.Status == model.AIImageTaskFailed || task.Status == model.AIImageTaskCancelled || task.Status == model.AIImageTaskExpired) && task.BillingStatus == model.AIBillingReleased)
	if !safe {
		return nil, false, nil
	}
	bindings, err := repository.NewVideoTaskInputRepository(s.db).ListForOwner(ctx, task.PublicID, owner)
	if err != nil {
		return nil, false, err
	}
	if len(bindings) != 1 || bindings[0].Role != model.AITaskInputReferenceImage || bindings[0].Ordinal != 0 {
		return nil, false, ErrVideoBillingState
	}
	if bindings[0].LeaseReleasedAt == nil {
		return nil, false, nil
	}
	var original struct {
		ID       uint64
		PublicID string
	}
	if err := s.db.WithContext(ctx).Table("ai_gateway_input_assets").Select("id,public_id").Where("id=? AND user_id=? AND project_id=?", bindings[0].InputAssetID, owner.UserID, owner.ProjectID).Take(&original).Error; err != nil {
		return nil, false, videoAccessReadError(err, repository.ErrVideoInputNotFound)
	}
	if original.PublicID != c.InputAssetID {
		return nil, false, nil
	}
	return &VideoQuoteInputBinding{InternalID: original.ID, InputAssetID: original.PublicID, NormalizedSHA256: bindings[0].NormalizedSHA256, Version: bindings[0].InputVersion}, true, nil
}

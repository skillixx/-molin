package repository

import (
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

// VerifyVideoNeverSubmittedTx 统一取消和网关零成本的证明边界；调用者必须已锁定Task/Request。
// 当前字段为空并不足以证明未提交，执行尝试、产物、回调和不可变状态事件都必须没有执行证据。
func VerifyVideoNeverSubmittedTx(tx *gorm.DB, task *VideoTaskRecord) error {
	if tx == nil || task == nil || task.ID == 0 || task.RequestID == "" || task.Capability != model.AIVideoCapability || (task.Status != model.AIImageTaskReserved && task.Status != model.AIImageTaskQueued && task.Status != model.AIImageTaskCancelled) || task.AttemptCount != 0 || task.ProviderCode != nil || task.ProviderTaskID != nil || task.BifrostProvider != nil || task.BifrostTaskID != nil || task.BifrostCompoundID != nil {
		return ErrVideoUsageInvalid
	}
	blocked := []string{model.AIImageTaskSubmitting, model.AIImageTaskSubmitted, model.AIImageTaskProcessing, model.AIImageTaskFetching, model.AIImageTaskStoring, model.AIImageTaskModerating, model.AIImageTaskLabeling, model.AIImageTaskSucceeded, model.AIImageTaskPendingReconcile}
	queries := []*gorm.DB{
		tx.Model(&model.AIExecutionAttempt{}).Where("request_id=?", task.RequestID),
		tx.Model(&model.AIImageAsset{}).Where("request_id=?", task.RequestID),
		tx.Model(&model.AIGatewayProviderCallbackEvent{}).Where("task_id=?", task.ID),
		tx.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND (from_status IN ? OR to_status IN ?)", task.ID, blocked, blocked),
	}
	for _, query := range queries {
		var n int64
		if err := query.Count(&n).Error; err != nil {
			return err
		}
		if n != 0 {
			return ErrVideoUsageInvalid
		}
	}
	return nil
}

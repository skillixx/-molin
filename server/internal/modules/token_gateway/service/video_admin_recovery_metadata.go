package service

import (
	"context"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 管理查询原已提交任务只使用持久化身份，不恢复生成资格，不读取Prompt、输入图片或媒体正文。
func (l *VideoRepositoryTaskLedger) loadRecoveryMetadata(ctx context.Context, id string) (video.GatewayTask, error) {
	if !l.deferDelivery || id != l.recoveryTaskID {
		return video.GatewayTask{}, repository.ErrVideoTaskNotFound
	}
	r, err := l.taskRepo.FindForOwner(ctx, id, l.owner)
	if err != nil {
		return video.GatewayTask{}, err
	}
	if r.Operation == nil || (*r.Operation != model.AIVideoOperationTextToVideo && *r.Operation != model.AIVideoOperationImageToVideo) || r.ProviderCode == nil || r.ProviderTaskID == nil || *r.ProviderCode != "fake-native-async" || !videoBillingPublicID.MatchString(*r.ProviderTaskID) || r.AttemptCount != 1 {
		return video.GatewayTask{}, ErrVideoReconciliation
	}
	switch r.Status {
	case "submitted", "processing", "fetching", "storing", "moderating", "labeling", "succeeded", "failed", "cancelled", "expired", "pending_reconcile":
	default:
		return video.GatewayTask{}, ErrVideoAdminCommandConflict
	}
	spec, err := parseVideoG4TaskSpec(r.InputJSON)
	if err != nil {
		return video.GatewayTask{}, err
	}
	t := video.GatewayTask{DeferDelivery: true, TaskID: r.PublicID, RequestID: r.RequestID, Operation: *r.Operation, Spec: spec, Status: video.TaskStatus(r.Status), Version: r.VersionNo, CancelRequestedAt: r.CancelRequestedAt, ProviderCode: *r.ProviderCode, ProviderTaskID: *r.ProviderTaskID}
	return t, nil
}

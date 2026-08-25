package repository

import (
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

func TestImageTaskTransitionContract(t *testing.T) {
	allowed := [][2]string{
		{model.AIImageTaskCreated, model.AIImageTaskReserved},
		{model.AIImageTaskReserved, model.AIImageTaskSubmitted},
		{model.AIImageTaskSubmitted, model.AIImageTaskProcessing},
		{model.AIImageTaskProcessing, model.AIImageTaskStoring},
		{model.AIImageTaskStoring, model.AIImageTaskModerating},
		{model.AIImageTaskModerating, model.AIImageTaskSucceeded},
		{model.AIImageTaskSubmitted, model.AIImageTaskPendingReconcile},
		{model.AIImageTaskPendingReconcile, model.AIImageTaskStoring},
	}
	for _, transition := range allowed {
		if !imageTaskTransitionAllowed(transition[0], transition[1]) {
			t.Fatalf("合法任务流转被拒绝: %s -> %s", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]string{
		{model.AIImageTaskCreated, model.AIImageTaskSucceeded},
		{model.AIImageTaskSucceeded, model.AIImageTaskProcessing},
		{model.AIImageTaskFailed, model.AIImageTaskReserved},
	} {
		if imageTaskTransitionAllowed(transition[0], transition[1]) {
			t.Fatalf("非法任务流转被允许: %s -> %s", transition[0], transition[1])
		}
	}
}

func TestImageAssetTransitionAndDestructiveContract(t *testing.T) {
	for _, transition := range [][2]string{
		{model.AIImageAssetTemporary, model.AIImageAssetAvailable},
		{model.AIImageAssetAvailable, model.AIImageAssetQuarantined},
		{model.AIImageAssetAvailable, model.AIImageAssetExpiring},
		{model.AIImageAssetExpiring, model.AIImageAssetDeleting},
		{model.AIImageAssetDeleting, model.AIImageAssetDeleted},
		{model.AIImageAssetDeleting, model.AIImageAssetDeleteFailed},
		{model.AIImageAssetDeleteFailed, model.AIImageAssetDeleting},
	} {
		if !imageAssetTransitionAllowed(transition[0], transition[1]) {
			t.Fatalf("合法资产流转被拒绝: %s -> %s", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]string{
		{model.AIImageAssetTemporary, model.AIImageAssetDeleted},
		{model.AIImageAssetAvailable, model.AIImageAssetDeleted},
		{model.AIImageAssetDeleted, model.AIImageAssetAvailable},
	} {
		if imageAssetTransitionAllowed(transition[0], transition[1]) {
			t.Fatalf("非法资产流转被允许: %s -> %s", transition[0], transition[1])
		}
	}
	for _, state := range []string{model.AIImageAssetExpiring, model.AIImageAssetDeleting, model.AIImageAssetDeleted} {
		if !imageAssetDestructiveState(state) {
			t.Fatalf("清理状态必须受legal hold阻断: %s", state)
		}
	}
	if imageAssetDestructiveState(model.AIImageAssetQuarantined) {
		t.Fatal("隔离不是删除动作，legal hold不能阻止安全隔离")
	}
}

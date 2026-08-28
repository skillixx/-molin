package repository

import (
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

// TestVideoExecutionTransitionMatrix 逐格验证执行状态矩阵，防止新增状态时意外开放回退或终态覆盖。
func TestVideoExecutionTransitionMatrix(t *testing.T) {
	states := []string{
		model.AIImageTaskCreated, model.AIImageTaskReserved, model.AIImageTaskQueued,
		model.AIImageTaskSubmitting, model.AIImageTaskSubmitted, model.AIImageTaskProcessing,
		model.AIImageTaskFetching, model.AIImageTaskStoring, model.AIImageTaskModerating,
		model.AIImageTaskLabeling, model.AIImageTaskSucceeded, model.AIImageTaskFailed,
		model.AIImageTaskCancelled, model.AIImageTaskExpired, model.AIImageTaskPendingReconcile,
	}
	allowed := map[[2]string]bool{
		{model.AIImageTaskCreated, model.AIImageTaskReserved}:           true,
		{model.AIImageTaskReserved, model.AIImageTaskQueued}:            true,
		{model.AIImageTaskQueued, model.AIImageTaskSubmitting}:          true,
		{model.AIImageTaskSubmitting, model.AIImageTaskSubmitted}:       true,
		{model.AIImageTaskSubmitted, model.AIImageTaskProcessing}:       true,
		{model.AIImageTaskProcessing, model.AIImageTaskFetching}:        true,
		{model.AIImageTaskFetching, model.AIImageTaskStoring}:           true,
		{model.AIImageTaskStoring, model.AIImageTaskModerating}:         true,
		{model.AIImageTaskModerating, model.AIImageTaskLabeling}:        true,
		{model.AIImageTaskLabeling, model.AIImageTaskSucceeded}:         true,
		{model.AIImageTaskPendingReconcile, model.AIImageTaskSucceeded}: true,
		{model.AIImageTaskPendingReconcile, model.AIImageTaskFailed}:    true,
		{model.AIImageTaskPendingReconcile, model.AIImageTaskCancelled}: true,
		{model.AIImageTaskPendingReconcile, model.AIImageTaskExpired}:   true,
	}
	for _, from := range states[:10] {
		for _, terminal := range []string{model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired} {
			allowed[[2]string{from, terminal}] = true
		}
		if from != model.AIImageTaskCreated && from != model.AIImageTaskReserved && from != model.AIImageTaskQueued {
			allowed[[2]string{from, model.AIImageTaskPendingReconcile}] = true
		}
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := videoExecutionTransitionAllowed(from, to), allowed[[2]string{from, to}]; got != want {
				t.Fatalf("执行状态矩阵不一致: %s -> %s got=%t want=%t", from, to, got, want)
			}
		}
	}
}

// TestVideoBillingTransitionMatrix 验证计费状态只沿冻结链路前进，settled与released不能互相覆盖。
func TestVideoBillingTransitionMatrix(t *testing.T) {
	states := []string{
		model.AIBillingUnquoted, model.AIBillingQuoted, model.AIBillingHeld,
		model.AIBillingSettlementPending, model.AIBillingSettled, model.AIBillingReleased,
		model.AIBillingAdjusted,
	}
	allowed := map[[2]string]bool{
		{model.AIBillingUnquoted, model.AIBillingQuoted}:            true,
		{model.AIBillingQuoted, model.AIBillingHeld}:                true,
		{model.AIBillingHeld, model.AIBillingSettlementPending}:     true,
		{model.AIBillingSettlementPending, model.AIBillingSettled}:  true,
		{model.AIBillingSettlementPending, model.AIBillingReleased}: true,
		{model.AIBillingSettled, model.AIBillingAdjusted}:           true,
		{model.AIBillingReleased, model.AIBillingAdjusted}:          true,
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := videoBillingTransitionAllowed(from, to), allowed[[2]string{from, to}]; got != want {
				t.Fatalf("计费状态矩阵不一致: %s -> %s got=%t want=%t", from, to, got, want)
			}
		}
	}
}

// TestVideoDeliveryTransitionMatrix 验证交付状态只有一次终态选择，禁止相反终态覆盖。
func TestVideoDeliveryTransitionMatrix(t *testing.T) {
	states := []string{model.AIDeliveryPending, model.AIDeliveryAvailable, model.AIDeliveryRejected, model.AIDeliveryExpired}
	for _, from := range states {
		for _, to := range states {
			want := from == model.AIDeliveryPending && to != model.AIDeliveryPending
			if got := videoDeliveryTransitionAllowed(from, to); got != want {
				t.Fatalf("交付状态矩阵不一致: %s -> %s got=%t want=%t", from, to, got, want)
			}
		}
	}
}

package repository

import "molin/server/internal/modules/token_gateway/model"

// videoExecutionTransitionAllowed 固化视频执行轴的单向迁移；pending_reconcile只能进入安全终态，
// 不能回到某个无法证明先后关系的执行中状态，从而避免乱序回调制造隐式状态回退。
func videoExecutionTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		model.AIImageTaskCreated: {
			model.AIImageTaskReserved: true, model.AIImageTaskFailed: true,
			model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true,
		},
		model.AIImageTaskReserved: {
			model.AIImageTaskQueued: true, model.AIImageTaskFailed: true,
			model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true,
		},
		model.AIImageTaskQueued: {
			model.AIImageTaskSubmitting: true, model.AIImageTaskFailed: true,
			model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true,
		},
		model.AIImageTaskSubmitting: videoExecutionForwardTargets(model.AIImageTaskSubmitted),
		model.AIImageTaskSubmitted:  videoExecutionForwardTargets(model.AIImageTaskProcessing),
		model.AIImageTaskProcessing: videoExecutionForwardTargets(model.AIImageTaskFetching),
		model.AIImageTaskFetching:   videoExecutionForwardTargets(model.AIImageTaskStoring),
		model.AIImageTaskStoring:    videoExecutionForwardTargets(model.AIImageTaskModerating),
		model.AIImageTaskModerating: videoExecutionForwardTargets(model.AIImageTaskLabeling),
		model.AIImageTaskLabeling:   videoExecutionForwardTargets(model.AIImageTaskSucceeded),
		model.AIImageTaskPendingReconcile: {
			model.AIImageTaskSucceeded: true, model.AIImageTaskFailed: true,
			model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true,
		},
	}
	return allowed[from][to]
}

func videoExecutionForwardTargets(next string) map[string]bool {
	return map[string]bool{
		next: true, model.AIImageTaskPendingReconcile: true, model.AIImageTaskFailed: true,
		model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true,
	}
}

// videoBillingTransitionAllowed 让计费轴独立于执行与交付轴，只允许目标定义的冻结链路。
func videoBillingTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		model.AIBillingUnquoted:          {model.AIBillingQuoted: true},
		model.AIBillingQuoted:            {model.AIBillingHeld: true},
		model.AIBillingHeld:              {model.AIBillingSettlementPending: true},
		model.AIBillingSettlementPending: {model.AIBillingSettled: true, model.AIBillingReleased: true},
		model.AIBillingSettled:           {model.AIBillingAdjusted: true},
		model.AIBillingReleased:          {model.AIBillingAdjusted: true},
	}
	return allowed[from][to]
}

// videoDeliveryTransitionAllowed 保证交付轴从pending一次性选择终态，终态之间不能相互覆盖。
func videoDeliveryTransitionAllowed(from, to string) bool {
	if from != model.AIDeliveryPending {
		return false
	}
	return to == model.AIDeliveryAvailable || to == model.AIDeliveryRejected || to == model.AIDeliveryExpired
}

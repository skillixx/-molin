package video

import "context"

// recordProviderResult 统一Poll/Cancel的财务确认门禁，避免一条入口绕过另一条的零成本和产物冲突检查。
func (g *VideoGateway) recordProviderResult(ctx context.Context, task GatewayTask, result QueryResult) error {
	if !task.DeferDelivery {
		return nil
	}
	if result.ProviderTaskID != task.ProviderTaskID {
		return ErrCallbackTaskMismatch
	}
	switch result.Status {
	case ProviderTaskQueued, ProviderTaskProcessing:
		if result.Confirmation != nil {
			return ErrProviderResultUnknown
		}
		return nil
	case ProviderTaskSucceeded, ProviderTaskFailed, ProviderTaskCancelled:
	default:
		return ErrProviderResultUnknown
	}
	c := result.Confirmation
	sink, ok := g.deps.Ledger.(VideoProviderCostSink)
	if !ok || c == nil || c.Outcome != result.Status || c.ProviderTaskID != task.ProviderTaskID || c.ProviderCode != task.ProviderCode || c.Operation != task.Operation {
		return ErrProviderResultUnknown
	}
	if result.Status == ProviderTaskCancelled || result.Status == ProviderTaskFailed {
		if !c.Quantity.IsZero() || !c.Amount.IsZero() || result.Content != nil {
			// 合法成本事实仍须保存，但带产物或非零成本不能生成本夹具合同的无产物证明。
			conflicts, ok := g.deps.Ledger.(VideoProviderConflictSink)
			if !ok {
				return ErrProviderResultUnknown
			}
			if err := conflicts.RecordProviderResultConflict(ctx, task.TaskID, *c); err != nil {
				return err
			}
			return ErrProviderResultUnknown
		}
		proof, ok := g.deps.Ledger.(VideoProviderNoProductSink)
		if !ok {
			return ErrProviderResultUnknown
		}
		return proof.RecordNoProductOutcome(ctx, task.TaskID, *c)
	}
	return sink.RecordProviderConfirmation(ctx, task.TaskID, *c)
}

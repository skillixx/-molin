package service

import (
	"testing"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

func TestApplyImageProviderTaskEvidence(t *testing.T) {
	updates := map[string]interface{}{}
	applyImageProviderTaskEvidence(updates, imagegateway.GatewayResult{
		ProviderCode: "openrouter-images", ProviderRequestID: "or-safe-id", ProviderAttempted: true,
	})
	if updates["provider_code"] != "openrouter-images" || updates["provider_task_id"] != "or-safe-id" || updates["attempt_count"] != 1 {
		t.Fatalf("任务必须保留一次低敏Provider尝试证据: %+v", updates)
	}

	closedUpdates := map[string]interface{}{}
	applyImageProviderTaskEvidence(closedUpdates, imagegateway.GatewayResult{})
	if len(closedUpdates) != 0 {
		t.Fatalf("未调用Provider时不得伪造尝试证据: %+v", closedUpdates)
	}
}

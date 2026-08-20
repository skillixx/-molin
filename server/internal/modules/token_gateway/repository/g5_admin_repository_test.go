package repository

import (
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

func TestValidateModelPublishMetadataRejectsMissingDocuments(t *testing.T) {
	channelID := uint64(1)
	upstream := "openrouter/qwen/qwen3.8-max"
	item := model.TokenModel{ChannelID: &channelID, UpstreamModel: &upstream, DocsURLHealthStatus: "unpublished", QuickStartURLHealthStatus: "unpublished"}
	if err := validateModelPublishMetadata(item); err != ErrModelDocumentsNotReady {
		t.Fatalf("缺少发布文档必须返回固定分类，实际 err=%v", err)
	}
	docs, quick := "https://example.invalid/docs", "https://example.invalid/quick"
	item.DocsURL, item.QuickStartURL = &docs, &quick
	item.DocsURLHealthStatus, item.QuickStartURLHealthStatus = "healthy", "healthy"
	if err := validateModelPublishMetadata(item); err != nil {
		t.Fatalf("完整且健康的模型材料应通过，实际 err=%v", err)
	}
}

func TestChooseWeightedPrimaryRouteHonorsFallbackAndPriority(t *testing.T) {
	candidates := []model.AIModelRoute{
		{ID: 1, FallbackOrder: 0, Priority: 100, Weight: 10},
		{ID: 2, FallbackOrder: 0, Priority: 90, Weight: 1000},
		{ID: 3, FallbackOrder: 1, Priority: 200, Weight: 1000},
	}
	for i := 0; i < 100; i++ {
		selected := chooseWeightedPrimaryRoute(candidates, string(rune(i)))
		if selected == nil || selected.ID != 1 {
			t.Fatalf("低优先级或回退路由不得进入主选择: %+v", selected)
		}
	}
}

func TestChooseWeightedPrimaryRouteIsStableAndUsesPeers(t *testing.T) {
	candidates := []model.AIModelRoute{{ID: 1, FallbackOrder: 0, Priority: 100, Weight: 1}, {ID: 2, FallbackOrder: 0, Priority: 100, Weight: 3}}
	first := chooseWeightedPrimaryRoute(candidates, "req-stable")
	second := chooseWeightedPrimaryRoute(candidates, "req-stable")
	if first == nil || second == nil || first.ID != second.ID {
		t.Fatalf("同一请求路由必须稳定: first=%+v second=%+v", first, second)
	}
	seen := map[uint64]bool{}
	for i := 0; i < 100; i++ {
		selected := chooseWeightedPrimaryRoute(candidates, string(rune(i)))
		seen[selected.ID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("同级权重路由应都能被选中: %+v", seen)
	}
}

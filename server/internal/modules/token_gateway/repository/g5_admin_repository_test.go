package repository

import (
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

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

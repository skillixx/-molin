package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/dto"
)

// fakeVisibleLister 忠实模拟 CatalogService.ListVisible 的 modality 过滤契约：
// modality 非空时只返回该模态的模型，空则返回全部。用于验证 /v1/models 固定传 "chat"。
type fakeVisibleLister struct {
	all         []dto.ModelResp
	gotModality string // 记录 handler 实际传入的 modality
	gotUserID   uint64
}

func (f *fakeVisibleLister) ListVisible(_ context.Context, userID uint64, modality string, _, _ int) ([]dto.ModelResp, int64, error) {
	f.gotModality = modality
	f.gotUserID = userID
	out := make([]dto.ModelResp, 0, len(f.all))
	for _, m := range f.all {
		if modality == "" || m.Modality == modality {
			out = append(out, m)
		}
	}
	return out, int64(len(out)), nil
}

// TestFetchOpenAIChatModels_OnlyChat 验证 /v1/models 固定按 modality="chat" 过滤，
// 非 chat（image/audio/video）模型不出现在输出里。
func TestFetchOpenAIChatModels_OnlyChat(t *testing.T) {
	lister := &fakeVisibleLister{
		all: []dto.ModelResp{
			{LogicalModelCode: "gpt-4o", Modality: "chat"},
			{LogicalModelCode: "dall-e-3", Modality: "image"},
			{LogicalModelCode: "claude-3-5-sonnet", Modality: "chat"},
			{LogicalModelCode: "whisper-1", Modality: "audio"},
			{LogicalModelCode: "sora", Modality: "video"},
		},
	}

	got, err := fetchOpenAIChatModels(context.Background(), lister, 42)
	if err != nil {
		t.Fatalf("未预期错误：%v", err)
	}

	// 确认 handler 确实以 "chat" 调用底层可见性查询。
	if lister.gotModality != "chat" {
		t.Fatalf("ListVisible modality 期望 chat，实际 %q", lister.gotModality)
	}
	if lister.gotUserID != 42 {
		t.Fatalf("ListVisible userID 期望 42，实际 %d", lister.gotUserID)
	}

	// 输出只应包含 chat 模型。
	wantIDs := map[string]bool{"gpt-4o": true, "claude-3-5-sonnet": true}
	if len(got.Data) != len(wantIDs) {
		t.Fatalf("输出模型数期望 %d，实际 %d", len(wantIDs), len(got.Data))
	}
	for _, m := range got.Data {
		if !wantIDs[m.ID] {
			t.Errorf("非 chat 模型 %q 不应出现在 /v1/models 输出里", m.ID)
		}
	}
	// 显式确认非 chat 的几个 code 一个都没漏进来。
	for _, banned := range []string{"dall-e-3", "whisper-1", "sora"} {
		for _, m := range got.Data {
			if m.ID == banned {
				t.Errorf("非 chat 模型 %q 出现在输出里", banned)
			}
		}
	}
}

// TestBuildOpenAIModelList 验证 GET /v1/models 的 OpenAI 标准格式转换：
// 顶层 object=list，每项 id=logical_model_code、object=model、created=Unix 秒、owned_by=molin。
func TestBuildOpenAIModelList(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 2, 9, 30, 0, 0, time.UTC)
	items := []dto.ModelResp{
		{LogicalModelCode: "gpt-4o", DisplayName: "GPT-4o", Modality: "chat", CreatedAt: t0},
		{LogicalModelCode: "claude-3-5-sonnet", DisplayName: "Claude 3.5 Sonnet", Modality: "chat", CreatedAt: t1},
	}

	got := buildOpenAIModelList(items)

	if got.Object != "list" {
		t.Fatalf("顶层 object 期望 list，实际 %q", got.Object)
	}
	if len(got.Data) != 2 {
		t.Fatalf("data 长度期望 2，实际 %d", len(got.Data))
	}
	if got.Data[0].ID != "gpt-4o" {
		t.Errorf("data[0].id 期望 gpt-4o，实际 %q", got.Data[0].ID)
	}
	if got.Data[0].Object != "model" {
		t.Errorf("data[0].object 期望 model，实际 %q", got.Data[0].Object)
	}
	if got.Data[0].Created != t0.Unix() {
		t.Errorf("data[0].created 期望 %d，实际 %d", t0.Unix(), got.Data[0].Created)
	}
	if got.Data[0].OwnedBy != "molin" {
		t.Errorf("data[0].owned_by 期望 molin，实际 %q", got.Data[0].OwnedBy)
	}
	if got.Data[1].ID != "claude-3-5-sonnet" {
		t.Errorf("data[1].id 期望 claude-3-5-sonnet，实际 %q", got.Data[1].ID)
	}
}

// TestBuildOpenAIModelList_Empty 无可见模型时返回空数组而非 null（OpenAI 客户端要求 data 为数组）。
func TestBuildOpenAIModelList_Empty(t *testing.T) {
	got := buildOpenAIModelList(nil)
	if got.Object != "list" {
		t.Fatalf("顶层 object 期望 list，实际 %q", got.Object)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	const want = `{"object":"list","data":[]}`
	if string(b) != want {
		t.Errorf("空列表序列化期望 %s，实际 %s", want, string(b))
	}
}

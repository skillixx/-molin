package handler

import (
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/dto"
)

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

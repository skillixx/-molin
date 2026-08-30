package video

import (
	"context"
	"testing"
)

// 明确失败和取消的零成本来自Fake确认，未知结果不能冒充已确认零成本。
func TestFakeVideoFinalCostConfirmation(t *testing.T) {
	for _, mode := range []FakeVideoMode{FakeVideoExplicitFailure, FakeVideoProviderCancelled, FakeVideoResultUnknown} {
		t.Run(string(mode), func(t *testing.T) {
			a := NewFakeAsyncVideoAdapter(mode)
			s, err := a.Submit(context.Background(), SubmitRequest{RequestID: "fixture-confirmation", Operation: OperationTextToVideo, Prompt: "非商业测试", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = a.Query(context.Background(), QueryRequest{ProviderTaskID: s.ProviderTaskID})
			q, _ := a.Query(context.Background(), QueryRequest{ProviderTaskID: s.ProviderTaskID})
			if mode == FakeVideoResultUnknown {
				if q.Confirmation != nil {
					t.Fatal("未知结果不得伪造成本确认")
				}
				return
			}
			if q.Confirmation == nil || q.Confirmation.Outcome != q.Status || !q.Confirmation.Amount.IsZero() || !q.Confirmation.Quantity.IsZero() || q.Content != nil {
				t.Fatal("无产物终态缺少明确零成本确认")
			}
			c, err := a.Cancel(context.Background(), CancelRequest{ProviderTaskID: s.ProviderTaskID})
			if err != nil || c.Confirmation == nil || c.Confirmation.Outcome != q.Status {
				t.Fatal("终态重放应保留原成本确认")
			}
		})
	}
}

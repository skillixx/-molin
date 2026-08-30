package video

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFakeAsyncVideoAdapterSupportsTextAndImageTasks(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     *ControlledInputRef
	}{
		{name: "text_to_video", operation: OperationTextToVideo},
		{name: "image_to_video", operation: OperationImageToVideo, input: &ControlledInputRef{AssetID: "vin_ready_1", SHA256: strings.Repeat("a", 64), Version: 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewFakeAsyncVideoAdapter(FakeVideoSuccess)
			result, err := adapter.Submit(context.Background(), SubmitRequest{
				RequestID: "vid_req_stable_1", Operation: test.operation, Prompt: "仅存在于内存的测试提示词",
				Input: test.input, Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24, Audio: false},
			})
			if err != nil {
				t.Fatalf("提交Fake视频任务失败: %v", err)
			}
			if !strings.HasPrefix(result.ProviderTaskID, "taskUUID-") || result.RequestID != "vid_req_stable_1" {
				t.Fatalf("异步任务标识不符合合同: %+v", result)
			}
			query, err := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: result.ProviderTaskID})
			if err != nil || query.Status != ProviderTaskProcessing {
				t.Fatalf("首次查询应为处理中: result=%+v err=%v", query, err)
			}
			query, err = adapter.Query(context.Background(), QueryRequest{ProviderTaskID: result.ProviderTaskID})
			if err != nil || query.Status != ProviderTaskSucceeded || query.Content == nil {
				t.Fatalf("第二次查询应成功并返回受控内容引用: result=%+v err=%v", query, err)
			}
		})
	}
}

func TestFakeAsyncVideoAdapterRejectsInvalidInputCardinality(t *testing.T) {
	adapter := NewFakeAsyncVideoAdapter(FakeVideoSuccess)
	validInput := &ControlledInputRef{AssetID: "vin_ready_1", SHA256: strings.Repeat("b", 64), Version: 1}
	for _, request := range []SubmitRequest{
		{RequestID: "vid_req_t2v_has_image", Operation: OperationTextToVideo, Prompt: "测试", Input: validInput, Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}},
		{RequestID: "vid_req_i2v_missing_image", Operation: OperationImageToVideo, Prompt: "测试", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}},
		{RequestID: "vid_req_i2v_external", Operation: OperationImageToVideo, Prompt: "测试", Input: &ControlledInputRef{AssetID: "https://example.com/a.png", SHA256: strings.Repeat("c", 64), Version: 1}, Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}},
	} {
		if _, err := adapter.Submit(context.Background(), request); !errors.Is(err, ErrVideoRequestInvalid) {
			t.Fatalf("非法输入必须失败关闭: request=%+v err=%v", request, err)
		}
	}
	if adapter.SubmitCalls() != 0 {
		t.Fatalf("校验失败不得进入Provider提交，实际调用=%d", adapter.SubmitCalls())
	}
}

func TestFakeAsyncVideoAdapterAckLossNeverResubmits(t *testing.T) {
	adapter := NewFakeAsyncVideoAdapter(FakeVideoAckLostKnownTask)
	request := SubmitRequest{RequestID: "vid_req_ack_lost", Operation: OperationTextToVideo, Prompt: "测试", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}}
	result, err := adapter.Submit(context.Background(), request)
	if !errors.Is(err, ErrSubmitAcknowledgementLost) || result.ProviderTaskID == "" {
		t.Fatalf("ACK丢失必须返回可查询任务ID: result=%+v err=%v", result, err)
	}
	second, secondErr := adapter.Submit(context.Background(), request)
	if !errors.Is(secondErr, ErrDuplicateSubmitForbidden) || second.ProviderTaskID != result.ProviderTaskID {
		t.Fatalf("相同request_id不得重新提交: first=%+v second=%+v err=%v", result, second, secondErr)
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatalf("ACK丢失只能产生一次Provider提交，实际=%d", adapter.SubmitCalls())
	}
	query, queryErr := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: result.ProviderTaskID})
	if queryErr != nil || query.Status != ProviderTaskProcessing {
		t.Fatalf("已知Provider任务ID必须使用Query恢复: result=%+v err=%v", query, queryErr)
	}
}

func TestFakeAsyncVideoAdapterCancelIsIdempotent(t *testing.T) {
	adapter := NewFakeAsyncVideoAdapter(FakeVideoSuccess)
	result, err := adapter.Submit(context.Background(), SubmitRequest{RequestID: "vid_req_cancel", Operation: OperationTextToVideo, Prompt: "测试", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		cancelled, cancelErr := adapter.Cancel(context.Background(), CancelRequest{ProviderTaskID: result.ProviderTaskID})
		if cancelErr != nil || cancelled.Status != ProviderTaskCancelled {
			t.Fatalf("取消必须幂等: result=%+v err=%v", cancelled, cancelErr)
		}
	}
}

func TestFakeAsyncVideoAdapterContentAndDeleteContract(t *testing.T) {
	adapter := NewFakeAsyncVideoAdapter(FakeVideoSuccess)
	submitted, err := adapter.Submit(context.Background(), SubmitRequest{RequestID: "vid_req_content", Operation: OperationTextToVideo, Prompt: "测试", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = adapter.Query(context.Background(), QueryRequest{ProviderTaskID: submitted.ProviderTaskID})
	result, err := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: submitted.ProviderTaskID})
	if err != nil {
		t.Fatal(err)
	}
	content, err := adapter.OpenContent(context.Background(), *result.Content)
	if err != nil || content.SizeBytes == 0 || content.ReaderAt == nil {
		t.Fatalf("Content必须返回受控流: content=%+v err=%v", content, err)
	}
	for index := 0; index < 2; index++ {
		if err := adapter.Delete(context.Background(), *result.Content); err != nil {
			t.Fatalf("Delete必须幂等: %v", err)
		}
	}
	if _, err := adapter.OpenContent(context.Background(), *result.Content); !errors.Is(err, ErrProviderTaskNotFound) {
		t.Fatalf("删除后Content必须不可读: %v", err)
	}
}

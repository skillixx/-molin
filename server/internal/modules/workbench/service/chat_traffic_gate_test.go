package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
)

// trafficClosedUpstream 模拟共享转发器在商业总闸关闭时拒绝调用。
type trafficClosedUpstream struct{}

func (trafficClosedUpstream) ChatOnce(context.Context, tokengatewaysvc.ChatOnceInput) (*tokengatewaysvc.ChatOnceResult, error) {
	return nil, tokengatewaysvc.ErrTrafficClosed
}

func TestRunLoopTrafficClosedBeforeStreaming(t *testing.T) {
	svc := &ChatService{upstream: trafficClosedUpstream{}, maxRounds: 1}
	recorder := httptest.NewRecorder()

	_, err := svc.runLoop(context.Background(), recorder, []interface{}{}, nil, nil, "g8/text-1", 1, "g8-request", true)
	if !errors.Is(err, tokengatewaysvc.ErrTrafficClosed) {
		t.Fatalf("流式请求应在写出 SSE 前返回商业总闸错误，实际为 %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("商业总闸关闭时不得提前写出 SSE，实际响应为 %q", recorder.Body.String())
	}
}

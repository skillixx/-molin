package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestForwardServiceTrafficGateCoversAllSharedExecutionEntrypoints(t *testing.T) {
	service := (&ForwardService{}).WithTrafficEnabled(false)
	if err := service.Forward(context.Background(), httptest.NewRecorder(), ForwardInput{}); !errors.Is(err, ErrTrafficClosed) {
		t.Fatalf("公开旧转发入口必须在读取模型或外呼前被总闸拒绝: %v", err)
	}
	if _, err := service.ChatOnce(context.Background(), ChatOnceInput{}); !errors.Is(err, ErrTrafficClosed) {
		t.Fatalf("工作台与会话摘要复用入口必须在读取模型或外呼前被总闸拒绝: %v", err)
	}
}

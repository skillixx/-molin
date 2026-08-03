package sender

import (
	"context"
	"sync"
)

// MockSender 仅用于测试注入，不会访问网络，也不会作为生产配置回退项。
type MockSender struct {
	mu      sync.Mutex
	result  Result
	err     error
	calls   int
	request Request
}

func NewMockSender(result Result, err error) *MockSender {
	return &MockSender{result: result, err: err}
}

func (s *MockSender) Send(_ context.Context, req Request) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.request = req
	return s.result, s.err
}

func (s *MockSender) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *MockSender) LastRequest() Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request
}

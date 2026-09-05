package bootstrap

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppRunContextClosesRuntimeWhenListenFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var closed atomic.Bool
	app := &App{Server: &http.Server{Addr: listener.Addr().String()}, shutdown: func(context.Context) error {
		closed.Store(true)
		return nil
	}}
	if err := app.RunContext(context.Background()); err == nil {
		t.Fatal("监听地址被占用时必须返回启动失败")
	}
	if !closed.Load() {
		t.Fatal("监听失败后也必须同步收口已装配视频运行时")
	}
}

func TestAppRunContextWaitsForRuntimeShutdown(t *testing.T) {
	var closed atomic.Bool
	app := &App{
		Server: &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })},
		shutdown: func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			closed.Store(true)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.RunContext(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("正常取消应优雅退出: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunContext未在超时内退出")
	}
	if !closed.Load() {
		t.Fatal("RunContext返回前必须等待运行时关闭完成")
	}
}

func TestAppRunContextPropagatesRuntimeShutdownFailure(t *testing.T) {
	want := errors.New("合成Worker收口失败")
	app := &App{Server: &http.Server{Addr: "127.0.0.1:0"}, shutdown: func(context.Context) error { return want }}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.RunContext(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, want) {
		t.Fatalf("运行时关闭失败必须传递给进程入口: %v", err)
	}
}

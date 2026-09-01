package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type videoSlowWriteListener struct{ net.Listener }

func (l videoSlowWriteListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(1024)
	}
	return conn, nil
}

// TestVideoG6ContentHTTPRealSlowClientTimeout 使用真实socket阻塞服务端写入，不能以Recorder代替慢客户端。
func TestVideoG6ContentHTTPRealSlowClientTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var reads, releases atomic.Int64
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			releases.Add(1)
			close(done)
		}()
		ServeVideoContent(w, r, VideoHTTPContent{
			Size: 32 << 20, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BeforeWrite: func(context.Context) (time.Time, error) { return time.Now().Add(100 * time.Millisecond), nil },
			OpenRange: func(context.Context, int64, int64) (io.ReadCloser, error) {
				reads.Add(1)
				return io.NopCloser(bytes.NewReader(make([]byte, 1<<20))), nil
			},
		})
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(videoSlowWriteListener{Listener: listener}) }()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET /v1/videos/v_slow/content HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("真实慢客户端未在写期限内中止")
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("真实慢客户端写期限不符合预期：%s", elapsed)
	}
	if reads.Load() < 1 || reads.Load() >= 32 || releases.Load() != 1 {
		t.Fatalf("慢客户端必须在完整32片前中止并执行一次清理：reads=%d releases=%d", reads.Load(), releases.Load())
	}
	_ = server.Close()
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("慢客户端测试服务器未退出")
	}
}

// 从真实回环连接验证总带宽，不能逐片重置令牌而形成持续超速。
func TestVideoG6ContentHTTPBandwidth(t *testing.T) {
	const size int64 = 4 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeVideoContent(w, r, VideoHTTPContent{Size: size, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OpenRange: func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(make([]byte, length))), nil
		}})
	}))
	defer server.Close()
	start := time.Now()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if err != nil || n != size {
		t.Fatal("完整正文传输失败")
	}
	// 20MiB/s加首片1MiB突发：4MiB至少需要150ms；仅保留计时精度的1ms容差。
	if elapsed := time.Since(start); elapsed < 149*time.Millisecond {
		t.Fatalf("带宽上限被绕过：%s", elapsed)
	}
}

type videoCancelWriter struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	deadline time.Time
}

func (w *videoCancelWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	w.cancel()
	return n, err
}
func (w *videoCancelWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

// 发送首片后取消，等待与续约不能读取下一片；真实HTTP完整带宽由上面的回环用例验证。
func TestVideoG6ContentHTTPLeaseDeadlineAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/v1/videos/video_fixture/content", nil).WithContext(ctx)
	w := &videoCancelWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	until := time.Now().Add(10 * time.Second)
	var reads atomic.Int64
	func() {
		defer func() {
			if !errors.Is(asVideoHTTPError(recover()), http.ErrAbortHandler) {
				t.Error("取消应中止媒体响应")
			}
		}()
		ServeVideoContent(w, r, VideoHTTPContent{Size: 2 << 20, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BeforeWrite: func(context.Context) (time.Time, error) { return until, nil }, OpenRange: func(context.Context, int64, int64) (io.ReadCloser, error) {
			reads.Add(1)
			return io.NopCloser(bytes.NewReader(make([]byte, 1<<20))), nil
		}})
	}()
	if reads.Load() != 1 || w.Body.Len() != 1<<20 || w.deadline.After(until) {
		t.Fatal("取消后不得读取第二片，写期限不能超过租约")
	}
}

func asVideoHTTPError(value any) error { err, _ := value.(error); return err }

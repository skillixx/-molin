package service

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// sseWriter 极简 SSE 事件写出器：首次写出时落 header（200），之后逐事件 flush。
// started 标记是否已落 header——供编排层判断「错误能否改 HTTP 状态码」。
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	f, _ := w.(http.Flusher)
	return &sseWriter{w: w, flusher: f}
}

// start 落 SSE 响应头（仅一次）。
func (s *sseWriter) start() {
	if s.started {
		return
	}
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.WriteHeader(http.StatusOK)
	s.started = true
	s.flush()
}

// event 写一条具名 SSE 事件（data 为 JSON）。未 start 则先 start。
func (s *sseWriter) event(name string, data interface{}) {
	if !s.started {
		s.start()
	}
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"message":"序列化失败"}`)
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, payload)
	s.flush()
}

// done 写流结束标记 [DONE]。
func (s *sseWriter) done() {
	if !s.started {
		s.start()
	}
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flush()
}

func (s *sseWriter) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

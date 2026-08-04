package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type completionRequest struct {
	Stream bool `json:"stream"`
}

var requestCount atomic.Uint64

func main() {
	host := flag.String("host", "0.0.0.0", "监听地址")
	port := flag.Int("port", 8080, "监听端口")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /count", count)
	mux.HandleFunc("POST /v1/chat/completions", completion)

	// 该服务只在隔离测试网络运行，不读取密钥，也不记录请求正文。
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *host, *port),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, map[string]string{"status": "ok"})
}

func count(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, map[string]uint64{"requests": requestCount.Load()})
}

func completion(writer http.ResponseWriter, request *http.Request) {
	requestCount.Add(1)
	defer request.Body.Close()
	var body completionRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
		return
	}
	if body.Stream {
		writeStream(writer)
		return
	}
	writeJSON(writer, map[string]any{
		"id":      "g1-fixed-json",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "g1-fixed",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": "OK"},
			"finish_reason": "stop",
		}},
		"usage": usage(),
	})
}

func writeStream(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	created := time.Now().Unix()
	chunks := []map[string]any{
		{
			"id": "g1-fixed-sse", "object": "chat.completion.chunk", "created": created, "model": "g1-fixed",
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]string{"role": "assistant", "content": "OK"}, "finish_reason": nil,
			}},
		},
		{
			"id": "g1-fixed-sse", "object": "chat.completion.chunk", "created": created, "model": "g1-fixed",
			"choices": []any{}, "usage": usage(),
		},
	}
	for _, chunk := range chunks {
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(writer, "data: %s\n\n", data)
	}
	fmt.Fprint(writer, "data: [DONE]\n\n")
}

func usage() map[string]int {
	return map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3}
}

func writeJSON(writer http.ResponseWriter, body any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(body)
}

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testMCPSpec 构造连本地 httptest stub 的调用参数。
func testMCPSpec(url string) MCPCallSpec {
	return MCPCallSpec{EndpointURL: url, TimeoutMs: 5000}
}

// TestMCPClient_DiscoverFlow 校验 initialize → notifications/initialized → tools/list 全链路（无 DB，用 httptest stub）。
func TestMCPClient_DiscoverFlow(t *testing.T) {
	fake := newFakeMCPServer([]MCPTool{
		{Name: "a", Description: "工具A", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "b", Description: "工具B", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}, "")
	defer fake.close()

	client := NewMCPClient()
	client.skipSSRF = true
	tools, proto, err := client.Discover(context.Background(), testMCPSpec(fake.srv.URL))
	if err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	if proto != "2025-06-18" {
		t.Errorf("协议版本错误: %s", proto)
	}
	if len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" {
		t.Errorf("tools 解析错误: %+v", tools)
	}
}

// TestMCPClient_ListToolsPagination 校验 tools/list 分页 nextCursor 循环取全。
func TestMCPClient_ListToolsPagination(t *testing.T) {
	fake := newFakeMCPServer([]MCPTool{
		{Name: "p1", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "p2", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}, "")
	fake.paginate = true
	defer fake.close()

	client := NewMCPClient()
	client.skipSSRF = true
	tools, _, err := client.Discover(context.Background(), testMCPSpec(fake.srv.URL))
	if err != nil {
		t.Fatalf("Discover 分页失败: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("分页应取全 2 工具，实际 %d", len(tools))
	}
}

// TestMCPClient_CallTool 校验 tools/call 取 result.content 文本 + 凭证头注入。
func TestMCPClient_CallTool(t *testing.T) {
	fake := newFakeMCPServer(nil, "调用结果文本")
	defer fake.close()

	client := NewMCPClient()
	client.skipSSRF = true
	spec := testMCPSpec(fake.srv.URL)
	spec.AuthHeader = "Authorization"
	spec.AuthValue = "Bearer tok-1"
	out, err := client.CallTool(context.Background(), spec, "mytool", json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("CallTool 失败: %v", err)
	}
	if out != "调用结果文本" {
		t.Errorf("content 解析错误: %q", out)
	}
	if fake.gotAuth != "Bearer tok-1" {
		t.Errorf("凭证头未注入，实际 %q", fake.gotAuth)
	}
}

// TestMCPClient_CallToolError 校验 JSON-RPC error 归一为 error（编排层据此降级）。
func TestMCPClient_CallToolError(t *testing.T) {
	fake := newFakeMCPServer(nil, "")
	fake.callErr = true
	defer fake.close()

	client := NewMCPClient()
	client.skipSSRF = true
	if _, err := client.CallTool(context.Background(), testMCPSpec(fake.srv.URL), "t", nil); err == nil {
		t.Errorf("JSON-RPC error 应归一为 error")
	}
}

// TestMCPClient_HTTPNon2xx 校验 HTTP 非 2xx 归一错误。
func TestMCPClient_HTTPNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := NewMCPClient()
	client.skipSSRF = true
	if _, _, err := client.Discover(context.Background(), testMCPSpec(srv.URL)); err == nil {
		t.Errorf("HTTP 500 应归一错误")
	}
}

// TestMCPClient_SSEFraming 校验 Streamable HTTP 的 SSE 包裹响应可解析。
func TestMCPClient_SSEFraming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\"}}\n\n"))
		case "tools/list":
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[{\"name\":\"sse_tool\"}]}}\n\n"))
		}
	}))
	defer srv.Close()
	client := NewMCPClient()
	client.skipSSRF = true
	tools, proto, err := client.Discover(context.Background(), testMCPSpec(srv.URL))
	if err != nil {
		t.Fatalf("SSE 解析失败: %v", err)
	}
	if proto != "2025-06-18" || len(tools) != 1 || tools[0].Name != "sse_tool" {
		t.Errorf("SSE 帧解析错误: proto=%s tools=%+v", proto, tools)
	}
}

// TestMCPClient_RuntimeSSRFBlocked 校验生产 client（skipSSRF=false）运行时拒绝内网 endpoint。
func TestMCPClient_RuntimeSSRFBlocked(t *testing.T) {
	client := NewMCPClient() // skipSSRF=false
	_, _, err := client.Discover(context.Background(), MCPCallSpec{EndpointURL: "https://127.0.0.1:1/rpc", TimeoutMs: 2000})
	if err == nil || !strings.Contains(err.Error(), "安全策略") {
		t.Errorf("运行时应拒绝内网 endpoint，实际 err=%v", err)
	}
}

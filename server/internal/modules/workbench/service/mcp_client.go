package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"molin/server/internal/modules/workbench/security"
)

// mcpProtocolVersion 本网关支持的 MCP 协议版本（initialize 时声明，按 server 返回适配）。
const mcpProtocolVersion = "2025-06-18"

// mcpClientName / mcpClientVersion clientInfo 声明。
const (
	mcpClientName    = "molin-workbench"
	mcpClientVersion = "1.0"
)

// mcpMaxToolsPages tools/list 分页保护上限，避免恶意 server 无限 nextCursor。
const mcpMaxToolsPages = 50

// MCPTool 从 server 发现的单个工具（tools/list 返回项）。
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPCallSpec 单次 MCP 协议交互所需的连接参数（由 service 层从 mcp_servers 行 + 解密凭证组装）。
type MCPCallSpec struct {
	EndpointURL    string // 已 SSRF 校验
	AuthHeader     string // 注入的鉴权头名（可空）
	AuthValue      string // 鉴权头值
	TimeoutMs      int    // 单次调用超时
	AllowedDomains []string
}

// MCPClient JSON-RPC 2.0 over Streamable HTTP 的 MCP 客户端。
// 范围 v1：initialize → notifications/initialized → tools/list（分页）→ tools/call；静态鉴权。
// 安全：运行时 SSRF（解析 DNS）+ 禁止跟随重定向 + 超时归一错误。
type MCPClient struct {
	httpClient *http.Client
	skipSSRF   bool // 仅测试场景置 true（连本地 httptest stub 时绕过私网拦截）；生产恒 false
}

// NewMCPClient 构造 MCP 客户端。禁止跟随重定向（防 302 跳内网绕过 SSRF）。
func NewMCPClient() *MCPClient {
	return &MCPClient{
		httpClient: &http.Client{CheckRedirect: noRedirect},
	}
}

// jsonRPCRequest JSON-RPC 2.0 请求体。
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"` // 通知（notification）时省略
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCError JSON-RPC 错误对象。
type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// jsonRPCResponse JSON-RPC 2.0 响应体。
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

// mcpSession 一次 discover/call 期间维护的会话状态（Mcp-Session-Id）。
type mcpSession struct {
	spec      MCPCallSpec
	sessionID string
}

// Discover 执行 initialize + notifications/initialized + tools/list（分页取全）。
// 返回发现的工具数组与协商到的协议版本。任何环节失败归一为 error（discover handler 映射 502）。
func (c *MCPClient) Discover(ctx context.Context, spec MCPCallSpec) (tools []MCPTool, protocolVersion string, err error) {
	sess := &mcpSession{spec: spec}
	protocolVersion, err = c.initialize(ctx, sess)
	if err != nil {
		return nil, "", err
	}
	// 通知 server 初始化完成（无响应）。
	if nerr := c.notifyInitialized(ctx, sess); nerr != nil {
		return nil, "", nerr
	}
	tools, err = c.listTools(ctx, sess)
	if err != nil {
		return nil, "", err
	}
	return tools, protocolVersion, nil
}

// CallTool 执行一次 tools/call（编排命中 MCP 工具时）。
// 返回 result.content 拼接后的文本；失败归一为 error，由编排层转为 tool 错误结果回灌（不中断对话）。
func (c *MCPClient) CallTool(ctx context.Context, spec MCPCallSpec, toolName string, args json.RawMessage) (string, error) {
	sess := &mcpSession{spec: spec}
	// Streamable HTTP 每次调用为独立 HTTP 请求；按需先 initialize 建立会话（取 Mcp-Session-Id）。
	if _, err := c.initialize(ctx, sess); err != nil {
		return "", err
	}
	if err := c.notifyInitialized(ctx, sess); err != nil {
		return "", err
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	params := map[string]interface{}{"name": toolName, "arguments": json.RawMessage(args)}
	res, err := c.rpc(ctx, sess, "tools/call", params)
	if err != nil {
		return "", err
	}
	return parseToolCallContent(res)
}

// initialize 发 initialize 请求，回填 sessionID，返回协商到的 protocolVersion。
func (c *MCPClient) initialize(ctx context.Context, sess *mcpSession) (string, error) {
	params := map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": mcpClientName, "version": mcpClientVersion},
	}
	res, err := c.rpc(ctx, sess, "initialize", params)
	if err != nil {
		return "", err
	}
	var out struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if jerr := json.Unmarshal(res, &out); jerr != nil {
		return "", fmt.Errorf("initialize 响应解析失败: %v", jerr)
	}
	return out.ProtocolVersion, nil
}

// notifyInitialized 发 notifications/initialized 通知（无 id，不解析响应体）。
func (c *MCPClient) notifyInitialized(ctx context.Context, sess *mcpSession) error {
	return c.notify(ctx, sess, "notifications/initialized", map[string]interface{}{})
}

// listTools 循环取全 tools/list（处理 nextCursor 分页）。
func (c *MCPClient) listTools(ctx context.Context, sess *mcpSession) ([]MCPTool, error) {
	var all []MCPTool
	cursor := ""
	for page := 0; page < mcpMaxToolsPages; page++ {
		var params map[string]interface{}
		if cursor != "" {
			params = map[string]interface{}{"cursor": cursor}
		}
		res, err := c.rpc(ctx, sess, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var out struct {
			Tools      []MCPTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if jerr := json.Unmarshal(res, &out); jerr != nil {
			return nil, fmt.Errorf("tools/list 响应解析失败: %v", jerr)
		}
		all = append(all, out.Tools...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return nil, fmt.Errorf("tools/list 分页超过保护上限 %d 页", mcpMaxToolsPages)
}

// rpc 发一条带 id 的 JSON-RPC 请求并返回 result（已归一错误）。
func (c *MCPClient) rpc(ctx context.Context, sess *mcpSession, method string, params interface{}) (json.RawMessage, error) {
	reqBody := jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	resp, body, err := c.do(ctx, sess, reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP %s 返回 HTTP %d", method, resp.StatusCode)
	}
	rpcResp, perr := parseJSONRPCResponse(body)
	if perr != nil {
		return nil, fmt.Errorf("MCP %s %v", method, perr)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP %s 返回错误[%d]: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// notify 发一条不带 id 的通知（不解析响应 result，仅校验传输层）。
func (c *MCPClient) notify(ctx context.Context, sess *mcpSession, method string, params interface{}) error {
	reqBody := jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params}
	resp, _, err := c.do(ctx, sess, reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 通知通常返回 202 Accepted 或 200；非 2xx 视为传输失败。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("MCP 通知 %s 返回 HTTP %d", method, resp.StatusCode)
	}
	return nil
}

// do 发 HTTP POST（带超时 + 运行时 SSRF + 会话头），返回响应与已读取的 body。
func (c *MCPClient) do(ctx context.Context, sess *mcpSession, reqBody jsonRPCRequest) (*http.Response, []byte, error) {
	// 运行时 SSRF 校验（解析 DNS + 白名单），每次外呼前都校验，防 DNS rebinding。
	if !c.skipSSRF {
		if err := security.ValidateOutboundURL(sess.spec.EndpointURL, sess.spec.AllowedDomains, true); err != nil {
			return nil, nil, fmt.Errorf("MCP 端点被安全策略拒绝: %v", err)
		}
	}

	timeout := time.Duration(sess.spec.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 15 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)

	payload, err := json.Marshal(reqBody)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("MCP 请求序列化失败: %v", err)
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, sess.spec.EndpointURL, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("构造 MCP 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Streamable HTTP：声明可接受 JSON 与 SSE 两种返回。
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if sess.spec.AuthHeader != "" {
		req.Header.Set(sess.spec.AuthHeader, sess.spec.AuthValue)
	}
	if sess.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sess.sessionID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("MCP 外呼失败: %v", err)
	}
	// 回填会话头（server 首次 initialize 可能下发）。
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		sess.sessionID = sid
	}
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	resp.Body.Close()
	cancel()
	if rerr != nil {
		return nil, nil, fmt.Errorf("读取 MCP 响应失败: %v", rerr)
	}
	// 重新包一个可关闭 body 供调用方 defer Close（已读完，Close 为 no-op）。
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, body, nil
}

// parseJSONRPCResponse 解析 JSON-RPC 响应；兼容 Streamable HTTP 的 SSE 包裹（text/event-stream，data: 行）。
func parseJSONRPCResponse(body []byte) (*jsonRPCResponse, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("响应体为空")
	}
	// 直接 JSON。
	if trimmed[0] == '{' {
		var r jsonRPCResponse
		if err := json.Unmarshal(trimmed, &r); err != nil {
			return nil, fmt.Errorf("响应解析失败: %v", err)
		}
		return &r, nil
	}
	// SSE：抽取所有 data: 行，取最后一个能解析为 JSON-RPC response 的。
	var last *jsonRPCResponse
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data[0] != '{' {
			continue
		}
		var r jsonRPCResponse
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			continue
		}
		rr := r
		last = &rr
	}
	if last == nil {
		return nil, errors.New("SSE 响应未找到有效 JSON-RPC 帧")
	}
	return last, nil
}

// parseToolCallContent 从 tools/call result 中抽取 content 文本（text 类型拼接），并标注为工具输出。
// MCP content 项形如 [{type:"text",text:"..."},{type:"image",...}]；非文本类型以占位描述。
func parseToolCallContent(result json.RawMessage) (string, error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		// 无法按标准结构解析时退回原始 JSON（仍是工具外部输出，调用方会截断）。
		return string(result), nil
	}
	var sb strings.Builder
	for _, item := range out.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
		} else {
			sb.WriteString(fmt.Sprintf("[非文本内容: %s]", item.Type))
		}
		sb.WriteString("\n")
	}
	text := strings.TrimRight(sb.String(), "\n")
	if out.IsError {
		return "工具返回错误: " + text, nil
	}
	if text == "" {
		return string(result), nil
	}
	return text, nil
}

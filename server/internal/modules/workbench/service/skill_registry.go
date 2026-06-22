package service

import (
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

// ErrSkillHandlerNotFound 内置 skill handler_key 未注册（运营配了 skill 但门面无对应实现）。
var ErrSkillHandlerNotFound = errors.New("skill handler 未实现")

// noRedirect 禁止外呼跟随 3xx 重定向：防止上游 302 跳内网绕过 SSRF 前置校验
// （已校验的公网域名重定向到内网地址）。任何重定向直接判失败。
func noRedirect(req *http.Request, via []*http.Request) error {
	return errors.New("拒绝跟随重定向")
}

// skillFunc 内置 skill 执行函数：入参为模型给出的 arguments（JSON），返回回灌给模型的结果文本。
type skillFunc func(ctx context.Context, args json.RawMessage) (string, error)

// SkillRegistry 平台内置 skill 的执行注册表（门面 dispatch，D4 不含 code_exec）。
// 本期内置 web_search（占位：未配置搜索服务商时优雅降级）+ doc_read（真实抓取 https 文档，SSRF 防护）。
type SkillRegistry struct {
	funcs          map[string]skillFunc
	httpClient     *http.Client
	allowedDomains []string // 外呼域名白名单（运维注入，S2-运2；空=仅按 SSRF 网段规则）
}

// NewSkillRegistry 构造内置 skill 注册表。allowedDomains 为外呼白名单（可空）。
func NewSkillRegistry(allowedDomains []string) *SkillRegistry {
	r := &SkillRegistry{
		funcs:          map[string]skillFunc{},
		httpClient:     &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect},
		allowedDomains: allowedDomains,
	}
	r.funcs["web_search"] = r.webSearch
	r.funcs["doc_read"] = r.docRead
	return r
}

// Has 判断某 handler_key 是否有内置实现。
func (r *SkillRegistry) Has(handlerKey string) bool {
	_, ok := r.funcs[handlerKey]
	return ok
}

// Dispatch 按 handler_key 执行内置 skill，返回回灌结果文本。
// 未注册的 handler_key 返回 ErrSkillHandlerNotFound（编排层转为 tool 错误结果回灌，不中断对话）。
func (r *SkillRegistry) Dispatch(ctx context.Context, handlerKey string, args json.RawMessage) (string, error) {
	fn, ok := r.funcs[handlerKey]
	if !ok {
		return "", ErrSkillHandlerNotFound
	}
	return fn(ctx, args)
}

// webSearch 联网搜索（占位实现）。本期未接入搜索服务商：返回明确错误，由编排层回灌为 tool 错误结果，
// 模型据此自行降级（不中断整次对话）。接入真实服务商（SEARCH_API_KEY）为运维/后续阶段事项。
func (r *SkillRegistry) webSearch(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	if strings.TrimSpace(p.Query) == "" {
		return "", errors.New("query 不能为空")
	}
	return "", errors.New("联网搜索服务尚未配置服务商，暂不可用")
}

// docRead 抓取 https 文档并返回截断后的文本（真实工具示例）。
// 安全：URL 来自模型输出，外呼前用 security.ValidateOutboundURL（解析 DNS + 白名单）防 SSRF。
func (r *SkillRegistry) docRead(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	if strings.TrimSpace(p.URL) == "" {
		return "", errors.New("url 不能为空")
	}
	if err := security.ValidateOutboundURL(p.URL, r.allowedDomains, true); err != nil {
		return "", fmt.Errorf("URL 被安全策略拒绝: %v", err)
	}
	maxChars := p.MaxChars
	if maxChars <= 0 || maxChars > 8000 {
		maxChars = 4000
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %v", err)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("抓取失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("抓取返回状态 %d", resp.StatusCode)
	}
	// 限制读取体积（256KB）防止超大响应拖垮内存。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}
	text := string(body)
	if len(text) > maxChars {
		text = text[:maxChars] + "\n…（内容已截断）"
	}
	return text, nil
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
	"molin/server/internal/modules/workbench/security"
)

// ErrPluginDailyLimit 付费插件当日调用已达上限（D3）。编排层转为 tool 错误结果回灌，不中断对话。
var ErrPluginDailyLimit = errors.New("该付费插件今日调用次数已达上限")

// pluginBreakerThreshold 熔断阈值：连续失败达此值自动将插件置 inactive 并告警（契约 §5）。
const pluginBreakerThreshold = 5

// PluginForwarder 外部第三方插件 HTTP 转发器（S2-甲9，协丁）。
// 职责：付费插件日上限计数 → SSRF 校验（解析 DNS + 白名单）→ 解密注入凭证 → 带超时转发 → 截断回灌 + 熔断。
type PluginForwarder struct {
	cipher         *crypto.AESGCM
	callRepo       *repository.PluginCallRepository
	pluginRepo     *repository.PluginRepository
	allowedDomains []string

	mu       sync.Mutex
	failures map[uint64]int // pluginID → 连续失败次数（内存熔断计数）
}

// NewPluginForwarder 构造插件转发器。allowedDomains 为外呼白名单（可空）。
func NewPluginForwarder(cipher *crypto.AESGCM, callRepo *repository.PluginCallRepository, pluginRepo *repository.PluginRepository, allowedDomains []string) *PluginForwarder {
	return &PluginForwarder{
		cipher:         cipher,
		callRepo:       callRepo,
		pluginRepo:     pluginRepo,
		allowedDomains: allowedDomains,
		failures:       map[uint64]int{},
	}
}

// Forward 执行一次插件外呼，返回回灌给模型的结果文本。
// 失败（含日上限/SSRF 拒绝/超时/上游错误）以 error 返回，由编排层转为 tool 错误结果回灌，不中断对话。
func (f *PluginForwarder) Forward(ctx context.Context, p *model.Plugin, userID uint64, args json.RawMessage) (string, error) {
	// ① 付费插件每用户每日上限（D3）。
	if p.IsPaid && p.DailyLimit > 0 {
		allowed, err := f.callRepo.IncrementIfUnderLimit(ctx, p.ID, userID, p.DailyLimit)
		if err != nil {
			return "", fmt.Errorf("日限额校验失败: %v", err)
		}
		if !allowed {
			return "", ErrPluginDailyLimit
		}
	}

	// ② SSRF 校验（运行时解析 DNS + 白名单）。
	if err := security.ValidateOutboundURL(p.EndpointURL, f.allowedDomains, true); err != nil {
		return "", fmt.Errorf("插件端点被安全策略拒绝: %v", err)
	}

	// ③ 解密注入凭证（auth_config 形如 {"header":"Authorization","value":"Bearer xxx"}）。
	header, value, err := f.resolveAuth(p)
	if err != nil {
		return "", err
	}

	// ④ 带超时转发（timeout_ms，封顶 30s）。
	timeout := time.Duration(p.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.EndpointURL, bytes.NewReader(args))
	if err != nil {
		return "", fmt.Errorf("构造插件请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(header, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.recordFailure(ctx, p)
		return "", fmt.Errorf("插件外呼失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		f.recordFailure(ctx, p)
		return "", fmt.Errorf("读取插件响应失败: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		f.recordFailure(ctx, p)
		return "", fmt.Errorf("插件返回状态 %d", resp.StatusCode)
	}

	f.recordSuccess(p.ID)
	text := string(body)
	if len(text) > 6000 {
		text = text[:6000] + "\n…（内容已截断）"
	}
	return text, nil
}

// resolveAuth 解密 auth_config 并解析出要注入的请求头（无凭证返回空串）。
func (f *PluginForwarder) resolveAuth(p *model.Plugin) (header, value string, err error) {
	if p.AuthConfigEncrypted == nil || *p.AuthConfigEncrypted == "" {
		return "", "", nil
	}
	plain, derr := f.cipher.Decrypt(*p.AuthConfigEncrypted)
	if derr != nil {
		return "", "", fmt.Errorf("插件凭证解密失败")
	}
	var cfg struct {
		Header string `json:"header"`
		Value  string `json:"value"`
	}
	if jerr := json.Unmarshal([]byte(plain), &cfg); jerr != nil || cfg.Header == "" {
		return "", "", fmt.Errorf("插件凭证配置格式非法（应为 {\"header\":\"\",\"value\":\"\"}）")
	}
	return cfg.Header, cfg.Value, nil
}

// recordFailure 记一次失败；连续失败达阈值则熔断（置插件 inactive + 告警）。
func (f *PluginForwarder) recordFailure(ctx context.Context, p *model.Plugin) {
	f.mu.Lock()
	f.failures[p.ID]++
	n := f.failures[p.ID]
	f.mu.Unlock()
	if n < pluginBreakerThreshold {
		return
	}
	log.Printf("[workbench] 插件连续失败 %d 次触发熔断，置 inactive plugin_id=%d code=%s", n, p.ID, p.Code)
	if f.pluginRepo != nil {
		if err := f.pluginRepo.Update(context.Background(), p.ID, map[string]interface{}{"status": "inactive"}); err != nil {
			log.Printf("[workbench] 熔断置 inactive 失败 plugin_id=%d: %v", p.ID, err)
		}
	}
	f.mu.Lock()
	f.failures[p.ID] = 0
	f.mu.Unlock()
}

// recordSuccess 成功则清零连续失败计数。
func (f *PluginForwarder) recordSuccess(pluginID uint64) {
	f.mu.Lock()
	if f.failures[pluginID] != 0 {
		f.failures[pluginID] = 0
	}
	f.mu.Unlock()
}

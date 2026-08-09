package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 渠道服务层错误。
var (
	// ErrChannelCodeRequired 渠道编码必填（校验类）。
	ErrChannelCodeRequired = newValidation("渠道编码不能为空")
	// ErrChannelFieldsRequired 创建必填字段缺失（校验类）。
	ErrChannelFieldsRequired = newValidation("name/base_url/api_key_plaintext 不能为空")
	// ErrChannelCodeExists 渠道编码已存在（唯一冲突，非校验类）。
	ErrChannelCodeExists = errors.New("渠道编码已存在")
	// ErrChannelNotFound 渠道不存在（透传仓库层）。
	ErrChannelNotFound = repository.ErrChannelNotFound
)

// 渠道类型与状态白名单。
var (
	validChannelTypes  = map[string]bool{"openai_compatible": true, "anthropic": true, "gemini": true}
	validChannelStatus = map[string]bool{"active": true, "inactive": true}
)

// ChannelService 渠道 CRUD 服务，负责上游 api_key 的 AES-256-GCM 加解密。
// 安全红线：任何返回 DTO 绝不含明文或密文 key。
type ChannelService struct {
	repo                    *repository.ChannelRepository
	cipher                  *crypto.AESGCM
	client                  *http.Client
	healthInternalAllowlist []string
}

// NewChannelService 创建渠道服务实例。cipher 用于 api_key 加解密。
func NewChannelService(repo *repository.ChannelRepository, cipher *crypto.AESGCM) *ChannelService {
	return &ChannelService{repo: repo, cipher: cipher, client: &http.Client{Timeout: 5 * time.Second}}
}

// WithHealthInternalAllowlist 注入测试或受控内网健康入口白名单。
// 白名单只影响健康探测，不会放宽模型转发、插件或其他外呼路径。
func (s *ChannelService) WithHealthInternalAllowlist(items []string) *ChannelService {
	s.healthInternalAllowlist = append([]string(nil), items...)
	return s
}

// Create 创建渠道：校验 + 唯一性 + 加密 api_key 落库，返回脱敏 DTO。
func (s *ChannelService) Create(ctx context.Context, req dto.CreateChannelReq) (*dto.ChannelResp, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, ErrChannelCodeRequired
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.BaseURL) == "" || req.APIKeyPlaintext == "" {
		return nil, ErrChannelFieldsRequired
	}

	chType := req.Type
	if chType == "" {
		chType = "openai_compatible"
	}
	if !validChannelTypes[chType] {
		return nil, newValidation("type 只能为 openai_compatible/anthropic/gemini")
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	if !validChannelStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	// 唯一性预检（DB 唯一键兜底）。
	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, ErrChannelCodeExists
	} else if !isNotFound(err) {
		return nil, err
	}

	encrypted, err := s.cipher.Encrypt(req.APIKeyPlaintext)
	if err != nil {
		return nil, err
	}

	c := &model.TokenChannel{
		Code:            code,
		Name:            req.Name,
		Type:            chType,
		BaseURL:         req.BaseURL,
		APIKeyEncrypted: encrypted,
		Status:          status,
		Priority:        req.Priority,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return channelToResp(c), nil
}

// Get 按 ID 查询渠道，返回脱敏 DTO。
func (s *ChannelService) Get(ctx context.Context, id uint64) (*dto.ChannelResp, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return channelToResp(c), nil
}

// ListPaged 分页查询渠道，返回脱敏 DTO 列表与总数。
func (s *ChannelService) ListPaged(ctx context.Context, status string, offset, limit int) ([]dto.ChannelResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, status, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.ChannelResp, len(items))
	for i := range items {
		resp[i] = *channelToResp(&items[i])
	}
	return resp, total, nil
}

// Update 更新渠道字段。api_key_plaintext 非空时重新加密覆盖，否则不动既有 key。
func (s *ChannelService) Update(ctx context.Context, id uint64, req dto.UpdateChannelReq) (*dto.ChannelResp, error) {
	updates := map[string]interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return nil, newValidation("name 不能为空")
		}
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		if !validChannelTypes[*req.Type] {
			return nil, newValidation("type 只能为 openai_compatible/anthropic/gemini")
		}
		updates["type"] = *req.Type
	}
	if req.BaseURL != nil {
		if strings.TrimSpace(*req.BaseURL) == "" {
			return nil, newValidation("base_url 不能为空")
		}
		updates["base_url"] = *req.BaseURL
	}
	if req.Status != nil {
		if !validChannelStatus[*req.Status] {
			return nil, newValidation("status 只能为 active/inactive")
		}
		updates["status"] = *req.Status
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	// 仅当传入非空明文时才重新加密覆盖，避免空串清空已配置 key。
	if req.APIKeyPlaintext != nil && *req.APIKeyPlaintext != "" {
		encrypted, err := s.cipher.Encrypt(*req.APIKeyPlaintext)
		if err != nil {
			return nil, err
		}
		updates["api_key_encrypted"] = encrypted
	}

	if len(updates) == 0 {
		// 无可更新字段，直接回查当前值。
		return s.Get(ctx, id)
	}
	if err := s.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除渠道。
func (s *ChannelService) Delete(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// CheckHealth 只请求渠道入口的公开 /health，不携带上游密钥，也不会产生模型调用费用。
func (s *ChannelService) CheckHealth(ctx context.Context, id uint64) (*dto.ChannelResp, error) {
	channel, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	status, errorClass := probeChannelHealthWithPolicy(ctx, s.client, net.DefaultResolver, channel.BaseURL, s.healthInternalAllowlist)
	now := time.Now().UTC()
	if err := s.repo.Update(ctx, id, map[string]interface{}{"health_status": status, "last_health_check_at": now, "last_health_error_class": nullableHealthError(errorClass)}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func probeChannelHealth(ctx context.Context, client *http.Client, baseURL string) (string, string) {
	return probeChannelHealthWithPolicy(ctx, client, net.DefaultResolver, baseURL, nil)
}

// healthDNSResolver 抽象 DNS 解析，便于覆盖域名指向私网、混合解析和重绑定场景。
type healthDNSResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func probeChannelHealthWithPolicy(ctx context.Context, client *http.Client, resolver healthDNSResolver, baseURL string, internalAllowlist []string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "down", "invalid_base_url"
	}
	allowedInternal := healthTargetAllowlisted(parsed.Hostname(), parsed.Port(), internalAllowlist)
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && blockedHealthIP(ip) && !allowedInternal {
		return "down", "restricted_address"
	}
	// 公网健康探测只允许 HTTPS；测试内网 HTTP 必须通过精确白名单显式开启。
	if parsed.Scheme != "https" && !allowedInternal {
		return "down", "insecure_scheme"
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "/health", "", "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "down", "invalid_base_url"
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, fmt.Errorf("健康探测目标地址无效")
		}
		ips, lookupErr := resolveHealthIPs(dialCtx, resolver, host)
		if lookupErr != nil {
			return nil, lookupErr
		}
		for _, ip := range ips {
			// 在实际拨号前重新校验解析结果，避免校验后由默认 Transport 二次解析造成 DNS 重绑定窗口。
			if blockedHealthIP(ip) && !allowedInternal {
				return nil, fmt.Errorf("健康探测目标解析到受限地址")
			}
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
	clientCopy := http.Client{Transport: transport, Timeout: 5 * time.Second}
	if client != nil && client.Timeout > 0 {
		clientCopy.Timeout = client.Timeout
	}
	// 健康入口必须由配置主机直接响应，禁止通过重定向把探测转向其他网络位置。
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := clientCopy.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return "down", "timeout"
		}
		return "down", "network_error"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "down", fmt.Sprintf("http_%d", response.StatusCode)
	}
	return "healthy", ""
}

func resolveHealthIPs(ctx context.Context, resolver healthDNSResolver, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("健康探测目标 DNS 解析失败")
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if address.IP == nil {
			return nil, fmt.Errorf("健康探测目标 DNS 返回空地址")
		}
		ips = append(ips, address.IP)
	}
	return ips, nil
}

func healthTargetAllowlisted(host, port string, items []string) bool {
	host = strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]")))
	for _, raw := range items {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == "" {
			continue
		}
		if item == host || (port != "" && (item == net.JoinHostPort(host, port) || item == host+":"+port)) {
			return true
		}
	}
	return false
}

func blockedHealthIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func nullableHealthError(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

// channelToResp 渠道实体 → 脱敏响应 DTO（不含明文/密文 key）。
func channelToResp(c *model.TokenChannel) *dto.ChannelResp {
	return &dto.ChannelResp{
		ID:                   c.ID,
		Code:                 c.Code,
		Name:                 c.Name,
		Type:                 c.Type,
		BaseURL:              c.BaseURL,
		HasAPIKey:            c.APIKeyEncrypted != "",
		Status:               c.Status,
		Priority:             c.Priority,
		HealthStatus:         c.HealthStatus,
		LastHealthCheckAt:    c.LastHealthCheckAt,
		LastHealthErrorClass: c.LastHealthErrorClass,
		CreatedAt:            c.CreatedAt,
		UpdatedAt:            c.UpdatedAt,
	}
}

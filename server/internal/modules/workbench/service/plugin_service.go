package service

import (
	"context"
	"errors"
	"strings"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
	"molin/server/internal/modules/workbench/security"
)

// ErrPluginCodeExists Plugin code 已存在（唯一冲突，非校验类）。
var ErrPluginCodeExists = errors.New("plugin code 已存在")

// maxPluginTimeoutMs 转发超时强制上限（契约 §5：≤30s）。
const maxPluginTimeoutMs = 30000

// defaultPluginTimeoutMs 未指定时的默认超时。
const defaultPluginTimeoutMs = 10000

// PluginService 外部第三方工具（Plugin）管理服务（管理端 CRUD + 用户端只读列表）。
// cipher 负责 auth_config 的 AES-256-GCM 加解密（复用 token_gateway/crypto，TOKEN_PROVIDER_KEY 或 PLUGIN_SECRET_KEY 注入）。
type PluginService struct {
	repo   *repository.PluginRepository
	cipher *crypto.AESGCM
}

// NewPluginService 创建 Plugin 服务实例。
func NewPluginService(repo *repository.PluginRepository, cipher *crypto.AESGCM) *PluginService {
	return &PluginService{repo: repo, cipher: cipher}
}

// Create 创建 Plugin：校验 + 唯一性 + 凭证加密。
func (s *PluginService) Create(ctx context.Context, req dto.CreatePluginReq) (*dto.PluginResp, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, newValidation("code 不能为空")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, newValidation("name 不能为空")
	}
	if err := validateToolSchema(req.ToolSchemaJSON); err != nil {
		return nil, err
	}
	if err := validateEndpointURL(req.EndpointURL); err != nil {
		return nil, err
	}
	timeout := req.TimeoutMs
	if timeout <= 0 {
		timeout = defaultPluginTimeoutMs
	}
	if timeout > maxPluginTimeoutMs {
		return nil, newValidation("timeout_ms 超过上限 30000")
	}
	if req.DailyLimit < 0 {
		return nil, newValidation("daily_limit 不能为负")
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	if !validResourceStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, ErrPluginCodeExists
	} else if !isNotFound(err) {
		return nil, err
	}

	enc, err := s.encryptAuth(req.AuthConfig)
	if err != nil {
		return nil, err
	}

	m := &model.Plugin{
		Code:                code,
		Name:                strings.TrimSpace(req.Name),
		Description:         req.Description,
		ToolSchemaJSON:      req.ToolSchemaJSON,
		EndpointURL:         strings.TrimSpace(req.EndpointURL),
		AuthConfigEncrypted: enc,
		TimeoutMs:           timeout,
		IsPaid:              req.IsPaid,
		DailyLimit:          req.DailyLimit,
		Status:              status,
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return pluginToResp(m), nil
}

// Get 按 ID 查询 Plugin（管理端视图，凭证不回）。
func (s *PluginService) Get(ctx context.Context, id uint64) (*dto.PluginResp, error) {
	m, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return pluginToResp(m), nil
}

// ListPaged 管理端分页查询 Plugin。
func (s *PluginService) ListPaged(ctx context.Context, status string, offset, limit int) ([]dto.PluginResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, status, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.PluginResp, len(items))
	for i := range items {
		resp[i] = *pluginToResp(&items[i])
	}
	return resp, total, nil
}

// ListPublic 用户端分页查询 active Plugin（精简视图，不回 endpoint/凭证）。
func (s *PluginService) ListPublic(ctx context.Context, offset, limit int) ([]dto.PublicPluginResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, "active", offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.PublicPluginResp, len(items))
	for i := range items {
		resp[i] = dto.PublicPluginResp{
			ID:          items[i].ID,
			Code:        items[i].Code,
			Name:        items[i].Name,
			Description: items[i].Description,
			IsPaid:      items[i].IsPaid,
		}
	}
	return resp, total, nil
}

// Update 更新 Plugin 字段。
func (s *PluginService) Update(ctx context.Context, id uint64, req dto.UpdatePluginReq) (*dto.PluginResp, error) {
	updates := map[string]interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return nil, newValidation("name 不能为空")
		}
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.ToolSchemaJSON != nil {
		if err := validateToolSchema(req.ToolSchemaJSON); err != nil {
			return nil, err
		}
		updates["tool_schema_json"] = []byte(req.ToolSchemaJSON)
	}
	if req.EndpointURL != nil {
		if err := validateEndpointURL(*req.EndpointURL); err != nil {
			return nil, err
		}
		updates["endpoint_url"] = strings.TrimSpace(*req.EndpointURL)
	}
	if req.AuthConfig != nil {
		enc, err := s.encryptAuth(*req.AuthConfig)
		if err != nil {
			return nil, err
		}
		updates["auth_config_encrypted"] = enc // nil=清空凭证
	}
	if req.TimeoutMs != nil {
		if *req.TimeoutMs <= 0 || *req.TimeoutMs > maxPluginTimeoutMs {
			return nil, newValidation("timeout_ms 须在 1~30000 之间")
		}
		updates["timeout_ms"] = *req.TimeoutMs
	}
	if req.IsPaid != nil {
		updates["is_paid"] = *req.IsPaid
	}
	if req.DailyLimit != nil {
		if *req.DailyLimit < 0 {
			return nil, newValidation("daily_limit 不能为负")
		}
		updates["daily_limit"] = *req.DailyLimit
	}
	if req.Status != nil {
		if !validResourceStatus[*req.Status] {
			return nil, newValidation("status 只能为 active/inactive")
		}
		updates["status"] = *req.Status
	}

	if len(updates) == 0 {
		return s.Get(ctx, id)
	}
	if err := s.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除 Plugin。
func (s *PluginService) Delete(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// encryptAuth 将明文鉴权配置加密为 base64 密文；空字符串返回 nil（不设凭证）。
func (s *PluginService) encryptAuth(plain string) (*string, error) {
	if strings.TrimSpace(plain) == "" {
		return nil, nil
	}
	enc, err := s.cipher.Encrypt(plain)
	if err != nil {
		return nil, err
	}
	return &enc, nil
}

// validateEndpointURL 配置时 SSRF 前置校验：复用 security.ValidateOutboundURL（不解析 DNS，
// 域名是否生效在运行时转发前二次校验 + 域名白名单 S2-运2 注入）。
func validateEndpointURL(raw string) error {
	if err := security.ValidateOutboundURL(raw, nil, false); err != nil {
		return newValidation("endpoint_url " + err.Error())
	}
	return nil
}

// pluginToResp Plugin 实体 → 管理端响应 DTO（凭证不回，以 has_auth 表征）。
func pluginToResp(m *model.Plugin) *dto.PluginResp {
	return &dto.PluginResp{
		ID:             m.ID,
		Code:           m.Code,
		Name:           m.Name,
		Description:    m.Description,
		ToolSchemaJSON: m.ToolSchemaJSON,
		EndpointURL:    m.EndpointURL,
		HasAuth:        m.AuthConfigEncrypted != nil && *m.AuthConfigEncrypted != "",
		TimeoutMs:      m.TimeoutMs,
		IsPaid:         m.IsPaid,
		DailyLimit:     m.DailyLimit,
		Status:         m.Status,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

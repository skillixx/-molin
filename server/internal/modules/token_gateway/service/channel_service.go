package service

import (
	"context"
	"errors"
	"strings"

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
	repo   *repository.ChannelRepository
	cipher *crypto.AESGCM
}

// NewChannelService 创建渠道服务实例。cipher 用于 api_key 加解密。
func NewChannelService(repo *repository.ChannelRepository, cipher *crypto.AESGCM) *ChannelService {
	return &ChannelService{repo: repo, cipher: cipher}
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

// channelToResp 渠道实体 → 脱敏响应 DTO（不含明文/密文 key）。
func channelToResp(c *model.TokenChannel) *dto.ChannelResp {
	return &dto.ChannelResp{
		ID:        c.ID,
		Code:      c.Code,
		Name:      c.Name,
		Type:      c.Type,
		BaseURL:   c.BaseURL,
		HasAPIKey: c.APIKeyEncrypted != "",
		Status:    c.Status,
		Priority:  c.Priority,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

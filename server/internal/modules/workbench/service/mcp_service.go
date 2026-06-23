package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
	"molin/server/internal/modules/workbench/security"
)

// ErrMCPServerCodeExists MCP server code 已存在（唯一冲突）。
var ErrMCPServerCodeExists = errors.New("mcp server code 已存在")

// ErrMCPDiscoverFailed discover 时连接/握手/发现失败（handler 映射 502）。
var ErrMCPDiscoverFailed = errors.New("mcp discover 失败")

// ErrMCPBindNonOfficial v1 仅官方 Agent 可绑 MCP（绑定用户自建被拒）。
var ErrMCPBindNonOfficial = errors.New("仅官方 Agent 可绑定 MCP server")

// maxMCPTimeoutMs 单次调用超时上限（契约 §7：≤30s）。
const maxMCPTimeoutMs = 30000

// defaultMCPTimeoutMs 未指定时默认超时。
const defaultMCPTimeoutMs = 15000

// MCPService MCP server 管理服务（管理端 CRUD + discover + 工具审核 + agent 绑定 + 用户端只读）。
// cipher 负责 auth_config 的 AES-256-GCM 加解密（复用 token_gateway/crypto）。
type MCPService struct {
	repo           *repository.MCPRepository
	agentRepo      *repository.AgentRepository
	cipher         *crypto.AESGCM
	client         *MCPClient
	allowedDomains []string
}

// NewMCPService 创建 MCP 服务实例。
func NewMCPService(repo *repository.MCPRepository, agentRepo *repository.AgentRepository, cipher *crypto.AESGCM, client *MCPClient, allowedDomains []string) *MCPService {
	return &MCPService{repo: repo, agentRepo: agentRepo, cipher: cipher, client: client, allowedDomains: allowedDomains}
}

// ——— server CRUD ———

// CreateServer 创建 MCP server：校验 + 唯一性 + SSRF 前置校验 + 凭证加密。
func (s *MCPService) CreateServer(ctx context.Context, req dto.CreateMCPServerReq) (*dto.MCPServerResp, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, newValidation("code 不能为空")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, newValidation("name 不能为空")
	}
	if err := validateMCPEndpoint(req.EndpointURL); err != nil {
		return nil, err
	}
	timeout, err := normalizeMCPTimeout(req.TimeoutMs, true)
	if err != nil {
		return nil, err
	}
	if req.DailyLimit < 0 {
		return nil, newValidation("daily_limit 不能为负")
	}
	status := req.Status
	if status == "" {
		status = "inactive" // 新建默认 inactive，发现+审核后再启用
	}
	if !validResourceStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	if _, ferr := s.repo.FindServerByCode(ctx, code); ferr == nil {
		return nil, ErrMCPServerCodeExists
	} else if !isNotFound(ferr) {
		return nil, ferr
	}

	enc, err := s.encryptAuth(req.AuthConfig)
	if err != nil {
		return nil, err
	}

	m := &model.MCPServer{
		Code:                code,
		Name:                strings.TrimSpace(req.Name),
		Description:         req.Description,
		EndpointURL:         strings.TrimSpace(req.EndpointURL),
		AuthConfigEncrypted: enc,
		TimeoutMs:           timeout,
		IsPaid:              req.IsPaid,
		DailyLimit:          req.DailyLimit,
		Status:              status,
	}
	if err := s.repo.CreateServer(ctx, m); err != nil {
		return nil, err
	}
	return mcpServerToResp(m), nil
}

// GetServer 按 ID 查询 MCP server（管理端视图，凭证不回）。
func (s *MCPService) GetServer(ctx context.Context, id uint64) (*dto.MCPServerResp, error) {
	m, err := s.repo.FindServerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return mcpServerToResp(m), nil
}

// ListServersPaged 管理端分页查询 MCP server。
func (s *MCPService) ListServersPaged(ctx context.Context, status string, offset, limit int) ([]dto.MCPServerResp, int64, error) {
	items, total, err := s.repo.ListServersPaged(ctx, status, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.MCPServerResp, len(items))
	for i := range items {
		resp[i] = *mcpServerToResp(&items[i])
	}
	return resp, total, nil
}

// ListPublicServers 用户端分页查询 active MCP server（精简视图，不回 endpoint/凭证）。
func (s *MCPService) ListPublicServers(ctx context.Context, offset, limit int) ([]dto.PublicMCPServerResp, int64, error) {
	items, total, err := s.repo.ListServersPaged(ctx, "active", offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.PublicMCPServerResp, len(items))
	for i := range items {
		resp[i] = dto.PublicMCPServerResp{
			ID:          items[i].ID,
			Code:        items[i].Code,
			Name:        items[i].Name,
			Description: items[i].Description,
			IsPaid:      items[i].IsPaid,
		}
	}
	return resp, total, nil
}

// UpdateServer 更新 MCP server 字段。
func (s *MCPService) UpdateServer(ctx context.Context, id uint64, req dto.UpdateMCPServerReq) (*dto.MCPServerResp, error) {
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
	if req.EndpointURL != nil {
		if err := validateMCPEndpoint(*req.EndpointURL); err != nil {
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
		timeout, err := normalizeMCPTimeout(*req.TimeoutMs, false)
		if err != nil {
			return nil, err
		}
		updates["timeout_ms"] = timeout
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
		return s.GetServer(ctx, id)
	}
	if err := s.repo.UpdateServer(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.GetServer(ctx, id)
}

// DeleteServer 删除 MCP server（先清绑定再删，避免悬挂；工具快照 FK CASCADE）。
func (s *MCPService) DeleteServer(ctx context.Context, id uint64) error {
	return s.repo.DeleteServer(ctx, id)
}

// ——— discover + 工具审核 ———

// Discover 触发 initialize + tools/list，upsert 工具快照并回填 protocol_version + last_discovered_at。
// 连接/握手失败返回 ErrMCPDiscoverFailed（不改 server 状态）。
func (s *MCPService) Discover(ctx context.Context, id uint64) (*dto.DiscoverMCPResp, error) {
	srv, err := s.repo.FindServerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	spec, err := s.buildCallSpec(srv)
	if err != nil {
		return nil, err
	}
	tools, protoVer, derr := s.client.Discover(ctx, spec)
	if derr != nil {
		return nil, fmt.Errorf("%w: %v", ErrMCPDiscoverFailed, derr)
	}

	changed := 0
	for _, t := range tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		hash := schemaHash(t.Name, t.Description, schema)
		snap := &model.MCPServerTool{
			ServerID:        srv.ID,
			ToolName:        t.Name,
			Description:     truncateStr(t.Description, 1024),
			InputSchemaJSON: schema,
			SchemaHash:      hash,
		}
		ch, uerr := s.repo.UpsertTool(ctx, snap)
		if uerr != nil {
			return nil, uerr
		}
		if ch {
			changed++
		}
	}

	now := time.Now()
	_ = s.repo.UpdateServer(ctx, srv.ID, map[string]interface{}{
		"protocol_version":   protoVer,
		"last_discovered_at": now,
	})

	snaps, err := s.repo.ListToolsByServer(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	return &dto.DiscoverMCPResp{
		ProtocolVersion: protoVer,
		Discovered:      len(tools),
		Changed:         changed,
		Tools:           mcpToolsToResp(snaps),
	}, nil
}

// ListTools 列出某 server 的全部工具快照（管理端审核视图）。
func (s *MCPService) ListTools(ctx context.Context, serverID uint64) ([]dto.MCPToolResp, error) {
	if _, err := s.repo.FindServerByID(ctx, serverID); err != nil {
		return nil, err
	}
	snaps, err := s.repo.ListToolsByServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return mcpToolsToResp(snaps), nil
}

// UpdateToolEnabled 审核启用/停用单个工具（校验工具属于该 server）。
func (s *MCPService) UpdateToolEnabled(ctx context.Context, serverID, toolID uint64, req dto.UpdateMCPToolReq) (*dto.MCPToolResp, error) {
	if req.Enabled == nil {
		return nil, newValidation("enabled 不能为空")
	}
	tool, err := s.repo.FindToolByID(ctx, toolID)
	if err != nil {
		return nil, err
	}
	if tool.ServerID != serverID {
		return nil, repository.ErrMCPToolNotFound
	}
	if err := s.repo.UpdateToolEnabled(ctx, toolID, *req.Enabled); err != nil {
		return nil, err
	}
	tool.Enabled = *req.Enabled
	r := mcpToolToResp(tool)
	return &r, nil
}

// ——— agent 绑定（v1 仅官方 Agent 可绑） ———

// BindAgentServers 覆盖式设置 Agent 绑定的 MCP server（空列表=全部解绑）。
func (s *MCPService) BindAgentServers(ctx context.Context, agentID uint64, serverIDs []uint64) error {
	agent, err := s.agentRepo.FindByID(ctx, agentID)
	if err != nil {
		return err
	}
	// v1 仅官方 Agent 可绑 MCP（外部接入风险高）。
	if agent.OwnerType != "official" {
		return ErrMCPBindNonOfficial
	}
	// 校验所有 server 存在（避免悬挂绑定）。
	if len(serverIDs) > 0 {
		found, ferr := s.repo.FindServersByIDs(ctx, serverIDs)
		if ferr != nil {
			return ferr
		}
		if len(found) != len(uniqueIDs(serverIDs)) {
			return newValidation("存在不存在的 mcp server id")
		}
	}
	return s.repo.ReplaceAgentBindings(ctx, agentID, serverIDs)
}

// ——— 内部辅助 ———

// encryptAuth 将明文鉴权配置加密为 base64 密文；空字符串返回 nil（不设凭证）。
func (s *MCPService) encryptAuth(plain string) (*string, error) {
	if strings.TrimSpace(plain) == "" {
		return nil, nil
	}
	enc, err := s.cipher.Encrypt(plain)
	if err != nil {
		return nil, err
	}
	return &enc, nil
}

// buildCallSpec 从 server 行 + 解密凭证组装 MCP 调用参数（含运行时 SSRF 所需白名单）。
func (s *MCPService) buildCallSpec(srv *model.MCPServer) (MCPCallSpec, error) {
	header, value, err := resolveMCPAuth(s.cipher, srv)
	if err != nil {
		return MCPCallSpec{}, err
	}
	return MCPCallSpec{
		EndpointURL:    srv.EndpointURL,
		AuthHeader:     header,
		AuthValue:      value,
		TimeoutMs:      srv.TimeoutMs,
		AllowedDomains: s.allowedDomains,
	}, nil
}

// resolveMCPAuth 解密 auth_config 并解析出要注入的请求头（无凭证返回空串）。
func resolveMCPAuth(cipher *crypto.AESGCM, srv *model.MCPServer) (header, value string, err error) {
	if srv.AuthConfigEncrypted == nil || *srv.AuthConfigEncrypted == "" {
		return "", "", nil
	}
	plain, derr := cipher.Decrypt(*srv.AuthConfigEncrypted)
	if derr != nil {
		return "", "", fmt.Errorf("mcp 凭证解密失败")
	}
	var cfg struct {
		Header string `json:"header"`
		Value  string `json:"value"`
	}
	if jerr := json.Unmarshal([]byte(plain), &cfg); jerr != nil || cfg.Header == "" {
		return "", "", fmt.Errorf("mcp 凭证配置格式非法（应为 {\"header\":\"\",\"value\":\"\"}）")
	}
	return cfg.Header, cfg.Value, nil
}

// validateMCPEndpoint 配置时 SSRF 前置校验（不解析 DNS）。
func validateMCPEndpoint(raw string) error {
	if err := security.ValidateOutboundURL(raw, nil, false); err != nil {
		return newValidation("endpoint_url " + err.Error())
	}
	return nil
}

// normalizeMCPTimeout 归一超时；create=true 时空值取默认，update 时 0/负值报错。
func normalizeMCPTimeout(v int, create bool) (int, error) {
	if v <= 0 {
		if create {
			return defaultMCPTimeoutMs, nil
		}
		return 0, newValidation("timeout_ms 须在 1~30000 之间")
	}
	if v > maxMCPTimeoutMs {
		return 0, newValidation("timeout_ms 超过上限 30000")
	}
	return v, nil
}

// schemaHash 工具定义指纹（name+description+inputSchema 规范化后 sha256）。
func schemaHash(name, desc string, schema json.RawMessage) string {
	var canon interface{}
	_ = json.Unmarshal(schema, &canon)
	canonBytes, _ := json.Marshal(canon) // 规范化键序，避免无意义 diff
	h := sha256.Sum256([]byte(name + "\x00" + desc + "\x00" + string(canonBytes)))
	return hex.EncodeToString(h[:])
}

// truncateStr 截断字符串到 max 字节（按 rune 安全）。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	out := ""
	for _, c := range r {
		if len(out)+len(string(c)) > max {
			break
		}
		out += string(c)
	}
	return out
}

// uniqueIDs 去重 ID 列表。
func uniqueIDs(ids []uint64) []uint64 {
	seen := map[uint64]struct{}{}
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// mcpServerToResp MCP server 实体 → 管理端响应 DTO（凭证不回，以 has_auth 表征）。
func mcpServerToResp(m *model.MCPServer) *dto.MCPServerResp {
	return &dto.MCPServerResp{
		ID:               m.ID,
		Code:             m.Code,
		Name:             m.Name,
		Description:      m.Description,
		EndpointURL:      m.EndpointURL,
		HasAuth:          m.AuthConfigEncrypted != nil && *m.AuthConfigEncrypted != "",
		ProtocolVersion:  m.ProtocolVersion,
		TimeoutMs:        m.TimeoutMs,
		IsPaid:           m.IsPaid,
		DailyLimit:       m.DailyLimit,
		Status:           m.Status,
		LastDiscoveredAt: m.LastDiscoveredAt,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

// mcpToolToResp 工具快照 → 响应 DTO。
func mcpToolToResp(m *model.MCPServerTool) dto.MCPToolResp {
	return dto.MCPToolResp{
		ID:              m.ID,
		ServerID:        m.ServerID,
		ToolName:        m.ToolName,
		Description:     m.Description,
		InputSchemaJSON: m.InputSchemaJSON,
		Enabled:         m.Enabled,
		SchemaHash:      m.SchemaHash,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func mcpToolsToResp(items []model.MCPServerTool) []dto.MCPToolResp {
	resp := make([]dto.MCPToolResp, len(items))
	for i := range items {
		resp[i] = mcpToolToResp(&items[i])
	}
	return resp
}

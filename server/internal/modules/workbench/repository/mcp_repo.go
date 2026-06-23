package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/model"
)

// ErrMCPServerNotFound MCP server 不存在（RowsAffected==0 守卫）。
var ErrMCPServerNotFound = errors.New("mcp server 不存在")

// ErrMCPToolNotFound MCP 工具快照不存在。
var ErrMCPToolNotFound = errors.New("mcp 工具不存在")

// MCPRepository MCP server / 工具快照 / Agent 绑定数据访问层。
type MCPRepository struct {
	db *gorm.DB
}

// NewMCPRepository 创建 MCP 仓库实例。
func NewMCPRepository(db *gorm.DB) *MCPRepository {
	return &MCPRepository{db: db}
}

// ——— mcp_servers ———

// CreateServer 创建 MCP server。
func (r *MCPRepository) CreateServer(ctx context.Context, m *model.MCPServer) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindServerByID 按 ID 查询 MCP server。
func (r *MCPRepository) FindServerByID(ctx context.Context, id uint64) (*model.MCPServer, error) {
	var m model.MCPServer
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindServerByCode 按 code 查询 MCP server（唯一性校验用）。
func (r *MCPRepository) FindServerByCode(ctx context.Context, code string) (*model.MCPServer, error) {
	var m model.MCPServer
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindServersByIDs 按 ID 集合批量查询。
func (r *MCPRepository) FindServersByIDs(ctx context.Context, ids []uint64) ([]model.MCPServer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var items []model.MCPServer
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListServersPaged 分页查询 MCP server，支持 status 过滤（空不过滤）。
func (r *MCPRepository) ListServersPaged(ctx context.Context, status string, offset, limit int) ([]model.MCPServer, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.MCPServer{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.MCPServer
	if err := query.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateServer 更新 MCP server 字段（map 方式支持零值更新）。
func (r *MCPRepository) UpdateServer(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.MCPServer{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMCPServerNotFound
	}
	return nil
}

// DeleteServer 删除 MCP server（工具快照表 FK CASCADE 自动清理；绑定表 server_id 无 FK，由 service 层保证不悬挂）。
func (r *MCPRepository) DeleteServer(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.MCPServer{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMCPServerNotFound
	}
	return nil
}

// ——— mcp_server_tools ———

// FindToolByID 按 ID 查询工具快照。
func (r *MCPRepository) FindToolByID(ctx context.Context, id uint64) (*model.MCPServerTool, error) {
	var m model.MCPServerTool
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindToolByServerAndName 按 (server_id, tool_name) 查询工具快照。
func (r *MCPRepository) FindToolByServerAndName(ctx context.Context, serverID uint64, name string) (*model.MCPServerTool, error) {
	var m model.MCPServerTool
	if err := r.db.WithContext(ctx).Where("server_id = ? AND tool_name = ?", serverID, name).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ListToolsByServer 列出某 server 的全部工具快照（运营审核用，含未启用）。
func (r *MCPRepository) ListToolsByServer(ctx context.Context, serverID uint64) ([]model.MCPServerTool, error) {
	var items []model.MCPServerTool
	if err := r.db.WithContext(ctx).Where("server_id = ?", serverID).
		Order("tool_name ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListEnabledToolsByServerIDs 列出多个 server 的 enabled 工具快照（编排装配用）。
func (r *MCPRepository) ListEnabledToolsByServerIDs(ctx context.Context, serverIDs []uint64) ([]model.MCPServerTool, error) {
	if len(serverIDs) == 0 {
		return nil, nil
	}
	var items []model.MCPServerTool
	if err := r.db.WithContext(ctx).
		Where("server_id IN ? AND enabled = ?", serverIDs, true).
		Order("server_id ASC, tool_name ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// UpsertTool 插入或更新工具快照。
// 若已存在且 schema_hash 变化：覆盖定义并强制 enabled=0（待重审）；hash 不变则仅刷新描述/schema。
// 返回 changed=true 表示新工具或定义有变（hash 变）。
func (r *MCPRepository) UpsertTool(ctx context.Context, t *model.MCPServerTool) (changed bool, err error) {
	existing, ferr := r.FindToolByServerAndName(ctx, t.ServerID, t.ToolName)
	if ferr != nil {
		if !errors.Is(ferr, gorm.ErrRecordNotFound) {
			return false, ferr
		}
		// 新工具：默认 enabled=0 待审。
		t.Enabled = false
		if cerr := r.db.WithContext(ctx).Create(t).Error; cerr != nil {
			return false, cerr
		}
		return true, nil
	}
	updates := map[string]interface{}{
		"description":       t.Description,
		"input_schema_json": []byte(t.InputSchemaJSON),
		"schema_hash":       t.SchemaHash,
	}
	hashChanged := existing.SchemaHash != t.SchemaHash
	if hashChanged {
		// 定义变化 → 强制置未启用待重审（挡 rug-pull）。
		updates["enabled"] = false
	}
	if uerr := r.db.WithContext(ctx).Model(&model.MCPServerTool{}).
		Where("id = ?", existing.ID).Updates(updates).Error; uerr != nil {
		return false, uerr
	}
	return hashChanged, nil
}

// UpdateToolEnabled 审核启用/停用单个工具。
func (r *MCPRepository) UpdateToolEnabled(ctx context.Context, id uint64, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&model.MCPServerTool{}).
		Where("id = ?", id).Update("enabled", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMCPToolNotFound
	}
	return nil
}

// ——— agent_mcp_bindings ———

// ReplaceAgentBindings 覆盖某 Agent 的 MCP server 绑定（覆盖语义，空列表=全部解绑）。
func (r *MCPRepository) ReplaceAgentBindings(ctx context.Context, agentID uint64, serverIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).Delete(&model.AgentMCPBinding{}).Error; err != nil {
			return err
		}
		if len(serverIDs) == 0 {
			return nil
		}
		rows := make([]model.AgentMCPBinding, 0, len(serverIDs))
		for _, sid := range serverIDs {
			rows = append(rows, model.AgentMCPBinding{AgentID: agentID, ServerID: sid, Enabled: true})
		}
		return tx.Create(&rows).Error
	})
}

// ListServerIDsByAgent 列出某 Agent 已启用绑定的 MCP server_id。
func (r *MCPRepository) ListServerIDsByAgent(ctx context.Context, agentID uint64) ([]uint64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&model.AgentMCPBinding{}).
		Where("agent_id = ? AND enabled = ?", agentID, true).
		Order("server_id ASC").Pluck("server_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

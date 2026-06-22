package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/model"
)

// ErrAgentNotFound Agent 记录不存在（RowsAffected==0 守卫）。
var ErrAgentNotFound = errors.New("agent 不存在")

// AgentRepository Agent 及其 skill/plugin 绑定关系数据访问层。
type AgentRepository struct {
	db *gorm.DB
}

// NewAgentRepository 创建 Agent 仓库实例。
func NewAgentRepository(db *gorm.DB) *AgentRepository {
	return &AgentRepository{db: db}
}

// CreateWithBindings 在单事务内创建 Agent 并写入 skill/plugin 绑定。
// skillIDs/pluginIDs 为空则不建绑定。创建成功后回填 a.ID。
func (r *AgentRepository) CreateWithBindings(ctx context.Context, a *model.Agent, skillIDs, pluginIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(a).Error; err != nil {
			return err
		}
		return replaceBindingsTx(tx, a.ID, skillIDs, pluginIDs, true, true)
	})
}

// FindByID 按 ID 查询 Agent。
func (r *AgentRepository) FindByID(ctx context.Context, id uint64) (*model.Agent, error) {
	var a model.Agent
	if err := r.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// FindByCode 按官方编码查询 Agent（唯一性校验用，仅 code 非空时调用）。
func (r *AgentRepository) FindByCode(ctx context.Context, code string) (*model.Agent, error) {
	var a model.Agent
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListVisiblePaged 分页列出对某用户可见的 Agent：官方（official, active）+ 本人自建（全部状态）。
func (r *AgentRepository) ListVisiblePaged(ctx context.Context, userID uint64, offset, limit int) ([]model.Agent, int64, error) {
	cond := r.db.WithContext(ctx).Model(&model.Agent{}).
		Where("(owner_type = ? AND status = ?) OR (owner_type = ? AND owner_user_id = ?)",
			"official", "active", "user", userID)

	var total int64
	if err := cond.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Agent
	if err := cond.Order("sort_order ASC, id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListAdminPaged 管理端分页列出官方 Agent，支持 status 过滤（空不过滤）。
func (r *AgentRepository) ListAdminPaged(ctx context.Context, ownerType, status string, offset, limit int) ([]model.Agent, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Agent{})
	if ownerType != "" {
		query = query.Where("owner_type = ?", ownerType)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Agent
	if err := query.Order("sort_order ASC, id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Update 更新 Agent 字段（map 方式支持零值更新）。
func (r *AgentRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.Agent{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// UpdateWithBindings 在单事务内更新 Agent 字段，并按需重置 skill/plugin 绑定。
// replaceSkills/replacePlugins 为 true 时用对应 ID 列表覆盖（空列表=清空该类绑定）。
func (r *AgentRepository) UpdateWithBindings(ctx context.Context, id uint64, updates map[string]interface{}, skillIDs, pluginIDs []uint64, replaceSkills, replacePlugins bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			result := tx.Model(&model.Agent{}).Where("id = ?", id).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrAgentNotFound
			}
		}
		return replaceBindingsTx(tx, id, skillIDs, pluginIDs, replaceSkills, replacePlugins)
	})
}

// ReplaceSkillBindings 覆盖某 Agent 的 skill 绑定（管理端绑定/解绑接口）。
func (r *AgentRepository) ReplaceSkillBindings(ctx context.Context, agentID uint64, skillIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return replaceBindingsTx(tx, agentID, skillIDs, nil, true, false)
	})
}

// ReplacePluginBindings 覆盖某 Agent 的 plugin 绑定（管理端绑定/解绑接口）。
func (r *AgentRepository) ReplacePluginBindings(ctx context.Context, agentID uint64, pluginIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return replaceBindingsTx(tx, agentID, nil, pluginIDs, false, true)
	})
}

// Delete 删除 Agent（绑定表 FK ON DELETE CASCADE 自动清理）。
func (r *AgentRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.Agent{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// ListSkillIDs 列出某 Agent 已启用绑定的 skill_id。
func (r *AgentRepository) ListSkillIDs(ctx context.Context, agentID uint64) ([]uint64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&model.AgentSkillBinding{}).
		Where("agent_id = ? AND enabled = ?", agentID, true).
		Order("skill_id ASC").Pluck("skill_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// ListPluginIDs 列出某 Agent 已启用绑定的 plugin_id。
func (r *AgentRepository) ListPluginIDs(ctx context.Context, agentID uint64) ([]uint64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&model.AgentPluginBinding{}).
		Where("agent_id = ? AND enabled = ?", agentID, true).
		Order("plugin_id ASC").Pluck("plugin_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// replaceBindingsTx 在事务内覆盖 Agent 的 skill/plugin 绑定。
// replaceSkills/replacePlugins 控制是否处理对应类型（false 则跳过，保留原绑定）。
func replaceBindingsTx(tx *gorm.DB, agentID uint64, skillIDs, pluginIDs []uint64, replaceSkills, replacePlugins bool) error {
	if replaceSkills {
		if err := tx.Where("agent_id = ?", agentID).Delete(&model.AgentSkillBinding{}).Error; err != nil {
			return err
		}
		if len(skillIDs) > 0 {
			rows := make([]model.AgentSkillBinding, 0, len(skillIDs))
			for _, sid := range skillIDs {
				rows = append(rows, model.AgentSkillBinding{AgentID: agentID, SkillID: sid, Enabled: true})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
	}
	if replacePlugins {
		if err := tx.Where("agent_id = ?", agentID).Delete(&model.AgentPluginBinding{}).Error; err != nil {
			return err
		}
		if len(pluginIDs) > 0 {
			rows := make([]model.AgentPluginBinding, 0, len(pluginIDs))
			for _, pid := range pluginIDs {
				rows = append(rows, model.AgentPluginBinding{AgentID: agentID, PluginID: pid, Enabled: true})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

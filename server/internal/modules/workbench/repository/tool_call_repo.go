package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/workbench/model"
)

// ToolCallRepository 通用工具每用户每日调用计数（限流）数据访问层。
// 收口替代 PluginCallRepository：插件（toolType="plugin"）/ MCP（toolType="mcp"）/ 未来工具源共用。
type ToolCallRepository struct {
	db *gorm.DB
}

// NewToolCallRepository 创建计数仓库实例。
func NewToolCallRepository(db *gorm.DB) *ToolCallRepository {
	return &ToolCallRepository{db: db}
}

// IncrementIfUnderLimit 原子地为 (toolType,toolID,user,今日) 计数 +1，但仅当未达 limit 时。
// limit<=0 表示不限（恒放行并计数）。返回 allowed=true 表示本次放行（且已计数）；
// allowed=false 表示已达上限（未计数）。
//
// 并发安全：INSERT IGNORE 幂等建当日行（count=0），再 FOR UPDATE 行锁串行化自增，杜绝超限放行。
func (r *ToolCallRepository) IncrementIfUnderLimit(ctx context.Context, toolType string, toolID, userID uint64, limit int) (bool, error) {
	today := time.Now().Format("2006-01-02")
	allowed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) 幂等确保当日行存在（count=0），避免后续 FOR UPDATE 落空 + 并发插入冲突。
		if err := tx.Exec(
			"INSERT IGNORE INTO tool_daily_call_logs (tool_type, tool_id, user_id, call_date, count) VALUES (?, ?, ?, ?, 0)",
			toolType, toolID, userID, today,
		).Error; err != nil {
			return err
		}
		// 2) 行锁读取当日计数。
		var rec model.ToolDailyCallLog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tool_type = ? AND tool_id = ? AND user_id = ? AND call_date = ?", toolType, toolID, userID, today).
			First(&rec).Error; err != nil {
			return err
		}
		// 3) 限额校验。
		if limit > 0 && rec.Count >= limit {
			allowed = false
			return nil
		}
		if err := tx.Model(&model.ToolDailyCallLog{}).Where("id = ?", rec.ID).
			Update("count", gorm.Expr("count + 1")).Error; err != nil {
			return err
		}
		allowed = true
		return nil
	})
	return allowed, err
}

package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/workbench/model"
)

// PluginCallRepository 付费插件每用户每日调用计数（D3 限流）数据访问层。
type PluginCallRepository struct {
	db *gorm.DB
}

// NewPluginCallRepository 创建计数仓库实例。
func NewPluginCallRepository(db *gorm.DB) *PluginCallRepository {
	return &PluginCallRepository{db: db}
}

// IncrementIfUnderLimit 原子地为 (plugin,user,今日) 计数 +1，但仅当未达 limit 时。
// limit<=0 表示不限（恒放行并计数）。返回 allowed=true 表示本次放行（且已计数）；
// allowed=false 表示已达上限（未计数）。
//
// 并发安全：INSERT IGNORE 幂等建当日行（count=0），再 FOR UPDATE 行锁串行化自增，杜绝超限放行。
func (r *PluginCallRepository) IncrementIfUnderLimit(ctx context.Context, pluginID, userID uint64, limit int) (bool, error) {
	today := time.Now().Format("2006-01-02")
	allowed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) 幂等确保当日行存在（count=0），避免后续 FOR UPDATE 落空 + 并发插入冲突。
		if err := tx.Exec(
			"INSERT IGNORE INTO plugin_daily_call_logs (plugin_id, user_id, call_date, count) VALUES (?, ?, ?, 0)",
			pluginID, userID, today,
		).Error; err != nil {
			return err
		}
		// 2) 行锁读取当日计数。
		var rec model.PluginDailyCallLog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("plugin_id = ? AND user_id = ? AND call_date = ?", pluginID, userID, today).
			First(&rec).Error; err != nil {
			return err
		}
		// 3) 限额校验。
		if limit > 0 && rec.Count >= limit {
			allowed = false
			return nil
		}
		if err := tx.Model(&model.PluginDailyCallLog{}).Where("id = ?", rec.ID).
			Update("count", gorm.Expr("count + 1")).Error; err != nil {
			return err
		}
		allowed = true
		return nil
	})
	return allowed, err
}

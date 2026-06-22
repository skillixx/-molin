package model

import "time"

// PluginDailyCallLog 付费插件每用户每日调用计数（D3 限流），对应 plugin_daily_call_logs 表。
type PluginDailyCallLog struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	PluginID  uint64    `gorm:"column:plugin_id;not null;uniqueIndex:uk_plugin_user_date" json:"plugin_id"`
	UserID    uint64    `gorm:"column:user_id;not null;uniqueIndex:uk_plugin_user_date" json:"user_id"`
	CallDate  string    `gorm:"column:call_date;type:date;not null;uniqueIndex:uk_plugin_user_date" json:"call_date"`
	Count     int       `gorm:"not null;default:0" json:"count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (PluginDailyCallLog) TableName() string { return "plugin_daily_call_logs" }

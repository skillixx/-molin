package model

import "time"

// APIKey 平台 API Key（sk），让外部程序 / Agent 凭 sk 调用模型门面。
// 沿用 Refresh Token「只存 HMAC、明文只回一次、支持吊销」安全模式：
// DB 只存 KeyHash（HMAC-SHA256(明文, API_KEY_HMAC_SECRET) 的 hex），绝不存明文；
// 明文 sk 仅在签发时返回一次。
//
// 安全红线：KeyHash 字段标记 json:"-"，绝不序列化到任何响应；
// 对外只暴露 KeyPrefix（展示用前缀，如 sk-molin-AbCd，不可反推）。
type APIKey struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_api_keys_id_user,priority:1;uniqueIndex:uk_api_keys_id_project_user,priority:1"`
	UserID        uint64     `gorm:"not null;uniqueIndex:uk_api_keys_id_user,priority:2;uniqueIndex:uk_api_keys_id_project_user,priority:3;index:idx_api_keys_user,priority:1"`
	ProjectID     *uint64    `gorm:"uniqueIndex:uk_api_keys_id_project_user,priority:2;index:idx_api_keys_project_status,priority:1"`
	KeyPrefix     string     `gorm:"size:32;not null"`                                                                                                // 展示用前缀，如 sk-molin-AbCd
	KeyHash       string     `gorm:"size:128;not null;uniqueIndex" json:"-"`                                                                          // HMAC-SHA256(明文)，绝不序列化
	Name          string     `gorm:"size:128;not null;default:''"`                                                                                    // 用户备注名
	BillingMode   string     `gorm:"size:16;not null;default:postpaid"`                                                                               // postpaid(钱包) / prepaid(套餐额度)
	SourceID      *uint64    `gorm:"column:source_id"`                                                                                                // prepaid=entitlement_id；postpaid=NULL
	ModelScope    string     `gorm:"size:512;not null;default:''"`                                                                                    // 逗号分隔 logical_model_code；空=不限
	ScopeMode     string     `gorm:"size:16;not null;default:legacy_all"`                                                                             // all / allowlist / legacy_all
	Status        string     `gorm:"size:16;not null;default:active;index:idx_api_keys_user,priority:2;index:idx_api_keys_project_status,priority:2"` // active / revoked
	ExpiresAt     *time.Time // Project SK 的可选过期时间；到期后鉴权立即失败
	RotatedFromID *uint64    `gorm:"column:rotated_from_id"` // 轮换来源，只保存内部关联，不暴露明文
	LastUsedAt    *time.Time // 最近一次使用时间
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TableName 指定表名为 api_keys。
func (APIKey) TableName() string {
	return "api_keys"
}

// APIKeyModelScope 保存 Project SK 的显式模型授权。空 allowlist 在数据库中没有记录，语义为拒绝全部模型。
type APIKeyModelScope struct {
	ID               uint64 `gorm:"primaryKey;autoIncrement"`
	APIKeyID         uint64 `gorm:"not null;uniqueIndex:uk_api_key_model_scope,priority:1"`
	ProjectID        uint64 `gorm:"not null;index:idx_api_key_model_scope_project,priority:1"`
	UserID           uint64 `gorm:"not null;index:idx_api_key_model_scope_project,priority:2"`
	LogicalModelCode string `gorm:"size:128;not null;uniqueIndex:uk_api_key_model_scope,priority:2"`
	CreatedAt        time.Time
}

// TableName 指定 Project SK 模型权限表名。
func (APIKeyModelScope) TableName() string { return "api_key_model_scopes" }

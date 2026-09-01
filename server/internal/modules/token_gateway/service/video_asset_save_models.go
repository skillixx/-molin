package service

import (
	"encoding/json"
	"github.com/shopspring/decimal"
	"time"
)

// 协调模型全部隐藏JSON；复制计划与存储位置只能用于服务端恢复，不能作为普通响应。
type videoAssetSave struct {
	TaskID                 uint64          `json:"-"`
	PublicID               string          `gorm:"primaryKey" json:"-"`
	AttemptNo              uint64          `gorm:"default:1" json:"-"`
	PreviousSaveID         *string         `json:"-"`
	RequestID              string          `json:"-"`
	UserID                 uint64          `json:"-"`
	ProjectID              uint64          `json:"-"`
	APIKeyID               *uint64         `json:"-"`
	Status                 string          `json:"-"`
	VersionNo              uint64          `json:"-"`
	StorageEntitlementID   uint64          `json:"-"`
	StorageEntitlementType string          `json:"-"`
	StorageProductID       uint64          `json:"-"`
	QuotaUnit              string          `json:"-"`
	QuotaAmount            decimal.Decimal `json:"-"`
	TotalBytes             uint64          `json:"-"`
	PolicyVersion          string          `json:"-"`
	PlanJSON               json.RawMessage `json:"-"`
	PlanSHA256             string          `json:"-"`
	SavedUserAssetID       *uint64         `json:"-"`
	CreatedAt              time.Time       `json:"-"`
	CompletedAt            *time.Time      `json:"-"`
	CleanupPolicyVersion   *string         `json:"-"`
	CleanupReason          *string         `json:"-"`
	CleanupEligibleAt      *time.Time      `json:"-"`
	CleanupStartedAt       *time.Time      `json:"-"`
	CleanupFinishedAt      *time.Time      `json:"-"`
	CleanupProofSHA256     *string         `json:"-"`
}

func (videoAssetSave) TableName() string { return "ai_video_asset_saves" }

type videoAssetSaveCommand struct {
	UserID, ProjectID, TaskID uint64
	SavePublicID              string `json:"-"`
	APIKeyID                  *uint64
	CommandKeyHash            string
	CreatedAt                 time.Time
}

func (videoAssetSaveCommand) TableName() string { return "ai_video_asset_save_commands" }

// 每份计划同时固定源的身份/版本/安全摘要和独立目标，重试不能切换到新对象。
type videoAssetSaveItem struct {
	AssetID        uint64 `json:"asset_id"`
	PublicID       string `json:"public_id"`
	Role           string `json:"role"`
	VersionNo      uint64 `json:"version_no"`
	SHA256         string `json:"sha256"`
	Size           uint64 `json:"size"`
	SourceBucket   string `json:"source_bucket"`
	SourceKey      string `json:"source_key"`
	TargetBucket   string `json:"target_bucket"`
	TargetKey      string `json:"target_key"`
	MetadataSHA256 string `json:"metadata_sha256"`
}

type VideoAssetSaveReply struct {
	AssetID     string `json:"asset_id"`
	VideoID     string `json:"video_id"`
	RequestID   string `json:"request_id"`
	UserAssetID uint64 `json:"user_asset_id"`
	Status      string `json:"status"`
	SizeBytes   uint64 `json:"size_bytes"`
	Idempotent  bool   `json:"idempotent"`
}

package service

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

type VideoAdminInputFilter struct {
	Page, PageSize                               int
	UserID, ProjectID                            uint64
	LifecycleState, SourceType, ModerationStatus string
}

// 管理输入元数据不是引用许可；来源公开ID与原Key必须由可信来源链解析，绝不暴露存储位置。
type VideoAdminInputDetails struct {
	InputAssetID            string     `json:"input_asset_id"`
	UserID                  uint64     `json:"user_id"`
	ProjectID               uint64     `json:"project_id"`
	APIKeyID                *uint64    `json:"api_key_id"`
	SourceType              string     `json:"source_type"`
	UploadSessionID         *string    `json:"upload_session_id"`
	SourceAssetID           *string    `json:"source_asset_id"`
	LifecycleState          string     `json:"lifecycle_state"`
	VersionNo               uint64     `json:"version_no"`
	MIMEType                *string    `json:"mime_type"`
	SizeBytes               *uint64    `json:"size_bytes"`
	Width                   *uint32    `json:"width"`
	Height                  *uint32    `json:"height"`
	ModerationStatus        string     `json:"moderation_status"`
	ModerationPolicyVersion *string    `json:"moderation_policy_version"`
	ExpiresAt               time.Time  `json:"expires_at"`
	LegalHold               bool       `json:"legal_hold"`
	DeleteRequestedAt       *time.Time `json:"delete_requested_at"`
	PendingDeleteAt         *time.Time `json:"pending_delete_at"`
	DeletedAt               *time.Time `json:"deleted_at"`
	CreatedAt               time.Time  `json:"created_at"`
}

type VideoAdminInputPage struct {
	Items    []VideoAdminInputDetails `json:"items"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Total    int64                    `json:"total"`
}

func videoAdminInputMetadata(a model.AIGatewayInputAsset, key *uint64, upload, source *string) VideoAdminInputDetails {
	return VideoAdminInputDetails{InputAssetID: a.PublicID, UserID: a.UserID, ProjectID: a.ProjectID, APIKeyID: key, SourceType: a.SourceType, UploadSessionID: upload, SourceAssetID: source, LifecycleState: a.LifecycleState, VersionNo: a.VersionNo, MIMEType: a.MIMEType, SizeBytes: a.SizeBytes, Width: a.Width, Height: a.Height, ModerationStatus: a.ModerationStatus, ModerationPolicyVersion: a.ModerationPolicyVersion, ExpiresAt: a.ExpiresAt, LegalHold: a.LegalHold, DeleteRequestedAt: a.DeleteRequestedAt, PendingDeleteAt: a.PendingDeleteAt, DeletedAt: a.DeletedAt, CreatedAt: a.CreatedAt}
}

func validVideoAdminInputFilter(f VideoAdminInputFilter) bool {
	if f.Page < 1 || f.Page > 10000 || f.PageSize < 1 || f.PageSize > 100 {
		return false
	}
	switch f.LifecycleState {
	case "", "pending", "normalizing", "moderating", "ready", "rejected", "quarantined", "pending_delete", "expiring", "deleting", "deleted", "delete_failed":
	default:
		return false
	}
	switch f.SourceType {
	case "", model.AIUploadSourcePlatformPresigned, model.AIUploadSourceOpenAIInlineMultipart, model.AIInputSourceGatewayAssetSnapshot:
	default:
		return false
	}
	switch f.ModerationStatus {
	case "", model.AIModerationPending, model.AIModerationPassed, model.AIModerationRejected, model.AIModerationError:
		return true
	}
	return false
}

func videoAdminInputSource(tx *gorm.DB, a model.AIGatewayInputAsset) (*uint64, *string, *string, error) {
	var key *uint64
	var uploadID, sourceID *string
	switch a.SourceType {
	case model.AIUploadSourcePlatformPresigned, model.AIUploadSourceOpenAIInlineMultipart:
		if a.UploadSessionID == nil || a.SourceGatewayAssetID != nil {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
		var s model.AIUploadSession
		if err := tx.Select("id,public_id,user_id,project_id,api_key_id,source_type,purpose,final_input_asset_id").Where("id=? AND user_id=? AND project_id=?", *a.UploadSessionID, a.UserID, a.ProjectID).Take(&s).Error; err != nil {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
		if !videoBillingPublicID.MatchString(s.PublicID) || s.SourceType != a.SourceType || s.Purpose != model.AIUploadPurposeVideoReferenceImage || (s.FinalInputAssetID != nil && *s.FinalInputAssetID != a.ID) {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
		key, uploadID = s.APIKeyID, &s.PublicID
	case model.AIInputSourceGatewayAssetSnapshot:
		if a.UploadSessionID != nil || a.SourceGatewayAssetID == nil {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
		var source struct {
			PublicID string
			APIKeyID *uint64
		}
		// 历史来源可过期或删除，但原图片、Task和Request的归属必须一致；不借用后续视频任务推断Key。
		err := tx.Table("ai_gateway_assets a").Select("a.public_id,t.api_key_id").Joins("JOIN ai_gateway_tasks t ON t.id=a.task_id AND t.request_id=a.request_id AND t.user_id=a.user_id AND t.project_id=a.project_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code").Where("a.id=? AND a.user_id=? AND a.project_id=? AND a.modality='image' AND t.capability='image.generate' AND t.operation IS NULL AND r.modality='image' AND r.capability='image.generate' AND r.operation IS NULL", *a.SourceGatewayAssetID, a.UserID, a.ProjectID).Take(&source).Error
		if err != nil || !videoBillingPublicID.MatchString(source.PublicID) {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
		key, sourceID = source.APIKeyID, &source.PublicID
	default:
		return nil, nil, nil, ErrVideoAccessUnavailable
	}
	// 停用Key不影响管理历史；非空Key仍须证明属于原用户和Project，缺失不能冒充JWT来源。
	if key != nil {
		var count int64
		if err := tx.Table("api_keys").Where("id=? AND user_id=? AND project_id=?", *key, a.UserID, a.ProjectID).Count(&count).Error; err != nil || count != 1 {
			return nil, nil, nil, ErrVideoAccessUnavailable
		}
	}
	return key, uploadID, sourceID, nil
}

func (s *VideoAdminService) ListInputs(ctx context.Context, caller VideoCaller, f VideoAdminInputFilter) (*VideoAdminInputPage, error) {
	if s == nil || s.app == nil || s.app.db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := &VideoAdminInputPage{Items: []VideoAdminInputDetails{}, Page: f.Page, PageSize: f.PageSize}
	err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:view"); err != nil {
			return err
		}
		if !validVideoAdminInputFilter(f) {
			return ErrVideoAdminQuery
		}
		query := func() *gorm.DB {
			q := tx.Model(&model.AIGatewayInputAsset{})
			if f.UserID != 0 {
				q = q.Where("user_id=?", f.UserID)
			}
			if f.ProjectID != 0 {
				q = q.Where("project_id=?", f.ProjectID)
			}
			if f.LifecycleState != "" {
				q = q.Where("lifecycle_state=?", f.LifecycleState)
			}
			if f.SourceType != "" {
				q = q.Where("source_type=?", f.SourceType)
			}
			if f.ModerationStatus != "" {
				q = q.Where("moderation_status=?", f.ModerationStatus)
			}
			return q
		}
		// 元数据不生成交付许可，不需要锁业务资产；RR快照统一计数、条目和来源，最终管理员认证仍是当前读。
		if err := query().Count(&result.Total).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var assets []model.AIGatewayInputAsset
		if err := query().Order("created_at DESC,public_id DESC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&assets).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, a := range assets {
			key, upload, source, err := videoAdminInputSource(tx, a)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, videoAdminInputMetadata(a, key, upload, source))
		}
		return s.authorizeTx(ctx, tx, caller, "ai_gateway:view")
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	return result, nil
}

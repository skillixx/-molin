package service

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

type VideoAdminOutputFilter struct {
	Page, PageSize                                                          int
	UserID, ProjectID                                                       uint64
	LifecycleState, Role, ModerationStatus, DisputeStatus, Model, Operation string
}

// 管理输出仅展示原资产与安全事实；审核副本和安全派生物可见元数据，但没有正文或下载能力。
type VideoAdminOutputDetails struct {
	AssetID                 string     `json:"asset_id"`
	VideoID                 string     `json:"video_id"`
	RequestID               string     `json:"request_id"`
	UserID                  uint64     `json:"user_id"`
	ProjectID               uint64     `json:"project_id"`
	APIKeyID                *uint64    `json:"api_key_id"`
	Model                   string     `json:"model"`
	Operation               string     `json:"operation"`
	Role                    string     `json:"role"`
	ParentAssetID           *string    `json:"parent_asset_id"`
	LifecycleState          string     `json:"lifecycle_state"`
	VersionNo               uint64     `json:"version_no"`
	MIMEType                *string    `json:"mime_type"`
	SizeBytes               *uint64    `json:"size_bytes"`
	Width                   *uint32    `json:"width"`
	Height                  *uint32    `json:"height"`
	ModerationStatus        string     `json:"moderation_status"`
	ModerationPolicyVersion *string    `json:"moderation_policy_version"`
	ExplicitLabelStatus     string     `json:"explicit_label_status"`
	ExplicitLabelVersion    *string    `json:"explicit_label_version"`
	ImplicitLabelStatus     string     `json:"implicit_label_status"`
	ImplicitLabelVersion    *string    `json:"implicit_label_version"`
	LegalHold               bool       `json:"legal_hold"`
	DisputeStatus           string     `json:"dispute_status"`
	ExpiresAt               time.Time  `json:"expires_at"`
	DeletedAt               *time.Time `json:"deleted_at"`
	MediaDeletedAt          *time.Time `json:"media_deleted_at"`
	CreatedAt               time.Time  `json:"created_at"`
}

type VideoAdminOutputPage struct {
	Items    []VideoAdminOutputDetails `json:"items"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
	Total    int64                     `json:"total"`
}

func validVideoAdminOutputFilter(f VideoAdminOutputFilter) bool {
	if f.Page < 1 || f.Page > 10000 || f.PageSize < 1 || f.PageSize > 100 {
		return false
	}
	if f.Model != "" && !videoAdminModelCode.MatchString(f.Model) {
		return false
	}
	if f.Operation != "" && f.Operation != model.AIVideoOperationTextToVideo && f.Operation != model.AIVideoOperationImageToVideo {
		return false
	}
	switch f.LifecycleState {
	case "", "temporary", "available", "quarantined", "expiring", "deleting", "deleted", "delete_failed":
	default:
		return false
	}
	switch f.Role {
	case "", "content", "cover", "preview", "thumbnail", "moderation_copy", "derived":
	default:
		return false
	}
	switch f.ModerationStatus {
	case "", "pending", "passed", "rejected", "error":
	default:
		return false
	}
	switch f.DisputeStatus {
	case "", "none", "open", "resolved":
		return true
	}
	return false
}

func videoAdminOutputDetails(tx *gorm.DB, a model.AIImageAsset) (VideoAdminOutputDetails, error) {
	d := VideoAdminOutputDetails{AssetID: a.PublicID, RequestID: a.RequestID, UserID: a.UserID, ProjectID: a.ProjectID, Role: a.AssetRole, LifecycleState: a.LifecycleState, VersionNo: a.VersionNo, MIMEType: a.MIMEType, SizeBytes: a.SizeBytes, Width: a.Width, Height: a.Height, ModerationStatus: a.ModerationStatus, ModerationPolicyVersion: a.ModerationPolicyVersion, ExplicitLabelStatus: a.ExplicitLabelStatus, ExplicitLabelVersion: a.ExplicitLabelVersion, ImplicitLabelStatus: a.ImplicitLabelStatus, ImplicitLabelVersion: a.ImplicitLabelVersion, LegalHold: a.LegalHold, DisputeStatus: a.DisputeStatus, ExpiresAt: a.ExpiresAt, DeletedAt: a.DeletedAt, MediaDeletedAt: a.MediaDeletedAt, CreatedAt: a.CreatedAt}
	if !videoBillingPublicID.MatchString(a.PublicID) || a.VersionNo == 0 || (a.AssetRole == "content") != (a.ParentAssetID == nil) {
		return d, ErrVideoAccessUnavailable
	}
	switch a.AssetRole {
	case "content", "cover", "preview", "thumbnail", "moderation_copy", "derived":
	default:
		return d, ErrVideoAccessUnavailable
	}
	var task struct {
		PublicID, LogicalModelCode, Operation string
		APIKeyID                              *uint64
	}
	// 原Request与Task必须同时证明模态、能力、操作和Key；不能使用管理员身份或当前模型发布状态重写历史。
	err := tx.Table("ai_gateway_tasks t").Select("t.public_id,t.logical_model_code,t.operation,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("t.id=? AND t.request_id=? AND t.user_id=? AND t.project_id=? AND t.capability='video.generate' AND t.operation IN ('text_to_video','image_to_video') AND r.modality='video' AND r.capability='video.generate'", a.TaskID, a.RequestID, a.UserID, a.ProjectID).Take(&task).Error
	if err != nil || !videoBillingPublicID.MatchString(task.PublicID) {
		return d, ErrVideoAccessUnavailable
	}
	d.VideoID, d.Model, d.Operation, d.APIKeyID = task.PublicID, task.LogicalModelCode, task.Operation, task.APIKeyID
	if task.APIKeyID != nil {
		var count int64
		if err := tx.Table("api_keys").Where("id=? AND user_id=? AND project_id=?", *task.APIKeyID, a.UserID, a.ProjectID).Count(&count).Error; err != nil || count != 1 {
			return d, ErrVideoAccessUnavailable
		}
	}
	if a.ParentAssetID != nil {
		var parent struct{ PublicID string }
		if err := tx.Table("ai_gateway_assets").Select("public_id").Where("id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=? AND modality='video' AND asset_role='content' AND parent_asset_id IS NULL", *a.ParentAssetID, a.TaskID, a.RequestID, a.UserID, a.ProjectID).Take(&parent).Error; err != nil || !videoBillingPublicID.MatchString(parent.PublicID) {
			return d, ErrVideoAccessUnavailable
		}
		d.ParentAssetID = &parent.PublicID
	}
	return d, nil
}

func (s *VideoAdminService) ListOutputs(ctx context.Context, caller VideoCaller, f VideoAdminOutputFilter) (*VideoAdminOutputPage, error) {
	if s == nil || s.app == nil || s.app.db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := &VideoAdminOutputPage{Items: []VideoAdminOutputDetails{}, Page: f.Page, PageSize: f.PageSize}
	err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:view"); err != nil {
			return err
		}
		if !validVideoAdminOutputFilter(f) {
			return ErrVideoAdminQuery
		}
		query := func() *gorm.DB {
			q := tx.Table("ai_gateway_assets a").Where("a.modality='video'")
			if f.UserID != 0 {
				q = q.Where("a.user_id=?", f.UserID)
			}
			if f.ProjectID != 0 {
				q = q.Where("a.project_id=?", f.ProjectID)
			}
			if f.LifecycleState != "" {
				q = q.Where("a.lifecycle_state=?", f.LifecycleState)
			}
			if f.Role != "" {
				q = q.Where("a.asset_role=?", f.Role)
			}
			if f.ModerationStatus != "" {
				q = q.Where("a.moderation_status=?", f.ModerationStatus)
			}
			if f.DisputeStatus != "" {
				q = q.Where("a.dispute_status=?", f.DisputeStatus)
			}
			if f.Model != "" || f.Operation != "" {
				q = q.Joins("LEFT JOIN ai_gateway_tasks selected_task ON selected_task.id=a.task_id")
				if f.Model != "" {
					q = q.Where("selected_task.logical_model_code=?", f.Model)
				}
				if f.Operation != "" {
					q = q.Where("selected_task.operation=?", f.Operation)
				}
			}
			return q
		}
		// 没有交付或钱包副作用；RR统一分页与原关联快照，异常来源导致整页503而非部分展示。
		if err := query().Count(&result.Total).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var assets []model.AIImageAsset
		if err := query().Select("a.*").Order("a.created_at DESC,a.public_id DESC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&assets).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, a := range assets {
			d, err := videoAdminOutputDetails(tx, a)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, d)
		}
		return s.authorizeTx(ctx, tx, caller, "ai_gateway:view")
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	return result, nil
}

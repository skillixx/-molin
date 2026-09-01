package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 生命周期仅描述已归属资产的低敏事实；隔离或删除记录可见不等于正文可下载。
type VideoAssetLifecycle struct {
	AssetID             string     `json:"asset_id"`
	VideoID             string     `json:"video_id"`
	RequestID           string     `json:"request_id"`
	Role                string     `json:"role"`
	ParentAssetID       *string    `json:"parent_asset_id"`
	VersionNo           uint64     `json:"version_no"`
	LifecycleState      string     `json:"lifecycle_state"`
	ExpiresAt           time.Time  `json:"expires_at"`
	MediaDeleted        bool       `json:"media_deleted"`
	MediaDeletedAt      *time.Time `json:"media_deleted_at"`
	TaskMediaDeleted    bool       `json:"task_media_deleted"`
	DeletionStatus      *string    `json:"deletion_status"`
	ModerationStatus    string     `json:"moderation_status"`
	ExplicitLabelStatus string     `json:"explicit_label_status"`
	ImplicitLabelStatus string     `json:"implicit_label_status"`
	LegalHold           bool       `json:"legal_hold"`
	DisputeStatus       string     `json:"dispute_status"`
	ExecutionStatus     string     `json:"execution_status"`
	BillingStatus       string     `json:"billing_status"`
	DeliveryStatus      string     `json:"delivery_status"`
	CanDownload         bool       `json:"can_download"`
}

func (s *VideoHTTPService) GetAssetLifecycle(ctx context.Context, caller VideoCaller, id string) (*VideoAssetLifecycle, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if !videoBillingPublicID.MatchString(id) || caller.UserID == 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	var result *VideoAssetLifecycle
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity struct{ PublicID string }
		// 先在当前用户/Project/来源Key范围内解析；审核副本不属于用户交付资产，统一不存在。
		q := videoTaskOwnerQuery(tx, caller).Joins("JOIN ai_gateway_assets a ON a.task_id=t.id AND a.request_id=t.request_id AND a.user_id=t.user_id AND a.project_id=t.project_id")
		if err := q.Select("t.public_id").Where("a.public_id=? AND a.modality='video' AND a.asset_role IN ('content','cover','preview','thumbnail','derived') AND (a.asset_role<>'derived' OR a.source='derived')", id).Take(&identity).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
		}
		task, owner, err := s.taskForPlatformTx(ctx, tx, caller, identity.PublicID, false)
		if err != nil {
			return err
		}
		if task.Operation == nil {
			return ErrVideoAccessUnavailable
		}
		if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		// 使用原G5财务→资产锁序与完整交付判断，不能凭一行available状态宣称可下载。
		detail, err := s.taskDetailsTx(ctx, tx, task, owner)
		if err != nil {
			return err
		}
		var asset model.AIImageAsset
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("public_id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=? AND modality='video'", id, task.ID, task.RequestID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
		}
		// 解析身份与取得锁之间可能有更新，锁定后再次确认公开角色，不能沿用首次筛选结果。
		switch asset.AssetRole {
		case "content", "cover", "preview", "thumbnail":
		case "derived":
			if asset.Source != "derived" {
				return repository.ErrVideoTaskNotFound
			}
		default:
			return repository.ErrVideoTaskNotFound
		}
		if (asset.AssetRole == "content") != (asset.ParentAssetID == nil) {
			return ErrVideoAccessUnavailable
		}
		result = &VideoAssetLifecycle{AssetID: asset.PublicID, VideoID: task.PublicID, RequestID: task.RequestID, Role: asset.AssetRole, VersionNo: asset.VersionNo, LifecycleState: asset.LifecycleState, ExpiresAt: asset.ExpiresAt, MediaDeleted: asset.MediaDeletedAt != nil || asset.DeletedAt != nil, MediaDeletedAt: asset.MediaDeletedAt, TaskMediaDeleted: detail.MediaDeleted, ModerationStatus: asset.ModerationStatus, ExplicitLabelStatus: asset.ExplicitLabelStatus, ImplicitLabelStatus: asset.ImplicitLabelStatus, LegalHold: asset.LegalHold, DisputeStatus: asset.DisputeStatus, ExecutionStatus: task.Status, BillingStatus: task.BillingStatus, DeliveryStatus: task.DeliveryStatus}
		if asset.ParentAssetID != nil {
			var parent struct{ PublicID string }
			if err := tx.Table("ai_gateway_assets").Clauses(clause.Locking{Strength: "SHARE"}).Select("public_id").Where("id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=? AND modality='video' AND asset_role='content'", *asset.ParentAssetID, task.ID, task.RequestID, owner.UserID, owner.ProjectID).Take(&parent).Error; err != nil {
				return ErrVideoAccessUnavailable
			}
			result.ParentAssetID = &parent.PublicID
		}
		var deletion struct{ Status string }
		err = tx.Table("ai_video_media_deletions").Select("status").Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Take(&deletion).Error
		if err == nil {
			result.DeletionStatus = &deletion.Status
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVideoAccessUnavailable
		} else {
			err = tx.Table("ai_video_asset_deletions").Select("status").Where("asset_id=? AND task_id=? AND user_id=? AND project_id=?", asset.ID, task.ID, owner.UserID, owner.ProjectID).Take(&deletion).Error
			if err == nil {
				result.DeletionStatus = &deletion.Status
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrVideoAccessUnavailable
			}
		}
		// 显式装配短签名后，JWT及普通派生物也有平台读取入口；缺签名配置仍保留原v1正文能力。
		hasReader := (caller.APIKeyID != 0 && asset.AssetRole == "content") || (len(s.downloadSecret) == 32 && videoPublicDownloadAsset(&asset))
		result.CanDownload = hasReader && s.contentStore != nil && detail.CanDeliver && !detail.MediaDeleted && result.DeletionStatus == nil && asset.LifecycleState == "available" && asset.ExpiresAt.After(time.Now().UTC())
		if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		if result.CanDownload {
			// G5已按原锁序锁住完整六资产，等待后置授权期间其他角色也可能到期。
			// 重新读取这些锁定记录，并在读取完成后取时钟；只复查时间，不重建财务结论。
			var deadlines []struct{ ExpiresAt time.Time }
			if err := tx.Model(&model.AIImageAsset{}).Select("expires_at").Where("request_id=?", task.RequestID).Find(&deadlines).Error; err != nil {
				return ErrVideoAccessUnavailable
			}
			if len(deadlines) != 6 {
				return ErrVideoAccessUnavailable
			}
			now := time.Now().UTC()
			for _, row := range deadlines {
				if !row.ExpiresAt.After(now) {
					result.CanDownload = false
				}
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return result, nil
}

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 输入详情只提供生命周期元数据；不携带对象位置、媒体能力、内部来源ID或保全案件信息。
type VideoInputDetails struct {
	InputAssetID   string    `json:"input_asset_id"`
	SourceType     string    `json:"source_type"`
	LifecycleState string    `json:"lifecycle_state"`
	MIMEType       *string   `json:"mime_type"`
	SizeBytes      *uint64   `json:"size_bytes"`
	Width          *uint32   `json:"width"`
	Height         *uint32   `json:"height"`
	ExpiresAt      time.Time `json:"expires_at"`
	VersionNo      uint64    `json:"version_no"`
	CanReference   bool      `json:"can_reference"`
}

type VideoInputPage struct {
	Items    []VideoInputDetails `json:"items"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Total    int64               `json:"total"`
}

// 快照前置条件与G6报价/生成共用；true不是具体模型、权利、预算或钱包放行结果。
func videoHTTPInputReferenceable(asset model.AIGatewayInputAsset, now time.Time) bool {
	if asset.ID == 0 || asset.VersionNo == 0 || asset.LifecycleState != model.AIInputAssetReady || asset.ModerationStatus != model.AIModerationPassed || asset.LegalHold || !asset.ExpiresAt.After(now) || asset.DeleteRequestedAt != nil || asset.PendingDeleteAt != nil || asset.DeletedAt != nil {
		return false
	}
	return videoHTTPInputSnapshotWellFormed(asset)
}

// 规格完整性与使用资格分开；执行读取仍须先取得已有绑定及删除凭据，不能单凭本检查授予访问。
func videoHTTPInputSnapshotWellFormed(asset model.AIGatewayInputAsset) bool {
	if asset.ID == 0 || asset.VersionNo == 0 {
		return false
	}
	if !lowerHex64.MatchString(asset.OriginalSHA256) || asset.NormalizedSHA256 == nil || !lowerHex64.MatchString(*asset.NormalizedSHA256) || asset.Bucket == nil || strings.TrimSpace(*asset.Bucket) == "" || asset.ObjectKey == nil || strings.TrimSpace(*asset.ObjectKey) == "" || asset.ModerationPolicyVersion == nil || !videoIntentPolicyCode.MatchString(*asset.ModerationPolicyVersion) {
		return false
	}
	if asset.MIMEType == nil || *asset.MIMEType != "image/png" || asset.SizeBytes == nil || *asset.SizeBytes == 0 || *asset.SizeBytes > uint64(videoUploadMaxBytes) || asset.Width == nil || asset.Height == nil || *asset.Width < 640 || *asset.Width > 4096 || *asset.Height < 640 || *asset.Height > 4096 {
		return false
	}
	w, h := uint64(*asset.Width), uint64(*asset.Height)
	return w*h <= 16777216 && w*2 >= h && w <= h*2
}

func videoInputDetails(asset model.AIGatewayInputAsset, now time.Time) VideoInputDetails {
	return VideoInputDetails{InputAssetID: asset.PublicID, SourceType: asset.SourceType, LifecycleState: asset.LifecycleState, MIMEType: asset.MIMEType, SizeBytes: asset.SizeBytes, Width: asset.Width, Height: asset.Height, ExpiresAt: asset.ExpiresAt, VersionNo: asset.VersionNo, CanReference: videoHTTPInputReferenceable(asset, now)}
}

// 查询、计数和详情采用与可信报价输入一致的来源Key及来源图片可见性限制。
func videoInputMetadataQuery(tx *gorm.DB, caller VideoCaller, now time.Time) *gorm.DB {
	q := tx.Table("ai_gateway_input_assets AS inputs").Select("inputs.*").Where("inputs.user_id=?", caller.UserID)
	if caller.ProjectID != 0 {
		q = q.Where("inputs.project_id=?", caller.ProjectID)
	}
	return scopeTrustedVideoInputSource(q, caller.APIKeyID, now)
}

func videoInputSubjectError(err error) error {
	if errors.Is(err, ErrVideoBillingAccess) {
		return repository.ErrVideoInputNotFound
	}
	return err
}

func (s *VideoHTTPService) GetInput(ctx context.Context, caller VideoCaller, id string) (*VideoInputDetails, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if !videoBillingPublicID.MatchString(id) {
		return nil, repository.ErrVideoInputNotFound
	}
	var result *VideoInputDetails
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if caller.APIKeyID == 0 && caller.ProjectID == 0 {
			// JWT只在自己且无Key的可信来源内派生Project，不能全局查询资源归属。
			var row struct{ ProjectID uint64 }
			if err := videoInputMetadataQuery(tx, caller, now).Select("inputs.project_id").Where("inputs.public_id=?", id).Take(&row).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoInputNotFound)
			}
			caller.ProjectID = row.ProjectID
		}
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, now)
		if err != nil {
			return videoInputSubjectError(err)
		}
		caller.ProjectID = owner.ProjectID
		var asset model.AIGatewayInputAsset
		if err := videoInputMetadataQuery(tx, caller, now).Where("inputs.public_id=?", id).Take(&asset).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		details := videoInputDetails(asset, now)
		result = &details
		return nil
	})
	return result, err
}

func (s *VideoHTTPService) ListInputs(ctx context.Context, caller VideoCaller, page, pageSize int) (*VideoInputPage, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if page < 1 || page > 10000 || pageSize < 1 || pageSize > 100 {
		return nil, ErrVideoListParameters
	}
	result := &VideoInputPage{Items: []VideoInputDetails{}, Page: page, PageSize: pageSize}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, now)
		if err != nil {
			return videoInputSubjectError(err)
		}
		caller.ProjectID = owner.ProjectID
		// 计数与数据共享当前事务快照和同一来源截止时刻，total不能统计其他主体记录。
		if err := videoInputMetadataQuery(tx, caller, now).Select("count(*)").Count(&result.Total).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var assets []model.AIGatewayInputAsset
		if err := videoInputMetadataQuery(tx, caller, now).Order("inputs.created_at DESC").Order("inputs.public_id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&assets).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, asset := range assets {
			result.Items = append(result.Items, videoInputDetails(asset, now))
		}
		return nil
	})
	return result, err
}

package service

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

// 来源候选只给公开ID和规格；不是下载授权，也不能代替导入时的再次校验。
type VideoSourceImage struct {
	AssetID   string    `json:"asset_id"`
	MIMEType  string    `json:"mime_type"`
	SizeBytes uint64    `json:"size_bytes"`
	Width     uint32    `json:"width"`
	Height    uint32    `json:"height"`
	VersionNo uint64    `json:"version_no"`
	ExpiresAt time.Time `json:"expires_at"`
}

type VideoSourceImagePage struct {
	Items    []VideoSourceImage `json:"items"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int64              `json:"total"`
}

// 与图片交付条件取交集，并额外限定任务/请求归属、当前Key范围和视频参考图规格。
// 计数和页面使用同一查询及截止时刻，不以Provider列表或客户端URL作为事实源。
func videoSourceImagesQuery(tx *gorm.DB, caller VideoCaller, now time.Time) *gorm.DB {
	q := tx.Table("ai_gateway_assets AS a").
		Joins("JOIN ai_gateway_tasks t ON t.id=a.task_id AND t.request_id=a.request_id AND t.user_id=a.user_id AND t.project_id=a.project_id").
		Joins("JOIN ai_requests r ON r.request_id=a.request_id AND r.user_id=a.user_id AND r.project_id=a.project_id AND r.api_key_id <=> t.api_key_id").
		Where("a.user_id=? AND a.project_id=?", caller.UserID, caller.ProjectID).
		Where("a.modality='image' AND a.asset_role='primary_output' AND a.is_billable_output=1").
		Where("a.lifecycle_state='available' AND a.moderation_status='passed' AND a.explicit_label_status='applied' AND a.implicit_label_status='applied'").
		Where("a.legal_hold=0 AND a.dispute_status<>'open' AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL AND a.expires_at>?", now).
		Where("r.modality='image' AND r.capability='image.generate' AND r.billing_status='settled' AND r.delivery_status='available'").
		Where("t.capability='image.generate' AND t.operation IS NULL AND t.status='succeeded' AND t.logical_model_code=r.logical_model_code").
		Where("a.mime_type IN ('image/png','image/jpeg') AND a.size_bytes BETWEEN 1 AND 10485760 AND a.width BETWEEN 640 AND 4096 AND a.height BETWEEN 640 AND 4096 AND a.width*a.height<=16777216 AND a.width*2>=a.height AND a.width<=a.height*2").
		Where("a.version_no>0 AND a.sha256 REGEXP '^[0-9a-f]{64}$' AND a.bucket IS NOT NULL AND TRIM(a.bucket)<>'' AND a.object_key IS NOT NULL AND TRIM(a.object_key)<>''")
	if caller.APIKeyID == 0 {
		return q.Where("t.api_key_id IS NULL")
	}
	// 高成本图片来源沿用显式图片模型授权；all不能自动获得既有图片的引用能力。
	return q.Where("t.api_key_id=?", caller.APIKeyID).
		Where(`EXISTS (SELECT 1 FROM api_key_model_scopes s WHERE s.api_key_id=? AND s.user_id=a.user_id AND s.project_id=a.project_id AND s.logical_model_code=t.logical_model_code)`, caller.APIKeyID)
}

func (s *VideoHTTPService) ListInputSourceImages(ctx context.Context, caller VideoCaller, page, size int) (*VideoSourceImagePage, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		return nil, ErrVideoListParameters
	}
	result := &VideoSourceImagePage{Items: []VideoSourceImage{}, Page: page, PageSize: size}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, now)
		if err != nil {
			return videoInputSubjectError(err)
		}
		caller.ProjectID = owner.ProjectID
		if err := videoSourceImagesQuery(tx, caller, now).Select("count(*)").Count(&result.Total).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var assets []model.AIImageAsset
		if err := videoSourceImagesQuery(tx, caller, now).Select("a.*").Order("a.created_at DESC").Order("a.public_id DESC").Limit(size).Offset((page - 1) * size).Find(&assets).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, a := range assets {
			result.Items = append(result.Items, VideoSourceImage{AssetID: a.PublicID, MIMEType: *a.MIMEType, SizeBytes: *a.SizeBytes, Width: *a.Width, Height: *a.Height, VersionNo: a.VersionNo, ExpiresAt: a.ExpiresAt})
		}
		return nil
	})
	return result, err
}

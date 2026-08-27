package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

// AttemptRecordingImageAdapter 在任何真实Provider调用前先提交一次尝试事实；记录失败或已有尝试时绝不调用上游。
type AttemptRecordingImageAdapter struct {
	inner imagegateway.ImageProviderAdapter
	db    *gorm.DB
}

func NewAttemptRecordingImageAdapter(inner imagegateway.ImageProviderAdapter, db *gorm.DB) (*AttemptRecordingImageAdapter, error) {
	if inner == nil || db == nil || !safeImageProviderCode(inner.Name()) {
		return nil, ErrImageAPIInvalid
	}
	return &AttemptRecordingImageAdapter{inner: inner, db: db}, nil
}

func safeImageProviderCode(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char)) {
			return false
		}
	}
	return true
}

func (a *AttemptRecordingImageAdapter) Name() string { return a.inner.Name() }

// Generate 把attempt_count=1与provider_code作为调用上游的前置提交；进程随后退出时，重放也会因已有尝试而失败关闭。
func (a *AttemptRecordingImageAdapter) Generate(ctx context.Context, request imagegateway.ProviderImageRequest) (imagegateway.ProviderImageResult, error) {
	if a == nil || a.inner == nil {
		return imagegateway.ProviderImageResult{}, imagegateway.ErrImageResultInvalid
	}
	providerCode := strings.TrimSpace(a.Name())
	if request.RequestID == "" || providerCode == "" {
		return imagegateway.ProviderImageResult{}, imagegateway.ErrImageResultInvalid
	}
	if err := a.recordAttempt(ctx, request.RequestID, providerCode); err != nil {
		return imagegateway.ProviderImageResult{ProviderCode: providerCode, ResultUnknown: true}, errors.Join(imagegateway.ErrProviderUnknown, err)
	}
	result, err := a.inner.Generate(ctx, request)
	result.ProviderAttempted = true
	if result.ProviderCode == "" {
		result.ProviderCode = providerCode
	}
	return result, err
}

func (a *AttemptRecordingImageAdapter) recordAttempt(ctx context.Context, requestID, providerCode string) error {
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.AIImageTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&task).Error; err != nil {
			return err
		}
		if task.Status != model.AIImageTaskProcessing || task.AttemptCount != 0 {
			return ErrImageExecutionStarted
		}
		result := tx.Model(&model.AIImageTask{}).
			Where("id = ? AND status = ? AND attempt_count = 0 AND version_no = ?", task.ID, model.AIImageTaskProcessing, task.VersionNo).
			Updates(map[string]interface{}{
				"provider_code": providerCode, "attempt_count": 1, "version_no": gorm.Expr("version_no + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrImageExecutionStarted
		}
		return nil
	})
}

var _ imagegateway.ImageProviderAdapter = (*AttemptRecordingImageAdapter)(nil)

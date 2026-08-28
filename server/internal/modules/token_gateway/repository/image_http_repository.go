package repository

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
)

type ImageJWTAccess struct {
	UserStatus     string
	RealNameStatus string
	ProjectStatus  string
}

type ImageTaskRecord struct {
	model.AIImageTask
	ExecutionStatus string
	BillingStatus   string
	DeliveryStatus  string
	QuotedAmount    *decimal.Decimal
	SettledAmount   *decimal.Decimal
}

type ImageTaskFilter struct {
	UserID    uint64
	ProjectID uint64
	APIKeyID  *uint64
	Status    string
	Offset    int
	Limit     int
}

type ImageAdminTaskFilter struct {
	UserID    uint64
	ProjectID uint64
	Status    string
	Model     string
	Offset    int
	Limit     int
}

type ImageAdminAssetFilter struct {
	UserID         uint64
	ProjectID      uint64
	LifecycleState string
	DisputeStatus  string
	Offset         int
	Limit          int
}

type ImageReconciliationSummary struct {
	SettlementPending    int64
	ActiveCompensations  int64
	DeadCompensations    int64
	OutboxPending        int64
	OutboxDead           int64
	UnreleasedHoldAmount decimal.Decimal
}

type ImageHTTPRepository struct {
	db *gorm.DB
}

func NewImageHTTPRepository(db *gorm.DB) *ImageHTTPRepository {
	return &ImageHTTPRepository{db: db}
}

func (r *ImageHTTPRepository) FindProjectKey(ctx context.Context, userID, keyID uint64) (*authmodel.APIKey, error) {
	var key authmodel.APIKey
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND project_id IS NOT NULL", keyID, userID).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectKeyNotFound
	}
	return &key, err
}

func (r *ImageHTTPRepository) LoadJWTAccess(ctx context.Context, userID, projectID uint64) (*ImageJWTAccess, error) {
	var access ImageJWTAccess
	err := r.db.WithContext(ctx).Raw(`SELECT u.status AS user_status, u.real_name_status, p.status AS project_status
FROM users AS u JOIN ai_projects AS p ON p.user_id = u.id
WHERE u.id = ? AND p.id = ?`, userID, projectID).Scan(&access).Error
	if err != nil {
		return nil, err
	}
	if access.UserStatus == "" {
		return nil, ErrProjectNotFound
	}
	return &access, nil
}

func (r *ImageHTTPRepository) FindImageModel(ctx context.Context, logicalModelCode string) (*model.TokenModel, error) {
	var item model.TokenModel
	err := r.db.WithContext(ctx).Where("logical_model_code = ?", logicalModelCode).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequestNotFound
	}
	return &item, err
}

// ImageCapabilityAllowed 要求Project SK对图片模型存在显式scope；历史all/legacy_all密钥不能自动继承高成本图片能力。
func (r *ImageHTTPRepository) ImageCapabilityAllowed(ctx context.Context, keyID uint64, logicalModelCode string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&authmodel.APIKeyModelScope{}).
		Where("api_key_id = ? AND logical_model_code = ?", keyID, logicalModelCode).Count(&count).Error
	return count == 1, err
}

func (r *ImageHTTPRepository) FindTaskRecordForOwner(ctx context.Context, publicID string, owner ImageOwner) (*ImageTaskRecord, error) {
	query := r.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").
		Select(`tasks.*, requests.execution_status, requests.billing_status, requests.delivery_status,
requests.quoted_amount, requests.settled_amount`).
		Joins("JOIN ai_requests AS requests ON requests.request_id = tasks.request_id").
		Where("tasks.public_id = ? AND tasks.user_id = ? AND tasks.project_id = ?", publicID, owner.UserID, owner.ProjectID).
		Where("tasks.capability = ? AND tasks.operation IS NULL", model.AIImageCapability).
		Where("requests.modality = ? AND requests.capability = ?", "image", model.AIImageCapability)
	if owner.APIKeyID != nil {
		query = query.Where("tasks.api_key_id = ?", *owner.APIKeyID)
	}
	var item ImageTaskRecord
	if err := query.First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImageTaskNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *ImageHTTPRepository) FindTaskRecordByRequestForOwner(ctx context.Context, requestID string, owner ImageOwner) (*ImageTaskRecord, error) {
	query := r.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").
		Select(`tasks.*, requests.execution_status, requests.billing_status, requests.delivery_status,
requests.quoted_amount, requests.settled_amount`).
		Joins("JOIN ai_requests AS requests ON requests.request_id = tasks.request_id").
		Where("tasks.request_id = ? AND tasks.user_id = ? AND tasks.project_id = ?", requestID, owner.UserID, owner.ProjectID).
		Where("tasks.capability = ? AND tasks.operation IS NULL", model.AIImageCapability).
		Where("requests.modality = ? AND requests.capability = ?", "image", model.AIImageCapability)
	if owner.APIKeyID != nil {
		query = query.Where("tasks.api_key_id = ?", *owner.APIKeyID)
	}
	var item ImageTaskRecord
	if err := query.First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImageTaskNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *ImageHTTPRepository) ListTasksForOwner(ctx context.Context, filter ImageTaskFilter) ([]ImageTaskRecord, int64, error) {
	query := r.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").
		Joins("JOIN ai_requests AS requests ON requests.request_id = tasks.request_id").
		Where("tasks.user_id = ? AND tasks.project_id = ?", filter.UserID, filter.ProjectID).
		Where("tasks.capability = ? AND tasks.operation IS NULL", model.AIImageCapability).
		Where("requests.modality = ? AND requests.capability = ?", "image", model.AIImageCapability)
	if filter.APIKeyID != nil {
		query = query.Where("tasks.api_key_id = ?", *filter.APIKeyID)
	}
	if filter.Status != "" {
		query = query.Where("tasks.status = ?", filter.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []ImageTaskRecord
	err := query.Select(`tasks.*, requests.execution_status, requests.billing_status, requests.delivery_status,
requests.quoted_amount, requests.settled_amount`).Order("tasks.id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&items).Error
	return items, total, err
}

func (r *ImageHTTPRepository) ListAssetsForRequest(ctx context.Context, requestID string, owner ImageOwner) ([]model.AIImageAsset, error) {
	query := r.db.WithContext(ctx).Where("request_id = ? AND user_id = ? AND project_id = ?", requestID, owner.UserID, owner.ProjectID).
		Where("modality = ?", "image")
	if owner.APIKeyID != nil {
		query = query.Where("EXISTS (SELECT 1 FROM ai_gateway_tasks t WHERE t.id = ai_gateway_assets.task_id AND t.api_key_id = ? AND t.capability = ? AND t.operation IS NULL)", *owner.APIKeyID, model.AIImageCapability)
	}
	var items []model.AIImageAsset
	err := query.Order("result_index ASC, asset_role ASC").Find(&items).Error
	return items, err
}

func (r *ImageHTTPRepository) ListAdminTasks(ctx context.Context, filter ImageAdminTaskFilter) ([]ImageTaskRecord, int64, error) {
	query := r.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").
		Joins("JOIN ai_requests AS requests ON requests.request_id = tasks.request_id").
		Where("tasks.capability = ? AND tasks.operation IS NULL", model.AIImageCapability).
		Where("requests.modality = ? AND requests.capability = ?", "image", model.AIImageCapability)
	if filter.UserID != 0 {
		query = query.Where("tasks.user_id = ?", filter.UserID)
	}
	if filter.ProjectID != 0 {
		query = query.Where("tasks.project_id = ?", filter.ProjectID)
	}
	if filter.Status != "" {
		query = query.Where("tasks.status = ?", filter.Status)
	}
	if filter.Model != "" {
		query = query.Where("tasks.logical_model_code = ?", filter.Model)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []ImageTaskRecord
	err := query.Select(`tasks.*, requests.execution_status, requests.billing_status, requests.delivery_status,
requests.quoted_amount, requests.settled_amount`).Order("tasks.id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&items).Error
	return items, total, err
}

func (r *ImageHTTPRepository) FindAdminTask(ctx context.Context, publicID string) (*ImageTaskRecord, error) {
	var item ImageTaskRecord
	err := r.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").
		Select(`tasks.*, requests.execution_status, requests.billing_status, requests.delivery_status,
requests.quoted_amount, requests.settled_amount`).
		Joins("JOIN ai_requests AS requests ON requests.request_id = tasks.request_id").
		Where("tasks.public_id = ?", publicID).
		Where("tasks.capability = ? AND tasks.operation IS NULL", model.AIImageCapability).
		Where("requests.modality = ? AND requests.capability = ?", "image", model.AIImageCapability).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrImageTaskNotFound
	}
	return &item, err
}

func (r *ImageHTTPRepository) ListAdminAssets(ctx context.Context, filter ImageAdminAssetFilter) ([]model.AIImageAsset, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).Where("modality = ?", "image")
	if filter.UserID != 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.ProjectID != 0 {
		query = query.Where("project_id = ?", filter.ProjectID)
	}
	if filter.LifecycleState != "" {
		query = query.Where("lifecycle_state = ?", filter.LifecycleState)
	}
	if filter.DisputeStatus != "" {
		query = query.Where("dispute_status = ?", filter.DisputeStatus)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.AIImageAsset
	err := query.Order("id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&items).Error
	return items, total, err
}

func (r *ImageHTTPRepository) FindAdminAsset(ctx context.Context, publicID string) (*model.AIImageAsset, error) {
	var item model.AIImageAsset
	err := r.db.WithContext(ctx).Where("public_id = ? AND modality = ?", publicID, "image").First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrImageAssetNotFound
	}
	return &item, err
}

func (r *ImageHTTPRepository) ReconciliationSummary(ctx context.Context) (*ImageReconciliationSummary, error) {
	result := &ImageReconciliationSummary{}
	if err := r.db.WithContext(ctx).Model(&model.AIRequest{}).Where("modality = 'image' AND billing_status = ?", model.AIBillingSettlementPending).Count(&result.SettlementPending).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).Where("task_type = 'image_reconcile' AND status IN ('pending','running','retry','manual_review')").Count(&result.ActiveCompensations).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).Where("task_type = 'image_reconcile' AND status = 'dead'").Count(&result.DeadCompensations).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).Where("aggregate_type = 'image_request' AND status IN ('pending','publishing')").Count(&result.OutboxPending).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).Where("aggregate_type = 'image_request' AND status = 'dead'").Count(&result.OutboxDead).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(h.hold_amount),0)
FROM wallet_holds AS h JOIN ai_request_wallet_links AS l ON l.wallet_hold_id = h.id
JOIN ai_requests AS r ON r.request_id = l.request_id
WHERE r.modality = 'image' AND h.status = 'holding'`).Scan(&result.UnreleasedHoldAmount).Error; err != nil {
		return nil, err
	}
	return result, nil
}

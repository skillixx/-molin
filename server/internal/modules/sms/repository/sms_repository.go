package repository

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/sms/model"
)

var (
	ErrBindingNotFound             = errors.New("短信场景未绑定可用模板")
	ErrAdminTemplateNotFound       = errors.New("短信模板不存在")
	ErrAdminTemplateConflict       = errors.New("短信模板版本或绑定状态冲突")
	ErrAdminSceneConflict          = errors.New("短信场景版本冲突")
	ErrAdminSceneTemplateInvalid   = errors.New("短信场景模板不可用")
	ErrTestSendIdempotencyConflict = errors.New("短信测试发送幂等参数冲突")
)

// SMSRepository 提供阶段 1 发送链路所需的最小数据访问能力。
type SMSRepository struct {
	db *gorm.DB
}

// ReserveTestSend 利用数据库唯一约束抢占测试发送；并发重复请求只能有一个调用供应商。
func (r *SMSRepository) ReserveTestSend(ctx context.Context, log *model.SendLog) (*model.SendLog, bool, error) {
	if err := r.db.WithContext(ctx).Create(log).Error; err == nil {
		return log, true, nil
	}
	var existing model.SendLog
	if err := r.db.WithContext(ctx).Where("idempotency_owner_key_hash = ?", log.IdempotencyOwnerKeyHash).First(&existing).Error; err != nil {
		return nil, false, err
	}
	if existing.RequestFingerprint == nil || log.RequestFingerprint == nil || *existing.RequestFingerprint != *log.RequestFingerprint {
		return nil, false, ErrTestSendIdempotencyConflict
	}
	return &existing, false, nil
}

func (r *SMSRepository) CompleteTestSend(ctx context.Context, id uint64, status string, providerRequestID, providerCode, failureSummary *string, completedAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.SendLog{}).Where("id = ? AND submit_status = ?", id, "pending").Updates(map[string]any{
		"submit_status": status, "provider_request_id": providerRequestID, "provider_code": providerCode,
		"failure_summary": failureSummary, "completed_at": completedAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("短信测试发送记录终态更新冲突")
	}
	return nil
}

func (r *SMSRepository) ListAdminTemplates(ctx context.Context, filter model.TemplateListFilter) ([]model.Template, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Template{})
	if filter.Scene != "" {
		query = query.Joins("JOIN sms_scene_bindings ON sms_scene_bindings.template_id = sms_templates.id AND sms_scene_bindings.scene = ? AND sms_scene_bindings.enabled = 1", filter.Scene)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where("sms_templates.template_code LIKE ? OR sms_templates.template_name LIKE ?", like, like)
	}
	if filter.AuditStatus != "" {
		query = query.Where("sms_templates.provider_audit_status = ?", filter.AuditStatus)
	}
	if filter.Enabled != nil {
		query = query.Where("sms_templates.local_enabled = ?", *filter.Enabled)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Template
	if err := query.Order("sms_templates.id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	for i := range items {
		if items[i].Variables == nil {
			items[i].Variables = []string{}
		}
		if err := r.db.WithContext(ctx).Model(&model.SceneBinding{}).Where("template_id = ? AND enabled = ?", items[i].ID, true).Order("scene ASC").Pluck("scene", &items[i].BoundScenes).Error; err != nil {
			return nil, 0, err
		}
		if items[i].BoundScenes == nil {
			items[i].BoundScenes = []string{}
		}
	}
	return items, total, nil
}

func (r *SMSRepository) ListAdminSceneBindings(ctx context.Context) ([]model.SceneBinding, error) {
	var bindings []model.SceneBinding
	err := r.db.WithContext(ctx).Preload("Template").Order("scene ASC").Find(&bindings).Error
	return bindings, err
}

// UpsertAdminSceneBinding 以当前版本作比较更新；version=0 只允许创建尚未存在的固定场景。
func (r *SMSRepository) UpsertAdminSceneBinding(ctx context.Context, scene, signName string, templateID, version, operatorID uint64, enabled bool) (*model.SceneBinding, error) {
	var result model.SceneBinding
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先锁模板再锁场景，与同步事务的锁顺序保持一致，避免并发启停或同步时形成反向等待。
		var template model.Template
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&template, templateID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminSceneTemplateInvalid
			}
			return err
		}
		if !template.LocalEnabled || template.ProviderAuditStatus != "approved" || template.TemplateType != "verification" || !strings.Contains(template.Content, "${code}") {
			return ErrAdminSceneTemplateInvalid
		}
		var existing model.SceneBinding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scene = ?", scene).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if version != 0 {
				return ErrAdminSceneConflict
			}
			result = model.SceneBinding{Scene: scene, TemplateID: templateID, SignName: signName, Enabled: enabled, Version: 1, CreatedBy: &operatorID, UpdatedBy: &operatorID}
			if err := tx.Create(&result).Error; err != nil {
				var mysqlErr *mysqlDriver.MySQLError
				if errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &mysqlErr) && mysqlErr.Number == 1062) {
					return ErrAdminSceneConflict
				}
				return err
			}
			return nil
		}
		if err != nil || existing.Version != version {
			if err != nil {
				return err
			}
			return ErrAdminSceneConflict
		}
		update := tx.Model(&model.SceneBinding{}).Where("id = ? AND version = ?", existing.ID, version).Updates(map[string]any{
			"template_id": templateID, "sign_name": signName, "enabled": enabled,
			"updated_by": operatorID, "version": gorm.Expr("version + 1"),
		})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrAdminSceneConflict
		}
		existing.TemplateID, existing.SignName, existing.Enabled, existing.Version, existing.UpdatedBy = templateID, signName, enabled, version+1, &operatorID
		result = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *SMSRepository) ListAdminSendLogs(ctx context.Context, filter model.SendLogListFilter) ([]model.SendLog, int64, error) {
	// pending 是内部并发占位，管理查询只公开供应商已经收敛的终态。
	query := r.db.WithContext(ctx).Model(&model.SendLog{}).Where("submit_status <> ?", "pending")
	if filter.Scene != "" {
		query = query.Where("scene = ?", filter.Scene)
	}
	if filter.Status != "" {
		query = query.Where("submit_status = ?", filter.Status)
	}
	if filter.TemplateID != 0 {
		query = query.Where("template_id = ?", filter.TemplateID)
	}
	if filter.BusinessRequestID != "" {
		query = query.Where("business_request_id = ?", filter.BusinessRequestID)
	}
	if filter.StartTime != nil {
		query = query.Where("submitted_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("submitted_at <= ?", *filter.EndTime)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.SendLog
	if err := query.Order("id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func NewSMSRepository(db *gorm.DB) *SMSRepository { return &SMSRepository{db: db} }

// GetAdminSummary 用单次数据库往返聚合模板和启用场景统计，避免管理端拉取多页后自行计算。
func (r *SMSRepository) GetAdminSummary(ctx context.Context) (model.AdminSummary, error) {
	var summary model.AdminSummary
	err := r.db.WithContext(ctx).Raw(`
SELECT
  (SELECT COUNT(*) FROM sms_templates) AS template_total,
  (SELECT COUNT(*) FROM sms_templates WHERE provider_audit_status = 'approved') AS approved_total,
  (SELECT COUNT(*) FROM sms_templates WHERE local_enabled = 1) AS enabled_total,
  (SELECT COUNT(*) FROM sms_scene_bindings WHERE enabled = 1) AS bound_scene_total,
  (SELECT last_synced_at FROM sms_template_sync_locks WHERE lock_name = 'aliyun_templates') AS last_synced_at`).Scan(&summary).Error
	if err != nil {
		return model.AdminSummary{}, err
	}
	return summary, nil
}

// GetAdminTemplate 返回管理端模板详情及当前启用场景，不暴露发送幂等摘要等内部字段。
func (r *SMSRepository) GetAdminTemplate(ctx context.Context, id uint64) (*model.Template, error) {
	var template model.Template
	if err := r.db.WithContext(ctx).First(&template, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdminTemplateNotFound
		}
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.SceneBinding{}).
		Where("template_id = ? AND enabled = ?", id, true).
		Order("scene ASC").Pluck("scene", &template.BoundScenes).Error; err != nil {
		return nil, err
	}
	if template.Variables == nil {
		template.Variables = []string{}
	}
	if template.BoundScenes == nil {
		template.BoundScenes = []string{}
	}
	return &template, nil
}

// UpdateAdminTemplateStatus 使用版本号和启用绑定保护执行原子更新。
// 停用时若仍有有效场景绑定，NOT EXISTS 条件会让更新失败，避免发送链路瞬间失去模板。
func (r *SMSRepository) UpdateAdminTemplateStatus(ctx context.Context, id, version uint64, enabled bool) error {
	result := r.db.WithContext(ctx).Exec(
		"UPDATE sms_templates SET local_enabled = ?, version = version + 1 WHERE id = ? AND version = ? AND (? = 1 OR NOT EXISTS (SELECT 1 FROM sms_scene_bindings WHERE template_id = ? AND enabled = 1))",
		enabled, id, version, enabled, id,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAdminTemplateConflict
	}
	return nil
}

// ApplyTemplateSnapshots 在单一事务内应用已经完整拉取的供应商快照。
// 同步锁保证并发同步串行执行；审核失效或供应商已移除的模板会被关闭，并同步关闭其场景绑定。
func (r *SMSRepository) ApplyTemplateSnapshots(ctx context.Context, snapshots []model.TemplateSnapshot, syncedAt time.Time) (model.TemplateSyncResult, error) {
	result := model.TemplateSyncResult{TotalCount: int64(len(snapshots)), LastSyncedAt: syncedAt}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockVersion uint64
		if err := tx.Raw("SELECT version FROM sms_template_sync_locks WHERE lock_name = ? FOR UPDATE", "aliyun_templates").Scan(&lockVersion).Error; err != nil {
			return err
		}
		if lockVersion == 0 {
			return errors.New("短信模板同步锁不存在")
		}

		var existing []model.Template
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider = ?", "aliyun").Find(&existing).Error; err != nil {
			return err
		}
		byCode := make(map[string]*model.Template, len(existing))
		for i := range existing {
			byCode[existing[i].TemplateCode] = &existing[i]
		}
		seen := make(map[string]struct{}, len(snapshots))
		for _, snapshot := range snapshots {
			seen[snapshot.TemplateCode] = struct{}{}
			current := byCode[snapshot.TemplateCode]
			valid := snapshot.ProviderAuditStatus == "approved" && snapshot.TemplateType == "verification"
			if current == nil {
				item := model.Template{
					Provider: snapshot.Provider, TemplateCode: snapshot.TemplateCode, TemplateName: snapshot.TemplateName,
					TemplateType: snapshot.TemplateType, ProviderAuditStatus: snapshot.ProviderAuditStatus,
					RejectionReason: snapshot.RejectionReason, ProviderUpdatedAt: snapshot.ProviderUpdatedAt,
					Content: snapshot.Content, Variables: snapshot.Variables, LocalEnabled: false,
					Version: 1, LastSyncedAt: syncedAt,
				}
				if err := tx.Create(&item).Error; err != nil {
					return err
				}
				result.CreatedCount++
				continue
			}
			wasEnabled := current.LocalEnabled
			variablesJSON, marshalErr := json.Marshal(snapshot.Variables)
			if marshalErr != nil {
				return marshalErr
			}
			unchanged := current.TemplateName == snapshot.TemplateName && current.TemplateType == snapshot.TemplateType &&
				current.ProviderAuditStatus == snapshot.ProviderAuditStatus && current.Content == snapshot.Content &&
				reflect.DeepEqual(current.Variables, snapshot.Variables) && stringPointerEqual(current.RejectionReason, snapshot.RejectionReason) &&
				timePointerEqual(current.ProviderUpdatedAt, snapshot.ProviderUpdatedAt) && (valid || !current.LocalEnabled)
			if unchanged {
				if err := tx.Model(&model.Template{}).Where("id = ?", current.ID).Update("last_synced_at", syncedAt).Error; err != nil {
					return err
				}
				result.UnchangedCount++
				continue
			}
			updates := map[string]any{
				"template_name": snapshot.TemplateName, "template_type": snapshot.TemplateType,
				"provider_audit_status": snapshot.ProviderAuditStatus, "rejection_reason": snapshot.RejectionReason,
				"provider_updated_at": snapshot.ProviderUpdatedAt, "content": snapshot.Content,
				"variables_json": variablesJSON, "last_synced_at": syncedAt,
				"version": gorm.Expr("version + 1"),
			}
			if !valid {
				updates["local_enabled"] = false
			}
			if err := tx.Model(&model.Template{}).Where("id = ?", current.ID).Updates(updates).Error; err != nil {
				return err
			}
			result.UpdatedCount++
			if wasEnabled && !valid {
				if err := tx.Model(&model.SceneBinding{}).Where("template_id = ? AND enabled = ?", current.ID, true).Updates(map[string]any{"enabled": false, "version": gorm.Expr("version + 1")}).Error; err != nil {
					return err
				}
			}
		}

		for _, current := range existing {
			if _, ok := seen[current.TemplateCode]; ok || !current.LocalEnabled {
				continue
			}
			if err := tx.Model(&model.Template{}).Where("id = ?", current.ID).Updates(map[string]any{"local_enabled": false, "version": gorm.Expr("version + 1"), "last_synced_at": syncedAt}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.SceneBinding{}).Where("template_id = ? AND enabled = ?", current.ID, true).Updates(map[string]any{"enabled": false, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			result.UpdatedCount++
		}
		return tx.Exec("UPDATE sms_template_sync_locks SET version = version + 1, last_synced_at = ? WHERE lock_name = ?", syncedAt, "aliyun_templates").Error
	})
	if err != nil {
		return model.TemplateSyncResult{}, err
	}
	return result, nil
}

func stringPointerEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func timePointerEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

// FindActiveBinding 只返回启用绑定且模板已审核、已本地启用的数据库快照。
func (r *SMSRepository) FindActiveBinding(ctx context.Context, scene string) (*model.SceneBinding, error) {
	var binding model.SceneBinding
	err := r.db.WithContext(ctx).
		Preload("Template").
		Where("scene = ? AND enabled = ?", scene, true).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBindingNotFound
		}
		return nil, err
	}
	if binding.Template.ID == 0 || !binding.Template.LocalEnabled || binding.Template.ProviderAuditStatus != "approved" {
		return nil, ErrBindingNotFound
	}
	return &binding, nil
}

// CreateSendLog 写入最终提交状态；模型本身不包含完整手机号和验证码字段。
func (r *SMSRepository) CreateSendLog(ctx context.Context, log *model.SendLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

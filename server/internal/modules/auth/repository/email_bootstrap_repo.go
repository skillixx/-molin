package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/auth/model"
)

// FindAdminVerifyBootstrapReceipt 读取全局唯一的成功凭据。
func (r *EmailRepository) FindAdminVerifyBootstrapReceipt(ctx context.Context) (*model.EmailAdminVerifyBootstrapReceipt, error) {
	var receipt model.EmailAdminVerifyBootstrapReceipt
	err := r.db.WithContext(ctx).Where("scope = ?", "admin_verify").First(&receipt).Error
	if err == nil {
		normalizeEmailBootstrapReceiptDatabaseUTC(&receipt)
	}
	return &receipt, err
}

// ApplyAdminVerifyBootstrap 在单一事务中完成行锁、凭据复查、镜像、绑定、receipt 与结果审计。
// 返回 created=false 表示等待行锁期间已有并发请求成功，调用方必须按凭据重新判定重放或冲突。
func (r *EmailRepository) ApplyAdminVerifyBootstrap(
	ctx context.Context,
	template model.EmailProviderTemplate,
	receipt model.EmailAdminVerifyBootstrapReceipt,
	resultAudit func(*gorm.DB, uint64, uint64) error,
) (*model.EmailAdminVerifyBootstrapReceipt, bool, error) {
	var output *model.EmailAdminVerifyBootstrapReceipt
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding model.EmailSceneBinding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scene = ?", "admin_verify").First(&binding).Error; err != nil {
			return err
		}
		normalizeEmailSceneBindingDatabaseUTC(&binding)

		var existing model.EmailAdminVerifyBootstrapReceipt
		if err := tx.Where("scope = ?", "admin_verify").First(&existing).Error; err == nil {
			normalizeEmailBootstrapReceiptDatabaseUTC(&existing)
			output = &existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if binding.TemplateID != nil || binding.Enabled || binding.Version != 1 {
			return ErrEmailConflict
		}

		now := time.Now().UTC().Truncate(time.Second)
		storedNow := databaseWriteDatetimeUTC(now)
		var stored model.EmailProviderTemplate
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("provider = ? AND provider_template_id = ?", template.Provider, template.ProviderTemplateID).
			First(&stored).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			template.LocalEnabled = true
			template.Missing = false
			template.MissingSince = nil
			template.Version = 1
			template.LastSyncedAt = now
			template.CreatedAt = now
			template.UpdatedAt = now
			storedTemplate := template
			prepareEmailTemplateWriteUTC(&storedTemplate, now)
			if err := tx.Create(&storedTemplate).Error; err != nil {
				return err
			}
			template.ID = storedTemplate.ID
			stored = template
		} else if findErr != nil {
			return findErr
		} else {
			normalizeEmailTemplateDatabaseUTC(&stored)
			updates := map[string]any{
				"name": template.Name, "subject": template.Subject, "sender_nickname": nil,
				"template_text": template.TemplateText, "variables_json": template.VariablesJSON,
				"content_sha256": template.ContentSHA256, "provider_status": template.ProviderStatus,
				"review_comment": nil, "variables_complete": template.VariablesComplete,
				"provider_created_at": databaseWriteDatetimeUTCPointer(template.ProviderCreatedAt), "last_synced_at": storedNow,
				"missing": false, "missing_since": nil, "local_enabled": true,
				"version": gorm.Expr("version + 1"), "updated_at": storedNow,
			}
			if err := tx.Model(&stored).Updates(updates).Error; err != nil {
				return err
			}
		}

		cas := tx.Model(&model.EmailSceneBinding{}).
			Where("scene = ? AND template_id IS NULL AND enabled = ? AND version = ?", "admin_verify", false, 1).
			Updates(map[string]any{"template_id": stored.ID, "enabled": true, "updated_by": receipt.CompletedBy, "version": 2, "updated_at": storedNow})
		if cas.Error != nil {
			return cas.Error
		}
		if cas.RowsAffected != 1 {
			return ErrEmailConflict
		}

		receipt.TemplateID = stored.ID
		receipt.CreatedAt = now
		storedReceipt := receipt
		prepareEmailBootstrapReceiptWriteUTC(&storedReceipt, now)
		if err := tx.Create(&storedReceipt).Error; err != nil {
			return err
		}
		receipt.ID = storedReceipt.ID
		if resultAudit == nil || resultAudit(tx, receipt.ID, stored.ID) != nil {
			return errors.New("结果审计写入失败")
		}
		copy := receipt
		output = &copy
		created = true
		return nil
	})
	return output, created, err
}

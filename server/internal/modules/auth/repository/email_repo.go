package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/auth/model"
)

var (
	ErrEmailNotFound = errors.New("邮件资源不存在")
	ErrEmailConflict = errors.New("邮件资源版本或幂等键冲突")
)

// EmailRepository 封装邮件模板管理与发送日志的数据库操作。
type EmailRepository struct{ db *gorm.DB }

func NewEmailRepository(db *gorm.DB) *EmailRepository { return &EmailRepository{db: db} }

func (r *EmailRepository) DB() *gorm.DB { return r.db }

func (r *EmailRepository) GetTemplate(ctx context.Context, id uint64) (*model.EmailProviderTemplate, error) {
	var item model.EmailProviderTemplate
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEmailNotFound
		}
		return nil, err
	}
	normalizeEmailTemplateDatabaseUTC(&item)
	return &item, nil
}

func (r *EmailRepository) GetBinding(ctx context.Context, scene string) (*model.EmailSceneBinding, *model.EmailProviderTemplate, error) {
	var binding model.EmailSceneBinding
	if err := r.db.WithContext(ctx).Where("scene = ?", scene).First(&binding).Error; err != nil {
		return nil, nil, err
	}
	normalizeEmailSceneBindingDatabaseUTC(&binding)
	if binding.TemplateID == nil {
		return &binding, nil, nil
	}
	tpl, err := r.GetTemplate(ctx, *binding.TemplateID)
	return &binding, tpl, err
}

func (r *EmailRepository) ListTemplates(ctx context.Context, keyword, providerStatus string, localEnabled, variablesComplete, missing *bool, scene string, offset, limit int) ([]model.EmailProviderTemplate, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.EmailProviderTemplate{})
	if keyword != "" {
		q = q.Where("name LIKE ? OR subject LIKE ? OR provider_template_id LIKE ?", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}
	if providerStatus != "" {
		q = q.Where("provider_status = ?", providerStatus)
	}
	if localEnabled != nil {
		q = q.Where("local_enabled = ?", *localEnabled)
	}
	if variablesComplete != nil {
		q = q.Where("variables_complete = ?", *variablesComplete)
	}
	if missing != nil {
		q = q.Where("missing = ?", *missing)
	}
	if scene != "" {
		q = q.Where("id IN (?)", r.db.Model(&model.EmailSceneBinding{}).Select("template_id").Where("scene = ?", scene))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.EmailProviderTemplate
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	for i := range items {
		normalizeEmailTemplateDatabaseUTC(&items[i])
	}
	return items, total, err
}

func (r *EmailRepository) BoundScenes(ctx context.Context, templateIDs []uint64) (map[uint64][]string, error) {
	result := make(map[uint64][]string)
	if len(templateIDs) == 0 {
		return result, nil
	}
	var rows []model.EmailSceneBinding
	if err := r.db.WithContext(ctx).Where("template_id IN ?", templateIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.TemplateID != nil {
			result[*row.TemplateID] = append(result[*row.TemplateID], row.Scene)
		}
	}
	return result, nil
}

func (r *EmailRepository) UpdateTemplateStatus(ctx context.Context, id, version uint64, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&model.EmailProviderTemplate{}).Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{"local_enabled": enabled, "version": gorm.Expr("version + 1"), "updated_at": databaseWriteDatetimeUTC(time.Now().UTC())})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}

func (r *EmailRepository) ListBindings(ctx context.Context) ([]model.EmailSceneBinding, error) {
	var items []model.EmailSceneBinding
	err := r.db.WithContext(ctx).Order("FIELD(scene,'register','login','reset_password','bind_email','admin_verify')").Find(&items).Error
	for i := range items {
		normalizeEmailSceneBindingDatabaseUTC(&items[i])
	}
	return items, err
}

func (r *EmailRepository) UpdateBinding(ctx context.Context, scene string, templateID, version, operatorID uint64, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&model.EmailSceneBinding{}).Where("scene = ? AND version = ?", scene, version).
		Updates(map[string]any{"template_id": templateID, "enabled": enabled, "updated_by": operatorID, "version": gorm.Expr("version + 1"), "updated_at": databaseWriteDatetimeUTC(time.Now().UTC())})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}

func (r *EmailRepository) CreateSyncRun(ctx context.Context, run *model.EmailTemplateSyncRun) error {
	stored := *run
	prepareEmailSyncRunWriteUTC(&stored, time.Now().UTC())
	if err := r.db.WithContext(ctx).Create(&stored).Error; err != nil {
		return err
	}
	run.ID = stored.ID
	return nil
}

func (r *EmailRepository) FindSyncByIdempotency(ctx context.Context, scope, keyHash string) (*model.EmailTemplateSyncRun, error) {
	var run model.EmailTemplateSyncRun
	err := r.db.WithContext(ctx).Where("idempotency_scope=? AND idempotency_key_hash=?", scope, keyHash).First(&run).Error
	if err == nil {
		normalizeEmailSyncRunDatabaseUTC(&run)
	}
	return &run, err
}

func (r *EmailRepository) GetSyncRun(ctx context.Context, id uint64) (*model.EmailTemplateSyncRun, error) {
	var run model.EmailTemplateSyncRun
	if err := r.db.WithContext(ctx).First(&run, id).Error; err != nil {
		return nil, err
	}
	normalizeEmailSyncRunDatabaseUTC(&run)
	return &run, nil
}

func (r *EmailRepository) HasRunningSync(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.EmailTemplateSyncRun{}).Where("status = ?", "running").Count(&count).Error
	return count > 0, err
}

// FindStaleSyncRuns 返回超时 running 记录，由 service 先写 attempt 审计后再执行状态收敛。
func (r *EmailRepository) FindStaleSyncRuns(ctx context.Context, cutoff time.Time) ([]model.EmailTemplateSyncRun, error) {
	var runs []model.EmailTemplateSyncRun
	err := r.db.WithContext(ctx).Where("status = ? AND started_at < ?", "running", databaseWriteDatetimeUTC(cutoff)).Find(&runs).Error
	for i := range runs {
		normalizeEmailSyncRunDatabaseUTC(&runs[i])
	}
	return runs, err
}

func (r *EmailRepository) FailStaleSync(ctx context.Context, id uint64, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.EmailTemplateSyncRun{}).Where("id = ? AND status = ?", id, "running").
		Updates(map[string]any{"status": "failed", "error_code": "sync_interrupted", "error_message": "同步进程中断", "completed_at": databaseWriteDatetimeUTC(now)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}

// ApplyTemplateSync 只在供应商全部分页和详情读取成功后调用，并在单一事务中更新镜像与同步记录。
func (r *EmailRepository) ApplyTemplateSync(ctx context.Context, runID uint64, incoming []model.EmailProviderTemplate, now time.Time) (created, updated, missing, unchanged uint, err error) {
	now = now.UTC().Truncate(time.Second)
	storedNow := databaseWriteDatetimeUTC(now)
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 事务开始先锁定同步 run，只有仍为 running 的 lease 所有者才能写镜像。
		var run model.EmailTemplateSyncRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", runID, "running").First(&run).Error; err != nil {
			return ErrEmailConflict
		}
		seen := make([]string, 0, len(incoming))
		for i := range incoming {
			item := &incoming[i]
			seen = append(seen, item.ProviderTemplateID)
			var old model.EmailProviderTemplate
			findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider=? AND provider_template_id=?", item.Provider, item.ProviderTemplateID).First(&old).Error
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				item.LocalEnabled = false
				item.Missing = false
				item.MissingSince = nil
				item.Version = 1
				item.LastSyncedAt = now
				storedItem := *item
				prepareEmailTemplateWriteUTC(&storedItem, now)
				if err := tx.Create(&storedItem).Error; err != nil {
					return err
				}
				item.ID = storedItem.ID
				created++
				continue
			}
			if findErr != nil {
				return findErr
			}
			changed := templateMirrorChangedFromDatabase(old, *item)
			values := map[string]any{"name": item.Name, "subject": item.Subject, "sender_nickname": item.SenderNickname, "template_text": item.TemplateText, "variables_json": item.VariablesJSON, "content_sha256": item.ContentSHA256, "provider_status": item.ProviderStatus, "review_comment": item.ReviewComment, "variables_complete": item.VariablesComplete, "provider_created_at": databaseWriteDatetimeUTCPointer(item.ProviderCreatedAt), "last_synced_at": storedNow, "missing": false, "missing_since": nil, "updated_at": storedNow}
			if changed {
				values["version"] = gorm.Expr("version + 1")
				updated++
			} else {
				unchanged++
			}
			if err := tx.Model(&old).Updates(values).Error; err != nil {
				return err
			}
		}
		q := tx.Model(&model.EmailProviderTemplate{}).Where("provider=? AND missing=?", "aliyun_directmail", false)
		if len(seen) > 0 {
			q = q.Where("provider_template_id NOT IN ?", seen)
		}
		var lost []model.EmailProviderTemplate
		if err := q.Find(&lost).Error; err != nil {
			return err
		}
		for _, item := range lost {
			if err := tx.Model(&item).Updates(map[string]any{"missing": true, "missing_since": storedNow, "version": gorm.Expr("version + 1"), "updated_at": storedNow}).Error; err != nil {
				return err
			}
			missing++
		}
		result := tx.Model(&model.EmailTemplateSyncRun{}).Where("id=? AND status=?", runID, "running").Updates(map[string]any{"status": "succeeded", "created_count": created, "updated_count": updated, "missing_count": missing, "unchanged_count": unchanged, "completed_at": storedNow})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// run 已被 stale 收敛或其他执行者终结时，回滚本事务内全部镜像变化。
			return ErrEmailConflict
		}
		return nil
	})
	return
}

func templateMirrorChanged(old, incoming model.EmailProviderTemplate) bool {
	return old.Name != incoming.Name || old.Subject != incoming.Subject || old.TemplateText != incoming.TemplateText || old.VariablesJSON != incoming.VariablesJSON || old.ContentSHA256 != incoming.ContentSHA256 || old.ProviderStatus != incoming.ProviderStatus || old.VariablesComplete != incoming.VariablesComplete || old.Missing || !sameStringPointer(old.SenderNickname, incoming.SenderNickname) || !sameStringPointer(old.ReviewComment, incoming.ReviewComment) || !sameTimePointer(old.ProviderCreatedAt, incoming.ProviderCreatedAt)
}

// templateMirrorChangedFromDatabase 先恢复数据库 DATETIME 的 UTC 墙钟语义，再比较供应商镜像。
// 这能避免 loc=Local 扫描得到的同墙钟时间被误判为不同时刻，导致重复同步错误递增版本。
func templateMirrorChangedFromDatabase(old, incoming model.EmailProviderTemplate) bool {
	normalizeEmailTemplateDatabaseUTC(&old)
	return templateMirrorChanged(old, incoming)
}

func sameStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (r *EmailRepository) FailSync(ctx context.Context, runID uint64, code, message string) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&model.EmailTemplateSyncRun{}).Where("id=? AND status=?", runID, "running").Updates(map[string]any{"status": "failed", "error_code": code, "error_message": message, "completed_at": databaseWriteDatetimeUTC(now)})
	return requireOneAffected(result)
}

func requireOneAffected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}

func (r *EmailRepository) ListSyncRuns(ctx context.Context, status string, offset, limit int) ([]model.EmailTemplateSyncRun, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.EmailTemplateSyncRun{})
	if status != "" {
		q = q.Where("status=?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.EmailTemplateSyncRun
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	for i := range items {
		normalizeEmailSyncRunDatabaseUTC(&items[i])
	}
	return items, total, err
}

func (r *EmailRepository) FindAllowlistByHMAC(ctx context.Context, h string) (*model.EmailTestRecipientAllowlist, error) {
	var v model.EmailTestRecipientAllowlist
	err := r.db.WithContext(ctx).Where("email_hmac=?", h).First(&v).Error
	if err == nil {
		normalizeEmailAllowlistDatabaseUTC(&v)
	}
	return &v, err
}
func (r *EmailRepository) CreateAllowlist(ctx context.Context, v *model.EmailTestRecipientAllowlist) error {
	stored := *v
	prepareEmailAllowlistWriteUTC(&stored, time.Now().UTC())
	if err := r.db.WithContext(ctx).Create(&stored).Error; err != nil {
		return err
	}
	v.ID = stored.ID
	return nil
}
func (r *EmailRepository) RestoreAllowlist(ctx context.Context, id, operator uint64) error {
	return r.db.WithContext(ctx).Model(&model.EmailTestRecipientAllowlist{}).Where("id=? AND status=?", id, "revoked").Updates(map[string]any{"status": "active", "revoked_at": nil, "updated_by": operator, "version": gorm.Expr("version + 1"), "updated_at": databaseWriteDatetimeUTC(time.Now().UTC())}).Error
}
func (r *EmailRepository) RevokeAllowlist(ctx context.Context, id, version, operator uint64) error {
	now := databaseWriteDatetimeUTC(time.Now().UTC())
	q := r.db.WithContext(ctx).Model(&model.EmailTestRecipientAllowlist{}).Where("id=? AND version=? AND status=?", id, version, "active").Updates(map[string]any{"status": "revoked", "revoked_at": now, "updated_by": operator, "version": gorm.Expr("version + 1"), "updated_at": now})
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}
func (r *EmailRepository) GetAllowlist(ctx context.Context, id uint64) (*model.EmailTestRecipientAllowlist, error) {
	var v model.EmailTestRecipientAllowlist
	if err := r.db.WithContext(ctx).First(&v, id).Error; err != nil {
		return nil, err
	}
	normalizeEmailAllowlistDatabaseUTC(&v)
	return &v, nil
}
func (r *EmailRepository) ListAllowlist(ctx context.Context, offset, limit int) ([]model.EmailTestRecipientAllowlist, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.EmailTestRecipientAllowlist{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var v []model.EmailTestRecipientAllowlist
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&v).Error
	for i := range v {
		normalizeEmailAllowlistDatabaseUTC(&v[i])
	}
	return v, total, err
}

func (r *EmailRepository) CreateSendLog(ctx context.Context, v *model.EmailSendLog) error {
	stored := *v
	prepareEmailSendLogWriteUTC(&stored, time.Now().UTC())
	if err := r.db.WithContext(ctx).Create(&stored).Error; err != nil {
		return err
	}
	v.ID = stored.ID
	return nil
}

// FinalizeSendLog 只允许 pending 向最终状态单向收敛，保证失败结果也可可靠记录。
func (r *EmailRepository) FinalizeSendLog(ctx context.Context, id uint64, status string, requestID, failureReason *string) error {
	result := r.db.WithContext(ctx).Model(&model.EmailSendLog{}).Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]any{"status": status, "provider_request_id": requestID, "failure_reason": failureReason})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmailConflict
	}
	return nil
}

// DeleteRevokedAllowlistBefore 清理撤销满 30 天的白名单记录，缩短邮箱 HMAC 与脱敏值保留期。
func (r *EmailRepository) DeleteRevokedAllowlistBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Where("status = ? AND revoked_at < ?", "revoked", databaseWriteDatetimeUTC(cutoff)).Delete(&model.EmailTestRecipientAllowlist{})
	return result.RowsAffected, result.Error
}
func (r *EmailRepository) FindSendLogByIdempotency(ctx context.Context, scope, keyHash string) (*model.EmailSendLog, error) {
	var v model.EmailSendLog
	err := r.db.WithContext(ctx).Where("idempotency_scope=? AND idempotency_key_hash=?", scope, keyHash).First(&v).Error
	if err == nil {
		normalizeEmailSendLogDatabaseUTC(&v)
	}
	return &v, err
}

// FindBlockingSendByScope 查询仍处于派生冷却期的 pending 或未知结果墓碑。
// OTP 使用 expires_at，模板测试使用 submitted_at 加十分钟；冷却截止时间不新增数据库列。
func (r *EmailRepository) FindBlockingSendByScope(ctx context.Context, scope string, now time.Time) (*model.EmailSendLog, error) {
	var v model.EmailSendLog
	err := r.db.WithContext(ctx).
		Where("idempotency_scope = ?", scope).
		Where("status = ? OR (status = ? AND failure_reason = ? AND ((purpose = ? AND expires_at > ?) OR (purpose = ? AND submitted_at > ?)))", "pending", "failed", "provider_outcome_unknown", "otp", databaseWriteDatetimeUTC(now), "test", databaseWriteDatetimeUTC(now.Add(-10*time.Minute))).
		Order("submitted_at DESC").First(&v).Error
	if err == nil {
		normalizeEmailSendLogDatabaseUTC(&v)
	}
	return &v, err
}

func (r *EmailRepository) FailStalePendingSend(ctx context.Context, scope, keyHash string, cutoff time.Time) (bool, error) {
	reason := "provider_outcome_unknown"
	result := r.db.WithContext(ctx).Model(&model.EmailSendLog{}).
		Where("idempotency_scope = ? AND idempotency_key_hash = ? AND status = ? AND submitted_at < ?", scope, keyHash, "pending", databaseWriteDatetimeUTC(cutoff)).
		Updates(map[string]any{"status": "failed", "failure_reason": reason})
	return result.RowsAffected == 1, result.Error
}
func (r *EmailRepository) FindSendLogByBusinessNo(ctx context.Context, businessNo string) (*model.EmailSendLog, error) {
	var v model.EmailSendLog
	err := r.db.WithContext(ctx).Where("business_request_no = ?", businessNo).First(&v).Error
	if err == nil {
		normalizeEmailSendLogDatabaseUTC(&v)
	}
	return &v, err
}
func (r *EmailRepository) ListSendLogs(ctx context.Context, scene, purpose, status string, templateID uint64, start, end *time.Time, offset, limit int) ([]model.EmailSendLog, int64, error) {
	// pending 仅供数据库内部幂等恢复，任何管理端列表都不得公开。
	q := r.db.WithContext(ctx).Model(&model.EmailSendLog{}).Where("status IN ?", []string{"accepted", "failed"})
	if scene != "" {
		q = q.Where("scene=?", scene)
	}
	if purpose != "" {
		q = q.Where("purpose=?", purpose)
	}
	if status != "" {
		q = q.Where("status=?", status)
	}
	if templateID > 0 {
		q = q.Where("template_id=?", templateID)
	}
	if start != nil {
		q = q.Where("submitted_at>=?", databaseWriteDatetimeUTC(*start))
	}
	if end != nil {
		q = q.Where("submitted_at<?", databaseWriteDatetimeUTC(*end))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var v []model.EmailSendLog
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&v).Error
	for i := range v {
		normalizeEmailSendLogDatabaseUTC(&v[i])
	}
	return v, total, err
}

type EmailSummary struct {
	TemplateTotal, ApprovedCount, LocalEnabledCount, UnboundSceneCount, SubmittedTodayCount, FailedTodayCount int64
	LastSyncedAt                                                                                              *time.Time
}

func (r *EmailRepository) Summary(ctx context.Context, startUTC, endUTC time.Time) (EmailSummary, error) {
	var s EmailSummary
	db := r.db.WithContext(ctx)
	queries := []struct {
		dest *int64
		q    *gorm.DB
	}{{&s.TemplateTotal, db.Model(&model.EmailProviderTemplate{})}, {&s.ApprovedCount, db.Model(&model.EmailProviderTemplate{}).Where("provider_status=? AND missing=?", "approved", false)}, {&s.LocalEnabledCount, db.Model(&model.EmailProviderTemplate{}).Where("local_enabled=?", true)}, {&s.UnboundSceneCount, db.Model(&model.EmailSceneBinding{}).Where("template_id IS NULL")}, {&s.SubmittedTodayCount, db.Model(&model.EmailSendLog{}).Where("status IN ? AND submitted_at>=? AND submitted_at<?", []string{"accepted", "failed"}, databaseWriteDatetimeUTC(startUTC), databaseWriteDatetimeUTC(endUTC))}, {&s.FailedTodayCount, db.Model(&model.EmailSendLog{}).Where("status=? AND submitted_at>=? AND submitted_at<?", "failed", databaseWriteDatetimeUTC(startUTC), databaseWriteDatetimeUTC(endUTC))}}
	for _, x := range queries {
		if err := x.q.Count(x.dest).Error; err != nil {
			return s, err
		}
	}
	var run model.EmailTemplateSyncRun
	if err := db.Where("status=?", "succeeded").Order("completed_at DESC").First(&run).Error; err == nil {
		s.LastSyncedAt = databaseDatetimeUTCPointer(run.CompletedAt)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return s, err
	}
	return s, nil
}

package repository

import (
	"time"

	"molin/server/internal/modules/auth/model"
)

// databaseDatetimeUTC 把无时区的 MySQL DATETIME 墙上时间明确解释为 UTC。
// 当前全局 DSN 使用 loc=Local，驱动扫描时会给 DATETIME 附加进程本地时区；这里不能调用 t.UTC()，
// 因为那会把原本代表 12:00 UTC 的数据库值错误换算为 04:00 UTC。
func databaseDatetimeUTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	// 当前 migration 使用 DATETIME 而非 DATETIME(6)，持久化契约只到秒；丢弃 Go 侧不可落库的纳秒。
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

func databaseDatetimeUTCPointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := databaseDatetimeUTC(*t)
	return &utc
}

// databaseWriteDatetimeUTC 为 loc=Local 的 MySQL 驱动构造“UTC 墙上时间”参数。
// 驱动会先把 time.Time 转为 loc 再格式化 DATETIME；因此直接绑定 UTC 瞬间会在上海进程下写成加八小时。
// 这里保留 UTC 年月日时分秒，但使用驱动的 Local 区域构造参数，最终数据库文本仍是 UTC 墙上时间。
func databaseWriteDatetimeUTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	utc := t.UTC().Truncate(time.Second)
	return time.Date(utc.Year(), utc.Month(), utc.Day(), utc.Hour(), utc.Minute(), utc.Second(), 0, time.Local)
}

func databaseWriteDatetimeUTCPointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	stored := databaseWriteDatetimeUTC(*t)
	return &stored
}

// normalizeVerificationCodeDatabaseUTC 统一验证码扫描结果，避免进程时区改变十分钟有效窗口。
func normalizeVerificationCodeDatabaseUTC(v *model.VerificationCode) {
	if v == nil {
		return
	}
	v.AcceptedAt = databaseDatetimeUTCPointer(v.AcceptedAt)
	v.ExpiresAt = databaseDatetimeUTC(v.ExpiresAt)
	v.UsedAt = databaseDatetimeUTCPointer(v.UsedAt)
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
}

// normalizeEmailSendLogDatabaseUTC 统一发送日志扫描结果，确保 pending、failed 与 accepted 共用 UTC 时间域。
func normalizeEmailSendLogDatabaseUTC(v *model.EmailSendLog) {
	if v == nil {
		return
	}
	v.ExpiresAt = databaseDatetimeUTCPointer(v.ExpiresAt)
	v.SubmittedAt = databaseDatetimeUTC(v.SubmittedAt)
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
}

func normalizeEmailTemplateDatabaseUTC(v *model.EmailProviderTemplate) {
	if v == nil {
		return
	}
	v.MissingSince = databaseDatetimeUTCPointer(v.MissingSince)
	v.ProviderCreatedAt = databaseDatetimeUTCPointer(v.ProviderCreatedAt)
	v.LastSyncedAt = databaseDatetimeUTC(v.LastSyncedAt)
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
	v.UpdatedAt = databaseDatetimeUTC(v.UpdatedAt)
}

// normalizeEmailSceneBindingDatabaseUTC 统一场景绑定的数据库时间语义，避免管理端读取时出现八小时漂移。
func normalizeEmailSceneBindingDatabaseUTC(v *model.EmailSceneBinding) {
	if v == nil {
		return
	}
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
	v.UpdatedAt = databaseDatetimeUTC(v.UpdatedAt)
}

// normalizeEmailAllowlistDatabaseUTC 统一测试收件人白名单的创建、更新与撤销时间语义。
func normalizeEmailAllowlistDatabaseUTC(v *model.EmailTestRecipientAllowlist) {
	if v == nil {
		return
	}
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
	v.UpdatedAt = databaseDatetimeUTC(v.UpdatedAt)
	v.RevokedAt = databaseDatetimeUTCPointer(v.RevokedAt)
}

// normalizeEmailBootstrapReceiptDatabaseUTC 统一一次性 bootstrap 成功凭据的创建时间语义。
func normalizeEmailBootstrapReceiptDatabaseUTC(v *model.EmailAdminVerifyBootstrapReceipt) {
	if v == nil {
		return
	}
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
}

func normalizeEmailSyncRunDatabaseUTC(v *model.EmailTemplateSyncRun) {
	if v == nil {
		return
	}
	v.StartedAt = databaseDatetimeUTC(v.StartedAt)
	v.CompletedAt = databaseDatetimeUTCPointer(v.CompletedAt)
	v.CreatedAt = databaseDatetimeUTC(v.CreatedAt)
}

// prepareEmailSyncRunWriteUTC 覆盖同步记录所有 DATETIME 写入字段，与扫描归一形成对称边界。
func prepareEmailSyncRunWriteUTC(v *model.EmailTemplateSyncRun, now time.Time) {
	if v == nil {
		return
	}
	v.StartedAt = databaseWriteDatetimeUTC(v.StartedAt)
	v.CompletedAt = databaseWriteDatetimeUTCPointer(v.CompletedAt)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
}

// prepareEmailTemplateWriteUTC 统一同步事务内模板镜像的时间字段，避免同步记录正确但镜像时间仍漂移。
func prepareEmailTemplateWriteUTC(v *model.EmailProviderTemplate, now time.Time) {
	if v == nil {
		return
	}
	v.MissingSince = databaseWriteDatetimeUTCPointer(v.MissingSince)
	v.ProviderCreatedAt = databaseWriteDatetimeUTCPointer(v.ProviderCreatedAt)
	v.LastSyncedAt = databaseWriteDatetimeUTC(v.LastSyncedAt)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.UpdatedAt = databaseWriteDatetimeUTC(v.UpdatedAt)
	}
}

// prepareEmailSceneBindingWriteUTC 覆盖场景绑定全部 DATETIME 写入字段。
func prepareEmailSceneBindingWriteUTC(v *model.EmailSceneBinding, now time.Time) {
	if v == nil {
		return
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.UpdatedAt = databaseWriteDatetimeUTC(v.UpdatedAt)
	}
}

// prepareEmailAllowlistWriteUTC 覆盖测试收件人白名单全部 DATETIME 写入字段。
func prepareEmailAllowlistWriteUTC(v *model.EmailTestRecipientAllowlist, now time.Time) {
	if v == nil {
		return
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.UpdatedAt = databaseWriteDatetimeUTC(v.UpdatedAt)
	}
	v.RevokedAt = databaseWriteDatetimeUTCPointer(v.RevokedAt)
}

// prepareEmailBootstrapReceiptWriteUTC 覆盖一次性 bootstrap 成功凭据的创建时间。
func prepareEmailBootstrapReceiptWriteUTC(v *model.EmailAdminVerifyBootstrapReceipt, now time.Time) {
	if v == nil {
		return
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
}

// prepareVerificationCodeWriteUTC 将带时区的 Go 时间先换算为 UTC 瞬间，再写入无时区 DATETIME。
func prepareVerificationCodeWriteUTC(v *model.VerificationCode, now time.Time) {
	if v == nil {
		return
	}
	v.AcceptedAt = databaseWriteDatetimeUTCPointer(v.AcceptedAt)
	v.ExpiresAt = databaseWriteDatetimeUTC(v.ExpiresAt)
	v.UsedAt = databaseWriteDatetimeUTCPointer(v.UsedAt)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
}

func prepareEmailSendLogWriteUTC(v *model.EmailSendLog, now time.Time) {
	if v == nil {
		return
	}
	v.ExpiresAt = databaseWriteDatetimeUTCPointer(v.ExpiresAt)
	v.SubmittedAt = databaseWriteDatetimeUTC(v.SubmittedAt)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = databaseWriteDatetimeUTC(now)
	} else {
		v.CreatedAt = databaseWriteDatetimeUTC(v.CreatedAt)
	}
}

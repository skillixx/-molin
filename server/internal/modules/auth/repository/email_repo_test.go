package repository

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

func TestTemplateMirrorChangedCoversUpdateAndMissingRecovery(t *testing.T) {
	createdAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	base := model.EmailProviderTemplate{Name: "模板", Subject: "主题", TemplateText: "${Code} ${ExpireMinutes}", VariablesJSON: `["Code","ExpireMinutes"]`, ContentSHA256: "fake-hash", ProviderStatus: "approved", VariablesComplete: true, ProviderCreatedAt: &createdAt}
	if templateMirrorChanged(base, base) {
		t.Fatal("完全相同的镜像不得计为更新")
	}
	updated := base
	updated.Subject = "新主题"
	if !templateMirrorChanged(base, updated) {
		t.Fatal("供应商字段变化必须计为更新")
	}
	missing := base
	missing.Missing = true
	if !templateMirrorChanged(missing, base) {
		t.Fatal("missing 模板重新出现必须计为更新并清除 missing")
	}
}

func TestRequireOneAffected(t *testing.T) {
	if err := requireOneAffected(&gorm.DB{RowsAffected: 1}); err != nil {
		t.Fatalf("一行更新应成功: %v", err)
	}
	if err := requireOneAffected(&gorm.DB{}); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("零行更新必须返回冲突: %v", err)
	}
	expected := errors.New("数据库测试错误")
	if err := requireOneAffected(&gorm.DB{Error: expected}); !errors.Is(err, expected) {
		t.Fatalf("数据库错误必须原样返回: %v", err)
	}
}

func TestPointerComparisonsAreValueBased(t *testing.T) {
	a, b := "相同", "相同"
	if !sameStringPointer(&a, &b) || sameStringPointer(&a, nil) {
		t.Fatal("字符串指针比较错误")
	}
	now := time.Now().UTC()
	copy := now
	if !sameTimePointer(&now, &copy) || sameTimePointer(&now, nil) {
		t.Fatal("时间指针比较错误")
	}
}

func TestDatabaseDatetimeIsReinterpretedAsUTC(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	// MySQL DATETIME 不携带时区；loc=Local 扫描后，12:00 会被错误标成上海时间。
	// 邮件仓储必须保留数据库墙上时间 12:00，并把它明确解释为 UTC，而不是换算成 04:00 UTC。
	scanned := time.Date(2026, 7, 27, 12, 0, 0, 0, shanghai)
	got := databaseDatetimeUTC(scanned)
	want := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("数据库 DATETIME 必须按 UTC 墙上时间解释: got=%s want=%s", got, want)
	}
}

func TestNormalizeEmailSendLogDatabaseUTC(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	expires := time.Date(2026, 7, 27, 12, 10, 0, 0, shanghai)
	logEntry := model.EmailSendLog{
		SubmittedAt: time.Date(2026, 7, 27, 12, 0, 0, 0, shanghai),
		ExpiresAt:   &expires,
	}
	normalizeEmailSendLogDatabaseUTC(&logEntry)
	if logEntry.SubmittedAt.Location() != time.UTC || logEntry.SubmittedAt.Hour() != 12 {
		t.Fatalf("提交时间必须按数据库 UTC 语义归一化: %s", logEntry.SubmittedAt)
	}
	if logEntry.ExpiresAt == nil || logEntry.ExpiresAt.Location() != time.UTC || logEntry.ExpiresAt.Hour() != 12 || logEntry.ExpiresAt.Minute() != 10 {
		t.Fatalf("过期时间必须按数据库 UTC 语义归一化: %#v", logEntry.ExpiresAt)
	}
	boundary := time.Date(2026, 7, 27, 12, 10, 0, 0, time.UTC)
	if logEntry.ExpiresAt.After(boundary) {
		t.Fatal("旧 failed 记录恰到真实十分钟边界后不得继续阻断")
	}
	if !logEntry.ExpiresAt.After(boundary.Add(-time.Second)) {
		t.Fatal("旧 failed 记录在数据库边界前一秒仍必须阻断")
	}
}

func TestEmailSyncRunWriteAndScanUseUTCSecondsInShanghai(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	originalLocal := time.Local
	time.Local = shanghai
	defer func() { time.Local = originalLocal }()

	startedInstant := time.Date(2026, 7, 27, 12, 0, 0, 900000000, time.UTC)
	completedInstant := time.Date(2026, 7, 27, 12, 6, 0, 700000000, time.UTC)
	run := model.EmailTemplateSyncRun{StartedAt: startedInstant, CompletedAt: &completedInstant}
	prepareEmailSyncRunWriteUTC(&run, startedInstant)
	if run.StartedAt.Location() != shanghai || run.StartedAt.Hour() != 12 || run.StartedAt.Nanosecond() != 0 {
		t.Fatalf("running.started_at 写入参数必须是 UTC 秒级墙上时间: %s", run.StartedAt)
	}
	if run.CompletedAt == nil || run.CompletedAt.Location() != shanghai || run.CompletedAt.Hour() != 12 || run.CompletedAt.Minute() != 6 || run.CompletedAt.Nanosecond() != 0 {
		t.Fatalf("completed_at 写入参数必须是 UTC 秒级墙上时间: %#v", run.CompletedAt)
	}

	// 模拟 loc=Local 从 DATETIME 扫描后，读边界必须恢复相同 UTC 秒值，而不是再漂移八小时。
	normalizeEmailSyncRunDatabaseUTC(&run)
	if !run.StartedAt.Equal(startedInstant.Truncate(time.Second)) || run.CompletedAt == nil || !run.CompletedAt.Equal(completedInstant.Truncate(time.Second)) {
		t.Fatalf("同步记录 UTC 往返不一致: %#v", run)
	}
}

func TestEmailBootstrapDatetimeRoundTripUsesUTCSecondsInShanghai(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	originalLocal := time.Local
	time.Local = shanghai
	defer func() { time.Local = originalLocal }()

	now := time.Date(2026, 7, 27, 12, 34, 56, 900000000, time.UTC)
	providerCreatedAt := now.Add(-time.Hour)
	template := model.EmailProviderTemplate{
		ProviderCreatedAt: &providerCreatedAt,
		LastSyncedAt:      now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	binding := model.EmailSceneBinding{CreatedAt: now, UpdatedAt: now}
	receipt := model.EmailAdminVerifyBootstrapReceipt{CreatedAt: now}
	revokedAt := now.Add(-time.Minute)
	allowlist := model.EmailTestRecipientAllowlist{CreatedAt: now, UpdatedAt: now, RevokedAt: &revokedAt}

	prepareEmailTemplateWriteUTC(&template, now)
	prepareEmailSceneBindingWriteUTC(&binding, now)
	prepareEmailBootstrapReceiptWriteUTC(&receipt, now)
	prepareEmailAllowlistWriteUTC(&allowlist, now)
	if template.LastSyncedAt.Location() != shanghai || template.LastSyncedAt.Hour() != 12 || template.LastSyncedAt.Nanosecond() != 0 {
		t.Fatalf("bootstrap 模板写入参数不得增加八小时: %s", template.LastSyncedAt)
	}
	if binding.UpdatedAt.Location() != shanghai || binding.UpdatedAt.Hour() != 12 || receipt.CreatedAt.Location() != shanghai || receipt.CreatedAt.Hour() != 12 || allowlist.RevokedAt == nil || allowlist.RevokedAt.Location() != shanghai {
		t.Fatalf("bootstrap、绑定和白名单写入参数不得增加八小时: binding=%s receipt=%s allowlist=%#v", binding.UpdatedAt, receipt.CreatedAt, allowlist.RevokedAt)
	}

	normalizeEmailTemplateDatabaseUTC(&template)
	normalizeEmailSceneBindingDatabaseUTC(&binding)
	normalizeEmailBootstrapReceiptDatabaseUTC(&receipt)
	normalizeEmailAllowlistDatabaseUTC(&allowlist)
	want := now.Truncate(time.Second)
	if !template.LastSyncedAt.Equal(want) || !binding.UpdatedAt.Equal(want) || !receipt.CreatedAt.Equal(want) || allowlist.RevokedAt == nil || !allowlist.RevokedAt.Equal(revokedAt.Truncate(time.Second)) {
		t.Fatalf("邮件仓储 DATETIME 往返必须恢复相同 UTC 秒值: template=%s binding=%s receipt=%s allowlist=%#v", template.LastSyncedAt, binding.UpdatedAt, receipt.CreatedAt, allowlist.RevokedAt)
	}
}

func TestRepeatedIdenticalTemplateSyncDoesNotIncreaseVersionInShanghai(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	providerCreatedAtUTC := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	providerCreatedAtScanned := time.Date(2026, 7, 27, 12, 0, 0, 0, shanghai)
	incoming := model.EmailProviderTemplate{
		Name: "模板", Subject: "主题", TemplateText: "${Code}", VariablesJSON: `["Code"]`,
		ContentSHA256: "fake-hash", ProviderStatus: "approved", VariablesComplete: true,
		ProviderCreatedAt: &providerCreatedAtUTC,
	}
	old := incoming
	old.ProviderCreatedAt = &providerCreatedAtScanned
	old.Version = 7

	version := old.Version
	unchanged := 0
	for range 2 {
		if templateMirrorChangedFromDatabase(old, incoming) {
			version++
			continue
		}
		unchanged++
	}
	if unchanged != 2 || version != old.Version {
		t.Fatalf("连续相同同步必须两次均 unchanged 且版本不递增: unchanged=%d version=%d", unchanged, version)
	}
}

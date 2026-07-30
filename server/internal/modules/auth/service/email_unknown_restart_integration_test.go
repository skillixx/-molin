package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

const (
	emailUnknownRestartAck               = "I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST"
	emailUnknownRestartCleanupAck        = "I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP"
	emailUnknownRestartAddressSecret     = "qa-phase4-address-secret-32-bytes-only"
	emailUnknownRestartIdempotencySecret = "qa-phase4-idempotency-secret-32-bytes"
)

// emailUnknownRestartState 只保存精确恢复所需的隔离标识，不保存完整邮箱、幂等键或业务号。
type emailUnknownRestartState struct {
	Version     int    `json:"version"`
	Phase       string `json:"phase"`
	Nonce       string `json:"nonce"`
	RedisRunID  string `json:"redis_run_id"`
	OperatorID  uint64 `json:"operator_id"`
	TemplateID  uint64 `json:"template_id"`
	AllowlistID uint64 `json:"allowlist_id"`
	SendLogID   uint64 `json:"send_log_id"`
	// UnexpectedSendLogID 仅在新 key 意外落库时记录精确主键；旧 version 1 文件缺少该字段时按零值兼容。
	UnexpectedSendLogID uint64 `json:"unexpected_send_log_id,omitempty"`
}

type emailUnknownRestartAuditor struct{}

func (*emailUnknownRestartAuditor) Record(context.Context, *uint64, string, string, *string, *string, string, any) error {
	return nil
}

// writeEmailUnknownRestartState 使用同目录原子替换，并把状态文件限制为当前账号可读写。
func writeEmailUnknownRestartState(path string, state emailUnknownRestartState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return errors.New("state_encode_failed")
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return errors.New("state_write_failed")
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return errors.New("state_permission_failed")
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("state_replace_failed")
	}
	return nil
}

func readEmailUnknownRestartState(path string) (emailUnknownRestartState, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return emailUnknownRestartState{}, errors.New("state_file_unsafe")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return emailUnknownRestartState{}, errors.New("state_read_failed")
	}
	var state emailUnknownRestartState
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Nonce == "" || state.OperatorID == 0 {
		return emailUnknownRestartState{}, errors.New("state_invalid")
	}
	return state, nil
}

func emailUnknownRestartValues(state emailUnknownRestartState) (string, string, string, string) {
	email := "phase4-" + state.Nonce + "@example.invalid"
	oldKey := "phase4-old-" + state.Nonce
	newKey := "phase4-new-" + state.Nonce
	providerTemplateID := "qa-phase4-" + state.Nonce
	return email, oldKey, newKey, providerTemplateID
}

// emailUnknownRestartIdentity 统一派生跨阶段业务身份，避免 phase1、phase2 和 cleanup 各自拼接导致 scope 漂移。
func emailUnknownRestartIdentity(state emailUnknownRestartState) (recipientHMAC, scope, fingerprint, oldKeyHash, newKeyHash string) {
	email, oldKey, newKey, _ := emailUnknownRestartValues(state)
	recipientHMAC = crypto.HMAC256(normalizeEmailAddress(email), emailUnknownRestartAddressSecret)
	scope = fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", state.OperatorID, state.TemplateID, "register", recipientHMAC)
	fingerprint = hash(fmt.Sprintf("POST\n/api/admin/email/templates/%d/test-send\n%s\n%s", state.TemplateID, "register", recipientHMAC))
	return recipientHMAC, scope, fingerprint, hash(oldKey), hash(newKey)
}

// emailUnknownRestartOwnedLog 严格确认日志属于本轮夹具，避免仅凭主键误删其他测试或业务数据。
func emailUnknownRestartOwnedLog(entry *model.EmailSendLog, state emailUnknownRestartState, scope, fingerprint, recipientHMAC, keyHash string) bool {
	if entry == nil || entry.ID == 0 || entry.TemplateID != state.TemplateID || entry.Scene != "register" || entry.Purpose != "test" {
		return false
	}
	if entry.RecipientHMAC != recipientHMAC || entry.IdempotencyScope != scope || entry.IdempotencyKeyHash != keyHash || entry.RequestFingerprint != fingerprint {
		return false
	}
	if entry.Status != "failed" || entry.FailureReason == nil || *entry.FailureReason != "provider_outcome_unknown" {
		return false
	}
	return true
}

func emailUnknownRestartAdapterCalls(adapter *MockEmailAdapter) int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.Calls
}

// captureEmailUnknownRestartUnexpectedLog 在失败返回前保存新 key 的唯一精确主键，供独立 cleanup 恢复。
func captureEmailUnknownRestartUnexpectedLog(ctx context.Context, repo *repository.EmailRepository, statePath string, state *emailUnknownRestartState, scope, fingerprint, recipientHMAC, newKeyHash string) (bool, error) {
	entry, err := repo.FindSendLogByIdempotency(ctx, scope, newKeyHash)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil || !emailUnknownRestartOwnedLog(entry, *state, scope, fingerprint, recipientHMAC, newKeyHash) {
		return false, errors.New("unexpected_log_ownership_failed")
	}
	if state.UnexpectedSendLogID != 0 && state.UnexpectedSendLogID != entry.ID {
		return false, errors.New("unexpected_log_id_conflict")
	}
	state.UnexpectedSendLogID = entry.ID
	if err := writeEmailUnknownRestartState(statePath, *state); err != nil {
		return false, errors.New("unexpected_log_state_failed")
	}
	return true, nil
}

// TestEmailUnknownRestartStateVersion1Compatibility 离线确认旧 version 1 状态缺少可选字段时仍可安全读取。
func TestEmailUnknownRestartStateVersion1Compatibility(t *testing.T) {
	raw := []byte(`{"version":1,"phase":"phase1_created","nonce":"offline-nonce","redis_run_id":"offline-run","operator_id":7,"template_id":11,"allowlist_id":13,"send_log_id":17}`)
	var state emailUnknownRestartState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal("旧 version 1 状态无法解析")
	}
	if state.Version != 1 || state.Phase != "phase1_created" || state.UnexpectedSendLogID != 0 {
		t.Fatal("旧 version 1 状态的可选恢复字段未保持零值兼容")
	}
	_, scope, fingerprint, oldKeyHash, newKeyHash := emailUnknownRestartIdentity(state)
	if scope == "" || fingerprint == "" || oldKeyHash == "" || newKeyHash == "" || oldKeyHash == newKeyHash {
		t.Fatal("旧 version 1 状态无法稳定派生隔离业务身份")
	}
}

func emailUnknownRestartRedisRunID(ctx context.Context, client *redis.Client) (string, error) {
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		return "", errors.New("redis_info_failed")
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "run_id:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "run_id:"))
			if value != "" {
				return value, nil
			}
		}
	}
	return "", errors.New("redis_run_id_missing")
}

func openEmailUnknownRestartDB(t *testing.T) *gorm.DB {
	t.Helper()
	config := mysqldriver.Config{
		User:      os.Getenv("MYSQL_USER"),
		Passwd:    os.Getenv("MYSQL_PASSWORD"),
		Net:       "tcp",
		Addr:      os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"),
		DBName:    os.Getenv("MYSQL_DATABASE"),
		ParseTime: true,
		// 必须与生产全局 DSN 的 loc=Local 保持一致；仓储会把 UTC 时刻转换为本地时区承载的 UTC 墙钟参数。
		Loc:                  time.Local,
		AllowNativePasswords: true,
	}
	if config.User == "" || config.Addr == ":" || config.DBName == "" {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=mysql_config_missing recovery_state=retained")
	}
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: config.FormatDSN()}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=mysql_connect_failed recovery_state=retained")
	}
	// MySQL DATETIME 不含时区。把 UTC_TIMESTAMP 扫描出的墙钟字段重新标成 UTC 后，必须与应用 UTC 当前时间接近。
	// 该断言在任何夹具写入前执行，可阻止 loc=UTC 与仓储 loc=Local 写契约混用造成八小时漂移。
	var mysqlUTCWall time.Time
	if err := db.Raw("SELECT UTC_TIMESTAMP()").Row().Scan(&mysqlUTCWall); err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=mysql_wall_clock_query_failed recovery_state=retained")
	}
	mysqlUTCInstant := time.Date(mysqlUTCWall.Year(), mysqlUTCWall.Month(), mysqlUTCWall.Day(), mysqlUTCWall.Hour(), mysqlUTCWall.Minute(), mysqlUTCWall.Second(), 0, time.UTC)
	clockDrift := mysqlUTCInstant.Sub(time.Now().UTC())
	if clockDrift < -5*time.Second || clockDrift > 5*time.Second {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=mysql_wall_clock_drift recovery_state=retained")
	}
	var version uint
	var dirty bool
	if err := db.Raw("SELECT version, dirty FROM schema_migrations").Row().Scan(&version, &dirty); err != nil || version != 57 || dirty {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=schema_gate_failed expected_version=57 expected_dirty=false recovery_state=retained")
	}
	return db
}

func openEmailUnknownRestartRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	dbIndex, err := strconv.Atoi(strings.TrimSpace(os.Getenv("REDIS_DB")))
	if addr == "" || err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=redis_config_missing recovery_state=retained")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: dbIndex})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=redis_connect_failed recovery_state=retained")
	}
	return client
}

type emailUnknownRestartCleanupOps struct {
	deleteUnexpected func() (int64, error)
	deletePrimary    func() (int64, error)
	deleteAllowlist  func() (int64, error)
	deleteTemplate   func() (int64, error)
}

// executeEmailUnknownRestartCleanupRows 只编排精确删除顺序和行数断言；事务提交或回滚由调用方统一控制。
func executeEmailUnknownRestartCleanupRows(state emailUnknownRestartState, ops emailUnknownRestartCleanupOps) error {
	if state.UnexpectedSendLogID != 0 {
		if state.UnexpectedSendLogID == state.SendLogID {
			return errors.New("unexpected_log_id_not_distinct")
		}
		rows, err := ops.deleteUnexpected()
		if err != nil || rows != 1 {
			return errors.New("unexpected_send_log_cleanup_failed")
		}
	}
	if state.SendLogID != 0 {
		rows, err := ops.deletePrimary()
		if err != nil || rows != 1 {
			return errors.New("send_log_cleanup_failed")
		}
	}
	if state.AllowlistID != 0 {
		rows, err := ops.deleteAllowlist()
		if err != nil || rows != 1 {
			return errors.New("allowlist_cleanup_failed")
		}
	}
	if state.TemplateID != 0 {
		rows, err := ops.deleteTemplate()
		if err != nil || rows != 1 {
			return errors.New("template_cleanup_failed")
		}
	}
	return nil
}

func TestEmailUnknownRestartCleanupRejectsDuplicateLogIDWithoutDelete(t *testing.T) {
	state := emailUnknownRestartState{SendLogID: 17, UnexpectedSendLogID: 17}
	calls := 0
	op := func() (int64, error) { calls++; return 1, nil }
	err := executeEmailUnknownRestartCleanupRows(state, emailUnknownRestartCleanupOps{deleteUnexpected: op, deletePrimary: op})
	if err == nil || err.Error() != "unexpected_log_id_not_distinct" || calls != 0 {
		t.Fatalf("重复日志主键必须在任何删除前失败: err=%v calls=%d", err, calls)
	}
}

func TestEmailUnknownRestartCleanupRejectsOwnershipMismatch(t *testing.T) {
	state := emailUnknownRestartState{SendLogID: 17, UnexpectedSendLogID: 18, AllowlistID: 19, TemplateID: 20}
	laterCalls := 0
	err := executeEmailUnknownRestartCleanupRows(state, emailUnknownRestartCleanupOps{
		// RowsAffected=0 表示精确归属谓词不匹配，必须立即停止而不是按主键继续删除。
		deleteUnexpected: func() (int64, error) { return 0, nil },
		deletePrimary:    func() (int64, error) { laterCalls++; return 1, nil },
		deleteAllowlist:  func() (int64, error) { laterCalls++; return 1, nil },
		deleteTemplate:   func() (int64, error) { laterCalls++; return 1, nil },
	})
	if err == nil || err.Error() != "unexpected_send_log_cleanup_failed" || laterCalls != 0 {
		t.Fatalf("归属不匹配必须停止后续删除: err=%v later_calls=%d", err, laterCalls)
	}
}

func TestEmailUnknownRestartCleanupLaterFailureRollsBackLogicalTransaction(t *testing.T) {
	state := emailUnknownRestartState{SendLogID: 17, UnexpectedSendLogID: 18, AllowlistID: 19, TemplateID: 20}
	committed := map[string]bool{"unexpected": true, "primary": true, "allowlist": true, "template": true}
	working := map[string]bool{}
	for key, value := range committed {
		working[key] = value
	}
	deleteWorking := func(key string, fail bool) func() (int64, error) {
		return func() (int64, error) {
			working[key] = false
			if fail {
				return 0, errors.New("injected_delete_failure")
			}
			return 1, nil
		}
	}
	err := executeEmailUnknownRestartCleanupRows(state, emailUnknownRestartCleanupOps{
		deleteUnexpected: deleteWorking("unexpected", false),
		deletePrimary:    deleteWorking("primary", false),
		deleteAllowlist:  deleteWorking("allowlist", true),
		deleteTemplate:   deleteWorking("template", false),
	})
	// 生产调用方只在编排成功时提交 GORM 事务；注入后续失败时工作副本不会覆盖已提交状态。
	if err == nil {
		committed = working
	}
	if err == nil || err.Error() != "allowlist_cleanup_failed" {
		t.Fatalf("后续删除失败分类错误: %v", err)
	}
	for key, exists := range committed {
		if !exists {
			t.Fatalf("事务失败后先前删除不得提交: key=%s state=%v", key, committed)
		}
	}
	if !working["template"] {
		t.Fatal("失败后不得继续执行模板删除")
	}
}

// cleanupEmailUnknownRestartFixture 只按状态文件记录的主键和推导出的唯一锁键清理本轮数据。
func cleanupEmailUnknownRestartFixture(ctx context.Context, db *gorm.DB, client *redis.Client, state emailUnknownRestartState) error {
	_, _, _, providerTemplateID := emailUnknownRestartValues(state)
	recipientHMAC, scope, fingerprint, oldKeyHash, newKeyHash := emailUnknownRestartIdentity(state)
	lockKey := "lock:email:dispatch:" + crypto.HMAC256(scope, emailUnknownRestartIdempotencySecret)
	// 先清理可幂等重试的唯一 Redis key；数据库三行随后在同一事务内同成同败。
	if err := client.Del(ctx, lockKey).Err(); err != nil {
		return errors.New("redis_cleanup_failed")
	}
	if exists, err := client.Exists(ctx, lockKey).Result(); err != nil || exists != 0 {
		return errors.New("redis_cleanup_unverified")
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deleteRows := func(result *gorm.DB) (int64, error) { return result.RowsAffected, result.Error }
		return executeEmailUnknownRestartCleanupRows(state, emailUnknownRestartCleanupOps{
			deleteUnexpected: func() (int64, error) {
				return deleteRows(tx.Where("id = ? AND template_id = ? AND scene = ? AND purpose = ? AND recipient_hmac = ? AND idempotency_scope = ? AND idempotency_key_hash = ? AND request_fingerprint = ? AND status = ? AND failure_reason = ?", state.UnexpectedSendLogID, state.TemplateID, "register", "test", recipientHMAC, scope, newKeyHash, fingerprint, "failed", "provider_outcome_unknown").Delete(&model.EmailSendLog{}))
			},
			deletePrimary: func() (int64, error) {
				return deleteRows(tx.Where("id = ? AND template_id = ? AND scene = ? AND purpose = ? AND recipient_hmac = ? AND idempotency_scope = ? AND idempotency_key_hash = ? AND request_fingerprint = ? AND status = ? AND failure_reason = ?", state.SendLogID, state.TemplateID, "register", "test", recipientHMAC, scope, oldKeyHash, fingerprint, "failed", "provider_outcome_unknown").Delete(&model.EmailSendLog{}))
			},
			deleteAllowlist: func() (int64, error) {
				return deleteRows(tx.Where("id = ? AND email_hmac = ?", state.AllowlistID, recipientHMAC).Delete(&model.EmailTestRecipientAllowlist{}))
			},
			deleteTemplate: func() (int64, error) {
				return deleteRows(tx.Where("id = ? AND provider_template_id = ?", state.TemplateID, providerTemplateID).Delete(&model.EmailProviderTemplate{}))
			},
		})
	}); err != nil {
		return err
	}
	return nil
}

// TestEmailUnknownTombstoneSurvivesRedisRestart 是两阶段集成门禁。
// phase1 创建数据库 unknown 墓碑后必须退出；维护人员只能重启测试 Redis，再运行 phase2。
func TestEmailUnknownTombstoneSurvivesRedisRestart(t *testing.T) {
	if os.Getenv("RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION") != "1" || os.Getenv("EMAIL_UNKNOWN_RESTART_ACK") != emailUnknownRestartAck {
		t.Skip("email_unknown_restart=SKIP")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "test" || strings.ToLower(strings.TrimSpace(os.Getenv("EMAIL_ADAPTER"))) != "mock" {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=environment_gate_failed expected_app_env=test expected_adapter=mock recovery_state=retained")
	}
	phase := strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_PHASE"))
	if phase == "cleanup" && (os.Getenv("RUN_EMAIL_UNKNOWN_RESTART_CLEANUP") != "1" || os.Getenv("EMAIL_UNKNOWN_RESTART_CLEANUP_ACK") != emailUnknownRestartCleanupAck) {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup classification=cleanup_gate_denied recovery_state=retained")
	}
	statePath, err := filepath.Abs(strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_STATE_FILE")))
	if err != nil || strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_STATE_FILE")) == "" {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=state_path_invalid recovery_state=retained")
	}
	operatorID, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_OPERATOR_ID")), 10, 64)
	if err != nil || operatorID == 0 {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=operator_invalid recovery_state=retained")
	}
	db := openEmailUnknownRestartDB(t)
	client := openEmailUnknownRestartRedis(t)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if phase == "phase1" {
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=state_already_exists recovery_state=retained")
		}
		var userCount int64
		if err := db.WithContext(ctx).Table("users").Where("id = ?", operatorID).Count(&userCount).Error; err != nil || userCount != 1 {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=operator_not_found recovery_state=retained")
		}
		nonce, err := randomNonce()
		if err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=nonce_failed recovery_state=retained")
		}
		redisRunID, err := emailUnknownRestartRedisRunID(ctx, client)
		if err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=redis_identity_failed recovery_state=retained")
		}
		state := emailUnknownRestartState{Version: 1, Phase: "initializing", Nonce: nonce, RedisRunID: redisRunID, OperatorID: operatorID}
		if err := writeEmailUnknownRestartState(statePath, state); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=state_create_failed recovery_state=not_created")
		}
		email, oldKey, _, providerTemplateID := emailUnknownRestartValues(state)
		now := time.Now().UTC().Truncate(time.Second)
		templateText := "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
		tpl := model.EmailProviderTemplate{Provider: emailProvider, ProviderTemplateID: providerTemplateID, Name: "Phase4 Redis 重启隔离模板", Subject: "Phase4 隔离验证", TemplateText: templateText, VariablesJSON: `["Code","ExpireMinutes"]`, ContentSHA256: hash(templateText), ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, LastSyncedAt: now, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.WithContext(ctx).Create(&tpl).Error; err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=template_fixture_failed recovery_state=retained")
		}
		state.TemplateID = tpl.ID
		if err := writeEmailUnknownRestartState(statePath, state); err != nil {
			_ = db.WithContext(ctx).Where("id = ? AND provider_template_id = ?", tpl.ID, providerTemplateID).Delete(&model.EmailProviderTemplate{}).Error
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=state_update_failed recovery_state=retained")
		}
		repo := repository.NewEmailRepository(db)
		adapter := &MockEmailAdapter{SendError: context.DeadlineExceeded}
		svc := NewEmailService(repo, nil, adapter, &emailUnknownRestartAuditor{}, client, emailUnknownRestartAddressSecret, emailUnknownRestartIdempotencySecret, "test", "mock")
		allowlist := model.EmailTestRecipientAllowlist{EmailHMAC: svc.emailHMAC(email), EmailMasked: maskEmailAddress(email), Status: "active", Version: 1, CreatedBy: operatorID, UpdatedBy: operatorID, CreatedAt: now, UpdatedAt: now}
		if err := db.WithContext(ctx).Create(&allowlist).Error; err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=allowlist_fixture_failed recovery_state=retained")
		}
		state.AllowlistID = allowlist.ID
		if err := writeEmailUnknownRestartState(statePath, state); err != nil {
			_ = db.WithContext(ctx).Where("id = ? AND email_hmac = ?", allowlist.ID, allowlist.EmailHMAC).Delete(&model.EmailTestRecipientAllowlist{}).Error
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=state_update_failed recovery_state=retained")
		}
		_, sendErr := svc.TestSend(ctx, tpl.ID, "register", email, oldKey, operatorID, "127.0.0.1")
		scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", operatorID, tpl.ID, "register", svc.emailHMAC(email))
		entry, findErr := repo.FindSendLogByIdempotency(ctx, scope, hash(oldKey))
		if findErr == nil {
			state.SendLogID = entry.ID
			state.Phase = "phase1_created"
			if err := writeEmailUnknownRestartState(statePath, state); err != nil {
				t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=state_update_failed recovery_state=retained")
			}
		}
		if !errors.Is(sendErr, ErrEmailOutcomeUnknown) || adapter.Calls != 1 || findErr != nil || entry.Status != "failed" || entry.FailureReason == nil || *entry.FailureReason != "provider_outcome_unknown" {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=tombstone_assertion_failed recovery_state=retained")
		}
		t.Log("[PASS] mode=email_unknown_restart phase=phase1 classification=tombstone_created schema=57 dirty=false adapter_calls=1 recovery_state=retained redis_restart_required=true")
		return
	}

	state, err := readEmailUnknownRestartState(statePath)
	if err != nil || state.OperatorID != operatorID {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=state_load_failed recovery_state=retained")
	}
	if phase == "cleanup" {
		if err := cleanupEmailUnknownRestartFixture(ctx, db, client, state); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup classification=cleanup_failed recovery_state=retained")
		}
		if err := os.Remove(statePath); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup classification=state_remove_failed recovery_state=retained")
		}
		t.Log("[PASS] mode=email_unknown_restart phase=cleanup classification=exact_cleanup_complete cleanup_db=true cleanup_key=true state_removed=true")
		return
	}
	if phase != "phase2" || state.Phase != "phase1_created" || state.TemplateID == 0 || state.AllowlistID == 0 || state.SendLogID == 0 {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=phase2 classification=phase_order_invalid recovery_state=retained")
	}
	currentRedisRunID, err := emailUnknownRestartRedisRunID(ctx, client)
	if err != nil || currentRedisRunID == state.RedisRunID {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=phase2 classification=redis_restart_unproven recovery_state=retained")
	}
	email, oldKey, newKey, _ := emailUnknownRestartValues(state)
	recipientHMAC, scope, fingerprint, oldKeyHash, newKeyHash := emailUnknownRestartIdentity(state)
	repo := repository.NewEmailRepository(db)
	// 任何 TestSend 前先核验原墓碑的完整归属与状态，防止错误夹具进入 Adapter。
	original, originalErr := repo.FindSendLogByIdempotency(ctx, scope, oldKeyHash)
	if originalErr != nil || original.ID != state.SendLogID || !emailUnknownRestartOwnedLog(original, state, scope, fingerprint, recipientHMAC, oldKeyHash) {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=phase2 classification=tombstone_preflight_failed tombstone_owned=false adapter_calls=0 recovery_state=retained")
	}
	// 先捕获此前失败运行可能留下的新 key 日志；一旦存在便停止，不得再次调用 Adapter。
	unexpectedPresent, captureErr := captureEmailUnknownRestartUnexpectedLog(ctx, repo, statePath, &state, scope, fingerprint, recipientHMAC, newKeyHash)
	if captureErr != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=phase2 classification=unexpected_log_capture_failed adapter_calls=0 recovery_state=retained")
	}
	if unexpectedPresent {
		t.Fatal("[BLOCKED] mode=email_unknown_restart phase=phase2 classification=unexpected_new_log_present adapter_calls=0 unexpected_log_recorded=true recovery_state=retained cleanup_authorization_required=true")
	}
	// test 墓碑仅有十分钟冷却窗；至少保留两分钟安全裕量，避免调用期间跨过边界产生误外呼。
	if sendCooldownUntil(original).Sub(emailPersistenceNowUTC()) < 120*time.Second {
		t.Fatal("[BLOCKED] mode=email_unknown_restart phase=phase2 classification=cooldown_safety_margin_insufficient cooldown_margin_ok=false adapter_calls=0 recovery_state=retained")
	}
	adapter := &MockEmailAdapter{SendError: context.DeadlineExceeded}
	svc := NewEmailService(repo, nil, adapter, &emailUnknownRestartAuditor{}, client, emailUnknownRestartAddressSecret, emailUnknownRestartIdempotencySecret, "test", "mock")
	_, oldErr := svc.TestSend(ctx, state.TemplateID, "register", email, oldKey, operatorID, "127.0.0.1")
	oldCalls := emailUnknownRestartAdapterCalls(adapter)
	oldUnknown := errors.Is(oldErr, ErrEmailOutcomeUnknown)
	if !oldUnknown || oldCalls != 0 {
		t.Fatalf("[FAIL] mode=email_unknown_restart phase=phase2 classification=old_key_assertion_failed old_key_unknown=%t adapter_calls=%d recovery_state=retained", oldUnknown, oldCalls)
	}
	_, newErr := svc.TestSend(ctx, state.TemplateID, "register", email, newKey, operatorID, "127.0.0.1")
	newCalls := emailUnknownRestartAdapterCalls(adapter)
	newPending := errors.Is(newErr, ErrEmailOutcomePending)
	unexpectedRecorded, captureErr := captureEmailUnknownRestartUnexpectedLog(ctx, repo, statePath, &state, scope, fingerprint, recipientHMAC, newKeyHash)
	if captureErr != nil {
		t.Fatalf("[FAIL] mode=email_unknown_restart phase=phase2 classification=unexpected_log_capture_failed new_key_pending=%t adapter_calls=%d recovery_state=retained", newPending, newCalls)
	}
	if !newPending || newCalls != 0 || unexpectedRecorded {
		t.Fatalf("[FAIL] mode=email_unknown_restart phase=phase2 classification=new_key_assertion_failed new_key_pending=%t adapter_calls=%d unexpected_log_recorded=%t recovery_state=retained cleanup_authorization_required=%t", newPending, newCalls, unexpectedRecorded, unexpectedRecorded)
	}
	state.Phase = "phase2_verified"
	if err := writeEmailUnknownRestartState(statePath, state); err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart phase=phase2 classification=state_update_failed recovery_state=retained")
	}
	t.Log("[PASS] mode=email_unknown_restart phase=phase2 classification=db_tombstone_blocked schema=57 dirty=false old_key_blocked=true new_key_blocked=true adapter_calls=0 cleanup_performed=false test_data=retained recovery_state=retained cleanup_authorization_required=true")
}

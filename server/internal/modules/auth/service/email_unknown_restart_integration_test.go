package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

const (
	emailUnknownRestartAck                = "I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST"
	emailUnknownRestartCleanupAck         = "I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP"
	emailUnknownRestartAddressSecret      = "qa-phase4-address-secret-32-bytes-only"
	emailUnknownRestartIdempotencySecret  = "qa-phase4-idempotency-secret-32-bytes"
	emailUnknownRestartLogPredicate       = "id = ? AND template_id = ? AND provider_template_id = ? AND provider = ? AND verification_code_id IS NULL AND scene = ? AND purpose = ? AND recipient_hmac = ? AND idempotency_scope = ? AND idempotency_key_hash = ? AND request_fingerprint = ? AND status = ? AND failure_reason = ?"
	emailUnknownRestartAllowlistPredicate = "id = ? AND email_hmac = ? AND email_masked = ? AND status = ? AND version = ? AND created_by = ? AND updated_by = ? AND revoked_at IS NULL"
	emailUnknownRestartTemplatePredicate  = "id = ? AND provider = ? AND provider_template_id = ? AND name = ? AND subject = ? AND sender_nickname IS NULL AND template_text = ? AND JSON_LENGTH(variables_json) = 2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes')) AND content_sha256 = ? AND provider_status = ? AND review_comment IS NULL AND variables_complete = ? AND local_enabled = ? AND missing = ? AND missing_since IS NULL AND provider_created_at IS NULL AND version = ?"
)

var (
	emailUnknownRestartNoncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	emailUnknownRestartRunIDPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
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

type emailUnknownRestartStateFileOps struct {
	lstat        func(string) (os.FileInfo, error)
	readFile     func(string) ([]byte, error)
	ownerMatches func(os.FileInfo) bool
}

// decodeEmailUnknownRestartState 使用流式 JSON 解码拒绝重复键、未知字段和尾随内容。
// 不能先解码到 map 或 struct，否则重复键会被后值静默覆盖，破坏冻结状态身份。
func decodeEmailUnknownRestartState(raw []byte) (emailUnknownRestartState, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return emailUnknownRestartState{}, errors.New("state_invalid")
	}
	state := emailUnknownRestartState{}
	seen := make(map[string]struct{}, 9)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return emailUnknownRestartState{}, errors.New("state_invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return emailUnknownRestartState{}, errors.New("state_duplicate_field")
		}
		seen[key] = struct{}{}
		var decodeErr error
		switch key {
		case "version":
			decodeErr = decoder.Decode(&state.Version)
		case "phase":
			decodeErr = decoder.Decode(&state.Phase)
		case "nonce":
			decodeErr = decoder.Decode(&state.Nonce)
		case "redis_run_id":
			decodeErr = decoder.Decode(&state.RedisRunID)
		case "operator_id":
			decodeErr = decoder.Decode(&state.OperatorID)
		case "template_id":
			decodeErr = decoder.Decode(&state.TemplateID)
		case "allowlist_id":
			decodeErr = decoder.Decode(&state.AllowlistID)
		case "send_log_id":
			decodeErr = decoder.Decode(&state.SendLogID)
		case "unexpected_send_log_id":
			decodeErr = decoder.Decode(&state.UnexpectedSendLogID)
		default:
			return emailUnknownRestartState{}, errors.New("state_unknown_field")
		}
		if decodeErr != nil {
			return emailUnknownRestartState{}, errors.New("state_invalid")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return emailUnknownRestartState{}, errors.New("state_invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return emailUnknownRestartState{}, errors.New("state_trailing_content")
	}
	if state.Version != 1 || state.Nonce == "" || state.OperatorID == 0 {
		return emailUnknownRestartState{}, errors.New("state_invalid")
	}
	return state, nil
}

func readEmailUnknownRestartStateWithOps(path string, ops emailUnknownRestartStateFileOps) (emailUnknownRestartState, error) {
	if ops.lstat == nil || ops.readFile == nil || ops.ownerMatches == nil {
		return emailUnknownRestartState{}, errors.New("state_file_ops_invalid")
	}
	info, err := ops.lstat(path)
	if err != nil || info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return emailUnknownRestartState{}, errors.New("state_file_unsafe")
	}
	if !ops.ownerMatches(info) {
		return emailUnknownRestartState{}, errors.New("state_file_owner_mismatch")
	}
	raw, err := ops.readFile(path)
	if err != nil {
		return emailUnknownRestartState{}, errors.New("state_read_failed")
	}
	return decodeEmailUnknownRestartState(raw)
}

func readEmailUnknownRestartState(path string) (emailUnknownRestartState, error) {
	return readEmailUnknownRestartStateWithOps(path, emailUnknownRestartStateFileOps{
		lstat:        os.Lstat,
		readFile:     os.ReadFile,
		ownerMatches: emailUnknownRestartStateOwnedByEffectiveUser,
	})
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

type emailUnknownRestartFakeFileInfo struct {
	mode os.FileMode
}

func (info emailUnknownRestartFakeFileInfo) Name() string       { return "state" }
func (info emailUnknownRestartFakeFileInfo) Size() int64        { return 1 }
func (info emailUnknownRestartFakeFileInfo) Mode() os.FileMode  { return info.mode }
func (info emailUnknownRestartFakeFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (info emailUnknownRestartFakeFileInfo) IsDir() bool        { return false }
func (info emailUnknownRestartFakeFileInfo) Sys() any           { return nil }

func TestEmailUnknownRestartStateReaderRejectsSymlinkBeforeRead(t *testing.T) {
	readCalls := 0
	_, err := readEmailUnknownRestartStateWithOps("state", emailUnknownRestartStateFileOps{
		lstat: func(string) (os.FileInfo, error) {
			return emailUnknownRestartFakeFileInfo{mode: os.ModeSymlink | 0o600}, nil
		},
		readFile:     func(string) ([]byte, error) { readCalls++; return nil, nil },
		ownerMatches: func(os.FileInfo) bool { return true },
	})
	if err == nil || err.Error() != "state_file_unsafe" || readCalls != 0 {
		t.Fatalf("符号链接必须在读取前失败: err=%v read_calls=%d", err, readCalls)
	}
}

func TestEmailUnknownRestartStateReaderRejectsOwnerMismatchBeforeRead(t *testing.T) {
	readCalls := 0
	_, err := readEmailUnknownRestartStateWithOps("state", emailUnknownRestartStateFileOps{
		lstat:        func(string) (os.FileInfo, error) { return emailUnknownRestartFakeFileInfo{mode: 0o600}, nil },
		readFile:     func(string) ([]byte, error) { readCalls++; return nil, nil },
		ownerMatches: func(os.FileInfo) bool { return false },
	})
	if err == nil || err.Error() != "state_file_owner_mismatch" || readCalls != 0 {
		t.Fatalf("owner 不匹配必须在读取前失败: err=%v read_calls=%d", err, readCalls)
	}
}

func TestEmailUnknownRestartStateDecoderRejectsDuplicateAndUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "重复字段", raw: `{"version":1,"version":1,"phase":"phase1_created","nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","redis_run_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","operator_id":7}`},
		{name: "未知字段", raw: `{"version":1,"phase":"phase1_created","nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","redis_run_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","operator_id":7,"unknown":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeEmailUnknownRestartState([]byte(test.raw)); err == nil {
				t.Fatal("重复或未知字段必须失败关闭")
			}
		})
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
	lockScope        func() error
	lockUnexpected   func() error
	lockPrimary      func() error
	lockAllowlist    func() error
	lockTemplate     func() error
	deleteUnexpected func() (int64, error)
	deletePrimary    func() (int64, error)
	deleteAllowlist  func() (int64, error)
	deleteTemplate   func() (int64, error)
}

type emailUnknownRestartCleanupRuntime struct {
	redisExists func(context.Context) (int64, error)
	cleanupDB   func(context.Context) error
}

// emailUnknownRestartVerifiedCleanupOps 只描述成功周期的一条日志、白名单和模板。
// 它与历史失败现场的双日志 cleanup 分离，避免放宽既有恢复契约。
type emailUnknownRestartVerifiedCleanupOps struct {
	lockScope       func() error
	lockPrimary     func() error
	lockAllowlist   func() error
	lockTemplate    func() error
	deletePrimary   func() (int64, error)
	deleteAllowlist func() (int64, error)
	deleteTemplate  func() (int64, error)
}

// validateEmailUnknownRestartCleanupState 在任何 Redis 或数据库访问前冻结历史夹具身份。
// cleanup 只接受本轮已确认的 phase1_created 形态，禁止用零值兼容逻辑跳过任何授权对象。
func validateEmailUnknownRestartCleanupState(state emailUnknownRestartState) error {
	if state.Version != 1 || state.Phase != "phase1_created" {
		return errors.New("cleanup_state_phase_invalid")
	}
	if !emailUnknownRestartNoncePattern.MatchString(state.Nonce) || !emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID) {
		return errors.New("cleanup_state_identity_invalid")
	}
	if state.OperatorID == 0 || state.TemplateID == 0 || state.AllowlistID == 0 || state.SendLogID == 0 || state.UnexpectedSendLogID == 0 {
		return errors.New("cleanup_state_id_missing")
	}
	if state.SendLogID == state.UnexpectedSendLogID {
		return errors.New("unexpected_log_id_not_distinct")
	}
	return nil
}

// validateEmailUnknownRestartVerifiedCleanupState 冻结成功 phase2 的唯一合法清理形态。
// 成功周期不得出现意外新 key 日志，因此 UnexpectedSendLogID 必须保持为零。
func validateEmailUnknownRestartVerifiedCleanupState(state emailUnknownRestartState) error {
	if state.Version != 1 || state.Phase != "phase2_verified" {
		return errors.New("verified_cleanup_state_phase_invalid")
	}
	if !emailUnknownRestartNoncePattern.MatchString(state.Nonce) || !emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID) {
		return errors.New("verified_cleanup_state_identity_invalid")
	}
	if state.OperatorID == 0 || state.TemplateID == 0 || state.AllowlistID == 0 || state.SendLogID == 0 {
		return errors.New("verified_cleanup_state_id_missing")
	}
	if state.UnexpectedSendLogID != 0 {
		return errors.New("verified_cleanup_unexpected_log_present")
	}
	return nil
}

// validateEmailUnknownRestartPhase1CleanupState 只接受 phase1 已完整落盘但尚未重启 Redis 的单日志现场。
// 该恢复形态不得带有意外新 key 日志，且三个夹具主键必须全部由状态文件冻结。
func validateEmailUnknownRestartPhase1CleanupState(state emailUnknownRestartState) error {
	if state.Version != 1 || state.Phase != "phase1_created" {
		return errors.New("phase1_cleanup_state_phase_invalid")
	}
	if !emailUnknownRestartNoncePattern.MatchString(state.Nonce) || !emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID) {
		return errors.New("phase1_cleanup_state_identity_invalid")
	}
	if state.OperatorID == 0 || state.TemplateID == 0 || state.AllowlistID == 0 || state.SendLogID == 0 {
		return errors.New("phase1_cleanup_state_id_missing")
	}
	if state.UnexpectedSendLogID != 0 {
		return errors.New("phase1_cleanup_unexpected_log_present")
	}
	return nil
}

// executeEmailUnknownRestartCleanupRows 先锁定并核验完整归属，再执行四项精确删除。
// 该函数必须运行在同一数据库事务内；任一锁定、归属或行数断言失败都会由调用方回滚。
func executeEmailUnknownRestartCleanupRows(ops emailUnknownRestartCleanupOps) error {
	locks := []struct {
		name string
		call func() error
	}{
		{name: "scope", call: ops.lockScope},
		{name: "unexpected_send_log", call: ops.lockUnexpected},
		{name: "send_log", call: ops.lockPrimary},
		{name: "allowlist", call: ops.lockAllowlist},
		{name: "template", call: ops.lockTemplate},
	}
	for _, lock := range locks {
		if lock.call == nil || lock.call() != nil {
			return errors.New(lock.name + "_ownership_failed")
		}
	}
	rows, err := ops.deleteUnexpected()
	if err != nil || rows != 1 {
		return errors.New("unexpected_send_log_cleanup_failed")
	}
	rows, err = ops.deletePrimary()
	if err != nil || rows != 1 {
		return errors.New("send_log_cleanup_failed")
	}
	rows, err = ops.deleteAllowlist()
	if err != nil || rows != 1 {
		return errors.New("allowlist_cleanup_failed")
	}
	rows, err = ops.deleteTemplate()
	if err != nil || rows != 1 {
		return errors.New("template_cleanup_failed")
	}
	return nil
}

// executeEmailUnknownRestartVerifiedCleanupRows 在删除前锁定成功周期的全部三类对象。
// 任一归属或影响行数不精确时立即返回，由外层事务统一回滚。
func executeEmailUnknownRestartVerifiedCleanupRows(ops emailUnknownRestartVerifiedCleanupOps) error {
	locks := []struct {
		name string
		call func() error
	}{
		{name: "scope", call: ops.lockScope},
		{name: "send_log", call: ops.lockPrimary},
		{name: "allowlist", call: ops.lockAllowlist},
		{name: "template", call: ops.lockTemplate},
	}
	for _, lock := range locks {
		if lock.call == nil || lock.call() != nil {
			return errors.New("verified_" + lock.name + "_ownership_failed")
		}
	}
	deletes := []struct {
		name string
		call func() (int64, error)
	}{
		{name: "send_log", call: ops.deletePrimary},
		{name: "allowlist", call: ops.deleteAllowlist},
		{name: "template", call: ops.deleteTemplate},
	}
	for _, deletion := range deletes {
		if deletion.call == nil {
			return errors.New("verified_" + deletion.name + "_cleanup_failed")
		}
		rows, err := deletion.call()
		if err != nil || rows != 1 {
			return errors.New("verified_" + deletion.name + "_cleanup_failed")
		}
	}
	return nil
}

// executeEmailUnknownRestartCleanup 只接受 Redis key 已不存在的历史现场。
// 历史正式只读门禁已经证明 EXISTS=0，因此这里不执行 DEL，避免数据库失败时形成跨系统部分清理。
func executeEmailUnknownRestartCleanup(ctx context.Context, state emailUnknownRestartState, runtime emailUnknownRestartCleanupRuntime) error {
	if err := validateEmailUnknownRestartCleanupState(state); err != nil {
		return err
	}
	if runtime.redisExists == nil || runtime.cleanupDB == nil {
		return errors.New("cleanup_runtime_invalid")
	}
	exists, err := runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("redis_cleanup_preflight_failed")
	}
	if err := runtime.cleanupDB(ctx); err != nil {
		return err
	}
	exists, err = runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("redis_cleanup_postflight_failed")
	}
	return nil
}

// executeEmailUnknownRestartVerifiedCleanup 复用相同的 Redis 前后验，但只接受成功周期状态。
// Redis 键必须在数据库事务前后均不存在，本函数不会执行 DEL。
func executeEmailUnknownRestartVerifiedCleanup(ctx context.Context, state emailUnknownRestartState, runtime emailUnknownRestartCleanupRuntime) error {
	if err := validateEmailUnknownRestartVerifiedCleanupState(state); err != nil {
		return err
	}
	if runtime.redisExists == nil || runtime.cleanupDB == nil {
		return errors.New("verified_cleanup_runtime_invalid")
	}
	exists, err := runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("verified_redis_cleanup_preflight_failed")
	}
	if err := runtime.cleanupDB(ctx); err != nil {
		return err
	}
	exists, err = runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("verified_redis_cleanup_postflight_failed")
	}
	return nil
}

// executeEmailUnknownRestartPhase1Cleanup 对完整 phase1 单日志现场执行相同的 Redis 前后验和事务清理。
// 它与 phase2_verified 清理分开校验，防止通过改写阶段来伪造尚未发生的 Redis 重启。
func executeEmailUnknownRestartPhase1Cleanup(ctx context.Context, state emailUnknownRestartState, runtime emailUnknownRestartCleanupRuntime) error {
	if err := validateEmailUnknownRestartPhase1CleanupState(state); err != nil {
		return err
	}
	if runtime.redisExists == nil || runtime.cleanupDB == nil {
		return errors.New("phase1_cleanup_runtime_invalid")
	}
	exists, err := runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("phase1_redis_cleanup_preflight_failed")
	}
	if err := runtime.cleanupDB(ctx); err != nil {
		return err
	}
	exists, err = runtime.redisExists(ctx)
	if err != nil || exists != 0 {
		return errors.New("phase1_redis_cleanup_postflight_failed")
	}
	return nil
}

// completeEmailUnknownRestartCleanup 把状态文件删除固定在全部清理后验成功之后。
// cleanup 失败、数据库已提交但 Redis 后验不可用等任何异常都必须保留 state 供人工对账。
func completeEmailUnknownRestartCleanup(cleanup func() error, removeState func() error) error {
	if cleanup == nil || removeState == nil {
		return errors.New("cleanup_completion_invalid")
	}
	if err := cleanup(); err != nil {
		return err
	}
	if err := removeState(); err != nil {
		return errors.New("state_remove_failed")
	}
	return nil
}

func validEmailUnknownRestartCleanupState() emailUnknownRestartState {
	return emailUnknownRestartState{
		Version:             1,
		Phase:               "phase1_created",
		Nonce:               strings.Repeat("a", 32),
		RedisRunID:          strings.Repeat("b", 40),
		OperatorID:          7,
		TemplateID:          11,
		AllowlistID:         13,
		SendLogID:           17,
		UnexpectedSendLogID: 19,
	}
}

func validEmailUnknownRestartVerifiedCleanupState() emailUnknownRestartState {
	return emailUnknownRestartState{
		Version:     1,
		Phase:       "phase2_verified",
		Nonce:       strings.Repeat("a", 32),
		RedisRunID:  strings.Repeat("b", 40),
		OperatorID:  7,
		TemplateID:  11,
		AllowlistID: 13,
		SendLogID:   17,
	}
}

func TestEmailUnknownRestartVerifiedCleanupStateIsStrict(t *testing.T) {
	state := validEmailUnknownRestartVerifiedCleanupState()
	if err := validateEmailUnknownRestartVerifiedCleanupState(state); err != nil {
		t.Fatalf("成功周期状态应通过清理门禁: %v", err)
	}
	state.UnexpectedSendLogID = 19
	if err := validateEmailUnknownRestartVerifiedCleanupState(state); err == nil || err.Error() != "verified_cleanup_unexpected_log_present" {
		t.Fatalf("成功周期不得接受意外日志: %v", err)
	}
	if err := validateEmailUnknownRestartCleanupState(validEmailUnknownRestartVerifiedCleanupState()); err == nil {
		t.Fatal("历史双日志 cleanup 不得接受成功周期状态")
	}
}

func TestEmailUnknownRestartPhase1CleanupStateIsStrict(t *testing.T) {
	state := validEmailUnknownRestartVerifiedCleanupState()
	state.Phase = "phase1_created"
	if err := validateEmailUnknownRestartPhase1CleanupState(state); err != nil {
		t.Fatalf("完整 phase1 单日志状态应通过恢复门禁: %v", err)
	}
	state.UnexpectedSendLogID = 19
	if err := validateEmailUnknownRestartPhase1CleanupState(state); err == nil || err.Error() != "phase1_cleanup_unexpected_log_present" {
		t.Fatalf("phase1 单日志恢复不得接受意外日志: %v", err)
	}
	state = validEmailUnknownRestartVerifiedCleanupState()
	if err := validateEmailUnknownRestartPhase1CleanupState(state); err == nil {
		t.Fatal("phase1 恢复不得接受 phase2_verified 状态")
	}
}

func TestEmailUnknownRestartVerifiedCleanupRowsRequireExactOwnershipAndDeletes(t *testing.T) {
	lock := func() error { return nil }
	remove := func() (int64, error) { return 1, nil }
	ops := emailUnknownRestartVerifiedCleanupOps{
		lockScope: lock, lockPrimary: lock, lockAllowlist: lock, lockTemplate: lock,
		deletePrimary: remove, deleteAllowlist: remove, deleteTemplate: remove,
	}
	if err := executeEmailUnknownRestartVerifiedCleanupRows(ops); err != nil {
		t.Fatalf("完整成功周期清理编排应通过: %v", err)
	}
	ops.deleteAllowlist = func() (int64, error) { return 0, nil }
	if err := executeEmailUnknownRestartVerifiedCleanupRows(ops); err == nil || err.Error() != "verified_allowlist_cleanup_failed" {
		t.Fatalf("缺少精确删除行必须失败: %v", err)
	}
}

func TestEmailUnknownRestartVerifiedCleanupRedisGateRetainsState(t *testing.T) {
	state := validEmailUnknownRestartVerifiedCleanupState()
	databaseCalls := 0
	err := executeEmailUnknownRestartVerifiedCleanup(context.Background(), state, emailUnknownRestartCleanupRuntime{
		redisExists: func(context.Context) (int64, error) { return 1, nil },
		cleanupDB:   func(context.Context) error { databaseCalls++; return nil },
	})
	if err == nil || err.Error() != "verified_redis_cleanup_preflight_failed" || databaseCalls != 0 {
		t.Fatalf("Redis 键存在时不得写数据库: err=%v database_calls=%d", err, databaseCalls)
	}
}

func TestEmailUnknownRestartCleanupInvalidStateBlocksConnections(t *testing.T) {
	state := validEmailUnknownRestartCleanupState()
	state.Phase = "phase2_verified"
	connections := 0
	_, err := prepareEmailUnknownRestartStateBeforeConnect("cleanup", "state", state.OperatorID, emailUnknownRestartPreconnectOps{
		lstat: func(string) (os.FileInfo, error) { return emailUnknownRestartFakeFileInfo{mode: 0o600}, nil },
		readState: func(string) (emailUnknownRestartState, error) {
			return state, nil
		},
	})
	// 该计数分支与正式测试入口一致：只有前置门禁成功，后续才允许建立 MySQL/Redis 连接。
	if err == nil {
		connections += 2
	}
	if err == nil || connections != 0 {
		t.Fatalf("非法 cleanup state 不得建立任何连接: err=%v connections=%d", err, connections)
	}
}

func TestEmailUnknownRestartCleanupRejectsInvalidStateBeforeExternalAccess(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*emailUnknownRestartState)
	}{
		{name: "版本错误", mutate: func(state *emailUnknownRestartState) { state.Version = 2 }},
		{name: "阶段错误", mutate: func(state *emailUnknownRestartState) { state.Phase = "phase2_verified" }},
		{name: "随机标识错误", mutate: func(state *emailUnknownRestartState) { state.Nonce = "invalid" }},
		{name: "Redis身份错误", mutate: func(state *emailUnknownRestartState) { state.RedisRunID = "invalid" }},
		{name: "操作员缺失", mutate: func(state *emailUnknownRestartState) { state.OperatorID = 0 }},
		{name: "模板缺失", mutate: func(state *emailUnknownRestartState) { state.TemplateID = 0 }},
		{name: "白名单缺失", mutate: func(state *emailUnknownRestartState) { state.AllowlistID = 0 }},
		{name: "原日志缺失", mutate: func(state *emailUnknownRestartState) { state.SendLogID = 0 }},
		{name: "意外日志缺失", mutate: func(state *emailUnknownRestartState) { state.UnexpectedSendLogID = 0 }},
		{name: "日志主键重复", mutate: func(state *emailUnknownRestartState) { state.UnexpectedSendLogID = state.SendLogID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := validEmailUnknownRestartCleanupState()
			test.mutate(&state)
			redisCalls := 0
			databaseCalls := 0
			stateRemoved := false
			err := completeEmailUnknownRestartCleanup(func() error {
				return executeEmailUnknownRestartCleanup(context.Background(), state, emailUnknownRestartCleanupRuntime{
					redisExists: func(context.Context) (int64, error) { redisCalls++; return 0, nil },
					cleanupDB:   func(context.Context) error { databaseCalls++; return nil },
				})
			}, func() error { stateRemoved = true; return nil })
			if err == nil || redisCalls != 0 || databaseCalls != 0 || stateRemoved {
				t.Fatalf("非法 state 必须在任何外部访问前失败并保留 state: err=%v redis_calls=%d database_calls=%d state_removed=%t", err, redisCalls, databaseCalls, stateRemoved)
			}
		})
	}
}

func TestEmailUnknownRestartCleanupRejectsExistingRedisKeyWithoutDatabaseWrite(t *testing.T) {
	databaseCalls := 0
	stateRemoved := false
	err := completeEmailUnknownRestartCleanup(func() error {
		return executeEmailUnknownRestartCleanup(context.Background(), validEmailUnknownRestartCleanupState(), emailUnknownRestartCleanupRuntime{
			redisExists: func(context.Context) (int64, error) { return 1, nil },
			cleanupDB:   func(context.Context) error { databaseCalls++; return nil },
		})
	}, func() error { stateRemoved = true; return nil })
	if err == nil || err.Error() != "redis_cleanup_preflight_failed" || databaseCalls != 0 || stateRemoved {
		t.Fatalf("Redis key 仍存在时不得执行数据库清理或删除 state: err=%v database_calls=%d state_removed=%t", err, databaseCalls, stateRemoved)
	}
}

func successfulEmailUnknownRestartCleanupLocks() emailUnknownRestartCleanupOps {
	lock := func() error { return nil }
	remove := func() (int64, error) { return 1, nil }
	return emailUnknownRestartCleanupOps{
		lockScope:        lock,
		lockUnexpected:   lock,
		lockPrimary:      lock,
		lockAllowlist:    lock,
		lockTemplate:     lock,
		deleteUnexpected: remove,
		deletePrimary:    remove,
		deleteAllowlist:  remove,
		deleteTemplate:   remove,
	}
}

func TestEmailUnknownRestartCleanupRejectsOwnershipDriftBeforeDelete(t *testing.T) {
	ops := successfulEmailUnknownRestartCleanupLocks()
	deleteCalls := 0
	ops.lockAllowlist = func() error { return errors.New("ownership_drift") }
	ops.deleteUnexpected = func() (int64, error) { deleteCalls++; return 1, nil }
	err := executeEmailUnknownRestartCleanupRows(ops)
	if err == nil || err.Error() != "allowlist_ownership_failed" || deleteCalls != 0 {
		t.Fatalf("归属漂移必须在任何删除前失败: err=%v delete_calls=%d", err, deleteCalls)
	}
}

func TestEmailUnknownRestartCleanupRejectsEveryMissingDeleteRow(t *testing.T) {
	tests := []struct {
		name string
		want string
		set  func(*emailUnknownRestartCleanupOps)
	}{
		{name: "意外日志", want: "unexpected_send_log_cleanup_failed", set: func(ops *emailUnknownRestartCleanupOps) {
			ops.deleteUnexpected = func() (int64, error) { return 0, nil }
		}},
		{name: "原日志", want: "send_log_cleanup_failed", set: func(ops *emailUnknownRestartCleanupOps) { ops.deletePrimary = func() (int64, error) { return 0, nil } }},
		{name: "白名单", want: "allowlist_cleanup_failed", set: func(ops *emailUnknownRestartCleanupOps) {
			ops.deleteAllowlist = func() (int64, error) { return 0, nil }
		}},
		{name: "模板", want: "template_cleanup_failed", set: func(ops *emailUnknownRestartCleanupOps) { ops.deleteTemplate = func() (int64, error) { return 0, nil } }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ops := successfulEmailUnknownRestartCleanupLocks()
			test.set(&ops)
			err := executeEmailUnknownRestartCleanupRows(ops)
			if err == nil || err.Error() != test.want {
				t.Fatalf("任一授权行缺失都不得报告成功: err=%v want=%s", err, test.want)
			}
		})
	}
}

func TestEmailUnknownRestartCleanupLaterFailureRollsBackLogicalTransaction(t *testing.T) {
	committed := map[string]bool{"unexpected": true, "primary": true, "allowlist": true, "template": true}
	working := map[string]bool{}
	for key, value := range committed {
		working[key] = value
	}
	remove := func(key string, fail bool) func() (int64, error) {
		return func() (int64, error) {
			working[key] = false
			if fail {
				return 0, errors.New("injected_delete_failure")
			}
			return 1, nil
		}
	}
	ops := successfulEmailUnknownRestartCleanupLocks()
	ops.deleteUnexpected = remove("unexpected", false)
	ops.deletePrimary = remove("primary", false)
	ops.deleteAllowlist = remove("allowlist", true)
	ops.deleteTemplate = remove("template", false)
	err := executeEmailUnknownRestartCleanupRows(ops)
	// 真实调用方只在编排成功时提交 GORM 事务；注入后续失败时工作副本不会覆盖已提交状态。
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

func TestEmailUnknownRestartCleanupPostflightFailureRetainsState(t *testing.T) {
	redisCalls := 0
	databaseCalls := 0
	stateRemoved := false
	err := completeEmailUnknownRestartCleanup(func() error {
		return executeEmailUnknownRestartCleanup(context.Background(), validEmailUnknownRestartCleanupState(), emailUnknownRestartCleanupRuntime{
			redisExists: func(context.Context) (int64, error) {
				redisCalls++
				if redisCalls == 2 {
					return 0, errors.New("injected_redis_failure")
				}
				return 0, nil
			},
			cleanupDB: func(context.Context) error { databaseCalls++; return nil },
		})
	}, func() error {
		stateRemoved = true
		return nil
	})
	if err == nil || err.Error() != "redis_cleanup_postflight_failed" || databaseCalls != 1 || stateRemoved {
		t.Fatalf("提交后 Redis 异常必须保留 state: err=%v database_calls=%d state_removed=%t", err, databaseCalls, stateRemoved)
	}
}

func TestEmailUnknownRestartCleanupFailureRetainsState(t *testing.T) {
	stateRemoved := false
	err := completeEmailUnknownRestartCleanup(func() error {
		return errors.New("injected_transaction_failure")
	}, func() error {
		stateRemoved = true
		return nil
	})
	if err == nil || stateRemoved {
		t.Fatalf("清理失败不得删除 state: err=%v state_removed=%t", err, stateRemoved)
	}
}

func TestEmailUnknownRestartCleanupSuccessRemovesStateOnce(t *testing.T) {
	removeCalls := 0
	err := completeEmailUnknownRestartCleanup(func() error { return nil }, func() error {
		removeCalls++
		return nil
	})
	if err != nil || removeCalls != 1 {
		t.Fatalf("全部后验成功后必须且只能删除一次 state: err=%v remove_calls=%d", err, removeCalls)
	}
}

func TestEmailUnknownRestartCleanupPredicatesCoverFrozenOwnership(t *testing.T) {
	required := map[string][]string{
		"日志": {
			"id = ?", "template_id = ?", "provider_template_id = ?", "provider = ?", "verification_code_id IS NULL",
			"scene = ?", "purpose = ?", "recipient_hmac = ?", "idempotency_scope = ?", "idempotency_key_hash = ?",
			"request_fingerprint = ?", "status = ?", "failure_reason = ?",
		},
		"白名单": {
			"id = ?", "email_hmac = ?", "email_masked = ?", "status = ?", "version = ?",
			"created_by = ?", "updated_by = ?", "revoked_at IS NULL",
		},
		"模板": {
			"id = ?", "provider = ?", "provider_template_id = ?", "name = ?", "subject = ?",
			"sender_nickname IS NULL", "template_text = ?", "JSON_LENGTH(variables_json) = 2",
			"JSON_CONTAINS(variables_json, JSON_QUOTE('Code'))",
			"JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))", "content_sha256 = ?",
			"provider_status = ?", "review_comment IS NULL", "variables_complete = ?", "local_enabled = ?",
			"missing = ?", "missing_since IS NULL", "provider_created_at IS NULL", "version = ?",
		},
	}
	predicates := map[string]string{
		"日志":  emailUnknownRestartLogPredicate,
		"白名单": emailUnknownRestartAllowlistPredicate,
		"模板":  emailUnknownRestartTemplatePredicate,
	}
	for name, tokens := range required {
		for _, token := range tokens {
			if !strings.Contains(predicates[name], token) {
				t.Fatalf("%s归属谓词缺少冻结字段: %s", name, token)
			}
		}
	}
}

// emailUnknownRestartTemplateVariablesOwned 按与 MySQL 归属谓词相同的集合语义校验变量数组。
// 长度限制与两个包含条件共同拒绝缺失、重复和额外变量，同时不依赖数组顺序或 JSON 空白。
func emailUnknownRestartTemplateVariablesOwned(raw string) bool {
	var variables []string
	if err := json.Unmarshal([]byte(raw), &variables); err != nil || len(variables) != 2 {
		return false
	}
	foundCode := false
	foundExpireMinutes := false
	for _, variable := range variables {
		switch variable {
		case "Code":
			foundCode = true
		case "ExpireMinutes":
			foundExpireMinutes = true
		}
	}
	return foundCode && foundExpireMinutes
}

func TestEmailUnknownRestartTemplateVariablesUseStrictJSONSemantics(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "标准顺序", raw: `["Code","ExpireMinutes"]`, want: true},
		{name: "逆序", raw: `["ExpireMinutes","Code"]`, want: true},
		{name: "空白差异", raw: `[ "Code",  "ExpireMinutes" ]`, want: true},
		{name: "缺少变量", raw: `["Code"]`, want: false},
		{name: "存在额外变量", raw: `["Code","ExpireMinutes","Other"]`, want: false},
		{name: "重复变量", raw: `["Code","Code"]`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := emailUnknownRestartTemplateVariablesOwned(test.raw); got != test.want {
				t.Fatalf("模板变量 JSON 归属判断错误: raw=%s got=%t want=%t", test.raw, got, test.want)
			}
		})
	}
	if strings.Contains(emailUnknownRestartTemplatePredicate, "variables_json = ?") {
		t.Fatal("模板归属谓词不得按 JSON 字面值比较")
	}
}

func emailUnknownRestartLogQuery(tx *gorm.DB, state emailUnknownRestartState, id uint64, providerTemplateID, recipientHMAC, scope, keyHash, fingerprint string) *gorm.DB {
	return tx.Where(
		emailUnknownRestartLogPredicate,
		id, state.TemplateID, providerTemplateID, emailProvider, "register", "test", recipientHMAC, scope, keyHash, fingerprint, "failed", "provider_outcome_unknown",
	)
}

func emailUnknownRestartAllowlistQuery(tx *gorm.DB, state emailUnknownRestartState, recipientHMAC, recipientMasked string) *gorm.DB {
	return tx.Where(
		emailUnknownRestartAllowlistPredicate,
		state.AllowlistID, recipientHMAC, recipientMasked, "active", 1, state.OperatorID, state.OperatorID,
	)
}

func emailUnknownRestartTemplateQuery(tx *gorm.DB, state emailUnknownRestartState, providerTemplateID, templateText string) *gorm.DB {
	return tx.Where(
		emailUnknownRestartTemplatePredicate,
		state.TemplateID, emailProvider, providerTemplateID, "Phase4 Redis 重启隔离模板", "Phase4 隔离验证", templateText, hash(templateText), "approved", true, true, false, 1,
	)
}

// cleanupEmailUnknownRestartDatabase 在一个事务内锁定、核验并删除本轮四类隔离数据。
func cleanupEmailUnknownRestartDatabase(ctx context.Context, db *gorm.DB, state emailUnknownRestartState) error {
	email, _, _, providerTemplateID := emailUnknownRestartValues(state)
	recipientHMAC, scope, fingerprint, oldKeyHash, newKeyHash := emailUnknownRestartIdentity(state)
	templateText := "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
	recipientMasked := maskEmailAddress(email)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deleteRows := func(result *gorm.DB) (int64, error) { return result.RowsAffected, result.Error }
		lockOne := func(query *gorm.DB, destination any) error {
			return query.Clauses(clause.Locking{Strength: "UPDATE"}).Take(destination).Error
		}
		return executeEmailUnknownRestartCleanupRows(emailUnknownRestartCleanupOps{
			lockScope: func() error {
				var rows []struct{ ID uint64 }
				if err := tx.Model(&model.EmailSendLog{}).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("idempotency_scope = ?", scope).Order("id ASC").Find(&rows).Error; err != nil {
					return err
				}
				if len(rows) != 2 || !((rows[0].ID == state.SendLogID && rows[1].ID == state.UnexpectedSendLogID) || (rows[0].ID == state.UnexpectedSendLogID && rows[1].ID == state.SendLogID)) {
					return errors.New("scope_rows_mismatch")
				}
				return nil
			},
			lockUnexpected: func() error {
				return lockOne(emailUnknownRestartLogQuery(tx, state, state.UnexpectedSendLogID, providerTemplateID, recipientHMAC, scope, newKeyHash, fingerprint), &model.EmailSendLog{})
			},
			lockPrimary: func() error {
				return lockOne(emailUnknownRestartLogQuery(tx, state, state.SendLogID, providerTemplateID, recipientHMAC, scope, oldKeyHash, fingerprint), &model.EmailSendLog{})
			},
			lockAllowlist: func() error {
				return lockOne(emailUnknownRestartAllowlistQuery(tx, state, recipientHMAC, recipientMasked), &model.EmailTestRecipientAllowlist{})
			},
			lockTemplate: func() error {
				return lockOne(emailUnknownRestartTemplateQuery(tx, state, providerTemplateID, templateText), &model.EmailProviderTemplate{})
			},
			deleteUnexpected: func() (int64, error) {
				return deleteRows(emailUnknownRestartLogQuery(tx, state, state.UnexpectedSendLogID, providerTemplateID, recipientHMAC, scope, newKeyHash, fingerprint).Delete(&model.EmailSendLog{}))
			},
			deletePrimary: func() (int64, error) {
				return deleteRows(emailUnknownRestartLogQuery(tx, state, state.SendLogID, providerTemplateID, recipientHMAC, scope, oldKeyHash, fingerprint).Delete(&model.EmailSendLog{}))
			},
			deleteAllowlist: func() (int64, error) {
				return deleteRows(emailUnknownRestartAllowlistQuery(tx, state, recipientHMAC, recipientMasked).Delete(&model.EmailTestRecipientAllowlist{}))
			},
			deleteTemplate: func() (int64, error) {
				return deleteRows(emailUnknownRestartTemplateQuery(tx, state, providerTemplateID, templateText).Delete(&model.EmailProviderTemplate{}))
			},
		})
	})
}

// cleanupEmailUnknownRestartVerifiedDatabase 在一个事务中清理成功周期的一条日志、白名单和模板。
func cleanupEmailUnknownRestartVerifiedDatabase(ctx context.Context, db *gorm.DB, state emailUnknownRestartState) error {
	email, _, _, providerTemplateID := emailUnknownRestartValues(state)
	recipientHMAC, scope, fingerprint, oldKeyHash, _ := emailUnknownRestartIdentity(state)
	templateText := "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
	recipientMasked := maskEmailAddress(email)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deleteRows := func(result *gorm.DB) (int64, error) { return result.RowsAffected, result.Error }
		lockOne := func(query *gorm.DB, destination any) error {
			return query.Clauses(clause.Locking{Strength: "UPDATE"}).Take(destination).Error
		}
		return executeEmailUnknownRestartVerifiedCleanupRows(emailUnknownRestartVerifiedCleanupOps{
			lockScope: func() error {
				var rows []struct{ ID uint64 }
				if err := tx.Model(&model.EmailSendLog{}).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("idempotency_scope = ?", scope).Order("id ASC").Find(&rows).Error; err != nil {
					return err
				}
				if len(rows) != 1 || rows[0].ID != state.SendLogID {
					return errors.New("verified_scope_rows_mismatch")
				}
				return nil
			},
			lockPrimary: func() error {
				return lockOne(emailUnknownRestartLogQuery(tx, state, state.SendLogID, providerTemplateID, recipientHMAC, scope, oldKeyHash, fingerprint), &model.EmailSendLog{})
			},
			lockAllowlist: func() error {
				return lockOne(emailUnknownRestartAllowlistQuery(tx, state, recipientHMAC, recipientMasked), &model.EmailTestRecipientAllowlist{})
			},
			lockTemplate: func() error {
				return lockOne(emailUnknownRestartTemplateQuery(tx, state, providerTemplateID, templateText), &model.EmailProviderTemplate{})
			},
			deletePrimary: func() (int64, error) {
				return deleteRows(emailUnknownRestartLogQuery(tx, state, state.SendLogID, providerTemplateID, recipientHMAC, scope, oldKeyHash, fingerprint).Delete(&model.EmailSendLog{}))
			},
			deleteAllowlist: func() (int64, error) {
				return deleteRows(emailUnknownRestartAllowlistQuery(tx, state, recipientHMAC, recipientMasked).Delete(&model.EmailTestRecipientAllowlist{}))
			},
			deleteTemplate: func() (int64, error) {
				return deleteRows(emailUnknownRestartTemplateQuery(tx, state, providerTemplateID, templateText).Delete(&model.EmailProviderTemplate{}))
			},
		})
	})
}

// cleanupEmailUnknownRestartFixture 只在 Redis 精确 key 已不存在时提交数据库清理，并在提交后再次只读复核。
func cleanupEmailUnknownRestartFixture(ctx context.Context, db *gorm.DB, client *redis.Client, state emailUnknownRestartState) error {
	_, scope, _, _, _ := emailUnknownRestartIdentity(state)
	lockKey := "lock:email:dispatch:" + crypto.HMAC256(scope, emailUnknownRestartIdempotencySecret)
	return executeEmailUnknownRestartCleanup(ctx, state, emailUnknownRestartCleanupRuntime{
		redisExists: func(checkCtx context.Context) (int64, error) {
			return client.Exists(checkCtx, lockKey).Result()
		},
		cleanupDB: func(cleanupCtx context.Context) error {
			return cleanupEmailUnknownRestartDatabase(cleanupCtx, db, state)
		},
	})
}

func cleanupEmailUnknownRestartVerifiedFixture(ctx context.Context, db *gorm.DB, client *redis.Client, state emailUnknownRestartState) error {
	_, scope, _, _, _ := emailUnknownRestartIdentity(state)
	lockKey := "lock:email:dispatch:" + crypto.HMAC256(scope, emailUnknownRestartIdempotencySecret)
	return executeEmailUnknownRestartVerifiedCleanup(ctx, state, emailUnknownRestartCleanupRuntime{
		redisExists: func(checkCtx context.Context) (int64, error) {
			return client.Exists(checkCtx, lockKey).Result()
		},
		cleanupDB: func(cleanupCtx context.Context) error {
			return cleanupEmailUnknownRestartVerifiedDatabase(cleanupCtx, db, state)
		},
	})
}

// cleanupEmailUnknownRestartPhase1Fixture 复用单日志事务谓词，只改变合法状态阶段的严格校验。
func cleanupEmailUnknownRestartPhase1Fixture(ctx context.Context, db *gorm.DB, client *redis.Client, state emailUnknownRestartState) error {
	_, scope, _, _, _ := emailUnknownRestartIdentity(state)
	lockKey := "lock:email:dispatch:" + crypto.HMAC256(scope, emailUnknownRestartIdempotencySecret)
	return executeEmailUnknownRestartPhase1Cleanup(ctx, state, emailUnknownRestartCleanupRuntime{
		redisExists: func(checkCtx context.Context) (int64, error) {
			return client.Exists(checkCtx, lockKey).Result()
		},
		cleanupDB: func(cleanupCtx context.Context) error {
			return cleanupEmailUnknownRestartVerifiedDatabase(cleanupCtx, db, state)
		},
	})
}

type emailUnknownRestartPreconnectOps struct {
	lstat     func(string) (os.FileInfo, error)
	readState func(string) (emailUnknownRestartState, error)
}

// prepareEmailUnknownRestartStateBeforeConnect 在建立 MySQL 或 Redis 连接前完成阶段与状态门禁。
// cleanup 的完整状态验证必须位于连接之前，避免恶意或损坏 state 触发任何外部访问。
func prepareEmailUnknownRestartStateBeforeConnect(phase, statePath string, operatorID uint64, ops emailUnknownRestartPreconnectOps) (emailUnknownRestartState, error) {
	if ops.lstat == nil || ops.readState == nil {
		return emailUnknownRestartState{}, errors.New("preconnect_ops_invalid")
	}
	switch phase {
	case "phase1":
		if _, err := ops.lstat(statePath); !errors.Is(err, os.ErrNotExist) {
			return emailUnknownRestartState{}, errors.New("state_already_exists")
		}
		return emailUnknownRestartState{}, nil
	case "phase2", "cleanup", "cleanup_verified", "cleanup_phase1":
		state, err := ops.readState(statePath)
		if err != nil || state.OperatorID != operatorID {
			return emailUnknownRestartState{}, errors.New("state_load_failed")
		}
		if phase == "cleanup" {
			if err := validateEmailUnknownRestartCleanupState(state); err != nil {
				return emailUnknownRestartState{}, err
			}
		}
		if phase == "cleanup_verified" {
			if err := validateEmailUnknownRestartVerifiedCleanupState(state); err != nil {
				return emailUnknownRestartState{}, err
			}
		}
		if phase == "cleanup_phase1" {
			if err := validateEmailUnknownRestartPhase1CleanupState(state); err != nil {
				return emailUnknownRestartState{}, err
			}
		}
		if phase == "phase2" && (state.Phase != "phase1_created" || state.TemplateID == 0 || state.AllowlistID == 0 || state.SendLogID == 0) {
			return emailUnknownRestartState{}, errors.New("phase_order_invalid")
		}
		return state, nil
	default:
		return emailUnknownRestartState{}, errors.New("phase_invalid")
	}
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
	if (phase == "cleanup" || phase == "cleanup_verified" || phase == "cleanup_phase1") && (os.Getenv("RUN_EMAIL_UNKNOWN_RESTART_CLEANUP") != "1" || os.Getenv("EMAIL_UNKNOWN_RESTART_CLEANUP_ACK") != emailUnknownRestartCleanupAck) {
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
	preparedState, err := prepareEmailUnknownRestartStateBeforeConnect(phase, statePath, operatorID, emailUnknownRestartPreconnectOps{
		lstat:     os.Lstat,
		readState: readEmailUnknownRestartState,
	})
	if err != nil {
		t.Fatal("[FAIL] mode=email_unknown_restart classification=preconnect_state_gate_failed recovery_state=retained")
	}
	db := openEmailUnknownRestartDB(t)
	client := openEmailUnknownRestartRedis(t)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if phase == "phase1" {
		// 将控制器生成的操作标识绑定到状态文件，避免二进制另行生成随机值后脱离固定 stage 归属。
		nonce := strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_NONCE"))
		if !emailUnknownRestartNoncePattern.MatchString(nonce) {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=nonce_invalid recovery_state=not_created")
		}
		var userCount int64
		if err := db.WithContext(ctx).Table("users").Where("id = ?", operatorID).Count(&userCount).Error; err != nil || userCount != 1 {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=phase1 classification=operator_not_found recovery_state=retained")
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

	state := preparedState
	if phase == "cleanup" {
		if err := completeEmailUnknownRestartCleanup(
			func() error { return cleanupEmailUnknownRestartFixture(ctx, db, client, state) },
			func() error { return os.Remove(statePath) },
		); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup classification=cleanup_or_state_remove_failed recovery_state=retained")
		}
		t.Log("[PASS] mode=email_unknown_restart phase=cleanup classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true")
		return
	}
	if phase == "cleanup_verified" {
		if err := completeEmailUnknownRestartCleanup(
			func() error { return cleanupEmailUnknownRestartVerifiedFixture(ctx, db, client, state) },
			func() error { return os.Remove(statePath) },
		); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup_verified classification=cleanup_or_state_remove_failed recovery_state=retained")
		}
		t.Log("[PASS] mode=email_unknown_restart phase=cleanup_verified classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true")
		return
	}
	if phase == "cleanup_phase1" {
		if err := completeEmailUnknownRestartCleanup(
			func() error { return cleanupEmailUnknownRestartPhase1Fixture(ctx, db, client, state) },
			func() error { return os.Remove(statePath) },
		); err != nil {
			t.Fatal("[FAIL] mode=email_unknown_restart phase=cleanup_phase1 classification=cleanup_or_state_remove_failed recovery_state=retained")
		}
		t.Log("[PASS] mode=email_unknown_restart phase=cleanup_phase1 classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true")
		return
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

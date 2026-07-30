package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

var (
	ErrEmailInvalid        = errors.New("请求参数错误")
	ErrEmailVariables      = errors.New("邮件模板变量不完整")
	ErrEmailConflict       = errors.New("数据冲突")
	ErrEmailBindingMissing = errors.New("邮件场景未绑定模板")
	ErrEmailSceneDisabled  = errors.New("邮件场景已停用")
	ErrEmailTemplateOff    = errors.New("邮件模板已停用")
	ErrEmailTemplateDraft  = errors.New("邮件模板尚未提交审核")
	ErrEmailTemplateReview = errors.New("邮件模板正在审核")
	ErrEmailTemplateReject = errors.New("邮件模板审核未通过")
	ErrEmailTemplateGone   = errors.New("邮件模板在供应商侧不存在")
	ErrEmailRecipientDeny  = errors.New("无权向该邮箱发送验证码")
	ErrEmailRateLimited    = errors.New("请求频率超限")
	ErrEmailSyncRunning    = errors.New("模板同步正在进行")
	ErrEmailNotReady       = errors.New("邮件发送服务未就绪")
	ErrEmailUpstream       = errors.New("邮件上游调用失败")
	ErrEmailOutcomeUnknown = errors.New("供应商响应未知，请在验证码过期后重试")
	ErrEmailOutcomePending = errors.New("邮件发送结果确认中，请在验证码过期后重试")
	ErrEmailSending        = errors.New("邮件正在发送，请稍后重试")
	ErrEmailNotAllowlisted = errors.New("测试邮箱未加入白名单")
)

const emailProvider = "aliyun_directmail"
const emailSyncScope = "admin-email-template-sync:aliyun_directmail"
const emailDispatchConfigScope = "email-template-dispatch-config"
const otpExpireMinutes = 10

var emailScenes = map[string]string{"register": "注册", "login": "邮箱验证码登录", "reset_password": "找回密码", "bind_email": "换绑邮箱", "admin_verify": "管理员邮箱双重认证"}

// emailPersistenceNowUTC 与 MySQL DATETIME 的秒级精度保持一致，避免纳秒参与不可落库的边界判断。
func emailPersistenceNowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

func emailSyncRunStale(startedAt, now time.Time) bool {
	cutoff := now.UTC().Truncate(time.Second).Add(-5 * time.Minute)
	return startedAt.UTC().Truncate(time.Second).Before(cutoff)
}

// EmailService 实现模板管理、白名单、发送日志和稳定 OTP 发送边界。
type EmailService struct {
	repo                                                  emailRepository
	verificationRepo                                      verificationRepository
	adapter                                               DirectMailAdapter
	audit                                                 emailAuditor
	redis                                                 *redis.Client
	lockOverride                                          func(context.Context, string, time.Duration) (*emailLease, bool, error)
	rateLimitOverride                                     func(context.Context, string, int, time.Duration) (bool, error)
	recipientAuthorizer                                   EmailOTPRecipientAuthorizer
	metrics                                               *EmailAdapterMetrics
	addressSecret, idempotencySecret, appEnv, adapterMode string
}

var emailRateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count
`)

func (s *EmailService) checkAccountRateLimit(ctx context.Context, scene string, userID uint64, targetHMAC string) error {
	dimension := "target:" + targetHMAC
	if scene == "bind_email" || scene == "admin_verify" {
		dimension = fmt.Sprintf("user:%d", userID)
	}
	// Redis key 只含独立 HMAC 摘要，不出现完整邮箱或用户 ID 明文。
	// 账号维度不再拼接场景，避免攻击者通过轮换发码场景绕过每分钟十次的总上限。
	key := "ratelimit:email:account:" + crypto.HMAC256(dimension, s.idempotencySecret)
	var allowed bool
	var err error
	if s.rateLimitOverride != nil {
		allowed, err = s.rateLimitOverride(ctx, key, 10, time.Minute)
	} else {
		count, runErr := emailRateLimitScript.Run(ctx, s.redis, []string{key}, time.Minute.Milliseconds()).Int64()
		err, allowed = runErr, runErr == nil && count <= 10
	}
	if err != nil {
		return ErrEmailNotReady
	}
	if !allowed {
		return ErrEmailRateLimited
	}
	return nil
}

// EmailOTPRecipientAuthorizer 在供应商调用前以用户真相源复核专属场景收件人。
// 实现由 AuthService 注入，避免 EmailService 直接访问用户 repository。
type EmailOTPRecipientAuthorizer interface {
	AuthorizeEmailOTPRecipient(ctx context.Context, scene, endpoint string, userID uint64, recipient, flowRecipient string) error
}

func (s *EmailService) SetRecipientAuthorizer(authorizer EmailOTPRecipientAuthorizer) {
	s.recipientAuthorizer = authorizer
}

// emailLease 把释放与所有权确认绑定在同一对象上，外呼前和提交前都必须显式 fencing。
type emailLease struct {
	release func()
	owned   func(context.Context) bool
}

func (l *emailLease) Release() {
	if l != nil && l.release != nil {
		l.release()
	}
}

func (l *emailLease) Owned(ctx context.Context) bool {
	return l != nil && l.owned != nil && l.owned(ctx)
}

type emailAuditor interface {
	Record(context.Context, *uint64, string, string, *string, *string, string, any) error
}

type emailRepository interface {
	ListTemplates(context.Context, string, string, *bool, *bool, *bool, string, int, int) ([]model.EmailProviderTemplate, int64, error)
	BoundScenes(context.Context, []uint64) (map[uint64][]string, error)
	GetTemplate(context.Context, uint64) (*model.EmailProviderTemplate, error)
	UpdateTemplateStatus(context.Context, uint64, uint64, bool) error
	ListBindings(context.Context) ([]model.EmailSceneBinding, error)
	GetBinding(context.Context, string) (*model.EmailSceneBinding, *model.EmailProviderTemplate, error)
	UpdateBinding(context.Context, string, uint64, uint64, uint64, bool) error
	FindSyncByIdempotency(context.Context, string, string) (*model.EmailTemplateSyncRun, error)
	CreateSyncRun(context.Context, *model.EmailTemplateSyncRun) error
	GetSyncRun(context.Context, uint64) (*model.EmailTemplateSyncRun, error)
	HasRunningSync(context.Context) (bool, error)
	FindStaleSyncRuns(context.Context, time.Time) ([]model.EmailTemplateSyncRun, error)
	FailStaleSync(context.Context, uint64, time.Time) error
	ApplyTemplateSync(context.Context, uint64, []model.EmailProviderTemplate, time.Time) (uint, uint, uint, uint, error)
	FailSync(context.Context, uint64, string, string) error
	ListSyncRuns(context.Context, string, int, int) ([]model.EmailTemplateSyncRun, int64, error)
	FindAllowlistByHMAC(context.Context, string) (*model.EmailTestRecipientAllowlist, error)
	CreateAllowlist(context.Context, *model.EmailTestRecipientAllowlist) error
	RestoreAllowlist(context.Context, uint64, uint64) error
	RevokeAllowlist(context.Context, uint64, uint64, uint64) error
	GetAllowlist(context.Context, uint64) (*model.EmailTestRecipientAllowlist, error)
	ListAllowlist(context.Context, int, int) ([]model.EmailTestRecipientAllowlist, int64, error)
	DeleteRevokedAllowlistBefore(context.Context, time.Time) (int64, error)
	CreateSendLog(context.Context, *model.EmailSendLog) error
	FinalizeSendLog(context.Context, uint64, string, *string, *string) error
	FindSendLogByIdempotency(context.Context, string, string) (*model.EmailSendLog, error)
	FindBlockingSendByScope(context.Context, string, time.Time) (*model.EmailSendLog, error)
	FailStalePendingSend(context.Context, string, string, time.Time) (bool, error)
	FindSendLogByBusinessNo(context.Context, string) (*model.EmailSendLog, error)
	ListSendLogs(context.Context, string, string, string, uint64, *time.Time, *time.Time, int, int) ([]model.EmailSendLog, int64, error)
	Summary(context.Context, time.Time, time.Time) (repository.EmailSummary, error)
}

func NewEmailService(repo emailRepository, verificationRepo verificationRepository, adapter DirectMailAdapter, audit emailAuditor, redisClient *redis.Client, addressSecret, idempotencySecret, appEnv, adapterMode string) *EmailService {
	return &EmailService{repo: repo, verificationRepo: verificationRepo, adapter: adapter, audit: audit, redis: redisClient, metrics: newEmailAdapterMetrics(), addressSecret: addressSecret, idempotencySecret: idempotencySecret, appEnv: appEnv, adapterMode: adapterMode}
}

func (s *EmailService) Ready() bool {
	if !strongDistinctEmailSecrets(s.addressSecret, s.idempotencySecret) || s.adapter == nil || (s.redis == nil && s.lockOverride == nil) {
		return false
	}
	if s.adapterMode != "production" && s.adapterMode != "mock" {
		return false
	}
	env := strings.ToLower(strings.TrimSpace(s.appEnv))
	safeNonProduction := env == "local" || env == "development" || env == "dev" || env == "test" || env == "testing"
	if s.adapterMode == "mock" && !safeNonProduction {
		return false
	}
	return s.adapter.Ready()
}

// strongDistinctEmailSecrets 要求两个用途隔离的 HMAC 密钥均至少 32 字节且不相同。
func strongDistinctEmailSecrets(addressSecret, idempotencySecret string) bool {
	a, b := []byte(addressSecret), []byte(idempotencySecret)
	if len(a) < 32 || len(b) < 32 || len(a) != len(b) {
		return len(a) >= 32 && len(b) >= 32 && addressSecret != idempotencySecret
	}
	return subtle.ConstantTimeCompare(a, b) != 1
}

func (s *EmailService) acquireDistributedLock(ctx context.Context, scope string, ttl time.Duration) (*emailLease, bool, error) {
	if s.lockOverride != nil {
		return s.lockOverride(ctx, scope, ttl)
	}
	token, nonceErr := randomNonce()
	if nonceErr != nil {
		return nil, false, nonceErr
	}
	key := "lock:email:dispatch:" + crypto.HMAC256(scope, s.idempotencySecret)
	if scope == emailSyncScope {
		key = "lock:email:template-sync:aliyun_directmail"
	}
	ok, err := s.redis.SetNX(ctx, key, token, ttl).Result()
	if err != nil || !ok {
		return nil, ok, err
	}
	lockCtx, cancelRenew := context.WithCancel(context.Background())
	renewDone := make(chan struct{})
	var lost atomic.Bool
	renewScript := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`)
	ownedScript := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return 1 else return 0 end`)
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-lockCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				result, renewErr := renewScript.Run(renewCtx, s.redis, []string{key}, token, ttl.Milliseconds()).Int64()
				cancel()
				if renewErr != nil || result != 1 {
					lost.Store(true)
					log.Printf("[email] Redis 锁续租或所有权校验失败: scope_kind=%s", emailLockScopeKind(scope))
					return
				}
			}
		}
	}()
	var once sync.Once
	lease := &emailLease{}
	lease.owned = func(checkCtx context.Context) bool {
		if lost.Load() {
			return false
		}
		result, checkErr := ownedScript.Run(checkCtx, s.redis, []string{key}, token).Int64()
		if checkErr != nil || result != 1 {
			lost.Store(true)
			return false
		}
		return true
	}
	lease.release = func() {
		once.Do(func() {
			cancelRenew()
			<-renewDone
			unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			deleted, unlockErr := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`).Run(unlockCtx, s.redis, []string{key}, token).Int64()
			if unlockErr != nil || deleted != 1 {
				log.Printf("[email] Redis 锁释放所有权异常: scope_kind=%s", emailLockScopeKind(scope))
			}
		})
	}
	return lease, true, nil
}

func emailLockScopeKind(scope string) string {
	if scope == emailSyncScope {
		return "template_sync"
	}
	if scope == emailDispatchConfigScope {
		return "dispatch_config"
	}
	return "send"
}
func validEmailScene(scene string) bool { _, ok := emailScenes[scene]; return ok }
func normalizeEmailAddress(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// validateEmailAddress 只接受单个裸邮箱地址，拒绝显示名、多地址和换行注入。
func validateEmailAddress(v string) (string, error) {
	if strings.ContainsAny(v, "\r\n,") {
		return "", ErrEmailInvalid
	}
	normalized := normalizeEmailAddress(v)
	parsed, err := mail.ParseAddress(normalized)
	if err != nil || parsed.Name != "" || parsed.Address != normalized || strings.Count(normalized, "@") != 1 {
		return "", ErrEmailInvalid
	}
	return normalized, nil
}
func maskEmailAddress(v string) string {
	v = normalizeEmailAddress(v)
	at := strings.LastIndex(v, "@")
	if at < 1 {
		return "***"
	}
	local := v[:at]
	keep := 2
	if len(local) < keep {
		keep = len(local)
	}
	return local[:keep] + "***" + v[at:]
}
func (s *EmailService) emailHMAC(v string) string {
	return crypto.HMAC256(normalizeEmailAddress(v), s.addressSecret)
}

// TargetKey 实现稳定邮箱目标键接口，校验后只返回 HMAC，不暴露供应商能力。
func (s *EmailService) TargetKey(email string) (string, error) {
	normalized, err := validateEmailAddress(email)
	if err != nil || !strongDistinctEmailSecrets(s.addressSecret, s.idempotencySecret) {
		return "", ErrEmailInvalid
	}
	return s.emailHMAC(normalized), nil
}
func hash(v string) string { return crypto.SHA256Hex(v) }

// validTemplateVariableBoundary 拒绝从畸形或嵌套占位符内部截取看似合法的子串。
// 前方残留美元符号或左花括号表示嵌套/重复起始符，后方残留右花括号表示 token 尚未完整结束。
func validTemplateVariableBoundary(text string, start, end int) bool {
	if start > 0 && (text[start-1] == '$' || text[start-1] == '{') {
		return false
	}
	return end >= len(text) || text[end] != '}'
}

func variablesFromText(text string) []string {
	set := map[string]struct{}{}
	// 先保留历史兼容语法；兼容语法允许读取其他合法变量名，供模板详情如实展示。
	compatibilityPattern := regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]*)\}|\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)
	for _, location := range compatibilityPattern.FindAllStringSubmatchIndex(text, -1) {
		if !validTemplateVariableBoundary(text, location[0], location[1]) {
			continue
		}
		nameStart, nameEnd := location[2], location[3]
		if nameStart < 0 {
			nameStart, nameEnd = location[4], location[5]
		}
		set[text[nameStart:nameEnd]] = struct{}{}
	}
	// DirectMail 官方正文使用单花括号变量。这里只识别冻结的两个业务变量，避免把 CSS、JSON 或普通花括号内容当成模板变量。
	officialPattern := regexp.MustCompile(`\{(Code|ExpireMinutes)\}`)
	for _, location := range officialPattern.FindAllStringSubmatchIndex(text, -1) {
		if !validTemplateVariableBoundary(text, location[0], location[1]) {
			continue
		}
		set[text[location[2]:location[3]]] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func variablesComplete(v []string) bool {
	hasCode, hasExpire := false, false
	for _, x := range v {
		hasCode = hasCode || x == "Code"
		hasExpire = hasExpire || x == "ExpireMinutes"
	}
	return hasCode && hasExpire
}

var emailTemplateTokenPattern = regexp.MustCompile(`\$\{[A-Za-z][A-Za-z0-9_]*\}|\{\{\s*[A-Za-z][A-Za-z0-9_]*\s*\}\}|\{(?:[A-Z][A-Za-z0-9_]*|(?i:code|expireminutes))\}`)
var malformedTemplateVariablePattern = regexp.MustCompile(`(?:\$\{\s*|\{\{\s*)[A-Za-z][A-Za-z0-9_]*|\{\s*(?:[A-Za-z0-9_]*[A-Z][A-Za-z0-9_]*|(?i:code|expireminutes))\b`)

// renderEmailTemplate 只渲染冻结的 Code 与 ExpireMinutes，普通 HTML、CSS 和 JSON 花括号保持原样。
// 官方单花括号及历史美元/双花括号语法均受支持；其他变量和畸形边界一律失败关闭。
func renderEmailTemplate(subject, templateText, code string, expireMinutes int) (string, error) {
	if strings.TrimSpace(templateText) == "" || !utf8.ValidString(templateText) || code == "" || expireMinutes <= 0 {
		return "", ErrEmailVariables
	}
	locations := emailTemplateTokenPattern.FindAllStringIndex(templateText, -1)
	var rendered strings.Builder
	rendered.Grow(len(templateText))
	last, codeCount, expireCount := 0, 0, 0
	for _, location := range locations {
		start, end := location[0], location[1]
		if !validTemplateVariableBoundary(templateText, start, end) {
			continue
		}
		token := templateText[start:end]
		name := token
		switch {
		case strings.HasPrefix(token, "${"):
			name = token[2 : len(token)-1]
		case strings.HasPrefix(token, "{{"):
			name = strings.TrimSpace(token[2 : len(token)-2])
		default:
			name = token[1 : len(token)-1]
		}
		if name != "Code" && name != "ExpireMinutes" {
			return "", ErrEmailVariables
		}
		rendered.WriteString(templateText[last:start])
		if name == "Code" {
			rendered.WriteString(code)
			codeCount++
		} else {
			rendered.WriteString(strconv.Itoa(expireMinutes))
			expireCount++
		}
		last = end
	}
	rendered.WriteString(templateText[last:])
	body := rendered.String()
	if codeCount == 0 || expireCount == 0 || emailTemplateTokenPattern.MatchString(body) || malformedTemplateVariablePattern.MatchString(body) {
		return "", ErrEmailVariables
	}
	if err := validateDirectMailContent(subject, body); err != nil {
		return "", err
	}
	return body, nil
}

// validateSendTemplate 返回冻结契约中的精确模板状态错误，禁止用通用冲突掩盖运营可修复原因。
func validateSendTemplate(tpl *model.EmailProviderTemplate) error {
	if tpl == nil {
		return repository.ErrEmailNotFound
	}
	if !tpl.LocalEnabled {
		return ErrEmailTemplateOff
	}
	switch tpl.ProviderStatus {
	case "draft":
		return ErrEmailTemplateDraft
	case "pending":
		return ErrEmailTemplateReview
	case "rejected":
		return ErrEmailTemplateReject
	case "approved":
	default:
		return ErrEmailConflict
	}
	if tpl.Missing {
		return ErrEmailTemplateGone
	}
	if !tpl.VariablesComplete {
		return ErrEmailVariables
	}
	return nil
}
func randomBusinessNo() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("email_%d_%x", time.Now().UnixMilli(), b), nil
}

func templateItem(m model.EmailProviderTemplate, bound []string) dto.EmailTemplateItem {
	return dto.EmailTemplateItem{ID: m.ID, Provider: m.Provider, ProviderTemplateID: m.ProviderTemplateID, Name: m.Name, Subject: m.Subject, ProviderStatus: m.ProviderStatus, ReviewComment: m.ReviewComment, VariablesComplete: m.VariablesComplete, LocalEnabled: m.LocalEnabled, BoundScenes: bound, Missing: m.Missing, MissingSince: m.MissingSince, LastSyncedAt: m.LastSyncedAt, Version: m.Version}
}
func decodeVariables(raw string) []string {
	var v []string
	_ = json.Unmarshal([]byte(raw), &v)
	if v == nil {
		return []string{}
	}
	return v
}

func (s *EmailService) ListTemplates(ctx context.Context, keyword, status string, enabled, complete, missing *bool, scene string, offset, limit int) ([]dto.EmailTemplateItem, int64, error) {
	items, total, err := s.repo.ListTemplates(ctx, keyword, status, enabled, complete, missing, scene, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]uint64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	bound, err := s.repo.BoundScenes(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.EmailTemplateItem, len(items))
	for i, v := range items {
		scenes := bound[v.ID]
		if scenes == nil {
			scenes = []string{}
		}
		out[i] = templateItem(v, scenes)
	}
	return out, total, nil
}
func (s *EmailService) GetTemplate(ctx context.Context, id uint64) (*dto.EmailTemplateDetail, error) {
	v, err := s.repo.GetTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	bound, _ := s.repo.BoundScenes(ctx, []uint64{id})
	return &dto.EmailTemplateDetail{EmailTemplateItem: templateItem(*v, bound[id]), SenderNickname: v.SenderNickname, TemplateText: v.TemplateText, Variables: decodeVariables(v.VariablesJSON), ContentSHA256: v.ContentSHA256}, nil
}
func (s *EmailService) SetTemplateStatus(ctx context.Context, id, version, operator uint64, enabled bool, ip string) (*dto.EmailTemplateItem, error) {
	lease, locked, lockErr := s.acquireDistributedLock(ctx, emailDispatchConfigScope, 30*time.Second)
	if lockErr != nil || !locked {
		return nil, ErrEmailConflict
	}
	defer lease.Release()
	v, err := s.repo.GetTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if enabled {
		if !v.VariablesComplete {
			return nil, ErrEmailVariables
		}
		if v.ProviderStatus != "approved" || v.Missing {
			return nil, ErrEmailConflict
		}
	}
	if err := s.auditAttempt(ctx, operator, "email.template.status.update", "email_template", id, ip, map[string]any{"template_id": id, "local_enabled": enabled, "version": version}); err != nil {
		return nil, err
	}
	if !lease.Owned(ctx) {
		return nil, ErrEmailConflict
	}
	if err := s.repo.UpdateTemplateStatus(ctx, id, version, enabled); err != nil {
		return nil, ErrEmailConflict
	}
	s.auditResult(ctx, operator, "email.template.status.update", "email_template", id, ip, map[string]any{"template_id": id, "status": "succeeded"})
	fresh, err := s.repo.GetTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	bound, _ := s.repo.BoundScenes(ctx, []uint64{id})
	out := templateItem(*fresh, bound[id])
	return &out, nil
}

func (s *EmailService) ListBindings(ctx context.Context) ([]dto.EmailSceneBindingItem, error) {
	rows, err := s.repo.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dto.EmailSceneBindingItem, 0, len(rows))
	for _, b := range rows {
		item := dto.EmailSceneBindingItem{Scene: b.Scene, DisplayName: emailScenes[b.Scene], TemplateID: b.TemplateID, Enabled: b.Enabled, VariableMapping: map[string]string{"code": "Code", "expire_minutes": "ExpireMinutes"}, Version: b.Version, UpdatedAt: b.UpdatedAt}
		if b.TemplateID != nil {
			if t, e := s.repo.GetTemplate(ctx, *b.TemplateID); e == nil {
				item.ProviderTemplateID = &t.ProviderTemplateID
				item.ProviderStatus = &t.ProviderStatus
				item.LocalEnabled = t.LocalEnabled
				item.VariablesComplete = t.VariablesComplete
				item.Missing = t.Missing
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func (s *EmailService) SetBinding(ctx context.Context, scene string, templateID, version, operator uint64, enabled bool, ip string) (*dto.EmailSceneBindingItem, error) {
	if !validEmailScene(scene) {
		return nil, ErrEmailInvalid
	}
	lease, locked, lockErr := s.acquireDistributedLock(ctx, emailDispatchConfigScope, 30*time.Second)
	if lockErr != nil || !locked {
		return nil, ErrEmailConflict
	}
	defer lease.Release()
	t, err := s.repo.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if !t.VariablesComplete {
		return nil, ErrEmailVariables
	}
	if t.ProviderStatus != "approved" || !t.LocalEnabled || t.Missing {
		return nil, ErrEmailConflict
	}
	if err := s.auditAttempt(ctx, operator, "email.scene.binding.update", "email_scene", 0, ip, map[string]any{"scene": scene, "template_id": templateID, "enabled": enabled, "version": version}); err != nil {
		return nil, err
	}
	if !lease.Owned(ctx) {
		return nil, ErrEmailConflict
	}
	if err := s.repo.UpdateBinding(ctx, scene, templateID, version, operator, enabled); err != nil {
		return nil, ErrEmailConflict
	}
	s.auditResult(ctx, operator, "email.scene.binding.update", "email_scene", 0, ip, map[string]any{"scene": scene, "status": "succeeded"})
	items, err := s.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Scene == scene {
			return &items[i], nil
		}
	}
	return nil, repository.ErrEmailNotFound
}

func syncItem(v model.EmailTemplateSyncRun, idempotent bool) dto.EmailSyncRunItem {
	return dto.EmailSyncRunItem{RunID: v.ID, Provider: v.Provider, Status: v.Status, CreatedCount: v.CreatedCount, UpdatedCount: v.UpdatedCount, MissingCount: v.MissingCount, UnchangedCount: v.UnchangedCount, ErrorCode: v.ErrorCode, ErrorMessage: v.ErrorMessage, CreatedBy: v.CreatedBy, StartedAt: v.StartedAt, CompletedAt: v.CompletedAt, Idempotent: idempotent}
}
func (s *EmailService) Sync(ctx context.Context, key string, operator uint64, ip string) (*dto.EmailSyncRunItem, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrEmailInvalid
	}
	if !s.Ready() {
		return nil, ErrEmailNotReady
	}
	keyHash := hash(key)
	fingerprint := hash("POST\n/api/admin/email/templates/sync\n" + emailProvider)
	if old, err := s.repo.FindSyncByIdempotency(ctx, emailSyncScope, keyHash); err == nil {
		if old.RequestFingerprint != fingerprint {
			return nil, ErrEmailConflict
		}
		// 陈旧 running 不能在 lease 外直接收敛；原执行者可能仍持有并续租同一同步锁。
		if old.Status != "running" || !emailSyncRunStale(old.StartedAt, emailPersistenceNowUTC()) {
			result := syncItem(*old, true)
			return &result, nil
		}
	}
	lease, locked, lockErr := s.acquireDistributedLock(ctx, emailSyncScope, 30*time.Second)
	if lockErr != nil || !locked {
		if running, runningErr := s.repo.HasRunningSync(ctx); runningErr == nil && running {
			return nil, ErrEmailSyncRunning
		} else if runningErr != nil {
			return nil, runningErr
		}
		return nil, ErrEmailNotReady
	}
	defer lease.Release()
	configUnlock, configLocked, configLockErr := s.acquireDistributedLock(ctx, emailDispatchConfigScope, 2*time.Minute)
	if configLockErr != nil || !configLocked {
		return nil, ErrEmailConflict
	}
	defer configUnlock.Release()
	// 只有成功取得同一全局同步 lease 后，才允许把陈旧 running 收敛为 failed。
	if old, err := s.repo.FindSyncByIdempotency(ctx, emailSyncScope, keyHash); err == nil {
		return s.replaySync(ctx, old, fingerprint, operator, ip)
	}
	if err := s.convergeStaleSyncs(ctx, operator, ip); err != nil {
		return nil, err
	}
	if running, err := s.repo.HasRunningSync(ctx); err != nil {
		return nil, err
	} else if running {
		return nil, ErrEmailSyncRunning
	}
	now := emailPersistenceNowUTC()
	run := &model.EmailTemplateSyncRun{Provider: emailProvider, IdempotencyScope: emailSyncScope, IdempotencyKeyHash: keyHash, RequestFingerprint: fingerprint, Status: "running", CreatedBy: operator, StartedAt: now, CreatedAt: now}
	if err := s.auditAttempt(ctx, operator, "email.template.sync", "email_sync_run", 0, ip, map[string]any{"provider": emailProvider}); err != nil {
		return nil, err
	}
	if !lease.Owned(ctx) || !configUnlock.Owned(ctx) {
		return nil, ErrEmailNotReady
	}
	if err := s.repo.CreateSyncRun(ctx, run); err != nil {
		return nil, ErrEmailConflict
	}
	incoming := []model.EmailProviderTemplate{}
	for page := 1; ; page++ {
		if !lease.Owned(ctx) || !configUnlock.Owned(ctx) {
			_ = s.failSync(ctx, run.ID, operator, ip, "redis_lock_lost_before_call")
			return nil, ErrEmailNotReady
		}
		list, more, err := s.adapter.QueryTemplates(ctx, page, 50)
		s.recordAdapterCall("query_templates", "template_sync", err)
		if err != nil {
			if finalizeErr := s.failSync(ctx, run.ID, operator, ip, "provider_query_failed"); finalizeErr != nil {
				return nil, finalizeErr
			}
			return nil, ErrEmailUpstream
		}
		for _, brief := range list {
			if !lease.Owned(ctx) || !configUnlock.Owned(ctx) {
				_ = s.failSync(ctx, run.ID, operator, ip, "redis_lock_lost_before_call")
				return nil, ErrEmailNotReady
			}
			detail, err := s.adapter.DescribeTemplate(ctx, brief.TemplateID)
			s.recordAdapterCall("describe_template", "template_sync", err)
			if err != nil {
				if finalizeErr := s.failSync(ctx, run.ID, operator, ip, "provider_describe_failed"); finalizeErr != nil {
					return nil, finalizeErr
				}
				return nil, ErrEmailUpstream
			}
			vars := variablesFromText(detail.TemplateText)
			raw, _ := json.Marshal(vars)
			review := safeProviderText(detail.ReviewComment)
			var reviewPtr *string
			if review != "" {
				reviewPtr = &review
			}
			var senderPtr *string
			if sender := strings.TrimSpace(detail.SenderNickname); sender != "" {
				senderPtr = &sender
			}
			incoming = append(incoming, model.EmailProviderTemplate{Provider: emailProvider, ProviderTemplateID: detail.TemplateID, Name: detail.Name, Subject: detail.Subject, SenderNickname: senderPtr, TemplateText: detail.TemplateText, VariablesJSON: string(raw), ContentSHA256: hash(detail.Subject + "\n" + detail.TemplateText), ProviderStatus: detail.Status, ReviewComment: reviewPtr, VariablesComplete: variablesComplete(vars), ProviderCreatedAt: detail.ProviderCreatedAt})
		}
		if !more {
			break
		}
	}
	// 镜像事务提交前再次确认两把 lease 所有权，并由 run=running 条件更新完成第二层 fencing。
	if !lease.Owned(ctx) || !configUnlock.Owned(ctx) {
		_ = s.failSync(ctx, run.ID, operator, ip, "redis_lock_lost_before_commit")
		return nil, ErrEmailNotReady
	}
	created, updated, missing, unchanged, err := s.repo.ApplyTemplateSync(ctx, run.ID, incoming, emailPersistenceNowUTC())
	if err != nil {
		// run 已被其他执行者收敛属于 fencing 冲突，不能误记为数据库提交失败。
		if errors.Is(err, repository.ErrEmailConflict) {
			return nil, ErrEmailConflict
		}
		if finalizeErr := s.failSync(ctx, run.ID, operator, ip, "database_commit_failed"); finalizeErr != nil {
			return nil, finalizeErr
		}
		return nil, err
	}
	completedRun, err := s.repo.GetSyncRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	s.auditResult(ctx, operator, "email.template.sync", "email_sync_run", run.ID, ip, map[string]any{"run_id": run.ID, "status": "succeeded", "created_count": created, "updated_count": updated, "missing_count": missing, "unchanged_count": unchanged})
	out := syncItem(*completedRun, false)
	return &out, nil
}
func (s *EmailService) failSync(ctx context.Context, id, operator uint64, ip, reason string) error {
	if err := s.repo.FailSync(ctx, id, reason, "邮件上游调用失败"); err != nil {
		return err
	}
	s.auditResult(ctx, operator, "email.template.sync", "email_sync_run", id, ip, map[string]any{"run_id": id, "status": "failed", "failure_reason": reason})
	return nil
}

func (s *EmailService) replaySync(ctx context.Context, old *model.EmailTemplateSyncRun, fingerprint string, operator uint64, ip string) (*dto.EmailSyncRunItem, error) {
	if old.RequestFingerprint != fingerprint {
		return nil, ErrEmailConflict
	}
	if old.Status == "running" && emailSyncRunStale(old.StartedAt, emailPersistenceNowUTC()) {
		if err := s.auditAttempt(ctx, operator, "email.template.sync.stale", "email_sync_run", old.ID, ip, map[string]any{"run_id": old.ID}); err != nil {
			return nil, err
		}
		if err := s.repo.FailStaleSync(ctx, old.ID, emailPersistenceNowUTC()); err != nil {
			return nil, err
		}
		s.auditResult(ctx, operator, "email.template.sync.stale", "email_sync_run", old.ID, ip, map[string]any{"run_id": old.ID, "status": "failed"})
		refreshed, err := s.repo.GetSyncRun(ctx, old.ID)
		if err != nil {
			return nil, err
		}
		old = refreshed
	}
	r := syncItem(*old, true)
	return &r, nil
}

func (s *EmailService) convergeStaleSyncs(ctx context.Context, operator uint64, ip string) error {
	runs, err := s.repo.FindStaleSyncRuns(ctx, emailPersistenceNowUTC().Add(-5*time.Minute))
	if err != nil {
		return err
	}
	for i := range runs {
		if _, err := s.replaySync(ctx, &runs[i], runs[i].RequestFingerprint, operator, ip); err != nil {
			return err
		}
	}
	return nil
}
func (s *EmailService) ListSyncRuns(ctx context.Context, status string, offset, limit int) ([]dto.EmailSyncRunItem, int64, error) {
	rows, total, err := s.repo.ListSyncRuns(ctx, status, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.EmailSyncRunItem, len(rows))
	for i, v := range rows {
		out[i] = syncItem(v, false)
	}
	return out, total, nil
}

func allowItem(v model.EmailTestRecipientAllowlist) dto.EmailAllowlistItem {
	return dto.EmailAllowlistItem{ID: v.ID, EmailMasked: v.EmailMasked, Status: v.Status, Version: v.Version, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, RevokedAt: v.RevokedAt}
}
func (s *EmailService) AddAllowlist(ctx context.Context, email string, operator uint64, ip string) (*dto.EmailAllowlistMutationResp, error) {
	var err error
	email, err = validateEmailAddress(email)
	if err != nil || !strongDistinctEmailSecrets(s.addressSecret, s.idempotencySecret) {
		return nil, ErrEmailInvalid
	}
	h := s.emailHMAC(email)
	if old, err := s.repo.FindAllowlistByHMAC(ctx, h); err == nil {
		if old.Status == "active" {
			return nil, ErrEmailConflict
		}
		if err := s.auditAttempt(ctx, operator, "email.test_allowlist.add", "email_allowlist", old.ID, ip, map[string]any{"allowlist_id": old.ID, "email_masked": old.EmailMasked}); err != nil {
			return nil, err
		}
		if err := s.repo.RestoreAllowlist(ctx, old.ID, operator); err != nil {
			return nil, err
		}
		fresh, err := s.repo.GetAllowlist(ctx, old.ID)
		if err != nil {
			return nil, err
		}
		s.auditResult(ctx, operator, "email.test_allowlist.add", "email_allowlist", old.ID, ip, map[string]any{"allowlist_id": old.ID, "email_masked": fresh.EmailMasked, "status": "active"})
		out := dto.EmailAllowlistMutationResp{ID: fresh.ID, EmailMasked: fresh.EmailMasked, Status: fresh.Status, Version: fresh.Version, CreatedAt: fresh.CreatedAt}
		return &out, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	v := &model.EmailTestRecipientAllowlist{EmailHMAC: h, EmailMasked: maskEmailAddress(email), Status: "active", Version: 1, CreatedBy: operator, UpdatedBy: operator}
	if err := s.auditAttempt(ctx, operator, "email.test_allowlist.add", "email_allowlist", 0, ip, map[string]any{"email_masked": v.EmailMasked}); err != nil {
		return nil, err
	}
	if err := s.repo.CreateAllowlist(ctx, v); err != nil {
		return nil, ErrEmailConflict
	}
	s.auditResult(ctx, operator, "email.test_allowlist.add", "email_allowlist", v.ID, ip, map[string]any{"allowlist_id": v.ID, "email_masked": v.EmailMasked, "status": "active"})
	out := dto.EmailAllowlistMutationResp{ID: v.ID, EmailMasked: v.EmailMasked, Status: v.Status, Version: v.Version, CreatedAt: v.CreatedAt}
	return &out, nil
}
func (s *EmailService) RevokeAllowlist(ctx context.Context, id, version, operator uint64, ip string) (*dto.EmailAllowlistMutationResp, error) {
	if err := s.auditAttempt(ctx, operator, "email.test_allowlist.revoke", "email_allowlist", id, ip, map[string]any{"allowlist_id": id, "version": version}); err != nil {
		return nil, err
	}
	if err := s.repo.RevokeAllowlist(ctx, id, version, operator); err != nil {
		return nil, ErrEmailConflict
	}
	v, err := s.repo.GetAllowlist(ctx, id)
	if err != nil {
		return nil, err
	}
	s.auditResult(ctx, operator, "email.test_allowlist.revoke", "email_allowlist", id, ip, map[string]any{"allowlist_id": id, "email_masked": v.EmailMasked, "status": "revoked", "version": v.Version})
	out := dto.EmailAllowlistMutationResp{ID: v.ID, EmailMasked: v.EmailMasked, Status: v.Status, Version: v.Version, RevokedAt: v.RevokedAt}
	return &out, nil
}
func (s *EmailService) ListAllowlist(ctx context.Context, offset, limit int) ([]dto.EmailAllowlistItem, int64, error) {
	rows, total, err := s.repo.ListAllowlist(ctx, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.EmailAllowlistItem, len(rows))
	for i, v := range rows {
		out[i] = allowItem(v)
	}
	return out, total, nil
}

// CleanupRevokedAllowlist 删除撤销满 30 天的记录；调用方可按日调度，操作幂等。
func (s *EmailService) CleanupRevokedAllowlist(ctx context.Context) (int64, error) {
	return s.repo.DeleteRevokedAllowlistBefore(ctx, revokedAllowlistCutoff(emailPersistenceNowUTC()))
}

func revokedAllowlistCutoff(now time.Time) time.Time { return now.AddDate(0, 0, -30) }

// StartAllowlistCleanup 在进程存活期间每天执行一次清理，并在启动时立即执行一次。
func (s *EmailService) StartAllowlistCleanup(ctx context.Context) {
	if _, err := s.CleanupRevokedAllowlist(ctx); err != nil {
		log.Printf("[email] 清理已撤销测试邮箱白名单失败: err=%v", err)
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.CleanupRevokedAllowlist(ctx); err != nil {
				log.Printf("[email] 清理已撤销测试邮箱白名单失败: err=%v", err)
			}
		}
	}
}

func sendResult(v model.EmailSendLog, idempotent bool) dto.EmailSendResult {
	return dto.EmailSendResult{SendLogID: v.ID, BusinessRequestNo: v.BusinessRequestNo, TemplateID: v.TemplateID, Scene: v.Scene, RecipientMasked: v.RecipientMasked, Status: v.Status, FailureReason: v.FailureReason, Idempotent: idempotent, SubmittedAt: v.SubmittedAt}
}

func isProviderOutcomeUnknown(err error) bool {
	if errors.Is(err, ErrDirectMailOutcomeUnknown) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func sendCooldownUntil(v *model.EmailSendLog) time.Time {
	if v == nil {
		return time.Time{}
	}
	if v.Purpose == "otp" && v.ExpiresAt != nil {
		return *v.ExpiresAt
	}
	return v.SubmittedAt.Add(10 * time.Minute)
}

// findBlockingSend 将失去执行者且超过五分钟的 pending 收敛为未知结果墓碑；冷却期结束后允许新的业务动作继续。
func (s *EmailService) findBlockingSend(ctx context.Context, scope string, now time.Time) (*model.EmailSendLog, error) {
	now = now.UTC().Truncate(time.Second)
	blocker, err := s.repo.FindBlockingSendByScope(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	if blocker.Status == "pending" && blocker.SubmittedAt.Before(now.Add(-5*time.Minute)) {
		var changed bool
		var failErr error
		if blocker.Purpose == "otp" {
			changed, failErr = s.verificationRepo.FailStaleEmailSend(ctx, scope, blocker.IdempotencyKeyHash, now.Add(-5*time.Minute))
		} else {
			changed, failErr = s.repo.FailStalePendingSend(ctx, scope, blocker.IdempotencyKeyHash, now.Add(-5*time.Minute))
		}
		if failErr != nil {
			return nil, failErr
		}
		if changed {
			reason := "provider_outcome_unknown"
			blocker.Status, blocker.FailureReason = "failed", &reason
		}
	}
	if blocker.Status == "failed" && blocker.FailureReason != nil && *blocker.FailureReason == "provider_outcome_unknown" && !sendCooldownUntil(blocker).After(now) {
		return nil, gorm.ErrRecordNotFound
	}
	return blocker, nil
}

func (s *EmailService) TestSend(ctx context.Context, templateID uint64, scene, email, key string, operator uint64, ip string) (*dto.EmailSendResult, error) {
	if !validEmailScene(scene) || strings.TrimSpace(key) == "" {
		return nil, ErrEmailInvalid
	}
	if !s.Ready() {
		return nil, ErrEmailNotReady
	}
	var err error
	email, err = validateEmailAddress(email)
	if err != nil || s.addressSecret == "" {
		return nil, ErrEmailInvalid
	}
	rh := s.emailHMAC(email)
	allow, err := s.repo.FindAllowlistByHMAC(ctx, rh)
	if err != nil || allow.Status != "active" {
		return nil, ErrEmailNotAllowlisted
	}
	scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", operator, templateID, scene, rh)
	kh := hash(key)
	fp := hash(fmt.Sprintf("POST\n/api/admin/email/templates/%d/test-send\n%s\n%s", templateID, scene, rh))
	if old, err := s.repo.FindSendLogByIdempotency(ctx, scope, kh); err == nil {
		return s.replayTestSend(ctx, old, fp, operator, ip)
	}
	// Idempotency-Key 只决定结果重放，不得进入锁 scope；同一四维业务对象必须竞争同一把锁。
	lease, locked, lockErr := s.acquireDistributedLock(ctx, scope, 15*time.Second)
	if lockErr != nil || !locked {
		if blocker, blockErr := s.findBlockingSend(ctx, scope, emailPersistenceNowUTC()); blockErr == nil {
			if blocker.IdempotencyKeyHash == kh {
				return s.replayTestSend(ctx, blocker, fp, operator, ip)
			}
			if blocker.Status == "pending" {
				return nil, ErrEmailSending
			}
			return nil, ErrEmailOutcomePending
		} else if !errors.Is(blockErr, gorm.ErrRecordNotFound) {
			return nil, blockErr
		}
		return nil, ErrEmailNotReady
	}
	defer lease.Release()
	// 获取互斥后再次查询幂等结果，保证同实例只调用一次供应商。
	if old, err := s.repo.FindSendLogByIdempotency(ctx, scope, kh); err == nil {
		return s.replayTestSend(ctx, old, fp, operator, ip)
	}
	// 即使 Redis 重启或锁 key 丢失，数据库中的未知结果墓碑仍阻断新 key 外呼。
	if blocker, blockErr := s.findBlockingSend(ctx, scope, emailPersistenceNowUTC()); blockErr == nil {
		if blocker.IdempotencyKeyHash == kh {
			return s.replayTestSend(ctx, blocker, fp, operator, ip)
		}
		return nil, ErrEmailOutcomePending
	} else if !errors.Is(blockErr, gorm.ErrRecordNotFound) {
		return nil, blockErr
	}
	// 发送配置写操作与供应商外呼共用全局配置锁。持锁后再读取模板，并持续持有到外呼结束，
	// 保证模板启停或同步不能插入“最终校验通过”与“真正外呼”之间。
	configLease, configLocked, configLockErr := s.acquireDistributedLock(ctx, emailDispatchConfigScope, 30*time.Second)
	if configLockErr != nil || !configLocked {
		return nil, ErrEmailConflict
	}
	defer configLease.Release()
	tpl, err := s.repo.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if err := validateSendTemplate(tpl); err != nil {
		return nil, err
	}
	business, err := randomBusinessNo()
	if err != nil {
		return nil, ErrEmailNotReady
	}
	submitted := emailPersistenceNowUTC()
	code, err := generateCode(6)
	if err != nil {
		return nil, ErrEmailNotReady
	}
	htmlBody, err := renderEmailTemplate(tpl.Subject, tpl.TemplateText, code, otpExpireMinutes)
	if err != nil {
		return nil, err
	}
	// 先持久化 pending 幂等占位，再调用供应商；进程崩溃后的重试只会看到 pending，绝不重复外发。
	entry := &model.EmailSendLog{BusinessRequestNo: business, TemplateID: tpl.ID, ProviderTemplateID: tpl.ProviderTemplateID, Scene: scene, Purpose: "test", RecipientHMAC: rh, RecipientMasked: maskEmailAddress(email), IdempotencyScope: scope, IdempotencyKeyHash: kh, RequestFingerprint: fp, Provider: emailProvider, Status: "pending", SubmittedAt: submitted}
	if err := s.auditAttempt(ctx, operator, "email.template.test_send", "email_send_log", 0, ip, map[string]any{"scene": scene, "recipient_masked": entry.RecipientMasked, "template_id": templateID}); err != nil {
		return nil, err
	}
	if err := s.repo.CreateSendLog(ctx, entry); err != nil {
		return nil, ErrEmailConflict
	}
	// pending 落库可能与异常失锁后的配置写入相邻；外呼前必须再次读取模板版本并校验可发送条件。
	latestTemplate, latestErr := s.repo.GetTemplate(ctx, templateID)
	if latestErr != nil || latestTemplate.Version != tpl.Version || validateSendTemplate(latestTemplate) != nil {
		reason := "dispatch_config_changed_before_call"
		failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFailure()
		if finalizeErr := s.repo.FinalizeSendLog(failureCtx, entry.ID, "failed", nil, &reason); finalizeErr != nil {
			return nil, finalizeErr
		}
		return nil, ErrEmailConflict
	}
	// pending 占位完成后、外呼前再次向 Redis 确认 token 所有权；丢锁时 Adapter 调用次数必须保持不变。
	if !lease.Owned(ctx) || !configLease.Owned(ctx) {
		reason := "send_lock_lost_before_call"
		failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFailure()
		if finalizeErr := s.repo.FinalizeSendLog(failureCtx, entry.ID, "failed", nil, &reason); finalizeErr != nil {
			return nil, finalizeErr
		}
		return nil, ErrEmailNotReady
	}
	accept, sendErr := s.adapter.SingleSendMail(ctx, EmailMessage{Recipient: email, Subject: tpl.Subject, HTMLBody: htmlBody})
	s.recordAdapterCall("send_mail", scene, sendErr)
	// 外呼超时通常会取消请求 context；终态落库必须使用保留链路值但不继承取消信号的短超时上下文。
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinalize()
	if !lease.Owned(finalizeCtx) {
		// 外呼已经开始后不得改报 503；仅告警并按明确响应或 unknown 墓碑唯一收敛。
		log.Printf("[email] 测试发送外呼期间丢失 Redis 锁: send_log_id=%d", entry.ID)
	}
	if sendErr != nil {
		reason := directMailFailureReason(sendErr)
		publicErr := ErrEmailUpstream
		if isProviderOutcomeUnknown(sendErr) {
			reason = "provider_outcome_unknown"
			publicErr = ErrEmailOutcomeUnknown
		}
		if err := s.repo.FinalizeSendLog(finalizeCtx, entry.ID, "failed", nil, &reason); err != nil {
			log.Printf("[email] 发送日志终态 fencing 冲突: send_log_id=%d", entry.ID)
			if current, findErr := s.repo.FindSendLogByIdempotency(finalizeCtx, scope, kh); findErr == nil {
				return s.replayTestSend(finalizeCtx, current, fp, operator, ip)
			}
			return nil, err
		}
		s.auditResult(finalizeCtx, operator, "email.template.test_send", "email_send_log", entry.ID, ip, map[string]any{"send_log_id": entry.ID, "scene": scene, "recipient_masked": entry.RecipientMasked, "status": "failed"})
		return nil, publicErr
	}
	if err := s.repo.FinalizeSendLog(finalizeCtx, entry.ID, "accepted", &accept.RequestID, nil); err != nil {
		log.Printf("[email] 发送日志终态 fencing 冲突: send_log_id=%d", entry.ID)
		if current, findErr := s.repo.FindSendLogByIdempotency(finalizeCtx, scope, kh); findErr == nil {
			return s.replayTestSend(finalizeCtx, current, fp, operator, ip)
		}
		return nil, err
	}
	entry.Status = "accepted"
	entry.ProviderRequestID = &accept.RequestID
	s.auditResult(finalizeCtx, operator, "email.template.test_send", "email_send_log", entry.ID, ip, map[string]any{"send_log_id": entry.ID, "scene": scene, "recipient_masked": entry.RecipientMasked, "status": "accepted"})
	out := sendResult(*entry, false)
	return &out, nil
}

func (s *EmailService) replayTestSend(ctx context.Context, old *model.EmailSendLog, fingerprint string, operator uint64, ip string) (*dto.EmailSendResult, error) {
	if old.RequestFingerprint != fingerprint {
		return nil, ErrEmailConflict
	}
	if old.Status == "pending" {
		if converged, err := s.findBlockingSend(ctx, old.IdempotencyScope, emailPersistenceNowUTC()); err == nil && converged.IdempotencyKeyHash == old.IdempotencyKeyHash {
			old = converged
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	switch old.Status {
	case "failed":
		if old.FailureReason != nil && *old.FailureReason == "provider_outcome_unknown" {
			return nil, ErrEmailOutcomeUnknown
		}
		return nil, ErrEmailUpstream
	case "pending":
		return nil, ErrEmailSending
	case "accepted":
		out := sendResult(*old, true)
		return &out, nil
	default:
		return nil, ErrEmailConflict
	}
}
func (s *EmailService) ListSendLogs(ctx context.Context, scene, purpose, status string, templateID uint64, start, end *time.Time, offset, limit int) ([]dto.EmailSendLogItem, int64, error) {
	rows, total, err := s.repo.ListSendLogs(ctx, scene, purpose, status, templateID, start, end, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.EmailSendLogItem, len(rows))
	for i, v := range rows {
		out[i] = dto.EmailSendLogItem{ID: v.ID, Scene: v.Scene, Purpose: v.Purpose, RecipientMasked: v.RecipientMasked, TemplateID: v.TemplateID, ProviderTemplateID: v.ProviderTemplateID, BusinessRequestNo: v.BusinessRequestNo, ProviderRequestID: v.ProviderRequestID, Status: v.Status, FailureReason: v.FailureReason, SubmittedAt: v.SubmittedAt}
	}
	return out, total, nil
}
func (s *EmailService) Summary(ctx context.Context) (*dto.EmailSummaryResp, error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	raw, err := s.repo.Summary(ctx, start.UTC(), start.AddDate(0, 0, 1).UTC())
	if err != nil {
		return nil, err
	}
	return &dto.EmailSummaryResp{TemplateTotal: raw.TemplateTotal, ApprovedCount: raw.ApprovedCount, LocalEnabledCount: raw.LocalEnabledCount, UnboundSceneCount: raw.UnboundSceneCount, SubmittedTodayCount: raw.SubmittedTodayCount, FailedTodayCount: raw.FailedTodayCount, LastSyncedAt: raw.LastSyncedAt}, nil
}

type emailOTPContextKey string

const emailOTPIdentityKey emailOTPContextKey = "email_otp_identity"

type emailOTPIdentity struct {
	Endpoint      string
	UserID        uint64
	FlowRecipient string
}

// withEmailOTPIdentity 只由 auth 服务建立受约束的专属发码流程上下文。
func withEmailOTPIdentity(ctx context.Context, endpoint string, userID uint64, flowRecipient string) context.Context {
	return context.WithValue(ctx, emailOTPIdentityKey, emailOTPIdentity{Endpoint: endpoint, UserID: userID, FlowRecipient: normalizeEmailAddress(flowRecipient)})
}

func (s *EmailService) authorizeRecipient(ctx context.Context, scene, recipient string, identity emailOTPIdentity) error {
	if scene != "bind_email" && scene != "admin_verify" {
		return nil
	}
	expectedEndpoint := "/api/me/verification-codes/email"
	if scene == "admin_verify" {
		expectedEndpoint = "/api/admin/auth/verification-codes/email"
	}
	if identity.UserID == 0 || identity.Endpoint != expectedEndpoint || identity.FlowRecipient == "" || normalizeEmailAddress(recipient) != identity.FlowRecipient || s.recipientAuthorizer == nil {
		return ErrEmailRecipientDeny
	}
	if err := s.recipientAuthorizer.AuthorizeEmailOTPRecipient(ctx, scene, identity.Endpoint, identity.UserID, recipient, identity.FlowRecipient); err != nil {
		return ErrEmailRecipientDeny
	}
	return nil
}

// SendOTP 是 auth 依赖的稳定发送接口。所有模板前置校验完成后才创建 pending 验证码。
func (s *EmailService) SendOTP(ctx context.Context, businessRequestNo, scene, recipient, code string, expireMinutes int) (EmailAcceptance, uint64, error) {
	if !validEmailScene(scene) || expireMinutes != otpExpireMinutes {
		return EmailAcceptance{}, 0, ErrEmailInvalid
	}
	if !s.Ready() {
		return EmailAcceptance{}, 0, ErrEmailNotReady
	}
	var normalizeErr error
	recipient, normalizeErr = validateEmailAddress(recipient)
	if normalizeErr != nil || s.addressSecret == "" {
		return EmailAcceptance{}, 0, ErrEmailInvalid
	}
	binding, tpl, err := s.repo.GetBinding(ctx, scene)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return EmailAcceptance{}, 0, ErrEmailBindingMissing
		}
		return EmailAcceptance{}, 0, err
	}
	if binding.TemplateID == nil {
		return EmailAcceptance{}, 0, ErrEmailBindingMissing
	}
	if !binding.Enabled {
		return EmailAcceptance{}, 0, ErrEmailSceneDisabled
	}
	if err := validateSendTemplate(tpl); err != nil {
		return EmailAcceptance{}, 0, err
	}
	identity, _ := ctx.Value(emailOTPIdentityKey).(emailOTPIdentity)
	if err := s.authorizeRecipient(ctx, scene, recipient, identity); err != nil {
		return EmailAcceptance{}, 0, err
	}
	targetHMAC := s.emailHMAC(recipient)
	if err := s.checkAccountRateLimit(ctx, scene, identity.UserID, targetHMAC); err != nil {
		return EmailAcceptance{}, 0, err
	}
	scope := fmt.Sprintf("auth:%s:email:%s", scene, targetHMAC)
	if scene == "bind_email" {
		scope = fmt.Sprintf("auth:bind_email:user:%d:email:%s", identity.UserID, targetHMAC)
	} else if scene == "admin_verify" {
		scope = fmt.Sprintf("auth:admin_verify:user:%d:email:%s", identity.UserID, targetHMAC)
	}
	fingerprint := hash(fmt.Sprintf("%s|%s|%s|otp|%d|%d|%d", identity.Endpoint, scene, targetHMAC, expireMinutes, tpl.ID, binding.Version))
	kh := crypto.HMAC256(businessRequestNo+"|"+scope, s.idempotencySecret)
	// 正式 OTP 不依赖客户端幂等头；按入口与目标 scope 在冷却窗口内串行创建或复用业务请求。
	lease, locked, lockErr := s.acquireDistributedLock(ctx, scope, 15*time.Second)
	if lockErr != nil || !locked {
		if blocker, blockErr := s.findBlockingSend(ctx, scope, emailPersistenceNowUTC()); blockErr == nil {
			if blocker.Status == "pending" {
				return EmailAcceptance{}, 0, ErrEmailSending
			}
			if blocker.IdempotencyKeyHash == kh {
				return EmailAcceptance{}, 0, ErrEmailOutcomeUnknown
			}
			return EmailAcceptance{}, 0, ErrEmailOutcomePending
		} else if !errors.Is(blockErr, gorm.ErrRecordNotFound) {
			return EmailAcceptance{}, 0, blockErr
		}
		return EmailAcceptance{}, 0, ErrEmailNotReady
	}
	defer lease.Release()
	// 场景绑定、模板启停和同步均使用同一配置锁。发送在持有业务 scope 锁后取得配置锁，
	// 并从锁内重新读取快照，使配置写入与外呼形成确定的先后关系。
	configLease, configLocked, configLockErr := s.acquireDistributedLock(ctx, emailDispatchConfigScope, 30*time.Second)
	if configLockErr != nil || !configLocked {
		return EmailAcceptance{}, 0, ErrEmailConflict
	}
	defer configLease.Release()
	// 拿到发送 scope lease 后重新读取并核对版本，避免使用加锁前已变化的绑定或模板快照。
	lockedBinding, lockedTemplate, err := s.repo.GetBinding(ctx, scene)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return EmailAcceptance{}, 0, ErrEmailBindingMissing
		}
		return EmailAcceptance{}, 0, err
	}
	if lockedBinding.TemplateID == nil {
		return EmailAcceptance{}, 0, ErrEmailBindingMissing
	}
	if !lockedBinding.Enabled {
		return EmailAcceptance{}, 0, ErrEmailSceneDisabled
	}
	if lockedBinding.Version != binding.Version || lockedTemplate == nil || lockedTemplate.Version != tpl.Version {
		return EmailAcceptance{}, 0, ErrEmailConflict
	}
	if err := validateSendTemplate(lockedTemplate); err != nil {
		return EmailAcceptance{}, 0, err
	}
	binding, tpl = lockedBinding, lockedTemplate
	now := emailPersistenceNowUTC()
	if old, findErr := s.verificationRepo.FindLatestByScope(ctx, scope, now.Add(-10*time.Minute)); findErr == nil && old.ExpiresAt.After(now) {
		if old.RequestFingerprint == nil || *old.RequestFingerprint != fingerprint {
			return EmailAcceptance{}, old.ID, ErrEmailConflict
		}
		switch old.SendStatus {
		case "pending":
			if blocker, blockErr := s.findBlockingSend(ctx, scope, now); blockErr == nil && blocker.Status == "failed" && blocker.FailureReason != nil && *blocker.FailureReason == "provider_outcome_unknown" {
				return EmailAcceptance{}, old.ID, ErrEmailOutcomeUnknown
			} else if blockErr != nil && !errors.Is(blockErr, gorm.ErrRecordNotFound) {
				return EmailAcceptance{}, old.ID, blockErr
			}
			return EmailAcceptance{}, old.ID, ErrEmailSending
		case "failed":
			if old.BusinessRequestNo != nil {
				if logEntry, logErr := s.repo.FindSendLogByBusinessNo(ctx, *old.BusinessRequestNo); logErr == nil && logEntry.FailureReason != nil && *logEntry.FailureReason == "provider_outcome_unknown" {
					// 正式 OTP 无客户端幂等键；冷却窗口内由服务端复用原业务请求号，因此稳定重放原 unknown 结果。
					return EmailAcceptance{}, old.ID, ErrEmailOutcomeUnknown
				}
			}
			return EmailAcceptance{}, old.ID, ErrEmailUpstream
		case "accepted":
			if old.BusinessRequestNo == nil {
				return EmailAcceptance{}, old.ID, ErrEmailConflict
			}
			logEntry, logErr := s.repo.FindSendLogByBusinessNo(ctx, *old.BusinessRequestNo)
			if logErr != nil || logEntry.ProviderRequestID == nil {
				return EmailAcceptance{}, old.ID, ErrEmailConflict
			}
			return EmailAcceptance{RequestID: *logEntry.ProviderRequestID, Idempotent: true, ExpiresAt: old.ExpiresAt}, old.ID, nil
		}
	}
	if blocker, blockErr := s.findBlockingSend(ctx, scope, now); blockErr == nil {
		if blocker.IdempotencyKeyHash == kh {
			return EmailAcceptance{}, 0, ErrEmailOutcomeUnknown
		}
		return EmailAcceptance{}, 0, ErrEmailOutcomePending
	} else if !errors.Is(blockErr, gorm.ErrRecordNotFound) {
		return EmailAcceptance{}, 0, blockErr
	}
	htmlBody, err := renderEmailTemplate(tpl.Subject, tpl.TemplateText, code, expireMinutes)
	if err != nil {
		return EmailAcceptance{}, 0, err
	}
	expires := now.Add(time.Duration(expireMinutes) * time.Minute)
	masked := maskEmailAddress(recipient)
	v := &model.VerificationCode{TargetType: "email", TargetHash: &targetHMAC, TargetMasked: &masked, CodeHash: hash(code), Scene: scene, SendStatus: "pending", BusinessRequestNo: &businessRequestNo, IdempotencyScope: &scope, RequestFingerprint: &fingerprint, ExpiresAt: expires}
	logEntry := &model.EmailSendLog{BusinessRequestNo: businessRequestNo, TemplateID: tpl.ID, ProviderTemplateID: tpl.ProviderTemplateID, Scene: scene, Purpose: "otp", RecipientHMAC: targetHMAC, RecipientMasked: masked, IdempotencyScope: scope, IdempotencyKeyHash: kh, RequestFingerprint: fingerprint, Provider: emailProvider, Status: "pending", SubmittedAt: now, ExpiresAt: &expires}
	// 创建 pending 验证码属于外呼前事务边界，必须在写入前确认仍持有 Redis lease。
	if !lease.Owned(ctx) || !configLease.Owned(ctx) {
		return EmailAcceptance{}, 0, ErrEmailNotReady
	}
	if err := s.verificationRepo.CreateEmailSendPending(ctx, v, logEntry); err != nil {
		return EmailAcceptance{}, 0, err
	}
	// OTP pending 落库后再次读取绑定与模板。即便 Redis lease 在极窄窗口发生所有权切换，
	// 版本或可发送状态的变化也会在外呼前被拒绝，并把验证码明确收敛为不可使用。
	finalBinding, finalTemplate, finalReadErr := s.repo.GetBinding(ctx, scene)
	configChanged := finalReadErr != nil || finalBinding.TemplateID == nil || binding.TemplateID == nil || !finalBinding.Enabled ||
		*finalBinding.TemplateID != *binding.TemplateID || finalBinding.Version != binding.Version ||
		finalTemplate == nil || finalTemplate.Version != tpl.Version || validateSendTemplate(finalTemplate) != nil
	if configChanged {
		reason := "dispatch_config_changed_before_call"
		logEntry.Status, logEntry.FailureReason = "failed", &reason
		failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFailure()
		if err := s.verificationRepo.FinalizeEmailSend(failureCtx, v.ID, "failed", nil, logEntry); err != nil {
			return EmailAcceptance{}, 0, err
		}
		return EmailAcceptance{}, v.ID, ErrEmailConflict
	}
	if !lease.Owned(ctx) || !configLease.Owned(ctx) {
		reason := "send_lock_lost_before_call"
		logEntry.Status, logEntry.FailureReason = "failed", &reason
		failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFailure()
		if err := s.verificationRepo.FinalizeEmailSend(failureCtx, v.ID, "failed", nil, logEntry); err != nil {
			return EmailAcceptance{}, 0, err
		}
		return EmailAcceptance{}, v.ID, ErrEmailNotReady
	}
	accept, sendErr := s.adapter.SingleSendMail(ctx, EmailMessage{Recipient: recipient, Subject: tpl.Subject, HTMLBody: htmlBody})
	s.recordAdapterCall("send_mail", scene, sendErr)
	// 即使请求因供应商超时被取消，也必须在独立短上下文中原子持久化 OTP failed 与 unknown 墓碑。
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinalize()
	if !lease.Owned(finalizeCtx) {
		// 外呼后所有权异常不覆盖供应商明确结果，也不把已调用动作误报为未就绪。
		log.Printf("[email] OTP 外呼期间丢失 Redis 锁: verification_id=%d scene=%s", v.ID, scene)
	}
	if sendErr != nil {
		reason := directMailFailureReason(sendErr)
		publicErr := ErrEmailUpstream
		if isProviderOutcomeUnknown(sendErr) {
			reason = "provider_outcome_unknown"
			publicErr = ErrEmailOutcomeUnknown
		}
		logEntry.Status = "failed"
		logEntry.FailureReason = &reason
		if err := s.verificationRepo.FinalizeEmailSend(finalizeCtx, v.ID, "failed", nil, logEntry); err != nil {
			return EmailAcceptance{}, 0, err
		}
		return EmailAcceptance{}, v.ID, publicErr
	}
	acceptedAt := emailPersistenceNowUTC()
	logEntry.Status = "accepted"
	logEntry.ProviderRequestID = &accept.RequestID
	if err := s.verificationRepo.FinalizeEmailSend(finalizeCtx, v.ID, "accepted", &acceptedAt, logEntry); err != nil {
		return EmailAcceptance{}, 0, err
	}
	accept.ExpiresAt = expires
	return accept, v.ID, nil
}

func (s *EmailService) writeAudit(ctx context.Context, operator uint64, action, targetType string, targetID uint64, ip string, summary any) error {
	if s.audit == nil {
		return errors.New("审计服务未就绪")
	}
	target := fmt.Sprintf("%d", targetID)
	return s.audit.Record(ctx, &operator, "email", action, &targetType, &target, ip, summary)
}

// auditAttempt 是写操作的前置门禁；写入失败时业务动作不得执行。
func (s *EmailService) auditAttempt(ctx context.Context, operator uint64, action, targetType string, targetID uint64, ip string, summary any) error {
	return s.writeAudit(ctx, operator, action+".attempt", targetType, targetID, ip, summary)
}

// auditResult 是动作后的结果记录；失败由日志显式告警，但不能把已生效动作伪装成 HTTP 失败。
func (s *EmailService) auditResult(ctx context.Context, operator uint64, action, targetType string, targetID uint64, ip string, summary any) {
	if err := s.writeAudit(ctx, operator, action+".result", targetType, targetID, ip, summary); err != nil {
		log.Printf("[email] 业务已生效但结果审计写入失败: action=%s target_type=%s target_id=%d err=%v", action, targetType, targetID, err)
	}
}

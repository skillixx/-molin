package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"molin/server/internal/config"
	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/repository"
	"molin/server/internal/modules/sms/sender"
)

const smsFixedSceneCount int64 = 5

var (
	ErrSMSInvalidRequest              = errors.New("短信管理请求参数错误")
	ErrSMSAdminUnavailable            = errors.New("短信管理服务未就绪")
	ErrSMSTemplateNotFound            = errors.New("短信模板不存在")
	ErrSMSTemplateNotApproved         = errors.New("短信模板未通过审核")
	ErrSMSTemplateVersionConflict     = errors.New("短信模板版本冲突")
	ErrSMSTemplateProviderUnavailable = errors.New("短信模板供应商查询未就绪")
	ErrSMSTemplateSyncFailed          = errors.New("短信模板同步失败")
	ErrSMSSceneInvalid                = errors.New("短信场景不合法")
	ErrSMSSceneTemplateInvalid        = errors.New("短信场景模板不可用")
	ErrSMSSceneVersionConflict        = errors.New("短信场景版本冲突")
	ErrSMSTestSendUnavailable         = errors.New("短信测试发送不可用")
	ErrSMSTestSendIdempotencyConflict = errors.New("短信测试发送幂等冲突")
	ErrSMSTestSendPending             = errors.New("短信测试发送处理中")
	ErrSMSTestSendRateLimited         = errors.New("短信测试发送频率超限")
	ErrSMSTestSendProviderFailed      = errors.New("短信供应商拒绝测试发送")
)

var fixedSMSScenes = []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}

// SMSAdminSummary 保留服务层稳定命名，底层数据结构由短信领域模型定义。
type SMSAdminSummary = model.AdminSummary

type smsAdminSummaryRepository interface {
	GetAdminSummary(ctx context.Context) (SMSAdminSummary, error)
}

type smsAdminTemplateRepository interface {
	GetAdminTemplate(ctx context.Context, id uint64) (*model.Template, error)
	UpdateAdminTemplateStatus(ctx context.Context, id, version uint64, enabled bool) error
}

type smsAdminSyncRepository interface {
	ApplyTemplateSnapshots(ctx context.Context, snapshots []model.TemplateSnapshot, syncedAt time.Time) (model.TemplateSyncResult, error)
}

type smsAdminQueryRepository interface {
	ListAdminTemplates(ctx context.Context, filter model.TemplateListFilter) ([]model.Template, int64, error)
	ListAdminSceneBindings(ctx context.Context) ([]model.SceneBinding, error)
	UpsertAdminSceneBinding(ctx context.Context, scene, signName string, templateID, version, operatorID uint64, enabled bool) (*model.SceneBinding, error)
	ListAdminSendLogs(ctx context.Context, filter model.SendLogListFilter) ([]model.SendLog, int64, error)
}

type smsAdminTestSendRepository interface {
	ReserveTestSend(ctx context.Context, log *model.SendLog) (*model.SendLog, bool, error)
	CompleteTestSend(ctx context.Context, id uint64, status string, providerRequestID, providerCode, failureSummary *string, retryAfterSeconds *int64, completedAt time.Time) error
}

type smsTestDispatcher interface {
	Prepare(ctx context.Context, scene, phone string) (PreparedSend, error)
	SendProvider(ctx context.Context, plan PreparedSend, phone, rawCode, businessRequestID string) (DispatchResult, error)
}

type smsTestSendLimiter interface {
	Allow(ctx context.Context, adminID uint64, phoneHMAC string) (bool, int64, error)
}

// SMSAdminService 承载短信管理端业务规则，不向 handler 暴露 GORM 或阿里云 SDK 类型。
type SMSAdminService struct {
	summaryRepo      smsAdminSummaryRepository
	templateRepo     smsAdminTemplateRepository
	syncRepo         smsAdminSyncRepository
	templateProvider sender.TemplateProvider
	fixedSignName    string
	now              func() time.Time
	queryRepo        smsAdminQueryRepository
	testSendRepo     smsAdminTestSendRepository
	testDispatcher   smsTestDispatcher
	testLimiter      smsTestSendLimiter
	testConfig       config.Config
}

func NewSMSAdminService(repo any) *SMSAdminService {
	service := &SMSAdminService{now: time.Now}
	if summaryRepo, ok := repo.(smsAdminSummaryRepository); ok {
		service.summaryRepo = summaryRepo
	}
	if templateRepo, ok := repo.(smsAdminTemplateRepository); ok {
		service.templateRepo = templateRepo
	}
	if syncRepo, ok := repo.(smsAdminSyncRepository); ok {
		service.syncRepo = syncRepo
	}
	if queryRepo, ok := repo.(smsAdminQueryRepository); ok {
		service.queryRepo = queryRepo
	}
	if testRepo, ok := repo.(smsAdminTestSendRepository); ok {
		service.testSendRepo = testRepo
	}
	return service
}

func (s *SMSAdminService) ListTemplates(ctx context.Context, filter model.TemplateListFilter) ([]model.Template, int64, error) {
	if s == nil || s.queryRepo == nil {
		return nil, 0, ErrSMSAdminUnavailable
	}
	return s.queryRepo.ListAdminTemplates(ctx, filter)
}

func (s *SMSAdminService) GetTemplate(ctx context.Context, id uint64) (*model.Template, error) {
	if s == nil || s.templateRepo == nil {
		return nil, ErrSMSAdminUnavailable
	}
	item, err := s.templateRepo.GetAdminTemplate(ctx, id)
	if errors.Is(err, repository.ErrAdminTemplateNotFound) {
		return nil, ErrSMSTemplateNotFound
	}
	return item, err
}

func (s *SMSAdminService) ListScenes(ctx context.Context) ([]model.AdminScene, error) {
	if s == nil || s.queryRepo == nil {
		return nil, ErrSMSAdminUnavailable
	}
	bindings, err := s.queryRepo.ListAdminSceneBindings(ctx)
	if err != nil {
		return nil, err
	}
	byScene := make(map[string]model.SceneBinding, len(bindings))
	for _, binding := range bindings {
		byScene[binding.Scene] = binding
	}
	items := make([]model.AdminScene, 0, len(fixedSMSScenes))
	for _, scene := range fixedSMSScenes {
		view := model.AdminScene{Scene: scene}
		if binding, ok := byScene[scene]; ok {
			view.TemplateID, view.SignName, view.UpdatedBy = &binding.TemplateID, &binding.SignName, binding.UpdatedBy
			view.Enabled, view.Version = binding.Enabled, binding.Version
			updatedAt := binding.UpdatedAt
			view.UpdatedAt = &updatedAt
			if binding.Template.ID != 0 {
				view.TemplateCode, view.TemplateName, view.ProviderAuditStatus = &binding.Template.TemplateCode, &binding.Template.TemplateName, &binding.Template.ProviderAuditStatus
			}
		}
		items = append(items, view)
	}
	return items, nil
}

func (s *SMSAdminService) SetScene(ctx context.Context, scene string, templateID, version, operatorID uint64, enabled bool) (*model.AdminScene, error) {
	if s == nil || s.queryRepo == nil || s.templateRepo == nil || s.fixedSignName == "" {
		return nil, ErrSMSAdminUnavailable
	}
	if !isFixedSMSScene(scene) || templateID == 0 || operatorID == 0 {
		return nil, ErrSMSSceneInvalid
	}
	template, err := s.templateRepo.GetAdminTemplate(ctx, templateID)
	if errors.Is(err, repository.ErrAdminTemplateNotFound) {
		return nil, ErrSMSTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	if !template.LocalEnabled || template.ProviderAuditStatus != "approved" || template.TemplateType != "verification" || !strings.Contains(template.Content, "${code}") {
		return nil, ErrSMSSceneTemplateInvalid
	}
	binding, err := s.queryRepo.UpsertAdminSceneBinding(ctx, scene, s.fixedSignName, templateID, version, operatorID, enabled)
	if errors.Is(err, repository.ErrAdminSceneConflict) {
		return nil, ErrSMSSceneVersionConflict
	}
	if errors.Is(err, repository.ErrAdminSceneTemplateInvalid) {
		return nil, ErrSMSSceneTemplateInvalid
	}
	if err != nil {
		return nil, err
	}
	updatedAt := binding.UpdatedAt
	return &model.AdminScene{Scene: scene, TemplateID: &template.ID, TemplateCode: &template.TemplateCode, TemplateName: &template.TemplateName,
		ProviderAuditStatus: &template.ProviderAuditStatus, SignName: &binding.SignName, Enabled: binding.Enabled,
		Version: binding.Version, UpdatedBy: binding.UpdatedBy, UpdatedAt: &updatedAt}, nil
}

func (s *SMSAdminService) ListSendLogs(ctx context.Context, filter model.SendLogListFilter) ([]model.SendLog, int64, error) {
	if s == nil || s.queryRepo == nil {
		return nil, 0, ErrSMSAdminUnavailable
	}
	return s.queryRepo.ListAdminSendLogs(ctx, filter)
}

func isFixedSMSScene(scene string) bool {
	for _, allowed := range fixedSMSScenes {
		if scene == allowed {
			return true
		}
	}
	return false
}

// ConfigureTestSend 注入真实发送器和 Redis 限流器；任一依赖缺失时测试发送失败关闭。
func (s *SMSAdminService) ConfigureTestSend(cfg config.Config, dispatcher smsTestDispatcher, redisClient *redis.Client) {
	s.testConfig, s.testDispatcher = cfg, dispatcher
	if redisClient != nil {
		s.testLimiter = &redisSMSAdminTestLimiter{client: redisClient}
	}
}

type TestSendResult struct {
	BusinessRequestID string    `json:"business_request_id"`
	SubmitStatus      string    `json:"submit_status"`
	Idempotent        bool      `json:"idempotent"`
	TemplateCode      string    `json:"template_code"`
	PhoneMasked       string    `json:"phone_masked"`
	SubmittedAt       time.Time `json:"submitted_at"`
	RetryAfterSeconds int64     `json:"retry_after_seconds,omitempty"`
}

func (s *SMSAdminService) TestSend(ctx context.Context, adminID, templateID uint64, scene, phone, idempotencyKey string) (TestSendResult, error) {
	if s == nil || s.testSendRepo == nil || s.testDispatcher == nil || s.testLimiter == nil || !s.testConfig.SMSEnabled || !s.testConfig.SMSTestMode || len(s.testConfig.SMSTestPhoneWhitelist) == 0 {
		return TestSendResult{}, ErrSMSTestSendUnavailable
	}
	if adminID == 0 || templateID == 0 || !isFixedSMSScene(scene) || len(idempotencyKey) < 1 || len(idempotencyKey) > 128 || strings.TrimSpace(phone) == "" {
		return TestSendResult{}, ErrSMSInvalidRequest
	}
	plan, err := s.testDispatcher.Prepare(ctx, scene, phone)
	if errors.Is(err, ErrPhoneNotAllowed) {
		return TestSendResult{}, ErrSMSInvalidRequest
	}
	if errors.Is(err, ErrSMSUnavailable) {
		return TestSendResult{}, ErrSMSTestSendUnavailable
	}
	if err != nil || plan.TemplateID != templateID {
		return TestSendResult{}, ErrSMSSceneTemplateInvalid
	}
	phoneDigest := SMSPhoneHMAC(phone, s.testConfig.SMSPhoneHMACSecret)
	scope := fmt.Sprintf("admin-sms-template-test:admin:%d:template:%d:scene:%s:phone:%s", adminID, templateID, scene, phoneDigest)
	keyHash := keyedDigest(idempotencyKey, s.testConfig.SMSPhoneHMACSecret)
	ownerKeyHash := keyedDigest(fmt.Sprintf("%d|%s", adminID, idempotencyKey), s.testConfig.SMSPhoneHMACSecret)
	fingerprint := keyedDigest(fmt.Sprintf("%d|%d|%s|%s", adminID, templateID, scene, phoneDigest), s.testConfig.SMSPhoneHMACSecret)
	businessID, err := randomSMSBusinessID()
	if err != nil {
		return TestSendResult{}, err
	}
	code, err := randomSMSCode()
	if err != nil {
		return TestSendResult{}, err
	}
	now := s.now().UTC()
	log := &model.SendLog{Purpose: "test", Scene: scene, PhoneMasked: MaskSMSPhone(phone), PhoneHMAC: phoneDigest,
		TemplateID: &templateID, TemplateCode: plan.TemplateCode, SignName: plan.SignName, Provider: plan.Provider,
		BusinessRequestID: businessID, IdempotencyScope: &scope, IdempotencyKeyHash: &keyHash,
		IdempotencyOwnerKeyHash: &ownerKeyHash, RequestFingerprint: &fingerprint, SubmitStatus: "pending", SubmittedAt: now}
	reserved, created, err := s.testSendRepo.ReserveTestSend(ctx, log)
	if errors.Is(err, repository.ErrTestSendIdempotencyConflict) {
		return TestSendResult{}, ErrSMSTestSendIdempotencyConflict
	}
	if err != nil {
		return TestSendResult{}, err
	}
	if !created {
		return replayTestSend(reserved)
	}
	allowed, retryAfter, err := s.testLimiter.Allow(ctx, adminID, phoneDigest)
	if err != nil {
		reason := "测试发送限流服务不可用"
		_ = s.testSendRepo.CompleteTestSend(context.WithoutCancel(ctx), reserved.ID, "failed", nil, nil, &reason, nil, s.now().UTC())
		return TestSendResult{}, ErrSMSTestSendUnavailable
	}
	if !allowed {
		reason := "测试发送频率超限"
		_ = s.testSendRepo.CompleteTestSend(context.WithoutCancel(ctx), reserved.ID, "failed", nil, nil, &reason, &retryAfter, s.now().UTC())
		return TestSendResult{RetryAfterSeconds: retryAfter}, ErrSMSTestSendRateLimited
	}
	providerResult, sendErr := s.testDispatcher.SendProvider(ctx, plan, phone, code, businessID)
	status := "accepted"
	var providerRequestID, providerCode, failure *string
	if providerResult.ProviderRequestID != "" {
		providerRequestID = stringPointer(providerResult.ProviderRequestID)
	}
	if providerResult.ProviderCode != "" {
		providerCode = stringPointer(providerResult.ProviderCode)
	}
	if sendErr != nil {
		status = "failed"
		safe := "短信供应商请求失败"
		var providerErr *sender.ProviderError
		if errors.As(sendErr, &providerErr) {
			safe = providerErr.SafeSummary()
		}
		failure = &safe
	}
	if err := s.testSendRepo.CompleteTestSend(context.WithoutCancel(ctx), reserved.ID, status, providerRequestID, providerCode, failure, nil, s.now().UTC()); err != nil {
		return TestSendResult{}, err
	}
	if sendErr != nil {
		return TestSendResult{}, ErrSMSTestSendProviderFailed
	}
	return TestSendResult{BusinessRequestID: businessID, SubmitStatus: "accepted", TemplateCode: plan.TemplateCode, PhoneMasked: MaskSMSPhone(phone), SubmittedAt: now}, nil
}

func replayTestSend(log *model.SendLog) (TestSendResult, error) {
	if log == nil || log.SubmitStatus == "pending" {
		return TestSendResult{}, ErrSMSTestSendPending
	}
	if log.SubmitStatus == "failed" {
		if log.FailureSummary != nil && *log.FailureSummary == "测试发送频率超限" {
			result := TestSendResult{}
			if log.RetryAfterSeconds != nil {
				result.RetryAfterSeconds = *log.RetryAfterSeconds
			}
			return result, ErrSMSTestSendRateLimited
		}
		if log.FailureSummary != nil && *log.FailureSummary == "测试发送限流服务不可用" {
			return TestSendResult{}, ErrSMSTestSendUnavailable
		}
		return TestSendResult{}, ErrSMSTestSendProviderFailed
	}
	return TestSendResult{BusinessRequestID: log.BusinessRequestID, SubmitStatus: log.SubmitStatus, Idempotent: true, TemplateCode: log.TemplateCode, PhoneMasked: log.PhoneMasked, SubmittedAt: log.SubmittedAt}, nil
}

func keyedDigest(value, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func randomSMSBusinessID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "sms_" + hex.EncodeToString(bytes), nil
}

func randomSMSCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

type redisSMSEvaluator interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
}

type redisSMSAdminTestLimiter struct{ client redisSMSEvaluator }

func (l *redisSMSAdminTestLimiter) Allow(ctx context.Context, adminID uint64, phoneDigest string) (bool, int64, error) {
	const script = `local a=tonumber(redis.call('GET',KEYS[1]) or '0'); local p=tonumber(redis.call('GET',KEYS[2]) or '0'); if a>=10 or p>=10 then local t=math.max(redis.call('TTL',KEYS[1]),redis.call('TTL',KEYS[2])); if t<1 then t=60 end; return {0,t} end; local na=redis.call('INCR',KEYS[1]); local np=redis.call('INCR',KEYS[2]); if na==1 then redis.call('EXPIRE',KEYS[1],60) end; if np==1 then redis.call('EXPIRE',KEYS[2],60) end; return {1,0}`
	keys := []string{fmt.Sprintf("sms:test:admin:%d", adminID), "sms:test:phone:" + phoneDigest}
	value, err := l.client.Eval(ctx, script, keys).Result()
	if err != nil {
		return false, 0, err
	}
	parts, ok := value.([]interface{})
	if !ok || len(parts) != 2 {
		return false, 0, errors.New("短信测试限流响应无效")
	}
	allowed, _ := parts[0].(int64)
	retry, _ := parts[1].(int64)
	return allowed == 1, retry, nil
}

// ConfigureTemplateSync 注入只读供应商与固定签名；供应商未就绪时同步接口失败关闭。
func (s *SMSAdminService) ConfigureTemplateSync(provider sender.TemplateProvider, fixedSignName string) {
	s.templateProvider = provider
	s.fixedSignName = strings.TrimSpace(fixedSignName)
}

// SyncTemplates 先在事务外取得完整快照，任一详情失败都不会触发数据库写入。
func (s *SMSAdminService) SyncTemplates(ctx context.Context) (model.TemplateSyncResult, error) {
	if s == nil || s.syncRepo == nil || s.templateProvider == nil || s.fixedSignName == "" {
		return model.TemplateSyncResult{}, ErrSMSTemplateProviderUnavailable
	}
	type providerResult struct {
		items []sender.TemplateSnapshot
		err   error
	}
	providerResults := make(chan providerResult, 1)
	go func() {
		items, err := s.templateProvider.ListTemplates(ctx)
		providerResults <- providerResult{items: items, err: err}
	}()
	var providerSnapshots []sender.TemplateSnapshot
	select {
	case <-ctx.Done():
		// 部分 SDK 调用无法直接绑定 context；缓冲通道保证迟到结果退出时不会阻塞协程。
		return model.TemplateSyncResult{}, ErrSMSTemplateSyncFailed
	case result := <-providerResults:
		if result.err != nil {
			return model.TemplateSyncResult{}, ErrSMSTemplateSyncFailed
		}
		providerSnapshots = result.items
	}
	if err := ctx.Err(); err != nil {
		return model.TemplateSyncResult{}, ErrSMSTemplateSyncFailed
	}
	snapshots := make([]model.TemplateSnapshot, 0, len(providerSnapshots))
	ignoredCount := int64(0)
	seenTemplates := make(map[string]struct{}, len(providerSnapshots))
	for _, item := range providerSnapshots {
		if item.Provider != "aliyun" || strings.TrimSpace(item.TemplateCode) == "" || (item.AuditStatus != "pending" && item.AuditStatus != "approved" && item.AuditStatus != "rejected") {
			return model.TemplateSyncResult{}, ErrSMSTemplateSyncFailed
		}
		// 只同步固定签名下含 ${code} 的验证码模板，其他供应商资源不进入本地控制面。
		if item.SignName != s.fixedSignName || item.TemplateType != "verification" || !strings.Contains(item.Content, "${code}") {
			ignoredCount++
			continue
		}
		key := item.Provider + "|" + item.TemplateCode
		if _, exists := seenTemplates[key]; exists {
			ignoredCount++
			continue
		}
		seenTemplates[key] = struct{}{}
		var rejectionReason *string
		if value := strings.TrimSpace(item.RejectionReason); value != "" {
			rejectionReason = &value
		}
		snapshots = append(snapshots, model.TemplateSnapshot{
			Provider: item.Provider, TemplateCode: item.TemplateCode, TemplateName: item.TemplateName,
			TemplateType: item.TemplateType, Content: item.Content, Variables: item.Variables,
			ProviderAuditStatus: item.AuditStatus, RejectionReason: rejectionReason,
			ProviderUpdatedAt: item.ProviderUpdatedAt,
		})
	}
	result, err := s.syncRepo.ApplyTemplateSnapshots(ctx, snapshots, s.now().UTC())
	if err != nil {
		return model.TemplateSyncResult{}, ErrSMSTemplateSyncFailed
	}
	result.IgnoredCount = ignoredCount
	result.TotalCount = int64(len(providerSnapshots))
	return result, nil
}

// Summary 返回后端聚合概览；未绑定数量按五个固定业务场景计算。
func (s *SMSAdminService) Summary(ctx context.Context) (SMSAdminSummary, error) {
	if s == nil || s.summaryRepo == nil {
		return SMSAdminSummary{}, ErrSMSAdminUnavailable
	}
	summary, err := s.summaryRepo.GetAdminSummary(ctx)
	if err != nil {
		return SMSAdminSummary{}, err
	}
	summary.UnboundSceneTotal = smsFixedSceneCount - summary.BoundSceneTotal
	if summary.UnboundSceneTotal < 0 {
		summary.UnboundSceneTotal = 0
	}
	return summary, nil
}

// SetTemplateStatus 校验供应商审核快照后执行乐观锁更新。
func (s *SMSAdminService) SetTemplateStatus(ctx context.Context, id, version uint64, enabled bool) (*model.Template, error) {
	if s == nil || s.templateRepo == nil || (enabled && s.fixedSignName == "") {
		return nil, ErrSMSAdminUnavailable
	}
	template, err := s.templateRepo.GetAdminTemplate(ctx, id)
	if err != nil || template == nil {
		if errors.Is(err, repository.ErrAdminTemplateNotFound) {
			return nil, ErrSMSTemplateNotFound
		}
		if err != nil {
			return nil, err
		}
		return nil, ErrSMSTemplateNotFound
	}
	if version == 0 || template.Version != version {
		return nil, ErrSMSTemplateVersionConflict
	}
	if enabled && (template.ProviderAuditStatus != "approved" || template.TemplateType != "verification" || !strings.Contains(template.Content, "${code}")) {
		return nil, ErrSMSTemplateNotApproved
	}
	if err := s.templateRepo.UpdateAdminTemplateStatus(ctx, id, version, enabled); err != nil {
		if errors.Is(err, repository.ErrAdminTemplateConflict) {
			return nil, ErrSMSTemplateVersionConflict
		}
		return nil, err
	}
	template.LocalEnabled = enabled
	template.Version++
	return template, nil
}

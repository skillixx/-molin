package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

const (
	emailAdminVerifyBootstrapScope = "admin_verify"
	emailAdminVerifyTemplateName   = "molin_admin_verify_code_v1"
)

var (
	ErrEmailBootstrapCompleted      = errors.New("管理员邮箱认证场景已完成首次配置")
	ErrEmailBootstrapName           = errors.New("邮件模板名称不符合管理员认证约定")
	ErrEmailBootstrapStatus         = errors.New("邮件模板状态不允许首次配置")
	ErrEmailBootstrapOutcomeUnknown = errors.New("供应商响应未知，请稍后重试")
)

// EmailAdminVerifyBootstrapResult 是一次性入口允许返回的最小结果。
type EmailAdminVerifyBootstrapResult struct {
	Scene      string `json:"scene"`
	Configured bool   `json:"configured"`
	Idempotent bool   `json:"idempotent"`
}

type emailBootstrapRepository interface {
	FindAdminVerifyBootstrapReceipt(context.Context) (*model.EmailAdminVerifyBootstrapReceipt, error)
	ApplyAdminVerifyBootstrap(context.Context, model.EmailProviderTemplate, model.EmailAdminVerifyBootstrapReceipt, func(*gorm.DB, uint64, uint64) error) (*model.EmailAdminVerifyBootstrapReceipt, bool, error)
}

type emailBootstrapAuditor interface {
	Record(context.Context, *uint64, string, string, *string, *string, string, any) error
	RecordWithTx(context.Context, *gorm.DB, *uint64, string, string, *string, *string, string, any) error
}

// EmailBootstrapService 实现一次性 admin_verify 配置，不依赖 Redis 锁。
type EmailBootstrapService struct {
	repo  emailBootstrapRepository
	email *EmailService
	audit emailBootstrapAuditor
}

func NewEmailBootstrapService(repo emailBootstrapRepository, email *EmailService, audit emailBootstrapAuditor) *EmailBootstrapService {
	return &EmailBootstrapService{repo: repo, email: email, audit: audit}
}

// ConfigureAdminVerify 完成预检、供应商只读查询和强事务写入。
func (s *EmailBootstrapService) ConfigureAdminVerify(ctx context.Context, providerTemplateID, key string, adminID uint64, ip string) (*EmailAdminVerifyBootstrapResult, error) {
	if s == nil || s.repo == nil || s.email == nil || s.audit == nil || !s.email.bootstrapReady() {
		return nil, ErrEmailNotReady
	}
	if !validBootstrapProviderTemplateID(providerTemplateID) || !validBootstrapValue(key, 16, 128) || adminID == 0 {
		return nil, ErrEmailInvalid
	}
	keyHash, fingerprint := s.bootstrapDigests(adminID, key, providerTemplateID)
	if old, err := s.repo.FindAdminVerifyBootstrapReceipt(ctx); err == nil {
		return replayAdminVerifyBootstrap(old, adminID, keyHash, fingerprint)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	operator, targetType, targetID := adminID, "email_scene_binding", emailAdminVerifyBootstrapScope
	if err := s.audit.Record(ctx, &operator, "email", "email.admin_verify.bootstrap.attempt", &targetType, &targetID, ip,
		map[string]any{"scene": emailAdminVerifyBootstrapScope, "provider": emailProvider, "provider_template_id": providerTemplateID}); err != nil {
		return nil, err
	}

	detail, err := s.email.adapter.DescribeTemplate(ctx, providerTemplateID)
	s.email.recordAdapterCall("describe_template", "template_sync", err)
	if err != nil {
		if errors.Is(err, ErrDirectMailNotFound) {
			return nil, ErrEmailTemplateGone
		}
		if errors.Is(err, ErrDirectMailStatusUnknown) {
			return nil, ErrEmailBootstrapStatus
		}
		if isProviderOutcomeUnknown(err) {
			return nil, ErrEmailBootstrapOutcomeUnknown
		}
		return nil, ErrEmailUpstream
	}
	if detail.Name != emailAdminVerifyTemplateName {
		return nil, ErrEmailBootstrapName
	}
	switch detail.Status {
	case "draft":
		return nil, ErrEmailTemplateDraft
	case "pending":
		return nil, ErrEmailTemplateReview
	case "rejected":
		return nil, ErrEmailTemplateReject
	case "approved":
	default:
		return nil, ErrEmailBootstrapStatus
	}
	vars := variablesFromText(detail.TemplateText)
	if !variablesComplete(vars) {
		return nil, ErrEmailVariables
	}
	rawVariables, _ := jsonMarshalStrings(vars)
	template := model.EmailProviderTemplate{
		Provider: emailProvider, ProviderTemplateID: providerTemplateID, Name: detail.Name,
		Subject: detail.Subject, TemplateText: detail.TemplateText, VariablesJSON: rawVariables,
		ContentSHA256: hash(detail.Subject + "\n" + detail.TemplateText), ProviderStatus: detail.Status,
		VariablesComplete: true, ProviderCreatedAt: detail.ProviderCreatedAt,
	}
	receipt := model.EmailAdminVerifyBootstrapReceipt{
		Scope: emailAdminVerifyBootstrapScope, Provider: emailProvider, ProviderTemplateID: providerTemplateID,
		IdempotencyKeyHash: keyHash, RequestFingerprint: fingerprint, CompletedBy: adminID,
	}
	stored, created, err := s.repo.ApplyAdminVerifyBootstrap(ctx, template, receipt, func(tx *gorm.DB, receiptID, templateID uint64) error {
		receiptTarget := fmt.Sprintf("%d", receiptID)
		resultTargetType := "email_admin_verify_bootstrap_receipt"
		return s.audit.RecordWithTx(ctx, tx, &operator, "email", "email.admin_verify.bootstrap.result", &resultTargetType, &receiptTarget, ip,
			map[string]any{"scene": emailAdminVerifyBootstrapScope, "provider": emailProvider, "template_id": templateID, "configured": true})
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrEmailConflict
		}
		if errors.Is(err, repository.ErrEmailConflict) {
			return nil, ErrEmailConflict
		}
		return nil, err
	}
	if !created {
		return replayAdminVerifyBootstrap(stored, adminID, keyHash, fingerprint)
	}
	return &EmailAdminVerifyBootstrapResult{Scene: emailAdminVerifyBootstrapScope, Configured: true}, nil
}

// validBootstrapProviderTemplateID 在服务层再次执行防御性校验，确保任何调用入口都不能绕过模板编号契约。
// 允许保留前导零，但整个字符串表示的值必须大于零，因此全零字符串会被拒绝。
func validBootstrapProviderTemplateID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	hasNonZero := false
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
		hasNonZero = hasNonZero || value[i] != '0'
	}
	return hasNonZero
}

func (s *EmailService) bootstrapReady() bool {
	if s == nil || !strongDistinctEmailSecrets(s.addressSecret, s.idempotencySecret) || s.adapter == nil || !s.adapter.Ready() {
		return false
	}
	if s.adapterMode != "production" && !(s.adapterMode == "mock" && isSafeEmailEnvironment(s.appEnv)) {
		return false
	}
	return true
}

func isSafeEmailEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "development", "dev", "test", "testing":
		return true
	default:
		return false
	}
}

func (s *EmailBootstrapService) bootstrapDigests(adminID uint64, key, providerTemplateID string) (string, string) {
	keyHash := crypto.HMAC256(fmt.Sprintf("admin:%d|%s", adminID, key), s.email.idempotencySecret)
	fingerprint := hash(fmt.Sprintf("admin_id=%d\nmethod=POST\npath=/api/internal/email/bootstrap/admin-verify\nscope=%s\nprovider_template_id=%s", adminID, emailAdminVerifyBootstrapScope, providerTemplateID))
	return keyHash, fingerprint
}

func replayAdminVerifyBootstrap(receipt *model.EmailAdminVerifyBootstrapReceipt, adminID uint64, keyHash, fingerprint string) (*EmailAdminVerifyBootstrapResult, error) {
	if receipt == nil {
		return nil, ErrEmailConflict
	}
	if receipt.CompletedBy == adminID && receipt.IdempotencyKeyHash == keyHash {
		if receipt.RequestFingerprint != fingerprint {
			return nil, ErrEmailConflict
		}
		return &EmailAdminVerifyBootstrapResult{Scene: emailAdminVerifyBootstrapScope, Configured: true, Idempotent: true}, nil
	}
	return nil, ErrEmailBootstrapCompleted
}

func validBootstrapValue(value string, minBytes, maxBytes int) bool {
	length := len([]byte(value))
	if length < minBytes || length > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func jsonMarshalStrings(values []string) (string, error) {
	raw, err := json.Marshal(values)
	return string(raw), err
}

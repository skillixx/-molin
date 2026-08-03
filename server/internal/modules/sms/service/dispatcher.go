package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"molin/server/internal/config"
	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/repository"
	"molin/server/internal/modules/sms/sender"
)

var (
	ErrSMSUnavailable  = errors.New("短信功能当前不可用")
	ErrSceneNotBound   = errors.New("短信场景未绑定可用模板")
	ErrPhoneNotAllowed = errors.New("测试手机号不在白名单")
)

var allowedScenes = map[string]bool{
	"register":       true,
	"login":          true,
	"reset_password": true,
	"bind_phone":     true,
	"admin_verify":   true,
}

type smsRepository interface {
	FindActiveBinding(ctx context.Context, scene string) (*model.SceneBinding, error)
	CreateSendLog(ctx context.Context, log *model.SendLog) error
}

// PreparedSend 是经过配置、白名单和数据库绑定校验后的不可变发送计划。
type PreparedSend struct {
	Scene        string
	TemplateID   uint64
	TemplateCode string
	SignName     string
	Provider     string
}

// DispatchResult 表示供应商提交结果；Accepted 只代表受理，不代表最终送达。
type DispatchResult struct {
	Accepted          bool
	ProviderRequestID string
	ProviderCode      string
}

// Dispatcher 负责关闭态校验、场景绑定解析、供应商调用和脱敏日志。
type Dispatcher struct {
	cfg      config.Config
	repo     smsRepository
	sender   sender.Sender
	accepted atomic.Uint64
	failed   atomic.Uint64
}

// MetricsSnapshot 是阶段 1 的最小运行指标，可由后续监控适配器定期采集。
type MetricsSnapshot struct {
	Accepted uint64
	Failed   uint64
}

func NewDispatcher(cfg config.Config, repo smsRepository, smsSender sender.Sender) *Dispatcher {
	return &Dispatcher{cfg: cfg, repo: repo, sender: smsSender}
}

// MetricsSnapshot 返回进程启动以来的供应商受理和失败计数，不包含手机号等敏感字段。
func (d *Dispatcher) MetricsSnapshot() MetricsSnapshot {
	if d == nil {
		return MetricsSnapshot{}
	}
	return MetricsSnapshot{Accepted: d.accepted.Load(), Failed: d.failed.Load()}
}

func (d *Dispatcher) Prepare(ctx context.Context, scene, phone string) (PreparedSend, error) {
	if d == nil || d.repo == nil || d.sender == nil || !d.cfg.SMSEnabled {
		return PreparedSend{}, ErrSMSUnavailable
	}
	if err := d.cfg.ValidateSMS(); err != nil {
		return PreparedSend{}, ErrSMSUnavailable
	}
	if !allowedScenes[scene] {
		return PreparedSend{}, ErrSceneNotBound
	}
	if d.cfg.SMSTestMode && !contains(d.cfg.SMSTestPhoneWhitelist, phone) {
		return PreparedSend{}, ErrPhoneNotAllowed
	}
	binding, err := d.repo.FindActiveBinding(ctx, scene)
	if err != nil || binding == nil {
		if errors.Is(err, repository.ErrBindingNotFound) || binding == nil {
			return PreparedSend{}, ErrSceneNotBound
		}
		return PreparedSend{}, err
	}
	// 固定签名必须与绑定快照一致，避免过期绑定绕过后端签名配置。
	if binding.Scene != scene || !binding.Enabled || binding.SignName != d.cfg.SMSAliyunSignName ||
		binding.Template.Provider != d.cfg.SMSProvider || !binding.Template.LocalEnabled ||
		binding.Template.ProviderAuditStatus != "approved" || binding.Template.TemplateCode == "" ||
		!model.HasExactCodeVariable(binding.Template.Content, binding.Template.Variables) {
		return PreparedSend{}, ErrSceneNotBound
	}
	return PreparedSend{
		Scene:        scene,
		TemplateID:   binding.Template.ID,
		TemplateCode: binding.Template.TemplateCode,
		SignName:     binding.SignName,
		Provider:     binding.Template.Provider,
	}, nil
}

func (d *Dispatcher) Submit(ctx context.Context, plan PreparedSend, phone, rawCode, businessRequestID string) (DispatchResult, error) {
	result, sendErr := d.SendProvider(ctx, plan, phone, rawCode, businessRequestID)
	logEntry := &model.SendLog{
		Purpose:     "otp",
		Scene:       plan.Scene,
		PhoneMasked: maskPhone(phone), PhoneHMAC: phoneHMAC(phone, d.cfg.SMSPhoneHMACSecret),
		TemplateID: &plan.TemplateID, TemplateCode: plan.TemplateCode, SignName: plan.SignName, Provider: plan.Provider,
		BusinessRequestID: businessRequestID, SubmitStatus: "accepted", SubmittedAt: time.Now().UTC(),
	}
	if result.ProviderRequestID != "" {
		logEntry.ProviderRequestID = stringPointer(result.ProviderRequestID)
	}
	if result.ProviderCode != "" {
		logEntry.ProviderCode = stringPointer(result.ProviderCode)
	}
	if sendErr != nil {
		logEntry.SubmitStatus = "failed"
		var providerErr *sender.ProviderError
		if errors.As(sendErr, &providerErr) {
			logEntry.FailureSummary = stringPointer(providerErr.SafeSummary())
			if providerErr.ProviderCode != "" {
				logEntry.ProviderCode = stringPointer(providerErr.ProviderCode)
			}
		} else {
			logEntry.FailureSummary = stringPointer("短信供应商请求失败")
		}
	}
	completedAt := time.Now().UTC()
	logEntry.CompletedAt = &completedAt
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := d.repo.CreateSendLog(logCtx, logEntry); err != nil {
		return DispatchResult{}, err
	}
	if sendErr != nil {
		return result, sendErr
	}
	return result, nil
}

// SendProvider 只执行供应商提交，测试发送由调用方在提交前完成幂等抢占并负责终态日志。
func (d *Dispatcher) SendProvider(ctx context.Context, plan PreparedSend, phone, rawCode, businessRequestID string) (DispatchResult, error) {
	params, err := json.Marshal(map[string]string{"code": rawCode})
	if err != nil {
		return DispatchResult{}, err
	}
	result, sendErr := d.sender.Send(ctx, sender.Request{
		Phone:             phone,
		SignName:          plan.SignName,
		TemplateCode:      plan.TemplateCode,
		TemplateParamJSON: string(params),
		BusinessRequestID: businessRequestID,
		Timeout:           5 * time.Second,
	})
	if sendErr != nil {
		d.failed.Add(1)
		return DispatchResult{ProviderRequestID: result.ProviderRequestID, ProviderCode: result.ProviderCode}, sendErr
	}
	d.accepted.Add(1)
	return DispatchResult{Accepted: true, ProviderRequestID: result.ProviderRequestID, ProviderCode: result.ProviderCode}, nil
}

func MaskSMSPhone(phone string) string         { return maskPhone(phone) }
func SMSPhoneHMAC(phone, secret string) string { return phoneHMAC(phone, secret) }

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:3] + "****" + phone[len(phone)-4:]
}

func phoneHMAC(phone, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(phone))
	return hex.EncodeToString(mac.Sum(nil))
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }

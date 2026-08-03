package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"molin/server/internal/config"
	authmodel "molin/server/internal/modules/auth/model"
	smsmodel "molin/server/internal/modules/sms/model"
	smssender "molin/server/internal/modules/sms/sender"
	smsservice "molin/server/internal/modules/sms/service"
)

type fakeVerificationRepository struct {
	created *authmodel.VerificationCode
	status  string
}

func (f *fakeVerificationRepository) Create(_ context.Context, code *authmodel.VerificationCode) error {
	f.created = code
	return nil
}

func (f *fakeVerificationRepository) CheckAndMarkUsed(_ context.Context, targetType, _, _, _ string) error {
	if targetType == "phone" && (f.created == nil || f.status != "accepted") {
		return errors.New("不可校验")
	}
	return nil
}

func (f *fakeVerificationRepository) UpdateSMSSendState(_ context.Context, _ uint64, status string, _ *time.Time, _, _ string) error {
	f.status = status
	return nil
}

func (f *fakeVerificationRepository) CreateEmailSendPending(context.Context, *authmodel.VerificationCode, *authmodel.EmailSendLog) error {
	return nil
}

func (f *fakeVerificationRepository) FindLatestByScope(context.Context, string, time.Time) (*authmodel.VerificationCode, error) {
	return nil, errors.New("未找到记录")
}

func (f *fakeVerificationRepository) FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (f *fakeVerificationRepository) FinalizeEmailSend(context.Context, uint64, string, *time.Time, *authmodel.EmailSendLog) error {
	return nil
}

type fakeSMSRepository struct {
	binding *smsmodel.SceneBinding
	logs    []*smsmodel.SendLog
}

func (f *fakeSMSRepository) FindActiveBinding(_ context.Context, _ string) (*smsmodel.SceneBinding, error) {
	return f.binding, nil
}

func (f *fakeSMSRepository) CreateSendLog(_ context.Context, log *smsmodel.SendLog) error {
	f.logs = append(f.logs, log)
	return nil
}

func TestPhoneCodeAcceptedTransitionsPendingToAccepted(t *testing.T) {
	verificationRepo := &fakeVerificationRepository{}
	mock := smssender.NewMockSender(smssender.Result{ProviderRequestID: "provider-request", ProviderCode: "OK"}, nil)
	dispatcher := newTestDispatcher(mock)
	svc := NewVerificationService(verificationRepo)
	svc.SetSMSDispatcher(dispatcher)

	result, err := svc.SendDetailed(context.Background(), "phone", "phone-test-value", "register")
	if err != nil {
		t.Fatalf("手机号验证码模拟发送失败: %v", err)
	}
	if result.Code != "" {
		t.Fatal("手机号发送接口不得向上层返回明文验证码")
	}
	if !result.Sent || result.ExpiresIn != 600 || result.BusinessRequestID == "" || result.SubmitStatus != "accepted" {
		t.Fatalf("手机发码安全响应字段不完整: %#v", result)
	}
	if verificationRepo.created == nil || verificationRepo.created.SendStatus != "pending" {
		t.Fatal("手机号验证码必须先以 pending 状态保存")
	}
	if verificationRepo.status != "accepted" || mock.CallCount() != 1 {
		t.Fatalf("受理后必须转为 accepted，当前状态 %s", verificationRepo.status)
	}
}

func TestPhoneCodeFailureRemainsUnusable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "供应商拒绝", err: smssender.NewProviderError(smssender.ErrorKindRejected, "REJECTED", errors.New("raw"))},
		{name: "超时", err: smssender.NewProviderError(smssender.ErrorKindTimeout, "", context.DeadlineExceeded)},
		{name: "网络异常", err: smssender.NewProviderError(smssender.ErrorKindNetwork, "", errors.New("network"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeVerificationRepository{}
			svc := NewVerificationService(repo)
			svc.SetSMSDispatcher(newTestDispatcher(smssender.NewMockSender(smssender.Result{}, tc.err)))

			if _, err := svc.Send(context.Background(), "phone", "phone-test-value", "register"); !errors.Is(err, ErrSMSSendFailed) {
				t.Fatalf("发送失败应返回统一错误，实际 %v", err)
			}
			if repo.status != "failed" {
				t.Fatalf("发送失败必须转为 failed，实际 %s", repo.status)
			}
			if err := svc.Check(context.Background(), "phone", "phone-test-value", "register", "otp-test-value"); !errors.Is(err, ErrInvalidCode) {
				t.Fatal("failed 手机验证码不得通过校验")
			}
		})
	}
}

func TestDisabledSMSDoesNotCreateCodeOrCallSender(t *testing.T) {
	repo := &fakeVerificationRepository{}
	mock := smssender.NewMockSender(smssender.Result{ProviderCode: "OK"}, nil)
	cfg := testSMSConfig()
	cfg.SMSEnabled = false
	dispatcher := smsservice.NewDispatcher(cfg, &fakeSMSRepository{binding: testBinding()}, mock)
	svc := NewVerificationService(repo)
	svc.SetSMSDispatcher(dispatcher)

	if _, err := svc.Send(context.Background(), "phone", "phone-test-value", "register"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("关闭态必须返回短信不可用，实际 %v", err)
	}
	if repo.created != nil || mock.CallCount() != 0 {
		t.Fatal("关闭态不得创建验证码或调用供应商")
	}
}

func TestEmailCodeRemainsIndependentFromSMSDispatcher(t *testing.T) {
	repo := &fakeVerificationRepository{}
	svc := NewVerificationService(repo)
	svc.SetEmailSender(fakeEmailOTPSender{})

	result, err := svc.Send(context.Background(), "email", "USER@EXAMPLE.COM", "register")
	if err != nil {
		t.Fatalf("邮箱验证码原流程不应受短信开关影响: %v", err)
	}
	if len(result.Code) != 6 || !result.Sent || result.ExpiresIn != 600 {
		t.Fatal("邮箱验证码必须继续通过独立邮件 Sender 返回安全发送结果")
	}
}

type fakeEmailOTPSender struct{}

func (fakeEmailOTPSender) SendOTP(_ context.Context, _, _, _, _ string, _ int) (EmailAcceptance, uint64, error) {
	return EmailAcceptance{}, 1, nil
}

func newTestDispatcher(mock smssender.Sender) *smsservice.Dispatcher {
	return smsservice.NewDispatcher(testSMSConfig(), &fakeSMSRepository{binding: testBinding()}, mock)
}

func testSMSConfig() config.Config {
	return config.Config{
		SMSEnabled:               true,
		SMSProvider:              "aliyun",
		SMSAliyunAccessKeyID:     "test-access-key-id",
		SMSAliyunAccessKeySecret: "secret-value",
		SMSAliyunSignName:        "test-sign",
		SMSAliyunEndpoint:        "dysmsapi.aliyuncs.com",
		SMSPhoneHMACSecret:       strings.Repeat("x", 32),
	}
}

func testBinding() *smsmodel.SceneBinding {
	return &smsmodel.SceneBinding{
		ID:       1,
		Scene:    "register",
		SignName: "test-sign",
		Enabled:  true,
		Template: smsmodel.Template{ID: 1, Provider: "aliyun", TemplateCode: "SMS_TEST", ProviderAuditStatus: "approved", Content: "验证码 ${code}", LocalEnabled: true},
	}
}

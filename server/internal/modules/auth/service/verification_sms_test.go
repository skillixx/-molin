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
	binding  *smsmodel.SceneBinding
	bindings map[string]*smsmodel.SceneBinding
	logs     []*smsmodel.SendLog
}

func (f *fakeSMSRepository) FindActiveBinding(_ context.Context, scene string) (*smsmodel.SceneBinding, error) {
	if f.bindings != nil {
		return f.bindings[scene], nil
	}
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

func TestPhoneCodeFiveScenesUseIndependentDatabaseBindings(t *testing.T) {
	scenes := []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}
	smsRepo := &fakeSMSRepository{bindings: make(map[string]*smsmodel.SceneBinding, len(scenes))}
	for index, scene := range scenes {
		smsRepo.bindings[scene] = testBindingForScene(scene, "SMS_SCENE_"+string(rune('A'+index)))
	}
	mock := smssender.NewMockSender(smssender.Result{ProviderRequestID: "provider-request", ProviderCode: "OK"}, nil)
	dispatcher := smsservice.NewDispatcher(testSMSConfig(), smsRepo, mock)

	for index, scene := range scenes {
		verificationRepo := &fakeVerificationRepository{}
		svc := NewVerificationService(verificationRepo)
		svc.SetSMSDispatcher(dispatcher)
		if _, err := svc.SendDetailed(context.Background(), "phone", "phone-scene-"+scene, scene); err != nil {
			t.Fatalf("场景 %s 未能通过短信发送编排: %v", scene, err)
		}
		if verificationRepo.status != "accepted" {
			t.Fatalf("场景 %s 供应商受理后验证码状态错误: %s", scene, verificationRepo.status)
		}
		if got := smsRepo.logs[index].TemplateCode; got != "SMS_SCENE_"+string(rune('A'+index)) {
			t.Fatalf("场景 %s 未使用自身数据库模板绑定: %s", scene, got)
		}
	}
	if mock.CallCount() != len(scenes) || len(smsRepo.logs) != len(scenes) {
		t.Fatalf("五场景必须各提交一次短信: provider=%d logs=%d", mock.CallCount(), len(smsRepo.logs))
	}
}

func TestPhoneCodeFailureRemainsUnusable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "供应商拒绝", err: smssender.NewProviderError(smssender.ErrorKindRejected, "REJECTED", errors.New("raw"))},
		{name: "超时", err: smssender.NewProviderError(smssender.ErrorKindTimeout, "", context.DeadlineExceeded)},
		{name: "供应商限流", err: smssender.NewProviderError(smssender.ErrorKindRateLimit, "isv.BUSINESS_LIMIT_CONTROL", errors.New("raw"))},
		{name: "签名错误", err: smssender.NewProviderError(smssender.ErrorKindSignature, "isv.SMS_SIGNATURE_ILLEGAL", errors.New("raw"))},
		{name: "模板错误", err: smssender.NewProviderError(smssender.ErrorKindTemplate, "isv.TEMPLATE_MISSING", errors.New("raw"))},
		{name: "账户异常", err: smssender.NewProviderError(smssender.ErrorKindArrears, "isv.AMOUNT_NOT_ENOUGH", errors.New("raw"))},
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

func TestTestSceneAllowlistRejectsNonLoginWithoutSideEffects(t *testing.T) {
	verificationRepo := &fakeVerificationRepository{}
	smsRepo := &fakeSMSRepository{binding: testBinding()}
	mock := smssender.NewMockSender(smssender.Result{ProviderCode: "OK"}, nil)
	cfg := testSMSConfig()
	cfg.SMSTestMode = true
	cfg.SMSTestPhoneWhitelist = []string{"phone-test-value"}
	cfg.SMSTestSceneAllowlist = []string{"login"}
	dispatcher := smsservice.NewDispatcher(cfg, smsRepo, mock)
	guard := &countingSMSGuard{}
	svc := NewVerificationService(verificationRepo)
	svc.SetSMSDispatcher(dispatcher)
	svc.SetSMSVerificationGuard(guard)

	if _, err := svc.SendDetailed(context.Background(), "phone", "phone-test-value", "register"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("未放行场景必须返回短信不可用，实际 %v", err)
	}
	if verificationRepo.created != nil || guard.sendCalls != 0 || len(smsRepo.logs) != 0 || mock.CallCount() != 0 {
		t.Fatalf("未放行场景不得产生 OTP、限流占用、发送日志或供应商调用: otp=%t guard=%d logs=%d provider=%d",
			verificationRepo.created != nil, guard.sendCalls, len(smsRepo.logs), mock.CallCount())
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

type countingSMSGuard struct {
	sendCalls int
}

func (g *countingSMSGuard) AllowSend(context.Context, string, string) (bool, error) {
	g.sendCalls++
	return true, nil
}

func (*countingSMSGuard) AllowCheckAttempt(context.Context, string, string) (bool, error) {
	return true, nil
}

func (*countingSMSGuard) ClearCheckFailures(context.Context, string, string) error {
	return nil
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
	return testBindingForScene("register", "SMS_TEST")
}

func testBindingForScene(scene, templateCode string) *smsmodel.SceneBinding {
	return &smsmodel.SceneBinding{
		ID:       1,
		Scene:    scene,
		SignName: "test-sign",
		Enabled:  true,
		Template: smsmodel.Template{ID: 1, Provider: "aliyun", TemplateCode: templateCode, ProviderAuditStatus: "approved", Content: "验证码 ${code}", LocalEnabled: true},
	}
}

package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"molin/server/internal/config"
	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/sender"
)

type fakeRepository struct {
	bindings map[string]*model.SceneBinding
	logs     []*model.SendLog
}

func (f *fakeRepository) FindActiveBinding(_ context.Context, scene string) (*model.SceneBinding, error) {
	return f.bindings[scene], nil
}

func (f *fakeRepository) CreateSendLog(_ context.Context, log *model.SendLog) error {
	f.logs = append(f.logs, log)
	return nil
}

func TestPrepareUsesIndependentBindingForFiveScenes(t *testing.T) {
	scenes := []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}
	repo := &fakeRepository{bindings: map[string]*model.SceneBinding{}}
	for i, scene := range scenes {
		repo.bindings[scene] = fixtureBinding(scene, "SMS_TEST_"+string(rune('A'+i)))
	}
	dispatcher := NewDispatcher(enabledConfig(), repo, sender.NewMockSender(sender.Result{ProviderCode: "OK"}, nil))

	for i, scene := range scenes {
		plan, err := dispatcher.Prepare(context.Background(), scene, "phone-test-value")
		if err != nil {
			t.Fatalf("场景 %s 准备失败: %v", scene, err)
		}
		want := "SMS_TEST_" + string(rune('A'+i))
		if plan.TemplateCode != want {
			t.Fatalf("场景 %s 串用了模板，得到 %s，期望 %s", scene, plan.TemplateCode, want)
		}
	}
}

func TestSubmitPersistsOnlyMaskedPhoneAndHMAC(t *testing.T) {
	repo := &fakeRepository{bindings: map[string]*model.SceneBinding{}}
	dispatcher := NewDispatcher(enabledConfig(), repo, sender.NewMockSender(sender.Result{ProviderRequestID: "request", ProviderCode: "OK"}, nil))
	plan := PreparedSend{Scene: "register", TemplateID: 1, TemplateCode: "SMS_TEST", SignName: "test-sign", Provider: "aliyun"}

	_, err := dispatcher.Submit(context.Background(), plan, "pho-private-0000", "otp-test-value", "business-request")
	if err != nil {
		t.Fatalf("模拟提交失败: %v", err)
	}
	if len(repo.logs) != 1 {
		t.Fatalf("期望一条发送日志，实际 %d", len(repo.logs))
	}
	log := repo.logs[0]
	if log.PhoneMasked != "pho****0000" || strings.Contains(log.PhoneMasked, "pho-private-0000") {
		t.Fatalf("手机号脱敏结果错误: %s", log.PhoneMasked)
	}
	if len(log.PhoneHMAC) != 64 || strings.Contains(log.PhoneHMAC, "pho-private-0000") {
		t.Fatal("手机号 HMAC 必须是独立的 64 位十六进制标识")
	}
	metrics := dispatcher.MetricsSnapshot()
	if metrics.Accepted != 1 || metrics.Failed != 0 {
		t.Fatalf("受理指标错误: %#v", metrics)
	}
	if got := dispatcher.SMSProviderMetricValue("register", "accepted"); got != 1 {
		t.Fatalf("注册场景受理指标应为 1，实际 %d", got)
	}
	count, totalNanoseconds := dispatcher.SMSProviderDuration("register")
	if count != 1 {
		t.Fatalf("注册场景耗时指标错误: count=%d sum=%d", count, totalNanoseconds)
	}
}

func TestSubmitCountsProviderFailure(t *testing.T) {
	repo := &fakeRepository{bindings: map[string]*model.SceneBinding{}}
	dispatcher := NewDispatcher(enabledConfig(), repo, sender.NewMockSender(sender.Result{}, context.DeadlineExceeded))
	plan := PreparedSend{Scene: "register", TemplateID: 1, TemplateCode: "SMS_TEST", SignName: "test-sign", Provider: "aliyun"}
	if _, err := dispatcher.Submit(context.Background(), plan, "phone-test-value", "otp-test-value", "business-request"); err == nil {
		t.Fatal("供应商失败不得被当作受理")
	}
	metrics := dispatcher.MetricsSnapshot()
	if metrics.Accepted != 0 || metrics.Failed != 1 {
		t.Fatalf("失败指标错误: %#v", metrics)
	}
	if got := dispatcher.SMSProviderMetricValue("register", "timeout"); got != 1 {
		t.Fatalf("超时必须归入固定 timeout 类别，实际 %d", got)
	}
}

func TestSMSProviderMetricsUseOnlyFixedSafeClassifications(t *testing.T) {
	tests := []struct {
		name string
		kind sender.ErrorKind
	}{
		{name: "供应商限流", kind: sender.ErrorKindRateLimit},
		{name: "签名错误", kind: sender.ErrorKindSignature},
		{name: "模板错误", kind: sender.ErrorKindTemplate},
		{name: "账户异常", kind: sender.ErrorKindArrears},
		{name: "网络错误", kind: sender.ErrorKindNetwork},
		{name: "普通拒绝", kind: sender.ErrorKindRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := NewDispatcher(enabledConfig(), &fakeRepository{bindings: map[string]*model.SceneBinding{}}, sender.NewMockSender(
				sender.Result{}, sender.NewProviderError(test.kind, "RAW_PROVIDER_CODE", errors.New("供应商原始错误")),
			))
			plan := PreparedSend{Scene: "login", TemplateID: 1, TemplateCode: "SMS_TEST", SignName: "test-sign", Provider: "aliyun"}
			_, _ = dispatcher.SendProvider(context.Background(), plan, "phone-test-value", "otp-test-value", "business-request")
			if got := dispatcher.SMSProviderMetricValue("login", string(test.kind)); got != 1 {
				t.Fatalf("安全类别 %s 计数应为 1，实际 %d", test.kind, got)
			}
			if got := dispatcher.SMSProviderMetricValue("login", "RAW_PROVIDER_CODE"); got != 0 {
				t.Fatal("指标不得把供应商原始错误码作为标签值")
			}
		})
	}
}

func TestSMSProviderMetricsRejectUnknownSceneAndNormalizeUnknownResult(t *testing.T) {
	dispatcher := NewDispatcher(enabledConfig(), &fakeRepository{bindings: map[string]*model.SceneBinding{}}, sender.NewMockSender(sender.Result{}, nil))
	dispatcher.recordProviderMetric("custom_scene", "accepted", time.Millisecond)
	if got := dispatcher.SMSProviderMetricValue("custom_scene", "accepted"); got != 0 {
		t.Fatalf("非固定场景不得进入指标聚合，实际 %d", got)
	}

	dispatcher.recordProviderMetric("register", "raw_provider_error", time.Millisecond)
	if got := dispatcher.SMSProviderMetricValue("register", "raw_provider_error"); got != 0 {
		t.Fatalf("未知结果不得成为指标标签，实际 %d", got)
	}
	if got := dispatcher.SMSProviderMetricValue("register", "rejected"); got != 1 {
		t.Fatalf("未知结果必须收敛到 rejected，实际 %d", got)
	}
}

func TestSMSProviderMetricsConcurrentReadWrite(t *testing.T) {
	dispatcher := NewDispatcher(enabledConfig(), &fakeRepository{bindings: map[string]*model.SceneBinding{}}, sender.NewMockSender(sender.Result{}, nil))
	const writerCount = 32
	const writesPerWriter = 200
	const readerCount = 8

	var writers sync.WaitGroup
	writers.Add(writerCount)
	for writer := 0; writer < writerCount; writer++ {
		go func() {
			defer writers.Done()
			for index := 0; index < writesPerWriter; index++ {
				dispatcher.recordProviderMetric("register", "accepted", time.Millisecond)
			}
		}()
	}

	var readers sync.WaitGroup
	readers.Add(readerCount)
	for reader := 0; reader < readerCount; reader++ {
		go func() {
			defer readers.Done()
			for index := 0; index < writesPerWriter; index++ {
				_ = dispatcher.SMSProviderMetricValue("register", "accepted")
				_, _ = dispatcher.SMSProviderDuration("register")
			}
		}()
	}

	writers.Wait()
	readers.Wait()
	expected := uint64(writerCount * writesPerWriter)
	if got := dispatcher.SMSProviderMetricValue("register", "accepted"); got != expected {
		t.Fatalf("并发写入计数错误: got=%d want=%d", got, expected)
	}
	count, totalNanoseconds := dispatcher.SMSProviderDuration("register")
	if count != expected || totalNanoseconds != expected*uint64(time.Millisecond) {
		t.Fatalf("并发耗时聚合错误: count=%d sum=%d", count, totalNanoseconds)
	}
}

func TestPrepareRejectsTemplateWithExtraVariable(t *testing.T) {
	binding := fixtureBinding("register", "SMS_EXTRA")
	binding.Template.Content = "${name} 的验证码 ${code}"
	binding.Template.Variables = []string{"name", "code"}
	repo := &fakeRepository{bindings: map[string]*model.SceneBinding{"register": binding}}
	dispatcher := NewDispatcher(enabledConfig(), repo, sender.NewMockSender(sender.Result{}, nil))

	if _, err := dispatcher.Prepare(context.Background(), "register", "phone-test-value"); !errors.Is(err, ErrSceneNotBound) {
		t.Fatalf("含额外变量的模板不得进入发送链路: %v", err)
	}
}

func TestPrepareRejectsSceneOutsideTestAllowlistBeforeRepositoryLookup(t *testing.T) {
	repo := &countingRepository{fakeRepository: fakeRepository{bindings: map[string]*model.SceneBinding{
		"register": fixtureBinding("register", "SMS_REGISTER"),
	}}}
	cfg := enabledConfig()
	cfg.SMSTestMode = true
	cfg.SMSTestPhoneWhitelist = []string{"phone-test-value"}
	cfg.SMSTestSceneAllowlist = []string{"login"}
	dispatcher := NewDispatcher(cfg, repo, sender.NewMockSender(sender.Result{}, nil))

	if _, err := dispatcher.Prepare(context.Background(), "register", "phone-test-value"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("测试模式未放行的场景必须按短信不可用拒绝，实际 %v", err)
	}
	if repo.findCalls != 0 {
		t.Fatalf("未放行场景不得查询模板绑定，实际查询 %d 次", repo.findCalls)
	}
}

func TestPrepareAllowsLoginInsideTestAllowlist(t *testing.T) {
	repo := &fakeRepository{bindings: map[string]*model.SceneBinding{
		"login": fixtureBinding("login", "SMS_LOGIN"),
	}}
	cfg := enabledConfig()
	cfg.SMSTestMode = true
	cfg.SMSTestPhoneWhitelist = []string{"phone-test-value"}
	cfg.SMSTestSceneAllowlist = []string{"login"}
	dispatcher := NewDispatcher(cfg, repo, sender.NewMockSender(sender.Result{}, nil))

	plan, err := dispatcher.Prepare(context.Background(), "login", "phone-test-value")
	if err != nil {
		t.Fatalf("测试模式已放行的登录场景应可准备发送: %v", err)
	}
	if plan.Scene != "login" {
		t.Fatalf("准备结果场景错误，实际 %s", plan.Scene)
	}
}

func TestPrepareRejectsEveryUnavailableBindingState(t *testing.T) {
	base := fixtureBinding("register", "SMS_VALID")
	tests := []struct {
		name    string
		scene   string
		binding *model.SceneBinding
	}{
		{name: "场景未绑定", scene: "register", binding: nil},
		{name: "场景不在固定集合", scene: "unknown", binding: base},
		{name: "绑定场景不一致", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Scene = "login"; return &v }()},
		{name: "场景已停用", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Enabled = false; return &v }()},
		{name: "签名不一致", scene: "register", binding: func() *model.SceneBinding { v := *base; v.SignName = "other-sign"; return &v }()},
		{name: "供应商不一致", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Template.Provider = "other"; return &v }()},
		{name: "模板未审核", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Template.ProviderAuditStatus = "pending"; return &v }()},
		{name: "模板本地停用", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Template.LocalEnabled = false; return &v }()},
		{name: "模板编码为空", scene: "register", binding: func() *model.SceneBinding { v := *base; v.Template.TemplateCode = ""; return &v }()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepository{bindings: map[string]*model.SceneBinding{test.scene: test.binding}}
			dispatcher := NewDispatcher(enabledConfig(), repo, sender.NewMockSender(sender.Result{}, nil))
			if _, err := dispatcher.Prepare(context.Background(), test.scene, "phone-test-value"); !errors.Is(err, ErrSceneNotBound) {
				t.Fatalf("不可用绑定必须在调用供应商前失败关闭: %v", err)
			}
		})
	}
}

func fixtureBinding(scene, templateCode string) *model.SceneBinding {
	return &model.SceneBinding{
		ID:       1,
		Scene:    scene,
		Enabled:  true,
		SignName: "test-sign",
		Template: model.Template{ID: 1, Provider: "aliyun", TemplateCode: templateCode, ProviderAuditStatus: "approved", Content: "验证码 ${code}", LocalEnabled: true},
	}
}

type countingRepository struct {
	fakeRepository
	findCalls int
}

func (r *countingRepository) FindActiveBinding(ctx context.Context, scene string) (*model.SceneBinding, error) {
	r.findCalls++
	return r.fakeRepository.FindActiveBinding(ctx, scene)
}

func enabledConfig() config.Config {
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

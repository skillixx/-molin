package service

import (
	"context"
	"errors"
	"strings"
	"testing"

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

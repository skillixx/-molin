package video

import (
	"errors"
	"testing"
)

// 默认值直接来自G0/G6合同及本轮范围内确认的Key/Model轴，不由被测函数反算预期。
func TestVideoG7CapacityPolicyDefaults(t *testing.T) {
	want := VideoCapacityLimits{
		Queued:  VideoQueuedCapacityLimits{User: 2, Project: 10, APIKey: 2, Model: 100, Global: 100},
		Running: VideoRunningCapacityLimits{User: 1, Project: 2, APIKey: 1, Model: 2, Provider: 2},
	}
	limits := DefaultVideoCapacityLimits()
	if limits != want {
		t.Fatal("关闭态默认容量必须等于冻结值")
	}
	policy, err := NewVideoCapacityPolicy(limits)
	if err != nil {
		t.Fatalf("完整合法策略应可构造: %v", err)
	}
	got, err := policy.Limits()
	if err != nil || got != want {
		t.Fatalf("策略必须保留已确认的全部维度: %v", err)
	}
	hash, err := policy.Fingerprint()
	// 已知向量来自UTF-8文本video-capacity-v1|2|10|2|100|100|1|2|1|2|2，无末尾换行。
	if err != nil || hash != "1d489742b370cb9cf8f4a82dca5051ed04f3afdb73b398ac5fbf7c92d1e29734" {
		t.Fatalf("策略版本与维度顺序必须匹配固定向量: %v", err)
	}
	limits.Queued.Global = 1
	got.Queued.Global = 1
	unchanged, err := policy.Limits()
	if err != nil || unchanged != want || DefaultVideoCapacityLimits() != want {
		t.Fatal("输入和输出副本不能改写已校验策略或全局默认值")
	}
	second, err := NewVideoCapacityPolicy(want)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := second.Fingerprint()
	if err != nil || secondHash != hash {
		t.Fatal("不同实例相同配置必须得到同一策略指纹")
	}
	tightened, err := NewVideoCapacityPolicy(limits)
	if err != nil {
		t.Fatalf("关闭态允许收紧: %v", err)
	}
	tightenedHash, err := tightened.Fingerprint()
	if err != nil || tightenedHash == hash {
		t.Fatal("收紧后的策略必须能与旧版本区分")
	}
}

func TestVideoG7CapacityPolicyRejectsWidening(t *testing.T) {
	axes := []struct {
		name  string
		field func(*VideoCapacityLimits) *uint32
	}{
		{"queued_user", func(v *VideoCapacityLimits) *uint32 { return &v.Queued.User }},
		{"queued_project", func(v *VideoCapacityLimits) *uint32 { return &v.Queued.Project }},
		{"queued_key", func(v *VideoCapacityLimits) *uint32 { return &v.Queued.APIKey }},
		{"queued_model", func(v *VideoCapacityLimits) *uint32 { return &v.Queued.Model }},
		{"queued_global", func(v *VideoCapacityLimits) *uint32 { return &v.Queued.Global }},
		{"running_user", func(v *VideoCapacityLimits) *uint32 { return &v.Running.User }},
		{"running_project", func(v *VideoCapacityLimits) *uint32 { return &v.Running.Project }},
		{"running_key", func(v *VideoCapacityLimits) *uint32 { return &v.Running.APIKey }},
		{"running_model", func(v *VideoCapacityLimits) *uint32 { return &v.Running.Model }},
		{"running_provider", func(v *VideoCapacityLimits) *uint32 { return &v.Running.Provider }},
	}
	for _, axis := range axes {
		t.Run(axis.name, func(t *testing.T) {
			for _, kind := range []string{"zero", "above_ceiling"} {
				bad := DefaultVideoCapacityLimits()
				field := axis.field(&bad)
				if kind == "zero" {
					*field = 0
				} else {
					*field++
				}
				if p, err := NewVideoCapacityPolicy(bad); p != nil || !errors.Is(err, ErrVideoCapacityPolicy) {
					t.Fatalf("%s必须失败关闭且无部分策略: %v", kind, err)
				}
			}
			lower := DefaultVideoCapacityLimits()
			originalValue := *axis.field(&lower)
			*axis.field(&lower) = 1
			lowerPolicy, err := NewVideoCapacityPolicy(lower)
			if err != nil {
				t.Fatalf("合法收紧不能拒绝: %v", err)
			}
			basePolicy, err := NewVideoCapacityPolicy(DefaultVideoCapacityLimits())
			if err != nil {
				t.Fatal(err)
			}
			baseHash, err := basePolicy.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			lowerHash, err := lowerPolicy.Fingerprint()
			if err != nil || ((lowerHash == baseHash) != (originalValue == 1)) {
				t.Fatal("每个可收紧维度都必须进入策略指纹")
			}
		})
	}
	for _, empty := range []*VideoCapacityPolicy{nil, {}} {
		if _, err := empty.Limits(); !errors.Is(err, ErrVideoCapacityPolicy) {
			t.Fatal("空策略不能返回可用默认值")
		}
		if _, err := empty.Fingerprint(); !errors.Is(err, ErrVideoCapacityPolicy) {
			t.Fatal("空策略不能产生有效指纹")
		}
	}
}

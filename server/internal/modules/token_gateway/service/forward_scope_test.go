package service

import (
	"context"
	"errors"
	"testing"
)

// fakeScopeResolver 内存桩，模拟 auth.APIKeyService.ModelScopeByID。
type fakeScopeResolver struct {
	scopes map[uint64][]string // api_key_id → model_scope
	valid  map[uint64]bool     // api_key_id → 是否有效（false=已吊销/不存在）
}

func (f *fakeScopeResolver) ModelScopeByID(_ context.Context, apiKeyID uint64) ([]string, bool) {
	if !f.valid[apiKeyID] {
		return nil, false
	}
	return f.scopes[apiKeyID], true
}

// TestCheckModelScope 覆盖 S2-丁4b sk model_scope 越界校验各分支。
func TestCheckModelScope(t *testing.T) {
	resolver := &fakeScopeResolver{
		scopes: map[uint64][]string{
			1: {"gpt-4o", "claude-3-5-sonnet"}, // 限定白名单
			2: {},                              // 空=不限
		},
		valid: map[uint64]bool{1: true, 2: true, 3: false},
	}

	cases := []struct {
		name     string
		svc      *ForwardService
		apiKeyID uint64
		model    string
		wantErr  error
	}{
		{
			name:     "登录态调用（apiKeyID=0）跳过校验",
			svc:      &ForwardService{scopeResolver: resolver},
			apiKeyID: 0,
			model:    "any-model",
			wantErr:  nil,
		},
		{
			name:     "scopeResolver 为 nil 时退化为不校验",
			svc:      &ForwardService{scopeResolver: nil},
			apiKeyID: 1,
			model:    "not-in-scope",
			wantErr:  nil,
		},
		{
			name:     "sk 命中白名单内模型 → 放行",
			svc:      &ForwardService{scopeResolver: resolver},
			apiKeyID: 1,
			model:    "gpt-4o",
			wantErr:  nil,
		},
		{
			name:     "sk 请求白名单外模型 → 40300（ErrModelNotInScope）",
			svc:      &ForwardService{scopeResolver: resolver},
			apiKeyID: 1,
			model:    "gemini-1.5-pro",
			wantErr:  ErrModelNotInScope,
		},
		{
			name:     "sk model_scope 为空（不限）→ 放行任意模型",
			svc:      &ForwardService{scopeResolver: resolver},
			apiKeyID: 2,
			model:    "any-model",
			wantErr:  nil,
		},
		{
			name:     "sk 已失效（ok=false）→ 按未授权拒绝",
			svc:      &ForwardService{scopeResolver: resolver},
			apiKeyID: 3,
			model:    "gpt-4o",
			wantErr:  ErrModelNotInScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.svc.checkModelScope(context.Background(), tc.apiKeyID, tc.model)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("checkModelScope() = %v, 期望 %v", err, tc.wantErr)
			}
		})
	}
}

package image

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrModerationRejected = errors.New("图片内容安全拒绝")
	ErrModerationFailed   = errors.New("图片审核服务不可用")
)

type ModerationDecision string

const (
	ModerationAllowed  ModerationDecision = "allowed"
	ModerationRejected ModerationDecision = "rejected"
)

type ImageModerationAdapter interface {
	ModeratePrompt(ctx context.Context, prompt string) (ModerationDecision, error)
	ModerateImage(ctx context.Context, image NormalizedImage) (ModerationDecision, error)
}

type FakeModerationMode string

const (
	FakeModerationAllow        FakeModerationMode = "allow"
	FakeModerationRejectPrompt FakeModerationMode = "reject_prompt"
	FakeModerationRejectImage  FakeModerationMode = "reject_image"
	FakeModerationErrorPrompt  FakeModerationMode = "error_prompt"
	FakeModerationErrorImage   FakeModerationMode = "error_image"
)

// FakeModerationAdapter 只返回固定审核结果，禁止把Prompt或图片正文写入日志和持久化。
type FakeModerationAdapter struct {
	mu          sync.Mutex
	mode        FakeModerationMode
	promptCalls int
	imageCalls  int
}

func NewFakeModerationAdapter(mode FakeModerationMode) *FakeModerationAdapter {
	return &FakeModerationAdapter{mode: mode}
}

func (a *FakeModerationAdapter) ModeratePrompt(ctx context.Context, _ string) (ModerationDecision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.promptCalls++
	mode := a.mode
	a.mu.Unlock()
	switch mode {
	case FakeModerationRejectPrompt:
		return ModerationRejected, nil
	case FakeModerationErrorPrompt:
		return "", ErrModerationFailed
	default:
		return ModerationAllowed, nil
	}
}

func (a *FakeModerationAdapter) ModerateImage(ctx context.Context, _ NormalizedImage) (ModerationDecision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.imageCalls++
	mode := a.mode
	a.mu.Unlock()
	switch mode {
	case FakeModerationRejectImage:
		return ModerationRejected, nil
	case FakeModerationErrorImage:
		return "", ErrModerationFailed
	default:
		return ModerationAllowed, nil
	}
}

func (a *FakeModerationAdapter) Calls() (prompt, image int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.promptCalls, a.imageCalls
}

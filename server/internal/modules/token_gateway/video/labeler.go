package video

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrVideoLabelFailed = errors.New("视频AI标识失败")

type LabelStatus string

const (
	LabelPending LabelStatus = "pending"
	LabelApplied LabelStatus = "applied"
	LabelFailed  LabelStatus = "failed"
)

type LabelRequest struct {
	TaskID  string
	AssetID string
	SHA256  string
}

type LabelResult struct {
	Version        string
	ExplicitStatus LabelStatus
	ImplicitStatus LabelStatus
}

type VideoAILabeler interface {
	Apply(ctx context.Context, request LabelRequest) (LabelResult, error)
}

type FakeVideoLabelMode string

const (
	FakeVideoLabelSuccess         FakeVideoLabelMode = "success"
	FakeVideoLabelExplicitFailure FakeVideoLabelMode = "explicit_failure"
	FakeVideoLabelImplicitFailure FakeVideoLabelMode = "implicit_failure"
)

type FakeVideoAILabeler struct {
	mu      sync.Mutex
	mode    FakeVideoLabelMode
	version string
	calls   int
}

func NewFakeVideoAILabeler(mode FakeVideoLabelMode, version string) *FakeVideoAILabeler {
	return &FakeVideoAILabeler{mode: mode, version: version}
}

// Apply 模拟显式与隐式双标识；任一失败都返回失败状态，调用方不得交付资产。
func (l *FakeVideoAILabeler) Apply(ctx context.Context, request LabelRequest) (LabelResult, error) {
	if err := ctx.Err(); err != nil {
		return LabelResult{}, err
	}
	if l == nil {
		return LabelResult{}, ErrVideoLabelFailed
	}
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	result := LabelResult{Version: l.version, ExplicitStatus: LabelPending, ImplicitStatus: LabelPending}
	if strings.TrimSpace(l.version) == "" || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.AssetID) == "" || strings.TrimSpace(request.SHA256) == "" {
		return result, ErrVideoLabelFailed
	}
	if l.mode == FakeVideoLabelExplicitFailure {
		result.ExplicitStatus = LabelFailed
		return result, ErrVideoLabelFailed
	}
	result.ExplicitStatus = LabelApplied
	if l.mode == FakeVideoLabelImplicitFailure {
		result.ImplicitStatus = LabelFailed
		return result, ErrVideoLabelFailed
	}
	result.ImplicitStatus = LabelApplied
	return result, nil
}

func (l *FakeVideoAILabeler) Calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

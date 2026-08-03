package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type memorySafetyRepository struct {
	policy    *model.AISafetyPolicyVersion
	events    []model.AISafetyEvent
	suspended bool
	err       error
	marked    map[string]string
}

func (r *memorySafetyRepository) ActiveSafetyPolicy(context.Context) (*model.AISafetyPolicyVersion, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.policy == nil {
		return nil, repository.ErrSafetyPolicyUnavailable
	}
	return r.policy, nil
}

func (r *memorySafetyRepository) RecordSafetyEvent(_ context.Context, event *model.AISafetyEvent) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, *event)
	return nil
}

func (r *memorySafetyRepository) IsSubjectSuspended(context.Context, uint64, uint64) (bool, error) {
	return r.suspended, r.err
}

func (r *memorySafetyRepository) MarkModeration(_ context.Context, requestID, status string) error {
	if r.marked == nil {
		r.marked = map[string]string{}
	}
	r.marked[requestID] = status
	return r.err
}

func testSafetyPolicy(t *testing.T) *model.AISafetyPolicyVersion {
	t.Helper()
	rules, err := json.Marshal([]safetyRule{{Code: "gambling-001", Category: "gambling", Keywords: []string{"网络赌博"}}})
	if err != nil {
		t.Fatal(err)
	}
	return &model.AISafetyPolicyVersion{ID: 9, VersionNo: 3, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal, RulesJSON: rules}
}

func TestSafetyServiceRejectsNormalizedInputWithoutPersistingRawContent(t *testing.T) {
	repo := &memorySafetyRepository{policy: testSafetyPolicy(t)}
	svc := NewSafetyService(repo, "0123456789abcdef0123456789abcdef")
	body := map[string]interface{}{"messages": []interface{}{map[string]interface{}{"role": "user", "content": "请介绍网 络-赌 博平台"}}}
	decision, err := svc.ModerateInput(context.Background(), SafetySubject{RequestID: "req-safe-1", UserID: 1, ProjectID: 2, APIKeyID: 3}, body)
	if !errors.Is(err, ErrContentPolicyViolation) || decision == nil || decision.Allowed {
		t.Fatalf("应稳定拒绝违规输入，decision=%+v err=%v", decision, err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("应写入一条最小化违规事件，实际 %d", len(repo.events))
	}
	event := repo.events[0]
	if event.Category != "gambling" || event.RuleCode != "gambling-001" || event.ContentDigest == "" || len(event.ContentDigest) != 64 {
		t.Fatalf("违规事件字段不完整: %+v", event)
	}
	if event.ContentDigest == "请介绍网络赌博平台" {
		t.Fatal("违规事件不得保存原始内容")
	}
}

func TestSafetyServiceFailsClosedWhenPolicyOrSubjectCheckUnavailable(t *testing.T) {
	svc := NewSafetyService(&memorySafetyRepository{err: errors.New("db down")}, "0123456789abcdef0123456789abcdef")
	_, err := svc.ModerateInput(context.Background(), SafetySubject{RequestID: "req-safe-2", UserID: 1, ProjectID: 2, APIKeyID: 3}, map[string]interface{}{"messages": []interface{}{}})
	if !errors.Is(err, ErrModerationUnavailable) {
		t.Fatalf("策略依赖异常必须 fail-closed，实际 %v", err)
	}

	svc = NewSafetyService(&memorySafetyRepository{policy: testSafetyPolicy(t), suspended: true}, "0123456789abcdef0123456789abcdef")
	_, err = svc.ModerateInput(context.Background(), SafetySubject{RequestID: "req-safe-3", UserID: 1, ProjectID: 2, APIKeyID: 3}, map[string]interface{}{"messages": []interface{}{}})
	if !errors.Is(err, ErrSafetySubjectSuspended) {
		t.Fatalf("已暂停主体必须在上游前拒绝，实际 %v", err)
	}
}

func TestSafetyServiceBlocksOutputAndUsesStableRefusal(t *testing.T) {
	repo := &memorySafetyRepository{policy: testSafetyPolicy(t)}
	svc := NewSafetyService(repo, "0123456789abcdef0123456789abcdef")
	decision, err := svc.ModerateOutput(context.Background(), SafetySubject{RequestID: "req-safe-4", UserID: 1, ProjectID: 2, APIKeyID: 3}, "这是网络赌博推广")
	if !errors.Is(err, ErrContentPolicyViolation) || decision.RefusalMessage != DefaultSafetyRefusal {
		t.Fatalf("输出违规应使用稳定拒绝文案，decision=%+v err=%v", decision, err)
	}
	if repo.events[0].Direction != "output" || repo.events[0].Action != "block_output" {
		t.Fatalf("输出违规事件不正确: %+v", repo.events[0])
	}
}

func TestSafetyResponseModeratesAllPublicStrings(t *testing.T) {
	jsonTopLevel := []byte(`{"id":"网络赌博推广","choices":[{"message":{"content":"正常回复"}}]}`)
	if text := extractJSONResponseText(jsonTopLevel); !strings.Contains(text, "网络赌博推广") {
		t.Fatalf("JSON 最终公共载荷的顶层字符串必须进入输出审核: %q", text)
	}
	sseTopLevel := []byte(`data: {"system_fingerprint":"网络赌博推广","choices":[{"delta":{"content":"正常回复"}}]}`)
	if text := extractSSEText(sseTopLevel); !strings.Contains(text, "网络赌博推广") {
		t.Fatalf("SSE 最终公共载荷的顶层字符串必须进入输出审核: %q", text)
	}
	if text := extractSSEText([]byte("event: 网络赌博推广")); !strings.Contains(text, "网络赌博推广") {
		t.Fatalf("未来开放的 SSE 非 data 文本行也必须进入输出审核: %q", text)
	}
}

func TestSafetyServiceModeratesToolDefinitionsAndToolCallArguments(t *testing.T) {
	repo := &memorySafetyRepository{policy: testSafetyPolicy(t)}
	svc := NewSafetyService(repo, "0123456789abcdef0123456789abcdef")
	body := map[string]interface{}{
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "正常问题"}},
		"tools":    []interface{}{map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "search", "description": "用于网络赌博推广"}}},
	}
	if _, err := svc.ModerateInput(context.Background(), SafetySubject{RequestID: "req-tool-input", UserID: 1, ProjectID: 2, APIKeyID: 3}, body); !errors.Is(err, ErrContentPolicyViolation) {
		t.Fatalf("工具定义文字必须参与输入审核: %v", err)
	}
	legacyBody := map[string]interface{}{
		"model":    "safe-model",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "正常问题"}},
		"functions": []interface{}{map[string]interface{}{
			"name": "legacy_search", "description": "用于网络赌博推广",
		}},
	}
	if _, err := svc.ModerateInput(context.Background(), SafetySubject{RequestID: "req-legacy-function", UserID: 1, ProjectID: 2, APIKeyID: 3}, legacyBody); !errors.Is(err, ErrContentPolicyViolation) {
		t.Fatalf("所有实际透传字段都必须参与输入审核: %v", err)
	}
	jsonOutput := []byte(`{"choices":[{"message":{"content":"正常回复","tool_calls":[{"function":{"name":"search","arguments":"{\"query\":\"网络赌博推广\"}"}}]}}]}`)
	if text := extractJSONResponseText(jsonOutput); !strings.Contains(text, "网络赌博推广") {
		t.Fatalf("JSON tool_calls 参数必须参与输出审核: %s", text)
	}
	sse := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"网络赌博推广"}}]}}]}`)
	if text := extractSSEText(sse); !strings.Contains(text, "网络赌博推广") {
		t.Fatalf("SSE delta.tool_calls 参数必须参与输出审核: %s", text)
	}
}

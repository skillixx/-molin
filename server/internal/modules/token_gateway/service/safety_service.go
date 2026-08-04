package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/pkg/idgen"
)

const (
	DefaultSafetyRefusal    = "请求内容违反中国大陆相关法律法规或平台安全规范，无法继续处理。"
	maxSafetyKeywordRunes   = 256
	streamSafetyOverlapSize = maxSafetyKeywordRunes - 1
)

var (
	ErrContentPolicyViolation = errors.New("内容违反安全策略")
	ErrModerationUnavailable  = errors.New("内容安全服务不可用")
	ErrSafetySubjectSuspended = errors.New("主体已被内容安全策略暂停")
)

type safetyRepository interface {
	ActiveSafetyPolicy(ctx context.Context) (*model.AISafetyPolicyVersion, error)
	RecordSafetyEvent(ctx context.Context, event *model.AISafetyEvent) error
	IsSubjectSuspended(ctx context.Context, userID, apiKeyID uint64) (bool, error)
	MarkModeration(ctx context.Context, requestID, status string) error
}

type safetyRule struct {
	Code     string   `json:"code"`
	Category string   `json:"category"`
	Keywords []string `json:"keywords"`
}

type SafetySubject struct {
	RequestID        string
	UserID           uint64
	ProjectID        uint64
	APIKeyID         uint64
	LogicalModelCode string
}

type SafetyDecision struct {
	Allowed        bool
	Category       string
	RuleCode       string
	PolicyVersion  uint64
	PolicyID       uint64
	RefusalMessage string
}

// SafetyService 执行版本化文字规则审核，只把 HMAC 摘要和命中元数据写入数据库。
type SafetyService struct {
	repo       safetyRepository
	digestKey  []byte
	policyLoad func(context.Context) (*model.AISafetyPolicyVersion, error)
}

func NewSafetyService(repo safetyRepository, digestSecret string) *SafetyService {
	service := &SafetyService{repo: repo, digestKey: []byte(digestSecret)}
	if repo != nil {
		service.policyLoad = repo.ActiveSafetyPolicy
	}
	return service
}

func (s *SafetyService) ModerateInput(ctx context.Context, subject SafetySubject, body map[string]interface{}) (*SafetyDecision, error) {
	if s == nil || s.repo == nil || s.policyLoad == nil || len(s.digestKey) < 16 {
		return nil, ErrModerationUnavailable
	}
	suspended, err := s.repo.IsSubjectSuspended(ctx, subject.UserID, subject.APIKeyID)
	if err != nil {
		return nil, ErrModerationUnavailable
	}
	if suspended {
		return nil, ErrSafetySubjectSuspended
	}
	return s.moderate(ctx, subject, "input", extractRequestText(body))
}

func (s *SafetyService) ModerateOutput(ctx context.Context, subject SafetySubject, text string) (*SafetyDecision, error) {
	if s == nil || s.repo == nil || s.policyLoad == nil || len(s.digestKey) < 16 {
		return nil, ErrModerationUnavailable
	}
	return s.moderate(ctx, subject, "output", text)
}

func (s *SafetyService) moderate(ctx context.Context, subject SafetySubject, direction, content string) (*SafetyDecision, error) {
	policy, err := s.policyLoad(ctx)
	if err != nil || policy == nil || policy.Status != model.AISafetyPolicyActive {
		return nil, ErrModerationUnavailable
	}
	decision := &SafetyDecision{Allowed: true, PolicyVersion: policy.VersionNo, PolicyID: policy.ID, RefusalMessage: DefaultSafetyRefusal}
	var rules []safetyRule
	if err := json.Unmarshal(policy.RulesJSON, &rules); err != nil || len(rules) == 0 {
		return nil, ErrModerationUnavailable
	}
	normalized := normalizeModerationText(content)
	for _, rule := range rules {
		if strings.TrimSpace(rule.Code) == "" || strings.TrimSpace(rule.Category) == "" {
			return nil, ErrModerationUnavailable
		}
		for _, keyword := range rule.Keywords {
			candidate := normalizeModerationText(keyword)
			if candidate == "" || !strings.Contains(normalized, candidate) {
				continue
			}
			decision.Allowed = false
			decision.Category = rule.Category
			decision.RuleCode = rule.Code
			action := "reject"
			if direction == "output" {
				action = "block_output"
			}
			event := &model.AISafetyEvent{
				EventID: idgen.NewRequestID(), RequestID: subject.RequestID, UserID: subject.UserID,
				ProjectID: subject.ProjectID, APIKeyID: subject.APIKeyID, Direction: direction,
				Category: rule.Category, RuleCode: rule.Code, PolicyVersionID: policy.ID,
				ContentDigest: s.digest(normalized), Action: action, Result: "blocked",
			}
			if err := s.repo.RecordSafetyEvent(ctx, event); err != nil {
				return nil, ErrModerationUnavailable
			}
			return decision, ErrContentPolicyViolation
		}
	}
	return decision, nil
}

func (s *SafetyService) MarkRequest(ctx context.Context, requestID, status string) error {
	if s == nil || s.repo == nil {
		return ErrModerationUnavailable
	}
	return s.repo.MarkModeration(ctx, requestID, status)
}

func (s *SafetyService) digest(content string) string {
	mac := hmac.New(sha256.New, s.digestKey)
	_, _ = mac.Write([]byte(content))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeModerationText(value string) string {
	normalized := strings.ToLower(norm.NFKC.String(value))
	return strings.Map(func(char rune) rune {
		// 空白、标点、格式字符和 Unicode 默认可忽略字符都不参与匹配，防止通过斜杠、零宽连接符或变体选择符拆分关键词。
		if unicode.IsSpace(char) || unicode.IsPunct(char) || unicode.IsMark(char) || unicode.Is(unicode.Cf, char) ||
			unicode.Is(unicode.Other_Default_Ignorable_Code_Point, char) || unicode.Is(unicode.Variation_Selector, char) {
			return -1
		}
		return char
	}, normalized)
}

func extractRequestText(body map[string]interface{}) string {
	var builder strings.Builder
	keys := make([]string, 0, len(body))
	for key := range body {
		// model 是平台解析的逻辑标识，不属于发送给模型理解或执行的用户内容。
		if key != "model" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		appendModerationStrings(&builder, body[key])
	}
	return builder.String()
}

func extractJSONResponseText(body []byte) string {
	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	var builder strings.Builder
	// 驱动已经移除内部字段；这里审核最终会返回给客户端的完整公共载荷，禁止顶层字符串绕过。
	appendModerationStrings(&builder, payload)
	return builder.String()
}

func extractSSEText(line []byte) string {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		// 当前驱动会丢弃 event/id/comment，仅保留空分隔行；若后续驱动开放文本行，也必须纳入审核。
		return trimmed
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return ""
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return ""
	}
	var builder strings.Builder
	appendModerationStrings(&builder, payload)
	return builder.String()
}

// extractSSEContinuityText 只提取会跨 chunk 增量拼接的生成字段。
// model、id、usage 等每段重复元数据仍由 extractSSEText 审核，但不能插入生成文字之间破坏连续匹配。
func extractSSEContinuityText(line []byte) string {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return trimmed
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return ""
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return ""
	}
	var builder strings.Builder
	if choices, ok := payload["choices"].([]interface{}); ok {
		for _, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]interface{})
			if !ok {
				continue
			}
			// logprobs 会随 SSE 分块公开实际 token 及候选 token；这些字符串同样必须进入连续视图，
			// 否则违规词可以拆在多个 logprobs chunk 中绕过单段审核后泄漏给客户端。
			for _, key := range []string{"delta", "message", "text", "logprobs"} {
				appendModerationStrings(&builder, choice[key])
			}
		}
	}
	for _, key := range []string{"content", "output", "output_text", "response", "text"} {
		appendModerationStrings(&builder, payload[key])
	}
	return builder.String()
}

// appendModerationStrings 递归提取实际透传字段中的所有文字，包括 legacy functions 和工具调用参数。
func appendModerationStrings(builder *strings.Builder, value interface{}) {
	switch typed := value.(type) {
	case string:
		builder.WriteString(typed)
		builder.WriteByte('\n')
	case []interface{}:
		for _, item := range typed {
			appendModerationStrings(builder, item)
		}
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			appendModerationStrings(builder, typed[key])
		}
	}
}

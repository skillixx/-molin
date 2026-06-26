package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	convcache "molin/server/internal/modules/conversation/cache"
	"molin/server/internal/modules/conversation/model"
	"molin/server/internal/modules/conversation/repository"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	workbenchsvc "molin/server/internal/modules/workbench/service"
	"molin/server/pkg/idgen"
)

// 压缩与上下文预算参数（启发式，token 以 rune 数粗估；可后续提升为环境变量）。
const (
	compressThresholdTokens = 6000  // 水位线之后未压缩消息累计 token 超过即触发压缩
	keepRecentMessages      = 8     // 压缩时保留最近 N 条原文不进摘要
	contextBudgetTokens     = 12000 // 单次上下文中原文消息的 token 预算上限（安全网，正常由压缩兜住）
	compressTimeout         = 60 * time.Second
	summarySystemPrompt     = "你是对话记忆压缩器。请把下列对话历史压缩为简洁的中文要点摘要：保留关键事实、用户偏好与身份信息、已达成的结论、待办或未完成事项、重要的上下文设定；省略寒暄与冗余复述。若已有摘要，请将其与新增对话融合为一份更新后的摘要，不要丢失原摘要中的关键信息。直接输出摘要正文，不要任何前后缀。"
)

// ErrConversationNotFound 透出仓库层的会话不存在错误，供 handler 映射 404。
var ErrConversationNotFound = repository.ErrConversationNotFound

// Orchestrator 编排引擎抽象（由 workbench.ChatService 实现）：复用其模型路由/工具/计费/可见性。
type Orchestrator interface {
	// RunWithContext 用给定上下文消息执行一次（可带工具）对话，返回最终 assistant 文本。
	// 返回 error 表示尚未向 w 写出任何内容（handler 据此映射 HTTP 错误码）；一旦开始写出即返回 nil。
	RunWithContext(ctx context.Context, w http.ResponseWriter, in workbenchsvc.RunContextInput) (string, error)
	// EnsureAgentVisible 校验 Agent 对用户可见（新建 Agent 会话时前置校验）。
	EnsureAgentVisible(ctx context.Context, agentID, userID uint64) error
}

// Summarizer 摘要压缩所需的单轮模型调用（由 token_gateway.ForwardService 实现）。nil 时禁用压缩。
type Summarizer interface {
	ChatOnce(ctx context.Context, in tokengatewaysvc.ChatOnceInput) (*tokengatewaysvc.ChatOnceResult, error)
}

// ConversationService 有状态会话服务：会话 CRUD + 上下文重建 + 滚动压缩 + 有记忆对话。
type ConversationService struct {
	repo       *repository.ConversationRepository
	orch       Orchestrator
	summarizer Summarizer
	cache      *convcache.ConversationCache // Redis 热缓存；nil 安全空转（纯 DB，fail-open）
}

// NewConversationService 构造会话服务。
//   - summarizer 为 nil 时不做上下文压缩（仅原文窗口）。
//   - cache 为 nil（或其内部 redis 为 nil）时退化为纯 MySQL 读写，行为不变。
func NewConversationService(repo *repository.ConversationRepository, orch Orchestrator, summarizer Summarizer, cache *convcache.ConversationCache) *ConversationService {
	return &ConversationService{repo: repo, orch: orch, summarizer: summarizer, cache: cache}
}

// validationError 参数校验错误（handler 回 400）。
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func newValidation(msg string) error { return &validationError{msg: msg} }

// IsValidation 判断是否为参数校验错误。
func IsValidation(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}

// CreateInput 新建会话入参。
type CreateInput struct {
	UserID    uint64
	AgentID   *uint64 // nil = 普通聊天
	ModelCode string
	Title     string
}

// Create 新建会话。普通聊天必须指定 model_code；Agent 会话需通过可见性校验。
func (s *ConversationService) Create(ctx context.Context, in CreateInput) (*model.Conversation, error) {
	in.ModelCode = strings.TrimSpace(in.ModelCode)
	in.Title = strings.TrimSpace(in.Title)
	if in.AgentID != nil {
		if *in.AgentID == 0 {
			return nil, newValidation("agent_id 非法")
		}
		if err := s.orch.EnsureAgentVisible(ctx, *in.AgentID, in.UserID); err != nil {
			return nil, err
		}
	} else if in.ModelCode == "" {
		return nil, newValidation("普通聊天会话必须指定 model_code")
	}
	c := &model.Conversation{
		UserID:    in.UserID,
		AgentID:   in.AgentID,
		ModelCode: in.ModelCode,
		Title:     in.Title,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// List 分页列出本人会话。
func (s *ConversationService) List(ctx context.Context, userID uint64, scope string, offset, limit int) ([]model.Conversation, int64, error) {
	return s.repo.ListByUser(ctx, userID, scope, offset, limit)
}

// Get 取会话元信息（带隔离）。
func (s *ConversationService) Get(ctx context.Context, id, userID uint64) (*model.Conversation, error) {
	return s.repo.FindOwned(ctx, id, userID)
}

// ListMessages 分页取会话内消息（id ASC）。先校验归属再查消息，防越权。
func (s *ConversationService) ListMessages(ctx context.Context, id, userID uint64, offset, limit int) ([]model.Message, int64, error) {
	if _, err := s.repo.FindOwned(ctx, id, userID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListMessages(ctx, id, offset, limit)
}

// Rename 重命名会话。
func (s *ConversationService) Rename(ctx context.Context, id, userID uint64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return newValidation("标题不能为空")
	}
	if len([]rune(title)) > 255 {
		return newValidation("标题过长")
	}
	return s.repo.UpdateTitle(ctx, id, userID, title)
}

// Delete 删除会话及其消息，并清掉热缓存。
func (s *ConversationService) Delete(ctx context.Context, id, userID uint64) error {
	if err := s.repo.Delete(ctx, id, userID); err != nil {
		return err
	}
	s.cache.Invalidate(ctx, id)
	return nil
}

// Chat 在会话内发一条消息并取得有记忆的回复（SSE 或 JSON）。
// 流程：校验归属 → 落库用户消息 → 重建上下文(摘要+近期) → 编排引擎调用 → 落库回复 → 异步压缩。
// 返回 error 表示尚未写出（handler 映射 HTTP）；已开始写出则返回 nil。
func (s *ConversationService) Chat(ctx context.Context, w http.ResponseWriter, id, userID uint64, content string, stream bool) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return newValidation("content 不能为空")
	}
	conv, err := s.repo.FindOwned(ctx, id, userID)
	if err != nil {
		return err
	}
	// 落库用户消息（即使后续模型调用失败也保留，符合 ChatGPT 体验，可重试）。
	userMsg := &model.Message{
		ConversationID: conv.ID, UserID: userID, Role: "user",
		Content: content, TokenEst: estTokens(content),
	}
	if err := s.repo.AppendMessage(ctx, userMsg); err != nil {
		return fmt.Errorf("保存消息失败: %w", err)
	}
	s.cache.Append(ctx, conv.ID, *userMsg) // 写穿热缓存（快照不存在则空转，下次读重建）
	// 首条消息自动命名标题。
	if strings.TrimSpace(conv.Title) == "" {
		_ = s.repo.UpdateTitle(ctx, conv.ID, userID, truncateTitle(content))
	}
	// 重建上下文（摘要 system 消息 + 水位线之后的近期原文，含刚落库的用户消息）。
	contextMsgs, err := s.buildContext(ctx, conv)
	if err != nil {
		return fmt.Errorf("重建上下文失败: %w", err)
	}
	var agentID uint64
	if conv.AgentID != nil {
		agentID = *conv.AgentID
	}
	final, err := s.orch.RunWithContext(ctx, w, workbenchsvc.RunContextInput{
		AgentID:         agentID,
		UserID:          userID,
		RequestID:       idgen.NewRequestID(),
		Model:           conv.ModelCode,
		Stream:          stream,
		ContextMessages: contextMsgs,
	})
	if err != nil {
		return err // 尚未写出 → handler 映射 HTTP 错误码
	}
	// 落库 assistant 回复（流式中途出错时 final 为空，跳过）。
	if strings.TrimSpace(final) != "" {
		assistantMsg := &model.Message{
			ConversationID: conv.ID, UserID: userID, Role: "assistant",
			Content: final, TokenEst: estTokens(final),
		}
		if aerr := s.repo.AppendMessage(ctx, assistantMsg); aerr != nil {
			log.Printf("[conversation] 保存 assistant 消息失败 conv=%d: %v", conv.ID, aerr)
		} else {
			s.cache.Append(ctx, conv.ID, *assistantMsg) // 写穿热缓存
		}
	}
	// 异步压缩，避免阻塞 SSE 连接关闭。
	s.maybeCompressAsync(conv.ID, userID)
	return nil
}

// buildContext 组装喂给模型的上下文消息：摘要(若有) + 水位线之后的近期原文(带 token 预算裁剪)。
// 不含 Agent 的 system 人设——那由编排引擎按 agent_id 注入。
func (s *ConversationService) buildContext(ctx context.Context, conv *model.Conversation) ([]interface{}, error) {
	var (
		summary string
		msgs    []model.Message
	)
	// 先读热缓存；命中即免查库。未命中则查库并回填缓存（warm-up）。
	if snap, ok := s.cache.Get(ctx, conv.ID); ok {
		summary, msgs = snap.Summary, snap.Messages
	} else {
		var err error
		msgs, err = s.repo.ListAfter(ctx, conv.ID, conv.SummarizedUntilID)
		if err != nil {
			return nil, err
		}
		summary = conv.Summary
		s.cache.Set(ctx, conv.ID, &convcache.ContextSnapshot{
			Summary: summary, SummarizedUntilID: conv.SummarizedUntilID, Messages: msgs,
		})
	}
	out := make([]interface{}, 0, len(msgs)+1)
	if strings.TrimSpace(summary) != "" {
		out = append(out, map[string]interface{}{
			"role":    "system",
			"content": "以下是本次会话早前内容的摘要记忆，请据此保持上下文连贯：\n" + summary,
		})
	}
	// 预算裁剪：从最新往回累加 token，超预算则丢弃更早的原文（安全网；至少保留最后一条）。
	start := 0
	budget := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		budget += msgs[i].TokenEst
		if budget > contextBudgetTokens {
			start = i + 1
			break
		}
	}
	if start > len(msgs)-1 && len(msgs) > 0 {
		start = len(msgs) - 1
	}
	for _, m := range msgs[start:] {
		out = append(out, messageToWire(m))
	}
	return out, nil
}

// messageToWire 将落库消息转为喂给模型的 OpenAI 风格消息。
func messageToWire(m model.Message) interface{} {
	wm := map[string]interface{}{"role": m.Role, "content": m.Content}
	if m.Role == "tool" && m.ToolCallID != "" {
		wm["tool_call_id"] = m.ToolCallID
	}
	if m.ToolCalls != nil && *m.ToolCalls != "" {
		wm["tool_calls"] = json.RawMessage(*m.ToolCalls)
	}
	return wm
}

// maybeCompressAsync 在后台尝试压缩（独立 ctx + 超时），不阻塞当次响应。
func (s *ConversationService) maybeCompressAsync(convID, userID uint64) {
	if s.summarizer == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), compressTimeout)
		defer cancel()
		if err := s.compress(ctx, convID, userID); err != nil {
			log.Printf("[conversation] 上下文压缩失败 conv=%d: %v", convID, err)
		}
	}()
}

// compress 若水位线之后累计 token 超阈值，则把较早部分压成摘要，推进水位线。
func (s *ConversationService) compress(ctx context.Context, convID, userID uint64) error {
	conv, err := s.repo.FindOwned(ctx, convID, userID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(conv.ModelCode) == "" {
		return nil // 无固化模型可用于摘要，跳过
	}
	msgs, err := s.repo.ListAfter(ctx, conv.ID, conv.SummarizedUntilID)
	if err != nil {
		return err
	}
	total := 0
	for _, m := range msgs {
		total += m.TokenEst
	}
	if total <= compressThresholdTokens || len(msgs) <= keepRecentMessages {
		return nil
	}
	toSummarize := msgs[:len(msgs)-keepRecentMessages]
	newSummary, err := s.summarize(ctx, conv, conv.Summary, toSummarize)
	if err != nil {
		return err
	}
	if newSummary == "" {
		return nil
	}
	until := toSummarize[len(toSummarize)-1].ID
	if err := s.repo.UpdateSummary(ctx, conv.ID, newSummary, until); err != nil {
		return err
	}
	// 压缩后刷新热缓存：丢弃已被摘要覆盖的消息，仅留水位线之后的近期原文 + 新摘要，保持缓存温热。
	kept := msgs[len(msgs)-keepRecentMessages:]
	s.cache.Set(ctx, conv.ID, &convcache.ContextSnapshot{
		Summary: newSummary, SummarizedUntilID: until, Messages: kept,
	})
	return nil
}

// summarize 调模型把（已有摘要 + 新增对话）融合为更新后的摘要。
func (s *ConversationService) summarize(ctx context.Context, conv *model.Conversation, prev string, msgs []model.Message) (string, error) {
	var b strings.Builder
	if strings.TrimSpace(prev) != "" {
		b.WriteString("已有摘要：\n")
		b.WriteString(prev)
		b.WriteString("\n\n")
	}
	b.WriteString("新增对话：\n")
	for _, m := range msgs {
		role := m.Role
		switch role {
		case "user":
			role = "用户"
		case "assistant":
			role = "助手"
		case "tool":
			role = "工具"
		}
		b.WriteString(role)
		b.WriteString("：")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	body := map[string]interface{}{
		"model": conv.ModelCode,
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": summarySystemPrompt},
			map[string]interface{}{"role": "user", "content": b.String()},
		},
	}
	res, err := s.summarizer.ChatOnce(ctx, tokengatewaysvc.ChatOnceInput{
		RequestID: idgen.NewRequestID(),
		UserID:    conv.UserID,
		APIKeyID:  0,
		Model:     conv.ModelCode,
		Body:      body,
		CountCall: false, // 内部压缩不计入用户调用次数（token 用量仍由 ChatOnce 正常计费）
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Content), nil
}

// estTokens token 粗估（rune 数，中文偏保守），用于上下文预算与压缩触发。
func estTokens(s string) int {
	return len([]rune(s))
}

// truncateTitle 由首条消息生成标题：取首行、最多 40 字。
func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/shopspring/decimal"
)

// ChatOnceInput 单轮非流式上游调用入参（供 W8 编排端点复用转发器）。
// 与 Forward 的区别：强制非流式、返回结构化结果（含 tool_calls），不向 http.ResponseWriter 写出。
type ChatOnceInput struct {
	RequestID string                 // 本轮请求唯一标识（建议 主请求ID:r{round}，幂等/日志基准）
	UserID    uint64                 // 已鉴权用户 ID（编排端点仅登录态）
	APIKeyID  uint64                 // 编排端点仅登录态 → 恒 0（不做 sk model_scope 校验）
	Model     string                 // 逻辑模型名
	Body      map[string]interface{} // 已组装的请求体（含 messages + tools），门面只改写 model、去除 stream
	CountCall bool                   // 本轮是否计 calls=1（编排：仅首轮 true，整次提问按次计 1）
}

// ChatOnceToolCall 上游返回的一个工具调用请求。
type ChatOnceToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 原始 JSON 字符串（OpenAI function.arguments）
}

// ChatOnceResult 单轮结构化结果。ToolCalls 非空表示需继续编排；为空表示最终答案。
type ChatOnceResult struct {
	Content      string             // assistant 文本内容（content 为 null 时为空串）
	ToolCalls    []ChatOnceToolCall // 工具调用列表（空=最终答案）
	FinishReason string             // 上游 finish_reason
	AssistantRaw json.RawMessage    // 上游 choices[0].message 原文，供回灌下一轮 messages 保真
	InputTokens  int64
	OutputTokens int64
}

// ChatOnce 执行一次非流式上游调用并结构化返回（供编排循环逐轮调用）。
// 复用 Forward 的门禁/计费/选渠道/转发/读 usage/结算路径，仅改为非流式 + 解析 tool_calls。
//
// 计费：每轮都按 token 实计（input/output）；calls 仅在 CountCall=true 的那一轮计 1（整次提问计 1 次）。
// 错误（模型不可用/门禁/余额闸/上游失败）原样返回服务层错误，由编排层决定 SSE/HTTP 映射。
func (s *ForwardService) ChatOnce(ctx context.Context, oin ChatOnceInput) (*ChatOnceResult, error) {
	in := ForwardInput{
		RequestID: oin.RequestID,
		UserID:    oin.UserID,
		APIKeyID:  oin.APIKeyID,
		Model:     oin.Model,
		Stream:    false,
		Body:      oin.Body,
	}

	// ① 模型校验：存在、active、配置了渠道路由。
	tm, err := s.modelRepo.FindByCode(ctx, in.Model)
	if err != nil {
		return nil, ErrModelNotConfigured
	}
	if tm.Status != "active" || tm.ChannelID == nil || tm.UpstreamModel == nil || *tm.UpstreamModel == "" {
		return nil, ErrModelNotConfigured
	}

	// ①.5 sk model_scope 越界校验（编排端点恒登录态 APIKeyID==0 → 直接放行；保留以防复用）。
	if err := s.checkModelScope(ctx, in.APIKeyID, in.Model); err != nil {
		return nil, err
	}

	// ② 访问门禁：必须持有 active 的 token 服务资产。
	ok, err := s.assetGate.HasActiveTokenAsset(ctx, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("门禁查询失败: %w", err)
	}
	if !ok {
		return nil, ErrAccessDenied
	}

	// ②.5 计费前置闸：postpaid 冻结保证金 / prepaid 预占额度（与 Forward 同口径）。
	bill := s.resolveBilling(ctx, in)
	bill.skipCallBilling = !oin.CountCall // 非首轮跳过按次计费，整次提问只计 1 次
	if bill.mode == billingModePostpaid && s.walletHolder != nil {
		holdAmount := s.holdUnitPrice.Mul(decimal.NewFromInt(bill.maxTokens))
		if holdAmount.GreaterThan(decimal.Zero) {
			holdID, ferr := s.walletHolder.FreezeHold(ctx, in.UserID, holdAmount, in.RequestID+":hold", "token 编排调用预扣保证金")
			if ferr != nil {
				return nil, classifyFreezeError(in.RequestID, ferr)
			}
			bill.holdID = holdID
		}
	}
	if err := s.reservePrepaid(ctx, in, &bill); err != nil {
		return nil, err
	}
	// 兜底释放：任一路径未结算则全额释放保证金/预占额度（与 Forward 对称）。
	defer func() {
		if bill.holdID == 0 || bill.settled {
			return
		}
		if bill.mode == billingModePrepaid {
			if s.entReserver != nil {
				if rerr := s.entReserver.Release(context.Background(), bill.holdID, in.RequestID+":quota_reserve"); rerr != nil {
					log.Printf("[token_gateway] 编排释放 prepaid 预占失败 request_id=%s hold_id=%d: %v", in.RequestID, bill.holdID, rerr)
				}
			}
			return
		}
		if s.walletHolder != nil {
			if rerr := s.walletHolder.ReleaseHold(context.Background(), bill.holdID, in.RequestID+":release"); rerr != nil {
				log.Printf("[token_gateway] 编排释放预扣保证金失败 request_id=%s hold_id=%d: %v", in.RequestID, bill.holdID, rerr)
			}
		}
	}()

	// ③ 选渠道并解密 key。
	ch, err := s.channelRepo.FindByID(ctx, *tm.ChannelID)
	if err != nil || ch.Status != "active" {
		return nil, ErrChannelUnavailable
	}
	apiKey, err := s.cipher.Decrypt(ch.APIKeyEncrypted)
	if err != nil {
		return nil, ErrChannelUnavailable
	}

	// ④ 改写请求体：model → 上游真实模型名；强制非流式（编排需解析 tool_calls）。
	in.Body["model"] = *tm.UpstreamModel
	delete(in.Body, "stream")
	delete(in.Body, "stream_options")
	payload, err := json.Marshal(in.Body)
	if err != nil {
		return nil, fmt.Errorf("请求体序列化失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildChatURL(ch.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// ⑤ 发起上游调用。
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logUsage(ctx, in.RequestID, in, tm, 0, 0, statusFromErr(err), errCodePtr("upstream_error"))
		return nil, ErrUpstream
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logUsage(ctx, in.RequestID, in, tm, 0, 0, "failed", errCodePtr("upstream_read_error"))
		return nil, ErrUpstream
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logUsage(ctx, in.RequestID, in, tm, 0, 0, "failed", errCodePtr(fmt.Sprintf("upstream_%d", resp.StatusCode)))
		return nil, ErrUpstream
	}

	// ⑥ 解析结构化结果（content + tool_calls + usage），仅取元数据，不落对话明文。
	result, perr := parseChatCompletion(raw)
	if perr != nil {
		s.logUsage(ctx, in.RequestID, in, tm, 0, 0, "failed", errCodePtr("upstream_parse_error"))
		return nil, ErrUpstream
	}

	// ⑦ 记用量 + 结算（每轮计 token；calls 仅首轮）。
	s.logUsage(ctx, in.RequestID, in, tm, result.InputTokens, result.OutputTokens, "success", nil)
	s.settle(context.Background(), in.RequestID, in, tm, result.InputTokens, result.OutputTokens, &bill)
	return result, nil
}

// parseChatCompletion 解析 OpenAI 兼容非流式响应为结构化结果。
func parseChatCompletion(raw []byte) (*ChatOnceResult, error) {
	var resp struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage upstreamUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	res := &ChatOnceResult{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}
	if len(resp.Choices) == 0 {
		return res, nil
	}
	res.FinishReason = resp.Choices[0].FinishReason
	res.AssistantRaw = resp.Choices[0].Message

	var msg struct {
		Content   *string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(resp.Choices[0].Message, &msg); err != nil {
		return nil, err
	}
	if msg.Content != nil {
		res.Content = *msg.Content
	}
	for _, tc := range msg.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ChatOnceToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return res, nil
}

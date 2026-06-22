package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// UpstreamChat 单轮上游调用接口（由 token_gateway.ForwardService 适配，bootstrap 注入）。
// 编排循环每轮调用，复用其门禁/选渠道/转发/读 usage/计费路径。
type UpstreamChat interface {
	ChatOnce(ctx context.Context, in tokengatewaysvc.ChatOnceInput) (*tokengatewaysvc.ChatOnceResult, error)
}

// defaultMaxRounds tool-use 编排默认最大轮数（MAX_ROUNDS 未配置时；契约 §4）。
const defaultMaxRounds = 5

// ChatRequest 编排端点入参（handler 解析后传入）。
type ChatRequest struct {
	AgentID   uint64
	UserID    uint64
	RequestID string
	Model     string            // 可选覆盖；空则用 Agent.default_model_code
	Stream    bool              // true → SSE
	Messages  []json.RawMessage // 客户端自持的完整对话历史（透传，后端不落库）
}

// ChatService 站内聊天工作台 tool-use 编排服务（D2 仅登录态）。
type ChatService struct {
	agentRepo  *repository.AgentRepository
	skillRepo  *repository.SkillRepository
	pluginRepo *repository.PluginRepository
	registry   *SkillRegistry
	forwarder  *PluginForwarder
	upstream   UpstreamChat
	maxRounds  int
}

// NewChatService 构造编排服务。maxRounds<=0 时取默认 5。
func NewChatService(
	agentRepo *repository.AgentRepository,
	skillRepo *repository.SkillRepository,
	pluginRepo *repository.PluginRepository,
	registry *SkillRegistry,
	forwarder *PluginForwarder,
	upstream UpstreamChat,
	maxRounds int,
) *ChatService {
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}
	return &ChatService{
		agentRepo:  agentRepo,
		skillRepo:  skillRepo,
		pluginRepo: pluginRepo,
		registry:   registry,
		forwarder:  forwarder,
		upstream:   upstream,
		maxRounds:  maxRounds,
	}
}

// toolEntry 工具名 → 后端实现的映射项。
type toolEntry struct {
	isPlugin bool
	skill    *model.Skill
	plugin   *model.Plugin
}

// ChatWithAgent 执行一次带 Agent 的 tool-use 编排对话。
//
// 返回 error 表示「尚未向 w 写出任何内容」，由 handler 决定 HTTP 错误码（含可见性 ErrForbidden / NotFound /
// 上游与计费类错误）；一旦开始写出（SSE 或 JSON），即返回 nil。
func (s *ChatService) ChatWithAgent(ctx context.Context, w http.ResponseWriter, req ChatRequest) error {
	if len(req.Messages) == 0 {
		return newValidation("messages 不能为空")
	}

	// ① 加载 Agent + 可见性校验（官方 active 或本人自建）。
	agent, err := s.agentRepo.FindByID(ctx, req.AgentID)
	if err != nil {
		return err // handler 映射 404
	}
	visible := (agent.OwnerType == "user" && agent.OwnerUserID != nil && *agent.OwnerUserID == req.UserID) ||
		(agent.OwnerType == "official" && agent.Status == "active")
	if !visible {
		return ErrForbidden
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = agent.DefaultModelCode
	}

	// ② 汇总绑定且 enabled 的 skill/plugin → tools + 名称索引。
	tools, toolIndex, err := s.assembleTools(ctx, agent.ID)
	if err != nil {
		return fmt.Errorf("装配工具失败: %w", err)
	}

	// ③ 组装消息：system(人设) + 客户端历史。
	msgs := make([]interface{}, 0, len(req.Messages)+1)
	msgs = append(msgs, map[string]interface{}{"role": "system", "content": agent.SystemPrompt})
	for _, m := range req.Messages {
		msgs = append(msgs, m)
	}

	sse := newSSEWriter(w) // 延迟到首次写出才落 header

	// ④ tool-use 编排循环。
	for round := 1; round <= s.maxRounds; round++ {
		body := map[string]interface{}{"model": model, "messages": msgs}
		if len(tools) > 0 {
			body["tools"] = tools
			body["tool_choice"] = "auto"
		}
		res, cerr := s.upstream.ChatOnce(ctx, tokengatewaysvc.ChatOnceInput{
			RequestID: fmt.Sprintf("%s:r%d", req.RequestID, round),
			UserID:    req.UserID,
			APIKeyID:  0, // 编排端点仅登录态
			Model:     model,
			Body:      body,
			CountCall: round == 1, // 整次提问按次计 1（仅首轮）
		})
		if cerr != nil {
			if !sse.started && !req.Stream {
				return cerr // 未写出 → handler 映射 HTTP 错误码
			}
			if !sse.started {
				sse.start()
			}
			sse.event("error", map[string]interface{}{"message": cerr.Error()})
			sse.done()
			return nil
		}

		// 无 tool_calls → 最终答案。
		if len(res.ToolCalls) == 0 {
			s.writeFinal(w, sse, req.Stream, res.Content, "stop")
			return nil
		}

		// 有 tool_calls：回灌 assistant 消息 + 逐个执行工具。
		if req.Stream && !sse.started {
			sse.start()
		}
		if len(res.AssistantRaw) > 0 {
			msgs = append(msgs, res.AssistantRaw)
		}
		for _, tc := range res.ToolCalls {
			if req.Stream {
				sse.event("tool_call", map[string]interface{}{"name": tc.Name, "arguments": tc.Arguments})
			}
			content := s.runTool(ctx, req.UserID, toolIndex, tc)
			if req.Stream {
				sse.event("tool_result", map[string]interface{}{"name": tc.Name, "content": content})
			}
			msgs = append(msgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      content,
			})
		}
	}

	// ⑤ 超过 MAX_ROUNDS 仍未收敛：安全终止，明确告知已正常计费。
	s.writeFinal(w, sse, req.Stream,
		"工具调用已达上限，本次对话已终止；已消耗的 token 已正常计费。", "max_rounds")
	return nil
}

// runTool 执行单个工具调用，返回回灌给模型的结果文本（失败转为错误说明文本，不中断对话）。
func (s *ChatService) runTool(ctx context.Context, userID uint64, toolIndex map[string]toolEntry, tc tokengatewaysvc.ChatOnceToolCall) string {
	entry, ok := toolIndex[tc.Name]
	if !ok {
		return "工具执行失败: 未知工具 " + tc.Name
	}
	args := json.RawMessage(tc.Arguments)
	var (
		result string
		err    error
	)
	if entry.isPlugin {
		result, err = s.forwarder.Forward(ctx, entry.plugin, userID, args)
	} else {
		result, err = s.registry.Dispatch(ctx, entry.skill.HandlerKey, args)
	}
	if err != nil {
		return "工具执行失败: " + err.Error()
	}
	return result
}

// assembleTools 汇总 Agent 绑定且 enabled 的 active skill/plugin 的 tool_schema_json，构建 tools 列表 + 名称索引。
// 名称取 tool_schema_json.function.name；解析失败或非 active 的项跳过。
func (s *ChatService) assembleTools(ctx context.Context, agentID uint64) ([]json.RawMessage, map[string]toolEntry, error) {
	tools := []json.RawMessage{}
	index := map[string]toolEntry{}

	skillIDs, err := s.agentRepo.ListSkillIDs(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if len(skillIDs) > 0 {
		skills, err := s.skillRepo.FindByIDs(ctx, skillIDs)
		if err != nil {
			return nil, nil, err
		}
		for i := range skills {
			if skills[i].Status != "active" {
				continue
			}
			name := toolFuncName(skills[i].ToolSchemaJSON)
			if name == "" {
				continue
			}
			tools = append(tools, skills[i].ToolSchemaJSON)
			sk := skills[i]
			index[name] = toolEntry{skill: &sk}
		}
	}

	pluginIDs, err := s.agentRepo.ListPluginIDs(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if len(pluginIDs) > 0 {
		plugins, err := s.pluginRepo.FindByIDs(ctx, pluginIDs)
		if err != nil {
			return nil, nil, err
		}
		for i := range plugins {
			if plugins[i].Status != "active" {
				continue
			}
			name := toolFuncName(plugins[i].ToolSchemaJSON)
			if name == "" {
				continue
			}
			tools = append(tools, plugins[i].ToolSchemaJSON)
			pl := plugins[i]
			index[name] = toolEntry{isPlugin: true, plugin: &pl}
		}
	}
	return tools, index, nil
}

// writeFinal 输出最终答案：stream → SSE message + [DONE]；非 stream → 单条 JSON。
func (s *ChatService) writeFinal(w http.ResponseWriter, sse *sseWriter, stream bool, content, finishReason string) {
	if stream {
		if !sse.started {
			sse.start()
		}
		sse.event("message", map[string]interface{}{"content": content, "finish_reason": finishReason})
		sse.done()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"role": "assistant", "content": content}, "finish_reason": finishReason},
		},
	})
}

// toolFuncName 从 OpenAI tool 定义中取 function.name。
func toolFuncName(schema json.RawMessage) string {
	var t struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(schema, &t); err != nil {
		return ""
	}
	return t.Function.Name
}

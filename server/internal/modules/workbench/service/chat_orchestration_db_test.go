package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/crypto"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// fakeUpstream 脚本化的上游单轮调用桩，记录每轮入参以校验编排行为（CountCall / messages 增长 / tools）。
type fakeUpstream struct {
	calls     []tokengatewaysvc.ChatOnceInput
	responses []*tokengatewaysvc.ChatOnceResult
	idx       int
}

func (f *fakeUpstream) ChatOnce(ctx context.Context, in tokengatewaysvc.ChatOnceInput) (*tokengatewaysvc.ChatOnceResult, error) {
	f.calls = append(f.calls, in)
	if f.idx >= len(f.responses) {
		// 脚本耗尽：返回最终答案兜底（避免越界）。
		return &tokengatewaysvc.ChatOnceResult{Content: "兜底最终答案"}, nil
	}
	r := f.responses[f.idx]
	f.idx++
	return r, nil
}

// setupChatTest 连本地库并构造编排所需的全部依赖（agentSvc 用于建数据；chatRepos 用于建 ChatService）。
func setupChatTest(t *testing.T) (*gorm.DB, *AgentService, *SkillService, func()) {
	t.Helper()
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("跳过 DB 集成测试（设置 RUN_DB_TESTS=1）")
	}
	dsn := envOrWB("TEST_MYSQL_USER", "molin") + ":" + envOrWB("TEST_MYSQL_PASSWORD", "molin_password") +
		"@tcp(" + envOrWB("TEST_MYSQL_HOST", "127.0.0.1") + ":" + envOrWB("TEST_MYSQL_PORT", "13306") + ")/" +
		envOrWB("TEST_MYSQL_DATABASE", "molin") + "?charset=utf8mb4&parseTime=True&loc=Local"
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连库失败: %v", err)
	}
	agentRepo := repository.NewAgentRepository(gdb)
	skillRepo := repository.NewSkillRepository(gdb)
	pluginRepo := repository.NewPluginRepository(gdb)
	agentSvc := NewAgentService(agentRepo, skillRepo, pluginRepo)
	skillSvc := NewSkillService(skillRepo)
	clean := func() {
		gdb.Where("owner_user_id IN ?", []uint64{wbTestUserA, wbTestUserB}).Delete(&model.Agent{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Agent{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Skill{})
		gdb.Exec("DELETE FROM plugin_daily_call_logs WHERE user_id IN (?, ?)", wbTestUserA, wbTestUserB)
	}
	clean()
	return gdb, agentSvc, skillSvc, clean
}

// buildChatService 用真实库 + fake 上游构造 ChatService。
func buildChatService(gdb *gorm.DB, up UpstreamChat, maxRounds int) *ChatService {
	agentRepo := repository.NewAgentRepository(gdb)
	skillRepo := repository.NewSkillRepository(gdb)
	pluginRepo := repository.NewPluginRepository(gdb)
	cipher, _ := crypto.New([]byte(wbTestCipherKey))
	callRepo := repository.NewPluginCallRepository(gdb)
	registry := NewSkillRegistry(nil)
	forwarder := NewPluginForwarder(cipher, callRepo, pluginRepo, nil)
	return NewChatService(agentRepo, skillRepo, pluginRepo, registry, forwarder, up, maxRounds)
}

var webSearchSchema = json.RawMessage(`{"type":"function","function":{"name":"web_search","description":"搜索","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}}`)

// TestOrchestration_ToolThenFinal 校验：首轮 tool_call → 派发 skill → 回灌 → 次轮最终答案；
// 计次仅首轮（CountCall），SSE 事件齐全，messages 逐轮增长。
func TestOrchestration_ToolThenFinal(t *testing.T) {
	gdb, agentSvc, skillSvc, clean := setupChatTest(t)
	defer clean()
	ctx := context.Background()

	sk, err := skillSvc.Create(ctx, dto.CreateSkillReq{Code: wbTestCodePrefix + "ws", Name: "搜索", ToolSchemaJSON: webSearchSchema, HandlerKey: "web_search"})
	if err != nil {
		t.Fatalf("建 skill 失败: %v", err)
	}
	agent, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "ag", Name: "助手", SystemPrompt: "你是助手", DefaultModelCode: "deepseek-chat",
		SkillIDs: []uint64{sk.ID},
	})
	if err != nil {
		t.Fatalf("建 agent 失败: %v", err)
	}

	up := &fakeUpstream{responses: []*tokengatewaysvc.ChatOnceResult{
		{ // 首轮：请求调用 web_search
			ToolCalls:    []tokengatewaysvc.ChatOnceToolCall{{ID: "call_1", Name: "web_search", Arguments: `{"query":"今天新闻"}`}},
			AssistantRaw: json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"今天新闻\"}"}}]}`),
			InputTokens:  10, OutputTokens: 5,
		},
		{Content: "根据搜索结果，今天的新闻是…", InputTokens: 20, OutputTokens: 30}, // 次轮：最终答案
	}}
	cs := buildChatService(gdb, up, 5)

	rec := httptest.NewRecorder()
	msgs := []json.RawMessage{json.RawMessage(`{"role":"user","content":"帮我查今天的新闻"}`)}
	if err := cs.ChatWithAgent(ctx, rec, ChatRequest{AgentID: agent.ID, UserID: wbTestUserA, RequestID: "req-orch-1", Stream: true, Messages: msgs}); err != nil {
		t.Fatalf("编排返回错误: %v", err)
	}

	// 两轮调用。
	if len(up.calls) != 2 {
		t.Fatalf("应调上游 2 轮，实际 %d", len(up.calls))
	}
	// 计次仅首轮。
	if !up.calls[0].CountCall || up.calls[1].CountCall {
		t.Errorf("CountCall 应仅首轮 true，实际 r1=%v r2=%v", up.calls[0].CountCall, up.calls[1].CountCall)
	}
	// 首轮带 tools。
	if up.calls[0].Body["tools"] == nil {
		t.Errorf("首轮 body 应含 tools")
	}
	// 次轮 messages 已增长（system+user+assistant+tool = 4）。
	r2msgs, _ := up.calls[1].Body["messages"].([]interface{})
	if len(r2msgs) < 4 {
		t.Errorf("次轮 messages 应≥4（含回灌 assistant+tool），实际 %d", len(r2msgs))
	}
	// SSE 事件齐全。
	body := rec.Body.String()
	for _, want := range []string{"event: tool_call", "event: tool_result", "event: message", "[DONE]", "今天的新闻"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE 输出缺少 %q\n实际: %s", want, body)
		}
	}
	// web_search 占位返回错误 → tool_result 含「失败」（降级回灌，不中断）。
	if !strings.Contains(body, "失败") {
		t.Errorf("web_search 占位应回灌失败说明，实际: %s", body)
	}
}

// TestOrchestration_MaxRounds 校验：上游始终要求工具调用 → 达 MAX_ROUNDS 安全终止 + 计费提示。
func TestOrchestration_MaxRounds(t *testing.T) {
	gdb, agentSvc, skillSvc, clean := setupChatTest(t)
	defer clean()
	ctx := context.Background()

	sk, _ := skillSvc.Create(ctx, dto.CreateSkillReq{Code: wbTestCodePrefix + "ws2", Name: "搜索", ToolSchemaJSON: webSearchSchema, HandlerKey: "web_search"})
	agent, _ := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{Code: wbTestCodePrefix + "ag2", Name: "助手", SystemPrompt: "x", DefaultModelCode: "deepseek-chat", SkillIDs: []uint64{sk.ID}})

	// 每轮都要求工具 → 永不收敛。
	toolResp := &tokengatewaysvc.ChatOnceResult{
		ToolCalls:    []tokengatewaysvc.ChatOnceToolCall{{ID: "c", Name: "web_search", Arguments: `{"query":"x"}`}},
		AssistantRaw: json.RawMessage(`{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"web_search","arguments":"{}"}}]}`),
	}
	up := &fakeUpstream{responses: []*tokengatewaysvc.ChatOnceResult{toolResp, toolResp, toolResp}}
	cs := buildChatService(gdb, up, 3)

	rec := httptest.NewRecorder()
	msgs := []json.RawMessage{json.RawMessage(`{"role":"user","content":"循环"}`)}
	if err := cs.ChatWithAgent(ctx, rec, ChatRequest{AgentID: agent.ID, UserID: wbTestUserA, RequestID: "req-max", Stream: true, Messages: msgs}); err != nil {
		t.Fatalf("编排返回错误: %v", err)
	}
	if len(up.calls) != 3 {
		t.Fatalf("应恰好 3 轮（MAX_ROUNDS），实际 %d", len(up.calls))
	}
	if !strings.Contains(rec.Body.String(), "已达上限") {
		t.Errorf("超限应输出终止提示，实际: %s", rec.Body.String())
	}
}

// TestPluginDailyLimit 校验付费插件每用户每日上限原子计数。
func TestPluginDailyLimit(t *testing.T) {
	gdb, _, _, clean := setupChatTest(t)
	defer clean()
	ctx := context.Background()
	callRepo := repository.NewPluginCallRepository(gdb)

	const pluginID = 9_900_900_001
	limit := 3
	got := 0
	for i := 0; i < 5; i++ {
		allowed, err := callRepo.IncrementIfUnderLimit(ctx, pluginID, wbTestUserA, limit)
		if err != nil {
			t.Fatalf("计数失败: %v", err)
		}
		if allowed {
			got++
		}
	}
	if got != limit {
		t.Errorf("limit=%d 应放行 %d 次，实际 %d", limit, limit, got)
	}
	// 清理本测试计数行。
	gdb.Exec("DELETE FROM plugin_daily_call_logs WHERE plugin_id = ?", pluginID)
}

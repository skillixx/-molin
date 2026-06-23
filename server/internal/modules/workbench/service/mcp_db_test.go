package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

// setupMCPTest 连本地库并构造 MCP 服务依赖。仅 RUN_DB_TESTS=1 时运行。
func setupMCPTest(t *testing.T) (*gorm.DB, *MCPService, *AgentService, func()) {
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
	cipher, err := crypto.New([]byte(wbTestCipherKey))
	if err != nil {
		t.Fatalf("构造 cipher 失败: %v", err)
	}
	mcpRepo := repository.NewMCPRepository(gdb)
	agentRepo := repository.NewAgentRepository(gdb)
	skillRepo := repository.NewSkillRepository(gdb)
	pluginRepo := repository.NewPluginRepository(gdb)
	categoryRepo := repository.NewAgentCategoryRepository(gdb)
	agentSvc := NewAgentService(agentRepo, skillRepo, pluginRepo, categoryRepo)
	client := NewMCPClient()
	// 测试用 allowedDomains 含 127.0.0.1 不适用（httptest 用 IP），运行时 SSRF 会拦截 127.0.0.1，
	// 因此 discover/call 的测试单独用 MCPClient 直连 httptest（绕过 service 层 SSRF），CRUD 测试不外呼。
	mcpSvc := NewMCPService(mcpRepo, agentRepo, cipher, client, nil)

	clean := func() {
		gdb.Where("owner_user_id IN ?", []uint64{wbTestUserA, wbTestUserB}).Delete(&model.Agent{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Agent{})
		// 删 MCP server（工具快照 FK CASCADE）。
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.MCPServer{})
		gdb.Exec("DELETE FROM tool_daily_call_logs WHERE user_id IN (?, ?)", wbTestUserA, wbTestUserB)
	}
	clean()
	return gdb, mcpSvc, agentSvc, clean
}

// fakeMCPServer 启一个 httptest JSON-RPC stub，模拟 MCP Streamable HTTP server。
// 支持 initialize / notifications/initialized / tools/list（可分页）/ tools/call。
type fakeMCPServer struct {
	srv        *httptest.Server
	tools      []MCPTool
	callResult string
	callErr    bool
	gotAuth    string // 记录收到的 Authorization 头，校验凭证注入
	paginate   bool   // true → tools/list 分两页返回
}

func newFakeMCPServer(tools []MCPTool, callResult string) *fakeMCPServer {
	f := &fakeMCPServer{tools: tools, callResult: callResult}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeMCPServer) close() { f.srv.Close() }

func (f *fakeMCPServer) handle(w http.ResponseWriter, r *http.Request) {
	f.gotAuth = r.Header.Get("Authorization")
	var req jsonRPCRequest
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)

	// 通知（无 id）：返回 202。
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-123")
		resp.Result = json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`)
	case "tools/list":
		if f.paginate {
			// 第一页带 cursor，第二页不带。
			pm, _ := req.Params.(map[string]interface{})
			if pm == nil || pm["cursor"] == nil {
				first, _ := json.Marshal(map[string]interface{}{"tools": f.tools[:1], "nextCursor": "page2"})
				resp.Result = first
			} else {
				rest, _ := json.Marshal(map[string]interface{}{"tools": f.tools[1:]})
				resp.Result = rest
			}
		} else {
			all, _ := json.Marshal(map[string]interface{}{"tools": f.tools})
			resp.Result = all
		}
	case "tools/call":
		if f.callErr {
			resp.Error = &jsonRPCError{Code: -32000, Message: "tool failed"}
		} else {
			res, _ := json.Marshal(map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": f.callResult}},
			})
			resp.Result = res
		}
	default:
		resp.Error = &jsonRPCError{Code: -32601, Message: "method not found"}
	}
	out, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

var mcpToolSchema = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

// TestMCPServerCRUD_NoCredentialLeak 校验：创建 MCP server 凭证不回（has_auth），SSRF 配置拦截内网/非 https。
func TestMCPServerCRUD_NoCredentialLeak(t *testing.T) {
	_, svc, _, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()

	// 内网 endpoint 配置时拒绝。
	if _, err := svc.CreateServer(ctx, dto.CreateMCPServerReq{
		Code: wbTestCodePrefix + "ssrf", Name: "ssrf", EndpointURL: "https://127.0.0.1/mcp",
	}); !IsValidation(err) {
		t.Fatalf("内网 endpoint 应被拒绝(校验错误)，实际 err=%v", err)
	}
	// 非 https 拒绝。
	if _, err := svc.CreateServer(ctx, dto.CreateMCPServerReq{
		Code: wbTestCodePrefix + "http", Name: "http", EndpointURL: "http://example.com/mcp",
	}); !IsValidation(err) {
		t.Fatalf("非 https endpoint 应被拒绝，实际 err=%v", err)
	}

	resp, err := svc.CreateServer(ctx, dto.CreateMCPServerReq{
		Code: wbTestCodePrefix + "s1", Name: "s1", EndpointURL: "https://mcp.example.com/rpc",
		AuthConfig: `{"header":"Authorization","value":"Bearer secret-xyz"}`,
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if !resp.HasAuth {
		t.Errorf("has_auth 应为 true")
	}
	// 序列化响应不得含凭证明文。
	raw, _ := json.Marshal(resp)
	if strings.Contains(string(raw), "secret-xyz") || strings.Contains(string(raw), "auth_config") {
		t.Errorf("响应泄漏凭证: %s", raw)
	}
	// 新建默认 inactive。
	if resp.Status != "inactive" {
		t.Errorf("新建默认应 inactive，实际 %s", resp.Status)
	}

	// code 重复冲突。
	if _, err := svc.CreateServer(ctx, dto.CreateMCPServerReq{Code: wbTestCodePrefix + "s1", Name: "dup", EndpointURL: "https://mcp.example.com/rpc"}); err == nil {
		t.Errorf("code 重复应冲突")
	}
}

// TestMCPDiscoverAndAudit 校验：discover 写快照（默认 enabled=0），审核启用后才暴露；schema 变更置未启用。
func TestMCPDiscoverAndAudit(t *testing.T) {
	gdb, svc, _, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()

	fake := newFakeMCPServer([]MCPTool{
		{Name: "search", Description: "搜索", InputSchema: mcpToolSchema},
		{Name: "calc", Description: "计算", InputSchema: mcpToolSchema},
	}, "结果")
	defer fake.close()

	// 直接用 fake 的 IP endpoint 建 server；service 配置校验会拦 127.0.0.1，故直接落库绕过配置 SSRF（模拟已是公网域名）。
	srv := &model.MCPServer{Code: wbTestCodePrefix + "disc", Name: "disc", EndpointURL: fake.srv.URL, Status: "inactive", TimeoutMs: 5000}
	if err := gdb.Create(srv).Error; err != nil {
		t.Fatalf("落库 server 失败: %v", err)
	}
	// service 的 client 运行时 SSRF 会拦 127.0.0.1；本测试改用允许私网的 client 验证 discover 链路。
	svc.client = newTestMCPClient()

	res, err := svc.Discover(ctx, srv.ID)
	if err != nil {
		t.Fatalf("discover 失败: %v", err)
	}
	if res.Discovered != 2 || res.Changed != 2 {
		t.Errorf("应发现 2 工具且全为新增，实际 discovered=%d changed=%d", res.Discovered, res.Changed)
	}
	if res.ProtocolVersion != "2025-06-18" {
		t.Errorf("协议版本回填错误: %s", res.ProtocolVersion)
	}
	// 快照默认 enabled=0。
	tools, _ := svc.ListTools(ctx, srv.ID)
	for _, tl := range tools {
		if tl.Enabled {
			t.Errorf("工具 %s 应默认未启用", tl.ToolName)
		}
	}

	// 审核启用 search。
	var searchID uint64
	for _, tl := range tools {
		if tl.ToolName == "search" {
			searchID = tl.ID
		}
	}
	en := true
	if _, err := svc.UpdateToolEnabled(ctx, srv.ID, searchID, dto.UpdateMCPToolReq{Enabled: &en}); err != nil {
		t.Fatalf("启用工具失败: %v", err)
	}

	// 再次 discover 但定义不变 → enabled 保持。
	if _, err := svc.Discover(ctx, srv.ID); err != nil {
		t.Fatalf("二次 discover 失败: %v", err)
	}
	st, _ := svc.repo.FindToolByID(ctx, searchID)
	if !st.Enabled {
		t.Errorf("定义未变，已启用工具不应被置未启用")
	}

	// 改 fake 的 search 定义（描述变化 → schema_hash 变）→ discover 应置未启用待重审。
	fake.tools[0].Description = "全新搜索描述"
	res2, err := svc.Discover(ctx, srv.ID)
	if err != nil {
		t.Fatalf("三次 discover 失败: %v", err)
	}
	if res2.Changed < 1 {
		t.Errorf("定义变更应计 changed≥1，实际 %d", res2.Changed)
	}
	st2, _ := svc.repo.FindToolByID(ctx, searchID)
	if st2.Enabled {
		t.Errorf("定义变更后工具应被置未启用待重审")
	}
}

// TestMCPBindOnlyOfficial 校验：v1 仅官方 Agent 可绑 MCP server；绑用户自建被拒。
func TestMCPBindOnlyOfficial(t *testing.T) {
	gdb, svc, agentSvc, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()

	srv := &model.MCPServer{Code: wbTestCodePrefix + "bind", Name: "bind", EndpointURL: "https://mcp.example.com/rpc", Status: "active", TimeoutMs: 5000}
	if err := gdb.Create(srv).Error; err != nil {
		t.Fatalf("落库 server 失败: %v", err)
	}

	// 官方 Agent 可绑。
	official, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "off", Name: "官方", SystemPrompt: "x", DefaultModelCode: "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("建官方 agent 失败: %v", err)
	}
	if err := svc.BindAgentServers(ctx, official.ID, []uint64{srv.ID}); err != nil {
		t.Fatalf("官方 agent 绑定应成功，实际 err=%v", err)
	}
	ids, _ := svc.repo.ListServerIDsByAgent(ctx, official.ID)
	if len(ids) != 1 || ids[0] != srv.ID {
		t.Errorf("绑定未生效: %v", ids)
	}

	// 用户自建 Agent 绑定应被拒。
	selfMade, err := agentSvc.UserCreate(ctx, wbTestUserA, dto.UserCreateAgentReq{
		Name: "自建", SystemPrompt: "x", DefaultModelCode: "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("建自建 agent 失败: %v", err)
	}
	if err := svc.BindAgentServers(ctx, selfMade.ID, []uint64{srv.ID}); err == nil {
		t.Errorf("自建 agent 绑定 MCP 应被拒")
	}
}

// TestMCPPublicListNoEndpoint 校验：用户端只读视图不回 endpoint/凭证。
func TestMCPPublicListNoEndpoint(t *testing.T) {
	gdb, svc, _, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()

	srv := &model.MCPServer{Code: wbTestCodePrefix + "pub", Name: "pub", EndpointURL: "https://mcp.example.com/secret-path", Status: "active", TimeoutMs: 5000}
	if err := gdb.Create(srv).Error; err != nil {
		t.Fatalf("落库失败: %v", err)
	}
	items, _, err := svc.ListPublicServers(ctx, 0, 50)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "secret-path") || strings.Contains(string(raw), "endpoint") {
		t.Errorf("用户端视图泄漏 endpoint: %s", raw)
	}
}

// TestMCPOrchestrationIntegration 校验：绑 MCP server 后 enabled 工具进编排，tools_call 路由到 client，
// 命名空间防撞，失败降级不中断。
func TestMCPOrchestrationIntegration(t *testing.T) {
	gdb, svc, agentSvc, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()

	fake := newFakeMCPServer([]MCPTool{{Name: "lookup", Description: "查", InputSchema: mcpToolSchema}}, "MCP工具返回内容")
	defer fake.close()

	srv := &model.MCPServer{Code: wbTestCodePrefix + "orch", Name: "orch", EndpointURL: fake.srv.URL, Status: "active", TimeoutMs: 5000}
	if err := gdb.Create(srv).Error; err != nil {
		t.Fatalf("落库失败: %v", err)
	}
	svc.client = newTestMCPClient()
	if _, err := svc.Discover(ctx, srv.ID); err != nil {
		t.Fatalf("discover 失败: %v", err)
	}
	// 审核启用 lookup。
	tools, _ := svc.ListTools(ctx, srv.ID)
	en := true
	if _, err := svc.UpdateToolEnabled(ctx, srv.ID, tools[0].ID, dto.UpdateMCPToolReq{Enabled: &en}); err != nil {
		t.Fatalf("启用失败: %v", err)
	}

	agent, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "orchag", Name: "助手", SystemPrompt: "你是助手", DefaultModelCode: "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("建 agent 失败: %v", err)
	}
	if err := svc.BindAgentServers(ctx, agent.ID, []uint64{srv.ID}); err != nil {
		t.Fatalf("绑定失败: %v", err)
	}

	expectedToolName := "mcp__" + srv.Code + "__lookup"
	up := &fakeUpstream{responses: []*tokengatewaysvc.ChatOnceResult{
		{
			ToolCalls:    []tokengatewaysvc.ChatOnceToolCall{{ID: "c1", Name: expectedToolName, Arguments: `{"q":"hi"}`}},
			AssistantRaw: json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"` + expectedToolName + `","arguments":"{}"}}]}`),
		},
		{Content: "最终答案"},
	}}
	cs := buildChatService(gdb, up, 5)
	// 编排走 service 配置的 MCP client（默认运行时 SSRF 会拦 127.0.0.1），替换为测试 client。
	cs.WithMCP(svc.repo, newTestMCPClient(), mustCipher(t), repository.NewToolCallRepository(gdb), nil)

	rec := httptest.NewRecorder()
	msgs := []json.RawMessage{json.RawMessage(`{"role":"user","content":"用工具"}`)}
	if err := cs.ChatWithAgent(ctx, rec, ChatRequest{AgentID: agent.ID, UserID: wbTestUserA, RequestID: "mcp-orch", Stream: true, Messages: msgs}); err != nil {
		t.Fatalf("编排失败: %v", err)
	}
	// 首轮 body 应含命名空间工具。
	body := rec.Body.String()
	if !strings.Contains(body, "MCP工具返回内容") {
		t.Errorf("MCP 工具结果未回灌: %s", body)
	}
	// 首轮 tools 含前缀名。
	toolsRaw, _ := json.Marshal(up.calls[0].Body["tools"])
	if !strings.Contains(string(toolsRaw), expectedToolName) {
		t.Errorf("工具集缺命名空间工具 %s: %s", expectedToolName, toolsRaw)
	}
}

// TestMCPPaidDailyLimit 校验：付费 server 用通用 tool_daily_call_logs（mcp 维度）限流。
func TestMCPPaidDailyLimit(t *testing.T) {
	gdb, _, _, clean := setupMCPTest(t)
	defer clean()
	ctx := context.Background()
	callRepo := repository.NewToolCallRepository(gdb)

	const serverID = 9_900_800_001
	limit := 2
	got := 0
	for i := 0; i < 4; i++ {
		allowed, err := callRepo.IncrementIfUnderLimit(ctx, "mcp", serverID, wbTestUserA, limit)
		if err != nil {
			t.Fatalf("计数失败: %v", err)
		}
		if allowed {
			got++
		}
	}
	if got != limit {
		t.Errorf("mcp limit=%d 应放行 %d 次，实际 %d", limit, limit, got)
	}
	// plugin 维度独立计数（同 tool_id 不串）。
	allowed, _ := callRepo.IncrementIfUnderLimit(ctx, "plugin", serverID, wbTestUserA, limit)
	if !allowed {
		t.Errorf("plugin 维度应与 mcp 维度独立计数")
	}
	gdb.Exec("DELETE FROM tool_daily_call_logs WHERE tool_id = ?", serverID)
}

// newTestMCPClient 构造绕过私网 SSRF 拦截的测试 client（连 httptest stub 用 127.0.0.1）。
func newTestMCPClient() *MCPClient {
	c := NewMCPClient()
	c.skipSSRF = true
	return c
}

// mustCipher 构造测试 cipher。
func mustCipher(t *testing.T) *crypto.AESGCM {
	t.Helper()
	c, err := crypto.New([]byte(wbTestCipherKey))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return c
}

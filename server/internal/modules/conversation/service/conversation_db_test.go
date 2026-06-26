package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/conversation/model"
	"molin/server/internal/modules/conversation/repository"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	workbenchsvc "molin/server/internal/modules/workbench/service"
)

// 测试用高位用户 ID（避免与真实数据冲突）。
const (
	convTestUserA = uint64(990001)
	convTestUserB = uint64(990002)
)

// fakeOrch 编排引擎桩：记录最近一次收到的上下文消息，返回脚本化回复。
type fakeOrch struct {
	reply       string
	lastContext []interface{}
	visibleErr  error
}

func (f *fakeOrch) RunWithContext(ctx context.Context, w http.ResponseWriter, in workbenchsvc.RunContextInput) (string, error) {
	f.lastContext = in.ContextMessages
	return f.reply, nil
}

func (f *fakeOrch) EnsureAgentVisible(ctx context.Context, agentID, userID uint64) error {
	return f.visibleErr
}

// fakeSummarizer 摘要桩：返回固定摘要文本，断言压缩链路。
type fakeSummarizer struct {
	called bool
	out    string
}

func (f *fakeSummarizer) ChatOnce(ctx context.Context, in tokengatewaysvc.ChatOnceInput) (*tokengatewaysvc.ChatOnceResult, error) {
	f.called = true
	return &tokengatewaysvc.ChatOnceResult{Content: f.out}, nil
}

func convEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func setupConvTest(t *testing.T) (*repository.ConversationRepository, *gorm.DB, func()) {
	t.Helper()
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("跳过 DB 集成测试（设置 RUN_DB_TESTS=1）")
	}
	dsn := convEnvOr("TEST_MYSQL_USER", "molin") + ":" + convEnvOr("TEST_MYSQL_PASSWORD", "molin_password") +
		"@tcp(" + convEnvOr("TEST_MYSQL_HOST", "127.0.0.1") + ":" + convEnvOr("TEST_MYSQL_PORT", "13306") + ")/" +
		convEnvOr("TEST_MYSQL_DATABASE", "molin") + "?charset=utf8mb4&parseTime=True&loc=Local"
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连库失败: %v", err)
	}
	repo := repository.NewConversationRepository(gdb)
	clean := func() {
		gdb.Exec("DELETE FROM chat_messages WHERE user_id IN (?, ?)", convTestUserA, convTestUserB)
		gdb.Exec("DELETE FROM chat_conversations WHERE user_id IN (?, ?)", convTestUserA, convTestUserB)
	}
	clean()
	return repo, gdb, clean
}

// TestConversationCRUDAndIsolation 校验新建/校验/用户隔离/列表过滤/改名/删除级联。
func TestConversationCRUDAndIsolation(t *testing.T) {
	repo, _, clean := setupConvTest(t)
	defer clean()
	ctx := context.Background()
	svc := NewConversationService(repo, &fakeOrch{reply: "x"}, nil, nil)

	// 普通聊天未传 model_code → 校验失败
	if _, err := svc.Create(ctx, CreateInput{UserID: convTestUserA}); !IsValidation(err) {
		t.Fatalf("期望校验错误（普通聊天缺 model_code），得到: %v", err)
	}
	// 普通聊天带 model_code → 成功
	plain, err := svc.Create(ctx, CreateInput{UserID: convTestUserA, ModelCode: "gpt-4o"})
	if err != nil {
		t.Fatalf("创建普通会话失败: %v", err)
	}
	// Agent 会话（fake 可见）→ 成功
	aid := uint64(123)
	agentConv, err := svc.Create(ctx, CreateInput{UserID: convTestUserA, AgentID: &aid, ModelCode: "gpt-4o"})
	if err != nil {
		t.Fatalf("创建 Agent 会话失败: %v", err)
	}

	// 隔离：用户 B 取用户 A 的会话 → 不存在
	if _, err := svc.Get(ctx, plain.ID, convTestUserB); err != ErrConversationNotFound {
		t.Fatalf("隔离失败：B 不应读到 A 的会话，得到 err=%v", err)
	}
	// 本人可取
	if _, err := svc.Get(ctx, plain.ID, convTestUserA); err != nil {
		t.Fatalf("本人读取会话失败: %v", err)
	}

	// 列表过滤
	plainList, total, err := svc.List(ctx, convTestUserA, "plain", 0, 20)
	if err != nil || total != 1 || len(plainList) != 1 || plainList[0].ID != plain.ID {
		t.Fatalf("plain 过滤异常: total=%d items=%d err=%v", total, len(plainList), err)
	}
	_, agentTotal, _ := svc.List(ctx, convTestUserA, "agent", 0, 20)
	if agentTotal != 1 {
		t.Fatalf("agent 过滤异常: total=%d", agentTotal)
	}
	_, allTotal, _ := svc.List(ctx, convTestUserA, "", 0, 20)
	if allTotal != 2 {
		t.Fatalf("全部会话数应为 2，得到 %d", allTotal)
	}

	// 改名（带隔离）：B 改 A 的会话 → 不存在
	if err := svc.Rename(ctx, plain.ID, convTestUserB, "黑客改名"); err != ErrConversationNotFound {
		t.Fatalf("隔离失败：B 不应能改 A 的会话标题，err=%v", err)
	}
	if err := svc.Rename(ctx, plain.ID, convTestUserA, "我的会话"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}

	// 删除 Agent 会话并校验级联（先塞一条消息）
	_ = repo.AppendMessage(ctx, &model.Message{ConversationID: agentConv.ID, UserID: convTestUserA, Role: "user", Content: "hi", TokenEst: 2})
	if err := svc.Delete(ctx, agentConv.ID, convTestUserA); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := svc.Get(ctx, agentConv.ID, convTestUserA); err != ErrConversationNotFound {
		t.Fatalf("删除后仍可读到会话")
	}
	// 删除后查消息：ListMessages 先校验归属，会话已删 → 返回 ErrConversationNotFound
	if _, _, err := svc.ListMessages(ctx, agentConv.ID, convTestUserA, 0, 20); err != ErrConversationNotFound {
		t.Fatalf("删除后查消息应返回会话不存在，err=%v", err)
	}
}

// TestChatMemoryAndContext 校验对话落库（用户+助手）+ 第二轮上下文携带历史（记忆连贯）。
func TestChatMemoryAndContext(t *testing.T) {
	repo, _, clean := setupConvTest(t)
	defer clean()
	ctx := context.Background()
	orch := &fakeOrch{reply: "你好张三，已记住你的名字"}
	svc := NewConversationService(repo, orch, nil, nil)

	conv, err := svc.Create(ctx, CreateInput{UserID: convTestUserA, ModelCode: "gpt-4o"})
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := svc.Chat(ctx, rec, conv.ID, convTestUserA, "我叫张三，请记住", false); err != nil {
		t.Fatalf("第一轮对话失败: %v", err)
	}
	// 落库：用户 + 助手 两条
	msgs, total, err := svc.ListMessages(ctx, conv.ID, convTestUserA, 0, 50)
	if err != nil || total != 2 {
		t.Fatalf("首轮后应有 2 条消息，得到 total=%d err=%v", total, err)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[1].Content != orch.reply {
		t.Fatalf("消息内容/角色不符: %+v", msgs)
	}
	// 标题自动生成
	got, _ := svc.Get(ctx, conv.ID, convTestUserA)
	if strings.TrimSpace(got.Title) == "" {
		t.Fatalf("首条消息后标题应自动生成")
	}

	// 第二轮：上下文应携带此前 user+assistant 历史（记忆连贯）
	orch.reply = "第二轮回复"
	rec2 := httptest.NewRecorder()
	if err := svc.Chat(ctx, rec2, conv.ID, convTestUserA, "我叫什么名字？", false); err != nil {
		t.Fatalf("第二轮对话失败: %v", err)
	}
	// 第二轮传给编排的上下文：user1 + assistant1 + user2（无摘要时） = 3 条
	if len(orch.lastContext) < 3 {
		t.Fatalf("第二轮上下文应含历史(≥3条)，实际 %d 条: %+v", len(orch.lastContext), orch.lastContext)
	}
	if _, total, _ := svc.ListMessages(ctx, conv.ID, convTestUserA, 0, 50); total != 4 {
		t.Fatalf("两轮后应有 4 条消息，得到 %d", total)
	}
}

// TestCompression 校验滚动压缩：超阈值后早期消息被压成摘要、水位线推进、上下文改用摘要+近期。
func TestCompression(t *testing.T) {
	repo, _, clean := setupConvTest(t)
	defer clean()
	ctx := context.Background()
	sum := &fakeSummarizer{out: "【摘要】用户名叫张三，正在咨询订单问题。"}
	svc := NewConversationService(repo, &fakeOrch{reply: "x"}, sum, nil)

	conv, err := svc.Create(ctx, CreateInput{UserID: convTestUserA, ModelCode: "gpt-4o"})
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	// 塞 10 条消息，每条 ~700 runes → 总计 ~7000 > 阈值 6000，且条数 > keepRecent(8)
	big := strings.Repeat("话", 700)
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := repo.AppendMessage(ctx, &model.Message{
			ConversationID: conv.ID, UserID: convTestUserA, Role: role, Content: big, TokenEst: estTokens(big),
		}); err != nil {
			t.Fatalf("塞消息失败: %v", err)
		}
	}

	// 直接触发压缩（生产为异步；此处同步调内部方法断言）
	if err := svc.compress(ctx, conv.ID, convTestUserA); err != nil {
		t.Fatalf("压缩失败: %v", err)
	}
	if !sum.called {
		t.Fatalf("压缩应调用 summarizer")
	}
	got, _ := svc.Get(ctx, conv.ID, convTestUserA)
	if strings.TrimSpace(got.Summary) == "" {
		t.Fatalf("压缩后摘要应非空")
	}
	if got.SummarizedUntilID == 0 {
		t.Fatalf("压缩后水位线应推进")
	}

	// 上下文重建：含摘要 system 消息 + 水位线之后的近期原文（keepRecent=8 条）
	mctx, err := svc.buildContext(ctx, got)
	if err != nil {
		t.Fatalf("重建上下文失败: %v", err)
	}
	if len(mctx) == 0 {
		t.Fatalf("上下文不应为空")
	}
	first, ok := mctx[0].(map[string]interface{})
	if !ok || first["role"] != "system" || !strings.Contains(first["content"].(string), "摘要") {
		t.Fatalf("上下文首条应为摘要 system 消息，实际: %+v", mctx[0])
	}
	// 水位线后应为 8 条原文（10 - (10-8) 已压缩）
	if len(mctx) != 1+8 {
		t.Fatalf("上下文应为 摘要1 + 近期8 = 9 条，实际 %d", len(mctx))
	}
}

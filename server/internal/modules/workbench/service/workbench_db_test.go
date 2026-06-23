package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// 测试专用、不与真实业务冲突的高位 user_id（仅清理自己写入的行）。
const (
	wbTestUserA uint64 = 9_900_100_001
	wbTestUserB uint64 = 9_900_100_002
)

// wbTestCodePrefix 测试数据 code 统一前缀，便于精准清理。
const wbTestCodePrefix = "wbtest_"

// 32 字节 AES-256-GCM 测试密钥。
const wbTestCipherKey = "0123456789abcdef0123456789abcdef"

func envOrWB(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// setupWBTest 连本地 molin 开发库（迁移已建好 agents/skills/plugins）。仅 RUN_DB_TESTS=1 时运行。
func setupWBTest(t *testing.T) (*AgentService, *AgentCategoryService, *SkillService, *PluginService, func()) {
	t.Helper()
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("跳过 DB 集成测试（设置 RUN_DB_TESTS=1 且本地 MySQL 13306 可用时运行）")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		envOrWB("TEST_MYSQL_USER", "molin"),
		envOrWB("TEST_MYSQL_PASSWORD", "molin_password"),
		envOrWB("TEST_MYSQL_HOST", "127.0.0.1"),
		envOrWB("TEST_MYSQL_PORT", "13306"),
		envOrWB("TEST_MYSQL_DATABASE", "molin"),
	)
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("GORM 连接 molin 库失败: %v", err)
	}

	cipher, err := crypto.New([]byte(wbTestCipherKey))
	if err != nil {
		t.Fatalf("构造测试 cipher 失败: %v", err)
	}

	agentRepo := repository.NewAgentRepository(gdb)
	skillRepo := repository.NewSkillRepository(gdb)
	pluginRepo := repository.NewPluginRepository(gdb)
	categoryRepo := repository.NewAgentCategoryRepository(gdb)

	agentSvc := NewAgentService(agentRepo, skillRepo, pluginRepo, categoryRepo)
	categorySvc := NewAgentCategoryService(categoryRepo)
	skillSvc := NewSkillService(skillRepo)
	pluginSvc := NewPluginService(pluginRepo, cipher)

	clean := func() {
		// 先删测试 Agent（绑定表 FK CASCADE 自动清理），再删 skill/plugin。
		gdb.Where("owner_user_id IN ?", []uint64{wbTestUserA, wbTestUserB}).Delete(&model.Agent{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Agent{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Skill{})
		gdb.Where("code LIKE ?", wbTestCodePrefix+"%").Delete(&model.Plugin{})
	}
	clean()
	return agentSvc, categorySvc, skillSvc, pluginSvc, clean
}

var wbToolSchema = json.RawMessage(`{"type":"function","function":{"name":"demo","parameters":{"type":"object"}}}`)

// TestSkillCRUD 校验 Skill 创建/唯一冲突/校验/列表（仅 active）。
func TestSkillCRUD(t *testing.T) {
	_, _, skillSvc, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 正常创建
	s, err := skillSvc.Create(ctx, dto.CreateSkillReq{
		Code: wbTestCodePrefix + "search", Name: "联网搜索", ToolSchemaJSON: wbToolSchema, HandlerKey: "web_search",
	})
	if err != nil {
		t.Fatalf("创建 skill 失败: %v", err)
	}
	if s.Status != "active" {
		t.Errorf("status 默认应为 active，实际 %q", s.Status)
	}

	// code 重复 → ErrSkillCodeExists
	if _, err := skillSvc.Create(ctx, dto.CreateSkillReq{
		Code: wbTestCodePrefix + "search", Name: "x", ToolSchemaJSON: wbToolSchema, HandlerKey: "web_search",
	}); err != ErrSkillCodeExists {
		t.Errorf("重复 code 应返回 ErrSkillCodeExists，实际 %v", err)
	}

	// 非法 tool_schema → 校验错误
	if _, err := skillSvc.Create(ctx, dto.CreateSkillReq{
		Code: wbTestCodePrefix + "bad", Name: "x", ToolSchemaJSON: json.RawMessage(`{not json`), HandlerKey: "k",
	}); !IsValidation(err) {
		t.Errorf("非法 tool_schema 应为校验错误，实际 %v", err)
	}

	// 缺 handler_key → 校验错误
	if _, err := skillSvc.Create(ctx, dto.CreateSkillReq{
		Code: wbTestCodePrefix + "nohk", Name: "x", ToolSchemaJSON: wbToolSchema,
	}); !IsValidation(err) {
		t.Errorf("缺 handler_key 应为校验错误，实际 %v", err)
	}

	// inactive 不出现在 ListPublic
	if _, err := skillSvc.Update(ctx, s.ID, dto.UpdateSkillReq{Status: strptr("inactive")}); err != nil {
		t.Fatalf("更新 status 失败: %v", err)
	}
	pub, _, err := skillSvc.ListPublic(ctx, 0, 200)
	if err != nil {
		t.Fatalf("ListPublic 失败: %v", err)
	}
	for _, p := range pub {
		if p.Code == wbTestCodePrefix+"search" {
			t.Errorf("inactive skill 不应出现在用户端列表")
		}
	}
}

// TestPluginEncryptionAndSSRF 校验插件凭证加密(has_auth 不回明文) + 配置时 SSRF 拦截。
func TestPluginEncryptionAndSSRF(t *testing.T) {
	_, _, _, pluginSvc, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 带凭证创建 → has_auth=true，且响应无明文
	p, err := pluginSvc.Create(ctx, dto.CreatePluginReq{
		Code: wbTestCodePrefix + "weather", Name: "天气", ToolSchemaJSON: wbToolSchema,
		EndpointURL: "https://api.example.com/weather", AuthConfig: `{"header":"Authorization","value":"Bearer SECRET123"}`,
	})
	if err != nil {
		t.Fatalf("创建 plugin 失败: %v", err)
	}
	if !p.HasAuth {
		t.Errorf("配置了凭证应 has_auth=true")
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "SECRET123") || strings.Contains(string(raw), "auth_config") {
		t.Errorf("响应不得包含凭证明文/字段，实际: %s", raw)
	}

	// http（非 https）→ 拦截
	if _, err := pluginSvc.Create(ctx, dto.CreatePluginReq{
		Code: wbTestCodePrefix + "http", Name: "x", ToolSchemaJSON: wbToolSchema, EndpointURL: "http://api.example.com",
	}); !IsValidation(err) {
		t.Errorf("http 端点应被拦截，实际 %v", err)
	}

	// 内网 IP → 拦截
	for _, badURL := range []string{"https://127.0.0.1/x", "https://10.0.0.5/x", "https://192.168.1.1/x", "https://localhost/x", "https://169.254.169.254/x"} {
		if _, err := pluginSvc.Create(ctx, dto.CreatePluginReq{
			Code: wbTestCodePrefix + "ssrf", Name: "x", ToolSchemaJSON: wbToolSchema, EndpointURL: badURL,
		}); !IsValidation(err) {
			t.Errorf("内网/回环端点 %s 应被拦截，实际 %v", badURL, err)
		}
	}

	// ListPublic 不回 endpoint/凭证
	pub, _, err := pluginSvc.ListPublic(ctx, 0, 200)
	if err != nil {
		t.Fatalf("ListPublic 失败: %v", err)
	}
	rawPub, _ := json.Marshal(pub)
	if strings.Contains(string(rawPub), "example.com") || strings.Contains(string(rawPub), "endpoint") {
		t.Errorf("用户端插件列表不应包含 endpoint，实际: %s", rawPub)
	}
}

// TestAgentBindingsAndOwnership 校验 Agent 绑定回填名称 + 自建越权 + 仅可绑 active 官方资源。
func TestAgentBindingsAndOwnership(t *testing.T) {
	agentSvc, _, skillSvc, pluginSvc, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 准备 1 个 active skill + 1 个 inactive skill + 1 个 active plugin
	activeSkill, err := skillSvc.Create(ctx, dto.CreateSkillReq{Code: wbTestCodePrefix + "s1", Name: "搜索", ToolSchemaJSON: wbToolSchema, HandlerKey: "web_search"})
	if err != nil {
		t.Fatalf("建 active skill 失败: %v", err)
	}
	inactiveSkill, err := skillSvc.Create(ctx, dto.CreateSkillReq{Code: wbTestCodePrefix + "s2", Name: "下架技能", ToolSchemaJSON: wbToolSchema, HandlerKey: "doc_read", Status: "inactive"})
	if err != nil {
		t.Fatalf("建 inactive skill 失败: %v", err)
	}
	activePlugin, err := pluginSvc.Create(ctx, dto.CreatePluginReq{Code: wbTestCodePrefix + "p1", Name: "天气", ToolSchemaJSON: wbToolSchema, EndpointURL: "https://api.example.com/w"})
	if err != nil {
		t.Fatalf("建 active plugin 失败: %v", err)
	}

	// 管理端建官方 Agent 绑定 active skill + plugin → 详情含绑定名称
	official, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "a1", Name: "官方助手", SystemPrompt: "你是助手", DefaultModelCode: "gpt-4o",
		SkillIDs: []uint64{activeSkill.ID}, PluginIDs: []uint64{activePlugin.ID},
	})
	if err != nil {
		t.Fatalf("管理端建 Agent 失败: %v", err)
	}
	if len(official.Skills) != 1 || official.Skills[0].Name != "搜索" {
		t.Errorf("Agent 详情应回填绑定 skill 名称，实际 %+v", official.Skills)
	}
	if len(official.Plugins) != 1 || official.Plugins[0].Name != "天气" {
		t.Errorf("Agent 详情应回填绑定 plugin 名称，实际 %+v", official.Plugins)
	}

	// 绑定不存在的 skill → 校验错误
	if _, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "a2", Name: "x", SystemPrompt: "x", DefaultModelCode: "gpt-4o", SkillIDs: []uint64{99_999_999},
	}); !IsValidation(err) {
		t.Errorf("绑定不存在 skill 应为校验错误，实际 %v", err)
	}

	// 用户自建：绑 inactive skill → 拒绝（requireActive）
	if _, err := agentSvc.UserCreate(ctx, wbTestUserA, dto.UserCreateAgentReq{
		Name: "我的", SystemPrompt: "x", DefaultModelCode: "gpt-4o", SkillIDs: []uint64{inactiveSkill.ID},
	}); !IsValidation(err) {
		t.Errorf("自建绑 inactive skill 应被拒，实际 %v", err)
	}

	// 用户自建：绑 active skill → ok
	mine, err := agentSvc.UserCreate(ctx, wbTestUserA, dto.UserCreateAgentReq{
		Name: "我的助手", SystemPrompt: "x", DefaultModelCode: "gpt-4o", SkillIDs: []uint64{activeSkill.ID},
	})
	if err != nil {
		t.Fatalf("自建 Agent 失败: %v", err)
	}
	if mine.OwnerType != "user" || mine.OwnerUserID == nil || *mine.OwnerUserID != wbTestUserA {
		t.Errorf("自建 Agent owner 应为 user/%d，实际 %+v", wbTestUserA, mine)
	}

	// 用户 B 改/删 用户 A 的 Agent → ErrForbidden
	if _, err := agentSvc.UserUpdate(ctx, wbTestUserB, mine.ID, dto.UserUpdateAgentReq{Name: strptr("篡改")}); err != ErrForbidden {
		t.Errorf("越权改应返回 ErrForbidden，实际 %v", err)
	}
	if err := agentSvc.UserDelete(ctx, wbTestUserB, mine.ID); err != ErrForbidden {
		t.Errorf("越权删应返回 ErrForbidden，实际 %v", err)
	}

	// 用户 A 可见官方 active + 本人自建
	list, _, err := agentSvc.UserList(ctx, wbTestUserA, "", 0, 200)
	if err != nil {
		t.Fatalf("UserList 失败: %v", err)
	}
	var sawOfficial, sawMine bool
	for _, a := range list {
		if a.ID == official.ID {
			sawOfficial = true
		}
		if a.ID == mine.ID {
			sawMine = true
		}
	}
	if !sawOfficial || !sawMine {
		t.Errorf("用户列表应含官方 active(%v) + 本人自建(%v)", sawOfficial, sawMine)
	}

	// 用户 B 查 A 的私有 Agent → ErrForbidden
	if _, err := agentSvc.UserGet(ctx, wbTestUserB, mine.ID); err != ErrForbidden {
		t.Errorf("越权查他人自建应 ErrForbidden，实际 %v", err)
	}
}

func strptr(s string) *string { return &s }

// TestAgentCategory 校验分类字典列表（仅 active 且按 sort_order）+ agent 带分类校验/过滤/回显。
// 依赖迁移 000045 已 seed 4 类（office/study/business/entertainment）。
func TestAgentCategory(t *testing.T) {
	agentSvc, categorySvc, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 1) 用户端分类列表：仅 active，按 sort_order 升序。
	cats, err := categorySvc.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive 失败: %v", err)
	}
	if len(cats) < 4 {
		t.Fatalf("分类应至少 4 个（seed），实际 %d", len(cats))
	}
	for i := 1; i < len(cats); i++ {
		if cats[i].SortOrder < cats[i-1].SortOrder {
			t.Errorf("分类未按 sort_order 升序：%+v", cats)
		}
		if cats[i].Status != "active" {
			t.Errorf("用户端列表应仅含 active，实际 %q", cats[i].Status)
		}
	}
	// seed 头部应为 office=办公。
	if cats[0].Code != "office" || cats[0].Name != "办公" {
		t.Errorf("首个分类应为 office/办公，实际 %+v", cats[0])
	}

	// 2) 管理端全量列表（本期无 inactive，至少含 4 个 seed）。
	all, err := categorySvc.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll 失败: %v", err)
	}
	if len(all) < len(cats) {
		t.Errorf("管理端全量应 >= active 数量，实际 all=%d active=%d", len(all), len(cats))
	}

	// 3) 建带合法 category_code 的 Agent → 回显 category_code + category_name。
	a1, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "cat1", Name: "周报助手", SystemPrompt: "x", DefaultModelCode: "gpt-4o",
		CategoryCode: "office",
	})
	if err != nil {
		t.Fatalf("建带合法分类 Agent 失败: %v", err)
	}
	if a1.CategoryCode == nil || *a1.CategoryCode != "office" {
		t.Errorf("应回显 category_code=office，实际 %+v", a1.CategoryCode)
	}
	if a1.CategoryName != "办公" {
		t.Errorf("应联字典回显 category_name=办公，实际 %q", a1.CategoryName)
	}

	// 4) 建带非法 category_code 的 Agent → 校验错误（40000）。
	if _, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "cat2", Name: "x", SystemPrompt: "x", DefaultModelCode: "gpt-4o",
		CategoryCode: "not_exist_cat",
	}); !IsValidation(err) {
		t.Errorf("非法 category_code 应为校验错误，实际 %v", err)
	}

	// 5) 空 category_code 允许（未分类）→ category_code=nil。
	a3, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "cat3", Name: "未分类助手", SystemPrompt: "x", DefaultModelCode: "gpt-4o",
	})
	if err != nil {
		t.Fatalf("建未分类 Agent 失败: %v", err)
	}
	if a3.CategoryCode != nil {
		t.Errorf("未分类 Agent category_code 应为 nil，实际 %v", *a3.CategoryCode)
	}
	if a3.CategoryName != "" {
		t.Errorf("未分类 Agent category_name 应为空串，实际 %q", a3.CategoryName)
	}

	// 6) 另建 study 分类 Agent，按 category 过滤只回对应类。
	a4, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "cat4", Name: "学习助手", SystemPrompt: "x", DefaultModelCode: "gpt-4o",
		CategoryCode: "study",
	})
	if err != nil {
		t.Fatalf("建 study 分类 Agent 失败: %v", err)
	}
	officeList, _, err := agentSvc.AdminList(ctx, "official", "", "office", "", 0, 500)
	if err != nil {
		t.Fatalf("按 office 过滤失败: %v", err)
	}
	var sawCat1, sawCat4 bool
	for _, a := range officeList {
		if a.ID == a1.ID {
			sawCat1 = true
		}
		if a.ID == a4.ID {
			sawCat4 = true
		}
	}
	if !sawCat1 {
		t.Errorf("office 过滤应含 office 类 Agent")
	}
	if sawCat4 {
		t.Errorf("office 过滤不应含 study 类 Agent")
	}

	// 7) 更新：清空分类（category_code=""）→ 置为未分类。
	upd, err := agentSvc.AdminUpdate(ctx, a1.ID, dto.AdminUpdateAgentReq{CategoryCode: strptr("")})
	if err != nil {
		t.Fatalf("更新清空分类失败: %v", err)
	}
	if upd.CategoryCode != nil {
		t.Errorf("清空后 category_code 应为 nil，实际 %v", *upd.CategoryCode)
	}

	// 8) 更新：改为非法分类 → 校验错误。
	if _, err := agentSvc.AdminUpdate(ctx, a4.ID, dto.AdminUpdateAgentReq{CategoryCode: strptr("bad_cat")}); !IsValidation(err) {
		t.Errorf("更新为非法分类应校验错误，实际 %v", err)
	}

	// 9) 用户自建允许选分类（契约 §5）。
	mine, err := agentSvc.UserCreate(ctx, wbTestUserA, dto.UserCreateAgentReq{
		Name: "我的办公助手", SystemPrompt: "x", DefaultModelCode: "gpt-4o", CategoryCode: "business",
	})
	if err != nil {
		t.Fatalf("自建带分类 Agent 失败: %v", err)
	}
	if mine.CategoryCode == nil || *mine.CategoryCode != "business" || mine.CategoryName != "商务" {
		t.Errorf("自建应回显 business/商务，实际 code=%v name=%q", mine.CategoryCode, mine.CategoryName)
	}
}

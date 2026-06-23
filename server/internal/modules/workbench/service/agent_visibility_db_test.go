package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/workbench/dto"
)

// fakeGroupResolver 脚本化分组解析桩：userGroups[userID] = {group_id: group_role}。
type fakeGroupResolver struct {
	userGroups map[uint64]map[uint64]string
	existing   map[uint64]struct{}
	err        error // 非 nil 时模拟解析异常（验证 fail-safe）
}

func (f *fakeGroupResolver) UserGroupRoles(ctx context.Context, userID uint64) (map[uint64]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.userGroups[userID], nil
}

func (f *fakeGroupResolver) ExistingGroupIDs(ctx context.Context, ids []uint64) (map[uint64]struct{}, error) {
	out := make(map[uint64]struct{})
	for _, id := range ids {
		if _, ok := f.existing[id]; ok {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

// fakeRoleResolver 脚本化全局角色解析桩。
type fakeRoleResolver struct {
	userRoles map[uint64][]string
	existing  map[string]struct{}
	err       error
}

func (f *fakeRoleResolver) GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.userRoles[userID], nil
}

func (f *fakeRoleResolver) ExistingRoleCodes(ctx context.Context, codes []string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	for _, c := range codes {
		if _, ok := f.existing[c]; ok {
			out[c] = struct{}{}
		}
	}
	return out, nil
}

// 测试用分组/角色（高位 group_id 与已知角色 code）。
const (
	visGroupVIP uint64 = 9_900_200_010
	visGroupOps uint64 = 9_900_200_011
)

// userListVisible 判断 UserList 结果是否含某 agentID。
func userListHas(items []dto.AgentResp, agentID uint64) bool {
	for i := range items {
		if items[i].ID == agentID {
			return true
		}
	}
	return false
}

// TestAgentVisibility_Groups 校验 scope=groups：组内可见 / 组外不可见 / 组内角色细分。
func TestAgentVisibility_Groups(t *testing.T) {
	agentSvc, _, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	gr := &fakeGroupResolver{
		userGroups: map[uint64]map[uint64]string{
			// A：VIP 组管理员；B：VIP 组普通成员；其他用户无分组。
			wbTestUserA: {visGroupVIP: "admin"},
			wbTestUserB: {visGroupVIP: "member"},
		},
		existing: map[uint64]struct{}{visGroupVIP: {}, visGroupOps: {}},
	}
	rr := &fakeRoleResolver{existing: map[string]struct{}{}}
	agentSvc.WithResolvers(gr, rr)

	// ① scope=groups 不分组内角色：A、B 都属 VIP 组 → 可见；组外用户不可见。
	g1, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "g1", Name: "VIP助手", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "groups", GroupIDs: []uint64{visGroupVIP},
	})
	if err != nil {
		t.Fatalf("建 g1 失败: %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserA, g1.ID); err != nil {
		t.Errorf("组内成员 A 应可见 g1，得 %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserB, g1.ID); err != nil {
		t.Errorf("组内成员 B 应可见 g1，得 %v", err)
	}
	// 组外用户（无分组归属）不可见。
	const outsider uint64 = 9_900_200_099
	if _, err := agentSvc.UserGet(ctx, outsider, g1.ID); err != ErrForbidden {
		t.Errorf("组外用户应 40003 不可见 g1，得 %v", err)
	}

	// ② scope=groups + group_roles=[admin]：仅 A（admin）可见，B（member）不可见。
	g2, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "g2", Name: "组管理工具", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "groups", GroupIDs: []uint64{visGroupVIP}, GroupRoles: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("建 g2 失败: %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserA, g2.ID); err != nil {
		t.Errorf("组管理员 A 应可见 g2，得 %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserB, g2.ID); err != ErrForbidden {
		t.Errorf("普通组员 B 应 40003 不可见 g2，得 %v", err)
	}

	// 列表过滤一致性：B 的列表含 g1 不含 g2。
	listB, _, err := agentSvc.UserList(ctx, wbTestUserB, "", 0, 500)
	if err != nil {
		t.Fatalf("UserList(B) 失败: %v", err)
	}
	if !userListHas(listB, g1.ID) {
		t.Error("B 列表应含 g1")
	}
	if userListHas(listB, g2.ID) {
		t.Error("B 列表不应含 g2（仅 admin 可见）")
	}
}

// TestAgentVisibility_Roles 校验 scope=roles：命中全局角色可见，未命中不可见。
func TestAgentVisibility_Roles(t *testing.T) {
	agentSvc, _, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	gr := &fakeGroupResolver{existing: map[uint64]struct{}{}}
	rr := &fakeRoleResolver{
		userRoles: map[uint64][]string{
			wbTestUserA: {"vip"},
			wbTestUserB: {"user"},
		},
		existing: map[string]struct{}{"vip": {}, "merchant": {}},
	}
	agentSvc.WithResolvers(gr, rr)

	r1, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "r1", Name: "VIP角色助手", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "roles", RoleCodes: []string{"vip", "merchant"},
	})
	if err != nil {
		t.Fatalf("建 r1 失败: %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserA, r1.ID); err != nil {
		t.Errorf("vip 用户 A 应可见 r1，得 %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserB, r1.ID); err != ErrForbidden {
		t.Errorf("非命中角色用户 B 应 40003 不可见 r1，得 %v", err)
	}
}

// TestAgentVisibility_AllAndOwn 校验 scope=all 全员可见（回归）+ 本人自建恒可见。
func TestAgentVisibility_AllAndOwn(t *testing.T) {
	agentSvc, _, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 无 resolver（nil）：scope=all 仍正常可见，定向 Agent 不可见（fail-safe，见下个用例）。
	agentSvc.WithResolvers(nil, nil)

	all, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "all", Name: "全员助手", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "all",
	})
	if err != nil {
		t.Fatalf("建 all 失败: %v", err)
	}
	// 任意用户可见。
	for _, uid := range []uint64{wbTestUserA, wbTestUserB, 9_900_200_098} {
		if _, err := agentSvc.UserGet(ctx, uid, all.ID); err != nil {
			t.Errorf("scope=all 应对用户 %d 可见，得 %v", uid, err)
		}
	}

	// 本人自建恒可见（不受 scope 影响；自建 owner_type=user）。
	mine, err := agentSvc.UserCreate(ctx, wbTestUserA, dto.UserCreateAgentReq{
		Name: "我的助手", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("自建失败: %v", err)
	}
	if _, err := agentSvc.UserGet(ctx, wbTestUserA, mine.ID); err != nil {
		t.Errorf("本人自建应恒可见，得 %v", err)
	}
}

// TestAgentVisibility_FailSafe 校验 resolver 异常/为 nil 时定向 Agent 不泄漏。
func TestAgentVisibility_FailSafe(t *testing.T) {
	agentSvc, _, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	// 先用正常 resolver 建定向 Agent（写入侧需校验存在性）。
	grOK := &fakeGroupResolver{
		userGroups: map[uint64]map[uint64]string{wbTestUserA: {visGroupVIP: "admin"}},
		existing:   map[uint64]struct{}{visGroupVIP: {}},
	}
	rrOK := &fakeRoleResolver{existing: map[string]struct{}{"vip": {}}}
	agentSvc.WithResolvers(grOK, rrOK)

	gAgent, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "fs_g", Name: "组定向", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "groups", GroupIDs: []uint64{visGroupVIP},
	})
	if err != nil {
		t.Fatalf("建 fs_g 失败: %v", err)
	}

	// 切换为异常 resolver：A 原本可见的组定向 Agent 现在判不可见（不误放）。
	agentSvc.WithResolvers(
		&fakeGroupResolver{err: context.DeadlineExceeded, existing: map[uint64]struct{}{visGroupVIP: {}}},
		&fakeRoleResolver{err: context.DeadlineExceeded},
	)
	if _, err := agentSvc.UserGet(ctx, wbTestUserA, gAgent.ID); err != ErrForbidden {
		t.Errorf("resolver 异常时组定向 Agent 应 40003 不泄漏，得 %v", err)
	}

	// 列表也不应出现该定向 Agent。
	list, _, err := agentSvc.UserList(ctx, wbTestUserA, "", 0, 500)
	if err != nil {
		t.Fatalf("UserList 失败: %v", err)
	}
	if userListHas(list, gAgent.ID) {
		t.Error("resolver 异常时定向 Agent 不应出现在列表")
	}
}

// TestAgentVisibility_WriteValidation 校验写入侧校验（契约 §5.3）。
func TestAgentVisibility_WriteValidation(t *testing.T) {
	agentSvc, _, _, _, clean := setupWBTest(t)
	defer clean()
	ctx := context.Background()

	gr := &fakeGroupResolver{existing: map[uint64]struct{}{visGroupVIP: {}}}
	rr := &fakeRoleResolver{existing: map[string]struct{}{"vip": {}}}
	agentSvc.WithResolvers(gr, rr)

	base := func() dto.AdminCreateAgentReq {
		return dto.AdminCreateAgentReq{Name: "x", SystemPrompt: "p", DefaultModelCode: "deepseek-chat"}
	}
	cases := []struct {
		name string
		mut  func(*dto.AdminCreateAgentReq)
	}{
		{"scope非法", func(r *dto.AdminCreateAgentReq) { r.VisibleScope = "wat" }},
		{"members预留拒绝", func(r *dto.AdminCreateAgentReq) { r.VisibleScope = "members" }},
		{"groups缺group_ids", func(r *dto.AdminCreateAgentReq) { r.VisibleScope = "groups" }},
		{"group_roles非法值", func(r *dto.AdminCreateAgentReq) {
			r.VisibleScope = "groups"
			r.GroupIDs = []uint64{visGroupVIP}
			r.GroupRoles = []string{"boss"}
		}},
		{"group_ids不存在", func(r *dto.AdminCreateAgentReq) {
			r.VisibleScope = "groups"
			r.GroupIDs = []uint64{visGroupVIP, 9_900_299_999}
		}},
		{"roles缺role_codes", func(r *dto.AdminCreateAgentReq) { r.VisibleScope = "roles" }},
		{"role_codes不存在", func(r *dto.AdminCreateAgentReq) {
			r.VisibleScope = "roles"
			r.RoleCodes = []string{"vip", "ghost"}
		}},
	}
	for _, c := range cases {
		req := base()
		c.mut(&req)
		if _, err := agentSvc.AdminCreate(ctx, req); !IsValidation(err) {
			t.Errorf("[%s] 期望校验错误(40000)，得 %v", c.name, err)
		}
	}
}

// TestAgentVisibility_ChatGuard 校验编排端点 ChatWithAgent 接入可见性（安全红线）：越权直连 → 40003。
func TestAgentVisibility_ChatGuard(t *testing.T) {
	gdb, agentSvc, _, clean := setupChatTest(t)
	defer clean()
	ctx := context.Background()

	gr := &fakeGroupResolver{
		userGroups: map[uint64]map[uint64]string{wbTestUserA: {visGroupVIP: "admin"}},
		existing:   map[uint64]struct{}{visGroupVIP: {}},
	}
	rr := &fakeRoleResolver{existing: map[string]struct{}{}}
	agentSvc.WithResolvers(gr, rr)

	agent, err := agentSvc.AdminCreate(ctx, dto.AdminCreateAgentReq{
		Code: wbTestCodePrefix + "chatvis", Name: "组定向编排", SystemPrompt: "p", DefaultModelCode: "deepseek-chat",
		VisibleScope: "groups", GroupIDs: []uint64{visGroupVIP},
	})
	if err != nil {
		t.Fatalf("建 agent 失败: %v", err)
	}

	chatSvc := buildChatService(gdb, &fakeUpstream{}, 3).WithResolvers(gr, rr)

	// 组外用户 B（无分组）直连编排 → ErrForbidden（未写出任何内容）。
	w := httptest.NewRecorder()
	err = chatSvc.ChatWithAgent(ctx, w, ChatRequest{
		AgentID: agent.ID, UserID: wbTestUserB, RequestID: "vis-test",
		Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)},
	})
	if err != ErrForbidden {
		t.Errorf("组外用户编排越权应返回 ErrForbidden(40003)，得 %v", err)
	}

	// 组内用户 A 可进入编排（fakeUpstream 兜底返回最终答案，不报 40003）。
	w2 := httptest.NewRecorder()
	err = chatSvc.ChatWithAgent(ctx, w2, ChatRequest{
		AgentID: agent.ID, UserID: wbTestUserA, RequestID: "vis-test2",
		Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)},
	})
	if err == ErrForbidden {
		t.Error("组内用户 A 不应被拒，得 ErrForbidden")
	}
}

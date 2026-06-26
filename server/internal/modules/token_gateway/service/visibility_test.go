package service

import (
	"context"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
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

// buildModel 用给定 scope + audience 原文构造一个 active 模型。
func buildModel(scope string, audience *string) *model.TokenModel {
	return &model.TokenModel{LogicalModelCode: "gpt-4o", Status: "active", VisibleScope: scope, TargetAudience: audience}
}

// TestBuildVisibility_All 空 scope 与显式 all 均归一化为 all + nil 定向。
func TestBuildVisibility_All(t *testing.T) {
	ctx := context.Background()
	for _, in := range []visibilityInput{{Scope: ""}, {Scope: "all"}} {
		scope, audience, err := buildVisibility(ctx, in, nil, nil)
		if err != nil {
			t.Fatalf("scope=%q 不应报错: %v", in.Scope, err)
		}
		if scope != scopeAll || audience != nil {
			t.Fatalf("scope=%q 应归一化为 all+nil，实际 scope=%q audience=%v", in.Scope, scope, audience)
		}
	}
}

// TestBuildVisibility_GroupsValidation 校验 groups 写入侧校验：空 group_ids / 非法组内角色 / 不存在分组 / resolver 缺失。
func TestBuildVisibility_GroupsValidation(t *testing.T) {
	ctx := context.Background()
	gr := &fakeGroupResolver{existing: map[uint64]struct{}{10: {}, 11: {}}}

	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "groups"}, gr, nil); err == nil {
		t.Fatal("空 group_ids 应报校验错误")
	}
	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{10}, GroupRoles: []string{"boss"}}, gr, nil); err == nil {
		t.Fatal("非法 group_roles 应报校验错误")
	}
	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{99}}, gr, nil); err == nil {
		t.Fatal("不存在的 group_id 应报校验错误")
	}
	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{10}}, nil, nil); err == nil {
		t.Fatal("groups 但 resolver 为 nil 应报校验错误")
	}
	// 合法：去重 + 序列化。
	scope, audience, err := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{10, 10, 11}, GroupRoles: []string{"admin"}}, gr, nil)
	if err != nil || scope != scopeGroups || audience == nil {
		t.Fatalf("合法 groups 配置应成功，err=%v scope=%q audience=%v", err, scope, audience)
	}
}

// TestBuildVisibility_RolesValidation 校验 roles 写入侧校验：空 role_codes / 不存在角色 / resolver 缺失。
func TestBuildVisibility_RolesValidation(t *testing.T) {
	ctx := context.Background()
	rr := &fakeRoleResolver{existing: map[string]struct{}{"vip": {}}}

	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "roles"}, nil, rr); err == nil {
		t.Fatal("空 role_codes 应报校验错误")
	}
	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "roles", RoleCodes: []string{"ghost"}}, nil, rr); err == nil {
		t.Fatal("不存在的角色 code 应报校验错误")
	}
	if _, _, err := buildVisibility(ctx, visibilityInput{Scope: "roles", RoleCodes: []string{"vip"}}, nil, nil); err == nil {
		t.Fatal("roles 但 resolver 为 nil 应报校验错误")
	}
	scope, audience, err := buildVisibility(ctx, visibilityInput{Scope: "roles", RoleCodes: []string{"vip"}}, nil, rr)
	if err != nil || scope != scopeRoles || audience == nil {
		t.Fatalf("合法 roles 配置应成功，err=%v scope=%q audience=%v", err, scope, audience)
	}
}

// TestBuildVisibility_RejectReserved members/users 预留 + 未知 scope 均拒绝。
func TestBuildVisibility_RejectReserved(t *testing.T) {
	ctx := context.Background()
	for _, s := range []string{"members", "users", "bogus"} {
		if _, _, err := buildVisibility(ctx, visibilityInput{Scope: s}, nil, nil); err == nil {
			t.Fatalf("scope=%q 应被拒绝", s)
		}
	}
}

// TestModelVisibleTo_All scope=all 对任意用户（含无分组无角色）恒可见。
func TestModelVisibleTo_All(t *testing.T) {
	ctx := context.Background()
	m := buildModel("all", nil)
	if !modelVisibleTo(ctx, m, 1, nil, nil) {
		t.Fatal("scope=all 应对所有人可见")
	}
	// 空 scope（历史数据）等同 all。
	if !modelVisibleTo(ctx, buildModel("", nil), 1, nil, nil) {
		t.Fatal("空 scope 应等同 all")
	}
}

// TestModelVisibleTo_Groups 组内可见 / 组外不可见 / 组内角色细分 / resolver 缺失 fail-safe。
func TestModelVisibleTo_Groups(t *testing.T) {
	ctx := context.Background()
	gr := &fakeGroupResolver{
		userGroups: map[uint64]map[uint64]string{
			1: {10: "member"}, // 用户1 是组10的 member
			2: {10: "admin"},  // 用户2 是组10的 admin
			3: {20: "member"}, // 用户3 在组20
		},
		existing: map[uint64]struct{}{10: {}, 20: {}},
	}
	// 定向组10，不限组内角色。
	_, aud, _ := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{10}}, gr, nil)
	m := buildModel("groups", aud)
	if !modelVisibleTo(ctx, m, 1, gr, nil) || !modelVisibleTo(ctx, m, 2, gr, nil) {
		t.Fatal("组10成员应可见")
	}
	if modelVisibleTo(ctx, m, 3, gr, nil) {
		t.Fatal("组20成员不应看到定向组10的模型")
	}
	// resolver 为 nil → fail-safe 不可见。
	if modelVisibleTo(ctx, m, 1, nil, nil) {
		t.Fatal("resolver 缺失时 groups 定向应不可见（fail-safe）")
	}

	// 定向组10且仅组内 admin 可见。
	_, aud2, _ := buildVisibility(ctx, visibilityInput{Scope: "groups", GroupIDs: []uint64{10}, GroupRoles: []string{"admin"}}, gr, nil)
	m2 := buildModel("groups", aud2)
	if modelVisibleTo(ctx, m2, 1, gr, nil) {
		t.Fatal("组内 member 不应看到仅 admin 可见的模型")
	}
	if !modelVisibleTo(ctx, m2, 2, gr, nil) {
		t.Fatal("组内 admin 应可见")
	}
}

// TestModelVisibleTo_Roles 角色命中可见 / 未命中不可见 / resolver 出错 fail-safe。
func TestModelVisibleTo_Roles(t *testing.T) {
	ctx := context.Background()
	rr := &fakeRoleResolver{
		userRoles: map[uint64][]string{1: {"vip", "user"}, 2: {"user"}},
		existing:  map[string]struct{}{"vip": {}},
	}
	_, aud, _ := buildVisibility(ctx, visibilityInput{Scope: "roles", RoleCodes: []string{"vip"}}, nil, rr)
	m := buildModel("roles", aud)
	if !modelVisibleTo(ctx, m, 1, nil, rr) {
		t.Fatal("拥有 vip 角色应可见")
	}
	if modelVisibleTo(ctx, m, 2, nil, rr) {
		t.Fatal("无 vip 角色不应可见")
	}
	// resolver 出错 → fail-safe 不可见。
	errRR := &fakeRoleResolver{err: context.DeadlineExceeded}
	if modelVisibleTo(ctx, m, 1, nil, errRR) {
		t.Fatal("resolver 出错时应 fail-safe 不可见")
	}
}

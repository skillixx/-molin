package service

import (
	"context"
	"encoding/json"
	"strings"

	"molin/server/internal/modules/token_gateway/model"
)

// 模型目录定向可见性逻辑，与 workbench Agent 的 visible_scope 模式完全对齐：
//   - all     所有登录用户可见
//   - groups  按分组（可细到组内 admin/member 角色）
//   - roles   按全局 IAM 角色 code
// members/users 预留未启用。

// visible_scope 取值。
const (
	scopeAll    = "all"
	scopeGroups = "groups"
	scopeRoles  = "roles"
)

// 组内角色合法取值（user_group_members.group_role 现有取值）。
var validGroupRoles = map[string]bool{"admin": true, "member": true}

// GroupResolver 解析用户的分组归属：group_id → group_role（admin/member），并校验分组存在性。
// nil 时定向 groups 的模型一律判为不可见（fail-safe，绝不泄漏定向模型）。
type GroupResolver interface {
	UserGroupRoles(ctx context.Context, userID uint64) (map[uint64]string, error)
	// ExistingGroupIDs 返回入参中真实存在的 group_id 集合（写入侧校验定向分组是否存在）。
	ExistingGroupIDs(ctx context.Context, groupIDs []uint64) (map[uint64]struct{}, error)
}

// RoleResolver 解析用户的全局 IAM 角色 code 列表，并校验角色存在性。
// nil 时定向 roles 的模型一律判为不可见（fail-safe）。
type RoleResolver interface {
	GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error)
	// ExistingRoleCodes 返回入参中真实存在的角色 code 集合（写入侧校验定向角色是否存在）。
	ExistingRoleCodes(ctx context.Context, codes []string) (map[string]struct{}, error)
}

// targetAudience 定向目标的解析结构，按 visible_scope 解释。
type targetAudience struct {
	GroupIDs   []uint64 `json:"group_ids,omitempty"`
	GroupRoles []string `json:"group_roles,omitempty"`
	RoleCodes  []string `json:"role_codes,omitempty"`
}

// parseTargetAudience 解析 token_models.target_audience_json 原文（nil/空 → 空结构）。
func parseTargetAudience(raw *string) (targetAudience, error) {
	var ta targetAudience
	if raw == nil {
		return ta, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" || s == "null" {
		return ta, nil
	}
	if err := json.Unmarshal([]byte(s), &ta); err != nil {
		return ta, err
	}
	return ta, nil
}

// visibilityInput 写入侧的定向参数（从 admin create/update DTO 收集）。
type visibilityInput struct {
	Scope      string
	GroupIDs   []uint64
	GroupRoles []string
	RoleCodes  []string
}

// buildVisibility 校验并组装定向配置，返回归一化 scope + target_audience_json（*string，scope=all 为 nil）。
// 校验：
//   - scope 非 all/groups/roles → 校验错误
//   - groups 但 group_ids 空 → 校验错误；group_roles 含非 admin/member → 校验错误；group_ids 含不存在分组 → 校验错误
//   - roles 但 role_codes 空 → 校验错误；role_codes 含不存在角色 → 校验错误
func buildVisibility(ctx context.Context, in visibilityInput, gr GroupResolver, rr RoleResolver) (string, *string, error) {
	scope := strings.TrimSpace(in.Scope)
	switch scope {
	case "", scopeAll:
		// 空 scope 视为 all（创建时不传 visible_scope 的默认行为）。
		return scopeAll, nil, nil
	case scopeGroups:
		ids := dedupUint64(in.GroupIDs)
		if len(ids) == 0 {
			return "", nil, newValidation("visible_scope=groups 时 group_ids 不能为空")
		}
		for _, role := range in.GroupRoles {
			if !validGroupRoles[role] {
				return "", nil, newValidation("group_roles 仅支持 admin/member")
			}
		}
		if gr == nil {
			return "", nil, newValidation("分组定向当前不可用（分组解析器未启用）")
		}
		existing, err := gr.ExistingGroupIDs(ctx, ids)
		if err != nil {
			return "", nil, err
		}
		for _, gid := range ids {
			if _, ok := existing[gid]; !ok {
				return "", nil, newValidation("group_ids 含不存在的分组")
			}
		}
		ta := targetAudience{GroupIDs: ids, GroupRoles: dedupStrings(in.GroupRoles)}
		return marshalAudience(scope, ta)
	case scopeRoles:
		codes := dedupStrings(in.RoleCodes)
		if len(codes) == 0 {
			return "", nil, newValidation("visible_scope=roles 时 role_codes 不能为空")
		}
		if rr == nil {
			return "", nil, newValidation("角色定向当前不可用（角色解析器未启用）")
		}
		existing, err := rr.ExistingRoleCodes(ctx, codes)
		if err != nil {
			return "", nil, err
		}
		for _, c := range codes {
			if _, ok := existing[c]; !ok {
				return "", nil, newValidation("role_codes 含不存在的角色")
			}
		}
		ta := targetAudience{RoleCodes: codes}
		return marshalAudience(scope, ta)
	case "members", "users":
		return "", nil, newValidation("暂不支持的 visible_scope（members/users 预留未启用）")
	default:
		return "", nil, newValidation("visible_scope 仅支持 all/groups/roles")
	}
}

// marshalAudience 序列化 target_audience。
func marshalAudience(scope string, ta targetAudience) (string, *string, error) {
	b, err := json.Marshal(ta)
	if err != nil {
		return "", nil, err
	}
	s := string(b)
	return scope, &s, nil
}

// audienceForResp 把 target_audience_json 原文解析为响应结构（scope=all → nil）。
func audienceForResp(scope string, raw *string) *targetAudience {
	if scope == "" || scope == scopeAll {
		return nil
	}
	ta, err := parseTargetAudience(raw)
	if err != nil {
		return nil
	}
	return &ta
}

// modelVisibleTo 判定模型对用户 U 是否可见（供用户端列表过滤 + 转发前置闸复用）。
// 调用方负责 active 前置；本函数只判 scope 维度：
//   - all                          → 可见
//   - groups（含组内角色）          → 用户属任一目标分组（group_roles 非空时还须组内角色命中）
//   - roles                        → 用户全局角色 ∩ role_codes ≠ ∅
//   - 其他（members/users/未知脏数据） → 不可见
//
// fail-safe：resolver 为 nil 或出错时，groups/roles 一律不可见（绝不泄漏定向模型）。
func modelVisibleTo(ctx context.Context, m *model.TokenModel, userID uint64, gr GroupResolver, rr RoleResolver) bool {
	switch m.VisibleScope {
	case "", scopeAll:
		return true
	case scopeGroups:
		return groupHit(ctx, m.TargetAudience, userID, gr)
	case scopeRoles:
		return roleHit(ctx, m.TargetAudience, userID, rr)
	default:
		return false
	}
}

// groupHit 判定用户是否命中 groups 定向（分组 + 可选组内角色）。
func groupHit(ctx context.Context, raw *string, userID uint64, gr GroupResolver) bool {
	if gr == nil {
		return false // fail-safe
	}
	ta, err := parseTargetAudience(raw)
	if err != nil || len(ta.GroupIDs) == 0 {
		return false
	}
	userGroups, err := gr.UserGroupRoles(ctx, userID)
	if err != nil || len(userGroups) == 0 {
		return false // fail-safe / 用户无分组
	}
	targetGroups := make(map[uint64]struct{}, len(ta.GroupIDs))
	for _, gid := range ta.GroupIDs {
		targetGroups[gid] = struct{}{}
	}
	roleFilter := make(map[string]struct{}, len(ta.GroupRoles))
	for _, r := range ta.GroupRoles {
		roleFilter[r] = struct{}{}
	}
	for gid, role := range userGroups {
		if _, inTarget := targetGroups[gid]; !inTarget {
			continue
		}
		if len(roleFilter) == 0 {
			return true // 属任一目标组即可（不分组内角色）
		}
		if _, ok := roleFilter[role]; ok {
			return true // 在目标组内且组内角色匹配
		}
	}
	return false
}

// roleHit 判定用户全局角色是否命中 roles 定向。
func roleHit(ctx context.Context, raw *string, userID uint64, rr RoleResolver) bool {
	if rr == nil {
		return false // fail-safe
	}
	ta, err := parseTargetAudience(raw)
	if err != nil || len(ta.RoleCodes) == 0 {
		return false
	}
	codes, err := rr.GetUserRoleCodes(ctx, userID)
	if err != nil || len(codes) == 0 {
		return false // fail-safe / 用户无角色
	}
	want := make(map[string]struct{}, len(ta.RoleCodes))
	for _, c := range ta.RoleCodes {
		want[c] = struct{}{}
	}
	for _, c := range codes {
		if _, ok := want[c]; ok {
			return true
		}
	}
	return false
}

// dedupUint64 去重 uint64 切片（保持稳定）。
func dedupUint64(in []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(in))
	out := make([]uint64, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// dedupStrings 去重字符串切片（保持稳定）。
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

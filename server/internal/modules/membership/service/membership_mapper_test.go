package service

import (
	"testing"
	"time"

	"molin/server/internal/modules/membership/model"
)

// TestMapMembershipResponse_WithLevel 验证 M2 映射：内联 level_code/level_name，
// 同时保留原 level_id 等字段（纯增量，不破坏既有契约）。
func TestMapMembershipResponse_WithLevel(t *testing.T) {
	now := time.Now()
	assetID := uint64(2)
	m := &model.UserMembership{
		ID:        1,
		UserID:    10,
		LevelID:   5,
		AssetID:   &assetID,
		Status:    "active",
		StartedAt: now,
	}
	level := &model.MembershipLevel{ID: 5, LevelCode: "vip", Name: "黄金会员"}

	resp := mapMembershipResponse(m, level)
	if resp.LevelID != 5 {
		t.Fatalf("level_id 必须保留，期望 5，实际 %d", resp.LevelID)
	}
	if resp.LevelCode != "vip" || resp.LevelName != "黄金会员" {
		t.Fatalf("应内联等级名，实际 level_code=%q level_name=%q", resp.LevelCode, resp.LevelName)
	}
	if resp.UserID != 10 || resp.Status != "active" || resp.AssetID == nil || *resp.AssetID != 2 {
		t.Fatalf("原字段必须原样保留，实际 %+v", resp)
	}
}

// TestMapMembershipResponse_NilLevel 验证等级加载失败时等级名留空，但会员信息仍正常返回。
func TestMapMembershipResponse_NilLevel(t *testing.T) {
	m := &model.UserMembership{ID: 1, UserID: 10, LevelID: 99, Status: "active"}
	resp := mapMembershipResponse(m, nil)
	if resp.LevelID != 99 {
		t.Fatalf("level_id 必须保留，期望 99，实际 %d", resp.LevelID)
	}
	if resp.LevelCode != "" || resp.LevelName != "" {
		t.Fatalf("等级缺失时等级名应留空，实际 level_code=%q level_name=%q", resp.LevelCode, resp.LevelName)
	}
}

// TestCollectLevelIDs_Dedup 验证收集 level_id 时去重（保证批量查询不重复、规模为去重后等级数）。
func TestCollectLevelIDs_Dedup(t *testing.T) {
	list := []*model.UserMembership{
		{ID: 1, LevelID: 1},
		{ID: 2, LevelID: 2},
		{ID: 3, LevelID: 1}, // 重复 level_id=1
		{ID: 4, LevelID: 2}, // 重复 level_id=2
	}
	ids := collectLevelIDs(list)
	if len(ids) != 2 {
		t.Fatalf("去重后应为 2 个 level_id，实际 %d 个：%v", len(ids), ids)
	}
	seen := map[uint64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("收集的 level_id 出现重复：%v", ids)
		}
		seen[id] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("应包含 level_id 1 和 2，实际 %v", ids)
	}
}

// TestCollectLevelIDs_Empty 验证空列表返回空切片（不会触发 IN () 查询）。
func TestCollectLevelIDs_Empty(t *testing.T) {
	if ids := collectLevelIDs(nil); len(ids) != 0 {
		t.Fatalf("空列表应返回空切片，实际 %v", ids)
	}
}

// TestMapAdminUserMembershipItems 验证 M9 列表映射：内联等级名、保留全部原字段、缺失等级兜底为空。
// 同时证明仅依赖预先批量构建的 levelMap（无逐条查库 = 无 N+1）。
func TestMapAdminUserMembershipItems(t *testing.T) {
	now := time.Now()
	list := []*model.UserMembership{
		{ID: 1, UserID: 10, LevelID: 1, Status: "active", StartedAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: 2, UserID: 11, LevelID: 2, Status: "expired", StartedAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: 3, UserID: 12, LevelID: 99, Status: "active", StartedAt: now, CreatedAt: now, UpdatedAt: now}, // 等级缺失
	}
	levelMap := map[uint64]*model.MembershipLevel{
		1: {ID: 1, LevelCode: "vip", Name: "黄金会员"},
		2: {ID: 2, LevelCode: "svip", Name: "白金会员"},
	}

	items := mapAdminUserMembershipItems(list, levelMap)
	if len(items) != 3 {
		t.Fatalf("应映射 3 条，实际 %d", len(items))
	}
	if items[0].LevelCode != "vip" || items[0].LevelName != "黄金会员" {
		t.Fatalf("第 1 条应内联黄金会员，实际 %+v", items[0])
	}
	if items[1].LevelCode != "svip" || items[1].LevelName != "白金会员" {
		t.Fatalf("第 2 条应内联白金会员，实际 %+v", items[1])
	}
	// 缺失等级兜底：等级名为空，但 level_id 等原字段保留。
	if items[2].LevelCode != "" || items[2].LevelName != "" {
		t.Fatalf("缺失等级时等级名应留空，实际 %+v", items[2])
	}
	if items[2].LevelID != 99 || items[2].UserID != 12 {
		t.Fatalf("原字段必须保留，实际 %+v", items[2])
	}
}

// TestBuildLevelMap 验证等级切片正确构建为 id→level 映射。
func TestBuildLevelMap(t *testing.T) {
	levels := []*model.MembershipLevel{
		{ID: 1, LevelCode: "vip", Name: "黄金会员"},
		{ID: 2, LevelCode: "svip", Name: "白金会员"},
	}
	m := buildLevelMap(levels)
	if len(m) != 2 {
		t.Fatalf("映射应含 2 个等级，实际 %d", len(m))
	}
	if m[1].Name != "黄金会员" || m[2].Name != "白金会员" {
		t.Fatalf("映射内容错误：%+v", m)
	}
}

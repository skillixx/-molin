package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

// uint64Ptr 返回 uint64 指针，便于构造测试用例。
func uint64Ptr(v uint64) *uint64 {
	return &v
}

// TestMembershipResponseAssetIDKeyAlwaysPresent 验证 D-MBI-01 修复：
// MembershipResponse.AssetID 去掉 omitempty 后，nil 时序列化为 "asset_id":null（key 恒在），
// 非 nil 时序列化为对应数值。
func TestMembershipResponseAssetIDKeyAlwaysPresent(t *testing.T) {
	// AssetID = nil → 期望含 "asset_id":null
	nilResp := MembershipResponse{ID: 1, UserID: 10, AssetID: nil}
	nilBytes, err := json.Marshal(nilResp)
	if err != nil {
		t.Fatalf("marshal nil AssetID 失败: %v", err)
	}
	if !strings.Contains(string(nilBytes), `"asset_id":null`) {
		t.Errorf("AssetID=nil 时应含 \"asset_id\":null，实际: %s", nilBytes)
	}

	// AssetID = 2 → 期望含 "asset_id":2
	valResp := MembershipResponse{ID: 1, UserID: 10, AssetID: uint64Ptr(2)}
	valBytes, err := json.Marshal(valResp)
	if err != nil {
		t.Fatalf("marshal 非空 AssetID 失败: %v", err)
	}
	if !strings.Contains(string(valBytes), `"asset_id":2`) {
		t.Errorf("AssetID=2 时应含 \"asset_id\":2，实际: %s", valBytes)
	}
}

// TestAdminUserMembershipResponseAssetIDKeyAlwaysPresent 验证 D-MBI-01 修复：
// AdminUserMembershipResponse.AssetID 同样去掉 omitempty，nil 序列化为 null。
func TestAdminUserMembershipResponseAssetIDKeyAlwaysPresent(t *testing.T) {
	// AssetID = nil → 期望含 "asset_id":null
	nilResp := AdminUserMembershipResponse{ID: 1, UserID: 10, AssetID: nil}
	nilBytes, err := json.Marshal(nilResp)
	if err != nil {
		t.Fatalf("marshal nil AssetID 失败: %v", err)
	}
	if !strings.Contains(string(nilBytes), `"asset_id":null`) {
		t.Errorf("AssetID=nil 时应含 \"asset_id\":null，实际: %s", nilBytes)
	}

	// AssetID = 2 → 期望含 "asset_id":2
	valResp := AdminUserMembershipResponse{ID: 1, UserID: 10, AssetID: uint64Ptr(2)}
	valBytes, err := json.Marshal(valResp)
	if err != nil {
		t.Fatalf("marshal 非空 AssetID 失败: %v", err)
	}
	if !strings.Contains(string(valBytes), `"asset_id":2`) {
		t.Errorf("AssetID=2 时应含 \"asset_id\":2，实际: %s", valBytes)
	}
}

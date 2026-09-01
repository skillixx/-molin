package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	iammodel "molin/server/internal/modules/iam/model"
	membermodel "molin/server/internal/modules/membership/model"
	productmodel "molin/server/internal/modules/product/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 多条授权是完整路径的或关系：每条权益/会员先与自己的父资产相交，不能跨行拼凑永久权限。
func TestVideoG6MediaDeleteEntitlementExpiryPathsMySQL(t *testing.T) {
	for _, kind := range []string{"entitlement", "membership"} {
		for _, mode := range []string{"expires", "parent_expires", "permanent_alternative", "finite_alternative", "crossed_parents"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				f := NewVideoContentHTTPFixture(t)
				id := f.CreateCompletedForKey(f.ProjectID)
				var root model.AIImageAsset
				if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
					t.Fatal(err)
				}
				p := productmodel.Product{ProductType: "token", ProductCode: fmt.Sprintf("g6-delete-access-%d", f.ProjectID), Name: "合成删除授权商品", Status: "active"}
				if err := f.DB.Create(&p).Error; err != nil {
					t.Fatal(err)
				}
				r := iammodel.Role{Code: fmt.Sprintf("g6-delete-access-%d", f.ProjectID), Name: "合成删除授权角色"}
				if err := f.DB.Create(&r).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.DB.Exec("INSERT INTO user_roles(user_id,role_id) VALUES(?,?)", f.ProjectID, r.ID).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.DB.Exec("INSERT INTO product_role_access(product_id,role_id,can_view,can_buy,can_use) VALUES(?,?,1,1,1)", p.ID, r.ID).Error; err != nil {
					t.Fatal(err)
				}
				level := membermodel.MembershipLevel{LevelCode: fmt.Sprintf("g6-delete-access-%d", f.ProjectID), Name: "合成删除授权会员", Status: "active"}
				if err := f.DB.Create(&level).Error; err != nil {
					t.Fatal(err)
				}
				contract, err := ParseVideoModelContract([]byte(videoG6NoEntitlementContract), nil)
				if err != nil {
					t.Fatal(err)
				}
				productJSON := "null"
				if kind == "entitlement" {
					contract.AssetRequired = true
					value := "video_access"
					contract.RequiredEntitlementType = &value
					productJSON = fmt.Sprint(p.ID)
				} else {
					contract.RequiredMembershipLevels = []uint64{level.ID}
				}
				raw, err := json.Marshal(contract)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.DB.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("UPDATE ai_model_release_versions SET status='retired' WHERE model_id=? AND status='active'", f.ProjectID).Error; err != nil {
						return err
					}
					if err := tx.Exec("INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) SELECT model_id,2,'active',JSON_SET(snapshot_json,'$.video_contract',CAST(? AS JSON),'$.product_id',CAST(? AS JSON)),'合成删除时效矩阵',created_by,UTC_TIMESTAMP() FROM ai_model_release_versions WHERE model_id=? AND version_no=1", string(raw), productJSON, f.ProjectID).Error; err != nil {
						return err
					}
					return tx.Exec("UPDATE token_models SET release_version_no=2 WHERE id=?", f.ProjectID).Error
				}); err != nil {
					t.Fatal(err)
				}
				start, short, long := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(5*time.Second), time.Now().UTC().Add(time.Hour)
				var deadline time.Time
				seed := func(parentEnd, childEnd *time.Time, deadlineFromParent bool) {
					t.Helper()
					a := assetmodel.UserAsset{UserID: f.ProjectID, ProductID: p.ID, AssetType: "token", Status: "active", StartedAt: &start, ExpiresAt: parentEnd}
					if err := f.DB.Create(&a).Error; err != nil {
						t.Fatal(err)
					}
					var row struct{ ExpiresAt *time.Time }
					var table string
					var childID uint64
					if kind == "entitlement" {
						e := assetmodel.UserEntitlement{UserID: f.ProjectID, AssetID: a.ID, ProductID: p.ID, EntitlementType: "video_access", Status: "active", StartedAt: &start, ExpiresAt: childEnd}
						if err := f.DB.Create(&e).Error; err != nil {
							t.Fatal(err)
						}
						table, childID = "user_entitlements", e.ID
					} else {
						m := membermodel.UserMembership{UserID: f.ProjectID, AssetID: &a.ID, LevelID: level.ID, Status: "active", StartedAt: start, ExpiresAt: childEnd}
						if err := f.DB.Create(&m).Error; err != nil {
							t.Fatal(err)
						}
						table, childID = "user_memberships", m.ID
					}
					if deadline.IsZero() {
						if deadlineFromParent {
							table, childID = "user_assets", a.ID
						}
						if err := f.DB.Table(table).Select("expires_at").Where("id=?", childID).Take(&row).Error; err != nil || row.ExpiresAt == nil {
							t.Fatal("必须读取真实数据库的短期截止时间")
						}
						deadline = *row.ExpiresAt
					}
				}
				switch mode {
				case "expires":
					seed(nil, &short, false)
				case "parent_expires":
					seed(&short, &long, true)
				case "permanent_alternative":
					seed(nil, &short, false)
					seed(nil, nil, false)
				case "finite_alternative":
					seed(nil, &short, false)
					seed(&long, &long, false)
				case "crossed_parents":
					seed(&short, &long, true)
					seed(&long, &short, false)
				}
				before := f.FinancialSnapshot()
				store := &mediaDeleteAccessExpiryStore{VideoMediaDeleteStore: f.App.mediaDeleteStore}
				f.App.mediaDeleteStore = store
				var injected, valid, crossed atomic.Bool
				var candidateCount atomic.Int64
				wantCandidates := int64(1)
				if mode == "permanent_alternative" || mode == "finite_alternative" || mode == "crossed_parents" {
					wantCandidates = 2
				}
				const hook = "g6_delete_model_entitlement_query_expiry"
				queryTable := "user_entitlements"
				if kind == "membership" {
					queryTable = "user_memberships"
				}
				if err := f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
					if tx.Error == nil && store.headReturned.Load() && strings.Contains(tx.Statement.SQL.String(), queryTable) && len(tx.Statement.Vars) > 0 && tx.Statement.Vars[0] == f.ProjectID && injected.CompareAndSwap(false, true) {
						candidateCount.Store(tx.RowsAffected)
						valid.Store(time.Now().Before(deadline))
						if wait := time.Until(deadline.Add(30 * time.Millisecond)); wait > 0 {
							time.Sleep(wait)
						}
						crossed.Store(!time.Now().Before(deadline))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.DB.Callback().Query().Remove(hook) })
				caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
				result, err := f.App.DeleteVideoAsset(context.Background(), caller, root.PublicID, root.VersionNo, "g6-expiry-path-"+kind+"-"+mode)
				if !injected.Load() || !valid.Load() || !crossed.Load() || candidateCount.Load() != wantCandidates {
					t.Fatalf("必须读取实际有效候选再跨期：count=%d want=%d injected=%t valid=%t crossed=%t", candidateCount.Load(), wantCandidates, injected.Load(), valid.Load(), crossed.Load())
				}
				wantDeleted := mode == "permanent_alternative" || mode == "finite_alternative"
				if wantDeleted {
					if err != nil || result == nil || !result.MediaDeleted || f.MediaDeleteCalls() != 5 {
						t.Fatalf("仍有效的完整替代路径必须允许删除：err=%v deletes=%d", err, f.MediaDeleteCalls())
					}
				} else if !errors.Is(err, ErrVideoEntitlementDenied) || result != nil || f.MediaDeleteCalls() != 0 {
					t.Errorf("过期或拼接路径必须拒绝且零删除：denied=%t deletes=%d", errors.Is(err, ErrVideoEntitlementDenied), f.MediaDeleteCalls())
				}
				for role, fact := range f.InspectMedia(id) {
					deleted := wantDeleted && role != "moderation_copy"
					if fact.Deleted != deleted || fact.Present == deleted || (!deleted && !fact.HashMatches) {
						t.Errorf("媒体事实不符：%s", role)
					}
				}
				if !bytes.Equal(before, f.FinancialSnapshot()) {
					t.Fatal("资格时效不能改写原生成财务")
				}
			})
		}
	}
}

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
)

// 合成模型明确声明资产、权益类型与会员等级，不能把任意配额或购买价格当作资格来源。
func TestVideoG6EntitlementMySQLMatrix(t *testing.T) {
	db := openVideoG6MySQL(t)
	const id = 996200
	const code = "molin/video-g6-entitlement"
	for _, q := range []string{
		"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(996200,'fixture','active','verified')",
		"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(996200,996200,'G6权益','active','disabled','UTC')",
		"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(996200,996200,996200,'g6','fixture-g6-entitlement','合成Key','postpaid','allowlist','active',1)",
		"INSERT INTO products(id,product_type,product_code,name,status) VALUES(996200,'token','video_g6_entitlement','合成资格商品','active')",
		"INSERT INTO roles(id,code,name) VALUES(996200,'video_g6_user','合成视频用户')",
		"INSERT INTO user_roles(user_id,role_id) VALUES(996200,996200)",
		"INSERT INTO product_role_access(product_id,role_id,can_view,can_buy,can_use) VALUES(996200,996200,1,1,1)",
		"INSERT INTO user_assets(id,user_id,asset_type,product_id,status,started_at) VALUES(996200,996200,'token',996200,'active',UTC_TIMESTAMP()-INTERVAL 1 DAY)",
		"INSERT INTO token_models(id,logical_model_code,display_name,modality,status,capabilities_json,release_version_no,published_at) VALUES(996200,'molin/video-g6-entitlement','合成权益模型','video','active',JSON_ARRAY('video.generate'),1,UTC_TIMESTAMP())",
		`INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(996200,1,'active','{"logical_model_code":"molin/video-g6-entitlement","modality":"video","capabilities":["video.generate"],"visible_scope":"all","product_id":996200}','隔离测试',996200,UTC_TIMESTAMP())`,
		"INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(996200,996200,996200,'molin/video-g6-entitlement')",
		"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(996200,996200,'molin/video-g6-entitlement','video.generate','active',996200,UTC_TIMESTAMP(),UTC_TIMESTAMP())",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 996200,id,code,'allow' FROM permissions WHERE code='video:generate'",
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	contract, err := ParseVideoModelContract([]byte(videoG6NoEntitlementContract), nil)
	if err != nil {
		t.Fatal(err)
	}
	contract.AssetRequired = true
	setContract := func(tx *gorm.DB, c VideoModelContract) {
		t.Helper()
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Exec("UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.video_contract',CAST(? AS JSON)) WHERE model_id=996200", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	setContract(db, contract)
	caller := VideoCaller{UserID: id, APIKeyID: id}
	ctx := context.Background()
	check := func(tx *gorm.DB, want error) {
		t.Helper()
		_, err := NewVideoAccessService(tx).Resolve(ctx, caller, code)
		if !errors.Is(err, want) {
			t.Errorf("准入错误=%v，期望=%v", err, want)
		}
	}
	check(db, nil)
	for _, tc := range []struct{ name, sql string }{
		{"can_buy不能代替can_use", "UPDATE product_role_access SET can_use=0 WHERE product_id=996200"},
		{"商品停用", "UPDATE products SET status='inactive' WHERE id=996200"},
		{"资产暂停", "UPDATE user_assets SET status='suspended' WHERE id=996200"},
		{"资产未来开始", "UPDATE user_assets SET started_at=UTC_TIMESTAMP()+INTERVAL 1 DAY WHERE id=996200"},
		{"资产已过期", "UPDATE user_assets SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE id=996200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rollback := errors.New("还原合成状态")
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tc.sql).Error; err != nil {
					t.Fatal(err)
				}
				check(tx, ErrVideoEntitlementDenied)
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	kind := "video_access"
	contract.RequiredEntitlementType = &kind
	setContract(db, contract)
	check(db, ErrVideoEntitlementDenied)
	if err := db.Exec("INSERT INTO user_entitlements(id,user_id,asset_id,entitlement_type,product_id,quota_total,quota_used,quota_reserved,status,started_at) VALUES(996200,996200,996200,'storage_gb',996200,10,0,0,'active',UTC_TIMESTAMP()-INTERVAL 1 DAY)").Error; err != nil {
		t.Fatal(err)
	}
	check(db, ErrVideoEntitlementDenied)
	if err := db.Exec("UPDATE user_entitlements SET entitlement_type='video_access' WHERE id=996200").Error; err != nil {
		t.Fatal(err)
	}
	check(db, nil)
	for _, tc := range []struct{ name, sql string }{
		{"预占耗尽权益", "UPDATE user_entitlements SET quota_used=2,quota_reserved=8 WHERE id=996200"},
		{"权益暂停", "UPDATE user_entitlements SET status='suspended' WHERE id=996200"},
		{"权益到期", "UPDATE user_entitlements SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE id=996200"},
		{"权益未来开始", "UPDATE user_entitlements SET started_at=UTC_TIMESTAMP()+INTERVAL 1 DAY WHERE id=996200"},
		{"有权益但父资产暂停", "UPDATE user_assets SET status='suspended' WHERE id=996200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rollback := errors.New("还原合成状态")
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tc.sql).Error; err != nil {
					t.Fatal(err)
				}
				check(tx, ErrVideoEntitlementDenied)
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	for _, q := range []string{"INSERT INTO membership_levels(id,level_code,name,status) VALUES(996200,'video_g6_fixture','合成会员','active')", "INSERT INTO user_memberships(id,user_id,level_id,status,started_at) VALUES(996200,996200,996200,'active',UTC_TIMESTAMP()-INTERVAL 1 DAY)"} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	contract.RequiredMembershipLevels = []uint64{id}
	setContract(db, contract)
	check(db, nil)
	for _, tc := range []struct{ name, sql string }{
		{"会员到期", "UPDATE user_memberships SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE id=996200"},
		{"会员未来开始", "UPDATE user_memberships SET started_at=UTC_TIMESTAMP()+INTERVAL 1 DAY WHERE id=996200"},
		{"会员取消", "UPDATE user_memberships SET status='cancelled' WHERE id=996200"},
		{"会员不能替代资产", "UPDATE user_assets SET status='suspended' WHERE id=996200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rollback := errors.New("还原合成状态")
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tc.sql).Error; err != nil {
					t.Fatal(err)
				}
				check(tx, ErrVideoEntitlementDenied)
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	t.Run("旧RR快照必须看到父资产撤销", func(t *testing.T) {
		err := db.Transaction(func(tx *gorm.DB) error {
			var state string
			if err := tx.Table("user_assets").Select("status").Where("id=996200").Scan(&state).Error; err != nil || state != "active" {
				t.Fatal("旧快照准备失败")
			}
			if err := db.Exec("UPDATE user_assets SET status='suspended' WHERE id=996200").Error; err != nil {
				t.Fatal(err)
			}
			key := uint64(id)
			return NewVideoAccessService(db).AuthorizeTx(ctx, tx, repository.VideoOwner{UserID: id, ProjectID: id, APIKeyID: &key}, code, time.Now().UTC())
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if !errors.Is(err, ErrVideoEntitlementDenied) {
			t.Errorf("父资产撤销未被观察：%v", err)
		}
	})
}

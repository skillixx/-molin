package service

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"molin/server/internal/modules/token_gateway/repository"
)

func openVideoG6MySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MOLIN_VIDEO_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置G6一次性MySQL")
	}
	c, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离DSN无效")
	}
	host, port, err := net.SplitHostPort(c.Addr)
	parsedPort, portErr := strconv.ParseUint(port, 10, 16)
	standardTarget := port == "3306" && (host == "mysql" || host == "127.0.0.1")
	sdkLoopbackTarget := os.Getenv("MOLIN_VIDEO_G6_SDK_EXECUTE") == "YES" && host == "127.0.0.1" && portErr == nil && parsedPort > 0
	if err != nil || os.Getenv("MOLIN_VIDEO_G6_ISOLATED") != "YES" || c.DBName != "molin_video_g6_contract" || c.Net != "tcp" || (!standardTarget && !sdkLoopbackTarget) {
		t.Fatal("禁止访问非G6隔离数据库")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("隔离数据库不可用")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(120)
	sqlDB.SetMaxIdleConns(120)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestVideoG6AccessMySQLMatrix(t *testing.T) {
	db := openVideoG6MySQL(t)
	const code = "molin/video-g6-access"
	for _, q := range []string{
		"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(996100,'fixture','active','verified')",
		"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(996100,996100,'G6准入','active','disabled','UTC')",
		"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(996100,996100,996100,'g6','fixture-g6-access','G6合成Key','postpaid','allowlist','active',1)",
		"INSERT INTO token_models(id,logical_model_code,display_name,modality,status,capabilities_json,release_version_no,published_at) VALUES(996100,'molin/video-g6-access','G6合成模型','video','active',JSON_ARRAY('video.generate'),1,UTC_TIMESTAMP())",
		`INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(996100,1,'active','{"logical_model_code":"molin/video-g6-access","display_name":"G6","modality":"video","capabilities":["video.generate"],"visible_scope":"all"}','隔离测试',996100,UTC_TIMESTAMP())`,
		"INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(996100,996100,996100,'molin/video-g6-access')",
		"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(996100,996100,'molin/video-g6-access','video.generate','active',996100,UTC_TIMESTAMP(),UTC_TIMESTAMP())",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 996100,id,code,'allow' FROM permissions WHERE code='video:generate'",
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatalf("准入合成夹具失败：%v", err)
		}
	}
	caller := VideoCaller{UserID: 996100, APIKeyID: 996100}
	if err := db.Exec("UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.video_contract',CAST(? AS JSON)) WHERE model_id=996100", videoG6NoEntitlementContract).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if owner, err := NewVideoAccessService(db).Resolve(ctx, caller, code); err != nil || owner.ProjectID != 996100 || owner.APIKeyID == nil {
		t.Fatalf("合法SK应通过：%v", err)
	}
	if _, err := NewVideoAccessService(db).Resolve(ctx, VideoCaller{UserID: 996100, ProjectID: 996100}, code); err != nil {
		t.Fatalf("合法JWT应通过：%v", err)
	}
	for _, tc := range []struct {
		name, sql string
		want      error
	}{
		{"用户停用", "UPDATE users SET status='disabled' WHERE id=996100", ErrVideoBillingAccess},
		{"实名缺失", "UPDATE users SET real_name_status='unverified' WHERE id=996100", ErrRealNameRequired},
		{"项目停用", "UPDATE ai_projects SET status='suspended' WHERE id=996100", ErrVideoBillingAccess},
		{"旧Key无视频授权", "UPDATE api_keys SET video_generate_allowed=0 WHERE id=996100", ErrVideoCapabilityDenied},
		{"Key吊销", "UPDATE api_keys SET status='revoked' WHERE id=996100", ErrVideoBillingAccess},
		{"Key到期", "UPDATE api_keys SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE id=996100", ErrVideoBillingAccess},
		{"旧模式不继承", "UPDATE api_keys SET scope_mode='legacy_all' WHERE id=996100", ErrVideoBillingAccess},
		{"项目撤权", "UPDATE ai_project_model_capability_grants SET status='revoked',version_no=version_no+1 WHERE project_id=996100", ErrVideoCapabilityDenied},
		{"模型scope缺失", "DELETE FROM api_key_model_scopes WHERE api_key_id=996100", ErrVideoCapabilityDenied},
		{"用户显式deny", "UPDATE user_permission_overrides SET effect='deny' WHERE user_id=996100", ErrVideoCapabilityDenied},
		{"解除记录不是暂停", "INSERT INTO ai_safety_subject_actions(subject_type,subject_id,action,status,reason,operator_id) VALUES('user','996100','reinstate','active','隔离复验',996100)", nil},
		{"用户暂停", "INSERT INTO ai_safety_subject_actions(subject_type,subject_id,action,status,reason,operator_id) VALUES('user','996100','suspend','active','隔离复验',996100)", ErrVideoCapabilityDenied},
		{"用户allow过期", "UPDATE user_permission_overrides SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE user_id=996100", ErrVideoCapabilityDenied},
		{"模型下架", "UPDATE token_models SET status='inactive' WHERE id=996100", ErrVideoCapabilityDenied},
		{"发布事实退役", "UPDATE ai_model_release_versions SET status='retired' WHERE model_id=996100", ErrVideoCapabilityDenied},
		{"发布快照能力缺失", "UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.capabilities',JSON_ARRAY()) WHERE model_id=996100", ErrVideoCapabilityDenied},
		{"发布定向无权限", "UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.visible_scope','roles','$.target_audience_json','{\"role_codes\":[\"admin\"]}') WHERE model_id=996100", ErrVideoCapabilityDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rollback := errors.New("回滚隔离反例")
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tc.sql).Error; err != nil {
					t.Fatal(err)
				}
				_, got := NewVideoAccessService(tx).Resolve(ctx, caller, code)
				if !errors.Is(got, tc.want) {
					t.Errorf("错误=%v，预期=%v", got, tc.want)
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	for _, foreign := range []VideoCaller{{UserID: 996101, APIKeyID: 996100}, {UserID: 996100, APIKeyID: 996100, ProjectID: 996101}, {UserID: 996100, APIKeyID: 996101}} {
		if _, err := NewVideoAccessService(db).Resolve(ctx, foreign, code); !errors.Is(err, ErrVideoBillingAccess) {
			t.Fatal("跨主体必须不存在")
		}
	}
	t.Run("可见性数据库故障不是无权限", func(t *testing.T) {
		const callback = "video_g6_visibility_failure"
		if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.TableExpr != nil && tx.Statement.TableExpr.SQL == "roles r" {
				tx.AddError(errors.New("合成数据库读取故障"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer db.Callback().Query().Remove(callback)
		rollback := errors.New("还原隔离配置")
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.visible_scope','roles','$.target_audience_json','{"role_codes":["admin"]}') WHERE model_id=996100`).Error; err != nil {
				t.Fatal(err)
			}
			_, err := NewVideoAccessService(tx).Resolve(ctx, caller, code)
			if !errors.Is(err, ErrVideoAccessUnavailable) {
				t.Errorf("可见性数据库故障错误=%v", err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	})
	t.Run("旧RR快照必须看到新deny", func(t *testing.T) {
		err := db.Transaction(func(tx *gorm.DB) error {
			var initial string
			if err := tx.Table("user_permission_overrides").Select("effect").Where("user_id=996100").Scan(&initial).Error; err != nil || initial != "allow" {
				t.Fatal("旧快照准备失败")
			}
			if err := db.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=996100").Error; err != nil {
				t.Fatal(err)
			}
			key := uint64(996100)
			return NewVideoAccessService(db).AuthorizeTx(ctx, tx, repository.VideoOwner{UserID: 996100, ProjectID: 996100, APIKeyID: &key}, code, time.Now().UTC())
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if !errors.Is(err, ErrVideoCapabilityDenied) {
			t.Errorf("旧快照绕过已提交撤权：%v", err)
		}
		if err := db.Exec("UPDATE user_permission_overrides SET effect='allow' WHERE user_id=996100").Error; err != nil {
			t.Fatal(err)
		}
	})
	// 撤销后的并发旧调用必须全部重新查库，不借用原授权结果。
	if err := db.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=996100").Error; err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 100)
	for i := 0; i < 100; i++ {
		go func() { _, err := NewVideoAccessService(db).Resolve(ctx, caller, code); results <- err }()
	}
	for i := 0; i < 100; i++ {
		if !errors.Is(<-results, ErrVideoCapabilityDenied) {
			t.Fatal("撤权被并发绕过")
		}
	}
}

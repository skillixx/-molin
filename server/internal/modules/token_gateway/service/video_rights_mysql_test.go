package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// 权利链只记录显式合成配置和当前认证主体，不为真实用户生成法律同意。
func TestVideoG6RightsMySQLAcceptanceAndInvalidation(t *testing.T) {
	db := openVideoG6MySQL(t)
	const id uint64 = 996400
	for _, sql := range []string{
		"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(996400,'fixture','active','verified'),(996401,'fixture','active','verified')",
		"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(996400,996400,'权利合成项目','active','disabled','UTC'),(996401,996401,'隔离项目','active','disabled','UTC')",
		"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(996400,996400,996400,'g6','fixture-g6-rights','合成凭据','postpaid','allowlist','active',1)",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 996400,id,code,'allow' FROM permissions WHERE code='video:generate'",
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	s := NewVideoRightsService(db)
	caller := VideoCaller{UserID: id, ProjectID: id}
	if _, err := s.CurrentPolicy(ctx, caller); !errors.Is(err, ErrVideoRightsUnavailable) {
		t.Fatalf("无政策配置必须失败关闭：%v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	body := "仅供非商业隔离测试的合成权利说明，不构成真实用户同意。"
	hash := sha256.Sum256([]byte(body))
	if err := db.Exec("INSERT INTO ai_video_rights_policies(id,policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) VALUES(996400,'rights-g6-fixture-v1','non_commercial_test_fixture','合成权利条款',?,?,'active',?,?,300,1)", body, hex.EncodeToString(hash[:]), now.Add(-time.Hour), now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE id=996400 AND status='active'")
	policy, err := s.CurrentPolicy(ctx, VideoCaller{UserID: id})
	if err != nil || policy.PolicyVersion != "rights-g6-fixture-v1" || policy.Scope != "non_commercial_test_fixture" || policy.Body != body {
		t.Fatalf("已认证主体应能阅读政策，无需模型grant：%v", err)
	}
	before, err := s.ProjectAcceptance(ctx, caller)
	if err != nil || before.Valid || before.AcceptanceID != nil || before.AcceptedAt != nil || before.ExpiresAt != nil {
		t.Fatalf("无接受记录必须明确null且不可用：%v", err)
	}
	command := VideoRightsAcceptCommand{Caller: caller, PolicyVersion: policy.PolicyVersion, Confirmed: true, IdempotencyKey: "rights-g6-accept-first-0001", RequestID: "rights-g6-request-0001"}
	start := make(chan struct{})
	results := make(chan *VideoRightsAcceptance, 100)
	failures := make(chan error, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := s.Accept(ctx, command)
			results <- result
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original *VideoRightsAcceptance
	created := 0
	for result := range results {
		if result == nil || !result.Valid || result.AcceptanceID == nil || result.AcceptedAt == nil || result.ExpiresAt == nil {
			t.Fatal("明确接受后必须返回有效回执")
		}
		if !result.Idempotent {
			created++
		}
		if original == nil {
			original = result
		} else if *result.AcceptanceID != *original.AcceptanceID || !result.AcceptedAt.Equal(*original.AcceptedAt) || !result.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatal("并发接受不能重复创建或延长时间")
		}
	}
	if created != 1 || original.ExpiresAt.After(policy.ExpiresAt) {
		t.Fatal("接受必须唯一，期限不能超过政策期限")
	}
	for _, tc := range []struct {
		name string
		edit func(*VideoRightsAcceptCommand)
		want error
	}{
		{"SK不能代签", func(c *VideoRightsAcceptCommand) { c.Caller.APIKeyID = id }, ErrVideoRightsOwnerJWTRequired},
		{"跨项目", func(c *VideoRightsAcceptCommand) { c.Caller.ProjectID = id + 1 }, ErrVideoBillingAccess},
		{"不能伪造同意", func(c *VideoRightsAcceptCommand) { c.Confirmed = false }, ErrVideoRightsRequired},
		{"同键不同版本", func(c *VideoRightsAcceptCommand) { c.PolicyVersion = "rights-g6-fixture-v2" }, ErrVideoRightsConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := command
			tc.edit(&copy)
			if _, err := s.Accept(ctx, copy); !errors.Is(err, tc.want) {
				t.Fatalf("拒绝错误：got=%v want=%v", err, tc.want)
			}
		})
	}
	// 通过可控时钟验证过期重放仍返回原事实，而不是隐式续期或恢复有效同意。
	s.now = func() time.Time { return original.ExpiresAt.Add(time.Second) }
	expired, err := s.Accept(ctx, command)
	if err != nil || expired.Valid || !expired.Idempotent || !expired.ExpiresAt.Equal(*original.ExpiresAt) {
		t.Fatalf("过期重放不能续期：%v", err)
	}
	s.now = time.Now
	for _, column := range []string{"body='替换正文'", "title='替换标题'"} {
		// 状态迁移与CAS本身合法；该反例只应被内容不可变约束拒绝，不能借其他条件凑绿。
		if err := db.Exec("UPDATE ai_video_rights_policies SET status='retired'," + column + ",version_no=version_no+1 WHERE id=996400").Error; err == nil {
			t.Fatal("合法退役也不能替换既有条款内容")
		}
	}
	if err := db.Exec("UPDATE ai_project_video_rights_acceptances SET expires_at=expires_at+INTERVAL 1 DAY WHERE user_id=996400").Error; err == nil {
		t.Fatal("接受事实不能修改")
	}
	if err := db.Exec("DELETE FROM ai_project_video_rights_acceptances WHERE user_id=996400").Error; err == nil {
		t.Fatal("接受事实不能删除")
	}
	var count int64
	if err := db.Table("ai_project_video_rights_acceptances").Where("user_id=?", id).Count(&count).Error; err != nil || count != 1 {
		t.Fatal(fmt.Sprintf("原接受事实必须保留一条：%d %v", count, err))
	}
	// 约束反例检查实际MySQL错误号，不能把拼写/SQL语法错误冒充外键或CHECK生效。
	assertSQLReject := func(name string, number uint16, write func(*gorm.DB) error) {
		t.Run(name, func(t *testing.T) {
			rollback := errors.New("回滚隔离约束反例")
			var observed error
			if err := db.Transaction(func(tx *gorm.DB) error { observed = write(tx); return rollback }); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			var failure *drivermysql.MySQLError
			if !errors.As(observed, &failure) || failure.Number != number {
				t.Fatalf("约束错误号不符：want=%d got=%v", number, observed)
			}
		})
	}
	var policyRow videoRightsPolicyRow
	if err := db.Where("id=996400").Take(&policyRow).Error; err != nil {
		t.Fatal(err)
	}
	assertSQLReject("禁止商业用途配置", 3819, func(tx *gorm.DB) error {
		p := policyRow
		p.ID = 0
		p.PolicyVersion = "rights-g6-invalid-purpose"
		p.Purpose = "commercial"
		p.Status = "draft"
		return tx.Create(&p).Error
	})
	assertSQLReject("只能一个active版本", 1062, func(tx *gorm.DB) error {
		p := policyRow
		p.ID = 0
		p.PolicyVersion = "rights-g6-second-active"
		return tx.Create(&p).Error
	})
	var receiptRow videoRightsAcceptanceRow
	if err := db.Where("user_id=996400").Take(&receiptRow).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tag string
		number    uint16
		change    func(*videoRightsAcceptanceRow)
	}{
		{"签署人不能替换", "actor", 3819, func(r *videoRightsAcceptanceRow) { r.AcceptedBy = 996401 }},
		{"Project所有者复合外键", "project", 1452, func(r *videoRightsAcceptanceRow) { r.ProjectID = 996401 }},
		{"政策三元身份不能错绑", "policy", 1452, func(r *videoRightsAcceptanceRow) { r.PolicyVersion = "rights-g6-unbound-version" }},
	} {
		assertSQLReject(tc.name, tc.number, func(tx *gorm.DB) error {
			r := receiptRow
			r.ID = 0
			r.PublicID = "vrights_invalid_" + tc.tag + "_0001"
			r.IdempotencyKeyHash = videoBillingDigest(tc.tag)
			tc.change(&r)
			return tx.Create(&r).Error
		})
	}
	t.Run("政策自身过期仍保留失效回执", func(t *testing.T) {
		s.now = func() time.Time { return policy.ExpiresAt.Add(time.Second) }
		defer func() { s.now = time.Now }()
		got, err := s.Accept(ctx, command)
		if err != nil || got == nil || got.Valid || !got.Idempotent || got.AcceptanceID == nil || *got.AcceptanceID != *original.AcceptanceID || !got.AcceptedAt.Equal(*original.AcceptedAt) {
			t.Fatalf("政策过期不能抹去原回执或恢复授权：%v", err)
		}
	})
	if err := db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE id=996400").Error; err != nil {
		t.Fatalf("合法退役必须成功：%v", err)
	}
	t.Run("无当前政策时历史回执仍可查", func(t *testing.T) {
		if _, err := s.CurrentPolicy(ctx, caller); !errors.Is(err, ErrVideoRightsUnavailable) {
			t.Fatalf("全局政策应不可用：%v", err)
		}
		got, err := s.ProjectAcceptance(ctx, caller)
		if err != nil || got == nil || got.Valid || got.AcceptanceID == nil || *got.AcceptanceID != *original.AcceptanceID {
			t.Fatalf("历史回执应保留且无效：%v", err)
		}
		got, err = s.Accept(ctx, command)
		if err != nil || got == nil || got.Valid || !got.Idempotent || !got.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatalf("旧键不能隐式续期：%v", err)
		}
	})
	if err := db.Exec("INSERT INTO ai_video_rights_policies(id,policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) VALUES(996402,'rights-g6-fixture-v2','non_commercial_test_fixture','新版本合成条款',?,?,'active',?,?,300,1)", body, hex.EncodeToString(hash[:]), now.Add(-time.Hour), now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE id=996402 AND status='active'")
	changed, err := s.Accept(ctx, command)
	if err != nil || changed.Valid || changed.InvalidReason != "policy_changed" {
		t.Fatalf("升级后旧键只能返回失效回执：%v", err)
	}
	renew := command
	renew.IdempotencyKey = "rights-g6-renew-second-0001"
	if _, err := s.Accept(ctx, renew); !errors.Is(err, ErrVideoRightsRequired) {
		t.Fatalf("旧版本新键不得重新接受：%v", err)
	}
	renew.PolicyVersion = "rights-g6-fixture-v2"
	if current, err := s.Accept(ctx, renew); err != nil || !current.Valid || current.Idempotent {
		t.Fatalf("新版本须显式新键接受：%v", err)
	}
}

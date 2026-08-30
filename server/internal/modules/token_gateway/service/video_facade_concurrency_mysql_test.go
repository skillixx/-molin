package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 使用第二个真实上传会话保存同一规范化图片；测试的是别名而不是更改原输入。
func videoG5InputAlias(t *testing.T, db *gorm.DB, original *model.AIGatewayInputAsset, owner repository.VideoOwner, suffix string) *model.AIGatewayInputAsset {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u := model.AIUploadSession{PublicID: original.PublicID + "_upload_" + suffix, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: model.AIUploadSourcePlatformPresigned, Status: model.AIUploadSessionVerifying, MIMEType: *original.MIMEType, SizeBytes: *original.SizeBytes, Bucket: "video-temp", ObjectKey: "fixture/alias/" + original.PublicID + suffix, ExpiresAt: now.Add(time.Hour)}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	a := *original
	a.ID = 0
	a.PublicID += "_" + suffix
	a.UserID = owner.UserID
	a.ProjectID = owner.ProjectID
	a.UploadSessionID = &u.ID
	a.Bucket = &u.Bucket
	a.ObjectKey = &u.ObjectKey
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIUploadSession{}).Where("id=?", u.ID).Updates(map[string]interface{}{"status": "completed", "final_input_asset_id": a.ID, "source_etag": "fixture-etag", "completed_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	return &a
}

func TestVideoG5ReserveMySQLFacadeAliasProjectKeyAndHashIsolation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	original := prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	projectID := f.owner.ProjectID + 1000000
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'别名隔离项目','active','disabled','Asia/Shanghai')", projectID, f.owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	otherProject := repository.VideoOwner{UserID: f.owner.UserID, ProjectID: projectID}
	projectAlias := videoG5InputAlias(t, db, original, otherProject, "project")
	keyID := *f.owner.APIKeyID + 2000000
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status) VALUES(?,?,?,'g5',?,'别名隔离Key','postpaid','allowlist','active')", keyID, f.owner.UserID, f.owner.ProjectID, fmt.Sprintf("fixture-alias-key-%d", keyID)).Error; err != nil {
		t.Fatal(err)
	}
	otherKey := f.owner
	otherKey.APIKeyID = &keyID
	keyAlias := videoG5InputAlias(t, db, original, otherKey, "key")
	different := *original
	sha := strings.Repeat("f", 64)
	different.NormalizedSHA256 = &sha
	different.OriginalSHA256 = sha
	hashAlias := videoG5InputAlias(t, db, &different, f.owner, "hash")
	for _, x := range []struct {
		asset *model.AIGatewayInputAsset
		want  error
	}{{projectAlias, repository.ErrVideoInputNotFound}, {keyAlias, repository.ErrVideoInputNotFound}, {hashAlias, repository.ErrVideoInputSnapshotDrift}} {
		for _, auto := range []bool{false, true} {
			r := videoG5FacadeRequest(f)
			input := *r.FingerprintInput.Input
			input.InputAssetID = x.asset.PublicID
			input.InternalID = original.ID
			r.FingerprintInput.Input = &input
			facade := NewVideoQuoteFacade(f.quotes, f.service)
			var err error
			if auto {
				_, err = facade.CreateOpenAIVideo(context.Background(), r)
			} else {
				_, err = facade.GenerateWithTokenQuote(context.Background(), r, f.command.QuotePublicID)
			}
			if !errors.Is(err, x.want) {
				t.Fatalf("别名必须依持久化归属/hash验证: auto=%t err=%v", auto, err)
			}
		}
	}
}

func TestVideoG5ReserveMySQLFacadeOwnedAliasAndDeletedOriginal(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	original := prepareVideoG5I2V(t, &f)
	alias := videoG5InputAlias(t, db, original, f.owner, "alias")
	first, err := f.service.ReserveAndCreate(context.Background(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), first.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewVideoInputAssetRepository(db).RequestDelete(context.Background(), original.PublicID, f.owner, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.quotes.WithInputSnapshotResolver(nil)
	for _, inputID := range []string{original.PublicID, alias.PublicID} {
		r := videoG5FacadeRequest(f)
		binding := *r.FingerprintInput.Input
		binding.InputAssetID = inputID
		binding.InternalID = 999999999
		r.FingerprintInput.Input = &binding
		got, err := NewVideoQuoteFacade(f.quotes, f.service).CreateOpenAIVideo(context.Background(), r)
		if err != nil || !got.Existing || got.TaskID != first.TaskID || got.ExecutionStatus != model.AIImageTaskCancelled || got.BillingStatus != model.AIBillingReleased || got.DeliveryStatus != model.AIDeliveryRejected {
			t.Fatalf("冻结原输入/同归属别名应返回原三轴: %+v %v", got, err)
		}
	}
	bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), first.TaskID, f.owner)
	if err != nil || len(bindings) != 1 || bindings[0].InputAssetID != original.ID || bindings[0].LeaseReleasedAt == nil {
		t.Fatal("重放不得替换输入或重新建立租约")
	}
	bad := videoG5FacadeRequest(f)
	binding := *bad.FingerprintInput.Input
	binding.InputAssetID = "missing-alias"
	bad.FingerprintInput.Input = &binding
	if _, err := NewVideoQuoteFacade(f.quotes, f.service).CreateOpenAIVideo(context.Background(), bad); !errors.Is(err, repository.ErrVideoInputNotFound) {
		t.Fatal("缺失别名必须404语义")
	}
}

// 同生成键的显式/自动或双别名并发，必须由生成事实裁决，而不是Quote资产ID差异先报冲突。
func TestVideoG5ReserveMySQLFacadeMixedConcurrency(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mixed := range []bool{false, true} {
		f := newVideoG5ReservationFixture(t, db, "10")
		original := prepareVideoG5I2V(t, &f)
		alias := videoG5InputAlias(t, db, original, f.owner, "concurrent")
		aliasBinding := *f.command.FingerprintInput.Input
		aliasBinding.InputAssetID = alias.PublicID
		aliasBinding.InternalID = alias.ID
		f.quotes.WithInputSnapshotResolver(&fakeVideoInputResolver{items: map[string]VideoQuoteInputBinding{original.PublicID: *f.command.FingerprintInput.Input, alias.PublicID: aliasBinding}})
		facade := NewVideoQuoteFacade(f.quotes, f.service)
		var wg sync.WaitGroup
		var bad, first atomic.Int32
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := videoG5FacadeRequest(f)
				r.RequestID += fmt.Sprintf("_%d", i)
				r.TaskID += fmt.Sprintf("_%d", i)
				var got *VideoPreparedGeneration
				var err error
				if mixed && i%2 == 0 {
					got, err = facade.GenerateWithTokenQuote(context.Background(), r, f.command.QuotePublicID)
				} else {
					if i%2 == 1 {
						r.FingerprintInput.Input = &aliasBinding
					}
					got, err = facade.CreateOpenAIVideo(context.Background(), r)
				}
				if err != nil {
					bad.Add(1)
					t.Logf("并发入口拒绝: %v", err)
				} else if !got.Existing {
					first.Add(1)
				}
			}(i)
		}
		wg.Wait()
		if bad.Load() != 0 || first.Load() != 1 {
			t.Fatalf("混合入口应单次创建: mixed=%t bad=%d first=%d", mixed, bad.Load(), first.Load())
		}
		for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds", "wallet_transactions"} {
			var n int64
			if err := db.Table(table).Where("user_id=?", f.owner.UserID).Count(&n).Error; err != nil || n != 1 {
				t.Fatalf("不得重复%s: %d %v", table, n, err)
			}
		}
		var n int64
		if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=? AND consumed_request_id IS NOT NULL", f.owner.UserID).Count(&n).Error; err != nil || n != 1 {
			t.Fatal("只能消费一个Quote")
		}
	}
}

func TestVideoG5ReserveMySQLAutoQuoteExpiryAndRollback(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"key_expired", "insufficient", "hold_fault"} {
		balance := "10"
		if mode == "insufficient" {
			balance = "0.1"
		}
		f := newVideoG5ReservationFixture(t, db, balance)
		if mode == "key_expired" {
			now := time.Now().UTC().Truncate(time.Second)
			expires := now.Add(time.Second)
			if err := db.Exec("UPDATE api_keys SET expires_at=? WHERE id=?", expires, *f.owner.APIKeyID).Error; err != nil {
				t.Fatal(err)
			}
			f.service.now = func() time.Time { return now }
			f.service.fault = func(at string) error {
				if at == "automatic_quote" {
					now = expires
				}
				return nil
			}
		}
		if mode == "hold_fault" {
			f.service.fault = func(at string) error {
				if at == "hold" {
					return errors.New("合成预占故障")
				}
				return nil
			}
		}
		if _, err := NewVideoQuoteFacade(f.quotes, f.service).CreateOpenAIVideo(context.Background(), videoG5FacadeRequest(f)); err == nil {
			t.Fatal("过期/资金故障应拒绝")
		}
		var quotes int64
		if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&quotes).Error; err != nil || quotes != 1 {
			t.Fatalf("失败不能留下自动Quote: %d %v", quotes, err)
		}
		for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds", "wallet_transactions"} {
			var n int64
			if err := db.Table(table).Where("user_id=?", f.owner.UserID).Count(&n).Error; err != nil || n != 0 {
				t.Fatalf("失败不得留下%s", table)
			}
		}
	}
}

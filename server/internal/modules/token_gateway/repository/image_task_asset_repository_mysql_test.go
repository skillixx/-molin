package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
)

func TestImageTaskAssetRepositoryMySQLIsolationAndStates(t *testing.T) {
	dsn := os.Getenv("MOLIN_IMAGE_G3_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 IMG-G3 隔离 MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(120)
	sqlDB.SetMaxIdleConns(120)

	const (
		userID       = uint64(93001)
		otherUserID  = uint64(93002)
		projectID    = uint64(93001)
		otherProject = uint64(93002)
		apiKeyID     = uint64(93001)
		otherKeyID   = uint64(93002)
		modelCode    = "molin/image-g3-mysql"
		fixtureID    = uint64(93001)
		requestID    = "img-g3-request"
	)
	cleanupImageG3MySQLFixture(t, db, userID, otherUserID, projectID, otherProject, apiKeyID, otherKeyID, modelCode, fixtureID)
	t.Cleanup(func() {
		cleanupImageG3MySQLFixture(t, db, userID, otherUserID, projectID, otherProject, apiKeyID, otherKeyID, modelCode, fixtureID)
	})

	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?, 'fixture', 'verified', 'active'),(?, 'fixture', 'verified', 'active')", userID, otherUserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,monthly_budget,timezone) VALUES(?,?,?,'active','disabled',NULL,'Asia/Shanghai'),(?,?,?,'active','disabled',NULL,'Asia/Shanghai')", projectID, userID, "图片G3项目", otherProject, otherUserID, "其他项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,'g3','g3-hash','G3密钥','postpaid','','allowlist','active'),(?,?,?,'g3-other','g3-other-hash','其他密钥','postpaid','','allowlist','active')", apiKeyID, userID, projectID, otherKeyID, otherUserID, otherProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", fixtureID, modelCode, "图片G3模型", "image", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	limits := json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)
	if err := db.Exec(`INSERT INTO ai_price_versions(
id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?, 'image.generate','image_variant',1,'CNY',1,'active',0.2,NULL,NULL,?,0.01,'test_fixture','g3-mysql','test_fixture','confirmed_usage','ceil_8',?,?,?,?)`,
		fixtureID, modelCode, limits, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Hour), userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,delivery_status,is_stream)
VALUES(?,?,?,?,?,'image','image.generate','pending',0)`, requestID, userID, projectID, apiKeyID, modelCode).Error; err != nil {
		t.Fatal(err)
	}
	fingerprint := fmt.Sprintf("%064x", fixtureID)
	variantHash := fmt.Sprintf("%064x", fixtureID+1)
	consumedAt := now
	quote := &model.AIGatewayQuote{
		ID: fixtureID, PublicID: "quote_img_g3_mysql", UserID: userID, ProjectID: projectID, APIKeyID: uint64Ptr(apiKeyID),
		LogicalModelCode: modelCode, Capability: model.AIImageCapability, RequestFingerprint: fingerprint,
		RequestVariantHash: variantHash, PriceVersionID: fixtureID, PriceSnapshotJSON: json.RawMessage(`{"schema_version":2}`),
		QuotedAmount: decimal.RequireFromString("0.50000000"), Currency: "CNY", ExpiresAt: now.Add(5 * time.Minute),
		ConsumedRequestID: stringPtr(requestID), ConsumedAt: &consumedAt, CreatedAt: now,
	}
	if err := NewImageQuoteRepository(db).Create(context.Background(), quote); err != nil {
		t.Fatal(err)
	}

	owner := ImageOwner{UserID: userID, ProjectID: projectID, APIKeyID: uint64Ptr(apiKeyID)}
	otherOwner := ImageOwner{UserID: otherUserID, ProjectID: otherProject, APIKeyID: uint64Ptr(otherKeyID)}
	taskRepo := NewImageTaskRepository(db)
	task := &model.AIImageTask{
		ID: fixtureID, PublicID: "task_img_g3_mysql", RequestID: requestID, QuoteID: fixtureID,
		UserID: userID, ProjectID: projectID, APIKeyID: uint64Ptr(apiKeyID), LogicalModelCode: modelCode,
		Capability: model.AIImageCapability, Status: model.AIImageTaskCreated, InputJSON: json.RawMessage(`{"resolution":"2K"}`),
	}
	if err := taskRepo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := taskRepo.FindForOwner(context.Background(), task.PublicID, otherOwner); !errors.Is(err, ErrImageTaskNotFound) {
		t.Fatalf("跨用户任务查询必须隐藏记录: %v", err)
	}

	var taskWinners atomic.Int64
	var taskConflicts atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, transitionErr := taskRepo.Transition(context.Background(), task.PublicID, owner, 1, model.AIImageTaskReserved, 10, now)
			switch {
			case transitionErr == nil:
				taskWinners.Add(1)
			case errors.Is(transitionErr, ErrImageTaskConflict):
				taskConflicts.Add(1)
			default:
				t.Errorf("任务并发流转异常: %v", transitionErr)
			}
		}()
	}
	wg.Wait()
	if taskWinners.Load() != 1 || taskConflicts.Load() != 99 {
		t.Fatalf("任务CAS必须只有一个胜者: winners=%d conflicts=%d", taskWinners.Load(), taskConflicts.Load())
	}

	assetRepo := NewImageAssetRepository(db)
	bucket := "ai-result"
	objectKey := "tenant/date/g3.jpeg"
	mime := "image/jpeg"
	size := uint64(100)
	sha := fmt.Sprintf("%064x", fixtureID+2)
	width := uint32(2048)
	height := uint32(2048)
	expiresAt := now.Add(30 * 24 * time.Hour)
	assetTemplate := model.AIImageAsset{
		PublicID: "asset_img_g3_mysql", UserID: userID, ProjectID: projectID, RequestID: requestID, TaskID: fixtureID,
		ResultIndex: 0, AssetRole: model.AIImageAssetPrimaryOutput, IsBillableOutput: true,
		Bucket: &bucket, ObjectKey: &objectKey, MIMEType: &mime, SizeBytes: &size, SHA256: &sha, Width: &width, Height: &height,
		Source: "provider_base64", ModerationStatus: model.AIModerationPassed,
		ExplicitLabelStatus: model.AIImageLabelApplied, ImplicitLabelStatus: model.AIImageLabelApplied,
		LifecycleState: model.AIImageAssetTemporary, RetentionPolicyID: "result-30d", ExpiresAt: expiresAt,
	}
	var assetWinners atomic.Int64
	var assetDuplicates atomic.Int64
	wg = sync.WaitGroup{}
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := assetTemplate
			createErr := assetRepo.Create(context.Background(), &candidate)
			if createErr == nil {
				assetWinners.Add(1)
			} else {
				assetDuplicates.Add(1)
			}
		}()
	}
	wg.Wait()
	if assetWinners.Load() != 1 || assetDuplicates.Load() != 99 {
		t.Fatalf("重复主图必须只有一个写入胜者: winners=%d duplicates=%d", assetWinners.Load(), assetDuplicates.Load())
	}
	primary, err := assetRepo.FindOwnedForInternal(context.Background(), assetTemplate.PublicID, owner)
	if err != nil {
		t.Fatal(err)
	}
	thumbnail := model.AIImageAsset{
		PublicID: "asset_img_g3_thumbnail", UserID: userID, ProjectID: projectID, RequestID: requestID, TaskID: fixtureID,
		ResultIndex: 0, AssetRole: model.AIImageAssetThumbnail, ParentAssetID: &primary.ID,
		Source: "derived", ModerationStatus: model.AIModerationPending,
		ExplicitLabelStatus: model.AIImageLabelPending, ImplicitLabelStatus: model.AIImageLabelPending,
		LifecycleState: model.AIImageAssetTemporary, RetentionPolicyID: "thumbnail-30d", ExpiresAt: expiresAt,
	}
	if err := assetRepo.Create(context.Background(), &thumbnail); err != nil {
		t.Fatalf("同请求派生缩略图应允许创建: %v", err)
	}
	derivedWithoutParent := model.AIImageAsset{
		PublicID: "asset_img_g3_no_parent", UserID: userID, ProjectID: projectID, RequestID: requestID, TaskID: fixtureID,
		ResultIndex: 3, AssetRole: model.AIImageAssetDerived, Source: "derived",
		ModerationStatus: model.AIModerationPending, ExplicitLabelStatus: model.AIImageLabelPending,
		ImplicitLabelStatus: model.AIImageLabelPending, LifecycleState: model.AIImageAssetTemporary,
		RetentionPolicyID: "derived-30d", ExpiresAt: expiresAt,
	}
	if err := assetRepo.Create(context.Background(), &derivedWithoutParent); err == nil {
		t.Fatal("派生资产缺少同请求父资产时必须被数据库拒绝")
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, owner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("temporary资产不得交付: %v", err)
	}
	available, err := assetRepo.TransitionLifecycle(context.Background(), assetTemplate.PublicID, owner, 1, model.AIImageAssetAvailable, now)
	if err != nil || available.VersionNo != 2 {
		t.Fatalf("资产进入available失败: asset=%+v err=%v", available, err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, owner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("请求未结算时available资产仍不得交付: %v", err)
	}
	if err := db.Exec("UPDATE ai_requests SET billing_status=?, delivery_status=? WHERE request_id=?", model.AIBillingSettled, model.AIDeliveryAvailable, requestID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, owner); err != nil {
		t.Fatalf("满足全部条件的主图应可交付: %v", err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, otherOwner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("跨用户资产查询必须统一不可访问: %v", err)
	}
	var disputeWinners atomic.Int64
	var disputeConflicts atomic.Int64
	wg = sync.WaitGroup{}
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, disputeErr := assetRepo.OpenDispute(context.Background(), assetTemplate.PublicID, owner, 2, now.Add(time.Minute))
			switch {
			case disputeErr == nil:
				disputeWinners.Add(1)
			case errors.Is(disputeErr, ErrImageAssetConflict):
				disputeConflicts.Add(1)
			default:
				t.Errorf("争议并发开启异常: %v", disputeErr)
			}
		}()
	}
	wg.Wait()
	if disputeWinners.Load() != 1 || disputeConflicts.Load() != 99 {
		t.Fatalf("争议开启CAS必须只有一个胜者: winners=%d conflicts=%d", disputeWinners.Load(), disputeConflicts.Load())
	}
	disputed, err := assetRepo.FindOwnedForInternal(context.Background(), assetTemplate.PublicID, owner)
	if err != nil || disputed.DisputeStatus != model.AIImageDisputeOpen || !disputed.LegalHold || disputed.VersionNo != 3 {
		t.Fatalf("开启争议必须同步legal hold: asset=%+v err=%v", disputed, err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, owner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("争议中资产不得普通下载: %v", err)
	}
	if _, err := assetRepo.TransitionLifecycle(context.Background(), assetTemplate.PublicID, owner, 3, model.AIImageAssetExpiring, now.Add(2*time.Minute)); !errors.Is(err, ErrImageAssetTransition) {
		t.Fatalf("争议/legal hold必须阻止清理: %v", err)
	}
	resolved, err := assetRepo.ResolveDispute(context.Background(), assetTemplate.PublicID, owner, 3, now.Add(3*time.Minute))
	if err != nil || resolved.DisputeStatus != model.AIImageDisputeResolved || !resolved.LegalHold || resolved.VersionNo != 4 {
		t.Fatalf("解决争议后必须继续保留legal hold: asset=%+v err=%v", resolved, err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), assetTemplate.PublicID, owner); err != nil {
		t.Fatalf("争议解决后可恢复普通交付，但保全仍保留: %v", err)
	}
	if _, err := assetRepo.TransitionLifecycle(context.Background(), assetTemplate.PublicID, owner, 4, model.AIImageAssetExpiring, now.Add(4*time.Minute)); !errors.Is(err, ErrImageAssetTransition) {
		t.Fatalf("争议解决但legal hold未释放时仍不得清理: %v", err)
	}

	quarantined := model.AIImageAsset{
		PublicID: "asset_img_g3_quarantined", UserID: userID, ProjectID: projectID, RequestID: requestID, TaskID: fixtureID,
		ResultIndex: 1, AssetRole: model.AIImageAssetPrimaryOutput, Source: "provider_base64",
		ModerationStatus: model.AIModerationRejected, ExplicitLabelStatus: model.AIImageLabelPending,
		ImplicitLabelStatus: model.AIImageLabelPending, LifecycleState: model.AIImageAssetQuarantined,
		RetentionPolicyID: "quarantine-30d", ExpiresAt: expiresAt,
	}
	if err := assetRepo.Create(context.Background(), &quarantined); err != nil {
		t.Fatal(err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), quarantined.PublicID, owner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("隔离资产不得交付: %v", err)
	}

	deleted := model.AIImageAsset{
		PublicID: "asset_img_g3_deleted", UserID: userID, ProjectID: projectID, RequestID: requestID, TaskID: fixtureID,
		ResultIndex: 2, AssetRole: model.AIImageAssetPrimaryOutput, Source: "provider_base64",
		ModerationStatus: model.AIModerationPending, ExplicitLabelStatus: model.AIImageLabelPending,
		ImplicitLabelStatus: model.AIImageLabelPending, LifecycleState: model.AIImageAssetTemporary,
		RetentionPolicyID: "temp-24h", ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := assetRepo.Create(context.Background(), &deleted); err != nil {
		t.Fatal(err)
	}
	deleting, err := assetRepo.TransitionLifecycle(context.Background(), deleted.PublicID, owner, 1, model.AIImageAssetDeleting, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assetRepo.OpenDispute(context.Background(), deleted.PublicID, owner, deleting.VersionNo, now.Add(30*time.Second)); !errors.Is(err, ErrImageAssetConflict) {
		t.Fatalf("资产进入删除序列后不得再开启争议或legal hold: %v", err)
	}
	if _, err := assetRepo.TransitionLifecycle(context.Background(), deleted.PublicID, owner, deleting.VersionNo, model.AIImageAssetDeleted, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), deleted.PublicID, owner); !errors.Is(err, ErrImageAssetAccess) {
		t.Fatalf("删除态资产不得交付: %v", err)
	}
}

func cleanupImageG3MySQLFixture(t *testing.T, db *gorm.DB, userID, otherUserID, projectID, otherProject, apiKeyID, otherKeyID uint64, modelCode string, fixtureID uint64) {
	t.Helper()
	statements := []struct {
		query string
		args  []interface{}
	}{
		{"DELETE FROM ai_gateway_assets WHERE user_id IN (?,?) AND parent_asset_id IS NOT NULL", []interface{}{userID, otherUserID}},
		{"DELETE FROM ai_gateway_assets WHERE user_id IN (?,?)", []interface{}{userID, otherUserID}},
		{"DELETE FROM ai_gateway_tasks WHERE user_id IN (?,?)", []interface{}{userID, otherUserID}},
		{"DELETE FROM ai_gateway_quotes WHERE user_id IN (?,?)", []interface{}{userID, otherUserID}},
		{"DELETE FROM ai_requests WHERE user_id IN (?,?)", []interface{}{userID, otherUserID}},
		{"DELETE FROM ai_price_versions WHERE id = ?", []interface{}{fixtureID}},
		{"DELETE FROM api_keys WHERE id IN (?,?)", []interface{}{apiKeyID, otherKeyID}},
		{"DELETE FROM ai_projects WHERE id IN (?,?)", []interface{}{projectID, otherProject}},
		{"DELETE FROM token_models WHERE logical_model_code = ?", []interface{}{modelCode}},
		{"DELETE FROM users WHERE id IN (?,?)", []interface{}{userID, otherUserID}},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("清理 IMG-G3 MySQL 夹具失败: %v", err)
		}
	}
}

func stringPtr(value string) *string { return &value }

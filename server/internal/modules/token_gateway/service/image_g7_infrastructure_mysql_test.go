package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/dto"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestImageG7InfrastructureClosedLoop(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G7_ISOLATED") != "YES" {
		t.Skip("IMG-G7只允许隔离基础设施门禁执行")
	}
	db, err := gorm.Open(mysql.Open(os.Getenv("MOLIN_IMAGE_G7_MYSQL_DSN")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil || databaseName != "molin_image_g7_contract" {
		t.Fatalf("拒绝非隔离数据库: database=%s err=%v", databaseName, err)
	}
	now := time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC)
	setupImageG5Base(t, db, now)
	if err := db.Exec("UPDATE token_models SET status='active',capabilities_json=JSON_ARRAY('image.generate'),release_version_no=1,published_at=? WHERE logical_model_code=?", now, imageG5ModelCode).Error; err != nil {
		t.Fatal(err)
	}
	userID := uint64(97501)
	seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
	store, err := imagegateway.NewMinIOObjectStore(imagegateway.MinIOObjectStoreConfig{
		Endpoint: os.Getenv("MOLIN_IMAGE_G7_MINIO_ENDPOINT"), PublicDownloadEndpoint: "https://images.example.invalid",
		AccessKey: os.Getenv("MOLIN_IMAGE_G7_MINIO_ACCESS"), SecretKey: os.Getenv("MOLIN_IMAGE_G7_MINIO_SECRET"),
		Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.EnsureBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	queue, err := imagegateway.NewImageTaskQueue(imagegateway.ImageTaskQueueConfig{
		URL: os.Getenv("MOLIN_IMAGE_G7_RABBIT_URL"), Exchange: "molin.image.g7.runtime", Queue: "molin.image.g7.runtime", RoutingKey: "image.generate",
		DeadExchange: "molin.image.g7.runtime.dead", DeadQueue: "molin.image.g7.runtime.dead", DeadRouting: "image.generate.dead",
	})
	if err != nil || queue.EnsureTopology(ctx) != nil {
		t.Fatalf("队列拓扑错误: %v", err)
	}
	adapter := imagegateway.NewFakeImageAdapter(imagegateway.FakeImageSuccess)
	metrics := NewAIGatewayMetrics(NewAIGatewayDBGaugeCollector(db))
	metrics.WithImageQueueGaugeCollector(NewImageQueueMetricsCollector(queue))
	metrics.AllowLogicalModel(imageG5ModelCode)
	observed, err := NewObservedImageAdapter(adapter, metrics)
	if err != nil {
		t.Fatal(err)
	}
	billing := newImageG5Service(t, db, observed, imagegateway.NewFakeModerationAdapter(imagegateway.FakeModerationAllow), store, now)
	pricing := NewImagePricingService(repository.NewG3PricingRepository(db))
	pricing.now = func() time.Time { return now }
	api, err := NewImageAPIService(db, billing, pricing, store, ImageAPISecrets{
		QuoteFingerprint: []byte("0123456789abcdef0123456789abcdef"), PromptHMAC: []byte("abcdef0123456789abcdef0123456789"),
	})
	if err != nil {
		t.Fatal(err)
	}
	api.now = func() time.Time { return now }
	api.quotes.now = func() time.Time { return now }
	api.WithVisibilityChecker(imageG6AllowVisibility{})
	limiter := &fakeImageResourceLimiter{}
	api.WithResourceLimiter(limiter)
	dispatcher, err := NewImageTaskDispatcher(queue, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return now }
	api.WithAsyncDispatcher(dispatcher)
	caller := ImageCaller{UserID: userID, APIKeyID: userID}
	quote, err := api.CreateQuote(ctx, caller, dto.ImageQuoteReq{Model: imageG5ModelCode, Prompt: "G7异步生成", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", ProjectID: userID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := api.Generate(ctx, ImageGenerationInput{
		Caller: caller, IdempotencyKey: "idem-image-g7-runtime-0001",
		Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "G7异步生成", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", QuoteID: quote.QuoteID},
	})
	if err != nil || result.Task.Status != model.AIImageTaskReserved || adapter.Calls() != 0 {
		t.Fatalf("异步分发前状态错误: task=%+v calls=%d err=%v", result, adapter.Calls(), err)
	}
	if err := queue.ConsumeOne(ctx, dispatcher); err != nil || adapter.Calls() != 1 {
		t.Fatalf("异步消费必须只调用一次Provider: calls=%d err=%v", adapter.Calls(), err)
	}
	settled, err := api.GetTask(ctx, caller, result.Task.TaskID, userID)
	if err != nil || settled.BillingStatus != model.AIBillingSettled || len(settled.Assets) != 2 {
		t.Fatalf("异步终态错误: task=%+v err=%v", settled, err)
	}
	if _, err := api.DownloadURL(ctx, caller, userID, settled.Assets[0].AssetID); err != nil {
		t.Fatalf("MinIO短效下载错误: %v", err)
	}

	// 模拟进程重启后内存Prompt丢失：队列消息只能触发未执行任务安全取消，不能猜测Prompt或重调Provider。
	quote = mustImageG6Quote(t, api, caller, userID, "G7丢失Prompt")
	missing, err := api.Generate(ctx, ImageGenerationInput{
		Caller: caller, IdempotencyKey: "idem-image-g7-missing-0001",
		Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "G7丢失Prompt", QuoteID: quote.QuoteID},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.mu.Lock()
	delete(dispatcher.jobs, missing.Task.RequestID)
	dispatcher.mu.Unlock()
	if err := queue.ConsumeOne(ctx, dispatcher); err != nil || adapter.Calls() != 1 {
		t.Fatalf("Prompt丢失必须释放且不重调Provider: calls=%d err=%v", adapter.Calls(), err)
	}
	cancelled, err := api.GetTask(ctx, caller, missing.Task.TaskID, userID)
	if err != nil || cancelled.Status != model.AIImageTaskCancelled || cancelled.BillingStatus != model.AIBillingReleased {
		t.Fatalf("Prompt丢失取消错误: task=%+v err=%v", cancelled, err)
	}

	// 用户在排队阶段取消后，既有Rabbit消息和重复终态消息都必须幂等Ack，不能进入DLQ或重调Provider。
	quote = mustImageG6Quote(t, api, caller, userID, "G7排队取消")
	queued, err := api.Generate(ctx, ImageGenerationInput{
		Caller: caller, IdempotencyKey: "idem-image-g7-queued-cancel-0001",
		Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "G7排队取消", QuoteID: quote.QuoteID},
	})
	if err != nil {
		t.Fatal(err)
	}
	queuedCancelled, err := api.CancelTask(ctx, caller, userID, queued.Task.TaskID)
	if err != nil || queuedCancelled.Status != model.AIImageTaskCancelled || queuedCancelled.BillingStatus != model.AIBillingReleased {
		t.Fatalf("排队取消终态错误: task=%+v err=%v", queuedCancelled, err)
	}
	if err := queue.ConsumeOne(ctx, dispatcher); err != nil || adapter.Calls() != 1 {
		t.Fatalf("取消后的原Rabbit消息必须Ack且不调Provider: calls=%d err=%v", adapter.Calls(), err)
	}
	if err := queue.Publish(ctx, queued.Task.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := queue.ConsumeOne(ctx, dispatcher); err != nil || adapter.Calls() != 1 {
		t.Fatalf("重复终态消息必须Ack且不调Provider: calls=%d err=%v", adapter.Calls(), err)
	}
	mainDepth, deadDepth, err := queue.QueueDepths()
	if err != nil || mainDepth != 0 || deadDepth != 0 {
		t.Fatalf("取消与终态重放不得进入DLQ: main=%d dead=%d err=%v", mainDepth, deadDepth, err)
	}

	// n=1 会形成主图和缩略图各一份；主图保持可交付以维持零差异，另建派生保全资产验证legal hold。
	var cleanupAssets []model.AIImageAsset
	if err := db.Where("request_id=?", settled.RequestID).Order("id").Find(&cleanupAssets).Error; err != nil || len(cleanupAssets) != 2 {
		t.Fatalf("n=1资产事实错误: count=%d err=%v", len(cleanupAssets), err)
	}
	var primary, thumbnail model.AIImageAsset
	for _, asset := range cleanupAssets {
		switch asset.AssetRole {
		case model.AIImageAssetPrimaryOutput:
			primary = asset
		case model.AIImageAssetThumbnail:
			thumbnail = asset
		}
	}
	if primary.ID == 0 || thumbnail.ID == 0 || thumbnail.ObjectKey == nil {
		t.Fatalf("主图或缩略图资产缺失: assets=%+v", cleanupAssets)
	}
	old := now.Add(-25 * time.Hour)
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", thumbnail.ID).Updates(map[string]interface{}{"lifecycle_state": model.AIImageAssetTemporary, "created_at": old, "updated_at": old}).Error; err != nil {
		t.Fatal(err)
	}
	heldKey := strings.TrimSuffix(*thumbnail.ObjectKey, "thumbnail.png") + "legal-hold.png"
	heldAsset := thumbnail
	heldAsset.ID = 0
	heldAsset.PublicID = "img_asset_g7_legal_hold"
	heldAsset.AssetRole = model.AIImageAssetDerived
	heldAsset.ParentAssetID = &primary.ID
	heldAsset.IsBillableOutput = false
	heldAsset.ObjectKey = &heldKey
	heldAsset.LifecycleState = model.AIImageAssetTemporary
	heldAsset.LegalHold = true
	heldAsset.VersionNo = 1
	heldAsset.CreatedAt, heldAsset.UpdatedAt = old, old
	if err := db.Create(&heldAsset).Error; err != nil {
		t.Fatal(err)
	}
	// settlement_pending主图即使超过24小时也可能关联资金与人工核对，绝不进入对象清理候选。
	pendingFixture := seedImageBillingFixture(t, db, 97502, "g7-cleanup-pending", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
	mustReserveImageG5(t, pendingFixture)
	if err := db.Model(&model.AIRequest{}).Where("request_id=?", pendingFixture.requestID).Update("billing_status", model.AIBillingSettlementPending).Error; err != nil {
		t.Fatal(err)
	}
	var pendingTask model.AIImageTask
	if err := db.Where("request_id=?", pendingFixture.requestID).First(&pendingTask).Error; err != nil {
		t.Fatal(err)
	}
	pendingKey := strings.TrimSuffix(*thumbnail.ObjectKey, "thumbnail.png") + "pending-primary.png"
	pendingAsset := primary
	pendingAsset.ID = 0
	pendingAsset.PublicID = "img_asset_g7_pending_primary"
	pendingAsset.UserID, pendingAsset.ProjectID = pendingFixture.owner.UserID, pendingFixture.owner.ProjectID
	pendingAsset.RequestID, pendingAsset.TaskID = pendingFixture.requestID, pendingTask.ID
	pendingAsset.ResultIndex, pendingAsset.ParentAssetID = 0, nil
	pendingAsset.ObjectKey = &pendingKey
	pendingAsset.LifecycleState, pendingAsset.VersionNo = model.AIImageAssetTemporary, 1
	pendingAsset.CreatedAt, pendingAsset.UpdatedAt = old, old
	if err := db.Create(&pendingAsset).Error; err != nil {
		t.Fatal(err)
	}

	// released请求仍有活动补偿时保持对象事实，待资金/交付补偿完成后再进入清理。
	var missingTask model.AIImageTask
	if err := db.Where("request_id=?", missing.Task.RequestID).First(&missingTask).Error; err != nil {
		t.Fatal(err)
	}
	compensatingKey := strings.TrimSuffix(*thumbnail.ObjectKey, "thumbnail.png") + "active-compensation.png"
	compensatingAsset := primary
	compensatingAsset.ID = 0
	compensatingAsset.PublicID = "img_asset_g7_active_compensation"
	compensatingAsset.RequestID, compensatingAsset.TaskID = missing.Task.RequestID, missingTask.ID
	compensatingAsset.ResultIndex, compensatingAsset.ParentAssetID = 0, nil
	compensatingAsset.ObjectKey = &compensatingKey
	compensatingAsset.LifecycleState, compensatingAsset.VersionNo = model.AIImageAssetTemporary, 1
	compensatingAsset.CreatedAt, compensatingAsset.UpdatedAt = old, old
	if err := db.Create(&compensatingAsset).Error; err != nil {
		t.Fatal(err)
	}
	activeClass := "release_failed"
	if err := db.Create(&model.AICompensationTask{
		TaskKey: "image:g7-active-cleanup-protection", TaskType: "image_reconcile", AggregateID: missing.Task.RequestID,
		Status: "pending", NextRetryAt: now, LastErrorClass: &activeClass,
	}).Error; err != nil {
		t.Fatal(err)
	}
	cleanup, err := NewImageCleanupWorker(db, store)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.now = func() time.Time { return now }
	cleanupResult, err := cleanup.RunBatch(ctx, 10)
	if err != nil || cleanupResult.Deleted != 1 {
		t.Fatalf("清理结果错误: result=%+v err=%v", cleanupResult, err)
	}
	var deleted, held, pendingProtected, compensationProtected model.AIImageAsset
	_ = db.First(&deleted, thumbnail.ID).Error
	_ = db.First(&held, heldAsset.ID).Error
	_ = db.First(&pendingProtected, pendingAsset.ID).Error
	_ = db.First(&compensationProtected, compensatingAsset.ID).Error
	if deleted.LifecycleState != model.AIImageAssetDeleted || held.LifecycleState != model.AIImageAssetTemporary || !held.LegalHold {
		t.Fatalf("清理/保全状态错误: deleted=%s held=%s legal=%t", deleted.LifecycleState, held.LifecycleState, held.LegalHold)
	}
	if pendingProtected.LifecycleState != model.AIImageAssetTemporary || compensationProtected.LifecycleState != model.AIImageAssetTemporary {
		t.Fatalf("资金pending或活动补偿资产不得进入清理: pending=%s compensation=%s", pendingProtected.LifecycleState, compensationProtected.LifecycleState)
	}

	// Delete成功但deleted终态写入首败时保持deleting，陈旧后以幂等Delete恢复。
	recoverySuccessKey := strings.TrimSuffix(*thumbnail.ObjectKey, "thumbnail.png") + "stale-delete-success.png"
	recoverySuccess := thumbnail
	recoverySuccess.ID = 0
	recoverySuccess.PublicID = "img_asset_g7_stale_delete_success"
	recoverySuccess.AssetRole, recoverySuccess.ResultIndex = model.AIImageAssetDerived, 8
	recoverySuccess.ParentAssetID = &primary.ID
	recoverySuccess.ObjectKey = &recoverySuccessKey
	recoverySuccess.LifecycleState, recoverySuccess.VersionNo = model.AIImageAssetDeleting, 1
	recoverySuccess.CreatedAt, recoverySuccess.UpdatedAt = old, old
	if err := db.Create(&recoverySuccess).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, imagegateway.ObjectRef{Bucket: *recoverySuccess.Bucket, Key: *recoverySuccess.ObjectKey}, strings.NewReader("stale-delete-success"), 1024); err != nil {
		t.Fatal(err)
	}
	const deletedTrigger = "trg_img_g7_deleted_terminal_failure"
	_ = db.Exec("DROP TRIGGER IF EXISTS " + deletedTrigger).Error
	t.Cleanup(func() { _ = db.Exec("DROP TRIGGER IF EXISTS " + deletedTrigger).Error })
	if err := db.Exec(`CREATE TRIGGER ` + deletedTrigger + ` BEFORE UPDATE ON ai_gateway_assets FOR EACH ROW
BEGIN
  IF NEW.public_id = 'img_asset_g7_stale_delete_success' AND NEW.lifecycle_state = 'deleted' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected deleted terminal failure';
  END IF;
END`).Error; err != nil {
		t.Fatal(err)
	}
	cleanup.now = func() time.Time { return now }
	if _, err := cleanup.RunBatch(ctx, 10); err == nil {
		t.Fatal("deleted终态写入首败必须返回错误并保留可恢复deleting")
	}
	if err := db.Exec("DROP TRIGGER IF EXISTS " + deletedTrigger).Error; err != nil {
		t.Fatal(err)
	}
	var staleDeleting model.AIImageAsset
	if err := db.First(&staleDeleting, recoverySuccess.ID).Error; err != nil || staleDeleting.LifecycleState != model.AIImageAssetDeleting {
		t.Fatalf("终态首败后必须保留deleting: asset=%+v err=%v", staleDeleting, err)
	}
	cleanup.now = func() time.Time { return now.Add(11 * time.Minute) }
	if result, err := cleanup.RunBatch(ctx, 10); err != nil || result.Deleted != 1 {
		t.Fatalf("陈旧deleting必须幂等恢复到deleted: result=%+v err=%v", result, err)
	}
	if err := db.First(&staleDeleting, recoverySuccess.ID).Error; err != nil || staleDeleting.LifecycleState != model.AIImageAssetDeleted {
		t.Fatalf("幂等恢复终态错误: asset=%+v err=%v", staleDeleting, err)
	}

	// Delete失败且delete_failed终态写入首败时同样保留deleting，下一批恢复到delete_failed。
	recoveryFailureKey := strings.TrimSuffix(*thumbnail.ObjectKey, "thumbnail.png") + "stale-delete-failure.png"
	recoveryFailure := thumbnail
	recoveryFailure.ID = 0
	recoveryFailure.PublicID = "img_asset_g7_stale_delete_failure"
	recoveryFailure.AssetRole, recoveryFailure.ResultIndex = model.AIImageAssetDerived, 9
	recoveryFailure.ParentAssetID = &primary.ID
	recoveryFailure.ObjectKey = &recoveryFailureKey
	recoveryFailure.LifecycleState, recoveryFailure.VersionNo = model.AIImageAssetDeleting, 1
	recoveryFailure.CreatedAt, recoveryFailure.UpdatedAt = old, old
	if err := db.Create(&recoveryFailure).Error; err != nil {
		t.Fatal(err)
	}
	const failedTrigger = "trg_img_g7_delete_failed_terminal_failure"
	_ = db.Exec("DROP TRIGGER IF EXISTS " + failedTrigger).Error
	t.Cleanup(func() { _ = db.Exec("DROP TRIGGER IF EXISTS " + failedTrigger).Error })
	if err := db.Exec(`CREATE TRIGGER ` + failedTrigger + ` BEFORE UPDATE ON ai_gateway_assets FOR EACH ROW
BEGIN
  IF NEW.public_id = 'img_asset_g7_stale_delete_failure' AND NEW.lifecycle_state = 'delete_failed' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected delete_failed terminal failure';
  END IF;
END`).Error; err != nil {
		t.Fatal(err)
	}
	failingCleanup, err := NewImageCleanupWorker(db, &g7DeleteFailStore{
		ObjectStore: store, failRef: imagegateway.ObjectRef{Bucket: *recoveryFailure.Bucket, Key: *recoveryFailure.ObjectKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	failingCleanup.now = func() time.Time { return now.Add(11 * time.Minute) }
	if result, err := failingCleanup.RunBatch(ctx, 10); err != nil || result.Failed != 1 {
		t.Fatalf("Delete失败首轮必须保留恢复机会: result=%+v err=%v", result, err)
	}
	if err := db.Exec("DROP TRIGGER IF EXISTS " + failedTrigger).Error; err != nil {
		t.Fatal(err)
	}
	staleDeleting = model.AIImageAsset{}
	if err := db.First(&staleDeleting, recoveryFailure.ID).Error; err != nil || staleDeleting.LifecycleState != model.AIImageAssetDeleting {
		t.Fatalf("delete_failed写入首败后必须保持deleting: asset=%+v err=%v", staleDeleting, err)
	}
	failingCleanup.now = func() time.Time { return now.Add(22 * time.Minute) }
	if result, err := failingCleanup.RunBatch(ctx, 10); err != nil || result.Failed != 1 {
		t.Fatalf("陈旧deleting删除仍失败时必须恢复到delete_failed: result=%+v err=%v", result, err)
	}
	staleDeleting = model.AIImageAsset{}
	if err := db.First(&staleDeleting, recoveryFailure.ID).Error; err != nil || staleDeleting.LifecycleState != model.AIImageAssetDeleteFailed {
		t.Fatalf("delete_failed恢复终态错误: asset=%+v err=%v", staleDeleting, err)
	}

	metricText, err := metrics.AIGatewayPrometheus(ctx)
	if err != nil || !strings.Contains(metricText, `request_type="image"`) || !strings.Contains(metricText, "molin_ai_gateway_image_tasks") || !strings.Contains(metricText, "molin_ai_gateway_image_assets") || !strings.Contains(metricText, `molin_ai_gateway_image_queue_depth{queue="dead"} 0`) {
		t.Fatalf("图片指标缺失: err=%v\n%s", err, metricText)
	}
	if !strings.Contains(metricText, "molin_ai_gateway_image_reconciliation_difference 0.00000000") {
		t.Fatalf("图片对账差异必须为0:\n%s", metricText)
	}
	report, err := billing.ReconcileRequest(ctx, settled.RequestID)
	if err != nil || !report.ZeroDifference() {
		t.Fatalf("清理缩略图不得改变主账对账: report=%+v err=%v", report, err)
	}
}

type g7DeleteFailStore struct {
	imagegateway.ObjectStore
	failRef imagegateway.ObjectRef
}

func (s *g7DeleteFailStore) Delete(ctx context.Context, ref imagegateway.ObjectRef) error {
	if ref == s.failRef {
		return errors.New("注入MinIO删除失败")
	}
	return s.ObjectStore.Delete(ctx, ref)
}

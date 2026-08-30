package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG4RepositoryLedgerMySQLRunsFakeT2VClosure(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G3_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置VID-G4隔离MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	const fixtureID = uint64(98701)
	const modelCode = "molin/video-g4-ledger"
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,?,'active','disabled','Asia/Shanghai')", fixtureID, fixtureID, "视频G4执行项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,?,?,'视频G4执行密钥','postpaid','','allowlist','active')",
		fixtureID, fixtureID, fixtureID, "g4-ledger", fmt.Sprintf("g4-ledger-hash-%d", fixtureID)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", fixtureID, modelCode, "视频G4执行模型", "video", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	limits := json.RawMessage("{\"meter_type\":\"video_seconds\",\"variants\":[{\"operation\":\"text_to_video\",\"resolution\":\"1280x720\",\"duration_seconds\":5,\"aspect_ratio\":\"16:9\",\"frame_rate\":24,\"audio\":true}]}")
	if err := db.Exec("INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(?,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,?,0.1,'non_commercial_test_fixture','vid-g4-ledger','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,?)",
		fixtureID, modelCode, limits, now, now.Add(time.Hour), now, fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	operation := model.AIVideoOperationTextToVideo
	projectID, apiKeyID := fixtureID, fixtureID
	requestID, taskPublicID := "vid_req_g4_ledger", "vid_task_g4_ledger"
	request := model.AIRequest{
		RequestID: requestID, UserID: fixtureID, ProjectID: &projectID, APIKeyID: &apiKeyID,
		LogicalModelCode: modelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: &operation,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending,
		BillingStatus: model.AIBillingHeld, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1,
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	quote := model.AIGatewayQuote{
		ID: fixtureID, PublicID: "vid_quote_g4_ledger", UserID: fixtureID, ProjectID: projectID, APIKeyID: &apiKeyID,
		LogicalModelCode: modelCode, Capability: model.AIVideoCapability, Operation: &operation,
		RequestFingerprint: fmt.Sprintf("%064x", fixtureID), RequestVariantHash: fmt.Sprintf("%064x", fixtureID+1),
		PriceVersionID: fixtureID, PriceSnapshotJSON: json.RawMessage("{\"schema_version\":3}"),
		QuotedAmount: decimal.RequireFromString("0.50000000"), Currency: "CNY", ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	inputJSON := json.RawMessage("{\"operation\":\"text_to_video\",\"resolution\":\"1280x720\",\"duration_seconds\":5,\"aspect_ratio\":\"16:9\",\"frame_rate\":24,\"audio\":true}")
	task := model.AIImageTask{
		ID: fixtureID, PublicID: taskPublicID, RequestID: requestID, QuoteID: fixtureID,
		UserID: fixtureID, ProjectID: projectID, APIKeyID: &apiKeyID, LogicalModelCode: modelCode,
		Capability: model.AIVideoCapability, Operation: &operation, Status: model.AIImageTaskCreated,
		InputJSON: inputJSON, VersionNo: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := NewVideoTaskPayloadProtector("vid-g4-test-key-v1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protector.Seal(task.ID, fixtureID, projectID, model.AITaskPayloadPrompt, []byte("仅在执行内存中出现的Fake提示词"))
	if err != nil {
		t.Fatal(err)
	}
	owner := repository.VideoOwner{UserID: fixtureID, ProjectID: projectID, APIKeyID: &apiKeyID}
	if err := repository.NewVideoTaskPayloadRepository(db, protector).Create(context.Background(), taskPublicID, owner, payload); err != nil {
		t.Fatal(err)
	}

	store := videogateway.NewFakeVideoObjectStore()
	ledger := NewVideoRepositoryTaskLedger(db, owner, protector, videoG4TestLocationFactory{}, nil)
	adapter := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{
		Ledger: ledger, Provider: adapter, Verifier: videogateway.NewFakeProviderCallbackVerifier([]byte("local-fixture-secret")),
		Probe:   videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()),
		Safety:  videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)),
		Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: store,
	})
	if _, err := videogateway.NewSubmitWorker(gateway).Run(context.Background(), taskPublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := videogateway.NewPollWorker(gateway).Run(context.Background(), taskPublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := videogateway.NewPollWorker(gateway).Run(context.Background(), taskPublicID); err != nil {
		t.Fatal(err)
	}
	final, err := videogateway.NewAssetFetchWorker(gateway).Run(context.Background(), taskPublicID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != videogateway.TaskSucceeded || final.Asset == nil || final.Asset.Lifecycle != videogateway.AssetAvailable {
		t.Fatalf("Repository Fake闭环未完成: status=%s asset=%+v", final.Status, final.Asset)
	}
	var eventCount int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=?", task.ID).Count(&eventCount).Error; err != nil || eventCount < 9 {
		t.Fatalf("每次状态推进必须追加TaskEvent: count=%d err=%v", eventCount, err)
	}
	var persisted model.AIImageAsset
	if err := db.Where("public_id=?", "vasset-"+taskPublicID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ModerationStatus != model.AIModerationPassed || persisted.ExplicitLabelStatus != model.AIImageLabelApplied ||
		persisted.ImplicitLabelStatus != model.AIImageLabelApplied || persisted.LifecycleState != model.AIImageAssetAvailable {
		t.Fatalf("共享资产审核和标识事实不完整: %+v", persisted)
	}
	assertVideoG4AssetTree(t, db, task.ID, persisted.ID)
	var protectedChild model.AIImageAsset
	if err := db.Where("task_id=? AND parent_asset_id=?", task.ID, persisted.ID).Order("id ASC").First(&protectedChild).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", protectedChild.ID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := gateway.DeleteContent(context.Background(), taskPublicID); !errors.Is(err, videogateway.ErrGatewayTaskTransition) {
		t.Fatalf("任一派生资产法律保全必须在删除正文前失败关闭: %v", err)
	}
	if _, err := store.Head(context.Background(), final.Asset.Object.Ref); err != nil {
		t.Fatalf("法律保全拒绝后正文必须仍存在: %v", err)
	}
	var nonAvailableCount int64
	if err := db.Model(&model.AIImageAsset{}).Where("task_id=? AND lifecycle_state<>'available'", task.ID).Count(&nonAvailableCount).Error; err != nil || nonAvailableCount != 0 {
		t.Fatalf("Prepare事务失败不得留下部分deleting: count=%d err=%v", nonAvailableCount, err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", protectedChild.ID).Update("legal_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.PrepareMediaDelete(context.Background(), taskPublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CompleteMediaDelete(context.Background(), taskPublicID, false); err != nil {
		t.Fatal(err)
	}
	var deleteFailedCount int64
	if err := db.Model(&model.AIImageAsset{}).Where("task_id=? AND lifecycle_state='delete_failed' AND media_deleted_at IS NULL", task.ID).Count(&deleteFailedCount).Error; err != nil || deleteFailedCount != 6 {
		t.Fatalf("删除故障必须原子保留六类可重试事实: count=%d err=%v", deleteFailedCount, err)
	}
	if err := gateway.DeleteContent(context.Background(), taskPublicID); err != nil {
		t.Fatalf("删除T2V媒体正文失败: %v", err)
	}
	var deletedMediaCount int64
	if err := db.Model(&model.AIImageAsset{}).Where("task_id=? AND media_deleted_at IS NOT NULL", task.ID).Count(&deletedMediaCount).Error; err != nil || deletedMediaCount != 6 {
		t.Fatalf("删除正文后六类资产事实必须保留并记录删除时间: count=%d err=%v", deletedMediaCount, err)
	}

	i2vID := fixtureID + 1
	i2vOperation := model.AIVideoOperationImageToVideo
	i2vRequestID, i2vTaskID := "vid_req_g4_ledger_i2v", "vid_task_g4_ledger_i2v"
	i2vRequest := model.AIRequest{
		RequestID: i2vRequestID, UserID: fixtureID, ProjectID: &projectID, APIKeyID: &apiKeyID,
		LogicalModelCode: modelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: &i2vOperation,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending,
		BillingStatus: model.AIBillingReleased, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1,
	}
	if err := db.Create(&i2vRequest).Error; err != nil {
		t.Fatal(err)
	}
	i2vQuote := quote
	i2vQuote.ID, i2vQuote.PublicID, i2vQuote.Operation = i2vID, "vid_quote_g4_ledger_i2v", &i2vOperation
	i2vQuote.RequestFingerprint, i2vQuote.RequestVariantHash = fmt.Sprintf("%064x", i2vID), fmt.Sprintf("%064x", i2vID+1)
	if err := db.Create(&i2vQuote).Error; err != nil {
		t.Fatal(err)
	}
	i2vInputJSON := json.RawMessage("{\"operation\":\"image_to_video\",\"resolution\":\"1280x720\",\"duration_seconds\":5,\"aspect_ratio\":\"16:9\",\"frame_rate\":24,\"audio\":true}")
	i2vTask := model.AIImageTask{
		ID: i2vID, PublicID: i2vTaskID, RequestID: i2vRequestID, QuoteID: i2vID,
		UserID: fixtureID, ProjectID: projectID, APIKeyID: &apiKeyID, LogicalModelCode: modelCode,
		Capability: model.AIVideoCapability, Operation: &i2vOperation, Status: model.AIImageTaskCreated,
		InputJSON: i2vInputJSON, VersionNo: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&i2vTask).Error; err != nil {
		t.Fatal(err)
	}
	referenceBytes := videoG4TestPNG(t)
	referenceDigest := sha256.Sum256(referenceBytes)
	normalizedHash := hex.EncodeToString(referenceDigest[:])
	upload := model.AIUploadSession{
		ID: i2vID, PublicID: "vid_upload_g4_ledger_i2v", UserID: fixtureID, ProjectID: projectID, APIKeyID: &apiKeyID,
		Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: model.AIUploadSourcePlatformPresigned,
		Status: model.AIUploadSessionVerifying, MIMEType: model.AIInputMIMEPNG, SizeBytes: 128,
		Bucket: "video-temp", ObjectKey: "i2v/source.png", ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	mimeType, sizeBytes, width, height := model.AIInputMIMEPNG, uint64(128), uint32(16), uint32(9)
	inputAsset := model.AIGatewayInputAsset{
		ID: i2vID, PublicID: "vin_g4_ledger_i2v", UserID: fixtureID, ProjectID: projectID,
		SourceType: model.AIUploadSourcePlatformPresigned, UploadSessionID: &upload.ID,
		OriginalSHA256: normalizedHash, NormalizedSHA256: &normalizedHash,
		Bucket: &upload.Bucket, ObjectKey: &upload.ObjectKey, MIMEType: &mimeType, SizeBytes: &sizeBytes,
		Width: &width, Height: &height, ModerationPolicyVersion: stringPointerForVideoG4Test("fake-input-v1"),
		ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&inputAsset).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIUploadSession{}).Where("id=?", upload.ID).Updates(map[string]interface{}{
		"status": model.AIUploadSessionCompleted, "final_input_asset_id": inputAsset.ID,
		"source_etag": "fixture-etag-i2v", "completed_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewVideoTaskInputRepository(db).BindReadyInput(context.Background(), i2vTaskID, inputAsset.PublicID, owner, now); err != nil {
		t.Fatal(err)
	}
	i2vPayload, err := protector.Seal(i2vTask.ID, fixtureID, projectID, model.AITaskPayloadPrompt, []byte("图生视频Fake提示词"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.NewVideoTaskPayloadRepository(db, protector).Create(context.Background(), i2vTaskID, owner, i2vPayload); err != nil {
		t.Fatal(err)
	}
	i2vLedger := NewVideoRepositoryTaskLedger(db, owner, protector, videoG4TestLocationFactory{}, func(_ context.Context, _ model.AIGatewayInputAsset) (*videogateway.NormalizedReferenceImage, error) {
		return &videogateway.NormalizedReferenceImage{Bytes: referenceBytes, MIMEType: "image/png", Width: 16, Height: 9, OriginalSHA256: normalizedHash, NormalizedSHA256: normalizedHash}, nil
	})
	i2vGateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{
		Ledger: i2vLedger, Provider: videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess),
		Verifier: videogateway.NewFakeProviderCallbackVerifier([]byte("local-fixture-secret")),
		Probe:    videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()),
		Safety:   videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)),
		Labeler:  videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: store,
	})
	if _, err := videogateway.NewSubmitWorker(i2vGateway).Run(context.Background(), i2vTaskID); err != nil {
		t.Fatal(err)
	}
	_, _ = videogateway.NewPollWorker(i2vGateway).Run(context.Background(), i2vTaskID)
	_, _ = videogateway.NewPollWorker(i2vGateway).Run(context.Background(), i2vTaskID)
	i2vFinal, err := videogateway.NewAssetFetchWorker(i2vGateway).Run(context.Background(), i2vTaskID)
	if err != nil || i2vFinal.Status != videogateway.TaskSucceeded || i2vFinal.Asset == nil || i2vFinal.Asset.Lifecycle != videogateway.AssetAvailable {
		t.Fatalf("I2V Repository Fake闭环未完成: task=%+v err=%v", i2vFinal, err)
	}
	var i2vContent model.AIImageAsset
	if err := db.Where("public_id=?", "vasset-"+i2vTaskID).First(&i2vContent).Error; err != nil {
		t.Fatal(err)
	}
	assertVideoG4AssetTree(t, db, i2vTask.ID, i2vContent.ID)
	var binding model.AIGatewayTaskInput
	if err := db.Where("task_id=? AND input_asset_id=?", i2vTask.ID, inputAsset.ID).First(&binding).Error; err != nil || binding.LeaseReleasedAt == nil {
		t.Fatalf("I2V成功终态必须释放真实输入租约: binding=%+v err=%v", binding, err)
	}
	assertVideoG4AdvanceRollsBackAssetFailure(t, db, protector, owner, quote, now, fixtureID+20)
}

func assertVideoG4AdvanceRollsBackAssetFailure(t *testing.T, db *gorm.DB, protector *VideoTaskPayloadProtector, owner repository.VideoOwner, baseQuote model.AIGatewayQuote, now time.Time, fixtureID uint64) {
	t.Helper()
	operation := model.AIVideoOperationTextToVideo
	requestID := fmt.Sprintf("vid_req_g4_atomic_%d", fixtureID)
	taskID := fmt.Sprintf("vid_task_g4_atomic_%d", fixtureID)
	projectID, apiKeyID := owner.ProjectID, *owner.APIKeyID
	request := model.AIRequest{RequestID: requestID, UserID: owner.UserID, ProjectID: &projectID, APIKeyID: &apiKeyID,
		LogicalModelCode: baseQuote.LogicalModelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: &operation,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending,
		BillingStatus: model.AIBillingHeld, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	quote := baseQuote
	quote.ID, quote.PublicID, quote.Operation = fixtureID, fmt.Sprintf("vid_quote_g4_atomic_%d", fixtureID), &operation
	quote.RequestFingerprint, quote.RequestVariantHash = fmt.Sprintf("%064x", fixtureID), fmt.Sprintf("%064x", fixtureID+1)
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	task := model.AIImageTask{ID: fixtureID, PublicID: taskID, RequestID: requestID, QuoteID: fixtureID,
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: baseQuote.LogicalModelCode,
		Capability: model.AIVideoCapability, Operation: &operation, Status: model.AIImageTaskStoring,
		InputJSON: json.RawMessage("{\"operation\":\"text_to_video\",\"resolution\":\"1280x720\",\"duration_seconds\":5,\"aspect_ratio\":\"16:9\",\"frame_rate\":24,\"audio\":true}"),
		VersionNo: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := protector.Seal(task.ID, owner.UserID, owner.ProjectID, model.AITaskPayloadPrompt, []byte("事务回滚Fake提示词"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.NewVideoTaskPayloadRepository(db, protector).Create(context.Background(), taskID, owner, payload); err != nil {
		t.Fatal(err)
	}
	callbackName := fmt.Sprintf("vid_g4_asset_failure_%d", fixtureID)
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "ai_gateway_assets" {
			tx.AddError(errors.New("Fake资产持久化故障"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove(callbackName)
	ledger := NewVideoRepositoryTaskLedger(db, owner, protector, videoG4TestLocationFactory{}, nil)
	_, err = ledger.Advance(context.Background(), taskID, 1, videogateway.TaskModerating, "worker", "atomic_failure", func(next *videogateway.GatewayTask) error {
		next.Asset = &videogateway.GatewayAsset{AssetID: "vasset-" + taskID, Role: model.AIImageAssetContent,
			Object:   videogateway.StoredVideoObject{Ref: videogateway.VideoObjectRef{Bucket: "video-temp", ObjectKey: taskID + "/vasset-" + taskID + "/content.bin"}},
			MIMEType: "video/mp4", SizeBytes: 1024, SHA256: fmt.Sprintf("%064x", fixtureID), Width: 1280, Height: 720,
			DurationMillis: 5000, FrameRate: 24, VideoCodec: "avc1", AudioCodec: "mp4a", HasAudio: true,
			Lifecycle: videogateway.AssetTemporary, ModerationStatus: videogateway.AssetModerationPending,
			ExplicitLabelStatus: videogateway.LabelPending, ImplicitLabelStatus: videogateway.LabelPending}
		return nil
	})
	if err == nil {
		t.Fatal("资产持久化故障必须让Advance失败")
	}
	var persistedTask model.AIImageTask
	if err := db.Where("id=?", task.ID).First(&persistedTask).Error; err != nil {
		t.Fatal(err)
	}
	var assetCount, eventCount int64
	_ = db.Model(&model.AIImageAsset{}).Where("task_id=?", task.ID).Count(&assetCount).Error
	_ = db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=?", task.ID).Count(&eventCount).Error
	if persistedTask.Status != model.AIImageTaskStoring || persistedTask.VersionNo != 1 || assetCount != 0 || eventCount != 0 {
		t.Fatalf("状态、事件和资产必须随事务一起回滚: task=%+v assets=%d events=%d", persistedTask, assetCount, eventCount)
	}
}

type videoG4TestLocationFactory struct{}

func (videoG4TestLocationFactory) NewVideoObjectLocation(_ context.Context, _ repository.VideoOwner, taskPublicID, assetPublicID, role string, _ uint32) (repository.VideoObjectLocation, error) {
	bucket := "video-result"
	if role == model.AIImageAssetContent {
		bucket = "video-temp"
	}
	return repository.VideoObjectLocation{Bucket: bucket, ObjectKey: taskPublicID + "/" + assetPublicID + "/" + role + ".bin"}, nil
}

func videoG4TestProbeLimits() videogateway.VideoProbeLimits {
	return videogateway.VideoProbeLimits{
		MaxBytes: 8 << 20, MaxBoxBytes: 1 << 20, MaxDurationMillis: 60_000,
		MaxWidth: 4096, MaxHeight: 4096, MinFrameRate: 1, MaxFrameRate: 120,
		AllowedVideoCodecs: map[string]bool{"avc1": true}, AllowedAudioCodecs: map[string]bool{"mp4a": true},
		MaxProbeDuration: time.Second, MaxTopLevelBoxes: 16,
	}
}

func stringPointerForVideoG4Test(value string) *string { return &value }

func assertVideoG4AssetTree(t *testing.T, db *gorm.DB, taskID, contentID uint64) {
	t.Helper()
	var assetCount, childCount int64
	if err := db.Model(&model.AIImageAsset{}).Where("task_id=?", taskID).Count(&assetCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("task_id=? AND parent_asset_id=?", taskID, contentID).Count(&childCount).Error; err != nil {
		t.Fatal(err)
	}
	if assetCount != 6 || childCount != 5 {
		t.Fatalf("content与五类派生资产父子关系错误: assets=%d children=%d", assetCount, childCount)
	}
}

func videoG4TestPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 16, 9))
	for y := 0; y < 9; y++ {
		for x := 0; x < 16; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(x + 1), G: uint8(y + 1), B: 80, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

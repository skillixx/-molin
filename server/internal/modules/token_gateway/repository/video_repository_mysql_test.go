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

func TestVideoG3RepositoryMySQLTaskInputCallbackAsset(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G3_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置VID-G3隔离MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(140)
	sqlDB.SetMaxIdleConns(140)
	t.Cleanup(func() { _ = sqlDB.Close() })

	const (
		userID        = uint64(98001)
		otherUserID   = uint64(98002)
		projectID     = uint64(98001)
		otherProject  = uint64(98002)
		secondProject = uint64(98003)
		apiKeyID      = uint64(98001)
		otherKeyID    = uint64(98002)
		secondKeyID   = uint64(98003)
		priceID       = uint64(98001)
		modelCode     = "molin/video-g3-mysql"
	)
	seedVideoG3Principals(t, db, userID, otherUserID, projectID, otherProject, secondProject, apiKeyID, otherKeyID, secondKeyID, priceID, modelCode)
	now := time.Now().UTC().Truncate(time.Second)
	owner := VideoOwner{UserID: userID, ProjectID: projectID, APIKeyID: videoG3Uint64Ptr(apiKeyID)}
	wrongUser := VideoOwner{UserID: otherUserID, ProjectID: otherProject, APIKeyID: videoG3Uint64Ptr(otherKeyID)}
	wrongUserOnly := VideoOwner{UserID: otherUserID, ProjectID: projectID, APIKeyID: videoG3Uint64Ptr(apiKeyID)}
	wrongProject := VideoOwner{UserID: userID, ProjectID: secondProject, APIKeyID: videoG3Uint64Ptr(secondKeyID)}
	wrongKey := VideoOwner{UserID: userID, ProjectID: projectID, APIKeyID: videoG3Uint64Ptr(otherKeyID)}

	// 任务CAS与横向归属隔离。
	t2v := seedVideoG3Task(t, db, 98101, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskCreated, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	taskRepo := NewVideoTaskRepository(db)
	if _, err := taskRepo.FindForOwner(context.Background(), t2v.PublicID, wrongUser); !errors.Is(err, ErrVideoTaskNotFound) {
		t.Fatalf("跨用户/Project查询必须隐藏任务: %v", err)
	}
	if _, err := taskRepo.FindForOwner(context.Background(), t2v.PublicID, wrongUserOnly); !errors.Is(err, ErrVideoTaskNotFound) {
		t.Fatalf("单独跨用户查询必须隐藏任务: %v", err)
	}
	if _, err := taskRepo.FindForOwner(context.Background(), t2v.PublicID, wrongProject); !errors.Is(err, ErrVideoTaskNotFound) {
		t.Fatalf("同用户跨Project查询必须隐藏任务: %v", err)
	}
	if _, err := taskRepo.FindForOwner(context.Background(), t2v.PublicID, wrongKey); !errors.Is(err, ErrVideoTaskNotFound) {
		t.Fatalf("跨API Key查询必须隐藏任务: %v", err)
	}
	var winners, conflicts atomic.Int64
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, transitionErr := taskRepo.TransitionExecution(context.Background(), VideoStateTransition{
				TaskPublicID: t2v.PublicID, Owner: owner, ExpectedVersion: 1,
				ToStatus: model.AIImageTaskReserved, Progress: 5, EventID: fmt.Sprintf("vid_g3_task_cas_%03d", index),
				Source: "system", SafeDetailJSON: json.RawMessage(`{"reason":"cas_test"}`), Now: now,
			})
			switch {
			case transitionErr == nil:
				winners.Add(1)
			case errors.Is(transitionErr, ErrVideoTaskConflict):
				conflicts.Add(1)
			default:
				t.Errorf("任务CAS返回异常: %v", transitionErr)
			}
		}(index)
	}
	group.Wait()
	if winners.Load() != 1 || conflicts.Load() != 99 {
		t.Fatalf("100并发任务CAS必须一胜九十九冲突: winners=%d conflicts=%d", winners.Load(), conflicts.Load())
	}
	var taskEventCount int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=?", t2v.ID).Count(&taskEventCount).Error; err != nil || taskEventCount != 1 {
		t.Fatalf("CAS只允许追加一个状态事件: count=%d err=%v", taskEventCount, err)
	}
	if input, err := NewVideoInputAssetRepository(db).ValidateTaskInputForProvider(context.Background(), t2v.PublicID, owner, now); err != nil || input != nil {
		t.Fatalf("T2V必须保持零输入: input=%+v err=%v", input, err)
	}
	unsafeTaskJSON := json.RawMessage(`{"operation":"text_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false,"message":"Prompt正文"}`)
	if err := db.Model(&model.AIImageTask{}).Where("id=?", t2v.ID).Update("input_json", unsafeTaskJSON).Error; err == nil {
		t.Fatal("视频Task input_json未知键必须被数据库白名单拒绝")
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=?", t2v.ID).Update("result_json", json.RawMessage(`{"data":"Provider正文"}`)).Error; err == nil {
		t.Fatal("VID-G3视频Task result_json必须保持为空")
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=?", t2v.ID).Update("error_message_safe", "Prompt正文").Error; err == nil {
		t.Fatal("VID-G3视频Task普通错误正文必须保持为空")
	}

	// I2V唯一输入、快照复核、跨Key隐藏与租约保护。
	i2v := seedVideoG3Task(t, db, 98102, priceID, owner, modelCode, model.AIVideoOperationImageToVideo, model.AIImageTaskCreated, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	input := seedVideoG3ReadyInput(t, db, 98201, owner, now, "a")
	taskInputRepo := NewVideoTaskInputRepository(db)
	binding, err := taskInputRepo.BindReadyInput(context.Background(), i2v.PublicID, input.PublicID, owner, now)
	if err != nil {
		t.Fatalf("I2V绑定ready输入失败: %v", err)
	}
	validated, err := NewVideoInputAssetRepository(db).ValidateTaskInputForProvider(context.Background(), i2v.PublicID, owner, now)
	if err != nil || validated.ID != binding.ID {
		t.Fatalf("Provider前快照复核失败: input=%+v err=%v", validated, err)
	}
	if _, err := NewVideoInputAssetRepository(db).FindForOwner(context.Background(), input.PublicID, wrongKey); !errors.Is(err, ErrVideoInputNotFound) {
		t.Fatalf("跨API Key输入查询必须统一不存在: %v", err)
	}
	if _, err := NewVideoInputAssetRepository(db).FindForOwner(context.Background(), input.PublicID, wrongProject); !errors.Is(err, ErrVideoInputNotFound) {
		t.Fatalf("同用户跨Project输入查询必须统一不存在: %v", err)
	}
	if _, err := NewVideoUploadSessionRepository(db).FindForOwner(context.Background(), "vid_g3_upload_a", wrongKey); !errors.Is(err, ErrVideoUploadNotFound) {
		t.Fatalf("跨API Key上传会话查询必须统一不存在: %v", err)
	}
	if _, err := NewVideoUploadSessionRepository(db).FindForOwner(context.Background(), "vid_g3_upload_a", wrongProject); !errors.Is(err, ErrVideoUploadNotFound) {
		t.Fatalf("同用户跨Project上传会话查询必须统一不存在: %v", err)
	}
	if _, err := taskInputRepo.ListForOwner(context.Background(), i2v.PublicID, wrongProject); !errors.Is(err, ErrVideoInputNotFound) {
		t.Fatalf("同用户跨Project TaskInput查询必须统一不存在: %v", err)
	}
	if err := db.Model(&model.AIGatewayInputAsset{}).Where("id=?", input.ID).Update("normalized_sha256", fmt.Sprintf("%064x", 88)).Error; err == nil {
		t.Fatal("ready输入hash替换必须被数据库触发器拒绝")
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=?", i2v.ID).Update("status", model.AIImageTaskPendingReconcile).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewVideoInputAssetRepository(db).ReleaseTaskLeases(context.Background(), i2v.PublicID, owner, now); !errors.Is(err, ErrVideoInputLeaseActive) {
		t.Fatalf("pending_reconcile不得释放输入租约: %v", err)
	}

	// 已生成图片只能通过同User、Project和API Key的不可变快照进入I2V。
	generatedInput := seedVideoG3GeneratedImageInput(t, db, 98301, priceID, owner, modelCode, now)
	generatedTask := seedVideoG3Task(t, db, 98107, priceID, owner, modelCode, model.AIVideoOperationImageToVideo, model.AIImageTaskCreated, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	if _, err := taskInputRepo.BindReadyInput(context.Background(), generatedTask.PublicID, generatedInput.PublicID, owner, now); err != nil {
		t.Fatalf("同归属GeneratedImageAsset快照应可绑定I2V: %v", err)
	}
	if _, err := NewVideoInputAssetRepository(db).FindForOwner(context.Background(), generatedInput.PublicID, wrongKey); !errors.Is(err, ErrVideoInputNotFound) {
		t.Fatalf("跨API Key GeneratedImageAsset快照必须统一不存在: %v", err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", *generatedInput.SourceGatewayAssetID).Update("lifecycle_state", model.AIImageAssetTemporary).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", *generatedInput.SourceGatewayAssetID).Update("moderation_status", model.AIModerationRejected).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", *generatedInput.SourceGatewayAssetID).Update("lifecycle_state", model.AIImageAssetQuarantined).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewVideoInputAssetRepository(db).ValidateTaskInputForProvider(context.Background(), generatedTask.PublicID, owner, now); !errors.Is(err, ErrVideoInputSnapshotDrift) {
		t.Fatalf("GeneratedImageAsset隔离后Provider前必须失败关闭: %v", err)
	}
	blockedGeneratedTask := seedVideoG3Task(t, db, 98108, priceID, owner, modelCode, model.AIVideoOperationImageToVideo, model.AIImageTaskCreated, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	if _, err := taskInputRepo.BindReadyInput(context.Background(), blockedGeneratedTask.PublicID, generatedInput.PublicID, owner, now); !errors.Is(err, ErrVideoInputUnavailable) {
		t.Fatalf("GeneratedImageAsset隔离后不得建立新TaskInput: %v", err)
	}
	sourceAssetID := *generatedInput.SourceGatewayAssetID
	restoreGeneratedSource := func() {
		t.Helper()
		if err := db.Model(&model.AIImageAsset{}).Where("id=?", sourceAssetID).Updates(map[string]interface{}{
			"lifecycle_state": model.AIImageAssetTemporary, "deleted_at": nil, "media_deleted_at": nil,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIImageAsset{}).Where("id=?", sourceAssetID).Updates(map[string]interface{}{
			"moderation_status":     model.AIModerationPassed,
			"explicit_label_status": model.AIImageLabelApplied, "implicit_label_status": model.AIImageLabelApplied,
			"expires_at":     now.Add(30 * 24 * time.Hour),
			"dispute_status": model.AIImageDisputeNone, "dispute_opened_at": nil, "dispute_resolved_at": nil, "legal_hold": false,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIImageAsset{}).Where("id=?", sourceAssetID).Update("lifecycle_state", model.AIImageAssetAvailable).Error; err != nil {
			t.Fatal(err)
		}
	}
	restoreGeneratedSource()
	unsafeSourceMutations := []struct {
		name    string
		updates map[string]interface{}
	}{
		{name: "过期", updates: map[string]interface{}{"expires_at": now.Add(-time.Second)}},
		{name: "删除中", updates: map[string]interface{}{"lifecycle_state": model.AIImageAssetDeleting}},
		{name: "已删除", updates: map[string]interface{}{"lifecycle_state": model.AIImageAssetDeleted, "deleted_at": now, "media_deleted_at": now}},
		{name: "争议中", updates: map[string]interface{}{"dispute_status": model.AIImageDisputeOpen, "dispute_opened_at": now, "legal_hold": true}},
		{name: "未双标识", updates: map[string]interface{}{"lifecycle_state": model.AIImageAssetTemporary, "explicit_label_status": model.AIImageLabelPending, "implicit_label_status": model.AIImageLabelPending}},
	}
	for _, item := range unsafeSourceMutations {
		t.Run("GeneratedImageAsset"+item.name, func(t *testing.T) {
			if err := db.Model(&model.AIImageAsset{}).Where("id=?", sourceAssetID).Updates(item.updates).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := NewVideoInputAssetRepository(db).ValidateTaskInputForProvider(context.Background(), generatedTask.PublicID, owner, now); !errors.Is(err, ErrVideoInputSnapshotDrift) {
				t.Fatalf("失效GeneratedImageAsset必须在Provider前失败关闭: %v", err)
			}
			restoreGeneratedSource()
		})
	}
	wrongOwnerGeneratedInput := *generatedInput
	wrongOwnerGeneratedInput.ID, wrongOwnerGeneratedInput.PublicID = 98302, "vid_g3_generated_input_wrong_owner"
	wrongOwnerGeneratedInput.UserID, wrongOwnerGeneratedInput.ProjectID = otherUserID, otherProject
	if err := db.Create(&wrongOwnerGeneratedInput).Error; err == nil {
		t.Fatal("跨用户/Project引用GeneratedImageAsset必须被组合外键拒绝")
	}

	// 绑定与删除100并发只能形成绑定或pending_delete之一，不得留下悬空TaskInput。
	raceTask := seedVideoG3Task(t, db, 98103, priceID, owner, modelCode, model.AIVideoOperationImageToVideo, model.AIImageTaskCreated, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	raceInput := seedVideoG3ReadyInput(t, db, 98202, owner, now, "b")
	group = sync.WaitGroup{}
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if index%2 == 0 {
				_, _ = taskInputRepo.BindReadyInput(context.Background(), raceTask.PublicID, raceInput.PublicID, owner, now)
				return
			}
			_, _ = NewVideoInputAssetRepository(db).RequestDelete(context.Background(), raceInput.PublicID, owner, 1, now)
		}(index)
	}
	group.Wait()
	var raceBindingCount int64
	if err := db.Model(&model.AIGatewayTaskInput{}).Where("task_id=? AND input_asset_id=?", raceTask.ID, raceInput.ID).Count(&raceBindingCount).Error; err != nil {
		t.Fatal(err)
	}
	var raceState string
	if err := db.Model(&model.AIGatewayInputAsset{}).Select("lifecycle_state").Where("id=?", raceInput.ID).Scan(&raceState).Error; err != nil {
		t.Fatal(err)
	}
	if !((raceBindingCount == 1 && raceState == model.AIInputAssetReady) || (raceBindingCount == 0 && raceState == model.AIInputAssetPendingDelete)) {
		t.Fatalf("绑定/删除竞争产生悬空事实: bindings=%d state=%s", raceBindingCount, raceState)
	}

	// 回调重放、正文冲突、乱序、未知任务与pending_reconcile迟到成功。
	callbackTask := seedVideoG3Task(t, db, 98104, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskSubmitted, model.AIBillingHeld, model.AIDeliveryPending, "fake", "fake-task-98104")
	callbackRepo := NewVideoProviderCallbackEventRepository(db)
	callback := VideoProviderCallbackCommand{
		ProviderCode: "fake", ProviderTaskID: "fake-task-98104", ExternalEventID: "evt-processing",
		RawBody: []byte(`{"status":"processing"}`), SignatureStatus: "valid", ToStatus: model.AIImageTaskProcessing,
		EventID: "vid_g3_callback_processing", SafeResultJSON: json.RawMessage(`{"status":"processing"}`), ReceivedAt: now,
	}
	first, err := callbackRepo.RecordAndApply(context.Background(), callback)
	if err != nil || !first.Applied || first.Replayed {
		t.Fatalf("首次合法回调应应用: outcome=%+v err=%v", first, err)
	}
	replay, err := callbackRepo.RecordAndApply(context.Background(), callback)
	if err != nil || !replay.Replayed || !replay.Applied {
		t.Fatalf("重复回调必须幂等ACK: outcome=%+v err=%v", replay, err)
	}
	if err := db.Model(&model.AIGatewayProviderCallbackEvent{}).Where("id=?", first.Event.ID).Update("application_result_json", json.RawMessage(`{"result":"ignored"}`)).Error; err == nil {
		t.Fatal("Provider回调终态应用结果不得二次覆盖")
	}
	if err := db.Delete(&model.AIGatewayProviderCallbackEvent{}, first.Event.ID).Error; err == nil {
		t.Fatal("Provider回调事实不得删除")
	}
	conflicting := callback
	conflicting.RawBody = []byte(`{"status":"failed"}`)
	if _, err := callbackRepo.RecordAndApply(context.Background(), conflicting); !errors.Is(err, ErrVideoCallbackBodyConflict) {
		t.Fatalf("同event_id不同body必须失败关闭: %v", err)
	}
	outOfOrder := callback
	outOfOrder.ExternalEventID, outOfOrder.EventID, outOfOrder.ToStatus = "evt-old", "vid_g3_callback_old", model.AIImageTaskSubmitted
	outOfOrder.RawBody = []byte(`{"status":"submitted"}`)
	ignored, err := callbackRepo.RecordAndApply(context.Background(), outOfOrder)
	if err != nil || ignored.Applied || ignored.Event.ProcessStatus != "ignored" {
		t.Fatalf("乱序回调必须安全忽略: outcome=%+v err=%v", ignored, err)
	}
	unknown := callback
	unknown.ProviderTaskID, unknown.ExternalEventID, unknown.EventID = "wrong-task", "evt-unknown", "vid_g3_callback_unknown"
	unknown.RawBody = []byte(`{"status":"succeeded"}`)
	unknown.ToStatus = model.AIImageTaskSucceeded
	unknownResult, err := callbackRepo.RecordAndApply(context.Background(), unknown)
	if err != nil || unknownResult.Applied || unknownResult.Event.TaskID != nil {
		t.Fatalf("未知或错绑Provider任务不得推进本地任务: outcome=%+v err=%v", unknownResult, err)
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=?", callbackTask.ID).Update("status", model.AIImageTaskPendingReconcile).Error; err != nil {
		t.Fatal(err)
	}
	late := callback
	late.ExternalEventID, late.EventID, late.ToStatus = "evt-late-success", "vid_g3_callback_late_success", model.AIImageTaskSucceeded
	late.RawBody = []byte(`{"status":"succeeded"}`)
	lateResult, err := callbackRepo.RecordAndApply(context.Background(), late)
	if err != nil || !lateResult.Applied {
		t.Fatalf("pending_reconcile迟到成功必须安全收敛: outcome=%+v err=%v", lateResult, err)
	}

	// TaskEvent只能追加，数据库必须拒绝UPDATE和DELETE。
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("event_id=?", "vid_g3_callback_late_success").Update("event_type", "tampered").Error; err == nil {
		t.Fatal("TaskEvent UPDATE必须被拒绝")
	}
	if err := db.Where("event_id=?", "vid_g3_callback_late_success").Delete(&model.AIGatewayTaskEvent{}).Error; err == nil {
		t.Fatal("TaskEvent DELETE必须被拒绝")
	}
	unsafeEvent := model.AIGatewayTaskEvent{
		EventID: "vid_g3_unsafe_event", TaskID: t2v.ID, UserID: owner.UserID, ProjectID: owner.ProjectID,
		Source: "system", EventType: "execution_status_changed", SafeDetailJSON: json.RawMessage(`{"message":"可换名Prompt正文"}`), CreatedAt: now,
	}
	if err := db.Create(&unsafeEvent).Error; err == nil {
		t.Fatal("直接数据库写入的TaskEvent自由文本必须被结构白名单拒绝")
	}

	// TaskPayload Repository只保存密文信封，并由触发器阻止UPDATE与DELETE。
	payload := &model.AIGatewayTaskPayload{
		TaskID: i2v.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, PayloadKind: "prompt",
		Ciphertext: []byte{1, 2, 3, 4, 5}, Nonce: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		KeyVersion: "vid-g3-key-v1", AADSHA256: fmt.Sprintf("%064x", 77), CiphertextSHA256: fmt.Sprintf("%064x", 78),
	}
	payloadRepo := NewVideoTaskPayloadRepository(db, nil)
	if err := payloadRepo.Create(context.Background(), i2v.PublicID, owner, payload); !errors.Is(err, ErrVideoPayloadNotFound) {
		t.Fatalf("无认证验证器或伪造摘要的任务载荷必须失败关闭: %v", err)
	}

	// 计费轴独立CAS，完整走完unquoted→quoted→held→settlement_pending→settled→adjusted。
	billingTask := seedVideoG3Task(t, db, 98105, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskCreated, model.AIBillingUnquoted, model.AIDeliveryPending, "", "")
	for index, status := range []string{model.AIBillingQuoted, model.AIBillingHeld, model.AIBillingSettlementPending, model.AIBillingSettled, model.AIBillingAdjusted} {
		record, transitionErr := taskRepo.FindForOwner(context.Background(), billingTask.PublicID, owner)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		updated, transitionErr := taskRepo.TransitionBilling(context.Background(), VideoStateTransition{
			TaskPublicID: billingTask.PublicID, Owner: owner, ExpectedVersion: record.RequestVersionNo,
			ToStatus: status, EventID: fmt.Sprintf("vid_g3_billing_%d", index), Source: "system", Now: now.Add(time.Duration(index) * time.Second),
		})
		if transitionErr != nil || updated.BillingStatus != status || updated.Status != model.AIImageTaskCreated || updated.DeliveryStatus != model.AIDeliveryPending {
			t.Fatalf("计费轴独立迁移失败: status=%s updated=%+v err=%v", status, updated, transitionErr)
		}
	}

	// 输出资产由Fake定位器生成对象位置，父子关系与交付/删除事实均可追溯。
	assetTask := seedVideoG3Task(t, db, 98106, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskSucceeded, model.AIBillingSettled, model.AIDeliveryPending, "", "")
	assetRecord, err := taskRepo.FindForOwner(context.Background(), assetTask.PublicID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskRepo.TransitionDelivery(context.Background(), VideoStateTransition{TaskPublicID: assetTask.PublicID, Owner: owner, ExpectedVersion: assetRecord.RequestVersionNo, ToStatus: model.AIDeliveryAvailable, EventID: "vid_g3_delivery_available", Source: "system", Now: now}); err != nil {
		t.Fatalf("安全终态交付失败: %v", err)
	}
	assetRepo := NewVideoOutputAssetRepository(db, fakeVideoG3LocationFactory{})
	hasAudio := false
	content, err := assetRepo.Create(context.Background(), VideoOutputAssetDraft{
		PublicID: "vid_asset_g3_content", TaskPublicID: assetTask.PublicID, Owner: owner,
		AssetRole: model.AIImageAssetContent, IsBillableOutput: true, MIMEType: "video/mp4", SizeBytes: 4096,
		SHA256: fmt.Sprintf("%064x", 98106), Width: 1280, Height: 720, DurationSeconds: decimal.NewFromInt(5),
		FrameRate: decimal.NewFromInt(24), Container: "mp4", VideoCodec: "h264", HasAudio: &hasAudio,
		Source: "fake_object_store", RetentionPolicyID: "video-30d", ExpiresAt: now.Add(30 * 24 * time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建视频主产物失败: %v", err)
	}
	if _, err := assetRepo.TransitionLifecycle(context.Background(), content.PublicID, owner, 1, model.AIImageAssetAvailable, now); !errors.Is(err, ErrVideoAssetTransition) {
		t.Fatalf("VID-G3不得伪造审核与标识后直接available: %v", err)
	}
	safetyUpdates := map[string]interface{}{
		"moderation_status": model.AIModerationPassed, "explicit_label_status": model.AIImageLabelApplied,
		"implicit_label_status": model.AIImageLabelApplied,
	}
	var safetyVersionColumnCount int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_assets' AND column_name IN ('moderation_policy_version','explicit_label_version','implicit_label_version')").Scan(&safetyVersionColumnCount).Error; err != nil {
		t.Fatal(err)
	}
	if safetyVersionColumnCount == 3 {
		safetyUpdates["moderation_policy_version"] = "vid-g3-imported-v1"
		safetyUpdates["explicit_label_version"] = "vid-g3-imported-v1"
		safetyUpdates["implicit_label_version"] = "vid-g3-imported-v1"
	}
	if err := db.Model(&model.AIImageAsset{}).Where("id=?", content.ID).Updates(safetyUpdates).Error; err != nil {
		t.Fatal(err)
	}
	content, err = assetRepo.FindOwnedForInternal(context.Background(), content.PublicID, owner)
	if err != nil {
		t.Fatal(err)
	}
	content, err = assetRepo.TransitionLifecycle(context.Background(), content.PublicID, owner, 1, model.AIImageAssetAvailable, now)
	if err != nil {
		t.Fatalf("视频主产物进入available失败: %v", err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), content.PublicID, owner); err != nil {
		t.Fatalf("已结算且可交付的视频主产物应可访问: %v", err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), content.PublicID, wrongUser); !errors.Is(err, ErrVideoAssetAccess) {
		t.Fatalf("跨用户产物查询必须统一不可访问: %v", err)
	}
	if _, err := assetRepo.FindOwnedForInternal(context.Background(), content.PublicID, wrongProject); !errors.Is(err, ErrVideoAssetNotFound) {
		t.Fatalf("同用户跨Project产物查询必须统一不存在: %v", err)
	}
	cover, err := assetRepo.Create(context.Background(), VideoOutputAssetDraft{
		PublicID: "vid_asset_g3_cover", TaskPublicID: assetTask.PublicID, ParentPublicID: content.PublicID, Owner: owner,
		AssetRole: model.AIImageAssetCover, MIMEType: "image/jpeg", SizeBytes: 1024,
		SHA256: fmt.Sprintf("%064x", 98107), Width: 640, Height: 360, Source: "derived",
		RetentionPolicyID: "video-cover-30d", ExpiresAt: now.Add(30 * 24 * time.Hour), Now: now,
	})
	if err != nil || cover.ParentAssetID == nil || *cover.ParentAssetID != content.ID {
		t.Fatalf("封面父子关系错误: cover=%+v err=%v", cover, err)
	}

	var cleanupAsset *model.AIImageAsset
	for index, role := range []string{model.AIImageAssetPreview, model.AIImageAssetThumbnail, model.AIImageAssetModerationCopy, model.AIImageAssetDerived} {
		draft := VideoOutputAssetDraft{
			PublicID: fmt.Sprintf("vid_asset_g3_%s", role), TaskPublicID: assetTask.PublicID, ParentPublicID: content.PublicID, Owner: owner,
			ResultIndex: uint32(index + 1), AssetRole: role, MIMEType: "image/jpeg", SizeBytes: uint64(1200 + index),
			SHA256: fmt.Sprintf("%064x", 98200+index), Width: 640, Height: 360, Source: "derived",
			RetentionPolicyID: "video-derived-30d", ExpiresAt: now.Add(30 * 24 * time.Hour), Now: now,
		}
		if role == model.AIImageAssetPreview {
			draft.MIMEType, draft.DurationSeconds, draft.FrameRate = "video/mp4", decimal.NewFromInt(5), decimal.NewFromInt(24)
			draft.Container, draft.VideoCodec, draft.HasAudio = "mp4", "h264", &hasAudio
		}
		child, createErr := assetRepo.Create(context.Background(), draft)
		if createErr != nil || child.ParentAssetID == nil || *child.ParentAssetID != content.ID {
			t.Fatalf("派生角色父子关系错误: role=%s child=%+v err=%v", role, child, createErr)
		}
		if role == model.AIImageAssetDerived {
			cleanupAsset = child
		}
	}

	content, err = assetRepo.OpenDispute(context.Background(), content.PublicID, owner, content.VersionNo, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("开启视频资产争议失败: %v", err)
	}
	if _, err := assetRepo.FindDeliverable(context.Background(), content.PublicID, owner); !errors.Is(err, ErrVideoAssetAccess) {
		t.Fatalf("争议中视频资产必须禁止交付: %v", err)
	}
	content, err = assetRepo.ResolveDispute(context.Background(), content.PublicID, owner, content.VersionNo, now.Add(2*time.Minute))
	if err != nil || !content.LegalHold {
		t.Fatalf("争议关闭后必须保留legal hold: asset=%+v err=%v", content, err)
	}

	for _, next := range []string{model.AIImageAssetQuarantined, model.AIImageAssetDeleting, model.AIImageAssetDeleteFailed, model.AIImageAssetDeleting, model.AIImageAssetDeleted} {
		if next == model.AIImageAssetQuarantined {
			rejectionUpdates := map[string]interface{}{"moderation_status": model.AIModerationRejected}
			if safetyVersionColumnCount == 3 {
				rejectionUpdates["moderation_policy_version"] = "vid-g3-imported-v1"
			}
			if err := db.Model(&model.AIImageAsset{}).Where("id=?", cleanupAsset.ID).Updates(rejectionUpdates).Error; err != nil {
				t.Fatal(err)
			}
			cleanupAsset, err = assetRepo.FindOwnedForInternal(context.Background(), cleanupAsset.PublicID, owner)
			if err != nil {
				t.Fatal(err)
			}
		}
		cleanupAsset, err = assetRepo.TransitionLifecycle(context.Background(), cleanupAsset.PublicID, owner, cleanupAsset.VersionNo, next, now.Add(3*time.Minute))
		if err != nil {
			t.Fatalf("视频资产删除生命周期失败: state=%s err=%v", next, err)
		}
	}
	if cleanupAsset.MediaDeletedAt == nil || cleanupAsset.SHA256 == nil || cleanupAsset.Width == nil || cleanupAsset.RequestID == "" || cleanupAsset.TaskID == 0 {
		t.Fatalf("删除正文后必须保留hash、规格和追溯事实: %+v", cleanupAsset)
	}
}

type fakeVideoG3LocationFactory struct{}

func (fakeVideoG3LocationFactory) NewVideoObjectLocation(_ context.Context, owner VideoOwner, taskPublicID, assetPublicID, role string, resultIndex uint32) (VideoObjectLocation, error) {
	return VideoObjectLocation{Bucket: "vid-g3-fake", ObjectKey: fmt.Sprintf("%d/%d/%s/%s/%d", owner.UserID, owner.ProjectID, taskPublicID, assetPublicID, resultIndex)}, nil
}

func seedVideoG3Principals(t *testing.T, db *gorm.DB, userID, otherUserID, projectID, otherProject, secondProject, apiKeyID, otherKeyID, secondKeyID, priceID uint64, modelCode string) {
	t.Helper()
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active'),(?,'fixture','verified','active')", userID, otherUserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,?,'active','disabled','Asia/Shanghai'),(?,?,?,'active','disabled','Asia/Shanghai'),(?,?,?,'active','disabled','Asia/Shanghai')", projectID, userID, "视频G3项目", otherProject, otherUserID, "其他视频项目", secondProject, userID, "同用户第二视频项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,?,?,'视频G3密钥','postpaid','','allowlist','active'),(?,?,?,?,?,'其他视频密钥','postpaid','','allowlist','active'),(?,?,?,?,?,'同用户第二Project密钥','postpaid','','allowlist','active')",
		apiKeyID, userID, projectID, fmt.Sprintf("g3-%d", apiKeyID), fmt.Sprintf("g3-hash-%d", apiKeyID),
		otherKeyID, otherUserID, otherProject, fmt.Sprintf("g3-other-%d", otherKeyID), fmt.Sprintf("g3-other-hash-%d", otherKeyID),
		secondKeyID, userID, secondProject, fmt.Sprintf("g3-second-%d", secondKeyID), fmt.Sprintf("g3-second-hash-%d", secondKeyID)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", priceID, modelCode, "视频G3模型", "video", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	limits := json.RawMessage(`{"meter_type":"video_seconds","variants":[{"operation":"text_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false}]}`)
	if err := db.Exec(`INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.1,'non_commercial_test_fixture','vid-g3-mysql','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,?)`, priceID, modelCode, limits, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Hour), userID).Error; err != nil {
		t.Fatal(err)
	}
}

func seedVideoG3Task(t *testing.T, db *gorm.DB, id, priceID uint64, owner VideoOwner, modelCode, operation, status, billing, delivery, providerCode, providerTaskID string) *model.AIImageTask {
	t.Helper()
	requestID := fmt.Sprintf("vid_g3_req_%d", id)
	request := model.AIRequest{
		RequestID: requestID, UserID: owner.UserID, ProjectID: &owner.ProjectID, APIKeyID: owner.APIKeyID,
		LogicalModelCode: modelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: &operation,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: VideoRequestExecutionStatus(status),
		BillingStatus: billing, DeliveryStatus: delivery, VersionNo: 1,
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	quote := model.AIGatewayQuote{
		ID: id, PublicID: fmt.Sprintf("vid_g3_quote_%d", id), UserID: owner.UserID, ProjectID: owner.ProjectID,
		APIKeyID: owner.APIKeyID, LogicalModelCode: modelCode, Capability: model.AIVideoCapability, Operation: &operation,
		RequestFingerprint: fmt.Sprintf("%064x", id), RequestVariantHash: fmt.Sprintf("%064x", id+1),
		PriceVersionID: priceID, PriceSnapshotJSON: json.RawMessage(`{"schema_version":3}`),
		QuotedAmount: decimal.RequireFromString("0.50000000"), Currency: "CNY", ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	inputJSON, err := json.Marshal(map[string]interface{}{
		"operation": operation, "resolution": "1280x720", "duration_seconds": 5,
		"aspect_ratio": "16:9", "frame_rate": 24, "audio": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.AIImageTask{
		ID: id, PublicID: fmt.Sprintf("vid_g3_task_%d", id), RequestID: requestID, QuoteID: id,
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: modelCode,
		Capability: model.AIVideoCapability, Operation: &operation, Status: status, InputJSON: inputJSON, VersionNo: 1,
	}
	if providerCode != "" {
		task.ProviderCode, task.ProviderTaskID = &providerCode, &providerTaskID
	}
	if videoExecutionTerminal(status) {
		completedAt := time.Now().UTC().Truncate(time.Second)
		task.CompletedAt = &completedAt
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func seedVideoG3ReadyInput(t *testing.T, db *gorm.DB, id uint64, owner VideoOwner, now time.Time, suffix string) *model.AIGatewayInputAsset {
	t.Helper()
	session := model.AIUploadSession{
		ID: id, PublicID: "vid_g3_upload_" + suffix, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID,
		Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: model.AIUploadSourcePlatformPresigned,
		Status: model.AIUploadSessionVerifying, MIMEType: model.AIInputMIMEJPEG, SizeBytes: 2048,
		Bucket: "vid-g3-input", ObjectKey: "input/" + suffix + ".jpg", ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%064x", id)
	mime, size, width, height, policy := model.AIInputMIMEJPEG, uint64(2048), uint32(640), uint32(360), "vid-g3-policy-v1"
	asset := &model.AIGatewayInputAsset{
		ID: id, PublicID: "vid_g3_input_" + suffix, UserID: owner.UserID, ProjectID: owner.ProjectID,
		SourceType: model.AIUploadSourcePlatformPresigned, UploadSessionID: &session.ID,
		Bucket: &session.Bucket, ObjectKey: &session.ObjectKey, OriginalSHA256: hash, NormalizedSHA256: &hash,
		MIMEType: &mime, SizeBytes: &size, Width: &width, Height: &height, ModerationPolicyVersion: &policy,
		ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIUploadSession{}).Where("id=?", session.ID).Updates(map[string]interface{}{
		"status": model.AIUploadSessionCompleted, "final_input_asset_id": asset.ID,
		"source_etag": "fixture-etag-" + suffix, "completed_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return asset
}

func seedVideoG3GeneratedImageInput(t *testing.T, db *gorm.DB, id, priceID uint64, owner VideoOwner, modelCode string, now time.Time) *model.AIGatewayInputAsset {
	t.Helper()
	requestID := fmt.Sprintf("img_g3_source_req_%d", id)
	request := model.AIRequest{
		RequestID: requestID, UserID: owner.UserID, ProjectID: &owner.ProjectID, APIKeyID: owner.APIKeyID,
		LogicalModelCode: modelCode, Modality: "image", Capability: model.AIImageCapability,
		ModerationStatus: model.AIModerationPassed, ExecutionStatus: model.AIExecutionSucceeded,
		BillingStatus: model.AIBillingSettled, DeliveryStatus: model.AIDeliveryAvailable, VersionNo: 1,
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	quote := model.AIGatewayQuote{
		ID: id, PublicID: fmt.Sprintf("img_g3_source_quote_%d", id), UserID: owner.UserID, ProjectID: owner.ProjectID,
		APIKeyID: owner.APIKeyID, LogicalModelCode: modelCode, Capability: model.AIImageCapability,
		RequestFingerprint: fmt.Sprintf("%064x", id), RequestVariantHash: fmt.Sprintf("%064x", id+1),
		PriceVersionID: priceID, PriceSnapshotJSON: json.RawMessage(`{"schema_version":2}`),
		QuotedAmount: decimal.RequireFromString("0.10000000"), Currency: "CNY", ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	completedAt := now
	task := model.AIImageTask{
		ID: id, PublicID: fmt.Sprintf("img_g3_source_task_%d", id), RequestID: requestID, QuoteID: id,
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: modelCode,
		Capability: model.AIImageCapability, Status: model.AIImageTaskSucceeded, Progress: 100,
		InputJSON: json.RawMessage(`{"resolution":"2K"}`), VersionNo: 1, CompletedAt: &completedAt,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	bucket, objectKey, mime := "img-g3-source", fmt.Sprintf("generated/%d.jpg", id), "image/jpeg"
	size, width, height, sha := uint64(2048), uint32(1024), uint32(1024), fmt.Sprintf("%064x", id+2)
	asset := model.AIImageAsset{
		ID: id, PublicID: fmt.Sprintf("img_g3_source_asset_%d", id), UserID: owner.UserID, ProjectID: owner.ProjectID,
		RequestID: requestID, TaskID: id, AssetRole: model.AIImageAssetPrimaryOutput, IsBillableOutput: true,
		Bucket: &bucket, ObjectKey: &objectKey, MIMEType: &mime, SizeBytes: &size, SHA256: &sha, Width: &width, Height: &height,
		Modality: "image", Source: "provider_url", ModerationStatus: model.AIModerationPassed,
		ExplicitLabelStatus: model.AIImageLabelApplied, ImplicitLabelStatus: model.AIImageLabelApplied,
		LifecycleState: model.AIImageAssetAvailable, RetentionPolicyID: "image-30d", ExpiresAt: now.Add(30 * 24 * time.Hour), VersionNo: 1,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	policy := "vid-g3-generated-image-policy-v1"
	input := &model.AIGatewayInputAsset{
		ID: id, PublicID: fmt.Sprintf("vid_g3_generated_input_%d", id), UserID: owner.UserID, ProjectID: owner.ProjectID,
		SourceType: model.AIInputSourceGatewayAssetSnapshot, SourceGatewayAssetID: &asset.ID,
		Bucket: &bucket, ObjectKey: &objectKey, OriginalSHA256: sha, NormalizedSHA256: &sha,
		MIMEType: &mime, SizeBytes: &size, Width: &width, Height: &height, ModerationPolicyVersion: &policy,
		ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady, ExpiresAt: now.Add(time.Hour),
	}
	if err := db.Create(input).Error; err != nil {
		t.Fatal(err)
	}
	return input
}

func videoG3Uint64Ptr(value uint64) *uint64 { return &value }

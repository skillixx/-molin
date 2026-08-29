package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestVideoG4RepositoryMySQLProviderSafetyAndMediaFacts(t *testing.T) {
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
	sqlDB.SetMaxOpenConns(120)
	sqlDB.SetMaxIdleConns(120)
	t.Cleanup(func() { _ = sqlDB.Close() })

	const (
		userID        = uint64(98601)
		otherUserID   = uint64(98602)
		projectID     = uint64(98601)
		otherProject  = uint64(98602)
		secondProject = uint64(98603)
		apiKeyID      = uint64(98601)
		otherKeyID    = uint64(98602)
		secondKeyID   = uint64(98603)
		priceID       = uint64(98601)
		modelCode     = "molin/video-g4-mysql"
	)
	seedVideoG3Principals(t, db, userID, otherUserID, projectID, otherProject, secondProject, apiKeyID, otherKeyID, secondKeyID, priceID, modelCode)
	now := time.Now().UTC().Truncate(time.Second)
	owner := VideoOwner{UserID: userID, ProjectID: projectID, APIKeyID: videoG3Uint64Ptr(apiKeyID)}
	task := seedVideoG3Task(t, db, 98611, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskSubmitting, model.AIBillingHeld, model.AIDeliveryPending, "", "")
	taskRepo := NewVideoTaskRepository(db)

	var winners, conflicts atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, bindErr := taskRepo.BindProviderTask(context.Background(), VideoProviderBinding{
				TaskPublicID: task.PublicID, Owner: owner, ExpectedVersion: 1,
				ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-g4-provider",
				EventID: fmt.Sprintf("vid_g4_bind_%03d", index), Now: now,
			})
			switch {
			case bindErr == nil:
				winners.Add(1)
			case errors.Is(bindErr, ErrVideoTaskConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Provider绑定返回异常: %v", bindErr)
			}
		}(index)
	}
	wait.Wait()
	if winners.Load() != 1 || conflicts.Load() != 99 {
		t.Fatalf("100并发Provider绑定必须一胜九十九冲突: winners=%d conflicts=%d", winners.Load(), conflicts.Load())
	}
	bound, err := taskRepo.FindForOwner(context.Background(), task.PublicID, owner)
	if err != nil || bound.Status != model.AIImageTaskSubmitted || bound.ProviderTaskID == nil || *bound.ProviderTaskID != "taskUUID-g4-provider" {
		t.Fatalf("Provider任务绑定未原子完成: task=%+v err=%v", bound, err)
	}
	otherTask := seedVideoG3Task(t, db, 98612, priceID, owner, modelCode, model.AIVideoOperationTextToVideo, model.AIImageTaskSubmitted, model.AIBillingHeld, model.AIDeliveryPending, "fake-native-async", "taskUUID-g4-other")
	mismatchBody := sha256.Sum256([]byte("misbound-callback-body"))
	mismatchOutcome, err := NewVideoProviderCallbackEventRepository(db).RecordAndApply(context.Background(), VideoProviderCallbackCommand{
		ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-g4-provider", ExternalEventID: "evt-g4-misbound",
		BodySHA256: hex.EncodeToString(mismatchBody[:]), ExpectedTaskPublicID: otherTask.PublicID, ExpectedOwner: owner,
		SignatureStatus: model.AIProviderCallbackSignatureValid, ToStatus: model.AIImageTaskProcessing,
		EventID: "vid_g4_callback_misbound", SafeResultJSON: json.RawMessage("{\"result\":\"applied\"}"), ReceivedAt: now,
	})
	if err != nil || mismatchOutcome.Applied {
		t.Fatalf("错绑回调只能记录ignored事实: outcome=%+v err=%v", mismatchOutcome, err)
	}
	bound, err = taskRepo.FindForOwner(context.Background(), task.PublicID, owner)
	if err != nil || bound.Status != model.AIImageTaskSubmitted {
		t.Fatalf("错绑回调不得先推进真实Provider任务: task=%+v err=%v", bound, err)
	}
	callbackBody := sha256.Sum256([]byte("verified-callback-body"))
	callbackCommand := VideoProviderCallbackCommand{
		ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-g4-provider", ExternalEventID: "evt-g4-hash-only",
		ExpectedTaskPublicID: task.PublicID, ExpectedOwner: owner,
		BodySHA256: hex.EncodeToString(callbackBody[:]), SignatureStatus: model.AIProviderCallbackSignatureValid,
		ToStatus: model.AIImageTaskProcessing, EventID: "vid_g4_callback_hash_only",
		SafeResultJSON: json.RawMessage("{\"result\":\"applied\"}"), ReceivedAt: now,
	}
	callbackRepo := NewVideoProviderCallbackEventRepository(db)
	firstCallback, err := callbackRepo.RecordAndApply(context.Background(), callbackCommand)
	if err != nil || !firstCallback.Applied {
		t.Fatalf("已验签body哈希回调应安全应用: outcome=%+v err=%v", firstCallback, err)
	}
	replayCallback, err := callbackRepo.RecordAndApply(context.Background(), callbackCommand)
	if err != nil || !replayCallback.Replayed {
		t.Fatalf("已验签body哈希回调必须幂等: outcome=%+v err=%v", replayCallback, err)
	}
	callbackCommand.BodySHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := callbackRepo.RecordAndApply(context.Background(), callbackCommand); !errors.Is(err, ErrVideoCallbackBodyConflict) {
		t.Fatalf("同event_id不同body哈希必须失败关闭: %v", err)
	}

	assetRepo := NewVideoOutputAssetRepository(db, fakeVideoG3LocationFactory{})
	hasAudio := true
	asset, err := assetRepo.Create(context.Background(), VideoOutputAssetDraft{
		PublicID: "vasset_g4_content", TaskPublicID: task.PublicID, Owner: owner,
		AssetRole: model.AIImageAssetContent, IsBillableOutput: true, MIMEType: "video/mp4",
		SizeBytes: 1024, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Width: 1280, Height: 720, DurationSeconds: decimal.NewFromInt(5), FrameRate: decimal.NewFromInt(24),
		Container: "mp4", VideoCodec: "avc1", AudioCodec: "mp4a", HasAudio: &hasAudio,
		Source: "fake_object_store", RetentionPolicyID: "video-g4-test", ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	moderated, err := assetRepo.ApplyModerationResult(context.Background(), asset.PublicID, owner, asset.VersionNo, model.AIModerationPassed, "fake-moderation-v1", now)
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := assetRepo.ApplyLabelResult(context.Background(), asset.PublicID, owner, moderated.VersionNo, model.AIImageLabelApplied, model.AIImageLabelApplied, "fake-label-v1", now)
	if err != nil {
		t.Fatal(err)
	}
	if labelled.ExplicitLabelVersion == nil || labelled.ImplicitLabelVersion == nil || *labelled.ExplicitLabelVersion != "fake-label-v1" {
		t.Fatalf("标识版本事实缺失: %+v", labelled)
	}
	available, err := assetRepo.TransitionLifecycle(context.Background(), asset.PublicID, owner, labelled.VersionNo, model.AIImageAssetAvailable, now)
	if err != nil || available.LifecycleState != model.AIImageAssetAvailable {
		t.Fatalf("审核与双标识完成后应可用: asset=%+v err=%v", available, err)
	}
	unsafeUpdates := []string{
		"UPDATE ai_gateway_assets SET moderation_status='pending' WHERE id=?",
		"UPDATE ai_gateway_assets SET moderation_policy_version=NULL WHERE id=?",
		"UPDATE ai_gateway_assets SET explicit_label_status='pending' WHERE id=?",
		"UPDATE ai_gateway_assets SET explicit_label_version='tampered' WHERE id=?",
		"UPDATE ai_gateway_assets SET implicit_label_status='pending' WHERE id=?",
		"UPDATE ai_gateway_assets SET implicit_label_version=NULL WHERE id=?",
	}
	for _, statement := range unsafeUpdates {
		if err := db.Exec(statement, available.ID).Error; err == nil {
			t.Fatalf("已形成的视频审核/标识事实和版本必须不可回退或篡改: %s", statement)
		}
	}
	var immutable model.AIImageAsset
	if err := db.Where("id=?", available.ID).First(&immutable).Error; err != nil || immutable.ModerationStatus != model.AIModerationPassed ||
		immutable.ExplicitLabelStatus != model.AIImageLabelApplied || immutable.ImplicitLabelStatus != model.AIImageLabelApplied ||
		immutable.ModerationPolicyVersion == nil || *immutable.ModerationPolicyVersion != "fake-moderation-v1" ||
		immutable.ExplicitLabelVersion == nil || *immutable.ExplicitLabelVersion != "fake-label-v1" ||
		immutable.ImplicitLabelVersion == nil || *immutable.ImplicitLabelVersion != "fake-label-v1" {
		t.Fatalf("非法更新后安全事实必须保持原值: asset=%+v err=%v", immutable, err)
	}
	deleted, err := assetRepo.MarkMediaDeleted(context.Background(), asset.PublicID, owner, available.VersionNo, now)
	if err != nil || deleted.MediaDeletedAt == nil || deleted.SHA256 == nil || deleted.SizeBytes == nil {
		t.Fatalf("删除正文后必须保留媒体事实: asset=%+v err=%v", deleted, err)
	}
}

func TestVideoG4RepositoryRejectsUnsafeProviderBinding(t *testing.T) {
	if err := validateVideoProviderBinding(VideoProviderBinding{
		TaskPublicID: "vid_task", Owner: VideoOwner{UserID: 1, ProjectID: 1}, ExpectedVersion: 1,
		ProviderCode: "fake-native-async", ProviderTaskID: "https://internal/task",
		EventID: "evt", Now: time.Now(),
	}); !errors.Is(err, ErrVideoTaskTransition) {
		t.Fatalf("Provider任务ID不得是URL: %v", err)
	}
	if err := validateVideoSafeJSON(json.RawMessage("{\"reason\":\"provider_bound\"}")); err != nil {
		t.Fatalf("G4低敏事件原因应进入白名单: %v", err)
	}
}

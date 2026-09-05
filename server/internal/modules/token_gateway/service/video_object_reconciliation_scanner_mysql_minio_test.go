package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7ObjectScannerMySQLMinIO(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_RUNTIME_ISOLATED") != "YES" {
		t.Skip("VID-G7只允许隔离全运行时门禁执行")
	}
	db, err := gorm.Open(mysql.Open(os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MYSQL_DSN")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_ENDPOINT")
	internalURL, _ := url.Parse("http://" + endpoint)
	publicProxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(internalURL))
	defer publicProxy.Close()
	access, secret := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_ACCESS"), os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_SECRET")
	outputStore, err := video.NewMinIOVideoObjectStore(video.MinIOVideoObjectStoreConfig{Endpoint: endpoint, AccessKey: access, SecretKey: secret, TempDirectory: t.TempDir(), Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine", "ai-user-assets"}, VerifyArchiveFence: func(context.Context, string, uint64) error { return nil }})
	if err != nil || outputStore.EnsureBuckets(context.Background()) != nil {
		t.Fatal("MinIO清单Store装配失败")
	}
	uploadStore, err := NewMinIOVideoUploadStore(MinIOVideoUploadStoreConfig{Endpoint: endpoint, PublicUploadEndpoint: publicProxy.URL, AccessKey: access, SecretKey: secret, SourceBucket: "ai-upload-temp", NormalizedBucket: "ai-result"})
	if err != nil {
		t.Fatal(err)
	}
	id := uint64(998117)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(?,'fixture','active','verified')", []any{id}},
		{"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'对象扫描测试','active','disabled','UTC')", []any{id, id}},
		{"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(?,?,?,'g7',?,'对象扫描Key','postpaid','allowlist','active',1)", []any{id, id, id, fmt.Sprintf("fixture-object-scan-%d", id)}},
		{"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='video:generate'", []any{id}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	var imageBody bytes.Buffer
	if err := png.Encode(&imageBody, image.NewRGBA(image.Rect(0, 0, 640, 640))); err != nil {
		t.Fatal(err)
	}
	raw := imageBody.Bytes()
	sharedImageStore, err := imagegateway.NewMinIOObjectStore(imagegateway.MinIOObjectStoreConfig{Endpoint: endpoint, PublicDownloadEndpoint: publicProxy.URL, AccessKey: access, SecretKey: secret, Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine"}})
	if err != nil {
		t.Fatal(err)
	}
	importAdapter, err := NewVideoMinIOImportStore(sharedImageStore, "ai-result")
	if err != nil {
		t.Fatal(err)
	}
	sourceRef := imagegateway.ObjectRef{Bucket: "ai-result", Key: "0123456789abcdef0123456789abcdef/0/primary.png"}
	if _, err := sharedImageStore.Put(context.Background(), sourceRef, bytes.NewReader(raw), videoUploadMaxBytes); err != nil {
		t.Fatal(err)
	}
	if read, err := importAdapter.Read(context.Background(), VideoImportObject{Bucket: sourceRef.Bucket, Key: sourceRef.Key}, videoUploadMaxBytes); err != nil || !bytes.Equal(read, raw) {
		t.Fatalf("真实MinIO图片来源读取失败: bytes=%d err=%v", len(read), err)
	}
	importTarget := VideoImportObject{Bucket: "ai-result", Key: fmt.Sprintf("import/%d/%d/vin_minio_import.png", id, id)}
	if err := importAdapter.Put(context.Background(), importTarget, raw, videoPayloadSHA256(raw)); err != nil {
		t.Fatal(err)
	}
	if err := importAdapter.Discard(context.Background(), importTarget); err != nil {
		t.Fatal(err)
	}
	if discarded, err := importAdapter.VerifyDiscarded(context.Background(), importTarget); err != nil || !discarded {
		t.Fatalf("真实MinIO导入副本清理失败: discarded=%t err=%v", discarded, err)
	}
	service, err := NewVideoUploadService(db, VideoUploadOptions{Store: uploadStore, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil), SourceBucket: "ai-upload-temp", NormalizedBucket: "ai-result", ModerationPolicyVersion: "vid-g7-object-scan-v1", MaxUserReservedBytes: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: id, ProjectID: id, APIKeyID: id}
	created, err := service.Create(context.Background(), VideoUploadCreateCommand{Caller: caller, IdempotencyKey: "g7-object-scan-create", Filename: "reference.png", MIMEType: "image/png", SizeBytes: uint64(len(raw)), SHA256: videoPayloadSHA256(raw)})
	if err != nil || created.Upload == nil {
		t.Fatalf("创建真实MinIO上传失败: reply=%+v err=%v", created, err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, created.Upload.URL, bytes.NewReader(raw))
	for name, value := range created.Upload.Headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("写入参考图失败: status=%v err=%v", videoScannerResponseStatus(response), err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	completed, err := service.Complete(context.Background(), caller, created.SessionID, "g7-object-scan-complete")
	if err != nil || completed.InputAssetID == nil {
		t.Fatalf("完成参考图封存失败: reply=%+v err=%v", completed, err)
	}
	owner := repository.VideoOwner{UserID: id, ProjectID: id, APIKeyID: &id}
	record, err := service.load(db, owner, created.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟对象删除已成功、数据库回写尚未发生；扫描器只能观察并生成补偿，不能删除业务事实。
	if err := uploadStore.Discard(context.Background(), record.target()); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewVideoObjectReconciliationScanner(db, outputStore, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	scanner.now = func() time.Time { return base }
	first, err := scanner.ScanExpected(context.Background(), 100)
	if err != nil || first.Observed != 2 {
		t.Fatalf("原图墓碑和规范化缺失必须先观察: summary=%+v err=%v", first, err)
	}
	scanner.now = func() time.Time { return base.Add(6 * time.Minute) }
	second, err := scanner.ScanExpected(context.Background(), 100)
	if err != nil || second.Observed != 2 {
		t.Fatalf("第二次缺失必须确认: summary=%+v err=%v", second, err)
	}
	var missingTasks int64
	if err := db.Table("ai_compensation_tasks").Where("task_type='video_object_missing_reconcile'").Count(&missingTasks).Error; err != nil || missingTasks != 2 {
		t.Fatalf("DB到MinIO必须形成两条可追溯补偿: count=%d err=%v", missingTasks, err)
	}
	// 使用每页1条强制跨页，并以新Scanner实例证明数据库权威清单从持久游标续扫而不是回到首页。
	pageOne, err := scanner.ScanExpected(context.Background(), 1)
	if err != nil || pageOne.ExpectedChecked != 1 {
		t.Fatalf("数据库权威对象第一页必须精确处理1条: summary=%+v err=%v", pageOne, err)
	}
	restartedScanner, err := NewVideoObjectReconciliationScanner(db, outputStore, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	restartedScanner.now = scanner.now
	pageTwo, err := restartedScanner.ScanExpected(context.Background(), 1)
	if err != nil || pageTwo.ExpectedChecked != 1 {
		t.Fatalf("重启后必须从数据库游标处理尾页: summary=%+v err=%v", pageTwo, err)
	}
	var expectedCursor videoObjectScanCursor
	if err := db.Table(expectedCursor.TableName()).Where("scope_key='db-expected'").Take(&expectedCursor).Error; err != nil || expectedCursor.LastBucket != "" || expectedCursor.LastObjectKey != "" || expectedCursor.CompletedCycles < 3 {
		t.Fatalf("尾页成功后必须回卷并记录完整轮次: cursor=%+v err=%v", expectedCursor, err)
	}

	orphanRaw := []byte("orphan-inline-fixture")
	orphan := VideoUploadTarget{SessionID: "vup_orphan_scan", InputAssetID: "vin_orphan_scan", UserID: id, ProjectID: id, SourceType: "openai_inline_multipart", SourceBucket: "ai-upload-temp", SourceKey: fmt.Sprintf("inline/%d/%d/vup_orphan_scan", id, id), NormalizedBucket: "ai-result", NormalizedKey: fmt.Sprintf("normalized/%d/%d/vin_orphan_scan.png", id, id), MIMEType: "image/png", ExpectedSHA256: videoPayloadSHA256(orphanRaw), SizeBytes: uint64(len(orphanRaw)), UploadExpiresAt: base.Add(time.Hour)}
	if err := uploadStore.PutOriginal(context.Background(), orphan, bytes.NewReader(orphanRaw), uint64(len(orphanRaw)), orphan.ExpectedSHA256); err != nil {
		t.Fatal(err)
	}
	scanner.now = func() time.Time { return base }
	storageFirst, err := scanner.ScanStorage(context.Background(), 100)
	if err != nil || storageFirst.Observed != 1 {
		t.Fatalf("MinIO无引用对象必须先观察: summary=%+v err=%v", storageFirst, err)
	}
	scanner.now = func() time.Time { return base.Add(6 * time.Minute) }
	storageSecond, err := scanner.ScanStorage(context.Background(), 100)
	if err != nil || storageSecond.Observed != 1 {
		t.Fatalf("跨静默窗的无引用对象必须确认: summary=%+v err=%v", storageSecond, err)
	}
	var orphanTasks int64
	if err := db.Table("ai_compensation_tasks").Where("task_type='video_orphan_cleanup'").Count(&orphanTasks).Error; err != nil || orphanTasks != 1 {
		t.Fatalf("MinIO到DB必须形成唯一补偿: count=%d err=%v", orphanTasks, err)
	}
	if err := db.Exec(`INSERT INTO ai_upload_sessions(public_id,user_id,project_id,api_key_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at)
VALUES('vup_late_orphan_binding',?,?,?,?,?,?,?,?,?,'created',?)`, id, id, id, "video_reference_image", "openai_inline_multipart", "image/png", uint64(len(orphanRaw)), orphan.SourceBucket, orphan.SourceKey, base.Add(time.Hour)).Error; err == nil {
		t.Fatal("confirmed孤儿在删除竞争期不得被迟到绑定到UploadSession")
	}
	// 注入物理删除成功后的首次DB确认失败；过期租约重入必须以对象已不存在完成原观察，而非重复误删。
	wrapped := &videoObjectDeleteHookInventory{VideoObjectInventory: outputStore}
	scanner.inventory = wrapped
	worker, err := NewVideoOrphanCleanupWorker(scanner, wrapped, "object-cleanup-worker")
	if err != nil {
		t.Fatal(err)
	}
	var arm, injected atomic.Bool
	callbackName := "video:g7:orphan:complete-failure"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if arm.Load() && tx.Statement != nil && tx.Statement.Table == "ai_video_object_reconciliation_observations" && injected.CompareAndSwap(false, true) {
			tx.AddError(errors.New("注入删除后数据库回写失败"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove(callbackName)
	wrapped.afterDelete = func() { arm.Store(true) }
	worker.now = func() time.Time { return base.Add(7 * time.Minute) }
	if ran, err := worker.RunOnce(context.Background()); !ran || err == nil || !injected.Load() {
		t.Fatalf("首次必须复现删除成功后DB失败: ran=%t injected=%t err=%v", ran, injected.Load(), err)
	}
	if _, err := outputStore.InspectObject(context.Background(), video.VideoObjectRef{Bucket: orphan.SourceBucket, ObjectKey: orphan.SourceKey}); !errors.Is(err, video.ErrVideoObjectNotFound) {
		t.Fatalf("故障注入前物理删除必须已完成: %v", err)
	}
	arm.Store(false)
	worker.now = func() time.Time { return base.Add(10 * time.Minute) }
	if ran, err := worker.RunOnce(context.Background()); !ran || err != nil {
		t.Fatalf("过期租约必须从对象已不存在状态收敛: ran=%t err=%v", ran, err)
	}
	var cleanupTask model.AICompensationTask
	if err := db.Table("ai_compensation_tasks").Where("task_type='video_orphan_cleanup'").Take(&cleanupTask).Error; err != nil || cleanupTask.Status != "completed" {
		t.Fatalf("孤儿补偿必须完成且保留事实: task=%+v err=%v", cleanupTask, err)
	}
	if err := db.Table("ai_gateway_input_assets").Where("public_id=?", *completed.InputAssetID).Update("expires_at", base.Add(-time.Minute)).Error; err != nil {
		t.Fatalf("准备已到期输入失败: %v", err)
	}
	retention, err := NewVideoInputRetentionWorker(&VideoHTTPService{db: db, uploads: service}, "retention-worker")
	if err != nil {
		t.Fatal(err)
	}
	retention.now = func() time.Time { return base.Add(11 * time.Minute) }
	if processed, err := retention.RunOnce(context.Background(), 10); err != nil || processed != 1 {
		t.Fatalf("到期输入必须经原删除/清理账本收口: processed=%d err=%v", processed, err)
	}
	var deletedInput model.AIGatewayInputAsset
	if err := db.Where("public_id=?", *completed.InputAssetID).Take(&deletedInput).Error; err != nil || deletedInput.LifecycleState != "deleted" || deletedInput.DeletedAt == nil {
		t.Fatalf("到期输入必须保留元数据并标记正文删除: input=%+v err=%v", deletedInput, err)
	}
	var retentionRequest repository.VideoInputDeletionRequest
	if err := db.Where("input_asset_id=?", deletedInput.ID).Take(&retentionRequest).Error; err != nil || retentionRequest.RequestKind != "retention" {
		t.Fatalf("后台清理必须留下retention凭据: request=%+v err=%v", retentionRequest, err)
	}
	var sourceCount, inputCount int64
	_ = db.Table("ai_upload_sessions").Where("public_id=?", created.SessionID).Count(&sourceCount).Error
	_ = db.Table("ai_gateway_input_assets").Where("public_id=?", *completed.InputAssetID).Count(&inputCount).Error
	if sourceCount != 1 || inputCount != 1 || strings.TrimSpace(*completed.InputAssetID) == "" {
		t.Fatal("扫描不得删除上传会话或InputAsset业务事实")
	}

	// 同一前缀放置超过页上限的三个对象，每次只取一条；第二、第三轮均由新实例续页，证明尾部不会饥饿。
	for index := 0; index < 3; index++ {
		body := []byte(fmt.Sprintf("pagination-orphan-%d", index))
		sessionID := fmt.Sprintf("vup_page_pagination_%02d", index)
		inputID := fmt.Sprintf("vin_page_pagination_%02d", index)
		target := VideoUploadTarget{SessionID: sessionID, InputAssetID: inputID, UserID: id, ProjectID: id, SourceType: "openai_inline_multipart", SourceBucket: "ai-upload-temp", SourceKey: fmt.Sprintf("inline/%d/%d/%s", id, id, sessionID), NormalizedBucket: "ai-result", NormalizedKey: fmt.Sprintf("normalized/%d/%d/%s.png", id, id, inputID), MIMEType: "image/png", ExpectedSHA256: videoPayloadSHA256(body), SizeBytes: uint64(len(body)), UploadExpiresAt: base.Add(time.Hour)}
		if err := uploadStore.PutOriginal(context.Background(), target, bytes.NewReader(body), target.SizeBytes, target.ExpectedSHA256); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 3; index++ {
		pageScanner, err := NewVideoObjectReconciliationScanner(db, outputStore, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		pageScanner.now = func() time.Time { return base.Add(12 * time.Minute) }
		page, err := pageScanner.ScanStorage(context.Background(), 1)
		if err != nil || page.Observed < 1 {
			t.Fatalf("第%d次扫描必须从持久游标处理下一条孤儿: summary=%+v err=%v", index+1, page, err)
		}
	}
	var pagedObservations int64
	if err := db.Table("ai_video_object_reconciliation_observations").Where("direction='storage_unreferenced_object' AND object_key LIKE ?", fmt.Sprintf("inline/%d/%d/vup_page_pagination_%%", id, id)).Count(&pagedObservations).Error; err != nil || pagedObservations != 3 {
		t.Fatalf("三次跨页和重启必须覆盖全部尾部对象: count=%d err=%v", pagedObservations, err)
	}
	var storageCursor videoObjectScanCursor
	if err := db.Table(storageCursor.TableName()).Where("scope_key=?", fmt.Sprintf("storage|%s|%s", "ai-upload-temp", "inline/")).Take(&storageCursor).Error; err != nil || storageCursor.LastObjectKey != "" || storageCursor.CompletedCycles < 3 {
		t.Fatalf("MinIO尾页必须回卷并保留完成轮次: cursor=%+v err=%v", storageCursor, err)
	}
}

type videoObjectDeleteHookInventory struct {
	video.VideoObjectInventory
	afterDelete func()
}

func (s *videoObjectDeleteHookInventory) DeleteObservedObject(ctx context.Context, ref video.VideoObjectRef, digest string, size uint64) error {
	err := s.VideoObjectInventory.DeleteObservedObject(ctx, ref, digest, size)
	if err == nil && s.afterDelete != nil {
		s.afterDelete()
	}
	return err
}

func videoScannerResponseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}

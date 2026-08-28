package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func newImageServiceIsolationSQLMock(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("创建图片服务隔离数据库桩失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("创建图片服务隔离GORM连接失败: %v", err)
	}
	return db, mock
}

func TestImageProviderAttemptCannotClaimVideoTask(t *testing.T) {
	db, mock := newImageServiceIsolationSQLMock(t)
	provider := imagegateway.NewFakeImageAdapter(imagegateway.FakeImageSuccess)
	adapter, err := NewAttemptRecordingImageAdapter(provider, db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `ai_gateway_tasks` WHERE .*capability = \\? AND operation IS NULL.*request_id = \\?.*FOR UPDATE").
		WithArgs(model.AIImageCapability, "req-video", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	_, err = adapter.Generate(context.Background(), imagegateway.ProviderImageRequest{RequestID: "req-video"})
	if err == nil {
		t.Fatal("图片Provider尝试不得认领视频任务")
	}
	if provider.Calls() != 0 {
		t.Fatal("图片任务隔离失败时不得触发Provider")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片Provider认领SQL缺少任务隔离条件: %v", err)
	}
}

func TestImageQueueStateAndRecoverySQLExcludeVideoTask(t *testing.T) {
	db, mock := newImageServiceIsolationSQLMock(t)
	service := &ImageBillingService{db: db}
	mock.ExpectQuery("SELECT task\\.status AS task_status, request\\.execution_status, request\\.billing_status FROM ai_gateway_tasks AS task JOIN ai_requests AS request ON request\\.request_id = task\\.request_id WHERE .*task\\.capability = \\? AND task\\.operation IS NULL.*request\\.modality = \\? AND request\\.capability = \\?").
		WithArgs("req-video", model.AIImageCapability, "image", model.AIImageCapability, 1).
		WillReturnRows(sqlmock.NewRows([]string{"task_status", "execution_status", "billing_status"}))
	if _, err := service.ImageRequestQueueState(context.Background(), "req-video"); err == nil {
		t.Fatal("视频任务不得被图片队列状态查询命中")
	}

	staleBefore := time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT task\\.request_id FROM ai_gateway_tasks AS task JOIN ai_requests AS request ON request\\.request_id = task\\.request_id WHERE .*task\\.capability = \\? AND task\\.operation IS NULL.*request\\.modality = \\? AND request\\.capability = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"request_id"}))
	count, err := service.RecoverStaleActiveExecutions(context.Background(), staleBefore, 10)
	if err != nil || count != 0 {
		t.Fatalf("图片恢复扫描空结果异常: count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片队列/恢复SQL缺少媒体隔离条件: %v", err)
	}
}

func TestImageCancelSQLCannotLockVideoTaskForSameOwner(t *testing.T) {
	db, mock := newImageServiceIsolationSQLMock(t)
	service := &ImageBillingService{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `ai_gateway_tasks` WHERE .*capability = \\? AND operation IS NULL.*public_id = \\? AND user_id = \\? AND project_id = \\?.*FOR UPDATE").
		WithArgs(model.AIImageCapability, "task-video", uint64(7), uint64(9), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	_, err := service.RequestCancel(context.Background(), "task-video", repository.ImageOwner{UserID: 7, ProjectID: 9})
	if err != repository.ErrImageTaskNotFound {
		t.Fatalf("图片取消不得锁定同owner视频任务: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片取消SQL缺少任务隔离条件: %v", err)
	}
}

func TestBuildImageAssetRejectsVideoRoleAndMedia(t *testing.T) {
	base := imagegateway.GatewayAsset{
		AssetRole: model.AIImageAssetPrimaryOutput, MIMEType: "image/png", Width: 1024, Height: 1024,
		StoredObject: imagegateway.StoredObject{Ref: imagegateway.ObjectRef{Bucket: "image-results", Key: "hash/0/primary.png"}},
	}
	videoRole := base
	videoRole.AssetRole = "video"
	if _, err := buildImageAsset("req-video-role", 1, repository.ImageOwner{UserID: 7, ProjectID: 9}, videoRole, nil, time.Now()); err == nil {
		t.Fatal("图片资产构造必须拒绝视频角色")
	}
	videoMIME := base
	videoMIME.MIMEType = "video/mp4"
	if _, err := buildImageAsset("req-video-mime", 1, repository.ImageOwner{UserID: 7, ProjectID: 9}, videoMIME, nil, time.Now()); err == nil {
		t.Fatal("图片资产构造必须拒绝视频MIME")
	}
	for _, role := range []string{model.AIImageAssetContent, model.AIImageAssetPreview} {
		sharedVideoRole := base
		sharedVideoRole.AssetRole = role
		if _, err := buildImageAsset("req-shared-video-role", 1, repository.ImageOwner{UserID: 7, ProjectID: 9}, sharedVideoRole, nil, time.Now()); err == nil {
			t.Fatalf("图片资产构造必须拒绝共享视频角色: %s", role)
		}
	}
}

func TestImagePrepareRejectsInlineVideoQuoteBeforeWalletWrite(t *testing.T) {
	db, mock := newImageServiceIsolationSQLMock(t)
	service := &ImageBillingService{db: db, now: time.Now}
	idempotencyKey := "idem-image-video-quote"
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	operation := model.AIVideoOperationTextToVideo
	_, err := service.PrepareAndReserve(context.Background(), ImagePrepareAndReserveCommand{
		Request: model.AIRequest{
			RequestID: "req-image", UserID: 7, ProjectID: uint64Pointer(9), Modality: "image", Capability: model.AIImageCapability,
			IdempotencyKey: &idempotencyKey, RequestFingerprint: &fingerprint,
		},
		Task: model.AIImageTask{PublicID: "task-image", Capability: model.AIImageCapability},
		InlineQuote: &model.AIGatewayQuote{
			PublicID: "quote-video", Capability: model.AIVideoCapability, Operation: &operation,
		},
		QuotePublicID: "quote-video",
		Owner:         repository.ImageOwner{UserID: 7, ProjectID: 9},
	})
	if err != ErrImageBillingState {
		t.Fatalf("图片预占必须在事务前拒绝视频报价: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝视频报价时不得发生请求或钱包数据库写入: %v", err)
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

func newImageIsolationSQLMock(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("创建图片隔离数据库桩失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("创建图片隔离GORM连接失败: %v", err)
	}
	return db, mock
}

func TestImageCreateRejectsVideoTaskAndAssetFacts(t *testing.T) {
	db, _ := newImageIsolationSQLMock(t)
	videoOperation := model.AIVideoOperationTextToVideo
	task := &model.AIImageTask{
		UserID: 1, ProjectID: 2, Capability: model.AIVideoCapability, Operation: &videoOperation,
	}
	if err := NewImageTaskRepository(db).Create(context.Background(), task); err == nil {
		t.Fatal("图片任务入口必须拒绝视频能力和视频operation")
	}

	videoMIME := "video/mp4"
	container := "mp4"
	asset := &model.AIImageAsset{
		UserID: 1, ProjectID: 2, TaskID: 3, Modality: "video", MIMEType: &videoMIME,
		Container: &container, AssetRole: "video", Source: "provider",
	}
	if err := NewImageAssetRepository(db).Create(context.Background(), asset); err == nil {
		t.Fatal("图片资产入口必须拒绝视频modality、角色和媒体字段")
	}
}

func TestImageTaskFindSQLExcludesVideoRowsForSameOwner(t *testing.T) {
	db, mock := newImageIsolationSQLMock(t)
	pattern := regexp.QuoteMeta("SELECT * FROM `ai_gateway_tasks` WHERE (public_id = ? AND user_id = ? AND project_id = ?) AND (capability = ? AND operation IS NULL) ORDER BY `ai_gateway_tasks`.`id` LIMIT ?")
	for range 2 {
		mock.ExpectQuery(pattern).
			WithArgs("task-video", uint64(7), uint64(9), model.AIImageCapability, 1).
			WillReturnRows(sqlmock.NewRows([]string{"id", "public_id", "capability", "operation"}))
	}
	repository := NewImageTaskRepository(db)
	_, err := repository.FindForOwner(context.Background(), "task-video", ImageOwner{UserID: 7, ProjectID: 9})
	if err != ErrImageTaskNotFound {
		t.Fatalf("同owner的视频任务必须对图片详情不可见: %v", err)
	}
	if _, err := repository.Transition(context.Background(), "task-video", ImageOwner{UserID: 7, ProjectID: 9}, 1, model.AIImageTaskReserved, 10, time.Now()); err != ErrImageTaskNotFound {
		t.Fatalf("同owner的视频任务必须拒绝图片状态推进: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片任务详情SQL缺少媒体隔离条件: %v", err)
	}
}

func TestImageHTTPListDetailAndAssetSQLExcludeVideoRowsForSameOwner(t *testing.T) {
	db, mock := newImageIsolationSQLMock(t)
	repository := NewImageHTTPRepository(db)
	owner := ImageOwner{UserID: 7, ProjectID: 9}

	mock.ExpectQuery("SELECT tasks\\.\\*, requests\\.execution_status.*FROM ai_gateway_tasks AS tasks JOIN ai_requests AS requests.*tasks\\.capability = \\? AND tasks\\.operation IS NULL.*requests\\.modality = \\? AND requests\\.capability = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := repository.FindTaskRecordForOwner(context.Background(), "task-video", owner); err != ErrImageTaskNotFound {
		t.Fatalf("图片详情不得命中同owner视频任务: %v", err)
	}

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM ai_gateway_tasks AS tasks JOIN ai_requests AS requests.*tasks\\.capability = \\? AND tasks\\.operation IS NULL.*requests\\.modality = \\? AND requests\\.capability = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT tasks\\.\\*, requests\\.execution_status.*FROM ai_gateway_tasks AS tasks JOIN ai_requests AS requests.*tasks\\.capability = \\? AND tasks\\.operation IS NULL.*requests\\.modality = \\? AND requests\\.capability = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	items, total, err := repository.ListTasksForOwner(context.Background(), ImageTaskFilter{UserID: 7, ProjectID: 9, Limit: 20})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("图片列表不得计入同owner视频任务: total=%d items=%d err=%v", total, len(items), err)
	}

	mock.ExpectQuery("SELECT \\* FROM `ai_gateway_assets` WHERE .*request_id = \\?.*modality = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	assets, err := repository.ListAssetsForRequest(context.Background(), "req-video", owner)
	if err != nil || len(assets) != 0 {
		t.Fatalf("图片资产列表不得命中同owner视频资产: assets=%d err=%v", len(assets), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片列表/详情/资产SQL缺少媒体隔离条件: %v", err)
	}
}

func TestImageCleanupAndReferenceSQLExcludeVideoAssets(t *testing.T) {
	db, mock := newImageIsolationSQLMock(t)
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT assets\\.\\* FROM ai_gateway_assets AS assets JOIN ai_requests AS requests ON requests\\.request_id = assets\\.request_id WHERE assets\\.modality = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := NewImageCleanupRepository(db).ListCleanupCandidates(context.Background(), now, 10); err != nil {
		t.Fatalf("读取图片清理候选失败: %v", err)
	}
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `ai_gateway_assets` WHERE .*bucket = \\? AND object_key = \\?").
		WithArgs("image-results", "hash/0/primary.png", 1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	hasReference, err := NewImageObjectCleanupRepository(db).HasAssetReference(context.Background(), imagegateway.ObjectRef{Bucket: "image-results", Key: "hash/0/primary.png"})
	if err != nil || !hasReference {
		t.Fatalf("视频资产引用同一对象时必须阻止图片对象回收: has=%v err=%v", hasReference, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片清理候选或跨模态对象引用保护SQL不符合预期: %v", err)
	}
}

func TestImageQuoteRepositoryRejectsVideoQuote(t *testing.T) {
	db, mock := newImageIsolationSQLMock(t)
	repo := NewImageQuoteRepository(db)
	operation := model.AIVideoOperationTextToVideo
	videoQuote := &model.AIGatewayQuote{Capability: model.AIVideoCapability, Operation: &operation}
	if err := repo.Create(context.Background(), videoQuote); err != ErrImageQuoteNotFound {
		t.Fatalf("图片报价写入口必须拒绝视频报价: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `ai_gateway_quotes` WHERE .*capability = \\? AND operation IS NULL.*public_id = \\? AND user_id = \\? AND project_id = \\?.*FOR UPDATE").
		WithArgs(model.AIImageCapability, "quote-video", uint64(7), uint64(9), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	_, _, err := repo.Consume(context.Background(), "quote-video", 7, 9, nil, "fingerprint", "req-image", time.Now())
	if err != ErrImageQuoteNotFound {
		t.Fatalf("图片报价消费入口不得命中同owner视频报价: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("图片报价消费SQL缺少能力与operation隔离条件: %v", err)
	}
}

func TestImageAssetValidationRejectsSharedVideoRoles(t *testing.T) {
	mimeType := "image/png"
	for _, role := range []string{model.AIImageAssetContent, model.AIImageAssetPreview} {
		asset := &model.AIImageAsset{Modality: "image", AssetRole: role, MIMEType: &mimeType}
		if validImageAssetFact(asset) {
			t.Fatalf("图片资产写入口必须拒绝共享视频角色: %s", role)
		}
	}
}

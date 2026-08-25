package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

func TestImageG7ObjectCleanupRepositoryMySQLRetryBoundary(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G7_ISOLATED") != "YES" {
		t.Skip("图片对象补偿MySQL边界只允许隔离基础设施门禁执行")
	}
	db, err := gorm.Open(mysql.Open(os.Getenv("MOLIN_IMAGE_G7_MYSQL_DSN")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil || databaseName != "molin_image_g7_contract" {
		t.Fatalf("拒绝非隔离数据库: database=%s err=%v", databaseName, err)
	}
	now := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)
	repo := NewImageObjectCleanupRepository(db)
	repo.now = func() time.Time { return now }
	cleanupTask := imagegateway.ObjectCleanupTask{
		RequestID: "cleanup-request-1",
		Ref: imagegateway.ObjectRef{
			Bucket: imagegateway.TemporaryObjectBucket,
			Key:    "d9b66751d38772a1e518f3e9a2ad11cb/7/primary.png",
		},
		Reason: imagegateway.ObjectCleanupAfterResultStored,
	}
	record, err := buildImageObjectCleanupModel(cleanupTask, now)
	if err != nil {
		t.Fatal(err)
	}
	boundaryKey := "image-object-cleanup:mysql-eighth-failure-boundary"
	t.Cleanup(func() {
		_ = db.Where("task_key IN ?", []string{record.TaskKey, boundaryKey}).Delete(&model.AICompensationTask{}).Error
	})
	if err := db.Where("task_key IN ?", []string{record.TaskKey, boundaryKey}).Delete(&model.AICompensationTask{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObjectCleanup(context.Background(), cleanupTask); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObjectCleanup(context.Background(), cleanupTask); err != nil {
		t.Fatalf("重复Recorder调用必须幂等: %v", err)
	}
	var count int64
	if err := db.Model(&model.AICompensationTask{}).Where("task_key = ?", record.TaskKey).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("相同对象只能形成一个补偿任务: count=%d err=%v", count, err)
	}

	lease := now.Truncate(time.Second)
	errorClass := "object_delete_failed"
	boundary := model.AICompensationTask{
		TaskKey: boundaryKey, TaskType: imageObjectCleanupTaskType, AggregateID: "temp:d9b66751d38772a1e518f3e9a2ad11cb:7:primary",
		Status: "running", RetryCount: 7, NextRetryAt: now, LockedAt: &lease, LastErrorClass: &errorClass,
	}
	if err := db.Create(&boundary).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkFailure(context.Background(), boundary.ID, lease, now.Add(time.Hour), errorClass); err != nil {
		t.Fatal(err)
	}
	var updated model.AICompensationTask
	if err := db.Where("id = ?", boundary.ID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.RetryCount != 8 || updated.Status != "dead" || updated.LockedAt != nil {
		t.Fatalf("MySQL第8次失败必须基于递增后次数进入dead: %+v", updated)
	}
	if err := repo.MarkFailure(context.Background(), boundary.ID, lease, now.Add(2*time.Hour), errorClass); !errors.Is(err, ErrImageObjectCleanupLeaseLost) {
		t.Fatalf("旧租约不得覆盖dead终态: %v", err)
	}
}

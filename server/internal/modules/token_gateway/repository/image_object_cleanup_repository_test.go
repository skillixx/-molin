package repository

import (
	"errors"
	"testing"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

func TestBuildImageObjectCleanupModelUsesHashedKeyAndRedactedAggregate(t *testing.T) {
	now := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	task := imagegateway.ObjectCleanupTask{
		RequestID: "cleanup-request-1",
		Ref: imagegateway.ObjectRef{
			Bucket: imagegateway.TemporaryObjectBucket,
			Key:    "d9b66751d38772a1e518f3e9a2ad11cb/7/primary.png",
		},
		Reason: imagegateway.ObjectCleanupAfterResultStored,
	}
	record, err := buildImageObjectCleanupModel(task, now)
	if err != nil {
		t.Fatal(err)
	}
	if record.TaskKey != "image-object-cleanup:128430afe74d83c0cdb2e6508ec13a9590e7ecc3761d01708dd9f6bc8b486bc4" ||
		record.TaskType != imageObjectCleanupTaskType || record.AggregateID != "temp:d9b66751d38772a1e518f3e9a2ad11cb:7:primary" ||
		record.Status != "pending" || record.NextRetryAt != now.Add(time.Minute) || record.LastErrorClass == nil || *record.LastErrorClass != "result_stored" {
		t.Fatalf("对象补偿持久化事实错误: %+v", record)
	}
	if ref, resolveErr := resolveImageObjectCleanupRef(record); resolveErr != nil || ref != task.Ref {
		t.Fatalf("脱敏描述符必须可重建原临时对象: ref=%+v err=%v", ref, resolveErr)
	}
}

func TestBuildImageObjectCleanupModelAcceptsControlledResultAndQuarantinePaths(t *testing.T) {
	const namespace = "d9b66751d38772a1e518f3e9a2ad11cb"
	for _, test := range []struct {
		bucket        string
		filename      string
		reason        imagegateway.ObjectCleanupReason
		wantAggregate string
	}{
		{bucket: imagegateway.ResultObjectBucket, filename: "primary.png", reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure, wantAggregate: "result:" + namespace + ":7:primary"},
		{bucket: imagegateway.ResultObjectBucket, filename: "thumbnail.png", reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure, wantAggregate: "result:" + namespace + ":7:thumbnail"},
		{bucket: imagegateway.QuarantineObjectBucket, filename: "primary.png", reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure, wantAggregate: "quarantine:" + namespace + ":7:primary"},
		{bucket: imagegateway.TemporaryObjectBucket, filename: "primary.png", reason: imagegateway.ObjectCleanupAfterTempPutUnknown, wantAggregate: "temp:" + namespace + ":7:primary"},
		{bucket: imagegateway.QuarantineObjectBucket, filename: "primary.png", reason: imagegateway.ObjectCleanupAfterQuarantinePutUnknown, wantAggregate: "quarantine:" + namespace + ":7:primary"},
		{bucket: imagegateway.ResultObjectBucket, filename: "primary.png", reason: imagegateway.ObjectCleanupAfterResultPutUnknown, wantAggregate: "result:" + namespace + ":7:primary"},
		{bucket: imagegateway.ResultObjectBucket, filename: "thumbnail.png", reason: imagegateway.ObjectCleanupAfterThumbnailPutUnknown, wantAggregate: "result:" + namespace + ":7:thumbnail"},
	} {
		task := imagegateway.ObjectCleanupTask{
			RequestID: "cleanup-request-1",
			Ref:       imagegateway.ObjectRef{Bucket: test.bucket, Key: namespace + "/7/" + test.filename},
			Reason:    test.reason,
		}
		record, err := buildImageObjectCleanupModel(task, time.Now())
		if err != nil || record.AggregateID != test.wantAggregate {
			t.Fatalf("受控元数据失败对象必须可记录: bucket=%s file=%s record=%+v err=%v", test.bucket, test.filename, record, err)
		}
		if ref, resolveErr := resolveImageObjectCleanupRef(record); resolveErr != nil || ref != task.Ref {
			t.Fatalf("受控结果描述符必须可重建: ref=%+v err=%v", ref, resolveErr)
		}
	}
}

func TestBuildImageObjectCleanupModelRejectsForgedAndOutOfBoundsPaths(t *testing.T) {
	validNamespace := "d9b66751d38772a1e518f3e9a2ad11cb"
	valid := imagegateway.ObjectCleanupTask{
		RequestID: "cleanup-request-1",
		Ref:       imagegateway.ObjectRef{Bucket: imagegateway.TemporaryObjectBucket, Key: validNamespace + "/7/primary.png"},
		Reason:    imagegateway.ObjectCleanupAfterResultStored,
	}
	tests := []imagegateway.ObjectCleanupTask{
		{RequestID: "bad request", Ref: valid.Ref, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: "ai-result", Key: valid.Ref.Key}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: valid.Ref.Bucket, Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/7/primary.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: valid.Ref.Bucket, Key: validNamespace + "/../primary.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: valid.Ref.Bucket, Key: validNamespace + "/07/primary.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: valid.Ref.Bucket, Key: validNamespace + "/18446744073709551616/primary.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: valid.Ref.Bucket, Key: validNamespace + "/7/thumbnail.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: valid.Ref, Reason: "unexpected_reason"},
		{RequestID: valid.RequestID, Ref: valid.Ref, Reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: imagegateway.ResultObjectBucket, Key: validNamespace + "/7/primary.png"}, Reason: valid.Reason},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: imagegateway.QuarantineObjectBucket, Key: validNamespace + "/7/thumbnail.png"}, Reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: "other-result", Key: validNamespace + "/7/primary.png"}, Reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure},
		{RequestID: valid.RequestID, Ref: valid.Ref, Reason: imagegateway.ObjectCleanupAfterResultPutUnknown},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: imagegateway.ResultObjectBucket, Key: validNamespace + "/7/primary.png"}, Reason: imagegateway.ObjectCleanupAfterTempPutUnknown},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: imagegateway.ResultObjectBucket, Key: validNamespace + "/7/thumbnail.png"}, Reason: imagegateway.ObjectCleanupAfterResultPutUnknown},
		{RequestID: valid.RequestID, Ref: imagegateway.ObjectRef{Bucket: imagegateway.QuarantineObjectBucket, Key: validNamespace + "/7/primary.png"}, Reason: imagegateway.ObjectCleanupAfterThumbnailPutUnknown},
	}
	for index, task := range tests {
		if _, err := buildImageObjectCleanupModel(task, time.Now()); !errors.Is(err, ErrImageObjectCleanupInvalid) {
			t.Fatalf("伪造或越界临时路径必须拒绝: index=%d err=%v", index, err)
		}
	}
}

func TestResolveImageObjectCleanupRefRejectsTamperedDescriptor(t *testing.T) {
	valid := model.AICompensationTask{
		TaskKey:     "image-object-cleanup:128430afe74d83c0cdb2e6508ec13a9590e7ecc3761d01708dd9f6bc8b486bc4",
		TaskType:    imageObjectCleanupTaskType,
		AggregateID: "temp:d9b66751d38772a1e518f3e9a2ad11cb:7:primary",
	}
	for index, task := range []model.AICompensationTask{
		{TaskKey: valid.TaskKey, TaskType: "image_reconcile", AggregateID: valid.AggregateID},
		{TaskKey: valid.TaskKey, TaskType: valid.TaskType, AggregateID: "temp:d9b66751d38772a1e518f3e9a2ad11cb:07:primary"},
		{TaskKey: valid.TaskKey, TaskType: valid.TaskType, AggregateID: "temp:d9b66751d38772a1e518f3e9a2ad11cb:18446744073709551616:primary"},
		{TaskKey: valid.TaskKey, TaskType: valid.TaskType, AggregateID: "temp:d9b66751d38772a1e518f3e9a2ad11cb:7:thumbnail"},
		{TaskKey: valid.TaskKey, TaskType: valid.TaskType, AggregateID: "quarantine:d9b66751d38772a1e518f3e9a2ad11cb:7:thumbnail"},
		{TaskKey: valid.TaskKey, TaskType: valid.TaskType, AggregateID: "../../ai-result"},
		{TaskKey: "image-object-cleanup:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TaskType: valid.TaskType, AggregateID: valid.AggregateID},
		{TaskKey: "128430afe74d83c0cdb2e6508ec13a9590e7ecc3761d01708dd9f6bc8b486bc4", TaskType: valid.TaskType, AggregateID: valid.AggregateID},
	} {
		if _, err := resolveImageObjectCleanupRef(task); !errors.Is(err, ErrImageObjectCleanupInvalid) {
			t.Fatalf("篡改补偿描述符必须拒绝: index=%d err=%v", index, err)
		}
	}
}

func TestImageObjectCleanupTaskKeyRejectsObjectsOutsideWhitelist(t *testing.T) {
	const namespace = "d9b66751d38772a1e518f3e9a2ad11cb"
	valid := imagegateway.ObjectRef{Bucket: imagegateway.TemporaryObjectBucket, Key: namespace + "/7/primary.png"}
	key, err := ImageObjectCleanupTaskKey(valid)
	if err != nil || key != "image-object-cleanup:128430afe74d83c0cdb2e6508ec13a9590e7ecc3761d01708dd9f6bc8b486bc4" {
		t.Fatalf("受控对象tombstone键错误: key=%s err=%v", key, err)
	}
	for index, ref := range []imagegateway.ObjectRef{
		{Bucket: "other", Key: valid.Key},
		{Bucket: imagegateway.TemporaryObjectBucket, Key: namespace + "/7/thumbnail.png"},
		{Bucket: imagegateway.QuarantineObjectBucket, Key: namespace + "/7/thumbnail.png"},
		{Bucket: imagegateway.ResultObjectBucket, Key: namespace + "/../primary.png"},
	} {
		if _, err := ImageObjectCleanupTaskKey(ref); !errors.Is(err, ErrImageObjectCleanupInvalid) {
			t.Fatalf("白名单外对象不得生成tombstone键: index=%d err=%v", index, err)
		}
	}
}

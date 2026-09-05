package video

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validMinIOVideoTestConfig(t *testing.T) MinIOVideoObjectStoreConfig {
	t.Helper()
	return MinIOVideoObjectStoreConfig{
		Endpoint: "minio:9000", AccessKey: "fake-access", SecretKey: "fake-secret-value", TempDirectory: t.TempDir(),
		Buckets:            []string{"ai-upload-temp", "ai-result", "ai-quarantine", "ai-user-assets"},
		VerifyArchiveFence: func(context.Context, string, uint64) error { return nil },
	}
}

func TestMinIOVideoObjectStoreRejectsUnsafeConfiguration(t *testing.T) {
	valid := validMinIOVideoTestConfig(t)
	symlink := filepath.Join(t.TempDir(), "temp-link")
	if err := os.Symlink(valid.TempDirectory, symlink); err != nil {
		t.Skipf("当前环境不能创建符号链接: %v", err)
	}
	tests := []func(*MinIOVideoObjectStoreConfig){
		func(c *MinIOVideoObjectStoreConfig) { c.Endpoint = "http://minio:9000" },
		func(c *MinIOVideoObjectStoreConfig) { c.AccessKey = "" },
		func(c *MinIOVideoObjectStoreConfig) { c.SecretKey = c.AccessKey },
		func(c *MinIOVideoObjectStoreConfig) { c.Buckets = c.Buckets[:3] },
		func(c *MinIOVideoObjectStoreConfig) { c.Buckets[1] = c.Buckets[0] },
		func(c *MinIOVideoObjectStoreConfig) { c.VerifyArchiveFence = nil },
		func(c *MinIOVideoObjectStoreConfig) { c.TempDirectory = "relative" },
		func(c *MinIOVideoObjectStoreConfig) { c.TempDirectory = symlink },
	}
	for index, mutate := range tests {
		candidate := valid
		candidate.Buckets = append([]string(nil), valid.Buckets...)
		mutate(&candidate)
		if _, err := NewMinIOVideoObjectStore(candidate); !errors.Is(err, ErrVideoObjectInvalid) {
			t.Fatalf("不安全配置必须拒绝: index=%d err=%v", index, err)
		}
	}
}

func TestMinIOVideoObjectStoreSpoolIsBoundedAndAlwaysCleaned(t *testing.T) {
	store, err := NewMinIOVideoObjectStore(validMinIOVideoTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	file, size, digest, err := store.spool(context.Background(), strings.NewReader("sealed-video"), 32)
	if err != nil || size != 12 || len(digest) != 64 {
		t.Fatalf("封存临时文件错误: size=%d digest=%s err=%v", size, digest, err)
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	if _, _, _, err := store.spool(context.Background(), strings.NewReader("too-large"), 2); !errors.Is(err, ErrVideoObjectTooLarge) {
		t.Fatalf("超限正文必须失败: %v", err)
	}
	if count, err := store.spoolTempCount(); err != nil || count != 0 {
		t.Fatalf("成功和失败路径都不得遗留临时正文: count=%d err=%v", count, err)
	}
}

func TestMinIOVideoObjectStoreArchiveFenceFailsClosed(t *testing.T) {
	config := validMinIOVideoTestConfig(t)
	config.VerifyArchiveFence = func(_ context.Context, taskID string, generation uint64) error {
		if taskID != "vid_task_1" || generation != 7 {
			return errors.New("围栏不匹配")
		}
		return nil
	}
	store, err := NewMinIOVideoObjectStore(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceArchiveFence(context.Background(), "vid_task_1", 7); err != nil {
		t.Fatalf("当前归档代次应通过: %v", err)
	}
	if err := store.verifyWrite(context.Background(), "vid_task_1"); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("普通旧执行在接管期间必须被拒绝: %v", err)
	}
	current := WithArchiveWriteGeneration(context.Background(), "vid_task_1", 7)
	if err := store.verifyWrite(current, "vid_task_1"); err != nil {
		t.Fatalf("当前归档上下文应通过: %v", err)
	}
	stale := WithArchiveWriteGeneration(context.Background(), "vid_task_1", 6)
	if err := store.verifyWrite(stale, "vid_task_1"); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("迟到归档代次必须拒绝: %v", err)
	}
}

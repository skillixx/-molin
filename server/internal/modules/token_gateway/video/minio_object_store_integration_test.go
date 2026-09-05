package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVideoG7MinIOObjectStoreIntegration(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_MINIO_ISOLATED") != "YES" {
		t.Skip("VID-G7只允许隔离MinIO门禁执行")
	}
	endpoint := os.Getenv("MOLIN_VIDEO_G7_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Fatal("隔离MinIO端点缺失")
	}
	var fenceMu sync.RWMutex
	active := map[string]uint64{}
	verifyFence := func(_ context.Context, taskID string, generation uint64) error {
		fenceMu.RLock()
		current := active[taskID]
		fenceMu.RUnlock()
		if (generation == 0 && current != 0) || (generation != 0 && current != generation) {
			return errors.New("归档围栏不匹配")
		}
		return nil
	}
	store, err := NewMinIOVideoObjectStore(MinIOVideoObjectStoreConfig{
		Endpoint: endpoint, AccessKey: os.Getenv("MOLIN_VIDEO_G7_MINIO_ACCESS"), SecretKey: os.Getenv("MOLIN_VIDEO_G7_MINIO_SECRET"), TempDirectory: t.TempDir(),
		Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine", "ai-user-assets"}, VerifyArchiveFence: verifyFence,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := store.EnsureBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	publicPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::ai-result/*"]}]}`
	if err := store.client.SetBucketPolicy(ctx, "ai-result", publicPolicy); err != nil {
		t.Fatalf("准备匿名策略漂移失败: %v", err)
	}
	if err := store.VerifyBuckets(ctx); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("存在匿名策略时运行时必须失败关闭: %v", err)
	}
	if err := store.client.SetBucketPolicy(ctx, "ai-result", ""); err != nil {
		t.Fatalf("恢复私有策略失败: %v", err)
	}
	if err := store.VerifyBuckets(ctx); err != nil {
		t.Fatalf("恢复私有策略后必须可启动: %v", err)
	}

	payload := bytes.Repeat([]byte("molin-video-range-"), 70000)
	request := PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "vid_minio_task", AssetID: "vasset_minio", Role: "content", Body: bytes.NewReader(payload), MaxBytes: int64(len(payload))}
	stored, err := store.Put(ctx, request)
	if err != nil || stored.SizeBytes != uint64(len(payload)) || len(stored.SHA256) != 64 {
		t.Fatalf("流式写入失败: stored=%+v err=%v", stored, err)
	}
	inventory, err := store.ListPrefix(ctx, "ai-upload-temp", "vid_", "", 100)
	if err != nil || len(inventory.Items) != 1 || inventory.Items[0].Ref != stored.Ref || inventory.Items[0].SHA256 != stored.SHA256 || inventory.Items[0].Discarded {
		t.Fatalf("受控用途前缀清单错误: items=%+v err=%v", inventory, err)
	}
	if _, err := store.ListPrefix(ctx, "ai-result", "", "", 100); !errors.Is(err, ErrVideoObjectInvalid) {
		t.Fatalf("无前缀全Bucket扫描必须拒绝: %v", err)
	}
	request.Body = bytes.NewReader(payload)
	replayed, err := store.Put(ctx, request)
	if err != nil || replayed != stored {
		t.Fatalf("相同正文必须幂等: replayed=%+v err=%v", replayed, err)
	}
	request.Body = bytes.NewReader([]byte("different"))
	request.MaxBytes = 64
	if _, err := store.Put(ctx, request); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("相同对象键不同正文必须冲突: %v", err)
	}
	promoted, err := store.PromoteToResult(ctx, stored.Ref)
	if err != nil || promoted.Ref.Bucket != "ai-result" || promoted.SHA256 != stored.SHA256 {
		t.Fatalf("结果晋级失败: promoted=%+v err=%v", promoted, err)
	}
	if gone, err := store.VerifyDeleted(ctx, stored.Ref); err != nil || !gone {
		t.Fatalf("晋级后临时对象必须消失: gone=%t err=%v", gone, err)
	}
	reader, err := store.GetRange(ctx, promoted.Ref, 17, 1024)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || !bytes.Equal(rangeBody, payload[17:17+1024]) {
		t.Fatalf("Range正文不一致: bytes=%d err=%v", len(rangeBody), readErr)
	}
	savedRef := VideoObjectRef{Bucket: "ai-user-assets", ObjectKey: "vsave_minio/vasset_minio/content.bin"}
	saved, err := store.CopyImmutable(ctx, promoted.Ref, savedRef, promoted.SHA256, promoted.SizeBytes)
	if err != nil || saved.Ref != savedRef || saved.SHA256 != promoted.SHA256 {
		t.Fatalf("不可变保存副本失败: saved=%+v err=%v", saved, err)
	}
	quarantined, err := store.MoveToQuarantine(ctx, promoted.Ref)
	if err != nil || quarantined.Ref.Bucket != "ai-quarantine" {
		t.Fatalf("隔离迁移失败: quarantined=%+v err=%v", quarantined, err)
	}
	for _, ref := range []VideoObjectRef{quarantined.Ref, savedRef} {
		if err := store.Delete(ctx, ref); err != nil {
			t.Fatal(err)
		}
		if gone, err := store.VerifyDeleted(ctx, ref); err != nil || !gone {
			t.Fatalf("删除必须可确认且幂等: ref=%+v gone=%t err=%v", ref, gone, err)
		}
		if err := store.Delete(ctx, ref); err != nil {
			t.Fatalf("重复删除必须成功: %v", err)
		}
	}

	// 多进程条件写使用MinIO If-None-Match；本测试先用并发请求证明同键不会被后到正文覆盖。
	var success atomic.Int64
	var conflicts atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			body := []byte(fmt.Sprintf("candidate-%02d", index))
			_, putErr := store.Put(ctx, PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "vid_minio_race", AssetID: "vasset_race", Role: "content", Body: bytes.NewReader(body), MaxBytes: 64})
			if putErr == nil {
				success.Add(1)
			} else if errors.Is(putErr, ErrVideoObjectConflict) {
				conflicts.Add(1)
			} else {
				t.Errorf("并发条件写出现非合同错误: %v", putErr)
			}
		}(index)
	}
	wait.Wait()
	if success.Load() != 1 || conflicts.Load() != 15 {
		t.Fatalf("并发不同正文只能一个获胜: success=%d conflicts=%d", success.Load(), conflicts.Load())
	}
	if count, err := store.spoolTempCount(); err != nil || count != 0 {
		t.Fatalf("并发写入不得遗留本地明文临时文件: count=%d err=%v", count, err)
	}

	fenceMu.Lock()
	active["vid_minio_fence"] = 9
	fenceMu.Unlock()
	blocked := PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "vid_minio_fence", AssetID: "vasset_fence", Role: "content", Body: bytes.NewReader([]byte("fenced")), MaxBytes: 16}
	if _, err := store.Put(ctx, blocked); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("归档接管后普通迟到写必须拒绝: %v", err)
	}
	archiveCtx := WithArchiveWriteGeneration(ctx, "vid_minio_fence", 9)
	blocked.Body = bytes.NewReader([]byte("fenced"))
	if _, err := store.Put(archiveCtx, blocked); err != nil {
		t.Fatalf("当前归档代次应允许写入: %v", err)
	}

	// bucket策略为空时，匿名对象读取和列表都不能成功。
	for _, path := range []string{"/ai-upload-temp/vid_minio_fence/vasset_fence/content.bin", "/ai-upload-temp?list-type=2"} {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+path, nil)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatalf("匿名对象读取或列表必须拒绝: path=%s", path)
		}
	}
}

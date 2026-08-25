package service

import (
	"context"
	"errors"
	"testing"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

func TestImageCleanupWorkerBoundsObjectDelete(t *testing.T) {
	worker := &ImageCleanupWorker{
		store:         &blockingCleanupObjectStore{ObjectStore: imagegateway.NewFakeObjectStore()},
		deleteTimeout: 10 * time.Millisecond,
	}
	started := time.Now()
	err := worker.deleteObject(context.Background(), imagegateway.ObjectRef{Bucket: imagegateway.ResultObjectBucket, Key: "safe/0/primary.png"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("资产清理Delete必须独立有界: elapsed=%s err=%v", time.Since(started), err)
	}
}

type blockingCleanupObjectStore struct {
	imagegateway.ObjectStore
}

func (s *blockingCleanupObjectStore) Delete(ctx context.Context, _ imagegateway.ObjectRef) error {
	<-ctx.Done()
	return ctx.Err()
}

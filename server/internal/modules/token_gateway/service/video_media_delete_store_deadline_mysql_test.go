package service

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 外部删除等待跨过资格期限后，再交给遵守context的Fake存储；不能继续消除正文。
type mediaDeleteDeadlineStore struct {
	VideoMediaDeleteStore
	expected                 time.Time
	entered, bounded, active atomic.Bool
}

func (s *mediaDeleteDeadlineStore) Delete(ctx context.Context, ref video.VideoObjectRef) error {
	s.entered.Store(true)
	d, ok := ctx.Deadline()
	s.bounded.Store(ok && d.Equal(s.expected))
	s.active.Store(time.Now().Before(s.expected))
	if !s.bounded.Load() {
		return errors.New("合成删除未收到资格期限")
	}
	<-ctx.Done()
	return s.VideoMediaDeleteStore.Delete(ctx, ref)
}

func TestVideoG6MediaDeleteStoreAuthorizationDeadlineMySQL(t *testing.T) {
	for _, role := range []string{"content", "thumbnail"} {
		t.Run(role, func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			id := f.CreateCompletedForKey(f.ProjectID)
			var asset model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role=?", id, role).Take(&asset).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("expires_at", time.Now().UTC().Add(5*time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			var key struct{ ExpiresAt time.Time }
			if err := f.DB.Table("api_keys").Select("expires_at").Where("id=?", f.ProjectID).Take(&key).Error; err != nil {
				t.Fatal(err)
			}
			store := &mediaDeleteDeadlineStore{VideoMediaDeleteStore: f.App.mediaDeleteStore, expected: key.ExpiresAt}
			f.App.mediaDeleteStore = store
			before := f.FinancialSnapshot()
			caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			result, err := f.App.DeleteVideoAsset(context.Background(), caller, asset.PublicID, asset.VersionNo, "g6-delete-store-deadline-"+role)
			if !store.entered.Load() || !store.bounded.Load() || !store.active.Load() || time.Now().Before(key.ExpiresAt) {
				t.Fatal("必须在有效期内进入Delete，收到精确资格deadline并实际等待至到期")
			}
			if !errors.Is(err, ErrVideoMediaDeleteUnavailable) || result != nil || f.MediaDeleteCalls() != 1 {
				t.Fatalf("外部截止应产生一次失败尝试而非完成：err=%v attempts=%d", err, f.MediaDeleteCalls())
			}
			for name, fact := range f.InspectMedia(id) {
				if !fact.Present || !fact.HashMatches || fact.Deleted {
					t.Errorf("资格过期后正文必须仍在：%s", name)
				}
			}
			if err := f.DB.Where("id=?", asset.ID).Take(&asset).Error; err != nil || asset.LifecycleState != "delete_failed" || asset.MediaDeletedAt != nil {
				t.Fatal("必须保留可恢复失败意图，不能标记实际删除")
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("外部等待到期不得改变原生成财务")
			}
		})
	}
}

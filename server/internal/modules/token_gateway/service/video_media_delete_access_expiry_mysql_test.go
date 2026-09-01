package service

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 只标记执行阶段的外部Head已经返回，不干预业务校验或存储内容。
type mediaDeleteAccessExpiryStore struct {
	VideoMediaDeleteStore
	executing    atomic.Bool
	headReturned atomic.Bool
}

func (s *mediaDeleteAccessExpiryStore) VerifyDeleted(ctx context.Context, ref video.VideoObjectRef) (bool, error) {
	gone, err := s.VideoMediaDeleteStore.VerifyDeleted(ctx, ref)
	if err == nil && !gone {
		s.executing.Store(true)
	}
	return gone, err
}
func (s *mediaDeleteAccessExpiryStore) Head(ctx context.Context, ref video.VideoObjectRef) (video.StoredVideoObject, error) {
	meta, err := s.VideoMediaDeleteStore.Head(ctx, ref)
	if err == nil && s.executing.Load() {
		s.headReturned.Store(true)
	}
	return meta, err
}

func TestVideoG6MediaDeleteKeyExpiresDuringAuthorizationMySQL(t *testing.T) {
	for _, role := range []string{"content", "thumbnail"} {
		t.Run(role, func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			id := f.CreateCompletedForKey(f.ProjectID)
			var a model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role=?", id, role).Take(&a).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("expires_at", time.Now().UTC().Add(5*time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			var key struct{ ExpiresAt time.Time }
			if err := f.DB.Table("api_keys").Select("expires_at").Where("id=?", f.ProjectID).Take(&key).Error; err != nil {
				t.Fatal(err)
			}
			before := f.FinancialSnapshot()
			store := &mediaDeleteAccessExpiryStore{VideoMediaDeleteStore: f.App.mediaDeleteStore}
			f.App.mediaDeleteStore = store
			var injected, valid atomic.Bool
			const hook = "g6_delete_key_query_expiry"
			if err := f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
				if tx.Error == nil && tx.Statement.Table == "api_keys" && store.headReturned.Load() && injected.CompareAndSwap(false, true) {
					valid.Store(time.Now().Before(key.ExpiresAt))
					if wait := time.Until(key.ExpiresAt.Add(30 * time.Millisecond)); wait > 0 {
						time.Sleep(wait)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.DB.Callback().Query().Remove(hook) })
			caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			result, err := f.App.DeleteVideoAsset(context.Background(), caller, a.PublicID, a.VersionNo, "g6-delete-key-query-expiry-"+role)
			if !injected.Load() || !valid.Load() {
				t.Fatal("必须先通过有效Key查询，再在返回时跨越数据库读回期限")
			}
			if !errors.Is(err, ErrVideoBillingAccess) || result != nil || f.MediaDeleteCalls() != 0 {
				t.Errorf("查询等待跨期不得沿用旧now：denied=%t deletes=%d", errors.Is(err, ErrVideoBillingAccess), f.MediaDeleteCalls())
			}
			for name, fact := range f.InspectMedia(id) {
				if !fact.Present || !fact.HashMatches || fact.Deleted {
					t.Errorf("过期Key不得删除媒体：%s", name)
				}
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("Key到期不能改写生成财务")
			}
		})
	}
}

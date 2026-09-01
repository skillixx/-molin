package service_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

// 首次墓碑查询标识执行阶段，随后Head返回时吊销JWT；准备阶段不得提前注入。
type mediaDeleteRevokeHead struct {
	service.VideoMediaDeleteStore
	executing atomic.Bool
	injected  atomic.Bool
	revoke    func()
}

func (s *mediaDeleteRevokeHead) VerifyDeleted(ctx context.Context, ref video.VideoObjectRef) (bool, error) {
	gone, err := s.VideoMediaDeleteStore.VerifyDeleted(ctx, ref)
	if err == nil && !gone {
		s.executing.Store(true)
	}
	return gone, err
}

func (s *mediaDeleteRevokeHead) Head(ctx context.Context, ref video.VideoObjectRef) (video.StoredVideoObject, error) {
	meta, err := s.VideoMediaDeleteStore.Head(ctx, ref)
	if err == nil && s.executing.Load() && s.injected.CompareAndSwap(false, true) {
		s.revoke()
	}
	return meta, err
}

func TestVideoG6MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL(t *testing.T) {
	for _, role := range []string{"content", "thumbnail"} {
		t.Run(role, func(t *testing.T) {
			f := service.NewVideoContentHTTPFixture(t)
			id := f.CreateCompletedForKey(0)
			var asset model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role=?", id, role).Take(&asset).Error; err != nil {
				t.Fatal(err)
			}
			before := f.FinancialSnapshot()
			var store *mediaDeleteRevokeHead
			f.WrapMediaDeleteStore(func(original service.VideoMediaDeleteStore) service.VideoMediaDeleteStore {
				store = &mediaDeleteRevokeHead{VideoMediaDeleteStore: original, revoke: f.RevokeToken}
				return store
			})
			mux := http.NewServeMux()
			gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 40 * time.Second}
			req, err := http.NewRequest("DELETE", srv.URL+"/api/token/video-assets/"+asset.PublicID, bytes.NewBufferString(fmt.Sprintf(`{"version_no":%d}`, asset.VersionNo)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+f.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "g6-delete-revoke-"+role)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if !store.injected.Load() {
				t.Fatal("必须在执行Head返回窗口实际吊销JWT")
			}
			if resp.StatusCode != 401 || f.MediaDeleteCalls() != 0 {
				t.Errorf("撤销后必须在不可逆删除前401：status=%d deletes=%d", resp.StatusCode, f.MediaDeleteCalls())
			}
			for name, fact := range f.InspectMedia(id) {
				if !fact.Present || !fact.HashMatches || fact.Deleted {
					t.Errorf("撤销时原媒体必须保留：%s", name)
				}
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("凭据失效不得改写生成财务")
			}
		})
	}
}

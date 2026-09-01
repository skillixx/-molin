package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"molin/server/internal/middleware"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

// 原G5真实执行/结算/交付产生可播放大MP4；第二片失败必须断流，不能追加JSON或遗留连接名额。
func TestVideoG6PlayableContentHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t, true)
	id := f.CreateCompletedForKey(f.ProjectID)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	done := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		middleware.Recovery(mux).ServeHTTP(w, r)
	}))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	get := func() (*http.Response, []byte, error) {
		t.Helper()
		r, err := http.NewRequest("GET", server.URL+"/v1/videos/"+id+"/content", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+f.Key)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("下载结束未执行服务端回收")
		}
		return resp, body, readErr
	}
	expected := service.VideoPlayableFixtureBytes()
	resp, body, err := get()
	if err != nil || resp.StatusCode != 200 || !bytes.Equal(body, expected) {
		t.Fatalf("实际G5私有媒体必须完整返回原可播放字节：status=%d err=%v", resp.StatusCode, err)
	}
	// 金融事实使用整行快照，排除请求读取过程中暗改金额、版本、状态或新增Outbox。
	finance := func() []byte {
		t.Helper()
		snapshot := map[string][]string{}
		for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events"} {
			var rows []map[string]any
			predicate := "user_id=?"
			if table == "ai_usage_items" || table == "ai_request_wallet_links" {
				predicate = "request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
			}
			if table == "ai_outbox_events" {
				predicate = "aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
			}
			if err := f.DB.Table(table).Where(predicate, f.ProjectID).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				raw, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				snapshot[table] = append(snapshot[table], string(raw))
			}
			sort.Strings(snapshot[table])
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := finance()
	f.FailAfterFirstRange(true)
	reads := f.RangeCalls()
	resp, body, err = get()
	if resp.StatusCode != 200 || !errors.Is(err, io.ErrUnexpectedEOF) || !bytes.Equal(body, expected[:1<<20]) || f.RangeCalls()-reads != 2 {
		t.Fatalf("第二片失败只能发送原首片并断流：length=%d reads=%d err=%v", len(body), f.RangeCalls()-reads, err)
	}
	var unreleased int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL", f.ProjectID).Count(&unreleased).Error; err != nil || unreleased != 0 {
		t.Fatal("中途断流必须释放租约")
	}
	if !bytes.Equal(before, finance()) {
		t.Fatal("断流不能改变原请求或财务事实")
	}
}

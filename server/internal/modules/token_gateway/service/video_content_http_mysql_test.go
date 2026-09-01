package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/service"
)

// 真正通过认证HTTP创建、查询内容和Range；外部执行显式推进，读取不得推进财务状态。
func TestVideoG6ContentHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	finished := make(chan string, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 客户端收到Content-Length最后一字节并不保证Handler已执行defer Close，用真实完成信号消除竞态。
		defer func() { finished <- w.Header().Get("X-Request-ID") }()
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	for k, v := range map[string]string{"model": f.Model, "prompt": "无商业用途的合成测试视频", "seconds": "5", "size": "1280x720"} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", server.URL+"/v1/videos", &form)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", w.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+f.Key)
	r.Header.Set("Idempotency-Key", "g6-content-http-create")
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var job dto.VideoJob
	err = json.NewDecoder(resp.Body).Decode(&job)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || job.Status != "queued" {
		t.Fatal("必须由实际HTTP创建待执行任务")
	}
	path := "/v1/videos/" + job.ID + "/content"
	get := func(path, key, rangeValue, ifRange string, want int) ([]byte, http.Header) {
		t.Helper()
		r, err := http.NewRequest("GET", server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+key)
		if rangeValue != "" {
			r.Header.Set("Range", rangeValue)
		}
		if ifRange != "" {
			r.Header.Set("If-Range", ifRange)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("内容HTTP期望%d，实际%d，读取错误%v", want, resp.StatusCode, err)
		}
		if resp.Header.Get("X-Request-ID") == "" {
			t.Fatal("内容和错误必须有HTTP追踪ID")
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for done := false; !done; {
			select {
			case id := <-finished:
				done = id == resp.Header.Get("X-Request-ID")
			case <-timer.C:
				t.Fatal("服务端未在期限内结束下载并释放租约")
			}
		}
		if want >= 400 {
			var envelope map[string]json.RawMessage
			decoder := json.NewDecoder(bytes.NewReader(body))
			if decoder.Decode(&envelope) != nil || envelope["error"] == nil {
				t.Fatal("错误必须为兼容Envelope")
			}
			var extra any
			if !errors.Is(decoder.Decode(&extra), io.EOF) {
				t.Fatal("错误响应只能有一段JSON")
			}
			if want == 503 {
				var detail struct {
					Code string `json:"code"`
				}
				if json.Unmarshal(envelope["error"], &detail) != nil || detail.Code != "video_content_unavailable" || bytes.Contains(body, []byte("内部存储失败标记")) {
					t.Fatal("存储错误必须低敏且保持冻结错误码")
				}
			}
		}
		return body, resp.Header
	}
	get(path, f.Key, "", "", 404)
	f.Execute(job.ID)
	get(path, f.Key, "", "", 404)
	f.Settle(job.ID)
	get(path, f.Key, "", "", 404)
	f.Deliver(job.ID)
	body, headers := get(path, f.Key, "", "", 200)
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(body))
	if len(body) < 8 || string(body[4:8]) != "ftyp" || headers.Get("ETag") != etag || headers.Get("Content-Type") != "video/mp4" || headers.Get("Content-Length") != strconv.Itoa(len(body)) || headers.Get("Accept-Ranges") != "bytes" {
		t.Fatal("完整MP4内容和HTTP元数据不一致")
	}
	part, partialHeaders := get(path, f.Key, "bytes=4-7", etag, 206)
	if string(part) != "ftyp" || partialHeaders.Get("Content-Range") != fmt.Sprintf("bytes 4-7/%d", len(body)) || partialHeaders.Get("Content-Length") != "4" {
		t.Fatal("单Range必须匹配原内容")
	}
	for _, value := range []string{"bytes=999999999-", "bytes=0-1,4-5", "bytes=-0"} {
		_, h := get(path, f.Key, value, "", 416)
		if h.Get("Content-Range") != fmt.Sprintf("bytes */%d", len(body)) {
			t.Fatal("416必须包含完整大小")
		}
	}
	full, _ := get(path, f.Key, "bytes=999999999-", "\"stale\"", 200)
	if !bytes.Equal(full, body) {
		t.Fatal("旧If-Range必须返回完整当前内容")
	}
	get(path+"?variant=thumbnail", f.Key, "", "", 400)
	get(path, f.OtherKey, "", "", 404)
	get(path, f.Token, "", "", 401)
	// 另一把有效Key读取自己的已交付任务，也必须共享同一用户的两个连接名额。
	otherVideo := f.CreateCompletedForKey(f.ProjectID + 9000000)
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	first, err := f.App.GetContent(context.Background(), caller, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := f.App.GetContent(context.Background(), caller, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	beforeHeads := f.HeadCalls()
	get(path, f.Key, "", "", 429)
	get("/v1/videos/"+otherVideo+"/content", f.OtherKey, "", "", 429)
	if f.HeadCalls() != beforeHeads {
		t.Fatal("下载超限不得访问Store")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	get("/v1/videos/"+otherVideo+"/content", f.OtherKey, "bytes=0-7", "", 206)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	f.FailHead(true)
	get(path, f.Key, "", "", 503)
	f.FailHead(false)
	f.ExpireLeaseOnRead()
	get(path, f.Key, "", "", 503)
	get(path, f.Key, "bytes=0-7", "", 206)
	var activeDownloads int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", f.ProjectID).Count(&activeDownloads).Error; err != nil || activeDownloads != 0 {
		t.Fatalf("成功、Head失败和写前到期后均应释放名额：active=%d err=%v", activeDownloads, err)
	}
	var unreleased int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL", f.ProjectID).Count(&unreleased).Error; err != nil || unreleased != 0 {
		t.Fatalf("已过期记录也必须执行释放，不得仅以到期冒充Close：unreleased=%d err=%v", unreleased, err)
	}
	if err := f.DB.Exec("UPDATE ai_gateway_assets SET legal_hold=1 WHERE request_id=(SELECT request_id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='thumbnail'", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	get(path, f.Key, "", "", 404)
}

func TestVideoG6ContentStreamRevocationAndDeleteMySQL(t *testing.T) {
	for _, mode := range []string{"permission_revoked", "media_deleted"} {
		t.Run(mode, func(t *testing.T) {
			f := service.NewVideoContentHTTPFixture(t, true)
			id := f.CreateCompletedForKey(f.ProjectID)
			caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			mux := http.NewServeMux()
			gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
			finished := make(chan struct{}, 1)
			invalidation := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/videos/"+id+"/content" {
					defer func() { finished <- struct{}{} }()
					w = &videoRevokeAfterChunk{ResponseWriter: w, revoke: func() {
						switch mode {
						case "permission_revoked":
							invalidation <- f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='video:generate'", f.ProjectID).Error
						case "media_deleted":
							_, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-content-stream-delete")
							invalidation <- err
						}
					}}
				}
				mux.ServeHTTP(w, r)
			}))
			defer server.Close()
			before := f.FinancialSnapshot()
			ranges := f.RangeCalls()
			req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/videos/"+id+"/content", nil)
			req.Header.Set("Authorization", "Bearer "+f.Key)
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			select {
			case <-finished:
			case <-time.After(30 * time.Second):
				t.Fatal("流中失效后Handler未退出")
			}
			if err := <-invalidation; err != nil {
				t.Fatalf("流中状态变化未提交：%v", err)
			}
			if resp.StatusCode != http.StatusOK || readErr == nil || len(body) != 1<<20 || f.RangeCalls()-ranges != 1 {
				t.Fatalf("流中失效必须恰好一片后截断：status=%d bytes=%d ranges=%d read_failed=%t", resp.StatusCode, len(body), f.RangeCalls()-ranges, readErr != nil)
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("流中撤权或删除不能改变原生成财务")
			}
			var active int64
			if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL", f.ProjectID).Count(&active).Error; err != nil || active != 0 {
				t.Fatalf("流中失效必须释放下载租约：active=%d err=%v", active, err)
			}
		})
	}
}

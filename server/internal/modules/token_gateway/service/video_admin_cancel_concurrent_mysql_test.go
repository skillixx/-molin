package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminCancelConcurrentMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	srv := f.server(t, "g6-admin-concurrent-v1", f.secret)
	body := []byte(fmt.Sprintf(`{"reason":"合成管理员并发取消","version_no":%d}`, f.version))
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 100, MaxIdleConnsPerHost: 100}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 35 * time.Second}
	type result struct {
		reply service.VideoAdminCancellationReply
		err   error
	}
	results := make(chan result, 100)
	start := make(chan struct{})
	submits := f.f.SubmitCalls()
	for i := 0; i < 100; i++ {
		go func() {
			<-start
			r, err := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+f.task+"/cancel", bytes.NewReader(body))
			if err != nil {
				results <- result{err: err}
				return
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+f.token)
			r.Header.Set("Idempotency-Key", "g6-admin-negative-command")
			resp, err := client.Do(r)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				results <- result{err: err}
				return
			}
			var envelope struct {
				Code int                                 `json:"code"`
				Data service.VideoAdminCancellationReply `json:"data"`
			}
			if json.Unmarshal(raw, &envelope) != nil || resp.StatusCode != 200 || envelope.Code != 0 {
				results <- result{err: fmt.Errorf("并发取消失败HTTP=%d code=%d", resp.StatusCode, envelope.Code)}
				return
			}
			results <- result{reply: envelope.Data}
		}()
	}
	close(start)
	created, replayed := 0, 0
	requestID := ""
	var finalVersion uint64
	for i := 0; i < 100; i++ {
		r := <-results
		if r.err != nil {
			t.Error(r.err)
			continue
		}
		v := r.reply
		if v.VideoAdminTaskDetails == nil || v.VideoTaskDetails == nil || v.TaskID != f.task || v.UserID != f.f.ProjectID || v.CancellationResult != "cancelled" || v.ExecutionStatus != "cancelled" || v.BillingStatus != "released" || v.CancelRequestedAt == nil {
			t.Error("并发回复必须保留同一安全取消结果")
			continue
		}
		if v.CurrentFrozenAmount == nil || *v.CurrentFrozenAmount != "0.00000000" || v.NetReleasedAmount == nil || *v.NetReleasedAmount != "0.50000000" {
			t.Error("每个并发响应都必须展示原0.50净释放，而非重复退款")
		}
		if requestID == "" {
			requestID, finalVersion = v.RequestID, v.VersionNo
		}
		if v.RequestID != requestID || v.VersionNo != finalVersion {
			t.Error("重放不能另建请求或推进版本")
		}
		if v.Idempotent {
			replayed++
		} else {
			created++
		}
	}
	if created != 1 || replayed != 99 {
		t.Fatalf("首次/重放应1/99，实际%d/%d", created, replayed)
	}
	commands, audits := f.counts(t)
	if commands != 1 || audits != 2 {
		t.Fatal("100并发只能形成一命令与前后两审计")
	}
	var unfreezes int64
	if err := f.f.DB.Table("wallet_transactions").Where("user_id=? AND type='unfreeze'", f.f.ProjectID).Count(&unfreezes).Error; err != nil || unfreezes != 2 {
		t.Fatalf("只允许原图片/视频各一笔解冻：count=%d err=%v", unfreezes, err)
	}
	before := f.f.FinancialSnapshot()
	f.call(t, srv, body, 200)
	if !bytes.Equal(before, f.f.FinancialSnapshot()) || submits != f.f.SubmitCalls() {
		t.Fatal("继续重放不得改财务或提交Provider")
	}
}

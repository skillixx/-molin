package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 定向故障只注入外部存储，完整业务准入与G5对账继续执行。
type videoContentFaultStore struct {
	VideoContentStore
	mode atomic.Value
	hits atomic.Uint32
}

func (s *videoContentFaultStore) setMode(mode string) {
	s.hits.Store(0)
	s.mode.Store(mode)
}

func (s *videoContentFaultStore) Head(ctx context.Context, ref video.VideoObjectRef) (video.StoredVideoObject, error) {
	m, err := s.VideoContentStore.Head(ctx, ref)
	if err != nil {
		return m, err
	}
	switch s.mode.Load() {
	case "hash":
		s.hits.Add(1)
		m.SHA256 = strings.Repeat("0", 64)
	case "size":
		s.hits.Add(1)
		m.SizeBytes++
	case "ref":
		s.hits.Add(1)
		m.Ref.ObjectKey = "wrong-object"
	case "missing":
		s.hits.Add(1)
		return video.StoredVideoObject{}, errors.New("内部存储失败")
	}
	return m, nil
}

func (s *videoContentFaultStore) GetRange(ctx context.Context, ref video.VideoObjectRef, offset, length int64) (io.ReadCloser, error) {
	switch s.mode.Load() {
	case "short":
		s.hits.Add(1)
		return io.NopCloser(bytes.NewReader(make([]byte, length-1))), nil
	case "long":
		s.hits.Add(1)
		return io.NopCloser(bytes.NewReader(make([]byte, length+1))), nil
	case "close":
		s.hits.Add(1)
		return videoContentBadClose{Reader: bytes.NewReader(make([]byte, length))}, nil
	}
	return s.VideoContentStore.GetRange(ctx, ref, offset, length)
}

type videoContentBadClose struct{ io.Reader }

func (videoContentBadClose) Close() error { return errors.New("内部关闭错误") }

// 内容必须来自原G5执行链；查询和分片不能顺便结算、交付或重新生成。
func TestVideoG6ContentMySQLFinancialGate(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	store := video.NewFakeVideoObjectStore()
	// 能力创建时固定同一个外部后端；之后注入后端故障，不能通过替换服务字段假装影响既有能力。
	faults := &videoContentFaultStore{VideoContentStore: store}
	faults.setMode("")
	app, err := NewVideoHTTPService(f.legacy.db, VideoBillingOptions{QuoteSecret: f.legacy.service.quoteSecret, PromptSecret: f.legacy.service.promptSecret, IntentSecret: f.legacy.service.intentSecret, Protector: f.legacy.service.protector, Safety: f.legacy.service.safety}, VideoHTTPOptions{ContentStore: faults})
	if err != nil {
		t.Fatal(err)
	}
	c := f.command
	c.Operation, c.InputAssetID = model.AIVideoOperationTextToVideo, ""
	c.IdempotencyKey = "g6-content-financial-create"
	job, err := app.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	assertDenied := func() {
		t.Helper()
		if got, err := app.GetContent(ctx, c.Caller, job.Job.ID); err == nil || got != nil {
			t.Fatal("未闭合不得返回内容能力")
		}
	}
	assertDenied()
	adapter := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: app.NewTaskLedger(f.legacy.owner, videoG4TestLocationFactory{}), Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
	if _, err := gateway.Submit(ctx, job.Job.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := gateway.Poll(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gateway.FetchAndFinalize(ctx, job.Job.ID); err != nil {
		t.Fatal(err)
	}
	assertDenied()
	if _, err := app.billing.SettleReady(ctx, job.Job.ID, f.legacy.owner); err != nil {
		t.Fatal(err)
	}
	assertDenied()
	if _, err := app.billing.DeliverReady(ctx, job.Job.ID, f.legacy.owner); err != nil {
		t.Fatal(err)
	}
	content, err := app.GetContent(ctx, c.Caller, job.Job.ID)
	if err != nil || content == nil {
		t.Fatalf("已闭合原任务应能读取：%v", err)
	}
	defer content.Close()
	r, err := content.OpenRange(ctx, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || len(data) != 8 || string(data[4:]) != "ftyp" {
		t.Fatal("必须读取原私有MP4正文")
	}
	for _, mode := range []string{"hash", "size", "ref", "missing", "short", "long", "close"} {
		t.Run(mode, func(t *testing.T) {
			faults.setMode(mode)
			defer faults.setMode("")
			if r, err := content.OpenRange(ctx, 0, 8); !errors.Is(err, ErrVideoContentUnavailable) || r != nil {
				t.Fatalf("存储完整性或读取失败必须低敏关闭：%v", err)
			}
			if faults.hits.Load() != 1 {
				t.Fatal("必须实际触发本次外部故障，不能用其它准入失败冒充完整性拒绝")
			}
		})
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if r, err := content.OpenRange(stopped, 0, 8); err == nil || r != nil {
		t.Fatal("取消不得返回内容片段")
	}
	for _, pair := range [][2]int64{{-1, 1}, {0, 0}, {0, 1<<20 + 1}, {content.Size, 1}, {content.Size - 1, 2}} {
		if r, err := content.OpenRange(ctx, pair[0], pair[1]); err == nil || r != nil {
			t.Fatal("分片边界必须受控")
		}
	}
	// 已取得的读取能力不得跨越当前Key撤权；恢复权限后还需重新核对财务事实。
	if err := f.legacy.db.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=?", c.Caller.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	if r, err := content.OpenRange(ctx, 0, 8); err == nil || r != nil {
		t.Fatal("撤权后旧分片能力必须失效")
	}
	if err := f.legacy.db.Exec("UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", c.Caller.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.legacy.db.Exec("UPDATE ai_outbox_events SET payload_json=JSON_SET(payload_json,'$.amount','999.00000000') WHERE aggregate_id=? AND event_type='video_billing_held'", job.RequestID).Error; err != nil {
		t.Fatal(err)
	}
	assertDenied()
	if r, err := content.OpenRange(ctx, 0, 8); err == nil || r != nil {
		t.Fatal("对账损坏后旧能力也不得读取")
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatal("读取不得重新提交Provider")
	}
}

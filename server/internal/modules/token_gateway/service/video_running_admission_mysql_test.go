package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG6RunningAdmissionMySQL(t *testing.T) {
	tests := []struct {
		name   string
		limits videoRunningLimits
		active int
	}{
		{"用户第1个运行第2个保持排队", videoRunningLimits{User: 1, Project: 100, Model: 100}, 1},
		{"Project第2个运行第3个保持排队", videoRunningLimits{User: 100, Project: 2, Model: 100}, 2},
		{"模型第2个运行第3个保持排队", videoRunningLimits{User: 100, Project: 100, Model: 2}, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newVideoG6I2VFixture(t)
			for i := 0; i < test.active; i++ {
				id := createVideoG6RunningTask(t, fixture, i)
				gateway, provider := newVideoG6RunningGateway(fixture, test.limits)
				result, err := gateway.Submit(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if result.Status != video.TaskSubmitted || provider.SubmitCalls() != 1 {
					t.Fatalf("运行名额内必须提交一次：index=%d status=%s calls=%d", i, result.Status, provider.SubmitCalls())
				}
			}
			queuedID := createVideoG6RunningTask(t, fixture, test.active)
			gateway, provider := newVideoG6RunningGateway(fixture, test.limits)
			result, err := gateway.Submit(context.Background(), queuedID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != video.TaskQueued || provider.SubmitCalls() != 0 {
				t.Fatalf("容量满必须保持排队且不调用Provider：status=%s calls=%d", result.Status, provider.SubmitCalls())
			}
		})
	}
}

func TestVideoG6RunningAdmissionConcurrentMySQL(t *testing.T) {
	fixture := newVideoG6I2VFixture(t)
	jobs := []string{createVideoG6RunningTask(t, fixture, 100), createVideoG6RunningTask(t, fixture, 101)}
	start := make(chan struct{})
	results := make(chan video.GatewayTask, len(jobs))
	errs := make(chan error, len(jobs))
	providers := make([]*videoPlayableProvider, len(jobs))
	var wg sync.WaitGroup
	for i, id := range jobs {
		gateway, provider := newVideoG6RunningGateway(fixture, videoG6RunningLimits())
		providers[i] = provider
		wg.Add(1)
		go func(g *video.VideoGateway, taskID string) {
			defer wg.Done()
			<-start
			result, err := g.Submit(context.Background(), taskID)
			results <- result
			errs <- err
		}(gateway, id)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var submitted, queued int
	for result := range results {
		switch result.Status {
		case video.TaskSubmitted:
			submitted++
		case video.TaskQueued:
			queued++
		default:
			t.Fatalf("并发裁决只能得到submitted或queued：%s", result.Status)
		}
	}
	providerCalls := 0
	for _, provider := range providers {
		providerCalls += provider.SubmitCalls()
	}
	if submitted != 1 || queued != 1 || providerCalls != 1 {
		t.Fatalf("并发运行名额必须唯一：submitted=%d queued=%d provider_calls=%d", submitted, queued, providerCalls)
	}
}

func createVideoG6RunningTask(t *testing.T, fixture videoG6I2VFixture, index int) string {
	t.Helper()
	command := fixture.command
	command.IdempotencyKey = fmt.Sprintf("g6-running-task-%d-%d", fixture.legacy.owner.UserID, index)
	command.Prompt = fmt.Sprintf("仅用于本地运行容量测试-%d", index)
	command.Operation = model.AIVideoOperationTextToVideo
	command.InputAssetID = ""
	command.RightsPolicyVersion = ""
	command.RightsAttestation = false
	command.QuoteID = ""
	created, err := fixture.app.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return created.Job.ID
}

func newVideoG6RunningGateway(fixture videoG6I2VFixture, limits videoRunningLimits) (*video.VideoGateway, *videoPlayableProvider) {
	ledger := fixture.app.NewTaskLedger(fixture.legacy.owner, videoG4TestLocationFactory{})
	ledger.runningLimits = limits
	provider := newVideoContentFixtureProvider(nil)
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{
		Ledger: ledger, Provider: provider, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()),
		Safety:  video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)),
		Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: video.NewFakeVideoObjectStore(),
	})
	return gateway, provider
}

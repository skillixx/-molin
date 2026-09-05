package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7RabbitBusinessHandlerMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "rabbit-business", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	capacityStore, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, capacityStore)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}

	opener := relayTestOpener(t, nil, nil)
	topology, err := videogateway.NewTaskTopology(fmt.Sprintf("molin.video.business.%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := opener(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := connection.Channel()
	if err != nil || topology.Declare(channel) != nil {
		t.Fatal("声明业务消费者拓扑失败")
	}
	_ = channel.Close()
	_ = connection.Close()
	publisher, err := videogateway.NewTaskPublisher(topology, opener, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := videogateway.NewTaskConsumer(topology, opener, publisher, 4, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	fixture := newVideoG5ReservationFixture(t, db, "10")
	reservation, err := NewVideoCapacityReservationCoordinator(fixture.service, recovery, capacityStore, key)
	if err != nil {
		t.Fatal(err)
	}
	created, err := reservation.ReserveAndCreate(ctx, fixture.command)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewVideoOutboxPublisher(db, publisher)
	if err != nil {
		t.Fatal(err)
	}
	outbox := NewOutboxWorker(repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", fixture.command.RequestID)), transport)
	outbox.now = func() time.Time { return time.Now().UTC().Add(time.Second) }
	if count, err := outbox.RunOnce(ctx, 10); err != nil || count != 1 {
		t.Fatalf("held Outbox必须确认发布一条submit消息: count=%d err=%v", count, err)
	}
	providerBase := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	provider := &videoCapacityProviderEntryCounter{VideoProviderAdapter: providerBase}
	objectStore := videogateway.NewFakeVideoObjectStore()
	factory := func(owner repository.VideoOwner) (*videogateway.VideoGateway, error) {
		base := NewVideoBillingTaskLedger(db, owner, fixture.service.protector, VideoServerObjectLocationFactory{}, fixture.service.referenceLoader)
		ledger, err := NewVideoCapacityTaskLedger(base, recovery, capacityStore, key)
		if err != nil {
			return nil, err
		}
		return videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: provider, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: objectStore}), nil
	}
	finalizer, err := NewVideoRabbitTaskFinalizer(&VideoHTTPService{db: db, billing: fixture.service}, recovery, capacityStore, key)
	if err != nil {
		t.Fatal(err)
	}
	var finalizationErr error
	observedFinalizer := func(ctx context.Context, taskID string, owner repository.VideoOwner) error {
		finalizationErr = finalizer(ctx, taskID, owner)
		return finalizationErr
	}
	// 先让原发布器把消息持久入队，再创建兼容Worker，模拟API应用回滚但保留收口执行器。
	handler, err := NewVideoRabbitTaskHandler(db, publisher, factory, observedFinalizer, "rollback-compatible-worker")
	if err != nil {
		t.Fatal(err)
	}
	consume := func(stage videogateway.TaskStage) {
		t.Helper()
		bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		if err := consumer.ConsumeOne(bounded, stage, handler); err != nil {
			t.Fatalf("消费%s失败: %v finalization=%v", stage, err, finalizationErr)
		}
	}
	consume(videogateway.TaskSubmit)
	if provider.entries.Load() != 1 || providerBase.SubmitCalls() != 1 {
		t.Fatalf("业务submit消息只能进入Provider一次: entries=%d tasks=%d", provider.entries.Load(), providerBase.SubmitCalls())
	}
	// 至少一次重复submit只读取已提交事实并重新唤醒poll，不得再次进入Provider。
	if err := publisher.Publish(ctx, videogateway.TaskSubmit, videogateway.TaskMessage{TaskID: created.TaskID, RequestID: created.RequestID, Attempt: 0}); err != nil {
		t.Fatal(err)
	}
	consume(videogateway.TaskSubmit)
	if provider.entries.Load() != 1 || providerBase.SubmitCalls() != 1 {
		t.Fatal("重复Rabbit投递不得重复Provider Submit")
	}
	consume(videogateway.TaskPoll)
	consume(videogateway.TaskPoll)
	consume(videogateway.TaskFetch)
	final, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, created.TaskID, fixture.owner)
	if err != nil || final.Status != "succeeded" || final.ProviderTaskID == nil || final.AttemptCount != 1 {
		t.Fatalf("Rabbit业务链必须到达唯一Provider成功事实: %+v err=%v", final, err)
	}
	beforeReplay := mediaDeleteFinanceSnapshot(t, db, fixture.owner.UserID)
	for _, stage := range []videogateway.TaskStage{videogateway.TaskPoll, videogateway.TaskFetch} {
		if err := publisher.Publish(ctx, stage, videogateway.TaskMessage{TaskID: created.TaskID, RequestID: created.RequestID, Attempt: 0}); err != nil {
			t.Fatal(err)
		}
		consume(stage)
	}
	if _, err := fixture.service.SettleReady(ctx, created.TaskID, fixture.owner); err != nil {
		t.Fatalf("结算重放必须只读返回原事实: %v", err)
	}
	if _, err := fixture.service.DeliverReady(ctx, created.TaskID, fixture.owner); err != nil {
		t.Fatalf("交付重放必须只读返回原事实: %v", err)
	}
	if !bytes.Equal(beforeReplay, mediaDeleteFinanceSnapshot(t, db, fixture.owner.UserID)) {
		t.Fatal("回滚后迟到消息和结算重放不得改写钱包、Quote、Usage或Outbox事实")
	}
	if provider.entries.Load() != 1 || providerBase.SubmitCalls() != 1 {
		t.Fatalf("回滚收口和迟到消息不得重复Provider create: entries=%d tasks=%d", provider.entries.Load(), providerBase.SubmitCalls())
	}
}

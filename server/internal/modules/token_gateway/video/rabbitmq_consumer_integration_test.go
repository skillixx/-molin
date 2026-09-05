package video

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type testDeadLetterRecovery struct {
	decision            TaskDeadLetterDecision
	prepared, completed int
}

func (h *testDeadLetterRecovery) PrepareDeadLetterRecovery(context.Context, TaskStage, TaskMessage) (TaskDeadLetterDecision, error) {
	h.prepared++
	return h.decision, nil
}

func (h *testDeadLetterRecovery) CompleteDeadLetterRecovery(context.Context, TaskStage, TaskMessage) error {
	h.completed++
	return nil
}

type testPoisonRecovery struct {
	wantDigest            string
	authorized, completed int
}

func (h *testPoisonRecovery) AuthorizeTaskPoisonDiscard(_ context.Context, _ TaskStage, digest string) error {
	if digest != h.wantDigest {
		return ErrTaskRecoveryUncertain
	}
	h.authorized++
	return nil
}

func (h *testPoisonRecovery) CompleteTaskPoisonDiscard(_ context.Context, _ TaskStage, digest string) error {
	if digest != h.wantDigest {
		return ErrTaskRecoveryUncertain
	}
	h.completed++
	return nil
}

func TestVideoG7RabbitConsumerIsolated(t *testing.T) {
	raw := os.Getenv("MOLIN_VIDEO_G7_AMQP_TEST_URL")
	if raw == "" {
		t.Skip("仅在G7本机一次性RabbitMQ验收执行")
	}
	u, err := url.Parse(raw)
	if err != nil || os.Getenv("MOLIN_VIDEO_G7_RABBIT_ISOLATED") != "YES" || u.Scheme != "amqp" || u.Hostname() != "127.0.0.1" || u.Path != "/vid_g7" || u.User == nil {
		t.Fatal("消费测试只允许授权的loopback隔离Broker")
	}
	open := func(ctx context.Context) (*amqp.Connection, error) {
		var stop func() bool
		conn, err := amqp.DialConfig(raw, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
			nc, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			if err == nil {
				stop = context.AfterFunc(ctx, func() { _ = nc.Close() })
			}
			return nc, err
		}})
		if stop != nil {
			stop()
		}
		return conn, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	admin, err := open(ctx)
	if err != nil {
		t.Fatal("隔离管理连接失败")
	}
	t.Cleanup(func() { _ = admin.CloseDeadline(time.Now().Add(time.Second)) })
	ch, err := admin.Channel()
	if err != nil {
		t.Fatal("隔离管理通道失败")
	}
	fixture := func(t *testing.T) (*TaskTopology, *TaskPublisher, *TaskConsumer) {
		t.Helper()
		topology, _ := NewTaskTopology(fmt.Sprintf("molin.video.cons.%x", time.Now().UnixNano()))
		if topology.Declare(ch) != nil {
			t.Fatal("消费测试拓扑声明失败")
		}
		publisher, err := NewTaskPublisher(topology, open, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := NewTaskConsumer(topology, open, publisher, 1, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return topology, publisher, consumer
	}
	read := func(t *testing.T, queue string) amqp.Delivery {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			d, ok, err := ch.Get(queue, false)
			if err != nil {
				t.Fatal("隔离读取失败")
			}
			if ok {
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("未收到应保留的原消息")
		return amqp.Delivery{}
	}
	message := TaskMessage{TaskID: "video_consumer01", RequestID: "req_consumer01"}
	t.Run("prefetch与处理后ACK", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		second := message
		second.TaskID = "video_consumer02"
		if publisher.Publish(ctx, TaskSubmit, message) != nil || publisher.Publish(ctx, TaskSubmit, second) != nil {
			t.Fatal("测试发布失败")
		}
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		processing, stop := context.WithCancel(ctx)
		defer stop()
		go func() {
			done <- consumer.ConsumeOne(processing, TaskSubmit, testTaskHandler(func(c context.Context, _ TaskStage, m TaskMessage) (TaskDisposition, error) {
				if m != message {
					return 0, ErrTaskMessageInvalid
				}
				close(started)
				select {
				case <-release:
					return TaskHandled, nil
				case <-c.Done():
					return 0, c.Err()
				}
			}))
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("消费者未进入处理")
		}
		if q, err := ch.QueueInspect(route.Queue); err != nil || q.Messages != 1 {
			t.Fatal("未完成处理时prefetch必须只占用一条且不提前ACK")
		}
		close(release)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("已完成处理未结束ACK")
		}
		remaining := read(t, route.Queue)
		got, err := DecodeTaskMessage(remaining.Body)
		if err != nil || got != second {
			t.Fatal("原消息完成后只能剩余第二条消息")
		}
		if remaining.Ack(false) != nil {
			t.Fatal("测试清理ACK失败")
		}
	})
	t.Run("处理错误与panic保留原消息", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		for _, panics := range []bool{false, true} {
			if publisher.Publish(ctx, TaskSubmit, message) != nil {
				t.Fatal("测试发布失败")
			}
			err := consumer.ConsumeOne(ctx, TaskSubmit, testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) {
				if panics {
					panic("DO_NOT_ECHO_PANIC")
				}
				return TaskHandled, errors.New("DO_NOT_ECHO_STORAGE_ERROR")
			}))
			if !errors.Is(err, ErrTaskHandlerUncertain) || strings.Contains(err.Error(), "DO_NOT_ECHO") {
				t.Fatal("未持久化/异常必须低敏拒绝确认")
			}
			d := read(t, route.Queue)
			got, err := DecodeTaskMessage(d.Body)
			if err != nil || got != message || !d.Redelivered {
				t.Fatal("错误处理不得ACK原消息")
			}
			_ = d.Ack(false)
		}
	})
	t.Run("重试先确认目标且次数有界", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskPoll)
		if publisher.Publish(ctx, TaskPoll, message) != nil {
			t.Fatal("测试发布失败")
		}
		attempts := []uint32{}
		handler := testTaskHandler(func(_ context.Context, _ TaskStage, m TaskMessage) (TaskDisposition, error) {
			attempts = append(attempts, m.Attempt)
			return TaskRetry, nil
		})
		if consumer.ConsumeOne(ctx, TaskPoll, handler) != nil || consumer.ConsumeOne(ctx, TaskPoll, handler) != nil {
			t.Fatal("延迟重试及到限转死信失败")
		}
		if len(attempts) != 2 || attempts[0] != 0 || attempts[1] != 1 {
			t.Fatal("重试必须保留身份并递增attempt，到限不能无限调用处理器")
		}
		dead := read(t, route.DeadQueue)
		got, err := DecodeTaskMessage(dead.Body)
		expected := message
		expected.Attempt = 1
		if err != nil || got != expected {
			t.Fatal("死信必须保留原标识和最后attempt")
		}
		_ = dead.Ack(false)
	})
	t.Run("重试不可路由不ACK原消息", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		if ch.QueueUnbind(route.Delays[0].Queue, route.Delays[0].RoutingKey, route.DelayExchange, nil) != nil {
			t.Fatal("隔离解绑失败")
		}
		if publisher.Publish(ctx, TaskSubmit, message) != nil {
			t.Fatal("测试发布失败")
		}
		err := consumer.ConsumeOne(ctx, TaskSubmit, testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) { return TaskRetry, nil }))
		if !errors.Is(err, ErrTaskUnroutable) {
			t.Fatal("重试目标失败必须返回失败")
		}
		d := read(t, route.Queue)
		got, err := DecodeTaskMessage(d.Body)
		if err != nil || got != message || !d.Redelivered {
			t.Fatal("重试搬运失败必须保留原消息及原attempt")
		}
		_ = d.Ack(false)
	})
	t.Run("重试确认丢失保留原消息", func(t *testing.T) {
		topology, publisher, _ := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		dropped := &atomic.Bool{}
		lostOpen := func(c context.Context) (*amqp.Connection, error) {
			var stop func() bool
			conn, err := amqp.DialConfig(raw, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
				nc, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(c, network, addr)
				if err != nil {
					return nil, err
				}
				stop = context.AfterFunc(c, func() { _ = nc.Close() })
				return &dropTaskConfirmConn{Conn: nc, dropped: dropped}, nil
			}})
			if stop != nil {
				stop()
			}
			return conn, err
		}
		uncertain, err := NewTaskPublisher(topology, lostOpen, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := NewTaskConsumer(topology, open, uncertain, 1, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if publisher.Publish(ctx, TaskSubmit, message) != nil {
			t.Fatal("测试发布失败")
		}
		err = consumer.ConsumeOne(ctx, TaskSubmit, testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) { return TaskRetry, nil }))
		if !errors.Is(err, ErrTaskPublishUnknown) || !dropped.Load() {
			t.Fatal("重试确认丢失必须返回未知，不能ACK原消息")
		}
		original := read(t, route.Queue)
		got, err := DecodeTaskMessage(original.Body)
		if err != nil || got != message || !original.Redelivered {
			t.Fatal("确认未知时原消息必须保留")
		}
		_ = original.Ack(false)
		retried := read(t, route.Queue)
		got, err = DecodeTaskMessage(retried.Body)
		expected := message
		expected.Attempt = 1
		if err != nil || got != expected {
			t.Fatal("已被Broker接收的重试副本必须保持原身份")
		}
		_ = retried.Ack(false)
	})
	t.Run("超出次数不再进入处理器", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskPoll)
		m := message
		m.Attempt = 2
		if publisher.Publish(ctx, TaskPoll, m) != nil {
			t.Fatal("测试发布失败")
		}
		called := false
		if err := consumer.ConsumeOne(ctx, TaskPoll, testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) {
			called = true
			return TaskHandled, nil
		})); err != nil || called {
			t.Fatal("超出次数必须直接安全转移死信")
		}
		dead := read(t, route.DeadQueue)
		got, err := DecodeTaskMessage(dead.Body)
		if err != nil || got != m {
			t.Fatal("死信不能把attempt重置为0")
		}
		_ = dead.Ack(false)
	})
	t.Run("超时不确认原消息", func(t *testing.T) {
		topology, publisher, _ := fixture(t)
		route, _ := topology.Route(TaskFetch)
		consumer, err := NewTaskConsumer(topology, open, publisher, 1, 50*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if publisher.Publish(ctx, TaskFetch, message) != nil {
			t.Fatal("测试发布失败")
		}
		err = consumer.ConsumeOne(ctx, TaskFetch, testTaskHandler(func(c context.Context, _ TaskStage, _ TaskMessage) (TaskDisposition, error) {
			<-c.Done()
			return TaskHandled, nil
		}))
		if !errors.Is(err, ErrTaskHandlerUncertain) {
			t.Fatal("处理期限后不能接受成功处置")
		}
		d := read(t, route.Queue)
		if !d.Redelivered {
			t.Fatal("超时必须恢复原消息")
		}
		_ = d.Ack(false)
	})
	t.Run("格式错误不复制正文到死信", func(t *testing.T) {
		topology, _, consumer := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		body := []byte(`{"task_id":"video_consumer01","request_id":"req_consumer01","attempt":0,"prompt":"DO_NOT_ECHO"}`)
		if ch.PublishWithContext(ctx, route.WorkExchange, route.RoutingKey, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body}) != nil {
			t.Fatal("异常消息注入失败")
		}
		called := false
		err := consumer.ConsumeOne(ctx, TaskSubmit, testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) {
			called = true
			return TaskHandled, nil
		}))
		if !errors.Is(err, ErrTaskMessageInvalid) || called {
			t.Fatal("错误消息不能进入业务处理")
		}
		d := read(t, route.Queue)
		if string(d.Body) != string(body) || !d.Redelivered {
			t.Fatal("原异常消息应保留供受控处置")
		}
		_ = d.Ack(false)
		if q, err := ch.QueueInspect(route.DeadQueue); err != nil || q.Messages != 0 {
			t.Fatal("不得把原错误正文再次复制到死信队列")
		}
	})
	t.Run("死信受控恢复保持attempt且重复不再发布", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskPoll)
		deadMessage := message
		deadMessage.Attempt = 1
		if err := publisher.PublishDead(ctx, TaskPoll, deadMessage); err != nil {
			t.Fatal("死信夹具发布失败")
		}
		first := &testDeadLetterRecovery{decision: TaskDeadLetterPublish}
		if err := consumer.RecoverDeadOne(ctx, TaskPoll, first); err != nil || first.prepared != 1 || first.completed != 1 {
			t.Fatalf("受控恢复必须先授权后完成审计: prepared=%d completed=%d err=%v", first.prepared, first.completed, err)
		}
		work := read(t, route.Queue)
		got, err := DecodeTaskMessage(work.Body)
		if err != nil || got != deadMessage || got.Attempt != 1 {
			t.Fatal("恢复必须保持原Task/Request和attempt")
		}
		_ = work.Ack(false)
		if err := publisher.PublishDead(ctx, TaskPoll, deadMessage); err != nil {
			t.Fatal(err)
		}
		replay := &testDeadLetterRecovery{decision: TaskDeadLetterAckExisting}
		if err := consumer.RecoverDeadOne(ctx, TaskPoll, replay); err != nil || replay.prepared != 1 || replay.completed != 0 {
			t.Fatal("已发布恢复事实只能ACK重复DLQ，不得再次发布")
		}
		if q, err := ch.QueueInspect(route.Queue); err != nil || q.Messages != 0 {
			t.Fatal("恢复重放不能形成第二条工作消息")
		}
		if err := publisher.PublishDead(ctx, TaskPoll, deadMessage); err != nil {
			t.Fatal(err)
		}
		hold := &testDeadLetterRecovery{decision: TaskDeadLetterHold}
		if err := consumer.RecoverDeadOne(ctx, TaskPoll, hold); !errors.Is(err, ErrTaskRecoveryUncertain) {
			t.Fatal("未取得持久许可必须保留原DLQ")
		}
		retained := read(t, route.DeadQueue)
		if !retained.Redelivered {
			t.Fatal("拒绝恢复的死信必须重新可见")
		}
		_ = retained.Ack(false)
	})
	t.Run("毒消息只按摘要受控丢弃且合法消息受保护", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskSubmit)
		poison := []byte(`{"task_id":"video_consumer01","request_id":"req_consumer01","attempt":0,"prompt":"DO_NOT_ECHO"}`)
		// 模拟8个进程依次熔断后各回队一条合法消息；第9条目标仍必须可达。
		legals := make([]TaskMessage, 8)
		expectedLegals := map[string]TaskMessage{}
		for i := range legals {
			legals[i] = message
			legals[i].TaskID = fmt.Sprintf("video_poison_legal_%d", i)
			expectedLegals[legals[i].TaskID] = legals[i]
			if err := publisher.Publish(ctx, TaskSubmit, legals[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := ch.PublishWithContext(ctx, route.WorkExchange, route.RoutingKey, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: poison}); err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(poison))
		recovery := &testPoisonRecovery{wantDigest: digest}
		if err := consumer.DiscardPoisonOne(ctx, TaskSubmit, recovery); err != nil || recovery.authorized != 1 || recovery.completed != 1 {
			t.Fatalf("毒消息必须经前后审计后ACK: %+v err=%v", recovery, err)
		}
		for range legals {
			legal := read(t, route.Queue)
			got, decodeErr := DecodeTaskMessage(legal.Body)
			if decodeErr != nil || !legal.Redelivered {
				t.Fatal("毒消息前方的合法消息必须原样回队")
			}
			expected, matched := expectedLegals[got.TaskID]
			if !matched || got != expected {
				t.Fatal("回队消息不得改变身份")
			}
			delete(expectedLegals, got.TaskID)
			_ = legal.Ack(false)
		}
		if len(expectedLegals) != 0 {
			t.Fatal("八条合法消息必须各自只回队一次")
		}
		if err := publisher.Publish(ctx, TaskSubmit, message); err != nil {
			t.Fatal(err)
		}
		short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancelShort()
		if err := consumer.DiscardPoisonOne(short, TaskSubmit, recovery); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrTaskRecoveryUncertain) {
			t.Fatal("合法业务消息不能由毒消息入口丢弃")
		}
		legal := read(t, route.Queue)
		got, decodeErr := DecodeTaskMessage(legal.Body)
		if decodeErr != nil || got != message || !legal.Redelivered {
			t.Fatal("合法消息必须原样回队")
		}
		_ = legal.Ack(false)
	})
	t.Run("本地Worker并行数受限", func(t *testing.T) {
		topology, publisher, consumer := fixture(t)
		route, _ := topology.Route(TaskFetch)
		for i := 0; i < 4; i++ {
			m := message
			m.TaskID = fmt.Sprintf("video_parallel%02d", i)
			if publisher.Publish(ctx, TaskFetch, m) != nil {
				t.Fatal("测试发布失败")
			}
		}
		running, stop := context.WithCancel(ctx)
		defer stop()
		started := make(chan struct{}, 4)
		finished := make(chan struct{}, 4)
		release := make(chan struct{})
		done := make(chan error, 1)
		var active, maximum atomic.Int32
		handler := testTaskHandler(func(c context.Context, _ TaskStage, _ TaskMessage) (TaskDisposition, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				old := maximum.Load()
				if n <= old || maximum.CompareAndSwap(old, n) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-release:
				finished <- struct{}{}
				return TaskHandled, nil
			case <-c.Done():
				return 0, c.Err()
			}
		})
		go func() { done <- consumer.RunWorkers(running, TaskFetch, 2, handler) }()
		for i := 0; i < 2; i++ {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("两个Worker未进入处理")
			}
		}
		if q, err := ch.QueueInspect(route.Queue); err != nil || q.Messages != 2 {
			t.Fatal("两个Worker不能额外预取未处理任务")
		}
		close(release)
		for i := 0; i < 4; i++ {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("Worker未处理完测试消息")
			}
		}
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("取消后Worker未收口")
		}
		if maximum.Load() != 2 {
			t.Fatal("本地处理并行数必须严格等于配置上限")
		}
	})
}

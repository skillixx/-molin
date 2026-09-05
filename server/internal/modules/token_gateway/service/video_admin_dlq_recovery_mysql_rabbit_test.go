package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoDLQAckFaultHandler struct {
	inner        *videoDLQRecoveryCoordinator
	afterPrepare func(video.TaskDeadLetterDecision)
}

func (h *videoDLQAckFaultHandler) PrepareDeadLetterRecovery(ctx context.Context, stage video.TaskStage, message video.TaskMessage) (video.TaskDeadLetterDecision, error) {
	decision, err := h.inner.PrepareDeadLetterRecovery(ctx, stage, message)
	if err == nil && h.afterPrepare != nil {
		h.afterPrepare(decision)
	}
	return decision, err
}
func (h *videoDLQAckFaultHandler) CompleteDeadLetterRecovery(ctx context.Context, stage video.TaskStage, message video.TaskMessage) error {
	return h.inner.CompleteDeadLetterRecovery(ctx, stage, message)
}

type videoDLQDropConfirmConn struct {
	net.Conn
	pending []byte
	dropped *atomic.Bool
}

func (c *videoDLQDropConfirmConn) Read(out []byte) (int, error) {
	if len(c.pending) == 0 {
		header := make([]byte, 7)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		size := binary.BigEndian.Uint32(header[3:7])
		if size > 4*1024*1024 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, int(size)+1)
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return 0, err
		}
		if header[0] == 1 && size >= 4 && binary.BigEndian.Uint16(body[:2]) == 60 && binary.BigEndian.Uint16(body[2:4]) == 80 {
			c.dropped.Store(true)
			_ = c.Conn.Close()
			return 0, io.EOF
		}
		c.pending = append(header, body...)
	}
	n := copy(out, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func TestVideoG7AdminDLQRecoveryMySQLRabbitUnknownWindows(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_RUNTIME_ISOLATED") != "YES" {
		t.Skip("仅在G7隔离运行时执行MySQL和Rabbit联合故障")
	}
	password := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_RABBIT_PASSWORD")
	raw := fmt.Sprintf("amqp://vidg7fake:%s@rabbit:5672/", url.QueryEscape(password))
	baseOpen, err := video.NewTaskConnectionOpener(raw)
	if err != nil || password == "" {
		t.Fatal("隔离Rabbit配置无效")
	}
	db := openVideoG6MySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	adminConnection, err := baseOpen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConnection.Close()
	channel, err := adminConnection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()

	type fixture struct {
		topology  *video.TaskTopology
		publisher *video.TaskPublisher
		consumer  *video.TaskConsumer
		admin     *VideoAdminService
		command   VideoAdminDLQRecoveryCommand
		message   video.TaskMessage
		task      model.AIImageTask
	}
	newFixture := func(t *testing.T, consumeOpen video.TaskConnectionOpener) fixture {
		t.Helper()
		billing := newVideoG5ReservationFixture(t, db, "10")
		if _, err := billing.service.ReserveAndCreate(ctx, billing.command); err != nil {
			t.Fatal(err)
		}
		var task model.AIImageTask
		if err := db.Where("public_id=?", billing.command.TaskID).Take(&task).Error; err != nil {
			t.Fatal(err)
		}
		repo := repository.NewVideoOutboxRepository(db)
		for attempts := 0; attempts < 10; attempts++ {
			now := time.Now().UTC().Truncate(time.Second)
			claimed, err := repo.ClaimBatch(ctx, now, now.Add(-time.Minute), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(claimed) == 0 {
				break
			}
			for _, event := range claimed {
				if err := repo.MarkPublished(ctx, event.ID, *event.LockedAt, now); err != nil {
					t.Fatal(err)
				}
			}
		}
		verified := time.Now().UTC().Add(-time.Minute)
		actor := authmodel.User{ID: NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
		if err := db.Create(&actor).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:reconcile_manage'", actor.ID).Error; err != nil {
			t.Fatal(err)
		}
		topology, _ := video.NewTaskTopology(fmt.Sprintf("molin.video.dlq.%x", time.Now().UnixNano()))
		if err := topology.Declare(channel); err != nil {
			t.Fatal(err)
		}
		publisher, _ := video.NewTaskPublisher(topology, baseOpen, 5*time.Second)
		consumer, _ := video.NewTaskConsumer(topology, consumeOpen, publisher, 4, 10*time.Second)
		protector, _ := NewVideoAdminReasonProtector("g7-dlq-rabbit-v1", []byte("12345678901234567890123456789012"))
		admin, err := NewVideoAdminService(&VideoHTTPService{db: db, billing: billing.service}, 24, VideoAdminWriteOptions{ReasonProtector: protector, DLQConsumer: consumer})
		if err != nil {
			t.Fatal(err)
		}
		caller := VideoCaller{UserID: actor.ID, credential: &videoReadCredential{userID: actor.ID, expiresAt: time.Now().Add(time.Hour), revalidate: func(context.Context) error { return nil }}}
		command := VideoAdminDLQRecoveryCommand{Caller: caller, TaskID: task.PublicID, Stage: video.TaskSubmit, IdempotencyKey: fmt.Sprintf("g7-dlq-rabbit-%d", actor.ID), Reason: "已核对联合故障窗口", VersionNo: task.VersionNo}
		message := video.TaskMessage{TaskID: task.PublicID, RequestID: task.RequestID, Attempt: 1}
		if err := publisher.PublishDead(ctx, video.TaskSubmit, message); err != nil {
			t.Fatal(err)
		}
		reply, err := admin.RecoverDeadLetter(ctx, command)
		if err != nil || reply == nil || !reply.Pending {
			t.Fatalf("首次恢复必须只调度Outbox并保留DLQ: reply=%+v err=%v", reply, err)
		}
		return fixture{topology: topology, publisher: publisher, consumer: consumer, admin: admin, command: command, message: message, task: task}
	}
	wait := func(t *testing.T, queue string) amqp.Delivery {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			item, ok, err := channel.Get(queue, false)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				return item
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("未观察到预期Rabbit消息")
		return amqp.Delivery{}
	}
	runRelay := func(t *testing.T, publisher *video.TaskPublisher) (int, error) {
		t.Helper()
		transport, err := NewVideoOutboxPublisher(db, publisher)
		if err != nil {
			t.Fatal(err)
		}
		return NewOutboxWorker(repository.NewVideoOutboxRepository(db), transport).RunOnce(ctx, 10)
	}
	runUntilPublished := func(t *testing.T, publisher *video.TaskPublisher, requestID string) {
		t.Helper()
		for attempt := 0; attempt < 20; attempt++ {
			if _, err := runRelay(t, publisher); err != nil {
				t.Fatal(err)
			}
			var dispatch model.AIOutboxEvent
			if err := db.Where("aggregate_id=? AND event_type='video_dlq_recovery_dispatch'", requestID).Take(&dispatch).Error; err != nil {
				t.Fatal(err)
			}
			if dispatch.Status == model.AIOutboxPublished {
				return
			}
			if dispatch.Status == model.AIOutboxPending {
				if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", dispatch.ID).Update("next_retry_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Fatal("目标恢复dispatch未在有界轮次内发布")
	}

	t.Run("publisher_confirm丢失由Outbox保留", func(t *testing.T) {
		f := newFixture(t, baseOpen)
		dropped := &atomic.Bool{}
		dropOpen := func(ctx context.Context) (*amqp.Connection, error) {
			return amqp.DialConfig(raw, amqp.Config{Dial: func(network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return &videoDLQDropConfirmConn{Conn: conn, dropped: dropped}, nil
			}})
		}
		lostPublisher, _ := video.NewTaskPublisher(f.topology, dropOpen, 5*time.Second)
		published, err := runRelay(t, lostPublisher)
		if err != nil || published != 0 || !dropped.Load() {
			t.Fatalf("confirm丢失必须由Outbox保留重试: published=%d dropped=%v err=%v", published, dropped.Load(), err)
		}
		var dispatch model.AIOutboxEvent
		if err := db.Where("aggregate_id=? AND event_type='video_dlq_recovery_dispatch'", f.task.RequestID).Take(&dispatch).Error; err != nil || dispatch.Status == model.AIOutboxPublished || dispatch.RetryCount == 0 {
			t.Fatalf("未知发布不得冒称完成: %+v err=%v", dispatch, err)
		}
		if err := db.Model(&model.AIOutboxEvent{}).Where("id=? AND status='pending'", dispatch.ID).Update("next_retry_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
			t.Fatal(err)
		}
		runUntilPublished(t, f.publisher, f.task.RequestID)
		reply, err := f.admin.RecoverDeadLetter(ctx, f.command)
		if err != nil || reply == nil || !reply.Existing {
			t.Fatalf("confirm未知经Outbox重试后必须补完成和ACK: reply=%+v err=%v", reply, err)
		}
		route, _ := f.topology.Route(video.TaskSubmit)
		// 第一次消息已被Broker接收但确认丢失，Outbox重试形成至少一次副本；两条都保留原身份并由业务幂等处理。
		first, second := wait(t, route.Queue), wait(t, route.Queue)
		_ = first.Ack(false)
		_ = second.Ack(false)
	})

	t.Run("发布后完成审计失败可重试", func(t *testing.T) {
		f := newFixture(t, baseOpen)
		runUntilPublished(t, f.publisher, f.task.RequestID)
		hook := "video:g7:dlq:after-audit-failure"
		if err := db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "audit_logs" {
				tx.AddError(errors.New("合成完成审计失败"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.RecoverDeadLetter(ctx, f.command); err == nil {
			t.Fatal("完成审计失败不得ACK原DLQ")
		}
		db.Callback().Create().Remove(hook)
		reply, err := f.admin.RecoverDeadLetter(ctx, f.command)
		if err != nil || reply == nil || !reply.Existing {
			t.Fatalf("审计恢复后必须只补完成和ACK: reply=%+v err=%v", reply, err)
		}
		route, _ := f.topology.Route(video.TaskSubmit)
		work := wait(t, route.Queue)
		_ = work.Ack(false)
	})

	t.Run("完成审计后ACK未知只补ACK", func(t *testing.T) {
		var mu sync.Mutex
		var consumerConnection *amqp.Connection
		captureOpen := func(ctx context.Context) (*amqp.Connection, error) {
			conn, err := baseOpen(ctx)
			if err == nil {
				mu.Lock()
				consumerConnection = conn
				mu.Unlock()
			}
			return conn, err
		}
		f := newFixture(t, captureOpen)
		runUntilPublished(t, f.publisher, f.task.RequestID)
		inner := &videoDLQRecoveryCoordinator{service: f.admin, command: f.command, commandHash: videoBillingDigest(fmt.Sprintf("video-admin-recovery:%d:%s", f.command.Caller.UserID, f.command.IdempotencyKey)), reasonHMAC: f.admin.reasons.digest("reason", f.command.Reason)}
		fault := &videoDLQAckFaultHandler{inner: inner, afterPrepare: func(decision video.TaskDeadLetterDecision) {
			if decision == video.TaskDeadLetterAckExisting {
				mu.Lock()
				if consumerConnection != nil {
					_ = consumerConnection.CloseDeadline(time.Now())
				}
				mu.Unlock()
			}
		}}
		if err := f.consumer.RecoverDeadOne(ctx, video.TaskSubmit, fault); !errors.Is(err, video.ErrTaskAckUnknown) {
			t.Fatalf("ACK未知必须显式返回: %v", err)
		}
		normalConsumer, _ := video.NewTaskConsumer(f.topology, baseOpen, f.publisher, 4, 10*time.Second)
		f.admin.dlqConsumer = normalConsumer
		reply, err := f.admin.RecoverDeadLetter(ctx, f.command)
		if err != nil || reply == nil || !reply.Existing {
			t.Fatalf("ACK未知重放必须只补ACK: reply=%+v err=%v", reply, err)
		}
		route, _ := f.topology.Route(video.TaskSubmit)
		work := wait(t, route.Queue)
		_ = work.Ack(false)
		if q, err := channel.QueueInspect(route.Queue); err != nil || q.Messages != 0 {
			t.Fatal("ACK补偿不得形成第二条工作消息")
		}
	})
}

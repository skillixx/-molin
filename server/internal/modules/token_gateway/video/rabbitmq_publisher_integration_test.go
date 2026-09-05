package video

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// dropTaskConfirmConn仅用于测试，在真实Broker已确认持久化后丢弃basic.ack帧。
// 不Mock发布器内部逻辑，也不通过sleep猜测消息是否已经到达Broker。
type dropTaskConfirmConn struct {
	net.Conn
	pending []byte
	dropped *atomic.Bool
	holdAck bool
}

func (c *dropTaskConfirmConn) Read(out []byte) (int, error) {
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
		if header[0] == 1 && size >= 4 && binary.BigEndian.Uint16(body[0:2]) == 60 && binary.BigEndian.Uint16(body[2:4]) == 80 {
			c.dropped.Store(true)
			if c.holdAck {
				return c.Read(out)
			}
			_ = c.Conn.Close()
			return 0, io.EOF
		}
		c.pending = append(header, body...)
	}
	n := copy(out, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func TestVideoG7RabbitPublisherIsolated(t *testing.T) {
	raw := os.Getenv("MOLIN_VIDEO_G7_AMQP_TEST_URL")
	if raw == "" {
		t.Skip("仅在G7本机一次性RabbitMQ验收执行")
	}
	u, err := url.Parse(raw)
	if err != nil || os.Getenv("MOLIN_VIDEO_G7_RABBIT_ISOLATED") != "YES" || u.Scheme != "amqp" || u.Hostname() != "127.0.0.1" || u.Path != "/vid_g7" || u.User == nil {
		t.Fatal("发布器测试只允许授权的loopback隔离Broker")
	}
	makeOpener := func(drop *atomic.Bool, hold ...bool) TaskConnectionOpener {
		return func(ctx context.Context) (*amqp.Connection, error) {
			var stop func() bool
			connection, err := amqp.DialConfig(raw, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
				if drop != nil {
					return &dropTaskConfirmConn{Conn: conn, dropped: drop, holdAck: len(hold) > 0 && hold[0]}, nil
				}
				return conn, nil
			}})
			if stop != nil {
				stop()
			}
			return connection, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := makeOpener(nil)(ctx)
	if err != nil {
		t.Fatal("隔离Broker管理连接失败")
	}
	t.Cleanup(func() { _ = admin.CloseDeadline(time.Now().Add(time.Second)) })
	ch, err := admin.Channel()
	if err != nil {
		t.Fatal("隔离Broker通道失败")
	}
	topology, _ := NewTaskTopology(fmt.Sprintf("molin.video.pub.%x", time.Now().UnixNano()))
	if topology.Declare(ch) != nil {
		t.Fatal("发布器测试拓扑声明失败")
	}
	publisher, err := NewTaskPublisher(topology, makeOpener(nil), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message := TaskMessage{TaskID: "video_publisher01", RequestID: "req_publisher01"}
	read := func(queue string, expected TaskMessage) {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			delivery, ok, err := ch.Get(queue, false)
			if err != nil {
				t.Fatal("隔离消息读取失败")
			}
			if ok {
				got, err := DecodeTaskMessage(delivery.Body)
				if err != nil || got != expected || delivery.DeliveryMode != amqp.Persistent {
					t.Fatal("生产发布器必须保留原四字段及持久属性")
				}
				if delivery.Ack(false) != nil {
					t.Fatal("测试消息确认失败")
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("未收到生产发布器发送的消息")
	}
	for _, stage := range []TaskStage{TaskSubmit, TaskPoll, TaskFetch} {
		route, _ := topology.Route(stage)
		if publisher.Publish(ctx, stage, message) != nil {
			t.Fatal("合法发布必须收到确认")
		}
		read(route.Queue, message)
		if publisher.PublishDead(ctx, stage, message) != nil {
			t.Fatal("死信发布必须收到确认")
		}
		read(route.DeadQueue, message)
	}
	route, _ := topology.Route(TaskPoll)
	if publisher.PublishDelayed(ctx, TaskPoll, message, 0) != nil {
		t.Fatal("延迟发布必须收到确认")
	}
	read(route.Queue, message)
	if publisher.PublishDelayed(ctx, TaskPoll, message, 4) == nil {
		t.Fatal("未知延迟阶梯不得发布")
	}
	if ch.QueueUnbind(route.Queue, route.RoutingKey, route.WorkExchange, nil) != nil {
		t.Fatal("隔离解绑失败")
	}
	// 重复验证return和ack同时就绪时，不能随机把不可路由当成功。
	for i := 0; i < 20; i++ {
		if err := publisher.Publish(ctx, TaskPoll, message); !errors.Is(err, ErrTaskUnroutable) {
			t.Fatalf("不可路由必须稳定拒绝：%v", err)
		}
	}
	if ch.QueueBind(route.Queue, route.RoutingKey, route.WorkExchange, false, nil) != nil {
		t.Fatal("隔离绑定恢复失败")
	}
	dropped := &atomic.Bool{}
	uncertain, err := NewTaskPublisher(topology, makeOpener(dropped), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := uncertain.Publish(ctx, TaskPoll, message); !errors.Is(err, ErrTaskPublishUnknown) || !dropped.Load() {
		t.Fatal("真实确认丢失必须返回结果未知")
	}
	read(route.Queue, message)
	if queued, err := ch.QueueInspect(route.Queue); err != nil || queued.Messages != 0 {
		t.Fatal("发布器不能在确认丢失后自动重新投递")
	}
	// Broker已确认但客户端始终收不到确认时，超时只关闭本次独占连接。
	blocked := &atomic.Bool{}
	timed, err := NewTaskPublisher(topology, makeOpener(blocked, true), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := timed.Publish(ctx, TaskPoll, message); !errors.Is(err, ErrTaskPublishUnknown) || !blocked.Load() {
		t.Fatal("确认被阻断时必须超时返回未知")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("独占连接关闭不能无限阻塞发布调用")
	}
	read(route.Queue, message)

	// 以真实quorum队列的reject-publish上限产生basic.nack，而不是Mock确认通道。
	rejectTopology, _ := NewTaskTopology(fmt.Sprintf("molin.video.nack.%x", time.Now().UnixNano()))
	rejectRoute, _ := rejectTopology.Route(TaskSubmit)
	if ch.ExchangeDeclare(rejectRoute.WorkExchange, "direct", true, false, false, false, nil) != nil {
		t.Fatal("隔离拒绝拓扑交换机失败")
	}
	if _, err := ch.QueueDeclare(rejectRoute.Queue, true, false, false, false, amqp.Table{"x-queue-type": "quorum", "x-overflow": "reject-publish", "x-max-length": int32(1)}); err != nil {
		t.Fatal("隔离拒绝队列失败")
	}
	if ch.QueueBind(rejectRoute.Queue, rejectRoute.RoutingKey, rejectRoute.WorkExchange, false, nil) != nil {
		t.Fatal("隔离拒绝队列绑定失败")
	}
	rejectPublisher, err := NewTaskPublisher(rejectTopology, makeOpener(nil), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rejectPublisher.Publish(ctx, TaskSubmit, message) != nil {
		t.Fatal("首条消息必须进入空队列")
	}
	// quorum长度上限允许少量在途超额，不能假定第二条必定NACK；必须实际观察到拒绝。
	accepted := 1
	rejected := false
	for i := 0; i < 10; i++ {
		err := rejectPublisher.Publish(ctx, TaskSubmit, message)
		if errors.Is(err, ErrTaskPublishRejected) {
			rejected = true
			break
		}
		if err != nil {
			t.Fatalf("拒绝场景不能以未知结果代替NACK：%v", err)
		}
		accepted++
	}
	if !rejected {
		t.Fatal("未观察到真实Broker NACK，不能宣称拒绝路径通过")
	}
	for i := 0; i < accepted; i++ {
		read(rejectRoute.Queue, message)
	}
}

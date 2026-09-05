package video

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestVideoG7RabbitTopologyIsolated(t *testing.T) {
	raw := os.Getenv("MOLIN_VIDEO_G7_AMQP_TEST_URL")
	if raw == "" {
		t.Skip("仅在G7本机一次性RabbitMQ验收执行")
	}
	u, err := url.Parse(raw)
	if err != nil || os.Getenv("MOLIN_VIDEO_G7_RABBIT_ISOLATED") != "YES" || u.Scheme != "amqp" || u.Hostname() != "127.0.0.1" || u.Path != "/vid_g7" || u.User == nil {
		t.Fatal("RabbitMQ集成只允许明确授权的loopback隔离vhost")
	}
	connection, err := amqp.DialConfig(raw, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
		conn, err := net.DialTimeout(network, addr, 5*time.Second)
		if err == nil {
			err = conn.SetDeadline(time.Now().Add(300 * time.Second))
			if err != nil {
				conn.Close()
			}
		}
		return conn, err
	}})
	if err != nil {
		t.Fatal("隔离RabbitMQ连接失败")
	}
	t.Cleanup(func() { _ = connection.CloseDeadline(time.Now().Add(time.Second)) })
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal("隔离RabbitMQ通道不可用")
	}
	topology, err := NewTaskTopology(fmt.Sprintf("molin.video.g7.%x", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	if topology.Declare(channel) != nil || topology.Declare(channel) != nil {
		t.Fatal("真实Broker持久拓扑声明和重复声明必须成功")
	}
	if channel.Confirm(false) != nil {
		t.Fatal("测试发布确认模式失败")
	}
	confirmed := channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))
	publish := func(exchange, key string, message TaskMessage) {
		t.Helper()
		body, err := EncodeTaskMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if channel.PublishWithContext(ctx, exchange, key, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body}) != nil {
			t.Fatal("测试消息发布失败")
		}
		select {
		case confirmation, ok := <-confirmed:
			if !ok || !confirmation.Ack {
				t.Fatal("消息未被Broker确认")
			}
			select {
			case <-returned:
				t.Fatal("mandatory消息不可路由")
			default:
			}
		case <-returned:
			t.Fatal("测试消息不可路由")
		case <-ctx.Done():
			t.Fatal("测试消息确认超时")
		}
	}
	wait := func(queue string, expected TaskMessage, timeout time.Duration) amqp.Delivery {
		t.Helper()
		until := time.Now().Add(timeout)
		for time.Now().Before(until) {
			delivery, ok, err := channel.Get(queue, false)
			if err != nil {
				t.Fatal("隔离队列读取失败")
			}
			if ok {
				message, err := DecodeTaskMessage(delivery.Body)
				if err != nil || message != expected || delivery.DeliveryMode != amqp.Persistent {
					t.Fatal("经过Broker的任务引用必须完整且持久")
				}
				return delivery
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("未收到预期的隔离队列消息")
		return amqp.Delivery{}
	}
	for _, stage := range []TaskStage{TaskSubmit, TaskPoll, TaskFetch} {
		route, _ := topology.Route(stage)
		for _, delay := range route.Delays {
			if _, err := channel.QueueInspect(delay.Queue); err != nil {
				t.Fatal("四阶延迟队列必须存在")
			}
		}
		for _, input := range []string{"", "input_isolated01"} {
			message := TaskMessage{TaskID: "video_isolated01", RequestID: "req_isolated01", InputAssetID: input}
			publish(route.WorkExchange, route.RoutingKey, message)
			delivery := wait(route.Queue, message, 45*time.Second)
			if delivery.Nack(false, false) != nil {
				t.Fatal("测试失败消息拒绝操作失败")
			}
			dead := wait(route.DeadQueue, message, 45*time.Second)
			if dead.Ack(false) != nil {
				t.Fatal("测试死信确认失败")
			}
		}
	}
	route, _ := topology.Route(TaskPoll)
	message := TaskMessage{TaskID: "video_delayed001", RequestID: "req_delayed001", Attempt: 1}
	started := time.Now()
	publish(route.DelayExchange, route.Delays[0].RoutingKey, message)
	delivery := wait(route.Queue, message, 45*time.Second)
	if time.Since(started) < 1800*time.Millisecond {
		t.Fatal("延迟队列不能绕过2秒退避")
	}
	if delivery.Ack(false) != nil {
		t.Fatal("测试延迟消息确认失败")
	}
	// 暂时解除目标绑定，验证过期消息不会因为目标不可路由而被静默丢弃。
	if channel.QueueUnbind(route.Queue, route.RoutingKey, route.WorkExchange, nil) != nil {
		t.Fatal("隔离故障注入解绑失败")
	}
	message.Attempt = 2
	publish(route.DelayExchange, route.Delays[0].RoutingKey, message)
	time.Sleep(3 * time.Second)
	if channel.QueueBind(route.Queue, route.RoutingKey, route.WorkExchange, false, nil) != nil {
		t.Fatal("隔离故障注入恢复绑定失败")
	}
	// RabbitMQ至少一次死信内部默认每3分钟重试；只扩大故障恢复观察窗，不改正常退避。
	recovered := wait(route.Queue, message, 210*time.Second)
	if recovered.Ack(false) != nil {
		t.Fatal("恢复后消息确认失败")
	}
}

package video

import (
	"context"
	"errors"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrTaskBrokerUnavailable = errors.New("视频消息连接不可用")
	ErrTaskPublishUnknown    = errors.New("视频消息发布结果未知")
	ErrTaskUnroutable        = errors.New("视频消息无法路由")
	ErrTaskPublishRejected   = errors.New("视频消息被Broker拒绝")
)

// TaskConnectionOpener必须返回本次调用独占的连接并遵守ctx，不得返回共享消费连接。
// 凭据由受限运行时注入；发布器不读取环境变量、Provider Key或任意客户端URL。
type TaskConnectionOpener func(context.Context) (*amqp.Connection, error)

type TaskPublisher struct {
	topology *TaskTopology
	open     TaskConnectionOpener
	timeout  time.Duration
}

func NewTaskPublisher(topology *TaskTopology, open TaskConnectionOpener, timeout time.Duration) (*TaskPublisher, error) {
	if _, err := topology.Route(TaskSubmit); err != nil || open == nil || timeout <= 0 || timeout > 30*time.Second {
		return nil, ErrTaskBrokerUnavailable
	}
	copyTopology := *topology
	return &TaskPublisher{topology: &copyTopology, open: open, timeout: timeout}, nil
}

func (p *TaskPublisher) Publish(ctx context.Context, stage TaskStage, message TaskMessage) error {
	return p.publish(ctx, stage, message, "work", 0)
}

// PublishDelayed只允许G0冻结四阶延迟；原消息身份不变，attempt由持久化消费策略决定。
func (p *TaskPublisher) PublishDelayed(ctx context.Context, stage TaskStage, message TaskMessage, delayIndex int) error {
	return p.publish(ctx, stage, message, "delay", delayIndex)
}

// PublishDead供已持久化失败处置使用，不自动重建任务或回流Submit。
func (p *TaskPublisher) PublishDead(ctx context.Context, stage TaskStage, message TaskMessage) error {
	return p.publish(ctx, stage, message, "dead", 0)
}

func (p *TaskPublisher) publish(ctx context.Context, stage TaskStage, message TaskMessage, target string, delayIndex int) error {
	if p == nil || p.open == nil || ctx == nil {
		return ErrTaskBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := p.topology.Route(stage)
	if err != nil {
		return err
	}
	body, err := EncodeTaskMessage(message)
	if err != nil {
		return err
	}
	exchange, key := route.WorkExchange, route.RoutingKey
	switch target {
	case "delay":
		if delayIndex < 0 || delayIndex >= len(route.Delays) {
			return ErrTaskTopology
		}
		exchange, key = route.DelayExchange, route.Delays[delayIndex].RoutingKey
	case "dead":
		exchange = route.DeadExchange
	case "work":
	default:
		return ErrTaskTopology
	}
	bounded, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	connection, err := p.open(bounded)
	if err != nil || connection == nil {
		if connection != nil {
			_ = connection.CloseDeadline(time.Now())
		}
		return ErrTaskBrokerUnavailable
	}
	// PublishWithContext只检查调用前取消，不能中断进行中的IO，因此超时必须关闭独占连接。
	closed := make(chan struct{})
	stop := context.AfterFunc(bounded, func() { _ = connection.CloseDeadline(time.Now()); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
		_ = connection.CloseDeadline(time.Now().Add(time.Second))
	}()
	channel, err := connection.Channel()
	if err != nil {
		return ErrTaskBrokerUnavailable
	}
	if channel.Confirm(false) != nil {
		return ErrTaskBrokerUnavailable
	}
	confirmations := channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	returns := channel.NotifyReturn(make(chan amqp.Return, 1))
	if err := bounded.Err(); err != nil {
		return err
	}
	// 不在发布时重新声明拓扑，否则会掩盖不可路由故障和运维配置漂移。
	if channel.PublishWithContext(bounded, exchange, key, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body, Timestamp: time.Now().UTC()}) != nil {
		return ErrTaskPublishUnknown
	}
	select {
	case <-bounded.Done():
		return ErrTaskPublishUnknown
	case _, ok := <-returns:
		if !ok {
			return ErrTaskPublishUnknown
		}
		return ErrTaskUnroutable
	case confirmation, ok := <-confirmations:
		if !ok || confirmation.DeliveryTag != 1 {
			return ErrTaskPublishUnknown
		}
		// RabbitMQ在对应确认前发送return；即使select先选中ack，也须检查已经入缓冲的退回。
		select {
		case _, ok := <-returns:
			if !ok {
				return ErrTaskPublishUnknown
			}
			return ErrTaskUnroutable
		default:
		}
		if !confirmation.Ack {
			return ErrTaskPublishRejected
		}
		return nil
	}
}

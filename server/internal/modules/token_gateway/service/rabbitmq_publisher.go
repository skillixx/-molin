package service

import (
	"context"
	"errors"
	"net"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"

	"molin/server/internal/modules/token_gateway/model"
)

// RabbitMQPublisher 每次发布建立短连接，优先保证断线恢复语义简单可靠；吞吐优化留给后续连接池阶段。
type RabbitMQPublisher struct {
	url      string
	exchange string
}

func NewRabbitMQPublisher(url, exchange string) OutboxPublisher {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	if strings.TrimSpace(exchange) == "" {
		exchange = "molin.ai.billing"
	}
	return &RabbitMQPublisher{url: url, exchange: exchange}
}

func (p *RabbitMQPublisher) Publish(ctx context.Context, event model.AIOutboxEvent) error {
	if p == nil || p.url == "" {
		return errors.New("RabbitMQ 发布器未配置")
	}
	conn, err := amqp.DialConfig(p.url, amqp.Config{
		// 网络连接必须服从 Worker 的有限上下文，Broker 故障时不能无限阻塞整个扫描批次。
		Dial: func(network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	})
	if err != nil {
		return errors.New("RabbitMQ 连接失败")
	}
	defer conn.Close()
	// context 到期时主动关闭连接，打断 Channel 声明、绑定和 Confirm 等非 context AMQP RPC。
	stopCancelClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancelClose()
	channel, err := conn.Channel()
	if err != nil {
		return errors.New("RabbitMQ Channel 创建失败")
	}
	defer channel.Close()
	if err := channel.ExchangeDeclare(p.exchange, "topic", true, false, false, false, nil); err != nil {
		return errors.New("RabbitMQ Exchange 声明失败")
	}
	queueName := p.exchange + ".events"
	if _, err := channel.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return errors.New("RabbitMQ 持久队列声明失败")
	}
	if err := channel.QueueBind(queueName, "#", p.exchange, false, nil); err != nil {
		return errors.New("RabbitMQ 队列绑定失败")
	}
	// 只有收到 Broker confirm 后才能把数据库事件标记为已发布，连接中断时保留事件重试。
	if err := channel.Confirm(false); err != nil {
		return errors.New("RabbitMQ 发布确认启用失败")
	}
	returns := channel.NotifyReturn(make(chan amqp.Return, 1))
	confirmation, err := channel.PublishWithDeferredConfirmWithContext(ctx, p.exchange, event.EventType, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "application/json",
		MessageId:    event.EventID,
		Type:         event.EventType,
		Body:         append([]byte(nil), event.PayloadJSON...),
	})
	if err != nil {
		return errors.New("RabbitMQ 发布失败")
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil || !acked {
		return errors.New("RabbitMQ 发布未确认")
	}
	select {
	case <-returns:
		return errors.New("RabbitMQ 事件未路由")
	default:
	}
	return nil
}

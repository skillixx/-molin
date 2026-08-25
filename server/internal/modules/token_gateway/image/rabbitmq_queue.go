package image

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrImageQueueUnavailable = errors.New("图片任务队列不可用")

type ImageTaskQueueConfig struct {
	URL          string
	Exchange     string
	Queue        string
	RoutingKey   string
	DeadExchange string
	DeadQueue    string
	DeadRouting  string
}

type ImageTaskQueue struct {
	config ImageTaskQueueConfig
}

func (q *ImageTaskQueue) QueueDepths() (int, int, error) {
	connection, channel, err := q.open()
	if err != nil {
		return 0, 0, err
	}
	defer connection.Close()
	defer channel.Close()
	main, err := channel.QueueInspect(q.config.Queue)
	if err != nil {
		return 0, 0, ErrImageQueueUnavailable
	}
	dead, err := channel.QueueInspect(q.config.DeadQueue)
	if err != nil {
		return 0, 0, ErrImageQueueUnavailable
	}
	return main.Messages, dead.Messages, nil
}

type ImageTaskMessageHandler interface {
	HandleImageTask(ctx context.Context, requestID string) error
}

func NewImageTaskQueue(config ImageTaskQueueConfig) (*ImageTaskQueue, error) {
	values := []*string{&config.URL, &config.Exchange, &config.Queue, &config.RoutingKey, &config.DeadExchange, &config.DeadQueue, &config.DeadRouting}
	for _, value := range values {
		*value = strings.TrimSpace(*value)
		if *value == "" || strings.ContainsAny(*value, "\r\n") {
			return nil, ErrImageQueueUnavailable
		}
	}
	return &ImageTaskQueue{config: config}, nil
}

// EnsureTopology 幂等声明持久主队列和死信队列，失败时不吞掉错误或伪造可用状态。
func (q *ImageTaskQueue) EnsureTopology(ctx context.Context) error {
	connection, channel, err := q.open()
	if err != nil {
		return err
	}
	defer connection.Close()
	defer channel.Close()
	if err := declareImageTopology(channel, q.config); err != nil {
		return err
	}
	return ctx.Err()
}

// Publish 只发布request_id，不允许Prompt、Base64、对象地址或凭据进入RabbitMQ。
func (q *ImageTaskQueue) Publish(ctx context.Context, requestID string) error {
	if !safeRequestID.MatchString(requestID) {
		return ErrImageQueueUnavailable
	}
	connection, channel, err := q.open()
	if err != nil {
		return err
	}
	defer connection.Close()
	defer channel.Close()
	if err := declareImageTopology(channel, q.config); err != nil {
		return err
	}
	if err := channel.Confirm(false); err != nil {
		return ErrImageQueueUnavailable
	}
	confirmations := channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	returns := channel.NotifyReturn(make(chan amqp.Return, 1))
	body, _ := json.Marshal(map[string]string{"request_id": requestID})
	if err := channel.PublishWithContext(ctx, q.config.Exchange, q.config.RoutingKey, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body, Timestamp: time.Now().UTC(),
	}); err != nil {
		return ErrImageQueueUnavailable
	}
	select {
	case <-ctx.Done():
		return ErrImageQueueUnavailable
	case <-returns:
		return ErrImageQueueUnavailable
	case confirmation := <-confirmations:
		if !confirmation.Ack {
			return ErrImageQueueUnavailable
		}
		return nil
	}
}

// ConsumeOne 处理一条任务；处理失败时不重入主队列，直接进入DLQ等待补偿或人工检查。
func (q *ImageTaskQueue) ConsumeOne(ctx context.Context, handler ImageTaskMessageHandler) error {
	if handler == nil {
		return ErrImageQueueUnavailable
	}
	connection, channel, err := q.open()
	if err != nil {
		return err
	}
	defer connection.Close()
	defer channel.Close()
	if err := declareImageTopology(channel, q.config); err != nil {
		return err
	}
	if err := channel.Qos(1, 0, false); err != nil {
		return ErrImageQueueUnavailable
	}
	deliveries, err := channel.Consume(q.config.Queue, "", false, false, false, false, nil)
	if err != nil {
		return ErrImageQueueUnavailable
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case delivery, ok := <-deliveries:
		if !ok {
			return ErrImageQueueUnavailable
		}
		var payload map[string]string
		if json.Unmarshal(delivery.Body, &payload) != nil || len(payload) != 1 || !safeRequestID.MatchString(payload["request_id"]) {
			_ = delivery.Nack(false, false)
			return ErrImageQueueUnavailable
		}
		if err := handler.HandleImageTask(ctx, payload["request_id"]); err != nil {
			_ = delivery.Nack(false, false)
			return err
		}
		return delivery.Ack(false)
	}
}

func (q *ImageTaskQueue) open() (*amqp.Connection, *amqp.Channel, error) {
	if q == nil {
		return nil, nil, ErrImageQueueUnavailable
	}
	connection, err := amqp.Dial(q.config.URL)
	if err != nil {
		return nil, nil, ErrImageQueueUnavailable
	}
	channel, err := connection.Channel()
	if err != nil {
		connection.Close()
		return nil, nil, ErrImageQueueUnavailable
	}
	return connection, channel, nil
}

func declareImageTopology(channel *amqp.Channel, config ImageTaskQueueConfig) error {
	if err := channel.ExchangeDeclare(config.DeadExchange, "direct", true, false, false, false, nil); err != nil {
		return ErrImageQueueUnavailable
	}
	if _, err := channel.QueueDeclare(config.DeadQueue, true, false, false, false, nil); err != nil {
		return ErrImageQueueUnavailable
	}
	if err := channel.QueueBind(config.DeadQueue, config.DeadRouting, config.DeadExchange, false, nil); err != nil {
		return ErrImageQueueUnavailable
	}
	if err := channel.ExchangeDeclare(config.Exchange, "direct", true, false, false, false, nil); err != nil {
		return ErrImageQueueUnavailable
	}
	arguments := amqp.Table{"x-dead-letter-exchange": config.DeadExchange, "x-dead-letter-routing-key": config.DeadRouting}
	if _, err := channel.QueueDeclare(config.Queue, true, false, false, false, arguments); err != nil {
		return ErrImageQueueUnavailable
	}
	if err := channel.QueueBind(config.Queue, config.RoutingKey, config.Exchange, false, nil); err != nil {
		return ErrImageQueueUnavailable
	}
	return nil
}

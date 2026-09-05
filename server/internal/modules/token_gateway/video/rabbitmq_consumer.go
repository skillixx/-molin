package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrTaskHandlerUncertain = errors.New("视频任务处理未确认持久化")
var ErrTaskAckUnknown = errors.New("视频消息确认结果未知")
var ErrTaskRecoveryUncertain = errors.New("视频死信恢复结果需要人工核对")

type taskMessageInvalidError struct{ bodySHA256 string }

func (e taskMessageInvalidError) Error() string                 { return ErrTaskMessageInvalid.Error() }
func (e taskMessageInvalidError) Is(target error) bool          { return target == ErrTaskMessageInvalid }
func (e taskMessageInvalidError) TaskMessageBodySHA256() string { return e.bodySHA256 }

func invalidTaskDeliveryError(body []byte) error {
	digest := sha256.Sum256(body)
	return taskMessageInvalidError{bodySHA256: hex.EncodeToString(digest[:])}
}

// TaskMessageInvalidSHA256只为持久熔断审计返回低敏摘要，不暴露非法正文。
func TaskMessageInvalidSHA256(err error) (string, bool) {
	var invalid interface{ TaskMessageBodySHA256() string }
	if !errors.Is(err, ErrTaskMessageInvalid) || !errors.As(err, &invalid) || invalid.TaskMessageBodySHA256() == "" {
		return "", false
	}
	return invalid.TaskMessageBodySHA256(), true
}

type TaskDisposition uint8

const (
	TaskHandled TaskDisposition = iota + 1
	TaskRetry
	TaskReject
)

// TaskMessageHandler必须先复验原账本归属/fencing，再持久化结果后返回显式处置。
// TaskHandled表示本阶段结果和后续工作已可靠记录，不是仅完成内存计算。
type TaskMessageHandler interface {
	HandleTask(context.Context, TaskStage, TaskMessage) (TaskDisposition, error)
}

type TaskDeadLetterDecision uint8

const (
	TaskDeadLetterPublish TaskDeadLetterDecision = iota + 1
	TaskDeadLetterAckExisting
	TaskDeadLetterHold
)

// TaskDeadLetterRecoveryHandler必须先以原Task/Request、状态、attempt、权限和审计事实取得持久化恢复许可。
// Complete只在工作队列发布已确认后追加完成事实；任一步不确定都保留原DLQ消息。
type TaskDeadLetterRecoveryHandler interface {
	PrepareDeadLetterRecovery(context.Context, TaskStage, TaskMessage) (TaskDeadLetterDecision, error)
	CompleteDeadLetterRecovery(context.Context, TaskStage, TaskMessage) error
}

// TaskPoisonRecoveryHandler只接收正文摘要，不接收原始非法消息；授权与前后审计必须在ACK前持久化。
type TaskPoisonRecoveryHandler interface {
	AuthorizeTaskPoisonDiscard(context.Context, TaskStage, string) error
	CompleteTaskPoisonDiscard(context.Context, TaskStage, string) error
}

type TaskConsumer struct {
	topology       *TaskTopology
	open           TaskConnectionOpener
	publisher      *TaskPublisher
	maxAttempts    uint32
	handlerTimeout time.Duration
}

func NewTaskConsumer(topology *TaskTopology, open TaskConnectionOpener, publisher *TaskPublisher, maxAttempts uint32, handlerTimeout time.Duration) (*TaskConsumer, error) {
	if _, err := topology.Route(TaskSubmit); err != nil || open == nil || publisher == nil || publisher.topology == nil || publisher.topology.prefix != topology.prefix || maxAttempts == 0 || maxAttempts > 16 || handlerTimeout <= 0 || handlerTimeout > 60*time.Second {
		return nil, ErrTaskBrokerUnavailable
	}
	copyTopology := *topology
	return &TaskConsumer{topology: &copyTopology, open: open, publisher: publisher, maxAttempts: maxAttempts, handlerTimeout: handlerTimeout}, nil
}

// watchConsumerConnection把context取消落实到独占连接，停止时等待关闭回调结束。
func watchConsumerConnection(ctx context.Context, connection *amqp.Connection) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = connection.CloseDeadline(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}
}

func (c *TaskConsumer) subscribe(ctx context.Context, connection *amqp.Connection, queue string, prefetch int) (<-chan amqp.Delivery, error) {
	if prefetch < 1 || prefetch > 16 {
		return nil, ErrTaskBrokerUnavailable
	}
	setup, cancel := context.WithTimeout(ctx, 5*time.Second)
	stop := watchConsumerConnection(setup, connection)
	defer func() { stop(); cancel() }()
	channel, err := connection.Channel()
	if err != nil {
		return nil, ErrTaskBrokerUnavailable
	}
	// 每个本地Worker只预取一条，未完成持久化处理时不会在内存中积压其他任务。
	if channel.Qos(prefetch, 0, false) != nil {
		return nil, ErrTaskBrokerUnavailable
	}
	deliveries, err := channel.Consume(queue, "", false, false, false, false, nil)
	if err != nil || setup.Err() != nil {
		return nil, ErrTaskBrokerUnavailable
	}
	return deliveries, nil
}

// ConsumeOne直到一条消息完成ACK或失败才返回；连接关闭会自动恢复尚未ACK的原消息。
// 不删除队列、不自动消费DLQ，也不在发布结果未知时冒称原消息已经安全转移。
func (c *TaskConsumer) ConsumeOne(ctx context.Context, stage TaskStage, handler TaskMessageHandler) error {
	if c == nil || ctx == nil || handler == nil {
		return ErrTaskBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := c.topology.Route(stage)
	if err != nil {
		return err
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	connection, err := c.open(dialCtx)
	dialCancel()
	if err != nil || connection == nil {
		if connection != nil {
			_ = connection.CloseDeadline(time.Now())
		}
		return ErrTaskBrokerUnavailable
	}
	stop := watchConsumerConnection(ctx, connection)
	defer func() { stop(); _ = connection.CloseDeadline(time.Now().Add(time.Second)) }()
	deliveries, err := c.subscribe(ctx, connection, route.Queue, 1)
	if err != nil {
		return err
	}
	var delivery amqp.Delivery
	select {
	case <-ctx.Done():
		return ctx.Err()
	case item, ok := <-deliveries:
		if !ok {
			return ErrTaskBrokerUnavailable
		}
		delivery = item
	}
	message, err := DecodeTaskMessage(delivery.Body)
	if err != nil || delivery.ContentType != "application/json" || delivery.DeliveryMode != amqp.Persistent {
		// 不把原始错误正文复制到死信队列；保留未确认原消息，交由受控诊断处置。
		return invalidTaskDeliveryError(delivery.Body)
	}
	processing, cancel := context.WithTimeout(ctx, c.handlerTimeout)
	stopProcessing := watchConsumerConnection(processing, connection)
	defer func() { stopProcessing(); cancel() }()
	disposition := TaskReject
	if message.Attempt <= c.maxAttempts {
		disposition, err = callTaskHandler(processing, handler, stage, message)
		if err != nil || processing.Err() != nil {
			return ErrTaskHandlerUncertain
		}
	}
	switch disposition {
	case TaskHandled:
	case TaskRetry:
		if message.Attempt >= c.maxAttempts {
			err = c.publisher.PublishDead(processing, stage, message)
		} else {
			delayIndex := int(message.Attempt)
			if delayIndex > 3 {
				delayIndex = 3
			}
			message.Attempt++
			err = c.publisher.PublishDelayed(processing, stage, message, delayIndex)
		}
	case TaskReject:
		err = c.publisher.PublishDead(processing, stage, message)
	default:
		return ErrTaskHandlerUncertain
	}
	if err != nil {
		return err
	}
	if processing.Err() != nil {
		return ErrTaskHandlerUncertain
	}
	if delivery.Ack(false) != nil {
		return ErrTaskAckUnknown
	}
	return nil
}

// RecoverDeadOne只处理一个合法DLQ头消息；它保持原attempt，不允许重置后盲目重新Submit。
// 工作队列publisher confirm和完成审计都成功后才ACK原DLQ消息。
func (c *TaskConsumer) RecoverDeadOne(ctx context.Context, stage TaskStage, handler TaskDeadLetterRecoveryHandler) error {
	if c == nil || ctx == nil || handler == nil {
		return ErrTaskBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := c.topology.Route(stage)
	if err != nil {
		return err
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	connection, err := c.open(dialCtx)
	dialCancel()
	if err != nil || connection == nil {
		if connection != nil {
			_ = connection.CloseDeadline(time.Now())
		}
		return ErrTaskBrokerUnavailable
	}
	stop := watchConsumerConnection(ctx, connection)
	defer func() { stop(); _ = connection.CloseDeadline(time.Now().Add(time.Second)) }()
	deliveries, err := c.subscribe(ctx, connection, route.DeadQueue, 1)
	if err != nil {
		return err
	}
	var delivery amqp.Delivery
	select {
	case <-ctx.Done():
		return ctx.Err()
	case item, ok := <-deliveries:
		if !ok {
			return ErrTaskBrokerUnavailable
		}
		delivery = item
	}
	message, err := DecodeTaskMessage(delivery.Body)
	if err != nil || delivery.ContentType != "application/json" || delivery.DeliveryMode != amqp.Persistent {
		return ErrTaskMessageInvalid
	}
	processing, cancel := context.WithTimeout(ctx, c.handlerTimeout)
	stopProcessing := watchConsumerConnection(processing, connection)
	defer func() { stopProcessing(); cancel() }()
	decision, err := handler.PrepareDeadLetterRecovery(processing, stage, message)
	if err != nil || processing.Err() != nil {
		return ErrTaskRecoveryUncertain
	}
	switch decision {
	case TaskDeadLetterPublish:
		if err := c.publisher.Publish(processing, stage, message); err != nil {
			return err
		}
		if err := handler.CompleteDeadLetterRecovery(processing, stage, message); err != nil || processing.Err() != nil {
			return ErrTaskRecoveryUncertain
		}
	case TaskDeadLetterAckExisting:
	case TaskDeadLetterHold:
		return ErrTaskRecoveryUncertain
	default:
		return ErrTaskRecoveryUncertain
	}
	if delivery.Ack(false) != nil {
		return ErrTaskAckUnknown
	}
	return nil
}

// DiscardPoisonOne只允许丢弃格式、Content-Type或持久性不合规的队头消息。
// 合法业务消息永不进入此路径；审计完成后才ACK，正文只以SHA-256参与授权事实。
func (c *TaskConsumer) DiscardPoisonOne(ctx context.Context, stage TaskStage, handler TaskPoisonRecoveryHandler) error {
	if c == nil || ctx == nil || handler == nil {
		return ErrTaskBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := c.topology.Route(stage)
	if err != nil {
		return err
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	connection, err := c.open(dialCtx)
	dialCancel()
	if err != nil || connection == nil {
		if connection != nil {
			_ = connection.CloseDeadline(time.Now())
		}
		return ErrTaskBrokerUnavailable
	}
	stop := watchConsumerConnection(ctx, connection)
	defer func() { stop(); _ = connection.CloseDeadline(time.Now().Add(time.Second)) }()
	// 最多8个进程可能依次接到同一毒消息，每进程另一个Worker最多回队1条合法消息。
	// 管理处置因此预取9条：暂存最多8条合法重排消息，并保证还能读到其后的目标毒消息。
	const scanLimit = 9
	deliveries, err := c.subscribe(ctx, connection, route.Queue, scanLimit)
	if err != nil {
		return err
	}
	held := make([]amqp.Delivery, 0, scanLimit)
	completed := false
	defer func() {
		if completed {
			return
		}
		for i := range held {
			_ = held[i].Nack(false, true)
		}
	}()
	var delivery amqp.Delivery
	for len(held) < scanLimit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-deliveries:
			if !ok {
				return ErrTaskBrokerUnavailable
			}
			if _, decodeErr := DecodeTaskMessage(item.Body); decodeErr == nil && item.ContentType == "application/json" && item.DeliveryMode == amqp.Persistent {
				held = append(held, item)
				continue
			}
			delivery = item
		}
		break
	}
	if delivery.DeliveryTag == 0 {
		return ErrTaskRecoveryUncertain
	}
	digest := sha256.Sum256(delivery.Body)
	bodySHA256 := hex.EncodeToString(digest[:])
	processing, cancel := context.WithTimeout(ctx, c.handlerTimeout)
	stopProcessing := watchConsumerConnection(processing, connection)
	defer func() { stopProcessing(); cancel() }()
	if err := handler.AuthorizeTaskPoisonDiscard(processing, stage, bodySHA256); err != nil || processing.Err() != nil {
		return ErrTaskRecoveryUncertain
	}
	if err := handler.CompleteTaskPoisonDiscard(processing, stage, bodySHA256); err != nil || processing.Err() != nil {
		return ErrTaskRecoveryUncertain
	}
	if delivery.Ack(false) != nil {
		return ErrTaskAckUnknown
	}
	for i := range held {
		if held[i].Nack(false, true) != nil {
			return ErrTaskAckUnknown
		}
	}
	completed = true
	return nil
}

func callTaskHandler(ctx context.Context, handler TaskMessageHandler, stage TaskStage, message TaskMessage) (result TaskDisposition, err error) {
	defer func() {
		if recover() != nil {
			result = 0
			err = ErrTaskHandlerUncertain
		}
	}()
	result, err = handler.HandleTask(ctx, stage, message)
	if err != nil {
		return 0, ErrTaskHandlerUncertain
	}
	return result, nil
}

// RunWorkers只限制当前进程的消费并行度；跨进程和Provider硬上限仍必须由共享租约保证。
// 处理器须遵守context并使用持久化fencing；不以后台脱离协程伪造可安全中断。
func (c *TaskConsumer) RunWorkers(ctx context.Context, stage TaskStage, workers int, handler TaskMessageHandler) error {
	if c == nil || ctx == nil || handler == nil || workers < 1 || workers > 8 {
		return ErrTaskBrokerUnavailable
	}
	if _, err := c.topology.Route(stage); err != nil {
		return err
	}
	running, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				if err := c.ConsumeOne(running, stage, handler); err != nil {
					results <- err
					return
				}
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-results:
	}
	cancel()
	group.Wait()
	return err
}

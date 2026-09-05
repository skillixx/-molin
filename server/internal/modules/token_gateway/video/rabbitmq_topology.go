package video

import (
	"errors"
	"fmt"
	"regexp"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrTaskTopology = errors.New("视频消息拓扑不可用或配置无效")
var taskNamespace = regexp.MustCompile(`^molin\.video(?:\.[a-z0-9][a-z0-9._-]{0,47})?$`)

type TaskStage string

const (
	TaskSubmit TaskStage = "submit"
	TaskPoll   TaskStage = "poll"
	TaskFetch  TaskStage = "fetch"
)

// TaskTopology仅保存固定视频命名空间；不保存连接、密码或可调用Provider。
type TaskTopology struct{ prefix string }
type TaskDelayRoute struct {
	Queue, RoutingKey string
	TTLMillis         int32
}
type TaskRoute struct {
	WorkExchange, DelayExchange, DeadExchange string
	Queue, RoutingKey, DeadQueue              string
	Delays                                    []TaskDelayRoute
}

func NewTaskTopology(prefix string) (*TaskTopology, error) {
	if !taskNamespace.MatchString(prefix) {
		return nil, ErrTaskTopology
	}
	return &TaskTopology{prefix: prefix}, nil
}

// Route按处理阶段分流，T2V/I2V共用相同阶段队列；每次返回独立配置副本。
func (t *TaskTopology) Route(stage TaskStage) (TaskRoute, error) {
	if t == nil || !taskNamespace.MatchString(t.prefix) || (stage != TaskSubmit && stage != TaskPoll && stage != TaskFetch) {
		return TaskRoute{}, ErrTaskTopology
	}
	r := TaskRoute{WorkExchange: t.prefix + ".work", DelayExchange: t.prefix + ".delay", DeadExchange: t.prefix + ".dead", Queue: t.prefix + "." + string(stage), RoutingKey: string(stage), DeadQueue: t.prefix + ".dead." + string(stage)}
	for _, seconds := range []int32{2, 5, 10, 15} {
		key := fmt.Sprintf("%s.%ds", stage, seconds)
		r.Delays = append(r.Delays, TaskDelayRoute{Queue: t.prefix + ".delay." + key, RoutingKey: key, TTLMillis: seconds * 1000})
	}
	return r, nil
}

// Declare在调用方拥有的有界连接上幂等声明持久拓扑；任何错误均原样拒绝可用性判断。
// quorum源队列使用至少一次死信和reject-publish，不在目标不可用时静默丢掉延迟/失败任务。
// 单节点隔离验收只证明本机协议行为，不代表已经验证多节点Broker高可用。
func (t *TaskTopology) Declare(channel *amqp.Channel) error {
	if channel == nil || channel.IsClosed() {
		return ErrTaskTopology
	}
	for _, stage := range []TaskStage{TaskSubmit, TaskPoll, TaskFetch} {
		r, err := t.Route(stage)
		if err != nil {
			return err
		}
		for _, exchange := range []string{r.WorkExchange, r.DelayExchange, r.DeadExchange} {
			if channel.ExchangeDeclare(exchange, "direct", true, false, false, false, nil) != nil {
				return ErrTaskTopology
			}
		}
		if _, err := channel.QueueDeclare(r.DeadQueue, true, false, false, false, amqp.Table{"x-queue-type": "quorum", "x-overflow": "reject-publish"}); err != nil {
			return ErrTaskTopology
		}
		if channel.QueueBind(r.DeadQueue, r.RoutingKey, r.DeadExchange, false, nil) != nil {
			return ErrTaskTopology
		}
		mainArgs := amqp.Table{"x-queue-type": "quorum", "x-overflow": "reject-publish", "x-dead-letter-strategy": "at-least-once", "x-dead-letter-exchange": r.DeadExchange, "x-dead-letter-routing-key": r.RoutingKey}
		if _, err := channel.QueueDeclare(r.Queue, true, false, false, false, mainArgs); err != nil {
			return ErrTaskTopology
		}
		if channel.QueueBind(r.Queue, r.RoutingKey, r.WorkExchange, false, nil) != nil {
			return ErrTaskTopology
		}
		for _, delay := range r.Delays {
			args := amqp.Table{"x-queue-type": "quorum", "x-overflow": "reject-publish", "x-dead-letter-strategy": "at-least-once", "x-dead-letter-exchange": r.WorkExchange, "x-dead-letter-routing-key": r.RoutingKey, "x-message-ttl": delay.TTLMillis}
			if _, err := channel.QueueDeclare(delay.Queue, true, false, false, false, args); err != nil {
				return ErrTaskTopology
			}
			if channel.QueueBind(delay.Queue, delay.RoutingKey, r.DelayExchange, false, nil) != nil {
				return ErrTaskTopology
			}
		}
	}
	return nil
}

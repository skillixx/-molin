package video

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type testTaskHandler func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error)

func (f testTaskHandler) HandleTask(ctx context.Context, stage TaskStage, msg TaskMessage) (TaskDisposition, error) {
	return f(ctx, stage, msg)
}

func TestVideoG7ConsumerRejectsBeforeOpeningConnection(t *testing.T) {
	topology, _ := NewTaskTopology("molin.video.consumer")
	opens := 0
	open := func(context.Context) (*amqp.Connection, error) { opens++; return nil, nil }
	publisher, _ := NewTaskPublisher(topology, open, time.Second)
	consumer, err := NewTaskConsumer(topology, open, publisher, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	handler := testTaskHandler(func(context.Context, TaskStage, TaskMessage) (TaskDisposition, error) { return TaskHandled, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if consumer.ConsumeOne(ctx, TaskSubmit, handler) == nil {
		t.Fatal("已取消调用不能消费")
	}
	if consumer.ConsumeOne(context.Background(), TaskStage("invalid"), handler) == nil {
		t.Fatal("非法阶段不能消费")
	}
	if consumer.ConsumeOne(context.Background(), TaskSubmit, nil) == nil {
		t.Fatal("缺少持久化处理器不能消费")
	}
	if opens != 0 {
		t.Fatal("拒绝发生前不应打开连接")
	}
	if consumer.RunWorkers(context.Background(), TaskSubmit, 0, handler) == nil {
		t.Fatal("零Worker配置不能启动")
	}
	other, _ := NewTaskTopology("molin.video.other")
	if _, err := NewTaskConsumer(other, open, publisher, 1, time.Second); err == nil {
		t.Fatal("消费和重试发布必须使用同一命名空间")
	}
}

package video

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestVideoG7PublisherRejectsBeforeOpeningConnection(t *testing.T) {
	topology, _ := NewTaskTopology("molin.video.publisher")
	opens := 0
	publisher, err := NewTaskPublisher(topology, func(context.Context) (*amqp.Connection, error) {
		opens++
		return nil, errors.New("DO_NOT_ECHO_CONNECTION_SECRET")
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message := TaskMessage{TaskID: "video_test0001", RequestID: "req_test0001"}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if publisher.Publish(cancelled, TaskSubmit, message) == nil {
		t.Fatal("已取消请求不得发布")
	}
	if publisher.Publish(context.Background(), TaskStage("invalid"), message) == nil {
		t.Fatal("未知阶段不得打开连接")
	}
	if publisher.Publish(context.Background(), TaskSubmit, TaskMessage{}) == nil {
		t.Fatal("非法消息不得打开连接")
	}
	if opens != 0 {
		t.Fatal("校验失败前不应产生连接副作用")
	}
}

func TestVideoG7PublisherConnectionFailureIsLowSensitivity(t *testing.T) {
	topology, _ := NewTaskTopology("molin.video.publisher")
	for _, open := range []TaskConnectionOpener{
		func(context.Context) (*amqp.Connection, error) {
			return nil, errors.New("DO_NOT_ECHO_CONNECTION_SECRET")
		},
		func(context.Context) (*amqp.Connection, error) { return nil, nil },
	} {
		p, err := NewTaskPublisher(topology, open, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		err = p.Publish(context.Background(), TaskSubmit, TaskMessage{TaskID: "video_test0001", RequestID: "req_test0001"})
		if !errors.Is(err, ErrTaskBrokerUnavailable) || strings.Contains(err.Error(), "DO_NOT_ECHO") {
			t.Fatal("连接错误必须映射稳定低敏错误")
		}
	}
	if _, err := NewTaskPublisher(nil, func(context.Context) (*amqp.Connection, error) { return nil, nil }, time.Second); err == nil {
		t.Fatal("缺少拓扑不能构造发布器")
	}
	for _, timeout := range []time.Duration{0, -1, 31 * time.Second} {
		if _, err := NewTaskPublisher(topology, func(context.Context) (*amqp.Connection, error) { return nil, nil }, timeout); err == nil {
			t.Fatal("发布必须有有效上限")
		}
	}
}

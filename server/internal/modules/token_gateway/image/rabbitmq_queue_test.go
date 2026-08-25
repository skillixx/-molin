package image

import "testing"

func TestImageTaskQueueRejectsIncompleteTopology(t *testing.T) {
	if _, err := NewImageTaskQueue(ImageTaskQueueConfig{}); err == nil {
		t.Fatal("空队列配置必须拒绝")
	}
	if _, err := NewImageTaskQueue(ImageTaskQueueConfig{URL: "amqp://fake", Exchange: "image", Queue: "image", RoutingKey: "image", DeadExchange: "dead", DeadQueue: "dead", DeadRouting: "bad\nkey"}); err == nil {
		t.Fatal("含换行的routing key必须拒绝")
	}
}

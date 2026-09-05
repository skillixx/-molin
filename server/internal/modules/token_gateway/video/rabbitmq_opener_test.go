package video

import "testing"

func TestTaskConnectionOpenerRejectsUnsafeURL(t *testing.T) {
	for _, raw := range []string{"", "http://user:password@rabbit:5672/", "amqp://rabbit:5672/", "amqp://user@rabbit:5672/", "amqp://user:@rabbit:5672/", "amqp://user:password@/", "amqp://user:password@rabbit:5672/?secret=1", "amqp://user:password@rabbit:5672/#secret"} {
		if _, err := NewTaskConnectionOpener(raw); err == nil {
			t.Fatalf("不安全RabbitMQ URL必须拒绝: %q", raw)
		}
	}
	if opener, err := NewTaskConnectionOpener("amqp://video:fake-password@rabbit:5672/video"); err != nil || opener == nil {
		t.Fatalf("合法受限URL应构造成功: %v", err)
	}
}

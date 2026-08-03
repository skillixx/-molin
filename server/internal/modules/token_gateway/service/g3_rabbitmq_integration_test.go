package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestG3RabbitMQOutboxIntegration 由隔离脚本分两次执行，验证 Broker 停止和恢复后的事件收敛。
func TestG3RabbitMQOutboxIntegration(t *testing.T) {
	dsn := os.Getenv("G3_MYSQL_DSN")
	rabbitURL := os.Getenv("G3_RABBITMQ_URL")
	phase := os.Getenv("G3_RABBIT_PHASE")
	if dsn == "" || rabbitURL == "" || phase == "" {
		t.Skip("未配置 G3 RabbitMQ 隔离测试环境")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	repo := repository.NewG3OutboxRepository(db)
	worker := NewOutboxWorker(repo, NewRabbitMQPublisher(rabbitURL, "molin.ai.g3.test"))

	const eventID = "g3-rabbit-recovery"
	switch phase {
	case "down":
		// 前序计费测试的事件视为已正常处理，只留下本用例事件模拟停机窗口。
		if err := db.Model(&model.AIOutboxEvent{}).Where("event_id <> ?", eventID).
			Updates(map[string]interface{}{"status": model.AIOutboxPublished, "processed_at": time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"request_id": eventID, "billing_status": "held", "amount": "0.01000000", "currency": "CNY"})
		event := &model.AIOutboxEvent{
			EventID: eventID, AggregateType: "ai_request", AggregateID: eventID,
			EventType: "billing_held", PayloadJSON: payload, Status: model.AIOutboxPending,
			NextRetryAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		if err := db.Create(event).Error; err != nil {
			t.Fatal(err)
		}
		if count, err := worker.RunOnce(ctx, 10); err != nil || count != 0 {
			t.Fatalf("Broker 停止时事件不得伪发布: count=%d err=%v", count, err)
		}
		assertOutboxState(t, db, eventID, model.AIOutboxPending, 1)
	case "recover":
		if err := db.Model(&model.AIOutboxEvent{}).Where("event_id = ?", eventID).
			Update("next_retry_at", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)).Error; err != nil {
			t.Fatal(err)
		}
		if count, err := worker.RunOnce(ctx, 10); err != nil || count != 1 {
			t.Fatalf("Broker 恢复后事件应完成发布确认: count=%d err=%v", count, err)
		}
		assertOutboxState(t, db, eventID, model.AIOutboxPublished, 1)
		assertRabbitMessage(t, rabbitURL, "molin.ai.g3.test.events", eventID)
	default:
		t.Fatalf("未知测试阶段: %s", phase)
	}
}

func assertRabbitMessage(t *testing.T, rabbitURL, queueName, eventID string) {
	t.Helper()
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	channel, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	delivery, ok, err := channel.Get(queueName, true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || delivery.MessageId != eventID {
		t.Fatalf("未从持久队列读取目标事件: ok=%t message_id=%s", ok, delivery.MessageId)
	}
}

func assertOutboxState(t *testing.T, db *gorm.DB, eventID, wantStatus string, wantRetry uint32) {
	t.Helper()
	var event model.AIOutboxEvent
	if err := db.Where("event_id = ?", eventID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.Status != wantStatus || event.RetryCount != wantRetry {
		t.Fatalf("Outbox 状态不符: status=%s retry=%d", event.Status, event.RetryCount)
	}
}

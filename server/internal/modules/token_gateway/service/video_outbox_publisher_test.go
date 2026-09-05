package service

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

// TestVideoG7OutboxPublisherRequiresDependencies 验证缺少依赖不降级、不连接数据库或Broker，也不触发空指针。
func TestVideoG7OutboxPublisherRequiresDependencies(t *testing.T) {
	for _, db := range []*gorm.DB{nil, {}} {
		if p, err := NewVideoOutboxPublisher(db, nil); p != nil || !errors.Is(err, ErrVideoOutboxPublisherUnavailable) {
			t.Fatal("未装配Broker必须拒绝构造")
		}
	}
	for _, p := range []*VideoOutboxPublisher{nil, {}} {
		if err := p.Publish(context.Background(), model.AIOutboxEvent{}); !errors.Is(err, ErrVideoOutboxPublisherUnavailable) {
			t.Fatal("空发布器必须稳定失败关闭")
		}
	}
}

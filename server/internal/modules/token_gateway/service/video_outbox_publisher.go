package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

var ErrVideoOutboxPublisherUnavailable = errors.New("视频事件发布器未装配")

// VideoOutboxPublisher 连接原账本投影与生产RabbitMQ确认发布器；不持有Provider、钱包写入或任务执行能力。
type VideoOutboxPublisher struct {
	projector *VideoOutboxProjector
	transport *video.TaskPublisher
}

func NewVideoOutboxPublisher(db *gorm.DB, transport *video.TaskPublisher) (*VideoOutboxPublisher, error) {
	if db == nil || transport == nil {
		return nil, ErrVideoOutboxPublisherUnavailable
	}
	return &VideoOutboxPublisher{projector: NewVideoOutboxProjector(db), transport: transport}, nil
}

// Publish 只有原事件身份/租约/业务依据通过且Broker明确确认后才返回成功；共享OutboxWorker随后确认原行。
// 发布结果未知不在这里重试，原事件由共享租约流程恢复。submit队列是任务调度入口，不是Provider提交许可。
// 终态和迟到财务事件也只唤醒原Task；消费者必须重新读取状态，不能每收到一条消息就重新提交Provider。
func (p *VideoOutboxPublisher) Publish(ctx context.Context, event model.AIOutboxEvent) error {
	if p == nil || p.projector == nil || p.transport == nil {
		return ErrVideoOutboxPublisherUnavailable
	}
	if event.EventType == "video_dlq_recovery_dispatch" {
		stage, message, err := p.projector.ProjectDLQRecovery(ctx, event)
		if err != nil {
			return err
		}
		return p.transport.Publish(ctx, stage, message)
	}
	message, err := p.projector.Project(ctx, event)
	if err != nil {
		return err
	}
	return p.transport.Publish(ctx, video.TaskSubmit, message)
}

var _ OutboxPublisher = (*VideoOutboxPublisher)(nil)

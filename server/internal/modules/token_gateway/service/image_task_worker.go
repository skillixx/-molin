package service

import (
	"context"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

type ImageTaskWorker struct {
	queue      *imagegateway.ImageTaskQueue
	dispatcher *ImageTaskDispatcher
}

func NewImageTaskWorker(queue *imagegateway.ImageTaskQueue, dispatcher *ImageTaskDispatcher) (*ImageTaskWorker, error) {
	if queue == nil || dispatcher == nil {
		return nil, ErrImageAsyncUnavailable
	}
	return &ImageTaskWorker{queue: queue, dispatcher: dispatcher}, nil
}

// Start 串行消费图片任务；消费失败进入DLQ或等待下一轮，绝不在Worker层重调Provider。
func (w *ImageTaskWorker) Start(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		consumeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_ = w.queue.ConsumeOne(consumeCtx, w.dispatcher)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

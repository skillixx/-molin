package service

import (
	"context"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

type ImageQueueMetricsCollector struct {
	queue *imagegateway.ImageTaskQueue
}

func NewImageQueueMetricsCollector(queue *imagegateway.ImageTaskQueue) *ImageQueueMetricsCollector {
	return &ImageQueueMetricsCollector{queue: queue}
}

func (c *ImageQueueMetricsCollector) CollectImageQueueDepths(ctx context.Context) (map[string]uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mainDepth, deadDepth, err := c.queue.QueueDepths()
	if err != nil {
		return nil, err
	}
	return map[string]uint64{"main": uint64(mainDepth), "dead": uint64(deadDepth)}, nil
}

var _ ImageQueueGaugeCollector = (*ImageQueueMetricsCollector)(nil)

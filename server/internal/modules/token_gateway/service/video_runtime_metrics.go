package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoRuntimeMetricsCollector struct {
	db            *gorm.DB
	topology      *video.TaskTopology
	open          video.TaskConnectionOpener
	capacity      *RedisVideoCapacityStore
	runID         string
	runtimeHealth func() map[string]VideoRuntimeWorkerState
}

type VideoRuntimeWorkerState struct {
	Up            bool
	FailureCount  uint64
	LastSuccessAt time.Time
}

func (c *VideoRuntimeMetricsCollector) WithRuntimeHealth(provider func() map[string]VideoRuntimeWorkerState) *VideoRuntimeMetricsCollector {
	if c != nil {
		c.runtimeHealth = provider
	}
	return c
}

func NewVideoRuntimeMetricsCollector(db *gorm.DB, topology *video.TaskTopology, open video.TaskConnectionOpener, capacity *RedisVideoCapacityStore, runID string) (*VideoRuntimeMetricsCollector, error) {
	if db == nil || topology == nil || open == nil || capacity == nil || len(runID) != 40 {
		return nil, ErrVideoGovernanceUnavailable
	}
	return &VideoRuntimeMetricsCollector{db: db, topology: topology, open: open, capacity: capacity, runID: runID}, nil
}

func (c *VideoRuntimeMetricsCollector) CollectVideoGauges(ctx context.Context, now time.Time) (VideoGaugeSnapshot, error) {
	result := VideoGaugeSnapshot{Tasks: map[string]uint64{}, TaskOldestAgeSeconds: map[string]uint64{}, Queues: map[string]uint64{}, Capacity: map[string]uint64{}, ObjectObservations: map[string]uint64{}, ObjectCompensations: map[string]uint64{}, ComponentUp: map[string]uint64{}, ComponentFailures: map[string]uint64{}, ComponentLastSuccessAgeSeconds: map[string]uint64{}, ObjectBytes: map[string]uint64{}, CleanupFailures: map[string]uint64{}}
	if c == nil || ctx == nil || now.IsZero() {
		return result, ErrVideoGovernanceUnavailable
	}
	mysqlErr := func() error {
		taskRows := []struct {
			Operation, Status string
			Count             uint64
			Oldest            *time.Time
		}{}
		if err := c.db.WithContext(ctx).Table("ai_gateway_tasks").Select("operation,status,COUNT(*) AS count,MIN(updated_at) AS oldest").Where("capability='video.generate' AND operation IN ('text_to_video','image_to_video')").Group("operation,status").Scan(&taskRows).Error; err != nil {
			return err
		}
		for _, row := range taskRows {
			key := row.Operation + ":" + row.Status
			result.Tasks[key] = row.Count
			if row.Oldest != nil && now.After(*row.Oldest) {
				result.TaskOldestAgeSeconds[key] = uint64(now.Sub(*row.Oldest).Seconds())
			}
		}
		var holds struct {
			Count  uint64
			Amount decimal.Decimal
			Oldest *time.Time
		}
		if err := c.db.WithContext(ctx).Table("ai_requests").Select("COUNT(*) AS count,COALESCE(SUM(held_amount),0) AS amount,MIN(created_at) AS oldest").Where("capability='video.generate' AND billing_status IN ('held','settlement_pending')").Scan(&holds).Error; err != nil {
			return err
		}
		result.UnsettledHolds.Count, result.UnsettledHolds.Amount = holds.Count, holds.Amount
		if holds.Oldest != nil && now.After(*holds.Oldest) {
			result.UnsettledHolds.OldestAgeSeconds = uint64(now.Sub(*holds.Oldest).Seconds())
		}
		observationRows := []struct {
			Direction, Status string
			Count             uint64
		}{}
		if err := c.db.WithContext(ctx).Table("ai_video_object_reconciliation_observations").Select("direction,status,COUNT(*) AS count").Where("status IN ('observing','confirmed')").Group("direction,status").Scan(&observationRows).Error; err != nil {
			return err
		}
		for _, row := range observationRows {
			result.ObjectObservations[row.Direction+":"+row.Status] = row.Count
		}
		compensationRows := []struct {
			TaskType, Status string
			Count            uint64
		}{}
		if err := c.db.WithContext(ctx).Table("ai_compensation_tasks").Select("task_type,status,COUNT(*) AS count").Where("task_type IN ('video_object_missing_reconcile','video_orphan_cleanup') AND status<>'completed'").Group("task_type,status").Scan(&compensationRows).Error; err != nil {
			return err
		}
		for _, row := range compensationRows {
			result.ObjectCompensations[row.TaskType+":"+row.Status] = row.Count
		}
		var objectRows []struct {
			Bucket string
			Bytes  uint64
		}
		if err := c.db.WithContext(ctx).Raw(`SELECT bucket,SUM(size_bytes) AS bytes FROM (
SELECT bucket,size_bytes FROM ai_gateway_assets WHERE modality='video' AND media_deleted_at IS NULL
UNION ALL SELECT bucket,size_bytes FROM ai_gateway_input_assets WHERE lifecycle_state<>'deleted' AND bucket IS NOT NULL AND size_bytes IS NOT NULL
) objects GROUP BY bucket`).Scan(&objectRows).Error; err != nil {
			return err
		}
		for _, row := range objectRows {
			result.ObjectBytes[row.Bucket] = row.Bytes
		}
		var cleanupRows []struct {
			Kind  string
			Count uint64
		}
		if err := c.db.WithContext(ctx).Raw(`SELECT 'object_compensation' AS kind,COUNT(*) AS count FROM ai_compensation_tasks WHERE task_type IN ('video_object_missing_reconcile','video_orphan_cleanup') AND status IN ('retry','dead','manual_review')
UNION ALL SELECT 'input_cleanup',COUNT(*) FROM ai_video_upload_controls WHERE cleanup_pending=1 OR last_safe_error<>''
UNION ALL SELECT 'asset_delete',COUNT(*) FROM ai_gateway_assets WHERE modality='video' AND lifecycle_state='delete_failed'`).Scan(&cleanupRows).Error; err != nil {
			return err
		}
		for _, row := range cleanupRows {
			result.CleanupFailures[row.Kind] = row.Count
		}
		return nil
	}()
	if mysqlErr == nil {
		result.ComponentUp["mysql"] = 1
	}
	capacity, err := c.capacity.CollectCapacityCounts(ctx, c.runID)
	if err == nil {
		result.Capacity = capacity
		result.ComponentUp["redis"] = 1
	}
	rabbitErr := func() error {
		connection, err := c.open(ctx)
		if err != nil {
			return err
		}
		defer connection.CloseDeadline(time.Now().Add(time.Second))
		channel, err := connection.Channel()
		if err != nil {
			return err
		}
		defer channel.Close()
		for _, stage := range []video.TaskStage{video.TaskSubmit, video.TaskPoll, video.TaskFetch} {
			route, err := c.topology.Route(stage)
			if err != nil {
				return err
			}
			work, err := channel.QueueInspect(route.Queue)
			if err != nil {
				return err
			}
			dead, err := channel.QueueInspect(route.DeadQueue)
			if err != nil {
				return err
			}
			var delayed uint64
			for _, queue := range route.Delays {
				view, err := channel.QueueInspect(queue.Queue)
				if err != nil {
					return err
				}
				delayed += uint64(view.Messages)
			}
			name := string(stage)
			result.Queues[name+":work"], result.Queues[name+":delay"], result.Queues[name+":dead"] = uint64(work.Messages), delayed, uint64(dead.Messages)
		}
		return nil
	}()
	if rabbitErr == nil {
		result.ComponentUp["rabbitmq"] = 1
	}
	if c.runtimeHealth != nil {
		for name, state := range c.runtimeHealth() {
			if state.Up {
				result.ComponentUp[name] = 1
			}
			result.ComponentFailures[name] = state.FailureCount
			if !state.LastSuccessAt.IsZero() && now.After(state.LastSuccessAt) {
				result.ComponentLastSuccessAgeSeconds[name] = uint64(now.Sub(state.LastSuccessAt).Seconds())
			}
		}
	}
	return result, nil
}

var _ VideoGaugeCollector = (*VideoRuntimeMetricsCollector)(nil)

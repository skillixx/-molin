package jobs

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
	assetrepository "molin/server/internal/modules/asset/repository"
)

// ExpireAssetsJob 定时任务：将已到期资产标记为 expired。
// 每小时运行一次，每次最多处理 1000 条，并写 asset_events 日志。
type ExpireAssetsJob struct {
	assetRepo *assetrepository.AssetRepository
	eventRepo *assetrepository.EventRepository
	db        *gorm.DB
}

// NewExpireAssetsJob 创建定时任务实例。
func NewExpireAssetsJob(db *gorm.DB) *ExpireAssetsJob {
	return &ExpireAssetsJob{
		assetRepo: assetrepository.NewAssetRepository(db),
		eventRepo: assetrepository.NewEventRepository(db),
		db:        db,
	}
}

// Start 启动定时任务（每小时运行一次）。
// 外部调用：go jobs.NewExpireAssetsJob(db).Start(ctx)
func (j *ExpireAssetsJob) Start(ctx context.Context) {
	// 启动时立即执行一次，避免等待第一个 ticker
	j.run(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			j.run(ctx)
		case <-ctx.Done():
			log.Println("[ExpireAssetsJob] 定时任务已停止")
			return
		}
	}
}

// run 执行一次到期资产处理逻辑（每次最多处理 1000 条）。
func (j *ExpireAssetsJob) run(ctx context.Context) {
	// 批量查询并更新到期资产
	expired, err := j.assetRepo.BatchExpire(ctx, 1000)
	if err != nil {
		log.Printf("[ExpireAssetsJob] 批量到期处理失败: %v", err)
		return
	}
	if len(expired) == 0 {
		return
	}

	log.Printf("[ExpireAssetsJob] 处理了 %d 个到期资产", len(expired))

	// 为每个到期资产写 asset_events 日志
	before := "active"
	after := "expired"
	for _, a := range expired {
		assetID := a.ID
		userID := a.UserID
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       userID,
			EventType:    "expired",
			BeforeStatus: &before,
			AfterStatus:  &after,
		}
		if err := j.eventRepo.Create(ctx, event); err != nil {
			log.Printf("[ExpireAssetsJob] 写资产事件日志失败，assetID=%d: %v", assetID, err)
		}
	}
}

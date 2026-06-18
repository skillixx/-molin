package jobs

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	membershiprepo "molin/server/internal/modules/membership/repository"
)

// ExpireMembershipsJob 定时任务：将到期的 active 会员标记为 expired（C-FIX-5）。
// 每小时运行一次，每次最多处理 1000 条，与资产到期任务（ExpireAssetsJob）对齐，
// 避免 user_memberships.status 长期陈旧（管理端列表展示过期但仍标 active 的记录）。
type ExpireMembershipsJob struct {
	memberRepo *membershiprepo.UserMembershipRepository
}

// NewExpireMembershipsJob 创建定时任务实例。
func NewExpireMembershipsJob(db *gorm.DB) *ExpireMembershipsJob {
	return &ExpireMembershipsJob{
		memberRepo: membershiprepo.NewUserMembershipRepository(db),
	}
}

// Start 启动定时任务（每小时运行一次）。
// 外部调用：go jobs.NewExpireMembershipsJob(db).Start(ctx)
func (j *ExpireMembershipsJob) Start(ctx context.Context) {
	// 启动时立即执行一次，避免等待第一个 ticker
	j.run(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			j.run(ctx)
		case <-ctx.Done():
			log.Println("[ExpireMembershipsJob] 定时任务已停止")
			return
		}
	}
}

// run 执行一次到期会员处理逻辑（每次最多处理 1000 条）。
func (j *ExpireMembershipsJob) run(ctx context.Context) {
	affected, err := j.memberRepo.BatchExpire(ctx, 1000)
	if err != nil {
		log.Printf("[ExpireMembershipsJob] 批量到期处理失败: %v", err)
		return
	}
	if affected > 0 {
		log.Printf("[ExpireMembershipsJob] 处理了 %d 个到期会员", affected)
	}
}

package service

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
)

// 授权有效期仅在持锁事务内使用；零值表示当前已通过条件没有时间上界，不表示无需鉴权。
type videoAccessExpiry struct {
	until   time.Time
	expired error
}

func (p *videoAccessExpiry) require(end *time.Time, denied error) {
	if p != nil && end != nil && (p.until.IsZero() || end.Before(p.until)) {
		p.until, p.expired = *end, denied
	}
}

// 一条候选路径内父资产与权益是且关系，多条完整路径是或关系，禁止跨候选拼接期限。
func (p *videoAccessExpiry) alternatives(paths [][]*time.Time, denied error) {
	if p == nil || len(paths) == 0 {
		return
	}
	var latest time.Time
	for _, path := range paths {
		var end time.Time
		for _, d := range path {
			if d != nil && (end.IsZero() || d.Before(end)) {
				end = *d
			}
		}
		if end.IsZero() {
			return
		}
		if latest.IsZero() || end.After(latest) {
			latest = end
		}
	}
	p.require(&latest, denied)
}

func firstVideoAccessExpiry(values []*videoAccessExpiry) *videoAccessExpiry {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// 所有数据库与吊销查询结束后才比较时钟，外部删除的context也不能越过当前授权截止时间。
func (s *VideoHTTPService) currentVideoDeleteAuthority(ctx context.Context, tx *gorm.DB, caller VideoCaller, task *repository.VideoTaskRecord, owner repository.VideoOwner) (context.Context, context.CancelFunc, error) {
	if task.Operation == nil {
		return nil, nil, ErrVideoMediaProtected
	}
	proof := &videoAccessExpiry{}
	if err := s.access.authorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), []string{*task.Operation}, proof); err != nil {
		return nil, nil, err
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if caller.credential != nil {
		proof.require(&caller.credential.expiresAt, ErrVideoJWTInvalid)
	}
	if !proof.until.IsZero() && !proof.until.After(time.Now().UTC()) {
		return nil, nil, proof.expired
	}
	if proof.until.IsZero() {
		bounded, cancel := context.WithCancel(ctx)
		return bounded, cancel, nil
	}
	bounded, cancel := context.WithDeadline(ctx, proof.until)
	if err := bounded.Err(); err != nil {
		cancel()
		if !proof.until.After(time.Now().UTC()) {
			return nil, nil, proof.expired
		}
		return nil, nil, err
	}
	return bounded, cancel, nil
}

// 无外部删除的执行入口或重放末尾同样复核完整资格，但不把事务内证明缓存到后续请求。
func (s *VideoHTTPService) checkCurrentVideoDeleteAuthority(ctx context.Context, tx *gorm.DB, caller VideoCaller, task *repository.VideoTaskRecord, owner repository.VideoOwner) error {
	bounded, cancel, err := s.currentVideoDeleteAuthority(ctx, tx, caller, task, owner)
	if cancel != nil {
		defer cancel()
	}
	if err == nil {
		return bounded.Err()
	}
	return err
}

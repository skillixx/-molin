package service

import (
	"context"
	"errors"
	"time"
)

// CheckPermissionFresh复用IAM权威覆盖及角色/组权限，但每次直接查库，不依赖Redis缓存。
// 视频高成本准入和重放用它及时观察撤权；数据库故障必须返回错误，不得退化为部分授权。
// 调用者仍须另外验证账号、Project、Key、访问限制与业务权益，本方法不替代完整准入。
func (s *IAMService) CheckPermissionFresh(ctx context.Context, userID uint64, permission string) (bool, error) {
	allowed, _, err := s.checkPermissionFresh(ctx, userID, permission, false)
	return allowed, err
}

// 返回当前事务中授权路径的时限；allowed=false表示无授权，allowed=true且nil表示无时间上界。
// 临时allow不应遮蔽永久角色路径；禁止覆盖仍优先，调用者必须在外部副作用前再次比较时钟。
func (s *IAMService) CheckPermissionFreshWithExpiry(ctx context.Context, userID uint64, permission string) (bool, *time.Time, error) {
	return s.checkPermissionFresh(ctx, userID, permission, true)
}

func (s *IAMService) checkPermissionFresh(ctx context.Context, userID uint64, permission string, includeExpiry bool) (bool, *time.Time, error) {
	if s == nil || s.overrideRepo == nil || s.userRoleRepo == nil || s.permissionRepo == nil || s.groupRepo == nil || userID == 0 || permission == "" {
		return false, nil, errors.New("权限校验未就绪")
	}
	overrides, err := s.overrideRepo.FindByUser(ctx, userID)
	if err != nil {
		return false, nil, err
	}
	allowed := false
	var until *time.Time
	for _, override := range overrides {
		if override.PermissionCode != permission {
			continue
		}
		if override.ExpiresAt != nil && !override.ExpiresAt.After(time.Now()) {
			continue
		}
		// 即使异常历史数据同时含allow与deny，禁止仍先于允许，不依赖数据库返回顺序。
		if override.Effect == "deny" {
			return false, nil, nil
		}
		if override.Effect != "allow" {
			return false, nil, errors.New("权限覆盖状态无效")
		}
		// 同类allow是或关系；永久允许或最晚截止可以覆盖更短的临时允许。
		if !allowed || (until != nil && (override.ExpiresAt == nil || override.ExpiresAt.After(*until))) {
			until = override.ExpiresAt
		}
		allowed = true
	}
	if allowed && (!includeExpiry || until == nil) {
		return true, until, nil
	}
	codes, err := s.getAllUserPermCodes(ctx, userID)
	if err != nil {
		return false, nil, err
	}
	if evalPerms(codes, permission) {
		return true, nil, nil
	}
	return allowed, until, nil
}

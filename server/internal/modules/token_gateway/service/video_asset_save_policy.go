package service

import (
	"context"
	"errors"
	"strings"

	"github.com/shopspring/decimal"
	video "molin/server/internal/modules/token_gateway/video"
)

var ErrVideoSaveUnavailable = errors.New("视频转存配置或执行依赖不可用")
var ErrVideoSaveCapacity = errors.New("长期视频存储容量不足")
var ErrVideoSaveConflict = errors.New("保存命令或原视频状态冲突")

type VideoAssetSaveOptions struct {
	Store  VideoAssetSaveStore `json:"-"`
	Policy VideoAssetSavePolicy
}

// 转存外部边界必须保证目标不可变及同步删除确认，不能依靠客户端复制或普通Head错误回收容量。
type VideoAssetSaveStore interface {
	VideoMediaDeleteStore
	CopyImmutable(context.Context, video.VideoObjectRef, video.VideoObjectRef, string, uint64) (video.StoredVideoObject, error)
}

// 所有限额与许可必须显式来自已批准配置；没有默认商业存储额度或默认宽限期。
type VideoAssetSavePolicy struct {
	Version          string
	StorageProductID uint64
	EntitlementType  string
	QuotaUnit        string
	AllowedModels    []string
	MaxUserBytes     uint64
	MaxProjectBytes  uint64
	MaxGlobalBytes   uint64
	GlobalAlertBytes uint64
}

func (p VideoAssetSavePolicy) validate() error {
	if p.Version == "" || len(p.Version) > 128 || strings.TrimSpace(p.Version) != p.Version || p.StorageProductID == 0 || p.EntitlementType == "" || len(p.EntitlementType) > 64 || p.MaxUserBytes == 0 || p.MaxProjectBytes == 0 || p.MaxGlobalBytes == 0 || p.GlobalAlertBytes == 0 || p.GlobalAlertBytes > p.MaxGlobalBytes || len(p.AllowedModels) == 0 {
		return ErrVideoSaveUnavailable
	}
	if _, err := videoSaveQuota(1, p.QuotaUnit); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, code := range p.AllowedModels {
		if code == "" || strings.TrimSpace(code) != code || seen[code] {
			return ErrVideoSaveUnavailable
		}
		seen[code] = true
	}
	return nil
}

// user_entitlements使用DECIMAL(18,6)，向上取整避免小对象消耗被截成零；这不是模型收费。
func videoSaveQuota(bytes uint64, unit string) (decimal.Decimal, error) {
	if bytes == 0 {
		return decimal.Zero, ErrVideoSaveUnavailable
	}
	var divisor int64
	switch unit {
	case "bytes":
		divisor = 1
	case "GB":
		divisor = 1000000000
	case "GiB":
		divisor = 1073741824
	default:
		return decimal.Zero, ErrVideoSaveUnavailable
	}
	amount := decimal.NewFromUint64(bytes).Div(decimal.NewFromInt(divisor)).RoundCeil(6)
	if amount.GreaterThan(decimal.RequireFromString("999999999999.999999")) {
		return decimal.Zero, ErrVideoSaveUnavailable
	}
	return amount, nil
}

package service

import (
	"strings"

	"molin/server/internal/modules/token_gateway/model"
)

// validVideoOutboxTransportState 只验证运输记录的有限状态形状，不把待发送误当作唯一合法财务事实。
// 事件身份、金额、币种、操作、序号、钱包动作和事实全集仍必须由各业务校验器逐项核对。
func validVideoOutboxTransportState(event model.AIOutboxEvent) bool {
	if event.NextRetryAt.IsZero() || (event.LockedAt != nil && event.LockedAt.IsZero()) || (event.ProcessedAt != nil && event.ProcessedAt.IsZero()) {
		return false
	}
	if event.LastErrorClass != nil && strings.TrimSpace(*event.LastErrorClass) == "" {
		return false
	}
	switch event.Status {
	case model.AIOutboxPending:
		// 重排会把重试次数归零但保留最后令牌；不能据此推断从未领取，也不要求令牌早于墙钟。
		return event.ProcessedAt == nil
	case model.AIOutboxPublishing:
		// 再次领取可保留上一次失败分类，只有可靠发布完成才清空。
		return event.ProcessedAt == nil && event.LockedAt != nil
	case model.AIOutboxPublished:
		return event.ProcessedAt != nil && event.LockedAt == nil && event.LastErrorClass == nil
	case model.AIOutboxDead:
		// 视频发布失败必须保留最后租约；缺失高水位不能被当作可安全重排的正常记录。
		return event.ProcessedAt == nil && event.RetryCount > 0 && event.LastErrorClass != nil && event.LockedAt != nil
	default:
		return false
	}
}

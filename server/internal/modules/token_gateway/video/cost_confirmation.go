package video

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ProviderCostFactSHA256 统一确认事件与持久化成本的规范摘要；金额为固定精度字符串，不经过浮点。
// event_id独立参与唯一键，摘要仅覆盖可从Task与Usage重建的已确认事实。
func ProviderCostFactSHA256(requestID string, c ProviderCostConfirmation) string {
	raw, _ := json.Marshal([]string{requestID, c.ProviderCode, c.ProviderTaskID, c.Operation, string(c.Outcome), c.Quantity.StringFixed(10), c.UnitPrice.StringFixed(8), c.Amount.StringFixed(8), c.Currency})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

package dto

// SubmitReq 用户提交实名认证请求（身份证号不存库，仅在 service 层处理）。
type SubmitReq struct {
	RealName    string   `json:"real_name"`
	IDCardNo    string   `json:"id_card_no"` // 仅在内存中使用，不持久化明文
	Attachments []string `json:"attachments,omitempty"`
}

// SubmitResp 提交实名认证的响应，返回新建记录的 ID 和初始状态。
// BUG-07 修复：补充 status 字段，新提交记录状态固定为 pending。
type SubmitResp struct {
	ID     uint64 `json:"id"`
	Status string `json:"status"`
}

// ReviewReq 管理员审核请求。
type ReviewReq struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason,omitempty"`
}

// VerificationResp 认证状态响应（不返回身份证明文）。
type VerificationResp struct {
	ID             uint64  `json:"id"`
	UserID         uint64  `json:"user_id"`
	RealName       string  `json:"real_name"`
	IDCardNoMasked string  `json:"id_card_no_masked"`
	Status         string  `json:"status"`
	RejectReason   *string `json:"reject_reason,omitempty"`
	SubmittedAt    string  `json:"submitted_at"`   // ISO 8601 提交时间
	ReviewedAt     *string `json:"reviewed_at"`    // ISO 8601 审核时间（审核通过/拒绝后非 null）
}

package model

import "time"

// VideoBillingRequest 是共享ai_requests的G5写入视图；旧AIRequest不新增持久化字段，避免旧阶段隔离库缺列。
// 该类型不是第二套请求账本，TableName与原请求完全相同。
type VideoBillingRequest struct {
	AIRequest           `gorm:"embedded"`
	CommandKind         string `gorm:"column:command_kind;size:32" json:"-"`
	IntentKeyHash       string `gorm:"column:intent_key_hash;size:64" json:"-"`
	IntentVersion       string `gorm:"column:intent_version;size:32" json:"-"`
	RightsPolicyVersion string `gorm:"column:rights_policy_version;size:64" json:"-"`
}

func (VideoBillingRequest) TableName() string { return "ai_requests" }

// VideoUsageItem 在共享Usage行追加完整视频归属；旧AIUsageItem保持原列集合，兼容历史阶段隔离库。
type VideoUsageItem struct {
	AIUsageItem                   `gorm:"embedded"`
	TaskID                        uint64  `gorm:"column:task_id" json:"task_id"`
	QuoteID                       uint64  `gorm:"column:quote_id" json:"quote_id"`
	UserID                        uint64  `gorm:"column:user_id" json:"user_id"`
	ProjectID                     uint64  `gorm:"column:project_id" json:"project_id"`
	APIKeyID                      *uint64 `gorm:"column:api_key_id" json:"api_key_id"`
	LogicalModelCode              string  `gorm:"column:logical_model_code" json:"logical_model_code"`
	Capability                    string  `gorm:"column:capability" json:"capability"`
	EvidenceEventID               *uint64 `gorm:"column:evidence_event_id" json:"evidence_event_id,omitempty"`
	AdjustmentWalletTransactionID *uint64 `gorm:"column:adjustment_wallet_transaction_id" json:"adjustment_wallet_transaction_id,omitempty"`
}

func (VideoUsageItem) TableName() string { return "ai_usage_items" }

// VideoFinancialEvent 为共享追加式TaskEvent保存确认事实摘要，不保存原始Provider正文。
type VideoFinancialEvent struct {
	AIGatewayTaskEvent `gorm:"embedded"`
	FactSHA256         string `gorm:"column:fact_sha256" json:"fact_sha256"`
}

func (VideoFinancialEvent) TableName() string { return "ai_gateway_task_events" }

// VideoExecutionFailureEvent 将封闭失败原因与原执行CAS同事务追加，不改变旧阶段事件模型列集合。
type VideoExecutionFailureEvent struct {
	AIGatewayTaskEvent `gorm:"embedded"`
	FailureOrigin      string `gorm:"column:failure_origin" json:"failure_origin"`
}

func (VideoExecutionFailureEvent) TableName() string { return "ai_gateway_task_events" }

// VideoCompensationTask 扩展既有补偿表的视频写入视图，旧图片任务继续使用原模型和原列集合。
type VideoCompensationTask struct {
	AICompensationTask     `gorm:"embedded"`
	VersionNo              uint64     `gorm:"column:version_no" json:"version_no"`
	AttemptCount           uint32     `gorm:"column:attempt_count" json:"attempt_count"`
	LockedBy               *string    `gorm:"column:locked_by" json:"locked_by,omitempty"`
	LeaseMode              *string    `gorm:"column:lease_mode" json:"lease_mode,omitempty"`
	LastSafeErrorCode      *string    `gorm:"column:last_safe_error_code" json:"last_safe_error_code,omitempty"`
	CompletedAt            *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	ReviewMakerID          *uint64    `gorm:"column:review_maker_id" json:"review_maker_id,omitempty"`
	ReviewCheckerID        *uint64    `gorm:"column:review_checker_id" json:"review_checker_id,omitempty"`
	OriginErrorCode        string     `gorm:"column:origin_error_code" json:"origin_error_code"`
	InitialBillingStatus   string     `gorm:"column:initial_billing_status" json:"initial_billing_status"`
	DeliveryRequestVersion *uint64    `gorm:"column:delivery_request_version" json:"-"`
	DeliveryPreparedAt     *time.Time `gorm:"column:delivery_prepared_at" json:"-"`
}

func (VideoCompensationTask) TableName() string { return "ai_compensation_tasks" }

// VideoCompensationReviewEvent 保留每一次人工核对主体，后续租约不会覆盖已经发生的审核事实。
type VideoCompensationReviewEvent struct {
	AIGatewayTaskEvent `gorm:"embedded"`
	ReviewMakerID      uint64 `gorm:"column:review_maker_id" json:"review_maker_id"`
	ReviewCheckerID    uint64 `gorm:"column:review_checker_id" json:"review_checker_id"`
}

func (VideoCompensationReviewEvent) TableName() string { return "ai_gateway_task_events" }

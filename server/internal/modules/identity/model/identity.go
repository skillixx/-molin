package model

import "time"

// IdentityVerification 实名认证主表。
// 身份证号只存 HMAC-SHA256 hash 和脱敏值，严禁明文。
// masked 格式：前 6 位 + ******** + 后 4 位
type IdentityVerification struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement"`
	UserID             uint64     `gorm:"not null;index"`
	RealName           string     `gorm:"size:128;not null"`
	IDCardNoHash       string     `gorm:"size:128;not null;index"` // HMAC-SHA256(id_card_no, ID_CARD_HMAC_SECRET)
	IDCardNoMasked     string     `gorm:"size:64;not null"`        // 前 6 后 4，中间 ********
	VerificationType   string     `gorm:"size:32;default:id_card"`
	AttachmentsJSON    *string    `gorm:"type:json"`
	Status             string     `gorm:"size:32;default:pending;index"` // pending / verified / rejected
	RejectReason       *string    `gorm:"size:512"`
	SubmittedAt        time.Time  `gorm:"autoCreateTime"`
	VerifiedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// IdentityVerificationLog 审核操作日志。
type IdentityVerificationLog struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	VerificationID uint64    `gorm:"not null;index"`
	UserID         uint64    `gorm:"not null"`
	Action         string    `gorm:"size:32;not null"` // pending / verified / rejected
	OperatorID     *uint64
	Remark         *string   `gorm:"size:512"`
	CreatedAt      time.Time
}

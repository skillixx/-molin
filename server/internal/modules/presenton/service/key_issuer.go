package service

import (
	"context"

	authsvc "molin/server/internal/modules/auth/service"
)

// apiKeyIssuer 抽象 auth.APIKeyService.IssueKey，便于注入与测试。
// *authsvc.APIKeyService 直接满足该接口。
type apiKeyIssuer interface {
	IssueKey(ctx context.Context, in authsvc.IssueKeyInput) (string, authsvc.APIKeyView, error)
}

// SessionKeyIssuer 为 presenton 会话签发用户的 token_gateway 个人 key。
// v1：每次「打开」签发一把 postpaid sk（明文仅此一次，随票据落 Redis）；
// 烧的 token 走该用户钱包/额度。后续可加复用/吊销以避免 key 累积。
type SessionKeyIssuer struct {
	svc     apiKeyIssuer
	keyName string // sk 备注名，便于在用户 key 列表中识别来源
}

// NewSessionKeyIssuer 构造签发器。
func NewSessionKeyIssuer(svc apiKeyIssuer, keyName string) *SessionKeyIssuer {
	if keyName == "" {
		keyName = "presenton"
	}
	return &SessionKeyIssuer{svc: svc, keyName: keyName}
}

// IssueUserKey 为用户签发一把 postpaid sk，返回明文。
func (k *SessionKeyIssuer) IssueUserKey(ctx context.Context, userID uint64) (string, error) {
	plaintext, _, err := k.svc.IssueKey(ctx, authsvc.IssueKeyInput{
		UserID:      userID,
		Name:        k.keyName,
		BillingMode: "postpaid",
	})
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

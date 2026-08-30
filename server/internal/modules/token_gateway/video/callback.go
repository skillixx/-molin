package video

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

var ErrCallbackInvalid = errors.New("视频Provider回调无效")

const maxCallbackBodyBytes = 64 << 10

type CallbackEnvelope struct {
	ProviderCode    string
	ProviderTaskID  string
	ExternalEventID string
	Signature       string `json:"-"`
	Body            []byte `json:"-"`
}

type VerifiedCallback struct {
	ProviderCode    string
	ProviderTaskID  string
	ExternalEventID string
	BodySHA256      string
	Status          ProviderTaskStatus
	Progress        uint8
	RawBody         []byte `json:"-"`
}

type ProviderCallbackVerifier interface {
	Verify(ctx context.Context, envelope CallbackEnvelope) (VerifiedCallback, error)
}

// FakeProviderCallbackVerifier 使用HMAC提供确定性测试验签，不连接真实Provider。
type FakeProviderCallbackVerifier struct {
	secret []byte
}

func NewFakeProviderCallbackVerifier(secret []byte) *FakeProviderCallbackVerifier {
	return &FakeProviderCallbackVerifier{secret: append([]byte(nil), secret...)}
}

func (v *FakeProviderCallbackVerifier) Sign(body []byte) string {
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (v *FakeProviderCallbackVerifier) Verify(ctx context.Context, envelope CallbackEnvelope) (VerifiedCallback, error) {
	if err := ctx.Err(); err != nil {
		return VerifiedCallback{}, err
	}
	if v == nil || len(v.secret) < 8 || envelope.ProviderCode != "fake-native-async" ||
		!strings.HasPrefix(envelope.ProviderTaskID, "taskUUID-") || strings.Contains(envelope.ProviderTaskID, "://") ||
		strings.TrimSpace(envelope.ExternalEventID) == "" || len(envelope.Body) == 0 || len(envelope.Body) > maxCallbackBodyBytes {
		return VerifiedCallback{}, ErrCallbackInvalid
	}
	provided, err := hex.DecodeString(envelope.Signature)
	if err != nil {
		return VerifiedCallback{}, ErrCallbackInvalid
	}
	expected, _ := hex.DecodeString(v.Sign(envelope.Body))
	if !hmac.Equal(provided, expected) {
		return VerifiedCallback{}, ErrCallbackInvalid
	}
	var payload struct {
		Status   ProviderTaskStatus
		Progress uint8
	}
	if err := json.Unmarshal(envelope.Body, &payload); err != nil || !validCallbackStatus(payload.Status) || payload.Progress > 100 {
		return VerifiedCallback{}, ErrCallbackInvalid
	}
	digest := sha256.Sum256(envelope.Body)
	return VerifiedCallback{
		ProviderCode: envelope.ProviderCode, ProviderTaskID: envelope.ProviderTaskID,
		ExternalEventID: envelope.ExternalEventID, BodySHA256: hex.EncodeToString(digest[:]),
		Status: payload.Status, Progress: payload.Progress,
	}, nil
}

func validCallbackStatus(status ProviderTaskStatus) bool {
	switch status {
	case ProviderTaskProcessing, ProviderTaskSucceeded, ProviderTaskFailed, ProviderTaskCancelled, ProviderTaskUnknown:
		return true
	default:
		return false
	}
}

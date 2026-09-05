package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// VideoCapacityNonceKey只在受限进程内持有独立容量密钥，用于多进程稳定重建同一内部能力。
type VideoCapacityNonceKey struct{ secret [32]byte }

func NewVideoCapacityNonceKey(secret []byte) (*VideoCapacityNonceKey, error) {
	if len(secret) != 32 {
		return nil, ErrVideoCapacityUnavailable
	}
	key := &VideoCapacityNonceKey{}
	copy(key.secret[:], secret)
	return key, nil
}

func (VideoCapacityNonceKey) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}
func (VideoCapacityNonceKey) String() string   { return "[video capacity nonce key]" }
func (VideoCapacityNonceKey) GoString() string { return "[video capacity nonce key]" }

// Attempt绑定恢复代次和完整规范身份；不同进程持有同一外部密钥时得到相同nonce，但不会持久化密钥。
func (k *VideoCapacityNonceKey) Attempt(epoch uint64, identity VideoCapacityIdentity) (*VideoCapacityAttempt, error) {
	if k == nil || epoch == 0 {
		return nil, ErrVideoCapacityUnavailable
	}
	canonical, err := canonicalVideoCapacityIdentity(identity)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, k.secret[:])
	_, _ = mac.Write([]byte("molin-video-capacity-nonce-v1\x00"))
	_, _ = mac.Write([]byte(strconv.FormatUint(epoch, 10)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(canonical))
	return &VideoCapacityAttempt{identity: canonical, task: identity.TaskID, nonce: hex.EncodeToString(mac.Sum(nil))}, nil
}

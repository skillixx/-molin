package idgen

import (
	"crypto/rand"
	"encoding/hex"
)

func NewRequestID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "req_" + hex.EncodeToString(buf)
}

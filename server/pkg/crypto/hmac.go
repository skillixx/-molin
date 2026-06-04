package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HMAC256 用 key 对 data 计算 HMAC-SHA256，返回十六进制字符串。
// 用途：身份证号哈希、Refresh Token 哈希。
func HMAC256(data, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

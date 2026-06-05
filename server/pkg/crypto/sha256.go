package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256Hex 对字符串计算 SHA-256，返回十六进制字符串（64 字符）。
// 适用场景：短时效 OTP 验证码存库前的单向哈希。
// 注意：身份证号等需防彩虹表攻击的字段必须使用 HMAC256，而非此函数。
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

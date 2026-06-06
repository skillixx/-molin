package idgen

import (
	"crypto/rand"
	"math/big"
	"time"
)

// orderChars 订单号随机部分字符集（大写字母+数字）。
const orderChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateOrderNo 生成订单号，格式：ORD + YYYYMMDD + 8 位随机大写字母数字。
// 示例：ORD202406041A3B9C2F
func GenerateOrderNo() string {
	date := time.Now().Format("20060102")
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(orderChars))))
		b[i] = orderChars[n.Int64()]
	}
	return "ORD" + date + string(b)
}

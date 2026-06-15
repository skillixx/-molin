package service

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"strconv"
	"time"
)

// wechatTimestampWindow 微信回调时间窗（防重放），允许时间戳与当前时间相差 ±5 分钟。
const wechatTimestampWindow = 5 * time.Minute

// WechatVerifier 微信支付 APIv3 签名校验器。
// 验签使用微信支付平台证书公钥（RSA-SHA256，PKCS#1 v1.5）。
type WechatVerifier struct {
	// platformPubKey 微信支付平台证书公钥；为 nil 表示未配置（fail-closed，拒绝所有回调）。
	platformPubKey *rsa.PublicKey
}

// NewWechatVerifier 创建微信签名校验器实例。
// platformPubKeyPEM 为微信支付平台证书公钥，PEM 内容（"-----BEGIN PUBLIC KEY-----" 文本，
// 由 route.go 从环境变量 WECHAT_PLATFORM_PUBLIC_KEY 读取并支持「PEM 文本或文件路径」二选一）。
// 解析失败或为空时 platformPubKey 保持 nil —— Verify 将 fail-closed 拒绝所有回调。
func NewWechatVerifier(platformPubKeyPEM string) *WechatVerifier {
	v := &WechatVerifier{}
	if platformPubKeyPEM == "" {
		return v
	}
	pub, err := parseRSAPublicKey(platformPubKeyPEM)
	if err != nil {
		// 解析失败不放行：保持 nil，Verify 时 fail-closed。
		return v
	}
	v.platformPubKey = pub
	return v
}

// Verify 校验微信支付 APIv3 回调签名。
//
// 验签流程：
//  1. 校验请求头 Wechatpay-Signature / Wechatpay-Timestamp / Wechatpay-Nonce 必须存在（缺失即拒绝）。
//  2. 对 Wechatpay-Timestamp 做 ±5 分钟时间窗校验，防重放。
//  3. 构造签名串：timestamp + "\n" + nonce + "\n" + body + "\n"。
//  4. 用微信平台证书公钥对 Base64 解码后的签名做 RSA-SHA256（PKCS#1 v1.5）验签。
//
// fail-closed：平台公钥未配置（nil）时直接拒绝——资金回调无法验签必须拒绝，绝不放行。
func (v *WechatVerifier) Verify(rawBody []byte, header http.Header) error {
	signature := header.Get("Wechatpay-Signature")
	timestamp := header.Get("Wechatpay-Timestamp")
	nonce := header.Get("Wechatpay-Nonce")

	// 1. 校验签名头存在性。
	if signature == "" || timestamp == "" || nonce == "" {
		return ErrMissingSignature
	}

	// fail-closed：未配置平台公钥则一律拒绝（不可验签 = 拒绝）。
	if v.platformPubKey == nil {
		return ErrInvalidSignature
	}

	// 2. 时间窗校验，防重放。
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	diff := time.Since(time.Unix(ts, 0))
	if diff < -wechatTimestampWindow || diff > wechatTimestampWindow {
		return ErrInvalidSignature
	}

	// 3. 构造签名串：timestamp\nnonce\nbody\n。
	signStr := timestamp + "\n" + nonce + "\n" + string(rawBody) + "\n"

	// 4. RSA-SHA256（PKCS#1 v1.5）验签。
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return ErrInvalidSignature
	}
	digest := sha256.Sum256([]byte(signStr))
	if err := rsa.VerifyPKCS1v15(v.platformPubKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

// parseRSAPublicKey 解析 PEM 编码的 RSA 公钥，支持 PKIX（"PUBLIC KEY"）与 PKCS#1（"RSA PUBLIC KEY"）两种格式。
func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, ErrInvalidSignature
	}
	// 优先按 PKIX 解析。
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, ErrInvalidSignature
	}
	// 回退按 PKCS#1 解析。
	if rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return rsaPub, nil
	}
	return nil, ErrInvalidSignature
}

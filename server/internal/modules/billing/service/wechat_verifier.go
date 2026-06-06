package service

import "net/http"

// WechatVerifier 微信支付 APIv3 签名校验器。
type WechatVerifier struct{}

// NewWechatVerifier 创建微信签名校验器实例。
func NewWechatVerifier() *WechatVerifier {
	return &WechatVerifier{}
}

// Verify 校验微信支付回调签名（Week 2 改进版桩）。
// 校验请求头必须包含 Wechatpay-Signature / Wechatpay-Timestamp / Wechatpay-Nonce，
// 缺失任一则拒绝请求，防止完全空请求的伪造攻击。
// TODO: Week 3 接入真实微信 APIv3 RSA-SHA256 签名校验（需配置商户证书和 API v3 密钥）。
func (v *WechatVerifier) Verify(rawBody []byte, header http.Header) error {
	if header.Get("Wechatpay-Signature") == "" ||
		header.Get("Wechatpay-Timestamp") == "" ||
		header.Get("Wechatpay-Nonce") == "" {
		return ErrMissingSignature
	}
	// TODO: 实现微信 APIv3 真实签名校验
	// 1. 构造签名串：timestamp + "\n" + nonce + "\n" + body + "\n"
	// 2. 使用微信平台公钥（RSA-SHA256）验证 Wechatpay-Signature
	return nil
}

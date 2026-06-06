package service

import (
	"encoding/json"
	"net/http"
)

// AlipayVerifier 支付宝 RSA2 签名校验器。
type AlipayVerifier struct{}

// NewAlipayVerifier 创建支付宝签名校验器实例。
func NewAlipayVerifier() *AlipayVerifier {
	return &AlipayVerifier{}
}

// Verify 校验支付宝回调签名（Week 2 改进版桩）。
// 校验 Body 中必须包含 sign 字段，缺失则拒绝请求，防止完全空请求的伪造攻击。
// TODO: Week 3 接入真实支付宝 RSA2 签名校验（需配置支付宝公钥和应用私钥）。
func (v *AlipayVerifier) Verify(rawBody []byte, header http.Header) error {
	// 解析 JSON body，检查 sign 字段是否存在
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return ErrMissingSignature
	}
	signVal, ok := body["sign"]
	if !ok || signVal == nil || signVal == "" {
		return ErrMissingSignature
	}
	// TODO: 实现支付宝 RSA2 真实签名校验
	// 1. 按字母顺序拼接参数串（排除 sign 和 sign_type）
	// 2. 使用支付宝公钥（RSA2-SHA256）验证 sign
	return nil
}

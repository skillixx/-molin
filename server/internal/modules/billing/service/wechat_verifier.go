package service

// WechatVerifier 微信支付 APIv3 签名校验器。
type WechatVerifier struct{}

// NewWechatVerifier 创建微信签名校验器实例。
func NewWechatVerifier() *WechatVerifier {
	return &WechatVerifier{}
}

// Verify 校验微信支付回调签名。
// TODO: Week 3 接入真实微信 APIv3 签名校验逻辑（需要配置商户证书和 API v3 密钥）。
// 当前为桩实现，生产部署前必须替换为真实校验。
func (v *WechatVerifier) Verify(rawBody []byte) error {
	// TODO: 实现微信 APIv3 签名校验
	// 1. 从请求头获取 Wechatpay-Timestamp, Wechatpay-Nonce, Wechatpay-Signature
	// 2. 构造签名串：timestamp + "\n" + nonce + "\n" + body + "\n"
	// 3. 使用微信平台公钥（RSA-SHA256）验证签名
	return nil
}

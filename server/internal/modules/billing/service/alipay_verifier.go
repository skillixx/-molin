package service

// AlipayVerifier 支付宝 RSA2 签名校验器。
type AlipayVerifier struct{}

// NewAlipayVerifier 创建支付宝签名校验器实例。
func NewAlipayVerifier() *AlipayVerifier {
	return &AlipayVerifier{}
}

// Verify 校验支付宝回调签名。
// TODO: Week 3 接入真实支付宝 RSA2 签名校验逻辑（需要配置支付宝公钥和应用私钥）。
// 当前为桩实现，生产部署前必须替换为真实校验。
func (v *AlipayVerifier) Verify(rawBody []byte) error {
	// TODO: 实现支付宝 RSA2 签名校验
	// 1. 解析 notify_id, sign, sign_type 等参数
	// 2. 按字母顺序拼接参数串（排除 sign 和 sign_type）
	// 3. 使用支付宝公钥（RSA2-SHA256）验证 sign
	return nil
}

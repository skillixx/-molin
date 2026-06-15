package service

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// AlipayVerifier 支付宝 RSA2 签名校验器。
// 验签使用支付宝公钥（RSA-SHA256，即 RSA2）。
type AlipayVerifier struct {
	// publicKey 支付宝公钥；为 nil 表示未配置（fail-closed，拒绝所有回调）。
	publicKey *rsa.PublicKey
}

// NewAlipayVerifier 创建支付宝签名校验器实例。
// publicKeyPEM 为支付宝公钥，PEM 内容（"-----BEGIN PUBLIC KEY-----" 文本，
// 由 route.go 从环境变量 ALIPAY_PUBLIC_KEY 读取并支持「PEM 文本或文件路径」二选一）。
// 解析失败或为空时 publicKey 保持 nil —— Verify 将 fail-closed 拒绝所有回调。
func NewAlipayVerifier(publicKeyPEM string) *AlipayVerifier {
	v := &AlipayVerifier{}
	if publicKeyPEM == "" {
		return v
	}
	pub, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return v
	}
	v.publicKey = pub
	return v
}

// Verify 校验支付宝 RSA2 回调签名。
//
// 验签流程：
//  1. 解析回调报文，提取 sign 字段（缺失即拒绝）。
//  2. 取除 sign / sign_type 外的全部参数，按 key 字母序拼接为 k1=v1&k2=v2...。
//  3. 用支付宝公钥对 Base64 解码后的 sign 做 RSA-SHA256（RSA2）验签。
//
// fail-closed：支付宝公钥未配置（nil）时直接拒绝——资金回调无法验签必须拒绝，绝不放行。
func (v *AlipayVerifier) Verify(rawBody []byte, header http.Header) error {
	// 1. 解析报文。支付宝回调以 application/json 形式投递时为对象；
	//    本实现按 JSON 对象解析（与项目其它解析逻辑一致）。
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return ErrMissingSignature
	}
	signVal, ok := body["sign"]
	signStr, _ := signVal.(string)
	if !ok || signStr == "" {
		return ErrMissingSignature
	}

	// fail-closed：未配置支付宝公钥则一律拒绝。
	if v.publicKey == nil {
		return ErrInvalidSignature
	}

	// 2. 按字母序拼接待验签串（排除 sign / sign_type，跳过空值）。
	keys := make([]string, 0, len(body))
	for k := range body {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		val := stringifyAlipayValue(body[k])
		if val == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(val)
	}

	// 3. RSA-SHA256（RSA2）验签。
	sigBytes, err := base64.StdEncoding.DecodeString(signStr)
	if err != nil {
		return ErrInvalidSignature
	}
	digest := sha256.Sum256([]byte(sb.String()))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

// stringifyAlipayValue 将参数值还原为待验签字符串。
// 字符串直接返回；其它类型按其原始文本表示拼接（与支付宝原始报文一致）。
func stringifyAlipayValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

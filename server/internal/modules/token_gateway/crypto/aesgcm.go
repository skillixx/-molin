// Package crypto 提供 token_gateway 模块内的 AES-256-GCM 加解密，
// 专用于上游渠道 api_key 的加密存储。密钥经环境变量 TOKEN_PROVIDER_KEY 注入（32 字节）。
//
// 安全红线：明文/密文 api_key 绝不出现在任何 API 响应或日志中；
// 本文件仅负责落库前加密与转发时解密，调用方不得把返回值写入响应。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrKeyLength 密钥长度非法（AES-256-GCM 要求 32 字节）。
var ErrKeyLength = errors.New("TOKEN_PROVIDER_KEY 必须为 32 字节（AES-256-GCM）")

// ErrCipherTooShort 密文长度不足，无法包含 nonce。
var ErrCipherTooShort = errors.New("密文长度不足")

// AESGCM 基于 32 字节密钥的 AES-256-GCM 加解密器。
type AESGCM struct {
	gcm cipher.AEAD
}

// New 用 32 字节密钥构造加解密器；密钥长度非法时返回错误。
func New(key []byte) (*AESGCM, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	return &AESGCM{gcm: gcm}, nil
}

// Encrypt 加密明文，返回 base64(nonce || ciphertext) 字符串，供落库。
func (a *AESGCM) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	// Seal 将密文追加到 nonce 之后，得到 nonce||ciphertext
	sealed := a.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 base64(nonce || ciphertext) 字符串，返回明文。
func (a *AESGCM) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	ns := a.gcm.NonceSize()
	if len(raw) < ns {
		return "", ErrCipherTooShort
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plaintext, err := a.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return string(plaintext), nil
}

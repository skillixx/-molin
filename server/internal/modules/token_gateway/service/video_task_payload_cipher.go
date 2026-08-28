package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"molin/server/internal/modules/token_gateway/model"
)

// VideoTaskPayloadProtector 使用阶段专用AES-GCM密钥保护Prompt及Provider敏感载荷。
// 实例只持有进程内密钥副本，持久化层仅接收密文信封和不可逆完整性摘要。
type VideoTaskPayloadProtector struct {
	keyVersion string
	key        []byte
	random     io.Reader
}

// NewVideoTaskPayloadProtector 创建任务载荷保护器；AES密钥只允许16、24或32字节。
func NewVideoTaskPayloadProtector(keyVersion string, key []byte) (*VideoTaskPayloadProtector, error) {
	keyVersion = strings.TrimSpace(keyVersion)
	if keyVersion == "" {
		return nil, fmt.Errorf("视频任务载荷密钥版本不能为空")
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, fmt.Errorf("视频任务载荷AES密钥长度无效")
	}
	return &VideoTaskPayloadProtector{keyVersion: keyVersion, key: append([]byte(nil), key...), random: rand.Reader}, nil
}

// Seal 把明文封装为绑定任务归属和载荷类型的AES-GCM密文；明文不会复制到模型的普通字段。
func (p *VideoTaskPayloadProtector) Seal(taskID, userID, projectID uint64, payloadKind string, plaintext []byte) (*model.AIGatewayTaskPayload, error) {
	if p == nil || taskID == 0 || userID == 0 || projectID == 0 || !validVideoPayloadKind(payloadKind) || len(plaintext) == 0 {
		return nil, fmt.Errorf("视频任务载荷参数无效")
	}
	gcm, err := p.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(p.random, nonce); err != nil {
		return nil, fmt.Errorf("生成视频任务载荷nonce失败: %w", err)
	}
	aad := videoPayloadAAD(taskID, userID, projectID, payloadKind)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	return &model.AIGatewayTaskPayload{
		TaskID: taskID, UserID: userID, ProjectID: projectID, PayloadKind: payloadKind,
		Ciphertext: ciphertext, Nonce: nonce, KeyVersion: p.keyVersion,
		AADSHA256: videoPayloadSHA256(aad), CiphertextSHA256: videoPayloadSHA256(ciphertext),
	}, nil
}

// Open 在解密前验证归属AAD、密文摘要、nonce长度和密钥版本，任何漂移都失败关闭。
func (p *VideoTaskPayloadProtector) Open(payload *model.AIGatewayTaskPayload) ([]byte, error) {
	if p == nil || payload == nil || payload.TaskID == 0 || payload.UserID == 0 || payload.ProjectID == 0 || !validVideoPayloadKind(payload.PayloadKind) {
		return nil, fmt.Errorf("视频任务载荷信封无效")
	}
	if subtle.ConstantTimeCompare([]byte(payload.KeyVersion), []byte(p.keyVersion)) != 1 {
		return nil, fmt.Errorf("视频任务载荷密钥版本不匹配")
	}
	gcm, err := p.gcm()
	if err != nil {
		return nil, err
	}
	if len(payload.Nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("视频任务载荷nonce长度无效")
	}
	aad := videoPayloadAAD(payload.TaskID, payload.UserID, payload.ProjectID, payload.PayloadKind)
	if subtle.ConstantTimeCompare([]byte(payload.AADSHA256), []byte(videoPayloadSHA256(aad))) != 1 ||
		subtle.ConstantTimeCompare([]byte(payload.CiphertextSHA256), []byte(videoPayloadSHA256(payload.Ciphertext))) != 1 {
		return nil, fmt.Errorf("视频任务载荷完整性摘要不匹配")
	}
	plaintext, err := gcm.Open(nil, payload.Nonce, payload.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("视频任务载荷认证解密失败: %w", err)
	}
	return plaintext, nil
}

// ValidateEnvelope 通过实际AES-GCM认证解密证明信封由匹配密钥生成，并立即清零临时明文副本。
func (p *VideoTaskPayloadProtector) ValidateEnvelope(payload *model.AIGatewayTaskPayload) error {
	plaintext, err := p.Open(payload)
	if err != nil {
		return err
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	return nil
}

func (p *VideoTaskPayloadProtector) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return nil, fmt.Errorf("创建视频任务载荷AES失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建视频任务载荷GCM失败: %w", err)
	}
	return gcm, nil
}

func validVideoPayloadKind(kind string) bool {
	return kind == model.AITaskPayloadPrompt || kind == model.AITaskPayloadProviderRequest || kind == model.AITaskPayloadProviderResult
}

func videoPayloadAAD(taskID, userID, projectID uint64, kind string) []byte {
	return []byte(fmt.Sprintf("molin:video-task-payload:v1:%d:%d:%d:%s", taskID, userID, projectID, kind))
}

func videoPayloadAADSHA256(taskID, userID, projectID uint64, kind string) string {
	return videoPayloadSHA256(videoPayloadAAD(taskID, userID, projectID, kind))
}

func videoPayloadSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

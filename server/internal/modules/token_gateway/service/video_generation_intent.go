package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"molin/server/internal/modules/token_gateway/model"
)

const VideoGenerationIntentVersion = "video-create-v1"

var (
	ErrVideoGenerationIntent = errors.New("视频生成意图无效")
	videoIntentPolicyCode    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	videoIntentModelCode     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// VideoGenerationIntent 只含生成语义；归属由请求幂等作用域独立冻结，不由接口门面决定。
// Input中的资产ID仍供创建绑定使用，但不会进入生成意图哈希。
type VideoGenerationIntent struct {
	LogicalModelCode    string
	PromptHMAC          string
	RightsPolicyVersion string
	Variant             VideoPriceVariant
	Input               *VideoQuoteInputBinding
}

// NormalizeVideoGenerationPrompt 统一换行和边缘空白；内部空白保留，不进行可能改变语义的折叠。
func NormalizeVideoGenerationPrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\r", "\n"))
	if !utf8.ValidString(prompt) || utf8.RuneCountInString(prompt) == 0 || utf8.RuneCountInString(prompt) > 1000 || strings.ContainsRune(prompt, 0) {
		return "", ErrVideoGenerationIntent
	}
	return prompt, nil
}

// VideoGenerationPromptHMAC 使用独立域分离标识保护规范化Prompt；从不返回明文或把明文加入普通模型。
func VideoGenerationPromptHMAC(secret []byte, prompt string) (string, error) {
	if len(secret) < 32 {
		return "", ErrVideoGenerationIntent
	}
	normalized, err := NormalizeVideoGenerationPrompt(prompt)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("molin:video-prompt:v1\x00"))
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// BuildVideoGenerationIntentFingerprint 与G2 Quote指纹分离，避免Quote/资产ID或门面变化制造第二次扣费。
func BuildVideoGenerationIntentFingerprint(secret []byte, input VideoGenerationIntent) (string, error) {
	if len(secret) < 32 || !videoIntentModelCode.MatchString(strings.TrimSpace(input.LogicalModelCode)) ||
		!lowerHex64.MatchString(input.PromptHMAC) || !videoIntentPolicyCode.MatchString(input.RightsPolicyVersion) {
		return "", ErrVideoGenerationIntent
	}
	variantJSON, _, err := CanonicalVideoPriceVariant(input.Variant)
	if err != nil {
		return "", ErrVideoGenerationIntent
	}
	// 复用实际执行规格解析，禁止仅能哈希但不能执行的尺寸或数值进入幂等事实。
	if _, err := parseVideoG4TaskSpec(variantJSON); err != nil {
		return "", ErrVideoGenerationIntent
	}
	var inputHash string
	var inputVersion uint64
	switch input.Variant.Operation {
	case model.AIVideoOperationTextToVideo:
		if input.Input != nil {
			return "", ErrVideoGenerationIntent
		}
	case model.AIVideoOperationImageToVideo:
		if input.Input == nil || !lowerHex64.MatchString(input.Input.NormalizedSHA256) || input.Input.Version == 0 {
			return "", ErrVideoGenerationIntent
		}
		inputHash, inputVersion = input.Input.NormalizedSHA256, input.Input.Version
	default:
		return "", ErrVideoGenerationIntent
	}
	payload := struct {
		Version             string          `json:"version"`
		Capability          string          `json:"capability"`
		LogicalModel        string          `json:"logical_model"`
		PromptHMAC          string          `json:"prompt_hmac"`
		RightsPolicyVersion string          `json:"rights_policy_version"`
		Variant             json.RawMessage `json:"variant"`
		InputSHA256         string          `json:"input_sha256,omitempty"`
		InputVersion        uint64          `json:"input_version,omitempty"`
	}{VideoGenerationIntentVersion, model.AIVideoCapability, strings.TrimSpace(input.LogicalModelCode), input.PromptHMAC, input.RightsPolicyVersion, variantJSON, inputHash, inputVersion}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", ErrVideoGenerationIntent
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("molin:video-create:v1\x00"))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

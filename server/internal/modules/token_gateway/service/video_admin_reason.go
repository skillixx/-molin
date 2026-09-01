package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"
)

var ErrVideoAdminReason = errors.New("管理原因加密信息无效")

// 原因使用显式专用密钥，不复用Prompt类型或Provider凭据；配置缺失不得开启管理写操作。
type VideoAdminReasonProtector struct {
	keyVersion     string
	key, digestKey []byte
}

type VideoAdminReasonIdentity struct {
	ActorID                         uint64
	TaskID, CommandKeyHash          string
	InputAssetID                    string
	OutputAssetID                   string
	OutputReleaseAssetID            string
	PollTaskID                      string
	ArchiveTaskID                   string
	AdjustmentTaskID                string
	ModelCode, ModelAction          string
	ProjectID                       uint64
	ProjectModelCode, ProjectAction string
	VersionNo                       uint64
}

// 信封只允许进入专用受保护列，任何普通JSON序列化均不能泄露密文或内部审计绑定。
type VideoAdminReasonEnvelope struct {
	KeyVersion        string `json:"-"`
	Nonce, Ciphertext []byte `json:"-"`
	AADSHA256         string `gorm:"column:aad_sha256" json:"-"`
	CiphertextSHA256  string `gorm:"column:ciphertext_sha256" json:"-"`
	ReasonHMAC        string `gorm:"column:reason_hmac" json:"-"`
	ReasonLength      uint32 `json:"-"`
}

func NewVideoAdminReasonProtector(version string, secret []byte) (*VideoAdminReasonProtector, error) {
	if !videoAdminModelCode.MatchString(version) || len(version) > 64 || len(secret) != 32 {
		return nil, ErrVideoAdminReason
	}
	derive := func(label string) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(label))
		return h.Sum(nil)
	}
	return &VideoAdminReasonProtector{keyVersion: version, key: derive("molin-video-admin-reason-encryption-v1"), digestKey: derive("molin-video-admin-reason-digest-v1")}, nil
}

func (p *VideoAdminReasonProtector) digest(domain, value string) string {
	h := hmac.New(sha256.New, p.digestKey)
	h.Write([]byte(domain + "\n" + value))
	return hex.EncodeToString(h.Sum(nil))
}

func (p *VideoAdminReasonProtector) aad(id VideoAdminReasonIdentity) ([]byte, error) {
	if p == nil || len(p.key) != 32 || len(p.digestKey) != 32 || id.ActorID == 0 || id.VersionNo == 0 || !lowerHex64.MatchString(id.CommandKeyHash) {
		return nil, ErrVideoAdminReason
	}
	if id.ProjectID != 0 || id.ProjectModelCode != "" || id.ProjectAction != "" {
		if id.ProjectID == 0 || !videoAdminModelCode.MatchString(id.ProjectModelCode) || len(id.ProjectModelCode) > 128 || (id.ProjectAction != "grant" && id.ProjectAction != "revoke") || id.ModelCode != "" || id.ModelAction != "" || id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || id.OutputReleaseAssetID != "" || id.PollTaskID != "" || id.ArchiveTaskID != "" || id.AdjustmentTaskID != "" {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-project-grant-%s-reason-v1\n%s\n%d\n%d\n%s\n%s\n%d", id.ProjectAction, p.keyVersion, id.ActorID, id.ProjectID, id.ProjectModelCode, id.CommandKeyHash, id.VersionNo)), nil
	}
	// 模型操作使用独立AAD域，不允许借用任务、资产或调账原因信封。
	if id.ModelCode != "" || id.ModelAction != "" {
		if !videoAdminModelCode.MatchString(id.ModelCode) || len(id.ModelCode) > 128 || (id.ModelAction != "create" && id.ModelAction != "update" && id.ModelAction != "publish" && id.ModelAction != "unpublish" && id.ModelAction != "rollback") || id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || id.OutputReleaseAssetID != "" || id.PollTaskID != "" || id.ArchiveTaskID != "" || id.AdjustmentTaskID != "" {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-model-%s-reason-v1\n%s\n%d\n%s\n%s\n%d", id.ModelAction, p.keyVersion, id.ActorID, id.ModelCode, id.CommandKeyHash, id.VersionNo)), nil
	}
	// 原取消AAD保持逐字兼容；输入隔离采用独立领域，禁止相同公开ID跨资源借用信封。
	if id.AdjustmentTaskID != "" {
		if id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || id.OutputReleaseAssetID != "" || id.PollTaskID != "" || id.ArchiveTaskID != "" || !videoBillingPublicID.MatchString(id.AdjustmentTaskID) {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-adjustment-reason-v1\n%s\n%d\n%s\n%s\n%d", p.keyVersion, id.ActorID, id.AdjustmentTaskID, id.CommandKeyHash, id.VersionNo)), nil
	}
	if id.ArchiveTaskID != "" {
		if id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || id.OutputReleaseAssetID != "" || id.PollTaskID != "" || !videoBillingPublicID.MatchString(id.ArchiveTaskID) {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-archive-reason-v1\n%s\n%d\n%s\n%s\n%d", p.keyVersion, id.ActorID, id.ArchiveTaskID, id.CommandKeyHash, id.VersionNo)), nil
	}
	if id.PollTaskID != "" {
		if id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || id.OutputReleaseAssetID != "" || !videoBillingPublicID.MatchString(id.PollTaskID) {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-poll-reason-v1\n%s\n%d\n%s\n%s\n%d", p.keyVersion, id.ActorID, id.PollTaskID, id.CommandKeyHash, id.VersionNo)), nil
	}
	if id.OutputReleaseAssetID != "" {
		if id.TaskID != "" || id.InputAssetID != "" || id.OutputAssetID != "" || !videoBillingPublicID.MatchString(id.OutputReleaseAssetID) {
			return nil, ErrVideoAdminReason
		}
		return []byte(fmt.Sprintf("molin-video-admin-output-release-reason-v1\n%s\n%d\n%s\n%s\n%d", p.keyVersion, id.ActorID, id.OutputReleaseAssetID, id.CommandKeyHash, id.VersionNo)), nil
	}
	domain, target := "molin-video-admin-cancel-reason-v1", id.TaskID
	if id.InputAssetID != "" {
		if id.TaskID != "" || id.OutputAssetID != "" {
			return nil, ErrVideoAdminReason
		}
		domain, target = "molin-video-admin-input-quarantine-reason-v1", id.InputAssetID
	}
	if id.OutputAssetID != "" {
		if id.TaskID != "" || id.InputAssetID != "" {
			return nil, ErrVideoAdminReason
		}
		domain, target = "molin-video-admin-output-quarantine-reason-v1", id.OutputAssetID
	}
	if !videoBillingPublicID.MatchString(target) {
		return nil, ErrVideoAdminReason
	}
	return []byte(fmt.Sprintf("%s\n%s\n%d\n%s\n%s\n%d", domain, p.keyVersion, id.ActorID, target, id.CommandKeyHash, id.VersionNo)), nil
}

func (p *VideoAdminReasonProtector) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return nil, ErrVideoAdminReason
	}
	return cipher.NewGCM(block)
}

func (p *VideoAdminReasonProtector) Seal(id VideoAdminReasonIdentity, reason []byte) (*VideoAdminReasonEnvelope, error) {
	aad, err := p.aad(id)
	if err != nil || !utf8.Valid(reason) || len(reason) == 0 || len(reason) > 1024 || utf8.RuneCount(reason) > 256 {
		return nil, ErrVideoAdminReason
	}
	gcm, err := p.aead()
	if err != nil {
		return nil, ErrVideoAdminReason
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrVideoAdminReason
	}
	ciphertext := gcm.Seal(nil, nonce, reason, aad)
	return &VideoAdminReasonEnvelope{KeyVersion: p.keyVersion, Nonce: nonce, Ciphertext: ciphertext, AADSHA256: videoPayloadSHA256(aad), CiphertextSHA256: videoPayloadSHA256(ciphertext), ReasonHMAC: p.digest("reason", string(reason)), ReasonLength: uint32(utf8.RuneCount(reason))}, nil
}

// 解密仅提供给受控审计调用方；本阶段没有公开原因解密HTTP入口。
func (p *VideoAdminReasonProtector) Open(id VideoAdminReasonIdentity, e VideoAdminReasonEnvelope) ([]byte, error) {
	aad, err := p.aad(id)
	if err != nil || e.KeyVersion != p.keyVersion || subtle.ConstantTimeCompare([]byte(e.AADSHA256), []byte(videoPayloadSHA256(aad))) != 1 || subtle.ConstantTimeCompare([]byte(e.CiphertextSHA256), []byte(videoPayloadSHA256(e.Ciphertext))) != 1 {
		return nil, ErrVideoAdminReason
	}
	gcm, err := p.aead()
	if err != nil || len(e.Nonce) != gcm.NonceSize() || len(e.Ciphertext) > 1024+gcm.Overhead() {
		return nil, ErrVideoAdminReason
	}
	plain, err := gcm.Open(nil, e.Nonce, e.Ciphertext, aad)
	if err != nil || !utf8.Valid(plain) || len(plain) == 0 || utf8.RuneCount(plain) > 256 || e.ReasonLength != uint32(utf8.RuneCount(plain)) || subtle.ConstantTimeCompare([]byte(e.ReasonHMAC), []byte(p.digest("reason", string(plain)))) != 1 {
		return nil, ErrVideoAdminReason
	}
	return plain, nil
}

package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	video "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoCallbackRequest        = errors.New("内部视频回调请求无效")
	ErrVideoCallbackAuthentication = errors.New("内部视频回调认证失败")
	ErrVideoCallbackUnavailable    = errors.New("内部视频回调未就绪")
)

const videoCallbackFakeProvider = "fake-native-async"
const videoCallbackPath = "/api/internal/ai/provider-callbacks/" + videoCallbackFakeProvider

// 请求仅短暂用于认证；原始正文与签名不能被普通响应或审计JSON序列化。
type VideoCallbackRequest struct {
	ProviderCode string `json:"-"`
	Method       string `json:"-"`
	Path         string `json:"-"`
	Timestamp    string `json:"-"`
	Nonce        string `json:"-"`
	Signature    string `json:"-"`
	Body         []byte `json:"-"`
}

// 通过验签后的对象只携带最小状态事实和摘要，不保留原始Provider正文。
type VideoVerifiedCallback struct {
	VideoID       string                 `json:"-"`
	Event         video.VerifiedCallback `json:"-"`
	NonceSHA256   string                 `json:"-"`
	RequestSHA256 string                 `json:"-"`
	SignedAt      time.Time              `json:"-"`
}

// 这是G6 Fake内部工程协议，不是Runware真实Webhook签名实现；没有默认secret或密钥回退。
type VideoCallbackVerifier struct{ secret []byte }

func NewVideoCallbackVerifier(secret []byte) (*VideoCallbackVerifier, error) {
	if len(secret) != 32 {
		return nil, ErrVideoCallbackUnavailable
	}
	return &VideoCallbackVerifier{secret: append([]byte(nil), secret...)}, nil
}

func (v *VideoCallbackVerifier) Verify(ctx context.Context, request VideoCallbackRequest, now time.Time) (VideoVerifiedCallback, error) {
	var empty VideoVerifiedCallback
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if v == nil || len(v.secret) != 32 {
		return empty, ErrVideoCallbackUnavailable
	}
	if request.ProviderCode != videoCallbackFakeProvider || request.Method != "POST" || request.Path != videoCallbackPath || len(request.Body) == 0 || len(request.Body) > 64<<10 || !lowerHex64.MatchString(request.Nonce) {
		return empty, ErrVideoCallbackRequest
	}
	stamp, err := strconv.ParseInt(request.Timestamp, 10, 64)
	if err != nil || stamp < 0 || strconv.FormatInt(stamp, 10) != request.Timestamp || now.IsZero() {
		return empty, ErrVideoCallbackRequest
	}
	if stamp < now.Unix()-300 || stamp > now.Unix()+30 || !lowerHex64.MatchString(request.Signature) {
		return empty, ErrVideoCallbackAuthentication
	}
	bodyHash := videoPayloadSHA256(request.Body)
	canonical := "molin-video-callback-v1\n" + request.Method + "\n" + request.Path + "\n" + request.Timestamp + "\n" + request.Nonce + "\n" + bodyHash
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(canonical))
	provided, _ := hex.DecodeString(request.Signature)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return empty, ErrVideoCallbackAuthentication
	}
	payload, err := decodeVideoCallbackPayload(request.Body)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return VideoVerifiedCallback{VideoID: payload.VideoID, NonceSHA256: videoPayloadSHA256([]byte(request.Nonce)), RequestSHA256: videoPayloadSHA256([]byte(canonical)), SignedAt: time.Unix(stamp, 0).UTC(), Event: video.VerifiedCallback{ProviderCode: request.ProviderCode, ProviderTaskID: payload.ProviderTaskID, ExternalEventID: payload.ExternalEventID, BodySHA256: bodyHash, Status: payload.Status, Progress: payload.Progress}}, nil
}

type videoCallbackPayload struct {
	ProviderTaskID  string                   `json:"provider_task_id"`
	ExternalEventID string                   `json:"external_event_id"`
	VideoID         string                   `json:"video_id"`
	Status          video.ProviderTaskStatus `json:"status"`
	Progress        uint8                    `json:"progress"`
}

// 逐键消费原始JSON，防止map/struct解码静默覆盖重复键或将null解释成零值。
func decodeVideoCallbackPayload(raw []byte) (*videoCallbackPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, ErrVideoCallbackRequest
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return nil, ErrVideoCallbackRequest
		}
		switch name {
		case "provider_task_id", "external_event_id", "video_id", "status", "progress":
		default:
			return nil, ErrVideoCallbackRequest
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrVideoCallbackRequest
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(seen) != 5 {
		return nil, ErrVideoCallbackRequest
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrVideoCallbackRequest
	}
	var p videoCallbackPayload
	if json.Unmarshal(raw, &p) != nil || !videoBillingPublicID.MatchString(p.VideoID) || !videoBillingPublicID.MatchString(p.ProviderTaskID) || !strings.HasPrefix(p.ProviderTaskID, "taskUUID-") || len(p.ProviderTaskID) <= len("taskUUID-") || !videoBillingPublicID.MatchString(p.ExternalEventID) || p.Progress > 100 {
		return nil, ErrVideoCallbackRequest
	}
	switch p.Status {
	case video.ProviderTaskProcessing, video.ProviderTaskSucceeded, video.ProviderTaskFailed, video.ProviderTaskCancelled, video.ProviderTaskUnknown:
	default:
		return nil, ErrVideoCallbackRequest
	}
	return &p, nil
}

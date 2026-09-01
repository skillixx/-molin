package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

const callbackVectorBody = `{"provider_task_id":"taskUUID-http-1","external_event_id":"evt-http-1","video_id":"vid-http-1","status":"processing","progress":15}`

func callbackVectorKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

// 独立按公开规范构造测试签名；固定向量另外经.NET HMAC验证，避免只用被测签名器自证。
func signCallbackFixture(r VideoCallbackRequest) string {
	body := sha256.Sum256(r.Body)
	canonical := fmt.Sprintf("molin-video-callback-v1\n%s\n%s\n%s\n%s\n%x", r.Method, r.Path, r.Timestamp, r.Nonce, body)
	mac := hmac.New(sha256.New, callbackVectorKey())
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVideoG6CallbackVerifierContract(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	r := VideoCallbackRequest{ProviderCode: "fake-native-async", Method: "POST", Path: "/api/internal/ai/provider-callbacks/fake-native-async", Timestamp: "1800000000", Nonce: strings.Repeat("0123456789abcdef", 4), Body: []byte(callbackVectorBody), Signature: "0884d43e669913511f92058b11e8d773bfa7ee46fafa8ad93d3f142bb0019a27"}
	verifier, err := NewVideoCallbackVerifier(callbackVectorKey())
	if err != nil {
		t.Fatal(err)
	}
	if signCallbackFixture(r) != r.Signature {
		t.Fatal("独立测试签名必须匹配已知向量")
	}
	got, err := verifier.Verify(context.Background(), r, now)
	if err != nil || got.VideoID != "vid-http-1" || got.Event.BodySHA256 != "122176b8b9e0f36903069831a619aa0dbf67c85a2f7dfe45ba0bf15ded8c5abb" || got.RequestSHA256 != "0c21450cded7de4b2665e0d380d646dc029cc5d4dd100d413e90f58b1cbc88dd" || got.Event.Progress != 15 {
		t.Fatalf("回调签名向量或解析不符：%v", err)
	}
	for _, value := range []any{r, got, verifier} {
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != "{}" {
			t.Fatal("认证对象不得进入普通JSON响应")
		}
	}
	for _, delta := range []int64{-301, -300, 30, 31} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			changed := r
			changed.Timestamp = strconv.FormatInt(now.Unix()+delta, 10)
			changed.Signature = signCallbackFixture(changed)
			_, err := verifier.Verify(context.Background(), changed, now)
			want := delta >= -300 && delta <= 30
			if (err == nil) != want {
				t.Fatal("签名时间窗边界不符")
			}
		})
	}
	for _, field := range []string{"provider", "path", "method", "nonce", "timestamp", "body", "signature"} {
		t.Run(field, func(t *testing.T) {
			changed := r
			switch field {
			case "provider":
				changed.ProviderCode = "other"
			case "path":
				changed.Path += "/"
			case "method":
				changed.Method = "PUT"
			case "nonce":
				changed.Nonce = strings.Repeat("f", 64)
			case "timestamp":
				changed.Timestamp = "1800000001"
			case "body":
				changed.Body = []byte(strings.Replace(callbackVectorBody, "vid-http-1", "vid-http-2", 1))
			case "signature":
				changed.Signature = strings.Repeat("0", 64)
			}
			if _, err := verifier.Verify(context.Background(), changed, now); err == nil {
				t.Fatal("签名范围篡改必须拒绝")
			}
		})
	}
	for _, body := range []string{`{}`, `[]`, `null`, strings.Replace(callbackVectorBody, `"progress":15`, `"progress":null`, 1), strings.Replace(callbackVectorBody, `"progress":15`, `"progress":101`, 1), strings.Replace(callbackVectorBody, `"status":"processing"`, `"status":"completed"`, 1), strings.Replace(callbackVectorBody, `"video_id"`, `"Video_ID"`, 1), strings.TrimSuffix(callbackVectorBody, "}") + `,"progress":15}`, strings.TrimSuffix(callbackVectorBody, "}") + `,"owner":1}`, callbackVectorBody + `{}`, strings.Repeat("x", (64<<10)+1)} {
		changed := r
		changed.Body = []byte(body)
		changed.Signature = signCallbackFixture(changed)
		if _, err := verifier.Verify(context.Background(), changed, now); err == nil {
			t.Fatal("已签名的歧义或非法JSON仍须拒绝")
		}
	}
	for _, stamp := range []string{"01800000000", "+1800000000", "1800000000.0", " 1800000000"} {
		changed := r
		changed.Timestamp = stamp
		changed.Signature = signCallbackFixture(changed)
		if _, err := verifier.Verify(context.Background(), changed, now); err == nil {
			t.Fatal("时间戳必须规范整数编码")
		}
	}
	if _, err := NewVideoCallbackVerifier(make([]byte, 31)); err == nil {
		t.Fatal("缺失专用32字节secret必须失败关闭")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifier.Verify(cancelled, r, now); err == nil {
		t.Fatal("取消不能继续验签")
	}
}

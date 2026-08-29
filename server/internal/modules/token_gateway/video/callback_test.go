package video

import (
	"context"
	"errors"
	"testing"
)

func TestFakeProviderCallbackVerifierReturnsOnlyLowSensitiveFacts(t *testing.T) {
	verifier := NewFakeProviderCallbackVerifier([]byte("local-fixture-secret"))
	body := []byte("{\"event_id\":\"evt-1\",\"task_id\":\"taskUUID-abc\",\"status\":\"succeeded\"}")
	signature := verifier.Sign(body)
	verified, err := verifier.Verify(context.Background(), CallbackEnvelope{
		ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-abc",
		ExternalEventID: "evt-1", Signature: signature, Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.BodySHA256 == "" || verified.ExternalEventID != "evt-1" || verified.ProviderTaskID != "taskUUID-abc" || verified.Status != ProviderTaskSucceeded {
		t.Fatalf("回调低敏事实不完整: %+v", verified)
	}
	if string(verified.RawBody) != "" {
		t.Fatal("验签结果不得暴露原始回调正文")
	}
	body[0] = 'X'
	if verified.BodySHA256 == "" {
		t.Fatal("body哈希必须独立于调用方后续修改")
	}
}

func TestFakeProviderCallbackVerifierRejectsInvalidSignatureAndIdentity(t *testing.T) {
	verifier := NewFakeProviderCallbackVerifier([]byte("local-fixture-secret"))
	for _, envelope := range []CallbackEnvelope{
		{ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-abc", ExternalEventID: "evt-1", Signature: "bad", Body: []byte("{\"status\":\"succeeded\"}")},
		{ProviderCode: "fake-native-async", ProviderTaskID: "https://internal/video", ExternalEventID: "evt-1", Body: []byte("{\"status\":\"succeeded\"}")},
		{ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-abc", ExternalEventID: "", Body: []byte("{\"status\":\"succeeded\"}")},
	} {
		if _, err := verifier.Verify(context.Background(), envelope); !errors.Is(err, ErrCallbackInvalid) {
			t.Fatalf("非法回调必须失败关闭: envelope=%+v err=%v", envelope, err)
		}
	}
}

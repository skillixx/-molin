package video

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVideoRuntimeJSONNeverExposesSensitiveContent(t *testing.T) {
	task := GatewayTask{
		TaskID: "vid_task_safe", RequestID: "vid_req_safe", Operation: OperationImageToVideo,
		Prompt: "PROMPT_PLAINTEXT_SENTINEL", ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-sensitive",
		Reference: &NormalizedReferenceImage{Bytes: []byte("REFERENCE_BYTES_SENTINEL"), MIMEType: "image/png"},
		Content:   &ControlledContentRef{ProviderTaskID: "taskUUID-sensitive", ContentID: "content-sensitive", MediaType: "video/mp4"},
		Asset:     &GatewayAsset{Object: StoredVideoObject{Ref: VideoObjectRef{Bucket: "SECRET_BUCKET", ObjectKey: "SECRET_OBJECT_KEY"}}},
	}
	callback := CallbackEnvelope{Signature: "SIGNATURE_SENTINEL", Body: []byte("CALLBACK_BODY_SENTINEL")}
	for name, value := range map[string]interface{}{"task": task, "callback": callback} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, forbidden := range []string{
			"PROMPT_PLAINTEXT_SENTINEL", "REFERENCE_BYTES_SENTINEL", "taskUUID-sensitive",
			"content-sensitive", "SECRET_BUCKET", "SECRET_OBJECT_KEY", "SIGNATURE_SENTINEL", "CALLBACK_BODY_SENTINEL",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s JSON泄露敏感正文或内部定位: %s", name, text)
			}
		}
	}
}

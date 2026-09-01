package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// 发布快照必须携带管理员明确配置的七键合同，不能把缺失字段补成隐式授权。
func TestVideoG6ModelSnapshotPreservesContract(t *testing.T) {
	const raw = `{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`
	var item TokenModel
	if err := json.Unmarshal([]byte(`{"logical_model_code":"molin/video-contract","modality":"video","video_contract":`+raw+`}`), &item); err != nil {
		t.Fatal(err)
	}
	encoded, err := item.MarshalReleaseSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	var contract map[string]json.RawMessage
	if json.Unmarshal(snapshot["video_contract"], &contract) != nil || len(contract) != 7 {
		t.Fatal("视频发布丢失七键合同")
	}
	if string(contract["default_model"]) != "false" || string(contract["required_entitlement_type"]) != "null" || string(contract["required_membership_levels"]) != "[]" {
		t.Fatal("发布不能将显式false/null/空数组省略")
	}
	// Chat/Image原快照不增加空视频字段。
	for _, modality := range []string{"chat", "image"} {
		b, err := (TokenModel{LogicalModelCode: "molin/legacy", Modality: modality}).MarshalReleaseSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		var legacy map[string]json.RawMessage
		if err := json.Unmarshal(b, &legacy); err != nil {
			t.Fatal(err)
		}
		if _, ok := legacy["video_contract"]; ok {
			t.Fatal("非视频快照出现视频字段")
		}
	}
}

func TestVideoG6ModelSnapshotRejectsInvalidContract(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"supported_operations":["text_to_video"]}`} {
		item := TokenModel{Modality: "video", VideoContractJSON: json.RawMessage(raw)}
		if _, err := item.MarshalReleaseSnapshot(); err == nil {
			t.Fatal("缺失或不完整视频合同进入发布快照")
		}
	}
	const valid = `{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`
	for _, raw := range []string{strings.Replace(valid, "non_commercial_test_fixture", "commercial", 1), strings.Replace(valid, `"default_model":false`, `"default_model":false,"default_model":true`, 1)} {
		if _, err := (TokenModel{Modality: "video", VideoContractJSON: json.RawMessage(raw)}).MarshalReleaseSnapshot(); err == nil {
			t.Fatal("未授权商业合同或重复键进入发布快照")
		}
	}
	if _, err := (TokenModel{Modality: "chat", VideoContractJSON: json.RawMessage(valid)}).MarshalReleaseSnapshot(); err == nil {
		t.Fatal("通过修改模态发布视频合同")
	}
}

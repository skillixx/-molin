package dto

import (
	"encoding/json"
	"testing"
)

// 公开Job必须完整保留冻结null字段；内部ID、Prompt和对象位置没有DTO字段可承载。
func TestVideoG6JobJSONSnapshot(t *testing.T) {
	job := VideoJob{ID: "video_0123456789abcdef", CreatedAt: 1, Model: "molin/video-standard", Object: "video", Seconds: "5", Size: "1280x720", Status: "queued"}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	expected := []string{"id", "completed_at", "created_at", "error", "expires_at", "model", "object", "progress", "prompt", "remixed_from_video_id", "seconds", "size", "status"}
	if len(fields) != len(expected) {
		t.Fatalf("字段数=%d", len(fields))
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			t.Fatalf("缺字段%s", name)
		}
	}
	for _, name := range []string{"completed_at", "error", "expires_at", "prompt", "remixed_from_video_id"} {
		if string(fields[name]) != "null" {
			t.Fatalf("%s必须显式为null", name)
		}
	}
	list, err := json.Marshal(VideoList{Object: "list", Data: []VideoJob{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(list) != `{"object":"list","data":[],"first_id":null,"last_id":null,"has_more":false}` {
		t.Fatal("空列表游标合同不符")
	}
}

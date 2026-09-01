package service

import (
	"bytes"
	"strings"
	"testing"
)

func TestVideoG6ModelDraftReasonBinding(t *testing.T) {
	p, err := NewVideoAdminReasonProtector("model-test-v1", bytes.Repeat([]byte{23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	id := VideoAdminReasonIdentity{ActorID: 9, ModelCode: "molin/video-model", ModelAction: "create", CommandKeyHash: strings.Repeat("a", 64), VersionNo: 1}
	env, err := p.Seal(id, []byte("模型调整仅用于合成验证"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(id, *env); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*VideoAdminReasonIdentity){func(x *VideoAdminReasonIdentity) { x.ActorID++ }, func(x *VideoAdminReasonIdentity) { x.ModelCode = "molin/other" }, func(x *VideoAdminReasonIdentity) { x.ModelAction = "update" }, func(x *VideoAdminReasonIdentity) { x.VersionNo++ }, func(x *VideoAdminReasonIdentity) { x.TaskID = "video_task_other" }, func(x *VideoAdminReasonIdentity) {
		x.ModelCode = ""
		x.ModelAction = ""
		x.AdjustmentTaskID = "video_task_other"
	}} {
		wrong := id
		mutate(&wrong)
		if _, err := p.Open(wrong, *env); err == nil {
			t.Fatal("模型原因信封被跨身份、动作或资源借用")
		}
	}
	a, _ := modelDraftResultHash([]byte(`{"initial_version":9007199254740992}`))
	b, _ := modelDraftResultHash([]byte(`{"initial_version":9007199254740993}`))
	if a == b {
		t.Fatal("大整数审计版本发生精度折叠")
	}
}

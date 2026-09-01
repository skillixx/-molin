package service

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"testing"
)

func TestVideoG6AdminReasonEncryption(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	p, err := NewVideoAdminReasonProtector("g6-admin-reason-test-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	identity := VideoAdminReasonIdentity{ActorID: 1, TaskID: "video_reason_fixture", CommandKeyHash: videoPayloadSHA256([]byte("测试命令")), VersionNo: 3}
	a, err := p.Seal(identity, []byte("合成管理员取消原因"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Seal(identity, []byte("合成管理员取消原因"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Nonce, b.Nonce) || bytes.Contains(a.Ciphertext, []byte("合成管理员取消原因")) {
		t.Fatal("原因必须专用密文并使用不同nonce")
	}
	plain, err := p.Open(identity, *a)
	if err != nil || string(plain) != "合成管理员取消原因" {
		t.Fatal("受保护原因必须能在原绑定下审阅")
	}
	for _, mutate := range []func(*VideoAdminReasonIdentity){func(i *VideoAdminReasonIdentity) { i.ActorID++ }, func(i *VideoAdminReasonIdentity) { i.TaskID += "x" }, func(i *VideoAdminReasonIdentity) { i.VersionNo++ }, func(i *VideoAdminReasonIdentity) { i.CommandKeyHash = videoPayloadSHA256([]byte("另一命令")) }} {
		other := identity
		mutate(&other)
		if _, err := p.Open(other, *a); err == nil {
			t.Fatal("原因AAD错绑必须失败关闭")
		}
	}
	bad := *a
	bad.Ciphertext = append([]byte(nil), a.Ciphertext...)
	bad.Ciphertext[0] ^= 1
	if _, err := p.Open(identity, bad); err == nil {
		t.Fatal("密文篡改必须拒绝")
	}
	for _, mutate := range []func(*VideoAdminReasonEnvelope){
		func(e *VideoAdminReasonEnvelope) { e.Nonce = append([]byte(nil), e.Nonce...); e.Nonce[0] ^= 1 },
		func(e *VideoAdminReasonEnvelope) { e.Nonce = e.Nonce[:11] },
		func(e *VideoAdminReasonEnvelope) { e.KeyVersion = "other-version" },
		func(e *VideoAdminReasonEnvelope) { e.AADSHA256 = videoPayloadSHA256([]byte("其它AAD")) },
		func(e *VideoAdminReasonEnvelope) { e.CiphertextSHA256 = videoPayloadSHA256([]byte("其它密文")) },
		func(e *VideoAdminReasonEnvelope) { e.ReasonHMAC = videoPayloadSHA256([]byte("其它原因")) },
		func(e *VideoAdminReasonEnvelope) { e.ReasonLength++ },
	} {
		changed := *a
		mutate(&changed)
		if _, err := p.Open(identity, changed); err == nil {
			t.Fatal("原因信封任何元数据漂移必须拒绝")
		}
	}
	otherKey := make([]byte, 32)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatal(err)
	}
	other, err := NewVideoAdminReasonProtector("g6-admin-reason-test-v1", otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(identity, *a); err == nil {
		t.Fatal("相同key version不能替代正确的专用密钥")
	}
	encoded, err := json.Marshal(a)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("普通JSON不得序列化管理原因信封")
	}
	if _, err := NewVideoAdminReasonProtector("", secret); err == nil {
		t.Fatal("必须显式设置密钥版本")
	}
	if _, err := NewVideoAdminReasonProtector("v1", secret[:16]); err == nil {
		t.Fatal("管理原因要求32字节专用密钥")
	}
}

func TestVideoG6AdminInputReasonBinding(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	p, err := NewVideoAdminReasonProtector("g6-admin-input-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	id := VideoAdminReasonIdentity{ActorID: 1, InputAssetID: "vin_admin_reason", CommandKeyHash: videoPayloadSHA256([]byte("隔离命令")), VersionNo: 1}
	e, err := p.Seal(id, []byte("合成输入隔离原因"))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := p.Open(id, *e); err != nil || string(raw) != "合成输入隔离原因" {
		t.Fatal("原输入隔离原因应可审阅")
	}
	wrong := id
	wrong.TaskID = wrong.InputAssetID
	wrong.InputAssetID = ""
	if _, err := p.Open(wrong, *e); err == nil {
		t.Fatal("输入原因不能被当成同名任务的取消原因")
	}
	wrong = id
	wrong.TaskID = "video_other"
	if _, err := p.Seal(wrong, []byte("模糊目标")); err == nil {
		t.Fatal("不允许同时绑定任务和输入")
	}
}

func TestVideoG6AdminOutputReasonBinding(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	p, err := NewVideoAdminReasonProtector("g6-admin-output-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	id := VideoAdminReasonIdentity{ActorID: 1, OutputAssetID: "vasset_reason", CommandKeyHash: videoPayloadSHA256([]byte("输出隔离命令")), VersionNo: 2}
	e, err := p.Seal(id, []byte("合成输出隔离原因"))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := p.Open(id, *e); err != nil || string(raw) != "合成输出隔离原因" {
		t.Fatal("输出原因应在原目标下可审阅")
	}
	wrong := id
	wrong.InputAssetID = wrong.OutputAssetID
	wrong.OutputAssetID = ""
	if _, err := p.Open(wrong, *e); err == nil {
		t.Fatal("输出原因不得借给同名输入")
	}
	wrong = id
	wrong.TaskID = "video_other"
	if _, err := p.Seal(wrong, []byte("模糊资源")); err == nil {
		t.Fatal("不允许输出和任务混合目标")
	}
}

func TestVideoG6AdminOutputReleaseReasonBinding(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	p, err := NewVideoAdminReasonProtector("g6-release-purpose-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	id := VideoAdminReasonIdentity{ActorID: 1, OutputReleaseAssetID: "vasset_release_reason", CommandKeyHash: videoPayloadSHA256([]byte("解除申请")), VersionNo: 3}
	e, err := p.Seal(id, []byte("独立解除复核"))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := p.Open(id, *e); err != nil || string(raw) != "独立解除复核" {
		t.Fatal("原解除原因必须可受控解密")
	}
	other := id
	other.OutputAssetID = other.OutputReleaseAssetID
	other.OutputReleaseAssetID = ""
	if _, err := p.Open(other, *e); err == nil {
		t.Fatal("解除原因不能冒充隔离原因")
	}
	other = id
	other.ActorID = 2
	if _, err := p.Open(other, *e); err == nil {
		t.Fatal("checker不能借用maker原因信封")
	}
	other = id
	other.InputAssetID = "vin_other"
	if _, err := p.Seal(other, []byte("混合目的")); err == nil {
		t.Fatal("解除身份必须与其他目的互斥")
	}
}

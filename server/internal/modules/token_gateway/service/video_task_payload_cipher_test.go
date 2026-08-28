package service

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVideoTaskPayloadProtectorRoundTripAndNoPlaintext(t *testing.T) {
	protector, err := NewVideoTaskPayloadProtector("vid-g3-key-v1", bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("仅用于测试的敏感提示词")
	first, err := protector.Seal(31, 7, 11, "prompt", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := protector.Seal(31, 7, 11, "prompt", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("相同明文的AES-GCM信封必须使用不同nonce和密文")
	}
	serialized, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, plaintext) || bytes.Contains(first.Ciphertext, plaintext) {
		t.Fatal("普通JSON或密文字节不得包含Prompt明文")
	}
	opened, err := protector.Open(first)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("AES-GCM解密结果不一致: opened=%q err=%v", opened, err)
	}
}

func TestVideoTaskPayloadProtectorRejectsEnvelopeTampering(t *testing.T) {
	protector, err := NewVideoTaskPayloadProtector("vid-g3-key-v1", bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protector.Seal(31, 7, 11, "prompt", []byte("测试载荷"))
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{name: "密文", mutate: func() { payload.Ciphertext[0] ^= 0xff }, restore: func() { payload.Ciphertext[0] ^= 0xff }},
		{name: "nonce", mutate: func() { payload.Nonce[0] ^= 0xff }, restore: func() { payload.Nonce[0] ^= 0xff }},
		{name: "AAD哈希", mutate: func() { payload.AADSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }, restore: func() {
			payload.AADSHA256 = videoPayloadAADSHA256(payload.TaskID, payload.UserID, payload.ProjectID, payload.PayloadKind)
		}},
		{name: "密文哈希", mutate: func() { payload.CiphertextSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" }, restore: func() { payload.CiphertextSHA256 = videoPayloadSHA256(payload.Ciphertext) }},
		{name: "归属", mutate: func() { payload.ProjectID++ }, restore: func() { payload.ProjectID-- }},
		{name: "密钥版本", mutate: func() { payload.KeyVersion = "vid-g3-key-v2" }, restore: func() { payload.KeyVersion = "vid-g3-key-v1" }},
	}
	for _, item := range mutations {
		t.Run(item.name, func(t *testing.T) {
			item.mutate()
			if _, openErr := protector.Open(payload); openErr == nil {
				t.Fatal("篡改后的任务载荷必须失败关闭")
			}
			item.restore()
		})
	}
}

func TestVideoTaskPayloadProtectorRejectsInvalidConfiguration(t *testing.T) {
	for _, item := range []struct {
		version string
		key     []byte
	}{
		{version: "", key: bytes.Repeat([]byte{1}, 32)},
		{version: "v1", key: []byte("too-short")},
	} {
		if _, err := NewVideoTaskPayloadProtector(item.version, item.key); err == nil {
			t.Fatal("无效密钥配置必须被拒绝")
		}
	}
}

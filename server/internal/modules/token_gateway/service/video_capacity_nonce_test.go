package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestVideoG7CapacityNonceKey(t *testing.T) {
	secret := bytes.Repeat([]byte("n"), 32)
	key, err := NewVideoCapacityNonceKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	identity := VideoCapacityIdentity{TaskID: "vid_nonce_1", RequestID: "req_nonce_1", UserID: 1, ProjectID: 2, Model: "molin/runway-gen4.5", Provider: "fake-native-async", Operation: "text_to_video"}
	first, err := key.Attempt(7, identity)
	if err != nil {
		t.Fatal(err)
	}
	if first.nonce != "74c79f0843ce36acdc7263891811e9508f239a718b14f45aa556915e22245fe8" {
		t.Fatalf("容量nonce固定向量漂移: %s", first.nonce)
	}
	second, err := key.Attempt(7, identity)
	if err != nil || first.identity != second.identity || first.nonce != second.nonce {
		t.Fatalf("同密钥、代次和完整身份必须稳定: %v", err)
	}
	otherEpoch, _ := key.Attempt(8, identity)
	if first.nonce == otherEpoch.nonce {
		t.Fatal("epoch变化必须派生不同容量能力")
	}
	apiKeyID := uint64(9)
	variations := []VideoCapacityIdentity{}
	for _, mutate := range []func(*VideoCapacityIdentity){
		func(v *VideoCapacityIdentity) { v.TaskID = "vid_nonce_2" },
		func(v *VideoCapacityIdentity) { v.RequestID = "req_nonce_2" },
		func(v *VideoCapacityIdentity) { v.UserID = 3 },
		func(v *VideoCapacityIdentity) { v.ProjectID = 4 },
		func(v *VideoCapacityIdentity) { v.APIKeyID = &apiKeyID },
		func(v *VideoCapacityIdentity) { v.Model = "molin/runway-gen4.5-alt" },
		func(v *VideoCapacityIdentity) { v.Operation = "image_to_video" },
	} {
		changed := identity
		mutate(&changed)
		variations = append(variations, changed)
	}
	for index, changed := range variations {
		other, err := key.Attempt(7, changed)
		if err != nil || first.nonce == other.nonce {
			t.Fatalf("完整身份字段%d变化必须派生不同容量能力: %v", index, err)
		}
	}
	otherKey, _ := NewVideoCapacityNonceKey(bytes.Repeat([]byte("m"), 32))
	otherSecret, err := otherKey.Attempt(7, identity)
	if err != nil || first.nonce == otherSecret.nonce {
		t.Fatalf("不同环境密钥不得派生相同容量能力: %v", err)
	}
	wrongProvider := identity
	wrongProvider.Provider = "other-provider"
	if attempt, err := key.Attempt(7, wrongProvider); err == nil || attempt != nil {
		t.Fatal("未冻结Provider不能进入容量身份")
	}
	clear(secret)
	replayed, err := key.Attempt(7, identity)
	if err != nil || replayed.nonce != first.nonce {
		t.Fatal("构造器必须复制密钥，不能引用调用方可变切片")
	}
	for _, value := range []any{key, *key} {
		body, err := json.Marshal(value)
		if err != nil || string(body) != `{"redacted":true}` || strings.Contains(fmt.Sprintf("%#v", value), strings.Repeat("n", 8)) {
			t.Fatal("容量密钥的指针和值副本必须脱敏")
		}
	}
	for _, invalid := range [][]byte{nil, bytes.Repeat([]byte("x"), 31), bytes.Repeat([]byte("x"), 33)} {
		if key, err := NewVideoCapacityNonceKey(invalid); err == nil || key != nil {
			t.Fatal("容量密钥必须精确32字节")
		}
	}
}

func mustVideoCapacityNonceKey(t *testing.T) *VideoCapacityNonceKey {
	t.Helper()
	key, err := NewVideoCapacityNonceKey(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

package videosecrets_test

import (
	"encoding/json"
	"errors"
	"testing"

	"molin/server/internal/config/videosecrets"
)

func TestVideoG7SecretInvalidBoundary(t *testing.T) {
	for _, root := range []string{"", ".", "relative/repo"} {
		bundle, err := videosecrets.Load(root, []videosecrets.File{{Purpose: videosecrets.Payload, Path: "relative/secret"}})
		if bundle != nil || !errors.Is(err, videosecrets.ErrInvalid) {
			t.Fatal("仓库和凭据边界必须是已解析的绝对路径")
		}
	}
}

func TestVideoG7SecretBundleValueRedaction(t *testing.T) {
	// 即使调用方复制Bundle值再写普通JSON，也必须进入同一个脱敏边界。
	raw, err := json.Marshal(videosecrets.Bundle{})
	if err != nil || string(raw) != `"[REDACTED]"` {
		t.Fatal("凭据包的值与指针必须具有相同脱敏行为")
	}
}

//go:build !linux

package videosecrets_test

import (
	"errors"
	"path/filepath"
	"testing"

	"molin/server/internal/config/videosecrets"
)

func TestVideoG7SecretUnsupportedPlatformFailsClosed(t *testing.T) {
	root := t.TempDir()
	bundle, err := videosecrets.Load(filepath.Join(root, "repo"), []videosecrets.File{{Purpose: videosecrets.Payload, Path: filepath.Join(root, "key")}})
	if bundle != nil || !errors.Is(err, videosecrets.ErrUnsupported) {
		t.Fatal("未验证ACL的平台不得静默跳过权限边界")
	}
}

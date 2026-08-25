package image

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadRestrictedSecretFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secret")
	if err := os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := ReadRestrictedSecretFile(path, 32)
	if err != nil || value != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("受限Secret读取错误: len=%d err=%v", len(value), err)
	}
	if err := os.WriteFile(path, []byte("contains space secret 01234567890123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRestrictedSecretFile(path, 16); err == nil {
		t.Fatal("含空格的Secret必须拒绝")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRestrictedSecretFile(path, 16); err == nil {
			t.Fatal("宽权限Secret必须拒绝")
		}
	}
}

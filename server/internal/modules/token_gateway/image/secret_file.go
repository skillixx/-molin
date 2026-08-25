package image

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrImageSecretFileInvalid = errors.New("图片网关凭据文件不安全或无效")

// ReadRestrictedSecretFile 只读取绝对路径、普通文件、非符号链接和受限权限的小型Secret文件。
func ReadRestrictedSecretFile(path string, minBytes int) (string, error) {
	if !filepath.IsAbs(path) || minBytes <= 0 {
		return "", ErrImageSecretFileInvalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 8192 {
		return "", ErrImageSecretFileInvalid
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", ErrImageSecretFileInvalid
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ErrImageSecretFileInvalid
	}
	trimmedNewline := strings.TrimRight(string(raw), "\r\n")
	if strings.TrimSpace(trimmedNewline) != trimmedNewline || len(trimmedNewline) < minBytes {
		return "", ErrImageSecretFileInvalid
	}
	for _, char := range trimmedNewline {
		if char <= 0x20 || char == 0x7f {
			return "", ErrImageSecretFileInvalid
		}
	}
	return trimmedNewline, nil
}

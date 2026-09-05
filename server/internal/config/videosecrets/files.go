// Package videosecrets 提供视频运行时专用的仓库外凭据加载边界。
package videosecrets

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("视频凭据文件不安全或无效")
	ErrUnsupported = errors.New("当前平台不支持视频凭据文件安全加载")
)

// Purpose 是服务端冻结的用途，不能从HTTP请求或MQ消息选择。
type Purpose string

const (
	Quote          Purpose = "quote"
	Payload        Purpose = "payload"
	Callback       Purpose = "callback"
	AdminReason    Purpose = "admin_reason"
	Download       Purpose = "download"
	MinIOAccess    Purpose = "minio_access"
	MinIOSecret    Purpose = "minio_secret"
	RabbitPassword Purpose = "rabbit_password"
	RedisPassword  Purpose = "redis_password"
	CapacityNonce  Purpose = "capacity_nonce"
)

// File 仅保存配置路径，不保存密文或明文；禁止注册真实Provider Key用途。
type File struct {
	Purpose Purpose
	Path    string
}

// Bundle 在内存中持有凭据；普通JSON和格式化输出只能得到脱敏标记。
type Bundle struct{ values map[Purpose][]byte }

// 使用值接收者覆盖指针与解引用副本，防止格式化副本时暴露内部字节数组。
func (Bundle) String() string               { return "[视频凭据已脱敏]" }
func (Bundle) GoString() string             { return "[视频凭据已脱敏]" }
func (Bundle) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// Bytes 返回用途对应的副本，调用者不能改写已加载快照或另一个消费者的密钥。
func (b *Bundle) Bytes(purpose Purpose) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	value, ok := b.values[purpose]
	if !ok {
		return nil, false
	}
	return bytes.Clone(value), true
}

// Clear在运行时组件完成自身复制后尽力清除加载包，避免bootstrap长期保留第二份全部凭据。
func (b *Bundle) Clear() {
	if b == nil {
		return
	}
	for purpose, value := range b.values {
		clear(value)
		delete(b.values, purpose)
	}
}

// ValidateReferences 只校验低敏配置引用，不打开文件，不替代Linux权限和链接校验。
func ValidateReferences(repositoryRoot string, files []File) error {
	if !filepath.IsAbs(repositoryRoot) || filepath.Clean(repositoryRoot) != repositoryRoot || len(files) == 0 || len(files) > 10 {
		return ErrInvalid
	}
	paths := make(map[string]bool, len(files))
	purposes := make(map[Purpose]bool, len(files))
	for _, spec := range files {
		if minimumBytes(spec.Purpose) == 0 || purposes[spec.Purpose] || !filepath.IsAbs(spec.Path) || filepath.Clean(spec.Path) != spec.Path || paths[spec.Path] {
			return ErrInvalid
		}
		relative, err := filepath.Rel(repositoryRoot, spec.Path)
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return ErrInvalid
		}
		paths[spec.Path], purposes[spec.Purpose] = true, true
	}
	return nil
}

// Load 先验证全部路径/用途，再读取文件；任一失败整包拒绝，不返回半份凭据。
// repositoryRoot必须由部署方注入真实仓库边界，不接受终端用户控制。
func Load(repositoryRoot string, files []File) (*Bundle, error) {
	if err := ValidateReferences(repositoryRoot, files); err != nil {
		return nil, err
	}
	if err := validateRepositoryRoot(repositoryRoot); err != nil {
		return nil, err
	}
	bundle := &Bundle{values: make(map[Purpose][]byte, len(files))}
	success := false
	defer func() {
		// 失败时尽力清除已经分配的密钥字节；不宣称运行时/GC提供绝对内存擦除保证。
		if !success {
			for _, value := range bundle.values {
				clear(value)
			}
		}
	}()
	for _, spec := range files {
		raw, err := readRestricted(spec.Path)
		if err != nil {
			return nil, err
		}
		value := bytes.TrimRight(raw, "\r\n")
		valid := len(value) >= minimumBytes(spec.Purpose) && utf8.Valid(value)
		if spec.Purpose == Payload || spec.Purpose == AdminReason || spec.Purpose == CapacityNonce {
			valid = valid && len(value) == 32
		}
		for _, char := range string(value) {
			if unicode.IsSpace(char) || unicode.IsControl(char) {
				valid = false
				break
			}
		}
		for _, previous := range bundle.values {
			if subtle.ConstantTimeCompare(previous, value) == 1 {
				valid = false
			}
		}
		if !valid {
			clear(raw)
			return nil, ErrInvalid
		}
		bundle.values[spec.Purpose] = bytes.Clone(value)
		clear(raw)
	}
	success = true
	return bundle, nil
}

func minimumBytes(purpose Purpose) int {
	switch purpose {
	case Quote, Payload, Callback, AdminReason, Download, CapacityNonce:
		return 32
	case MinIOAccess:
		return 8
	case MinIOSecret, RabbitPassword, RedisPassword:
		return 16
	default:
		return 0
	}
}

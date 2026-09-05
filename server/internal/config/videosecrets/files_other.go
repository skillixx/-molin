//go:build !linux

package videosecrets

// 未实现Windows ACL等价校验前不放宽凭据边界；本机通过隔离Linux运行时验收。
func validateRepositoryRoot(string) error   { return ErrUnsupported }
func readRestricted(string) ([]byte, error) { return nil, ErrUnsupported }

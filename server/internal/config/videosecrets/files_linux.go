package videosecrets

import (
	"io"
	"os"
	"strings"
	"syscall"
)

// openWithoutLinks 逐层以目录描述符锚定路径，O_NOFOLLOW拒绝任一层符号链接。
// 不采用Lstat后按完整路径重新打开，避免父目录被换成链接的检查/使用竞争。
func openWithoutLinks(path string, directory bool) (*os.File, error) {
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		parts = nil
	}
	for index, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if directory || index < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, openErr := syscall.Openat(fd, part, flags, 0)
		_ = syscall.Close(fd)
		if openErr != nil {
			return nil, ErrInvalid
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "video-restricted-file"), nil
}

func validateRepositoryRoot(path string) error {
	file, err := openWithoutLinks(path, true)
	if err != nil {
		return err
	}
	if file.Close() != nil {
		return ErrInvalid
	}
	return nil
}

// readRestricted 在同一个已打开文件上校验权限、读取并复核身份和元数据。
// 非阻塞打开避免FIFO挂死，硬链接拒绝防止同一inode绕过仓库/用途边界。
func readRestricted(path string) ([]byte, error) {
	file, err := openWithoutLinks(path, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var before syscall.Stat_t
	if syscall.Fstat(int(file.Fd()), &before) != nil || before.Mode&syscall.S_IFMT != syscall.S_IFREG || before.Mode&0o7777&^uint32(0o600) != 0 || before.Nlink != 1 || before.Size < 1 || before.Size > 8192 {
		return nil, ErrInvalid
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 8193))
	var after syscall.Stat_t
	if readErr != nil || len(raw) > 8192 || syscall.Fstat(int(file.Fd()), &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Mode != after.Mode || before.Nlink != after.Nlink || before.Uid != after.Uid || before.Gid != after.Gid || before.Mtim != after.Mtim || before.Ctim != after.Ctim || int64(len(raw)) != after.Size {
		clear(raw)
		return nil, ErrInvalid
	}
	return raw, nil
}

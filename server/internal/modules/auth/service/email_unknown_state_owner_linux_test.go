//go:build linux

package service

import (
	"os"
	"syscall"
)

// emailUnknownRestartStateOwnedByEffectiveUser 核对状态文件属于当前有效 UID。
// 远程 cleanup 只允许在 Linux 测试服务器执行，因此所有权必须由内核 stat 元数据证明。
func emailUnknownRestartStateOwnedByEffectiveUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat != nil && stat.Uid == uint32(os.Geteuid())
}

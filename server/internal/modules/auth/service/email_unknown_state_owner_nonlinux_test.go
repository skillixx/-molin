//go:build !linux

package service

import "os"

// emailUnknownRestartStateOwnedByEffectiveUser 在非 Linux 平台失败关闭。
// 离线单元测试通过可注入 ownerMatches 验证控制流，不允许绕过远程 Linux 的有效 UID 门禁。
func emailUnknownRestartStateOwnedByEffectiveUser(os.FileInfo) bool {
	return false
}

package service

import (
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/repository"
)

// ErrForbidden 越权：用户试图改/删非本人自建（或官方）Agent；handler 据此返回 40003。
var ErrForbidden = errors.New("无权操作该资源")

// isNotFound 判断错误是否为"记录不存在"，兼容 gorm.ErrRecordNotFound 与各仓库 NotFound 错误。
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) ||
		errors.Is(err, repository.ErrAgentNotFound) ||
		errors.Is(err, repository.ErrSkillNotFound) ||
		errors.Is(err, repository.ErrPluginNotFound) ||
		errors.Is(err, repository.ErrMCPServerNotFound) ||
		errors.Is(err, repository.ErrMCPToolNotFound)
}

// IsNotFound 导出版本，供 handler 判断是否回 404。
func IsNotFound(err error) bool { return isNotFound(err) }

// ValidationError 表示服务层参数校验失败，handler 据此返回 400 + 中文 message。
// 与 NotFound / 唯一冲突 / DB 故障区分开，避免基础设施错误被误判为 400。
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// newValidation 构造一个校验错误。
func newValidation(msg string) error { return &ValidationError{Msg: msg} }

// IsValidation 判断错误是否为参数校验类错误。
func IsValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// IsForbidden 判断错误是否为越权错误。
func IsForbidden(err error) bool { return errors.Is(err, ErrForbidden) }

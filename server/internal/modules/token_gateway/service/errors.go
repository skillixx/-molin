package service

import (
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/repository"
)

// isNotFound 判断错误是否为"记录不存在"，兼容 gorm.ErrRecordNotFound 与各仓库 NotFound 错误。
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) ||
		errors.Is(err, repository.ErrChannelNotFound) ||
		errors.Is(err, repository.ErrTokenModelNotFound)
}

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

package handler

import (
	"net/http"
	"strconv"

	"molin/server/internal/modules/workbench/service"
)

// pathUint64 从路径参数解析 uint64。
func pathUint64(r *http.Request, key string) (uint64, error) {
	return strconv.ParseUint(r.PathValue(key), 10, 64)
}

// isNotFound 判断错误是否为"记录不存在"（透传 service 层判断）。
func isNotFound(err error) bool {
	return service.IsNotFound(err)
}

// isValidationErr 判断是否为服务层参数校验类错误（带中文 message，handler 回 400）。
func isValidationErr(err error) bool {
	return service.IsValidation(err)
}

// isForbidden 判断是否为越权错误（handler 回 40003）。
func isForbidden(err error) bool {
	return service.IsForbidden(err)
}

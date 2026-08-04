package response

import (
	"encoding/json"
	"net/http"
)

type Body struct {
	Code      int         `json:"code"`
	ErrorType string      `json:"error,omitempty"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data"`
	RequestID string      `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

func Error(w http.ResponseWriter, status int, code int, message string) {
	ErrorWithType(w, status, code, "", message)
}

// ErrorWithType 在保留平台数字错误码的同时提供稳定机器可读错误分类。
func ErrorWithType(w http.ResponseWriter, status int, code int, errorType, message string) {
	ErrorWithTypeAndRequestID(w, status, code, errorType, message, "")
}

// ErrorWithTypeAndRequestID 为可恢复的异步状态返回账本请求 ID。
func ErrorWithTypeAndRequestID(w http.ResponseWriter, status int, code int, errorType, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{
		Code: code, ErrorType: errorType, Message: message, Data: nil, RequestID: requestID,
	})
}

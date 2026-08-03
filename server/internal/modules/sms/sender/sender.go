package sender

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

type ErrorKind string

const (
	ErrorKindTimeout   ErrorKind = "timeout"
	ErrorKindRateLimit ErrorKind = "rate_limit"
	ErrorKindSignature ErrorKind = "signature"
	ErrorKindTemplate  ErrorKind = "template"
	ErrorKindArrears   ErrorKind = "arrears"
	ErrorKindNetwork   ErrorKind = "network"
	ErrorKindRejected  ErrorKind = "rejected"
)

// Request 是 auth 与具体供应商之间的稳定发送请求。
type Request struct {
	Phone             string
	SignName          string
	TemplateCode      string
	TemplateParamJSON string
	BusinessRequestID string
	Timeout           time.Duration
}

// Result 只保留供应商可追踪标识，不携带原始响应。
type Result struct {
	ProviderRequestID string
	ProviderCode      string
}

// Sender 隔离具体短信供应商，业务层不得直接依赖阿里云 SDK。
type Sender interface {
	Send(ctx context.Context, req Request) (Result, error)
}

// ProviderError 将供应商异常归一化为安全类别和摘要。
type ProviderError struct {
	Kind         ErrorKind
	ProviderCode string
	cause        error
}

func NewProviderError(kind ErrorKind, providerCode string, cause error) *ProviderError {
	return &ProviderError{Kind: kind, ProviderCode: providerCode, cause: cause}
}

func (e *ProviderError) Error() string { return e.SafeSummary() }

func (e *ProviderError) Unwrap() error { return e.cause }

func (e *ProviderError) SafeSummary() string {
	switch e.Kind {
	case ErrorKindTimeout:
		return "短信供应商请求超时"
	case ErrorKindRateLimit:
		return "短信供应商触发频率限制"
	case ErrorKindSignature:
		return "短信签名不可用"
	case ErrorKindTemplate:
		return "短信模板不可用"
	case ErrorKindArrears:
		return "短信账户状态异常"
	case ErrorKindNetwork:
		return "短信供应商网络异常"
	default:
		return "短信供应商拒绝请求"
	}
}

// ClassifyError 根据供应商代码和本地错误类别生成可安全记录的统一错误。
func ClassifyError(providerCode string, err error) *ProviderError {
	if errors.Is(err, context.DeadlineExceeded) {
		return NewProviderError(ErrorKindTimeout, providerCode, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return NewProviderError(ErrorKindTimeout, providerCode, err)
		}
		return NewProviderError(ErrorKindNetwork, providerCode, err)
	}
	code := strings.ToUpper(providerCode)
	switch {
	case strings.Contains(code, "TIMEOUT"):
		return NewProviderError(ErrorKindTimeout, providerCode, err)
	case strings.Contains(code, "NETWORK") || strings.Contains(code, "CONNECTION"):
		return NewProviderError(ErrorKindNetwork, providerCode, err)
	case strings.Contains(code, "LIMIT") || strings.Contains(code, "BUSINESS_CONTROL"):
		return NewProviderError(ErrorKindRateLimit, providerCode, err)
	case strings.Contains(code, "SIGN"):
		return NewProviderError(ErrorKindSignature, providerCode, err)
	case strings.Contains(code, "TEMPLATE"):
		return NewProviderError(ErrorKindTemplate, providerCode, err)
	case strings.Contains(code, "AMOUNT") || strings.Contains(code, "BALANCE") || strings.Contains(code, "ARREAR"):
		return NewProviderError(ErrorKindArrears, providerCode, err)
	default:
		return NewProviderError(ErrorKindRejected, providerCode, err)
	}
}

func providerRejected(code string) error {
	return fmt.Errorf("供应商返回非 OK 状态: %s", code)
}

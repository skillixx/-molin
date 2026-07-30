package service

import (
	"sort"
	"sync"
)

// EmailAdapterCallsMetricName 是供应商调用计数器对外导出时必须使用的固定名称。
const EmailAdapterCallsMetricName = "email_adapter_calls_total"

// EmailAdapterMetricSample 是不含敏感信息的只读指标样本。
type EmailAdapterMetricSample struct {
	Operation string
	Scene     string
	Result    string
	Value     uint64
}

// EmailAdapterMetrics 只按固定枚举维度累计供应商调用次数，避免把邮箱、请求号等敏感或高基数字段写入标签。
type EmailAdapterMetrics struct {
	mu     sync.RWMutex
	counts map[emailAdapterMetricKey]uint64
}

type emailAdapterMetricKey struct {
	Operation string
	Scene     string
	Result    string
}

func newEmailAdapterMetrics() *EmailAdapterMetrics {
	return &EmailAdapterMetrics{counts: make(map[emailAdapterMetricKey]uint64)}
}

func (m *EmailAdapterMetrics) add(operation, scene, result string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.counts[emailAdapterMetricKey{Operation: operation, Scene: scene, Result: result}]++
	m.mu.Unlock()
}

// AdapterCallCount 为测试和后续指标导出器提供只读计数，不暴露任何收件人信息。
func (s *EmailService) AdapterCallCount(operation, scene, result string) uint64 {
	if s == nil || s.metrics == nil {
		return 0
	}
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()
	return s.metrics.counts[emailAdapterMetricKey{Operation: operation, Scene: scene, Result: result}]
}

// AdapterMetricsSnapshot 返回可由后续统一指标导出器采集的稳定快照。
func (s *EmailService) AdapterMetricsSnapshot() []EmailAdapterMetricSample {
	if s == nil || s.metrics == nil {
		return []EmailAdapterMetricSample{}
	}
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()
	out := make([]EmailAdapterMetricSample, 0, len(s.metrics.counts))
	for key, value := range s.metrics.counts {
		out = append(out, EmailAdapterMetricSample{Operation: key.Operation, Scene: key.Scene, Result: key.Result, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Operation != out[j].Operation {
			return out[i].Operation < out[j].Operation
		}
		if out[i].Scene != out[j].Scene {
			return out[i].Scene < out[j].Scene
		}
		return out[i].Result < out[j].Result
	})
	return out
}

func (s *EmailService) recordAdapterCall(operation, scene string, err error) {
	result := "accepted"
	if err != nil {
		result = "failed"
		if isProviderOutcomeUnknown(err) {
			result = "timeout"
		}
	}
	s.metrics.add(operation, scene, result)
}

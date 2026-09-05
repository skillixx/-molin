package video

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

var ErrVideoCapacityPolicy = errors.New("视频容量策略无效")

// 排队与运行各自只接受合同定义的维度，不增加未经批准的全局运行或Provider排队政策。
type VideoQueuedCapacityLimits struct {
	User, Project, APIKey, Model, Global uint32
}

type VideoRunningCapacityLimits struct {
	User, Project, APIKey, Model, Provider uint32
}

type VideoCapacityLimits struct {
	Queued  VideoQueuedCapacityLimits
	Running VideoRunningCapacityLimits
}

// 返回独立值副本；修改某个调用方的配置不会改变进程中的默认上限。
func DefaultVideoCapacityLimits() VideoCapacityLimits {
	return VideoCapacityLimits{
		Queued:  VideoQueuedCapacityLimits{User: 2, Project: 10, APIKey: 2, Model: 100, Global: 100},
		Running: VideoRunningCapacityLimits{User: 1, Project: 2, APIKey: 1, Model: 2, Provider: 2},
	}
}

// 策略对象不暴露可变字段；后续Redis准入和恢复只消费已校验的同一策略。
type VideoCapacityPolicy struct {
	limits VideoCapacityLimits
}

func NewVideoCapacityPolicy(limits VideoCapacityLimits) (*VideoCapacityPolicy, error) {
	if !validVideoCapacityLimits(limits) {
		return nil, ErrVideoCapacityPolicy
	}
	return &VideoCapacityPolicy{limits: limits}, nil
}

func (p *VideoCapacityPolicy) Limits() (VideoCapacityLimits, error) {
	if p == nil || !validVideoCapacityLimits(p.limits) {
		return VideoCapacityLimits{}, ErrVideoCapacityPolicy
	}
	return p.limits, nil
}

// 指纹只标识已校验的策略内容，不是容量、执行或恢复授权；固定版本和维度顺序防止跨实例映射漂移。
func (p *VideoCapacityPolicy) Fingerprint() (string, error) {
	limits, err := p.Limits()
	if err != nil {
		return "", err
	}
	var canonical strings.Builder
	canonical.WriteString("video-capacity-v1")
	for _, value := range videoCapacityValues(limits) {
		canonical.WriteByte('|')
		canonical.WriteString(strconv.FormatUint(uint64(value), 10))
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), nil
}

func validVideoCapacityLimits(limits VideoCapacityLimits) bool {
	actual, ceiling := videoCapacityValues(limits), videoCapacityValues(DefaultVideoCapacityLimits())
	for index, value := range actual {
		// 零不是无限制，也不自动补默认；任何放宽都拒绝整份策略，而不是悄悄截断。
		if value == 0 || value > ceiling[index] {
			return false
		}
	}
	return true
}

// 顺序属于策略指纹协议：queued的用户/项目/Key/模型/全局，再到running的用户/项目/Key/模型/Provider。
func videoCapacityValues(limits VideoCapacityLimits) [10]uint32 {
	return [10]uint32{
		limits.Queued.User, limits.Queued.Project, limits.Queued.APIKey, limits.Queued.Model, limits.Queued.Global,
		limits.Running.User, limits.Running.Project, limits.Running.APIKey, limits.Running.Model, limits.Running.Provider,
	}
}

package service

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

var videoProviderTaskUUIDPattern = regexp.MustCompile(`^taskUUID-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// newVideoProviderTaskUUID在Provider调用前生成UUIDv4；只允许持久化后的值进入Adapter。
func newVideoProviderTaskUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", ErrVideoGovernanceUnavailable
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	value := fmt.Sprintf("taskUUID-%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	if !videoProviderTaskUUIDPattern.MatchString(value) {
		return "", ErrVideoGovernanceUnavailable
	}
	return value, nil
}

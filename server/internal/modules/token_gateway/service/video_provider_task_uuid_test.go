package service

import "testing"

func TestVideoProviderTaskUUIDIsUniqueV4(t *testing.T) {
	seen := map[string]bool{}
	for index := 0; index < 1000; index++ {
		value, err := newVideoProviderTaskUUID()
		if err != nil || !videoProviderTaskUUIDPattern.MatchString(value) || seen[value] {
			t.Fatalf("Provider taskUUID必须是唯一UUIDv4: value=%s err=%v", value, err)
		}
		seen[value] = true
	}
}

package service

import "time"

// videoRetentionPolicy集中冻结G7后台留存周期和审计版本，避免各入口独立漂移。
type videoRetentionPolicy struct {
	InputBound          time.Duration
	UploadSession       time.Duration
	ImportProcessing    time.Duration
	InputPolicyVersion  string
	UploadPolicyVersion string
	OutputPolicyVersion string
}

var currentVideoRetentionPolicy = videoRetentionPolicy{
	InputBound:          7 * 24 * time.Hour,
	UploadSession:       24 * time.Hour,
	ImportProcessing:    24 * time.Hour,
	InputPolicyVersion:  "vid-g7-input-retention-v1",
	UploadPolicyVersion: "vid-g7-upload-session-retention-v1",
	OutputPolicyVersion: "vid-g7-output-retention-v1",
}

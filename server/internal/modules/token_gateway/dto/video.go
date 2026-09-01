package dto

// VideoNull固定序列化为null，避免Prompt或未支持的remix引用被意外回显。
type VideoNull struct{}

func (VideoNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// VideoJob是Molin冻结的公开视频形状，不使用内部Task模型直接JSON序列化。
type VideoJob struct {
	ID                 string      `json:"id"`
	CompletedAt        *int64      `json:"completed_at"`
	CreatedAt          int64       `json:"created_at"`
	Error              *VideoError `json:"error"`
	ExpiresAt          *int64      `json:"expires_at"`
	Model              string      `json:"model"`
	Object             string      `json:"object"`
	Progress           uint8       `json:"progress"`
	Prompt             VideoNull   `json:"prompt"`
	RemixedFromVideoID VideoNull   `json:"remixed_from_video_id"`
	Seconds            string      `json:"seconds"`
	Size               string      `json:"size"`
	Status             string      `json:"status"`
}

// VideoError只允许应用层填入已归一化的低敏错误，不透传Provider正文。
type VideoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// VideoList使用兼容游标形状；平台与管理列表另用D-95，不能混用。
type VideoList struct {
	Object  string     `json:"object"`
	Data    []VideoJob `json:"data"`
	FirstID *string    `json:"first_id"`
	LastID  *string    `json:"last_id"`
	HasMore bool       `json:"has_more"`
}

type VideoDeleted struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

package video

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

var ErrTaskMessageInvalid = errors.New("视频队列消息不符合低敏合同")
var taskMessageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// TaskMessage只携带持久任务的低敏引用，不把operation、Prompt或存储位置放进消息。
// 消费者仍须从原账本复核这些标识属于同一任务，格式通过不等于业务授权通过。
type TaskMessage struct {
	TaskID       string `json:"task_id"`
	RequestID    string `json:"request_id"`
	InputAssetID string `json:"input_asset_id,omitempty"`
	Attempt      uint32 `json:"attempt"`
}

func EncodeTaskMessage(message TaskMessage) ([]byte, error) {
	if !validTaskMessage(message) {
		return nil, ErrTaskMessageInvalid
	}
	return json.Marshal(message)
}

// DecodeTaskMessage逐键读取以拒绝重复、别名、null、未知字段和尾随正文。
// 大小上限在反序列化前检查，失败错误不携带原始消息。
func DecodeTaskMessage(body []byte) (TaskMessage, error) {
	fail := func() (TaskMessage, error) { return TaskMessage{}, ErrTaskMessageInvalid }
	if len(body) == 0 || len(body) > 1024 {
		return fail()
	}
	d := json.NewDecoder(bytes.NewReader(body))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return fail()
	}
	seen := map[string]bool{}
	var result TaskMessage
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return fail()
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fail()
		}
		seen[key] = true
		switch key {
		case "task_id", "request_id", "input_asset_id":
			var value *string
			if d.Decode(&value) != nil || value == nil {
				return fail()
			}
			switch key {
			case "task_id":
				result.TaskID = *value
			case "request_id":
				result.RequestID = *value
			default:
				result.InputAssetID = *value
			}
		case "attempt":
			var value *uint32
			if d.Decode(&value) != nil || value == nil {
				return fail()
			}
			result.Attempt = *value
		default:
			return fail()
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return fail()
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !seen["task_id"] || !seen["request_id"] || !seen["attempt"] || !validTaskMessage(result) {
		return fail()
	}
	return result, nil
}

func validTaskMessage(message TaskMessage) bool {
	return taskMessageID.MatchString(message.TaskID) && taskMessageID.MatchString(message.RequestID) && (message.InputAssetID == "" || taskMessageID.MatchString(message.InputAssetID))
}

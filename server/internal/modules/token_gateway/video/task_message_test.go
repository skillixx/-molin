package video

import (
	"strings"
	"testing"
)

func TestVideoG7TaskMessageContract(t *testing.T) {
	for _, input := range []string{"", "input_reference01"} {
		message := TaskMessage{TaskID: "video_test0001", RequestID: "req_test0001", InputAssetID: input, Attempt: 0}
		body, err := EncodeTaskMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeTaskMessage(body)
		if err != nil || decoded != message {
			t.Fatal("消息必须仅按冻结的任务/请求/输入标识与attempt往返")
		}
		if strings.Contains(string(body), "operation") || strings.Contains(string(body), "prompt") {
			t.Fatal("T2V/I2V不得新增另一种消息载荷")
		}
	}
}

func TestVideoG7TaskMessageRejectsUnsafePayload(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"task_id":"video_test0001","request_id":"req_test0001"}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":null}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":-1}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":1.2}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":4294967296}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":"1"}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":0,"attempt":1}`,
		`{"TASK_ID":"video_test0001","request_id":"req_test0001","attempt":0}`,
		`{"task_id":null,"request_id":"req_test0001","attempt":0}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":0,"prompt":"DO_NOT_ECHO"}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":0,"input_asset_id":"https://storage.invalid/key"}`,
		`{"task_id":"video_test0001","request_id":"req_test0001","attempt":0} {}`,
		strings.Repeat(" ", 1025),
	} {
		decoded, err := DecodeTaskMessage([]byte(body))
		if err == nil || decoded != (TaskMessage{}) {
			t.Fatal("消息错误必须失败关闭且不返回部分标识")
		}
		if strings.Contains(err.Error(), "DO_NOT_ECHO") {
			t.Fatal("错误不得回显消息正文")
		}
	}
}

func TestVideoG7TopologyNamesAndDelayContract(t *testing.T) {
	topology, err := NewTaskTopology("molin.video.g7")
	if err != nil {
		t.Fatal(err)
	}
	route, err := topology.Route(TaskSubmit)
	if err != nil || route.Queue != "molin.video.g7.submit" || route.WorkExchange != "molin.video.g7.work" || route.DeadQueue != "molin.video.g7.dead.submit" {
		t.Fatal("提交路由必须位于独立视频命名空间")
	}
	want := []int32{2000, 5000, 10000, 15000}
	if len(route.Delays) != 4 {
		t.Fatal("G0退避阶梯必须完整")
	}
	for i, delay := range route.Delays {
		if delay.TTLMillis != want[i] {
			t.Fatal("延迟阶梯不符合G0冻结合同")
		}
	}
	if _, err := topology.Route(TaskStage("image_to_video")); err == nil {
		t.Fatal("不能以operation拆成另一套队列")
	}
	for _, bad := range []string{"", "molin.ai.billing", "amq.video", "molin.video.bad\nname"} {
		if _, err := NewTaskTopology(bad); err == nil {
			t.Fatal("非法或其他业务命名空间不得声明")
		}
	}
}

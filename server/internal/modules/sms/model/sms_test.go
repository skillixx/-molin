package model

import "testing"

func TestFixedScenes(t *testing.T) {
	scenes := FixedScenes()
	if len(scenes) != 5 {
		t.Fatalf("固定短信场景数量错误: %v", scenes)
	}
	for _, scene := range scenes {
		if !IsFixedScene(scene) {
			t.Fatalf("权威场景必须通过统一校验: %s", scene)
		}
	}
	if IsFixedScene("marketing") || IsFixedScene("") {
		t.Fatal("非验证码固定场景不得通过统一校验")
	}
	scenes[0] = "tampered"
	if !IsFixedScene("register") {
		t.Fatal("调用方修改返回副本不得污染权威场景集合")
	}
}

func TestHasExactCodeVariable(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		variables []string
		want      bool
	}{
		{name: "精确变量", content: "验证码 ${code}", variables: []string{"code"}, want: true},
		{name: "历史快照按正文兼容", content: "验证码 ${code}", want: true},
		{name: "额外正文变量", content: "${name} 的验证码 ${code}", variables: []string{"name", "code"}, want: false},
		{name: "额外变量数组", content: "验证码 ${code}", variables: []string{"code", "name"}, want: false},
		{name: "变量名含空白", content: "验证码 ${ code }", variables: []string{"code"}, want: false},
		{name: "缺少验证码变量", content: "验证码", variables: []string{"code"}, want: false},
		{name: "变量占位符不完整", content: "验证码 ${code", variables: []string{"code"}, want: false},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			if got := HasExactCodeVariable(item.content, item.variables); got != item.want {
				t.Fatalf("变量校验结果错误: got=%v want=%v", got, item.want)
			}
		})
	}
}

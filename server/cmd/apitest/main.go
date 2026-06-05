package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var base = "http://localhost:8080"

func post(path string, body any, token string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  请求失败:", err)
		return 0, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func get(path, token string) (int, map[string]any) {
	req, _ := http.NewRequest("GET", base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  请求失败:", err)
		return 0, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "  ", "  ")
	return string(b)
}

func check(label string, status int, expect int, body map[string]any) bool {
	ok := status == expect
	mark := "✅"
	if !ok {
		mark = "❌"
	}
	fmt.Printf("  %s HTTP %d（期望 %d）\n", mark, status, expect)
	fmt.Printf("  响应: %s\n", pretty(body))
	return ok
}

func main() {
	allPass := true

	email := fmt.Sprintf("apitest_%d@example.com", time.Now().Unix())

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("  A-01 接口人工测试")
	fmt.Printf("  测试邮箱: %s\n", email)
	fmt.Println("═══════════════════════════════════════════")

	// 1. 发送注册验证码
	fmt.Println("\n[1] 发送邮箱注册验证码")
	status, body := post("/api/auth/verification-codes/email", map[string]string{
		"target": email,
		"scene":  "register",
	}, "")
	allPass = check("发送验证码", status, 200, body) && allPass
	data, _ := body["data"].(map[string]any)
	code, _ := data["code"].(string)
	fmt.Printf("  验证码（本地可见）: %s\n", code)

	if code == "" {
		fmt.Println("  无法获取验证码，中止测试")
		os.Exit(1)
	}

	// 2. 邮箱注册
	fmt.Println("\n[2] 邮箱注册")
	status, body = post("/api/auth/register/email", map[string]string{
		"email":    email,
		"password": "Test1234!",
		"code":     code,
	}, "")
	allPass = check("注册", status, 201, body) && allPass
	data, _ = body["data"].(map[string]any)
	accessToken, _ := data["access_token"].(string)
	_, _ = data["refresh_token"].(string) // 注册时的 token，本测试流程不直接使用

	// 3. 重复注册同邮箱（期望 409）
	fmt.Println("\n[3] 重复注册同邮箱（期望 4xx）")
	status, body = post("/api/auth/register/email", map[string]string{
		"email": email, "password": "Test1234!", "code": "000000",
	}, "")
	fmt.Printf("  HTTP %d → %s\n", status, pretty(body))

	// 4. GET /api/me 需要 Token
	fmt.Println("\n[4] GET /api/me（有效 Token）")
	status, body = get("/api/me", accessToken)
	allPass = check("查询用户信息", status, 200, body) && allPass

	// 5. GET /api/me 无 Token（期望 401）
	fmt.Println("\n[5] GET /api/me（无 Token，期望 401）")
	status, body = get("/api/me", "")
	allPass = check("无 Token 鉴权", status, 401, body) && allPass

	// 6. 邮箱密码登录
	fmt.Println("\n[6] 邮箱密码登录")
	status, body = post("/api/auth/login/email", map[string]string{
		"email":    email,
		"password": "Test1234!",
	}, "")
	allPass = check("密码登录", status, 200, body) && allPass
	data, _ = body["data"].(map[string]any)
	accessToken2, _ := data["access_token"].(string)
	refreshToken2, _ := data["refresh_token"].(string)

	// 7. 错误密码登录（期望 401）
	fmt.Println("\n[7] 错误密码登录（期望 401）")
	status, body = post("/api/auth/login/email", map[string]string{
		"email": email, "password": "WrongPwd!",
	}, "")
	allPass = check("错误密码", status, 401, body) && allPass

	// 8. 刷新 Token
	fmt.Println("\n[8] 刷新 Token")
	status, body = post("/api/auth/refresh", map[string]string{
		"refresh_token": refreshToken2,
	}, "")
	allPass = check("刷新 Token", status, 200, body) && allPass
	data, _ = body["data"].(map[string]any)
	newRefresh, _ := data["refresh_token"].(string)

	// 9. 退出登录
	fmt.Println("\n[9] 退出登录")
	status, body = post("/api/auth/logout", map[string]string{
		"refresh_token": newRefresh,
	}, accessToken2)
	allPass = check("退出登录", status, 200, body) && allPass

	// 10. 退出后用刚被吊销的 newRefresh 刷新（期望 401）
	fmt.Println("\n[10] 退出后用已吊销 refresh_token 刷新（期望 401）")
	status, body = post("/api/auth/refresh", map[string]string{
		"refresh_token": newRefresh,
	}, "")
	allPass = check("已吊销 Token 刷新", status, 401, body) && allPass

	fmt.Println("\n═══════════════════════════════════════════")
	if allPass {
		fmt.Println("  测试结论：全部通过 ✅")
	} else {
		fmt.Println("  测试结论：有失败项 ❌，请检查上方日志")
		os.Exit(1)
	}
	fmt.Println("═══════════════════════════════════════════")
}

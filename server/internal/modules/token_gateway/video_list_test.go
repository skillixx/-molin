package token_gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 列表与创建使用同一关闭态入口；未装配依赖时明确503，不能404或返回空列表伪装可用。
func TestVideoG6ListHTTPRequiresDependencies(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	res, err := server.Client().Get(server.URL + "/v1/videos")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result struct {
		Error struct{ Code string } `json:"error"`
	}
	if res.StatusCode != 503 || json.NewDecoder(res.Body).Decode(&result) != nil || result.Error.Code != "video_gateway_traffic_closed" {
		t.Fatalf("列表缺依赖应503关闭态，实际HTTP=%d", res.StatusCode)
	}
}

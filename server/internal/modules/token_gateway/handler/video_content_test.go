package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"molin/server/internal/middleware"
)

// 此处只验证已授权正文的HTTP传输边界；完整身份和账务准入另由真实MySQL链路验收。
func TestVideoG6ContentHTTPFullAndSingleRange(t *testing.T) {
	body := []byte("0123456789abcdef")
	digest := sha256.Sum256(body)
	sha := hex.EncodeToString(digest[:])
	server := httptest.NewServer(middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeVideoContent(w, r, VideoHTTPContent{
			Size: int64(len(body)), SHA256: sha,
			OpenRange: func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body[offset : offset+length])), nil
			},
		})
	})))
	defer server.Close()
	for _, tc := range []struct {
		name, rangeHeader, wantBody, contentRange string
		status                                    int
	}{
		{"完整正文", "", "0123456789abcdef", "", 200},
		{"指定字节", "bytes=2-5", "2345", "bytes 2-5/16", 206},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status || string(got) != tc.wantBody || res.Header.Get("Content-Range") != tc.contentRange {
				t.Fatalf("HTTP正文合同不符：status=%d length=%d range=%q", res.StatusCode, len(got), res.Header.Get("Content-Range"))
			}
			if res.Header.Get("Content-Type") != "video/mp4" || res.Header.Get("Accept-Ranges") != "bytes" || res.Header.Get("ETag") != `"`+sha+`"` || res.ContentLength != int64(len(tc.wantBody)) || res.Header.Get("X-Request-ID") == "" {
				t.Fatal("媒体响应头不完整")
			}
		})
	}
}

func TestVideoG6ContentHTTPRangeAndValidatorMatrix(t *testing.T) {
	body := []byte("0123456789abcdef")
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	var opens atomic.Int64
	server := httptest.NewServer(middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeVideoContent(w, r, VideoHTTPContent{Size: 16, SHA256: hex.EncodeToString(digest[:]), OpenRange: func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
			opens.Add(1)
			return io.NopCloser(bytes.NewReader(body[offset : offset+length])), nil
		}})
	})))
	defer server.Close()
	for _, tc := range []struct {
		name, query        string
		ranges, validators []string
		status             int
		want               string
	}{
		{"后缀", "", []string{"bytes=-4"}, nil, 206, "cdef"},
		{"开尾", "", []string{"bytes=12-"}, nil, 206, "cdef"},
		{"超长末尾截断", "", []string{"bytes=12-99"}, nil, 206, "cdef"},
		{"超长后缀", "", []string{"bytes=-99"}, nil, 206, "0123456789abcdef"},
		{"验证匹配", "", []string{"bytes=2-5"}, []string{etag}, 206, "2345"},
		{"验证不匹配", "", []string{"bytes=2-5"}, []string{`"other"`}, 200, "0123456789abcdef"},
		{"旧验证器忽略合法越界范围", "", []string{"bytes=20-"}, []string{`"other"`}, 200, "0123456789abcdef"},
		{"旧验证器不容忍多范围", "", []string{"bytes=0-1,4-5"}, []string{`"other"`}, 416, ""},
		{"弱验证器", "", []string{"bytes=2-5"}, []string{"W/" + etag}, 200, "0123456789abcdef"},
		{"多范围", "", []string{"bytes=0-1,4-5"}, nil, 416, ""},
		{"重复范围头", "", []string{"bytes=0-1", "bytes=4-5"}, nil, 416, ""},
		{"重复验证头", "", []string{"bytes=0-1"}, []string{etag, etag}, 416, ""},
		{"起点越界", "", []string{"bytes=16-"}, nil, 416, ""},
		{"逆序", "", []string{"bytes=5-2"}, nil, 416, ""},
		{"空范围", "", []string{"bytes=-"}, nil, 416, ""},
		{"空头", "", []string{""}, nil, 416, ""},
		{"零后缀", "", []string{"bytes=-0"}, nil, 416, ""},
		{"溢出", "", []string{"bytes=0-9223372036854775808"}, nil, 416, ""},
		{"正号", "", []string{"bytes=+1-2"}, nil, 416, ""},
		{"非法单位", "", []string{"items=0-1"}, nil, 416, ""},
		{"默认变体", "?variant=video", nil, nil, 200, "0123456789abcdef"},
		{"未支持变体", "?variant=thumbnail", nil, nil, 400, ""},
		{"重复变体", "?variant=video&variant=video", nil, nil, 400, ""},
		{"未知参数", "?object_key=forged", nil, nil, 400, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := opens.Load()
			req, err := http.NewRequest(http.MethodGet, server.URL+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range tc.ranges {
				req.Header.Add("Range", value)
			}
			for _, value := range tc.validators {
				req.Header.Add("If-Range", value)
			}
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("HTTP=%d，预期%d", res.StatusCode, tc.status)
			}
			if tc.status < 400 {
				if string(got) != tc.want {
					t.Fatal("字节选择不符")
				}
				return
			}
			if opens.Load() != before {
				t.Fatal("非法范围或变体读取了对象")
			}
			var envelope struct {
				Error struct{ Code, Message, RequestID string }
			}
			var raw map[string]json.RawMessage
			if json.Unmarshal(got, &envelope) != nil || json.Unmarshal(got, &raw) != nil || len(raw) != 1 || envelope.Error.Code == "" || envelope.Error.Message == "" {
				t.Fatal("错误信封不符")
			}
			var detail map[string]string
			if json.Unmarshal(raw["error"], &detail) != nil || len(detail) != 3 || detail["request_id"] != res.Header.Get("X-Request-ID") {
				t.Fatal("追踪ID或字段集合不符")
			}
			if tc.status == 416 && res.Header.Get("Content-Range") != "bytes */16" {
				t.Fatal("416缺少总长度")
			}
		})
	}
}

func TestVideoG6ContentHTTPRecoveryNeverAppendsJSON(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, 1<<20)
	server := httptest.NewServer(middleware.RequestID(middleware.Logger(middleware.Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeVideoContent(w, r, VideoHTTPContent{Size: 2 << 20, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OpenRange: func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
			if offset > 0 {
				return nil, errors.New("第二片读取失败")
			}
			return io.NopCloser(bytes.NewReader(body)), nil
		}})
	})))))
	defer server.Close()
	res, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, readErr := io.ReadAll(res.Body)
	if readErr == nil || !bytes.Equal(got, body) {
		t.Fatalf("断流应只含已发送媒体且返回读取错误：length=%d error=%v", len(got), readErr)
	}
}

func TestVideoG6ContentHTTPStoreFailureIsLowSensitivity(t *testing.T) {
	server := httptest.NewServer(middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeVideoContent(w, r, VideoHTTPContent{Size: 16, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OpenRange: func(context.Context, int64, int64) (io.ReadCloser, error) {
			return nil, errors.New("private storage location must never escape")
		}})
	})))
	defer server.Close()
	res, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 503 || bytes.Contains(body, []byte("private storage")) || res.Header.Get("Content-Type") != "application/json" {
		t.Fatal("存储错误泄漏或伪装成功")
	}
}

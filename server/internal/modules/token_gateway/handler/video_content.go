package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/pkg/response"
)

// VideoHTTPContent只能由应用层在归属、交付及对账门禁后构造，不接受客户端对象位置。
// OpenRange仍须读取受控私有存储；传输层不持有Provider客户端，也不生成公网URL。
type VideoHTTPContent struct {
	Size        int64
	SHA256      string
	OpenRange   func(context.Context, int64, int64) (io.ReadCloser, error)
	BeforeWrite func(context.Context) (time.Time, error)
}

var videoContentHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

const videoHTTPChunkBytes int64 = 1 << 20

// 内容路由先使用真实Project SK，再由应用层构建受控读取能力；不接受任何客户端存储位置。
func (h *VideoHandler) Content(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	content, err := h.app.GetContent(r.Context(), caller, r.PathValue("video_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	defer content.Close()
	ServeVideoContent(w, r, VideoHTTPContent{Size: content.Size, SHA256: content.SHA256, OpenRange: content.OpenRange, BeforeWrite: content.BeforeWrite})
}

// ServeVideoContent处理已经通过应用层授权的MP4；它本身不是路由注册器或鉴权替代品。
// 每次只读取有界分片，第一片OpenRange失败不发送成功头；Read开始后遇错中断连接而不混入JSON。
func ServeVideoContent(w http.ResponseWriter, r *http.Request, content VideoHTTPContent) {
	// ParseQuery错误不能被URL.Query静默忽略；重复参数也不能采用首值绕过协议。
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil || len(query) > 1 || (len(query) == 1 && (len(query["variant"]) != 1 || query.Get("variant") != "video")) {
		writeVideoContentError(w, r, 400, "invalid_request_error", "仅支持默认MP4视频内容")
		return
	}
	serveVideoContent(w, r, content, "video/mp4")
}

// 平台派生物使用服务端白名单MIME，Range/带宽/租约与v1共享，不能由query指定媒体类型。
func serveVideoContent(w http.ResponseWriter, r *http.Request, content VideoHTTPContent, mimeType string) {
	switch mimeType {
	case "video/mp4", "image/png", "image/jpeg", "image/webp":
	default:
		writeVideoContentError(w, r, 503, "video_content_unavailable", "媒体类型不可交付")
		return
	}
	if content.Size <= 0 || content.Size > 256<<20 || !videoContentHash.MatchString(content.SHA256) || content.OpenRange == nil {
		writeVideoContentError(w, r, 503, "video_content_unavailable", "视频内容暂不可用")
		return
	}
	etag := `"` + content.SHA256 + `"`
	start, length, partial, valid := videoHTTPRange(r.Header, content.Size, etag)
	if !valid {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", content.Size))
		writeVideoContentError(w, r, 416, "invalid_range", "视频字节范围无效")
		return
	}
	chunkLength := min(length, videoHTTPChunkBytes)
	reader, err := content.OpenRange(r.Context(), start, chunkLength)
	if err != nil || reader == nil {
		if reader != nil {
			_ = reader.Close()
		}
		writeVideoContentError(w, r, 503, "video_content_unavailable", "视频内容暂不可用")
		return
	}
	deadline, err := videoContentWriteDeadline(r.Context(), content.BeforeWrite)
	if err != nil {
		_ = reader.Close()
		writeVideoContentError(w, r, 503, "video_content_unavailable", "下载租约已失效")
		return
	}
	// 每条响应共享一个限速器，首片最多突发1MiB，其后不能逐片重置额度。
	pacer := videoContentPacer{next: time.Now().Add(time.Duration(chunkLength) * time.Second / (20 << 20))}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, content.Size))
	}
	w.WriteHeader(status)
	for remaining := length; remaining > 0; {
		if r.Context().Err() != nil {
			_ = reader.Close()
			panic(http.ErrAbortHandler)
		}
		// 写超时只影响本次下载连接，不修改媒体、任务或财务事实。
		_ = http.NewResponseController(w).SetWriteDeadline(deadline)
		_, copyErr := io.CopyN(w, reader, chunkLength)
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			panic(http.ErrAbortHandler)
		}
		remaining -= chunkLength
		start += chunkLength
		if remaining == 0 {
			break
		}
		chunkLength = min(remaining, videoHTTPChunkBytes)
		if err := pacer.wait(r.Context(), chunkLength); err != nil {
			panic(http.ErrAbortHandler)
		}
		reader, err = content.OpenRange(r.Context(), start, chunkLength)
		if err != nil || reader == nil {
			if reader != nil {
				_ = reader.Close()
			}
			panic(http.ErrAbortHandler)
		}
		deadline, err = videoContentWriteDeadline(r.Context(), content.BeforeWrite)
		if err != nil {
			_ = reader.Close()
			panic(http.ErrAbortHandler)
		}
	}
}

// 写操作最迟30秒结束，租约剩余不足30秒则采用更早的期限，不能先回收名额后仍无限写出。
func videoContentWriteDeadline(ctx context.Context, renew func(context.Context) (time.Time, error)) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	deadline := time.Now().Add(30 * time.Second)
	if renew == nil {
		return deadline, nil
	}
	until, err := renew(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if !until.After(time.Now()) {
		return time.Time{}, context.DeadlineExceeded
	}
	if until.Before(deadline) {
		deadline = until
	}
	return deadline, nil
}

// 虚拟完成时间实现20MiB/s、1MiB突发桶；空闲积累最多一个突发，等待不占数据库事务。
type videoContentPacer struct{ next time.Time }

func (p *videoContentPacer) wait(ctx context.Context, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	duration := time.Duration(size) * time.Second / (20 << 20)
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	allowed := p.next.Add(duration - 50*time.Millisecond)
	if delay := time.Until(allowed); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if now = time.Now(); p.next.Before(now) {
		p.next = now
	}
	p.next = p.next.Add(duration)
	return nil
}

// 只接受单一规范十进制范围，拒绝符号、溢出、空范围及multipart/byteranges。
func videoHTTPRange(header http.Header, size int64, etag string) (int64, int64, bool, bool) {
	ranges := header.Values("Range")
	if len(ranges) == 0 {
		return 0, size, false, true
	}
	if len(ranges) != 1 || !strings.HasPrefix(ranges[0], "bytes=") {
		return 0, 0, false, false
	}
	value := strings.TrimPrefix(ranges[0], "bytes=")
	parts := strings.Split(value, "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return 0, 0, false, false
	}
	parse := func(s string) (int64, bool) {
		if s == "" {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	var start, length int64
	if parts[0] == "" {
		suffix, ok := parse(parts[1])
		if !ok || suffix == 0 {
			return 0, 0, false, false
		}
		length = min(suffix, size)
		start = size - length
	} else {
		var ok bool
		start, ok = parse(parts[0])
		if !ok {
			return 0, 0, false, false
		}
		end := size - 1
		if parts[1] != "" {
			end, ok = parse(parts[1])
			if !ok || end < start {
				return 0, 0, false, false
			}
			end = min(end, size-1)
		}
		length = end - start + 1
	}
	validators := header.Values("If-Range")
	if len(validators) > 1 {
		return 0, 0, false, false
	}
	if len(validators) == 1 && validators[0] != etag {
		return 0, size, false, true
	}
	// 先判定If-Range再检查可满足性：旧ETag对应完整新对象，不能因旧偏移越界返回416。
	if start >= size {
		return 0, 0, false, false
	}
	return start, length, true, true
}

func writeVideoContentError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/token/") {
		numeric := status * 100
		switch status {
		case 400, 415:
			numeric = 40000
		case 401:
			numeric = 40001
		case 403:
			numeric = 40003
		case 402:
			numeric = 60001
		}
		// 报价错误沿用既有平台专用码；不存在与越权保持同一语义，不暴露Quote归属。
		switch code {
		case "70001":
			numeric = 70001
		case "quote_not_found":
			numeric = 40420
		case "quote_expired":
			numeric = 40920
		case "idempotency_conflict":
			numeric = 40901
		case "concurrency_limit_exceeded":
			numeric = 42922
		case "budget_limit_exceeded":
			numeric = 42920
		case "governance_unavailable":
			numeric = 50321
		}
		response.ErrorWithTypeAndRequestID(w, status, numeric, code, message, middleware.RequestIDFromContext(r.Context()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"code": code, "message": message, "request_id": middleware.RequestIDFromContext(r.Context()),
	}})
}

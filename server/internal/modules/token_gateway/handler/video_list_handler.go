package handler

import (
	"net/http"
	"net/url"
	"strconv"

	"molin/server/internal/modules/token_gateway/service"
)

// List保持OpenAI游标形状，不接收D-95或客户端提供的Project、用户与Key覆盖参数。
func (h *VideoHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoAPIError(w, r, service.ErrVideoListParameters)
		return
	}
	query := service.VideoListQuery{Limit: 20, Order: "desc"}
	for field, values := range values {
		if len(values) != 1 || values[0] == "" {
			writeVideoAPIError(w, r, service.ErrVideoListParameters)
			return
		}
		switch field {
		case "after":
			query.After = values[0]
		case "order":
			query.Order = values[0]
		case "limit":
			// ParseInt可接受正号；冻结合同只接受十进制数字，主动拒绝空白和带符号值。
			for _, digit := range values[0] {
				if digit < '0' || digit > '9' {
					writeVideoAPIError(w, r, service.ErrVideoListParameters)
					return
				}
			}
			query.Limit, err = strconv.Atoi(values[0])
			if err != nil || query.Limit < 1 || query.Limit > 100 {
				writeVideoAPIError(w, r, service.ErrVideoListParameters)
				return
			}
		default:
			writeVideoAPIError(w, r, service.ErrVideoListParameters)
			return
		}
	}
	result, err := h.app.ListVideos(r.Context(), caller, query)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoJob(w, result)
}

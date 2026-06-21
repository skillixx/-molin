package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// UsageHandler 处理 token 用量流水查询（用户端「我的用量」/ 管理端「全量用量」）。
// 仅返回 tokens/状态等元数据，绝不暴露对话内容；用户端视图额外剔除 user_id / api_key_id。
type UsageHandler struct {
	svc *service.UsageService
}

// NewUsageHandler 创建用量查询 handler。
func NewUsageHandler(svc *service.UsageService) *UsageHandler {
	return &UsageHandler{svc: svc}
}

// ListMine GET /api/token/usage（用户端，sk/JWT 双模式）
// 查本人 token 调用流水与消费，扁平分页；可选筛选 model / start / end（RFC3339）。
// 说明：路由已接 RequireUserAuth（S2-丁3）；UserIDFromContext 在 sk 路径返回
// sk 绑定的 user_id、JWT 路径返回登录用户 id，两者均按本人 user_id 过滤。
func (h *UsageHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}

	start, end, ok := parseTimeRange(w, r)
	if !ok {
		return
	}

	f := repository.UsageQueryFilter{
		UserID:           userID,
		LogicalModelCode: strings.TrimSpace(r.URL.Query().Get("model")),
		Start:            start,
		End:              end,
	}
	p := pagination.Parse(r)
	logs, total, err := h.svc.ListPaged(r.Context(), f, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}

	items := make([]dto.UserUsageResp, len(logs))
	for i := range logs {
		items[i] = dto.ToUserUsageResp(&logs[i])
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// ListAll GET /api/admin/token/usage（管理端，token:manage + 管理员双重认证）
// 全量 token 用量流水，扁平分页；可按 user_id / api_key_id / model / start / end 筛选。
// items 在用户端字段基础上额外含 user_id、api_key_id（可空）。
func (h *UsageHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	start, end, ok := parseTimeRange(w, r)
	if !ok {
		return
	}

	f := repository.UsageQueryFilter{
		LogicalModelCode: strings.TrimSpace(r.URL.Query().Get("model")),
		Start:            start,
		End:              end,
	}

	if v := strings.TrimSpace(r.URL.Query().Get("user_id")); v != "" {
		uid, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "user_id 非法")
			return
		}
		f.UserID = uid
	}
	if v := strings.TrimSpace(r.URL.Query().Get("api_key_id")); v != "" {
		akid, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "api_key_id 非法")
			return
		}
		f.APIKeyID = &akid
	}

	p := pagination.Parse(r)
	logs, total, err := h.svc.ListPaged(r.Context(), f, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}

	items := make([]dto.AdminUsageResp, len(logs))
	for i := range logs {
		items[i] = dto.ToAdminUsageResp(&logs[i])
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// parseTimeRange 解析 start/end 查询参数（RFC3339），任一非法即写出 400 并返回 ok=false。
// 二者均可选，缺省返回 nil（不参与过滤）。
func parseTimeRange(w http.ResponseWriter, r *http.Request) (start, end *time.Time, ok bool) {
	if v := strings.TrimSpace(r.URL.Query().Get("start")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "start 时间格式应为 RFC3339")
			return nil, nil, false
		}
		start = &t
	}
	if v := strings.TrimSpace(r.URL.Query().Get("end")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "end 时间格式应为 RFC3339")
			return nil, nil, false
		}
		end = &t
	}
	return start, end, true
}

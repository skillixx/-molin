package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/content/dto"
	"molin/server/internal/modules/content/service"
	"molin/server/pkg/response"
)

// IAMRoleGetter 获取用户角色 code 的接口，由 iam.IAMService 实现。
type IAMRoleGetter interface {
	GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error)
}

// MembershipChecker 查询用户是否有活跃会员的接口，由 membership.MembershipService 实现。
type MembershipChecker interface {
	HasActiveMembership(ctx context.Context, userID uint64) (bool, error)
}

// ContentHandler 内容 HTTP 处理器。
type ContentHandler struct {
	svc           *service.ContentService
	iamGetter     IAMRoleGetter
	memberChecker MembershipChecker
}

// NewContentHandler 创建处理器实例。
// iamGetter 和 memberChecker 可为 nil（仅影响 visible_scope 过滤精度）。
func NewContentHandler(svc *service.ContentService, iamGetter IAMRoleGetter, memberChecker MembershipChecker) *ContentHandler {
	return &ContentHandler{
		svc:           svc,
		iamGetter:     iamGetter,
		memberChecker: memberChecker,
	}
}

// ListAnnouncements 查询公告（需登录，按 visible_scope 过滤）。
// GET /api/announcements
func (h *ContentHandler) ListAnnouncements(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	// 获取用户角色 codes
	var userRoles []string
	if h.iamGetter != nil && userID > 0 {
		codes, err := h.iamGetter.GetUserRoleCodes(r.Context(), userID)
		if err == nil {
			userRoles = codes
		}
	}

	// 查询用户是否有有效会员
	isMember := false
	if h.memberChecker != nil && userID > 0 {
		has, err := h.memberChecker.HasActiveMembership(r.Context(), userID)
		if err == nil {
			isMember = has
		}
	}

	announcements, err := h.svc.ListAnnouncements(r.Context(), userID, userRoles, isMember)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询公告失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": announcements})
}

// ListHelpCategories 查询帮助分类（无需登录）。
// GET /api/help/categories
func (h *ContentHandler) ListHelpCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.svc.ListHelpCategories(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询帮助分类失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": categories})
}

// ListHelpArticles 查询帮助文章列表（无需登录，支持 category_id 过滤）。
// GET /api/help/articles
func (h *ContentHandler) ListHelpArticles(w http.ResponseWriter, r *http.Request) {
	var categoryID uint64
	if cid := r.URL.Query().Get("category_id"); cid != "" {
		id, _ := strconv.ParseUint(cid, 10, 64)
		categoryID = id
	}

	articles, err := h.svc.ListHelpArticles(r.Context(), categoryID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询帮助文章失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": articles})
}

// GetHelpArticle 查询帮助文章详情（无需登录）。
// GET /api/help/articles/:id
func (h *ContentHandler) GetHelpArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "文章 ID 无效")
		return
	}

	article, err := h.svc.GetHelpArticle(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "文章不存在或未发布")
		return
	}
	response.JSON(w, http.StatusOK, article)
}

// AdminListAnnouncements 管理员查所有公告（分页）。
// GET /api/admin/announcements
func (h *ContentHandler) AdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	list, total, err := h.svc.AdminListAnnouncements(r.Context(), page, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询公告失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": list,
		"total": total,
		"page":  page,
	})
}

// AdminCreateAnnouncement 创建公告。
// POST /api/admin/announcements
func (h *ContentHandler) AdminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	operatorID := middleware.UserIDFromContext(r.Context())

	var req dto.CreateAnnouncementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	a, err := h.svc.CreateAnnouncement(r.Context(), operatorID, req.Title, req.Content,
		req.VisibleScope, req.StartAt, req.EndAt, req.SortOrder)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, a)
}

// AdminUpdateAnnouncement 修改公告（含发布/下线操作）。
// PATCH /api/admin/announcements/:id
func (h *ContentHandler) AdminUpdateAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "公告 ID 无效")
		return
	}

	var req dto.UpdateAnnouncementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.Content != nil {
		updates["content"] = *req.Content
	}
	if req.VisibleScope != nil {
		updates["visible_scope"] = *req.VisibleScope
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.StartAt != nil {
		updates["start_at"] = *req.StartAt
	}
	if req.EndAt != nil {
		updates["end_at"] = *req.EndAt
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}

	if err := h.svc.UpdateAnnouncement(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新公告失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// AdminListHelpCategories 管理员查帮助分类。
// GET /api/admin/help/categories
func (h *ContentHandler) AdminListHelpCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.svc.AdminListHelpCategories(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询分类失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": categories})
}

// AdminCreateHelpCategory 创建帮助分类。
// POST /api/admin/help/categories
func (h *ContentHandler) AdminCreateHelpCategory(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateCategoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	c, err := h.svc.CreateHelpCategory(r.Context(), req.Name, req.Description, req.SortOrder)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, c)
}

// AdminUpdateHelpCategory 修改帮助分类。
// PATCH /api/admin/help/categories/:id
func (h *ContentHandler) AdminUpdateHelpCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "ID 无效")
		return
	}

	var req dto.UpdateCategoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.svc.UpdateHelpCategory(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// AdminListHelpArticles 管理员查帮助文章（分页，支持 category_id 过滤）。
// GET /api/admin/help/articles
func (h *ContentHandler) AdminListHelpArticles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var categoryID uint64
	if cid := q.Get("category_id"); cid != "" {
		id, _ := strconv.ParseUint(cid, 10, 64)
		categoryID = id
	}

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	list, total, err := h.svc.AdminListHelpArticles(r.Context(), categoryID, page, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询文章失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": list,
		"total": total,
		"page":  page,
	})
}

// AdminCreateHelpArticle 创建帮助文章。
// POST /api/admin/help/articles
func (h *ContentHandler) AdminCreateHelpArticle(w http.ResponseWriter, r *http.Request) {
	operatorID := middleware.UserIDFromContext(r.Context())

	var req dto.CreateArticleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	a, err := h.svc.CreateHelpArticle(r.Context(), operatorID, req.CategoryID, req.Title, req.Content, req.SortOrder)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, a)
}

// AdminUpdateHelpArticle 修改帮助文章。
// PATCH /api/admin/help/articles/:id
func (h *ContentHandler) AdminUpdateHelpArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "ID 无效")
		return
	}

	var req dto.UpdateArticleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.CategoryID != nil {
		updates["category_id"] = *req.CategoryID
	}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.Content != nil {
		updates["content"] = *req.Content
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.svc.UpdateHelpArticle(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

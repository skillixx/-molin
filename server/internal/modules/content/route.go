package content

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/content/handler"
	"molin/server/internal/modules/content/service"
)

// RegisterRoutes 注册 content 模块所有路由。
// iamGetter 和 memberChecker 可为 nil，传 nil 时用户端公告不做 visible_scope 精细过滤。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
	iamGetter handler.IAMRoleGetter,
	memberChecker handler.MembershipChecker,
) {
	svc := service.NewContentService(db)
	h := handler.NewContentHandler(svc, iamGetter, memberChecker)

	// 认证中间件（需要登录）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端权限中间件
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端路由（需要登录，按 visible_scope 过滤）
	mux.Handle("GET /api/announcements", auth(h.ListAnnouncements))

	// 帮助文档（无需登录）
	mux.HandleFunc("GET /api/help/categories", h.ListHelpCategories)
	mux.HandleFunc("GET /api/help/articles", h.ListHelpArticles)
	mux.HandleFunc("GET /api/help/articles/{id}", h.GetHelpArticle)

	// 管理端路由
	mux.Handle("GET /api/admin/announcements", adminAuth("content:manage", h.AdminListAnnouncements))
	mux.Handle("POST /api/admin/announcements", adminAuth("content:manage", h.AdminCreateAnnouncement))
	mux.Handle("PATCH /api/admin/announcements/{id}", adminAuth("content:manage", h.AdminUpdateAnnouncement))
	mux.Handle("GET /api/admin/help/categories", adminAuth("content:manage", h.AdminListHelpCategories))
	mux.Handle("POST /api/admin/help/categories", adminAuth("content:manage", h.AdminCreateHelpCategory))
	mux.Handle("PATCH /api/admin/help/categories/{id}", adminAuth("content:manage", h.AdminUpdateHelpCategory))
	mux.Handle("GET /api/admin/help/articles", adminAuth("content:manage", h.AdminListHelpArticles))
	mux.Handle("POST /api/admin/help/articles", adminAuth("content:manage", h.AdminCreateHelpArticle))
	mux.Handle("PATCH /api/admin/help/articles/{id}", adminAuth("content:manage", h.AdminUpdateHelpArticle))
}

// NewService 创建内容服务（供 bootstrap/app.go 使用）。
func NewService(db *gorm.DB) *service.ContentService {
	return service.NewContentService(db)
}

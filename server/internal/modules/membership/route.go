package membership

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/membership/handler"
	"molin/server/internal/modules/membership/service"
)

// RegisterRoutes 注册 membership 模块所有路由。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
) {
	svc := service.NewMembershipService(db)
	h := handler.NewMembershipHandler(svc)

	// 认证中间件（需要登录）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端权限中间件
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端/公开路由（无需登录）
	mux.HandleFunc("GET /api/memberships", h.ListPublicLevels)

	// 用户端路由（需要登录）
	mux.Handle("GET /api/my/membership", auth(h.GetMyMembership))

	// 管理端路由
	mux.Handle("GET /api/admin/membership-levels", adminAuth("membership:view", h.AdminListLevels))
	mux.Handle("POST /api/admin/membership-levels", adminAuth("membership:manage", h.AdminCreateLevel))
	mux.Handle("PATCH /api/admin/membership-levels/{id}", adminAuth("membership:manage", h.AdminUpdateLevel))
	mux.Handle("GET /api/admin/membership-benefits", adminAuth("membership:view", h.AdminListBenefits))
	mux.Handle("POST /api/admin/membership-benefits", adminAuth("membership:manage", h.AdminCreateBenefit))
	mux.Handle("PATCH /api/admin/membership-benefits/{id}", adminAuth("membership:manage", h.AdminUpdateBenefit))
	mux.Handle("GET /api/admin/user-memberships", adminAuth("membership:view", h.AdminListUserMemberships))
	mux.Handle("POST /api/admin/user-memberships", adminAuth("membership:manage", h.AdminGrantMembership))
	mux.Handle("PATCH /api/admin/user-memberships/{id}", adminAuth("membership:manage", h.AdminUpdateUserMembership))
}

// NewService 创建会员服务（供 bootstrap/app.go 使用）。
func NewService(db *gorm.DB) *service.MembershipService {
	return service.NewMembershipService(db)
}

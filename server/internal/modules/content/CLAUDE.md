# Content 模块 — 后端 C 负责

## 职责边界

只负责：系统公告、帮助文档分类、帮助文章的发布和查询。

## 需要创建的文件

```text
model/
  content.go        -- announcements, help_categories, help_articles

repository/
  announcement_repo.go
  help_repo.go

service/
  content_service.go    -- 发布 / 下线 / 可见范围过滤

handler/
  content_handler.go    -- 用户端 + 管理端

dto/
  content_dto.go

route.go
```

## 可见范围过滤（用户端查询时必须执行）

```go
func (s *ContentService) ListAnnouncements(ctx context.Context, userID uint64, roles []string, isMember bool) ([]Announcement, error) {
    now := time.Now()
    // 筛选 status = published AND start_at <= now AND (end_at IS NULL OR end_at >= now)
    // 再按 visible_scope 过滤：
    //   all     → 所有用户可见
    //   roles   → 检查 target_roles_json 是否包含用户角色
    //   members → 用户必须有有效会员
    //   admins  → 用户端不展示
}
```

## 接口清单

```text
GET  /api/announcements
GET  /api/help/categories
GET  /api/help/articles
GET  /api/help/articles/:id
GET  /api/admin/announcements
POST /api/admin/announcements
PATCH /api/admin/announcements/:id
GET  /api/admin/help/categories
POST /api/admin/help/categories
PATCH /api/admin/help/categories/:id
GET  /api/admin/help/articles
POST /api/admin/help/articles
PATCH /api/admin/help/articles/:id
```

## 依赖关系

- 依赖 `modules/iam/service` — 查询用户角色（可见范围过滤）
- 依赖 `modules/membership/service` — 查询用户是否有效会员

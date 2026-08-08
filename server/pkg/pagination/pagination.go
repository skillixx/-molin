package pagination

import (
	"net/http"
	"strconv"
)

const maxPage = 10000

// Params 分页请求参数。
type Params struct {
	Page     int
	PageSize int
}

// Result 分页响应元数据，用于嵌入到各列表接口的响应体中。
type Result struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// Parse 从 Query String 解析分页参数，带默认值和边界保护：
//   - page 默认 1，最小 1
//   - page 最大 10000，避免恶意超大 OFFSET 拖垮共享数据库
//   - page_size 默认 20，最大 100
func Parse(r *http.Request) Params {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return Params{Page: page, PageSize: pageSize}
}

// Offset 计算 SQL OFFSET 值。
func (p Params) Offset() int {
	return (p.Page - 1) * p.PageSize
}

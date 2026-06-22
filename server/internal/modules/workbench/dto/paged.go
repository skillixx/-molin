package dto

import "molin/server/pkg/pagination"

// PagedResp 通用分页响应结构（D-95 扁平分页）。
// List 序列化为 items，匿名嵌入 pagination.Result 使 page/page_size/total 与 items 同级。
type PagedResp struct {
	List interface{} `json:"items"`
	pagination.Result
}

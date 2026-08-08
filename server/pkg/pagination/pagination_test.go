package pagination

import (
	"net/http/httptest"
	"testing"
)

func TestParseClampsExtremePageAndPageSize(t *testing.T) {
	req := httptest.NewRequest("GET", "/items?page=2147483647&page_size=999", nil)
	params := Parse(req)
	if params.Page != maxPage || params.PageSize != 100 {
		t.Fatalf("分页边界未生效: %+v", params)
	}
	if params.Offset() != 999900 {
		t.Fatalf("分页偏移计算错误: %d", params.Offset())
	}
}
